package moneyreport_test

import (
	"bytes"
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

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]int64{"4500": 450000, "4,500": 450000, "₦4,500.5": 450050, " ₦12.05 ": 1205, "0": 0} {
		if got, ok := moneyreport.ParseAmount(in); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "4.", "4.123", "-5", "P-1", "1e5", "12345678901234567"} {
		if _, ok := moneyreport.ParseAmount(in); ok {
			t.Errorf("%q read as an amount", in)
		}
	}
}

func TestCursor(t *testing.T) {
	c := moneyreport.Cursor{At: time.Date(2026, 11, 25, 9, 30, 0, 123456000, time.UTC), ID: uuid.New()}
	back, err := moneyreport.ParseCursor(c.String())
	if err != nil || !back.At.Equal(c.At) || back.ID != c.ID {
		t.Fatalf("%v %v", back, err)
	}
	for _, bad := range []string{"", "yesterday", "2026-11-25T09:30:00Z_x", "x_" + uuid.NewString()} {
		if _, err := moneyreport.ParseCursor(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

// The Transactions page's list: one row per movement with its latest
// status, filtered, searched and paged, and totals that agree with the
// Money page's summary.
func TestMovements(t *testing.T) {
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
	// ₦250,000 in with a ₦3,750 fee from Bola, matched to an invoice;
	// ₦7,500 in not matched; a payout of ₦40,000 settled then reversed;
	// an expense of ₦12,000 in cash; ₦50 in dollars.
	fee, amount := int64(375000), int64(25000000)
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event", PayerName: "Bola Ade", PayerAccount: "0123456789"}); err != nil {
		t.Fatal(err)
	}
	heard("payment.received", map[string]any{"payment_id": "P-2", "amount_kobo": int64(750000)})
	heard("payout.settled", map[string]any{"payout_id": "T-1", "amount_kobo": int64(4000000)})
	heard("payout.reversed", map[string]any{"payout_id": "T-1", "reason": "account closed"})
	if _, _, err := log.Enter(ctx, e.Tenant, "user:ada", moneylog.Entered{Kind: "expense", AmountKobo: 1200000, Description: "Diesel 100% fuel", Vendor: "Total", Category: "fuel", Key: "x"}); err != nil {
		t.Fatal(err)
	}
	usd := int64(5000)
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-USD",
		AmountKobo: &usd, Currency: "USD", Source: "event", Event: "payment.received"}); err != nil {
		t.Fatal(err)
	}
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

	list := func(f moneyreport.Filter, after *moneyreport.Cursor, limit int) ([]moneyreport.Movement, *moneyreport.Cursor) {
		t.Helper()
		var got []moneyreport.Movement
		var next *moneyreport.Cursor
		in(func(tx pgx.Tx) error {
			var err error
			got, next, err = moneyreport.Movements(ctx, tx, e.Tenant, f, after, limit)
			return err
		})
		return got, next
	}
	refs := func(l []moneyreport.Movement) map[string]moneyreport.Movement {
		m := map[string]moneyreport.Movement{}
		for _, x := range l {
			m[x.Reference] = x
		}
		return m
	}
	all, next := list(moneyreport.Filter{}, nil, 0)
	if len(all) != 5 || next != nil {
		t.Fatalf("five movements, not six entries: %+v", all)
	}
	got := refs(all)
	if p := got["T-1"]; p.Status != "reversed" || p.Statuses != 2 || p.AmountKobo == nil || *p.AmountKobo != 4000000 || p.Direction != "out" {
		t.Errorf("the payout: %+v", p)
	}
	if p := got["P-1"]; p.Towards != "invoice "+x.Number || p.FeeKobo == nil || *p.FeeKobo != 375000 || p.LeftKobo == nil || *p.LeftKobo != 0 {
		t.Errorf("the payment: %+v", p)
	}
	if p := got["P-USD"]; p.Currency != "USD" {
		t.Errorf("dollars: %+v", p)
	}

	// Paging: two at a time, every movement once, newest first.
	seen := map[uuid.UUID]bool{}
	var after *moneyreport.Cursor
	var last time.Time
	for page := 0; ; page++ {
		l, n := list(moneyreport.Filter{}, after, 2)
		for _, m := range l {
			if seen[m.MovementID] || (!last.IsZero() && m.When.After(last)) {
				t.Fatalf("page %d: %+v", page, l)
			}
			seen[m.MovementID], last = true, m.When
		}
		if n == nil {
			break
		}
		after = n
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d", len(seen))
	}

	// Filters.
	count := func(f moneyreport.Filter) int {
		l, _ := list(f, nil, 0)
		return len(l)
	}
	for name, c := range map[string]struct {
		f    moneyreport.Filter
		want int
	}{
		"in":            {moneyreport.Filter{Direction: "in"}, 3},
		"out":           {moneyreport.Filter{Direction: "out"}, 2},
		"cash":          {moneyreport.Filter{Channel: "cash"}, 1},
		"matched":       {moneyreport.Filter{Matched: "yes"}, 1},
		"not matched":   {moneyreport.Filter{Matched: "no"}, 1}, // P-2: not the dollars, not money out
		"vendor":        {moneyreport.Filter{Search: "total"}, 1},
		"percent typed": {moneyreport.Filter{Search: "100%"}, 1},
		"reference":     {moneyreport.Filter{Search: "p-1"}, 1},
		"invoice":       {moneyreport.Filter{Search: x.Number}, 1},
		"amount":        {moneyreport.Filter{Search: "₦7,500"}, 1},
		"nothing":       {moneyreport.Filter{Search: "zzz"}, 0},
		"in and amount": {moneyreport.Filter{Direction: "out", Search: "40,000"}, 1},
	} {
		if got := count(c.f); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	from, to := moneyreport.Day(time.Now())
	tomorrow := to
	afterTomorrow := to.AddDate(0, 0, 1)
	if n := count(moneyreport.Filter{From: &tomorrow, To: &afterTomorrow}); n != 0 {
		t.Errorf("tomorrow: %d", n)
	}
	if n := count(moneyreport.Filter{From: &from, To: &to}); n != 5 {
		t.Errorf("today: %d", n)
	}
	in(func(tx pgx.Tx) error {
		if _, _, err := moneyreport.Movements(ctx, tx, e.Tenant, moneyreport.Filter{Direction: "sideways"}, nil, 0); err == nil {
			t.Error("direction sideways taken")
		}
		if _, err := moneyreport.SumMovements(ctx, tx, e.Tenant, moneyreport.Filter{Matched: "maybe"}); err == nil {
			t.Error("matched maybe taken")
		}
		return nil
	})

	// The totals agree with the Money page's summary for the day.
	var sum moneyreport.Totals
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		if sum, err = moneyreport.SumMovements(ctx, tx, e.Tenant, moneyreport.Filter{From: &from, To: &to}); err != nil {
			return err
		}
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, from, to)
		return err
	})
	if sum.Count != 5 || sum.InSettled != s.InSettled || sum.OutSettled != s.OutSettled || sum.FeesKobo != s.FeesKobo || sum.InPending != s.InPending || sum.Other != 1 {
		t.Errorf("totals %+v, summary in %d out %d fees %d", sum, s.InSettled, s.OutSettled, s.FeesKobo)
	}
	if sum.InSettled != 25750000 || sum.OutSettled != 1200000 {
		t.Errorf("totals %+v", sum)
	}

	// One movement on its own, found by any of its entries.
	var d moneyreport.Detail
	var ok bool
	in(func(tx pgx.Tx) error {
		var reversal uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE reference = 'T-1' AND corrects IS NOT NULL`).Scan(&reversal); err != nil {
			return err
		}
		var err error
		d, ok, err = moneyreport.MovementOf(ctx, tx, e.Tenant, reversal)
		return err
	})
	if !ok || d.Reference != "T-1" || len(d.History) != 2 || d.History[0].Status != "settled" || d.History[1].Status != "reversed" || d.Source != "event" {
		t.Errorf("the payout's page: %+v", d)
	}
	in(func(tx pgx.Tx) error {
		var err error
		d, ok, err = moneyreport.MovementOf(ctx, tx, e.Tenant, got["P-1"].ID)
		return err
	})
	if !ok || len(d.Counts) != 1 || d.Counts[0].What != "invoice" || d.Counts[0].Number != x.Number || d.Counts[0].AmountKobo != 25000000 || d.Payer == nil {
		t.Errorf("the payment's page: %+v", d)
	}
	in(func(tx pgx.Tx) error {
		var err error
		_, ok, err = moneyreport.MovementOf(ctx, tx, e.Tenant, uuid.New())
		return err
	})
	if ok {
		t.Error("an entry that is not there was found")
	}
}
