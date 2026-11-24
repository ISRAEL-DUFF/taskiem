package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Customers' conversations (chat-five-more-flows.md, phase 1): a customer
// writing to the business's own number is a thread in the chat, sealed;
// staff who handle orders read and answer it; the answer goes from the
// business's number, as words within 24 hours of the customer's last
// message and as the approved template after; STOP deletes the thread's
// messages and stops replies until START.
func TestCustomerConversations(t *testing.T) {
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Mama Put Kitchen", "owner@mamaput.test")
	const waba = "300000000000001"
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": waba, "access_token": tokA, "own_app": false})
	cashier := w.member(t, alpha, "cashier@mamaput.test", "cashier")
	viewer := w.member(t, alpha, "viewer@mamaput.test", "viewer")

	const customer = "+2348031112222"
	say := func(text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, customer, whatsapptest.Text(text))
	}
	// A customer's message: no "link your account", no reply at all; a
	// thread for the business.
	if got := say("Do you deliver to Yaba? I want 2 plates of jollof"); len(got) != 0 {
		t.Fatalf("the customer was answered: %+v", got)
	}
	chats := cashier.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(chats) != 1 {
		t.Fatalf("chats: %s", toJSON(chats))
	}
	c := chats[0].(map[string]any)
	id := c["id"].(string)
	if c["phone"] != customer || c["name"] != "Someone" || !strings.Contains(c["last"].(string), "Yaba") || c["unread"] != float64(1) ||
		c["window_open"] != true || c["stopped"] != false || c["last_mine"] != false {
		t.Fatalf("chat: %s", toJSON(c))
	}
	// Sealed: neither the words nor the number are stored in the clear.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT (SELECT string_agg(body::text, ' ') FROM customer_messages) || (SELECT string_agg(who::text, ' ') FROM customer_conversations)`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "Yaba") || strings.Contains(raw, "8031112222") {
		t.Fatalf("stored in the clear: %s", raw)
	}
	// Only those who handle orders.
	viewer.must(403, "GET", "/v1/customer-chats", nil)
	viewer.must(403, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "hi"})

	// A reply goes from the business's own number, as words.
	before := len(w.graph.Sent(customer))
	m := cashier.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Yes, Yaba is ₦1,500.", "client_id": "r1"})
	if m["sent_as"] != "text" || m["mine"] != true || m["sender_name"] == "" {
		t.Fatalf("reply: %s", toJSON(m))
	}
	sent := w.graph.Sent(customer)[before:]
	if len(sent) != 1 || sent[0].From != pnA || sent[0].Type != "text" || sent[0].Text != "Yes, Yaba is ₦1,500." {
		t.Fatalf("sent: %+v", sent)
	}
	// The same send again is the same message, not a second one.
	again := cashier.must(200, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Yes, Yaba is ₦1,500.", "client_id": "r1"})
	if again["id"] != m["id"] || len(w.graph.Sent(customer)) != before+1 {
		t.Fatalf("repeat sent again: %s", toJSON(again))
	}
	msgs := cashier.must(200, "GET", "/v1/customer-chats/"+id+"/messages", nil)["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["mine"] != false || msgs[1].(map[string]any)["body"] != "Yes, Yaba is ₦1,500." {
		t.Fatalf("messages: %s", toJSON(msgs))
	}
	cashier.must(204, "POST", "/v1/customer-chats/"+id+"/read", nil)
	if c := cashier.must(200, "GET", "/v1/customer-chats/"+id, nil); c["unread"] != float64(0) || c["owes_kobo"] != float64(0) {
		t.Fatalf("after reading: %s", toJSON(c))
	}
	cashier.must(400, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "  "})
	cashier.must(404, "GET", "/v1/customer-chats/00000000-0000-7000-8000-000000000000", nil)

	// A day on: only the approved template reaches them; until it is
	// approved, the reply is refused and nothing goes.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE customer_conversations SET last_inbound_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_own_contacts SET last_inbound_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if c := cashier.must(200, "GET", "/v1/customer-chats/"+id, nil); c["window_open"] != false {
		t.Fatalf("window: %s", toJSON(c))
	}
	setTemplate := func(status string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO whatsapp_templates (waba_id, name, language, status) VALUES ($1, 'taskiem_customer_reply', 'en', $2)
			ON CONFLICT (waba_id, name, language) DO UPDATE SET status = EXCLUDED.status`, waba, status); err != nil {
			t.Fatal(err)
		}
	}
	setTemplate("pending")
	before = len(w.graph.Sent(customer))
	cashier.must(409, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Are you still there?"})
	if len(w.graph.Sent(customer)) != before {
		t.Fatal("sent outside the window without an approved template")
	}
	setTemplate("approved")
	m = cashier.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Are you still there?"})
	sent = w.graph.Sent(customer)[before:]
	if m["sent_as"] != "template" || len(sent) != 1 || sent[0].Template != "taskiem_customer_reply" || !strings.Contains(sent[0].Text, "[Mama Put Kitchen] Reply to your message: Are you still there?") {
		t.Fatalf("template: %s %+v", toJSON(m), sent)
	}

	// STOP: the thread's messages are deleted, and nothing more is sent.
	got := say("STOP")
	if len(got) != 1 || !strings.Contains(got[0].Text, "no longer get messages from Mama Put Kitchen") {
		t.Fatalf("stop: %+v", got)
	}
	if n := len(cashier.must(200, "GET", "/v1/customer-chats/"+id+"/messages", nil)["messages"].([]any)); n != 0 {
		t.Fatalf("%d messages kept after STOP", n)
	}
	if c := cashier.must(200, "GET", "/v1/customer-chats/"+id, nil); c["stopped"] != true || c["window_open"] != false {
		t.Fatalf("stopped: %s", toJSON(c))
	}
	before = len(w.graph.Sent(customer))
	cashier.must(409, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Sorry to see you go"})
	if len(w.graph.Sent(customer)) != before {
		t.Fatal("sent after STOP")
	}
	// START: they can write, and be answered, again.
	if got := say("start"); len(got) != 1 || !strings.Contains(got[0].Text, "write to Mama Put Kitchen here again") {
		t.Fatalf("start: %+v", got)
	}
	say("Hello again")
	cashier.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Welcome back"})

	// Archived, it leaves the list; a new message brings it back.
	cashier.must(204, "PUT", "/v1/customer-chats/"+id, map[string]any{"archived": true})
	if n := len(cashier.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)); n != 0 {
		t.Fatalf("%d chats after archiving", n)
	}
	if n := len(cashier.must(200, "GET", "/v1/customer-chats?archived=1", nil)["chats"].([]any)); n != 1 {
		t.Fatalf("%d archived", n)
	}
	say("One more thing")
	if n := len(cashier.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)); n != 1 {
		t.Fatalf("%d chats after a new message", n)
	}

	// Another business sees none of it.
	beta := w.tenant(t, "Beta", "owner@beta.test")
	if n := len(beta.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)); n != 0 {
		t.Fatalf("beta sees %d chats", n)
	}
	beta.must(404, "GET", "/v1/customer-chats/"+id, nil)
	beta.must(404, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "hi"})
	// The shared number keeps telling a stranger how to link.
	if got := w.sendTo(t, "/channels/whatsapp", waPNID, waSecret, "+2348031113333", whatsapptest.Text("hello")); len(got) != 1 || !strings.Contains(got[0].Text, "not linked") {
		t.Fatalf("shared number: %+v", got)
	}

	// Search: in names, numbers and the latest messages.
	found := cashier.must(200, "GET", "/v1/customer-chats/search?q=HELLO%20again", nil)
	if n := len(found["messages"].([]any)); n < 1 || !strings.Contains(toJSON(found["messages"]), "Hello again") {
		t.Fatalf("search: %s", toJSON(found))
	}
	if n := len(cashier.must(200, "GET", "/v1/customer-chats/search?q=8031112", nil)["chats"].([]any)); n != 1 {
		t.Fatalf("search by number: %d", n)
	}
	cashier.must(400, "GET", "/v1/customer-chats/search?q=y", nil)
	viewer.must(403, "GET", "/v1/customer-chats/search?q=yaba", nil)
}

// What the review of phase 1 found: riders and suppliers are not made
// customers; START is only for a thread that was stopped; WhatsApp's
// word on a reply (delivered, read, failed) is kept; a late reply too
// long for the template is refused, not cut; the unread count; a thread
// follows the business to a new number; messages past their year go.
func TestCustomerConversationsEdges(t *testing.T) {
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.graph.AddNumber(pnB, tokB, "+234 800 000 0002")
	alpha := w.tenant(t, "Mama Put Kitchen", "owner@mamaput.test")
	const wabaA, wabaB = "300000000000001", "300000000000002"
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": wabaA, "access_token": tokA, "own_app": false})
	ctx := context.Background()
	admin := w.env.DB.Admin
	say := func(pnid, from, text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnid, waSecret, from, whatsapptest.Text(text))
	}
	chats := func() []any { return alpha.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) }

	// A rider's free words are not a customer's (riders and suppliers are
	// known by their numbers' keyed hashes, through the reach book).
	w.srv.Reach = &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: w.srv.Reach, PageBase: "https://" + reachFormHost}
	const rider = "+2348031234888"
	alpha.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": rider, "zones": []string{"Yaba"}, "fee_kobo": 100000})
	say(pnA, rider, "I am at the gate")
	if n := len(chats()); n != 0 {
		t.Fatalf("a rider made a customer: %d chats", n)
	}

	// START from a customer who never said STOP: not the thread's to answer.
	const customer = "+2348031112222"
	say(pnA, customer, "Hello, are you open today?")
	if got := say(pnA, customer, "START"); len(got) == 1 && strings.Contains(got[0].Text, "write to Mama Put Kitchen here again") {
		t.Fatalf("START answered for a thread never stopped: %+v", got)
	}
	cs := chats()
	if len(cs) != 1 {
		t.Fatalf("chats: %s", toJSON(cs))
	}
	id := cs[0].(map[string]any)["id"].(string)
	if u := alpha.must(200, "GET", "/v1/customer-chats/unread", nil); u["chats"] != float64(1) || u["messages"].(float64) < 1 {
		t.Fatalf("unread: %v", u)
	}

	// WhatsApp's word on a reply.
	alpha.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Yes, till 9pm"})
	var wamid string
	if err := admin.QueryRow(ctx, `SELECT wa_id FROM customer_messages WHERE mine ORDER BY created_at DESC LIMIT 1`).Scan(&wamid); err != nil || wamid == "" {
		t.Fatalf("the reply's id: %q %v", wamid, err)
	}
	status := func(st string) {
		t.Helper()
		body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"WABA","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",` +
			`"metadata":{"display_phone_number":"2348000000001","phone_number_id":"` + pnA + `"},"statuses":[{"id":"` + wamid + `","status":"` + st + `","recipient_id":"2348031112222"}]}}]}]}`)
		if code, raw := w.post(t, "/channels/whatsapp", body, whatsapp.Sign(waSecret, body)); code != 200 {
			t.Fatalf("status: %d %s", code, raw)
		}
	}
	delivery := func() string {
		msgs := alpha.must(200, "GET", "/v1/customer-chats/"+id+"/messages", nil)["messages"].([]any)
		return msgs[len(msgs)-1].(map[string]any)["delivery"].(string)
	}
	if d := delivery(); d != "sent" {
		t.Fatalf("after sending: %q", d)
	}
	status("delivered")
	status("read")
	status("delivered") // late: never back
	if d := delivery(); d != "read" {
		t.Fatalf("after read: %q", d)
	}
	// One WhatsApp could not deliver: failed, with the reason.
	alpha.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Your order is ready"})
	if err := admin.QueryRow(ctx, `SELECT wa_id FROM customer_messages WHERE mine ORDER BY created_at DESC LIMIT 1`).Scan(&wamid); err != nil {
		t.Fatal(err)
	}
	failed := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"WABA","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",` +
		`"metadata":{"display_phone_number":"2348000000001","phone_number_id":"` + pnA + `"},"statuses":[{"id":"` + wamid + `","status":"failed","recipient_id":"2348031112222",` +
		`"errors":[{"code":131026,"title":"Message undeliverable"}]}]}}]}]}`)
	if code, raw := w.post(t, "/channels/whatsapp", failed, whatsapp.Sign(waSecret, failed)); code != 200 {
		t.Fatalf("failed status: %d %s", code, raw)
	}
	msgs := alpha.must(200, "GET", "/v1/customer-chats/"+id+"/messages", nil)["messages"].([]any)
	if last := msgs[len(msgs)-1].(map[string]any); last["delivery"] != "failed" || last["failure"] != "Message undeliverable" {
		t.Fatalf("failed: %s", toJSON(last))
	}
	status("read") // a failed reply is not read later
	if d := delivery(); d != "failed" {
		t.Fatalf("after failed: %q", d)
	}

	// A day on: a reply too long for the template is refused, not cut.
	if _, err := admin.Exec(ctx, `UPDATE whatsapp_own_contacts SET last_inbound_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO whatsapp_templates (waba_id, name, language, status) VALUES ($1, 'taskiem_customer_reply', 'en', 'approved'), ($2, 'taskiem_customer_reply', 'en', 'approved')
		ON CONFLICT (waba_id, name, language) DO UPDATE SET status = 'approved'`, wabaA, wabaB); err != nil {
		t.Fatal(err)
	}
	before := len(w.graph.Sent(customer))
	alpha.must(400, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": strings.Repeat("Our menu today is long. ", 40)})
	if len(w.graph.Sent(customer)) != before {
		t.Fatal("a long reply went after the 24 hours")
	}
	if m := alpha.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "Still there?"}); m["sent_as"] != "template" {
		t.Fatalf("late reply: %s", toJSON(m))
	}

	// The business moves to another number: the thread follows, and the
	// reply goes from the new number.
	alpha.must(204, "DELETE", "/v1/whatsapp/number", nil)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnB, "waba_id": wabaB, "access_token": tokB, "own_app": false})
	before = len(w.graph.Sent(customer))
	alpha.must(201, "POST", "/v1/customer-chats/"+id+"/messages", map[string]any{"text": "We have a new number"})
	sent := w.graph.Sent(customer)[before:]
	if len(sent) != 1 || sent[0].From != pnB {
		t.Fatalf("from the new number: %+v", sent)
	}
	if got := say(pnB, customer, "Thanks"); len(got) != 0 {
		t.Fatalf("answered: %+v", got)
	}
	if n := len(chats()); n != 1 {
		t.Fatalf("the customer's thread is split: %d", n)
	}

	// Messages past their year are deleted with the chat history's.
	if _, err := admin.Exec(ctx, `UPDATE customer_messages SET expires_at = now() - interval '1 second' WHERE NOT mine`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.Store.Pool.Exec(ctx, `SELECT taskiem_customer_messages_purge()`); err != nil {
		t.Fatal(err)
	}
	for _, m := range alpha.must(200, "GET", "/v1/customer-chats/"+id+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["mine"] == false {
			t.Fatalf("an expired message is kept: %s", toJSON(m))
		}
	}
}
