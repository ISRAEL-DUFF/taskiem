package ingest_test

import (
	"crypto/sha512"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/zohobooks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

const onChargeFlow = `{"schema":"wd/v1","id":"wf_thanks","version":1,"name":"thanks",
  "trigger":{"type":"connector_event","config":{"connector":"paystack@1","trigger":"charge_event","events":["charge.success"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.reference"}}]}`

// A business that never set Paystack's webhook up still has its payments
// noticed (no-technical-steps N4): the poller asks Paystack from where it
// last looked, and each payment is delivered as Paystack would have sent
// it; a payment the webhook also delivered starts nothing twice.
func TestPollerDeliversPaystackPayments(t *testing.T) {
	w := newWorld(t)
	var asked atomic.Int64
	var from atomic.Value
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_x" || r.URL.Path != "/transaction" || r.URL.Query().Get("status") != "success" {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked.Add(1)
		from.Store(r.URL.Query().Get("from"))
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"status":true,"data":[{"reference":"P-1","amount":500000,"customer":{"email":"bola@example.com"}},{"reference":"P-2","amount":250000}]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(paystack.New(paystack.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, onChargeFlow)
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}

	// The first look only marks where to start: nothing before is replayed.
	if n, err := p.Tick(ctx); err != nil || n != 0 || asked.Load() != 0 {
		t.Fatalf("first tick: %d %v (asked %d)", n, err, asked.Load())
	}
	// Five minutes on: it asks from then (less the overlap), and both
	// payments start the workflow.
	now = now.Add(6 * time.Minute)
	if n, err := p.Tick(ctx); err != nil || n != 2 {
		t.Fatalf("second tick: %d %v", n, err)
	}
	if f, _ := from.Load().(string); f == "" || !strings.HasSuffix(f, "Z") {
		t.Errorf("asked from %q", f)
	}
	if n := w.runCount(t, wf); n != 2 {
		t.Fatalf("runs: %d", n)
	}
	// Not due yet: nothing is asked.
	if n, _ := p.Tick(ctx); n != 0 || asked.Load() != 1 {
		t.Errorf("tick before due: %d (asked %d)", n, asked.Load())
	}
	// The webhook delivers P-1 too, and the next poll lists both again:
	// nothing starts twice.
	body := []byte(`{"data":{"amount":500000,"customer":{"email":"bola@example.com"},"reference":"P-1"},"event":"charge.success"}`)
	if st, out := w.post(t, "/connectors/paystack@1/charge_event", body, "x-paystack-signature", sign(sha512.New, "sk_test_x", body)); st != 202 || out["duplicate"] != true {
		t.Errorf("the webhook after the poll: %d %v", st, out)
	}
	now = now.Add(6 * time.Minute)
	if _, err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.runCount(t, wf); n != 2 {
		t.Errorf("runs after polling again: %d", n)
	}
	// Where it got to is recorded.
	var errText *string
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_error FROM trigger_polls WHERE connector = 'paystack@1' AND trigger_name = 'charge_event'`).Scan(&errText)
	})
	if err != nil || errText != nil {
		t.Errorf("poll state: %v %v", errText, err)
	}
}

// The money log's own intake (spec 7.3): with no workflow at all, a
// connected Paystack is still asked about its payments, and each one is
// logged once.
func TestPollerFeedsTheMoneyLog(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/transaction" {
			_, _ = rw.Write([]byte(`{"status":true,"data":[{"reference":"P-7","amount":120000}]}`))
			return
		}
		_, _ = rw.Write([]byte(`{"status":true,"data":[]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(paystack.New(paystack.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h, MoneyLog: true,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 3 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(6 * time.Minute)
	}
	got := w.moneyLog(t)
	if len(got) != 1 || got[0].reference != "P-7" || got[0].kind != "payment" {
		t.Fatalf("logged %+v", got)
	}
	// Without the log's intake, nothing is asked: no workflow wants it.
	w2 := newWorld(t)
	p2 := &ingest.Poller{Store: w2.Store, Registry: w2.Registry, Connections: w2.Vault, Hooks: w2.h}
	if n, err := p2.Tick(ctx); err != nil || n != 0 {
		t.Errorf("no targets: %d %v", n, err)
	}
}

// The money log's intake polls money events only: a connected Zoho Books
// (books events, records of money) is not asked anything, which would
// spend its organisation's daily quota, until a workflow listens.
func TestPollerLeavesTheBooksToWorkflows(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	var asked atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"code":0,"message":"success","invoices":[],"customerpayments":[]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(zohobooks.New(zohobooks.Options{BaseURL: fake.URL, AccountsServer: fake.URL})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"refresh_token": "1000.r", "client_id": "1000.c", "client_secret": "s", "accounts_server": "https://accounts.zoho.com", "api_domain": "https://www.zohoapis.com",
		"organization_id": "1", "access_token": "1000.a", "access_expires_at": "2099-01-01T00:00:00Z"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "zoho_books", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h, MoneyLog: true,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 3 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(16 * time.Minute)
	}
	if asked.Load() != 0 {
		t.Errorf("asked Zoho %d times", asked.Load())
	}
}

// Polling Zoho Books with an expired access token refreshes it and keeps
// it, as a workflow step would (RotateCredentials), and the change it finds
// starts the workflow.
func TestPollerRefreshesZohoTokens(t *testing.T) {
	w := newWorld(t)
	var issued atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/v2/token":
			issued.Add(1)
			_, _ = rw.Write([]byte(`{"access_token":"1000.fresh","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Zoho-oauthtoken 1000.fresh":
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = rw.Write([]byte(`{"code":57,"message":"You are not authorized to perform this operation"}`))
		case r.URL.Path == "/invoices":
			_, _ = rw.Write([]byte(`{"code":0,"message":"success","invoices":[{"invoice_id":"460000000027017","invoice_number":"INV-000042","status":"paid","total":53750,"balance":0,"created_time":"2026-10-20T10:00:00+0100","last_modified_time":"2026-10-26T11:00:00+0100"}]}`))
		default:
			_, _ = rw.Write([]byte(`{"code":0,"message":"success","customerpayments":[]}`))
		}
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(zohobooks.New(zohobooks.Options{BaseURL: fake.URL, AccountsServer: fake.URL})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"refresh_token": "1000.r", "client_id": "1000.c", "client_secret": "s", "accounts_server": "https://accounts.zoho.com", "api_domain": "https://www.zohoapis.com",
		"organization_id": "1", "access_token": "1000.expired", "access_expires_at": "2020-01-01T00:00:00Z"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "zoho_books", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, `{"schema":"wd/v1","id":"wf_paid","version":1,"name":"paid","trigger":{"type":"connector_event","config":{"connector":"zoho_books@1","trigger":"changes","events":["invoice.paid"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.id"}}]}`)
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 2 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(16 * time.Minute)
	}
	stored, err := w.Vault.Credentials(ctx, w.Tenant, "prod", "zoho_books", "main")
	if err != nil || stored["access_token"] != "1000.fresh" || issued.Load() != 1 {
		t.Fatalf("token %q, issued %d: %v", stored["access_token"], issued.Load(), err)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("runs: %d", n)
	}
}
