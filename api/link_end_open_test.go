package api_test

import (
	"strings"
	"testing"
)

// Ending a link with an order still open between the two businesses says
// so in both sides' conversations, naming the order; the order itself is
// left as ending always left it.
func TestEndingALinkNamesWhatIsStillOpen(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4500"})["supplier"].(map[string]any)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup["id"].(string)+"/link", map[string]any{"link_id": link})
	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup["id"], "lines": []any{map[string]any{"item_id": rice, "quantity": 10}}},
		"Idempotency-Key", "po-1")["purchase"].(map[string]any)["id"].(string)
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)

	said := func(c *client, link string) string {
		t.Helper()
		var all []string
		for _, m := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if body, _ := m.(map[string]any)["body"].(string); body != "" {
				all = append(all, body)
			}
		}
		return strings.Join(all, "\n")
	}
	before := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any)
	seller.must(200, "POST", "/v1/links/"+sellerLink+"/end", nil)
	for name, got := range map[string]string{"the seller, who ended it": said(seller, sellerLink), "the buyer": said(buyer, link)} {
		if !strings.Contains(got, ref) || !strings.Contains(got, "still open") {
			t.Errorf("%s is not told that %s is still open: %q", name, ref, got)
		}
	}
	after := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any)
	if after["status"] != before["status"] || after["seller_status"] != before["seller_status"] {
		t.Errorf("ending changed the order: %v, was %v", after, before)
	}
}
