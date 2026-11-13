package api_test

import (
	"testing"

	"github.com/google/uuid"
)

// A linked business made a supplier from the link alone (13 November 2026,
// the founder): ordering from a linked business that was not yet a
// supplier asked the buyer for the seller's phone number. Now the name and
// the link make the supplier: no number, its orders through the link, no
// SMS; it is that business once only, and it cannot be unlinked (it would
// have no way to be reached).
func TestLinkedSupplierWithoutPhone(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, buyerID := linkedPair(t, w.waWorld)
	const sellerOn, buyerOn = "+2348010000461", "+2348010000462"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")

	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)

	// Not a link of theirs: refused.
	buyer.must(400, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "link_id": uuid.NewString()})
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "link_id": link})["supplier"].(map[string]any)
	if sup["phone"] != "" || sup["reached"] != "link" || sup["link_id"] != link {
		t.Fatalf("the supplier: %v", sup)
	}
	// That business once only.
	buyer.must(409, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote 2", "link_id": link})
	// Listed with the rest.
	if list := buyer.must(200, "GET", "/v1/stock/suppliers", nil)["suppliers"].([]any); len(list) != 1 || list[0].(map[string]any)["name"] != "Dangote Stores" {
		t.Errorf("the suppliers: %v", list)
	}
	// No number to fall back on: it stays linked.
	buyer.must(400, "PUT", "/v1/stock/suppliers/"+sup["id"].(string)+"/link", map[string]any{"link_id": nil})

	// An order to it goes through the link, with no SMS.
	tenant := uuid.MustParse(buyerID)
	sms := smsCount(t, w.world, tenant)
	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup["id"], "lines": []any{map[string]any{"item_id": rice, "quantity": 2}}},
		"Idempotency-Key", "po")["purchase"].(map[string]any)["id"].(string)
	sent := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)
	if sent["status"] != "sent" || sent["link_id"] != link || smsCount(t, w.world, tenant) != sms {
		t.Fatalf("sent: %v", sent)
	}
	drainLinked(t, w.world)
	if got := seller.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any); len(got) != 1 {
		t.Errorf("the seller's orders: %v", got)
	}
}
