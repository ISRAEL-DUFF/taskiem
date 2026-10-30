package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

var testTerms = api.TermsConfig{URL: "https://taskiem.test/terms", Version: "2026-10", PrivacyVersion: "2026-10p"}

// chatSignupOn turns sign-up from chat on for the channels.
func chatSignupOn(w *world, channels ...string) {
	w.srv.ChatSignup = api.ChatSignupConfig{Channels: channels}
	w.srv.Terms = testTerms
}

// onboardingOf is how the business a person made from chat was recorded:
// its source, and the terms they agreed to.
func onboardingOf(t *testing.T, w *world, user uuid.UUID) (tenant uuid.UUID, source string, terms int) {
	t.Helper()
	ctx := context.Background()
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT o.tenant_id, o.source FROM tenant_onboarding o WHERE o.signed_up_by = $1 ORDER BY o.signed_up_at LIMIT 1`, user).
		Scan(&tenant, &source); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM terms_acceptances WHERE user_id = $1 AND accepted_at IS NOT NULL
		AND ((document = 'terms' AND version = '2026-10') OR (document = 'privacy' AND version = '2026-10p'))`, user).Scan(&terms); err != nil {
		t.Fatal(err)
	}
	return tenant, source, terms
}

func buttonIDs(bs []whatsapp.Button) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

// A stranger on the shared WhatsApp number signs up from the chat: the
// offer, the terms (No makes nothing, cancel makes nothing), four
// questions, the business, the binding, three templates, and the first
// one built without the AI. A linked person makes another business with
// "new business", without the account steps.
func TestChatSignupOnWhatsApp(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	// The first template tells the owner (notify@1): nothing to connect.
	if err := w.env.Registry.Register(notify.New(&recordTeller{})); err != nil {
		t.Fatal(err)
	}
	const ada = "+2348031230001"
	// Off: the unbound message, as before.
	if got := w.say(t, ada, "hello"); !strings.Contains(got, "not linked to a Taskiem account") {
		t.Fatalf("off: %q", got)
	}
	chatSignupOn(w.world, "whatsapp")
	const other = "+2348031230002"
	offer := w.send(t, other, whatsapptest.Text("hello"))
	if len(offer) != 1 || !strings.Contains(offer[0].Text, "Do you want to start, for free?") ||
		fmt.Sprint(buttonIDs(offer[0].Buttons)) != "[su:start su:have su:no]" {
		t.Fatalf("offer: %+v", offer)
	}
	if got := w.press(t, other, "su:no"); len(got) != 1 || !strings.Contains(got[0].Text, "Send *start* any time") {
		t.Fatalf("not now: %+v", got)
	}
	// No to the terms makes nothing; nor does cancel later on.
	terms := w.press(t, other, "su:start")
	if len(terms) != 1 || !strings.Contains(terms[0].Text, testTerms.URL) || fmt.Sprint(buttonIDs(terms[0].Buttons)) != "[su:yes su:no]" {
		t.Fatalf("terms: %+v", terms)
	}
	if got := w.say(t, other, "no"); !strings.Contains(got, "Nothing was made") {
		t.Fatalf("no: %q", got)
	}
	w.say(t, other, "start")
	if got := w.press(t, other, "su:yes"); len(got) != 1 || !strings.Contains(got[0].Text, "What is your business called?") {
		t.Fatalf("yes: %+v", got)
	}
	if got := w.say(t, other, "cancel"); !strings.Contains(got, "nothing was made") {
		t.Fatalf("cancel: %q", got)
	}
	if u, _ := w.wa.UserOf(ctx, other); u != uuid.Nil {
		t.Fatal("a cancelled sign-up made an account")
	}

	// All the way.
	w.say(t, ada, "start")
	w.say(t, ada, "yes")
	if got := w.say(t, ada, "Visit www.evil.example"); !strings.Contains(got, "no links") {
		t.Fatalf("a link for a name: %q", got)
	}
	if got := w.say(t, ada, "Ada Foods"); !strings.Contains(got, "your name") {
		t.Fatalf("business: %q", got)
	}
	if got := w.say(t, ada, "Ada Obi"); !strings.Contains(got, "What do you sell") {
		t.Fatalf("name: %q", got)
	}
	if got := w.say(t, ada, "9"); !strings.Contains(got, "What do you sell") {
		t.Fatalf("no such choice: %q", got)
	}
	if got := w.say(t, ada, "2"); !strings.Contains(got, "How do customers pay you?") {
		t.Fatalf("sells: %q", got)
	}
	done := w.send(t, ada, whatsapptest.Text("1, pos"))
	if len(done) != 1 || !strings.Contains(done[0].Text, "Done: *Ada Foods* is on Taskiem") || !strings.Contains(done[0].Text, "Send *pin*") ||
		fmt.Sprint(buttonIDs(done[0].Buttons)) != "[su:tpl:daily-reminder su:tpl:sales-day-total su:tpl:pos-daily-summary]" {
		t.Fatalf("done: %+v", done)
	}
	user, err := w.wa.UserOf(ctx, ada)
	if err != nil || user == uuid.Nil {
		t.Fatalf("the number is linked: %v %v", user, err)
	}
	tenant, source, agreed := onboardingOf(t, w.world, user)
	if source != "whatsapp" || agreed != 2 {
		t.Fatalf("onboarding: %s, %d terms", source, agreed)
	}
	var email string
	var password *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT email, password_hash FROM users WHERE id = $1`, user).Scan(&email, &password); err != nil ||
		!strings.HasSuffix(email, "@phone.taskiem.invalid") || password != nil {
		t.Fatalf("the person: %s %v %v", email, password, err)
	}
	var actions []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(action ORDER BY chain_seq) FROM audit_log WHERE tenant_id = $1`, tenant).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(actions) != "[tenant.create terms.accept]" {
		t.Fatalf("audit: %v", actions)
	}

	// The first template, without the AI: its one missing detail, the
	// steps, saved as a draft.
	got := w.press(t, ada, "su:tpl:daily-reminder")
	if len(got) != 1 || !strings.Contains(got[0].Text, "What to remind you of") {
		t.Fatalf("template: %+v", got)
	}
	if got := w.send(t, ada, whatsapptest.Text("Count the cash and lock the shop")); len(got) != 1 || !strings.Contains(got[0].Text, "Every day at 08:00") || !strings.Contains(got[0].Text, "Save this as a draft workflow?") {
		t.Fatalf("detail: %+v", got)
	}
	if got := w.say(t, ada, "yes"); !strings.Contains(got, "Saved") && !strings.Contains(got, "saved") {
		t.Fatalf("saved: %q", got)
	}
	var drafts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM workflow_versions v JOIN workflows f ON f.id = v.workflow_id
		WHERE f.tenant_id = $1 AND v.template_id = 'daily-reminder'`, tenant).Scan(&drafts); err != nil || drafts != 1 {
		t.Fatalf("drafts: %d %v", drafts, err)
	}
	if got := w.say(t, ada, "cancel"); got == "" {
		t.Fatal("no answer")
	}

	// Another business: no terms, no name.
	if got := w.say(t, ada, "new business"); !strings.Contains(got, "What is your business called?") {
		t.Fatalf("new business: %q", got)
	}
	// An approval tapped meanwhile goes where it always went.
	if got := w.press(t, ada, "tk1.not-a-token"); len(got) != 1 || strings.Contains(got[0].Text, "business name") {
		t.Fatalf("a button meanwhile: %+v", got)
	}
	w.say(t, ada, "Ada Fashion")
	w.say(t, ada, "fashion")
	if got := w.say(t, ada, "transfer"); !strings.Contains(got, "Done: *Ada Fashion*") {
		t.Fatalf("second: %q", got)
	}
	var businesses int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE user_id = $1 AND role = 'owner'`, user).Scan(&businesses); err != nil || businesses != 2 {
		t.Fatalf("businesses: %d %v", businesses, err)
	}
}

// Who may sign up: the countries open, the hourly limit across everyone,
// and never on a business's own number (its strangers are its customers).
func TestChatSignupLimits(t *testing.T) {
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	if got := w.say(t, "+15550002222", "start"); !strings.Contains(got, "not open in your country") {
		t.Fatalf("country: %q", got)
	}
	w.srv.ChatSignup.Countries = []string{"234", "1"}
	if got := w.say(t, "+15550002222", "start"); !strings.Contains(got, "Reply *Yes*") {
		t.Fatalf("a country added: %q", got)
	}
	// Two made this hour, two allowed.
	w.srv.ChatSignup.PerHour = 2
	for i, n := range []string{"+2348031230011", "+2348031230012"} {
		w.say(t, n, "start")
		w.say(t, n, "yes")
		w.say(t, n, fmt.Sprintf("Shop %d", i))
		w.say(t, n, "Bisi")
		w.say(t, n, "1")
		if got := w.say(t, n, "1"); !strings.Contains(got, "Done") {
			t.Fatalf("%s: %q", n, got)
		}
	}
	if got := w.say(t, "+2348031230013", "start"); !strings.Contains(got, "try again in an hour") {
		t.Fatalf("the hour's limit: %q", got)
	}
	// The web form is not counted by the hour's limit.
	w.srv.ChatSignup.PerHour = 3
	w.tenant(t, "Web Shop", "web@shop.test")
	const third = "+2348031230014"
	if got := w.say(t, third, "start"); !strings.Contains(got, "Reply *Yes*") {
		t.Fatalf("the web's signup counted: %q", got)
	}
	w.say(t, third, "cancel")
	// Three businesses a day per number: as if this number had made them all.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE tenant_onboarding SET channel_hash = sha256(convert_to('chat-identity:' || $1, 'UTF8'))`, third); err != nil {
		t.Fatal(err)
	}
	w.srv.ChatSignup.PerHour = 100
	if got := w.say(t, third, "start"); !strings.Contains(got, "made 3 businesses today") {
		t.Fatalf("the day's limit: %q", got)
	}
}

// On Telegram the same, from /start, with the person's own contact before
// the business is made: someone else's is refused, and a number a
// WhatsApp-linked person has makes no second account.
func TestChatSignupOnTelegram(t *testing.T) {
	ctx := context.Background()
	w := newTGWorld(t)
	chatSignupOn(w.world, "telegram")
	const acct = int64(8101)
	offer := w.say(t, acct, "/start")
	if !strings.Contains(offer.Text, "Do you want to start, for free?") || len(offer.Keyboard) == 0 {
		t.Fatalf("offer: %+v", offer)
	}
	// The offer's Start button.
	data := ""
	for _, row := range offer.Keyboard {
		for _, b := range row {
			if b.Text == "Start my business" {
				data = b.Data
			}
		}
	}
	got := w.update(t, acct, func(id int64) []byte { return telegramtest.Callback(id, acct, offer.MessageID, "q1", data) })
	if len(got) < 1 || !strings.Contains(got[len(got)-1].Text, "Reply Yes") && !strings.Contains(got[len(got)-1].Text, "Reply *Yes*") {
		t.Fatalf("start: %+v", got)
	}
	w.say(t, acct, "yes")
	w.say(t, acct, "Bisi Salon")
	w.say(t, acct, "Bisi Ade")
	w.say(t, acct, "salon")
	if c := w.say(t, acct, "transfer opay"); !c.AskContact || !strings.Contains(c.Text, "Share my number") {
		t.Fatalf("contact asked: %+v", c)
	}
	contact := func(phone string, of int64) []telegramtest.Call {
		return w.update(t, acct, func(id int64) []byte { return telegramtest.ContactMessage(id, acct, phone, of) })
	}
	if got := contact("+2348031234599", 9999); len(got) != 1 || !strings.Contains(got[0].Text, "not your own number") {
		t.Fatalf("another's contact: %+v", got)
	}
	if u, _ := w.tg.UserOf(ctx, acct); u != uuid.Nil {
		t.Fatal("made before the contact")
	}
	got = contact("2348031234599", acct)
	if len(got) != 1 || !strings.Contains(got[0].Text, "Done: Bisi Salon is on Taskiem") && !strings.Contains(got[0].Text, "Done: *Bisi Salon*") {
		t.Fatalf("made: %+v", got)
	}
	user, err := w.tg.UserOf(ctx, acct)
	if err != nil || user == uuid.Nil {
		t.Fatalf("the account is linked: %v %v", user, err)
	}
	if _, source, agreed := onboardingOf(t, w.world, user); source != "telegram" || agreed != 2 {
		t.Fatalf("onboarding: %s, %d terms", source, agreed)
	}

	// A number already a WhatsApp-linked person's: no second account.
	owner := w.tenant(t, "Chidi Stores", "chidi@stores.test")
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number) SELECT u.id, '+2348035550000' FROM users u WHERE u.email = 'chidi@stores.test'`); err != nil {
		t.Fatal(err)
	}
	_ = owner
	const acct2 = int64(8102)
	w.say(t, acct2, "start")
	w.say(t, acct2, "yes")
	w.say(t, acct2, "Chidi Two")
	w.say(t, acct2, "Chidi")
	w.say(t, acct2, "2")
	w.say(t, acct2, "1")
	if got := w.update(t, acct2, func(id int64) []byte { return telegramtest.ContactMessage(id, acct2, "+2348035550000", acct2) }); len(got) != 1 ||
		!strings.Contains(got[0].Text, "already uses Taskiem") {
		t.Fatalf("taken: %+v", got)
	}
	if u, _ := w.tg.UserOf(ctx, acct2); u != uuid.Nil {
		t.Fatal("a second account")
	}
}

// A binding silent past the dormant days acts on nothing and is sent
// nothing until proven again: with the chat PIN in its form, or by linking
// again. The chat PIN is set from the chat by a person with no other
// credential. Bindings dormant as long again are removed.
func TestBindingDormant(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.flows()
	chatSignupOn(w.world, "whatsapp")
	w.srv.BindingDormantDays = 90
	const ada = "+2348031230101"
	w.say(t, ada, "start")
	w.say(t, ada, "yes")
	w.say(t, ada, "Ada Foods")
	w.say(t, ada, "Ada")
	w.say(t, ada, "1")
	w.say(t, ada, "1")
	user, _ := w.wa.UserOf(ctx, ada)

	// The chat PIN, in its form; a weak one refused there.
	const ep = "/channels/whatsapp/flows"
	form := w.send(t, ada, whatsapptest.Text("pin"))
	if len(form) != 1 || form[0].Flow != whatsapp.FlowPin {
		t.Fatalf("PIN form: %+v", form)
	}
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", form[0].FlowToken, map[string]any{"pin": "123456"})
	if st != 200 || !strings.Contains(fmt.Sprint(screenData(t, resp)["error"]), "not one digit repeated") {
		t.Fatalf("weak: %d %v", st, resp)
	}
	if st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", form[0].FlowToken, map[string]any{"pin": "482915"}); st != 200 || flowToken(t, resp) != form[0].FlowToken {
		t.Fatalf("set: %d %v", st, resp)
	}
	if got := w.send(t, ada, whatsapptest.FlowCompleted(form[0].FlowToken)); len(got) != 1 || !strings.Contains(got[0].Text, "chat PIN is set") {
		t.Fatalf("completion: %+v", got)
	}
	// Set once from the chat: holding the chat does not replace it.
	if got := w.say(t, ada, "change pin"); !strings.Contains(got, "You have a chat PIN already") {
		t.Fatalf("change pin: %q", got)
	}

	// Silent 91 days: dormant, sent nothing, and asked for the PIN.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_bindings SET last_active_at = now() - interval '91 days' WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	wake := w.send(t, ada, whatsapptest.Text("help"))
	if len(wake) != 1 || wake[0].Flow != whatsapp.FlowPin || !strings.Contains(wake[0].Text, "quiet for 90 days") {
		t.Fatalf("dormant: %+v", wake)
	}
	var sendable int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM whatsapp_bindings WHERE user_id = $1 AND dormant_at IS NULL`, user).Scan(&sendable); err != nil || sendable != 0 {
		t.Fatalf("not dormant: %d %v", sendable, err)
	}
	if st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", wake[0].FlowToken, map[string]any{"pin": "000000"}); st != 200 ||
		!strings.Contains(fmt.Sprint(screenData(t, resp)["error"]), "tries left") {
		t.Fatalf("wrong: %d %v", st, resp)
	}
	if st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", wake[0].FlowToken, map[string]any{"pin": "482915"}); st != 200 || flowToken(t, resp) != wake[0].FlowToken {
		t.Fatalf("right: %d %v", st, resp)
	}
	if got := w.send(t, ada, whatsapptest.FlowCompleted(wake[0].FlowToken)); len(got) != 1 || !strings.Contains(got[0].Text, "yours again") {
		t.Fatalf("woken: %+v", got)
	}
	if got := w.say(t, ada, "help"); strings.Contains(got, "quiet for") {
		t.Fatalf("still dormant: %q", got)
	}

	// A web-linked person with no PIN: told to link again; linking again
	// wakes it. The sweep then removes what stays dormant.
	owner := w.tenant(t, "Bisi Stores", "bisi@stores.test")
	const bisi = "+2348031230102"
	w.bind(t, owner, bisi)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_bindings SET last_active_at = now() - interval '200 days' WHERE number = $1`, bisi); err != nil {
		t.Fatal(err)
	}
	if got := w.say(t, bisi, "help"); !strings.Contains(got, "link it again") {
		t.Fatalf("no PIN: %q", got)
	}
	w.bind(t, owner, bisi)
	if got := w.say(t, bisi, "help"); strings.Contains(got, "quiet for") {
		t.Fatalf("linked again: %q", got)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_bindings SET last_active_at = now() - interval '91 days', dormant_at = now() - interval '91 days' WHERE number IN ($1, $2)`, bisi, ada); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if u, _ := w.wa.UserOf(ctx, bisi); u != uuid.Nil {
		t.Fatal("not removed")
	}
	if got := w.say(t, bisi, "hello"); !strings.Contains(got, "Do you want to start") {
		t.Fatalf("a stranger again: %q", got)
	}
	// A number reissued after its account made in chat was swept: its new
	// holder signs up as a new person, never the old owner.
	var email string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email); err != nil || !strings.HasPrefix(email, "released+") {
		t.Fatalf("the old sign-in name: %q %v", email, err)
	}
	for _, m := range []string{"start", "yes", "Bola Phones", "Bola", "4", "1"} {
		w.say(t, ada, m)
	}
	again, _ := w.wa.UserOf(ctx, ada)
	if again == uuid.Nil || again == user || memberRoles(t, w.world, again, "Ada Foods") != "" {
		t.Fatalf("the new holder is %v (old %v), in Ada Foods as %q", again, user, memberRoles(t, w.world, again, "Ada Foods"))
	}
}

// The web form asks the same terms, recorded with the web as the channel;
// an account from before them is asked once.
func TestTermsOnTheWeb(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	before := w.tenant(t, "Older Shop", "older@shop.test")
	w.srv.Terms = testTerms
	anon := &client{t: t, base: w.base}
	opts := anon.must(200, "GET", "/v1/signup", nil)
	if tm, _ := opts["terms"].(map[string]any); tm["url"] != testTerms.URL || tm["terms_version"] != "2026-10" {
		t.Fatalf("options: %v", opts)
	}
	req := map[string]any{"tenant": "New Shop", "email": "new@shop.test", "password": testPassword}
	if st, body := anon.do("POST", "/v1/signup", req); st != 400 || !strings.Contains(fmt.Sprint(body["error"]), "terms") {
		t.Fatalf("without agreeing: %d %v", st, body)
	}
	req["accept_terms"] = true
	anon.must(201, "POST", "/v1/signup", req)
	var channel string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT a.channel FROM terms_acceptances a JOIN users u ON u.id = a.user_id
		WHERE u.email = 'new@shop.test' AND a.document = 'terms' AND a.accepted_at IS NOT NULL`).Scan(&channel); err != nil || channel != "web" {
		t.Fatalf("recorded: %q %v", channel, err)
	}
	// The older account: asked until it answers, once.
	if me := before.must(200, "GET", "/v1/me", nil); me["terms"] == nil {
		t.Fatalf("not asked: %v", me)
	}
	before.must(200, "POST", "/v1/me/terms", map[string]any{"accept": false})
	if me := before.must(200, "GET", "/v1/me", nil); me["terms"] != nil {
		t.Fatalf("asked again: %v", me)
	}
	before.must(200, "POST", "/v1/me/terms", map[string]any{"accept": true})
	var accepted int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM terms_acceptances a JOIN users u ON u.id = a.user_id
		WHERE u.email = 'older@shop.test' AND a.accepted_at IS NOT NULL`).Scan(&accepted); err != nil || accepted != 2 {
		t.Fatalf("accepted: %d %v", accepted, err)
	}
}

// The ways in in a person's own words (docs/spec/chat-way-in.md 11): the
// word lists' signup phrases, and free text the model says means "sign
// me up", for that intent only.
func TestSignupInOwnWords(t *testing.T) {
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	answer := `{"intent":"signup","argument":""}`
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}, Model: &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: answer, StopReason: "end_turn"}, nil
	}}}
	// The word list.
	if got := w.say(t, "+2348031239501", "I wan open account"); !strings.Contains(got, testTerms.URL) {
		t.Fatalf("pidgin words: %q", got)
	}
	// Free text the model routes.
	if got := w.say(t, "+2348031239502", "abeg I need this thing for my provisions shop"); !strings.Contains(got, testTerms.URL) {
		t.Fatalf("routed: %q", got)
	}
	// Anything else: the offer, as before.
	answer = `{"intent":"unknown","argument":""}`
	if got := w.send(t, "+2348031239503", whatsapptest.Text("abeg who una be")); len(got) != 1 || fmt.Sprint(buttonIDs(got[0].Buttons)) != "[su:start su:have su:no]" {
		t.Fatalf("not routed: %+v", got)
	}
	// What the model may not answer for a stranger is not acted on.
	answer = `{"intent":"status","argument":""}`
	if got := w.send(t, "+2348031239504", whatsapptest.Text("wetin don fail today o")); len(got) != 1 || fmt.Sprint(buttonIDs(got[0].Buttons)) != "[su:start su:have su:no]" {
		t.Fatalf("a command for a stranger: %+v", got)
	}
}
