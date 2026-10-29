package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/leaks"
)

// Counts (docs/spec/stock.md K4): by chat in one message or item by item,
// and from the web's sheet; each difference is a count movement naming the
// count; a count of every item is a full one; what was missing is valued
// at cost, is shrinkage for the period, and a leak above the limit.
func TestStockCounts(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	const on = "+2348010000095"
	w.bind(t, owner, on)
	tenant := uuid.MustParse(tenantOf(t, owner))
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 20, "unit": "bag", "cost_kobo": 4000000}, "Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	indomie := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 10, "unit": "carton"}, "Idempotency-Key", "i")["item"].(map[string]any)["id"].(string)

	// In one message, with a reason.
	r := w.say(t, on, "count rice 17, indomie 10: rats in the store")
	if !strings.Contains(r, "Count of 2 items:\n• Rice: 20 → 17 bags\n1 as Taskiem has them.") {
		t.Fatalf("count: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "items changed.") || !strings.Contains(r, ": 1 items changed. Missing: ₦120,000 at cost.") {
		t.Fatalf("counted: %q", r)
	}
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); items["Rice"]["stock"] != float64(17) {
		t.Fatalf("stock: %v", items)
	}
	counts := viewer.must(200, "GET", "/v1/stock/counts", nil)["counts"].([]any)
	if len(counts) != 1 || counts[0].(map[string]any)["full"] != true || counts[0].(map[string]any)["reason"] != "rats in the store" ||
		counts[0].(map[string]any)["lost_kobo"] != float64(12000000) {
		t.Fatalf("counts: %v", counts)
	}

	// Item by item: a number, a skip, then done.
	if r := w.say(t, on, "count"); !strings.Contains(r, "Counting 2 items.") || !strings.Contains(r, "How many *Indomie*? (Taskiem has 10 cartons.)") {
		t.Fatalf("asked: %q", r)
	}
	if r := w.say(t, on, "ten"); !strings.Contains(r, "Send a whole number") {
		t.Fatalf("not a number: %q", r)
	}
	if r := w.say(t, on, "8"); !strings.Contains(r, "How many *Rice*? (Taskiem has 17 bags.)") {
		t.Fatalf("next: %q", r)
	}
	if r := w.say(t, on, "skip"); !strings.Contains(r, "Count of 1 items:\n• Indomie: 10 → 8 cartons") {
		t.Fatalf("shown: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "1 items changed.") || strings.Contains(r, "Missing") {
		t.Fatalf("counted: %q", r)
	}

	// From the web's sheet: a count of one item is not a full one.
	viewer.must(403, "POST", "/v1/stock/counts", map[string]any{"lines": []any{map[string]any{"item_id": rice, "count": 1}}}, "Idempotency-Key", "c0")
	owner.must(404, "POST", "/v1/stock/counts", map[string]any{"lines": []any{map[string]any{"item_id": "999", "count": 1}}}, "Idempotency-Key", "c1")
	c := owner.must(201, "POST", "/v1/stock/counts", map[string]any{"lines": []any{map[string]any{"item_id": rice, "count": 16}}, "reason": "spoilt"},
		"Idempotency-Key", "c2")["count"].(map[string]any)
	if c["full"] != false || c["changed"] != float64(1) || c["lost_kobo"] != float64(4000000) {
		t.Fatalf("web count: %v", c)
	}
	_ = indomie

	// Shrinkage this month: received 30, lost 4 Rice (₦160,000) and 2 Indomie.
	sh := viewer.must(200, "GET", "/v1/stock/shrinkage", nil)
	if sh["received"] != float64(30) || sh["lost"] != float64(6) || sh["lost_kobo"] != float64(16000000) ||
		sh["items"].([]any)[0].(map[string]any)["item"] != "Rice" {
		t.Fatalf("shrinkage: %v", sh)
	}
	viewer.must(400, "GET", "/v1/stock/shrinkage?from=2026-10-29&to=2026-10-01", nil)

	// The leaks: Rice's first count (₦120,000) is above the ₦5,000 limit;
	// raise the limit and the next is not; Indomie has no cost: units.
	owner.must(200, "PUT", "/v1/stock/settings", map[string]any{"count_limit_kobo": 5000000})
	if set := owner.must(200, "PUT", "/v1/stock/settings", map[string]any{"purchase_limit_kobo": 100})["settings"].(map[string]any); set["count_limit_kobo"] != float64(5000000) {
		t.Fatalf("settings kept: %v", set)
	}
	owner.must(400, "PUT", "/v1/stock/settings", map[string]any{"nope": 1})
	if got := owner.must(200, "GET", "/v1/stock/settings", nil)["settings"].(map[string]any); got["count_limit_kobo"] != float64(5000000) || got["purchase_limit_kobo"] != float64(100) {
		t.Fatalf("settings: %v", got)
	}
	if err := (&leaks.Detector{Pool: w.env.DB.App}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var details []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(detail ORDER BY detail) FROM money_leaks WHERE tenant_id = $1 AND kind = 'stock_count'`, tenant).Scan(&details); err != nil ||
		len(details) != 2 || !strings.Contains(details[0], "a count found 2 Indomie fewer") || !strings.Contains(details[1], "a count found 3 Rice fewer") {
		t.Fatalf("leaks: %v %v", details, err)
	}
}

// The sheet-based stock and sales templates give way to stock's own for a
// business keeping stock in a table, and recording sales (K5).
func TestStockTemplatesReplaceSheets(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	offered := func() map[string]bool {
		out := map[string]bool{}
		for _, x := range owner.must(200, "GET", "/v1/templates", nil)["templates"].([]any) {
			out[x.(map[string]any)["id"].(string)] = true
		}
		return out
	}
	if got := offered(); !got["stock-reorder-alert"] || !got["daily-sales-summary"] || !got["low-stock-alert"] || !got["sales-day-total"] {
		t.Fatalf("before: %v", got)
	}
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 5, "price_kobo": 4500000}, "Idempotency-Key", "r")["item"].(map[string]any)["id"]
	if got := offered(); got["stock-reorder-alert"] || !got["daily-sales-summary"] {
		t.Fatalf("keeping stock: %v", got)
	}
	owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}}, "paid_how": "cash"}, "Idempotency-Key", "s")
	if got := offered(); got["daily-sales-summary"] || !got["sales-day-total"] {
		t.Fatalf("recording sales: %v", got)
	}
}

// The stock words in another language (K5): an item's stock, what is low,
// and stock taken off with why.
func TestStockWordsInPidgin(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}}
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	const on = "+2348010000096"
	w.bind(t, owner, on)
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 4, "unit": "carton", "reorder_level": 10}, "Idempotency-Key", "i")
	w.say(t, on, "language pidgin")
	if r := w.say(t, on, "how many cartons of indomie dey"); !strings.Contains(r, "Indomie") || !strings.Contains(r, "4 cartons") {
		t.Fatalf("how many: %q", r)
	}
	if r := w.say(t, on, "check stock"); !strings.Contains(r, "Indomie") {
		t.Fatalf("what is low: %q", r)
	}
	if r := w.say(t, on, "comot indomie 2 spoil"); !strings.Contains(r, "-2") {
		t.Fatalf("taken off: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "2") {
		t.Fatalf("written: %q", r)
	}
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); items["Indomie"]["stock"] != float64(2) {
		t.Fatalf("stock: %v", items)
	}
}
