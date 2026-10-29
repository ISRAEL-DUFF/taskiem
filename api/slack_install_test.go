package api_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// Signing up from Slack (docs/spec/chat-way-in.md 9): Add to Slack from
// the website installs Taskiem's app with no business behind it; the bot
// writes to the installer, who makes a business in the DM, and the
// install's token becomes its Slack connection. The request works once,
// only in the browser that started it; an installer whose Slack email is
// someone's account is never merged into it; a workspace that is already
// a business's makes nothing unless the installer chooses; a token no
// business took is given back.

func newSlackInstallWorld(t *testing.T) *slackWorld {
	w := newSlackWorld(t)
	c, _ := w.env.Registry.Get("slack@1")
	c.Manifest.Auth.OAuth2.TokenURL = w.fake.URL + "/oauth.v2.access"
	c.Manifest.Auth.OAuth2.RevokeURL = w.fake.URL + "/auth.revoke"
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"slack": {ID: "111.222", Secret: "slack-secret"}}
	w.srv.Terms = testTerms
	w.srv.ChatSignup = api.ChatSignupConfig{Channels: []string{"slack"}}
	w.fake.install = map[string]any{"ok": true, "access_token": "xoxb-acme", "token_type": "bot", "app_id": "A0TASKIEM",
		"team": map[string]any{"id": "T0ACME", "name": "Acme Foods"}, "authed_user": map[string]any{"id": "U0ADA"}}
	w.fake.people["U0ADA"] = [2]string{"Ada Obi", "ada@acme.test"}
	w.fake.people["U0BEN"] = [2]string{"Ben Eze", "ben@acme.test"}
	return w
}

// startInstall is Add to Slack pressed: the state Slack is sent and the
// browser's cookie.
func (w *slackWorld) startInstall(t *testing.T) (string, *http.Cookie) {
	t.Helper()
	resp, err := noRedirect.Get(w.base + "/v1/slack/install")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc == nil || loc.Host != "slack.com" || loc.Path != "/oauth/v2/authorize" {
		t.Fatalf("install start: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	q := loc.Query()
	if q.Get("client_id") != "111.222" || q.Get("redirect_uri") != w.base+"/v1/slack/install/callback" || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(q.Get("scope"), "users:read.email") || len(q.Get("state")) != 43 {
		t.Fatalf("authorize query: %v", q)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "taskiem_slack_install" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("no browser cookie: %v", resp.Cookies())
	}
	return q.Get("state"), cookie
}

// finishInstall is Slack sending the browser back; it returns where the
// browser goes next.
func (w *slackWorld) finishInstall(t *testing.T, state, code string, cookie *http.Cookie) string {
	t.Helper()
	req, _ := http.NewRequest("GET", w.base+"/v1/slack/install/callback?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	w.srv.WaitBackground()
	if resp.StatusCode != 302 {
		t.Fatalf("callback: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func (w *slackWorld) install(t *testing.T) {
	t.Helper()
	state, cookie := w.startInstall(t)
	if loc := w.finishInstall(t, state, "good-code", cookie); loc != "https://slack.com/app_redirect?app=A0TASKIEM&team=T0ACME" {
		t.Fatalf("after install: %s", loc)
	}
}

// says is the text of the messages posted to a Slack user from n on.
func (w *slackWorld) says(user string, n int) []string {
	var out []string
	for _, m := range w.fake.to(user)[n:] {
		out = append(out, m["text"].(string))
	}
	return out
}

func (w *slackWorld) scalar(t *testing.T, q string, args ...any) any {
	t.Helper()
	var v any
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestSlackSignup(t *testing.T) {
	w := newSlackInstallWorld(t)
	anon := &client{t: t, base: w.base}
	if out := anon.must(200, "GET", "/v1/signup", nil); out["slack"] != "/v1/slack/install" {
		t.Fatalf("signup options: %v", out)
	}

	// Another browser holding the state finds nothing; the starter's
	// still can.
	state, cookie := w.startInstall(t)
	if loc := w.finishInstall(t, state, "good-code", nil); !strings.Contains(loc, "/slack/added?error=") {
		t.Fatalf("no cookie: %s", loc)
	}
	if loc := w.finishInstall(t, state, "good-code", &http.Cookie{Name: "taskiem_slack_install", Value: strings.Repeat("x", 43)}); !strings.Contains(loc, "already+used+or+expired") {
		t.Fatalf("another browser: %s", loc)
	}
	if n := len(w.fake.to("U0ADA")); n != 0 {
		t.Fatalf("written to before an install: %d", n)
	}
	if loc := w.finishInstall(t, state, "good-code", cookie); loc != "https://slack.com/app_redirect?app=A0TASKIEM&team=T0ACME" {
		t.Fatalf("after install: %s", loc)
	}
	// Once.
	if loc := w.finishInstall(t, state, "good-code", cookie); !strings.Contains(loc, "/slack/added?error=") {
		t.Fatalf("a second callback: %s", loc)
	}
	got := w.fake.to("U0ADA")
	if len(got) != 2 || !strings.Contains(got[0]["text"].(string), "Acme Foods") || !strings.Contains(got[1]["text"].(string), testTerms.URL) ||
		strings.Join(buttons(got[1]), " ") != "su:yes su:no" {
		t.Fatalf("the first messages: %v", got)
	}
	if n := w.scalar(t, `SELECT count(*) FROM tenant_onboarding`); n != int64(0) {
		t.Fatalf("a business before Yes: %v", n)
	}

	w.event(t, dm("Ev1", "U0ADA", "yes"), slackSecret)
	ask := w.fake.to("U0ADA")
	if b := buttons(ask[len(ask)-1]); len(b) != 1 || b[0] != "su:suggest" {
		t.Fatalf("the business's name: %v", ask[len(ask)-1])
	}
	// The workspace's name, offered; the person's name is Slack's.
	n := len(w.fake.to("U0ADA"))
	w.click(t, "U0ADA", "taskiem.reply.0", "su:suggest", slackSecret)
	if s := w.says("U0ADA", n); len(s) != 1 || !strings.Contains(s[0], "sell") {
		t.Fatalf("after the name: %v", s)
	}
	w.event(t, dm("Ev2", "U0ADA", "1"), slackSecret)
	n = len(w.fake.to("U0ADA"))
	w.event(t, dm("Ev3", "U0ADA", "2"), slackSecret)
	if s := w.says("U0ADA", n); len(s) != 1 || !strings.Contains(s[0], "Acme Foods") {
		t.Fatalf("done: %v", s)
	}

	tenant := w.scalar(t, `SELECT tenant_id::text FROM tenant_onboarding WHERE source = 'slack'`)
	if v := w.scalar(t, `SELECT u.email || '|' || u.name || '|' || (u.email_verified_at IS NULL) FROM users u JOIN memberships m ON m.user_id = u.id AND m.role = 'owner' WHERE m.tenant_id = $1`, tenant); v != "ada@acme.test|Ada Obi|true" {
		t.Errorf("the owner: %v", v)
	}
	if v := w.scalar(t, `SELECT c.name || '|' || c.status || '|' || a.account_key FROM connections c JOIN connection_app_accounts a ON a.connection_id = c.id WHERE c.tenant_id = $1 AND c.connector = 'slack'`, tenant); v != "main|active|T0ACME" {
		t.Errorf("the Slack connection: %v", v)
	}
	if v := w.scalar(t, `SELECT count(*) FROM slack_bindings WHERE tenant_id = $1 AND team_id = 'T0ACME' AND slack_user_id = 'U0ADA' AND verified_at IS NOT NULL`, tenant); v != int64(1) {
		t.Errorf("the owner's link: %v", v)
	}
	if v := w.scalar(t, `SELECT channel FROM terms_acceptances`); v != "slack" {
		t.Errorf("terms: %v", v)
	}
	if v := w.scalar(t, `SELECT count(*) FROM slack_installs`); v != int64(0) {
		t.Errorf("the install kept after its business: %v", v)
	}

	// Now a linked owner: the conversation, on the business's connection.
	n = len(w.fake.to("U0ADA"))
	w.event(t, dm("Ev4", "U0ADA", "help"), slackSecret)
	if s := w.says("U0ADA", n); len(s) != 1 || !strings.Contains(s[0], "status") {
		t.Fatalf("help after sign-up: %v", s)
	}
	// Another business from Slack has the workspace's Slack too.
	w.event(t, dm("Ev5", "U0ADA", "new business"), slackSecret)
	w.event(t, dm("Ev6", "U0ADA", "Acme Drinks"), slackSecret)
	w.event(t, dm("Ev7", "U0ADA", "1"), slackSecret)
	n = len(w.fake.to("U0ADA"))
	w.event(t, dm("Ev8", "U0ADA", "2"), slackSecret)
	if s := w.says("U0ADA", n); len(s) != 1 || !strings.Contains(s[0], "Acme Drinks") {
		t.Fatalf("another business: %v", s)
	}
	if v := w.scalar(t, `SELECT count(*) FROM slack_bindings b JOIN tenants t ON t.id = b.tenant_id AND t.name = 'Acme Drinks' JOIN connection_app_accounts a ON a.tenant_id = t.id AND a.account_key = 'T0ACME'`); v != int64(1) {
		t.Errorf("the second business's Slack: %v", v)
	}

	// Someone else in the workspace is not answered by a business they
	// are not in.
	w.event(t, dm("Ev9", "U0EVE", "start"), slackSecret)
	if got := w.fake.to("U0EVE"); len(got) != 0 {
		t.Errorf("a stranger answered: %v", got)
	}
}

func TestSlackSignupKnownWorkspace(t *testing.T) {
	w := newSlackInstallWorld(t)
	w.install(t)
	w.event(t, dm("Ev1", "U0ADA", "yes"), slackSecret)
	w.event(t, dm("Ev2", "U0ADA", "Acme Foods"), slackSecret)
	w.event(t, dm("Ev3", "U0ADA", "1"), slackSecret)
	w.event(t, dm("Ev4", "U0ADA", "2"), slackSecret)
	tenant := w.scalar(t, `SELECT tenant_id::text FROM tenant_onboarding WHERE source = 'slack'`)
	// Slack took the token back (the app was removed), then Ben adds it
	// again: the connection takes the new install's token.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE connections SET status = 'expired' WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	w.fake.install["authed_user"] = map[string]any{"id": "U0BEN"}
	w.install(t)
	if v := w.scalar(t, `SELECT status FROM connections WHERE tenant_id = $1 AND connector = 'slack'`, tenant); v != "active" {
		t.Errorf("the connection after a new install: %v", v)
	}
	got := w.fake.to("U0BEN")
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "Acme Foods") || strings.Join(buttons(got[0]), " ") != "su:start" {
		t.Fatalf("a known workspace: %v", got)
	}
	if v := w.scalar(t, `SELECT count(*) FROM tenant_onboarding WHERE source = 'slack'`); v != int64(1) {
		t.Fatalf("a business made without a choice: %v", v)
	}
	// Ben chooses a separate business: the terms first.
	w.click(t, "U0BEN", "taskiem.reply.0", "su:start", slackSecret)
	got = w.fake.to("U0BEN")
	if !strings.Contains(got[len(got)-1]["text"].(string), testTerms.URL) {
		t.Fatalf("separate: %v", got[len(got)-1])
	}
	w.event(t, dm("Ev5", "U0BEN", "no"), slackSecret)

	// A day on, nobody took Ben's install; its token is the business's
	// too, so it is not given back.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE slack_installs SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.WhatsAppTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v := w.scalar(t, `SELECT count(*) FROM slack_installs`); v != int64(0) || len(w.fake.revoked) != 0 {
		t.Errorf("a shared install's sweep: %v %v", v, w.fake.revoked)
	}
}

func TestSlackSignupEmailTaken(t *testing.T) {
	w := newSlackInstallWorld(t)
	web := w.tenant(t, "Ada Web", "ada@acme.test")
	w.install(t)
	got := w.fake.to("U0ADA")
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "ada@acme.test already has a Taskiem account") || strings.Join(buttons(got[0]), " ") != "su:start" {
		t.Fatalf("an email that is an account: %v", got)
	}
	w.click(t, "U0ADA", "taskiem.reply.0", "su:start", slackSecret)
	w.event(t, dm("Ev1", "U0ADA", "yes"), slackSecret)
	w.event(t, dm("Ev2", "U0ADA", "Acme Foods"), slackSecret)
	w.event(t, dm("Ev3", "U0ADA", "1"), slackSecret)
	w.event(t, dm("Ev4", "U0ADA", "2"), slackSecret)
	// A Slack-only account: the web account is not touched.
	if v := w.scalar(t, `SELECT u.email FROM users u JOIN memberships m ON m.user_id = u.id AND m.role = 'owner' JOIN tenants t ON t.id = m.tenant_id AND t.name = 'Acme Foods'`); !strings.HasSuffix(v.(string), "@slack.taskiem.invalid") {
		t.Errorf("the Slack owner's email: %v", v)
	}
	if v := w.scalar(t, `SELECT count(*) FROM memberships m JOIN users u ON u.id = m.user_id AND u.email = 'ada@acme.test'`); v != int64(1) {
		t.Errorf("the web account was given the business: %v memberships", v)
	}
	if out := web.must(200, "GET", "/v1/me", nil); out["user"].(map[string]any)["name"] != "Owner" {
		t.Errorf("the web account changed: %v", out["user"])
	}
}

func TestSlackInstallUnused(t *testing.T) {
	w := newSlackInstallWorld(t)
	w.install(t)
	// The app removed before a business: the install is forgotten.
	w.event(t, map[string]any{"type": "event_callback", "team_id": "T0ACME", "event_id": "Ev1", "event": map[string]any{"type": "app_uninstalled"}}, slackSecret)
	if v := w.scalar(t, `SELECT count(*) FROM slack_installs`); v != int64(0) {
		t.Fatalf("an uninstalled install kept: %v", v)
	}
	n := len(w.fake.to("U0ADA"))
	w.event(t, dm("Ev2", "U0ADA", "yes"), slackSecret)
	if s := w.says("U0ADA", n); len(s) != 0 {
		t.Fatalf("answered after uninstall: %v", s)
	}
	// Installed again and left for a day: the token is given back.
	w.install(t)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE slack_installs SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.WhatsAppTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(w.fake.revoked) != 1 || w.fake.revoked[0] != "xoxb-acme" {
		t.Errorf("revoked: %v", w.fake.revoked)
	}
}
