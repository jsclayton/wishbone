package imagestore_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"jsclayton.dev/wishbone/internal/imagestore"
)

func newStore(t *testing.T, allowPrivate bool) *imagestore.Store {
	t.Helper()
	s, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{AllowPrivateAddrs: allowPrivate})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func stored(t *testing.T, s *imagestore.Store, key string) image.Image {
	t.Helper()
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, format, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("stored format = %s, want jpeg", format)
	}
	return img
}

func TestSaveShrinksLargeImagesAndKeepsAspectRatio(t *testing.T) {
	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(pngBytes(t, solid(2400, 600, color.RGBA{200, 0, 0, 255}))))
	if err != nil {
		t.Fatal(err)
	}
	if b := stored(t, s, key).Bounds(); b.Dx() != imagestore.MaxDimension || b.Dy() != 300 {
		t.Fatalf("size = %v, want 1200x300", b)
	}
}

func TestPutReturnsTheSizeOfTheStoredImage(t *testing.T) {
	s := newStore(t, false)
	saved, err := s.Put(bytes.NewReader(pngBytes(t, solid(2400, 600, color.RGBA{200, 0, 0, 255}))))
	require.NoError(t, err)
	require.Equal(t, imagestore.MaxDimension, saved.Width)
	require.Equal(t, 300, saved.Height)
	b := stored(t, s, saved.Key).Bounds()
	require.Equal(t, saved.Width, b.Dx())
	require.Equal(t, saved.Height, b.Dy())
}

func TestSaveLeavesSmallImagesAtTheirSize(t *testing.T) {
	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(pngBytes(t, solid(80, 50, color.Black))))
	if err != nil {
		t.Fatal(err)
	}
	if b := stored(t, s, key).Bounds(); b.Dx() != 80 || b.Dy() != 50 {
		t.Fatalf("size = %v, want 80x50", b)
	}
}

func TestSaveFlattensTransparencyOntoWhite(t *testing.T) {
	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(pngBytes(t, solid(10, 10, color.NRGBA{0, 0, 0, 0}))))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := stored(t, s, key).At(5, 5).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("pixel = %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestSaveRejects(t *testing.T) {
	s := newStore(t, false)
	huge := pngBytes(t, image.NewGray(image.Rect(0, 0, 8000, 8000)))
	for name, data := range map[string][]byte{
		"text":            []byte("not an image"),
		"empty":           {},
		"svg":             []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"too many bytes":  append(pngBytes(t, solid(2, 2, color.Black)), make([]byte, imagestore.MaxBytes)...),
		"too many pixels": huge,
	} {
		_, err := s.Save(bytes.NewReader(data))
		if !errors.Is(err, imagestore.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSaveAppliesExifOrientation(t *testing.T) {
	// A 40x20 image whose left half is red, tagged "rotate 90 clockwise" (6):
	// upright it is 20x40 with the red half on top.
	src := solid(40, 20, color.RGBA{0, 0, 255, 255})
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			src.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var j bytes.Buffer
	if err := jpeg.Encode(&j, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	withExif := insertOrientation(j.Bytes(), 6)

	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(withExif))
	if err != nil {
		t.Fatal(err)
	}
	img := stored(t, s, key)
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("size = %v, want 20x40", b)
	}
	if r, _, bl, _ := img.At(10, 5).RGBA(); r>>8 < 200 || bl>>8 > 80 {
		t.Fatalf("top pixel is not red")
	}
	if r, _, bl, _ := img.At(10, 35).RGBA(); bl>>8 < 200 || r>>8 > 80 {
		t.Fatalf("bottom pixel is not blue")
	}
}

func insertOrientation(jpegData []byte, o uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	_ = binary.Write(&tiff, binary.BigEndian, uint16(42))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))
	_ = binary.Write(&tiff, binary.BigEndian, o)
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0))
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(payload)+2))
	seg = append(seg, payload...)
	return append(append(append([]byte{}, jpegData[:2]...), seg...), jpegData[2:]...)
}

func TestOpenAndDelete(t *testing.T) {
	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(pngBytes(t, solid(4, 4, color.Black))))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(key); !errors.Is(err, imagestore.ErrNotFound) {
		t.Fatalf("Open after Delete: %v, want ErrNotFound", err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestOpenRefusesKeysThatAreNotStoreKeys(t *testing.T) {
	s := newStore(t, false)
	for _, k := range []string{"", "../../etc/passwd", strings.Repeat("g", 32), strings.Repeat("a", 31)} {
		if _, err := s.Open(k); !errors.Is(err, imagestore.ErrNotFound) {
			t.Errorf("Open(%q) = %v, want ErrNotFound", k, err)
		}
	}
}

func TestDownload(t *testing.T) {
	good := pngBytes(t, solid(30, 30, color.Black))
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		// A wrong type is fine: the bytes decide.
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(good)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "<html>nope</html>")
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(append(append([]byte{}, good...), make([]byte, imagestore.MaxBytes)...))
	})
	mux.HandleFunc("/missing", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s := newStore(t, true)

	for _, path := range []string{"/ok", "/redirect"} {
		key, err := s.Download(context.Background(), srv.URL+path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if b := stored(t, s, key).Bounds(); b.Dx() != 30 {
			t.Errorf("%s: width %d", path, b.Dx())
		}
	}
	for _, path := range []string{"/html", "/big", "/missing"} {
		if _, err := s.Download(context.Background(), srv.URL+path); !errors.Is(err, imagestore.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", path, err)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/a.png", "not a url", ""} {
		if _, err := s.Download(context.Background(), u); !errors.Is(err, imagestore.ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", u, err)
		}
	}
}

func TestDownloadRefusesPrivateAddressesByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pngBytes(t, solid(4, 4, color.Black)))
	}))
	defer srv.Close()
	if _, err := newStore(t, false).Download(context.Background(), srv.URL); err == nil {
		t.Fatal("downloaded from a loopback address")
	}
}

func TestDownloadTracesRequestsWithoutURL(t *testing.T) {
	var gotHeaders []http.Header
	mux := http.NewServeMux()
	mux.HandleFunc("/secret-path/img", func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = append(gotHeaders, r.Header.Clone())
		http.Redirect(w, r, "/secret-path/final?t=secret-query", http.StatusFound)
	})
	mux.HandleFunc("/secret-path/final", func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = append(gotHeaders, r.Header.Clone())
		_, _ = w.Write(pngBytes(t, solid(4, 4, color.Black)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	s, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{AllowPrivateAddrs: true, Tracer: tp})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Download(context.Background(), srv.URL+"/secret-path/img?t=secret-query"); err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2 (request and redirect)", len(spans))
	}
	for _, sp := range spans {
		if sp.Name != "HTTP GET" {
			t.Errorf("span name = %q", sp.Name)
		}
		if strings.Contains(sp.Name, "secret") {
			t.Errorf("span name %q holds the URL", sp.Name)
		}
		for _, kv := range sp.Attributes {
			if strings.Contains(kv.Value.String(), "secret") {
				t.Errorf("attribute %s = %s holds the URL", kv.Key, kv.Value.String())
			}
		}
	}
	for _, h := range gotHeaders {
		if h.Get("Traceparent") != "" || h.Get("Tracestate") != "" {
			t.Errorf("image host got trace headers: %v", h)
		}
	}
}

func TestDownloadWithTracerStillRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pngBytes(t, solid(4, 4, color.Black)))
	}))
	defer srv.Close()
	tp := sdktrace.NewTracerProvider()
	s, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{Tracer: tp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), srv.URL); err == nil {
		t.Fatal("downloaded from a loopback address")
	}
}

func TestSaveWritesOnlyUnderItsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "images")
	s, _ := imagestore.New(dir, imagestore.Options{})
	if _, err := s.Save(bytes.NewReader(pngBytes(t, solid(4, 4, color.Black)))); err != nil {
		t.Fatal(err)
	}
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if n != 1 {
		t.Fatalf("%d files, want 1 (no temp files left)", n)
	}
}

func TestCopyMakesAnIndependentImage(t *testing.T) {
	s := newStore(t, false)
	key, err := s.Save(bytes.NewReader(pngBytes(t, solid(4, 4, color.Black))))
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.Copy(key)
	if err != nil || cp == key {
		t.Fatalf("Copy = %q, %v; want a new key", cp, err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(cp)
	if err != nil {
		t.Fatalf("the copy should survive deleting the original: %v", err)
	}
	_ = f.Close()

	for _, bad := range []string{key, "../x"} {
		if _, err := s.Copy(bad); !errors.Is(err, imagestore.ErrNotFound) {
			t.Errorf("Copy(%q) error = %v, want ErrNotFound", bad, err)
		}
	}
}

func TestNormalizeDropsExif(t *testing.T) {
	var j bytes.Buffer
	if err := jpeg.Encode(&j, solid(40, 20, color.RGBA{255, 0, 0, 255}), nil); err != nil {
		t.Fatal(err)
	}
	in := insertOrientation(j.Bytes(), 6)
	if !bytes.Contains(in, []byte("Exif\x00\x00")) {
		t.Fatal("test input has no Exif segment")
	}
	out, err := imagestore.Normalize(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Exif")) {
		t.Fatal("output still has an Exif segment")
	}
	if b, _, err := image.DecodeConfig(bytes.NewReader(out)); err != nil || b.Width != 20 || b.Height != 40 {
		t.Fatalf("want an upright 20x40 JPEG, got %v, %v", b, err)
	}
}

func TestNormalizeGivesAShrunkJPEG(t *testing.T) {
	out, err := imagestore.Normalize(bytes.NewReader(pngBytes(t, solid(3000, 1500, color.NRGBA{200, 10, 10, 255}))))
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("want a JPEG, got %q, %v", format, err)
	}
	if b := img.Bounds(); b.Dx() != imagestore.MaxDimension || b.Dy() != imagestore.MaxDimension/2 {
		t.Fatalf("size = %v", b)
	}
	if _, err := imagestore.Normalize(strings.NewReader("not an image")); !errors.Is(err, imagestore.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestConcurrentDecodesAreBounded(t *testing.T) {
	s := newStore(t, false)
	data := pngBytes(t, solid(8, 8, color.White))

	var mu sync.Mutex
	var running, peak int
	release := make(chan struct{})
	restore := imagestore.SetDecodeHook(func() {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
	})
	defer restore()

	const uploads = 6
	errs := make(chan error, uploads)
	for range uploads {
		go func() {
			_, err := s.Save(bytes.NewReader(data))
			errs <- err
		}()
	}
	// Give every upload time to reach the cap, then let the decodes finish.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return running == imagestore.MaxConcurrentDecodes
	}, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	close(release)
	for range uploads {
		require.NoError(t, <-errs)
	}
	require.Equal(t, imagestore.MaxConcurrentDecodes, peak)
}
