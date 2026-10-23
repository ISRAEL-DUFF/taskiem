package moneyreport_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

var ctx = context.Background()

func TestSummaryAndExports(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	inv := &invoicing.Invoices{Pool: e.DB.App, Vault: e.Vault, PageBase: "https://forms.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: e.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
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
	// ₦250,000 in with a ₦3,750 fee; ₦50,000 in on a POS; a payout of
	// ₦40,000 that settled then was reversed (counts as reversed, not
	// out); an expense of ₦12,000 in cash.
	fee, amount := int64(375000), int64(25000000)
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.RecordEvent(ctx, e.Tenant, "prod", "moniepoint_pos", "shop", "pos.sale", map[string]any{"reference": "S-1", "amount_kobo": int64(5000000), "status": "approved"}); err != nil {
		t.Fatal(err)
	}
	heard("payout.settled", map[string]any{"payout_id": "T-1", "amount_kobo": int64(4000000)})
	heard("payout.reversed", map[string]any{"payout_id": "T-1", "reason": "account closed"})
	if _, _, err := log.Enter(ctx, e.Tenant, "user:ada", moneylog.Entered{Kind: "expense", AmountKobo: 1200000, Description: "Diesel", Category: "fuel", Key: "x"}); err != nil {
		t.Fatal(err)
	}
	// An invoice paid by the ₦250,000.
	var x invoicing.Invoice
	in(func(tx pgx.Tx) error {
		var err error
		x, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Ada", Email: "ada@example.com"}, AmountKobo: 25000000}, "user:ada")
		return err
	})
	if _, err := inv.Send(ctx, e.Tenant, x.Number, "user:ada", "k"); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		var entry uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE reference = 'P-1'`).Scan(&entry); err != nil {
			return err
		}
		_, _, err := inv.RecordPayment(ctx, tx, e.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 25000000, PaymentID: "P-1", EntryID: &entry, How: "exact", By: "system"})
		return err
	})

	from, to := moneyreport.Day(time.Now())
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, from, to)
		return err
	})
	if s.InSettled != 30000000 || s.OutSettled != 1200000 || s.FeesKobo != 375000 || s.FeeRateBP == nil || *s.FeeRateBP != 150 ||
		s.InvoicesPaid != 1 || s.InvoicesPaidKobo != 25000000 || len(s.Channels) != 3 || s.Channels[0].Channel != "paystack/main" {
		t.Fatalf("summary %+v", s)
	}

	// The log: one line per movement, its latest status.
	var buf bytes.Buffer
	in(func(tx pgx.Tx) error { return moneyreport.ExportLog(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ := csv.NewReader(&buf).ReadAll()
	if len(lines) != 5 {
		t.Fatalf("log %d lines: %v", len(lines), lines)
	}
	got := map[string][]string{}
	for _, l := range lines[1:] {
		got[l[9]] = l
	}
	if p := got["T-1"]; p == nil || p[4] != "reversed" || p[5] != "-40000.00" {
		t.Errorf("payout line %v", p)
	}
	if p := got["P-1"]; p == nil || p[14] != x.Number || p[6] != "3750.00" {
		t.Errorf("payment line %v", p)
	}

	// The journal balances, and posts what each movement was.
	buf.Reset()
	in(func(tx pgx.Tx) error { return moneyreport.ExportJournal(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ = csv.NewReader(&buf).ReadAll()
	var debit, credit int64
	accounts := map[string]bool{}
	for _, l := range lines[1:] {
		accounts[l[2]] = true
		debit += kobo(l[3])
		credit += kobo(l[4])
	}
	if debit != credit || debit == 0 {
		t.Errorf("journal: debits %d, credits %d", debit, credit)
	}
	for _, a := range []string{"Bank: paystack/main", moneyreport.AccountReceivable, moneyreport.AccountFees, moneyreport.AccountSales, "Expenses: Fuel", "Cash"} {
		if !accounts[a] {
			t.Errorf("no %q in %v", a, accounts)
		}
	}
	if accounts[moneyreport.AccountPayouts] {
		t.Error("a reversed payout was posted")
	}

	// Invoices: no customer without the name function; with it, the name.
	buf.Reset()
	in(func(tx pgx.Tx) error {
		return moneyreport.ExportInvoices(ctx, tx, e.Tenant, from, to, func(uuid.UUID) string { return "Ada" }, &buf)
	})
	if out := buf.String(); !strings.Contains(out, x.Number+",paid,Ada,") || strings.Contains(out, "ada@example.com") {
		t.Errorf("invoices %q", out)
	}
}

func kobo(s string) int64 {
	if s == "" {
		return 0
	}
	whole, frac, _ := strings.Cut(s, ".")
	n, _ := strconv.ParseInt(whole, 10, 64)
	f, _ := strconv.ParseInt(frac, 10, 64)
	return n*100 + f
}
