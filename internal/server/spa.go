package server

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// web/dist is copied from the frontend build (`just build-frontend`) before
// compiling. A placeholder index.html is enough for tests and linting.
//
//go:embed all:web/dist
var webFS embed.FS

func init() {
	// The distroless image has no system MIME table, and Go has no built-in
	// entry for this extension.
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic(err)
	}
}

// spaHandler serves files from the embedded frontend build and falls back to
// index.html for client-side routes.
func spaHandler() http.Handler {
	dist, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		panic(err) // the embed path is fixed at compile time
	}
	files := http.FileServerFS(dist)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if info, err := fs.Stat(dist, name); err != nil || info.IsDir() {
			// Unknown path: let the SPA router handle it.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}

		if strings.HasPrefix(name, "assets/") {
			// Vite fingerprints everything under assets/.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
