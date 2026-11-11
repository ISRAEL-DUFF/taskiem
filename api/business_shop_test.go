package api_test

import (
	"testing"
)

// A linked business that shares no price list offers its storefront
// (docs/spec/business-chat-tools.md §2, §7): the other side sees every
// product marked to show on it, at the storefront's prices, whether the
// storefront is published or not, and nothing hidden; an order from it is
// priced at the storefront's prices, for the seller to accept or change.
// Sharing a price list puts that list's prices in its place.
func TestStorefrontPricesForLinks(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)

	// The seller's products: rice shown on the storefront, beans not. The
	// storefront is never published.
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 50kg", "quantity": 40, "unit": "bag", "price_kobo": 8000000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sBeans := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Beans 25kg", "quantity": 10, "unit": "bag", "price_kobo": 3000000},
		"Idempotency-Key", "sb")["item"].(map[string]any)["id"].(string)
	seller.must(200, "PATCH", "/v1/stock/items/"+sBeans, map[string]any{"available": false})
	seller.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	w.srv.WaitBackground()
	drainLinked(t, w.world)

	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	got := buyer.must(200, "GET", "/v1/supplier-prices?link="+link, nil)["suppliers"].([]any)
	if len(got) != 1 {
		t.Fatalf("the buyer sees no storefront: %v", got)
	}
	shop := got[0].(map[string]any)
	if shop["name"] != "Storefront prices" {
		t.Fatalf("what the buyer's view is called: %v", shop["name"])
	}
	items := shop["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the buyer sees what is hidden, or not what is shown: %v", items)
	}
	rice := items[0].(map[string]any)
	if rice["item_id"] != sRice || rice["price_kobo"] != float64(8000000) || rice["available"] != true {
		t.Fatalf("the rice as the buyer sees it: %v", rice)
	}
	// "#" in their conversation names it too, theirs, at that price.
	tag := buyer.must(200, "GET", "/v1/link-chats/"+link+"/items", nil)
	if tag["whose"] != "theirs" || len(tag["items"].([]any)) != 1 {
		t.Fatalf("what the buyer can tag: %v", tag)
	}

	// An order from it is priced at the storefront's price.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	mine := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 50kg", "quantity": 1, "unit": "bag"},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4777"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	po := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order",
		map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 3, "item_id": mine}}}, "Idempotency-Key", "o1")["purchase"].(map[string]any)
	if po["total_kobo"] != float64(24000000) {
		t.Fatalf("drafted at the storefront's price: %v", po)
	}
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po["id"].(string)+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	var found map[string]any
	for _, x := range seller.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any) {
		if o := x.(map[string]any); o["order_reference"] == ref {
			found = o
		}
	}
	if found == nil {
		t.Fatalf("the seller has no order %s", ref)
	}
	if found["total_kobo"] != float64(24000000) {
		t.Fatalf("the seller's order, at the storefront's price: %v", found)
	}

	// A price list shared with the buyer takes the storefront's place.
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 7600000}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	drainLinked(t, w.world)
	shop = buyer.must(200, "GET", "/v1/supplier-prices?link="+link, nil)["suppliers"].([]any)[0].(map[string]any)
	if shop["name"] != "Trade prices" || shop["items"].([]any)[0].(map[string]any)["price_kobo"] != float64(7600000) {
		t.Fatalf("the shared list, in the storefront's place: %v", shop)
	}
	// Unshared again: the storefront again.
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{}})
	drainLinked(t, w.world)
	shop = buyer.must(200, "GET", "/v1/supplier-prices?link="+link, nil)["suppliers"].([]any)[0].(map[string]any)
	if shop["name"] != "Storefront prices" {
		t.Fatalf("unshared, the storefront again: %v", shop["name"])
	}
}
