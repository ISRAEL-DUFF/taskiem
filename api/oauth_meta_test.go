package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/meta"
)

// Connecting a Facebook Page (K5; connector/v1 rule 21): Graph takes the
// code exchange as a GET, and the connector's Link makes the granted user
// token into the one Page's connection before it is saved: its own token,
// its id as the account, subscribed to Taskiem's app. The user token is
// not kept.
func TestMessengerLinksTheOnePage(t *testing.T) {
	w := newWorld(t)
	var mu sync.Mutex
	var exchange *url.URL
	var subscribed string
	pages := `{"data":[{"id":"104123456789012","name":"Ada's Cakes","access_token":"EAApage"}]}`
	graph := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/oauth/access_token" && r.URL.Query().Get("code") != "":
			exchange = r.URL
			if r.Method != http.MethodGet {
				rw.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_, _ = io.WriteString(rw, `{"access_token":"EAAshort","token_type":"bearer","expires_in":5400}`)
		case r.URL.Path == "/oauth/access_token":
			_, _ = io.WriteString(rw, `{"access_token":"EAAlong","token_type":"bearer","expires_in":5183944}`)
		case r.URL.Path == "/me/accounts":
			_, _ = io.WriteString(rw, pages)
		case strings.HasSuffix(r.URL.Path, "/subscribed_apps"):
			subscribed = r.URL.Path + " " + r.Header.Get("Authorization")
			_, _ = io.WriteString(rw, `{"success":true}`)
		default:
			http.NotFound(rw, r)
		}
	}))
	defer graph.Close()
	c := meta.Messenger(meta.Options{BaseURL: graph.URL})
	c.Manifest.Auth.OAuth2.TokenURL = graph.URL + "/oauth/access_token"
	if err := w.env.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"meta": {ID: "123456789", Secret: "app-secret"}}
	owner := w.tenant(t, "Ada Cakes", "owner@adacakes.test")
	tenant := tenantID(t, owner)

	ow := &oauthWorld{world: w}
	q := ow.startFor(t, owner, map[string]any{"connector": "messenger@1", "environment": "prod"})
	if q.Get("client_id") != "123456789" || q.Get("scope") != "pages_show_list,pages_messaging,pages_manage_metadata,pages_read_engagement" {
		t.Fatalf("consent query %v", q)
	}
	back := ow.callback(t, url.Values{"code": {"fb-code-1"}, "state": {q.Get("state")}})
	if back.Get("linked") == "" {
		t.Fatalf("not linked: %s", back.Get("error"))
	}
	mu.Lock()
	if exchange == nil || exchange.Query().Get("client_secret") != "app-secret" || exchange.Query().Get("redirect_uri") != w.base+"/v1/connections/oauth/callback" {
		t.Errorf("exchange %v", exchange)
	}
	if subscribed != "/104123456789012/subscribed_apps Bearer EAApage" {
		t.Errorf("subscribed %q", subscribed)
	}
	mu.Unlock()
	var listed map[string]any
	for _, x := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if x := x.(map[string]any); x["id"] == back.Get("linked") {
			listed = x
		}
	}
	if listed == nil || listed["expires_at"] != nil {
		t.Fatalf("listed %v: a Page token does not expire", listed)
	}
	creds, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "messenger", listed["name"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if creds["page_id"] != "104123456789012" || creds["page_token"] != "EAApage" || creds["page_name"] != "Ada's Cakes" || strings.Contains(strings.Join(values(creds), " "), "EAAshort") || strings.Contains(strings.Join(values(creds), " "), "EAAlong") {
		t.Fatalf("credentials %v", creds)
	}
	var client string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT client FROM connection_app_accounts WHERE account_key = '104123456789012'`).Scan(&client); err != nil || client != "meta" {
		t.Fatalf("app account %q: %v", client, err)
	}

	// Two Pages granted: nothing is saved, and the person is told why.
	mu.Lock()
	pages = `{"data":[{"id":"1","name":"A","access_token":"x"},{"id":"2","name":"B","access_token":"y"}]}`
	mu.Unlock()
	q = ow.startFor(t, owner, map[string]any{"connector": "messenger@1", "environment": "prod", "name": "second"})
	back = ow.callback(t, url.Values{"code": {"fb-code-2"}, "state": {q.Get("state")}})
	if back.Get("linked") != "" || !strings.Contains(back.Get("error"), "choose exactly one") {
		t.Fatalf("two Pages: %v", back)
	}
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
