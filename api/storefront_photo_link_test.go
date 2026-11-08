package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// A product's photo from a link (docs/spec/chat-categories-and-variants.md,
// Part C): fetched once through the egress guard and kept as the business's
// own copy; a page, a missing picture, a private address and a bad link
// are each said plainly, and nothing is set.
func TestProductPhotoFromALink(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	pic, pic2 := photo(t), photoN(t, 1)
	web := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shoe.jpg":
			rw.Header().Set("Content-Type", "image/jpeg")
			_, _ = rw.Write(pic)
		case "/shoe-no-type":
			_, _ = rw.Write(pic2) // no Content-Type: the bytes decide
		case "/page":
			rw.Header().Set("Content-Type", "text/html")
			_, _ = rw.Write([]byte("<html><title>Shoes</title></html>"))
		case "/notes.bin":
			rw.Header().Set("Content-Type", "application/octet-stream")
			_, _ = rw.Write([]byte(strings.Repeat("not a picture ", 50)))
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(web.Close)

	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "zuri-atelier", "draft": map[string]any{"name": "Zuri Atelier"}})
	table := owner.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"})["table_id"].(string)
	added := owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Air Max 90", "price": 45000, "available": true}})
	row := jsonNum(added["rows"].([]any)[0].(map[string]any)["id"])
	base := "/v1/storefront/catalogue/rows/" + row + "/photo"
	photos := func() int {
		t.Helper()
		items := owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any)
		it := items[0].(map[string]any)
		n := 0
		if it["photo"] != nil {
			n++
		}
		if g, ok := it["gallery"].([]any); ok {
			n += len(g)
		}
		return n
	}

	// No guard: links are not fetched here.
	w.srv.Egress = nil
	owner.must(409, "POST", base, map[string]any{"url": web.URL + "/shoe.jpg"})
	// The default guard: a private address (this test's server) is refused.
	w.srv.Egress = &egress.Guard{}
	if out := owner.must(400, "POST", base, map[string]any{"url": web.URL + "/shoe.jpg"}); !strings.Contains(out["error"].(string), "could not open that link") {
		t.Fatalf("private: %v", out)
	}
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	for _, c := range []struct{ url, says string }{
		{"ftp://example.test/shoe.jpg", "starts with https://"},
		{"not a link", "starts with https://"},
		{web.URL + "/page", "a page, not a picture"},
		{web.URL + "/missing.jpg", "it answered 404"},
		{web.URL + "/notes.bin", "not a picture we can use"},
	} {
		if out := owner.must(400, "POST", base, map[string]any{"url": c.url}); !strings.Contains(out["error"].(string), c.says) {
			t.Errorf("%s: %v", c.url, out)
		}
	}
	if n := photos(); n != 0 {
		t.Fatalf("nothing set by a refused link: %d", n)
	}

	// A picture: kept as the business's copy, the first the main photo,
	// the next in the gallery; the bytes decide, not the Content-Type.
	m := owner.must(200, "POST", base, map[string]any{"url": web.URL + "/shoe.jpg"})
	if m["id"] == nil {
		t.Fatalf("stored: %v", m)
	}
	owner.must(200, "POST", base+"?add=1", map[string]any{"url": web.URL + "/shoe-no-type"})
	if n := photos(); n != 2 {
		t.Fatalf("two photos: %d", n)
	}
	if audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=50", nil)); !strings.Contains(audit, web.URL+"/shoe.jpg") {
		t.Fatal("where a photo came from is not in the audit")
	}
}
