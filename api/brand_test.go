package api_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/publicweb"
)

func TestBrandSettings(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Adunni Bakes", "owner@adunni.test")

	got := owner.must(200, "GET", "/v1/settings/brand", nil)
	if got["theme"] != "clean" || got["color"] != "#0b6e4f" || len(got["themes"].([]any)) != 3 {
		t.Fatalf("default: %v", got)
	}

	got = owner.must(200, "PUT", "/v1/settings/brand", map[string]any{"theme": "bold", "color": "#FFEB3B"})
	pal := got["palette"].(map[string]any)
	if got["theme"] != "bold" || got["color"] != "#ffeb3b" || pal["on_brand"] != "#141414" {
		t.Fatalf("saved: %v", got)
	}
	owner.must(400, "PUT", "/v1/settings/brand", map[string]any{"theme": "neon"})
	owner.must(400, "PUT", "/v1/settings/brand", map[string]any{"color": "red;}"})
	owner.must(400, "PUT", "/v1/settings/brand", map[string]any{"theme": "clean", "logo": uuid.NewString()})
	owner.must(400, "PUT", "/v1/settings/brand", map[string]any{"theme": "clean", "logo": "not-an-id"})

	// The public host reads it, and only for an active business.
	read := publicweb.DBBrands(w.env.DB.PublicPool(t, 1))
	b, ok, err := read(t.Context(), tenantID(t, owner))
	if err != nil || !ok || b.Theme != "bold" || b.Color != "#ffeb3b" {
		t.Fatalf("public read: %+v %v %v", b, ok, err)
	}
	if _, ok, err := read(t.Context(), uuid.New()); ok || err != nil {
		t.Fatalf("an unknown business: %v %v", ok, err)
	}

	// A preview of a look not yet saved: the public host's pages, sealed
	// for it.
	owner.must(409, "POST", "/v1/settings/brand/preview", map[string]any{"theme": "editorial"})
	keys, _ := forms.NewKeys(bytes.Repeat([]byte{7}, 32))
	w.srv.Forms = &forms.Service{Pool: w.env.DB.App, Keys: keys, FormHost: "forms.taskiem.test"}
	pages := owner.must(200, "POST", "/v1/settings/brand/preview", map[string]any{"theme": "editorial", "color": "#1f3a5f"})["pages"].(map[string]any)
	for _, page := range publicweb.PreviewPages {
		u, _ := pages[page].(string)
		if !strings.HasPrefix(u, "https://forms.taskiem.test/_preview/"+page+"?p=") {
			t.Errorf("%s: %q", page, u)
		}
	}
	owner.must(400, "POST", "/v1/settings/brand/preview", map[string]any{"theme": "neon"})
	if got := owner.must(200, "GET", "/v1/settings/brand", nil); got["theme"] != "bold" {
		t.Errorf("a preview changed the brand: %v", got)
	}

	// Members who may not manage the storefront cannot change it.
	approver := addMember(t, w, owner, "app", "approver")
	approver.must(200, "GET", "/v1/settings/brand", nil)
	approver.must(403, "PUT", "/v1/settings/brand", map[string]any{"theme": "clean"})
}
