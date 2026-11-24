package api_test

import (
	"context"
	"strings"
	"testing"
)

// Make one product (decision 0070, what-a-business-buys §4.1): the product
// made of three items keeps no stock of its own. It has no stock item;
// the three items keep theirs, with their counts and movements, grouped
// under it on the Stock page. Renaming it makes none; a size added later
// gets its stock item with a size's unit and levels.
func TestGroupMakesNoStockOfItsOwn(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	add := func(name string, qty int) map[string]any {
		return owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": name, "quantity": qty, "unit": "piece", "price_kobo": 2500000,
			"reorder_level": 1, "category": "Kaftans"}, "Idempotency-Key", "add-"+strings.ReplaceAll(name, " ", "-"))["item"].(map[string]any)
	}
	sBlue, mBlue, mRed := add("Kaftan S Blue", 4), add("Kaftan M Blue", 6), add("Kaftan M Red", 3)
	movesOf := func(sid any) int {
		var n int
		if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM stock_movements WHERE stock_item_id = $1`, sid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	movesBefore := map[string]int{}
	for _, it := range []map[string]any{sBlue, mBlue, mRed} {
		movesBefore[it["id"].(string)] = movesOf(it["stock_item_id"])
	}
	item := func(it map[string]any, size, colour string) map[string]any {
		return map[string]any{"item_id": it["id"], "variant": map[string]any{"Size": size, "Colour": colour}}
	}
	out := owner.must(200, "POST", "/v1/stock/group", map[string]any{"name": "Kaftan", "choices": []string{"Size", "Colour"},
		"items": []any{item(sBlue, "S", "Blue"), item(mBlue, "M", "Blue"), item(mRed, "M", "Red")}}, "Idempotency-Key", "g1")
	prod := out["item"].(map[string]any)
	pid := prod["id"].(string)
	if prod["stock_item_id"] != nil {
		t.Fatalf("the product has a stock item of its own: %v", prod)
	}
	if p := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["Kaftan"]; p == nil || p["stock_item_id"] != nil {
		t.Fatalf("the product as listed: %v", p)
	}

	rows := func() []map[string]any {
		var out []map[string]any
		for _, x := range owner.must(200, "GET", "/v1/stock/stock-items", nil)["items"].([]any) {
			out = append(out, x.(map[string]any))
		}
		return out
	}
	want := map[string]float64{sBlue["stock_item_id"].(string): 4, mBlue["stock_item_id"].(string): 6, mRed["stock_item_id"].(string): 3}
	got := rows()
	if len(got) != 3 {
		t.Fatalf("three stock items, none for the product: %v", got)
	}
	for _, r := range got {
		n, ok := want[r["id"].(string)]
		if !ok || r["count"] != n || r["group_of"] != pid || r["group_name"] != "Kaftan" {
			t.Fatalf("a size's stock item, its count kept, under the product: %v", r)
		}
	}
	for _, it := range []map[string]any{sBlue, mBlue, mRed} {
		if n := movesOf(it["stock_item_id"]); n != movesBefore[it["id"].(string)] {
			t.Fatalf("%s's movements: %d, were %d", it["name"], n, movesBefore[it["id"].(string)])
		}
	}

	// Renamed, it still has none.
	owner.must(200, "PATCH", "/v1/stock/items/"+pid, map[string]any{"name": "Ankara Kaftan"})
	if got := rows(); len(got) != 3 || got[0]["group_name"] != "Ankara Kaftan" {
		t.Fatalf("renamed: %v", got)
	}

	// A size added later: its stock item at 0, with a size's unit and level.
	cells := []any{
		map[string]any{"id": sBlue["id"], "variant": map[string]any{"Size": "S", "Colour": "Blue"}},
		map[string]any{"id": mBlue["id"], "variant": map[string]any{"Size": "M", "Colour": "Blue"}},
		map[string]any{"id": mRed["id"], "variant": map[string]any{"Size": "M", "Colour": "Red"}},
		map[string]any{"variant": map[string]any{"Size": "L", "Colour": "Red"}},
	}
	owner.must(200, "PUT", "/v1/stock/items/"+pid+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"S", "M", "L"}},
		map[string]any{"name": "Colour", "values": []string{"Blue", "Red"}},
	}, "cells": cells}, "Idempotency-Key", "v1")
	got = rows()
	if len(got) != 4 {
		t.Fatalf("four sizes' stock items, none for the product: %v", got)
	}
	for _, r := range got {
		if r["group_of"] != pid {
			t.Fatalf("under the product: %v", r)
		}
		if _, old := want[r["id"].(string)]; !old && (r["count"] != float64(0) || r["unit"] != "piece" || r["low_level"] != float64(1)) {
			t.Fatalf("the new size's stock item: %v", r)
		}
	}
}

// A product counted before it had sizes: once its count has moved into
// its sizes, its own stock item is retired, keeping its movements, so the
// Stock page and the low list no longer show it at 0; its sizes cleared,
// it is brought back.
func TestSizesRetireTheProductsOwnStock(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	prod := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 5, "unit": "pair", "price_kobo": 4500000,
		"reorder_level": 2}, "Idempotency-Key", "p")["item"].(map[string]any)
	id, sid := prod["id"].(string), prod["stock_item_id"].(string)
	state := func() (retired bool, moves int) {
		if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT i.retired_at IS NOT NULL, (SELECT count(*) FROM stock_movements m WHERE m.stock_item_id = i.id)
			FROM stock_items i WHERE i.id = $1`, sid).Scan(&retired, &moves); err != nil {
			t.Fatal(err)
		}
		return
	}
	owner.must(200, "PUT", "/v1/stock/items/"+id+"/variants", map[string]any{
		"choices": []any{map[string]any{"name": "Size", "values": []string{"40", "41"}}},
		"cells": []any{
			map[string]any{"variant": map[string]any{"Size": "40"}, "count": 2},
			map[string]any{"variant": map[string]any{"Size": "41"}, "count": 1},
		}}, "Idempotency-Key", "v1")
	if retired, moves := state(); !retired || moves < 3 {
		t.Fatalf("the product's own stock item: retired %v, %d movements", retired, moves)
	}
	got := owner.must(200, "GET", "/v1/stock/stock-items", nil)
	if items := got["items"].([]any); len(items) != 2 {
		t.Fatalf("its two sizes only: %v", items)
	}
	if got["low"] != float64(2) { // 40 at 2 and 41 at 1, at or under their level of 2; the product's own at 0 is not counted
		t.Fatalf("low: %v", got["low"])
	}
	if p := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["Air Max 90"]; p["stock"] != float64(0) {
		t.Fatalf("the product keeps no stock: %v", p)
	}

	// Its sizes cleared: it is counted by its own again.
	owner.must(200, "PUT", "/v1/stock/items/"+id+"/variants", map[string]any{"choices": []any{}, "cells": []any{}}, "Idempotency-Key", "v2")
	if retired, _ := state(); retired {
		t.Fatal("the product's own stock item is not brought back")
	}
}

// Make one product with no stock item of its own still knows what it is
// sold by and what it costs: its row on a price list reads the sizes'
// unit, and a size added later with a count comes in at a size's cost.
func TestGroupedProductUnitAndCost(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	add := func(name string, qty int) map[string]any {
		return owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": name, "quantity": qty, "unit": "piece", "price_kobo": 2500000,
			"cost_kobo": 1200000, "category": "Kaftans"}, "Idempotency-Key", "add-"+strings.ReplaceAll(name, " ", "-"))["item"].(map[string]any)
	}
	sBlue, mBlue, mRed := add("Kaftan S Blue", 4), add("Kaftan M Blue", 6), add("Kaftan M Red", 3)
	list := owner.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": sBlue["id"], "price_kobo": 2000000, "min_quantity": 1},
		map[string]any{"item_id": mBlue["id"], "price_kobo": 2000000, "min_quantity": 1},
		map[string]any{"item_id": mRed["id"], "price_kobo": 2000000, "min_quantity": 1},
	}})
	item := func(it map[string]any, size, colour string) map[string]any {
		return map[string]any{"item_id": it["id"], "variant": map[string]any{"Size": size, "Colour": colour}}
	}
	pid := owner.must(200, "POST", "/v1/stock/group", map[string]any{"name": "Kaftan", "choices": []string{"Size", "Colour"},
		"items": []any{item(sBlue, "S", "Blue"), item(mBlue, "M", "Blue"), item(mRed, "M", "Red")}}, "Idempotency-Key", "g1")["item"].(map[string]any)["id"].(string)
	got := owner.must(200, "GET", "/v1/price-lists", nil)["lists"].([]any)[0].(map[string]any)["items"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["item_id"] != pid || got[0].(map[string]any)["unit"] != "piece" {
		t.Fatalf("the product on the list, per piece: %v", got)
	}

	// L/Red added with 5: they came in at a size's cost.
	owner.must(200, "PUT", "/v1/stock/items/"+pid+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"S", "M", "L"}},
		map[string]any{"name": "Colour", "values": []string{"Blue", "Red"}},
	}, "cells": []any{
		map[string]any{"id": sBlue["id"], "variant": map[string]any{"Size": "S", "Colour": "Blue"}},
		map[string]any{"id": mBlue["id"], "variant": map[string]any{"Size": "M", "Colour": "Blue"}},
		map[string]any{"id": mRed["id"], "variant": map[string]any{"Size": "M", "Colour": "Red"}},
		map[string]any{"variant": map[string]any{"Size": "L", "Colour": "Red"}, "count": 5},
	}}, "Idempotency-Key", "v1")
	var delta int64
	var cost *int64
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT delta, cost_kobo FROM stock_movements
		WHERE item = 'Kaftan – L, Red' AND reason = 'received'`).Scan(&delta, &cost); err != nil {
		t.Fatal(err)
	}
	if delta != 5 || cost == nil || *cost != 1200000 {
		t.Fatalf("L/Red received %d at %v, want 5 at a size's cost", delta, cost)
	}
}
