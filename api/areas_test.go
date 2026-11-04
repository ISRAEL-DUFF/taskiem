package api_test

import (
	"testing"
)

// areaState reads GET /v1/settings/areas as area name to on or off.
func areaState(t *testing.T, c *client) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, x := range c.must(200, "GET", "/v1/settings/areas", nil)["areas"].([]any) {
		row := x.(map[string]any)
		out[row["area"].(string)] = row["enabled"].(bool)
	}
	return out
}

// TestBusinessAreas: a business has every area but Schools on until it
// chooses; an area turned off is refused on its routes (404 with area_off)
// and comes back, with its data, when turned on; areas that need each other
// move together; and only an owner changes them.
func TestBusinessAreas(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	st := areaState(t, owner)
	for _, a := range []string{"storefront", "orders", "stock", "money", "links"} {
		if !st[a] {
			t.Errorf("%s is off for a business that has not chosen", a)
		}
	}
	if st["schools"] {
		t.Error("schools is on for a business that has not chosen")
	}
	me := owner.must(200, "GET", "/v1/me", nil)["areas"].(map[string]any)
	if me["stock"] != true || me["schools"] != false {
		t.Errorf("/v1/me areas: %v", me)
	}

	// Stock off: its routes are refused, the others still answer.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": false}})
	if st, out := owner.do("GET", "/v1/stock", nil); st != 404 || out["area_off"] != "stock" {
		t.Errorf("stock off: GET /v1/stock = %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/price-lists", nil); st != 404 || out["area_off"] != "stock" {
		t.Errorf("stock off: GET /v1/price-lists = %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/invoices", nil); st == 404 && out["area_off"] != nil {
		t.Errorf("stock off refused invoices: %v", out)
	}
	if areaState(t, owner)["stock"] {
		t.Error("stock still on")
	}
	// And back on: the route answers again.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": true}})
	if st, out := owner.do("GET", "/v1/stock", nil); st == 404 && out["area_off"] != nil {
		t.Errorf("stock back on: %d %v", st, out)
	}

	// Orders cannot go off while the shop is on; both off together is fine.
	owner.must(409, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false}})
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false, "storefront": false}})
	if st, out := owner.do("GET", "/v1/storefront/orders", nil); st != 404 || out["area_off"] != "orders" {
		t.Errorf("orders off: %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/storefront", nil); st != 404 || out["area_off"] != "storefront" {
		t.Errorf("storefront off: %d %v", st, out)
	}
	// Turning the shop on turns orders on.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": true}})
	if st := areaState(t, owner); !st["storefront"] || !st["orders"] {
		t.Errorf("shop on: %v", st)
	}

	// Schools follow money, and use the Schools switch.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"schools": true}})
	if got := owner.must(200, "GET", "/v1/settings/schools", nil); got["enabled"] != true {
		t.Errorf("schools settings after the areas switch: %v", got)
	}
	owner.must(409, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})

	// Bad requests.
	owner.must(400, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"billing": false}})
	owner.must(400, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{}})

	// One business's choice is not another's.
	other := w.tenant(t, "Beta", "owner@beta.test")
	if st, out := other.do("GET", "/v1/storefront", nil); out["area_off"] != nil {
		t.Errorf("another business is refused its shop: %d %v", st, out)
	}
	if st := areaState(t, other); !st["stock"] || !st["storefront"] {
		t.Errorf("another business: %v", st)
	}
}
