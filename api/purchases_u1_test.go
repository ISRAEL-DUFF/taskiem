package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// What a business buys §12, Purchases, through the API, from a linked
// supplier's price list: a sewing machine kept track of (Machines) and
// fabric to stock (a material); tracking 60 asks first; the seller's side
// carries no treatment, stock item or label; on arrival the fabric's count
// rises and one row is on Things we have; the next order of the machine
// asks nothing; a received line is locked; the machine sold with a price
// is money in, with permission to enter money.
func TestBuyingThroughALink(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, buyerID := linkedPair(t, w.waWorld)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	fabric := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Ankara fabric", "quantity": 0, "unit": "yard", "material": true},
		"Idempotency-Key", "f")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sMachine := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Singer heavy-duty sewing machine", "quantity": 5, "price_kobo": 18000000},
		"Idempotency-Key", "sm")["item"].(map[string]any)["id"].(string)
	sFabric := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Ankara, 6 yards", "quantity": 500, "unit": "yard", "price_kobo": 150000},
		"Idempotency-Key", "sf")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Singer Ltd", "phone": "0803 123 4612"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": sMachine, "price_kobo": 18000000}, map[string]any{"item_id": sFabric, "price_kobo": 150000}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	machines := uuid.NewString()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES ($1, $2, 'Machines', 'user:owner')`, machines, buyerID); err != nil {
		t.Fatal(err)
	}

	// Tracking 60 asks first, in words the screens can put as a question.
	code, out := buyer.do("POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{
		map[string]any{"seller_item_id": sMachine, "quantity": 60, "treatment": "track"}}}, "Idempotency-Key", "o0")
	if code != 409 || out["code"] != "track_ask" || out["quantity"] != float64(60) || !strings.Contains(out["error"].(string), "Things we have") {
		t.Fatalf("60 tracked: %d %v", code, out)
	}
	x := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{
		map[string]any{"seller_item_id": sMachine, "quantity": 1, "treatment": "track", "label_id": machines},
		map[string]any{"seller_item_id": sFabric, "quantity": 30, "item_id": fabric}}}, "Idempotency-Key", "o1")["purchase"].(map[string]any)
	lines := x["lines"].([]any)
	if l := lines[0].(map[string]any); l["treatment"] != "track" || l["label_id"] != machines || l["unconfirmed"] != nil {
		t.Fatalf("the machine: %v", l)
	}
	if l := lines[1].(map[string]any); l["treatment"] != "stock" || l["stock_item_id"] != fabric {
		t.Fatalf("the fabric: %v", l)
	}
	id := x["id"].(string)
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)

	// The seller sees the same order as ever: its own items, and nothing
	// of how the buyer treats them.
	o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	raw, _ := json.Marshal(o)
	for _, never := range []string{"treatment", "stock_item", "label", machines, fabric, "unconfirmed", "Things we have"} {
		if strings.Contains(string(raw), never) {
			t.Fatalf("the seller sees %q: %s", never, raw)
		}
	}
	if l := o["lines"].([]any)[0].(map[string]any); l["item_id"] != sMachine || l["matched"] != "linked" {
		t.Fatalf("reached the seller as its own item: %v", l)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	drainLinked(t, w.world)
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/delivered", nil)
	drainLinked(t, w.world)
	buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/all-came", nil)

	// The fabric's count rose; one row on Things we have; nothing else.
	if got := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))["Ankara fabric"]; got == nil || got["stock"] != float64(30) {
		t.Fatalf("the fabric: %v", got)
	}
	th := buyer.must(200, "GET", "/v1/things?group=label", nil)
	things := th["things"].([]any)
	if len(things) != 1 || th["count"] != float64(1) || th["total_kobo"] != float64(18000000) {
		t.Fatalf("Things we have: %v", th)
	}
	thing := things[0].(map[string]any)
	if thing["label"] != "Machines" || thing["supplier"] != "Singer Ltd" || thing["order_reference"] != ref || thing["unit_no"] != float64(1) {
		t.Fatalf("the machine's row: %v", thing)
	}
	if g := th["groups"].([]any); len(g) != 1 || g[0].(map[string]any)["label"] != "Machines" {
		t.Fatalf("by label: %v", g)
	}
	// On its order and line: the rows that came on them, counted.
	for q, want := range map[string]float64{"order=" + id: 1, "line=" + thing["purchase_line_id"].(string): 1, "order=" + uuid.NewString(): 0} {
		if got := buyer.must(200, "GET", "/v1/things?group=label&"+q, nil); got["count"] != want || len(got["things"].([]any)) != int(want) || len(got["groups"].([]any)) != int(want) {
			t.Fatalf("%s: %v", q, got)
		}
	}
	buyer.must(400, "GET", "/v1/things?line=machine", nil)
	// Received, the line's treatment is locked.
	if code, out := buyer.do("PATCH", "/v1/stock/purchases/"+id+"/lines/0", map[string]any{"treatment": "record"}); code != 409 || out["code"] != "line_locked" {
		t.Fatalf("a received line: %d %v", code, out)
	}

	// The next order of the machine asks nothing.
	next := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{
		map[string]any{"seller_item_id": sMachine, "quantity": 1}}}, "Idempotency-Key", "o2")["purchase"].(map[string]any)
	if l := next["lines"].([]any)[0].(map[string]any); l["treatment"] != "track" || l["label_id"] != machines || l["unconfirmed"] != nil {
		t.Fatalf("the machine again: %v", l)
	}
	// Changed on the line, remembered: the order after follows it.
	buyer.must(200, "PATCH", "/v1/stock/purchases/"+next["id"].(string)+"/lines/0", map[string]any{"treatment": "record", "remember": true})
	if ls := buyer.must(200, "GET", "/v1/item-links/"+link, nil)["item_links"].([]any); len(ls) != 2 {
		t.Fatalf("remembered: %v", ls)
	}
	again := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{
		map[string]any{"seller_item_id": sMachine, "quantity": 1}}}, "Idempotency-Key", "o3")["purchase"].(map[string]any)
	if l := again["lines"].([]any)[0].(map[string]any); l["treatment"] != "record" {
		t.Fatalf("after the change: %v", l)
	}

	// Sold with a price: money in, which a cashier may not enter.
	cashier := addMember(t, w.world, buyer, "kemi", "cashier")
	sale := map[string]any{"status": "sold", "sale_price_kobo": 9000000}
	cashier.must(403, "PATCH", "/v1/things/"+thing["id"].(string), sale)
	cashier.must(200, "PATCH", "/v1/things/"+thing["id"].(string), map[string]any{"serial": "SN-1", "warranty_until": "2027-01-31"})
	got := buyer.must(200, "PATCH", "/v1/things/"+thing["id"].(string), sale, "Idempotency-Key", "sale-1")["thing"].(map[string]any)
	if got["status"] != "sold" || got["sale_entry_id"] == nil || got["serial"] != "SN-1" || got["warranty_until"] != "2027-01-31" {
		t.Fatalf("sold: %v", got)
	}
	// The same sale retried with its key (its answer lost) is the row as it
	// is; with another key it is refused, recorded already.
	if again := buyer.must(200, "PATCH", "/v1/things/"+thing["id"].(string), sale, "Idempotency-Key", "sale-1")["thing"].(map[string]any); again["sale_entry_id"] != got["sale_entry_id"] {
		t.Fatalf("retried: %v", again)
	}
	if code, out := buyer.do("PATCH", "/v1/things/"+thing["id"].(string), sale, "Idempotency-Key", "sale-2"); code != 400 || !strings.Contains(out["error"].(string), "recorded already") {
		t.Fatalf("sold again: %d %v", code, out)
	}
	var sales int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND reference LIKE 'thing-sale:%'`, buyerID).Scan(&sales); err != nil || sales != 1 {
		t.Fatalf("the sale's money: %d %v", sales, err)
	}
	if th := buyer.must(200, "GET", "/v1/things?all=true", nil); len(th["things"].([]any)) != 1 || th["count"] != float64(0) {
		t.Fatalf("in the history, out of the totals: %v", th)
	}
	added := buyer.must(201, "POST", "/v1/things", map[string]any{"name": "Generator", "cost_kobo": 25000000, "label_id": machines})["thing"].(map[string]any)
	if added["label"] != "Machines" || added["purchase_order_id"] != nil {
		t.Fatalf("added by hand: %v", added)
	}
}
