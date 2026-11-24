package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/israel-duff/taskiem/engine/httpsec"
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
		// /ocr/<versions>/: the files that read a photo's text in the
		// browser, in a folder named by their exact versions
		// (web/scripts/ocr-version.mjs), so a year's cache never serves an
		// old one. Its worker compiles WebAssembly under its own response's
		// policy: only these files are allowed to.
		if strings.HasPrefix(clean, "/assets/") || strings.HasPrefix(clean, "/ocr/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if strings.HasPrefix(clean, "/ocr/") {
			w.Header().Set("Content-Security-Policy", httpsec.OCRCSP)
		}
		if clean == "/sw.js" {
			// The service worker (chat push): a browser checks it afresh each
			// time, and it may control the whole app.
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Service-Worker-Allowed", "/")
		}
		files.ServeHTTP(w, r)
	})
}
