package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/store"
)

// privateImages gives the server an image store that may dial loopback, so a
// test can point a download at an httptest server. Without this, the store keeps
// the instance's own network out of reach.
func privateImages(t *testing.T) func(*Options) {
	return func(o *Options) {
		files, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{AllowPrivateAddrs: true})
		require.NoError(t, err)
		o.Images = image.NewService(o.Store.(*store.Store).DB(), files, o.Now)
	}
}

// pictureShop serves what a store's page links to.
func pictureShop(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngOf(t, 40, 30))
	})
	mux.HandleFunc("/mislabelled", func(w http.ResponseWriter, _ *http.Request) {
		// The type is not trusted: the bytes decide.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(pngOf(t, 8, 8))
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	})
	mux.HandleFunc("/gone", http.NotFound)
	mux.HandleFunc("/huge", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, imagestore.MaxBytes+10))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func (e *env) downloadImage(token, url string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/images", body: map[string]any{"url": url}, header: cookie(token)})
}

func TestAnImageAddressIsDownloadedAndStoredAsAnUpload(t *testing.T) {
	e := newEnv(t, privateImages(t))
	_, admin := e.signUp()
	shop := pictureShop(t)

	res := e.downloadImage(admin, shop.URL+"/pic.png")

	require.Equal(t, http.StatusCreated, res.StatusCode)
	img := decode[imageView](t, res)
	require.Equal(t, "/api/v2/images/"+img.ID, res.Header.Get("Location"))
	require.Equal(t, 40, img.Width)
	require.Equal(t, 30, img.Height)
	require.Equal(t, "image/jpeg", img.ContentType)
	// Like an upload, it is the downloader's alone until something holds it.
	require.Equal(t, http.StatusOK, e.getImage(admin, img.ID).StatusCode)
	_, grace := e.otherUser("Grace")
	require.Equal(t, http.StatusNotFound, e.getImage(grace, img.ID).StatusCode)
	// The bytes decide, not the type the shop claims.
	require.Equal(t, http.StatusCreated, e.downloadImage(admin, shop.URL+"/mislabelled").StatusCode)
}

func TestEveryFailureOfADownloadIsAnImageFetchFailed(t *testing.T) {
	e := newEnv(t, privateImages(t))
	_, admin := e.signUp()
	shop := pictureShop(t)

	for name, url := range map[string]string{
		"an error from the shop": shop.URL + "/gone",
		"not an image":           shop.URL + "/page.html",
		"too large":              shop.URL + "/huge",
		"nobody answers":         "http://127.0.0.1:1/pic.png",
		"not a web address":      "ftp://example.com/pic.png",
		"no address at all":      "not a url",
	} {
		t.Run(name, func(t *testing.T) {
			res := e.downloadImage(admin, url)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			p := readProblem(t, res)
			require.Equal(t, "image_fetch_failed", p.Code)
			require.Regexp(t, `^[A-Z][^:]*\.$`, p.Detail, "the detail is a sentence for a person")
		})
	}
	require.Zero(t, e.imageRows(), "a failed download leaves no image")
}

func TestADownloadKeepsAwayFromTheInstancesOwnNetwork(t *testing.T) {
	e := newEnv(t) // the default store: loopback is out of reach
	_, admin := e.signUp()
	shop := pictureShop(t)

	res := e.downloadImage(admin, shop.URL+"/pic.png")

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "image_fetch_failed", readProblem(t, res).Code)
	require.Zero(t, e.imageRows())
}

func TestADownloadNeedsASignInAndAUrl(t *testing.T) {
	e := newEnv(t, privateImages(t))
	_, admin := e.signUp()
	shop := pictureShop(t)

	res := e.do(request{method: http.MethodPost, path: "/images", body: map[string]any{"url": shop.URL + "/pic.png"}})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	for name, body := range map[string]any{
		"no url":     map[string]any{},
		"an empty":   map[string]any{"url": ""},
		"not a url":  map[string]any{"url": 5},
		"not an obj": []string{"x"},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.do(request{method: http.MethodPost, path: "/images", body: body, header: cookie(admin)})
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "invalid_input", readProblem(t, res).Code)
		})
	}
	res = e.do(request{method: http.MethodPost, path: "/images", raw: []byte("{"), contentType: "application/json", header: cookie(admin)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}
