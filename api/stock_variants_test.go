package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// Sizes and colours for the shop itself (docs/spec/chat-categories-and-
// variants.md, phase 3): a trainer in 3 sizes and 2 colours, each its own
// row with its own stock; the product's own stock moves into them; a
// sale of one; stock in for several as one diary line; a low card per
// product; a size renamed, another retired; the product's new price
// reaching the sizes that share it; the public page listing the product.
func TestStockVariants(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	prod := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 5, "unit": "pair", "price_kobo": 4500000,
		"reorder_level": 2, "category": "Sneakers"}, "Idempotency-Key", "p")["item"].(map[string]any)
	id := prod["id"].(string)
	base := "/v1/stock/items/" + id + "/variants"
	choices := []any{
		map[string]any{"name": "Size", "values": []string{"40", "41", "42"}},
		map[string]any{"name": "Colour", "values": []string{"Black", "White"}},
	}
	cell := func(size, colour string, count int, extra ...any) map[string]any {
		c := map[string]any{"variant": map[string]any{"Size": size, "Colour": colour}, "count": count}
		for i := 0; i+1 < len(extra); i += 2 {
			c[extra[i].(string)] = extra[i+1]
		}
		return c
	}
	owner.must(400, "PUT", base, map[string]any{"choices": choices, "cells": []any{}}) // no key
	owner.must(400, "PUT", base, map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{}}}}, "Idempotency-Key", "bad")
	owner.must(400, "PUT", base, map[string]any{"choices": choices, "cells": []any{cell("44", "Black", 1)}}, "Idempotency-Key", "bad2")
	out := owner.must(200, "PUT", base, map[string]any{"choices": choices, "cells": []any{
		cell("40", "Black", 2), cell("41", "Black", 4), cell("42", "Black", 3),
		cell("40", "White", 0), cell("41", "White", 1), cell("42", "White", 2, "price_kobo", 4800000),
	}}, "Idempotency-Key", "v1")
	vs := out["variants"].([]any)
	if len(vs) != 6 {
		t.Fatalf("variants: %v", out)
	}
	byName := map[string]map[string]any{}
	for _, x := range vs {
		v := x.(map[string]any)
		byName[v["name"].(string)] = v
	}
	b42 := byName["Air Max 90 – 42, Black"]
	w42 := byName["Air Max 90 – 42, White"]
	if b42 == nil || b42["stock"] != float64(3) || b42["variant_of"] != id || b42["price_kobo"] != float64(4500000) || b42["category"] != "Sneakers" ||
		b42["unit"] != "pair" || b42["reorder_level"] != float64(2) || w42["price_kobo"] != float64(4800000) {
		t.Fatalf("made: %v", byName)
	}
	if it := out["item"].(map[string]any); it["stock"] != float64(0) || len(it["choices"].([]any)) != 2 {
		t.Fatalf("the product keeps no stock and its choices: %v", it)
	}
	// Another product's size is not a product.
	owner.must(400, "PUT", "/v1/stock/items/"+b42["id"].(string)+"/variants", map[string]any{"choices": choices}, "Idempotency-Key", "v-of-v")

	// A sale of size 42 black.
	owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": b42["id"], "quantity": 2}}, "paid_how": "cash"},
		"Idempotency-Key", "sale1")
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Air Max 90 – 42, Black"]["stock"] != float64(1) {
		t.Fatalf("sold: %v", items["Air Max 90 – 42, Black"])
	}
	// One low card for the product, naming its low sizes.
	alerts := toJSON(owner.must(200, "GET", "/v1/chat-alerts", nil))
	if strings.Count(alerts, "Air Max 90 is running low") != 1 || !strings.Contains(alerts, `"`+b42["id"].(string)+`"`) ||
		!strings.Contains(alerts, "40, White") || strings.Contains(alerts, "Air Max 90 – 42, Black is running low") {
		t.Fatalf("one low card for the product, its low sizes named: %s", alerts)
	}

	// Stock in for two of them: one diary line.
	owner.must(400, "POST", "/v1/stock/movements/grid", map[string]any{"reason": "received", "lines": []any{map[string]any{"item_id": id, "change": 1}}},
		"Idempotency-Key", "g0")
	owner.must(200, "POST", "/v1/stock/movements/grid", map[string]any{"reason": "received", "lines": []any{
		map[string]any{"item_id": b42["id"], "change": 5}, map[string]any{"item_id": w42["id"], "change": 2},
	}}, "Idempotency-Key", "g1")
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Air Max 90 – 42, Black"]["stock"] != float64(6) || items["Air Max 90 – 42, White"]["stock"] != float64(4) {
		t.Fatalf("stock in: %v", items)
	}
	owner.must(200, "POST", "/v1/stock/movements/grid", map[string]any{"reason": "count", "lines": []any{
		map[string]any{"item_id": b42["id"], "count": 7},
	}}, "Idempotency-Key", "g2")
	diary := toJSON(owner.must(200, "GET", "/v1/taskiem-chat", nil))
	for _, line := range []string{"Air Max 90 now comes in 6 sizes and colours", "Stock in: Air Max 90, 2 sizes and colours, +7", "Stock of Air Max 90 counted: 1 size or colour"} {
		if !strings.Contains(diary, line) {
			t.Errorf("not in the diary: %q", line)
		}
	}

	// 40 black corrected to 43; 40 white no longer sold.
	choices[0] = map[string]any{"name": "Size", "values": []string{"41", "42", "43"}}
	b40 := byName["Air Max 90 – 40, Black"]
	owner.must(200, "PUT", base, map[string]any{"choices": choices, "cells": []any{
		map[string]any{"id": b40["id"], "variant": map[string]any{"Size": "43", "Colour": "Black"}},
		map[string]any{"id": byName["Air Max 90 – 41, Black"]["id"], "variant": map[string]any{"Size": "41", "Colour": "Black"}},
		map[string]any{"id": b42["id"], "variant": map[string]any{"Size": "42", "Colour": "Black"}},
		map[string]any{"id": byName["Air Max 90 – 41, White"]["id"], "variant": map[string]any{"Size": "41", "Colour": "White"}},
		map[string]any{"id": w42["id"], "variant": map[string]any{"Size": "42", "Colour": "White"}, "price_kobo": 4800000},
	}}, "Idempotency-Key", "v2")
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if r := items["Air Max 90 – 43, Black"]; r == nil || r["id"] != b40["id"] || r["stock"] != float64(2) {
		t.Fatalf("renamed, its stock kept: %v", r)
	}
	if r := items["Air Max 90 – 40, White"]; r == nil || r["available"] != false {
		t.Fatalf("retired, not deleted: %v", r)
	}

	// The product's new price reaches the sizes that share it.
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"price_kobo": 4600000})
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Air Max 90 – 42, Black"]["price_kobo"] != float64(4600000) || items["Air Max 90 – 42, White"]["price_kobo"] != float64(4800000) {
		t.Fatalf("prices: %v / %v", items["Air Max 90 – 42, Black"], items["Air Max 90 – 42, White"])
	}

	// The page lists the product with its sizes together, not each size.
	cat := owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any)
	if len(cat) != 1 {
		t.Fatalf("the page's items: %v", cat)
	}
	page := cat[0].(map[string]any)
	if page["variants"] != float64(5) || page["stock"] != float64(2+4+7+1+4) {
		t.Fatalf("the product on the page: %v", page)
	}

	// No choices: all off sale.
	owner.must(200, "PUT", base, map[string]any{"choices": []any{}, "cells": []any{}}, "Idempotency-Key", "v3")
	n := 0
	for name, it := range itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)) {
		if strings.HasPrefix(name, "Air Max 90 –") && it["available"] == true {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("on sale after taking them off: %d", n)
	}
	_ = fmt.Sprint
}
