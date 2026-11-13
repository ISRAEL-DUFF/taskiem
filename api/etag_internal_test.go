package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// etagJSON: a JSON GET gets an ETag and, asked again with it, a 304 with no
// body; anything else passes through as it is (a stream, a file, an error,
// a write).
func TestETagJSON(t *testing.T) {
	body := `{"orders":[1,2,3]}`
	h := etagJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		case "/file":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF"))
		case "/error":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no"}`))
		}
	}))
	get := func(method, path, ifNone string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if ifNone != "" {
			r.Header.Set("If-None-Match", ifNone)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := get(http.MethodGet, "/json", "")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || first.Body.String() != body || tag == "" {
		t.Fatalf("first: %d %q %q", first.Code, first.Body.String(), tag)
	}
	again := get(http.MethodGet, "/json", tag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("ETag") != tag {
		t.Errorf("unchanged: %d %q", again.Code, again.Body.String())
	}
	if other := get(http.MethodGet, "/json", `W/"something-else"`); other.Code != 200 || other.Body.String() != body {
		t.Errorf("changed: %d", other.Code)
	}
	if f := get(http.MethodGet, "/file", ""); f.Code != 200 || f.Header().Get("ETag") != "" || f.Body.String() != "%PDF" {
		t.Errorf("a file: %d %q", f.Code, f.Header().Get("ETag"))
	}
	if e := get(http.MethodGet, "/error", "*"); e.Code != 404 || e.Header().Get("ETag") != "" {
		t.Errorf("an error: %d", e.Code)
	}
	if p := get(http.MethodPost, "/json", tag); p.Code != 200 || p.Header().Get("ETag") != "" {
		t.Errorf("a write: %d", p.Code)
	}
}
