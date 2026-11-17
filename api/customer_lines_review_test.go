package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The review of stage 4's server ("As built: stage 4, server review fixes"
// in docs/spec/taskiem-for-everyone.md).

// changePhone moves a person's phone from old to newer (§5.5).
func (w *doorShopWorld) changePhone(t *testing.T, c *client, old, newer string) {
	t.Helper()
	out := c.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer, "via": "sms"})
	body := map[string]any{"code": w.sms.code(t, newer)}
	if out["needs_old_code"] == true {
		c.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer, "via": "sms", "confirm_old": true})
		body = map[string]any{"code": w.sms.code(t, newer), "old_code": w.sms.code(t, old)}
	}
	c.must(200, "POST", "/v1/me/phone/verify", body)
}

// customerRow is the business's row of a thread in its list.
func customerRowIn(t *testing.T, owner *client, id string) map[string]any {
	t.Helper()
	for _, archived := range []string{"0", "1"} {
		for _, r := range owner.must(200, "GET", "/v1/customer-chats?archived="+archived, nil)["chats"].([]any) {
			if x := r.(map[string]any); x["id"] == id {
				return x
			}
		}
	}
	return nil
}

// A client_id is one per customer: a repeat answers the first message on
// the line it went on, even after the reply line moved, by either id.
func TestOneCustomerClientIDAcrossLines(t *testing.T) {
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const number = "+2348031110021"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	app := w.threadOf(t, toluID)
	w.onWhatsApp(t, number, "On WhatsApp")
	wa := w.whatsappThread(t)[0]
	if l := w.linkedTo(t, wa); l == nil || l.String() != app {
		t.Fatal("not joined")
	}
	count := func(body string) int {
		n := 0
		for _, m := range owner.must(200, "GET", "/v1/customer-chats/"+app+"/messages", nil)["messages"].([]any) {
			if m.(map[string]any)["body"] == body {
				n++
			}
		}
		return n
	}

	// First on WhatsApp; the customer then writes in the app, so the
	// reply line moves; the repeat answers the WhatsApp message.
	first := owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Hello", "client_id": "c1"})
	if first["line"] != "whatsapp" {
		t.Fatalf("first: %s", toJSON(first))
	}
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Now in the app"})
	if th := owner.must(200, "GET", "/v1/customer-chats/"+app, nil); th["reply_line"] != "app" {
		t.Fatalf("reply line: %v", th["reply_line"])
	}
	sent := len(w.graph.Sent(number))
	for _, req := range []struct {
		thread string
		body   map[string]any
	}{
		{app, map[string]any{"text": "Hello", "client_id": "c1"}},
		{wa, map[string]any{"text": "Hello", "client_id": "c1", "line": "app"}},
	} {
		again := owner.must(200, "POST", "/v1/customer-chats/"+req.thread+"/messages", req.body)
		if again["id"] != first["id"] || again["line"] != "whatsapp" {
			t.Fatalf("a repeat after the line moved: %s", toJSON(again))
		}
	}
	if count("Hello") != 1 || len(w.graph.Sent(number)) != sent {
		t.Fatalf("sent twice: %d", count("Hello"))
	}

	// The other way: first in the app, then the line moves to WhatsApp.
	first = owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "In the app", "client_id": "c2"})
	if first["line"] != "app" {
		t.Fatalf("first: %s", toJSON(first))
	}
	w.onWhatsApp(t, number, "Back on WhatsApp")
	sent = len(w.graph.Sent(number))
	again := owner.must(200, "POST", "/v1/customer-chats/"+wa+"/messages", map[string]any{"text": "In the app", "client_id": "c2"})
	if again["id"] != first["id"] || again["line"] != "app" || count("In the app") != 1 || len(w.graph.Sent(number)) != sent {
		t.Fatalf("a repeat after the line moved to WhatsApp: %s", toJSON(again))
	}
	// An offer's client_id by either id: once.
	tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true})
	offer := owner.must(201, "POST", "/v1/customer-chats/"+wa+"/messages", map[string]any{"text": "10% off", "offer": true, "client_id": "o1"})
	if again := owner.must(200, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "10% off", "offer": true, "client_id": "o1"}); again["id"] != offer["id"] {
		t.Fatalf("an offer repeated: %s", toJSON(again))
	}
}

// A number that changed hands: Bob's old number's WhatsApp thread never
// joins Carol, who verified that number later, by Bob's messages; it says
// Bob changed his number until Carol writes from it on WhatsApp, and then
// Carol's copy shows only what came after her phone was verified.
func TestOneCustomerRecycledNumberWaitsForItsOwner(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const o, n = "+2348031110031", "+2348031110032"
	bob, _ := w.personIn(t, o)
	bob.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})
	w.onWhatsApp(t, o, "Bob here")
	bobWA := w.whatsappThread(t)[0]
	if w.linkedTo(t, bobWA) == nil {
		t.Fatal("Bob's lines not joined")
	}
	w.changePhone(t, bob, o, n)
	if w.linkedTo(t, bobWA) != nil {
		t.Fatal("still joined after Bob's phone changed")
	}

	carol, _ := w.personIn(t, o)
	carolID := personID(t, carol)
	carolChat := carol.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	carolApp := w.threadOf(t, carolID)
	carol.must(201, "POST", "/v1/me/chats/"+carolChat+"/messages", map[string]any{"text": "Carol in the app"})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = NULL WHERE id = $1`, carolApp); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.linkedTo(t, bobWA) != nil {
		t.Fatal("Bob's old WhatsApp thread joined Carol by Bob's messages")
	}
	if row := customerRowIn(t, owner, bobWA); row == nil || row["notice"] != "This customer changed their number" {
		t.Fatalf("Bob's old thread: %s", toJSON(row))
	}
	for _, m := range carol.must(200, "GET", "/v1/me/chats/"+carolChat+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["line"] != "app" {
			t.Fatalf("Bob's WhatsApp in Carol's copy: %s", toJSON(m))
		}
	}

	// Carol writes from the number on WhatsApp: now it is hers.
	w.onWhatsApp(t, o, "Carol here")
	if l := w.linkedTo(t, bobWA); l == nil || l.String() != carolApp {
		t.Fatal("Carol's WhatsApp message did not join")
	}
	var shown []string
	for _, m := range carol.must(200, "GET", "/v1/me/chats/"+carolChat+"/messages", nil)["messages"].([]any) {
		if x := m.(map[string]any); x["line"] == "whatsapp" {
			shown = append(shown, x["body"].(string))
		}
	}
	if strings.Join(shown, "|") != "Carol here" {
		t.Fatalf("Carol's copy: %v", shown)
	}
}

// A join whose person's phone changed without the change reaching the
// business is parted: by the next WhatsApp message from the old number
// (never shown in the person's copy), or by the sweep.
func TestOneCustomerStaleLinkParted(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	moved := func(person uuid.UUID, number string) {
		t.Helper()
		// The change, as if phoneChangedLines never reached the business.
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE user_phones SET number = $2, verified_at = now() WHERE user_id = $1`, person, number); err != nil {
			t.Fatal(err)
		}
	}
	joined := func(number string) (*client, uuid.UUID, string, string, string) {
		t.Helper()
		c, _ := w.personIn(t, number)
		id := personID(t, c)
		chat := c.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
		app := w.threadOf(t, id)
		w.onWhatsApp(t, number, "Hello from "+number)
		var wa string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM customer_conversations WHERE linked_to = $1`, app).Scan(&wa); err != nil {
			t.Fatalf("not joined: %v", err)
		}
		return c, id, chat, app, wa
	}

	// By the old number's next message.
	const bobOld, bobNew = "+2348031110041", "+2348031110042"
	bob, bobID, bobChat, bobApp, bobWA := joined(bobOld)
	moved(bobID, bobNew)
	w.onWhatsApp(t, bobOld, "Whoever has this number now")
	if w.linkedTo(t, bobWA) != nil {
		t.Fatal("a stale link kept by the old number's message")
	}
	for _, m := range bob.must(200, "GET", "/v1/me/chats/"+bobChat+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["body"] == "Whoever has this number now" {
			t.Fatal("the number's next owner reached Bob's copy")
		}
	}
	if row := customerRowIn(t, owner, bobWA); row == nil || row["notice"] != "This customer changed their number" {
		t.Fatalf("the old number's thread: %s", toJSON(row))
	}
	if row := customerRowIn(t, owner, bobApp); row == nil || row["phone"] != bobNew {
		t.Fatalf("Bob's app line: %s", toJSON(row))
	}

	// By the sweep; a pair whose phone did not change stays joined.
	const kemiOld, kemiNew, tolu = "+2348031110043", "+2348031110044", "+2348031110045"
	_, kemiID, _, kemiApp, kemiWA := joined(kemiOld)
	_, _, _, toluApp, toluWA := joined(tolu)
	moved(kemiID, kemiNew)
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.linkedTo(t, kemiWA) != nil {
		t.Fatal("the sweep kept a stale link")
	}
	if l := w.linkedTo(t, toluWA); l == nil || l.String() != toluApp {
		t.Fatal("the sweep parted a pair whose phone did not change")
	}
	if row := customerRowIn(t, owner, kemiApp); row == nil || row["phone"] != kemiNew {
		t.Fatalf("Kemi's app line: %s", toJSON(row))
	}
	if row := customerRowIn(t, owner, kemiWA); row == nil || row["notice"] != "This customer changed their number" {
		t.Fatalf("Kemi's old number's thread: %s", toJSON(row))
	}
	// Parted once, audited.
	if err := w.srv.CustomerChatsTick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.linkAudits(t, kemiID) != 2 || w.linkAudits(t, bobID) != 2 {
		t.Fatalf("audits: %d %d", w.linkAudits(t, kemiID), w.linkAudits(t, bobID))
	}
}

// History copied on joining shows only the business's replies WhatsApp
// took (never one that failed or is still going), and a reply sent as the
// template in the template's words.
func TestOneCustomerHistoryOnlyDelivered(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const number = "+2348031110051"
	ade, _ := w.personIn(t, number)
	w.onWhatsApp(t, number, "Hi there")
	wa := w.whatsappThread(t)[0]
	reply := func(text, set string) {
		t.Helper()
		m := owner.must(201, "POST", "/v1/customer-chats/"+wa+"/messages", map[string]any{"text": text})
		if set != "" {
			if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_messages SET `+set+` WHERE id = $1`, m["id"]); err != nil {
				t.Fatal(err)
			}
		}
	}
	reply("Delivered reply", "delivery = 'delivered'")
	reply("Failed reply", "delivery = 'failed'")
	reply("Still sending", "delivery = 'sending'")
	reply("Late reply", "sent_as = 'template'")
	chat := ade.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	if w.linkedTo(t, wa) == nil {
		t.Fatal("not joined")
	}
	var got []string
	for _, m := range ade.must(200, "GET", "/v1/me/chats/"+chat+"/messages", nil)["messages"].([]any) {
		got = append(got, m.(map[string]any)["body"].(string))
	}
	if want := "Hi there|Delivered reply|[Mama's Kitchen] Reply to your message: Late reply Answer here to carry on."; strings.Join(got, "|") != want {
		t.Fatalf("the person's copy: %q", got)
	}
}

// Joining failing on the person's side never fails what they did: the
// chat starts and the message is kept; the lines join later.
func TestOneCustomerJoinFailureKeepsThePersonsAction(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	const number = "+2348031110061"
	tolu, _ := w.personIn(t, number)
	w.onWhatsApp(t, number, "On WhatsApp first")
	wa := w.whatsappThread(t)[0]
	grant := func(sql string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, sql+` EXECUTE ON FUNCTION taskiem_customer_link_phone(uuid, uuid) `+map[bool]string{true: "FROM", false: "TO"}[strings.HasPrefix(sql, "REVOKE")]+` taskiem_app`); err != nil {
			t.Fatal(err)
		}
	}
	grant("REVOKE")
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+chat+"/messages", map[string]any{"text": "In the app"})
	if w.linkedTo(t, wa) != nil {
		t.Fatal("joined with the function refused")
	}
	grant("GRANT")
	tolu.must(201, "POST", "/v1/me/chats/"+chat+"/messages", map[string]any{"text": "Again"})
	if w.linkedTo(t, wa) == nil {
		t.Fatal("not joined once it works again")
	}
}

// A WhatsApp thread archived before it joined: the customer's row is the
// app thread's (not archived), and the unread count agrees with it.
func TestOneCustomerUnreadAgreesWithTheList(t *testing.T) {
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const number = "+2348031110071"
	tolu, _ := w.personIn(t, number)
	w.onWhatsApp(t, number, "Unread on WhatsApp")
	wa := w.whatsappThread(t)[0]
	owner.must(204, "PUT", "/v1/customer-chats/"+wa, map[string]any{"archived": true})
	tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})
	if w.linkedTo(t, wa) == nil {
		t.Fatal("not joined")
	}
	list := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["unread"] != float64(1) {
		t.Fatalf("the list: %s", toJSON(list))
	}
	if u := owner.must(200, "GET", "/v1/customer-chats/unread", nil); u["chats"] != float64(1) || u["messages"] != float64(1) {
		t.Fatalf("unread: %v", u)
	}
	if a := owner.must(200, "GET", "/v1/customer-chats?archived=1", nil)["chats"].([]any); len(a) != 0 {
		t.Fatalf("archived: %s", toJSON(a))
	}
}

// Needs you's alert and the customer card read the pair: its id, the name
// the thread shows (the WhatsApp line's when the app's has none), by
// either line's id.
func TestOneCustomerAlertAndCardReadThePair(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const number = "+2348031110081"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})
	app := w.threadOf(t, toluID)
	w.onWhatsApp(t, number, "Hello")
	wa := w.whatsappThread(t)[0]
	// The app line shows no name of its own.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET who = '{"number": ""}', waiting_since = now() - interval '40 minutes' WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	th := owner.must(200, "GET", "/v1/customer-chats/"+wa, nil)
	name := th["name"].(string)
	if th["id"] != app || name == "A customer" {
		t.Fatalf("the thread: %s", toJSON(th))
	}
	found := false
	for _, a := range owner.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		if x := a.(map[string]any); x["kind"] == "customer_waiting" {
			found = true
			if x["title"] != name+" is waiting for an answer" {
				t.Fatalf("the alert: %s", toJSON(x))
			}
		}
	}
	if !found {
		t.Fatal("no alert")
	}
	for _, id := range []string{app, wa} {
		card := owner.must(200, "GET", "/v1/chat-card?kind=customer&id="+id, nil)
		if card["id"] != app || card["name"] != name {
			t.Fatalf("the card by %s: %s", id, toJSON(card))
		}
	}
}

// The default reply line: WhatsApp past its 24 hours with the template
// not approved, while the app is open, is the app; with the template
// approved, WhatsApp, and the person's copy shows the template's words;
// with WhatsApp the only line open, WhatsApp (refused until approved).
// Every answer for the pair carries the app thread's id.
func TestOneCustomerReplyLineAndTemplateWords(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.withOwnNumber(t)
	owner := w.owner
	const number = "+2348031110091"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	app := w.threadOf(t, toluID)
	w.onWhatsApp(t, number, "On WhatsApp")
	wa := w.whatsappThread(t)[0]
	for _, q := range []string{`UPDATE customer_conversations SET last_inbound_at = now() - interval '25 hours' WHERE id = $1`,
		`UPDATE whatsapp_own_contacts SET last_inbound_at = now() - interval '25 hours' WHERE $1::uuid IS NOT NULL`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, wa); err != nil {
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
	for _, id := range []string{app, wa} {
		th := owner.must(200, "GET", "/v1/customer-chats/"+id, nil)
		if th["id"] != app || th["reply_line"] != "app" || th["window_open"] != true {
			t.Fatalf("the template not approved: %s", toJSON(th))
		}
	}
	if m := owner.must(201, "POST", "/v1/customer-chats/"+wa+"/messages", map[string]any{"text": "Answered in the app"}); m["line"] != "app" {
		t.Fatalf("the default: %s", toJSON(m))
	}
	setTemplate("approved")
	if th := owner.must(200, "GET", "/v1/customer-chats/"+app, nil); th["reply_line"] != "whatsapp" {
		t.Fatalf("the template approved: %s", toJSON(th))
	}
	m := owner.must(201, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Still\nthere?"})
	if m["line"] != "whatsapp" || m["sent_as"] != "template" {
		t.Fatalf("as the template: %s", toJSON(m))
	}
	msgs := tolu.must(200, "GET", "/v1/me/chats/"+chat+"/messages", nil)["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["line"] != "whatsapp" || last["body"] != "[Mama's Kitchen] Reply to your message: Still there? Answer here to carry on." {
		t.Fatalf("the person's copy: %s", toJSON(last))
	}
	// Only WhatsApp open: it stays the reply line, refused until approved.
	setTemplate("pending")
	tolu.must(200, "POST", "/v1/me/chats/"+chat+"/block", nil)
	if th := owner.must(200, "GET", "/v1/customer-chats/"+app, nil); th["reply_line"] != "whatsapp" {
		t.Fatalf("only WhatsApp open: %s", toJSON(th))
	}
	owner.must(409, "POST", "/v1/customer-chats/"+app+"/messages", map[string]any{"text": "Hello?"})
	// Every list and search answer names the pair by the app thread's id.
	if row := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)[0].(map[string]any); row["id"] != app {
		t.Fatalf("the list: %s", toJSON(row))
	}
	if s := owner.must(200, "GET", "/v1/customer-chats/search?q=tolu", nil)["chats"].([]any); len(s) != 1 || s[0].(map[string]any)["id"] != app {
		t.Fatalf("search: %s", toJSON(s))
	}
}

// The sweeps' definers are for the sweep alone: refused inside a
// business's transaction.
func TestOneCustomerSweepDefinersRefuseScope(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	for _, q := range []string{`SELECT * FROM taskiem_customer_link_due(10)`, `SELECT * FROM taskiem_customer_link_stale(10)`, `SELECT * FROM taskiem_customer_waiting('2 hours')`} {
		if _, err := w.env.DB.App.Exec(ctx, q); err != nil {
			t.Fatalf("the sweep's own: %s: %v", q, err)
		}
		tx, err := w.env.DB.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, "{"+w.biz.String()+"}")
		if err == nil {
			_, err = tx.Exec(ctx, q)
		}
		_ = tx.Rollback(ctx)
		if err == nil || !strings.Contains(err.Error(), "42501") {
			t.Fatalf("in a business's transaction: %s: %v", q, err)
		}
	}
}
