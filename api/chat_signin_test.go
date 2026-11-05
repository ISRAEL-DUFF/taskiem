package api_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

var (
	signinLinkRe   = regexp.MustCompile(`/signin#token=([A-Za-z0-9_-]+)`)
	passwordLinkRe = regexp.MustCompile(`/reset-password#token=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
)

// signUpOnWhatsApp makes a business from WhatsApp, as an owner would: a
// chat-born owner with no password and a placeholder sign-in name.
func signUpOnWhatsApp(t *testing.T, w *waWorld, number, business, name string) {
	t.Helper()
	chatSignupOn(w.world, "whatsapp")
	w.say(t, number, "start")
	w.say(t, number, "yes")
	w.say(t, number, business)
	w.say(t, number, name)
	w.say(t, number, "2")
	if done := w.send(t, number, whatsapptest.Text("1, pos")); len(done) != 1 || !strings.Contains(done[0].Text, "is on Taskiem") {
		t.Fatalf("signed up: %+v", done)
	}
}

// Signing in on the web with the chat (docs/spec/chat-sign-in.md): an
// owner who started on WhatsApp asks for a code by number on the login
// page and signs in with it; a code works once, for its number, and five
// wrong lock it; an unknown number gets the same answer and nothing is
// sent; "sign in" in chat sends a link that signs in once, after its
// page's POST; each sign-in is told to the chat; "password" asks for an
// email first; the person may turn it off; "sign out everywhere" ends
// every web session.
func TestChatSignIn(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	const ada = "+2348031230101"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	anon := &client{t: t, base: w.base}

	start := func(phone string) {
		t.Helper()
		out := anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": phone})
		if !strings.Contains(out["status"].(string), "If this number is on a Taskiem account") {
			t.Fatalf("start: %v", out)
		}
		w.srv.WaitBackground()
	}
	code := func() string {
		t.Helper()
		last := w.graph.Last(t, ada)
		if last.Template != "taskiem_otp" {
			t.Fatalf("the code: %+v", last)
		}
		return codeRe.FindString(last.Text)
	}

	// The number as people type it; a wrong code, then the right one.
	start("0803 123 0101")
	c1 := code()
	if st, out := anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": "000000"}); st != 401 || !strings.Contains(out["error"].(string), "not right") {
		t.Fatalf("a wrong code: %d %v", st, out)
	}
	before := len(w.graph.Sent(ada))
	out := anon.must(200, "POST", "/v1/auth/chat/verify", map[string]any{"phone": "08031230101", "code": c1, "bearer": true})
	web := &client{t: t, base: w.base, token: out["token"].(string)}
	me := web.must(200, "GET", "/v1/me", nil)
	if me["auth_method"] != "chat" || me["user"].(map[string]any)["chat_signin"] != true {
		t.Fatalf("signed in with the chat: %v", me)
	}
	// Told on the chat.
	w.srv.WaitBackground()
	told := w.graph.Sent(ada)[before:]
	if len(told) != 1 || !strings.Contains(told[0].Text, "You signed in to Taskiem on the web") {
		t.Fatalf("told: %+v", told)
	}
	// Once.
	if st, _ := anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": c1, "bearer": true}); st != 401 {
		t.Fatalf("a code twice: %d", st)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'auth.chat_signin' AND detail->>'how' = 'code'`).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audited: %d %v", audited, err)
	}

	// An unknown number: the same answer, nothing sent.
	const nobody = "+2348031230199"
	start(nobody)
	if got := w.graph.Sent(nobody); len(got) != 0 {
		t.Fatalf("sent to an unknown number: %+v", got)
	}
	// Another number's code is no good for this one; five wrong lock it.
	start(ada)
	c2 := code()
	if st, _ := anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": nobody, "code": c2}); st != 401 {
		t.Fatalf("for another number: %d", st)
	}
	for range 5 {
		anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": "000001"})
	}
	if st, _ := anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": c2}); st != 401 {
		t.Fatalf("locked: %d", st)
	}

	// "sign in" in chat: a link; its page greets without using it; its POST
	// signs in, once.
	r := w.say(t, ada, "sign in")
	m := signinLinkRe.FindStringSubmatch(r)
	if m == nil || !strings.Contains(r, "10 minutes") {
		t.Fatalf("sign in: %q", r)
	}
	anon.must(204, "POST", "/v1/auth/chat/link/peek", map[string]any{"token": m[1]})
	anon.must(204, "POST", "/v1/auth/chat/link/peek", map[string]any{"token": m[1]}) // a peek uses nothing
	out = anon.must(200, "POST", "/v1/auth/chat/link", map[string]any{"token": m[1], "bearer": true})
	linked := &client{t: t, base: w.base, token: out["token"].(string)}
	linked.must(200, "GET", "/v1/me", nil)
	if st, _ := anon.do("POST", "/v1/auth/chat/link", map[string]any{"token": m[1], "bearer": true}); st != 401 {
		t.Fatalf("a link twice: %d", st)
	}
	anon.must(404, "POST", "/v1/auth/chat/link/peek", map[string]any{"token": m[1]})
	if st, _ := anon.do("POST", "/v1/auth/chat/link", map[string]any{"token": "nope", "bearer": true}); st != 401 {
		t.Fatalf("not a link: %d", st)
	}

	// "password" without an email: the email first.
	if r := w.say(t, ada, "password"); !strings.Contains(r, "you need an email first") || strings.Contains(r, "reset-password") {
		t.Fatalf("password: %q", r)
	}

	// Turned off: no code, no link; on again.
	linked.must(200, "PUT", "/v1/me/chat-signin", map[string]any{"on": false})
	before = len(w.graph.Sent(ada))
	start(ada)
	if got := w.graph.Sent(ada)[before:]; len(got) != 0 {
		t.Fatalf("a code when off: %+v", got)
	}
	if r := w.say(t, ada, "sign in"); !strings.Contains(r, "is off for you") || signinLinkRe.MatchString(r) {
		t.Fatalf("sign in when off: %q", r)
	}
	linked.must(200, "PUT", "/v1/me/chat-signin", map[string]any{"on": true})

	// Sign out everywhere: every web session ends.
	if r := w.say(t, ada, "sign out everywhere"); !strings.Contains(r, "2 session(s) ended") {
		t.Fatalf("sign out everywhere: %q", r)
	}
	web.must(401, "GET", "/v1/me", nil)
	linked.must(401, "GET", "/v1/me", nil)
	if r := w.say(t, ada, "sign out everywhere"); !strings.Contains(r, "not signed in on the web anywhere") {
		t.Fatalf("again: %q", r)
	}
}

// With an email, "password" sends the link to choose a password and names
// the email to sign in with, which then works on the login form.
func TestChatPasswordWithEmail(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	const bisi = "+2348031230102"
	signUpOnWhatsApp(t, w, bisi, "Bisi Salon", "Bisi Ade")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users u SET email = 'bisi@salon.test', email_verified_at = now()
		FROM whatsapp_bindings b WHERE b.user_id = u.id AND b.number = $1`, bisi); err != nil {
		t.Fatal(err)
	}
	r := w.say(t, bisi, "password")
	m := passwordLinkRe.FindStringSubmatch(r)
	if m == nil || !strings.Contains(r, "sign in on the web with your email (the one at *salon.test*)") {
		t.Fatalf("password: %q", r)
	}
	anon := &client{t: t, base: w.base}
	anon.must(200, "POST", "/v1/auth/password/reset", map[string]any{"token": m[1], "password": testPassword})
	w.login(t, "bisi@salon.test", testPassword)
}

// Administrators meet the same rule as with a password: one with a passkey
// signs in with it, where the deployment requires it (no code is sent and
// the link is refused); one without enrols a passkey before anything else.
func TestChatSignInAdministrators(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	const ada = "+2348031230103"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	w.srv.RequireAdminPasskeys = true
	anon := &client{t: t, base: w.base}

	// No passkey: signed in to enrol one.
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": ada})
	w.srv.WaitBackground()
	out := anon.must(200, "POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true})
	web := &client{t: t, base: w.base, token: out["token"].(string)}
	if me := web.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != true {
		t.Fatalf("enrol first: %v", me)
	}

	// A passkey: no code, and the link refused.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO webauthn_credentials (id, user_id, public_key, alg)
		SELECT '\x01'::bytea, b.user_id, '\x02'::bytea, -7 FROM whatsapp_bindings b WHERE b.number = $1`, ada); err != nil {
		t.Fatal(err)
	}
	before := len(w.graph.Sent(ada))
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": ada})
	w.srv.WaitBackground()
	if got := w.graph.Sent(ada)[before:]; len(got) != 0 {
		t.Fatalf("a code for an administrator with a passkey: %+v", got)
	}
	if r := w.say(t, ada, "sign in"); !strings.Contains(r, "Administrators sign in to Taskiem with their passkey") {
		t.Fatalf("sign in: %q", r)
	}
}

// A Telegram owner, found by the number they shared at sign-up, gets the
// code as a message and signs in with it.
func TestChatSignInTelegram(t *testing.T) {
	w := newTGWorld(t)
	chatSignupOn(w.world, "telegram")
	const acct = int64(8201)
	offer := w.say(t, acct, "/start")
	data := ""
	for _, row := range offer.Keyboard {
		for _, b := range row {
			if b.Text == "Start my business" {
				data = b.Data
			}
		}
	}
	w.update(t, acct, func(id int64) []byte { return telegramtest.Callback(id, acct, offer.MessageID, "q1", data) })
	w.say(t, acct, "yes")
	w.say(t, acct, "Bisi Salon")
	w.say(t, acct, "Bisi Ade")
	w.say(t, acct, "salon")
	w.say(t, acct, "transfer opay")
	if got := w.update(t, acct, func(id int64) []byte { return telegramtest.ContactMessage(id, acct, "2348031234601", acct) }); len(got) != 1 ||
		!strings.Contains(got[0].Text, "Bisi Salon") {
		t.Fatalf("made: %+v", got)
	}
	anon := &client{t: t, base: w.base}
	before := len(w.bot.Sent(acct))
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": "08031234601"})
	w.srv.WaitBackground()
	got := w.bot.Sent(acct)[before:]
	if len(got) != 1 || !strings.Contains(got[0].Text, "is your Taskiem sign-in code") {
		t.Fatalf("the code on Telegram: %+v", got)
	}
	out := anon.must(200, "POST", "/v1/auth/chat/verify", map[string]any{"phone": "+2348031234601", "code": codeRe.FindString(got[0].Text), "bearer": true})
	web := &client{t: t, base: w.base, token: out["token"].(string)}
	if me := web.must(200, "GET", "/v1/me", nil); me["auth_method"] != "chat" {
		t.Fatalf("signed in: %v", me)
	}
}
