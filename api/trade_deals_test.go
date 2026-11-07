package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Wholesale prices and price deliberation (chat tools phase 7,
// docs/spec/chat-tools.md §9): a quantity break; the buyer's offer; a
// cashier's accept below the floor waits for the owner, a counter below
// it is refused, one within it goes; the buyer counters back from
// WhatsApp ("counter 760k"); the owner accepts and keeps the price for the
// buyer; the order follows at the agreed prices, each step a card and the
// deal a diary line; an offer that lapsed is not accepted.
func TestTradeDeal(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000931"
	w.bind(t, buyer, buyerOn)
	for _, c := range []*client{seller, buyer} {
		c.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
		c.must(200, "POST", "/v1/stock/setup", nil)
	}
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 25kg bag", "quantity": 400, "unit": "bag", "price_kobo": 4100000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
	// A quantity break that goes up in price is refused.
	seller.must(400, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 4100000, "min_quantity": 5,
		"breaks": []any{map[string]any{"min_quantity": 10, "price_kobo": 4200000}}}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 4100000, "min_quantity": 5,
		"breaks": []any{map[string]any{"min_quantity": 10, "price_kobo": 3950000}}}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 25kg", "quantity": 2, "unit": "bag"}, "Idempotency-Key", "br")["item"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Bayo Rice Mills", "phone": "0803 123 4777"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})

	// The buyer sees the break; an order of 20 is at its price.
	items := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)
	if b, _ := items[0].(map[string]any)["breaks"].([]any); len(b) != 1 {
		t.Fatalf("the break, as the buyer sees it: %v", items)
	}
	plain := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 20, "item_id": rice}}},
		"Idempotency-Key", "o0")["purchase"].(map[string]any)
	if l := plain["lines"].([]any)[0].(map[string]any); l["cost_kobo"] != float64(3950000) {
		t.Fatalf("20 bags at the break's price: %v", l)
	}
	buyer.must(200, "POST", "/v1/stock/purchases/"+plain["id"].(string)+"/cancel", nil)

	// The offer: ₦740,000 for 20 (₦37,000 a bag, against ₦39,500).
	offer := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 20, "item_id": rice}},
		"offer": true, "total_kobo": 74000000, "note": "Regular buyer"}, "Idempotency-Key", "o1")["purchase"].(map[string]any)
	po := offer["id"].(string)
	if offer["offer"] != true || offer["total_kobo"] != float64(74000000) {
		t.Fatalf("the offer drafted: %v", offer)
	}
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	order := func() map[string]any {
		t.Helper()
		for _, x := range seller.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any) {
			if o := x.(map[string]any); o["order_reference"] == ref || o["reference"] == ref {
				return seller.must(200, "GET", "/v1/trade-orders/"+o["id"].(string), nil)["order"].(map[string]any)
			}
		}
		t.Fatalf("no order %s", ref)
		return nil
	}
	o := order()
	id := o["id"].(string)
	l := o["lines"].([]any)[0].(map[string]any)
	if o["status"] != "received" || l["priced_by"] != "offer" || l["price_kobo"] != float64(3700000) || l["list_kobo"] != float64(3950000) ||
		len(o["deal_history"].([]any)) != 1 {
		t.Fatalf("the offer at the seller: %v", o)
	}
	// The seller's alert says what it is against.
	var seen bool
	for _, x := range seller.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		a := x.(map[string]any)
		seen = seen || a["kind"] == "new_order" && strings.Contains(a["title"].(string), "offers ₦740,000") && a["line"] == "was ₦790,000"
	}
	if !seen {
		t.Fatal("no offer card for the seller")
	}

	// A floor of ₦38,000 a bag. The cashier's accept waits for the owner.
	seller.must(200, "PUT", "/v1/trade-floors", map[string]any{"item_id": sRice, "floor_kobo": 3800000})
	cashier := addMember(t, w.world, seller, "tunde", "cashier")
	waiting := cashier.must(202, "POST", "/v1/trade-orders/"+id+"/accept", nil)
	if why := waiting["why"].([]any); len(why) != 1 || !strings.Contains(why[0].(string), "under its lowest price, ₦38,000") {
		t.Fatalf("waiting for the owner: %v", waiting)
	}
	if o := order(); o["status"] != "received" || o["deal_owner_at"] == nil {
		t.Fatalf("not accepted, the owner asked: %v", o)
	}
	var asked bool
	for _, x := range seller.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		a := x.(map[string]any)
		asked = asked || a["kind"] == "deal" && strings.Contains(a["title"].(string), "would agree ₦740,000")
	}
	if !asked {
		t.Fatal("no Needs your OK card for the owner")
	}
	cashier.must(403, "GET", "/v1/trade-floors", nil)
	cashier.must(403, "POST", "/v1/trade-orders/"+id+"/accept", map[string]any{"keep_days": 7})

	// A counter below the floor is refused; at ₦38,500 it goes to the buyer.
	cashier.must(403, "POST", "/v1/trade-orders/"+id+"/change", map[string]any{"lines": []any{map[string]any{"item": "x", "quantity": 20, "price_kobo": 3750000}}})
	cashier.must(400, "POST", "/v1/trade-orders/"+id+"/accept", map[string]any{"total_kobo": 77000000})
	cashier.must(200, "POST", "/v1/trade-orders/"+id+"/change", map[string]any{"total_kobo": 77000000, "note": "Best I can do"})
	got := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any)
	if got["seller_status"] != "changed" || len(got["deal_history"].([]any)) != 2 || got["deal_until"] == nil {
		t.Fatalf("the seller's counter at the buyer: %v", got)
	}

	// The buyer answers from WhatsApp: ₦760,000.
	// (The seller's counter reaches the phone first, inside the window.)
	if got := w.send(t, buyerOn, whatsapptest.Text("counter 760k")); !strings.Contains(got[len(got)-1].Text, "Sent: ₦760,000 for "+ref) {
		t.Fatalf("counter from WhatsApp: %+v", got)
	}
	o = order()
	h := o["deal_history"].([]any)
	if o["status"] != "received" || len(h) != 3 || h[2].(map[string]any)["by"] != "buyer" || h[2].(map[string]any)["total_kobo"] != float64(76000000) ||
		o["lines"].([]any)[0].(map[string]any)["price_kobo"] != float64(3800000) {
		t.Fatalf("the buyer's counter at the seller: %v", o)
	}
	// At the floor now: the owner accepts and keeps the price 7 days.
	seller.must(200, "POST", "/v1/trade-orders/"+id+"/accept", map[string]any{"keep_days": 7})
	if d := diaryOf(t, seller); !hasLine(d, "Agreed ₦760,000 with Ada's Kitchen on "+ref) {
		t.Fatalf("the deal's diary line: %q", d)
	}
	got = buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any)
	if got["status"] != "confirmed" || got["lines"].([]any)[0].(map[string]any)["cost_kobo"] != float64(3800000) {
		t.Fatalf("the order at the agreed price: %v", got)
	}
	drainLinked(t, w.world)
	items = buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)
	if it := items[0].(map[string]any); it["price_kobo"] != float64(3800000) || it["breaks"] != nil {
		t.Fatalf("the kept price, as the buyer sees it: %v", it)
	}

	// An offer that lapsed is not accepted.
	late := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 10, "item_id": rice,
		"price_kobo": 3900000}}, "offer": true}, "Idempotency-Key", "o2")["purchase"].(map[string]any)
	ref = buyer.must(200, "POST", "/v1/stock/purchases/"+late["id"].(string)+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	lid := order()["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_orders SET deal_until = now() - interval '1 minute' WHERE order_id = $1`, lid); err != nil {
		t.Fatal(err)
	}
	seller.must(409, "POST", "/v1/trade-orders/"+lid+"/accept", nil)
}
