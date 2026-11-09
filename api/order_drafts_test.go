package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/orders/read"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/voice"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Orders from customers' messages (chat-five-more-flows.md, phase 2): a
// customer's message is read against the business's catalogue into a draft
// order on a card in their thread; what is unclear is a question, never a
// guess; staff ask the customer, whose answer is read onto it; a person
// confirms it, and it becomes an order of the kind chat; the customer gets
// the order, its total and how to pay, then word when it is paid.
func TestOrdersFromCustomersMessages(t *testing.T) {
	w := newWATablesWorld(t)
	w.srv.PublicHosts = []string{shopDomain}
	ctx := context.Background()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Mama Put Kitchen", "owner@mamaput.test")
	alphaID := uuid.MustParse(alpha.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	verifyPhone(t, w.world, alpha, "phone", "business")
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	alpha.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	cashier := w.member(t, alpha, "cashier@mamaput.test", "cashier")
	viewer := w.member(t, alpha, "viewer@mamaput.test", "viewer")
	beta := w.tenant(t, "Other Shop", "owner@other.test")

	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, PreviewDomain: shopDomain,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	w.srv.Orders.ChatNotify = w.srv.ChatNotify
	w.srv.OrderReadInline = true

	// The business's page and catalogue.
	alpha.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-put", "draft": map[string]any{"name": "Mama Put Kitchen"}})
	table := alpha.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"})["table_id"].(string)
	for _, it := range []map[string]any{
		{"name": "Jollof rice plate", "price": 4500, "available": true},
		{"name": "Jollof rice cooler", "price": 38000, "available": true},
		{"name": "Chapman", "price": 1500, "available": true},
	} {
		alpha.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": it})
	}
	ids := map[string]string{}
	for _, it := range alpha.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)["items"].([]any) {
		it := it.(map[string]any)
		ids[it["name"].(string)] = it["id"].(string)
	}
	jp, jc, ch := ids["Jollof rice plate"], ids["Jollof rice cooler"], ids["Chapman"]
	if jp == "" || jc == "" || ch == "" {
		t.Fatalf("catalogue: %v", ids)
	}

	// The model reads as the fake says, by the customer's words.
	reads := map[string]read.Said{
		"2 jolof and 1 chapman, deliver to 12 Herbert Macaulay Yaba": {Orderable: true, Lines: []read.SaidLine{
			{Words: "2 jolof", Quantity: 2}, {Words: "1 chapman", Item: "Chapman", Quantity: 1}},
			Fulfilment: "delivery", Address: "12 Herbert Macaulay Yaba"},
		"the plate": {Orderable: true, Answers: []read.SaidAnswer{{Question: 1, Answer: "Jollof rice plate"}}},
	}
	model := &ai.Fake{Respond: func(r ai.Request) (*ai.Response, error) {
		msg := r.Messages[len(r.Messages)-1].Text
		for words, said := range reads {
			if strings.HasSuffix(msg, words) {
				raw, _ := json.Marshal(said)
				return &ai.Response{JSON: raw}, nil
			}
		}
		return &ai.Response{JSON: json.RawMessage(`{"orderable":false,"lines":[],"fulfilment":"","address":"","when":"","note":"","answers":[]}`)}, nil
	}}
	w.srv.AI = &api.AISettings{Provider: model}

	const customer = "+2348031112222"
	say := func(text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, customer, whatsapptest.Text(text))
	}
	sent := func() []whatsapptest.Sent { return w.graph.Sent(customer) }

	// A greeting names nothing sold: no model, no order.
	say("Good afternoon")
	if n := len(model.Requests()); n != 0 {
		t.Fatalf("the model was asked about a greeting: %d", n)
	}
	chat := cashier.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)[0].(map[string]any)["id"].(string)
	orders := func() []any {
		return cashier.must(200, "GET", "/v1/customer-chats/"+chat+"/orders", nil)["orders"].([]any)
	}
	if n := len(orders()); n != 0 {
		t.Fatalf("orders: %d", n)
	}

	// An order: read into a card, nothing said to the customer; "jolof"
	// fits two dishes, so it is a question, never a pick.
	if got := say("2 jolof and 1 chapman, deliver to 12 Herbert Macaulay Yaba"); len(got) != 0 {
		t.Fatalf("the customer was answered: %+v", got)
	}
	list := cashier.must(200, "GET", "/v1/customer-chats/"+chat+"/orders", nil)
	if list["pay_page"] != true {
		t.Fatalf("pay page: %v", list["pay_page"])
	}
	os := list["orders"].([]any)
	if len(os) != 1 {
		t.Fatalf("orders: %s", toJSON(os))
	}
	d := os[0].(map[string]any)
	id := d["id"].(string)
	lines := d["lines"].([]any)
	l0, l1 := lines[0].(map[string]any), lines[1].(map[string]any)
	if d["status"] != "open" || d["by_reader"] != true || d["questions"] != float64(1) || d["ready"] != false || d["fulfilment"] != "delivery" ||
		l0["ask"].(map[string]any)["kind"] != "which" || l0["item_id"] != nil || l1["item_id"] != ch || l1["price_kobo"] != float64(150000) {
		t.Fatalf("draft: %s", toJSON(d))
	}
	// Sealed: the address and words are not stored in the clear.
	var raw string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(body::text, ' ') FROM order_drafts`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "Herbert") || strings.Contains(raw, "jolof") {
		t.Fatalf("stored in the clear: %s", raw)
	}
	// On Needs you, for those who handle orders.
	var found bool
	for _, a := range alpha.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		a := a.(map[string]any)
		if a["kind"] == "order_draft" {
			found = true
			if a["title"] != "Order from Someone" || a["line"] != "2 items · ₦1,500 · 1 question" || a["id"] != chat {
				t.Fatalf("alert: %s", toJSON(a))
			}
		}
	}
	if !found {
		t.Fatal("no Needs you card")
	}
	// Only those who handle orders; another business sees nothing.
	viewer.must(403, "GET", "/v1/customer-chats/"+chat+"/orders", nil)
	viewer.must(403, "POST", "/v1/order-drafts/"+id+"/confirm", map[string]any{"version": 1})
	beta.must(404, "GET", "/v1/customer-chats/"+chat+"/orders", nil)
	beta.must(404, "POST", "/v1/order-drafts/"+id+"/confirm", map[string]any{"version": 1})
	// Not ready: refused, with why.
	if e := cashier.must(400, "POST", "/v1/order-drafts/"+id+"/confirm", map[string]any{"version": d["version"]}); !strings.Contains(e["error"].(string), "questions") {
		t.Fatalf("confirm early: %v", e)
	}

	// The question goes to the customer as one message.
	before := len(sent())
	d = cashier.must(200, "POST", "/v1/order-drafts/"+id+"/ask", map[string]any{"version": d["version"]})
	asked := sent()[before:]
	if len(asked) != 1 || !strings.Contains(asked[0].Text, "To get your order right:") ||
		!strings.Contains(asked[0].Text, "2 jolof: Jollof rice plate (₦4,500) or Jollof rice cooler (₦38,000)?") || d["status"] != "asked" {
		t.Fatalf("asked: %+v %s", asked, toJSON(d))
	}
	// Their answer is read onto it, and it is back with staff.
	say("the plate")
	d = orders()[0].(map[string]any)
	if d["status"] != "open" || d["questions"] != float64(0) || d["lines"].([]any)[0].(map[string]any)["item_id"] != jp {
		t.Fatalf("answered: %s", toJSON(d))
	}
	// Staff set the delivery fee and how it is paid; a change on an old
	// version is refused.
	cashier.must(409, "PUT", "/v1/order-drafts/"+id, map[string]any{"version": 1, "op": "quantity", "line": 1, "quantity": 2})
	fee := int64(150000)
	d = cashier.must(200, "PUT", "/v1/order-drafts/"+id, map[string]any{"version": d["version"], "op": "details", "delivery_kobo": fee, "pay": "transfer"})
	if d["total_kobo"] != float64(2*450000+150000+fee) || d["ready"] != true {
		t.Fatalf("details: %s", toJSON(d))
	}
	cashier.must(400, "PUT", "/v1/order-drafts/"+id, map[string]any{"version": d["version"], "op": "choose", "line": 0, "item_id": "shawarma"})

	// Confirmed by a person: an order of the kind chat, and the customer
	// gets it, its total and the page to pay on.
	before = len(sent())
	out := cashier.must(200, "POST", "/v1/order-drafts/"+id+"/confirm", map[string]any{"version": d["version"]})
	o := out["order"].(map[string]any)
	ref := o["reference"].(string)
	if out["told"] != true || o["status"] != "confirmed" || o["order_status"] != "awaiting_payment" || !strings.HasPrefix(ref, "MA-") {
		t.Fatalf("confirmed: %s", toJSON(out))
	}
	told := sent()[before:]
	if len(told) != 1 {
		t.Fatalf("told: %+v", told)
	}
	for _, want := range []string{"Your order " + ref + ":", "2 × Jollof rice plate  ₦9,000", "1 × Chapman  ₦1,500", "Delivery  ₦1,500", "Total  ₦12,000",
		"https://mama-put." + shopDomain + "/o/" + ref + "?k="} {
		if !strings.Contains(told[0].Text, want) {
			t.Fatalf("told lacks %q: %q", want, told[0].Text)
		}
	}
	var kind, status, decided string
	var total int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind, status, total_kobo, decided_by FROM storefront_orders WHERE reference = $1`, ref).Scan(&kind, &status, &total, &decided); err != nil {
		t.Fatal(err)
	}
	if kind != "chat" || status != "awaiting_payment" || total != 1200000 || decided == "" {
		t.Fatalf("order: %s %s %d %s", kind, status, total, decided)
	}
	// Confirmed twice: one order, told once.
	cashier.must(200, "POST", "/v1/order-drafts/"+id+"/confirm", map[string]any{"version": d["version"]})
	cashier.must(200, "POST", "/v1/order-drafts/"+id+"/tell", nil)
	if n := len(sent()) - before; n != 1 {
		t.Fatalf("told %d times", n)
	}
	cashier.must(409, "PUT", "/v1/order-drafts/"+id, map[string]any{"version": d["version"], "op": "remove", "line": 0})

	// Paid (as the matcher counts it): paid, and the customer is told.
	before = len(sent())
	var counted storefront.Counted
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{alphaID}, func(tx pgx.Tx) error {
		var err error
		counted, err = w.srv.Orders.CountPayment(ctx, tx, alphaID, ref, storefront.PaymentIn{AmountKobo: total, How: "manual", By: "test"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.Orders.Paid(ctx, alphaID, counted); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || status != "paid" {
		t.Fatalf("paid: %s %v", status, err)
	}
	if paid := sent()[before:]; len(paid) != 1 || !strings.Contains(paid[0].Text, "Paid, thank you: "+ref) {
		t.Fatalf("paid told: %+v", paid)
	}

	// An order started by hand, then dropped; a second one waits.
	h := cashier.must(201, "POST", "/v1/customer-chats/"+chat+"/orders", nil)
	cashier.must(409, "POST", "/v1/customer-chats/"+chat+"/orders", nil)
	h = cashier.must(200, "PUT", "/v1/order-drafts/"+h["id"].(string), map[string]any{"version": h["version"], "op": "add", "item_id": jc, "quantity": 1})
	if h["lines"].([]any)[0].(map[string]any)["name"] != "Jollof rice cooler" || h["total_kobo"] != float64(3800000) {
		t.Fatalf("by hand: %s", toJSON(h))
	}
	cashier.must(200, "POST", "/v1/order-drafts/"+h["id"].(string)+"/drop", map[string]any{"version": h["version"]})

	// A voice note, where voice notes are on: put in words in the thread,
	// the recording not kept, and read for an order like typed words.
	w.srv.Voice = &api.VoiceSettings{Provider: &voice.Fake{Text: "2 chapman please"},
		Limits: voice.Limits{MaxBytes: 64 << 10, MaxDuration: 30 * time.Second}, Retention: time.Hour}
	alpha.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "voice_notes": true})
	reads["2 chapman please"] = read.Said{Orderable: true, Lines: []read.SaidLine{{Words: "2 chapman", Item: "Chapman", Quantity: 2}}}
	w.graph.AddMedia("v-1", "audio/ogg; codecs=opus", voice.TestOgg(3, 100), 0)
	if got := w.sendTo(t, "/channels/whatsapp", pnA, waSecret, customer, whatsapptest.Audio("v-1", "audio/ogg; codecs=opus", true)); len(got) != 0 {
		t.Fatalf("the voice note was answered: %+v", got)
	}
	msgs := cashier.must(200, "GET", "/v1/customer-chats/"+chat+"/messages", nil)["messages"].([]any)
	if last := msgs[len(msgs)-1].(map[string]any); last["body"] != "Voice note: 2 chapman please" || last["kind"] != "media" {
		t.Fatalf("voice note: %s", toJSON(last))
	}
	os = orders()
	v := os[len(os)-1].(map[string]any)
	if v["status"] != "open" || v["lines"].([]any)[0].(map[string]any)["item_id"] != ch || v["lines"].([]any)[0].(map[string]any)["quantity"] != float64(2) {
		t.Fatalf("voice order: %s", toJSON(v))
	}

	// STOP: the thread's orders being read go; the order stays.
	say("STOP")
	var drafts, kept int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_drafts), (SELECT count(*) FROM storefront_orders WHERE reference = $1)`, ref).Scan(&drafts, &kept); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 || kept != 1 {
		t.Fatalf("after STOP: %d drafts, %d orders", drafts, kept)
	}
}
