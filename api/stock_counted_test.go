package api_test

import "testing"

// Counted by (what a business buys §4.1): a product is counted by a stock
// item with how many of it one sale uses, or by none. A pack of 6 counted
// by a material of bottles: selling 2 packs lowers the bottles by 12, and
// the Stock page reads its uses. Only a product of the catalogue is
// counted by something; a viewer may not change it.
func TestCountedByAPI(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Malt House", "ada@malt.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	owner.must(200, "POST", "/v1/stock/setup", nil)

	bottles := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Malt bottle", "quantity": 20, "unit": "bottle", "cost_kobo": 25000, "material": true},
		"Idempotency-Key", "b1")["item"].(map[string]any)["id"].(string)
	pack := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Malt, pack of 6", "quantity": 1, "price_kobo": 210000},
		"Idempotency-Key", "p1")["item"].(map[string]any)["id"].(string)

	path := "/v1/stock/products/" + pack + "/counted-by"
	viewer.must(403, "PUT", path, map[string]any{"stock_item_id": bottles, "uses": 6})
	owner.must(400, "PUT", path, map[string]any{"stock_item_id": bottles, "uses": 0})
	owner.must(400, "PUT", path, map[string]any{"stock_item_id": bottles, "uses": 1.5})
	owner.must(400, "PUT", path, map[string]any{"uses": 6})
	owner.must(404, "PUT", path, map[string]any{"stock_item_id": "01900000-0000-7000-8000-000000000000", "uses": 6})
	owner.must(400, "PUT", "/v1/stock/products/"+bottles+"/counted-by", map[string]any{"stock_item_id": bottles, "uses": 6})

	it := owner.must(200, "PUT", path, map[string]any{"stock_item_id": bottles, "uses": 6})["item"].(map[string]any)
	if it["id"] != pack || it["stock_item_id"] != bottles || it["uses"] != float64(6) || it["stock"] != float64(20) {
		t.Fatalf("counted by the bottles: %v", it)
	}
	// Two packs sold: 12 bottles.
	owner.must(201, "POST", "/v1/stock/sales", map[string]any{"paid_how": "cash", "lines": []any{map[string]any{"item_id": pack, "quantity": 2}}},
		"Idempotency-Key", "s1")
	// The pack's own stock item, from when it was added, is a material now.
	var b map[string]any
	for _, x := range owner.must(200, "GET", "/v1/stock", nil)["items"].([]any) {
		if x.(map[string]any)["id"] == pack {
			b = x.(map[string]any)
		}
	}
	if b["stock"] != float64(8) || b["uses"] != float64(6) {
		t.Fatalf("after 2 packs: %v", b)
	}

	// Counted by none: its stock item stays, a material again.
	it = owner.must(200, "DELETE", path, nil)["item"].(map[string]any)
	if it["stock"] != nil || it["stock_item_id"] != nil {
		t.Fatalf("counted by none: %v", it)
	}
	if b := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["Malt bottle"]; b["material"] != true || b["stock"] != float64(8) {
		t.Fatalf("the bottles after: %v", b)
	}
}
