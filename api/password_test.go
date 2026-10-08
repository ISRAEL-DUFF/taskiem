package api_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
)

const newPassword = "a much better passphrase"

// mailWorld is a world that can send email, into a sink.
func mailWorld(t *testing.T, w *world) *fakeMail {
	t.Helper()
	mail := &fakeMail{}
	w.srv.Alerts = &alerts.Alerter{Mailer: mail, From: "no-reply@taskiem.test"}
	if w.srv.PublicURL == "" {
		w.srv.PublicURL = "https://app.taskiem.test"
	}
	if w.srv.LoginBurst == 0 {
		w.srv.LoginBurst = 100
	}
	return mail
}

var resetLink = regexp.MustCompile(`https://app\.taskiem\.test/reset-password#token=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)

// forgot asks for a reset link and returns the emails sent since.
func (w *world) forgot(t *testing.T, mail *fakeMail, email string) []string {
	t.Helper()
	before := len(mail.all())
	out := (&client{t: t, base: w.base}).must(202, "POST", "/v1/auth/password/forgot", map[string]any{"email": email})
	if !strings.Contains(out["status"].(string), "If that email belongs") {
		t.Fatalf("forgot answer: %v", out)
	}
	w.srv.WaitBackground()
	return mail.all()[before:]
}

// tokenFor asks for a reset link for email and returns its token.
func (w *world) tokenFor(t *testing.T, mail *fakeMail, email string) string {
	t.Helper()
	sent := w.forgot(t, mail, email)
	if len(sent) != 1 {
		t.Fatalf("%d emails for %s", len(sent), email)
	}
	if !strings.Contains(sent[0], "To: "+email) {
		t.Fatalf("email went elsewhere:\n%s", sent[0])
	}
	m := resetLink.FindStringSubmatch(sent[0])
	if m == nil {
		t.Fatalf("no reset link in:\n%s", sent[0])
	}
	return m[1]
}

func reset(t *testing.T, w *world, token, password string) (int, map[string]any) {
	t.Helper()
	return (&client{t: t, base: w.base}).do("POST", "/v1/auth/password/reset", map[string]any{"token": token, "password": password})
}

func auditActions(t *testing.T, c *client, action string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range c.must(200, "GET", "/v1/audit?action="+action, nil)["entries"].([]any) {
		out = append(out, e.(map[string]any))
	}
	return out
}

func TestForgotPasswordDoesNotTellWhoHasAnAccount(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "op@acme.test", "password": testPassword, "roles": []string{"operator"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "nopw@acme.test", "roles": []string{"viewer"}}) // SSO or passkeys only

	anon := &client{t: t, base: w.base}
	var answers []string
	for _, email := range []string{"op@acme.test", "nobody@acme.test", "nopw@acme.test", "OP@acme.test"} {
		st, out := anon.do("POST", "/v1/auth/password/forgot", map[string]any{"email": email})
		if st != 202 {
			t.Fatalf("%s: %d %v", email, st, out)
		}
		answers = append(answers, toJSON(out))
	}
	for _, a := range answers[1:] {
		if a != answers[0] {
			t.Errorf("answers differ: %s vs %s", a, answers[0])
		}
	}
	w.srv.WaitBackground()
	// Only the person with a password gets a link (twice: case does not
	// matter), and nobody without one is given a password this way.
	sent := mail.all()
	if len(sent) != 2 || !strings.Contains(sent[0], "To: op@acme.test") || !strings.Contains(sent[1], "To: OP@acme.test") {
		t.Fatalf("emails:\n%s", strings.Join(sent, "\n----\n"))
	}
	if strings.Contains(strings.Join(sent, ""), "nopw@") {
		t.Error("a passwordless member was sent a reset link")
	}

	// JSON only, like sign-in.
	req, _ := http.NewRequest("POST", w.base+"/v1/auth/password/forgot", strings.NewReader(`email=op@acme.test`))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Errorf("form body: %d", resp.StatusCode)
	}

	// Per email: past the limit the answer is the same, and nothing is sent.
	for range 4 {
		w.forgot(t, mail, "op@acme.test")
	}
	if n := len(mail.all()); n != 5 {
		t.Errorf("%d emails after the per-email limit (want 5)", n)
	}

	// Per address: too many requests are refused outright.
	limited := newWorld(t)
	mailWorld(t, limited)
	limited.srv.LoginBurst = 2
	lanon := &client{t: t, base: limited.base}
	lanon.must(202, "POST", "/v1/auth/password/forgot", map[string]any{"email": "a@x.test"})
	lanon.must(202, "POST", "/v1/auth/password/forgot", map[string]any{"email": "b@x.test"})
	lanon.must(429, "POST", "/v1/auth/password/forgot", map[string]any{"email": "c@x.test"})
}

func TestForgotPasswordWithoutMail(t *testing.T) {
	w := newWorld(t) // no SMTP, no public URL
	w.tenant(t, "Acme", "owner@acme.test")
	(&client{t: t, base: w.base}).must(202, "POST", "/v1/auth/password/forgot", map[string]any{"email": "owner@acme.test"})
	w.srv.WaitBackground()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM password_resets`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d links made without a way to send them (%v)", n, err)
	}
}

func TestPasswordReset(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "op@acme.test", "password": testPassword, "roles": []string{"operator"}})
	op := w.login(t, "op@acme.test", testPassword)
	opID := memberID(t, owner, "op@acme.test")
	// op belongs to a second tenant too.
	beta := w.tenant(t, "Beta", "owner@beta.test")
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "op@acme.test", "roles": []string{"viewer"}})
	op.must(200, "POST", "/v1/me/invitations/"+beta.must(200, "GET", "/v1/me", nil)["tenant_id"].(string)+"/accept", nil)
	other := w.login(t, "op@acme.test", testPassword)

	// Asking again replaces the earlier link.
	first := w.tokenFor(t, mail, "op@acme.test")
	tok := w.tokenFor(t, mail, "op@acme.test")
	if st, _ := reset(t, w, first, newPassword); st != 400 {
		t.Errorf("a replaced link worked: %d", st)
	}
	// A short password is refused without using the link.
	if st, _ := reset(t, w, tok, "short"); st != 400 {
		t.Errorf("short password: %d", st)
	}
	for _, bad := range []string{"", "nonsense", "a.b", tok + "x"} {
		if st, _ := reset(t, w, bad, newPassword); st != 400 {
			t.Errorf("token %q: %d", bad, st)
		}
	}
	// JSON only.
	req, _ := http.NewRequest("POST", w.base+"/v1/auth/password/reset", strings.NewReader(`{"token":"`+tok+`","password":"`+newPassword+`"}`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Errorf("text/plain reset: %d", resp.StatusCode)
	}

	// The reset sets the password, signs nobody in, and ends every session.
	req, _ = http.NewRequest("POST", w.base+"/v1/auth/password/reset", strings.NewReader(`{"token":"`+tok+`","password":"`+newPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) != 0 {
		t.Fatalf("reset: %d, cookies %v", resp.StatusCode, resp.Cookies())
	}
	if st, _ := reset(t, w, tok, "yet another passphrase"); st != 400 {
		t.Errorf("a link worked twice: %d", st)
	}
	for _, c := range []*client{op, other} {
		if st, _ := c.do("GET", "/v1/me", nil); st != 401 {
			t.Errorf("a session survived the reset: %d", st)
		}
	}
	if st, _ := (&client{t: t, base: w.base}).do("POST", "/v1/auth/login", map[string]any{"email": "op@acme.test", "password": testPassword}); st != 401 {
		t.Errorf("old password still signs in: %d", st)
	}
	w.login(t, "op@acme.test", newPassword)
	w.srv.WaitBackground()
	if last := mail.all()[len(mail.all())-1]; !strings.Contains(last, "To: op@acme.test") || !strings.Contains(last, "was just changed") {
		t.Errorf("no notice of the change:\n%s", last)
	}
	// Audited in every tenant the person belongs to.
	for _, c := range []*client{owner, beta} {
		got := auditActions(t, c, "auth.password.reset")
		if len(got) != 1 || got[0]["target"] != opID || !strings.Contains(toJSON(got[0]["detail"]), `"sessions_ended":2`) {
			t.Errorf("audit: %v", got)
		}
	}
	// Owners keep their sessions: nothing of theirs changed.
	owner.must(200, "GET", "/v1/me", nil)

	// Links expire.
	tok = w.tokenFor(t, mail, "op@acme.test")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE password_resets SET expires_at = now() - interval '1 second' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if st, _ := reset(t, w, tok, "yet another passphrase"); st != 400 {
		t.Errorf("an expired link worked: %d", st)
	}

	// Five wrong secrets lock the link, even against the right one.
	tok = w.tokenFor(t, mail, "op@acme.test")
	selector, _, _ := strings.Cut(tok, ".")
	for range 5 {
		guess := make([]byte, 32)
		_, _ = rand.Read(guess)
		if st, _ := reset(t, w, selector+"."+base64.RawURLEncoding.EncodeToString(guess), "yet another passphrase"); st != 400 {
			t.Fatalf("wrong secret: %d", st)
		}
	}
	if st, _ := reset(t, w, tok, "yet another passphrase"); st != 400 {
		t.Errorf("a locked link worked: %d", st)
	}
	w.login(t, "op@acme.test", newPassword)

	// Disabled people get no link.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE email = 'op@acme.test'`); err != nil {
		t.Fatal(err)
	}
	if sent := w.forgot(t, mail, "op@acme.test"); len(sent) != 0 {
		t.Errorf("a disabled person got a link")
	}
}

func TestPasswordResetKeepsAdministratorsOnPasskeys(t *testing.T) {
	w := passkeyWorld(t)
	mail := mailWorld(t, w)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	key := addPasskey(t, owner, "Laptop")

	tok := w.tokenFor(t, mail, "owner@acme.test")
	if st, out := reset(t, w, tok, newPassword); st != 200 {
		t.Fatalf("reset: %d %v", st, out)
	}
	// The new password does not get round the passkey rule.
	anon := &client{t: t, base: w.base}
	if st, body := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": newPassword}); st != 401 || body["passkey_required"] != true {
		t.Fatalf("password sign-in of an admin with a passkey after reset: %d %v", st, body)
	}
	// The passkey is untouched.
	pk := w.passkeyLogin(t, key, 200)
	if n := len(pk.must(200, "GET", "/v1/me/passkeys", nil)["passkeys"].([]any)); n != 1 {
		t.Errorf("%d passkeys after reset", n)
	}

	// An administrator without a passkey gets only an enrol-only session.
	pk.must(201, "POST", "/v1/members", map[string]any{"email": "adm@acme.test", "password": testPassword, "roles": []string{"admin"}})
	tok = w.tokenFor(t, mail, "adm@acme.test")
	if st, out := reset(t, w, tok, newPassword); st != 200 {
		t.Fatalf("reset: %d %v", st, out)
	}
	adm := w.login(t, "adm@acme.test", newPassword)
	if me := adm.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != true {
		t.Errorf("after reset: %v", me)
	}
	adm.must(403, "POST", "/v1/me/password", map[string]any{"current_password": newPassword, "new_password": "another passphrase!"})
}

func TestPasswordResetUnderSSOEnforcement(t *testing.T) {
	w := ssoWorld(t)
	mail := mailWorld(t, w)
	idp := oidctest.New()
	defer idp.Close()
	owner := w.tenant(t, "Bank", "owner@bank.test")
	conn := owner.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "Okta",
		"oidc":          map[string]any{"issuer": idp.URL, "client_id": idp.ClientID, "client_secret": idp.ClientSecret},
		"default_roles": []string{"viewer"}})
	id := conn["id"].(string)
	dom := owner.must(201, "POST", "/v1/sso/"+id+"/domains", map[string]any{"domain": "bank.test"})
	w.txt["_taskiem-verify.bank.test"] = []string{dom["txt_value"].(string)}
	owner.must(204, "POST", "/v1/sso/domains/bank.test/verify", nil)
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "bob@bank.test", "password": testPassword, "roles": []string{"viewer"}})

	// Before enforcement, bob can reset.
	bobTok := w.tokenFor(t, mail, "bob@bank.test")
	owner.must(204, "PUT", "/v1/sso/"+id, map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{}, "enforce": true})
	// Under enforcement a password signs bob in nowhere: no link, and the
	// one sent before no longer works.
	if sent := w.forgot(t, mail, "bob@bank.test"); len(sent) != 0 {
		t.Errorf("a member held to SSO got a reset link")
	}
	if st, _ := reset(t, w, bobTok, newPassword); st != 400 {
		t.Errorf("a link worked under enforcement: %d", st)
	}
	// Someone who signs in by SSO only gets no password, by link or by
	// setting one.
	idp.Claims = map[string]any{"sub": "u-ada", "email": "ada@bank.test", "email_verified": true}
	_, ada := w.oidcSignIn(t, idp, id)
	if ada == nil {
		t.Fatal("SSO sign-in failed")
	}
	ada.must(403, "POST", "/v1/me/password", map[string]any{"new_password": newPassword})
	if sent := w.forgot(t, mail, "ada@bank.test"); len(sent) != 0 {
		t.Errorf("an SSO-only member got a reset link")
	}
	// The owner's break-glass password can still be recovered.
	tok := w.tokenFor(t, mail, "owner@bank.test")
	if st, out := reset(t, w, tok, newPassword); st != 200 {
		t.Fatalf("owner reset under enforcement: %d %v", st, out)
	}
	w.login(t, "owner@bank.test", newPassword)
}

func TestChangePassword(t *testing.T) {
	w := newWorld(t)
	mail := mailWorld(t, w)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "op@acme.test", "password": testPassword, "roles": []string{"operator"}})
	here := w.login(t, "op@acme.test", testPassword)
	there := w.login(t, "op@acme.test", testPassword)

	if st, body := here.do("POST", "/v1/me/password", map[string]any{"current_password": "not my password", "new_password": newPassword}); st != 403 || body["reauth"] == nil {
		t.Errorf("wrong current password: %d %v", st, body)
	}
	here.must(403, "POST", "/v1/me/password", map[string]any{"new_password": newPassword})
	here.must(400, "POST", "/v1/me/password", map[string]any{"current_password": testPassword, "new_password": "short"})
	here.must(204, "POST", "/v1/me/password", map[string]any{"current_password": testPassword, "new_password": newPassword})
	// This session stays; the others end.
	here.must(200, "GET", "/v1/me", nil)
	if st, _ := there.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("another session survived: %d", st)
	}
	w.login(t, "op@acme.test", newPassword)
	if got := auditActions(t, owner, "auth.password.change"); len(got) != 1 {
		t.Errorf("audit: %v", got)
	}
	w.srv.WaitBackground()
	if last := mail.all()[len(mail.all())-1]; !strings.Contains(last, "To: op@acme.test") || !strings.Contains(last, "was just changed") {
		t.Errorf("no notice of the change:\n%s", last)
	}

	// API keys have no password.
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.read"}})["key"].(string)
	(&client{t: t, base: w.base, token: key}).must(403, "POST", "/v1/me/password", map[string]any{"current_password": testPassword, "new_password": newPassword})
}
