package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
)

// Product categories (docs/spec/chat-categories-and-variants.md, Part A):
// the catalogue gets a category column the first time one is set; items
// carry it; WhatsApp answers a category asked for by name, 10 at a time.
func TestCategories(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const on = "+2348010000081"
	w.bind(t, owner, on)
	owner.must(200, "POST", "/v1/stock/setup", nil)
	if cols := owner.must(200, "GET", "/v1/stock", nil)["columns"].(map[string]any); cols["category"] != nil {
		t.Fatalf("no category column before one is set: %v", cols)
	}
	plain := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Shoe polish", "quantity": 9}, "Idempotency-Key", "p")["item"].(map[string]any)
	if plain["category"] != nil {
		t.Fatalf("no category: %v", plain)
	}
	owner.must(400, "POST", "/v1/stock/items", map[string]any{"name": "Long", "quantity": 1, "category": strings.Repeat("x", 61)}, "Idempotency-Key", "x")
	for i := 1; i <= 12; i++ {
		it := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": fmt.Sprintf("Air Max %02d", i), "quantity": i, "unit": "pair",
			"category": "  Sneakers ", "reorder_level": 2}, "Idempotency-Key", fmt.Sprintf("s%d", i))["item"].(map[string]any)
		if it["category"] != "Sneakers" {
			t.Fatalf("category trimmed and kept: %v", it)
		}
	}
	got := owner.must(200, "GET", "/v1/stock", nil)
	if got["columns"].(map[string]any)["category"] != "category" {
		t.Fatalf("the column is added and mapped: %v", got["columns"])
	}
	// Set on an item that has none, and cleared.
	id := plain["id"].(string)
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": "Care"})["item"].(map[string]any); it["category"] != "Care" {
		t.Fatalf("set: %v", it)
	}
	if it := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": ""})["item"].(map[string]any); it["category"] != nil {
		t.Fatalf("cleared: %v", it)
	}
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"category": "Sandals"})
	// The shop's page has them too: it shows its items by category.
	for _, x := range owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any) {
		it := x.(map[string]any)
		if it["name"] == "Shoe polish" && it["category"] != "Sandals" || it["name"] == "Air Max 01" && it["category"] != "Sneakers" {
			t.Fatalf("the page's items: %v", it)
		}
	}

	// WhatsApp: the category by name, 10 at a time, then *more*.
	r := w.say(t, on, "sneakers")
	if !strings.Contains(r, "*Sneakers* (12):") || !strings.Contains(r, "• Air Max 01: 1 pair (low)") || !strings.Contains(r, "• Air Max 10: 10 pairs") ||
		strings.Contains(r, "Air Max 11") || !strings.Contains(r, "Send *more* for the rest.") {
		t.Fatalf("a category: %q", r)
	}
	if r := w.say(t, on, "more"); !strings.Contains(r, "• Air Max 11: 11 pairs") || !strings.Contains(r, "• Air Max 12") || strings.Contains(r, "Air Max 10") ||
		strings.Contains(r, "*more*") {
		t.Fatalf("more: %q", r)
	}
	if r := w.say(t, on, "show me sandals"); !strings.Contains(r, "*Sandals* (1):") || !strings.Contains(r, "• Shoe polish: 9") {
		t.Fatalf("show me: %q", r)
	}
	if r := w.say(t, on, "stock sneaker"); !strings.Contains(r, "*Sneakers* (12):") {
		t.Fatalf("stock and a singular: %q", r)
	}
	// Something else lets the list go and is read as itself.
	w.say(t, on, "sneakers")
	if r := w.say(t, on, "stock shoe polish"); !strings.Contains(r, "*Shoe polish*: 9.") {
		t.Fatalf("after a list, an item: %q", r)
	}
	// An item called what a category is called answers as the item.
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Sandals", "quantity": 4, "category": "Sandals"}, "Idempotency-Key", "sd")
	if r := w.say(t, on, "stock sandals"); !strings.Contains(r, "*Sandals*: 4.") {
		t.Fatalf("an item: %q", r)
	}
	if audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=100", nil)); !strings.Contains(audit, `"products.category"`) {
		t.Fatal("the column's adding is not audited")
	}
}

// A price list carries each item's category to the buyer, and a category
// changed later reaches the buyer too.
func TestCategoriesOnPriceLists(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	// The seller's catalogue maps a description too, as one made on the web.
	sf := seller.must(200, "GET", "/v1/storefront", nil)
	table := sf["catalogue_table"].(string)
	if s, out := seller.do("POST", "/v1/tables/"+table+"/columns", map[string]any{"name": "description", "type": "text"}); s != 200 && s != 201 {
		t.Fatalf("a description column: %d %v", s, out)
	}
	cols := sf["catalogue_columns"].(map[string]any)
	cols["description"] = "description"
	seller.must(200, "PUT", "/v1/storefront", map[string]any{"slug": sf["slug"], "draft": sf["draft"], "catalogue_table": table, "catalogue_columns": cols})
	rice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50 kg", "quantity": 50, "unit": "bag", "price_kobo": 4500000, "category": "Grains"},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	seller.must(200, "PATCH", "/v1/tables/"+table+"/rows/"+rice, map[string]any{"set": map[string]any{"description": "Mama Gold, long grain"}}, "Idempotency-Key", "d1")
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": rice, "price_kobo": 4200000}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	category := func() any {
		t.Helper()
		got := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)
		if len(got) != 1 {
			t.Fatalf("one supplier: %v", got)
		}
		return got[0].(map[string]any)["items"].([]any)[0].(map[string]any)["category"]
	}
	if c := category(); c != "Grains" {
		t.Fatalf("the buyer sees the seller's category: %v", c)
	}
	// And its description, for search.
	if d := buyer.must(200, "GET", "/v1/supplier-prices", nil)["suppliers"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["description"]; d != "Mama Gold, long grain" {
		t.Fatalf("the buyer sees the seller's description: %v", d)
	}
	if l := seller.must(200, "GET", "/v1/price-lists", nil)["lists"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any); l["category"] != "Grains" {
		t.Fatalf("the seller's list: %v", l)
	}
	seller.must(200, "PATCH", "/v1/stock/items/"+rice, map[string]any{"category": "Rice and grains"})
	if c := category(); c != "Rice and grains" {
		t.Fatalf("changed later: %v", c)
	}
}

// A catalogue with as many columns as the plan allows gets no category
// column: the answer says what to do, and nothing else changes.
func TestCategoryWithNoRoomForTheColumn(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id FROM storefronts WHERE slug = 'zuri-atelier'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var columns int
	for _, x := range owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		if tb := x.(map[string]any); tb["name"] == "products" {
			columns = len(tb["columns"].([]any))
		}
	}
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"max_data_table_columns": columns}, "operator"); err != nil {
		t.Fatal(err)
	}
	out := owner.must(409, "POST", "/v1/stock/items", map[string]any{"name": "Slides", "quantity": 1, "category": "Sandals"}, "Idempotency-Key", "a")
	if out["code"] != "no_category_column" || !strings.Contains(out["error"].(string), "as many columns as your plan allows") {
		t.Fatalf("no room: %v", out)
	}
	it := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Slides", "quantity": 1}, "Idempotency-Key", "b")["item"].(map[string]any)
	owner.must(409, "PATCH", "/v1/stock/items/"+it["id"].(string), map[string]any{"category": "Sandals"})
	// Clearing a category the catalogue cannot hold is nothing to do.
	owner.must(200, "PATCH", "/v1/stock/items/"+it["id"].(string), map[string]any{"category": ""})
}

// On Telegram as on WhatsApp: a category asked for by name.
func TestCategoriesOnTelegram(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	withStock(w.world)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	const acct = 70081
	w.link(t, owner, acct, "zuri")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Slides", "quantity": 4, "unit": "pair", "category": "Sandals"}, "Idempotency-Key", "s")
	if r := w.say(t, acct, "show me sandals"); !strings.Contains(r.Text, "Sandals") || !strings.Contains(r.Text, "Slides: 4 pairs") {
		t.Fatalf("telegram: %+v", r)
	}
}
