package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// One customer across the app and the business's own WhatsApp number
// (taskiem-for-everyone §11, §5.5 item 4; stage 4).

const linesWABA = "300000000000077"

// withOwnNumber gives the door world's business its own WhatsApp number.
func (w *doorShopWorld) withOwnNumber(t *testing.T) {
	t.Helper()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.owner.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": linesWABA, "access_token": tokA, "own_app": false})
}

// onWhatsApp is a message from number to the business's own number.
func (w *doorShopWorld) onWhatsApp(t *testing.T, number, text string) []whatsapptest.Sent {
	t.Helper()
	return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, number, whatsapptest.Text(text))
}

func lineNames(v map[string]any) []string {
	var out []string
	for _, l := range v["lines"].([]any) {
		switch x := l.(type) {
		case string:
			out = append(out, x)
		case map[string]any:
			out = append(out, x["line"].(string))
		}
	}
	return out
}

func lineState(v map[string]any, line string) map[string]any {
	for _, l := range v["lines"].([]any) {
		if x := l.(map[string]any); x["line"] == line {
			return x
		}
	}
	return nil
}

func (w *doorShopWorld) linkedTo(t *testing.T, waThread string) *uuid.UUID {
	t.Helper()
	var app *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT linked_to FROM customer_conversations WHERE id = $1`, waThread).Scan(&app); err != nil {
		t.Fatal(err)
	}
	return app
}

// whatsappThread is the business's WhatsApp thread of a number.
func (w *doorShopWorld) whatsappThread(t *testing.T) []string {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT id::text FROM customer_conversations WHERE tenant_id = $1 AND line = 'whatsapp' ORDER BY created_at`, w.biz)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func bodies(msgs []any) []string {
	var out []string
	for _, m := range msgs {
		x := m.(map[string]any)
		out = append(out, x["line"].(string)+":"+x["body"].(string))
	}
	return out
}

// The person in the app and the WhatsApp thread of their verified phone
// are one customer: one row in Customers with both lines, the messages of
// both in time order marked with their line, unread across both; the
// person's copy shows the WhatsApp messages marked; a reply goes where
// the customer last wrote, and staff may choose the other line;
// WhatsApp's 24 hours and template hold on its line; offers and Needs you
// count once per customer; STOP and Block each keep their own meaning.
func TestOneCustomerTwoLines(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const number = "+2348031110001"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)

	// On WhatsApp first: a thread of its own, not joined (no app chat yet).
	w.onWhatsApp(t, number, "Hello from WhatsApp")
	wa := w.whatsappThread(t)
	if len(wa) != 1 || w.linkedTo(t, wa[0]) != nil {
		t.Fatalf("whatsapp threads: %v", wa)
	}

	// The app chat starts: the lines are joined, and the person's copy
	// shows the WhatsApp message, theirs, marked.
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})
	id := chat["id"].(string)
	app := w.threadOf(t, toluID)
	if got := w.linkedTo(t, wa[0]); got == nil || got.String() != app {
		t.Fatalf("not joined: %v, app %s", got, app)
	}
	if l := lineNames(chat); len(l) != 2 || l[1] != "whatsapp" {
		t.Fatalf("the person's lines: %v", chat["lines"])
	}
	mine := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["line"] != "whatsapp" || mine[0].(map[string]any)["mine"] != true ||
		mine[0].(map[string]any)["body"] != "Hello from WhatsApp" {
		t.Fatalf("the person's copy: %s", toJSON(mine))
	}
	if w.audits(t, "customer.link", toluID) != 1 {
		t.Fatal("joining the lines is not audited")
	}

	// In the app now: one row, both lines, the app's last.
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "And from the app"})
	list := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(list) != 1 {
		t.Fatalf("rows: %s", toJSON(list))
	}
	row := list[0].(map[string]any)
	if row["id"] != app || row["line"] != "app" || row["reply_line"] != "app" || row["unread"] != float64(2) || row["last"] != "And from the app" ||
		len(lineNames(row)) != 2 || lineState(row, "whatsapp")["id"] != wa[0] || lineState(row, "whatsapp")["phone"] != number {
		t.Fatalf("row: %s", toJSON(row))
	}
	if u := owner.must(200, "GET", "/v1/customer-chats/unread", nil); u["chats"] != float64(1) || u["messages"] != float64(2) {
		t.Fatalf("unread: %v", u)
	}
	// Either line's id opens the thread; the messages interleave, marked.
	for _, which := range []string{app, wa[0]} {
		th := owner.must(200, "GET", "/v1/customer-chats/"+which, nil)
		if th["id"] != app || len(lineNames(th)) != 2 {
			t.Fatalf("thread by %s: %s", which, toJSON(th))
		}
		msgs := owner.must(200, "GET", "/v1/customer-chats/"+which+"/messages", nil)["messages"].([]any)
		if got := strings.Join(bodies(msgs), "|"); got != "whatsapp:Hello from WhatsApp|app:And from the app" {
			t.Fatalf("messages: %s", got)
		}
	}
	// Needs you once for the customer.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET waiting_since = now() - interval '40 minutes' WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	needs := 0
	for _, a := range owner.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		if a.(map[string]any)["kind"] == "customer_waiting" {
			needs++
		}
	}
	if needs != 1 {
		t.Fatalf("Needs you: %d", needs)
	}
	owner.must(204, "POST", "/v1/customer-chats/"+app+"/read", nil)

	// A reply with no line goes where they last wrote: the app.
	before := len(w.graph.Sent(number))
	m := owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Yes, in the app"})
	if m["line"] != "app" || len(w.graph.Sent(number)) != before {
		t.Fatalf("default to the app: %s", toJSON(m))
	}
	var waiting *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT waiting_since FROM customer_conversations WHERE id = $1`, app).Scan(&waiting); err != nil || waiting != nil {
		t.Fatalf("still waiting: %v %v", waiting, err)
	}

	// Back on WhatsApp: the person's copy shows it; the business is told
	// on the joined thread, and waits there (not on the WhatsApp thread).
	w.onWhatsApp(t, number, "Back on WhatsApp")
	var waWaiting, appWaiting *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT waiting_since FROM customer_conversations WHERE id = $1), (SELECT waiting_since FROM customer_conversations WHERE id = $2)`,
		wa[0], app).Scan(&waWaiting, &appWaiting); err != nil || waWaiting != nil || appWaiting == nil {
		t.Fatalf("waiting: wa %v app %v %v", waWaiting, appWaiting, err)
	}
	th := owner.must(200, "GET", "/v1/customer-chats/"+app, nil)
	if th["reply_line"] != "whatsapp" || th["window_open"] != true {
		t.Fatalf("after WhatsApp: %s", toJSON(th))
	}
	// The reply goes on WhatsApp, from the business's number, and shows
	// in the person's copy marked; staff may choose the app instead.
	before = len(w.graph.Sent(number))
	m = owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Yes, on WhatsApp", "client_id": "r1"})
	sent := w.graph.Sent(number)[before:]
	if m["line"] != "whatsapp" || m["sent_as"] != "text" || len(sent) != 1 || sent[0].From != pnA || sent[0].Text != "Yes, on WhatsApp" {
		t.Fatalf("on WhatsApp: %s %+v", toJSON(m), sent)
	}
	if again := owner.must(200, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Yes, on WhatsApp", "client_id": "r1"}); again["id"] != m["id"] {
		t.Fatalf("a repeat: %s", toJSON(again))
	}
	m = owner.must(201, "POST", "/v1/customer-chats/"+wa[0]+"/messages", map[string]any{"text": "Or here in the app", "line": "app"})
	if m["line"] != "app" || len(w.graph.Sent(number)) != before+1 {
		t.Fatalf("the app chosen: %s", toJSON(m))
	}
	owner.must(400, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "x", "line": "telegram"})
	mine = tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	got := []string{}
	for _, x := range mine {
		mm := x.(map[string]any)
		got = append(got, mm["line"].(string)+":"+mm["body"].(string)+":"+map[bool]string{true: "me", false: "them"}[mm["mine"].(bool)])
	}
	if strings.Join(got, "|") != "whatsapp:Hello from WhatsApp:me|app:And from the app:me|app:Yes, in the app:them|whatsapp:Back on WhatsApp:me|whatsapp:Yes, on WhatsApp:them|app:Or here in the app:them" {
		t.Fatalf("the person's copy: %v", got)
	}
	// What the person had on WhatsApp is not unread in the app.
	if c := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); c["unread"] != float64(2) {
		t.Fatalf("person's unread: %v", c["unread"])
	}

	// A day on: on WhatsApp only the approved template reaches them; until
	// it is approved, the reply is refused and nothing goes.
	for _, q := range []string{`UPDATE customer_conversations SET last_inbound_at = now() - interval '25 hours' WHERE id = $1`,
		`UPDATE whatsapp_own_contacts SET last_inbound_at = now() - interval '25 hours' WHERE $1::uuid IS NOT NULL`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, wa[0]); err != nil {
			t.Fatal(err)
		}
	}
	setTemplate := func(status string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO whatsapp_templates (waba_id, name, language, status) VALUES ($1, 'taskiem_customer_reply', 'en', $2)
			ON CONFLICT (waba_id, name, language) DO UPDATE SET status = EXCLUDED.status`, linesWABA, status); err != nil {
			t.Fatal(err)
		}
	}
	setTemplate("pending")
	if st := lineState(owner.must(200, "GET", "/v1/customer-chats/"+app, nil), "whatsapp"); st["window_open"] != false {
		t.Fatalf("WhatsApp's window: %v", st)
	}
	before = len(w.graph.Sent(number))
	owner.must(409, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Still there?", "line": "whatsapp"})
	if len(w.graph.Sent(number)) != before {
		t.Fatal("sent outside the window without an approved template")
	}
	setTemplate("approved")
	if m = owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Still there?", "line": "whatsapp"}); m["sent_as"] != "template" {
		t.Fatalf("template: %s", toJSON(m))
	}
	// In the app there is no window.
	owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "In the app, any time", "line": "app"})

	// Offers: asked once, counted once for the customer, always in the app.
	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	if b := owner.must(200, "POST", "/v1/customer-chats/offers", map[string]any{"text": "10% off today"}); b["sent"] != float64(1) || b["skipped"] != float64(0) {
		t.Fatalf("broadcast: %v", b)
	}
	if m = owner.must(201, "POST", "/v1/customer-chats/"+wa[0]+"/messages", map[string]any{"text": "Free delivery", "offer": true}); m["line"] != "app" {
		t.Fatalf("an offer by the WhatsApp line's id: %s", toJSON(m))
	}
	owner.must(429, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "One more", "offer": true})
	if b := owner.must(200, "POST", "/v1/customer-chats/offers", map[string]any{"text": "Another"}); b["sent"] != float64(0) || b["skipped"] != float64(1) {
		t.Fatalf("broadcast past two: %v", b)
	}

	// Typing goes to the app line by either id; an order started on the
	// WhatsApp line is listed on the customer's thread.
	owner.must(204, "POST", "/v1/customer-chats/"+wa[0]+"/typing", nil)
	draft := owner.must(201, "POST", "/v1/customer-chats/"+wa[0]+"/orders", nil)
	drafts := owner.must(200, "GET", "/v1/customer-chats/"+app+"/orders", nil)["orders"].([]any)
	if len(drafts) != 1 || drafts[0].(map[string]any)["id"] != draft["id"] || drafts[0].(map[string]any)["chat_id"] != wa[0] {
		t.Fatalf("orders: %s", toJSON(drafts))
	}

	// Search finds the customer once, by either line.
	s := owner.must(200, "GET", "/v1/customer-chats/search?q=back+on", nil)
	if len(s["messages"].([]any)) != 1 || s["messages"].([]any)[0].(map[string]any)["chat_id"] != app {
		t.Fatalf("search: %s", toJSON(s))
	}
	if s = owner.must(200, "GET", "/v1/customer-chats/search?q=tolu", nil); len(s["chats"].([]any)) != 1 {
		t.Fatalf("search by name: %s", toJSON(s))
	}

	// STOP on WhatsApp deletes that line's messages only; the app's stay,
	// and so does the person's copy. A reply goes in the app then.
	if got := w.onWhatsApp(t, number, "STOP"); len(got) != 1 {
		t.Fatalf("stop: %+v", got)
	}
	msgs := owner.must(200, "GET", "/v1/customer-chats/"+app+"/messages", nil)["messages"].([]any)
	for _, x := range msgs {
		if x.(map[string]any)["line"] != "app" {
			t.Fatalf("a WhatsApp message after STOP: %s", toJSON(x))
		}
	}
	th = owner.must(200, "GET", "/v1/customer-chats/"+app, nil)
	if lineState(th, "whatsapp")["stopped"] != true || lineState(th, "app")["stopped"] != false || th["reply_line"] != "app" ||
		!strings.Contains(lineState(th, "whatsapp")["notice"].(string), "STOP") {
		t.Fatalf("after STOP: %s", toJSON(th))
	}
	if m = owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Still here in the app"}); m["line"] != "app" {
		t.Fatalf("after STOP: %s", toJSON(m))
	}
	owner.must(409, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "On WhatsApp?", "line": "whatsapp"})
	if n := len(tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)); n < 8 {
		t.Fatalf("the person's copy after STOP: %d", n)
	}
	// Block in the app: that line stops too; both states shown.
	tolu.must(200, "POST", "/v1/me/chats/"+id+"/block", nil)
	th = owner.must(200, "GET", "/v1/customer-chats/"+app, nil)
	if lineState(th, "app")["stopped"] != true || lineState(th, "whatsapp")["stopped"] != true || th["stopped"] != true {
		t.Fatalf("both stopped: %s", toJSON(th))
	}
	owner.must(409, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Hello?"})
	// START on WhatsApp: that line opens again; the app's Block stands,
	// and nothing more of WhatsApp is shown in their copy meanwhile.
	w.onWhatsApp(t, number, "START")
	w.onWhatsApp(t, number, "Sorry, I'm back")
	th = owner.must(200, "GET", "/v1/customer-chats/"+app, nil)
	if lineState(th, "whatsapp")["stopped"] != false || lineState(th, "app")["stopped"] != true || th["reply_line"] != "whatsapp" {
		t.Fatalf("after START: %s", toJSON(th))
	}
	for _, x := range tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any) {
		if x.(map[string]any)["body"] == "Sorry, I'm back" {
			t.Fatal("shown in the app while they block the business")
		}
	}
	if m = owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Welcome back"}); m["line"] != "whatsapp" {
		t.Fatalf("reply after START: %s", toJSON(m))
	}
}

// Matching needs the phone verified on both sides, inside one business:
// a phone typed on a form, an app thread whose person has no verified
// phone, and another business's thread never match; another person never
// sees the customer's WhatsApp messages; the narrow functions refuse a
// person's transaction for anyone else.
func TestOneCustomerMatchingIsVerifiedOnly(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner

	// A typed phone: Ade's verified phone is one number; the delivery
	// phone on their saved address is another, which writes on WhatsApp.
	const adePhone, typed = "+2348031110002", "+2348031110003"
	ade, _ := w.personIn(t, adePhone)
	adeID := personID(t, ade)
	ade.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Home", "line": "12 Allen Avenue", "phone": typed})
	w.onWhatsApp(t, typed, "I typed this number on a form")
	adeChat := ade.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	ade.must(201, "POST", "/v1/me/chats/"+adeChat+"/messages", map[string]any{"text": "Hello"})
	for _, x := range w.whatsappThread(t) {
		if w.linkedTo(t, x) != nil {
			t.Fatal("a typed phone matched")
		}
	}
	if n := len(owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)); n != 2 {
		t.Fatalf("rows: %d", n)
	}
	for _, x := range ade.must(200, "GET", "/v1/me/chats/"+adeChat+"/messages", nil)["messages"].([]any) {
		if x.(map[string]any)["line"] != "app" {
			t.Fatalf("another number's WhatsApp in Ade's copy: %v", x)
		}
	}

	// No verified phone on the account: an app thread whose phone was
	// only what it was made with never matches, nor does the sweep.
	const bisiPhone = "+2348031110004"
	bisi, _ := w.personIn(t, bisiPhone)
	bisiID := personID(t, bisi)
	bisiChat := bisi.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM user_phones WHERE user_id = $1`, bisiID); err != nil {
		t.Fatal(err)
	}
	w.onWhatsApp(t, bisiPhone, "Bisi on WhatsApp")
	bisi.must(201, "POST", "/v1/me/chats/"+bisiChat+"/messages", map[string]any{"text": "Bisi in the app"})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = NULL WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, x := range w.whatsappThread(t) {
		if w.linkedTo(t, x) != nil {
			t.Fatal("an unverified phone matched")
		}
	}

	// Another business with its own number: the same person's WhatsApp
	// thread there is never joined with their chat here.
	const tolu = "+2348031110005"
	toluC, _ := w.personIn(t, tolu)
	toluID := personID(t, toluC)
	toluChat := toluC.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	w.graph.AddNumber(pnB, tokB, "+234 800 000 0002")
	other := w.tenant(t, "Bola Stores", "bola@bola.test")
	other.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnB, "waba_id": "300000000000078", "access_token": tokB, "own_app": false})
	w.sendTo(t, "/channels/whatsapp", pnB, waSecret, tolu, whatsapptest.Text("Hello Bola Stores"))
	bola := other.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(bola) != 1 || len(lineNames(bola[0].(map[string]any))) != 1 {
		t.Fatalf("the other business: %s", toJSON(bola))
	}
	var linked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_conversations WHERE linked_to IS NOT NULL`).Scan(&linked); err != nil || linked != 0 {
		t.Fatalf("joined across businesses: %d %v", linked, err)
	}
	for _, x := range toluC.must(200, "GET", "/v1/me/chats/"+toluChat+"/messages", nil)["messages"].([]any) {
		if strings.Contains(x.(map[string]any)["body"].(string), "Bola") {
			t.Fatal("another business's WhatsApp in the person's copy")
		}
	}

	// Tolu writes to this business on WhatsApp: joined; Ade never sees it.
	w.onWhatsApp(t, tolu, "Tolu on WhatsApp")
	app := w.threadOf(t, toluID)
	found := false
	for _, x := range w.whatsappThread(t) {
		if l := w.linkedTo(t, x); l != nil && l.String() == app {
			found = true
		}
	}
	if !found {
		t.Fatal("a verified phone's WhatsApp message did not join")
	}
	if c := toluC.must(200, "GET", "/v1/me/chats/"+toluChat, nil); len(lineNames(c)) != 2 {
		t.Fatalf("Tolu's lines: %v", c["lines"])
	}
	for _, x := range ade.must(200, "GET", "/v1/me/chats/"+adeChat+"/messages", nil)["messages"].([]any) {
		if strings.Contains(x.(map[string]any)["body"].(string), "Tolu") {
			t.Fatal("another person sees the customer's WhatsApp")
		}
	}
	ade.must(404, "GET", "/v1/me/chats/"+toluChat+"/messages", nil)

	// The narrow functions: never another person's from a person's
	// transaction, and never a business out of scope.
	tx, err := w.env.DB.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.person_scope', $1, true), set_config('app.tenant_scope', $2, true)`, adeID.String(), "{"+w.biz.String()+"}"); err != nil {
		t.Fatal(err)
	}
	sp, _ := tx.Begin(ctx)
	if _, err := sp.Exec(ctx, `SELECT * FROM taskiem_customer_link_person($1, $2)`, w.biz, tolu); err == nil || !strings.Contains(err.Error(), "42501") {
		t.Fatalf("a person asked who a number is: %v", err)
	}
	_ = sp.Rollback(ctx)
	sp, _ = tx.Begin(ctx)
	if _, err := sp.Exec(ctx, `SELECT * FROM taskiem_customer_link_phone($1, $2)`, w.biz, toluID); err == nil || !strings.Contains(err.Error(), "42501") {
		t.Fatalf("a person asked another's phone: %v", err)
	}
	_ = sp.Rollback(ctx)
	tx2, err := w.env.DB.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback(ctx) //nolint:errcheck
	if _, err := tx2.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, "{"+tenantIDNamed(t, w.world, "Bola Stores").String()+"}"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx2.Exec(ctx, `SELECT * FROM taskiem_customer_link_phone($1, $2)`, w.biz, toluID); err == nil || !strings.Contains(err.Error(), "42501") {
		t.Fatalf("another business asked: %v", err)
	}
}

// A phone change parts the lines (§5.5 item 4): the WhatsApp thread stays
// under the old number and says so, the app line follows the person, and
// the two are matched again only when the new number writes on WhatsApp.
// Deleting the account parts them; so does erasing the customer. The
// sweep joins the pairs there were before.
func TestOneCustomerPartedAndJoinedAgain(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const old, newer = "+2348031110011", "+2348031110012"
	tolu, _ := w.personIn(t, old)
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	app := w.threadOf(t, toluID)

	// The pair there was before: joined by the sweep, once.
	w.onWhatsApp(t, old, "Hello")
	oldWA := w.whatsappThread(t)[0]
	if w.linkedTo(t, oldWA) == nil {
		t.Fatal("not joined by the WhatsApp message")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET linked_to = NULL, linked_at = NULL WHERE id = $1;`, oldWA); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = NULL WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	if l := w.linkedTo(t, oldWA); l == nil || l.String() != app {
		t.Fatal("the sweep did not join the pair there was")
	}

	// The phone changes.
	out := tolu.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer, "via": "sms"})
	body := map[string]any{"code": w.sms.code(t, newer)}
	if out["needs_old_code"] == true {
		tolu.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer, "via": "sms", "confirm_old": true})
		body = map[string]any{"code": w.sms.code(t, newer), "old_code": w.sms.code(t, old)}
	}
	tolu.must(200, "POST", "/v1/me/phone/verify", body)
	if w.linkedTo(t, oldWA) != nil {
		t.Fatal("still joined after the phone changed")
	}
	rows := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows after the change: %s", toJSON(rows))
	}
	for _, r := range rows {
		x := r.(map[string]any)
		switch x["id"] {
		case oldWA:
			if x["notice"] != "This customer changed their number" || x["phone"] != old {
				t.Fatalf("the old number's thread: %s", toJSON(x))
			}
		case app:
			if x["phone"] != newer || len(lineNames(x)) != 1 {
				t.Fatalf("the app line: %s", toJSON(x))
			}
		}
	}
	if c := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); len(lineNames(c)) != 1 {
		t.Fatalf("the person's lines: %v", c["lines"])
	}
	// The old number writes again: it is no one's in the app now.
	w.onWhatsApp(t, old, "Still the old number")
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "From the app"})
	if w.linkedTo(t, oldWA) != nil {
		t.Fatal("the old number joined again")
	}
	// The new number writes on WhatsApp: joined again.
	w.onWhatsApp(t, newer, "From my new number")
	var newWA string
	for _, x := range w.whatsappThread(t) {
		if x != oldWA {
			newWA = x
		}
	}
	if l := w.linkedTo(t, newWA); l == nil || l.String() != app {
		t.Fatal("the new number's WhatsApp did not join")
	}

	// Erasing the customer parts the lines; each keeps its own erasure,
	// and an erased number never joins again.
	secret := enrol(t, owner)
	owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": newer, "totp": codeAfter(secret, 1)})
	if w.linkedTo(t, newWA) != nil {
		t.Fatal("still joined after erasure")
	}
	w.onWhatsApp(t, newer, "After erasure")
	for _, x := range w.whatsappThread(t) {
		if w.linkedTo(t, x) != nil {
			t.Fatal("an erased customer joined again")
		}
	}

	// Deleting the account parts the lines too.
	const kemiPhone = "+2348031110013"
	kemi, _ := w.personIn(t, kemiPhone)
	kemiID := personID(t, kemi)
	kemi.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})
	kemiApp := w.threadOf(t, kemiID)
	w.onWhatsApp(t, kemiPhone, "Kemi on WhatsApp")
	var kemiWA string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM customer_conversations WHERE linked_to = $1`, kemiApp).Scan(&kemiWA); err != nil {
		t.Fatalf("Kemi's lines not joined: %v", err)
	}
	kemi.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	kemi.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, kemiPhone)})
	if w.linkedTo(t, kemiWA) != nil {
		t.Fatal("still joined after the account was deleted")
	}
	th := owner.must(200, "GET", "/v1/customer-chats/"+kemiApp, nil)
	if th["notice"] != "This customer left Taskiem" || len(lineNames(th)) != 1 {
		t.Fatalf("after leaving: %s", toJSON(th))
	}
	// Every join and parting is in the business's chain, about the person.
	// Tolu: joined by WhatsApp, by the sweep, by the new number; parted by
	// the phone change and the erasure. Kemi: joined, parted.
	if w.linkAudits(t, toluID) != 5 || w.linkAudits(t, kemiID) != 2 {
		t.Fatalf("joining and parting audited: %d, %d", w.linkAudits(t, toluID), w.linkAudits(t, kemiID))
	}
}

func (w *doorShopWorld) linkAudits(t *testing.T, person uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'customer.link' AND target = $2`,
		w.biz, person.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The chat link of a business with its own WhatsApp number offers both:
// Chat on WhatsApp (its own number, a wa.me link) and Chat on Taskiem, on
// the card and on the page; the business may turn WhatsApp off.
func TestChatLinkOffersWhatsApp(t *testing.T) {
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	anon := &client{t: t, base: w.base}
	link := w.owner.must(200, "GET", "/v1/customers/chat-link", nil)
	code := link["code"].(string)
	if own := link["settings"].(map[string]any)["own_number"]; own != nil {
		t.Fatalf("own_number with no number of its own: %v", own)
	}
	card := anon.must(200, "GET", "/v1/chat-links/"+code, nil)
	if card["whatsapp"] != nil || len(card["choices"].([]any)) != 1 || card["choices"].([]any)[0] != "taskiem" {
		t.Fatalf("no number of its own: %s", toJSON(card))
	}
	w.withOwnNumber(t)
	card = anon.must(200, "GET", "/v1/chat-links/"+code, nil)
	wa, _ := card["whatsapp"].(map[string]any)
	if wa == nil || wa["number"] != "+2348000000001" || wa["link"] != "https://wa.me/2348000000001" || toJSON(card["choices"]) != `["whatsapp","taskiem"]` {
		t.Fatalf("with its own number: %s", toJSON(card))
	}
	st, page := w.pageAt(t, "/m/"+code)
	if st != 200 || !strings.Contains(page, "Chat on WhatsApp") || !strings.Contains(page, "https://wa.me/2348000000001") || !strings.Contains(page, "Chat on Taskiem") {
		t.Fatalf("the page: %d %s", st, page)
	}
	settings := w.owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"link_whatsapp": false})
	if settings["link_whatsapp"] != false || settings["own_number"] != "+2348000000001" {
		t.Fatalf("settings: %v", settings)
	}
	card = anon.must(200, "GET", "/v1/chat-links/"+code, nil)
	if card["whatsapp"] != nil || toJSON(card["choices"]) != `["taskiem"]` {
		t.Fatalf("WhatsApp turned off: %s", toJSON(card))
	}
	if _, page = w.pageAt(t, "/m/"+code); strings.Contains(page, "Chat on WhatsApp") {
		t.Fatal("the page still offers WhatsApp")
	}
	// Chats off in Taskiem: WhatsApp alone.
	w.owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"link_whatsapp": true, "takes_chats": false})
	card = anon.must(200, "GET", "/v1/chat-links/"+code, nil)
	if toJSON(card["choices"]) != `["whatsapp"]` {
		t.Fatalf("chats off: %s", toJSON(card))
	}
	if _, page = w.pageAt(t, "/m/"+code); !strings.Contains(page, "Chat on WhatsApp") || strings.Contains(page, "not taking chats") {
		t.Fatalf("the page with chats off: %s", page)
	}
}
