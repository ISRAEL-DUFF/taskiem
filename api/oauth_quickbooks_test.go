package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/quickbooks"
)

// Connecting QuickBooks (K3; connector/v1 rule 20): the person chooses a
// sandbox company, so linking uses Taskiem's development keys; Intuit
// takes the client in HTTP Basic at the token URL, sends the company back
// as realmId, and takes a JSON body at the revoke URL.
func TestQuickBooksLinksInItsMode(t *testing.T) {
	w := newWorld(t)
	var mu sync.Mutex
	var exchanged url.Values
	var basicID, basicSecret, revokeType, revokeBody, revokeID string
	intuit := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/token":
			exchanged, _ = url.ParseQuery(string(body))
			basicID, basicSecret, _ = r.BasicAuth()
			_, _ = io.WriteString(rw, `{"token_type":"bearer","access_token":"eyJ-access-1","refresh_token":"AB11-refresh-1","expires_in":3600,"x_refresh_token_expires_in":8726400}`)
		case "/revoke":
			revokeType, revokeBody = r.Header.Get("Content-Type"), string(body)
			revokeID, _, _ = r.BasicAuth()
		default:
			http.NotFound(rw, r)
		}
	}))
	defer intuit.Close()
	c := quickbooks.New(quickbooks.Options{TestURL: intuit.URL})
	c.Manifest.Auth.OAuth2.TokenURL = intuit.URL + "/token"
	c.Manifest.Auth.OAuth2.RevokeURL = intuit.URL + "/revoke"
	c.Manifest.Egress = append(c.Manifest.Egress, "127.0.0.1")
	if err := w.env.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	w.srv.PublicURL = w.base
	// Only the development keys are registered here (a test server).
	w.srv.OAuthClients = map[string]api.OAuthClient{"quickbooks_sandbox": {ID: "ABdevclient", Secret: "dev-secret"}}
	owner := w.tenant(t, "Ledger Ltd", "owner@ledger.test")
	tenant := tenantID(t, owner)

	for _, x := range owner.must(200, "GET", "/v1/connectors", nil)["connectors"].([]any) {
		if x := x.(map[string]any); x["ref"] == "quickbooks@1" {
			if modes, _ := x["oauth_modes"].([]any); x["oauth_linkable"] != true || len(modes) != 1 || modes[0] != "test" {
				t.Fatalf("linkable %v in %v", x["oauth_linkable"], x["oauth_modes"])
			}
		}
	}
	// Live needs the production keys, which this Taskiem lacks.
	if st, out := owner.do("POST", "/v1/connections/oauth/start", map[string]any{"connector": "quickbooks@1", "mode": "live"}); st != 503 || out["code"] != "oauth_unavailable" ||
		!strings.Contains(out["error"].(string), "TASKIEM_OAUTH_QUICKBOOKS_CLIENT_ID") {
		t.Fatalf("live: %d %v", st, out)
	}
	ow := &oauthWorld{world: w}
	q := ow.startFor(t, owner, map[string]any{"connector": "quickbooks@1", "environment": "prod", "mode": "test"})
	if q.Get("client_id") != "ABdevclient" || q.Get("scope") != "com.intuit.quickbooks.accounting" {
		t.Fatalf("consent query %v", q)
	}
	back := ow.callback(t, url.Values{"code": {"intuit-code-1"}, "state": {q.Get("state")}, "realmId": {"9341453050298730"}})
	id := back.Get("linked")
	if id == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}
	mu.Lock()
	if basicID != "ABdevclient" || basicSecret != "dev-secret" || exchanged.Get("client_id") != "" || exchanged.Get("client_secret") != "" || exchanged.Get("code") != "intuit-code-1" {
		t.Errorf("exchange: basic %q/%q, form %v", basicID, basicSecret, exchanged)
	}
	mu.Unlock()

	var listed map[string]any
	for _, x := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if x := x.(map[string]any); x["id"] == id {
			listed = x
		}
	}
	if listed == nil || listed["mode"] != "test" || listed["expires_at"] != nil {
		t.Fatalf("listed %v", listed)
	}
	creds, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "quickbooks", listed["name"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if creds["realm_id"] != "9341453050298730" || creds["refresh_token"] != "AB11-refresh-1" || creds["client_id"] != "ABdevclient" || creds["client_secret"] != "dev-secret" || creds["$mode"] != "test" {
		t.Fatalf("credentials %v", creds)
	}
	var client string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT client FROM connection_app_accounts WHERE account_key = '9341453050298730'`).Scan(&client); err != nil || client != "quickbooks_sandbox" {
		t.Fatalf("app account client %q: %v", client, err)
	}

	// Deleting gives the refresh token back, as JSON, with the same keys.
	owner.must(204, "DELETE", "/v1/connections/"+id, nil)
	w.srv.WaitBackground()
	mu.Lock()
	defer mu.Unlock()
	var sent map[string]string
	if revokeType != "application/json" || json.Unmarshal([]byte(revokeBody), &sent) != nil || sent["token"] != "AB11-refresh-1" || revokeID != "ABdevclient" {
		t.Errorf("revoke: %s %s as %s", revokeType, revokeBody, revokeID)
	}
}
