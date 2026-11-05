package matching_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Money paid through a customer's payment link (decision 0060, R5) is
// theirs: counted exactly to their open lines oldest first, with no one
// asked, the rest their credit, which their next notebook line takes;
// heard from the rail's event or found by the checker first, it counts
// once.
func TestCustomerLinkPaysOldestFirst(t *testing.T) {
	w := newWorld(t)
	tenant := w.env.Tenant
	var ada invoicing.BookCustomer
	var lines []invoicing.Invoice
	var link uuid.UUID
	w.in(func(tx pgx.Tx) error {
		var err error
		if ada, err = w.inv.NewBookCustomer(ctx, tx, tenant, "Ada", "+2348031234567", "user:owner"); err != nil {
			return err
		}
		for i, kobo := range []int64{300000, 200000} {
			x, err := w.inv.RecordCredit(ctx, tx, tenant, "c"+string(rune('1'+i)), ada, invoicing.CreditDraft{AmountKobo: kobo}, "user:owner")
			if err != nil {
				return err
			}
			lines = append(lines, x)
		}
		if _, err := w.inv.CustomerLink(ctx, tx, tenant, lines[0].CustomerID(), "user:owner"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM invoice_customer_links WHERE customer_id = $1`, lines[0].CustomerID()).Scan(&link); err != nil {
			return err
		}
		for i, kobo := range []int64{400000, 200000} {
			if _, err := tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, customer_link_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
				VALUES (gen_random_uuid(), $1, $2, $3, 'transfer', 'paystack', 'main', $4, $5, 'open')`, tenant, link, i+1, "tsk_link_"+string(rune('1'+i)), kobo); err != nil {
				return err
			}
		}
		return nil
	})
	account := func(pid string) (uuid.UUID, string) {
		t.Helper()
		var id uuid.UUID
		var status string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id, status FROM customer_invoice_accounts WHERE payment_id = $1`, pid).Scan(&id, &status); err != nil {
			t.Fatal(err)
		}
		return id, status
	}
	paid := func() (int64, int64) {
		t.Helper()
		return w.read(lines[0].Number).PaidKobo, w.read(lines[1].Number).PaidKobo
	}

	// Heard first: the oldest line is paid, the next in part.
	w.heard("payment.received", map[string]any{"payment_id": "tsk_link_1", "amount_kobo": int64(400000)})
	if a, b := paid(); a != 300000 || b != 100000 {
		t.Fatalf("paid %d %d", a, b)
	}
	if x := w.read(lines[0].Number); x.Status != "paid" || len(x.Payments) != 1 || x.Payments[0].How != "exact" ||
		x.Payments[0].Reasons[0] != "paid through the customer's own payment link" {
		t.Errorf("the oldest: %+v", x)
	}
	id1, st := account("tsk_link_1")
	if st != "paid" {
		t.Errorf("account: %s", st)
	}
	// The checker finds it after: nothing more.
	if err := w.m.LinkPaid(ctx, tenant, link, invoicing.Received{PaymentID: "tsk_link_1", AmountKobo: 400000, Connection: "paystack/main", Account: id1}); err != nil {
		t.Fatal(err)
	}
	if a, b := paid(); a != 300000 || b != 100000 {
		t.Fatalf("counted twice: %d %d", a, b)
	}

	// Found by the checker first: the rest of the line, then credit; the
	// rail's event after counts nothing more.
	id2, _ := account("tsk_link_2")
	if err := w.m.LinkPaid(ctx, tenant, link, invoicing.Received{PaymentID: "tsk_link_2", AmountKobo: 200000, Connection: "paystack/main", Account: id2}); err != nil {
		t.Fatal(err)
	}
	w.heard("payment.received", map[string]any{"payment_id": "tsk_link_2", "amount_kobo": int64(200000)})
	if a, b := paid(); a != 300000 || b != 200000 || w.creditOf(lines[0].CustomerID()) != 100000 {
		t.Fatalf("paid %d %d, credit %d", a, b, w.creditOf(lines[0].CustomerID()))
	}
	if _, st := account("tsk_link_2"); st != "paid" {
		t.Errorf("account 2: %s", st)
	}
	var how string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(how, ',') FROM money_entry_customers WHERE customer_id = $1`, lines[0].CustomerID()).Scan(&how); err != nil || how != "link,link" {
		t.Errorf("marked: %q %v", how, err)
	}

	// Her next notebook line takes the credit.
	w.in(func(tx pgx.Tx) error {
		x, err := w.inv.RecordCredit(ctx, tx, tenant, "c3", ada, invoicing.CreditDraft{AmountKobo: 300000}, "user:owner")
		if err != nil || x.Status != "part_paid" || x.DueKobo != 200000 {
			t.Errorf("credit taken: %+v %v", x, err)
		}
		return err
	})
	if c := w.creditOf(lines[0].CustomerID()); c != 0 {
		t.Errorf("credit left: %d", c)
	}
}
