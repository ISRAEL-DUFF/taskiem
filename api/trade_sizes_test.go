package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// Sizes and colours in trade (phase 4): a supplier lists a trainer once
// with its 7 sizes (one dearer); a retailer orders by size, one line each,
// into a product of its own that gets the sizes it lacks; the quantity
// price counts the sizes together (50 pairs in mixed sizes earn the
// 50-pair price), so does the minimum; the order reaches the supplier as
// its own sizes, priced the same way; the product's lowest price is each
// size's; a product with other choices is refused in words.
func TestSizesInTrade(t *testing.T) {
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

	// The supplier's trainer in sizes 40 to 46, black.
	am := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "am")["item"].(map[string]any)["id"].(string)
	sizes := []string{"40", "41", "42", "43", "44", "45", "46"}
	var cells []any
	for _, z := range sizes {
		cells = append(cells, map[string]any{"variant": map[string]any{"Size": z, "Colour": "Black"}, "count": 20})
	}
	vs := seller.must(200, "PUT", "/v1/stock/items/"+am+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": sizes}, map[string]any{"name": "Colour", "values": []string{"Black"}}}, "cells": cells}, "Idempotency-Key", "v")["variants"].([]any)
	sz := map[string]string{}
	for _, x := range vs {
		v := x.(map[string]any)
		sz[v["variant"].(map[string]any)["Size"].(string)] = v["id"].(string)
	}
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": am, "price_kobo": 4000000, "min_quantity": 6,
		"breaks": []any{map[string]any{"min_quantity": 50, "price_kobo": 3600000}}, "sizes": []any{map[string]any{"item_id": sz["46"], "price_kobo": 4400000}}}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})

	// The buyer sees it once, with its sizes.
	items := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)
	if len(items) != 1 || len(items[0].(map[string]any)["sizes"].([]any)) != 7 {
		t.Fatalf("listed once with its 7 sizes: %v", items)
	}
	order := func(key string, qty map[string]int, extra map[string]any) (int, map[string]any) {
		var lines []any
		for _, z := range sizes {
			if n := qty[z]; n > 0 {
				l := map[string]any{"seller_item_id": sz[z], "quantity": n}
				for k, v := range extra {
					l[k] = v
				}
				lines = append(lines, l)
			}
		}
		return buyer.do("POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": lines}, "Idempotency-Key", key)
	}
	// No product of the buyer's yet: which of yours is it?
	if code, out := order("o1", map[string]int{"42": 6}, nil); code != 422 || out["item"].(map[string]any)["item_id"] != am {
		t.Fatalf("which of yours: %d %v", code, out)
	}
	// A product with stock and no sizes: its sizes set up first.
	stocked := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Trainers", "quantity": 4, "unit": "pair"}, "Idempotency-Key", "t")["item"].(map[string]any)["id"].(string)
	if code, out := order("o2", map[string]int{"42": 6}, map[string]any{"item_id": stocked}); code != 409 || out["code"] != "sizes_first" {
		t.Fatalf("sizes first: %d %v", code, out)
	}
	// A new product of the buyer's: 36 pairs in 7 sizes, under the minimum
	// for none alone but over it together; no quantity price yet.
	mine := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair"}, "Idempotency-Key", "m")["item"].(map[string]any)["id"].(string)
	code, out := order("o3", map[string]int{"40": 4, "41": 5, "42": 6, "43": 6, "44": 6, "45": 5, "46": 4}, map[string]any{"item_id": mine})
	if code != 201 {
		t.Fatalf("36 pairs in 7 sizes: %d %v", code, out)
	}
	x := out["purchase"].(map[string]any)
	if n := len(x["lines"].([]any)); n != 7 || x["total_kobo"] != float64(32*4000000+4*4400000) {
		t.Fatalf("a line a size, at the list's prices: %v", x)
	}
	got := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))
	if got["Air Max 90 – 42, Black"] == nil || got["Air Max 90 – 42, Black"]["variant_of"] != mine || got["Air Max 90 – 42, Black"]["stock"] != float64(0) {
		t.Fatalf("the buyer's product got the sizes: %v", got)
	}
	if ls := buyer.must(200, "GET", "/v1/item-links/"+link, nil)["item_links"].([]any); len(ls) != 8 {
		t.Fatalf("the product and each size linked: %v", ls)
	}
	// 50 pairs in mixed sizes: the 50-pair price, for the sizes at the
	// product's price; size 46 keeps its own.
	code, out = order("o4", map[string]int{"40": 10, "42": 20, "44": 10, "46": 10}, nil)
	if code != 201 {
		t.Fatalf("50 pairs: %d %v", code, out)
	}
	x = out["purchase"].(map[string]any)
	if x["total_kobo"] != float64(40*3600000+10*4400000) {
		t.Fatalf("the 50-pair price across sizes: %v", x["total_kobo"])
	}
	// Sent, it reaches the supplier as its own sizes at those prices.
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+x["id"].(string)+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	for _, l := range o["lines"].([]any) {
		line := l.(map[string]any)
		want := float64(3600000)
		if line["item_id"] == sz["46"] {
			want = 4400000
		}
		if line["matched"] != "linked" || line["price_kobo"] != want {
			t.Fatalf("reached the supplier as its sizes: %v", line)
		}
	}
	// The product's lowest price is each size's.
	seller.must(200, "PUT", "/v1/trade-floors", map[string]any{"item_id": am, "floor_kobo": 3700000})
	code, out = buyer.do("POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{
		map[string]any{"seller_item_id": sz["42"], "quantity": 10}}, "offer": true, "total_kobo": 10 * 3000000}, "Idempotency-Key", "o5")
	if code != 201 {
		t.Fatalf("an offer: %d %v", code, out)
	}
	offerRef := buyer.must(200, "POST", "/v1/stock/purchases/"+out["purchase"].(map[string]any)["id"].(string)+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	cashier := addMember(t, w.world, seller, "kemi", "cashier")
	if c, res := cashier.do("POST", "/v1/trade-orders/"+offerRef+"/accept", map[string]any{}); c == 200 || !strings.Contains(fmt.Sprint(res), "lowest price") {
		t.Fatalf("under the product's lowest price, staff may not accept: %d %v", c, res)
	}

	// A product with other choices (Size alone): refused in words.
	alone := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Runner", "quantity": 0, "unit": "pair"}, "Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/items/"+alone+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{"42"}}},
		"cells": []any{map[string]any{"variant": map[string]any{"Size": "42"}, "count": 0}}}, "Idempotency-Key", "rv")
	buyer.do("DELETE", "/v1/item-links/"+link+"/"+mine, nil)
	if code, out := order("o6", map[string]int{"42": 6}, map[string]any{"item_id": alone}); code != 409 || out["code"] != "sizes_differ" {
		t.Fatalf("other choices: %d %v", code, out)
	}
}
