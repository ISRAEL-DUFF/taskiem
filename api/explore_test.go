package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Explore (migration 00401): the page lists businesses that chose to be
// found, with a logo and photos (the gallery, else a few product photos);
// what is not to be shown answers the same 404 whoever asks and why.
func TestExploreBrowse(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Media = newProjectStorage(t).service()
	ada := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	dangote := w.tenant(t, "Dangote Stores", "d@dangote.test")
	cherry := w.tenant(t, "Cherry Blocks", "c@cherry.test")
	hidden := w.tenant(t, "Hidden Stores", "h@hidden.test")
	adaID, dangoteID, cherryID, hiddenID := tenantOf(t, ada), tenantOf(t, dangote), tenantOf(t, cherry), tenantOf(t, hidden)

	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "", "area": "Ikeja", "about": ""})
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	dangote.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "Dangote Cement & Sons", "tagline": "Cement and sand, delivered", "area": "Ikeja", "about": "Family-run since 1998."})
	if s, _ := dangote.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 7)); s != 200 {
		t.Fatalf("logo: %d", s)
	}
	for i := range 3 {
		if s, _ := dangote.rawPost(t, "/v1/business-profile/gallery?caption="+url.QueryEscape("Photo "+string(rune('A'+i))), "image/jpeg", photoN(t, i)); s != 201 {
			t.Fatalf("photo %d: %d", i, s)
		}
	}
	// Not findable: photos it has are never shown.
	if s, _ := hidden.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 9)); s != 201 {
		t.Fatal("hidden photo")
	}
	// Cherry: findable in Lekki, no gallery; its published catalogue has a
	// product photo (and one for an item that is not available).
	cherry.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	cherry.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "Blocks and pavers", "area": "Lekki", "about": ""})
	if s, _ := cherry.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 5)); s != 201 {
		t.Fatal("cherry photo")
	}
	cp := cherry.must(200, "GET", "/v1/business-profile", nil)["gallery"].([]any)[0].(map[string]any)
	urls, _ := json.Marshal(cp["photo"].(map[string]any)["urls"])
	cherry.must(200, "DELETE", "/v1/business-profile/gallery/"+cp["id"].(string), nil)
	sf := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, published_version, published_slug, created_by) VALUES ($1, $2, 'cherry-blocks', 'published', 1, 'cherry-blocks', 'test')`, sf, cherryID); err != nil {
		t.Fatal(err)
	}
	for i, avail := range []bool{true, false} {
		item, _ := json.Marshal(map[string]any{"id": string(rune('a' + i)), "name": "Block " + string(rune('A'+i)), "available": avail, "photo": json.RawMessage(urls)})
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, 'menu', $2, $3, $4, $5)`, sf, string(rune('a'+i)), cherryID, i, item); err != nil {
			t.Fatal(err)
		}
	}

	names := func(cards any) []string {
		var out []string
		for _, c := range cards.([]any) {
			out = append(out, c.(map[string]any)["name"].(string))
		}
		return out
	}
	page := ada.must(200, "GET", "/v1/explore", nil)
	// Near: our own area (Ikeja) only, and not the business itself.
	if got := names(page["near"]); len(got) != 1 || got[0] != "Dangote Cement & Sons" {
		t.Fatalf("near: %v", got)
	}
	// New: every findable one, the newest first; never the hidden one.
	if got := names(page["fresh"]); len(got) != 2 || got[0] != "Cherry Blocks" || got[1] != "Dangote Cement & Sons" || page["more"] != false {
		t.Fatalf("fresh: %v %v", got, page["more"])
	}
	d := page["near"].([]any)[0].(map[string]any)
	if d["logo"] != true || d["photos"] != float64(3) || d["tagline"] != "Cement and sand, delivered" || d["about"] != "Family-run since 1998." || d["state"] != "none" || d["business"] != dangoteID {
		t.Errorf("a card: %v", d)
	}
	// Each photo's words, in order, for the card (chat tools phase 6), and
	// our own area for "Near you · Ikeja".
	if caps, _ := d["captions"].([]any); len(caps) != 3 || caps[0] != "Photo A" || caps[2] != "Photo C" || page["area"] != "Ikeja" {
		t.Errorf("captions and our area: %v %v", d["captions"], page["area"])
	}
	if d["v"] == nil || d["v"] == "" || page["near_more"] != false {
		t.Errorf("a card's version and paging: %v %v", d["v"], page["near_more"])
	}
	c := page["fresh"].([]any)[0].(map[string]any)
	if c["logo"] != false || c["photos"] != float64(1) { // the product photo that is available
		t.Errorf("a card with no gallery: %v", c)
	}

	// Search: by the profile, with the same card; none for the hidden one.
	if got := ada.must(200, "GET", "/v1/explore?q=cement", nil)["results"].([]any); len(got) != 1 || got[0].(map[string]any)["photos"] != float64(3) || got[0].(map[string]any)["logo"] != true {
		t.Errorf("search: %v", got)
	}
	if got := ada.must(200, "GET", "/v1/explore?q=hidden", nil)["results"].([]any); len(got) != 0 {
		t.Errorf("a business that is not findable was found: %v", got)
	}
	// A phone number is the one blind row, never a search.
	if out := ada.must(200, "GET", "/v1/explore?q="+url.QueryEscape("0803 123 4567"), nil); len(out["results"].([]any)) != 0 || out["blind"].(map[string]any)["shown"] != "0803…4567" {
		t.Errorf("a phone: %v", out)
	}

	// The pictures: a photo, the logo; none for what is not to be shown, all the same 404.
	get := func(c *client, path string) int { s, _, _ := c.getRaw(t, path); return s }
	if s, hdr, body := ada.getRaw(t, "/v1/explore/"+dangoteID+"/photos/0"); s != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "image/") || len(body) == 0 || hdr.Get("Cache-Control") != "private, max-age=3600" {
		t.Errorf("a photo: %d %v", s, hdr)
	}
	for path, want := range map[string]int{
		"/v1/explore/" + dangoteID + "/logo":      200,
		"/v1/explore/" + dangoteID + "/photos/2":  200,
		"/v1/explore/" + dangoteID + "/photos/3":  404, // there are three
		"/v1/explore/" + dangoteID + "/photos/99": 404,
		"/v1/explore/" + dangoteID + "/photos/-1": 404,
		"/v1/explore/" + dangoteID + "/photos/x":  404,
		"/v1/explore/" + cherryID + "/logo":       404, // no logo
		"/v1/explore/" + cherryID + "/photos/0":   200, // the product photo
		"/v1/explore/" + cherryID + "/photos/1":   404, // the unavailable item's is not shown
		"/v1/explore/" + hiddenID + "/photos/0":   404, // not findable
		"/v1/explore/" + adaID + "/photos/0":      404, // ourselves
		"/v1/explore/not-an-id/logo":              404,
	} {
		if got := get(ada, path); got != want {
			t.Errorf("GET %s: %d, want %d", path, got, want)
		}
	}

	// The version changes with the photos (so a changed picture is not served from the cache).
	before := d["v"]
	g := dangote.must(200, "GET", "/v1/business-profile", nil)["gallery"].([]any)
	dangote.must(200, "PUT", "/v1/business-profile/gallery/order", map[string]any{"ids": []any{g[2].(map[string]any)["id"], g[1].(map[string]any)["id"], g[0].(map[string]any)["id"]}})
	if after := ada.must(200, "GET", "/v1/explore?q=cement", nil)["results"].([]any)[0].(map[string]any)["v"]; after == before || after == "" {
		t.Errorf("the version did not change with the order: %v -> %v", before, after)
	}

	// A refused photo is not shown, and not counted.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_media SET moderation = 'rejected' WHERE id = (SELECT media_id FROM business_gallery WHERE tenant_id = $1 ORDER BY position LIMIT 1)`, dangoteID); err != nil {
		t.Fatal(err)
	}
	if d := ada.must(200, "GET", "/v1/explore?q=cement", nil)["results"].([]any)[0].(map[string]any); d["photos"] != float64(2) {
		t.Errorf("a refused photo counted: %v", d)
	}
	if got := get(ada, "/v1/explore/"+dangoteID+"/photos/2"); got != 404 {
		t.Errorf("a refused photo shown: %d", got)
	}

	// Linked: left out of the browse sections (it is in the chats), still shown.
	ada.must(201, "POST", "/v1/links", map[string]any{"business": dangoteID, "role": "both"})
	l := dangote.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	dangote.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	if got := names(ada.must(200, "GET", "/v1/explore", nil)["near"]); len(got) != 0 {
		t.Errorf("a linked business in the browse: %v", got)
	}
	if got := ada.must(200, "GET", "/v1/explore?q=cement", nil)["results"].([]any); len(got) != 1 || got[0].(map[string]any)["state"] != "active" {
		t.Errorf("a linked business in a search: %v", got)
	}

	// No longer findable: gone from the lists and the pictures, unless linked.
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": false})
	if got := get(ada, "/v1/explore/"+dangoteID+"/photos/0"); got != 200 { // linked: still ours to see
		t.Errorf("a linked business's photo: %d", got)
	}
	cherry.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": false})
	if got := get(ada, "/v1/explore/"+cherryID+"/photos/0"); got != 404 {
		t.Errorf("a business that stopped being found: %d", got)
	}
	if got := names(ada.must(200, "GET", "/v1/explore?section=new", nil)["fresh"]); len(got) != 0 {
		t.Errorf("the new list: %v", got)
	}
}

// Near you and New on Taskiem page: twelve at a time, another page when there
// are more, and no business twice.
func TestExplorePaging(t *testing.T) {
	w := newWAWorld(t)
	w.srv.LoginBurst = 1000     // fifteen businesses sign up from one address,
	w.srv.SignupPerAddress = -1 // and the daily limit is off
	ada := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "", "area": "Ikeja", "about": ""})
	for i := range 14 {
		c := w.tenant(t, "Shop "+string(rune('A'+i))+" Stores", "s"+string(rune('a'+i))+"@paging.test")
		c.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
		c.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "", "area": "Ikeja", "about": ""})
	}
	ids := func(cards any) (out []string) {
		for _, c := range cards.([]any) {
			out = append(out, c.(map[string]any)["business"].(string))
		}
		return out
	}
	first := ada.must(200, "GET", "/v1/explore", nil)
	if len(ids(first["near"])) != 12 || first["near_more"] != true || len(ids(first["fresh"])) != 12 || first["more"] != true {
		t.Fatalf("the first page: near %d/%v, new %d/%v", len(ids(first["near"])), first["near_more"], len(ids(first["fresh"])), first["more"])
	}
	for _, sec := range []struct{ name, key, more string }{{"near", "near", "near_more"}, {"new", "fresh", "more"}} {
		second := ada.must(200, "GET", "/v1/explore?section="+sec.name+"&offset=12", nil)
		if got := ids(second[sec.key]); len(got) != 2 || second[sec.more] != false {
			t.Fatalf("the second page of %s: %v %v", sec.name, got, second[sec.more])
		}
		seen := map[string]bool{}
		for _, id := range append(ids(first[sec.key]), ids(second[sec.key])...) {
			if seen[id] {
				t.Errorf("%s: a business listed twice: %s", sec.name, id)
			}
			seen[id] = true
		}
		if len(seen) != 14 {
			t.Errorf("%s: %d businesses over two pages, want 14", sec.name, len(seen))
		}
	}
}
