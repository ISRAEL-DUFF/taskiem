package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/termii"
)

// drop forgets the emails containing text.
func (m *fakeMail) drop(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.sent[:0]
	for _, x := range m.sent {
		if !strings.Contains(x, text) {
			kept = append(kept, x)
		}
	}
	m.sent = kept
}

var verifyLink = regexp.MustCompile(`https://app\.taskiem\.test/verify-email#token=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)

func signupBody(tenant, email string) map[string]any {
	return map[string]any{"tenant": tenant, "email": email, "name": "Ada Obi", "password": "correct horse battery", "bearer": true}
}

// TestSignupRefusesAbuse: the checks a public signup form needs, none of
// which call a third party.
func TestSignupRefusesAbuse(t *testing.T) {
	w := newWorld(t)
	w.srv.SignupBlockedDomains = []string{"spam.example"}
	w.srv.LoginBurst = 100
	anon := &client{t: t, base: w.base}
	for name, body := range map[string]map[string]any{
		"honeypot":            {"tenant": "Ada Stores", "email": "ada@stores.test", "password": "correct horse battery", "website": "http://x"},
		"throwaway":           signupBody("Ada Stores", "ada@mailinator.com"),
		"throwaway subdomain": signupBody("Ada Stores", "ada@eu.yopmail.com"),
		"operator's list":     signupBody("Ada Stores", "ada@mx.spam.example"),
		"no domain dot":       signupBody("Ada Stores", "ada@localhost"),
		"display name":        signupBody("Ada Stores", "Ada <ada@stores.test>"),
		"link in name":        signupBody("Verify at https://evil.test", "ada@stores.test"),
		"domain in name":      signupBody("Pay at evil.example now", "ada@stores.test"),
		"email in name":       signupBody("ada@stores.test", "ada@stores.test"),
		"one letter":          signupBody("A", "ada@stores.test"),
		"short password":      {"tenant": "Ada Stores", "email": "ada@stores.test", "password": "short"},
		"password is email":   {"tenant": "Ada Stores", "email": "ada.obi@stores.test", "password": "ADA.OBI@stores.test"},
	} {
		if st, out := anon.do("POST", "/v1/signup", body); st != 400 {
			t.Errorf("%s: %d %v", name, st, out)
		}
	}
	// JSON only: a cross-site form cannot sign anyone up.
	resp, err := http.Post(w.base+"/v1/signup", "application/x-www-form-urlencoded", strings.NewReader("tenant=x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form signup: %d", resp.StatusCode)
	}
	// Ordinary names pass.
	anon.must(201, "POST", "/v1/signup", signupBody("St. Mary's Stores Ltd.", "ada@stores.test"))
	if st, out := anon.do("POST", "/v1/signup", signupBody("Other", "ADA@stores.test")); st != 409 {
		t.Errorf("same email again: %d %v", st, out)
	}
	if out := anon.must(200, "GET", "/v1/signup", nil); out["enabled"] != true {
		t.Errorf("signup options: %v", out)
	}
}

// TestSignupLimitPerAddressAcrossReplicas: the count is in the database,
// so a second API replica sees the first one's signups.
func TestSignupLimitPerAddressAcrossReplicas(t *testing.T) {
	w := newWorld(t)
	w.srv.SignupPerAddress = 2
	other := &api.Server{Store: w.env.Store, Vault: w.env.Vault, Registry: w.env.Registry, AllowSignup: true, SignupPerAddress: 2, Logger: slog.New(slog.DiscardHandler)}
	ts := httptest.NewServer(other.Handler())
	t.Cleanup(ts.Close)
	a, b := &client{t: t, base: w.base}, &client{t: t, base: ts.URL}
	a.must(201, "POST", "/v1/signup", signupBody("One", "one@stores.test"))
	b.must(201, "POST", "/v1/signup", signupBody("Two", "two@stores.test"))
	if st, out := a.do("POST", "/v1/signup", signupBody("Three", "three@stores.test")); st != 429 {
		t.Errorf("third signup from one address: %d %v", st, out)
	}
	if st, _ := b.do("POST", "/v1/signup", signupBody("Three", "three@stores.test")); st != 429 {
		t.Errorf("third signup on the other replica: %d", st)
	}
	// Signup off: GET says so, POST is not there.
	off := &api.Server{Store: w.env.Store, Vault: w.env.Vault, Registry: w.env.Registry, Logger: slog.New(slog.DiscardHandler)}
	ts2 := httptest.NewServer(off.Handler())
	t.Cleanup(ts2.Close)
	c := &client{t: t, base: ts2.URL}
	if out := c.must(200, "GET", "/v1/signup", nil); out["enabled"] != false {
		t.Errorf("signup off: %v", out)
	}
	if st, _ := c.do("POST", "/v1/signup", signupBody("Four", "four@stores.test")); st != 405 && st != 404 {
		t.Errorf("signup off POST: %d", st)
	}
}

// TestSignupVerifyEmail: signed in at once; a confirmation link by email;
// invitations and API keys wait for it; the link works once, only for its
// person.
func TestSignupVerifyEmail(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	anon := &client{t: t, base: w.base}
	out := anon.must(201, "POST", "/v1/signup", signupBody("Ada Stores", "ada@stores.test"))
	if out["verification_sent"] != true || out["token"] == nil {
		t.Fatalf("signup: %v", out)
	}
	owner := &client{t: t, base: w.base, token: out["token"].(string)}
	me := owner.must(200, "GET", "/v1/me", nil)
	if me["user"].(map[string]any)["email_verified"] != false {
		t.Fatalf("me: %v", me)
	}
	w.srv.WaitBackground()
	sent := mail.all()
	if len(sent) != 1 || !strings.Contains(sent[0], "To: ada@stores.test") {
		t.Fatalf("emails: %v", sent)
	}
	m := verifyLink.FindStringSubmatch(strings.ReplaceAll(sent[0], "=\r\n", ""))
	if m == nil {
		t.Fatalf("no link in:\n%s", sent[0])
	}
	token := m[1]

	// Held back until confirmed; building is not.
	if st, out := owner.do("POST", "/v1/members", map[string]any{"email": "op@stores.test", "password": testPassword, "roles": []string{"operator"}}); st != 403 || !strings.Contains(out["error"].(string), "confirm your email") {
		t.Errorf("invite before confirming: %d %v", st, out)
	}
	if st, _ := owner.do("POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.start"}}); st != 403 {
		t.Errorf("API key before confirming: %d", st)
	}
	publishFlow(t, owner, loanFlow)

	// Someone else's session cannot use the link, and a wrong secret counts.
	other := w.tenant(t, "Other", "owner@other.test")
	if st, _ := other.do("POST", "/v1/me/email/verify", map[string]any{"token": token}); st != 400 {
		t.Errorf("another tenant used the link: %d", st)
	}
	sel, _, _ := strings.Cut(token, ".")
	if st, _ := owner.do("POST", "/v1/me/email/verify", map[string]any{"token": sel + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}); st != 400 {
		t.Errorf("wrong secret: %d", st)
	}
	owner.must(200, "POST", "/v1/me/email/verify", map[string]any{"token": token})
	if st, _ := owner.do("POST", "/v1/me/email/verify", map[string]any{"token": token}); st != 400 {
		t.Errorf("link used twice: %d", st)
	}
	if me := owner.must(200, "GET", "/v1/me", nil); me["user"].(map[string]any)["email_verified"] != true {
		t.Errorf("not verified: %v", me)
	}
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "op@stores.test", "password": testPassword, "roles": []string{"operator"}})
	owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.start"}})
	if len(auditActions(t, owner, "user.email.verify")) != 1 {
		t.Error("verification not audited")
	}
	// Already confirmed: nothing more is sent.
	w.srv.WaitBackground()
	before := len(mail.all())
	owner.must(200, "POST", "/v1/me/email/verify/resend", nil)
	w.srv.WaitBackground()
	if n := len(mail.all()); n != before {
		t.Errorf("%d emails after resend when confirmed, want %d", n, before)
	}
}

// TestUnconfirmedTenantCannotEmailStrangers: an email alert channel makes
// the platform's mail server write to any address, so a self-serve tenant
// confirms its email first; test messages are paced (security review R2).
func TestUnconfirmedTenantCannotEmailStrangers(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	anon := &client{t: t, base: w.base}
	out := anon.must(201, "POST", "/v1/signup", signupBody("Spam Stores", "ada@spam.test"))
	owner := &client{t: t, base: w.base, token: out["token"].(string)}
	channel := map[string]any{"kind": "email", "name": "Your bank account is locked", "to": []string{"victim@elsewhere.test"}}
	if st, out := owner.do("POST", "/v1/alerts/channels", channel); st != 403 || !strings.Contains(out["error"].(string), "confirm your email") {
		t.Fatalf("email channel before confirming: %d %v", st, out)
	}
	// Channels that reach only the tenant's own members are not held back.
	owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "whatsapp", "name": "On call", "to": []string{"ada@spam.test"}})

	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE users SET email_verified_at = now() WHERE email = 'ada@spam.test'`); err != nil {
		t.Fatal(err)
	}
	ch := owner.must(201, "POST", "/v1/alerts/channels", channel)
	id := ch["id"].(string)
	w.srv.WaitBackground()
	before := len(mail.all())
	limited := 0
	for range 12 {
		if st, _ := owner.do("POST", "/v1/alerts/channels/"+id+"/test", nil); st == 429 {
			limited++
		} else if st != 200 {
			t.Fatalf("test message: %d", st)
		}
	}
	if limited != 2 || len(mail.all())-before != 10 {
		t.Fatalf("%d test messages refused, %d sent; want 2 and 10", limited, len(mail.all())-before)
	}
}

func TestResendVerification(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	out := (&client{t: t, base: w.base}).must(201, "POST", "/v1/signup", signupBody("Ada Stores", "ada@stores.test"))
	owner := &client{t: t, base: w.base, token: out["token"].(string)}
	owner.must(202, "POST", "/v1/me/email/verify/resend", nil)
	w.srv.WaitBackground()
	sent := mail.all()
	if len(sent) != 2 {
		t.Fatalf("%d emails", len(sent))
	}
	first := verifyLink.FindStringSubmatch(strings.ReplaceAll(sent[0], "=\r\n", ""))[1]
	second := verifyLink.FindStringSubmatch(strings.ReplaceAll(sent[1], "=\r\n", ""))[1]
	// A new link replaces the old one.
	if st, _ := owner.do("POST", "/v1/me/email/verify", map[string]any{"token": first}); st != 400 {
		t.Errorf("replaced link: %d", st)
	}
	owner.must(200, "POST", "/v1/me/email/verify", map[string]any{"token": second})
	owner.must(200, "POST", "/v1/me/email/verify/resend", nil) // already confirmed
}

// TestOnboardingToFirstRun is gate G4's path through the API: signup,
// a Termii connection (a fake provider), the owner's number, a template,
// publish, a run; the checklist follows, and the time to the first
// successful run is stamped once.
func TestOnboardingToFirstRun(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		texts = append(texts, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message_id":"m1","message":"Successfully Sent","balance":99}`)
	}))
	t.Cleanup(fake.Close)
	w := newWorld(t)
	if err := w.env.Registry.Register(termii.New(termii.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	out := (&client{t: t, base: w.base}).must(201, "POST", "/v1/signup", signupBody("Ada Stores", "ada@stores.test"))
	owner := &client{t: t, base: w.base, token: out["token"].(string)}

	ob := owner.must(200, "GET", "/v1/onboarding", nil)
	if ob["self_serve"] != true || ob["done"].(float64) != 0 || ob["signed_up_at"] == nil || ob["seconds_to_first_run"] != nil || ob["can_send_email"] != false {
		t.Fatalf("new tenant's checklist: %v", ob)
	}
	items := func() map[string]bool {
		got := map[string]bool{}
		for _, it := range owner.must(200, "GET", "/v1/onboarding", nil)["items"].([]any) {
			m := it.(map[string]any)
			got[m["id"].(string)] = m["done"].(bool)
		}
		return got
	}
	if got := items(); len(got) != 6 || got["verify_email"] || got["first_run"] {
		t.Fatalf("items: %v", got)
	}

	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "termii@1", "environment": "prod", "name": "main", "credentials": map[string]string{"api_key": "TL-test"}})
	owner.must(204, "PUT", "/v1/variables/prod/owner_phone", map[string]any{"value": "2348012345678"})
	inst := owner.must(201, "POST", "/v1/templates/new-order-alert-sms/instantiate", map[string]any{"params": map[string]any{}})
	if probs := inst["problems"].([]any); len(probs) > 0 {
		t.Fatalf("template problems: %v", probs)
	}
	wf := inst["id"].(string)
	if got := items(); !got["connection"] || !got["workflow"] || got["publish"] {
		t.Fatalf("after the draft: %v", got)
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"order_id": "A-1001", "customer_name": "Chidi", "total_naira": 25000}})
	w.env.Drain(t)
	if st := owner.must(200, "GET", "/v1/runs/"+run["run_id"].(string), nil)["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run status %v", st)
	}
	mu.Lock()
	if len(texts) != 1 || !strings.Contains(texts[0], "New order A-1001 from Chidi") || !strings.Contains(texts[0], "2348012345678") {
		t.Errorf("texts: %v", texts)
	}
	mu.Unlock()
	ob = owner.must(200, "GET", "/v1/onboarding", nil)
	if ob["first_run_at"] == nil || ob["seconds_to_first_run"] == nil || ob["seconds_to_first_run"].(float64) < 0 {
		t.Fatalf("first run not stamped: %v", ob)
	}
	if got := items(); !got["publish"] || !got["first_run"] || got["invite"] {
		t.Errorf("after the run: %v", got)
	}
	// Stamped once: a second run leaves it.
	first := ob["first_run_at"]
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"order_id": "A-1002", "customer_name": "Chidi", "total_naira": 100}})
	w.env.Drain(t)
	if again := owner.must(200, "GET", "/v1/onboarding", nil)["first_run_at"]; again != first {
		t.Errorf("first run moved: %v -> %v", first, again)
	}
	var stored int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM tenant_onboarding WHERE first_run_id = $1`, run["run_id"]).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("first_run_id: %d %v", stored, err)
	}

	// Inviting someone completes it; dismissing hides it for the tenant.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "op@stores.test", "password": testPassword, "roles": []string{"operator"}})
	if got := items(); !got["invite"] {
		t.Errorf("invite: %v", got)
	}
	op := w.login(t, "op@stores.test", testPassword)
	if st, _ := op.do("POST", "/v1/onboarding/dismiss", nil); st != 403 {
		t.Errorf("operator dismissed: %d", st)
	}
	owner.must(204, "POST", "/v1/onboarding/dismiss", nil)
	if ob := op.must(200, "GET", "/v1/onboarding", nil); ob["dismissed"] != true {
		t.Errorf("dismissed: %v", ob)
	}
}

// TestOnboardingOtherTenants: tenants that did not sign themselves up get
// the checklist from their state, nothing measured; another tenant's runs
// never count.
func TestOnboardingOtherTenants(t *testing.T) {
	w := newWorld(t)
	a := w.tenant(t, "Alpha", "owner@alpha.test")
	b := w.tenant(t, "Beta", "owner@beta.test")
	// Make Beta look like a bootstrapped tenant.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `DELETE FROM tenant_onboarding WHERE tenant_id = (SELECT tenant_id FROM memberships m JOIN users u ON u.id = m.user_id WHERE u.email = 'owner@beta.test')`); err != nil {
		t.Fatal(err)
	}
	wf := publishFlow(t, a, `{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"manual"},"steps":[{"id":"s","type":"transform","config":{"output":{"ok":true}}}]}`)
	a.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	ob := b.must(200, "GET", "/v1/onboarding", nil)
	if ob["self_serve"] != false || ob["signed_up_at"] != nil {
		t.Errorf("bootstrapped tenant: %v", ob)
	}
	for _, it := range ob["items"].([]any) {
		m := it.(map[string]any)
		if m["id"] != "verify_email" && m["done"] == true {
			t.Errorf("Beta sees Alpha's progress: %v", m)
		}
	}
	if ob := a.must(200, "GET", "/v1/onboarding", nil); ob["seconds_to_first_run"] == nil {
		t.Errorf("Alpha: %v", ob)
	}
}
