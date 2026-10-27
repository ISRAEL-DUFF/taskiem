package connector

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
)

const modesManifest = `manifest: connector/v1
id: wallet
version: 1.0.0
name: Wallet
category: payments
auth:
  type: api_key
  fields: [{ key: api_key, label: API key, secret: true }]
  modes:
    - { mode: live, label: Live (real money) }
    - { mode: test, label: Test (practice), base_url: https://sandbox.wallet.example/v1 }
base_url: https://sandbox.wallet.example/v1
actions:
  ping: { title: Ping, class: read, input: { type: object } }
`

func TestHostModes(t *testing.T) {
	m, probs := Parse([]byte(modesManifest))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if got := m.Auth.Modes(); len(got) != 2 || got[0].Mode != "live" || got[1].Label != "Test (practice)" {
		t.Errorf("modes %+v", got)
	}
	// Live has no address until the deployment gives it one.
	if _, ok := m.ModeURL("live"); ok {
		t.Error("live offered with no address")
	}
	if _, err := m.BaseFor(map[string]string{ModeKey: "live"}); !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "no address") {
		t.Errorf("a live connection with no live address: %v", err)
	}
	if base, err := m.BaseFor(map[string]string{ModeKey: "test"}); err != nil || base != "https://sandbox.wallet.example/v1" {
		t.Errorf("test: %q %v", base, err)
	}
	// A connection made before modes goes where it always went.
	if base, _ := m.BaseFor(map[string]string{}); base != "https://sandbox.wallet.example/v1" {
		t.Errorf("no mode: %q", base)
	}
	m.SetModeURL("live", "https://api.wallet.example/v1/")
	m.SetModeURL("test", "")
	if base, err := m.BaseFor(map[string]string{ModeKey: "live"}); err != nil || base != "https://api.wallet.example/v1" {
		t.Errorf("live once set: %q %v", base, err)
	}
	if base, _ := m.BaseFor(map[string]string{ModeKey: "test"}); base != "https://sandbox.wallet.example/v1" {
		t.Errorf("an empty override keeps the mode: %q", base)
	}
	// Both hosts may be reached, even after the base URL is overridden.
	m.OverrideBaseURL("http://127.0.0.1:8701")
	if h := m.Hosts(); !slices.Contains(h, "api.wallet.example") || !slices.Contains(h, "sandbox.wallet.example") || !slices.Contains(h, "127.0.0.1") {
		t.Errorf("hosts %v", h)
	}
	if (Request{Credentials: map[string]string{ModeKey: "test"}}).Mode() != "test" {
		t.Error("Request.Mode")
	}
}

func TestHostModesRules(t *testing.T) {
	for name, c := range map[string]struct{ from, to, want string }{
		"twice": {"- { mode: test, label: Test (practice), base_url: https://sandbox.wallet.example/v1 }",
			"- { mode: live, label: Again }", `mode "live" is named twice`},
		"with prefixes": {"fields: [{ key: api_key, label: API key, secret: true }]",
			"fields: [{ key: api_key, label: API key, secret: true, prefixes: [{ prefix: sk_live_, mode: live }] }]", "hosts or key prefixes, not both"},
		"http": {"base_url: https://sandbox.wallet.example/v1 }", "base_url: http://sandbox.wallet.example/v1 }", "modes"},
		// A mode's own platform app (rule 20) is for linking by OAuth.
		"a client without oauth": {"base_url: https://sandbox.wallet.example/v1 }", "base_url: https://sandbox.wallet.example/v1, client: walletsandbox }", "a mode's platform app needs auth.oauth2"},
	} {
		src := strings.Replace(modesManifest, c.from, c.to, 1)
		_, probs := Parse([]byte(src))
		if !strings.Contains(strings.Join(probs, "; "), c.want) {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}

const zohoLike = `manifest: connector/v1
id: books
version: 1.0.0
name: Books
category: productivity
auth:
  type: oauth2
  fields:
    - { key: access_token, label: Access token, secret: true }
    - { key: accounts_server, label: Accounts server }
    - { key: api_domain, label: API domain }
  oauth2:
    authorize_url: https://accounts.books.example/oauth/v2/auth
    token_url: "${connection.accounts_server}/oauth/v2/token"
    scopes: [books]
    pkce: true
    fields_from_callback: { accounts_server: accounts-server }
    fields_from_token: { access_token: access_token, api_domain: api_domain }
connection_hosts: ["*.books.example", "*.books.eu"]
egress_hosts: ["${connection.api_domain}", "${connection.accounts_server}"]
base_url: https://www.books.example
actions:
  ping: { title: Ping, class: read, input: { type: object } }
`

func TestConnectionHosts(t *testing.T) {
	m, probs := Parse([]byte(zohoLike))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	for host, ok := range map[string]bool{"accounts.books.eu": true, "www.books.example": true, "books.example": false, "evilbooks.example": false, "books.example.evil.net": false} {
		if m.ConnectionHostAllowed(host) != ok {
			t.Errorf("%s: allowed %v", host, !ok)
		}
	}
	if u, err := m.OAuthURL(m.Auth.OAuth2.TokenURL, map[string]string{"accounts_server": "https://accounts.books.eu"}); err != nil || u != "https://accounts.books.eu/oauth/v2/token" {
		t.Errorf("the account's token URL: %q %v", u, err)
	}
	for _, bad := range []string{"https://accounts.evil.example", "http://accounts.books.eu", "https://accounts.books.eu/steal?x=", "", "accounts.books.eu"} {
		if _, err := m.OAuthURL(m.Auth.OAuth2.TokenURL, map[string]string{"accounts_server": bad}); err == nil {
			t.Errorf("%q accepted as the accounts server", bad)
		}
	}
	// The egress allow-list takes a connection's hosts only where allowed.
	got := m.HostsFor(map[string]string{"api_domain": "https://www.books.eu", "accounts_server": "https://accounts.evil.example"})
	if !slices.Equal(got, []string{"www.books.eu"}) {
		t.Errorf("hosts for the connection: %v", got)
	}
	// A server from a connection field needs the list of the provider's.
	src := strings.Replace(zohoLike, `connection_hosts: ["*.books.example", "*.books.eu"]`+"\n", "", 1)
	if _, probs := Parse([]byte(src)); !strings.Contains(strings.Join(probs, ";"), "connection_hosts must list") {
		t.Errorf("no connection_hosts: %v", probs)
	}
}

// Each mode may be linked with its own platform app (rule 20); the
// connector's is the default.
func TestModeClients(t *testing.T) {
	src := strings.Replace(zohoLike, "  oauth2:\n", `  modes:
    - { mode: live, label: Live, base_url: https://www.books.example }
    - { mode: test, label: Test, base_url: https://sandbox.books.example, client: bookssandbox }
  oauth2:
    client_auth: basic
    revoke_encoding: json
`, 1)
	m, probs := Parse([]byte(src))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if m.ClientFor("test") != "bookssandbox" || m.ClientFor("live") != "books" || m.ClientFor("") != "books" || !slices.Equal(m.Clients(), []string{"books", "bookssandbox"}) {
		t.Errorf("clients: test %s, live %s, all %v", m.ClientFor("test"), m.ClientFor("live"), m.Clients())
	}
	if m.Auth.OAuth2.ClientAuth != "basic" || m.Auth.OAuth2.RevokeEncoding != "json" {
		t.Errorf("oauth2 %+v", m.Auth.OAuth2)
	}
	if _, probs := Parse([]byte(strings.Replace(src, "client_auth: basic", "client_auth: header", 1))); len(probs) == 0 {
		t.Error("an unknown client_auth was taken")
	}
}

// A grant the connector completes (rule 21): linked fields count as
// filled, and they need a Link, as a Link needs them.
func TestLinkedFields(t *testing.T) {
	src := strings.Replace(zohoLike, "    fields_from_token: { access_token: access_token, api_domain: api_domain }\n",
		"    fields_from_token: { api_domain: api_domain }\n    token_method: get\n    linked_fields: [access_token]\n", 1)
	m, probs := Parse([]byte(src))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if m.Auth.OAuth2.TokenMethod != "get" {
		t.Errorf("token_method %q", m.Auth.OAuth2.TokenMethod)
	}
	ping := ActionFunc(func(context.Context, Request) (Response, error) { return Response{}, nil })
	if err := NewRegistry().Register(&Connector{Manifest: m, Actions: map[string]Action{"ping": ping}}); err == nil || !strings.Contains(err.Error(), "linked_fields") {
		t.Errorf("linked fields without a Link: %v", err)
	}
	link := func(context.Context, LinkCall) (map[string]string, error) { return nil, nil }
	if err := NewRegistry().Register(&Connector{Manifest: m, Actions: map[string]Action{"ping": ping}, Link: link}); err != nil {
		t.Errorf("with a Link: %v", err)
	}
	if _, probs := Parse([]byte(strings.Replace(src, "linked_fields: [access_token]", "linked_fields: [nope]", 1))); !strings.Contains(strings.Join(probs, "; "), `"nope" is not an auth field`) {
		t.Errorf("an unknown linked field: %v", probs)
	}
}

// body_token (Zoho CRM's watch token, echoed in the body) names its field,
// and may verify a remotely registered trigger.
func TestBodyToken(t *testing.T) {
	src := `manifest: connector/v1
id: watched
version: 1.0.0
name: Watched
category: productivity
auth:
  type: api_key
  fields: [{ key: token, label: Token, secret: true }]
base_url: https://watched.example
actions:
  ping: { title: Ping, class: read, input: { type: object } }
triggers:
  changed:
    type: webhook
    registration: remote
    verify: { scheme: body_token, field: token }
`
	if _, probs := Parse([]byte(src)); len(probs) > 0 {
		t.Fatal(probs)
	}
	if _, probs := Parse([]byte(strings.Replace(src, "body_token, field: token", "body_token", 1))); !strings.Contains(strings.Join(probs, "; "), "body_token names the body field") {
		t.Errorf("without a field: %v", probs)
	}
}
