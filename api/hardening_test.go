package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Security self-review, 2026-10-07 round: S35 (step-up bound to its
// operation, no session token in a cookie sign-in's body, HSTS), K5 (key
// operations need step-up) and the embedding residual (preflights paced
// per address).

// options asks for a step-up challenge for op and target.
func options(t *testing.T, c *client, op, target string) []byte {
	t.Helper()
	return challengeFrom(t, c.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": op, "target": target}))
}

// A passkey assertion passes step-up only for the operation and target its
// challenge was asked for: not another approval, not the other decision,
// not a factor change.
func TestStepUpIsBoundToItsOperation(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "password": testPassword, "roles": []string{"approver", "credit_officer"}})
	ada := w.login(t, "ada@acme.test", testPassword)
	key := addPasskey(t, ada, "Phone")

	policy := `{"rules":[{"levels":[{"role":"credit_officer"}],"step_up":"passkey"}]}`
	owner.must(201, "PUT", "/v1/policies/high_value", map[string]any{"document": json.RawMessage(policy)})
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "disburse", "definition": json.RawMessage(policyFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	small := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 100}})["run_id"].(string)
	large := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 900000000}})["run_id"].(string)

	// The operation and its target are required, and must be known.
	for _, body := range []map[string]any{nil, {"operation": "approval.decide"}, {"operation": "anything", "target": "x"}, {"operation": "approval.decide", "target": "a b"}} {
		if st, out := ada.do("POST", "/v1/me/step-up/options", body); st != 400 {
			t.Errorf("options %v: %d %v", body, st, out)
		}
	}
	vote := func(run string, decision string, ch []byte) (int, map[string]any) {
		return ada.do("POST", "/v1/approvals/"+run+"/ok", map[string]any{"decision": decision, "passkey": key.Assert(ch).JSON()})
	}
	// Asked for the small payout, used on the large one: refused.
	if st, out := vote(large, "approved", options(t, ada, "approval.decide", small+"/ok/approved")); st != 403 || out["step_up"] != "passkey" {
		t.Errorf("another approval's assertion: %d %v", st, out)
	}
	// Asked to reject, used to approve: refused.
	if st, _ := vote(large, "approved", options(t, ada, "approval.decide", large+"/ok/rejected")); st != 403 {
		t.Errorf("a rejection's assertion approved: %d", st)
	}
	// Asked for a factor change, used on an approval: refused.
	if st, _ := vote(large, "approved", options(t, ada, "account.reauth", "passkey.add")); st != 403 {
		t.Errorf("a factor change's assertion approved: %d", st)
	}
	// And the other way: an approval's assertion does not add a passkey.
	if st, out := ada.do("POST", "/v1/me/passkeys/options", map[string]any{"passkey": key.Assert(options(t, ada, "approval.decide", large+"/ok/approved")).JSON()}); st != 403 || out["reauth"] == nil {
		t.Errorf("an approval's assertion changed factors: %d %v", st, out)
	}
	// The challenge itself says what it was for.
	ch := options(t, ada, "approval.decide", large+"/ok/approved")
	if res := ada.must(200, "POST", "/v1/approvals/"+large+"/ok", map[string]any{"decision": "approved", "passkey": key.Assert(ch).JSON()}); res["status"] != "approved" {
		t.Fatalf("the right assertion: %v", res)
	}
	if st, _ := vote(small, "approved", ch); st != 403 {
		t.Errorf("a used challenge passed again: %d", st)
	}
}

// signIn posts to a sign-in route and returns the status, body and cookies.
func signIn(t *testing.T, w *world, path string, body map[string]any) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(w.base+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, resp.Cookies()
}

// A browser's sign-in gets an HttpOnly cookie and no token in the body,
// so the page's scripts never see the session. API and CLI clients ask for
// a bearer token and get no cookie.
func TestSignInTokenOnlyForBearerClients(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	w.tenant(t, "Acme", "owner@acme.test")
	sessionCookie := func(cs []*http.Cookie) *http.Cookie {
		for _, c := range cs {
			if c.Name == "taskiem_session" {
				return c
			}
		}
		return nil
	}

	st, out, cookies := signIn(t, w, "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": testPassword})
	c := sessionCookie(cookies)
	if st != 200 || out["token"] != nil || c == nil || !c.HttpOnly || out["tenant_id"] == nil {
		t.Fatalf("cookie sign-in: %d %v %v", st, out, cookies)
	}
	if strings.Contains(toJSON(out), c.Value) {
		t.Fatal("the cookie's token is in the body")
	}
	// The cookie works (with the CSRF header for changes).
	req, _ := http.NewRequest("GET", w.base+"/v1/me", nil)
	req.AddCookie(c)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("cookie session: %d", resp.StatusCode)
	}

	st, out, cookies = signIn(t, w, "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": testPassword, "bearer": true})
	if st != 200 || out["token"] == nil || sessionCookie(cookies) != nil {
		t.Fatalf("bearer sign-in: %d %v %v", st, out, cookies)
	}
	(&client{t: t, base: w.base, token: out["token"].(string)}).must(200, "GET", "/v1/me", nil)

	// Passkey sign-in and signup follow the same rule.
	owner := w.login(t, "owner@acme.test", testPassword)
	key := addPasskey(t, owner, "Laptop")
	anon := &client{t: t, base: w.base}
	ch := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	st, out, cookies = signIn(t, w, "/v1/auth/passkey", map[string]any{"credential": key.Assert(ch).JSON()})
	if st != 200 || out["token"] != nil || sessionCookie(cookies) == nil {
		t.Errorf("passkey cookie sign-in: %d %v", st, out)
	}
	st, out, cookies = signIn(t, w, "/v1/signup", map[string]any{"tenant": "Beta", "email": "owner@beta.test", "name": "B", "password": testPassword})
	if st != 201 || out["token"] != nil || sessionCookie(cookies) == nil {
		t.Errorf("signup: %d %v", st, out)
	}
}

// HSTS is sent on the platform's host when configured, and not on a
// partner's custom domain.
func TestHSTS(t *testing.T) {
	w := newWorld(t)
	get := func(host string) http.Header {
		req, _ := http.NewRequest("GET", w.base+"/healthz", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.Header
	}
	if h := get("taskiem.example.com").Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HSTS without configuration: %q", h)
	}
	w.srv.HSTS, w.srv.PublicURL = "max-age=31536000", "https://taskiem.example.com"
	if h := get("taskiem.example.com").Get("Strict-Transport-Security"); h != "max-age=31536000" {
		t.Errorf("HSTS on the platform host: %q", h)
	}
	if h := get("automations.partner.test").Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HSTS on another host: %q", h)
	}
}

// Rotating, bringing, replacing and removing keys need a person's step-up,
// bound to the operation and the tenant; API keys cannot (K5).
func TestKeyOperationsNeedStepUp(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := tenantOf(t, owner)
	beta := w.tenant(t, "Beta", "owner@beta.test")

	// No factor yet: told to add one.
	st, out := owner.do("POST", "/v1/keys/rotate", nil)
	if st != 403 || out["step_up"] != "required" || !strings.Contains(fmt.Sprint(out["error"]), "add a passkey") {
		t.Fatalf("rotate with no factor: %d %v", st, out)
	}
	key := addPasskey(t, owner, "Laptop")
	if st, out := owner.do("POST", "/v1/keys/rotate", nil); st != 403 || toJSON(out["methods"]) != `["passkey"]` {
		t.Fatalf("rotate without proof: %d %v", st, out)
	}
	rotate := func(ch []byte) int {
		st, _ := owner.do("POST", "/v1/keys/rotate", map[string]any{"passkey": key.Assert(ch).JSON()})
		return st
	}
	if st := rotate(options(t, owner, "key.byok.disable", tenant)); st != 403 {
		t.Errorf("another key operation's assertion rotated: %d", st)
	}
	if st := rotate(options(t, owner, "key.rotate", tenantOf(t, beta))); st != 403 {
		t.Errorf("another tenant's assertion rotated: %d", st)
	}
	if st := rotate(options(t, owner, "key.rotate", tenant)); st != 202 {
		t.Fatalf("rotate with its assertion: %d", st)
	}
	// Removing a customer key is held the same way (answered 403 before 404).
	if st, out := owner.do("DELETE", "/v1/keys/byok", nil); st != 403 || out["step_up"] != "required" {
		t.Errorf("disable without proof: %d %v", st, out)
	}
	if st, out := owner.do("PUT", "/v1/keys/byok/credentials", map[string]any{"credentials": map[string]string{"token": "x"}}); st != 403 || out["step_up"] != "required" {
		t.Errorf("credentials without proof: %d %v", st, out)
	}
	if st, out := owner.do("DELETE", "/v1/keys/byok", map[string]any{"passkey": key.Assert(options(t, owner, "key.byok.disable", tenant)).JSON()}); st != 404 {
		t.Errorf("disable with its assertion and no customer key: %d %v", st, out)
	}

	// An authenticator code works too.
	secret := owner.must(200, "POST", "/v1/me/totp", map[string]any{"passkey": stepUp(t, owner, key, "account.reauth", "totp.setup")})["secret"].(string)
	owner.must(204, "POST", "/v1/me/totp/confirm", map[string]any{"code": codeAfter(secret, 0)})
	if st, out := owner.do("POST", "/v1/keys/rotate", map[string]any{"totp": codeAfter(secret, 0)}); st != 403 && st != 429 {
		t.Errorf("a used code rotated: %d %v", st, out)
	}
	owner.must(202, "POST", "/v1/keys/rotate", map[string]any{"totp": codeAfter(secret, 1)})

	// An API key is not a person and has no second factor.
	apiKey := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ops", "permissions": []string{"key.manage"}})["key"].(string)
	bot := &client{t: t, base: w.base, token: apiKey}
	bot.must(200, "GET", "/v1/keys", nil)
	if st, out := bot.do("POST", "/v1/keys/rotate", map[string]any{"totp": codeAfter(secret, 1)}); st != 403 || !strings.Contains(fmt.Sprint(out["error"]), "API key") {
		t.Errorf("API key rotated: %d %v", st, out)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=100", nil))
	if n := strings.Count(audit, "secret.rotate_key"); n != 2 {
		t.Errorf("%d rotations audited, want 2", n)
	}
}

// Unauthenticated CORS preflights are paced per address before their
// database read; other addresses are unaffected.
func TestEmbedPreflightIsPaced(t *testing.T) {
	w := newWorld(t)
	w.srv.TrustProxy = true
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	const site = "https://app.payrolla.test"
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{site}})
	preflight := func(ip string) int {
		req, _ := http.NewRequest(http.MethodOptions, w.base+"/v1/embed/"+app+"/workflows/"+uuid.NewString(), nil)
		req.Header.Set("Origin", site)
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	limited := false
	for range 200 {
		switch st := preflight("203.0.113.7"); st {
		case 204:
		case 429:
			limited = true
		default:
			t.Fatalf("preflight: %d", st)
		}
		if limited {
			break
		}
	}
	if !limited {
		t.Fatal("200 preflights from one address were never paced")
	}
	if st := preflight("203.0.113.8"); st != 204 {
		t.Errorf("another address was paced: %d", st)
	}
}
