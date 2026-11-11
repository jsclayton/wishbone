// Package imagestore keeps uploaded and downloaded pictures on disk under the
// data directory. It is the one place that decodes untrusted image bytes.
//
// Every image is decoded, flattened onto white, shrunk to fit [MaxDimension],
// and re-encoded as JPEG, so what is stored is never the caller's bytes. A
// stored image is named by a random [Key]. Callers keep the key (for example
// on an item row) and decide who may read the image. The store itself has no
// notion of owners.
package imagestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Decoders for the formats the store accepts.
	_ "image/gif"
	_ "image/png"

	"go.opentelemetry.io/otel/trace"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/telemetry"
)

var (
	// ErrInvalid means the bytes are not an image the store accepts. Its
	// message is safe to show.
	ErrInvalid = errors.New("invalid image")
	// ErrNotFound means no image has that key.
	ErrNotFound = errors.New("image not found")
)

// Limits. They are variables of the package's contract, not options: the
// callers all want the same ones.
const (
	// MaxBytes is the most the store reads from one upload or download.
	MaxBytes = 10 << 20
	// MaxPixels is the largest decoded image, width times height. It stops a
	// small file that expands to gigabytes of memory.
	MaxPixels = 25_000_000
	// MaxDimension is the longest side of a stored image, in pixels.
	MaxDimension = 1200
	// ContentType is the type of every stored image.
	ContentType = "image/jpeg"

	// MaxConcurrentDecodes is how many images the store decodes at once,
	// across the whole instance. A 25 MP image needs about 100 MB to decode,
	// so this keeps a burst of uploads from exhausting a small host.
	MaxConcurrentDecodes = 2

	jpegQuality     = 85
	downloadTimeout = 15 * time.Second
	maxRedirects    = 5
)

// decodeSlots holds one token for each decode in progress. A decode waits for
// a free slot.
var decodeSlots = make(chan struct{}, MaxConcurrentDecodes)

// decodeHook is a test seam: it runs inside a decode that holds a slot.
var decodeHook func()

// Options configures a Store.
type Options struct {
	// AllowPrivateAddrs lets Download connect to loopback, private, and
	// link-local addresses. Off by default so a pasted URL can't reach the
	// host's network. Tests turn it on to use httptest.
	AllowPrivateAddrs bool
	// Tracer records a span for each download request, redirects included.
	// The span holds the method, host, and status, never the path or query.
	// Nil turns tracing off.
	Tracer trace.TracerProvider
}

// Store reads and writes images under one directory.
type Store struct {
	dir    string
	client *http.Client
}

// New returns a Store that keeps images under dir. It creates dir if needed.
func New(dir string, o Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create image directory: %w", err)
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	if !o.AllowPrivateAddrs {
		tr.DialContext = netguard.DialContext(dialer)
	}
	return &Store{dir: dir, client: &http.Client{
		Timeout:   downloadTimeout,
		Transport: telemetry.Transport(o.Tracer, tr),
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}, nil
}

// Save decodes an image from r, resizes and re-encodes it, stores it, and
// returns its key. It reads at most [MaxBytes]. Anything that is not a JPEG,
// PNG, GIF, or WebP image within the limits is [ErrInvalid].
func (s *Store) Save(r io.Reader) (string, error) {
	saved, err := s.Put(r)
	return saved.Key, err
}

// Saved is an image that [Store.Put] stored: its key, and its size in pixels
// after the resize.
type Saved struct {
	Key           string
	Width, Height int
}

// Put does what [Store.Save] does, and also returns the size of the stored
// image.
func (s *Store) Put(r io.Reader) (Saved, error) {
	encoded, err := Normalize(r)
	if err != nil {
		return Saved{}, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil {
		return Saved{}, fmt.Errorf("read the size of the stored image: %w", err)
	}
	key, err := s.write(encoded)
	if err != nil {
		return Saved{}, err
	}
	return Saved{Key: key, Width: cfg.Width, Height: cfg.Height}, nil
}

// Normalize does everything [Store.Save] does except store: it decodes an
// image from r within the same limits, flattens and shrinks it to fit
// [MaxDimension], and returns the JPEG bytes. The result has none of the
// original's metadata. Errors are the same as Save's.
func Normalize(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("use an image smaller than %d MB: %w", MaxBytes>>20, ErrInvalid)
	}
	return process(data)
}

// write stores encoded bytes under a new key.
func (s *Store) write(encoded []byte) (string, error) {
	key, err := newKey()
	if err != nil {
		return "", err
	}
	path := s.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	// Write to a temp name and rename, so a reader never sees half a file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(encoded)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return "", werr
	}
	return key, nil
}

// Copy stores a second copy of an image under a new key and returns the key.
// Deleting either one leaves the other. A key that is not valid, or has no
// image, is [ErrNotFound].
func (s *Store) Copy(key string) (string, error) {
	src, err := s.Open(key)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	return s.write(data)
}

// Open returns the stored image. The caller closes it. A key that is not
// valid, or has no image, is [ErrNotFound].
func (s *Store) Open(key string) (*os.File, error) {
	if !validKey(key) {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete removes an image. A missing image is not an error.
func (s *Store) Delete(key string) error {
	if !validKey(key) {
		return nil
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Stored is one image file in the store.
type Stored struct {
	Key     string
	ModTime time.Time
}

// List returns every image in the store. Files that are not images (such as a
// temp file from a write in progress) are left out.
func (s *Store) List() ([]Stored, error) {
	var out []Stored
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		name := d.Name()
		key, ok := strings.CutSuffix(name, ".jpg")
		if d.IsDir() || !ok || !validKey(key) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		out = append(out, Stored{Key: key, ModTime: info.ModTime()})
		return nil
	})
	return out, err
}

// Download fetches an http or https image URL, then stores it as [Store.Save]
// does. The response is limited by size and must decode as an image. Its
// Content-Type header is not trusted. It refuses non-public addresses.
func (s *Store) Download(ctx context.Context, rawURL string) (string, error) {
	saved, err := s.DownloadPut(ctx, rawURL)
	return saved.Key, err
}

// DownloadPut does what [Store.Download] does, and also returns the size of the
// stored image, as [Store.Put] does.
func (s *Store) DownloadPut(ctx context.Context, rawURL string) (Saved, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return Saved{}, fmt.Errorf("use an http or https image address: %w", ErrInvalid)
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Wishbone)")
	resp, err := s.client.Do(req)
	if err != nil {
		return Saved{}, fmt.Errorf("download image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Saved{}, fmt.Errorf("the image address answered %d: %w", resp.StatusCode, ErrInvalid)
	}
	return s.Put(resp.Body)
}

// path is the on-disk layout. internal/backup repeats it to check that a
// snapshot has every referenced image. The columns that hold keys are listed in
// internal/imagerefs, which both the backup check and the cleanup use.
func (s *Store) path(key string) string { return filepath.Join(s.dir, key[:2], key+".jpg") }

func newKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, c := range key {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// process decodes, orients, flattens, shrinks, and re-encodes.
func process(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("use a JPEG, PNG, GIF, or WebP image: %w", ErrInvalid)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > MaxPixels {
		return nil, fmt.Errorf("use an image under %d megapixels: %w", MaxPixels/1_000_000, ErrInvalid)
	}
	// The size check above is cheap and never waits. The decode and resize
	// below are what use memory, so only they take a slot.
	decodeSlots <- struct{}{}
	defer func() { <-decodeSlots }()
	if decodeHook != nil {
		decodeHook()
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("use a JPEG, PNG, GIF, or WebP image: %w", ErrInvalid)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > MaxDimension || h > MaxDimension {
		if w >= h {
			w, h = MaxDimension, max(1, h*MaxDimension/w)
		} else {
			w, h = max(1, w*MaxDimension/h), MaxDimension
		}
	}
	// Paint onto white first: JPEG has no alpha, and transparent pixels
	// would otherwise turn black.
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)

	// Orient the small result, not the full-size source.
	var out bytes.Buffer
	if err := jpeg.Encode(&out, orient(dst, exifOrientation(data)), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
