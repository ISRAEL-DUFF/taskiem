package api_test

import (
	"strings"
	"testing"
)

// Sizes and colours, as the phase 3 review left them: the product's stock
// is moved into its sizes (not lost and received again); the catalogue
// gets an "available" column to take one off sale with; the product's
// name, details and price reach its sizes, a size's own price staying;
// nothing is written when a count is out of bounds; a size taken off sale
// takes no stock in; price lists take the product with its sizes; and
// WhatsApp sells a size, never the product itself.
func TestStockVariantsFixes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const on = "+2348010000093"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	prod := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 12, "unit": "pair", "price_kobo": 4500000,
		"cost_kobo": 3000000, "reorder_level": 2}, "Idempotency-Key", "p")["item"].(map[string]any)
	id := prod["id"].(string)
	base := "/v1/stock/items/" + id + "/variants"
	list := owner.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": id, "price_kobo": 4000000}}})

	// A catalogue mapped without its "available" column.
	sf := owner.must(200, "GET", "/v1/storefront", nil)
	cols := sf["catalogue_columns"].(map[string]any)
	delete(cols, "available")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": sf["slug"], "draft": sf["draft"], "catalogue_table": sf["catalogue_table"], "catalogue_columns": cols})

	choices := []any{
		map[string]any{"name": "Size", "values": []string{"41", "42"}},
		map[string]any{"name": "Colour", "values": []string{"Black"}},
	}
	cell := func(size string, count int, extra ...any) map[string]any {
		c := map[string]any{"variant": map[string]any{"Size": size, "Colour": "Black"}, "count": count}
		for i := 0; i+1 < len(extra); i += 2 {
			c[extra[i].(string)] = extra[i+1]
		}
		return c
	}
	// Out of bounds: refused before anything is written.
	owner.must(400, "PUT", base, map[string]any{"choices": choices, "cells": []any{cell("41", 1), cell("42", 2_000_000)}}, "Idempotency-Key", "big")
	owner.must(400, "PUT", base, map[string]any{"choices": choices, "cells": []any{cell("41", 1, "price_kobo", 20_000_000_000)}}, "Idempotency-Key", "dear")
	if n := strings.Count(toJSON(owner.must(200, "GET", "/v1/stock", nil)), "Air Max 90 –"); n != 0 {
		t.Fatalf("nothing made by a refused save: %d", n)
	}

	// 12 on the shelf, 10 counted into two sizes (42 at its own price, the
	// product's): 10 moved, 2 counted off the product.
	owner.must(200, "PUT", base, map[string]any{"choices": choices, "cells": []any{cell("41", 4), cell("42", 6, "price_kobo", 4500000)}},
		"Idempotency-Key", "v1")
	if got := owner.must(200, "GET", "/v1/stock", nil)["columns"].(map[string]any); got["available"] != "available" {
		t.Fatalf("the available column mapped again: %v", got)
	}
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	b41, b42 := items["Air Max 90 – 41, Black"], items["Air Max 90 – 42, Black"]
	if items["Air Max 90"]["stock"] != float64(0) || b41["stock"] != float64(4) || b42["stock"] != float64(6) {
		t.Fatalf("shared out: %v", items)
	}
	sh := owner.must(200, "GET", "/v1/stock/shrinkage", nil)
	if sh["received"] != float64(12) || sh["lost"] != float64(2) || sh["lost_kobo"] != float64(6000000) {
		t.Fatalf("moved, not received again; only what was not counted is lost: %v", sh)
	}
	moved := toJSON(owner.must(200, "GET", "/v1/stock/movements?reason=moved", nil))
	if strings.Count(moved, `"reason":"moved"`) != 3 {
		t.Fatalf("moved out of the product, into each size: %s", moved)
	}

	// The price list: the product stays on it, listed once with its sizes
	// on sale (phase 4); a size cannot go on alone.
	lists := toJSON(owner.must(200, "GET", "/v1/price-lists", nil))
	if !strings.Contains(lists, `"name":"Air Max 90 – 41, Black"`) || !strings.Contains(lists, `"item_id":"`+id+`"`) {
		t.Fatalf("the product listed with its sizes: %s", lists)
	}
	owner.must(400, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": b41["id"], "price_kobo": 4000000}}})
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": id, "price_kobo": 4000000, "sizes": []any{map[string]any{"item_id": b42["id"], "price_kobo": 4200000}}}}})

	// The product's changes reach its sizes: name, reorder level, and the
	// price of those without their own.
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"name": "Air Max 95", "price_kobo": 4600000, "reorder_level": 3})
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	n41, n42 := items["Air Max 95 – 41, Black"], items["Air Max 95 – 42, Black"]
	if n41 == nil || n42 == nil || n41["price_kobo"] != float64(4600000) || n42["price_kobo"] != float64(4500000) || n41["reorder_level"] != float64(3) {
		t.Fatalf("followed: %v / %v", n41, n42)
	}
	// A size priced on its own keeps its price from then on.
	owner.must(200, "PATCH", "/v1/stock/items/"+b41["id"].(string), map[string]any{"price_kobo": 4700000})
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"price_kobo": 4900000})
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Air Max 95 – 41, Black"]["price_kobo"] != float64(4700000) {
		t.Fatalf("its own price kept: %v", items["Air Max 95 – 41, Black"])
	}

	// WhatsApp: the product's name asks which size, one choice at a time
	// (black is its only colour, so only the size is asked; its sizes are
	// numbers, so by value, not numbered); the answer goes on with the
	// sale. The size's own name sells it at once.
	if r := w.say(t, on, "sold 1 air max 95 46,000 cash"); !strings.Contains(r, "Which size of Air Max 95? Say it: 41, 42") ||
		strings.Contains(r, "1. 41") || strings.Contains(r, "colour") {
		t.Fatalf("which size: %s", r)
	}
	if r := w.say(t, on, "42"); !strings.Contains(r, "Air Max 95 – 42, Black") || !strings.Contains(r, "₦46,000") {
		t.Fatalf("the sale, once its size is said: %s", r)
	}
	w.say(t, on, "no")
	if r := w.say(t, on, "sold 1 air max 95 42 black 45,000 cash"); !strings.Contains(r, "Air Max 95 – 42, Black") {
		t.Fatalf("a size sold: %s", r)
	}
	w.say(t, on, "yes")
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Air Max 95 – 42, Black"]["stock"] != float64(5) || items["Air Max 95"]["stock"] != float64(0) {
		t.Fatalf("the size sold, not the product: %v / %v", items["Air Max 95 – 42, Black"], items["Air Max 95"])
	}

	// 41 off sale: no stock in for it, and it is offered nowhere.
	owner.must(200, "PUT", base, map[string]any{"choices": choices, "cells": []any{
		map[string]any{"id": b42["id"], "variant": map[string]any{"Size": "42", "Colour": "Black"}, "price_kobo": 4500000},
	}}, "Idempotency-Key", "v2")
	owner.must(400, "POST", "/v1/stock/movements/grid", map[string]any{"reason": "received", "lines": []any{map[string]any{"item_id": b41["id"], "change": 1}}},
		"Idempotency-Key", "g-retired")
	if r := w.say(t, on, "stock air max 95"); strings.Contains(r, "41, Black") {
		t.Fatalf("a size off sale offered: %s", r)
	}
}
