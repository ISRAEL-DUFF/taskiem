package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
)

// Live and test as hosts (connector/v1 rule 17): the person picks one, a
// mode this Taskiem has no address for is refused, the connection keeps
// its mode, the check before saving reaches that mode's host, and the
// vault hands the mode out with the credentials without ever storing it.
func TestConnectionHostModes(t *testing.T) {
	w := newWorld(t)
	var hits []string
	test := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = rw.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(test.Close)
	// Before go-live: no live iswallet address; the test one is a fake.
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{TestURL: test.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")

	info := owner.must(200, "GET", "/v1/connectors", nil)
	if s := toJSON(info); !strings.Contains(s, `"modes":[{"label":"Live (real money)","mode":"live"},{"base_url":"`+test.URL+`","label":"Test (practice)","mode":"test"}]`) {
		i := strings.Index(s, `"modes"`)
		t.Errorf("iswallet's modes as listed: %s", s[max(i, 0):min(len(s), max(i, 0)+300)])
	}

	_, out := owner.do("POST", "/v1/connections", map[string]any{"connector": "iswallet@1", "environment": "prod", "credentials": map[string]string{"api_key": "isw_k"}, "mode": "live"})
	if !strings.Contains(toJSON(out), "iswallet live (real money) is not set up on this Taskiem yet") {
		t.Errorf("live before go-live: %v", out)
	}
	owner.must(400, "POST", "/v1/connections", map[string]any{"connector": "iswallet@1", "environment": "prod", "credentials": map[string]string{"api_key": "isw_k"}, "mode": "staging"})

	// Test, checked first against the test host.
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "iswallet@1", "environment": "prod", "credentials": map[string]string{"api_key": "isw_k"}, "mode": "test", "check": true})
	if strings.Join(hits, ",") != "/v1/banks" {
		t.Errorf("the check before saving reached %v", hits)
	}
	list := owner.must(200, "GET", "/v1/connections", nil)
	if !strings.Contains(toJSON(list), `"mode":"test"`) {
		t.Errorf("the connection's mode: %v", list)
	}

	// The vault hands the mode out, and a write back never stores it.
	var tenant, conn uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id, id FROM connections WHERE connector = 'iswallet'`).Scan(&tenant, &conn); err != nil {
		t.Fatal(err)
	}
	creds, err := w.env.Vault.Credentials(context.Background(), tenant, "prod", "iswallet", "")
	if err != nil || creds[connector.ModeKey] != "test" || creds["api_key"] != "isw_k" {
		t.Fatalf("credentials: %v %v", creds, err)
	}
	err = db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return w.env.Vault.ReplaceCredentialsTx(context.Background(), tx, tenant, conn, creds, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE connections SET mode = NULL WHERE id = $1`, conn); err != nil {
		t.Fatal(err)
	}
	if again, _ := w.env.Vault.Credentials(context.Background(), tenant, "prod", "iswallet", ""); again[connector.ModeKey] != "" || again["api_key"] != "isw_k" {
		t.Errorf("the mode was stored with the credentials: %v", again)
	}
}
