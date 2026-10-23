package moneyreport_test

import (
	"bytes"
	"encoding/csv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A refund is money out: counted out when it went out, pending while it
// is, nothing when it failed; the journal posts it to Refunds.
func TestRefundsAreMoneyOut(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	heard := func(event string, f map[string]any) {
		t.Helper()
		if _, err := log.RecordEvent(ctx, e.Tenant, "prod", "paystack", "main", event, f); err != nil {
			t.Fatal(err)
		}
	}
	// ₦100 refunded (pending, then processed); ₦50 still pending; ₦70
	// failed.
	heard("refund.pending", map[string]any{"payment_id": "P-1", "amount_kobo": int64(10000)})
	heard("refund.processed", map[string]any{"payment_id": "P-1", "refund_id": "RF-1", "amount_kobo": int64(10000)})
	heard("refund.pending", map[string]any{"payment_id": "P-2", "amount_kobo": int64(5000)})
	heard("refund.pending", map[string]any{"payment_id": "P-3", "amount_kobo": int64(7000)})
	heard("refund.failed", map[string]any{"payment_id": "P-3", "amount_kobo": int64(7000)})

	from, to := moneyreport.Day(time.Now())
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, from, to)
		return err
	})
	if s.InSettled != 0 || s.OutSettled != 10000 || s.OutPending != 5000 || len(s.Channels) != 1 || s.Channels[0].Movements != 3 {
		t.Fatalf("summary %+v", s)
	}

	var buf bytes.Buffer
	in(func(tx pgx.Tx) error { return moneyreport.ExportLog(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ := csv.NewReader(&buf).ReadAll()
	got := map[string][]string{}
	for _, l := range lines[1:] {
		got[l[9]] = l
	}
	if len(got) != 3 {
		t.Fatalf("log %v", lines)
	}
	if p := got["P-1"]; p[2] != "out" || p[3] != "refund" || p[4] != "settled" || p[5] != "-100.00" {
		t.Errorf("refund line %v", p)
	}
	if p := got["P-3"]; p[4] != "failed" {
		t.Errorf("failed refund line %v", p)
	}

	buf.Reset()
	in(func(tx pgx.Tx) error { return moneyreport.ExportJournal(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ = csv.NewReader(&buf).ReadAll()
	if len(lines) != 3 {
		t.Fatalf("journal %v", lines)
	}
	var debit, credit int64
	for _, l := range lines[1:] {
		switch l[2] {
		case moneyreport.AccountRefunds:
			debit += kobo(l[3])
		case "Bank: paystack/main":
			credit += kobo(l[4])
		default:
			t.Errorf("posted to %q: %v", l[2], l)
		}
	}
	if debit != 10000 || credit != 10000 {
		t.Errorf("journal: refunds %d, bank %d: %v", debit, credit, lines)
	}
}
