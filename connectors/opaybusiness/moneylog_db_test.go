package opaybusiness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// An OPay sale reaches the money log: the delivery is decrypted before it
// is read as pos.sale, with no workflow listening.
func TestSalesReachTheMoneyLog(t *testing.T) {
	ctx := context.Background()
	e := rt.New(t)
	if err := e.Registry.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "opay_business", "main", "api_key", creds(t), "test"); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry, Egress: &egress.Guard{},
		MoneyLog: &moneylog.Log{Pool: e.DB.App, PII: e.Vault}}
	srv := httptest.NewServer(http.StripPrefix("/hooks", h))
	t.Cleanup(srv.Close)
	body := delivery(t, docWebhook, time.Now(), creds(t)["client_auth_key"], opayKey)
	for range 2 {
		res, err := http.Post(srv.URL+"/hooks/"+e.Tenant.String()+"/connectors/opay_business@1/payment", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 202 && res.StatusCode != 200 {
			t.Fatalf("delivery: %d %s", res.StatusCode, out)
		}
	}
	var n int
	var kind, status, ref string
	var amount, fee int64
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) OVER (), kind, status, reference, amount_kobo, fee_kobo FROM money_entries`).Scan(&n, &kind, &status, &ref, &amount, &fee)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 || kind != "pos_sale" || status != "settled" || ref != "2334345345345245" || amount != 30000 || fee != 150 {
		t.Errorf("logged %d: %s %s %s %d %d", n, kind, status, ref, amount, fee)
	}
}

// saleWorld is a tenant with an OPay connection in prod, a workflow on
// the payment trigger and the ingest endpoint; post delivers a body to it
// with an X-Opay-Tranid header (none when empty), and runs counts the
// workflow's runs.
func saleWorld(t *testing.T) (post func(body []byte, tranid string), runs func() int) {
	t.Helper()
	ctx := context.Background()
	e := rt.New(t)
	if err := e.Registry.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "opay_business", "main", "api_key", creds(t), "test"); err != nil {
		t.Fatal(err)
	}
	const doc = `{"schema":"wd/v1","id":"wf_sale","version":1,"name":"sale",
  "trigger":{"type":"connector_event","config":{"connector":"opay_business@1","trigger":"payment"}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.orderNo"}}]}`
	wf := e.PublishIn(t, e.Tenant, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return ingest.Sync(ctx, tx, e.Tenant, wf, 1, def, e.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry, Egress: &egress.Guard{},
		MoneyLog: &moneylog.Log{Pool: e.DB.App, PII: e.Vault}}
	srv := httptest.NewServer(http.StripPrefix("/hooks", h))
	t.Cleanup(srv.Close)
	post = func(body []byte, tranid string) {
		t.Helper()
		req, err := http.NewRequest("POST", srv.URL+"/hooks/"+e.Tenant.String()+"/connectors/opay_business@1/payment?env=prod", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if tranid != "" {
			req.Header.Set("X-Opay-Tranid", tranid)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 202 {
			t.Fatalf("delivery %q: %d %s", tranid, res.StatusCode, out)
		}
	}
	runs = func() int {
		t.Helper()
		var n int
		if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return post, runs
}

// A captured sale notification posted again with another X-Opay-Tranid
// header (which OPay does not sign) is the same sale: its workflows run
// once (F54).
func TestReplayWithAnotherTranidStartsNoRun(t *testing.T) {
	post, runs := saleWorld(t)
	body := delivery(t, docWebhook, time.Now(), creds(t)["client_auth_key"], opayKey)
	for _, tranid := range []string{"TRAN-1", "TRAN-1", "attacker-2", ""} {
		post(body, tranid)
	}
	if n := runs(); n != 1 {
		t.Errorf("one signed body posted four times started %d runs, want 1", n)
	}
}

// OPay retries an unanswered notification at growing intervals, and one
// sent after five minutes needs a new timestamp, so a new encryption and
// signature: the same sale, re-signed, still starts its workflows once.
// The same payment in another status is news (F54b).
func TestResignedRetryStartsNoRun(t *testing.T) {
	post, runs := saleWorld(t)
	key := creds(t)["client_auth_key"]
	post(delivery(t, docWebhook, time.Now().Add(-4*time.Minute), key, opayKey), "TRAN-1")
	post(delivery(t, docWebhook, time.Now(), key, opayKey), "TRAN-1")
	if n := runs(); n != 1 {
		t.Fatalf("a sale and its re-signed retry started %d runs, want 1", n)
	}
	post(delivery(t, strings.Replace(docWebhook, `"status":"SUCCESS"`, `"status":"CLOSE"`, 1), time.Now(), key, opayKey), "TRAN-1")
	if n := runs(); n != 2 {
		t.Fatalf("the same payment in another status started %d runs in all, want 2", n)
	}
}
