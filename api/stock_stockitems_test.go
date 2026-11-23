package api_test

import "testing"

// The Stock page's rows (what a business buys §4.5): stock items, each
// sold as its products or not sold (a material), with a filter.
func TestStockItemsAPI(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Malt House", "ada@malt.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	if out := owner.must(200, "GET", "/v1/stock/stock-items", nil); out["keeps_stock"] != false {
		t.Fatalf("before setup: %v", out)
	}
	owner.must(200, "POST", "/v1/stock/setup", nil)

	bottles := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Malt bottle", "quantity": 20, "unit": "bottle", "cost_kobo": 25000, "material": true},
		"Idempotency-Key", "b1")["item"].(map[string]any)["id"].(string)
	pack := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Malt, pack of 6", "quantity": 1, "price_kobo": 210000},
		"Idempotency-Key", "p1")["item"].(map[string]any)["id"].(string)
	single := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Malt", "quantity": 1, "price_kobo": 40000},
		"Idempotency-Key", "s1")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/stock/products/"+pack+"/counted-by", map[string]any{"stock_item_id": bottles, "uses": 6})
	owner.must(200, "PUT", "/v1/stock/products/"+single+"/counted-by", map[string]any{"stock_item_id": bottles, "uses": 1})

	out := viewer.must(200, "GET", "/v1/stock/stock-items?show=sold", nil)
	items := out["items"].([]any)
	if out["keeps_stock"] != true || len(items) != 1 {
		t.Fatalf("sold: %v", out)
	}
	b := items[0].(map[string]any)
	sold := b["sold_as"].([]any)
	if b["id"] != bottles || b["count"] != float64(20) || b["value_kobo"] != float64(500000) || len(sold) != 2 ||
		sold[0].(map[string]any)["product_id"] != single || sold[1].(map[string]any)["uses"] != float64(6) ||
		sold[1].(map[string]any)["price_kobo"] != float64(210000) || out["value_kobo"] != float64(500000) {
		t.Fatalf("the bottles, sold as two: %v", out)
	}
	// The pack's and the single's own stock items, from when they were
	// added, are materials now: not sold.
	for _, x := range owner.must(200, "GET", "/v1/stock/stock-items?show=not_sold", nil)["items"].([]any) {
		if x := x.(map[string]any); len(x["sold_as"].([]any)) != 0 || x["id"] == bottles {
			t.Fatalf("not sold: %v", x)
		}
	}
	if n := len(owner.must(200, "GET", "/v1/stock/stock-items", nil)["items"].([]any)); n != 3 {
		t.Fatalf("all: %d", n)
	}
	owner.must(400, "GET", "/v1/stock/stock-items?show=some", nil)
	owner.must(400, "GET", "/v1/stock/stock-items?retired=maybe", nil)
}
