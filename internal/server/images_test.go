package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/imagestore"
)

// imageView is the image that POST /images returns.
type imageView struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentType string `json:"content_type"`
}

func solidImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = 200, 255
	}
	return img
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, solidImage(w, h)))
	return b.Bytes()
}

func gifOf(t *testing.T, w, h int) []byte {
	t.Helper()
	pal := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.White, color.Black})
	var b bytes.Buffer
	require.NoError(t, gif.Encode(&b, pal, nil))
	return b.Bytes()
}

// jpegWithMetadata is a JPEG with an EXIF segment that holds a GPS tag, as a
// phone's camera makes.
func jpegWithMetadata(t *testing.T, w, h int) []byte {
	t.Helper()
	var plain bytes.Buffer
	require.NoError(t, jpeg.Encode(&plain, solidImage(w, h), nil))
	var tiff bytes.Buffer
	tiff.WriteString("II*\x00")
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint32(8)))
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint16(1)))      // one entry
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint16(0x8825))) // the GPS IFD pointer
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint16(4)))      // LONG
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint32(1)))      // one value
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint32(0x1234))) // the offset
	require.NoError(t, binary.Write(&tiff, binary.LittleEndian, uint32(0)))      // no next IFD
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := []byte{0xFF, 0xE1}
	segment = binary.BigEndian.AppendUint16(segment, uint16(len(payload)+2))
	segment = append(segment, payload...)
	data := plain.Bytes()
	return append(append(append([]byte{}, data[:2]...), segment...), data[2:]...)
}

func (e *env) upload(token string, body []byte, contentType string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/images", raw: body, contentType: contentType, header: cookie(token)})
}

func (e *env) getImage(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/images/" + id, header: cookie(token)})
}

// uploaded uploads a small PNG and returns the image.
func (e *env) uploaded(token string) imageView {
	e.t.Helper()
	res := e.upload(token, pngOf(e.t, 4, 3), "image/png")
	require.Equal(e.t, http.StatusCreated, res.StatusCode)
	return decode[imageView](e.t, res)
}

func TestAnUploadedImageIsStoredAsAShrunkJPEGAndTheUploaderLoadsIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.upload(admin, pngOf(t, 2400, 600), "image/png")

	require.Equal(t, http.StatusCreated, res.StatusCode)
	img := decode[imageView](t, res)
	require.Equal(t, "/api/v2/images/"+img.ID, res.Header.Get("Location"))
	require.Equal(t, "/api/v2/images/"+img.ID, img.URL)
	require.Equal(t, 1200, img.Width)
	require.Equal(t, 300, img.Height)
	require.Equal(t, "image/jpeg", img.ContentType)

	got := e.getImage(admin, img.ID)
	require.Equal(t, http.StatusOK, got.StatusCode)
	require.Equal(t, "image/jpeg", got.Header.Get("Content-Type"))
	require.Equal(t, "private, max-age=31536000, immutable", got.Header.Get("Cache-Control"))
	decoded, format, err := image.Decode(got.Body)
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, image.Pt(1200, 300), decoded.Bounds().Size())
}

func TestAnImageKeepsNoExifOrGPSMetadata(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	in := jpegWithMetadata(t, 40, 30)
	require.Contains(t, string(in), "Exif", "the test input has metadata")

	img := decode[imageView](t, e.upload(admin, in, "image/jpeg"))

	out := readBody(t, e.getImage(admin, img.ID))
	require.NotContains(t, out, "Exif")
	require.NotContains(t, out, "GPS")
}

// webp is a lossless WebP of one pixel. The standard library has no WebP
// encoder, so the test carries the file.
const webp = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func webpBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(webp)
	require.NoError(t, err)
	return raw
}

func TestJPEGPNGGIFAndWebPAreAccepted(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for name, tc := range map[string]struct {
		body        []byte
		contentType string
	}{
		"a JPEG":                     {jpegWithMetadata(t, 8, 6), "image/jpeg"},
		"a PNG":                      {pngOf(t, 8, 6), "image/png"},
		"a GIF":                      {gifOf(t, 8, 6), "image/gif"},
		"a PNG sent as octet-stream": {pngOf(t, 8, 6), "application/octet-stream"},
		"a WebP":                     {webpBytes(t), "image/webp"},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.upload(admin, tc.body, tc.contentType)
			require.Equal(t, http.StatusCreated, res.StatusCode)
			img := decode[imageView](t, res)
			if name == "a WebP" {
				require.Equal(t, imageView{ID: img.ID, URL: img.URL, Width: 1, Height: 1, ContentType: "image/jpeg"}, img)
				return
			}
			require.Equal(t, 8, img.Width)
			require.Equal(t, 6, img.Height)
		})
	}
}

func TestBytesThatAreNotAnImageAreA422(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for name, body := range map[string][]byte{
		"text":                       []byte("not an image"),
		"an empty body":              {},
		"a PNG cut short":            pngOf(t, 40, 40)[:30],
		"more pixels than allowed":   pngOf(t, 6000, 5000),
		"a JSON body with a url":     []byte(`{"url":"https://example.com/a.png"}`),
		"an SVG, which can't be run": []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
	} {
		t.Run(name, func(t *testing.T) {
			res := e.upload(admin, body, "image/png")
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			p := readProblem(t, res)
			require.Equal(t, "image_invalid", p.Code)
			require.Regexp(t, `^[A-Z][^:]*\.$`, p.Detail, "the detail is a sentence for a person")
		})
	}
}

func TestAnImageOverTheLimitIsA413(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.upload(admin, make([]byte, imagestore.MaxBytes+1), "image/png")
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	require.Equal(t, "image_too_large", readProblem(t, res).Code)

	// A body of exactly the limit is read. It isn't an image, so it is a 422.
	res = e.upload(admin, make([]byte, imagestore.MaxBytes), "image/png")
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "image_invalid", readProblem(t, res).Code)
}

func TestABodyOverHumasDefaultLimitIsNotAnImageProblem(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.do(request{method: http.MethodPost, path: "/users", header: cookie(admin),
		raw: bytes.Repeat([]byte(" "), 2<<20), contentType: "application/json"})

	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	require.Equal(t, "request_too_large", readProblem(t, res).Code)
}

func TestImagesNeedASignIn(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	img := e.uploaded(admin)

	res := e.do(request{method: http.MethodPost, path: "/images", raw: pngOf(t, 4, 3), contentType: "image/png"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "unauthorized", readProblem(t, res).Code)
	res = e.do(request{path: "/images/" + img.ID})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestBeforeItIsAttachedOnlyTheUploaderSeesAnImage(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	img := e.uploaded(grace)

	require.Equal(t, http.StatusOK, e.getImage(grace, img.ID).StatusCode)
	// Not even the admin, who sees every user.
	res := e.getImage(admin, img.ID)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)
}

func TestAnImageThatDoesNotExistIsA404(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for _, id := range []string{"999", "0", "-1", "abc", "01"} {
		res := e.getImage(admin, id)
		require.Equal(t, http.StatusNotFound, res.StatusCode, id)
		require.Equal(t, "not_found", readProblem(t, res).Code, id)
	}
}
