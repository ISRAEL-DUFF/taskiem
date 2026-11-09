package api_test

import (
	"strings"
	"testing"
)

// Sizes and colours on WhatsApp (phase 5): "stock <product>" answers its
// sizes' counts in words, low ones marked; a product named alone is asked
// one choice at a time (a value said as a word as good as its number, a
// choice with one value left not asked); values said after the name pick
// the size at once ("add 5 ankara kaftan size s red"); a size's number is
// not taken for the sale's amount; a size's full name with its comma is
// one item; a size not on sale is said with what the product comes in.
// Then the buyer's side: a supplier's size ordered by its values, the
// buyer's product given the size it lacks.
func TestSizesOnWhatsApp(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const on = "+2348010000094"
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
		"cells": []any{cell("S", "Blue", 3), cell("M", "Blue", 0), cell("S", "Red", 2), cell("M", "Red", 4)}}, "Idempotency-Key", "kv")
	shoe := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "s")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/stock/items/"+shoe+"/variants", map[string]any{"choices": []any{map[string]any{"name": "Size", "values": []string{"42", "43"}}},
		"cells": []any{map[string]any{"variant": map[string]any{"Size": "42"}, "count": 5}, map[string]any{"variant": map[string]any{"Size": "43"}, "count": 5}}},
		"Idempotency-Key", "sv")
	stockOf := func(name string) any {
		return itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))[name]["stock"]
	}

	// Its sizes' counts in words.
	if r := w.say(t, on, "stock ankara kaftan"); !strings.Contains(r, "Ankara Kaftan: 4 sizes and colours, 9 pieces in all") ||
		!strings.Contains(r, "Blue: S × 3, M × 0") || !strings.Contains(r, "Red: S × 2, M × 4") {
		t.Fatalf("stock of a product in sizes: %q", r)
	}

	// One choice at a time: the size, then the colour; then the sale.
	if r := w.say(t, on, "sold 1 ankara kaftan cash"); !strings.Contains(r, "Which size of Ankara Kaftan?") || !strings.Contains(r, "1. S") || !strings.Contains(r, "2. M") {
		t.Fatalf("the size asked: %s", r)
	}
	if r := w.say(t, on, "large"); !strings.Contains(r, "Reply with the number of the size") {
		t.Fatalf("a value it does not have: %s", r)
	}
	if r := w.say(t, on, "m"); !strings.Contains(r, "Which colour of Ankara Kaftan?") || !strings.Contains(r, "2. Red") {
		t.Fatalf("the colour asked: %s", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "Ankara Kaftan – M, Red") || !strings.Contains(r, "₦25,000") {
		t.Fatalf("the sale, its size said: %s", r)
	}
	w.say(t, on, "yes")
	if got := stockOf("Ankara Kaftan – M, Red"); got != float64(3) {
		t.Fatalf("M Red sold: %v", got)
	}

	// The colour said: only the size is asked.
	if r := w.say(t, on, "sold 1 ankara kaftan blue"); !strings.Contains(r, "Which size of Ankara Kaftan?") || strings.Contains(r, "colour") {
		t.Fatalf("the size alone asked: %s", r)
	}
	w.say(t, on, "cancel")
	// Both said: at once.
	if r := w.say(t, on, "add 5 ankara kaftan size s red"); !strings.Contains(r, "Ankara Kaftan – S, Red") {
		t.Fatalf("stock in for a size named by its values: %s", r)
	}
	w.say(t, on, "yes")
	if got := stockOf("Ankara Kaftan – S, Red"); got != float64(7) {
		t.Fatalf("S Red stock in: %v", got)
	}
	// A size not on sale: what it comes in.
	if r := w.say(t, on, "stock ankara kaftan xl blue"); !strings.Contains(r, "Ankara Kaftan has none on sale") || !strings.Contains(r, "S, M") {
		t.Fatalf("a size not sold: %s", r)
	}

	// A size's number is its size, not the amount.
	if r := w.say(t, on, "sold 1 air max 90 42"); !strings.Contains(r, "Air Max 90 – 42") || !strings.Contains(r, "₦45,000") {
		t.Fatalf("42 read as the amount: %s", r)
	}
	w.say(t, on, "cancel")
	// A size's full name, comma and all, is one item.
	if r := w.say(t, on, "sold 1 Ankara Kaftan – S, Blue"); !strings.Contains(r, "Ankara Kaftan – S, Blue") || !strings.Contains(r, "₦25,000") {
		t.Fatalf("the full name with its comma: %s", r)
	}
	w.say(t, on, "cancel")
}

// A buyer orders a supplier's size on WhatsApp by its values; the
// product alone is asked for by name, with the chat on Taskiem for many.
func TestSizesOrderedOnWhatsApp(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const on = "+2348010000095"
	w.bind(t, buyer, on)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4693"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	am := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "am")["item"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/stock/items/"+am+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"42", "43"}}, map[string]any{"name": "Colour", "values": []string{"Black"}}},
		"cells": []any{
			map[string]any{"variant": map[string]any{"Size": "42", "Colour": "Black"}, "count": 20},
			map[string]any{"variant": map[string]any{"Size": "43", "Colour": "Black"}, "count": 20},
		}}, "Idempotency-Key", "v")
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": am, "price_kobo": 4000000, "min_quantity": 1}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair"}, "Idempotency-Key", "m")

	// The product alone: which size, by name, or many in the chat.
	if r := w.say(t, on, "order 6 air max 90 from dangote"); !strings.Contains(r, "Air Max 90 from Dangote Stores comes in sizes and colours: 42, 43 · Black") ||
		!strings.Contains(r, "order 6 Air Max 90 – 42, Black from Dangote Stores") {
		t.Fatalf("which size, to a buyer: %s", r)
	}
	// By its values: drafted, the buyer's product given the size.
	if r := w.say(t, on, "order 6 air max 90 43 black from dangote"); !strings.Contains(r, "Air Max 90 – 43, Black") || !strings.Contains(r, "₦240,000") {
		t.Fatalf("a size ordered by its values: %s", r)
	}
	got := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))
	if got["Air Max 90 – 43, Black"] == nil {
		t.Fatalf("the buyer's product got the size: %v", got)
	}
}
