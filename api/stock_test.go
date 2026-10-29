package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// withStock gives a world's server the shops' stock movements over its
// fake Xenibase.
func withStock(w *world) *stock.Stock {
	w.srv.Stock = &stock.Stock{Pool: w.env.DB.App, Table: &storefront.XenibaseStock{Pool: w.env.DB.App, Registry: w.env.Registry, Vault: w.env.Vault,
		Guard: w.env.Egress}}
	return w.srv.Stock
}

func itemsByName(t *testing.T, out map[string]any) map[string]map[string]any {
	t.Helper()
	m := map[string]map[string]any{}
	for _, x := range out["items"].([]any) {
		it := x.(map[string]any)
		m[it["name"].(string)] = it
	}
	return m
}

// Stock from the web (docs/spec/stock.md K1): Keep my stock makes a draft
// page and a products table; items are added with what is there as goods
// received; their columns are changed in place and their stock by
// movements only; a count changed in the table by hand is recorded as an
// outside movement when the catalogue is refreshed.
func TestStockByAPI(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")

	if out := owner.must(200, "GET", "/v1/stock", nil); out["keeps_stock"] != false {
		t.Fatalf("before: %v", out)
	}
	owner.must(409, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 5}, "Idempotency-Key", "i0")
	viewer.must(403, "POST", "/v1/stock/setup", nil)

	set := owner.must(200, "POST", "/v1/stock/setup", nil)
	cols := set["columns"].(map[string]any)
	if set["catalogue"] != "products" || cols["stock"] != "stock" || cols["unit"] != "unit" || cols["cost_price"] != "cost_price" || cols["reorder_level"] != "reorder_level" {
		t.Fatalf("setup: %v", set)
	}
	if sf := owner.must(200, "GET", "/v1/storefront", nil); sf["status"] != "draft" || sf["slug"] != "mama-put-stores" {
		t.Fatalf("a shop with no page gets a draft: %v", sf)
	}
	// Again: nothing more.
	owner.must(200, "POST", "/v1/stock/setup", nil)

	owner.must(400, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 5}) // no key
	owner.must(400, "POST", "/v1/stock/items", map[string]any{"name": "", "quantity": 5}, "Idempotency-Key", "i1")
	add := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 50, "unit": "carton", "cost_kobo": 850000},
		"Idempotency-Key", "i2")
	it := add["item"].(map[string]any)
	mv := add["movement"].(map[string]any)
	if it["stock"] != float64(50) || it["unit"] != "carton" || it["cost_kobo"] != float64(850000) || it["available"] != false ||
		mv["reason"] != "received" || mv["status"] != "done" || mv["change"] != float64(50) {
		t.Fatalf("added: %v", add)
	}
	id := it["id"].(string)
	if out := owner.must(409, "POST", "/v1/stock/items", map[string]any{"name": "indomie", "quantity": 3}, "Idempotency-Key", "i3"); out["code"] != "item_exists" || out["item_id"] != id {
		t.Fatalf("again: %v", out)
	}
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 4, "unit": "bag", "price_kobo": 4500000}, "Idempotency-Key", "i4")

	// Its columns change in place; its stock never does.
	owner.must(400, "PATCH", "/v1/stock/items/"+id, map[string]any{"reorder_level": -1})
	if out := owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"reorder_level": 10, "target_level": 60, "price_kobo": 900000, "supplier": "Dangote Depot"}); out["item"].(map[string]any)["reorder_level"] != float64(10) {
		t.Fatalf("patched: %v", out)
	}
	viewer.must(403, "PATCH", "/v1/stock/items/"+id, map[string]any{"reorder_level": 1})

	// Movements: an adjustment with a reason, a count; others refused.
	owner.must(400, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "sale", "change": -1}, "Idempotency-Key", "m0")
	owner.must(400, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "adjustment", "change": 0}, "Idempotency-Key", "m1")
	owner.must(404, "POST", "/v1/stock/movements", map[string]any{"item_id": "999", "reason": "adjustment", "change": -1}, "Idempotency-Key", "m2")
	viewer.must(403, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "adjustment", "change": -1}, "Idempotency-Key", "m3")
	adj := owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "adjustment", "change": -45, "note": "water damage"},
		"Idempotency-Key", "m4")["movement"].(map[string]any)
	if adj["after"] != float64(5) || adj["note"] != "water damage" {
		t.Fatalf("adjusted: %v", adj)
	}
	// The same key again is the same movement.
	if again := owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "adjustment", "change": -45}, "Idempotency-Key", "m4"); again["movement"].(map[string]any)["id"] != adj["id"] {
		t.Fatalf("again: %v", again)
	}
	got := viewer.must(200, "GET", "/v1/stock", nil)
	items := itemsByName(t, got)
	if got["low"] != float64(1) || items["Indomie"]["low"] != true || items["Indomie"]["suggest"] != float64(55) || items["Indomie"]["stock"] != float64(5) ||
		items["Rice"]["low"] != false {
		t.Fatalf("stock: %v", got)
	}
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": id, "reason": "count", "count": 7}, "Idempotency-Key", "m5")

	// The owner edits the count in the table itself: found on refresh.
	tables := owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any)
	var products string
	for _, x := range tables {
		if x.(map[string]any)["name"] == "products" {
			products = x.(map[string]any)["id"].(string)
		}
	}
	owner.must(200, "PATCH", "/v1/tables/"+products+"/rows/"+id, map[string]any{"set": map[string]any{"stock": 9}}, "Idempotency-Key", "edit-1")
	owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	list := viewer.must(200, "GET", "/v1/stock/movements?item="+id, nil)["movements"].([]any)
	reasons := []string{}
	for _, x := range list {
		reasons = append(reasons, x.(map[string]any)["reason"].(string))
	}
	if strings.Join(reasons, ",") != "outside,count,adjustment,received" || list[0].(map[string]any)["change"] != float64(2) {
		t.Fatalf("movements: %v", list)
	}
	viewer.must(400, "GET", "/v1/stock/movements?reason=stolen", nil)
	if n := len(viewer.must(200, "GET", "/v1/stock/movements?reason=received", nil)["movements"].([]any)); n != 2 {
		t.Fatalf("received: %d", n)
	}
	if out := owner.must(200, "GET", "/v1/audit?limit=100", nil); !strings.Contains(toJSON(out), `"stock.move"`) || !strings.Contains(toJSON(out), `"stock.setup"`) {
		t.Fatal("not audited")
	}
}

// A catalogue the business already has keeps its own columns: Keep my
// stock maps the stock column it has and adds the others.
func TestStockOnAnExistingCatalogue(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "menu", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "left", "type": "integer"}, {"name": "unit", "type": "text"}}})
	table := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Jollof rice", "price": "1500", "left": 4, "unit": "plate"}}, "Idempotency-Key", "r1")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price", "stock": "left"}})

	set := owner.must(200, "POST", "/v1/stock/setup", nil)
	cols := set["columns"].(map[string]any)
	if set["catalogue"] != "menu" || cols["stock"] != "left" || cols["unit"] != "unit" || cols["cost_price"] != "cost_price" || cols["supplier"] != "supplier" {
		t.Fatalf("setup: %v", set)
	}
	got := owner.must(200, "GET", "/v1/stock", nil)
	jollof := itemsByName(t, got)["Jollof rice"]
	if jollof["stock"] != float64(4) || jollof["unit"] != "plate" {
		t.Fatalf("stock: %v", got)
	}
	// The first count seen in the table is explained before the first
	// change lands on it.
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": jollof["id"], "reason": "received", "change": 10}, "Idempotency-Key", "m1")
	list := owner.must(200, "GET", "/v1/stock/movements", nil)["movements"].([]any)
	if len(list) != 2 || list[1].(map[string]any)["reason"] != "outside" || list[1].(map[string]any)["note"] != "First count seen in the table" {
		t.Fatalf("movements: %v", list)
	}
}

// Stock from chat (docs/spec/stock.md §4): the first add starts keeping
// stock; adds top up; how many and stock answer from the table; adjust
// and a count are movements; who may is table.read and table.edit.
func TestStockByChat(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	const on, vn, sn = "+2348010000071", "+2348010000072", "+2348010000073"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "vic@mama.test", "viewer"), vn)

	if r := w.say(t, on, "help"); !strings.Contains(r, "*add 50 cartons of Indomie at 8,500*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, on, "stock"); !strings.Contains(r, "keeps no stock in Taskiem yet") {
		t.Fatalf("nothing yet: %q", r)
	}
	if r := w.say(t, vn, "add 50 cartons of Indomie at 8,500"); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "add 50 cartons of Indomie at 8,500"); !strings.Contains(r, "Start keeping stock with *Indomie*: 50 cartons at ₦8,500 each?") {
		t.Fatalf("first add: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Added *Indomie*: 50 cartons.") || !strings.Contains(r, "send *price Indomie 9500*") {
		t.Fatalf("added: %q", r)
	}
	if r := w.say(t, on, "add 20 cartons of indomie"); !strings.Contains(r, "Add 20 cartons to *Indomie* (50 cartons → 70 cartons)?") {
		t.Fatalf("top up: %q", r)
	}
	if r := w.yes(t, on); !strings.HasSuffix(r, "Done: *Indomie* is 70 cartons now.") {
		t.Fatalf("topped up: %q", r)
	}
	if r := w.say(t, vn, "how many cartons of indomie do I have?"); !strings.Contains(r, "*Indomie*: 70 cartons.") || !strings.Contains(r, "Last: received 20 cartons") {
		t.Fatalf("how many: %q", r)
	}
	if r := w.say(t, on, "adjust indomie -3 spoilt"); !strings.Contains(r, "Change *Indomie* by -3 cartons (70 cartons → 67 cartons): spoilt?") {
		t.Fatalf("adjust: %q", r)
	}
	if r := w.yes(t, on); !strings.HasSuffix(r, "Done: *Indomie* is 67 cartons now.") {
		t.Fatalf("adjusted: %q", r)
	}
	if r := w.say(t, on, "adjust indomie lots"); !strings.Contains(r, "*adjust rice -2 spoilt*") {
		t.Errorf("unreadable: %q", r)
	}
	// A count, the storefront's word: a movement now.
	if r := w.say(t, sn, "stock indomie 60"); r != "" && !strings.Contains(r, "not linked") {
		t.Logf("unbound: %q", r)
	}
	if r := w.say(t, on, "stock indomie 60"); !strings.Contains(r, "from 67 to 60") {
		t.Fatalf("count: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "*Indomie* is updated") {
		t.Fatalf("counted: %q", r)
	}
	if r := w.say(t, on, "stock"); !strings.Contains(r, "none has a reorder level yet") {
		t.Fatalf("no levels: %q", r)
	}
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id FROM storefronts WHERE slug = 'mama-put-stores'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))
	id := items["Indomie"]["id"].(string)
	owner.must(200, "PATCH", "/v1/stock/items/"+id, map[string]any{"reorder_level": 70})
	if r := w.say(t, on, "stock"); !strings.Contains(r, "Low on stock:") || !strings.Contains(r, "• Indomie: 60 cartons (reorder at 70); order 80 cartons") {
		t.Fatalf("low: %q", r)
	}
	list := owner.must(200, "GET", "/v1/stock/movements?item="+id, nil)["movements"].([]any)
	var reasons []string
	for _, x := range list {
		reasons = append(reasons, x.(map[string]any)["reason"].(string))
	}
	if strings.Join(reasons, ",") != "count,adjustment,received,received" {
		t.Fatalf("movements: %v", reasons)
	}
	// Several items match: a numbered list.
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie Chicken", "quantity": 3}, "Idempotency-Key", "i9")
	if r := w.say(t, on, "stock indo"); !strings.Contains(r, "More than one matches") {
		t.Fatalf("pick: %q", r)
	}
	if got := w.send(t, on, whatsapptest.Text("2")); len(got) != 1 || !strings.Contains(got[0].Text, "*Indomie Chicken*: 3.") {
		t.Fatalf("picked: %+v", got)
	}
}
