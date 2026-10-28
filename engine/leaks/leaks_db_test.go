package leaks_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

var ctx = context.Background()

func TestLeaks(t *testing.T) {
	e := rt.New(t)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	inv := &invoicing.Invoices{Pool: e.DB.App, Vault: e.Vault, PageBase: "https://forms.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: e.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	m := &matching.Matcher{Invoices: inv, PII: e.Vault}
	send := func(name string, kobo int64, due string) invoicing.Invoice {
		var x invoicing.Invoice
		in(func(tx pgx.Tx) error {
			var err error
			x, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: name, Email: strings.ToLower(name) + "@example.com"}, AmountKobo: kobo, DueOn: due}, "user:ada")
			return err
		})
		if _, err := inv.Send(ctx, e.Tenant, x.Number, "user:ada", "k"+x.Number); err != nil {
			t.Fatal(err)
		}
		return x
	}
	heard := func(event string, f map[string]any) {
		if _, err := log.RecordEvent(ctx, e.Tenant, "prod", "paystack", "main", event, f); err != nil {
			t.Fatal(err)
		}
	}

	// Money in with nothing to match.
	heard("payment.received", map[string]any{"payment_id": "LOST-1", "amount_kobo": int64(777700)})
	// The same payment reported as two kinds of event.
	heard("payment.received", map[string]any{"payment_id": "DUP-1", "amount_kobo": int64(50000)})
	heard("virtual_account.credited", map[string]any{"reference": "DUP-1", "credit_id": "DUP-1", "amount_kobo": int64(50000)})
	// An overdue invoice.
	late := send("Bola", 300000, "2099-01-31")
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET due_on = '2020-01-01' WHERE id = $1`, late.ID); err != nil {
		t.Fatal(err)
	}
	// An invoice paid more than it asked.
	over := send("Chidi", 100000, "")
	in(func(tx pgx.Tx) error {
		_, _, err := inv.RecordPayment(ctx, tx, e.Tenant, over.Number, invoicing.PaymentIn{AmountKobo: 150000, PaymentID: "OVER-1", How: "manual", By: "user:ada"})
		return err
	})
	// Money into the account of a voided invoice.
	void := send("Dayo", 200000, "")
	in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
			VALUES (gen_random_uuid(), $1, $2, 1, 'transfer', 'paystack', 'main', 'tsk_void', 200000, 'open')`, e.Tenant, void.ID); err != nil {
			return err
		}
		_, err := inv.Void(ctx, tx, e.Tenant, void.Number, "sent twice", "user:ada")
		return err
	})
	heard("payment.received", map[string]any{"payment_id": "tsk_void", "amount_kobo": int64(200000)})
	// A fee above the agreed rate: 1.5% capped at ₦2,000, charged ₦3,000.
	in(func(tx pgx.Tx) error {
		cap := int64(200000)
		return leaks.SetFeeRate(ctx, tx, e.Tenant, leaks.FeeRate{Channel: "paystack/main", PercentBP: 150, CapKobo: &cap})
	})
	fee, amount := int64(300000), int64(50000000)
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "FEE-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	// A day later, the detector finds each.
	d := &leaks.Detector{Pool: e.DB.App, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var open []leaks.Leak
	in(func(tx pgx.Tx) error {
		var err error
		open, err = leaks.List(ctx, tx, e.Tenant, "open", 0)
		return err
	})
	byKind := map[string][]leaks.Leak{}
	for _, l := range open {
		byKind[l.Kind] = append(byKind[l.Kind], l)
	}
	check := func(kind string, amount int64, detail string) {
		t.Helper()
		var hit *leaks.Leak
		for i := range byKind[kind] {
			if byKind[kind][i].AmountKobo == amount {
				hit = &byKind[kind][i]
			}
		}
		if hit == nil || !strings.Contains(hit.Detail, detail) {
			t.Errorf("%s ₦%d: %+v", kind, amount/100, byKind[kind])
		}
	}
	check("unmatched", 777700, "₦7,777 came in on paystack/main")
	check("duplicate", 50000, "with reference DUP-1 is on paystack/main 2 times")
	check("overdue", 300000, late.Number+" is overdue: ₦3,000 still due")
	check("overpaid", 50000, over.Number+" was paid ₦500 more")
	check("void_paid", 200000, "was paid for "+void.Number+", which was voided")
	check("fee", 100000, "a fee of ₦3,000 on ₦500,000")
	// Looking again opens nothing new.
	if n, err := d.Tenant(ctx, e.Tenant); err != nil || n != 0 {
		t.Errorf("again: %d %v", n, err)
	}

	// The lifecycle: assigned, closed as recovered with the amount; a
	// closed leak takes no change; one whose cause goes away resolves.
	var u leaks.Leak
	for _, l := range byKind["unmatched"] {
		if l.AmountKobo == 777700 {
			u = l
		}
	}
	member := uuid.New()
	in(func(tx pgx.Tx) error {
		if _, err := leaks.Assign(ctx, tx, e.Tenant, u.ID, member); err != nil {
			return err
		}
		if _, err := leaks.Close(ctx, tx, e.Tenant, u.ID, "recovered", nil, "", "user:ada", time.Now()); !errors.Is(err, leaks.ErrInvalid) {
			t.Errorf("recovered without an amount: %v", err)
		}
		got := int64(777700)
		l, err := leaks.Close(ctx, tx, e.Tenant, u.ID, "recovered", &got, "the customer paid the wrong account", "user:ada", time.Now())
		if err != nil || l.Status != "recovered" || l.AssignedTo == nil || *l.AssignedTo != member {
			t.Errorf("closed %v %+v", err, l)
		}
		if _, err := leaks.Close(ctx, tx, e.Tenant, u.ID, "written_off", nil, "", "user:ada", time.Now()); !errors.Is(err, leaks.ErrState) {
			t.Errorf("closed twice: %v", err)
		}
		// Open: the five other kinds, and the two DUP-1 entries and FEE-1,
		// which nothing matches either; not the voided invoice's payment a
		// second time, nor LOST-1, recovered.
		tot, err := leaks.Sum(ctx, tx, e.Tenant)
		if err != nil || tot.RecoveredKobo != 777700 || tot.Open != 8 {
			now, _ := leaks.List(ctx, tx, e.Tenant, "open", 0)
			for _, l := range now {
				t.Logf("open: %s %d %s", l.Kind, l.AmountKobo, l.Detail)
			}
			t.Errorf("totals %v %+v", err, tot)
		}
		return nil
	})
	in(func(tx pgx.Tx) error {
		_, _, err := inv.RecordPayment(ctx, tx, e.Tenant, late.Number, invoicing.PaymentIn{AmountKobo: 300000, PaymentID: "LATE-1", How: "manual", By: "user:ada"})
		return err
	})
	if err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM money_leaks WHERE kind = 'overdue'`).Scan(&status); err != nil {
			return err
		}
		if status != "resolved" {
			t.Errorf("paid overdue invoice: %s", status)
		}
		return nil
	})
}
