package moneyreport_test

import (
	"bytes"
	"encoding/csv"
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

// An invoice of ₦100,000 tuition and ₦20,000 for WAEC, paid in two
// payments of ₦90,000 and ₦30,000: the first is all the school's own, the
// second ₦10,000 own and ₦20,000 WAEC's (own lines first). The journal
// credits Collected for WAEC with WAEC's share and balances; the summary
// keeps it out of the school's own money in; the invoices export has the
// pass-through columns and the amendment.
func TestPassThroughInReports(t *testing.T) {
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
	var x invoicing.Invoice
	in(func(tx pgx.Tx) error {
		var err error
		x, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Ada", Phone: "+2348031239999"}, Lines: []invoicing.Line{
			{Description: "Tuition", Quantity: 1, UnitKobo: 10_000_000},
			{Description: "WAEC fee", Quantity: 1, UnitKobo: 2_000_000, PassThroughFor: "WAEC"},
		}}, "user:ada")
		return err
	})
	if _, err := inv.Send(ctx, e.Tenant, x.Number, "user:ada", "k"); err != nil {
		t.Fatal(err)
	}
	for i, amount := range []int64{9_000_000, 3_000_000} {
		ref := []string{"TRF-1", "TRF-2"}[i]
		entry, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main", Reference: ref,
			Event: "virtual_account.credited", AmountKobo: &amount, Source: "event"})
		if err != nil {
			t.Fatal(err)
		}
		in(func(tx pgx.Tx) error {
			_, _, err := inv.RecordPayment(ctx, tx, e.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: amount, PaymentID: ref, EntryID: &entry, How: "manual", By: "user:ada"})
			return err
		})
	}
	from, to := moneyreport.Day(time.Now())
	var buf bytes.Buffer
	in(func(tx pgx.Tx) error { return moneyreport.ExportJournal(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ := csv.NewReader(&buf).ReadAll()
	posted := map[string][2]int64{}
	var debit, credit int64
	for _, l := range lines[1:] {
		p := posted[l[2]]
		posted[l[2]] = [2]int64{p[0] + kobo(l[3]), p[1] + kobo(l[4])}
		debit += kobo(l[3])
		credit += kobo(l[4])
	}
	if debit != credit || debit != 12_000_000 {
		t.Errorf("journal: debits %d, credits %d: %v", debit, credit, lines)
	}
	if posted["Collected for WAEC"] != [2]int64{0, 2_000_000} || posted[moneyreport.AccountReceivable] != [2]int64{0, 10_000_000} {
		t.Errorf("posted %v", posted)
	}
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, from, to)
		return err
	})
	if s.CollectedForOthersKobo != 2_000_000 || len(s.CollectedFor) != 1 || s.CollectedFor[0].Body != "WAEC" || s.OwnInSettled != 10_000_000 {
		t.Errorf("summary %+v", s)
	}
	in(func(tx pgx.Tx) error {
		_, err := inv.Amend(ctx, tx, e.Tenant, x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Books", AmountKobo: 500_000, Reason: "Books"}, "user:ada")
		return err
	})
	buf.Reset()
	in(func(tx pgx.Tx) error { return moneyreport.ExportInvoices(ctx, tx, e.Tenant, from, to, nil, &buf) })
	rows, _ := csv.NewReader(&buf).ReadAll()
	if len(rows) != 2 || rows[0][10] != "pass_through" || rows[1][10] != "20000.00" || rows[1][11] != "WAEC" || rows[1][12] != "15000.00" || rows[1][13] != "5000.00" {
		t.Errorf("invoices %v", rows)
	}
}
