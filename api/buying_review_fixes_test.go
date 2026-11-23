package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Review fixes to buying (what a business buys §7, §11 As built):
//   - a seller placing an order of more than 50 of an item the buyer
//     remembers keeping track of makes the order, its line recorded and
//     left for the buyer to confirm: not a refusal telling the seller how
//     the buyer treats the item;
//   - the buyer cannot accept a change that leaves nothing on the order.
func TestBuyingReviewFixes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const sellerOn, buyerOn = "+2348010000381", "+2348010000382"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	seller.must(200, "POST", "/v1/stock/setup", nil)
	chairs := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Plastic chair", "quantity": 500, "unit": "piece", "price_kobo": 500000},
		"Idempotency-Key", "ch")["item"].(map[string]any)["id"].(string)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Chairs Plus", "phone": "0803 123 4581"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	// The buyer keeps track of these chairs, one by one.
	buyer.must(200, "PUT", "/v1/item-links/"+link, map[string]any{"seller_item_id": chairs, "seller_item": "Plastic chair", "treatment": "track"})
	purchaseOf := func(ref string) map[string]any {
		t.Helper()
		for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
			if p := x.(map[string]any); p["reference"] == ref {
				return buyer.must(200, "GET", "/v1/stock/purchases/"+p["id"].(string), nil)["purchase"].(map[string]any)
			}
		}
		t.Fatalf("no purchase order %s", ref)
		return nil
	}

	// 100 chairs placed by the seller: made, recorded, to confirm.
	code, out := seller.do("POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item_id": chairs, "quantity": 100}}})
	raw, _ := json.Marshal(out)
	if code != 201 {
		t.Fatalf("placing 100 chairs: %d %s", code, raw)
	}
	for _, word := range []string{"Things we have", "track", "one row each"} {
		if strings.Contains(string(raw), word) {
			t.Errorf("the seller is told %q: %s", word, raw)
		}
	}
	ref := out["order"].(map[string]any)["reference"].(string)
	p := purchaseOf(ref)
	if l := p["lines"].([]any)[0].(map[string]any); l["treatment"] != "record" || l["unconfirmed"] != true || l["quantity"] != float64(100) {
		t.Fatalf("the buyer's line: %v", l)
	}
	// The buyer says: keep track of them, yes to 100.
	p = buyer.must(200, "POST", "/v1/stock/purchases/"+p["id"].(string)+"/confirm-proposed",
		map[string]any{"lines": []any{map[string]any{"treatment": "track", "track_many": true}}})["purchase"].(map[string]any)
	if l := p["lines"].([]any)[0].(map[string]any); l["treatment"] != "track" || l["unconfirmed"] == true || p["status"] != "confirmed" {
		t.Fatalf("confirmed: %v", p)
	}

	// The buyer's order of 2 chairs, changed by the seller to 1. A change
	// supplying nothing is the seller's decline, refused on its side; one
	// that reaches the buyer as nothing all the same (here written so) is
	// not accepted: the order would be confirmed with no lines.
	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item": "Plastic chair",
		"quantity": 2, "cost_kobo": 500000}}}, "Idempotency-Key", "two")["purchase"].(map[string]any)["id"].(string)
	ref = buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	seller.must(400, "POST", "/v1/trade-orders/"+o["id"].(string)+"/change", map[string]any{"lines": []any{map[string]any{"item": "Plastic chair", "quantity": 0,
		"price_kobo": 500000}}})
	seller.must(200, "POST", "/v1/trade-orders/"+o["id"].(string)+"/change", map[string]any{"lines": []any{map[string]any{"item": "Plastic chair", "quantity": 1,
		"price_kobo": 500000}}, "note": "One left"})
	if p := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any); p["seller_status"] != "changed" {
		t.Fatalf("the change mirrored: %v", p)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE purchase_orders SET change = jsonb_set(change, '{0,quantity}', '0') WHERE id = $1`, po); err != nil {
		t.Fatal(err)
	}
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/accept-change", nil)
	if p := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any); p["status"] != "sent" || len(p["lines"].([]any)) != 1 {
		t.Fatalf("nothing accepted: %v", p)
	}
}
