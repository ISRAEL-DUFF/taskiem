package api_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Regression tests for the chat findings of the second audit (E1 to E7):
// each was written to fail on the code as it stood.

// bindWA links a number to the client's person through the web app.
func (w *reachWorld) bindWA(t *testing.T, c *client, number string) {
	t.Helper()
	c.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": number, "password": testPassword})
	last := w.graph.Last(t, number)
	if last.Template != "taskiem_otp" {
		t.Fatalf("code message: %+v", last)
	}
	c.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(last.Text)})
}

// standingCode is the message a business's standing page fills in for
// WhatsApp, and the code a Telegram link carries.
func (w *reachWorld) standingPath(t *testing.T, owner *client) string {
	t.Helper()
	pageURL, _ := owner.must(200, "GET", "/v1/reach", nil)["page_url"].(string)
	if !strings.HasPrefix(pageURL, "https://"+reachFormHost+"/u/") {
		t.Fatalf("reach page: %q", pageURL)
	}
	return strings.TrimPrefix(pageURL, "https://"+reachFormHost)
}

// TestStopFromSomeoneOnTaskiem (E1): a person with a Taskiem account of
// their own who takes another business's updates can stop them, on
// WhatsApp and on Telegram.
func TestStopFromSomeoneOnTaskiem(t *testing.T) {
	w := newReachWorld(t)
	supplier := w.tenant(t, "Ada Stores", "ada@ada.test")
	shift := publishFlow(t, supplier, shiftFlow)
	shop := w.tenant(t, "Chidi Shop", "chidi@chidi.test")
	const chidi = "+2348039998888"
	w.bindWA(t, shop, chidi)

	path := w.standingPath(t, supplier)
	to := w.page(t, path+"/whatsapp", nil)
	loc, _ := url.Parse(to.Header().Get("Location"))
	if sent := w.whatsapp(t, chidi, loc.Query().Get("text")); len(sent) != 1 || !strings.Contains(sent[0].Text, "You'll get updates from Ada Stores here") {
		t.Fatalf("Chidi links: %+v", sent)
	}
	remind := func() {
		t.Helper()
		supplier.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": chidi}})
		w.env.Drain(t)
		w.tick(t)
	}
	remind()
	if last := w.graph.Last(t, chidi); !strings.Contains(last.Text, "Your shift starts") {
		t.Fatalf("the update: %+v", last)
	}

	// Something of Chidi's own is under way: STOP drops it and stops Ada
	// Stores, and says both.
	if got := w.whatsapp(t, chidi, "invite +2348012340000 as viewer"); len(got) != 1 || !strings.Contains(got[0].Text, "Invite +2348012340000") {
		t.Fatalf("something pending: %+v", got)
	}
	got := w.whatsapp(t, chidi, "STOP")
	if len(got) != 1 || !strings.Contains(got[0].Text, "no longer get updates from Ada Stores") || !strings.Contains(got[0].Text, "dropped") {
		t.Fatalf("STOP from a linked number: %+v", got)
	}
	if got := w.whatsapp(t, chidi, "yes"); len(got) != 1 || strings.Contains(got[0].Text, "Invitation sent") {
		t.Fatalf("the dropped action was confirmed: %+v", got)
	}
	before := len(w.graph.Sent(chidi))
	texts := w.texts(t)
	remind()
	if after := w.graph.Sent(chidi); len(after) != before {
		t.Fatalf("an update after STOP: %+v", after[before:])
	}
	if w.texts(t) != texts+1 {
		t.Fatalf("the update did not go by SMS instead: %d texts", w.texts(t)-texts)
	}
	// With nothing to stop, "stop" is the conversation's own word again.
	if got := w.whatsapp(t, chidi, "stop"); len(got) != 1 || !strings.Contains(got[0].Text, "Nothing to cancel") {
		t.Fatalf("stop with nothing to stop: %+v", got)
	}
	// START brings the updates back.
	if got := w.whatsapp(t, chidi, "START"); len(got) != 1 || !strings.Contains(got[0].Text, "updates from Ada Stores here again") {
		t.Fatalf("START: %+v", got)
	}
	remind()
	if last := w.graph.Last(t, chidi); !strings.Contains(last.Text, "Your shift starts") {
		t.Fatalf("the update after START: %+v", last)
	}

	// The same on Telegram.
	const dayo = int64(7301)
	const dayoPhone = "+2348035557777"
	other := w.tenant(t, "Dayo Bakes", "dayo@dayo.test")
	made := other.must(201, "POST", "/v1/me/telegram", map[string]any{"password": testPassword})
	u, _ := url.Parse(made["url"].(string))
	if got := w.telegram(t, dayo, func(id int64) []byte { return telegramtest.Start(id, dayo, "dayo", u.Query().Get("start")) }); len(got) != 1 || !strings.Contains(got[0].Text, "now linked") {
		t.Fatalf("linking Telegram: %+v", got)
	}
	tgTo := w.page(t, path+"/telegram", nil)
	tgLoc, _ := url.Parse(tgTo.Header().Get("Location"))
	if tgLoc == nil || tgLoc.Query().Get("start") == "" {
		t.Fatalf("the Telegram button: %d %v", tgTo.Code, tgTo.Header())
	}
	w.telegram(t, dayo, func(id int64) []byte { return telegramtest.Start(id, dayo, "dayo", tgLoc.Query().Get("start")) })
	if got := w.telegram(t, dayo, func(id int64) []byte { return telegramtest.ContactMessage(id, dayo, dayoPhone, dayo) }); len(got) != 1 || !strings.Contains(got[0].Text, "You'll get updates from Ada Stores here") {
		t.Fatalf("Dayo links: %+v", got)
	}
	if got := w.telegram(t, dayo, func(id int64) []byte { return telegramtest.Message(id, dayo, "dayo", "/stop") }); len(got) != 1 || !strings.Contains(got[0].Text, "no longer get updates from Ada Stores") {
		t.Fatalf("/stop from a linked account: %+v", got)
	}
	n := len(w.bot.Sent(dayo))
	supplier.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": dayoPhone}})
	w.env.Drain(t)
	w.tick(t)
	if after := w.bot.Sent(dayo); len(after) != n {
		t.Fatalf("a Telegram update after /stop: %+v", after[n:])
	}
}

// TestTypedStopDeclinesAnInvitation (E5): the word, not only the button.
func TestTypedStopDeclinesAnInvitation(t *testing.T) {
	ctx := context.Background()
	w := newReachWorld(t)
	chatSignupOn(w.world, "whatsapp")
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ownerNumber, bayo = "+2348031239301", "+2348031239303"
	w.bindWA(t, owner, ownerNumber)
	w.whatsapp(t, ownerNumber, "invite "+bayo+" as viewer")
	if got := w.whatsapp(t, ownerNumber, "yes"); len(got) != 1 || !strings.Contains(got[0].Text, "Invitation sent to "+bayo) {
		t.Fatalf("inviting: %+v", got)
	}
	if got := w.whatsapp(t, bayo, "Stop"); len(got) != 1 || !strings.Contains(got[0].Text, "you did not join") {
		t.Fatalf("a typed Stop: %+v", got)
	}
	var state string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT state FROM chat_invites WHERE address = $1`, bayo).Scan(&state); err != nil || state != "declined" {
		t.Fatalf("declined: %q %v", state, err)
	}
}

// TestSomeoneOnTaskiemIsACustomerElsewhere (E2): a person with an account
// who writes to another business's own number is its customer, with
// nothing of their own businesses reachable there.
func TestSomeoneOnTaskiemIsACustomerElsewhere(t *testing.T) {
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Mama Put Kitchen", "owner@mamaput.test")
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	shop := w.tenant(t, "Chidi Shop", "chidi@chidi.test")
	const chidi = "+2348031112233"
	w.bind(t, shop, chidi)
	say := func(text string) []whatsapptest.Sent {
		t.Helper()
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, chidi, whatsapptest.Text(text))
	}
	if got := say("Do you deliver to Yaba?"); len(got) != 0 {
		t.Fatalf("the customer was answered: %+v", got)
	}
	chats := alpha.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(chats) != 1 || !strings.Contains(chats[0].(map[string]any)["last"].(string), "Yaba") {
		t.Fatalf("chats: %s", toJSON(chats))
	}
	// Their own business's words do nothing there: they are more of the
	// customer's messages.
	for _, word := range []string{"status", "help", "approvals", "invite +2348012340000 as viewer"} {
		if got := say(word); len(got) != 0 {
			t.Fatalf("%q was answered on another business's number: %+v", word, got)
		}
	}
	// STOP is the customer's.
	if got := say("STOP"); len(got) != 1 || !strings.Contains(got[0].Text, "no longer get messages from Mama Put Kitchen") {
		t.Fatalf("STOP: %+v", got)
	}
	// On the shared number they are still themselves.
	if got := w.say(t, chidi, "help"); !strings.Contains(got, "status") {
		t.Fatalf("the shared number: %q", got)
	}
}

// TestAnOldYesButtonConfirmsNothing (E3): the Yes under a summary is for
// that summary only.
func TestAnOldYesButtonConfirmsNothing(t *testing.T) {
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ownerNumber, ada, bayo, chi = "+2348031239401", "+2348031239402", "+2348031239403", "+2348031239404"
	w.bind(t, owner, ownerNumber)
	yesOf := func(sent []whatsapptest.Sent) string {
		t.Helper()
		if len(sent) != 1 || len(sent[0].Buttons) != 2 || !strings.HasPrefix(sent[0].Buttons[0].ID, "yes") {
			t.Fatalf("a summary with Yes: %+v", sent)
		}
		return sent[0].Buttons[0].ID
	}
	old := yesOf(w.send(t, ownerNumber, whatsapptest.Text("invite "+ada+" as viewer")))
	w.say(t, ownerNumber, "cancel")
	current := yesOf(w.send(t, ownerNumber, whatsapptest.Text("invite "+bayo+" as owner")))
	// The Yes under the first summary, tapped now.
	got := w.press(t, ownerNumber, old)
	if len(got) != 1 || !strings.Contains(got[0].Text, "something earlier") || strings.Contains(got[0].Text, "Invitation sent") {
		t.Fatalf("the old Yes: %+v", got)
	}
	if len(w.graph.Sent(bayo)) != 0 {
		t.Fatalf("the old Yes invited someone: %+v", w.graph.Sent(bayo))
	}
	// The Yes under the summary that is waiting still works, and so does
	// the word.
	if got := w.press(t, ownerNumber, current); len(got) != 1 || !strings.Contains(got[0].Text, "Invitation sent to "+bayo) {
		t.Fatalf("the current Yes: %+v", got)
	}
	yesOf(w.send(t, ownerNumber, whatsapptest.Text("invite "+chi+" as viewer")))
	if got := w.say(t, ownerNumber, "yes"); !strings.Contains(got, "Invitation sent to "+chi) {
		t.Fatalf("typed yes: %q", got)
	}
	// With nothing waiting, an old Yes says so.
	if got := w.press(t, ownerNumber, current); len(got) != 1 || !strings.Contains(got[0].Text, "something earlier") {
		t.Fatalf("an old Yes with nothing waiting: %+v", got)
	}
}

// TestOutboxSlowBatchIsSentOnce (E4): a batch that takes longer than the
// lease is not sent again by a second notifier: each sender sends only
// what it still holds.
func TestOutboxSlowBatchIsSentOnce(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	numbers := []string{"+2348010000101", "+2348010000102", "+2348010000103", "+2348010000104"}
	emails := []string{"owner@acme.test"}
	w.bind(t, owner, numbers[0])
	for i, n := range numbers[1:] {
		email := fmt.Sprintf("m%d@acme.test", i)
		w.bind(t, w.member(t, owner, email, "viewer"), n)
		emails = append(emails, email)
	}
	tenant := tenantID(t, owner)
	al := &alerts.Alerter{Pool: w.env.Store.Pool, Secrets: w.env.Vault, WhatsApp: w.wa, PublicURL: waPublicURL}
	w.srv.Alerts = al
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "whatsapp", "name": "ops", "to": emails})["id"].(string)
	owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "fails", "kind": "run_failed", "channel_ids": []string{ch}})
	wf := publishFlow(t, owner, waLoanFlow)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at)
		VALUES ($1, $2, $3, 1, 'prod', 'failed', now(), now() + interval '1 second')`, uuid.Must(uuid.NewV7()), tenant, wf); err != nil {
		t.Fatal(err)
	}
	// The alert is queued, one message each, and none can be sent yet.
	w.graph.SetFail(131000)
	al.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if err := al.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if p := w.outbox(t, owner, "pending"); len(p) != len(numbers) {
		t.Fatalf("pending: %v", p)
	}
	w.graph.SetFail(0)
	w.due(t)

	// A slow Graph API and a short lease: the first notifier's batch
	// outlasts it, and a second notifier starts meanwhile.
	lease := whatsapp.OutboxLease
	whatsapp.OutboxLease = time.Second
	t.Cleanup(func() { whatsapp.OutboxLease = lease })
	w.graph.SetDelay(700 * time.Millisecond)
	done := make(chan error, 2)
	go func() { done <- w.srv.WhatsAppTick(ctx) }()
	time.Sleep(1100 * time.Millisecond)
	go func() { done <- w.srv.WhatsAppTick(ctx) }()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	w.graph.SetDelay(0)
	w.due(t)
	w.tick(t)
	for _, n := range numbers {
		if got := countText(w.graph.Sent(n), "loan failed in prod"); got != 1 {
			t.Errorf("%s got the alert %d times", n, got)
		}
	}
	var attempts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT max(attempts) FROM whatsapp_outbox WHERE status = 'sent'`).Scan(&attempts); err != nil || attempts > 3 {
		t.Errorf("attempts: %d %v", attempts, err)
	}
}

// TestTelegramLocationGoesWithTheClockIn (E6): a linked member's shared
// location reaches the conversation on Telegram as it does on WhatsApp.
func TestTelegramLocationGoesWithTheClockIn(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const ada = int64(7401)
	w.link(t, owner, ada, "ada")
	if got := w.say(t, ada, "in"); !strings.Contains(got.Text, "Clocked in at") {
		t.Fatalf("in: %+v", got)
	}
	got := w.update(t, ada, func(id int64) []byte { return telegramtest.LocationMessage(id, ada, 6.4541, 3.3947) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "Your location is with today's clock-in") {
		t.Fatalf("the location: %+v", got)
	}
	var where string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT COALESCE(in_where, '') FROM staff_clock`).Scan(&where); err != nil || !strings.Contains(where, "6.4541") {
		t.Fatalf("the clock-in's place: %q %v", where, err)
	}
}
