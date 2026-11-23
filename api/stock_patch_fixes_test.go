package api_test

import (
	"strings"
	"testing"
)

// PATCH /v1/stock/items/{id}, as review left it: a product renamed keeps
// the stock item it shares with other products as it was; a refused
// change writes nothing; a product in sizes and colours gets no stock
// item of its own, its levels going to its sizes'.
func TestPatchStockItemFixes(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Malt House", "ada@malt.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	stockItems := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, x := range owner.must(200, "GET", "/v1/stock/stock-items", nil)["items"].([]any) {
			out[x.(map[string]any)["id"].(string)] = x.(map[string]any)
		}
		return out
	}

	// One stock item, "Coke", counting two products.
	coke := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Coke", "quantity": 24, "unit": "bottle", "material": true},
		"Idempotency-Key", "coke")["item"].(map[string]any)["id"].(string)
	bottle := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Coke bottle", "quantity": 0, "price_kobo": 40000},
		"Idempotency-Key", "bottle")["item"].(map[string]any)["id"].(string)
	pack := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Coke pack of 6", "quantity": 0, "price_kobo": 210000},
		"Idempotency-Key", "pack")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/stock/products/"+bottle+"/counted-by", map[string]any{"stock_item_id": coke, "uses": 1})
	owner.must(200, "PUT", "/v1/stock/products/"+pack+"/counted-by", map[string]any{"stock_item_id": coke, "uses": 6})

	// 1. Renaming the pack leaves the shared stock item's name; what is
	// the stock item's own (its levels, its unit: the Stock page changes
	// them through the product it is sold as) still reaches it.
	owner.must(200, "PATCH", "/v1/stock/items/"+pack, map[string]any{"name": "Coke six-pack", "reorder_level": 12})
	x := stockItems()[coke]
	if x["name"] != "Coke" || x["unit"] != "bottle" {
		t.Fatalf("the shared stock item renamed with one of its products: %v", x)
	}
	if x["low_level"] != float64(12) {
		t.Fatalf("the shared stock item's level still set: %v", x)
	}
	owner.must(200, "PATCH", "/v1/stock/items/"+pack, map[string]any{"unit": "bottle (35cl)"})
	if x := stockItems()[coke]; x["name"] != "Coke" || x["unit"] != "bottle (35cl)" {
		t.Fatalf("the shared stock item's unit: %v", x)
	}
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Coke six-pack"] == nil || items["Coke bottle"] == nil {
		t.Fatalf("the pack renamed, the bottle not: %v", items)
	}
	// A product its stock item counts alone still takes it along.
	fanta := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Fanta", "quantity": 3, "unit": "bottle", "price_kobo": 40000},
		"Idempotency-Key", "fanta")["item"].(map[string]any)
	owner.must(200, "PATCH", "/v1/stock/items/"+fanta["id"].(string), map[string]any{"name": "Fanta Orange", "unit": "can"})
	if x := stockItems()[fanta["stock_item_id"].(string)]; x["name"] != "Fanta Orange" || x["unit"] != "can" {
		t.Fatalf("its own stock item follows it: %v", x)
	}

	// 2. A supplier that is none of the business's: refused, and nothing
	// of the product changed.
	code, out := owner.do("PATCH", "/v1/stock/items/"+fanta["id"].(string), map[string]any{"name": "Fanta Lemon", "price_kobo": 50000, "supplier": "Nobody Ltd"})
	if code != 400 || !strings.Contains(toJSON(out), "not one of your suppliers") {
		t.Fatalf("a bad supplier: %d %v", code, out)
	}
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if f := items["Fanta Orange"]; f == nil || f["price_kobo"] != float64(40000) || items["Fanta Lemon"] != nil {
		t.Fatalf("a refused change wrote the row: %v", items)
	}
	if x := stockItems()[fanta["stock_item_id"].(string)]; x["name"] != "Fanta Orange" {
		t.Fatalf("a refused change wrote the stock item: %v", x)
	}

	// 3. A product in sizes and colours: its reorder level goes to its
	// sizes, no stock item made for it (here one counted by none before
	// its sizes were set up).
	prod := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Kaftan", "quantity": 0, "unit": "piece", "price_kobo": 2500000},
		"Idempotency-Key", "k")["item"].(map[string]any)
	owner.must(200, "DELETE", "/v1/stock/products/"+prod["id"].(string)+"/counted-by", nil)
	owner.must(200, "PUT", "/v1/stock/items/"+prod["id"].(string)+"/variants", map[string]any{
		"choices": []any{map[string]any{"name": "Size", "values": []string{"S", "M"}}},
		"cells":   []any{map[string]any{"variant": map[string]any{"Size": "S"}, "count": 2}, map[string]any{"variant": map[string]any{"Size": "M"}, "count": 3}},
	}, "Idempotency-Key", "v")
	product := func() map[string]any {
		for _, x := range owner.must(200, "GET", "/v1/stock", nil)["items"].([]any) {
			if x.(map[string]any)["id"] == prod["id"] {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no product %v", prod["id"])
		return nil
	}
	if it := product(); it["choices"] == nil || it["stock_item_id"] != nil {
		t.Fatalf("the product in sizes, counted by none: %v", it)
	}
	got := owner.must(200, "PATCH", "/v1/stock/items/"+prod["id"].(string), map[string]any{"reorder_level": 3})["item"].(map[string]any)
	if got["stock_item_id"] != nil {
		t.Fatalf("a stock item linked to the product in sizes: %v", got)
	}
	for _, x := range stockItems() {
		for _, p := range x["sold_as"].([]any) {
			if p.(map[string]any)["product_id"] == prod["id"] {
				t.Fatalf("a stock item counts the product in sizes: %v", x)
			}
		}
	}
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	for _, n := range []string{"Kaftan – S", "Kaftan – M"} {
		if items[n] == nil || items[n]["reorder_level"] != float64(3) {
			t.Fatalf("%s takes the product's level: %v", n, items)
		}
	}
}
