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

// One payment split across two invoices, settled net of its fee, with the
// rest left as the customer's credit: the journal posts each part for what
// it counts, what landed to the bank, the fee as a cost, and the rest as
// credit; the log names both invoices.
func TestSplitNetPaymentInExports(t *testing.T) {
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
	// ₦99,000 landed of the ₦100,000 sent; the rail kept ₦1,000.
	amount, fee := int64(9900000), int64(100000)
	entry, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main", Reference: "VA-NET",
		Event: "virtual_account.credited", AmountKobo: &amount, FeeKobo: &fee, Net: true, Source: "event"})
	if err != nil {
		t.Fatal(err)
	}
	var numbers []string
	for _, total := range []int64{6000000, 3000000} {
		var x invoicing.Invoice
		in(func(tx pgx.Tx) error {
			var err error
			x, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Ada", Phone: "+2348031239999"}, AmountKobo: total}, "user:ada")
			return err
		})
		if _, err := inv.Send(ctx, e.Tenant, x.Number, "user:ada", "k"+x.Number); err != nil {
			t.Fatal(err)
		}
		in(func(tx pgx.Tx) error {
			_, _, err := inv.RecordPayment(ctx, tx, e.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: total, PaymentID: "VA-NET", EntryID: &entry, How: "manual", By: "user:ada"})
			if err != nil {
				return err
			}
			return invoicing.MarkCustomers(ctx, tx, e.Tenant, entry, x.CustomerID(), nil, "person", "user:ada")
		})
		numbers = append(numbers, x.Number)
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
	if debit != credit || debit != 10000000 {
		t.Errorf("journal: debits %d, credits %d: %v", debit, credit, lines)
	}
	want := map[string][2]int64{
		"Bank: paystack/main":         {9900000, 0},
		moneyreport.AccountFees:       {100000, 0},
		moneyreport.AccountReceivable: {0, 9000000},
		moneyreport.AccountCredit:     {0, 1000000},
	}
	for a, w := range want {
		if posted[a] != w {
			t.Errorf("%s: %v, want %v (%v)", a, posted[a], w, posted)
		}
	}
	buf.Reset()
	in(func(tx pgx.Tx) error { return moneyreport.ExportLog(ctx, tx, e.Tenant, from, to, &buf) })
	lines, _ = csv.NewReader(&buf).ReadAll()
	if len(lines) != 2 || lines[1][14] != numbers[0]+" "+numbers[1] {
		t.Errorf("log %v", lines)
	}
}
