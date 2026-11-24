package api_test

import (
	"net/url"
	"strings"
	"testing"
)

// A business's profile and gallery (migration 00399): words, a logo and up
// to twelve photos with captions, changed by whoever manages links and read
// by anyone in the business; the search finds a business by its profile
// (its name, one-line description, area and text about it), once it is
// findable, and by nothing else it holds.
func TestBusinessProfileAndGallery(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Media = newProjectStorage(t).service()
	ada := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	dangote := w.tenant(t, "Dangote Stores", "d@dangote.test")
	viewer := addMember(t, w.world, dangote, "vic", "viewer")

	// Nothing said yet: empty words, no logo, no photos.
	p := dangote.must(200, "GET", "/v1/business-profile", nil)
	if p["name"] != "" || p["logo"] != nil || len(p["gallery"].([]any)) != 0 || p["max_gallery"] != float64(12) {
		t.Fatalf("a new profile: %v", p)
	}

	// The words: too long is refused, a viewer may read but not write.
	if s, _ := dangote.do("PUT", "/v1/business-profile", map[string]any{"tagline": strings.Repeat("x", 141)}); s != 400 {
		t.Errorf("a long tagline: %d", s)
	}
	if s, _ := viewer.do("PUT", "/v1/business-profile", map[string]any{"name": "Nope"}); s != 403 {
		t.Errorf("a viewer wrote the profile: %d", s)
	}
	viewer.must(200, "GET", "/v1/business-profile", nil)
	p = dangote.must(200, "PUT", "/v1/business-profile", map[string]any{
		"name": "  Dangote Cement & Sons ", "tagline": "Cement and sand, delivered", "area": "Ikeja", "about": "Family-run since 1998. We deliver to site."})
	if p["name"] != "Dangote Cement & Sons" || p["area"] != "Ikeja" {
		t.Fatalf("the profile: %v", p)
	}

	// Found by the profile, only once it chooses to be.
	search := func(q string) []any {
		return ada.must(200, "GET", "/v1/links/search?q="+url.QueryEscape(q), nil)["results"].([]any)
	}
	if got := search("cement"); len(got) != 0 {
		t.Fatalf("found before choosing to be: %v", got)
	}
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	for _, q := range []string{"cement", "sons", "ikeja", "since 1998", "dangote stores"} {
		got := search(q)
		if len(got) != 1 {
			t.Fatalf("search %q: %v", q, got)
		}
		c := got[0].(map[string]any)
		if c["name"] != "Dangote Cement & Sons" || c["tagline"] != "Cement and sand, delivered" || c["area"] != "Ikeja" {
			t.Errorf("the card says what the profile says (%q): %v", q, c)
		}
	}
	// Clearing a field takes it out of the search.
	dangote.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "Dangote Cement & Sons", "tagline": "", "area": "Ikeja", "about": ""})
	if got := search("since 1998"); len(got) != 0 {
		t.Errorf("cleared text still found: %v", got)
	}
	if got := search("Cement"); len(got) != 1 || got[0].(map[string]any)["tagline"] != nil {
		t.Errorf("a cleared tagline: %v", got)
	}

	// The logo: not an image is refused; an image is shown; it can come off.
	if s, _ := dangote.rawPost(t, "/v1/business-profile/logo", "image/jpeg", []byte("<html>not a photo</html>")); s != 400 {
		t.Errorf("a logo that is no image: %d", s)
	}
	if s, _ := viewer.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photo(t)); s != 403 {
		t.Errorf("a viewer set the logo: %d", s)
	}
	s, p := dangote.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0))
	logo, _ := p["logo"].(map[string]any)
	if s != 200 || logo == nil || logo["width"] == nil {
		t.Fatalf("the logo: %d %v", s, p)
	}
	p = dangote.must(200, "DELETE", "/v1/business-profile/logo", nil)
	if p["logo"] != nil {
		t.Errorf("the logo came off: %v", p)
	}

	// The gallery: twelve, each with its caption; a thirteenth is refused
	// and stores nothing; the same photo twice is one.
	for i := range 12 {
		s, p := dangote.rawPost(t, "/v1/business-profile/gallery?caption="+url.QueryEscape("Photo "+string(rune('A'+i))), "image/jpeg", photoN(t, i))
		if s != 201 || len(p["gallery"].([]any)) != i+1 {
			t.Fatalf("photo %d: %d %v", i, s, p)
		}
	}
	if s, out := dangote.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 12)); s != 400 || !strings.Contains(out["error"].(string), "12") {
		t.Errorf("a thirteenth photo: %d %v", s, out)
	}
	if s, _ := dangote.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 3)); s != 400 {
		// Full already: even a repeat is refused before anything is stored.
		t.Errorf("a repeat when full: %d", s)
	}
	gallery := func() []map[string]any {
		var out []map[string]any
		for _, g := range dangote.must(200, "GET", "/v1/business-profile", nil)["gallery"].([]any) {
			out = append(out, g.(map[string]any))
		}
		return out
	}
	g := gallery()
	if len(g) != 12 || g[0]["caption"] != "Photo A" || g[11]["caption"] != "Photo L" {
		t.Fatalf("the gallery: %v", g)
	}

	// A caption set and cleared; the order changed; a photo removed closes the gap.
	first := g[0]["id"].(string)
	dangote.must(200, "PATCH", "/v1/business-profile/gallery/"+first, map[string]any{"caption": "The shop front"})
	if gallery()[0]["caption"] != "The shop front" {
		t.Errorf("caption")
	}
	dangote.must(200, "PATCH", "/v1/business-profile/gallery/"+first, map[string]any{"caption": ""})
	if gallery()[0]["caption"] != "" {
		t.Errorf("caption cleared")
	}
	if s, _ := dangote.do("PATCH", "/v1/business-profile/gallery/"+first, map[string]any{"caption": strings.Repeat("x", 121)}); s != 400 {
		t.Errorf("a long caption: %d", s)
	}
	var ids []any
	for i := len(g) - 1; i >= 0; i-- {
		ids = append(ids, g[i]["id"])
	}
	if s, _ := dangote.do("PUT", "/v1/business-profile/gallery/order", map[string]any{"ids": ids[1:]}); s != 400 {
		t.Errorf("an order that leaves one out: %d", s)
	}
	dangote.must(200, "PUT", "/v1/business-profile/gallery/order", map[string]any{"ids": ids})
	if g2 := gallery(); g2[0]["id"] != g[11]["id"] || g2[11]["id"] != g[0]["id"] {
		t.Errorf("reordered: %v", g2)
	}
	dangote.must(200, "DELETE", "/v1/business-profile/gallery/"+g[11]["id"].(string), nil)
	if g3 := gallery(); len(g3) != 11 || g3[0]["id"] != g[10]["id"] {
		t.Errorf("after a removal: %v", g3)
	}
	// Room again: the photo that was removed can be added back.
	if s, _ := dangote.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 11)); s != 201 {
		t.Errorf("adding after a removal: %d", s)
	}

	// Another business sees none of it and cannot change it.
	if p := ada.must(200, "GET", "/v1/business-profile", nil); len(p["gallery"].([]any)) != 0 || p["name"] != "" {
		t.Errorf("ada read dangote's profile: %v", p)
	}
	if s, _ := ada.do("DELETE", "/v1/business-profile/gallery/"+first, nil); s != 404 {
		t.Errorf("ada removed dangote's photo: %d", s)
	}
}

// A linked business is shown in chat by the name and logo it chose; the
// logo is served only for an active link, and the registered name stays
// where it is the legal one (the links list that bills and orders read).
func TestLinkedBusinessShownByItsProfile(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Media = newProjectStorage(t).service()
	seller, buyer, sellerID, _ := linkedPair(t, w)
	stranger := w.tenant(t, "Cherry Blocks", "c@cherry.test")
	conv := func(c *client) map[string]any {
		return c.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)
	}

	// Nothing chosen yet: the registered name, no logo.
	if c := conv(buyer); c["name"] != "Dangote Stores" || c["logo"] != false {
		t.Fatalf("before a profile: %v", c)
	}
	if s, _, _ := buyer.getRaw(t, "/v1/links/"+sellerID+"/logo"); s != 404 {
		t.Errorf("a logo that does not exist: %d", s)
	}

	// A profile name and a logo: chat shows them.
	seller.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "Dangote Cement & Sons", "tagline": "", "area": "", "about": ""})
	if s, _ := seller.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photo(t)); s != 200 {
		t.Fatalf("logo: %d", s)
	}
	c := conv(buyer)
	if c["name"] != "Dangote Cement & Sons" || c["logo"] != true {
		t.Fatalf("after a profile: %v", c)
	}
	s, hdr, body := buyer.getRaw(t, "/v1/links/"+sellerID+"/logo")
	if s != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "image/") || len(body) == 0 || hdr.Get("Cache-Control") != "private, max-age=3600" {
		t.Errorf("the logo: %d %v %d bytes", s, hdr, len(body))
	}
	// The registered name stays in the links list (bills and orders read it).
	if l := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any); l["name"] != "Dangote Stores" {
		t.Errorf("the links list: %v", l)
	}

	// Not linked: no logo and the same 404 as no logo; the seller's own
	// conversation names the buyer as it is.
	if s, _, _ := stranger.getRaw(t, "/v1/links/"+sellerID+"/logo"); s != 404 {
		t.Errorf("a stranger read the logo: %d", s)
	}
	if s, _, _ := buyer.getRaw(t, "/v1/links/not-an-id/logo"); s != 404 {
		t.Errorf("a bad id: %d", s)
	}
	if c := conv(seller); c["name"] != "Ada's Kitchen" || c["logo"] != false {
		t.Errorf("the other side: %v", c)
	}

	// The logo taken off: back to none.
	seller.must(200, "DELETE", "/v1/business-profile/logo", nil)
	if c := conv(buyer); c["logo"] != false || c["name"] != "Dangote Cement & Sons" {
		t.Errorf("after the logo came off: %v", c)
	}
	if s, _, _ := buyer.getRaw(t, "/v1/links/"+sellerID+"/logo"); s != 404 {
		t.Errorf("the logo came off: %d", s)
	}
}
