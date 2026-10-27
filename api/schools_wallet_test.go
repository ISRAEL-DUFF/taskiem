package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/connectors/iswallet"
)

// The school's wallet is created from the Schools settings (I3): flat
// identity fields with the director's BVN sent once, the wallet-level
// number never shown, a second press answering the same wallet, and the
// students' numbers then paying into it.
func TestCreateSchoolWallet(t *testing.T) {
	w := newWorld(t)
	var mu sync.Mutex
	var bodies []map[string]any
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("X-Request-ID", "req_wallet")
		switch r.URL.Path {
		case "/v1/wallets":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			bodies, keys = append(bodies, b), append(keys, r.Header.Get("Idempotency-Key"))
			mu.Unlock()
			rw.WriteHeader(201)
			_, _ = rw.Write([]byte(`{"wallet_id":"5b00d9dd","account_number":"6256638601","internal_reference":"6256638601","tier":"TIER_1"}`))
		default:
			_, _ = rw.Write([]byte(`{"providers":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{BaseURL: srv.URL, TestURL: srv.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Greenfield Academy", "owner@greenfield.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_good"}})
	req := map[string]any{"connection": "iswallet/main", "business_email": "bursar@greenfield.test", "bvn": "22212345678"}
	owner.must(409, "POST", "/v1/settings/schools/wallet", req) // the Schools area is off
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	owner.must(400, "POST", "/v1/settings/schools/wallet", map[string]any{"connection": "iswallet/main", "business_email": "bursar@greenfield.test", "bvn": "123"})

	out := owner.must(201, "POST", "/v1/settings/schools/wallet", req)
	if out["wallet_id"] != "5b00d9dd" || out["issuing"].(map[string]any)["account"] != "5b00d9dd" || out["issuing"].(map[string]any)["connection"] != "iswallet/main" {
		t.Fatalf("created %v", out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "6256638601") || strings.Contains(string(raw), "22212345678") {
		t.Errorf("the wallet-level number or the BVN in the answer: %s", raw)
	}
	b := bodies[0]
	if b["owner_type"] != "client_end_user_business" || b["bvn"] != "22212345678" || b["business_email"] != "bursar@greenfield.test" ||
		b["business_name"] != "Greenfield Academy" || !strings.HasPrefix(b["owner_ref"].(string), "tenant:") || b["kyc_profile"] != nil {
		t.Errorf("sent %v", b)
	}
	settings := owner.must(200, "GET", "/v1/settings/schools", nil)
	if is, _ := settings["issuing"].(map[string]any); is["account"] != "5b00d9dd" {
		t.Errorf("settings %v", settings)
	}
	// A second press asks with the same key: iswallet answers the same wallet.
	owner.must(201, "POST", "/v1/settings/schools/wallet", req)
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Errorf("keys %v", keys)
	}
	// Owners only.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "admin@greenfield.test", "password": testPassword, "roles": []string{"admin"}})
	w.login(t, "admin@greenfield.test", testPassword).must(403, "POST", "/v1/settings/schools/wallet", req)
	// The audit never holds the BVN.
	var n int
	if err := w.env.DB.Admin.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'settings.schools.wallet_created' AND detail::text LIKE '%22212345678%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the BVN is in %d audit entries", n)
	}
}
