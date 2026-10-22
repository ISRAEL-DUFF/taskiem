package api_test

import (
	"testing"

	"github.com/google/uuid"

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

	// Members who may not manage the storefront cannot change it.
	approver := addMember(t, w, owner, "app", "approver")
	approver.must(200, "GET", "/v1/settings/brand", nil)
	approver.must(403, "PUT", "/v1/settings/brand", map[string]any{"theme": "clean"})
}
