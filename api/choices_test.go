package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/mailchimp"
)

// A template's audience is chosen by name (K8; a connector's pickers):
// GET /v1/connections/choices runs the picker on the connection.
func TestConnectionChoices(t *testing.T) {
	w := newWorld(t)
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mctoken" || r.URL.Path != "/lists" {
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(rw, `{"title":"API Key Invalid","status":401}`)
			return
		}
		_, _ = io.WriteString(rw, `{"lists":[{"id":"a6b5da1054","name":"Newsletter","stats":{"member_count":118}}]}`)
	}))
	defer fake.Close()
	if err := w.env.Registry.Register(mailchimp.New(mailchimp.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Cakes", "owner@adacakes.test")
	if _, err := w.env.Vault.CreateConnection(context.Background(), tenantID(t, owner), "prod", "mailchimp", "main", "api_key", map[string]string{"access_token": "mctoken"}, "test"); err != nil {
		t.Fatal(err)
	}
	out := owner.must(200, "GET", "/v1/connections/choices?connector=mailchimp&picker=audiences", nil)
	choices, _ := out["choices"].([]any)
	if len(choices) != 1 || choices[0].(map[string]any)["id"] != "a6b5da1054" || choices[0].(map[string]any)["label"] != "Newsletter (118 subscribed)" {
		t.Fatalf("choices %v", out)
	}
	owner.must(400, "GET", "/v1/connections/choices?connector=mailchimp&picker=nope", nil)
	owner.must(409, "GET", "/v1/connections/choices?connector=mailchimp&picker=audiences&connection=other", nil)
}

// Connecting Mailchimp (K8): no scope is asked for, and the account's
// server comes from Mailchimp's metadata before the connection is saved.
func TestMailchimpLinksWithItsServer(t *testing.T) {
	w := newWorld(t)
	login := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_, _ = io.WriteString(rw, `{"access_token":"mc-granted","expires_in":0,"scope":null}`)
		case "/oauth2/metadata":
			if r.Header.Get("Authorization") != "OAuth mc-granted" {
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(rw, `{"dc":"us6","accountname":"Ada Cakes","api_endpoint":"https://us6.api.mailchimp.com"}`)
		default:
			http.NotFound(rw, r)
		}
	}))
	defer login.Close()
	c := mailchimp.New(mailchimp.Options{MetadataURL: login.URL + "/oauth2/metadata"})
	c.Manifest.Auth.OAuth2.TokenURL = login.URL + "/oauth2/token"
	if err := w.env.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"mailchimp": {ID: "123456789012", Secret: "mc-secret"}}
	owner := w.tenant(t, "Ada Cakes", "owner@adacakes.test")
	ow := &oauthWorld{world: w}
	q := ow.startFor(t, owner, map[string]any{"connector": "mailchimp@1", "environment": "prod"})
	if _, asked := q["scope"]; asked || q.Get("client_id") != "123456789012" {
		t.Fatalf("consent query %v", q)
	}
	back := ow.callback(t, url.Values{"code": {"mc-code"}, "state": {q.Get("state")}})
	if back.Get("linked") == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}
	creds, err := w.env.Vault.Credentials(context.Background(), tenantID(t, owner), "prod", "mailchimp", "main")
	if err != nil || creds["access_token"] != "mc-granted" || creds["server"] != "us6" || creds["account_name"] != "Ada Cakes" {
		t.Fatalf("credentials %v: %v", creds, err)
	}
}
