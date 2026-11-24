package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// fakeStorage is enough of Xenibase's management file API for uploads.
func fakeStorage(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = io.WriteString(w, `{"buckets":[]}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/files/buckets"):
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/storage/v1/public/"):
			_, _ = w.Write(photo(t))
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/object"):
			body, _ := io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Query().Get("path"), "size": len(body), "mime_type": r.Header.Get("Content-Type")})
		default:
			t.Errorf("storage: unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func photo(t *testing.T) []byte { return photoN(t, 0) }

// photoN is a photo of its own for each n (another image, another record).
func photoN(t *testing.T, n int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := range 600 {
		for x := range 900 {
			img.Set(x, y, color.RGBA{R: uint8(x / 4), G: uint8(90 + 20*n), B: uint8(y / 3), A: 255}) //nolint:gosec // bounded
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *client) rawPost(t *testing.T, path, ctype string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

var menuTable = map[string]any{"name": "menu", "title": "Menu", "columns": []map[string]any{
	{"name": "name", "type": "text", "required": true},
	{"name": "price", "type": "number"},
	{"name": "photo", "type": "text"},
	{"name": "available", "type": "boolean"},
	{"name": "section", "type": "text"},
	{"name": "supplier_phone", "type": "phone"},
}}

func TestStorefront(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business") // publishing goes live at once (moderation.md)
	owner.must(404, "GET", "/v1/storefront", nil)

	tbl := owner.must(201, "POST", "/v1/tables", menuTable)
	table := tbl["id"].(string)
	jollof := owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Jollof rice", "price": "1500", "available": "yes", "section": "Rice"}}, "Idempotency-Key", "r1")
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Zobo", "price": "300.5", "available": "no", "section": "Drinks"}}, "Idempotency-Key", "r2")
	row := jollof["rows"].([]any)[0].(map[string]any)["id"]

	card := map[string]any{"name": "Ada's Kitchen", "tagline": "Home-cooked", "phone": "0803 123 4567", "whatsapp": "08031234567", "template": "bold", "accent": "orange",
		"hours": map[string]any{"days": []any{[]any{map[string]any{"open": "08:00", "close": "18:00"}}, nil, nil, nil, nil, nil, nil}}}
	columns := map[string]any{"name": "name", "price": "price", "photo": "photo", "available": "available", "category": "section"}
	for _, bad := range []map[string]any{
		{"slug": "gtbank-help", "draft": card},
		{"slug": "ok-slug", "draft": card, "catalogue_table": table, "catalogue_columns": map[string]any{"name": "supplier_phone"}},
		{"slug": "ok-slug", "draft": card, "catalogue_table": table, "catalogue_columns": map[string]any{"name": "name", "price": "section"}},
		{"slug": "ok-slug", "draft": card, "catalogue_table": table, "catalogue_columns": map[string]any{}},
	} {
		owner.must(400, "PUT", "/v1/storefront", bad)
	}
	sf := owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "Ada-Kitchen", "draft": card, "catalogue_table": table, "catalogue_columns": columns})
	if sf["slug"] != "ada-kitchen" || sf["status"] != "draft" || sf["preview_url"] != "" || sf["retention_days"] != float64(365) {
		t.Fatalf("%v", sf)
	}
	// How long ended orders keep their customer's details: 30 days to 10 years.
	owner.must(400, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table, "catalogue_columns": columns, "retention_days": 7})
	if sf := owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table, "catalogue_columns": columns,
		"retention_days": 90}); sf["retention_days"] != float64(90) {
		t.Fatalf("retention: %v", sf)
	}

	// A name that may not be published is refused at publishing.
	bad := map[string]any{}
	for k, v := range card {
		bad[k] = v
	}
	bad["name"] = "Paystack Payments"
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": bad, "catalogue_table": table, "catalogue_columns": columns})
	owner.must(400, "POST", "/v1/storefront/publish", nil)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table, "catalogue_columns": columns})

	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	if pub["status"] != "published" || pub["published_version"] != float64(1) || pub["preview_url"] != "https://ada-kitchen.shops.taskiem.test" || pub["published_slug"] != "ada-kitchen" {
		t.Fatalf("%v", pub)
	}
	cat := owner.must(200, "GET", "/v1/storefront/catalogue", nil)
	items := cat["items"].([]any)
	if len(items) != 2 || cat["error"] != nil {
		t.Fatalf("%v", cat)
	}
	first := items[0].(map[string]any)
	if first["name"] != "Jollof rice" || first["price_kobo"] != float64(150000) || first["available"] != true || first["category"] != "Rice" {
		t.Fatalf("%v", first)
	}
	if second := items[1].(map[string]any); second["price_kobo"] != float64(30050) || second["available"] != false {
		t.Fatalf("%v", second)
	}

	// A photo for a row: stored, set in the table, shown after a refresh.
	if s, _ := owner.rawPost(t, "/v1/storefront/media", "image/jpeg", []byte("<html>not a photo</html>")); s != 400 {
		t.Fatalf("not an image: %d", s)
	}
	s, m := owner.rawPost(t, "/v1/storefront/catalogue/rows/"+jsonNum(row)+"/photo", "image/jpeg", photo(t))
	if s != 200 || m["moderation"] != "unreviewed" || m["urls"].(map[string]any)["320"] == nil {
		t.Fatalf("photo: %d %v", s, m)
	}
	cat = owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	p, _ := cat["items"].([]any)[0].(map[string]any)["photo"].(map[string]any)
	if p["800"] == nil || !strings.HasPrefix(p["800"].(string), store.URL+"/storage/v1/public/taskiem-public/images/") {
		t.Fatalf("photo not in the catalogue: %v", cat)
	}
	// The web app shows it through the API; nothing else is fetched.
	if s, h, body := owner.rawGet(t, "/v1/storefront/image?src="+url.QueryEscape(p["320"].(string))); s != 200 || h.Get("Content-Type") != "image/jpeg" || len(body) == 0 {
		t.Fatalf("image: %d %v", s, h)
	}
	if s, _, _ := owner.rawGet(t, "/v1/storefront/image?src="+url.QueryEscape("http://169.254.169.254/latest/meta-data")); s != 400 {
		t.Fatalf("a foreign URL: %d", s)
	}
	if s, logo := owner.rawPost(t, "/v1/storefront/media", "image/jpeg", photo(t)); s != 201 || logo["id"] != m["id"] {
		t.Fatalf("the same image is one record: %d %v", s, logo)
	}

	// The gallery: photos added with add=1 join the main one, up to six;
	// a new main photo keeps them; any can be taken out, even an entry
	// that is not a photo at all, and the next becomes the main one.
	base := "/v1/storefront/catalogue/rows/" + jsonNum(row) + "/photo"
	photos := func() []any {
		t.Helper()
		got := owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any)[0].(map[string]any)
		ids, _ := got["photo_ids"].([]any)
		return ids
	}
	want := []any{m["id"]}
	for n := 1; n <= 5; n++ {
		s, g := owner.rawPost(t, base+"?add=1", "image/jpeg", photoN(t, n))
		if s != 200 {
			t.Fatalf("photo %d: %d %v", n+1, s, g)
		}
		want = append(want, g["id"])
	}
	if got := photos(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the gallery: %v, want %v", got, want)
	}
	if s, _ := owner.rawPost(t, base+"?add=1", "image/jpeg", photoN(t, 6)); s != 409 {
		t.Fatalf("a seventh photo: %d", s)
	}
	s, main := owner.rawPost(t, base, "image/jpeg", photoN(t, 7))
	if s != 200 {
		t.Fatalf("a new main photo: %d %v", s, main)
	}
	want[0] = main["id"]
	if got := photos(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("a new main photo keeps the gallery: %v, want %v", got, want)
	}
	// Another made the main one: first, the rest in their order.
	owner.must(204, "PUT", base+"/main?media="+want[3].(string), nil)
	if got := photos(); fmt.Sprint(got) != fmt.Sprint([]any{want[3], want[0], want[1], want[2], want[4], want[5]}) {
		t.Fatalf("made the main one: %v", got)
	}
	owner.must(404, "PUT", base+"/main?media="+uuid.NewString(), nil)
	owner.must(204, "PUT", base+"/main?media="+main["id"].(string), nil)
	want = []any{want[0], want[3], want[1], want[2], want[4], want[5]}
	owner.must(204, "DELETE", base+"?media="+main["id"].(string), nil)
	owner.must(404, "DELETE", base+"?media="+main["id"].(string), nil)
	if got := photos(); fmt.Sprint(got) != fmt.Sprint(want[1:]) {
		t.Fatalf("the next is the main one: %v, want %v", got, want[1:])
	}
	cellNow := ""
	for _, id := range want[1:] {
		cellNow += id.(string) + ","
	}
	owner.must(200, "PATCH", "/v1/tables/"+table+"/rows/"+jsonNum(row), map[string]any{"set": map[string]any{"photo": cellNow + "not-a-photo"}})
	owner.must(204, "DELETE", base+"?media=not-a-photo", nil)
	if s, g := owner.rawPost(t, base+"?add=1", "image/jpeg", photoN(t, 8)); s != 200 || len(photos()) != 6 {
		t.Fatalf("its place is free again: %d %v", s, g)
	}
	// A full gallery holding what the page cannot show lets go of it.
	owner.must(200, "PATCH", "/v1/tables/"+table+"/rows/"+jsonNum(row), map[string]any{"set": map[string]any{"photo": cellNow + "not-a-photo"}})
	if s, g := owner.rawPost(t, base+"?add=1", "image/jpeg", photoN(t, 9)); s != 200 || len(photos()) != 6 {
		t.Fatalf("a full gallery with an entry it cannot show: %d %v", s, g)
	}

	// Closed and reopened; the QR code is the preview address.
	if c := owner.must(200, "POST", "/v1/storefront/close", map[string]any{"message": "Back on Monday"}); c["status"] != "closed" || c["closed_message"] != "Back on Monday" {
		t.Fatalf("%v", c)
	}
	owner.must(400, "POST", "/v1/storefront/close", map[string]any{"message": "see https://evil.test"})
	if r := owner.must(200, "POST", "/v1/storefront/reopen", nil); r["status"] != "published" {
		t.Fatalf("%v", r)
	}
	if s, h, body := owner.rawGet(t, "/v1/storefront/qr"); s != 200 || h.Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(body, "<svg") {
		t.Fatalf("qr: %d %v", s, h)
	}
	if s, h, body := owner.rawGet(t, "/v1/storefront/qr?format=png"); s != 200 || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("qr png: %d", s)
	}

	// Another organisation cannot take the address; a builder cannot edit.
	other := w.tenant(t, "Bola Bakes", "bola@bola.test")
	other.must(409, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Bola"}})
	other.must(404, "GET", "/v1/storefront/catalogue", nil)
	builder := addMember(t, w.world, owner, "bld", "builder")
	builder.must(403, "GET", "/v1/storefront", nil)
	builder.must(403, "POST", "/v1/storefront/publish", nil)

	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"storefront.save", "storefront.publish", "storefront.close", "storefront.reopen", "storefront.media_add"} {
		if !strings.Contains(audit, a) {
			t.Errorf("no %s in the audit log", a)
		}
	}
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The first photo makes the business's data project, where photos live, as
// the first table does; when the platform's Xenibase refuses (here, a
// server address that already ends in /api/v1), the owner is told what
// was refused, not "Internal error".
func TestStorefrontPhotoMakesTheDataProject(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.Media = &media.Service{Credentials: w.svc.Credentials, Guard: w.env.Egress}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	w.svc.Platform.URL = w.pg.URL + "/api/v1"
	s, body := owner.rawPost(t, "/v1/storefront/media", "image/jpeg", photo(t))
	if s == 500 || !strings.Contains(toJSON(body), "GET /api/v1/api/v1/projects") {
		t.Fatalf("a wrong server address: %d %v", s, body)
	}
	// With the right address, the photo's upload makes the project first.
	w.svc.Platform.URL = w.pg.URL
	s, body = owner.rawPost(t, "/v1/storefront/media", "image/jpeg", photo(t))
	st := owner.must(200, "GET", "/v1/tables/status", nil)
	if st["status"] != "ready" {
		t.Fatalf("the project after a photo: %v (upload %d %v)", st, s, body)
	}
}
