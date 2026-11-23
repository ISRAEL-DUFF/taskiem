package api_test

import (
	"strings"
	"testing"
)

// A count by scanning (device fields D2): the barcode on a pack is kept on
// the stock item (migration 00590), a product's or a material's; items and
// stock items carry it for the app's count to match; one current stock
// item has a code (409 naming the one that has it); the public page never
// shows it.
func TestStockItemCodes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Bola Mart", "bola@codes.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	it := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie Chicken", "quantity": 40, "price_kobo": 25000}, "Idempotency-Key", "i")["item"].(map[string]any)
	id := it["id"].(string)
	nylon := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Nylon bags", "quantity": 100, "material": true}, "Idempotency-Key", "n")["item"].(map[string]any)["id"].(string)

	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": strings.Repeat("9", 65)})
	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": "615\x0011"})
	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": "615\t11"})
	if got := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": " 6151100093217 "})["item"].(map[string]any); got["code"] != "6151100093217" {
		t.Fatalf("set: %v", got)
	}
	if cols := owner.must(200, "GET", "/v1/stock", nil)["columns"].(map[string]any); cols["code"] != nil {
		t.Fatalf("no catalogue column for it: %v", cols)
	}
	if x := stockItemByName(t, owner, "Indomie Chicken"); x["code"] != "6151100093217" {
		t.Fatalf("on the stock item: %v", x)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/stock", nil)["items"]), `"code":"6151100093217"`) {
		t.Fatal("items carry it")
	}
	// By the stock item's own id too, and on a material.
	sid := stockItemByName(t, owner, "Indomie Chicken")["id"].(string)
	if got := owner.must(200, "PATCH", "/v1/stock/items/"+sid, map[string]any{"code": "6151100093217"})["item"].(map[string]any); got["code"] != "6151100093217" {
		t.Fatalf("unchanged by its stock item: %v", got)
	}
	e := owner.must(409, "PATCH", "/v1/stock/items/"+nylon, map[string]any{"code": "6151100093217"})
	if e["code"] != "code_taken" || !strings.Contains(e["error"].(string), "Indomie Chicken") || e["stock_item_id"] != sid {
		t.Fatalf("a code another has: %v", e)
	}
	owner.must(200, "PATCH", "/v1/stock/items/"+nylon, map[string]any{"code": "nyl-1"})
	if e := owner.must(409, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": "NYL-1", "unit": "carton"}); !strings.Contains(e["error"].(string), "Nylon bags") {
		t.Fatalf("in another case: %v", e)
	}
	if x := stockItemByName(t, owner, "Indomie Chicken"); x["code"] != "6151100093217" || x["unit"] == "carton" {
		t.Fatalf("a refused code changes nothing: %v", x)
	}
	if x := stockItemByName(t, owner, "Nylon bags"); x["code"] != "nyl-1" {
		t.Fatalf("a material's: %v", x)
	}
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/storefront/catalogue", nil)), "6151100093217") {
		t.Fatal("the barcode on the page's items")
	}
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"code": ""})["item"].(map[string]any); it["code"] != nil {
		t.Fatalf("cleared: %v", it)
	}
	if x := stockItemByName(t, owner, "Indomie Chicken"); x["code"] != "" {
		t.Fatalf("cleared on the stock item: %v", x)
	}
	// Changed as an item's details are: audited.
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), `["code"]`) {
		t.Fatal("not audited")
	}
}
