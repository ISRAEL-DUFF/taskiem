package api_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
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
