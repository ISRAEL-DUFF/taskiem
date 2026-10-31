package api_test

import (
	"strings"
	"testing"
)

// Price lists and item links (L4, docs/spec/business-links.md §9): the
// seller's list, built on its own stock items, shared with a linked buyer
// and one item overridden for it; the buyer sees it, and orders from it
// at those prices, saying once which of its own items the seller's is; the
// order reaches the seller as that item, with no name matching; a price
// change does not touch an open order; "prices from" and "order … from"
// in chat; a list withdrawn is gone from the buyer.
func TestPriceLists(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000601"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50 kg", "quantity": 50, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sGarri := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Garri", "quantity": 20, "unit": "bag", "price_kobo": 1500000},
		"Idempotency-Key", "sg")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4602"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)

	// The seller's list.
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(400, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})
	seller.must(400, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": "not-mine", "price_kobo": 100}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": sRice, "price_kobo": 4200000, "min_quantity": 5},
		map[string]any{"item_id": sGarri, "price_kobo": 1400000, "available": false},
	}})
	if got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any); len(got) != 0 {
		t.Fatalf("not shared yet: %v", got)
	}
	shared := seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})["lists"].([]any)[0].(map[string]any)
	if ls := shared["links"].([]any); len(ls) != 1 || ls[0] != sellerLink {
		t.Fatalf("shared: %v", shared)
	}
	prices := func() map[string]map[string]any {
		t.Helper()
		got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)
		if len(got) != 1 {
			t.Fatalf("one supplier's prices: %v", got)
		}
		p := got[0].(map[string]any)
		if p["business"] != "Dangote Stores" || p["name"] != "Trade prices" || p["link"] != link {
			t.Fatalf("the prices: %v", p)
		}
		out := map[string]map[string]any{}
		for _, x := range p["items"].([]any) {
			out[x.(map[string]any)["item_id"].(string)] = x.(map[string]any)
		}
		return out
	}
	if p := prices(); p[sRice]["price_kobo"] != float64(4200000) || p[sRice]["min_quantity"] != float64(5) || p[sRice]["unit"] != "bag" || p[sGarri]["available"] != false {
		t.Fatalf("the buyer's view: %v", p)
	}
	// This buyer's own price for rice.
	lp := seller.must(200, "PUT", "/v1/link-prices/"+sellerLink, map[string]any{"prices": map[string]any{sRice: 4000000}})
	if it := lp["items"].([]any)[0].(map[string]any); it["price_kobo"] != float64(4000000) || it["overridden"] != true {
		t.Fatalf("overridden: %v", lp)
	}
	if p := prices(); p[sRice]["price_kobo"] != float64(4000000) {
		t.Fatalf("the buyer's own price: %v", p)
	}

	// Ordering from the list: the item said once, then linked.
	buyer.must(409, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 10}}},
		"Idempotency-Key", "o0")
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	code, out := buyer.do("POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 10}}},
		"Idempotency-Key", "o1")
	if code != 422 || out["code"] != "item_unlinked" || out["item"].(map[string]any)["item_id"] != sRice {
		t.Fatalf("which of yours: %d %v", code, out)
	}
	buyer.must(400, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 2, "item_id": rice}}},
		"Idempotency-Key", "o2")
	buyer.must(400, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sGarri, "quantity": 2, "item_id": rice}}},
		"Idempotency-Key", "o3")
	x := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 10, "item_id": rice}}},
		"Idempotency-Key", "o4")["purchase"].(map[string]any)
	if x["total_kobo"] != float64(40000000) || x["lines"].([]any)[0].(map[string]any)["item_id"] != rice {
		t.Fatalf("drafted at the list's price: %v", x)
	}
	if ls := buyer.must(200, "GET", "/v1/item-links/"+link, nil)["item_links"].([]any); len(ls) != 1 || ls[0].(map[string]any)["seller_item_id"] != sRice {
		t.Fatalf("linked: %v", ls)
	}
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+x["id"].(string)+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	if l := o["lines"].([]any)[0].(map[string]any); l["item_id"] != sRice || l["matched"] != "linked" || l["price_kobo"] != float64(4000000) {
		t.Fatalf("reached the seller as its own item: %v", l)
	}

	// A price change mirrors at once, and leaves the open order as it was.
	seller.must(200, "PUT", "/v1/link-prices/"+sellerLink, map[string]any{"prices": map[string]any{sRice: 4100000}})
	if p := prices(); p[sRice]["price_kobo"] != float64(4100000) {
		t.Fatalf("the new price: %v", p)
	}
	if got := buyer.must(200, "GET", "/v1/stock/purchases/"+x["id"].(string), nil)["purchase"].(map[string]any); got["total_kobo"] != float64(40000000) {
		t.Errorf("the open order kept its price: %v", got)
	}
	if o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any); o["total_kobo"] != float64(40000000) {
		t.Errorf("the seller's order kept its price: %v", o)
	}
	// Reordered with no item said: linked once.
	buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 5}}},
		"Idempotency-Key", "o5")

	// In chat.
	if r := w.say(t, buyerOn, "prices from dangote"); !strings.Contains(r, "Rice, 50 kg") || !strings.Contains(r, "₦41,000") || !strings.Contains(r, "(at least 5)") ||
		!strings.Contains(r, "not available now") {
		t.Fatalf("prices from: %q", r)
	}
	if r := w.say(t, buyerOn, "order 10 bags of rice from dangote"); !strings.Contains(r, "₦410,000") || !strings.Contains(r, "Send it to them?") {
		t.Fatalf("order from the list: %q", r)
	}
	if r := w.say(t, buyerOn, "yes"); !strings.Contains(r, "Dangote") {
		t.Fatalf("sent: %q", r)
	}
	if r := w.say(t, buyerOn, "order 2 bags of rice from dangote"); !strings.Contains(r, "order at least 5") {
		t.Fatalf("under the minimum: %q", r)
	}
	if r := w.say(t, buyerOn, "prices from nobody"); !strings.Contains(r, "shares no price list") {
		t.Fatalf("no such supplier: %q", r)
	}

	// Withdrawn: gone from the buyer, and nothing is ordered from it.
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{}})
	if got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any); len(got) != 0 {
		t.Fatalf("withdrawn: %v", got)
	}
	buyer.must(409, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 5}}},
		"Idempotency-Key", "o6")
	buyer.must(204, "DELETE", "/v1/item-links/"+link+"/"+rice, nil)
	// Shared again, then the list deleted: gone again.
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	prices()
	seller.must(200, "DELETE", "/v1/price-lists/"+list, nil)
	if got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any); len(got) != 0 {
		t.Fatalf("deleted: %v", got)
	}
	// Only the business's own: a buyer cannot read the seller's lists.
	if got := buyer.must(200, "GET", "/v1/price-lists", nil)["lists"].([]any); len(got) != 0 {
		t.Errorf("another business's lists: %v", got)
	}
}
