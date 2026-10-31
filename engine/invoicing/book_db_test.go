package invoicing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// The debtor book over invoices (decision 0060, R5): customers of the book
// with no phone, two of the same name kept apart; a notebook debt recorded
// and open, never sent, taking part payments and dated by the owner's
// terms; payments attributed oldest first across a customer's lines; a
// receipt with no address answered sent: false; the terms applied to open
// lines; the search name erased with the customer.
func TestDebtorBook(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tenant := w.env.Tenant
	var ada, ada2 invoicing.BookCustomer
	w.in(func(tx pgx.Tx) error {
		var err error
		if ada, err = w.inv.NewBookCustomer(ctx, tx, tenant, "  Ada   Obi ", "", "user:owner"); err != nil {
			return err
		}
		ada2, err = w.inv.NewBookCustomer(ctx, tx, tenant, "Ada", "", "user:owner")
		return err
	})
	if ada.Name != "Ada Obi" || ada.Key == ada2.Key || ada.Key[:5] != "book:" {
		t.Fatalf("customers: %+v %+v", ada, ada2)
	}
	w.in(func(tx pgx.Tx) error {
		for words, want := range map[string]int{"ada": 1, "ADA OBI": 1, "ad": 2, "obi": 1, "chidi": 0} {
			got, err := w.inv.FindBookCustomers(ctx, tx, tenant, words)
			if err != nil || len(got) != want {
				t.Errorf("find %q: %d %v", words, len(got), err)
			}
		}
		return nil
	})

	// No terms yet: recorded with no due date; a retry under the same key
	// records nothing twice.
	record := func(key string, c invoicing.BookCustomer, kobo int64, due string) invoicing.Invoice {
		t.Helper()
		var x invoicing.Invoice
		w.in(func(tx pgx.Tx) error {
			var err error
			x, err = w.inv.RecordCredit(ctx, tx, tenant, key, c, invoicing.CreditDraft{AmountKobo: kobo, Description: "Owed", DueOn: due}, "user:owner")
			return err
		})
		return x
	}
	first := record("chat:1", ada, 500000, "")
	if first.Status != "recorded" || first.Kind != "credit" || first.DueOn != "" || first.SentAt != nil || first.RecordedAt == nil ||
		!first.AllowPartPayments || first.Customer.Key != ada.Key || first.CustomerID() != "key:"+ada.Key {
		t.Fatalf("recorded: %+v", first)
	}
	if again := record("chat:1", ada, 500000, ""); again.ID != first.ID {
		t.Fatalf("a retry recorded twice: %s %s", again.Number, first.Number)
	}

	// The owner's terms date the next line; no address is not a failure.
	w.in(func(tx pgx.Tx) error {
		days := 14
		return invoicing.SetCreditTerms(ctx, tx, tenant, &days, "user:owner")
	})
	second := record("chat:2", ada, 300000, "")
	lagos, _ := time.LoadLocation("Africa/Lagos")
	if want := time.Now().In(lagos).AddDate(0, 0, 14).Format(time.DateOnly); second.DueOn != want {
		t.Fatalf("due by the terms: %q, want %q", second.DueOn, want)
	}
	out, err := w.inv.NotifyCustomer(ctx, tenant, first.Number, "Thank you", "", "r1")
	if err != nil || out["sent"] != false || out["reason"] != "no_address" {
		t.Fatalf("no address: %v %v", out, err)
	}

	// A payment counted on a recorded line moves it as on a sent one, and
	// the customer's open lines are oldest first (the undated one, by when
	// it was recorded, before the dated one only when that is later).
	if x := w.pay(first.Number, "cash-1", 200000); x.Status != "part_paid" || x.DueKobo != 300000 {
		t.Fatalf("part paid: %+v", x)
	}
	w.in(func(tx pgx.Tx) error {
		open, err := w.inv.OpenInvoicesOf(ctx, tx, tenant, first.CustomerID())
		if err != nil || len(open) != 2 || open[0].Number != first.Number || open[1].Number != second.Number {
			t.Errorf("open: %v %v", open, err)
		}
		other, err := w.inv.OpenInvoicesOf(ctx, tx, tenant, "key:"+ada2.Key)
		if err != nil || len(other) != 0 {
			t.Errorf("the other Ada: %v %v", other, err)
		}
		return nil
	})

	// A phone given later reaches her lines not yet sent; the receipt goes.
	w.in(func(tx pgx.Tx) error {
		var err error
		ada, err = w.inv.SetBookPhone(ctx, tx, tenant, ada, "+2348031234567", "user:owner")
		return err
	})
	if out, err := w.inv.NotifyCustomer(ctx, tenant, first.Number, "Thank you", "", "r2"); err != nil || out["sent"] != true {
		t.Fatalf("with a phone: %v %v", out, err)
	}

	// Past its due date a recorded line is overdue and listed so.
	late := record("chat:3", ada2, 100000, "2020-01-01")
	if late.Status != "overdue" {
		t.Fatalf("late: %q", late.Status)
	}
	listed, err := invoicing.Service{Invoices: w.inv}.ListFor(ctx, tenant, "overdue", 0, 10)
	if err != nil || len(listed) != 1 || listed[0].Number != late.Number {
		t.Fatalf("overdue list: %v %v", listed, err)
	}
	// One row per customer with something overdue; Ada's lines are not due.
	customers, err := invoicing.Service{Invoices: w.inv}.ListCustomersOverdue(ctx, tenant, 10)
	if err != nil || len(customers) != 1 || customers[0].Number != late.Number || customers[0].Customer != "key:"+ada2.Key ||
		customers[0].Lines != 1 || customers[0].DueKobo != 100000 || customers[0].DaysOverdue < 1 {
		t.Fatalf("customers overdue: %+v %v", customers, err)
	}

	// The terms applied to the open lines date each from when it was
	// recorded; no fixed time takes the dates away.
	w.in(func(tx pgx.Tx) error {
		days := 7
		if err := invoicing.SetCreditTerms(ctx, tx, tenant, &days, "user:owner"); err != nil {
			return err
		}
		n, err := invoicing.ApplyCreditTerms(ctx, tx, tenant, "user:owner")
		if err != nil || n != 3 {
			t.Errorf("applied: %d %v", n, err)
		}
		x, err := w.inv.Get(ctx, tx, tenant, first.Number)
		if want := time.Now().In(lagos).AddDate(0, 0, 7).Format(time.DateOnly); err != nil || x.DueOn != want {
			t.Errorf("first re-dated: %q %v", x.DueOn, err)
		}
		if err := invoicing.SetCreditTerms(ctx, tx, tenant, nil, "user:owner"); err != nil {
			return err
		}
		if _, err := invoicing.ApplyCreditTerms(ctx, tx, tenant, "user:owner"); err != nil {
			return err
		}
		x, err = w.inv.Get(ctx, tx, tenant, late.Number)
		if err != nil || x.DueOn != "" || x.Status != "recorded" {
			t.Errorf("no fixed time: %q %q %v", x.DueOn, x.Status, err)
		}
		days = 400
		if err := invoicing.SetCreditTerms(ctx, tx, tenant, &days, "user:owner"); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("400 days: %v", err)
		}
		return nil
	})

	// An ordinary invoice still needs somewhere to send it.
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.Create(ctx, tx, tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Bola"}, AmountKobo: 1000}, "user:owner")
		if !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("no address on an invoice: %v", err)
		}
		_, err = w.inv.Create(ctx, tx, tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Bola", Key: "book:forged"}, AmountKobo: 1000}, "user:owner")
		return err
	})

	// Erased: the name and its search form go.
	var subject string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT subject_id FROM invoice_customers WHERE id = $1`, ada2.ID).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Vault.Erase(ctx, tenant, subject, "user:owner"); err != nil {
		t.Fatal(err)
	}
	var search string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT search FROM invoice_customers WHERE id = $1`, ada2.ID).Scan(&search); err != nil || search != "(erased)" {
		t.Errorf("search after erasure: %q %v", search, err)
	}
	w.in(func(tx pgx.Tx) error {
		c, err := w.inv.BookCustomerByKey(ctx, tx, tenant, ada2.Key)
		if err != nil || c.Name != "(erased)" {
			t.Errorf("erased: %+v %v", c, err)
		}
		return nil
	})
}

// A customer of a sent invoice is found by name and, chosen, kept in the
// book as their phone's customer: lines recorded for them and invoices sent
// to them are one balance. Someone new given a phone is that phone's
// customer from the start, and a phone has one record.
func TestBookCustomerOfSentInvoice(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tenant := w.env.Tenant
	send := func(c invoicing.Customer, kobo int64, key string) invoicing.Invoice {
		t.Helper()
		x := w.create(invoicing.Draft{Customer: c, AmountKobo: kobo})
		if _, err := w.inv.Send(ctx, tenant, x.Number, "user:owner", key); err != nil {
			t.Fatal(err)
		}
		var got invoicing.Invoice
		w.in(func(tx pgx.Tx) error {
			var err error
			got, err = w.inv.Get(ctx, tx, tenant, x.Number)
			return err
		})
		return got
	}
	sent := send(invoicing.Customer{Name: "Chidi  Eze", Phone: "+2348035550001"}, 400000, "s1")
	// A draft is not offered, nor a customer with only an email.
	w.create(invoicing.Draft{Customer: invoicing.Customer{Name: "Chidinma Draft", Phone: "+2348035550009"}, AmountKobo: 1000})
	send(invoicing.Customer{Name: "Chidiebere Mail", Email: "chidiebere@example.com"}, 1000, "s2")

	var offered invoicing.BookCustomer
	w.in(func(tx pgx.Tx) error {
		got, err := w.inv.FindBookCustomers(ctx, tx, tenant, "chidi")
		if err != nil || len(got) != 1 {
			t.Fatalf("find: %+v %v", got, err)
		}
		offered = got[0]
		if offered.Key != "" || offered.Name != "Chidi Eze" || offered.Phone != "+2348035550001" || offered.CustomerID != sent.CustomerID() ||
			offered.Ref() != invoicing.SentRefPrefix+sent.CustomerID() {
			t.Errorf("offered: %+v", offered)
		}
		again, err := w.inv.BookCustomerByRef(ctx, tx, tenant, offered.Ref())
		if err != nil || again.CustomerID != offered.CustomerID || again.Name != offered.Name {
			t.Errorf("by ref: %+v %v", again, err)
		}
		return nil
	})

	// Chosen: a record of the book under their phone's identity, once.
	var chidi invoicing.BookCustomer
	w.in(func(tx pgx.Tx) error {
		var err error
		if chidi, err = w.inv.AdoptCustomer(ctx, tx, tenant, offered.CustomerID, "user:owner"); err != nil {
			return err
		}
		again, err := w.inv.AdoptCustomer(ctx, tx, tenant, offered.CustomerID, "user:owner")
		if err != nil || again.ID != chidi.ID {
			t.Errorf("adopted twice: %+v %v", again, err)
		}
		byRef, err := w.inv.BookCustomerByRef(ctx, tx, tenant, offered.Ref())
		if err != nil || byRef.ID != chidi.ID {
			t.Errorf("the ref after: %+v %v", byRef, err)
		}
		same, err := w.inv.NewBookCustomer(ctx, tx, tenant, "Chidi E", "+2348035550001", "user:owner")
		if err != nil || same.ID != chidi.ID || same.Name != "Chidi Eze" {
			t.Errorf("the same phone: %+v %v", same, err)
		}
		if _, err := w.inv.SetBookPhone(ctx, tx, tenant, chidi, "+2348035550002", "user:owner"); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("a second phone: %v", err)
		}
		return nil
	})
	if chidi.Key == "" || chidi.CustomerID != sent.CustomerID() || chidi.Name != "Chidi Eze" || chidi.Phone != "+2348035550001" {
		t.Fatalf("adopted: %+v", chidi)
	}
	var line invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		if line, err = w.inv.RecordCredit(ctx, tx, tenant, "chat:c1", chidi, invoicing.CreditDraft{AmountKobo: 150000, Description: "Owed"}, "user:owner"); err != nil {
			return err
		}
		open, err := w.inv.OpenInvoicesOf(ctx, tx, tenant, sent.CustomerID())
		if err != nil || len(open) != 2 {
			t.Errorf("one balance: %d %v", len(open), err)
		}
		// Now the book's own: found once, as the record.
		got, err := w.inv.FindBookCustomers(ctx, tx, tenant, "chidi")
		if err != nil || len(got) != 1 || got[0].ID != chidi.ID {
			t.Errorf("found after: %+v %v", got, err)
		}
		return nil
	})
	if line.CustomerID() != sent.CustomerID() || line.Customer.Key != "" || line.Customer.Phone != "+2348035550001" || line.Status != "recorded" {
		t.Fatalf("line: %+v %q", line, line.CustomerID())
	}

	// Someone new with a phone is that phone's customer from the start: an
	// invoice sent to the number later joins their lines.
	var dayo invoicing.BookCustomer
	w.in(func(tx pgx.Tx) error {
		var err error
		dayo, err = w.inv.NewBookCustomer(ctx, tx, tenant, "Dayo", "+2348035550003", "user:owner")
		return err
	})
	later := send(invoicing.Customer{Name: "Dayo Ade", Phone: "+2348035550003"}, 2000, "s3")
	if dayo.CustomerID != later.CustomerID() || dayo.Key == "" {
		t.Fatalf("dayo: %+v %q", dayo, later.CustomerID())
	}
	// Someone new with no phone is their record's key.
	w.in(func(tx pgx.Tx) error {
		bayo, err := w.inv.NewBookCustomer(ctx, tx, tenant, "Bayo", "", "user:owner")
		if err != nil || bayo.CustomerID != "key:"+bayo.Key {
			t.Errorf("no phone: %+v %v", bayo, err)
		}
		return nil
	})
}
