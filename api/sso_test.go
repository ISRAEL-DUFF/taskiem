package api_test

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
	"github.com/israel-duff/taskiem/engine/saml/samltest"
)

const publicURL = "https://app.taskiem.test"

func ssoWorld(t *testing.T) *world {
	w := newWorld(t)
	w.srv.PublicURL = publicURL
	w.srv.LoginBurst = 100
	txt := map[string][]string{}
	old := api.LookupTXT
	api.LookupTXT = func(_ context.Context, name string) ([]string, error) { return txt[name], nil }
	t.Cleanup(func() { api.LookupTXT = old })
	w.txt = txt
	return w
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// local turns a public URL of ours into the test server's.
func (w *world) local(u string) string { return strings.Replace(u, publicURL, w.base, 1) }

// follow makes a request without following redirects and returns where it
// points and the session cookie it set, if any.
func follow(t *testing.T, req *http.Request) (string, string) {
	t.Helper()
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: %d %s", req.Method, req.URL, resp.StatusCode, b)
	}
	tok := ""
	for _, c := range resp.Cookies() {
		if c.Name == "taskiem_session" {
			tok = c.Value
		}
	}
	return resp.Header.Get("Location"), tok
}

func get(t *testing.T, u string) *http.Request {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// oidcSignIn runs the whole code flow and returns where it lands and the
// session.
func (w *world) oidcSignIn(t *testing.T, idp *oidctest.Provider, conn string) (string, *client) {
	t.Helper()
	toIdP, _ := follow(t, get(t, w.base+"/v1/auth/sso/"+conn+"/start?return_to=/runs"))
	back, _ := follow(t, get(t, toIdP))
	landed, tok := follow(t, get(t, w.local(back)))
	if tok == "" {
		return landed, nil
	}
	return landed, &client{t: t, base: w.base, token: tok}
}

func TestOIDCSignIn(t *testing.T) {
	w := ssoWorld(t)
	idp := oidctest.New()
	defer idp.Close()
	owner := w.tenant(t, "Bank", "owner@bank.test")

	conn := owner.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "Okta",
		"oidc":          map[string]any{"issuer": idp.URL, "client_id": idp.ClientID, "client_secret": idp.ClientSecret, "groups_claim": "groups"},
		"default_roles": []string{"viewer"}, "group_roles": map[string]any{"treasury": []string{"operator"}}})
	id := conn["id"].(string)
	if conn["redirect_uri"] != publicURL+"/v1/auth/sso/oidc/callback" {
		t.Errorf("redirect uri %v", conn["redirect_uri"])
	}

	// The domain routes nobody, and signs nobody in, until verified.
	dom := owner.must(201, "POST", "/v1/sso/"+id+"/domains", map[string]any{"domain": "bank.test"})
	anon := &client{t: t, base: w.base}
	if out := anon.must(200, "POST", "/v1/auth/sso/discover", map[string]any{"email": "ada@bank.test"}); out["sso"] != false {
		t.Errorf("unverified domain routes: %v", out)
	}
	idp.Claims = map[string]any{"sub": "u-ada", "email": "ada@bank.test", "email_verified": true, "name": "Ada", "groups": []any{"treasury"}}
	if landed, c := w.oidcSignIn(t, idp, id); c != nil || !strings.Contains(landed, "sso_error") {
		t.Fatalf("signed in on an unverified domain: %s", landed)
	}
	owner.must(409, "POST", "/v1/sso/domains/bank.test/verify", nil)
	w.txt["_taskiem-verify.bank.test"] = []string{dom["txt_value"].(string)}
	owner.must(204, "POST", "/v1/sso/domains/bank.test/verify", nil)
	if out := anon.must(200, "POST", "/v1/auth/sso/discover", map[string]any{"email": "Ada@Bank.test"}); out["sso"] != true || out["connection_id"] != id {
		t.Errorf("discover: %v", out)
	}

	// First sign-in creates the member with mapped roles.
	landed, ada := w.oidcSignIn(t, idp, id)
	if ada == nil || landed != "/runs" {
		t.Fatalf("sign-in landed %s", landed)
	}
	me := ada.must(200, "GET", "/v1/me", nil)
	if me["auth_method"] != "sso" || strings.Join(toStrings(me["roles"]), ",") != "operator,viewer" {
		t.Fatalf("me: %v", me)
	}
	// Leaving the group takes away the role SSO gave, not others.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@bank.test", "roles": []string{"approver"}})
	idp.Claims["groups"] = []any{}
	_, ada = w.oidcSignIn(t, idp, id)
	if roles := strings.Join(toStrings(ada.must(200, "GET", "/v1/me", nil)["roles"]), ","); roles != "approver,viewer" {
		t.Errorf("after leaving the group: %s", roles)
	}

	// Refusals: another domain, an unverified email, a replayed callback.
	idp.Claims = map[string]any{"sub": "u-eve", "email": "eve@evil.test", "email_verified": true}
	if _, c := w.oidcSignIn(t, idp, id); c != nil {
		t.Error("signed in someone outside the connection's domains")
	}
	idp.Claims = map[string]any{"sub": "u-bob", "email": "bob@bank.test", "email_verified": false}
	if _, c := w.oidcSignIn(t, idp, id); c != nil {
		t.Error("signed in an unverified email")
	}
	idp.Claims = map[string]any{"sub": "u-ada", "email": "ada@bank.test", "email_verified": true}
	toIdP, _ := follow(t, get(t, w.base+"/v1/auth/sso/"+id+"/start"))
	back, _ := follow(t, get(t, toIdP))
	follow(t, get(t, w.local(back)))
	if landed, tok := follow(t, get(t, w.local(back))); tok != "" || !strings.Contains(landed, "sso_error") {
		t.Error("a replayed callback signed in")
	}
	// Return addresses stay on our pages.
	toIdP, _ = follow(t, get(t, w.base+"/v1/auth/sso/"+id+"/start?return_to=//evil.test/x"))
	back, _ = follow(t, get(t, toIdP))
	if landed, _ := follow(t, get(t, w.local(back))); landed != "/" {
		t.Errorf("open redirect: %s", landed)
	}

	// Enforced: members on the domain cannot use their password.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "bob@bank.test", "password": "correct horse battery", "roles": []string{"viewer"}})
	owner.must(204, "PUT", "/v1/sso/"+id, map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{}, "enforce": true})
	if st, body := anon.do("POST", "/v1/auth/login", map[string]any{"email": "bob@bank.test", "password": "correct horse battery"}); st != 401 || body["sso_required"] != true {
		t.Errorf("password under enforcement: %d %v", st, body)
	}
	if _, c := w.oidcSignIn(t, idp, id); c == nil {
		t.Error("SSO stopped working under enforcement")
	}
	// Owners are exempt: a broken identity provider cannot lock the tenant out.
	w.login(t, "owner@bank.test", "correct horse battery")
}

func TestSSOMappingCannotEscalate(t *testing.T) {
	w := ssoWorld(t)
	idp := oidctest.New()
	defer idp.Close()
	owner := w.tenant(t, "Bank", "owner@bank.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm@bank.test", "password": "correct horse battery", "roles": []string{"admin"}})
	adm := w.login(t, "adm@bank.test", "correct horse battery")
	cfg := map[string]any{"issuer": idp.URL, "client_id": idp.ClientID, "client_secret": idp.ClientSecret}
	adm.must(403, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "x", "oidc": cfg, "group_roles": map[string]any{"it": []string{"owner"}}})
	owner.must(201, "PUT", "/v1/roles/privacy", map[string]any{"permissions": []string{"pii.reveal"}})
	adm.must(403, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "x", "oidc": cfg, "default_roles": []string{"privacy"}})
	adm.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "x", "oidc": cfg, "default_roles": []string{"viewer"}})
	// A domain another tenant claimed cannot be claimed again.
	other := w.tenant(t, "Other", "owner@other.test")
	oc := other.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "y", "oidc": cfg})
	other.must(201, "POST", "/v1/sso/"+oc["id"].(string)+"/domains", map[string]any{"domain": "bank.test"})
	conns := owner.must(200, "GET", "/v1/sso", nil)["connections"].([]any)
	owner.must(409, "POST", "/v1/sso/"+conns[0].(map[string]any)["id"].(string)+"/domains", map[string]any{"domain": "bank.test"})
}

func TestSAMLSignIn(t *testing.T) {
	w := ssoWorld(t)
	owner := w.tenant(t, "Bank", "owner@bank.test")
	idp := samltest.New("https://idp.bank.test", "https://idp.bank.test/sso")
	conn := owner.must(201, "POST", "/v1/sso", map[string]any{"protocol": "saml", "name": "ADFS",
		"saml":          map[string]any{"metadata_xml": string(idp.Metadata()), "name_attr": "name", "groups_attr": "groups"},
		"default_roles": []string{"viewer"}, "group_roles": map[string]any{"approvers": []string{"approver"}}})
	id := conn["id"].(string)
	dom := owner.must(201, "POST", "/v1/sso/"+id+"/domains", map[string]any{"domain": "bank.test"})
	w.txt["_taskiem-verify.bank.test"] = []string{dom["txt_value"].(string)}
	owner.must(204, "POST", "/v1/sso/domains/bank.test/verify", nil)

	md, err := http.Get(w.local(conn["metadata_url"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	mdb, _ := io.ReadAll(md.Body)
	_ = md.Body.Close()
	if !strings.Contains(string(mdb), "/v1/auth/sso/saml/acs") {
		t.Errorf("sp metadata: %s", mdb)
	}

	signIn := func(edit func(*samltest.Assertion)) string {
		toIdP, _ := follow(t, get(t, w.base+"/v1/auth/sso/"+id+"/start?return_to=/approvals"))
		u, _ := url.Parse(toIdP)
		a := samltest.Assertion{InResponseTo: requestID(t, u.Query().Get("SAMLRequest")), Audience: conn["entity_id"].(string), Recipient: conn["acs_url"].(string),
			NameID: "ada@bank.test", Attributes: map[string][]string{"name": {"Ada"}, "groups": {"approvers"}}}
		if edit != nil {
			edit(&a)
		}
		form := url.Values{"SAMLResponse": {idp.Response(a)}, "RelayState": {u.Query().Get("RelayState")}}
		req, _ := http.NewRequest("POST", w.base+"/v1/auth/sso/saml/acs", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		landed, tok := follow(t, req)
		if tok == "" {
			return landed
		}
		c := &client{t: t, base: w.base, token: tok}
		if roles := strings.Join(toStrings(c.must(200, "GET", "/v1/me", nil)["roles"]), ","); roles != "approver,viewer" {
			t.Errorf("roles %s", roles)
		}
		return landed
	}
	if landed := signIn(nil); landed != "/approvals" {
		t.Fatalf("saml sign-in landed %s", landed)
	}
	if landed := signIn(func(a *samltest.Assertion) { a.InResponseTo = "_another" }); !strings.Contains(landed, "sso_error") {
		t.Error("an answer to another request signed in")
	}
	if landed := signIn(func(a *samltest.Assertion) { a.NameID = "eve@evil.test" }); !strings.Contains(landed, "sso_error") {
		t.Error("someone outside the domains signed in")
	}
}

// requestID reads the ID of a deflated, base64 AuthnRequest.
func requestID(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	xmlb, err := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		ID string `xml:"ID,attr"`
	}
	if err := xml.Unmarshal(xmlb, &req); err != nil {
		t.Fatal(err)
	}
	return req.ID
}
