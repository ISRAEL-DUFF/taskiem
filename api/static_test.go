package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// The web app is served as files; the service worker of chat push is checked
// afresh by a browser each time and may control the whole app.
func TestStaticServesTheServiceWorker(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "assets"), 0o750))
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "sw.js"), []byte("// worker"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "assets", "app-abc.js"), []byte("// app"), 0o600))
	h := api.SPA(dir)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	sw := get("/sw.js")
	if sw.Code != 200 || sw.Header().Get("Cache-Control") != "no-cache" || sw.Header().Get("Service-Worker-Allowed") != "/" || sw.Body.String() != "// worker" {
		t.Errorf("/sw.js: %d %v %q", sw.Code, sw.Header(), sw.Body.String())
	}
	if ct := sw.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("/sw.js is served as %q: a browser refuses a worker that is not JavaScript", ct)
	}
	if a := get("/assets/app-abc.js"); a.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || a.Header().Get("Service-Worker-Allowed") != "" {
		t.Errorf("an asset: %v", a.Header())
	}
	if r := get("/chat"); r.Code != 200 || r.Body.String() != "<html>app</html>" || r.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("a route: %d %q", r.Code, r.Body.String())
	}
	if r := get("/v1/nothing"); r.Code != 404 {
		t.Errorf("an API path: %d", r.Code)
	}
}
