package api_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/connector"
)

// A connection made through Taskiem's platform app (decision 0051) is
// looked up by the app's name, which several connectors may share, takes
// fields from the query the provider sends the browser back with, and
// records the account it is for, so the app's events for that account
// reach it.
func TestOAuthThroughAPlatformApp(t *testing.T) {
	w := newOAuthWorld(t)
	c, ok := w.env.Registry.Get("xenibase@1")
	if !ok {
		t.Fatal("no xenibase")
	}
	o := c.Manifest.Auth.OAuth2
	o.Client, o.AccountField = "platformapp", "project_id"
	o.FieldsFromCallback = map[string]string{"api_url": "api_url"}
	t.Cleanup(func() { o.Client, o.AccountField, o.FieldsFromCallback = "", "", nil })
	// Linking asks the provider about a field from the callback before
	// saving it: the test action sees the credentials as they will be kept.
	// (The fake Xenibase is plain http, which the real test refuses.)
	var tested string
	real := c.Actions["test_connection"]
	c.Actions["test_connection"] = connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
		tested = req.Credentials["api_url"]
		return connector.Response{Output: map[string]any{}}, nil
	})
	t.Cleanup(func() { c.Actions["test_connection"] = real })
	// Registered under the app's name, not the connector's.
	w.srv.OAuthClients = map[string]api.OAuthClient{"platformapp": {ID: "xbapp_taskiem", Secret: "xbc_secret"}}
	owner := w.tenant(t, "Acme", "owner@acme.test")

	q := w.start(t, owner, "main")
	w.fake.AddOAuthCode(xenibasetest.OAuthCode{Code: "code-app", Challenge: q.Get("code_challenge"), Scopes: []string{"read", "write"}, ProjectIDs: []string{oauthProject}})
	back := w.callback(t, url.Values{"code": {"code-app"}, "state": {q.Get("state")}, "api_url": {"https://shop.data.example"}})
	id := back.Get("linked")
	if id == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}
	if tested != "https://shop.data.example" {
		t.Errorf("the provider was not asked about the callback's field: %q", tested)
	}
	var client, account string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT client, account_key FROM connection_app_accounts WHERE connection_id = $1`, uuid.MustParse(id)).Scan(&client, &account); err != nil || client != "platformapp" || account != oauthProject {
		t.Errorf("the account recorded: %q %q %v", client, account, err)
	}
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id FROM connections WHERE id = $1`, uuid.MustParse(id)).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	creds, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "xenibase", "main")
	if err != nil || creds["api_url"] != "https://shop.data.example" {
		t.Errorf("a field from the callback: %v %v", creds, err)
	}
	// Deleting the connection removes its account with it.
	owner.must(204, "DELETE", "/v1/connections/"+id+"?force=true", nil)
	var left int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM connection_app_accounts WHERE connection_id = $1`, uuid.MustParse(id)).Scan(&left); err != nil || left != 0 {
		t.Errorf("after delete: %d %v", left, err)
	}
}

// Where the provider names the account's server on the callback (Zoho's
// accounts-server), the code is exchanged there only when it is one of the
// provider's own servers: anything else is refused before the client
// secret or the code goes anywhere (connector/v1 rule 19).
func TestOAuthCallbackServerMustBeTheProviders(t *testing.T) {
	w := newOAuthWorld(t)
	c, _ := w.env.Registry.Get("xenibase@1")
	o, m := c.Manifest.Auth.OAuth2, c.Manifest
	token, hosts := o.TokenURL, m.ConnectionHosts
	o.TokenURL, m.ConnectionHosts = "${connection.api_url}/api/v1/oauth/token", []string{"*.xenibase.example"}
	o.FieldsFromCallback = map[string]string{"api_url": "accounts-server"}
	t.Cleanup(func() { o.TokenURL, m.ConnectionHosts, o.FieldsFromCallback = token, hosts, nil })
	owner := w.tenant(t, "Acme", "owner@acme.test")

	q := w.start(t, owner, "main")
	back := w.callback(t, url.Values{"code": {"code-x"}, "state": {q.Get("state")}, "accounts-server": {"https://accounts.evil.example"}})
	if back.Get("linked") != "" || !strings.Contains(back.Get("error"), "naming a server Taskiem does not use") {
		t.Errorf("a server not the provider's: %v", back)
	}
}
