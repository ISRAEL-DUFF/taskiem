package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/embed"
)

// partner is a partner tenant as its server sees it: an owner session and
// an API key holding the partner permissions.
type partner struct {
	id    uuid.UUID
	owner *client
	key   *client
}

func (w *world) partner(t *testing.T, name, email string) *partner {
	t.Helper()
	owner := w.tenant(t, name, email)
	id := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	setPartner(t, w, id, 0, 0, 0)
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "server", "permissions": []string{"partner.read", "partner.manage"}})["key"].(string)
	return &partner{id: id, owner: owner, key: &client{t: t, base: w.base, token: key}}
}

func setPartner(t *testing.T, w *world, id uuid.UUID, maxSubs int, perDay, perMonth int64) {
	t.Helper()
	ctx := context.Background()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{id}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_partner($1, true, $2, $3, $4, 'test')`, id, maxSubs, perDay, perMonth)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (p *partner) sub(t *testing.T, name string, limits map[string]any) string {
	t.Helper()
	body := map[string]any{"name": name}
	if limits != nil {
		body["limits"] = limits
	}
	return p.key.must(201, "POST", "/v1/partner/sub-tenants", body)["id"].(string)
}

// app registers an embed app and returns its id and webhook secret.
func (p *partner) app(t *testing.T, body map[string]any) (string, string) {
	t.Helper()
	out := p.key.must(201, "POST", "/v1/partner/embed-apps", body)
	secret, _ := out["webhook_secret"].(string)
	return out["id"].(string), secret
}

// endUser mints a token and returns a client using it from origin ("" for
// a server-side, headless call).
func (p *partner) endUser(t *testing.T, w *world, app, sub, id, origin string, perms ...string) *endUser {
	t.Helper()
	out := p.key.must(201, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": id, "sub_tenant": sub, "permissions": perms})
	return &endUser{client: &client{t: t, base: w.base, token: out["token"].(string)}, app: app, origin: origin}
}

type endUser struct {
	*client
	app, origin string
}

func (e *endUser) call(want int, method, path string, body any) map[string]any {
	e.t.Helper()
	var hdr []string
	if e.origin != "" {
		hdr = []string{"Origin", e.origin}
	}
	return e.must(want, method, "/v1/embed/"+e.app+path, body, hdr...)
}

const transformFlow = `{"schema":"wd/v1","id":"wf_hello","version":1,"name":"hello","trigger":{"type":"manual"},
  "steps":[{"id":"greet","type":"transform","config":{"output":{"msg":"=\"hello \" + string(trigger.body.name)"}}}]}`

const fakepayFlow = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"pay","trigger":{"type":"manual"},
  "steps":[{"id":"pay","type":"connector","connector":"fakepay@1","action":"notify","input":{}}]}`

const httpFlow = `{"schema":"wd/v1","id":"wf_http","version":1,"name":"h","trigger":{"type":"manual"},
  "steps":[{"id":"call","type":"parallel","config":{"branches":[{"name":"a","steps":[{"id":"get","type":"http","config":{"method":"GET","url":"https://example.com"}}]}]}}]}`

func auditCount(t *testing.T, w *world, tenant, action string) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = $2`, tenant, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Sub-tenants are isolated from each other and from the partner's own
// users; the partner API reaches them, and every access is in both chains.
func TestSubTenantIsolation(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	a, b := p.sub(t, "Customer A", nil), p.sub(t, "Customer B", nil)
	if n := len(p.key.must(200, "GET", "/v1/partner/sub-tenants", nil)["sub_tenants"].([]any)); n != 2 {
		t.Fatalf("partner lists %d sub-tenants", n)
	}
	app, _ := p.app(t, map[string]any{"name": "builder", "headless": true, "allowed_connectors": []string{}})
	ua := p.endUser(t, w, app, a, "alice", "", "workflow.read", "workflow.edit")
	ub := p.endUser(t, w, app, b, "bob", "", "workflow.read", "workflow.edit")
	wf := ua.call(201, "POST", "/workflows", map[string]any{"name": "hello", "definition": json.RawMessage(transformFlow)})["id"].(string)

	// B's end user sees nothing of A.
	if n := len(ub.call(200, "GET", "/workflows", nil)["workflows"].([]any)); n != 0 {
		t.Errorf("sub-tenant B sees %d workflows", n)
	}
	ub.call(404, "GET", "/workflows/"+wf, nil)
	// The partner's own users do not see sub-tenants' data.
	if n := len(p.owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any)); n != 0 {
		t.Errorf("the partner's owner sees %d workflows", n)
	}
	p.owner.must(404, "GET", "/v1/workflows/"+wf, nil)
	// End-user tokens are not accepted outside the embed API.
	ua.must(401, "GET", "/v1/workflows", nil)
	ua.must(401, "GET", "/v1/me", nil)

	// The partner API reaches A, audited in both chains.
	got := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+a+"/workflows", nil)["workflows"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["id"] != wf {
		t.Errorf("partner sees A's workflows %v", got)
	}
	if auditCount(t, w, p.id.String(), "partner.subtenant.workflows.read") != 1 || auditCount(t, w, a, "partner.subtenant.workflows.read") != 1 {
		t.Error("partner access not in both audit chains")
	}
	if n := len(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+b+"/workflows", nil)["workflows"].([]any)); n != 0 {
		t.Errorf("B shows %d workflows", n)
	}
	// The end user's actions are in the sub-tenant's chain, as the end user.
	var actorType, actor string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT actor_type, actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'workflow.create'`, a).Scan(&actorType, &actor); err != nil {
		t.Fatal(err)
	}
	if actorType != "end_user" || actor != "end_user:"+app+"/alice" {
		t.Errorf("end user recorded as %s %s", actorType, actor)
	}

	// Only partner keys reach the partner API: not sessions, not keys
	// without the permissions, not another partner, not a non-partner.
	p.owner.must(403, "GET", "/v1/partner/sub-tenants", nil)
	plain := p.owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "plain", "permissions": []string{"workflow.read"}})["key"].(string)
	(&client{t: t, base: w.base, token: plain}).must(403, "GET", "/v1/partner/sub-tenants", nil)
	other := w.partner(t, "Other", "owner@other.test")
	other.key.must(404, "GET", "/v1/partner/sub-tenants/"+a+"/workflows", nil)
	other.key.must(404, "POST", "/v1/partner/sub-tenants/"+a+"/suspend", nil)
	other.key.must(404, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "x", "sub_tenant": a, "permissions": []string{"workflow.read"}})
	stranger := w.tenant(t, "Plain", "owner@plain.test")
	sk := stranger.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"partner.read", "partner.manage"}})["key"].(string)
	(&client{t: t, base: w.base, token: sk}).must(403, "GET", "/v1/partner/sub-tenants", nil)
	(&client{t: t, base: w.base, token: sk}).must(403, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "x"})
	// A partner's own end-user token minting cannot reach another partner's sub-tenant.
	p.key.must(404, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "x", "sub_tenant": other.sub(t, "theirs", nil), "permissions": []string{"workflow.read"}})
}

// End-user tokens: expiry, audience and origin binding, revocation,
// permission subsets, and forged or altered tokens.
func TestEndUserTokens(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	const site, site2 = "https://app.payrolla.test", "https://admin.payrolla.test"
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{site, site2 + "/"}, "allowed_connectors": []string{"fakepay"},
		"branding": map[string]any{"colours": map[string]string{"primary": "#0a7"}, "font_family": "Inter, sans-serif", "logo_url": "https://cdn.payrolla.test/logo.svg"}})
	other, _ := p.app(t, map[string]any{"name": "other", "headless": true, "allowed_connectors": []string{}})

	// Minting: ttl bounds, permissions within the app's, the ceiling.
	mint := func(want int, body map[string]any) map[string]any {
		return p.key.must(want, "POST", "/v1/partner/embed-apps/"+app+"/tokens", body)
	}
	base := func(extra map[string]any) map[string]any {
		b := map[string]any{"end_user_id": "u1", "sub_tenant": sub, "permissions": []string{"workflow.read"}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	mint(400, base(map[string]any{"ttl": 3601}))
	mint(403, base(map[string]any{"permissions": []string{"workflow.publish"}})) // not allowed by this app
	// Outside the end-user permissions the document lists: refused like any
	// permission beyond the app's.
	p.key.must(403, "POST", "/v1/partner/embed-apps/"+app+"/tokens", base(map[string]any{"permissions": []string{"secret.manage"}}),
		invalidBody("a permission end users never get, refused as beyond the app's")...)
	mint(400, base(map[string]any{"origin": "https://evil.test"}))
	mint(400, base(map[string]any{"end_user_id": "<script>"}))
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "headless": true, "end_user_permissions": []string{"member.manage"}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "allowed_origins": []string{"https://*.payrolla.test"}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "allowed_origins": []string{"http://payrolla.test"}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "allowed_origins": []string{site}, "branding": map[string]any{"logo_url": "javascript:alert(1)"}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "allowed_origins": []string{site}, "branding": map[string]any{"colours": map[string]string{"primary": "red;}"}}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad", "allowed_origins": []string{site}, "allowed_connectors": []string{"nope"}})
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "bad"}) // no origins and not headless

	out := mint(201, base(nil))
	if exp, _ := time.Parse(time.RFC3339Nano, out["expires_at"].(string)); time.Until(exp) > 15*time.Minute+time.Minute || time.Until(exp) < 14*time.Minute {
		t.Errorf("default expiry %v", exp)
	}
	u1 := &endUser{client: &client{t: t, base: w.base, token: out["token"].(string)}, app: app, origin: site}
	me := u1.call(200, "GET", "/me", nil)
	if me["sub_tenant_id"] != sub || me["actor"] != "end_user:"+app+"/u1" || toJSON(me["permissions"]) != `["workflow.read"]` {
		t.Errorf("me = %v", me)
	}
	if !strings.Contains(toJSON(me["branding"]), "#0a7") {
		t.Errorf("branding = %v", me["branding"])
	}
	// Permission subset: read only.
	u1.call(403, "POST", "/workflows", map[string]any{"name": "x", "definition": json.RawMessage(transformFlow)})

	// Origin binding: the app's origins only; no Origin only for headless apps.
	u1.must(403, "GET", "/v1/embed/"+app+"/me", nil)
	u1.must(403, "GET", "/v1/embed/"+app+"/me", nil, "Origin", "https://evil.test")
	u1.must(200, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site2)
	bound := mint(201, base(map[string]any{"end_user_id": "u2", "origin": site}))["token"].(string)
	bc := &client{t: t, base: w.base, token: bound}
	bc.must(200, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	bc.must(403, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site2)

	// Audience: the token is for its app only.
	u1.must(401, "GET", "/v1/embed/"+other+"/me", nil, "Origin", site)
	// Forged and altered tokens.
	tok := u1.token
	for _, bad := range []string{tok[:len(tok)-1] + flip(tok[len(tok)-1]), "tsk_eut_" + strings.Repeat("A", 43), strings.TrimPrefix(tok, "tsk_eut_"), p.key.token} {
		(&client{t: t, base: w.base, token: bad}).must(401, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	}

	// Expiry.
	expiring := mint(201, base(map[string]any{"end_user_id": "u3", "ttl": 60}))["token"].(string)
	ec := &client{t: t, base: w.base, token: expiring}
	ec.must(200, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE end_user_tokens SET created_at = created_at - interval '2 minutes', expires_at = now() - interval '1 second' WHERE token_hash = sha256($1::bytea)`, []byte(expiring)); err != nil {
		t.Fatal(err)
	}
	ec.must(401, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)

	// Revocation per end user: u1's tokens stop; u2's do not.
	second := mint(201, base(nil))["token"].(string)
	if n := p.key.must(200, "POST", "/v1/partner/embed-apps/"+app+"/tokens/revoke", map[string]any{"sub_tenant": sub, "end_user_id": "u1"})["revoked"]; n != float64(2) {
		t.Errorf("revoked %v tokens, want 2", n)
	}
	u1.call(401, "GET", "/me", nil)
	(&client{t: t, base: w.base, token: second}).must(401, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	bc.must(200, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	if auditCount(t, w, sub, "partner.end_user_token.revoke") != 1 || auditCount(t, w, p.id.String(), "partner.end_user_token.mint") < 4 {
		t.Error("minting and revocation not audited in both chains")
	}

	// Narrowing the app's end-user permissions narrows live tokens at once.
	bc.must(200, "GET", "/v1/embed/"+app+"/workflows", nil, "Origin", site)
	p.key.must(200, "PUT", "/v1/partner/embed-apps/"+app, map[string]any{"name": "web", "allowed_origins": []string{site, site2}, "end_user_permissions": []string{"run.read"}})
	bc.must(403, "GET", "/v1/embed/"+app+"/workflows", nil, "Origin", site)

	// Disabling the app stops its tokens at once.
	p.key.must(200, "PUT", "/v1/partner/embed-apps/"+app, map[string]any{"name": "web", "allowed_origins": []string{site, site2}, "status": "disabled"})
	bc.must(401, "GET", "/v1/embed/"+app+"/me", nil, "Origin", site)
	mint(409, base(nil))
}

func flip(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

// CORS answers only an app's allowed origins, and only on the embed API.
func TestEmbedCORS(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	const site = "https://app.payrolla.test"
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{site}})
	preflight := func(path, origin string) (int, http.Header) {
		req, _ := http.NewRequest(http.MethodOptions, w.base+path, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header
	}
	if st, h := preflight("/v1/embed/"+app+"/workflows", site); st != 204 || h.Get("Access-Control-Allow-Origin") != site || !strings.Contains(h.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("allowed preflight: %d %v", st, h)
	}
	if st, h := preflight("/v1/embed/"+app+"/workflows", "https://evil.test"); st != 403 || h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("other origin's preflight: %d %v", st, h)
	}
	if st, _ := preflight("/v1/embed/"+uuid.NewString()+"/workflows", site); st != 403 {
		t.Errorf("unknown app's preflight: %d", st)
	}
	// The rest of the API sends no CORS headers to anyone.
	for _, path := range []string{"/v1/workflows", "/v1/partner/sub-tenants", "/v1/auth/login"} {
		if _, h := preflight(path, site); h.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s answers CORS: %v", path, h)
		}
	}
	// Actual requests: the header for the allowed origin, even on errors.
	req, _ := http.NewRequest(http.MethodGet, w.base+"/v1/embed/"+app+"/me", nil)
	req.Header.Set("Origin", site)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode != 401 || r2.Header.Get("Access-Control-Allow-Origin") != site || r2.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("unauthenticated request from the site: %d %v", r2.StatusCode, r2.Header)
	}
}

// Headless mode: everything an embedded builder needs, through the API with
// an end-user token, within the app's connectors and templates.
func TestHeadlessFlow(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{}, "allowed_templates": []string{"payroll-reminder"},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start", "run.cancel"}})
	u := p.endUser(t, w, app, sub, "cust-42", "", "workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start")

	if c := u.call(200, "GET", "/connectors", nil); len(c["connectors"].([]any)) != 0 {
		t.Errorf("connectors offered: %v", c)
	}
	// Connectors and step types the app does not allow, anywhere in the
	// definition, and templates it does not list.
	for _, doc := range []string{fakepayFlow, httpFlow} {
		out := u.call(403, "POST", "/workflows", map[string]any{"name": "x", "definition": json.RawMessage(doc)})
		if out["not_allowed"] == nil {
			t.Errorf("refusal without the list: %v", out)
		}
		u.call(403, "POST", "/validate", map[string]any{"definition": json.RawMessage(doc)})
	}
	u.call(403, "POST", "/workflows", map[string]any{"name": "x", "template": "other", "definition": json.RawMessage(transformFlow)})

	// Create (from an allowed template), validate, save, lay out, publish.
	created := u.call(201, "POST", "/workflows", map[string]any{"name": "hello", "template": "payroll-reminder", "definition": json.RawMessage(transformFlow)})
	wf := created["id"].(string)
	if probs := created["problems"].([]any); len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	u.call(200, "POST", "/validate", map[string]any{"definition": json.RawMessage(transformFlow)})
	u.call(403, "POST", "/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(fakepayFlow)})
	v2 := u.call(201, "POST", "/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(strings.Replace(transformFlow, "hello ", "hi ", 1))})
	if v2["version"] != float64(2) {
		t.Fatalf("save: %v", v2)
	}
	u.call(204, "PUT", "/workflows/"+wf+"/versions/2/layout", map[string]any{"greet": map[string]any{"x": 1, "y": 2}})
	u.call(200, "GET", "/workflows/"+wf+"/versions/2", nil)
	u.call(200, "POST", "/workflows/"+wf+"/versions/2/publish", nil)
	if got := u.call(200, "GET", "/workflows/"+wf, nil); got["workflow"].(map[string]any)["active_version"] != float64(2) {
		t.Errorf("workflow after publish: %v", got)
	}

	// Run, stream, read.
	run := u.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "Ada"}})["run_id"].(string)
	w.env.Drain(t)
	got := u.call(200, "GET", "/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v", toJSON(got))
	}
	if by := got["run"].(map[string]any)["started_by"]; by != "end_user:"+app+"/cust-42" {
		t.Errorf("started_by %v", by)
	}
	if !strings.Contains(toJSON(got), "hi Ada") {
		t.Errorf("run output missing: %s", toJSON(got))
	}
	if n := len(u.call(200, "GET", "/runs", nil)["runs"].([]any)); n != 1 {
		t.Errorf("runs listed: %d", n)
	}
	req, _ := http.NewRequest(http.MethodGet, w.base+"/v1/embed/"+app+"/runs/"+run+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+u.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(stream), "RunCompleted") || !strings.Contains(string(stream), "event: end") {
		t.Errorf("stream: %d %s", resp.StatusCode, stream)
	}
	// Not granted to this token: cancel.
	u.call(403, "POST", "/runs/"+run+"/cancel", nil)

	// The partner sees the outcome, redacted: status, never data.
	runs := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/runs", nil)["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["status"] != "completed" || strings.Contains(toJSON(runs), "Ada") {
		t.Errorf("partner's view of runs: %s", toJSON(runs))
	}
	usage := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/usage", nil)
	if toJSON(usage["usage"].(map[string]any)["runs_today"]) != "1" {
		t.Errorf("usage: %v", usage)
	}

	// Narrowing the app later stops runs of what it no longer allows.
	p.key.must(200, "PUT", "/v1/partner/embed-apps/"+app, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{"fakepay"},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	pay := u.call(201, "POST", "/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(fakepayFlow)})["id"].(string)
	u.call(200, "POST", "/workflows/"+pay+"/versions/1/publish", nil)
	p.key.must(200, "PUT", "/v1/partner/embed-apps/"+app, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	u.call(403, "POST", "/workflows/"+pay+"/runs", map[string]any{"input": map[string]any{}})
}

// A sub-tenant's limits default to and stay within the partner's; partner-
// wide caps hold across its sub-tenants.
func TestSubTenantLimits(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	ctx := context.Background()
	if err := w.env.Store.SetLimits(ctx, p.id, map[string]any{"max_workflows": 5, "runs_per_day": 100, "worker_concurrency": 4}, "test"); err != nil {
		t.Fatal(err)
	}
	p.key.must(400, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "big", "limits": map[string]any{"max_workflows": 10}})
	p.key.must(400, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "unlimited", "limits": map[string]any{"runs_per_day": 0}})
	p.key.must(400, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "bad", "limits": map[string]any{"nope": 1}})
	small := p.key.must(201, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "small", "limits": map[string]any{"max_workflows": 2}})
	lim := small["limits"].(map[string]any)
	if lim["max_workflows"] != float64(2) || lim["runs_per_day"] != float64(100) || lim["worker_concurrency"] != float64(4) {
		t.Errorf("small's limits: %v", lim)
	}
	plain := p.key.must(201, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "plain"})
	if lim := plain["limits"].(map[string]any); lim["max_workflows"] != float64(5) || lim["runs_per_day"] != float64(100) {
		t.Errorf("default limits: %v", lim)
	}
	sid := small["id"].(string)
	got := p.key.must(200, "PUT", "/v1/partner/sub-tenants/"+sid+"/limits", map[string]any{"max_workflows": nil, "runs_per_day": 10})["limits"].(map[string]any)
	if got["max_workflows"] != float64(5) || got["runs_per_day"] != float64(10) {
		t.Errorf("after PUT: %v", got)
	}
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sid+"/limits", map[string]any{"runs_per_day": 101})
	// Lowering the partner lowers its sub-tenants too, whatever they hold.
	if err := w.env.Store.SetLimits(ctx, p.id, map[string]any{"runs_per_day": 3}, "test"); err != nil {
		t.Fatal(err)
	}
	w.env.Store.ForgetLimits(uuid.MustParse(sid))
	if l, err := w.env.Store.LimitsFor(ctx, uuid.MustParse(sid)); err != nil || l.RunsPerDay != 3 || l.Partner() != p.id {
		t.Errorf("after the partner's change: %+v %v", l, err)
	}

	// The sub-tenant's own count limit applies to its end users.
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	p.key.must(200, "PUT", "/v1/partner/sub-tenants/"+sid+"/limits", map[string]any{"max_workflows": 1})
	u := p.endUser(t, w, app, sid, "u", "", "workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start")
	wf := u.call(201, "POST", "/workflows", map[string]any{"name": "a", "definition": json.RawMessage(transformFlow)})["id"].(string)
	u.call(429, "POST", "/workflows", map[string]any{"name": "b", "definition": json.RawMessage(transformFlow)})

	// Partner-wide: one run a day across all its sub-tenants.
	setPartner(t, w, p.id, 0, 1, 0)
	w.env.Store.ForgetLimits(uuid.MustParse(sid))
	u.call(200, "POST", "/workflows/"+wf+"/versions/1/publish", nil)
	u.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "x"}})
	if out := u.call(429, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "y"}}); out["code"] != "quota_exceeded" {
		t.Errorf("partner-wide quota: %v", out)
	}

	// At most N sub-tenants.
	setPartner(t, w, p.id, 2, 0, 0)
	if out := p.key.must(429, "POST", "/v1/partner/sub-tenants", map[string]any{"name": "third"}); out["limit"] != "max_subtenants" {
		t.Errorf("sub-tenant cap: %v", out)
	}
	info := p.key.must(200, "GET", "/v1/partner", nil)
	if toJSON(info["caps"].(map[string]any)["max_subtenants"]) != "2" || toJSON(info["usage"].(map[string]any)["subtenants"]) != "2" {
		t.Errorf("partner info: %v", info)
	}
}

// Suspending a sub-tenant stops its tokens and sessions at once.
func TestSubTenantSuspension(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{}})
	u := p.endUser(t, w, app, sub, "u", "", "workflow.read")
	u.call(200, "GET", "/me", nil)
	// A session inside the sub-tenant (support access, say), to show it stops too.
	ctx := context.Background()
	user, tok := uuid.Must(uuid.NewV7()), "session-"+uuid.NewString()
	for _, st := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO users (id, email) VALUES ($1, 'support@payrolla.test')`, []any{user}},
		{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`, []any{sub, user}},
		{`INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, auth_method) VALUES (sha256(convert_to($1, 'UTF8')), $2, $3, now() + interval '1 hour', 'password')`, []any{tok, user, sub}},
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, st.q, st.args...); err != nil {
			t.Fatal(err)
		}
	}
	sess := &client{t: t, base: w.base, token: tok}
	sess.must(200, "GET", "/v1/me", nil)

	p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/suspend", nil)
	p.key.must(409, "POST", "/v1/partner/sub-tenants/"+sub+"/suspend", nil)
	u.call(401, "GET", "/me", nil)
	sess.must(401, "GET", "/v1/me", nil)
	p.key.must(409, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "u", "sub_tenant": sub, "permissions": []string{"workflow.read"}})
	// The partner can still read it, and resume it.
	p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/workflows", nil)
	p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/resume", nil)
	u.call(401, "GET", "/me", nil) // revoked by the suspension, not revived
	p.endUser(t, w, app, sub, "u", "", "workflow.read").call(200, "GET", "/me", nil)
	if auditCount(t, w, sub, "partner.subtenant.suspend") != 1 || auditCount(t, w, p.id.String(), "partner.subtenant.resume") != 1 {
		t.Error("suspension not audited in both chains")
	}
}

// Partner webhooks: queued with the run's outcome or the publish, signed
// like alert webhooks, retried until delivered, and logged.
func TestPartnerWebhooks(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", map[string]any{"runs_per_day": 1})
	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	fail := true
	hook := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		got, bodies = append(got, r), append(bodies, b)
		if fail {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hook.Close)
	app, secret := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{}, "webhook_url": hook.URL,
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("webhook secret %q", secret)
	}
	u := p.endUser(t, w, app, sub, "u", "", "workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start")
	wf := u.call(201, "POST", "/workflows", map[string]any{"name": "a", "definition": json.RawMessage(transformFlow)})["id"].(string)
	u.call(200, "POST", "/workflows/"+wf+"/versions/1/publish", nil)
	u.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "x"}})
	w.env.Drain(t)

	hooks := &embed.Webhooks{Pool: w.env.Store.Pool, Secrets: w.env.Vault, Limits: w.env.Store.LimitsFor, Client: http.DefaultClient}
	ctx := context.Background()
	if err := hooks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	events := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, d := range p.key.must(200, "GET", "/v1/partner/webhook-deliveries", nil)["deliveries"].([]any) {
			m := d.(map[string]any)
			out[m["event"].(string)] = m
		}
		return out
	}
	ev := events()
	for _, e := range []string{"workflow.published", "run.completed", "usage.threshold"} {
		d, ok := ev[e]
		if !ok {
			t.Fatalf("no %s delivery: %v", e, ev)
		}
		if d["status"] != "pending" || d["attempts"] != float64(1) || d["last_status"] != float64(500) {
			t.Errorf("%s after a failed attempt: %v", e, d)
		}
	}
	if strings.Contains(toJSON(ev["run.completed"]), "hello x") {
		t.Error("the run's output reached the partner")
	}
	// Retried once due: delivered.
	mu.Lock()
	fail = false
	mu.Unlock()
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE partner_webhook_deliveries SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	if err := hooks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for e, d := range events() {
		if d["status"] != "delivered" || d["attempts"] != float64(2) {
			t.Errorf("%s: %v", e, d)
		}
	}
	// Each request is signed: t=<ts>,v1=HMAC-SHA256(secret, "<ts>." + body).
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 8 {
		t.Errorf("%d requests, want 8 (published, completed, usage at 80%% and 100%%: each failed once, then delivered)", len(got))
	}
	for i, r := range got {
		sig := r.Header.Get("Taskiem-Signature")
		ts, v1, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",v1=")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "."))
		mac.Write(bodies[i])
		if !hmac.Equal([]byte(v1), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Errorf("request %d: bad signature %q", i, sig)
		}
		var m embed.Message
		if err := json.Unmarshal(bodies[i], &m); err != nil || m.Event != r.Header.Get("Taskiem-Event") || m.ID.String() != r.Header.Get("Taskiem-Delivery-Id") {
			t.Errorf("request %d: %s %v", i, bodies[i], err)
		}
		if m.Event != "usage.threshold" && (m.SubTenantID == nil || m.SubTenantID.String() != sub) {
			t.Errorf("request %d: sub-tenant %v", i, m.SubTenantID)
		}
	}
	// Ticking again sends nothing new; usage events are once per period.
	if err := hooks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 8 {
		t.Errorf("%d requests after a quiet tick", len(got))
	}
	// A rotated secret signs from then on.
	if s2 := p.key.must(200, "POST", "/v1/partner/embed-apps/"+app+"/webhook-secret", nil)["webhook_secret"]; s2 == secret || s2 == "" {
		t.Errorf("rotated secret %v", s2)
	}
	if apps := p.key.must(200, "GET", "/v1/partner/embed-apps", nil)["embed_apps"].([]any); len(apps) != 1 {
		t.Errorf("apps: %v", apps)
	}
	// A delivery that gave up is sent again on request; a delivered one is not.
	failed, done := events()["workflow.published"]["id"].(string), events()["run.completed"]["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE partner_webhook_deliveries SET status = 'failed' WHERE id = $1`, failed); err != nil {
		t.Fatal(err)
	}
	if out := p.key.must(202, "POST", "/v1/partner/webhook-deliveries/"+failed+"/retry", nil); out["status"] != "pending" {
		t.Errorf("retry: %v", out)
	}
	p.key.must(404, "POST", "/v1/partner/webhook-deliveries/"+done+"/retry", nil)
}
