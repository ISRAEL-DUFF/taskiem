package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
)

// Phase 5's review fixes in the chat apps' words. A size or colour the
// product does not have is said, never taken for the one size the other
// words leave ("xl red" of a kaftan whose only red is M), in a sale, stock
// in, "stock", a count and the catalogue's words. Sizes that are numbers
// (UK 3 to 12) are asked by value, so "8" is size 8, not the 8th on a list.
// A count, a price and a delivery ask the size one choice at a time like a
// sale; a supplier named for a product supplies all its sizes; goods
// received in a size are received against its supplier's open order.
func TestSizesFixesOnWhatsApp(t *testing.T) {
	w, owner, on := kaftanShop(t, "+2348010000096")
	items := func() map[string]map[string]any { return itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)) }
	before := len(items())

	// A size it does not have: said, nothing sold, added or shown.
	for _, said := range []string{"sold 1 ankara kaftan xl red cash", "add 5 ankara kaftan xl red", "stock ankara kaftan xl red", "count ankara kaftan xl red 4"} {
		if r := w.say(t, on, said); !strings.Contains(r, "Ankara Kaftan has none on sale in xl Red") || !strings.Contains(r, "S, M · Blue, Red") {
			t.Fatalf("%q: %s", said, r)
		}
		w.say(t, on, "cancel")
	}
	if n := len(items()); n != before {
		t.Fatalf("an item was added for a size not sold: %d, was %d", n, before)
	}
	if got := items()["Ankara Kaftan – M, Red"]["stock"]; got != float64(4) {
		t.Fatalf("M Red was touched: %v", got)
	}

	// Sizes that are numbers: asked by value; "8" is size 8.
	if r := w.say(t, on, "sold 1 loafer cash"); !strings.Contains(r, "Which size of Loafer? Say it: 3, 4, 5") || strings.Contains(r, "1. 3") {
		t.Fatalf("number sizes asked by value: %s", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "Say the size: 3, 4") {
		t.Fatalf("2 is not one of its sizes: %s", r)
	}
	if r := w.say(t, on, "8"); !strings.Contains(r, "Loafer – 8") || strings.Contains(r, "Loafer – 10") {
		t.Fatalf("8 is size 8: %s", r)
	}
	w.say(t, on, "cancel")

	// A count asks the size, then goes on.
	if r := w.say(t, on, "count ankara kaftan 7"); !strings.Contains(r, "Which size of Ankara Kaftan?") {
		t.Fatalf("a count's size asked: %s", r)
	}
	w.say(t, on, "m")
	if r := w.say(t, on, "red"); !strings.Contains(r, "Ankara Kaftan – M, Red") {
		t.Fatalf("the count of M Red: %s", r)
	}
	w.say(t, on, "yes")
	if got := items()["Ankara Kaftan – M, Red"]["stock"]; got != float64(7) {
		t.Fatalf("M Red counted: %v", got)
	}

	// The catalogue's words: a size by its values in any order; the
	// product itself when only it is named.
	if r := w.say(t, on, "price ankara kaftan red m 30000"); !strings.Contains(r, "Ankara Kaftan – M, Red") || !strings.Contains(r, "30,000") {
		t.Fatalf("a size's price by its values: %s", r)
	}
	w.say(t, on, "yes")
	if got := items()["Ankara Kaftan – M, Red"]["price_kobo"]; got != float64(3000000) {
		t.Fatalf("M Red's own price: %v", got)
	}
	if r := w.say(t, on, "price ankara kaftan 26000"); strings.Contains(r, "Which size") || !strings.Contains(r, "Ankara Kaftan") {
		t.Fatalf("the product's own price: %s", r)
	}
	w.say(t, on, "cancel")

}

// A supplier named for a product sold in sizes supplies all of them; goods
// received in one of its sizes, asked, are received against its supplier's
// open order.
func TestSizesSuppliedOnWhatsApp(t *testing.T) {
	w, owner, on := kaftanShop(t, "+2348010000098")
	items := func() map[string]map[string]any { return itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)) }
	// A supplier for the product: all its sizes.
	if r := w.say(t, on, "add supplier Kano Textiles 0803 111 2222 for ankara kaftan"); !strings.Contains(r, "Ankara Kaftan") || strings.Contains(r, "Say which") {
		t.Fatalf("a supplier for a product in sizes: %s", r)
	}
	w.say(t, on, "yes")
	got := items()
	for _, n := range []string{"Ankara Kaftan", "Ankara Kaftan – S, Blue", "Ankara Kaftan – M, Blue", "Ankara Kaftan – M, Red"} {
		if got[n]["supplier"] != "Kano Textiles" {
			t.Fatalf("%s's supplier: %v", n, got[n]["supplier"])
		}
	}

	// Goods received in a size: its size asked, then received against
	// the supplier's open order for it.
	var sup string
	for _, x := range owner.must(200, "GET", "/v1/stock/suppliers", nil)["suppliers"].([]any) {
		if x.(map[string]any)["name"] == "Kano Textiles" {
			sup = x.(map[string]any)["id"].(string)
		}
	}
	mRed := got["Ankara Kaftan – M, Red"]["id"].(string)
	po := owner.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": mRed, "quantity": 6}}},
		"Idempotency-Key", "po")["purchase"].(map[string]any)["id"].(string)
	ref := owner.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	if r := w.say(t, on, "received 6 ankara kaftan"); !strings.Contains(r, "Which size of Ankara Kaftan?") {
		t.Fatalf("a delivery's size asked: %s", r)
	}
	w.say(t, on, "m")
	if r := w.say(t, on, "red"); !strings.Contains(r, "on order "+ref) {
		t.Fatalf("received against the open order: %s", r)
	}
}

// A buyer orders a size the supplier does not sell: said, with what it
// comes in, and nothing ordered or added.
func TestSizesNotSoldToABuyer(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const on = "+2348010000097"
	w.bind(t, buyer, on)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4694"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	am := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "am")["item"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/stock/items/"+am+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"42", "43"}}, map[string]any{"name": "Colour", "values": []string{"Black", "White"}}},
		"cells": []any{
			map[string]any{"variant": map[string]any{"Size": "42", "Colour": "White"}, "count": 20},
			map[string]any{"variant": map[string]any{"Size": "43", "Colour": "Black"}, "count": 20},
		}}, "Idempotency-Key", "v")
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": am, "price_kobo": 4000000, "min_quantity": 1}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair"}, "Idempotency-Key", "m")

	// 45 is no size of it; only 43 is black: never 43 for "45 black".
	if r := w.say(t, on, "order 6 air max 90 45 black from dangote"); !strings.Contains(r, "Air Max 90 from Dangote Stores doesn't come in 45 Black") ||
		!strings.Contains(r, "42, 43 · Black, White") {
		t.Fatalf("a size not sold, to a buyer: %s", r)
	}
	got := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))
	if len(got) != 1 {
		t.Fatalf("a size was added to the buyer's product: %v", got)
	}
}

// kaftanShop is Zuri Atelier on WhatsApp with a kaftan in S Blue, M Blue
// and M Red (its only red is M), and a loafer in UK sizes 3 to 12.
func kaftanShop(t *testing.T, on string) (*waTablesWorld, *client, string) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	kaftan := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Ankara Kaftan", "quantity": 0, "unit": "piece", "price_kobo": 2500000},
		"Idempotency-Key", "k")["item"].(map[string]any)["id"].(string)
	cell := func(size, colour string, n int) map[string]any {
		return map[string]any{"variant": map[string]any{"Size": size, "Colour": colour}, "count": n}
	}
	owner.must(200, "PUT", "/v1/stock/items/"+kaftan+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"S", "M"}}, map[string]any{"name": "Colour", "values": []string{"Blue", "Red"}}},
		"cells": []any{cell("S", "Blue", 3), cell("M", "Blue", 1), cell("M", "Red", 4)}}, "Idempotency-Key", "kv")
	loafer := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Loafer", "quantity": 0, "unit": "pair", "price_kobo": 1000000},
		"Idempotency-Key", "l")["item"].(map[string]any)["id"].(string)
	uk := []string{"3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}
	var cells []any
	for _, v := range uk {
		cells = append(cells, map[string]any{"variant": map[string]any{"Size": v}, "count": 2})
	}
	owner.must(200, "PUT", "/v1/stock/items/"+loafer+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": uk}}, "cells": cells},
		"Idempotency-Key", "lv")
	return w, owner, on
}

// The same words on Telegram: a size asked one choice at a time, then sold.
func TestSizesOnTelegram(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const acct = 70082
	w.link(t, owner, acct, "zuri")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	k := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Ankara Kaftan", "quantity": 0, "unit": "piece", "price_kobo": 2500000},
		"Idempotency-Key", "k")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/stock/items/"+k+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{"S", "M"}}},
		"cells": []any{map[string]any{"variant": map[string]any{"Size": "S"}, "count": 3}, map[string]any{"variant": map[string]any{"Size": "M"}, "count": 3}}},
		"Idempotency-Key", "kv")
	if r := w.say(t, acct, "sold 1 ankara kaftan cash"); !strings.Contains(r.Text, "Which size of Ankara Kaftan?") {
		t.Fatalf("telegram, the size asked: %+v", r)
	}
	if r := w.say(t, acct, "m"); !strings.Contains(r.Text, "Ankara Kaftan – M") || !strings.Contains(r.Text, "₦25,000") {
		t.Fatalf("telegram, the sale: %+v", r)
	}
	w.say(t, acct, "yes")
	if got := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["Ankara Kaftan – M"]["stock"]; got != float64(2) {
		t.Fatalf("telegram, M sold: %v", got)
	}
	if r := w.say(t, acct, "stock ankara kaftan xl"); !strings.Contains(r.Text, "Ankara Kaftan has none on sale in xl") {
		t.Fatalf("telegram, a size not sold: %+v", r)
	}
}
