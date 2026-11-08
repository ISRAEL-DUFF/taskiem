package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// Product categories (docs/spec/chat-categories-and-variants.md, Part A):
// the catalogue gets a category column the first time one is set; items
// carry it; WhatsApp answers a category asked for by name, 10 at a time.
func TestCategories(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const on = "+2348010000081"
	w.bind(t, owner, on)
	owner.must(200, "POST", "/v1/stock/setup", nil)
	if cols := owner.must(200, "GET", "/v1/stock", nil)["columns"].(map[string]any); cols["category"] != nil {
		t.Fatalf("no category column before one is set: %v", cols)
	}
	plain := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Shoe polish", "quantity": 9}, "Idempotency-Key", "p")["item"].(map[string]any)
	if plain["category"] != nil {
		t.Fatalf("no category: %v", plain)
	}
	owner.must(400, "POST", "/v1/stock/items", map[string]any{"name": "Long", "quantity": 1, "category": strings.Repeat("x", 61)}, "Idempotency-Key", "x")
	for i := 1; i <= 12; i++ {
		it := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": fmt.Sprintf("Air Max %02d", i), "quantity": i, "unit": "pair",
			"category": "  Sneakers ", "reorder_level": 2}, "Idempotency-Key", fmt.Sprintf("s%d", i))["item"].(map[string]any)
		if it["category"] != "Sneakers" {
			t.Fatalf("category trimmed and kept: %v", it)
		}
	}
	got := owner.must(200, "GET", "/v1/stock", nil)
	if got["columns"].(map[string]any)["category"] != "category" {
		t.Fatalf("the column is added and mapped: %v", got["columns"])
	}
	// Set on an item that has none, and cleared.
	id := plain["id"].(string)
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": "Care"})["item"].(map[string]any); it["category"] != "Care" {
		t.Fatalf("set: %v", it)
	}
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": ""})["item"].(map[string]any); it["category"] != nil {
		t.Fatalf("cleared: %v", it)
	}
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": "Sandals"})

	// WhatsApp: the category by name, 10 at a time, then *more*.
	r := w.say(t, on, "sneakers")
	if !strings.Contains(r, "*Sneakers* (12):") || !strings.Contains(r, "• Air Max 01: 1 pair (low)") || !strings.Contains(r, "• Air Max 10: 10 pairs") ||
		strings.Contains(r, "Air Max 11") || !strings.Contains(r, "Send *more* for the rest.") {
		t.Fatalf("a category: %q", r)
	}
	if r := w.say(t, on, "more"); !strings.Contains(r, "• Air Max 11: 11 pairs") || !strings.Contains(r, "• Air Max 12") || strings.Contains(r, "Air Max 10") ||
		strings.Contains(r, "*more*") {
		t.Fatalf("more: %q", r)
	}
	if r := w.say(t, on, "show me sandals"); !strings.Contains(r, "*Sandals* (1):") || !strings.Contains(r, "• Shoe polish: 9") {
		t.Fatalf("show me: %q", r)
	}
	if r := w.say(t, on, "stock sneaker"); !strings.Contains(r, "*Sneakers* (12):") {
		t.Fatalf("stock and a singular: %q", r)
	}
	// Something else lets the list go and is read as itself.
	w.say(t, on, "sneakers")
	if r := w.say(t, on, "stock shoe polish"); !strings.Contains(r, "*Shoe polish*: 9.") {
		t.Fatalf("after a list, an item: %q", r)
	}
	// An item called what a category is called answers as the item.
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Sandals", "quantity": 4, "category": "Sandals"}, "Idempotency-Key", "sd")
	if r := w.say(t, on, "stock sandals"); !strings.Contains(r, "*Sandals*: 4.") {
		t.Fatalf("an item: %q", r)
	}
	if audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=100", nil)); !strings.Contains(audit, `"products.category"`) {
		t.Fatal("the column's adding is not audited")
	}
}

// A price list carries each item's category to the buyer, and a category
// changed later reaches the buyer too.
func TestCategoriesOnPriceLists(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	rice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50 kg", "quantity": 50, "unit": "bag", "price_kobo": 4500000, "category": "Grains"},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": rice, "price_kobo": 4200000}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	category := func() any {
		t.Helper()
		got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)
		if len(got) != 1 {
			t.Fatalf("one supplier: %v", got)
		}
		return got[0].(map[string]any)["items"].([]any)[0].(map[string]any)["category"]
	}
	if c := category(); c != "Grains" {
		t.Fatalf("the buyer sees the seller's category: %v", c)
	}
	if l := seller.must(200, "GET", "/v1/price-lists", nil)["lists"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any); l["category"] != "Grains" {
		t.Fatalf("the seller's list: %v", l)
	}
	seller.must(200, "PATCH", "/v1/stock/items/"+rice, map[string]any{"category": "Rice and grains"})
	if c := category(); c != "Rice and grains" {
		t.Fatalf("changed later: %v", c)
	}
}
