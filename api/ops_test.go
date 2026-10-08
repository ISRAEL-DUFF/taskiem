package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/catalogue/cataloguetest"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

// opsClient is a browser on the operator console: a cookie jar (so the
// session cookie goes only where its path says) and the CSRF header.
type opsClient struct {
	t    *testing.T
	base string
	http *http.Client
	csrf bool
}

func newOpsClient(t *testing.T, base string) *opsClient {
	jar, _ := cookiejar.New(nil)
	return &opsClient{t: t, base: base, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, csrf: true}
}

func (c *opsClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf {
		req.Header.Set("X-Taskiem-Request", "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return resp.StatusCode, out
}

func (c *opsClient) must(want int, method, path string, body any) map[string]any {
	c.t.Helper()
	got, out := c.do(method, path, body)
	if got != want {
		c.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

// session is the operator session cookie's value.
func (c *opsClient) session() string {
	c.t.Helper()
	for _, ck := range c.http.Jar.Cookies(mustURL(c.t, c.base+"/v1/ops/me")) {
		if ck.Name == "taskiem_ops_session" {
			return ck.Value
		}
	}
	return ""
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// statusOf sends req and returns the answer's status.
func statusOf(t *testing.T, req *http.Request) int {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func opsWorld(t *testing.T) *world {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	w.srv.Ops = &api.OpsSettings{}
	return w
}

// enrolOperator makes an operator with the CLI's functions and enrols a
// software passkey through the API, which signs them in.
func enrolOperator(t *testing.T, w *world, email string) (*opsClient, *webauthntest.Authenticator) {
	t.Helper()
	ctx := context.Background()
	pool := w.env.Store.Pool
	if _, err := ops.Add(ctx, pool, email, "Op", "cli:test"); err != nil {
		t.Fatal(err)
	}
	link, err := ops.Enrol(ctx, pool, email, origin, time.Hour, "cli:test")
	if err != nil {
		t.Fatal(err)
	}
	_, tok, _ := strings.Cut(link, "/ops/enrol#")
	c := newOpsClient(t, w.base)
	opts := c.must(200, "POST", "/v1/ops/auth/enrol/options", map[string]any{"token": tok})
	if opts["email"] != email {
		t.Fatalf("enrol options: %v", opts)
	}
	a := webauthntest.New(rpID, origin)
	user := opts["publicKey"].(map[string]any)["user"].(map[string]any)
	a.UserHandle, _ = base64.RawURLEncoding.DecodeString(user["id"].(string))
	c.must(201, "POST", "/v1/ops/auth/enrol", map[string]any{"token": tok, "name": "Laptop", "credential": a.Register(challengeFrom(t, opts)).JSON()})
	// The link works once.
	c2 := newOpsClient(t, w.base)
	c2.must(401, "POST", "/v1/ops/auth/enrol/options", map[string]any{"token": tok})
	return c, a
}

// stepUp asks for a challenge for op on target and answers it.
func (c *opsClient) stepUp(a *webauthntest.Authenticator, op, target string) map[string]any {
	c.t.Helper()
	opts := c.must(200, "POST", "/v1/ops/step-up/options", map[string]any{"operation": op, "target": target})
	return map[string]any{"passkey": a.Assert(challengeFrom(c.t, opts)).JSON()}
}

func opsSignIn(t *testing.T, w *world, a *webauthntest.Authenticator, want int) *opsClient {
	t.Helper()
	c := newOpsClient(t, w.base)
	opts := c.must(200, "POST", "/v1/ops/auth/passkey/options", nil)
	c.must(want, "POST", "/v1/ops/auth/passkey", map[string]any{"credential": a.Assert(challengeFrom(t, opts)).JSON()})
	return c
}

// TestOperatorSessionsAreSeparate: operators enrol from a CLI link, sign in
// with a passkey, and their sessions and tenants' never cross.
func TestOperatorSessionsAreSeparate(t *testing.T) {
	w := opsWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenantKey := addPasskey(t, owner, "Owner's laptop")
	_, key := enrolOperator(t, w, "ops@taskiem.test")

	c := opsSignIn(t, w, key, 200)
	if me := c.must(200, "GET", "/v1/ops/me", nil); me["operator"].(map[string]any)["email"] != "ops@taskiem.test" || me["reviewer"] != false {
		t.Fatalf("ops me: %v", me)
	}
	tok := c.session()
	if !strings.HasPrefix(tok, "tsk_ops_") {
		t.Fatalf("operator session cookie %q", tok)
	}
	// The cookie is scoped to /v1/ops: the browser never sends it elsewhere.
	for _, ck := range c.http.Jar.Cookies(mustURL(t, w.base+"/v1/me")) {
		if ck.Name == "taskiem_ops_session" {
			t.Error("the operator cookie is sent to tenant routes")
		}
	}
	// An operator session is not accepted on tenant routes, however sent.
	op := &client{t: t, base: w.base, token: tok}
	if st, _ := op.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("operator token as a tenant bearer: %d", st)
	}
	req, _ := http.NewRequest("GET", w.base+"/v1/workflows", nil)
	req.AddCookie(&http.Cookie{Name: "taskiem_session", Value: tok})
	if st := statusOf(t, req); st != 401 {
		t.Errorf("operator token as a tenant cookie: %d", st)
	}
	// Nor a tenant's session, key or passkey on operator routes.
	if st, _ := owner.do("GET", "/v1/ops/me", nil); st != 401 {
		t.Errorf("tenant session on operator routes: %d", st)
	}
	apiKey := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"workflow.read"}})["key"].(string)
	if st, _ := (&client{t: t, base: w.base, token: apiKey}).do("GET", "/v1/ops/tenants", nil); st != 401 {
		t.Errorf("tenant API key on operator routes: %d", st)
	}
	req, _ = http.NewRequest("GET", w.base+"/v1/ops/me", nil)
	req.AddCookie(&http.Cookie{Name: "taskiem_ops_session", Value: owner.token})
	if st := statusOf(t, req); st != 401 {
		t.Errorf("tenant token as the operator cookie: %d", st)
	}
	// Even with an operator cookie, an Authorization header is refused.
	req, _ = http.NewRequest("GET", w.base+"/v1/ops/me", nil)
	req.AddCookie(&http.Cookie{Name: "taskiem_ops_session", Value: tok})
	req.Header.Set("Authorization", "Bearer "+owner.token)
	if st := statusOf(t, req); st != 401 {
		t.Errorf("operator cookie with a tenant bearer: %d", st)
	}
	// The owner's passkey is a tenant's: it does not sign in an operator,
	// and the operator's does not sign in to a tenant.
	opsSignIn(t, w, tenantKey, 401)
	w.passkeyLogin(t, key, 401)
	// A tenant's sign-in challenge is not an operator's.
	anon := &client{t: t, base: w.base}
	ch := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	newOpsClient(t, w.base).must(401, "POST", "/v1/ops/auth/passkey", map[string]any{"credential": key.Assert(ch).JSON()})

	// Writes need the CSRF header.
	c.csrf = false
	c.must(403, "POST", "/v1/ops/auth/logout", nil)
	c.csrf = true

	// Disabling an operator ends their sessions at once.
	if err := ops.Disable(context.Background(), w.env.Store.Pool, "ops@taskiem.test", "cli:test"); err != nil {
		t.Fatal(err)
	}
	c.must(401, "GET", "/v1/ops/me", nil)
	opsSignIn(t, w, key, 401)

	// Sign-in, enrolment and the disable are in the platform chain, which
	// verifies, and no tenant can read it.
	ctx := context.Background()
	var actions []string
	err := db.InTenantTx(ctx, w.env.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action FROM audit_log ORDER BY chain_seq`)
		if err != nil {
			return err
		}
		actions, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actions, ","); got != "operator.add,operator.enrol.issue,operator.passkey.add,operator.sign_in,operator.sign_in,operator.disable" {
		t.Errorf("platform chain: %s", got)
	}
	for _, e := range owner.must(200, "GET", "/v1/audit", nil)["entries"].([]any) {
		if strings.HasPrefix(e.(map[string]any)["action"].(string), "operator.") {
			t.Errorf("a tenant reads operator entries: %v", e)
		}
	}
}

// TestOperatorConsoleWork: a reviewer approves a submission from the
// console (four eyes in the database, every checklist item, a passkey
// bound to the decision), manages publishers, declares an incident, and
// looks at tenants; all of it lands in the platform chain.
func TestOperatorConsoleWork(t *testing.T) {
	ctx := context.Background()
	w := opsWorld(t)
	w.srv.Catalogue = &catalogue.Checker{Resolver: cataloguetest.Public}
	pool := w.env.Store.Pool
	acme := w.tenant(t, "Acme", "owner@acme.test")
	acmeID := uuid.MustParse(tenantOf(t, acme))
	key, pubB64 := cataloguetest.Key()
	acme.must(201, "PUT", "/v1/catalogue/publisher", map[string]any{"slug": "acme", "name": "Acme Ltd", "public_key": pubB64})

	c, a := enrolOperator(t, w, "reviewer@taskiem.test")
	// A member of the publisher who is also an operator cannot review it.
	insider, ia := enrolOperator(t, w, "owner@acme.test")

	// Publisher verification, with a passkey bound to the slug.
	c.must(200, "GET", "/v1/ops/catalogue/publishers", nil)
	c.must(403, "POST", "/v1/ops/catalogue/publishers/acme/verify", map[string]any{})
	c.must(403, "POST", "/v1/ops/catalogue/publishers/acme/verify", map[string]any{"step_up": c.stepUp(a, "ops.publisher.verify", "globex")})
	c.must(200, "POST", "/v1/ops/catalogue/publishers/acme/verify", map[string]any{"step_up": c.stepUp(a, "ops.publisher.verify", "acme")})

	sub := acme.must(201, "POST", "/v1/catalogue/submissions", cataloguetest.Package(t, "acme", "1.0.0", key, nil))
	id := sub["id"].(string)
	q := c.must(200, "GET", "/v1/ops/catalogue/queue", nil)["submissions"].([]any)
	if len(q) != 1 || q[0].(map[string]any)["id"] != id {
		t.Fatalf("queue: %v", q)
	}
	detail := c.must(200, "GET", "/v1/ops/catalogue/submissions/"+id, nil)
	if len(detail["checklist"].([]any)) != 8 || detail["summary"] == nil || !strings.Contains(toJSON(detail["submission"]), `"passed":true`) {
		t.Fatalf("submission: %s", toJSON(detail))
	}

	all := map[string]bool{}
	for _, k := range catalogue.ChecklistKeys() {
		all[k] = true
	}
	approve := func(c *opsClient, a *webauthntest.Authenticator, checklist map[string]bool, target string) (int, map[string]any) {
		return c.do("POST", "/v1/ops/catalogue/submissions/"+id+"/review", map[string]any{"decision": "approve", "note": "Checked against the provider's docs",
			"checklist": checklist, "step_up": c.stepUp(a, "ops.catalogue.review", target)})
	}
	// Not on the reviewer list yet: the database refuses.
	if st, out := approve(c, a, all, id+"/approve"); st != 403 || !strings.Contains(out["error"].(string), "not a catalogue reviewer") {
		t.Fatalf("unlisted reviewer: %d %v", st, out)
	}
	for _, r := range []string{"reviewer@taskiem.test", "owner@acme.test"} {
		if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_set_reviewer($1, true, 'cli:test')`, r); err != nil {
			t.Fatal(err)
		}
	}
	if st, out := approve(insider, ia, all, id+"/approve"); st != 403 || !strings.Contains(out["error"].(string), "four eyes") {
		t.Fatalf("publisher's member reviewed: %d %v", st, out)
	}
	// Every item, a note, and a passkey for this decision on this submission.
	partial := map[string]bool{"identity": true, "classes": true}
	c.must(422, "POST", "/v1/ops/catalogue/submissions/"+id+"/review", map[string]any{"decision": "approve", "note": "ok", "checklist": partial,
		"step_up": c.stepUp(a, "ops.catalogue.review", id+"/approve")})
	c.must(403, "POST", "/v1/ops/catalogue/submissions/"+id+"/review", map[string]any{"decision": "approve", "note": "ok", "checklist": all})
	if st, _ := approve(c, a, all, id+"/reject"); st != 403 {
		t.Errorf("a passkey asked for rejecting approved: %d", st)
	}
	// Another operator's passkey cannot step up for this one.
	if st, _ := c.do("POST", "/v1/ops/catalogue/submissions/"+id+"/review", map[string]any{"decision": "approve", "note": "ok", "checklist": all,
		"step_up": map[string]any{"passkey": ia.Assert(challengeFrom(t, c.must(200, "POST", "/v1/ops/step-up/options",
			map[string]any{"operation": "ops.catalogue.review", "target": id + "/approve"}))).JSON()}}); st != 403 {
		t.Errorf("someone else's passkey stepped up: %d", st)
	}
	if st, out := approve(c, a, all, id+"/approve"); st != 200 || out["state"] != "approved" {
		t.Fatalf("approve: %d %v", st, out)
	}
	if st, _ := approve(c, a, all, id+"/approve"); st != 409 {
		t.Errorf("reviewed twice: %d", st)
	}
	// The publisher sees who reviewed it: the signed-in operator.
	if got := acme.must(200, "GET", "/v1/catalogue/submissions/"+id, nil); got["state"] != "approved" || !strings.Contains(toJSON(got), "reviewer@taskiem.test") {
		t.Errorf("publisher's view: %s", toJSON(got))
	}

	// Suspension needs a note.
	c.must(422, "POST", "/v1/ops/catalogue/publishers/acme/suspend", map[string]any{"step_up": c.stepUp(a, "ops.publisher.suspend", "acme")})
	c.must(200, "POST", "/v1/ops/catalogue/publishers/acme/suspend", map[string]any{"note": "contact does not answer", "step_up": c.stepUp(a, "ops.publisher.suspend", "acme")})
	if pubs := toJSON(c.must(200, "GET", "/v1/ops/catalogue/publishers", nil)); !strings.Contains(pubs, `"status":"suspended"`) {
		t.Errorf("publishers: %s", pubs)
	}
	if revs := c.must(200, "GET", "/v1/ops/catalogue/reviewers", nil)["reviewers"].([]any); len(revs) != 2 {
		t.Errorf("reviewers: %v", revs)
	}

	// Status page: open, update, resolve.
	inc := c.must(201, "POST", "/v1/ops/status/incidents", map[string]any{"kind": "incident", "title": "Slow runs", "components": []string{"runs"},
		"message": "Looking into it", "step_up": c.stepUp(a, "ops.status.open", "incident")})
	iid := inc["id"].(string)
	c.must(403, "POST", "/v1/ops/status/incidents/"+iid+"/updates", map[string]any{"status": "resolved", "message": "Fixed",
		"step_up": c.stepUp(a, "ops.status.update", iid+"/monitoring")})
	c.must(201, "POST", "/v1/ops/status/incidents/"+iid+"/updates", map[string]any{"status": "resolved", "message": "Fixed",
		"step_up": c.stepUp(a, "ops.status.update", iid+"/resolved")})
	list := c.must(200, "GET", "/v1/ops/status/incidents", nil)["incidents"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["status"] != "resolved" || !strings.Contains(toJSON(list), "operator:reviewer@taskiem.test") {
		t.Errorf("incidents: %s", toJSON(list))
	}

	// Tenants, read-only.
	ts := c.must(200, "GET", "/v1/ops/tenants?q=acme", nil)["tenants"].([]any)
	if len(ts) != 1 || ts[0].(map[string]any)["id"] != acmeID.String() || ts[0].(map[string]any)["pool"] != "shared" {
		t.Fatalf("tenants: %v", ts)
	}
	one := c.must(200, "GET", "/v1/ops/tenants/"+acmeID.String(), nil)
	if one["limits"] == nil || one["tenant"].(map[string]any)["name"] != "Acme" {
		t.Errorf("tenant: %v", one)
	}
	c.must(404, "GET", "/v1/ops/tenants/"+uuid.NewString(), nil)
	if st, _ := c.do("PUT", "/v1/ops/tenants/"+acmeID.String(), map[string]any{}); st != 405 {
		t.Errorf("a write to tenants: %d", st)
	}

	// The platform chain records every write and view, verifies, and
	// exports for the offline verifier.
	entries := c.must(200, "GET", "/v1/ops/audit?limit=500", nil)["entries"].([]any)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"catalogue.publisher.verify", "catalogue.approve", "catalogue.publisher.suspend", "status.open", "status.update", "tenant.view"} {
		if !seen[want] {
			t.Errorf("platform chain lacks %s: %v", want, seen)
		}
	}
	if v := c.must(200, "GET", "/v1/ops/audit/verify", nil); v["intact"] != true {
		t.Errorf("verify: %v", v)
	}
	req, _ := http.NewRequest("GET", w.base+"/v1/ops/audit/export", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res, err := audit.Verify(resp.Body)
	_ = resp.Body.Close()
	if err != nil || res.FirstBroken != 0 || res.Entries < 10 {
		t.Errorf("export: %+v %v", res, err)
	}
	// And the publisher's chain has the review, as from the CLI.
	found := false
	for _, e := range acme.must(200, "GET", "/v1/audit?action=catalogue.approve", nil)["entries"].([]any) {
		found = found || e.(map[string]any)["actor_id"] == "reviewer:reviewer@taskiem.test"
	}
	if !found {
		t.Error("the publisher's chain lacks the approval")
	}

	// Sign out ends the session.
	c.must(204, "POST", "/v1/ops/auth/logout", nil)
	c.must(401, "GET", "/v1/ops/me", nil)
}

func TestOperatorConsoleOff(t *testing.T) {
	w := passkeyWorld(t)
	(&client{t: t, base: w.base}).must(404, "GET", "/v1/ops/auth/config", nil)
	(&client{t: t, base: w.base}).must(404, "POST", "/v1/ops/auth/passkey/options", nil)
}

// TestOperatorSSO: only an existing operator signs in from the configured
// issuer, and the first sign-in pins the subject.
func TestOperatorSSO(t *testing.T) {
	w := opsWorld(t)
	w.srv.PublicURL = publicURL
	idp := oidctest.New()
	defer idp.Close()
	w.srv.Ops.OIDC = &api.OpsOIDC{Name: "Okta", Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret}
	if _, err := ops.Add(context.Background(), w.env.Store.Pool, "ada@taskiem.test", "Ada", "cli:test"); err != nil {
		t.Fatal(err)
	}
	signIn := func() (string, string) {
		toIdP, cookies := followCookies(t, get(t, w.base+"/v1/ops/auth/sso/start"))
		bind := cookies["taskiem_ops_sso"]
		if bind == nil || bind.Path != "/v1/ops/auth/sso/" {
			t.Fatalf("binding cookie: %v", bind)
		}
		back, _ := follow(t, get(t, toIdP))
		landed, set := followCookies(t, with(get(t, w.local(back)), bind))
		if s := set["taskiem_ops_session"]; s != nil && s.Value != "" {
			if s.Path != "/v1/ops" || !s.HttpOnly {
				t.Errorf("session cookie: %+v", s)
			}
			return landed, s.Value
		}
		return landed, ""
	}
	idp.Claims = map[string]any{"sub": "u-eve", "email": "eve@taskiem.test", "email_verified": true}
	if landed, tok := signIn(); tok != "" || !strings.Contains(landed, "sso_error") {
		t.Errorf("a stranger signed in: %s", landed)
	}
	idp.Claims = map[string]any{"sub": "u-ada", "email": "ada@taskiem.test", "email_verified": true}
	landed, tok := signIn()
	if tok == "" || landed != "/ops" {
		t.Fatalf("operator sign-in: %s", landed)
	}
	req, _ := http.NewRequest("GET", w.base+"/v1/ops/me", nil)
	req.AddCookie(&http.Cookie{Name: "taskiem_ops_session", Value: tok})
	if st := statusOf(t, req); st != 200 {
		t.Fatalf("me after SSO: %d", st)
	}
	// Another subject claiming the same email is refused.
	idp.Claims = map[string]any{"sub": "u-mallory", "email": "ada@taskiem.test", "email_verified": true}
	if _, tok := signIn(); tok != "" {
		t.Error("a second subject took over the operator")
	}
	// An SSO session still needs a passkey for writes: Ada has none.
	c := newOpsClient(t, w.base)
	c.http.Jar.SetCookies(mustURL(t, w.base+"/v1/ops/"), []*http.Cookie{{Name: "taskiem_ops_session", Value: tok, Path: "/v1/ops"}})
	c.must(403, "POST", "/v1/ops/step-up/options", map[string]any{"operation": "ops.status.open", "target": "incident"})
	c.must(403, "POST", "/v1/ops/status/incidents", map[string]any{"kind": "incident", "title": "x", "components": []string{"api"}, "message": "x"})
}
