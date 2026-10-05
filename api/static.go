package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SPA serves the built web app from dir. Unknown paths that are not files
// get index.html, so client-side routes survive a reload. Hashed assets
// are cached for a year; index.html never is.
func SPA(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/v1/") || strings.HasPrefix(clean, "/hooks/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		//nolint:gosec // clean is path.Clean of a rooted path: it cannot climb out of dir
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean))); err != nil || fi.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
