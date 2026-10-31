package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Linked orders (L2, docs/spec/business-links.md §7): a purchase order to a
// supplier that is a linked business goes through the link, not by chat,
// and arrives as an order in the seller's business; the seller's change,
// accepted by the buyer, the order readied, dispatched and delivered, flow
// back to the purchase order; All came raises the buyer's stock and, the
// invoice being raised on delivery, the seller's invoice becomes the
// buyer's bill. A cancel before acceptance cancels both sides; after, it
// is a request the seller keeps or grants. Invoiced at acceptance, a short
// delivery the seller agrees takes what did not come off the invoice. A
// delivery no one answers is received from the seller's count a day later.
func TestLinkedOrders(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	seller, buyer, sellerID, buyerID := linkedPair(t, w.waWorld)
	const sellerOn, buyerOn = "+2348010000361", "+2348010000362"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	tick := func(want int) {
		t.Helper()
		n, err := w.srv.LinkOrdersTick(ctx)
		if err != nil || n != want {
			t.Fatalf("did %d, want %d: %v", n, want, err)
		}
		w.srv.WaitBackground()
	}

	// The buyer's stock and its supplier, said to be the linked business.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000, "reorder_level": 5,
		"target_level": 20}, "Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	indomie := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 4, "unit": "carton", "cost_kobo": 850000},
		"Idempotency-Key", "i")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4500"})["supplier"].(map[string]any)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(400, "PUT", "/v1/stock/suppliers/"+sup["id"].(string)+"/link", map[string]any{"link_id": uuid.NewString()})
	if x := buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup["id"].(string)+"/link", map[string]any{"link_id": link})["supplier"].(map[string]any); x["reached"] != "link" || x["link_id"] != link {
		t.Fatalf("linked supplier: %v", x)
	}
	tenant := uuid.MustParse(buyerID)
	sms := smsCount(t, w.world, tenant)
	draft := func(key string, lines ...map[string]any) string {
		t.Helper()
		return buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup["id"], "lines": lines}, "Idempotency-Key", key)["purchase"].(map[string]any)["id"].(string)
	}
	purchase := func(id string) map[string]any {
		t.Helper()
		return buyer.must(200, "GET", "/v1/stock/purchases/"+id, nil)["purchase"].(map[string]any)
	}
	orderOf := func(ref string) map[string]any {
		t.Helper()
		return seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	}

	// Sent: through the link, not by SMS or chat.
	po := draft("po-1", map[string]any{"item_id": rice, "quantity": 10}, map[string]any{"item_id": indomie, "quantity": 5})
	sent := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)
	ref := sent["reference"].(string)
	if sent["status"] != "sent" || sent["link_id"] != link || smsCount(t, w.world, tenant) != sms {
		t.Fatalf("sent: %v", sent)
	}
	before := len(w.graph.Sent(sellerOn))
	tick(1)
	tick(0)
	o := orderOf(ref)
	if o["status"] != "received" || o["buyer_name"] != "Ada's Kitchen" || o["total_kobo"] != float64(44250000) || o["buyer"] != buyerID {
		t.Fatalf("the order: %v", o)
	}
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* ordered 10 bags Rice, 5 cartons Indomie, ₦442,500") ||
		len(told[0].Buttons) != 2 || told[0].Buttons[0].ID != "lo:accept:"+orderOf(ref)["id"].(string) {
		t.Errorf("the seller told: %+v", told)
	}
	if p := purchase(po); p["seller_status"] != "received" {
		t.Errorf("the purchase: %v", p)
	}
	// The buyer sees no orders; the seller sees no purchase orders of the buyer's.
	if got := buyer.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any); len(got) != 0 {
		t.Errorf("the buyer's orders: %v", got)
	}
	seller.must(404, "GET", "/v1/stock/purchases/"+po, nil)

	// The seller's change, accepted by the buyer.
	seller.must(409, "POST", "/v1/trade-orders/"+ref+"/ready", nil)
	before = len(w.graph.Sent(buyerOn))
	change := map[string]any{"lines": []any{map[string]any{"item": "Rice", "quantity": 8, "price_kobo": 4200000}, map[string]any{"item": "Indomie", "quantity": 5, "price_kobo": 850000}},
		"note": "Only 8 bags left"}
	if o := seller.must(200, "POST", "/v1/trade-orders/"+o["id"].(string)+"/change", change)["order"].(map[string]any); o["status"] != "changed" {
		t.Fatalf("changed: %v", o)
	}
	seller.must(409, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	if p := purchase(po); p["seller_status"] != "changed" || len(p["change"].([]any)) != 2 || !strings.Contains(p["counter"].(string), "8 Rice at ₦42,000") {
		t.Errorf("the change mirrored: %v", p)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* can supply order "+ref+" like this: 8 Rice at ₦42,000. Only 8 bags left") {
		t.Errorf("the buyer told: %+v", told)
	}
	p := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/accept-change", nil)["purchase"].(map[string]any)
	if p["status"] != "confirmed" || p["seller_status"] != "accepted" || p["total_kobo"] != float64(37850000) || p["change"] != nil {
		t.Fatalf("the change accepted: %v", p)
	}
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/accept-change", nil)
	if o := orderOf(ref); o["status"] != "accepted" || o["total_kobo"] != float64(37850000) {
		t.Fatalf("accepted with the change: %v", o)
	}

	// Readied, dispatched and delivered; All came.
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/all-came", nil)
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/ready", nil)
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/dispatched", map[string]any{"note": "Musa is bringing it"})
	w.srv.WaitBackground()
	before = len(w.graph.Sent(buyerOn))
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/delivered", nil)
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* delivered your order "+ref+": 8 Rice, 5 Indomie") {
		t.Errorf("the buyer told of the delivery: %+v", told)
	}
	if p := purchase(po); p["seller_status"] != "delivered" || p["lines"].([]any)[0].(map[string]any)["delivered"] != float64(8) || p["seller_note"] != "Musa is bringing it" {
		t.Errorf("delivered: %v", p)
	}
	tick(0) // no invoice until the receipt settles; the day's wait is not up
	before = len(w.graph.Sent(sellerOn))
	if p := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/all-came", nil)["purchase"].(map[string]any); p["status"] != "received" || p["received_kobo"] != float64(37850000) {
		t.Fatalf("all came: %v", p)
	}
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/all-came", nil)
	if items := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil)); items["Rice"]["stock"] != float64(11) || items["Indomie"]["stock"] != float64(9) {
		t.Errorf("stock: %v", items)
	}
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* says all of order "+ref+" came.") {
		t.Errorf("the seller told of the receipt: %+v", told)
	}
	tick(1) // the invoice, raised and sent
	o = orderOf(ref)
	if o["invoice_id"] == nil || o["receipt_at"] == nil {
		t.Fatalf("invoiced: %v", o)
	}
	inv := seller.must(200, "GET", "/v1/invoices/"+o["invoice_id"].(string), nil)["invoice"].(map[string]any)
	if inv["total_kobo"] != float64(37850000) || inv["sent_at"] == nil || inv["customer"].(map[string]any)["key"] != "link:"+buyerID {
		t.Fatalf("the invoice: %v", inv)
	}
	if n, err := w.srv.LinkMirrorTick(ctx); err != nil || n != 1 {
		t.Fatalf("mirrored %d: %v", n, err)
	}
	if bills := buyer.must(200, "GET", "/v1/bills", nil)["bills"].([]any); len(bills) != 1 || bills[0].(map[string]any)["amount_kobo"] != float64(37850000) {
		t.Fatalf("the bill: %v", bills)
	}

	// A cancel before acceptance cancels both sides.
	send := func(key string, lines ...map[string]any) (string, string) {
		t.Helper()
		id := draft(key, lines...)
		r := buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
		tick(1)
		return id, r
	}
	po2, ref2 := send("po-2", map[string]any{"item_id": rice, "quantity": 2})
	if out := buyer.must(200, "POST", "/v1/stock/purchases/"+po2+"/cancel", nil); out["cancel"] != "cancelled" || out["purchase"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancelled: %v", out)
	}
	if o := orderOf(ref2); o["status"] != "cancelled" {
		t.Errorf("the seller's: %v", o)
	}
	seller.must(409, "POST", "/v1/trade-orders/"+ref2+"/accept", nil)

	// After acceptance, a cancel is asked: kept, asked again, granted.
	po3, ref3 := send("po-3", map[string]any{"item_id": rice, "quantity": 2})
	seller.must(200, "POST", "/v1/trade-orders/"+ref3+"/accept", nil)
	if out := buyer.must(200, "POST", "/v1/stock/purchases/"+po3+"/cancel", nil); out["cancel"] != "asked" || out["purchase"].(map[string]any)["status"] != "confirmed" {
		t.Fatalf("asked: %v", out)
	}
	if o := orderOf(ref3); o["cancel_asked_at"] == nil || o["status"] != "accepted" {
		t.Errorf("asked, the seller's: %v", o)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref3+"/keep", map[string]any{"note": "It is on the truck"})
	if p := purchase(po3); p["cancel_asked_at"] != nil || p["seller_note"] != "It is on the truck" {
		t.Errorf("kept: %v", p)
	}
	seller.must(409, "POST", "/v1/trade-orders/"+ref3+"/keep", nil)
	buyer.must(200, "POST", "/v1/stock/purchases/"+po3+"/cancel", nil)
	before = len(w.graph.Sent(buyerOn))
	seller.must(200, "POST", "/v1/trade-orders/"+ref3+"/cancel", nil)
	if p := purchase(po3); p["status"] != "cancelled" || p["seller_status"] != "cancelled" {
		t.Errorf("granted: %v", p)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* agreed to cancel your order "+ref3) {
		t.Errorf("the buyer told of the cancel: %+v", told)
	}

	// Declined, with a reason.
	po4, ref4 := send("po-4", map[string]any{"item_id": indomie, "quantity": 1})
	seller.must(200, "POST", "/v1/trade-orders/"+ref4+"/decline", map[string]any{"note": "We do not sell single cartons"})
	if p := purchase(po4); p["status"] != "cancelled" || p["seller_status"] != "declined" || p["seller_note"] != "We do not sell single cartons" {
		t.Errorf("declined: %v", p)
	}

	// Invoiced at acceptance; a short delivery, agreed.
	addMember(t, w.world, seller, "vic", "viewer").must(403, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"invoice_at": "accepted"})
	if got := seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"invoice_at": "accepted"}); got["invoice_at"] != "accepted" {
		t.Fatalf("invoice at: %v", got)
	}
	seller.must(400, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"invoice_at": "never"})
	po5, ref5 := send("po-5", map[string]any{"item_id": rice, "quantity": 10})
	seller.must(200, "POST", "/v1/trade-orders/"+ref5+"/accept", nil)
	tick(1) // the invoice, at acceptance
	o5 := orderOf(ref5)
	if o5["invoice_id"] == nil || o5["invoice_at"] != "accepted" {
		t.Fatalf("invoiced at acceptance: %v", o5)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref5+"/delivered", map[string]any{"delivered": []int64{10}})
	buyer.must(400, "POST", "/v1/stock/purchases/"+po5+"/not-all-came", map[string]any{"counts": []int64{7, 1}})
	before = len(w.graph.Sent(sellerOn))
	buyer.must(200, "POST", "/v1/stock/purchases/"+po5+"/not-all-came", map[string]any{"counts": []int64{7}})
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* says not all of order "+ref5+" came. They counted: 7 Rice.") {
		t.Errorf("the seller told of the short count: %+v", told)
	}
	if items := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil)); items["Rice"]["stock"] != float64(18) {
		t.Errorf("stock after a short delivery: %v", items["Rice"])
	}
	if o := orderOf(ref5); o["short"] == nil || o["short"].([]any)[0] != float64(7) {
		t.Errorf("short: %v", o)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref5+"/agree", nil)
	seller.must(200, "POST", "/v1/trade-orders/"+ref5+"/agree", nil) // once
	inv5 := seller.must(200, "GET", "/v1/invoices/"+o5["invoice_id"].(string), nil)["invoice"].(map[string]any)
	if inv5["total_kobo"] != float64(29400000) { // 10 at the ₦42,000 the first receipt set, less the 3 that did not come
		t.Errorf("the invoice after the count agreed: %v", inv5["total_kobo"])
	}

	// No answer: received from the seller's count a day after.
	po6, ref6 := send("po-6", map[string]any{"item_id": indomie, "quantity": 4})
	seller.must(200, "POST", "/v1/trade-orders/"+ref6+"/accept", nil)
	tick(1) // its invoice (at acceptance)
	seller.must(200, "POST", "/v1/trade-orders/"+ref6+"/delivered", nil)
	tick(0)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_orders_due SET due_at = now() WHERE kind = 'receipt' AND object_id = $1`, po6); err != nil {
		t.Fatal(err)
	}
	tick(1)
	if p := purchase(po6); p["status"] != "received" {
		t.Errorf("received by itself: %v", p)
	}
	if o := orderOf(ref6); o["receipt_at"] == nil {
		t.Errorf("the seller's: %v", o)
	}

	// By chat: Accept on the new order, All came on its delivery; a button
	// pressed again, or by someone who may not, answers so.
	before = len(w.graph.Sent(sellerOn))
	po7, ref7 := send("po-7", map[string]any{"item_id": rice, "quantity": 1})
	asked := w.lastTo(t, sellerOn, before)
	if len(asked) != 1 || len(asked[0].Buttons) != 2 {
		t.Fatalf("the order in chat: %+v", asked)
	}
	if r := w.press(t, sellerOn, asked[0].Buttons[0].ID); len(r) != 1 || !strings.Contains(r[0].Text, "Accepted "+ref7+". They are told.") {
		t.Fatalf("accepted by chat: %+v", r)
	}
	if r := w.press(t, sellerOn, asked[0].Buttons[1].ID); len(r) != 1 || !strings.Contains(r[0].Text, "That order has moved on") {
		t.Errorf("declined after accepting: %+v", r)
	}
	if r := w.press(t, buyerOn, asked[0].Buttons[0].ID); len(r) != 1 || !strings.Contains(r[0].Text, "That order has moved on") {
		t.Errorf("the buyer pressing the seller's button: %+v", r)
	}
	tick(1) // its invoice (at acceptance)
	before = len(w.graph.Sent(buyerOn))
	seller.must(200, "POST", "/v1/trade-orders/"+ref7+"/delivered", nil)
	came := w.lastTo(t, buyerOn, before)
	if len(came) != 1 || len(came[0].Buttons) != 1 || came[0].Buttons[0].ID != "lo:all_came:"+po7 {
		t.Fatalf("the delivery in chat: %+v", came)
	}
	if r := w.press(t, buyerOn, came[0].Buttons[0].ID); len(r) != 1 || !strings.Contains(r[0].Text, "Recorded: all of "+ref7+" came.") {
		t.Fatalf("all came by chat: %+v", r)
	}
	if p := purchase(po7); p["status"] != "received" {
		t.Errorf("received by chat: %v", p)
	}

	// Each step is audited in both businesses.
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(DISTINCT tenant_id) FROM audit_log WHERE action = 'link.order.delivered' AND tenant_id IN ($1, $2)`,
		sellerID, buyerID).Scan(&n); err != nil || n != 2 {
		t.Errorf("audited in %d businesses: %v", n, err)
	}
}

// The isolation harness (§14) for L2: an order sent, written into the
// seller's business, accepted, delivered and received, its invoice raised
// and mirrored as a bill, every statement logged with its transaction's
// scope. No transaction holds both businesses, and one names the other
// only through the link functions or, past the door, in the other's scope.
func TestLinkedOrderIsolation(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	seller, buyer, sellerID, buyerID := linkedPair(t, w.waWorld)
	log := &scopeLog{scope: map[*pgx.Conn]string{}}
	cfg := w.env.Store.Pool.Config()
	cfg.ConnConfig.Tracer = log
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	w.srv.Store = &runtime.Store{Pool: traced, Registry: w.env.Store.Registry, PII: w.env.Store.PII}

	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4501"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	log.take()

	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": rice, "quantity": 4}}},
		"Idempotency-Key", "po")["purchase"].(map[string]any)["id"].(string)
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	for _, step := range []func(){
		func() { _, _ = w.srv.LinkOrdersTick(ctx) },
		func() { seller.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil) },
		func() { seller.must(200, "POST", "/v1/trade-orders/"+ref+"/delivered", nil) },
		func() { buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/all-came", nil) },
		func() { _, _ = w.srv.LinkOrdersTick(ctx) },
		func() { _, _ = w.srv.LinkMirrorTick(ctx) },
	} {
		step()
		w.srv.WaitBackground()
	}
	if bills := buyer.must(200, "GET", "/v1/bills", nil)["bills"].([]any); len(bills) != 1 {
		t.Fatalf("the bill: %v", bills)
	}
	ofEach := checkIsolation(t, log.take(), sellerID, buyerID)
	if ofEach[sellerID] == 0 || ofEach[buyerID] == 0 {
		t.Errorf("statements by business: %v", ofEach)
	}
}

// The rest of L2 (§7, §17): a new order's lines are matched to the
// seller's own items, by name to confirm, then by the match it confirmed,
// taking its prices; accepting lowers the seller's stock and gives the
// buyer's order the agreed prices; orders a link set to accept by
// themselves arrive accepted; and an order the seller drafts for the
// buyer waits for the buyer's Confirm, saying which of its items each
// line is, or its cancel.
func TestLinkedOrdersMatchedAndPlaced(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const sellerOn, buyerOn = "+2348010000371", "+2348010000372"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	tick := func(want int) {
		t.Helper()
		n, err := w.srv.LinkOrdersTick(ctx)
		if err != nil || n != want {
			t.Fatalf("did %d, want %d: %v", n, want, err)
		}
		w.srv.WaitBackground()
	}
	// Both keep stock; the seller sells Rice at ₦45,000.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "rice", "quantity": 50, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sGarri := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Garri", "quantity": 20, "unit": "bag", "price_kobo": 1500000},
		"Idempotency-Key", "sg")["item"].(map[string]any)["id"].(string)
	sellerStock := func(name string) any {
		t.Helper()
		return itemsByName(t, seller.must(200, "GET", "/v1/stock", nil))[name]["stock"]
	}
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4502"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	send := func(key string, n int) (string, string) {
		t.Helper()
		id := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": rice, "quantity": n}}},
			"Idempotency-Key", key)["purchase"].(map[string]any)["id"].(string)
		ref := buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
		tick(1)
		return id, ref
	}
	orderOf := func(ref string) map[string]any {
		t.Helper()
		return seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	}

	// Matched by name, at the seller's price; the seller confirms it.
	po, ref := send("m-1", 10)
	o := orderOf(ref)
	l0 := o["lines"].([]any)[0].(map[string]any)
	if l0["item_id"] != sRice || l0["matched"] != "name" || l0["price_kobo"] != float64(4500000) || o["total_kobo"] != float64(45000000) {
		t.Fatalf("matched by name: %v", o)
	}
	seller.must(400, "PUT", "/v1/trade-orders/"+ref+"/lines/0/item", map[string]any{"item_id": "nope"})
	if o := seller.must(200, "PUT", "/v1/trade-orders/"+ref+"/lines/0/item", map[string]any{"item_id": sRice})["order"].(map[string]any); o["lines"].([]any)[0].(map[string]any)["matched"] != "linked" {
		t.Fatalf("confirmed: %v", o)
	}
	// Accepted: the seller's stock is lowered, the buyer's order takes its price.
	if out := seller.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil); len(out["short"].([]any)) != 0 {
		t.Fatalf("accepted: %v", out)
	}
	seller.must(409, "PUT", "/v1/trade-orders/"+ref+"/lines/0/item", map[string]any{"item_id": sRice})
	if got := sellerStock("rice"); got != float64(40) {
		t.Errorf("the seller's stock after accepting: %v", got)
	}
	if p := buyer.must(200, "GET", "/v1/stock/purchases/"+po, nil)["purchase"].(map[string]any); p["total_kobo"] != float64(45000000) ||
		p["lines"].([]any)[0].(map[string]any)["cost_kobo"] != float64(4500000) {
		t.Errorf("the buyer's order at the agreed price: %v", p)
	}

	// Accepted by itself, as the seller sets for the link.
	if got := seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"auto_accept": true}); got["auto_accept"] != true || got["invoice_at"] != "delivered" {
		t.Fatalf("auto accept: %v", got)
	}
	before := len(w.graph.Sent(sellerOn))
	po2, ref2 := send("m-2", 5)
	if o := orderOf(ref2); o["status"] != "accepted" || o["lines"].([]any)[0].(map[string]any)["matched"] != "linked" {
		t.Fatalf("accepted by itself: %v", o)
	}
	if p := buyer.must(200, "GET", "/v1/stock/purchases/"+po2, nil)["purchase"].(map[string]any); p["status"] != "confirmed" || p["seller_status"] != "accepted" {
		t.Errorf("the buyer's, accepted: %v", p)
	}
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* ordered 5 bags Rice, ₦225,000. It is accepted, as you set for them.") ||
		len(told[0].Buttons) != 0 {
		t.Errorf("the seller told: %+v", told)
	}
	if got := sellerStock("rice"); got != float64(35) {
		t.Errorf("the seller's stock after accepting by itself: %v", got)
	}

	// An order the seller drafts for the buyer: Confirm, saying which of
	// the buyer's items each line is.
	before = len(w.graph.Sent(buyerOn))
	placed := seller.must(201, "POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item_id": sRice, "quantity": 4}},
		"note": "As we said on the phone"})["order"].(map[string]any)
	ref3 := placed["reference"].(string)
	if placed["status"] != "proposed" || placed["placed_by_seller"] != true || placed["total_kobo"] != float64(18000000) {
		t.Fatalf("placed: %v", placed)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* drafted an order for you: 4 bags rice, ₦180,000.") {
		t.Errorf("the buyer told: %+v", told)
	}
	var po3 string
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		if p := x.(map[string]any); p["reference"] == ref3 {
			po3 = p["id"].(string)
			if p["status"] != "drafted" || p["seller_status"] != "proposed" {
				t.Errorf("the buyer's proposed order: %v", p)
			}
		}
	}
	if po3 == "" {
		t.Fatal("no proposed order on the buyer's side")
	}
	buyer.must(400, "POST", "/v1/stock/purchases/"+po3+"/send", nil)
	buyer.must(400, "POST", "/v1/stock/purchases/"+po3+"/confirm-proposed", map[string]any{"items": []string{"nope"}})
	if p := buyer.must(200, "POST", "/v1/stock/purchases/"+po3+"/confirm-proposed", map[string]any{"items": []string{rice}})["purchase"].(map[string]any); p["status"] != "confirmed" ||
		p["lines"].([]any)[0].(map[string]any)["item_id"] != rice || p["lines"].([]any)[0].(map[string]any)["item"] != "Rice" {
		t.Fatalf("confirmed: %v", p)
	}
	buyer.must(409, "POST", "/v1/stock/purchases/"+po3+"/confirm-proposed", map[string]any{"items": []string{rice}})
	if o := orderOf(ref3); o["status"] != "accepted" {
		t.Errorf("the seller's, confirmed: %v", o)
	}
	if got := sellerStock("rice"); got != float64(31) {
		t.Errorf("the seller's stock after the buyer confirmed: %v", got)
	}

	// Another, cancelled by the buyer; the seller's withdrawn; and none
	// placed for a buyer that has not marked the seller as its supplier.
	placed = seller.must(201, "POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item_id": sGarri, "quantity": 2}}})["order"].(map[string]any)
	var po4 string
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		if p := x.(map[string]any); p["reference"] == placed["reference"] {
			po4 = p["id"].(string)
		}
	}
	if out := buyer.must(200, "POST", "/v1/stock/purchases/"+po4+"/cancel", nil); out["cancel"] != "cancelled" {
		t.Fatalf("cancelled: %v", out)
	}
	if o := orderOf(placed["reference"].(string)); o["status"] != "cancelled" {
		t.Errorf("the seller's, cancelled: %v", o)
	}
	placed = seller.must(201, "POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item": "Palm oil", "quantity": 1, "price_kobo": 3000000}}})["order"].(map[string]any)
	seller.must(200, "POST", "/v1/trade-orders/"+placed["reference"].(string)+"/cancel", nil)
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		if p := x.(map[string]any); p["reference"] == placed["reference"] && (p["status"] != "cancelled" || p["seller_status"] != "cancelled") {
			t.Errorf("withdrawn: %v", p)
		}
	}
	seller.must(400, "POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item": "Palm oil", "quantity": 1}}})
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": nil})
	seller.must(409, "POST", "/v1/trade-orders", map[string]any{"link": sellerLink, "lines": []any{map[string]any{"item_id": sGarri, "quantity": 1}}})
}
