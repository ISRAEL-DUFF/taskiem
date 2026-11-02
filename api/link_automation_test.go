package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	billsconn "github.com/israel-duff/taskiem/connectors/bills"
	linkconn "github.com/israel-duff/taskiem/connectors/link"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/links"
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
	w.srv.AccountRails = &linkRails{} // the seller issues the buyer an account at acceptance, so its bills can be paid
	payments := &links.Payments{Pool: w.env.DB.App}
	for _, c := range []*connector.Connector{linkconn.New(w.srv), billsconn.New(payments), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) {
		return []map[string]any{{"name": "Wema Bank Plc", "code": "035"}}, nil
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

	// low_orders, list_bills and pay_bill: what the low items would order
	// from each linked supplier (Oil, linked to the seller's list; Garri,
	// listed by no one; Rice, on an open order already, left out), the
	// bills to pay, and the first paid through the business's bill
	// payments, which its approver approves.
	sOil := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Groundnut oil, 25 l", "quantity": 40, "unit": "keg", "price_kobo": 3000000},
		"Idempotency-Key", "so")["item"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{
		map[string]any{"item_id": sRice, "price_kobo": 4200000, "min_quantity": 5}, map[string]any{"item_id": sOil, "price_kobo": 2900000, "min_quantity": 2}}})
	oil := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Oil", "quantity": 1, "unit": "keg", "cost_kobo": 3000000, "reorder_level": 5, "target_level": 12},
		"Idempotency-Key", "o")["item"].(map[string]any)["id"].(string)
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Garri", "quantity": 0, "unit": "bag", "reorder_level": 2, "target_level": 6}, "Idempotency-Key", "g")
	buyer.must(200, "PUT", "/v1/item-links/"+link, map[string]any{"item_id": oil, "seller_item_id": sOil})
	drainLinked(t, w.world)
	buyer.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	buyer.must(200, "PUT", "/v1/bills/pay/setup", map[string]any{"rail_connection": "main"})
	const payIt = `{"schema":"wd/v1","id":"wf_payIt","version":1,"name":"pay it","trigger":{"type":"manual"},
	  "steps":[
	    {"id":"low","type":"connector","connector":"link@1","action":"low_orders","input":{}},
	    {"id":"bills","type":"connector","connector":"link@1","action":"list_bills","needs":["low"],"input":{"business":"Dangote Stores"}},
	    {"id":"pay","type":"connector","connector":"link@1","action":"pay_bill","needs":["bills"],"input":{"reference":"=steps.bills.output.bills[0].reference"}}]}`
	payWF := publishFlow(t, buyer, payIt)
	run = buyer.must(201, "POST", "/v1/workflows/"+payWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got = buyer.must(200, "GET", "/v1/runs/"+run, nil)
	ev = toJSON(got["events"])
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("low orders, bills and pay: %v\n%s", st, ev)
	}
	for _, want := range []string{`"own_item":"Oil"`, `"quantity":11`, `"price_kobo":2900000`, `"not_listed":["Garri"]`, `"payable":true`,
		`"reference":"` + x["number"].(string) + `"`, `"status":"approving"`} {
		if !strings.Contains(ev, want) {
			t.Errorf("the run's events have no %s:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, `"own_item":"Rice"`) {
		t.Errorf("Rice, on an open order, offered again:\n%s", ev)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+x["number"].(string), nil); b["status"] != "paying" || b["paying_kobo"] != float64(1000000) {
		t.Errorf("paying: %v", b)
	}
	var startedBy string
	var payRun *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT created_by, run_id::text FROM bill_payments WHERE tenant_id = $1`, buyerID).Scan(&startedBy, &payRun); err != nil ||
		!strings.HasPrefix(startedBy, "workflow:") || payRun == nil {
		t.Fatalf("the payment, started by the step, with its run: %q %v %v", startedBy, payRun, err)
	}
	// Its payment waits for the approver, through the fake rail's workflow.
	if r := buyer.must(200, "GET", "/v1/runs/"+*payRun, nil)["run"].(map[string]any); r["status"] == "failed" || r["status"] == "completed" {
		t.Errorf("the bill's payment ended at once: %v", r)
	}
	// Paying: no longer listed to pay.
	run = buyer.must(201, "POST", "/v1/workflows/"+payWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got = buyer.must(200, "GET", "/v1/runs/"+run, nil)
	if ev := toJSON(got["events"]); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(ev, `"count":0`) {
		t.Errorf("a bill under way listed again, or paid twice: %v\n%s", got["run"].(map[string]any)["status"], ev)
	}

	// A write outside prod is refused: a sandbox run reads, never orders.
	run = buyer.must(201, "POST", "/v1/workflows/"+buyerWF+"/runs", map[string]any{"environment": "dev", "input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got = buyer.must(200, "GET", "/v1/runs/"+run, nil)
	if ev := toJSON(got["events"]); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(ev, "place_order acts only in prod") ||
		!strings.Contains(ev, `"price_kobo":4200000`) {
		t.Errorf("a dev run ordering: %v\n%s", got["run"].(map[string]any)["status"], ev)
	}
	var orders int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM purchase_orders WHERE tenant_id = $1`, buyerID).Scan(&orders); err != nil || orders != 1 {
		t.Errorf("orders after the dev run: %d %v", orders, err)
	}

	// reply writes to a linked business with the publisher's chat.trade: a
	// workflow published by a member who may publish but not trade writes
	// nothing; the owner's does.
	seller.must(201, "PUT", "/v1/roles/publisher", map[string]any{"permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	publisher := addMember(t, w.world, seller, "pub", "publisher")
	const sayHi = `{"schema":"wd/v1","id":"wf_sayHi","version":1,"name":"say hi","trigger":{"type":"manual"},
	  "steps":[{"id":"hi","type":"connector","connector":"link@1","action":"reply","input":{"business":"Ada's Kitchen","text":"Good morning from the shop"}}]}`
	hiWF := publishFlow(t, publisher, sayHi)
	run = seller.must(201, "POST", "/v1/workflows/"+hiWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got = seller.must(200, "GET", "/v1/runs/"+run, nil)
	if ev := toJSON(got["events"]); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(ev, "needs chat.trade") {
		t.Errorf("published without chat.trade: %v\n%s", got["run"].(map[string]any)["status"], ev)
	}
	hiWF = publishFlow(t, seller, sayHi)
	run = seller.must(201, "POST", "/v1/workflows/"+hiWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	if got = seller.must(200, "GET", "/v1/runs/"+run, nil); got["run"].(map[string]any)["status"] != "completed" {
		t.Errorf("published by the owner: %v\n%s", got["run"].(map[string]any)["status"], toJSON(got["events"]))
	}
	w.srv.WaitBackground()
	var said int
	for _, m := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["body"] == "Good morning from the shop" {
			said++
		}
	}
	if said != 1 {
		t.Errorf("the owner's workflow's message, once: %d", said)
	}

	// An order over the customer's credit limit is not accepted by a run:
	// the seller's workflow fails plainly, and a person accepts it.
	seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"credit_limit_kobo": 100})
	run = buyer.must(201, "POST", "/v1/workflows/"+buyerWF+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	drainLinked(t, w.world)
	w.env.Drain(t)
	drainLinked(t, w.world)
	var overRun string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id::text FROM link_starts WHERE tenant_id = $1 AND workflow_id = $2 AND event = 'order.received'
		ORDER BY created_at DESC LIMIT 1`, sellerID, sellerWF).Scan(&overRun); err != nil {
		t.Fatal(err)
	}
	got = seller.must(200, "GET", "/v1/runs/"+overRun, nil)
	if ev := toJSON(got["events"]); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(ev, "past the credit limit you set") {
		t.Errorf("over the limit, accepted by a run: %v\n%s", got["run"].(map[string]any)["status"], ev)
	}
	var flagged int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM link_orders WHERE tenant_id = $1 AND status = 'received' AND over_limit_at IS NOT NULL`, sellerID).Scan(&flagged); err != nil || flagged != 1 {
		t.Errorf("the order waiting, flagged: %d %v", flagged, err)
	}
}

// A step retried places one order (L6; the review): the same key finds
// the order drafted and sent before, and a second draft or send is
// neither made nor audited.
func TestLinkPlaceOrderRetry(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, buyerID := linkedPair(t, w.waWorld)
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000}, "Idempotency-Key", "r")
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50 kg", "quantity": 50, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4802"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 4200000, "min_quantity": 5}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	drainLinked(t, w.world)

	call := linkconn.Call{Tenant: uuid.MustParse(buyerID), Run: uuid.New(), Step: "place", Action: "place_order", Key: "k-1", Environment: "prod",
		Input: map[string]any{"business": "Dangote Stores", "items": []any{map[string]any{"item": "Rice", "quantity": 6}}}}
	first, err := w.srv.LinkAction(ctx, call)
	if err != nil || first["status"] != "sent" {
		t.Fatalf("placed: %v %v", first, err)
	}
	again, err := w.srv.LinkAction(ctx, call)
	if err != nil || again["reference"] != first["reference"] || again["status"] != "sent" {
		t.Fatalf("again: %v %v", again, err)
	}
	var orders, drafts, sends, itemLinks int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_orders WHERE tenant_id = $1),
		(SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'purchase.draft'),
		(SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'purchase.send'),
		(SELECT count(*) FROM item_links WHERE tenant_id = $1)`, buyerID).Scan(&orders, &drafts, &sends, &itemLinks); err != nil {
		t.Fatal(err)
	}
	if orders != 1 || drafts != 1 || sends != 1 || itemLinks != 1 {
		t.Errorf("one order, drafted and sent once, one item link: %d %d %d %d", orders, drafts, sends, itemLinks)
	}
	// Another key is another order.
	call.Key = "k-2"
	if second, err := w.srv.LinkAction(ctx, call); err != nil || second["reference"] == first["reference"] {
		t.Errorf("a new key: %v %v", second, err)
	}
}
