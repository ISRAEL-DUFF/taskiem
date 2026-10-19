package api_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Connecting Xenibase by OAuth (decision 0034): start answers the consent
// URL with the state and a PKCE challenge; the fake plays the consent page
// (a code with what the person approved); the callback exchanges the code
// with the client secret and the verifier and saves the connection with
// the fields the manifest takes from the token.

const (
	oauthProject = "7d2c9a10-5b4e-4f00-9c11-0000000000a1"
	oauthOther   = "7d2c9a10-5b4e-4f00-9c11-0000000000b2"
)

type oauthWorld struct {
	*world
	fake *xenibasetest.Fake
}

// newOAuthWorld is a world whose platform Xenibase is the fake, with
// Taskiem registered there as a client.
func newOAuthWorld(t *testing.T) *oauthWorld {
	w := newWorld(t)
	f := xenibasetest.New(t)
	f.AddProject(oauthProject, "shop")
	f.AddProject(oauthOther, "blog")
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: f.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"xenibase": {ID: "xbapp_taskiem", Secret: "xbc_secret"}}
	f.RegisterOAuthClient(xenibasetest.OAuthClient{ID: "xbapp_taskiem", Secret: "xbc_secret", RedirectURI: w.base + "/v1/connections/oauth/callback"})
	return &oauthWorld{world: w, fake: f}
}

// start begins linking and returns the consent URL's query.
func (w *oauthWorld) start(t *testing.T, c *client, name string) url.Values {
	t.Helper()
	out := c.must(200, "POST", "/v1/connections/oauth/start", map[string]any{"connector": "xenibase@1", "environment": "prod", "name": name})
	u, err := url.Parse(out["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.String(), w.fake.URL+"/oauth/authorize?") {
		t.Fatalf("consent URL %s is not the platform's Xenibase", u)
	}
	return u.Query()
}

// callback plays the browser coming back and returns where it is sent.
func (w *oauthWorld) callback(t *testing.T, params url.Values) url.Values {
	t.Helper()
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(w.base + "/v1/connections/oauth/callback?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback: status %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loc.String(), w.base+"/connections?") {
		t.Fatalf("callback sends the browser to %s, not the Connections page", loc)
	}
	return loc.Query()
}

// link connects a named Xenibase connection restricted to the project.
func (w *oauthWorld) link(t *testing.T, c *client, name, code string) string {
	t.Helper()
	q := w.start(t, c, name)
	w.fake.AddOAuthCode(xenibasetest.OAuthCode{Code: code, Challenge: q.Get("code_challenge"), Scopes: []string{"read", "write"}, ProjectIDs: []string{oauthProject}})
	back := w.callback(t, url.Values{"code": {code}, "state": {q.Get("state")}})
	if back.Get("linked") == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}
	return back.Get("linked")
}

func TestOAuthConnection(t *testing.T) {
	w := newOAuthWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := tenantID(t, owner)

	// The connector says it is linkable here.
	for _, c := range owner.must(200, "GET", "/v1/connectors", nil)["connectors"].([]any) {
		if c := c.(map[string]any); c["ref"] == "xenibase@1" && c["oauth_linkable"] != true {
			t.Fatalf("xenibase@1 is not linkable: %v", c["oauth_linkable"])
		}
	}

	q := w.start(t, owner, "shop")
	if q.Get("client_id") != "xbapp_taskiem" || q.Get("redirect_uri") != w.base+"/v1/connections/oauth/callback" || q.Get("scope") != "read write" ||
		q.Get("code_challenge_method") != "S256" || len(q.Get("state")) != 43 || len(q.Get("code_challenge")) != 43 {
		t.Fatalf("consent query: %v", q)
	}
	// What the person approved: the organisation, both scopes, one project.
	w.fake.AddOAuthCode(xenibasetest.OAuthCode{Code: "code-1", Challenge: q.Get("code_challenge"), Scopes: []string{"read", "write"}, ProjectIDs: []string{oauthProject}, ExpiresIn: 7776000})
	back := w.callback(t, url.Values{"code": {"code-1"}, "state": {q.Get("state")}})
	id := back.Get("linked")
	if id == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}

	// The connection is listed, active, expiring with the token.
	var listed map[string]any
	for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if c := c.(map[string]any); c["id"] == id {
			listed = c
		}
	}
	if listed == nil || listed["connector"] != "xenibase" || listed["name"] != "shop" || listed["auth_type"] != "oauth2" || listed["status"] != "active" || listed["expires_at"] == nil {
		t.Fatalf("listed: %v", listed)
	}
	// Its credentials are the token and what the manifest takes from the
	// token answer: the organisation, the one project, the server.
	creds, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "xenibase", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(creds["token"], "xb_") || !w.fake.HasToken(creds["token"]) || creds["org_id"] != w.fake.OrgID || creds["project_id"] != oauthProject || creds["base_url"] != w.fake.URL || creds["secret_key"] != "" {
		t.Fatalf("credentials: %v", creds)
	}
	token := creds["token"]
	// The connection test works with them (less the server: the fake is
	// plain http, which the connector takes only as the platform's own).
	c, _ := w.env.Registry.Get("xenibase@1")
	usable := map[string]string{}
	for k, v := range creds {
		if k != "base_url" {
			usable[k] = v
		}
	}
	if _, err := c.Actions["test_connection"].Execute(context.Background(), connectorRequest(usable)); err != nil {
		t.Fatalf("test_connection with the linked token: %v", err)
	}
	// The exchange was sent as published: the secret in the body, once.
	if n := strings.Count(strings.Join(w.fake.Requests, "\n"), "POST /api/v1/oauth/token"); n != 1 {
		t.Fatalf("%d token exchanges", n)
	}

	// The state works once.
	if back := w.callback(t, url.Values{"code": {"code-1"}, "state": {q.Get("state")}}); !strings.Contains(back.Get("error"), "unknown, already used or expired") {
		t.Fatalf("reused state: %q", back.Get("error"))
	}

	// Afterwards the person adds the secret key and data API URL; the
	// token and the rest stay. Unknown fields are refused.
	w.fake.AddSecretKey(oauthProject, "xb_sec_shop")
	owner.must(400, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"nope": "x"}})
	owner.must(400, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"token": ""}})
	owner.must(204, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"secret_key": "xb_sec_shop", "api_url": w.fake.APIURL(oauthProject)}})
	owner.must(404, "PUT", "/v1/connections/"+uuid.Must(uuid.NewV7()).String()+"/credentials", map[string]any{"credentials": map[string]string{"secret_key": "x"}})
	creds, err = w.env.Vault.Credentials(context.Background(), tenant, "prod", "xenibase", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if creds["token"] != token || creds["secret_key"] != "xb_sec_shop" || creds["api_url"] != w.fake.APIURL(oauthProject) || creds["project_id"] != oauthProject {
		t.Fatalf("merged credentials: %v", creds)
	}

	// A deployed workflow naming the connection stops a delete, unless
	// forced; deleting revokes the token at Xenibase.
	wf := publishFlow(t, owner, `{"schema":"wd/v1","id":"wf_rows","version":1,"name":"rows","trigger":{"type":"manual"},
	  "steps":[{"id":"q","type":"connector","connector":"xenibase@1","action":"query_rows","connection":"shop","input":{"table":"orders"}}]}`)
	st, out := owner.do("DELETE", "/v1/connections/"+id, nil)
	if st != 409 || out["code"] != "in_use" || !strings.Contains(toJSON(out["workflows"]), wf) || !strings.Contains(toJSON(out["workflows"]), `"prod"`) {
		t.Fatalf("delete in use: %d %v", st, out)
	}
	owner.must(204, "DELETE", "/v1/connections/"+id+"?force=true", nil)
	owner.must(404, "DELETE", "/v1/connections/"+id, nil)
	if w.fake.HasToken(token) || len(w.fake.Revoked) != 1 || w.fake.Revoked[0] != token {
		t.Fatalf("token not revoked at Xenibase: revoked %v", w.fake.Revoked)
	}
	for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if c.(map[string]any)["id"] == id {
			t.Fatal("deleted connection still listed")
		}
	}
	if _, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "xenibase", "shop"); err == nil {
		t.Fatal("credentials survive the delete")
	}

	// A connection nothing names deletes at once.
	id2 := w.link(t, owner, "shop2", "code-2")
	owner.must(204, "DELETE", "/v1/connections/"+id2, nil)

	// Audited: the start, the create (via oauth), the merge, the deletes.
	actions := map[string]int{}
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT action, detail::text FROM audit_log WHERE tenant_id = $1 AND action LIKE 'connection.%'`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var created string
	for rows.Next() {
		var a, d string
		if err := rows.Scan(&a, &d); err != nil {
			t.Fatal(err)
		}
		actions[a]++
		if a == "connection.create" {
			created = d
		}
	}
	if actions["connection.oauth_start"] != 2 || actions["connection.create"] != 2 || actions["connection.credentials_replace"] != 1 || actions["connection.delete"] != 2 || !strings.Contains(created, `"via": "oauth"`) && !strings.Contains(created, `"via":"oauth"`) {
		t.Fatalf("audit: %v (create detail %s)", actions, created)
	}
}

func TestOAuthConnectionRefusals(t *testing.T) {
	w := newOAuthWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// Unknown state, and a start whose request has expired.
	if back := w.callback(t, url.Values{"code": {"x"}, "state": {strings.Repeat("a", 43)}}); !strings.Contains(back.Get("error"), "unknown, already used or expired") {
		t.Fatalf("unknown state: %q", back.Get("error"))
	}
	if back := w.callback(t, url.Values{"code": {"x"}, "state": {"short"}}); !strings.Contains(back.Get("error"), "unknown, already used or expired") {
		t.Fatalf("short state: %q", back.Get("error"))
	}
	q := w.start(t, owner, "late")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE oauth_connection_requests SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	w.fake.AddOAuthCode(xenibasetest.OAuthCode{Code: "code-late", Challenge: q.Get("code_challenge"), Scopes: []string{"read"}, ProjectIDs: []string{oauthProject}})
	if back := w.callback(t, url.Values{"code": {"code-late"}, "state": {q.Get("state")}}); !strings.Contains(back.Get("error"), "expired") {
		t.Fatalf("expired state: %q", back.Get("error"))
	}

	// The person denied.
	q = w.start(t, owner, "denied")
	if back := w.callback(t, url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}); !strings.Contains(back.Get("error"), "did not approve") {
		t.Fatalf("denied: %q", back.Get("error"))
	}

	// The grant must name exactly one project: none (every project) and
	// two are refused, and the token given back.
	for name, projects := range map[string][]string{"all": nil, "two": {oauthProject, oauthOther}} {
		q = w.start(t, owner, name)
		w.fake.AddOAuthCode(xenibasetest.OAuthCode{Code: "code-" + name, Challenge: q.Get("code_challenge"), Scopes: []string{"read", "write"}, ProjectIDs: projects})
		back := w.callback(t, url.Values{"code": {"code-" + name}, "state": {q.Get("state")}})
		if e := back.Get("error"); !strings.Contains(e, "choose exactly one") || back.Get("linked") != "" {
			t.Fatalf("%s projects: linked %q, error %q", name, back.Get("linked"), e)
		}
	}
	if len(w.fake.Revoked) != 2 {
		t.Fatalf("tokens that made no connection were not revoked: %v", w.fake.Revoked)
	}

	// A code works once: the second exchange fails and Xenibase revokes
	// what the first issued (the fake does what RFC 6749 says).
	id := w.link(t, owner, "once", "code-once")
	q = w.start(t, owner, "again")
	if back := w.callback(t, url.Values{"code": {"code-once"}, "state": {q.Get("state")}}); !strings.Contains(back.Get("error"), "invalid_grant") {
		t.Fatalf("replayed code: %q", back.Get("error"))
	}
	owner.must(204, "DELETE", "/v1/connections/"+id, nil)

	// A name already taken is refused at the start, not after consent.
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "xenibase@1", "name": "pasted", "credentials": map[string]string{"token": "pgd_x", "project_id": oauthProject}})
	owner.must(409, "POST", "/v1/connections/oauth/start", map[string]any{"connector": "xenibase@1", "environment": "prod", "name": "pasted"})
	// A connector without OAuth, and an unknown one.
	owner.must(400, "POST", "/v1/connections/oauth/start", map[string]any{"connector": "fakepay@1", "environment": "prod"})
	owner.must(400, "POST", "/v1/connections/oauth/start", map[string]any{"connector": "nope@1", "environment": "prod"})

	// Without a client registered the connector is not linkable, and the
	// API says so.
	w.srv.OAuthClients = nil
	st, out := owner.do("POST", "/v1/connections/oauth/start", map[string]any{"connector": "xenibase@1", "environment": "prod", "name": "later"})
	if st != 503 || out["code"] != "oauth_unavailable" || !strings.Contains(out["error"].(string), "TASKIEM_OAUTH_XENIBASE_CLIENT_ID") {
		t.Fatalf("no client: %d %v", st, out)
	}
	for _, c := range owner.must(200, "GET", "/v1/connectors", nil)["connectors"].([]any) {
		if c := c.(map[string]any); c["ref"] == "xenibase@1" && c["oauth_linkable"] != false {
			t.Fatalf("xenibase@1 linkable without a client: %v", c["oauth_linkable"])
		}
	}

	// A member without connection.manage may not start.
	viewer := addMember(t, w.world, owner, "viewer", "viewer")
	viewer.must(403, "POST", "/v1/connections/oauth/start", map[string]any{"connector": "xenibase@1", "environment": "prod"})
}

// connectorRequest is a connector call with the credentials, as a test
// would make it.
func connectorRequest(creds map[string]string) connector.Request {
	return connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: http.DefaultClient, Attempt: 1}
}
