package api_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Grouping existing items (phase 5): a shop selling a kaftan as three
// products ("Ankara Kaftan S Blue", "… M Blue", "… M Red") makes them one
// product with sizes and colours. Each keeps its row: its stock, its sales
// history, its price (the one most share becomes the product's; the dearer
// one keeps its own); its name is written out in full. A price list that
// held them one by one holds the product instead. Refusals in words.
func TestGroupStockItems(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	add := func(name string, qty, price int) string {
		return owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": name, "quantity": qty, "unit": "piece", "price_kobo": price,
			"category": "Kaftans"}, "Idempotency-Key", "add-"+strings.ReplaceAll(name, " ", "-"))["item"].(map[string]any)["id"].(string)
	}
	sBlue := add("Ankara Kaftan S Blue", 4, 2500000)
	mBlue := add("Ankara Kaftan M Blue", 6, 2500000)
	mRed := add("Ankara Kaftan M Red", 3, 2800000)
	rice := add("Rice", 9, 100000)
	owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": mBlue, "quantity": 1}}, "paid_how": "cash"},
		"Idempotency-Key", "sale1")
	list := owner.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": rice, "price_kobo": 90000, "min_quantity": 1},
		map[string]any{"item_id": sBlue, "price_kobo": 2000000, "min_quantity": 2},
		map[string]any{"item_id": mBlue, "price_kobo": 2000000, "min_quantity": 3},
	}})

	group := func(key string, body map[string]any) (int, map[string]any) {
		return owner.do("POST", "/v1/stock/group", body, "Idempotency-Key", key)
	}
	item := func(id, size, colour string) map[string]any {
		return map[string]any{"item_id": id, "variant": map[string]any{"Size": size, "Colour": colour}}
	}
	// Refused in words, nothing written.
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"name": "Ankara Kaftan", "items": []any{item(sBlue, "S", "Blue")}}, "at least two"},
		{map[string]any{"name": "", "items": []any{item(sBlue, "S", "Blue"), item(mBlue, "M", "Blue")}}, "give the product a name"},
		{map[string]any{"name": "Rice", "items": []any{item(sBlue, "S", "Blue"), item(mBlue, "M", "Blue")}}, "already the name"},
		{map[string]any{"name": "Ankara Kaftan", "items": []any{item(sBlue, "M", "Blue"), item(mBlue, "M", "Blue")}}, "would be the same size and colour"},
		{map[string]any{"name": "Ankara Kaftan", "items": []any{item(sBlue, "S", "Blue"), map[string]any{"item_id": mBlue, "variant": map[string]any{"Size": "M"}}}}, "same choices"},
		{map[string]any{"name": "Ankara Kaftan", "items": []any{item(sBlue, "S", "Blue"), item("nope", "M", "Blue")}}, "not one of your items"},
	} {
		if code, out := group("bad", c.body); code != 400 || !strings.Contains(fmt.Sprint(out), c.want) {
			t.Fatalf("%v: %d %v", c.body, code, out)
		}
	}
	if n := len(itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))); n != 4 {
		t.Fatalf("a refused grouping wrote something: %d items", n)
	}

	body := map[string]any{"name": "Ankara Kaftan", "choices": []string{"Size", "Colour"},
		"items": []any{item(sBlue, "S", "Blue"), item(mBlue, "M", "Blue"), item(mRed, "M", "Red")}}
	code, out := group("g1", body)
	if code != 200 {
		t.Fatalf("group: %d %v", code, out)
	}
	prod := out["item"].(map[string]any)
	if prod["name"] != "Ankara Kaftan" || prod["price_kobo"] != float64(2500000) || prod["category"] != "Kaftans" {
		t.Fatalf("the product: %v", prod)
	}
	// A retry answers the same, and makes nothing more.
	if code, again := group("g1", body); code != 200 || again["item"].(map[string]any)["id"] != prod["id"] {
		t.Fatalf("a retry: %d %v", code, again)
	}
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if len(items) != 5 {
		t.Fatalf("the product added, each item kept: %d items", len(items))
	}
	for name, want := range map[string][2]any{
		"Ankara Kaftan – S, Blue": {sBlue, float64(4)},
		"Ankara Kaftan – M, Blue": {mBlue, float64(5)},
		"Ankara Kaftan – M, Red":  {mRed, float64(3)},
	} {
		it := items[name]
		if it == nil || it["id"] != want[0] || it["stock"] != want[1] || it["variant_of"] != prod["id"] {
			t.Fatalf("%s kept its row and stock: %v", name, it)
		}
	}
	if items["Ankara Kaftan – M, Red"]["price_kobo"] != float64(2800000) {
		t.Fatalf("the dearer one keeps its own price: %v", items["Ankara Kaftan – M, Red"])
	}
	// Its history is its own still.
	if mv := owner.must(200, "GET", "/v1/stock/movements?item="+mBlue, nil)["movements"].([]any); len(mv) < 2 {
		t.Fatalf("the sale before grouping is still in its history: %v", mv)
	}
	// The product's price reaches the two that share it, not the dearer.
	owner.must(200, "PATCH", "/v1/stock/items/"+prod["id"].(string), map[string]any{"price_kobo": 2600000})
	items = itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Ankara Kaftan – S, Blue"]["price_kobo"] != float64(2600000) || items["Ankara Kaftan – M, Red"]["price_kobo"] != float64(2800000) {
		t.Fatalf("the product's new price: S %v, M Red %v", items["Ankara Kaftan – S, Blue"]["price_kobo"], items["Ankara Kaftan – M, Red"]["price_kobo"])
	}
	// The price list holds the product where the two sizes were: at their
	// price, the lower minimum; red, not on it before, is off it.
	got := owner.must(200, "GET", "/v1/price-lists", nil)["lists"].([]any)[0].(map[string]any)["items"].([]any)
	if len(got) != 2 {
		t.Fatalf("rice and the product: %v", got)
	}
	var kaftan map[string]any
	for _, x := range got {
		if x.(map[string]any)["item_id"] == prod["id"] {
			kaftan = x.(map[string]any)
		}
	}
	if kaftan == nil || kaftan["price_kobo"] != float64(2000000) || kaftan["min_quantity"] != float64(2) {
		t.Fatalf("the product on the list: %v", kaftan)
	}
	on := map[string]bool{}
	for _, z := range kaftan["sizes"].([]any) {
		on[z.(map[string]any)["item_id"].(string)] = z.(map[string]any)["available"].(bool)
	}
	if !on[sBlue] || !on[mBlue] || on[mRed] {
		t.Fatalf("the sizes the list held, and red off it: %v", on)
	}
	// Grouped items are not grouped again.
	if code, out := group("g2", map[string]any{"name": "Kaftan Two", "items": []any{item(sBlue, "S", "Blue"), item(rice, "M", "Blue")}}); code != 400 || !strings.Contains(fmt.Sprint(out), "already a size") {
		t.Fatalf("grouped again: %d %v", code, out)
	}
}

// A grouping that stopped part way (its product made, its items pointing
// at it, its sizes not yet saved) is finished by asking again, never
// refused for its own product; the product takes the first item's photo
// and shows only when one of the items was on sale; the diary says what
// was done.
func TestGroupFinishesWhatStopped(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	// A product in sizes first, so the catalogue has their columns.
	k := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Kaftan", "quantity": 0, "unit": "piece", "price_kobo": 100000},
		"Idempotency-Key", "k")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/stock/items/"+k+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{"S", "M"}}},
		"cells": []any{map[string]any{"variant": map[string]any{"Size": "S"}, "count": 1}, map[string]any{"variant": map[string]any{"Size": "M"}, "count": 1}}},
		"Idempotency-Key", "kv")
	sf := owner.must(200, "GET", "/v1/storefront", nil)
	table, cols := sf["catalogue_table"].(string), sf["catalogue_columns"].(map[string]any)
	add := func(name string) string {
		return owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": name, "quantity": 2, "unit": "piece", "price_kobo": 500000},
			"Idempotency-Key", "add-"+strings.ReplaceAll(name, " ", "-"))["item"].(map[string]any)["id"].(string)
	}
	red, blue := add("Apron Red"), add("Apron Blue")
	set := func(row string, cells map[string]any) {
		owner.must(200, "PATCH", "/v1/tables/"+table+"/rows/"+row, map[string]any{"set": cells}, "Idempotency-Key", "set-"+row+fmt.Sprint(len(cells)))
	}
	photo := cols["photo"].(string)
	set(red, map[string]any{photo: "media-red"})
	// Blue is not on sale; red is.
	set(blue, map[string]any{cols["available"].(string): false})

	// Stopped part way: the product made, the items pointing at it.
	apron := add("Apron")
	set(red, map[string]any{cols["variant_of"].(string): apron, cols["variant"].(string): `{"Colour":"Red"}`})
	set(blue, map[string]any{cols["variant_of"].(string): apron, cols["variant"].(string): `{"Colour":"Blue"}`})

	body := map[string]any{"name": "Apron", "items": []any{
		map[string]any{"item_id": red, "variant": map[string]any{"Colour": "Red"}},
		map[string]any{"item_id": blue, "variant": map[string]any{"Colour": "Blue"}}}}
	code, out := owner.do("POST", "/v1/stock/group", body, "Idempotency-Key", "again")
	if code != 200 || out["item"].(map[string]any)["id"] != apron {
		t.Fatalf("finished on its own product: %d %v", code, out)
	}
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	if items["Apron – Red"]["variant_of"] != apron || items["Apron – Blue"]["variant_of"] != apron || items["Apron"]["choices"] == nil {
		t.Fatalf("its sizes: %v", items)
	}
	if items["Apron"]["available"] != true {
		t.Fatalf("on sale, as red was: %v", items["Apron"])
	}
	var row map[string]any
	for _, r := range owner.must(200, "GET", "/v1/tables/"+table+"/rows?limit=100", nil)["rows"].([]any) {
		if fmt.Sprint(r.(map[string]any)["id"]) == apron {
			row = r.(map[string]any)
		}
	}
	if row == nil || row[photo] != "media-red" {
		t.Fatalf("the product's photo, red's: %v", row)
	}
	var diary []string
	for _, x := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if m := x.(map[string]any); m["kind"] == "diary" {
			diary = append(diary, m["text"].(string))
		}
	}
	if !slices.Contains(diary, "Made one product of 2 items: Apron") || slices.ContainsFunc(diary, func(l string) bool {
		return strings.HasPrefix(l, "Apron now comes in")
	}) {
		t.Fatalf("the diary: %q", diary)
	}
}
