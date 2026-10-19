package api_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/alerts"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	tgToken  = "123456:AAtelegram-platform-token-abcdef"
	tgSecret = "tg-webhook-secret"
	tgBot    = "taskiem_test_bot"
)

// tgWorld is a deployment with the Telegram bot and no WhatsApp number:
// approval buttons are signed with TASKIEM_CHAT_TOKEN_KEY.
type tgWorld struct {
	*world
	bot  *telegramtest.Bot
	tg   *telegram.Platform
	hook string
	seq  atomic.Int64
}

func newTGWorld(t *testing.T) *tgWorld {
	e := rt.New(t)
	bot := telegramtest.New(t, tgToken)
	bot.Username = tgBot
	tg := telegram.New(e.Store.Pool, telegram.Config{BotToken: tgToken, WebhookSecret: tgSecret, BotUsername: tgBot, APIURL: bot.URL},
		e.Egress, slog.New(slog.DiscardHandler))
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, Connectors: e.Connectors, AllowSignup: true, Egress: e.Egress,
		Logger: slog.New(slog.DiscardHandler), Telegram: tg, ChatSigner: whatsapp.Signer{Key: waKey}, PublicURL: waPublicURL}
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/telegram", srv.TelegramHooks().ServeHTTP)
	mux.Handle("/", specCheck(t, srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &tgWorld{world: &world{env: e, base: ts.URL, srv: srv}, bot: bot, tg: tg, hook: ts.URL + "/channels/telegram"}
}

// deliver posts an update with a secret token; it returns the status.
func (w *tgWorld) deliver(t *testing.T, body []byte, secret string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", w.hook, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// update delivers an update built with the next id and returns what the
// bot sent to the account meanwhile.
func (w *tgWorld) update(t *testing.T, account int64, build func(id int64) []byte) []telegramtest.Call {
	t.Helper()
	before := len(w.bot.Sent(account))
	if st := w.deliver(t, build(w.seq.Add(1)), tgSecret); st != 200 {
		t.Fatalf("update: %d", st)
	}
	return w.bot.Sent(account)[before:]
}

// say sends text from an account and returns the single reply.
func (w *tgWorld) say(t *testing.T, account int64, text string) telegramtest.Call {
	t.Helper()
	got := w.update(t, account, func(id int64) []byte { return telegramtest.Message(id, account, "", text) })
	if len(got) != 1 {
		t.Fatalf("%q from %d: %d replies: %+v", text, account, len(got), got)
	}
	return got[0]
}

// link links an account to the client's person: a one-time link made on
// the account page, opened in Telegram.
func (w *tgWorld) link(t *testing.T, c *client, account int64, username string) {
	t.Helper()
	made := c.must(201, "POST", "/v1/me/telegram", map[string]any{"password": testPassword})
	u, err := url.Parse(made["url"].(string))
	if err != nil || u.Host != "t.me" || u.Path != "/"+tgBot {
		t.Fatalf("link: %v", made)
	}
	got := w.update(t, account, func(id int64) []byte { return telegramtest.Start(id, account, username, u.Query().Get("start")) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "now linked") {
		t.Fatalf("opening the link: %+v", got)
	}
}

func (w *tgWorld) member(t *testing.T, owner *client, email string, roles ...string) *client {
	t.Helper()
	owner.must(201, "POST", "/v1/members", map[string]any{"email": email, "password": testPassword, "roles": roles})
	return w.login(t, email, testPassword)
}

func (w *tgWorld) tick(t *testing.T) {
	t.Helper()
	if err := w.srv.WhatsAppTick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func TestTelegramLinking(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ada, eve = int64(5001), int64(5002)

	info := owner.must(200, "GET", "/v1/me/telegram", nil)
	if info["enabled"] != true || info["linked"] != false || info["bot_username"] != tgBot {
		t.Fatalf("before: %v", info)
	}
	// Making a link takes the same proof as adding a passkey.
	owner.must(403, "POST", "/v1/me/telegram", map[string]any{})

	// An account linked to no one is told how to link.
	if r := w.say(t, ada, "hi"); !strings.Contains(r.Text, "not linked") || !strings.Contains(r.Text, waPublicURL+"/account") {
		t.Errorf("unbound: %q", r.Text)
	}

	made := owner.must(201, "POST", "/v1/me/telegram", map[string]any{"password": testPassword})
	u, _ := url.Parse(made["url"].(string))
	code := u.Query().Get("start")
	if len(code) != 32 {
		t.Fatalf("code %q", code)
	}
	if owner.must(200, "GET", "/v1/me/telegram", nil)["pending_expires_at"] == nil {
		t.Error("no pending link shown")
	}
	r := w.update(t, ada, func(id int64) []byte { return telegramtest.Start(id, ada, "ada_o", code) })
	if len(r) != 1 || !strings.Contains(r[0].Text, "now linked") {
		t.Fatalf("link: %+v", r)
	}
	// WhatsApp's *help* reads as bold, with no asterisks left.
	if strings.Contains(r[0].Text, "*") || len(r[0].Entities) != 1 || r[0].Entities[0].Type != "bold" {
		t.Errorf("formatting: %q %+v", r[0].Text, r[0].Entities)
	}
	info = owner.must(200, "GET", "/v1/me/telegram", nil)
	if info["linked"] != true || info["username"] != "ada_o" {
		t.Fatalf("after: %v", info)
	}
	// A link works once, for one account.
	r = w.update(t, eve, func(id int64) []byte { return telegramtest.Start(id, eve, "eve", code) })
	if len(r) != 1 || !strings.Contains(r[0].Text, "expired or was already used") {
		t.Errorf("reused link: %+v", r)
	}
	// Ada's account now speaks for the owner.
	if r := w.say(t, ada, "/help"); !strings.Contains(r.Text, "Acme") {
		t.Errorf("help: %q", r.Text)
	}

	// No secret, or the wrong one: refused before anything is read.
	body := telegramtest.Message(w.seq.Add(1), ada, "", "help")
	if st := w.deliver(t, body, ""); st != 401 {
		t.Errorf("no secret: %d", st)
	}
	if st := w.deliver(t, body, "nope"); st != 401 {
		t.Errorf("wrong secret: %d", st)
	}
	// A redelivered update is acted on once.
	body = telegramtest.Message(w.seq.Add(1), ada, "", "help")
	before := len(w.bot.Sent(ada))
	for range 2 {
		if st := w.deliver(t, body, tgSecret); st != 200 {
			t.Fatalf("delivery: %d", st)
		}
	}
	if n := len(w.bot.Sent(ada)) - before; n != 1 {
		t.Errorf("a redelivery was answered again (%d replies)", n)
	}
	// Group chats are ignored: anyone present would read the replies.
	group := []byte(`{"update_id": 99999, "message": {"message_id": 1, "from": {"id": 5001, "is_bot": false, "first_name": "A"},
		"chat": {"id": -100123, "type": "supergroup", "title": "Ops"}, "date": 1759800000, "text": "approvals"}}`)
	before = len(w.bot.Calls("sendMessage"))
	if st := w.deliver(t, group, tgSecret); st != 200 || len(w.bot.Calls("sendMessage")) != before {
		t.Errorf("group chat answered: %d", st)
	}

	owner.must(204, "DELETE", "/v1/me/telegram", nil)
	if r := w.say(t, ada, "approvals"); !strings.Contains(r.Text, "not linked") {
		t.Errorf("after unlinking: %q", r.Text)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"telegram.link_made"`, `"telegram.link"`, `"telegram.unlink"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
}

func TestTelegramApprovals(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"credit_officer", "approver"}})
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	bob := w.member(t, owner, "bob@acme.test", "approver", "credit_officer")
	const an, bn = int64(7001), int64(7002)
	w.link(t, alice, an, "alice")
	w.link(t, bob, bn, "bob")

	wf := publishFlow(t, owner, waLoanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
	w.tick(t)

	am := w.bot.Last(t, an)
	if len(am.Keyboard) != 2 || len(am.Keyboard[0]) != 1 || !strings.HasPrefix(am.Keyboard[0][0].Data, "c:") || !strings.HasPrefix(am.Text, "[Acme] Approval needed: loan · ok") {
		t.Fatalf("alice's request: %+v", am)
	}
	if strings.Contains(am.Text, "22212345678") || strings.Contains(am.Text, "0123456789") || !strings.Contains(am.Text, "••••6789") {
		t.Errorf("request shows personal data: %q", am.Text)
	}
	// Once per approver, level and channel.
	before := len(w.bot.Calls("sendMessage"))
	w.tick(t)
	if len(w.bot.Calls("sendMessage")) != before {
		t.Error("a second tick sent again")
	}

	tap := func(account int64, data string) string {
		t.Helper()
		got := w.update(t, account, func(id int64) []byte { return telegramtest.Callback(id, account, am.MessageID, "q"+data, data) })
		if len(got) != 1 {
			t.Fatalf("tap: %+v", got)
		}
		return got[0].Text
	}
	approveA, rejectA := am.Keyboard[0][0].Data, am.Keyboard[1][0].Data

	// Alice's button from Bob's account is unknown there.
	if r := tap(bn, approveA); !strings.Contains(r, "expired") {
		t.Errorf("wrong account: %q", r)
	}
	if r := tap(an, "c:AAAAAAAAAAAAAAAAAAAAAA"); !strings.Contains(r, "expired") {
		t.Errorf("unknown button: %q", r)
	}
	if r := tap(an, approveA); !strings.Contains(r, "Recorded: you approved ok") {
		t.Fatalf("approve: %q", r)
	}
	if r := tap(an, rejectA); !strings.Contains(r, "already answered") {
		t.Errorf("pair: %q", r)
	}
	if n := len(w.bot.Calls("answerCallbackQuery")); n < 4 {
		t.Errorf("buttons not acknowledged: %d", n)
	}

	var bm telegramtest.Call // Bob's request (his last message answers the tap above)
	for _, c := range w.bot.Sent(bn) {
		if len(c.Keyboard) == 2 {
			bm = c
		}
	}
	if len(bm.Keyboard) != 2 {
		t.Fatalf("bob's request: %+v", w.bot.Sent(bn))
	}
	if r := tap(bn, bm.Keyboard[0][0].Data); !strings.Contains(r, "request is approved") {
		t.Fatalf("bob: %q", r)
	}
	w.env.Drain(t)
	res := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := res["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v", st)
	}
	if !strings.Contains(toJSON(res["events"]), `"channel":"telegram"`) {
		t.Error("the decision is not recorded with channel telegram")
	}
	if audit := toJSON(owner.must(200, "GET", "/v1/audit", nil)); !strings.Contains(audit, `"approval.decide"`) || !strings.Contains(audit, `"channel":"telegram"`) {
		t.Errorf("audit: %s", audit)
	}
}

func TestTelegramAlertsAndBlockedBot(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = int64(8001)
	w.link(t, owner, on, "")

	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "telegram", "name": "ops", "to": []string{"nobody@acme.test"}})
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "telegram", "name": "ops", "to": []string{"owner@acme.test"}})
	list := owner.must(200, "GET", "/v1/alerts/channels", nil)
	if list["telegram_configured"] != true {
		t.Errorf("list: %v", list)
	}
	// A test message goes at once.
	alerter := &alerts.Alerter{Pool: w.env.Store.Pool, Telegram: &api.TelegramAlerts{Pool: w.env.Store.Pool, Telegram: w.tg}}
	w.srv.Alerts = alerter
	before := len(w.bot.Sent(on))
	owner.must(200, "POST", "/v1/alerts/channels/"+ch["id"].(string)+"/test", nil)
	if got := w.bot.Sent(on)[before:]; len(got) != 1 || !strings.Contains(got[0].Text, "[Acme]") {
		t.Errorf("test alert: %+v", got)
	}

	// A person who blocked the bot is not retried forever: the outbox
	// records the failure and moves on.
	w.bot.Block(on, true)
	if _, err := w.tg.Send(context.Background(), telegram.Address(on), whatsapp.Message{Text: "x"}); err == nil {
		t.Error("send to a blocked chat succeeded")
	}
}
