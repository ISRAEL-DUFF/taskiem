package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

// rawResp is a response whose body was read and closed.
type rawResp struct {
	StatusCode int
	Header     http.Header
}

// raw sends a request with full control of Host and headers, and returns
// the response and its body.
func raw(t *testing.T, method, url, host, token string, hdr map[string]string) (*rawResp, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if host != "" {
		req.Host = host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return &rawResp{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

func setCapabilities(t *testing.T, w *world, id uuid.UUID, caps ...string) {
	t.Helper()
	ctx := context.Background()
	if caps == nil {
		caps = []string{}
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{id}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_partner_capabilities($1, $2, 'test')`, id, caps)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

const (
	siteApp   = "https://app.payrolla.test"
	siteAdmin = "https://admin.payrolla.test"
)

// The bundle is public and cacheable; the frame page alone may be framed,
// and only by its app's origins; every other page keeps frame-ancestors
// 'none'.
func TestEmbedBundleAndFrame(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "taskiem.js"), []byte("customElements.define('taskiem-builder', class extends HTMLElement {});"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.srv.EmbedDir = dir
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{siteApp, siteAdmin}})
	other, _ := p.app(t, map[string]any{"name": "other", "allowed_origins": []string{"https://other.payrolla.test"}})
	headless, _ := p.app(t, map[string]any{"name": "server", "headless": true})

	resp, body := raw(t, "GET", w.base+"/embed/v1/taskiem.js", "", "", nil)
	h := resp.Header
	if resp.StatusCode != 200 || !strings.Contains(body, "taskiem-builder") || h.Get("Content-Type") != "text/javascript; charset=utf-8" ||
		h.Get("Cache-Control") != "public, max-age=300, stale-while-revalidate=86400" || h.Get("Access-Control-Allow-Origin") != "*" ||
		h.Get("Cross-Origin-Resource-Policy") != "cross-origin" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Last-Modified") == "" {
		t.Errorf("bundle: %d %v", resp.StatusCode, h)
	}
	if resp, _ := raw(t, "GET", w.base+"/embed/v1/taskiem.js", "", "", map[string]string{"If-Modified-Since": h.Get("Last-Modified")}); resp.StatusCode != 304 {
		t.Errorf("revalidation: %d", resp.StatusCode)
	}
	for _, path := range []string{"/embed/v1/../../etc/passwd", "/embed/v1/other.js", "/embed/v2/taskiem.js"} {
		if resp, _ := raw(t, "GET", w.base+path, "", "", nil); resp.StatusCode != 404 {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}

	// The frame page: frame-ancestors is the app's origins, no
	// X-Frame-Options, and no token anywhere.
	resp, body = raw(t, "GET", w.base+"/embed/"+app+"/frame", "", "", nil)
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != 200 || !strings.HasSuffix(csp, "frame-ancestors "+siteApp+" "+siteAdmin) || resp.Header.Get("X-Frame-Options") != "" ||
		!strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("frame page: %d %v", resp.StatusCode, resp.Header)
	}
	if !strings.Contains(body, `<taskiem-frame app="`+app+`" view="builder" parent-origins="`+siteApp+` `+siteAdmin+`">`) || !strings.Contains(body, `src="/embed/v1/taskiem.js"`) {
		t.Errorf("frame body: %s", body)
	}
	if _, body := raw(t, "GET", w.base+"/embed/"+app+"/frame?view=runs&token=x", "", "", nil); !strings.Contains(body, `view="runs"`) || strings.Contains(body, "token") {
		t.Errorf("runs view: %s", body)
	}
	resp, _ = raw(t, "GET", w.base+"/embed/"+other+"/frame", "", "", nil)
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "frame-ancestors https://other.payrolla.test") {
		t.Errorf("other app's frame: %s", csp)
	}
	resp, _ = raw(t, "GET", w.base+"/embed/"+headless+"/frame", "", "", nil)
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "frame-ancestors 'none'") {
		t.Errorf("headless app's frame: %s", csp)
	}
	// Unknown, malformed and disabled apps: not found, not frameable.
	p.key.must(200, "PUT", "/v1/partner/embed-apps/"+other, map[string]any{"name": "other", "allowed_origins": []string{"https://other.payrolla.test"}, "status": "disabled"})
	for _, path := range []string{"/embed/" + uuid.NewString() + "/frame", "/embed/nope/frame", "/embed/" + other + "/frame", "/embed/frame"} {
		resp, _ := raw(t, "GET", w.base+path, "", "", nil)
		if resp.StatusCode != 404 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: %d %v", path, resp.StatusCode, resp.Header)
		}
	}
	// Everything else keeps frame-ancestors 'none'.
	for _, path := range []string{"/healthz", "/v1/me", "/v1/embed/" + app + "/me", "/embed/v1/taskiem.js", "/v1/partner/sub-tenants"} {
		resp, _ := raw(t, "GET", w.base+path, "", "", nil)
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s is frameable: %v", path, resp.Header)
		}
	}
}

// The frame page calls the embed API from the platform's own origin and
// names the partner page that framed it; the API checks that origin as it
// would the page's own.
func TestEmbedFrameOrigin(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{siteApp, siteAdmin}})
	tok := p.key.must(201, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "u1", "sub_tenant": sub,
		"permissions": []string{"workflow.read"}})["token"].(string)
	bound := p.key.must(201, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "u2", "sub_tenant": sub,
		"permissions": []string{"workflow.read"}, "origin": siteApp})["token"].(string)
	me := w.base + "/v1/embed/" + app + "/me"
	for _, c := range []struct {
		name  string
		token string
		hdr   map[string]string
		want  int
	}{
		{"frame without its parent", tok, map[string]string{"Origin": w.base}, 403},
		{"frame naming itself", tok, map[string]string{"Origin": w.base, "X-Taskiem-Embed-Parent": w.base}, 403},
		{"frame in an allowed page", tok, map[string]string{"Origin": w.base, "X-Taskiem-Embed-Parent": siteAdmin}, 200},
		{"frame in another page", tok, map[string]string{"Origin": w.base, "X-Taskiem-Embed-Parent": "https://evil.test"}, 403},
		{"same-origin GET", tok, map[string]string{"Sec-Fetch-Site": "same-origin", "X-Taskiem-Embed-Parent": siteApp}, 200},
		{"same-origin GET without its parent", tok, map[string]string{"Sec-Fetch-Site": "same-origin"}, 403},
		{"bound token, its origin's frame", bound, map[string]string{"Origin": w.base, "X-Taskiem-Embed-Parent": siteApp}, 200},
		{"bound token, another origin's frame", bound, map[string]string{"Origin": w.base, "X-Taskiem-Embed-Parent": siteAdmin}, 403},
		{"the header from another origin is ignored", tok, map[string]string{"Origin": "https://evil.test", "X-Taskiem-Embed-Parent": siteApp}, 403},
		{"the page itself", tok, map[string]string{"Origin": siteApp}, 200},
		{"a server, not headless", tok, nil, 403},
	} {
		if resp, body := raw(t, "GET", me, "", c.token, c.hdr); resp.StatusCode != c.want {
			t.Errorf("%s: %d, want %d: %s", c.name, resp.StatusCode, c.want, body)
		}
	}
}

// A custom domain serves its app only once verified, and then only the
// embed surface; it becomes one of the app's origins.
func TestCustomDomains(t *testing.T) {
	w := ssoWorld(t) // a fake DNS for TXT records
	w.srv.EmbedDir = t.TempDir()
	_ = os.WriteFile(filepath.Join(w.srv.EmbedDir, "taskiem.js"), []byte("//"), 0o600)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{siteApp}})
	otherApp, _ := p.app(t, map[string]any{"name": "other", "allowed_origins": []string{siteApp}})
	const host = "automations.payrolla.test"
	domains := "/v1/partner/embed-apps/" + app + "/domains"

	// The plan must include custom domains.
	p.key.must(403, "POST", domains, map[string]any{"domain": host})
	setCapabilities(t, w, p.id, "custom_domains")
	for _, bad := range []string{"https://" + host, host + ":8443", "*.payrolla.test", "payrolla", "10.0.0.1", host + "/x", "app.taskiem.test", "x.app.taskiem.test"} {
		p.key.must(400, "POST", domains, map[string]any{"domain": bad})
	}
	dom := p.key.must(201, "POST", domains, map[string]any{"domain": "Automations.Payrolla.test"})
	if dom["domain"] != host || dom["record"] != "_taskiem-verify."+host || !strings.HasPrefix(dom["txt_value"].(string), "taskiem-verify=") {
		t.Fatalf("claim: %v", dom)
	}
	p.key.must(409, "POST", domains, map[string]any{"domain": host})
	p.key.must(404, "POST", "/v1/partner/embed-apps/"+uuid.NewString()+"/domains", map[string]any{"domain": "x.payrolla.test"})

	onHost := func(path string, hdr map[string]string, token string) (*rawResp, string) {
		return raw(t, "GET", w.base+path, host, token, hdr)
	}
	// Unverified: the host is not the app's.
	if resp, _ := onHost("/embed/frame", nil, ""); resp.StatusCode != 404 {
		t.Errorf("unverified domain serves the frame: %d", resp.StatusCode)
	}
	p.key.must(409, "POST", domains+"/"+host+"/verify", nil)
	w.txt["_taskiem-verify."+host] = []string{"unrelated", dom["txt_value"].(string)}
	p.key.must(200, "POST", domains+"/"+host+"/verify", nil)
	got := p.key.must(200, "GET", "/v1/partner/embed-apps/"+app, nil)["domains"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["verified_at"] == nil {
		t.Errorf("app's domains: %v", got)
	}

	// Verified: the frame (its app implied by the host), the bundle and the
	// app's embed API; nothing else.
	resp, body := onHost("/embed/frame", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(body, `app="`+app+`"`) || !strings.HasSuffix(resp.Header.Get("Content-Security-Policy"), "frame-ancestors "+siteApp) {
		t.Errorf("frame on the domain: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	if resp, _ := onHost("/embed/"+app+"/frame", nil, ""); resp.StatusCode != 200 {
		t.Errorf("frame with its app on the domain: %d", resp.StatusCode)
	}
	if resp, _ := onHost("/embed/"+otherApp+"/frame", nil, ""); resp.StatusCode != 404 {
		t.Errorf("another app's frame on the domain: %d", resp.StatusCode)
	}
	if resp, _ := onHost("/embed/v1/taskiem.js", nil, ""); resp.StatusCode != 200 {
		t.Errorf("bundle on the domain: %d", resp.StatusCode)
	}
	for _, path := range []string{"/v1/me", "/v1/partner/sub-tenants", "/v1/embed/" + otherApp + "/me", "/", "/login", "/v1/auth/login"} {
		if resp, _ := onHost(path, nil, ""); resp.StatusCode != 404 {
			t.Errorf("%s on the domain: %d", path, resp.StatusCode)
		}
	}
	tok := p.key.must(201, "POST", "/v1/partner/embed-apps/"+app+"/tokens", map[string]any{"end_user_id": "u1", "sub_tenant": sub,
		"permissions": []string{"workflow.read"}})["token"].(string)
	if resp, body := onHost("/v1/embed/"+app+"/me", map[string]string{"Origin": siteApp}, tok); resp.StatusCode != 200 {
		t.Errorf("embed API on the domain: %d %s", resp.StatusCode, body)
	}
	// The frame served on the domain calls it from the domain's origin.
	if resp, _ := onHost("/v1/embed/"+app+"/me", map[string]string{"Origin": "https://" + host}, tok); resp.StatusCode != 403 {
		t.Errorf("frame on the domain without its parent: %d", resp.StatusCode)
	}
	if resp, body := onHost("/v1/embed/"+app+"/me", map[string]string{"Origin": "https://" + host, "X-Taskiem-Embed-Parent": siteApp}, tok); resp.StatusCode != 200 {
		t.Errorf("frame on the domain: %d %s", resp.StatusCode, body)
	}
	// It is one of the app's origins: CORS answers it, on the platform host too.
	req, _ := http.NewRequest(http.MethodOptions, w.base+"/v1/embed/"+app+"/workflows", nil)
	req.Header.Set("Origin", "https://"+host)
	req.Header.Set("Access-Control-Request-Method", "GET")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = pre.Body.Close()
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "https://"+host {
		t.Errorf("preflight from the domain: %d %v", pre.StatusCode, pre.Header)
	}

	// Another partner may claim it, but not verify it: a verified domain is
	// exclusive.
	q := w.partner(t, "Squatter", "owner@squat.test")
	setCapabilities(t, w, q.id, "custom_domains")
	qapp, _ := q.app(t, map[string]any{"name": "web", "allowed_origins": []string{"https://squat.test"}})
	squat := q.key.must(201, "POST", "/v1/partner/embed-apps/"+qapp+"/domains", map[string]any{"domain": host})
	w.txt["_taskiem-verify."+host] = append(w.txt["_taskiem-verify."+host], squat["txt_value"].(string))
	q.key.must(409, "POST", "/v1/partner/embed-apps/"+qapp+"/domains/"+host+"/verify", nil)
	// Nor can it see or remove the partner's.
	q.key.must(404, "DELETE", "/v1/partner/embed-apps/"+app+"/domains/"+host, nil)

	// The mapping needs the capability and an active app.
	hostApp := func() *uuid.UUID {
		var id *uuid.UUID
		if err := w.env.DB.App.QueryRow(context.Background(), `SELECT taskiem_embed_host_app($1)`, host).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if id := hostApp(); id == nil || id.String() != app {
		t.Errorf("host maps to %v", id)
	}
	setCapabilities(t, w, p.id)
	if id := hostApp(); id != nil {
		t.Errorf("host maps to %v without the capability", id)
	}
	setCapabilities(t, w, p.id, "custom_domains")

	// Removed, it stops at once.
	p.key.must(204, "DELETE", domains+"/"+host, nil)
	if resp, _ := onHost("/embed/frame", nil, ""); resp.StatusCode != 404 {
		t.Errorf("removed domain serves the frame: %d", resp.StatusCode)
	}
	if n := auditCount(t, w, p.id.String(), "embed_app.domain_verify"); n != 1 {
		t.Errorf("domain verifications audited: %d", n)
	}
}

type capturedMail struct {
	mu   sync.Mutex
	msgs []string
}

func (m *capturedMail) Send(_ context.Context, _ string, _ []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, string(msg))
	return nil
}

// White-label needs the partner's plan; it reaches the embedded UI (/me)
// and messages sent on the sub-tenant's behalf.
func TestWhiteLabel(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	body := map[string]any{"name": "web", "allowed_origins": []string{siteApp}, "white_label": true}
	p.key.must(403, "POST", "/v1/partner/embed-apps", body)
	plain, _ := p.app(t, map[string]any{"name": "plain", "allowed_origins": []string{siteApp}})
	mail := &capturedMail{}
	a := &alerts.Alerter{Pool: w.env.DB.App, Mailer: mail, From: "alerts@taskiem.test"}
	ctx := context.Background()
	subID := uuid.MustParse(sub)
	if a.WhiteLabel(ctx, subID) {
		t.Error("white-label without any white-label app")
	}

	setCapabilities(t, w, p.id, "white_label")
	app, _ := p.app(t, body)
	if got := p.key.must(200, "GET", "/v1/partner/embed-apps/"+app, nil); got["white_label"] != true {
		t.Errorf("app: %v", got)
	}
	me := func(app string) any {
		u := p.endUser(t, w, app, sub, "u1", siteApp, "workflow.read")
		return u.call(200, "GET", "/me", nil)["white_label"]
	}
	if me(app) != true || me(plain) != false {
		t.Errorf("/me white_label: %v %v", me(app), me(plain))
	}
	if !a.WhiteLabel(ctx, subID) || a.WhiteLabel(ctx, p.id) {
		t.Error("messages for the sub-tenant (and only it) should be white-labelled")
	}
	if err := a.SendTest(ctx, subID, uuid.New(), "email", []byte(`{"to":["ops@customer.test"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.SendTest(ctx, p.id, uuid.New(), "email", []byte(`{"to":["ops@payrolla.test"]}`)); err != nil {
		t.Fatal(err)
	}
	if len(mail.msgs) != 2 || strings.Contains(mail.msgs[0], "Taskiem") || !strings.Contains(mail.msgs[0], "Subject: Test alert") ||
		!strings.Contains(mail.msgs[1], "Subject: [Taskiem] Test alert from Taskiem") {
		t.Errorf("mail:\n%s", strings.Join(mail.msgs, "\n---\n"))
	}

	// The operator withdrawing the capability withdraws it everywhere, at once.
	setCapabilities(t, w, p.id)
	if me(app) != false || a.WhiteLabel(ctx, subID) {
		t.Error("white-label outlives the capability")
	}
	// An app cannot be switched to white-label without it.
	p.key.must(403, "PUT", "/v1/partner/embed-apps/"+plain, body)
}

// The partner connector bridge: the partner's own connector, shared with
// its sub-tenants, and credentials it provisions into each sub-tenant's
// vault, usable by that sub-tenant's end users and seen by no one.
func TestConnectorBridge(t *testing.T) {
	manifest, module := wasmtest.Example(t)
	var mu sync.Mutex
	var auths []string
	ledger := newLedger(t, &mu, &auths)
	w := newWorld(t)
	w.env.Connectors.BaseURLs = map[string]string{"x_example_ledger": ledger}
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	a, b := p.sub(t, "Customer A", nil), p.sub(t, "Customer B", nil)
	perms := []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}
	appBody := map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{"x_example_ledger"}, "end_user_permissions": perms}
	conn := map[string]any{"environment": "prod", "connector": "x_example_ledger@1", "name": "main", "credentials": map[string]string{"api_key": "ledger-key-A"}}
	conns := "/v1/partner/sub-tenants/" + a + "/connections"

	// Not shared yet: unknown to apps and sub-tenants.
	if st, out := p.owner.upload(manifest, module); st != 201 {
		t.Fatalf("upload: %d %v", st, out)
	}
	p.key.must(400, "POST", "/v1/partner/embed-apps", appBody)
	p.key.must(400, "POST", conns, conn)
	p.key.must(400, "PUT", "/v1/partner/connectors/x_nothing/share", nil)

	p.key.must(200, "PUT", "/v1/partner/connectors/x_example_ledger/share", nil)
	if got := p.key.must(200, "GET", "/v1/partner/connectors", nil)["connectors"].([]any); len(got) != 1 || !strings.Contains(toJSON(got), "1.0.0") {
		t.Errorf("shared: %v", got)
	}
	// Shared, but no app allows it yet: no credentials for it.
	p.key.must(400, "POST", conns, conn)
	app, _ := p.app(t, appBody)
	p.key.must(400, "POST", conns, map[string]any{"connector": "x_example_ledger@1", "name": "main", "credentials": map[string]string{"nope": "x"}})
	p.key.must(404, "POST", "/v1/partner/sub-tenants/"+uuid.NewString()+"/connections", conn)
	created := p.key.must(201, "POST", conns, conn)
	p.key.must(409, "POST", conns, conn)
	if strings.Contains(toJSON(created), "ledger-key") {
		t.Errorf("the credential is shown back: %v", created)
	}
	connID := created["id"].(string)

	// Written into A's vault under A's key; audited in both chains.
	ctx := context.Background()
	var owner uuid.UUID
	var by string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT s.tenant_id, c.provisioned_by FROM connections c JOIN secrets s ON s.id = c.secret_ref WHERE c.id = $1`, connID).
		Scan(&owner, &by); err != nil || owner.String() != a || !strings.HasPrefix(by, "partner:"+p.id.String()+"/key:") {
		t.Errorf("stored in %v by %q (%v)", owner, by, err)
	}
	if auditCount(t, w, p.id.String(), "partner.connection.create") != 1 || auditCount(t, w, a, "partner.connection.create") != 1 ||
		auditCount(t, w, a, "connection.create") != 1 || auditCount(t, w, b, "connection.create") != 0 {
		t.Error("provisioning is not audited in both chains")
	}
	if _, err := w.env.Vault.Credentials(ctx, uuid.MustParse(b), "prod", "x_example_ledger", "main"); err == nil {
		t.Error("B can read A's credentials")
	}

	// The partner can write but not read: names and status only.
	listed := p.key.must(200, "GET", conns, nil)
	if len(listed["connections"].([]any)) != 1 || strings.Contains(toJSON(listed), "ledger-key") {
		t.Errorf("listed: %v", listed)
	}
	if strings.Contains(toJSON(p.owner.must(200, "GET", "/v1/connections", nil)), "main") {
		t.Error("the partner's own console lists the sub-tenant's connection")
	}

	// A's end user uses it, pre-authenticated, and never sees it.
	ua := p.endUser(t, w, app, a, "alice", "", perms...)
	if !strings.Contains(toJSON(ua.call(200, "GET", "/connectors", nil)), "x_example_ledger@1") {
		t.Error("A's end user is not offered the partner's connector")
	}
	runLedger := func(u *endUser) map[string]any {
		t.Helper()
		wf := u.call(201, "POST", "/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(ledgerFlow)})["id"].(string)
		u.call(200, "POST", "/workflows/"+wf+"/versions/1/publish", nil)
		run := u.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
		w.env.Drain(t)
		return u.call(200, "GET", "/runs/"+run, nil)
	}
	got := runLedger(ua)
	if st := got["run"].(map[string]any)["status"]; st != "completed" || strings.Contains(toJSON(got), "ledger-key") {
		t.Fatalf("A's run: %v\n%s", st, toJSON(got))
	}
	ua.call(404, "GET", "/connections", nil)
	// B's end user, same app, same connector: A's credential is not B's.
	ub := p.endUser(t, w, app, b, "bob", "", perms...)
	if st := runLedger(ub)["run"].(map[string]any)["status"]; st == "completed" {
		t.Error("B's run used A's credential")
	}
	mu.Lock()
	if len(auths) != 1 || auths[0] != "Bearer ledger-key-A" {
		t.Errorf("the ledger saw %v", auths)
	}
	mu.Unlock()

	// Rotated, the next run uses the new credential; removed, none.
	p.key.must(200, "PUT", conns+"/"+connID+"/credentials", map[string]any{"credentials": map[string]string{"api_key": "ledger-key-A2"}})
	runLedger(ua)
	mu.Lock()
	if auths[len(auths)-1] != "Bearer ledger-key-A2" {
		t.Errorf("after rotation the ledger saw %v", auths)
	}
	mu.Unlock()
	p.key.must(404, "PUT", "/v1/partner/sub-tenants/"+b+"/connections/"+connID+"/credentials", map[string]any{"credentials": map[string]string{"api_key": "x"}})
	p.key.must(204, "DELETE", conns+"/"+connID, nil)
	if n := len(p.key.must(200, "GET", conns, nil)["connections"].([]any)); n != 0 {
		t.Errorf("%d connections after removal", n)
	}

	// Another partner reaches none of it.
	q := w.partner(t, "Other", "owner@other.test")
	q.key.must(404, "POST", conns, conn)
	q.key.must(404, "GET", conns, nil)

	// Unshared, sub-tenants no longer get it.
	p.key.must(200, "DELETE", "/v1/partner/connectors/x_example_ledger/share", nil)
	if strings.Contains(toJSON(ua.call(200, "GET", "/connectors", nil)), "x_example_ledger") {
		t.Error("an unshared connector is still offered")
	}
}

// newLedger is a fake ledger API for the example connector that records the
// Authorization header of each payment.
func newLedger(t *testing.T, mu *sync.Mutex, auths *[]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		ref, _ := b["reference"].(string)
		_, _ = w.Write([]byte(`{"id":"pay_9","reference":"` + ref + `","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
