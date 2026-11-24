package api_test

import (
	"testing"
)

// The catalogue table holds no count (what-a-business-buys §4): a column
// mapped as stock is not read. A product is counted by a stock item, and
// the page's copy says how many are left of it from there, at once when a
// movement changes it.
func TestStorefrontStock(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business") // publishing goes live at once (moderation.md)
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "menu", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "left", "type": "integer"}, {"name": "note", "type": "text"}}})
	table := tbl["id"].(string)
	added := owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Jollof rice", "price": "1500", "left": 4}}, "Idempotency-Key", "r1")
	row := jsonNum(added["rows"].([]any)[0].(map[string]any)["id"])
	card := map[string]any{"name": "Ada's Kitchen"}
	owner.must(400, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price", "stock": "left"}})
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	if items := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any); items[0].(map[string]any)["stock"] != nil {
		t.Fatalf("the table's column read as a count: %v", items)
	}
	// Counted from now: its stock item, and the copy, at once.
	mv := owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": row, "reason": "count", "count": 4}, "Idempotency-Key", "c1")["movement"].(map[string]any)
	if mv["after"] != float64(4) || mv["item_id"] == row {
		t.Fatalf("counted: %v", mv)
	}
	if items := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any); items[0].(map[string]any)["stock"] != float64(4) {
		t.Fatalf("the copy: %v", items)
	}
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": row, "reason": "adjustment", "change": -4, "note": "spoilt"}, "Idempotency-Key", "a1")
	items := owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any)
	if items[0].(map[string]any)["stock"] != float64(0) {
		t.Fatalf("sold out: %v", items)
	}
}
