package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/googlesheets"
	"github.com/israel-duff/taskiem/connectors/paystack"
)

// fakePaystack accepts one secret key and refuses any other.
func fakePaystack(t *testing.T, good string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+good {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":false,"message":"Invalid key"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"message":"Balances retrieved","data":[{"currency":"NGN","balance":100000}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeGoogle is Google's token endpoint and the Sheets API, for one
// spreadsheet.
func fakeGoogle(t *testing.T, sheet string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"ya29.fake","expires_in":3599,"token_type":"Bearer"}`))
		case strings.HasPrefix(r.URL.Path, "/spreadsheets/"+sheet):
			_, _ = w.Write([]byte(`{"spreadsheetId":"` + sheet + `","properties":{"title":"Shop sales"},"sheets":[{"properties":{"sheetId":0,"title":"Sales","index":0}},{"properties":{"sheetId":1,"title":"Stock","index":1}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Connecting without technical steps (no-technical-steps N2): one name,
// a pasted key checked by its start and by the service before it is
// saved, whether it is live or test, a spreadsheet from its link, and
// Google linked in one click.
func TestGuidedConnections(t *testing.T) {
	w := newWorld(t)
	ps := fakePaystack(t, "sk_test_good")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	const sheet = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	g := fakeGoogle(t, sheet)
	if err := w.env.Registry.Register(googlesheets.New(googlesheets.Options{BaseURL: g.URL, TokenURL: g.URL + "/token"})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	paystackConn := func(status int, mode, key string, check bool) map[string]any {
		t.Helper()
		return owner.must(status, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "mode": mode, "check": check,
			"credentials": map[string]string{"secret_key": key}})
	}

	// A key pasted wrong gets plain words, before anything is asked of
	// Paystack or saved.
	if e := paystackConn(400, "live", "pk_live_abc", false)["error"].(string); !strings.Contains(e, "That is your public key") {
		t.Errorf("a public key: %q", e)
	}
	if e := paystackConn(400, "live", "sk_test_good", false)["error"].(string); !strings.Contains(e, "that is a test (practice) secret key, but you chose live (real money)") {
		t.Errorf("a test key for live: %q", e)
	}
	if e := paystackConn(400, "test", "hello", false)["error"].(string); !strings.Contains(e, "it starts with sk_test_") {
		t.Errorf("not a key: %q", e)
	}
	// One from somewhere else (Paystack refuses it).
	if e := paystackConn(400, "test", "sk_test_other", true)["error"].(string); !strings.Contains(e, "Paystack did not accept these details") {
		t.Errorf("a key Paystack refuses: %q", e)
	}
	if out := owner.must(400, "POST", "/v1/connections/check", map[string]any{"connector": "paystack@1", "mode": "test", "credentials": map[string]string{"secret_key": "sk_test_other"}}); !strings.Contains(out["error"].(string), "did not accept") {
		t.Errorf("check: %v", out)
	}
	if out := owner.must(200, "POST", "/v1/connections/check", map[string]any{"connector": "paystack@1", "mode": "test", "credentials": map[string]string{"secret_key": "sk_test_good"}}); out["ok"] != true {
		t.Errorf("check: %v", out)
	}

	// One name: unnamed, the first is main and the next main_2; the test
	// key is recorded as test.
	if c := paystackConn(201, "test", "sk_test_good", true); c["name"] != "main" {
		t.Fatalf("the first connection: %v", c)
	}
	if c := paystackConn(201, "", "sk_test_good", false); c["name"] != "main_2" {
		t.Fatalf("the second connection: %v", c)
	}
	names := owner.must(200, "GET", "/v1/connection-names?connector=paystack@1", nil)["connections"].([]any)
	if len(names) != 2 || names[0].(map[string]any)["name"] != "main" || names[0].(map[string]any)["mode"] != "test" {
		t.Fatalf("connection names: %v", names)
	}
	// A builder without connection.manage still sees names for pickers.
	builder := addMember(t, w, owner, "bo", "builder")
	if l := builder.must(200, "GET", "/v1/connection-names", nil)["connections"].([]any); len(l) != 2 || strings.Contains(toJSON(l), "sk_test") {
		t.Errorf("names for a builder: %v", l)
	}

	// A spreadsheet from its link: its title and tabs, with the business's
	// Google Sheets connection.
	owner.must(409, "POST", "/v1/sheets/resolve", map[string]any{"link": "https://docs.google.com/spreadsheets/d/" + sheet + "/edit#gid=0"})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "googlesheets@1",
		"credentials": map[string]string{"client_id": "app.apps.googleusercontent.com", "client_secret": "s", "refresh_token": "1//r"}})
	got := owner.must(200, "POST", "/v1/sheets/resolve", map[string]any{"link": "https://docs.google.com/spreadsheets/d/" + sheet + "/edit#gid=0"})
	if got["spreadsheet_id"] != sheet || got["title"] != "Shop sales" || toJSON(got["tabs"]) != `["Sales","Stock"]` {
		t.Fatalf("resolve: %v", got)
	}
	if e := owner.must(400, "POST", "/v1/sheets/resolve", map[string]any{"link": "my sales sheet"})["error"].(string); !strings.Contains(e, "not a Google Sheets link") {
		t.Errorf("not a link: %q", e)
	}
	if e := owner.must(400, "POST", "/v1/sheets/resolve", map[string]any{"link": "https://docs.google.com/spreadsheets/d/1ZZZZZZZZZZZZZZZZZZZZZZZZZZZZ/edit"})["error"].(string); !strings.Contains(e, "cannot open that sheet") {
		t.Errorf("a sheet it cannot open: %q", e)
	}
	builder.must(200, "POST", "/v1/sheets/resolve", map[string]any{"link": sheet})

	// Google in one click: the consent URL asks for a refresh token and
	// the Sheets and Drive-file scopes, with Taskiem's client.
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"googlesheets": {ID: "app.apps.googleusercontent.com", Secret: "GOCSPX-s"}}
	info := owner.must(200, "GET", "/v1/connectors", nil)
	if !strings.Contains(toJSON(info), `"oauth_linkable":true`) {
		t.Errorf("Google Sheets is not linkable: %s", toJSON(info))
	}
	raw, _ := json.Marshal(map[string]any{"connector": "googlesheets@1", "environment": "prod"})
	req, _ := http.NewRequest("POST", w.base+"/v1/connections/oauth/start", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+owner.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var start map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&start)
	_ = resp.Body.Close()
	u, err := url.Parse(start["url"].(string))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("start: %d %v", resp.StatusCode, start)
	}
	q := u.Query()
	if u.Host != "accounts.google.com" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("client_id") != "app.apps.googleusercontent.com" ||
		!strings.Contains(q.Get("scope"), "auth/spreadsheets") || !strings.Contains(q.Get("scope"), "auth/drive.file") || q.Get("code_challenge_method") != "S256" {
		t.Errorf("the consent URL: %s", u)
	}
}
