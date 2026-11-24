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
		fmt.Sprint(buttonIDs(offer[0].Buttons)) != "[su:start su:have su:customer]" || !strings.Contains(offer[0].Text, "Type *no* for not now") {
		t.Fatalf("offer: %+v", offer)
	}
	// WhatsApp allows three buttons: Not now is typed (taskiem-for-everyone §12).
	if got := w.send(t, other, whatsapptest.Text("No")); len(got) != 1 || !strings.Contains(got[0].Text, "Send *start* any time") {
		t.Fatalf("not now: %+v", got)
	}
	// A button pressed on an older offer still works.
	if got := w.press(t, other, "su:no"); len(got) != 1 || !strings.Contains(got[0].Text, "Send *start* any time") {
		t.Fatalf("not now, pressed: %+v", got)
	}
	// I'm a customer: where to chat with a business.
	if got := w.press(t, other, "su:customer"); len(got) != 1 || got[0].Text != "To chat with a business, open its Taskiem link: the business shares it on its page, its receipts and its messages." {
		t.Fatalf("a customer, with no app link: %+v", got)
	}
	w.srv.AppGetURL = "https://taskiem.test/app"
	if got := w.press(t, other, "su:customer"); len(got) != 1 ||
		got[0].Text != "To chat with a business, open its Taskiem link, or find it in the Taskiem app: https://taskiem.test/app" {
		t.Fatalf("a customer: %+v", got)
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
	// The first question: a business, or workflows (taskiem-for-everyone §18.3).
	if got := w.press(t, other, "su:yes"); len(got) != 1 || !strings.Contains(got[0].Text, "Is this for a business, or for workflows?") ||
		fmt.Sprint(buttonIDs(got[0].Buttons)) != "[su:kind:business su:kind:workflows]" {
		t.Fatalf("yes: %+v", got)
	}
	if got := w.say(t, other, "a shop"); !strings.Contains(got, "a business, or for workflows") {
		t.Fatalf("not a choice: %q", got)
	}
	if got := w.press(t, other, "su:kind:business"); len(got) != 1 || !strings.Contains(got[0].Text, "What is your business called?") {
		t.Fatalf("a business: %+v", got)
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
	w.press(t, ada, "su:kind:business")
	if got := w.say(t, ada, "Visit www.evil.example"); !strings.Contains(got, "no links") {
		t.Fatalf("a link for a name: %q", got)
	}
	if got := w.say(t, ada, "Ada Foods"); !strings.Contains(got, "your name") {
		t.Fatalf("business: %q", got)
	}
	if got := w.say(t, ada, "Ada Obi"); !strings.Contains(got, "What do you sell") {
		t.Fatalf("name: %q", got)
	}
	if got := w.say(t, ada, "10"); !strings.Contains(got, "What do you sell") {
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
	// Provisions is a shop: its areas, as chosen rows, and a business.
	if got := kindAndAreas(t, w.world, tenant); got != "business: links money orders stock storefront" {
		t.Errorf("kind and areas: %s", got)
	}
	// Asked once, in the sign-up's last message, whether other businesses
	// find it (business links): the default holds until it answers.
	if !strings.Contains(done[0].Text, "send *find us* to let them, or *hide us*") {
		t.Errorf("not asked about being found: %q", done[0].Text)
	}
	var asked bool
	var findable *bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT asked_at IS NOT NULL, findable FROM link_settings WHERE tenant_id = $1`, tenant).Scan(&asked, &findable); err != nil || !asked || findable != nil {
		t.Errorf("link settings: asked %v, findable %v, %v", asked, findable, err)
	}
	if r := w.say(t, ada, "find us"); !strings.Contains(r, "can now find Ada Foods") {
		t.Errorf("find us: %q", r)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT findable FROM link_settings WHERE tenant_id = $1`, tenant).Scan(&findable); err != nil || findable == nil || !*findable {
		t.Errorf("after find us: %v %v", findable, err)
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

}

// kindAndAreas is a tenant's kind and the areas it has on, from its own
// rows ("business: money orders", "workflows:").
func kindAndAreas(t *testing.T, w *world, tenant uuid.UUID) string {
	t.Helper()
	var out string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT t.kind || ':' || COALESCE((SELECT ' ' || string_agg(b.area, ' ' ORDER BY b.area)
		FROM business_areas b WHERE b.tenant_id = t.id AND b.enabled), '') FROM tenants t WHERE t.id = $1`, tenant).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A stranger on WhatsApp who wants workflows: their name only, nothing
// about selling, and a workspace named after them with every area off.
func TestChatSignupWorkflowsOnWhatsApp(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	const kemi = "+2348031230021"
	w.say(t, kemi, "start")
	w.say(t, kemi, "yes")
	if got := w.say(t, kemi, "workflows"); !strings.Contains(got, "your name") {
		t.Fatalf("workflows: %q", got)
	}
	done := w.send(t, kemi, whatsapptest.Text("Kemi Ola"))
	if len(done) != 1 || !strings.Contains(done[0].Text, "Done: *Kemi Ola's workflows* is on Taskiem") || !strings.Contains(done[0].Text, "Send *pin*") ||
		strings.Contains(done[0].Text, "find us") || fmt.Sprint(buttonIDs(done[0].Buttons)) != "[su:tpl:daily-reminder su:tpl:leave-request]" {
		t.Fatalf("done: %+v", done)
	}
	user, err := w.wa.UserOf(ctx, kemi)
	if err != nil || user == uuid.Nil {
		t.Fatalf("the number is linked: %v %v", user, err)
	}
	tenant, source, agreed := onboardingOf(t, w.world, user)
	if source != "whatsapp" || agreed != 2 {
		t.Fatalf("onboarding: %s, %d terms", source, agreed)
	}
	if got := kindAndAreas(t, w.world, tenant); got != "workflows:" {
		t.Errorf("kind and areas: %s", got)
	}
	var rows int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM business_areas WHERE tenant_id = $1`, tenant).Scan(&rows); err != nil || rows != 5 {
		t.Errorf("every area a row: %d %v", rows, err)
	}
	// Never asked to be found.
	var asked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM link_settings WHERE tenant_id = $1`, tenant).Scan(&asked); err != nil || asked != 0 {
		t.Errorf("asked about being found: %d %v", asked, err)
	}
	// Another business, for someone with an account: no terms, no name;
	// the same first question.
	if got := w.say(t, kemi, "new business"); !strings.Contains(got, "a business, or for workflows") {
		t.Fatalf("new business: %q", got)
	}
	// An approval tapped meanwhile goes where it always went.
	if got := w.press(t, kemi, "tk1.not-a-token"); len(got) != 1 || strings.Contains(got[0].Text, "business, or for workflows") {
		t.Fatalf("a button meanwhile: %+v", got)
	}
	if got := w.say(t, kemi, "1"); !strings.Contains(got, "What is your business called?") {
		t.Fatalf("1 for a business: %q", got)
	}
	w.say(t, kemi, "Kemi Fashion")
	w.say(t, kemi, "fashion")
	if got := w.say(t, kemi, "transfer"); !strings.Contains(got, "Done: *Kemi Fashion*") {
		t.Fatalf("a business: %q", got)
	}
	// Workflows again, for someone with an account: made at once, named
	// after them.
	w.say(t, kemi, "new business")
	made := w.press(t, kemi, "su:kind:workflows")
	if len(made) != 1 || !strings.Contains(made[0].Text, "Done: *Kemi Ola's workflows*") ||
		fmt.Sprint(buttonIDs(made[0].Buttons)) != "[su:tpl:daily-reminder su:tpl:leave-request]" {
		t.Fatalf("workflows: %+v", made)
	}
	var kinds string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(t.kind, ' ' ORDER BY t.created_at) FROM memberships m JOIN tenants t ON t.id = m.tenant_id
		WHERE m.user_id = $1 AND m.role = 'owner'`, user).Scan(&kinds); err != nil || kinds != "workflows business workflows" {
		t.Errorf("Kemi's: %q %v", kinds, err)
	}
}

// What a business sells sets the areas it starts with; "something else"
// keeps every area but Schools, as before the question.
func TestChatSignupSellsSetAreas(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	w.srv.ChatSignup.PerHour = 100
	for i, c := range []struct{ sells, want string }{
		{"7", "business: links money"},  // services
		{"5", "business: money"},        // school (its own switch is school_settings)
		{"8", "business: links orders"}, // deliveries
		{"9", ""},                       // something else
	} {
		number := fmt.Sprintf("+23480312301%02d", 30+i)
		for _, m := range []string{"start", "yes", "1", fmt.Sprintf("Shop %d", i), "Bisi", c.sells} {
			w.say(t, number, m)
		}
		if got := w.say(t, number, "1"); !strings.Contains(got, "Done") {
			t.Fatalf("%s: %q", c.sells, got)
		}
		user, _ := w.wa.UserOf(ctx, number)
		tenant, _, _ := onboardingOf(t, w.world, user)
		got := kindAndAreas(t, w.world, tenant)
		if c.sells == "9" {
			// No rows: the default holds (areas.DefaultOn).
			var rows int
			if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM business_areas WHERE tenant_id = $1`, tenant).Scan(&rows); err != nil || rows != 0 || got != "business:" {
				t.Errorf("something else: %s, %d rows %v", got, rows, err)
			}
			continue
		}
		if got != c.want {
			t.Errorf("sells %s: %s, want %s", c.sells, got, c.want)
		}
		var schoolsOn bool
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_settings WHERE tenant_id = $1 AND enabled)`, tenant).Scan(&schoolsOn); err != nil || schoolsOn != (c.sells == "5") {
			t.Errorf("sells %s: schools %v %v", c.sells, schoolsOn, err)
		}
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
		w.say(t, n, "1")
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
	if k := w.say(t, acct, "yes"); !strings.Contains(k.Text, "a business, or for workflows") || len(k.Keyboard) != 2 {
		t.Fatalf("the kind: %+v", k)
	}
	w.say(t, acct, "business")
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
	w.say(t, acct2, "business")
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
	w.say(t, ada, "business")
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
	for _, m := range []string{"start", "yes", "business", "Bola Phones", "Bola", "4", "1"} {
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
	if got := w.send(t, "+2348031239503", whatsapptest.Text("abeg who una be")); len(got) != 1 || fmt.Sprint(buttonIDs(got[0].Buttons)) != "[su:start su:have su:customer]" {
		t.Fatalf("not routed: %+v", got)
	}
	// What the model may not answer for a stranger is not acted on.
	answer = `{"intent":"status","argument":""}`
	if got := w.send(t, "+2348031239504", whatsapptest.Text("wetin don fail today o")); len(got) != 1 || fmt.Sprint(buttonIDs(got[0].Buttons)) != "[su:start su:have su:customer]" {
		t.Fatalf("a command for a stranger: %+v", got)
	}
}

// emailOf is a person's sign-in name.
func emailOf(t *testing.T, w *world, user uuid.UUID) string {
	t.Helper()
	var email string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT email FROM users WHERE id = $1`, user).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

// A reissued number is a stranger however its binding ends (F18): after
// a chat-born owner unlinks their number on the web, or links another,
// the sign-in name made from the old number is released, so the number's
// next holder signing up is a new person, not the old owner.
func TestReleasedNumberIsAStranger(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	const n1, n2, n3 = "+2348031235501", "+2348031235502", "+2348031235503"
	anon := &client{t: t, base: w.base}
	// born signs up from number and signs in on the web with the chat's
	// one-time sign-in link.
	born := func(number, business string) (uuid.UUID, *client) {
		for _, m := range []string{"start", "yes", "business", business, "Ada", "1", "1"} {
			w.say(t, number, m)
		}
		u, _ := w.wa.UserOf(ctx, number)
		if u == uuid.Nil {
			t.Fatalf("no chat-born account for %s", number)
		}
		m := signinLinkRe.FindStringSubmatch(w.say(t, number, "sign in"))
		if m == nil {
			t.Fatalf("no sign-in link for %s", number)
		}
		out := anon.must(200, "POST", "/v1/auth/chat/link", map[string]any{"token": m[1], "bearer": true})
		return u, &client{t: t, base: w.base, token: out["token"].(string)}
	}

	// Unlinked on the web.
	a, web := born(n1, "Ada Foods")
	web.must(204, "DELETE", "/v1/me/whatsapp", nil)
	if e := emailOf(t, w.world, a); !strings.HasPrefix(e, "released+") {
		t.Fatalf("after unlinking, the sign-in name is %s", e)
	}
	for _, m := range []string{"hello", "start", "yes", "business", "Bola Phones", "Bola", "4", "1"} {
		w.say(t, n1, m)
	}
	if b, _ := w.wa.UserOf(ctx, n1); b == uuid.Nil || b == a || memberRoles(t, w.world, b, "Ada Foods") != "" {
		t.Fatalf("the number's next holder is %v (the old owner %v), in Ada Foods as %q", b, a, memberRoles(t, w.world, b, "Ada Foods"))
	}

	// Linked to another number on the web.
	c, web := born(n2, "Chi Foods")
	web.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": n3, "password": "chi-web-password-1"})
	web.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, n3).Text)})
	if e := emailOf(t, w.world, c); !strings.HasPrefix(e, "released+") {
		t.Fatalf("after linking another number, the sign-in name is %s", e)
	}
	for _, m := range []string{"hello", "start", "yes", "business", "Dayo Phones", "Dayo", "4", "1"} {
		w.say(t, n2, m)
	}
	if d, _ := w.wa.UserOf(ctx, n2); d == uuid.Nil || d == c {
		t.Fatalf("the old number's next holder is %v (the old owner %v)", d, c)
	}

	// A Telegram account's sign-in name goes with its binding the same way.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email = 'owner+0123456789abcdef@telegram.taskiem.invalid' WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO telegram_bindings (user_id, telegram_id) VALUES ($1, 8201)`, c); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.App.Exec(ctx, `SELECT taskiem_tg_unbind($1)`, c); err != nil {
		t.Fatal(err)
	}
	if e := emailOf(t, w.world, c); e != "released+"+c.String()+"@telegram.taskiem.invalid" {
		t.Fatalf("after unlinking Telegram, the sign-in name is %s", e)
	}
}

// Workflows on Telegram: the kind's button, the person's name, their own
// contact last, and a workspace with every area off.
func TestChatSignupWorkflowsOnTelegram(t *testing.T) {
	ctx := context.Background()
	w := newTGWorld(t)
	chatSignupOn(w.world, "telegram")
	const acct = int64(8111)
	w.say(t, acct, "start")
	kind := w.say(t, acct, "yes")
	data := ""
	for _, row := range kind.Keyboard {
		for _, b := range row {
			if b.Text == "Workflows" {
				data = b.Data
			}
		}
	}
	if data == "" {
		t.Fatalf("no Workflows button: %+v", kind)
	}
	got := w.update(t, acct, func(id int64) []byte { return telegramtest.Callback(id, acct, kind.MessageID, "q1", data) })
	if len(got) < 1 || !strings.Contains(got[len(got)-1].Text, "your name") {
		t.Fatalf("after Workflows: %+v", got)
	}
	if c := w.say(t, acct, "Tolu Bello"); !c.AskContact {
		t.Fatalf("contact asked: %+v", c)
	}
	got = w.update(t, acct, func(id int64) []byte { return telegramtest.ContactMessage(id, acct, "2348031234611", acct) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "Tolu Bello's workflows") {
		t.Fatalf("made: %+v", got)
	}
	user, err := w.tg.UserOf(ctx, acct)
	if err != nil || user == uuid.Nil {
		t.Fatalf("linked: %v %v", user, err)
	}
	tenant, source, _ := onboardingOf(t, w.world, user)
	if got := kindAndAreas(t, w.world, tenant); source != "telegram" || got != "workflows:" {
		t.Errorf("%s: %s", source, got)
	}
}
