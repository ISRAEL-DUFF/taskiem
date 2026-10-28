package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/iswallet"
)

// fakeISWallet lists the partners issuing account numbers for one key.
func fakeISWallet(t *testing.T, good string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "req_check")
		if r.Header.Get("Authorization") != "Bearer "+good {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"invalid api key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"providers":[{"code":"flutterwave","currencies":["NGN"],"virtual_accounts":{"permanent":true,"dynamic":true}},
			{"code":"ghana_partner","currencies":["GHS"],"virtual_accounts":{"permanent":true,"dynamic":false}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A saved connection is checked with its own details: the key works, and
// which partners can issue account numbers; a key the service refuses is
// said in plain words with its request id.
func TestCheckSavedConnection(t *testing.T) {
	w := newWorld(t)
	srv := fakeISWallet(t, "isw_good")
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{BaseURL: srv.URL, TestURL: srv.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Greenfield Academy", "bursar@greenfield.test")
	good := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_good"}})
	out := owner.must(200, "POST", "/v1/connections/"+good["id"].(string)+"/check", nil)
	if out["ok"] != true || len(out["partners"].([]any)) != 1 || out["partners"].([]any)[0] != "flutterwave" {
		t.Errorf("a good key: %v", out)
	}
	bad := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "old", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_revoked"}})
	out = owner.must(200, "POST", "/v1/connections/"+bad["id"].(string)+"/check", nil)
	if msg, _ := out["message"].(string); out["ok"] != false || !strings.Contains(msg, "did not accept") || !strings.Contains(msg, "request req_check") || strings.Contains(msg, "fatal") {
		t.Errorf("a refused key: %v", out)
	}
	owner.must(404, "POST", "/v1/connections/0192e2e0-0000-7000-8000-000000000009/check", nil)
}
