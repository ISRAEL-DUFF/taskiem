package api_test

import (
	"strings"
	"testing"
)

// A count by scanning (device fields D2): an item is given the barcode on
// its pack, the catalogue getting a barcode column the first time; items
// carry it for the app's count to match; the public page never shows it.
func TestStockItemCodes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Bola Mart", "bola@codes.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	it := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie Chicken", "quantity": 40, "price_kobo": 25000}, "Idempotency-Key", "i")["item"].(map[string]any)
	id := it["id"].(string)
	if cols := owner.must(200, "GET", "/v1/stock", nil)["columns"].(map[string]any); cols["code"] != nil {
		t.Fatalf("no barcode column before one is set: %v", cols)
	}
	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": strings.Repeat("9", 65)})
	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": "615\x0011"})
	if got := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": " 6151100093217 "})["item"].(map[string]any); got["code"] != "6151100093217" {
		t.Fatalf("set: %v", got)
	}
	got := owner.must(200, "GET", "/v1/stock", nil)
	if got["columns"].(map[string]any)["code"] != "barcode" {
		t.Fatalf("the column is added and mapped: %v", got["columns"])
	}
	if !strings.Contains(toJSON(got["items"]), `"code":"6151100093217"`) {
		t.Fatalf("items carry it: %v", got["items"])
	}
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/storefront/catalogue", nil)), "6151100093217") {
		t.Fatal("the barcode on the page's items")
	}
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": ""})["item"].(map[string]any); it["code"] != nil {
		t.Fatalf("cleared: %v", it)
	}
}
