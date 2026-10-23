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
