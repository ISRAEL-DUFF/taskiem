package moneylog_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

var ctx = context.Background()

// flaky fails the next insert after it reached Xenibase: the answer is
// lost, as on a dropped connection.
type flaky struct {
	moneylog.Rows
	failNext bool
}

func (f *flaky) Call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error) {
	out, err := f.Rows.Call(ctx, tenant, action, in, key)
	if action == "insert_rows" && f.failNext {
		f.failNext = false
		return nil, errors.New("connection reset")
	}
	return out, err
}

// The log is copied to the kept money_log table, each entry once, even
// when an insert's answer is lost; a business without data tables keeps
// its entries until it has them.
func TestMirrorCopiesEachEntryOnce(t *testing.T) {
	e := rt.New(t)
	x := xenibasetest.New(t)
	now := time.Now()
	x.Now = func() time.Time { return now }
	x.AddToken(xenibasetest.Token{Secret: "xb_platform", ID: "tok", Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: now.Add(10 * 365 * 24 * time.Hour)})
	if err := e.Registry.Register(xenibase.New(xenibase.Options{BaseURL: x.URL})); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tables := &datatables.Service{Pool: e.DB.App, Vault: e.Vault, Egress: e.Egress,
		Platform: datatables.Platform{URL: x.URL, Token: "xb_platform", Org: x.OrgID, Region: "lagos"}, PollInterval: time.Millisecond}
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	rows := &flaky{Rows: &moneylog.XenibaseRows{Registry: e.Registry, Vault: e.Vault, Guard: e.Egress}}
	m := &moneylog.Mirror{Log: log, Tables: tables, Rows: rows, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}

	for _, ev := range []struct {
		name   string
		fields map[string]any
	}{
		{"payment.received", map[string]any{"payment_id": "P-1", "amount_kobo": int64(250000)}},
		{"virtual_account.credited", map[string]any{"reference": "VA-1", "amount_kobo": int64(90000), "payer_name": "ADA OBI"}},
	} {
		if _, err := log.RecordEvent(ctx, e.Tenant, "prod", "paystack", "main", ev.name, ev.fields); err != nil {
			t.Fatal(err)
		}
	}
	rows.failNext = true
	if err := m.Tick(ctx); err != nil { // the first insert's answer is lost
		t.Fatal(err)
	}
	for range 2 {
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tbl := x.Table(projectOf(t, tables, e.Tenant), moneylog.Table)
	if tbl == nil {
		t.Fatal("no money_log table")
	}
	got, err := rows.Rows.Call(ctx, e.Tenant, "read_rows", map[string]any{"table": moneylog.Table, "limit": 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := got["rows"].([]any)
	if len(list) != 2 {
		t.Fatalf("money_log rows: %v", list)
	}
	var payer any
	for _, r := range list {
		row := r.(map[string]any)
		if row["reference"] == "VA-1" {
			payer = row["payer"]
		}
	}
	if payer != "ADA OBI" {
		t.Errorf("payer %v", payer)
	}
	var waiting int
	_ = db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE mirrored_at IS NULL`).Scan(&waiting)
	})
	if waiting != 0 {
		t.Errorf("%d entries not copied", waiting)
	}
	// The kept table refuses everyone else's writes.
	if kept, err := datatables.IsKept(ctx, e.DB.App, e.Tenant, moneylog.Table); err != nil || kept != moneylog.Table {
		t.Errorf("kept %q %v", kept, err)
	}
}

func projectOf(t *testing.T, s *datatables.Service, tenant uuid.UUID) string {
	t.Helper()
	st, err := s.Status(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return st.ProjectID
}
