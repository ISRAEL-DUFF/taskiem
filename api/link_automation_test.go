package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	linkconn "github.com/israel-duff/taskiem/connectors/link"
)

// Automation between linked businesses (L6, docs/spec/business-links.md
// §11): a buyer's workflow reads a linked supplier's price and orders at
// it with link@1; the order reaching the seller starts the seller's
// workflow on the link trigger (order.received), which reads it, accepts
// it and answers on its card, all as the system; a business not on the
// link, with the same trigger, starts nothing.
func TestLinkAutomation(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	if err := w.env.Registry.Register(linkconn.New(w.srv)); err != nil {
		t.Fatal(err)
	}
	seller, buyer, sellerID, buyerID := linkedPair(t, w.waWorld)
	other := w.tenant(t, "Bystander Ltd", "b@bills.test")
	otherID := tenantOf(t, other)

	buyer.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000}, "Idempotency-Key", "r")
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50 kg", "quantity": 50, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4801"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 4200000, "min_quantity": 5}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	drainLinked(t, w.world)

	// The seller's workflow, and the bystander's, start on a linked order.
	const onOrder = `{"schema":"wd/v1","id":"wf_onOrder","version":1,"name":"on order","trigger":{"type":"link","config":{"event":"order.received"}},
	  "steps":[
	    {"id":"order","type":"connector","connector":"link@1","action":"get_order","input":{"reference":"=trigger.body.reference"}},
	    {"id":"accept","type":"connector","connector":"link@1","action":"order_step","needs":["order"],"input":{"reference":"=trigger.body.reference","step":"accept"}},
	    {"id":"thanks","type":"connector","connector":"link@1","action":"reply","needs":["accept"],"input":{"reference":"=trigger.body.reference","text":"Thanks, it is on its way"}}]}`
	sellerWF := publishFlow(t, seller, onOrder)
	publishFlow(t, other, onOrder)

	// The buyer's workflow orders at the supplier's price.
	const orderIt = `{"schema":"wd/v1","id":"wf_orderIt","version":1,"name":"order it","trigger":{"type":"manual"},
	  "steps":[
	    {"id":"price","type":"connector","connector":"link@1","action":"price_of","input":{"business":"dangote","item":"Rice"}},
	    {"id":"place","type":"connector","connector":"link@1","action":"place_order","needs":["price"],"input":{"business":"Dangote Stores","items":[{"item":"Rice","quantity":6}]}}]}`
	buyerWF := publishFlow(t, buyer, orderIt)
	run := buyer.must(201, "POST", "/v1/workflows/"+buyerWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got := buyer.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("the buyer's run: %v\n%s", st, toJSON(got["events"]))
	}
	ev := toJSON(got["events"])
	// "Rice" is the only item on their list with the word.
	if !strings.Contains(ev, `"price_kobo":4200000`) || !strings.Contains(ev, `"status":"sent"`) {
		t.Fatalf("priced and sent: %s", ev)
	}
	var ref string
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		ref = x.(map[string]any)["reference"].(string)
	}

	// The order reaches the seller; its workflow starts and accepts it.
	drainLinked(t, w.world)
	w.env.Drain(t)
	drainLinked(t, w.world)
	if o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any); o["status"] != "accepted" {
		t.Fatalf("accepted by the seller's run: %v", o)
	}
	var runs, others int
	var by string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), max(r.started_by) FROM link_starts s JOIN runs r ON r.id = s.run_id
		WHERE s.tenant_id = $1 AND s.workflow_id = $2 AND s.event = 'order.received'`, sellerID, sellerWF).Scan(&runs, &by); err != nil || runs != 1 {
		t.Fatalf("one run for the seller: %d %v", runs, err)
	}
	if by != "link:order.received" {
		t.Errorf("started by: %q", by)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM link_starts WHERE tenant_id = $1`, otherID).Scan(&others); err != nil || others != 0 {
		t.Errorf("the bystander started nothing: %d %v", others, err)
	}
	// The answer is on the order's card, in the buyer's conversation.
	answered := false
	for _, m := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		m := m.(map[string]any)
		if m["body"] == "Thanks, it is on its way" {
			r, _ := m["reply"].(map[string]any)
			c, _ := r["card"].(map[string]any)
			answered = c != nil && c["reference"] == ref
		}
	}
	if !answered {
		t.Error("the answer on the order's card")
	}
	// Audited as the run, in the seller's business.
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'trade_order.accept' AND actor_type = 'system' AND actor_id LIKE 'workflow:%'`,
		sellerID).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audited: %d %v", audited, err)
	}

	// A step refused for good fails plainly: a business not linked.
	const bad = `{"schema":"wd/v1","id":"wf_bad","version":1,"name":"bad","trigger":{"type":"manual"},
	  "steps":[{"id":"price","type":"connector","connector":"link@1","action":"price_of","input":{"business":"Bystander","item":"Rice"}}]}`
	badWF := publishFlow(t, buyer, bad)
	run = buyer.must(201, "POST", "/v1/workflows/"+badWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got = buyer.must(200, "GET", "/v1/runs/"+run, nil)
	raw, _ := json.Marshal(got["events"])
	if st := got["run"].(map[string]any)["status"]; st != "failed" || !strings.Contains(string(raw), "no business linked with you is called") {
		t.Fatalf("not linked: %v %s", st, raw)
	}

	// A bill from the linked supplier starts the buyer's workflow, which
	// reads it.
	const onBill = `{"schema":"wd/v1","id":"wf_onBill","version":1,"name":"on bill","trigger":{"type":"link","config":{"event":"bill.received"}},
	  "steps":[{"id":"bill","type":"connector","connector":"link@1","action":"get_bill","input":{"reference":"=trigger.body.reference"}}]}`
	billWF := publishFlow(t, buyer, onBill)
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Sacks", "quantity": 1, "unit_kobo": 1000000}}}, "Idempotency-Key", "i-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	drainLinked(t, w.world)
	w.env.Drain(t)
	var billRun string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id::text FROM link_starts WHERE workflow_id = $1`, billWF).Scan(&billRun); err != nil {
		t.Fatal(err)
	}
	got = buyer.must(200, "GET", "/v1/runs/"+billRun, nil)
	if ev := toJSON(got["events"]); got["run"].(map[string]any)["status"] != "completed" || !strings.Contains(ev, `"due_kobo":1000000`) ||
		!strings.Contains(ev, `"reference":"`+x["number"].(string)+`"`) {
		t.Fatalf("the bill read: %s", ev)
	}
}
