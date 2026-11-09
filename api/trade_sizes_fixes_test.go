package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// The phase 4 review's fixes in trade:
//   - an order that cannot be placed (under the minimum) adds no size to
//     the buyer's product and links nothing;
//   - ordering a size the buyer lacks keeps the prices of the buyer's
//     sizes that have their own;
//   - a line naming one of the buyer's sizes must be the same size as the
//     supplier's;
//   - a size taken off sale leaves the lists, and buyers', on its own;
//     with none on sale the product cannot be ordered;
//   - the product's lowest price holds for a size no list holds now.
func TestSizesInTradeFixes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	seller.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Zuri Wholesale", "phone": "0803 123 4692"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})

	// The supplier's trainer in sizes 42 to 44.
	am := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "am")["item"].(map[string]any)["id"].(string)
	sizes := []string{"42", "43", "44"}
	var cells []any
	for _, z := range sizes {
		cells = append(cells, map[string]any{"variant": map[string]any{"Size": z}, "count": 20})
	}
	vs := seller.must(200, "PUT", "/v1/stock/items/"+am+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": sizes}}, "cells": cells}, "Idempotency-Key", "v")["variants"].([]any)
	sz := map[string]string{}
	for _, x := range vs {
		v := x.(map[string]any)
		sz[v["variant"].(map[string]any)["Size"].(string)] = v["id"].(string)
	}
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": am, "price_kobo": 4000000, "min_quantity": 6}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})

	// The buyer's own trainer, sold in 42 (at a price of its own) and 43.
	mine := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 5000000},
		"Idempotency-Key", "m")["item"].(map[string]any)["id"].(string)
	own := buyer.must(200, "PUT", "/v1/stock/items/"+mine+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{"42", "43"}}},
		"cells": []any{
			map[string]any{"variant": map[string]any{"Size": "42"}, "count": 0, "price_kobo": 5500000},
			map[string]any{"variant": map[string]any{"Size": "43"}, "count": 0},
		}}, "Idempotency-Key", "mv")["variants"].([]any)
	mineSz := map[string]string{}
	for _, x := range own {
		v := x.(map[string]any)
		mineSz[v["variant"].(map[string]any)["Size"].(string)] = v["id"].(string)
	}
	order := func(key string, lines ...map[string]any) (int, map[string]any) {
		in := make([]any, len(lines))
		for i, l := range lines {
			in[i] = l
		}
		return buyer.do("POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": in}, "Idempotency-Key", key)
	}
	sizesOf := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for name, it := range itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil)) {
			if it["variant_of"] == mine {
				out[name] = it
			}
		}
		return out
	}

	// Under the minimum: refused, and 44 is not added to the buyer's.
	if code, out := order("o1", map[string]any{"seller_item_id": sz["44"], "quantity": 2, "item_id": mine}); code != 400 || !strings.Contains(fmt.Sprint(out), "at least 6") {
		t.Fatalf("under the minimum: %d %v", code, out)
	}
	if got := sizesOf(); len(got) != 2 {
		t.Fatalf("a refused order added sizes: %v", got)
	}
	if ls := buyer.must(200, "GET", "/v1/item-links/"+link, nil)["item_links"].([]any); len(ls) != 0 {
		t.Fatalf("a refused order linked items: %v", ls)
	}

	// The supplier's 42 is not the buyer's 43.
	if code, out := order("o2", map[string]any{"seller_item_id": sz["42"], "quantity": 6, "item_id": mineSz["43"]}); code != 400 || !strings.Contains(fmt.Sprint(out), "is not their") {
		t.Fatalf("their 42 into our 43: %d %v", code, out)
	}

	// 6 of 44: added at 0; 42 keeps its own price, and it is still its own.
	if code, out := order("o3", map[string]any{"seller_item_id": sz["44"], "quantity": 6, "item_id": mine}); code != 201 {
		t.Fatalf("6 of 44: %d %v", code, out)
	}
	got := sizesOf()
	if len(got) != 3 || got["Air Max 90 – 44"] == nil || got["Air Max 90 – 44"]["stock"] != float64(0) {
		t.Fatalf("44 added at 0: %v", got)
	}
	if got["Air Max 90 – 42"]["price_kobo"] != float64(5500000) {
		t.Fatalf("42 lost its own price: %v", got["Air Max 90 – 42"])
	}
	buyer.must(200, "PATCH", "/v1/stock/items/"+mine, map[string]any{"price_kobo": 5200000})
	got = sizesOf()
	if got["Air Max 90 – 42"]["price_kobo"] != float64(5500000) || got["Air Max 90 – 43"]["price_kobo"] != float64(5200000) {
		t.Fatalf("the product's price reaches only those without their own: 42 %v, 43 %v", got["Air Max 90 – 42"]["price_kobo"], got["Air Max 90 – 43"]["price_kobo"])
	}

	// The supplier takes 43 off sale on its own: the list, and the buyer's
	// copy, follow.
	seller.must(200, "PATCH", "/v1/stock/items/"+sz["43"], map[string]any{"available": false})
	drainLinked(t, w.world)
	items := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)
	if n := len(items[0].(map[string]any)["sizes"].([]any)); n != 2 {
		t.Fatalf("43 off sale still listed: %d sizes", n)
	}
	// None on sale: the product is not ordered as an item of its own.
	seller.must(200, "PATCH", "/v1/stock/items/"+sz["42"], map[string]any{"available": false})
	seller.must(200, "PATCH", "/v1/stock/items/"+sz["44"], map[string]any{"available": false})
	drainLinked(t, w.world)
	items = buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)
	if it := items[0].(map[string]any); it["sizes"] != nil && len(it["sizes"].([]any)) > 0 || len(it["choices"].([]any)) == 0 {
		t.Fatalf("none on sale, still sold in sizes: %v", it)
	}
	if code, out := order("o4", map[string]any{"seller_item_id": am, "quantity": 6, "item_id": mine}); code != 400 || !strings.Contains(fmt.Sprint(out), "comes in sizes and colours") {
		t.Fatalf("the product itself ordered: %d %v", code, out)
	}
}
