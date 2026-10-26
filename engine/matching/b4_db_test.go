package matching_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
)

// family makes a school (the area on) with one family of two students,
// and says whether the school shares family credit.
func (w *world) family(share bool) (a, b invoicing.Customer) {
	w.t.Helper()
	s := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.inv}
	var ids [2]uuid.UUID
	w.in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, w.env.Tenant, true, "user:ada"); err != nil {
			return err
		}
		c, err := s.AddClass(ctx, tx, w.env.Tenant, schools.ClassIn{Name: "JSS 1", Order: 7}, "user:ada")
		if err != nil {
			return err
		}
		f, err := s.AddFamily(ctx, tx, w.env.Tenant, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031230001"}}, "user:ada")
		if err != nil {
			return err
		}
		for i, n := range []string{"GCA/001", "GCA/002"} {
			d, err := s.AddStudent(ctx, tx, w.env.Tenant, schools.StudentIn{Name: "Child " + n, AdmissionNumber: n, ClassID: c.ID, FamilyID: &f.ID}, "user:ada")
			if err != nil {
				return err
			}
			ids[i] = d.ID
		}
		_, err = schools.SetIssuing(ctx, tx, w.env.Tenant, schools.Issuing{SiblingsShareCredit: share}, "user:ada")
		return err
	})
	a = invoicing.Customer{Name: "Ngozi Eze", Phone: "+2348031230001", Key: schools.StudentKey(ids[0])}
	b = invoicing.Customer{Name: "Ngozi Eze", Phone: "+2348031230001", Key: schools.StudentKey(ids[1])}
	return a, b
}

// With the school sharing family credit, what a student's account takes
// beyond their own invoices counts towards their sibling's, oldest first,
// once; and a sibling's invoice sent later takes what is still left.
func TestSiblingsShareCredit(t *testing.T) {
	w := newWorld(t)
	a, b := w.family(true)
	own := w.invoiceFor(a, 2000000, "2099-09-01")
	sib1 := w.invoiceFor(b, 1500000, "2099-09-01")
	sib2 := w.invoiceFor(b, 1500000, "2099-12-01")
	w.register(a, "9912345678", "tsk_a")
	w.credit(moneylog.Entry{Reference: "tsk_a", AccountNumber: "9912345678", AmountKobo: kobo(4000000), Credit: "c1"})
	x, y, z := w.read(own.Number), w.read(sib1.Number), w.read(sib2.Number)
	if x.Status != "paid" || y.Status != "paid" || z.PaidKobo != 500000 {
		t.Fatalf("after the credit: own %s, sibling's %s and %d", x.Status, y.Status, z.PaidKobo)
	}
	if r := y.Payments[0].Reasons; len(r) != 2 || r[1] != matching.ReasonSibling {
		t.Errorf("sibling's reasons %q", r)
	}
	// Looked at again: nothing more is counted.
	if err := w.m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if z = w.read(sib2.Number); z.PaidKobo != 500000 || w.creditOf(a.Key) != 0 && w.creditOf("key:"+a.Key) != 0 {
		t.Fatalf("again: %d, credit %d", z.PaidKobo, w.creditOf("key:"+a.Key))
	}
	// A second payment, more than everything open: the rest is the
	// student's credit, and the sibling's next invoice takes it when sent.
	w.credit(moneylog.Entry{Reference: "tsk_a", AccountNumber: "9912345678", AmountKobo: kobo(2000000), Credit: "c2"})
	if z = w.read(sib2.Number); z.Status != "paid" || w.creditOf("key:"+a.Key) != 1000000 {
		t.Fatalf("second payment: %s, credit %d", z.Status, w.creditOf("key:"+a.Key))
	}
	next := w.invoiceFor(b, 400000, "")
	if next = w.read(next.Number); next.Status != "paid" || next.Payments[0].Reasons[0] != invoicing.FamilyShareReason {
		t.Fatalf("sent with the sibling's credit: %s %+v", next.Status, next.Payments)
	}
}

// Off (the default), a student's leftover stays their own credit.
func TestSiblingsCreditOffByDefault(t *testing.T) {
	w := newWorld(t)
	a, b := w.family(false)
	w.invoiceFor(a, 1000000, "2099-09-01")
	sib := w.invoiceFor(b, 1000000, "2099-09-01")
	w.register(a, "9912345678", "tsk_a")
	w.credit(moneylog.Entry{Reference: "tsk_a", AccountNumber: "9912345678", AmountKobo: kobo(3000000), Credit: "c1"})
	if y := w.read(sib.Number); y.PaidKobo != 0 || w.creditOf("key:"+a.Key) != 2000000 {
		t.Fatalf("sibling paid %d, credit %d", y.PaidKobo, w.creditOf("key:"+a.Key))
	}
}

func (w *world) alerts(kind string) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, kind).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// A refund of a credit to a customer's account (named by its credit_id)
// undoes its matches and reopens the invoices; the money counts for
// nothing more, the owners are told once; a partial refund leaves the
// rest matched again, oldest invoice first.
func TestRefundedCreditReopensInvoices(t *testing.T) {
	w := newWorld(t)
	c := invoicing.Customer{Name: "Ebube Eze", Phone: "+2348031230001", Key: "student:1"}
	first := w.invoiceFor(c, 6000000, "2099-09-01")
	second := w.invoiceFor(c, 4000000, "2099-12-01")
	w.register(c, "9912345678", "tsk_e")
	entry := w.credit(moneylog.Entry{Reference: "tsk_e", AccountNumber: "9912345678", AmountKobo: kobo(10000000), Credit: "PSK-1"})
	if w.read(first.Number).Status != "paid" || w.read(second.Number).Status != "paid" {
		t.Fatal("not paid")
	}
	// ₦30,000 of it refunded.
	w.heard("refund.processed", map[string]any{"payment_id": "PSK-1", "refund_id": "R1", "amount_kobo": 3000000})
	a, b := w.read(first.Number), w.read(second.Number)
	if a.Status != "paid" || b.PaidKobo != 1000000 || b.Status == "paid" {
		t.Fatalf("after a partial refund: %s %d, %s %d", a.Status, a.PaidKobo, b.Status, b.PaidKobo)
	}
	if w.alerts(matching.AlertReversed) != 1 || w.creditOf("key:student:1") != 0 {
		t.Fatalf("alerts %d, credit %d", w.alerts(matching.AlertReversed), w.creditOf("key:student:1"))
	}
	// Another credit refunded whole (the refund names no amount):
	// everything it paid reopens, nothing is left to count.
	third := w.invoiceFor(c, 2000000, "2100-01-01")
	entry = w.credit(moneylog.Entry{Reference: "tsk_e", AccountNumber: "9912345678", AmountKobo: kobo(5000000), Credit: "PSK-2"})
	if w.read(second.Number).Status != "paid" || w.read(third.Number).PaidKobo != 2000000 {
		t.Fatal("the second credit did not pay")
	}
	w.heard("refund.processed", map[string]any{"payment_id": "PSK-2", "refund_id": "R2"})
	b, x := w.read(second.Number), w.read(third.Number)
	if b.PaidKobo != 1000000 || x.PaidKobo != 0 {
		t.Fatalf("after the whole refund: %s %d, %s %d", b.Status, b.PaidKobo, x.Status, x.PaidKobo)
	}
	var left int64
	var state, reason string
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_money_entry_left($1), s.state, COALESCE(s.held_reason, '') FROM money_match_state s WHERE s.entry_id = $1`, entry).Scan(&left, &state, &reason)
	})
	if left != 0 || state != "matched" || w.creditOf("key:student:1") != 0 || len(w.unmatched()) != 0 {
		t.Fatalf("entry left %d %s %q, credit %d", left, state, reason, w.creditOf("key:student:1"))
	}
	// Sent a new invoice: no credit counts towards it.
	fourth := w.invoiceFor(c, 100000, "")
	if w.read(fourth.Number).PaidKobo != 0 {
		t.Error("a refunded credit paid a new invoice")
	}
	// Looked at again: nothing more happens.
	if err := w.m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.alerts(matching.AlertReversed) != 2 {
		t.Errorf("%d alerts", w.alerts(matching.AlertReversed))
	}
	var notice string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT title FROM alerts WHERE tenant_id = $1 AND kind = $2 ORDER BY created_at DESC LIMIT 1`, w.env.Tenant, matching.AlertReversed).Scan(&notice); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, second.Number) || !strings.Contains(notice, third.Number) || strings.Contains(notice, "Ebube") {
		t.Errorf("told %q", notice)
	}
}

// A payment matched by hand and then returned by the rail
// (payment.returned) is taken out of the invoice, which is open again; it
// does not go back to To match.
func TestReturnedPaymentReopensInvoice(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 2500000)
	w.heard("payment.received", map[string]any{"payment_id": "PAY-9", "amount_kobo": 2500000})
	var entry uuid.UUID
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE reference = 'PAY-9' AND status = 'settled'`).Scan(&entry)
	})
	if _, err := w.m.Match(ctx, w.env.Tenant, entry, x.Number, "user:ada"); err != nil {
		t.Fatal(err)
	}
	if w.read(x.Number).Status != "paid" {
		t.Fatal("not paid")
	}
	w.heard("payment.returned", map[string]any{"payment_id": "PAY-9", "reason": "other"})
	if got := w.read(x.Number); got.Status == "paid" || got.PaidKobo != 0 {
		t.Fatalf("after the return: %s %d", got.Status, got.PaidKobo)
	}
	if len(w.unmatched()) != 0 || len(w.open()) != 0 || w.alerts(matching.AlertReversed) != 1 {
		t.Fatalf("unmatched %+v, open %+v, alerts %d", w.unmatched(), w.open(), w.alerts(matching.AlertReversed))
	}
	// Returned before it was ever looked at: it is never offered.
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.log.RecordTx(ctx, tx, w.env.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main",
			Reference: "PAY-10", Event: "payment.received", Source: "event", AmountKobo: kobo(2500000)})
		if err != nil {
			return err
		}
		_, _, err = w.log.RecordTx(ctx, tx, w.env.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "reversed", Channel: "paystack/main",
			Reference: "PAY-10", Event: "payment.returned", Source: "event"})
		return err
	})
	if err := w.m.Reversals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.open()) != 0 || len(w.unmatched()) != 0 {
		t.Fatalf("a returned payment was offered: %+v %+v", w.open(), w.unmatched())
	}
}

// With the school sharing family credit, To match offers a sibling's open
// invoice as a part of the split, and a split by hand across the family
// may leave the rest as the student's credit; the sibling's part says so.
func TestSplitOfferedToSiblings(t *testing.T) {
	w := newWorld(t)
	a, b := w.family(true)
	own := w.invoiceFor(a, 2000000, "2099-09-01")
	sib := w.invoiceFor(b, 1500000, "2099-09-01")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-FAM", "amount_kobo": int64(2000000), "payer_name": "NGOZI EZE"})
	open := w.open()
	if len(open) == 0 || open[0].Number != own.Number {
		t.Fatalf("suggestions %+v", open)
	}
	var p matching.Proposal
	w.in(func(tx pgx.Tx) error {
		var err error
		p, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, true)
		return err
	})
	if p.Over || len(p.Parts) != 2 || p.Parts[0].Number != own.Number || p.Parts[0].AmountKobo != 2000000 || p.Parts[1].Number != sib.Number || p.Parts[1].AmountKobo != 0 || !p.Parts[1].Sibling || p.CreditKobo != 0 {
		t.Fatalf("proposal %+v", p)
	}
	s, err := w.m.ConfirmParts(ctx, w.env.Tenant, open[0].ID, []matching.Part{{Invoice: own.Number, AmountKobo: 1000000}, {Invoice: sib.Number, AmountKobo: 500000}}, true, "user:ada")
	if err != nil || len(s.Parts) != 2 || s.CreditKobo != 500000 || s.LeftKobo != 0 {
		t.Fatalf("split %v %+v", err, s)
	}
	if y := w.read(sib.Number); y.PaidKobo != 500000 || len(y.Payments) != 1 || y.Payments[0].Reasons[0] != matching.SplitSiblingReason {
		t.Fatalf("the sibling's part %d %+v", y.PaidKobo, y.Payments)
	}
	if w.read(own.Number).PaidKobo != 1000000 || w.creditOf("key:"+a.Key) != 500000 {
		t.Fatalf("own %d, credit %d", w.read(own.Number).PaidKobo, w.creditOf("key:"+a.Key))
	}
}

// Off (the default), a sibling's invoice is not offered, and the rest
// cannot be left as credit when a part is a sibling's; the split by hand
// itself is allowed, as across any invoices.
func TestSplitNotOfferedToSiblingsByDefault(t *testing.T) {
	w := newWorld(t)
	a, b := w.family(false)
	own := w.invoiceFor(a, 2000000, "2099-09-01")
	sib := w.invoiceFor(b, 1500000, "2099-09-01")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-FAM2", "amount_kobo": int64(2000000), "payer_name": "NGOZI EZE"})
	open := w.open()
	if len(open) == 0 || open[0].Number != own.Number {
		t.Fatalf("suggestions %+v", open)
	}
	var p matching.Proposal
	w.in(func(tx pgx.Tx) error {
		var err error
		p, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, true)
		return err
	})
	if len(p.Parts) != 0 || p.Over {
		t.Fatalf("proposal %+v", p)
	}
	parts := []matching.Part{{Invoice: own.Number, AmountKobo: 1000000}, {Invoice: sib.Number, AmountKobo: 500000}}
	if _, err := w.m.ConfirmParts(ctx, w.env.Tenant, open[0].ID, parts, true, "user:ada"); !errors.Is(err, invoicing.ErrInvalid) {
		t.Fatalf("credit across customers: %v", err)
	}
	if s, err := w.m.ConfirmParts(ctx, w.env.Tenant, open[0].ID, parts, false, "user:ada"); err != nil || s.LeftKobo != 500000 || w.read(sib.Number).Payments[0].Reasons[0] != matching.SplitReason {
		t.Fatalf("split %v %+v", err, s)
	}
}
