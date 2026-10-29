package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// One person, many doors (docs/spec/chat-way-in.md 10): a chat linked to
// a web account by an emailed link, with the same answer for an email
// with no account; a Telegram sign-up with a WhatsApp person's number; an
// email added from chat; the plan and paying from chat.

var chatLinkRe = regexp.MustCompile(`/link-chat#token=([A-Za-z0-9_-]{32})`)

// mailedTo is the last email sent to an address, and how many went there.
func mailedTo(mail *fakeMail, to string) (string, int) {
	var last string
	n := 0
	for _, m := range mail.all() {
		if strings.Contains(m, "To: "+to) {
			last, n = m, n+1
		}
	}
	return last, n
}

func TestChatLinkByEmail(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	mail := mailWorld(t, w.world)
	ada := w.tenant(t, "Ada Foods", "ada@foods.test")
	bob := w.tenant(t, "Bob Shop", "bob@shop.test")
	const number, other = "+2348031239001", "+2348031239002"

	w.send(t, number, whatsapptest.Text("hello"))
	if got := w.press(t, number, "su:have"); len(got) != 1 || !strings.Contains(got[0].Text, "What email do you sign in") {
		t.Fatalf("I already use Taskiem: %+v", got)
	}
	if got := w.say(t, number, "not an email"); !strings.Contains(got, "does not look like an email") {
		t.Fatalf("a bad email: %q", got)
	}
	sent := w.say(t, number, "Ada@Foods.test")
	w.srv.WaitBackground()
	// The same answer for an email with no account, and nothing sent.
	w.send(t, other, whatsapptest.Text("hello"))
	w.press(t, other, "su:have")
	none := w.say(t, other, "nobody@foods.test")
	w.srv.WaitBackground()
	if strings.Replace(sent, "ada@foods.test", "X", 1) != strings.Replace(none, "nobody@foods.test", "X", 1) || !strings.Contains(sent, "works once, for an hour") {
		t.Fatalf("answers differ:\n%q\n%q", sent, none)
	}
	if _, n := mailedTo(mail, "nobody@foods.test"); n != 0 {
		t.Fatal("mail to an email with no account")
	}
	msg, n := mailedTo(mail, "ada@foods.test")
	m := chatLinkRe.FindStringSubmatch(msg)
	if n != 1 || m == nil || !strings.Contains(msg, "WhatsApp (+234") {
		t.Fatalf("the link email (%d):\n%s", n, msg)
	}
	token := m[1]
	// Another account cannot use it, and that does not spend it.
	if st, out := bob.do("POST", "/v1/me/chat-link", map[string]any{"token": token}); st != 400 {
		t.Fatalf("another account: %d %v", st, out)
	}
	if u, _ := w.wa.UserOf(ctx, number); u != uuid.Nil {
		t.Fatal("linked before the link was opened")
	}
	before := len(w.graph.Sent(number))
	if out := ada.must(200, "POST", "/v1/me/chat-link", map[string]any{"token": token}); out["channel"] != "whatsapp" {
		t.Fatalf("link: %v", out)
	}
	w.srv.WaitBackground()
	if got := w.graph.Sent(number)[before:]; len(got) != 1 || !strings.Contains(got[0].Text, "this chat is linked to your Taskiem account") {
		t.Fatalf("the chat told: %+v", got)
	}
	adaID := uuid.MustParse(ada.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
	if u, _ := w.wa.UserOf(ctx, number); u != adaID {
		t.Fatalf("the number is %v's, want ada's", u)
	}
	if st, _ := ada.do("POST", "/v1/me/chat-link", map[string]any{"token": token}); st != 400 {
		t.Fatalf("a second use: %d", st)
	}
	if got := w.say(t, number, "help"); !strings.Contains(got, "status") {
		t.Fatalf("the conversation: %q", got)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'auth.link' AND detail->>'via' = 'email_link'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit: %d %v", audited, err)
	}
	// A second number for an account that has one: refused.
	w.send(t, other, whatsapptest.Text("hello"))
	w.press(t, other, "su:have")
	w.say(t, other, "ada@foods.test")
	w.srv.WaitBackground()
	msg, _ = mailedTo(mail, "ada@foods.test")
	if st, out := ada.do("POST", "/v1/me/chat-link", map[string]any{"token": chatLinkRe.FindStringSubmatch(msg)[1]}); st != 409 || !strings.Contains(fmt.Sprint(out["error"]), "another WhatsApp number") {
		t.Fatalf("a second number: %d %v", st, out)
	}
}

func TestTelegramWithAWhatsAppNumber(t *testing.T) {
	ctx := context.Background()
	w := newTGWorld(t)
	chatSignupOn(w.world, "telegram")
	mail := mailWorld(t, w.world)
	// A chat-born owner on WhatsApp, and a web owner who linked WhatsApp.
	born := w.tenant(t, "Bisi Salon", "bisi@salon.test")
	web := w.tenant(t, "Chidi Stores", "chidi@stores.test")
	for _, q := range []string{
		`UPDATE users SET email = 'owner+0011aa@phone.taskiem.invalid', password_hash = NULL WHERE email = 'bisi@salon.test'`,
		`INSERT INTO whatsapp_bindings (user_id, number) SELECT id, '+2348035550001' FROM users WHERE email = 'owner+0011aa@phone.taskiem.invalid'`,
		`INSERT INTO whatsapp_bindings (user_id, number) SELECT id, '+2348035550002' FROM users WHERE email = 'chidi@stores.test'`,
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	bornID := uuid.MustParse(born.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
	signUp := func(acct int64, phone string) []telegramtest.Call {
		for _, s := range []string{"start", "yes", "Another Shop", "Me", "2", "1"} {
			w.say(t, acct, s)
		}
		return w.update(t, acct, func(id int64) []byte { return telegramtest.ContactMessage(id, acct, phone, acct) })
	}
	// Chat-born: the number was its only proof, and the same proof joins.
	if got := signUp(8201, "+2348035550001"); len(got) != 1 || !strings.Contains(got[0].Text, "This Telegram is now linked to it too") {
		t.Fatalf("chat-born: %+v", got)
	}
	if u, _ := w.tg.UserOf(ctx, 8201); u != bornID {
		t.Fatalf("the Telegram account is %v's, want the WhatsApp person's", u)
	}
	var tenants int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tenant_onboarding WHERE source = 'telegram'`).Scan(&tenants); err != nil || tenants != 0 {
		t.Fatalf("a business made: %d %v", tenants, err)
	}
	// A web account: a number alone is not enough; its email gets the link.
	if got := signUp(8202, "+2348035550002"); len(got) != 1 || !strings.Contains(got[0].Text, "We emailed that account a link") {
		t.Fatalf("web account: %+v", got)
	}
	if u, _ := w.tg.UserOf(ctx, 8202); u != uuid.Nil {
		t.Fatal("linked by a number alone")
	}
	w.srv.WaitBackground()
	msg, n := mailedTo(mail, "chidi@stores.test")
	m := chatLinkRe.FindStringSubmatch(msg)
	if n != 1 || m == nil {
		t.Fatalf("the link email (%d):\n%s", n, msg)
	}
	if out := web.must(200, "POST", "/v1/me/chat-link", map[string]any{"token": m[1]}); out["channel"] != "telegram" {
		t.Fatalf("link: %v", out)
	}
	if u, _ := w.tg.UserOf(ctx, 8202); u == uuid.Nil {
		t.Fatal("not linked by the email link")
	}
}

var confirmEmailRe = regexp.MustCompile(`/confirm-email#(t=[0-9a-f-]{36}&token=[A-Za-z0-9_.-]+)`)

func TestEmailAndPlanFromChat(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	mail := mailWorld(t, w.world)
	cfg, err := billing.LoadConfig("../deploy/plans.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := billing.Store(ctx, w.env.DB.App, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	var checkoutEmail string
	ps := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		checkoutEmail, _ = body["email"].(string)
		_ = json.NewEncoder(rw).Encode(map[string]any{"status": true, "data": map[string]any{"authorization_url": "https://checkout.paystack.test/x"}})
	}))
	t.Cleanup(ps.Close)
	w.env.Store.Billing = true
	w.srv.Billing = &billing.Service{Pool: w.env.DB.App, Store: w.env.Store, Config: cfg, Enabled: true, Default: "paystack",
		Providers: map[string]billing.Provider{"paystack": &billing.Paystack{SecretKey: "sk_test", BaseURL: ps.URL}}}

	const ada = "+2348031239101"
	for _, s := range []string{"start", "yes", "Ada Foods", "Ada Obi", "1", "1"} {
		w.say(t, ada, s)
	}
	if got := w.send(t, ada, whatsapptest.Text("plan")); len(got) != 1 || !strings.Contains(got[0].Text, "free for") || fmt.Sprint(buttonIDs(got[0].Buttons)) != "[plan:pay]" {
		t.Fatalf("plan: %+v", got)
	}
	// No email of their own: asked first, since receipts go by email.
	if got := w.press(t, ada, "plan:pay"); len(got) != 1 || !strings.Contains(got[0].Text, "pay ada@example.com") {
		t.Fatalf("pay without an email: %+v", got)
	}
	if got := w.say(t, ada, "pay ada@foods.test"); !strings.Contains(got, "https://checkout.paystack.test/x") || checkoutEmail != "ada@foods.test" {
		t.Fatalf("pay: %q (paystack got %q)", got, checkoutEmail)
	}

	// An email of their own, confirmed from its link without signing in.
	if got := w.say(t, ada, "email"); !strings.Contains(got, "email ada@example.com") {
		t.Fatalf("email: %q", got)
	}
	if got := w.say(t, ada, "email ada@foods.test"); !strings.Contains(got, "A link is on its way to ada@foods.test") {
		t.Fatalf("email ada@...: %q", got)
	}
	w.srv.WaitBackground()
	msg, _ := mailedTo(mail, "ada@foods.test")
	m := confirmEmailRe.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("no confirmation link in:\n%s", msg)
	}
	q, _ := url.ParseQuery(m[1])
	anon := &client{t: t, base: w.base}
	if st, _ := anon.do("POST", "/v1/auth/email/confirm", map[string]any{"tenant": q.Get("t"), "token": q.Get("token") + "x"}); st != 400 {
		t.Fatalf("a wrong secret: %d", st)
	}
	if out := anon.must(200, "POST", "/v1/auth/email/confirm", map[string]any{"tenant": q.Get("t"), "token": q.Get("token")}); out["email"] != "ada@foods.test" {
		t.Fatalf("confirm: %v", out)
	}
	if st, _ := anon.do("POST", "/v1/auth/email/confirm", map[string]any{"tenant": q.Get("t"), "token": q.Get("token")}); st != 400 {
		t.Fatalf("a second use: %d", st)
	}
	var email string
	var verified bool
	user, _ := w.wa.UserOf(ctx, ada)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, user).Scan(&email, &verified); err != nil || email != "ada@foods.test" || !verified {
		t.Fatalf("the account's email: %q %v %v", email, verified, err)
	}
	if got := w.say(t, ada, "email"); !strings.Contains(got, "Your account's email is ada@foods.test") {
		t.Fatalf("email after: %q", got)
	}

	// On the web: a web account's email is no placeholder, and is
	// confirmed already where mail is off; Add your email refuses then.
	web := w.tenant(t, "Web Shop", "web@shop.test")
	if u := web.must(200, "GET", "/v1/me", nil)["user"].(map[string]any); u["email_placeholder"] != false {
		t.Fatalf("me: %v", u)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE email = 'web@shop.test'`); err != nil {
		t.Fatal(err)
	}
	if st, _ := web.do("POST", "/v1/me/email", map[string]any{"email": "other@shop.test"}); st != 409 {
		t.Fatalf("add email to a confirmed account: %d", st)
	}
}
