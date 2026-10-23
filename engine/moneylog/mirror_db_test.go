package moneylog_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
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
	// A match is copied to money_matches, and its undoing as its own row.
	inv := &invoicing.Invoices{Pool: e.DB.App, Vault: e.Vault, PageBase: "https://forms.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: e.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	var bill invoicing.Invoice
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		var err error
		bill, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Ada", Phone: "+2348031234567"}, AmountKobo: 250000}, "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Send(ctx, e.Tenant, bill.Number, "user:ada", "k"); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		y, _, err := inv.RecordPayment(ctx, tx, e.Tenant, bill.Number, invoicing.PaymentIn{AmountKobo: 250000, PaymentID: "P-1", How: "manual", By: "user:ada"})
		if err != nil {
			return err
		}
		_, err = inv.UndoPayment(ctx, tx, e.Tenant, bill.Number, y.Payments[0].ID, "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A storefront order's match is copied as order:<reference>.
	for _, q := range []string{
		`INSERT INTO storefronts (id, tenant_id, slug, draft, created_by) VALUES (gen_random_uuid(), $1, 'mirror-shop', '{}', 'user:ada')`,
		`INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, total_kobo, payment, paid_at)
			SELECT gen_random_uuid(), $1, s.id, 'order', 'AK-MIRROR1', '\x00', 'received', '{}', 'x', 250000, 'none', now() FROM storefronts s WHERE s.tenant_id = $1`,
		`INSERT INTO storefront_order_payments (id, tenant_id, order_id, entry_id, amount_kobo, how, by)
			SELECT gen_random_uuid(), $1, o.id, (SELECT id FROM money_entries WHERE tenant_id = $1 AND reference = 'VA-1' LIMIT 1), 250000, 'manual', 'user:ada'
			FROM storefront_orders o WHERE o.reference = 'AK-MIRROR1'`,
	} {
		if _, err := e.DB.Admin.Exec(ctx, q, e.Tenant); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err = rows.Rows.Call(ctx, e.Tenant, "read_rows", map[string]any{"table": moneylog.MatchesTable, "limit": 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	list, _ = got["rows"].([]any)
	if len(list) != 3 || list[0].(map[string]any)["target"] != "invoice:"+bill.Number || list[2].(map[string]any)["target"] != "order:AK-MIRROR1" {
		t.Fatalf("money_matches rows: %v", list)
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

// A statement is added once however often it is uploaded, its columns are
// remembered for the channel, and cash entered is once per key.
func TestStatementsAndEntries(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	cols := moneylog.StatementFormat{Date: "Trans Date", Description: "Narration", Amount: "Amount", Reference: "Ref"}
	csv := "Trans Date,Narration,Amount,Ref\n20/10/2026,TRF FROM ADA,25000,FT1\n20/10/2026,TRF FROM ADA,25000,FT2\n21/10/2026,Charges,-50,\n"
	for i, want := range []moneylog.Imported{{Added: 3}, {Skipped: 3}} {
		lines, err := moneylog.ParseStatement(strings.NewReader(csv), cols)
		if err != nil {
			t.Fatal(err)
		}
		got, err := log.ImportStatement(ctx, e.Tenant, "gtbank/current", "user:ada", cols, lines)
		if err != nil || got != want {
			t.Fatalf("upload %d: %v %+v", i+1, err, got)
		}
	}
	if _, err := log.ImportStatement(ctx, e.Tenant, "Not A Channel!", "user:ada", cols, nil); !errors.Is(err, moneylog.ErrInvalid) {
		t.Errorf("bad channel: %v", err)
	}
	var kept moneylog.StatementFormat
	var n int
	var dirs string
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		var err error
		if kept, err = moneylog.StatementColumns(ctx, tx, e.Tenant, "gtbank/current"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*), string_agg(direction, ',' ORDER BY reference) FROM money_entries WHERE kind = 'statement' AND entered_by = 'user:ada'`).Scan(&n, &dirs)
	}); err != nil {
		t.Fatal(err)
	}
	if kept != cols || n != 3 || dirs != "out,in,in" {
		t.Errorf("kept %+v, %d lines %q", kept, n, dirs)
	}

	in := moneylog.Entered{Kind: "expense", AmountKobo: 1200000, Description: "Diesel", Vendor: "Total", Category: "Fuel", Key: "chat:1"}
	a, fresh, err := log.Enter(ctx, e.Tenant, "user:ada", in)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	if b, again, err := log.Enter(ctx, e.Tenant, "user:ada", in); err != nil || again || b != a {
		t.Errorf("retry: %v %v", err, again)
	}
	for _, bad := range []moneylog.Entered{{Kind: "gift", AmountKobo: 1, Description: "x"}, {Kind: "cash", Direction: "sideways", AmountKobo: 1, Description: "x"},
		{Kind: "expense", AmountKobo: 0, Description: "x"}, {Kind: "expense", AmountKobo: 1}} {
		if _, _, err := log.Enter(ctx, e.Tenant, "user:ada", bad); !errors.Is(err, moneylog.ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
