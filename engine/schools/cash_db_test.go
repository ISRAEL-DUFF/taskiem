package schools_test

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
)

// cashWorld is a term invoiced and sent for two siblings, with receipts
// wired to the invoices, as the server wires them.
type cashWorld struct {
	*termWorld
	a, b schools.StudentDetail
	invA invoicing.Invoice
	invB invoicing.Invoice
}

func newCashWorld(t *testing.T) *cashWorld {
	w := &cashWorld{termWorld: newTermWorld(t)}
	schools.WireReceipts(w.s.Invoices, w.s)
	w.a = w.student("Ebube Eze", "GCA/001", w.j1, &w.fam.ID)
	w.b = w.student("Chidi Eze", "GCA/002", w.j1, &w.fam.ID)
	w.start(w.first)
	w.setFees(w.first, w.j1, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required}, {Name: "WAEC fee", AmountKobo: 1_800_000, Kind: schools.Required, PassThroughFor: "WAEC"}})
	w.invoice(w.plan().Hash)
	if _, failed, err := w.s.SendTerm(ctx, w.env.Tenant, w.first.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("sending the term: %v %v", err, failed)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.TermInvoices(ctx, tx, w.env.Tenant, w.first.ID, nil, "")
		if err != nil {
			return err
		}
		for _, ti := range list {
			x, err := w.s.Invoices.Get(ctx, tx, w.env.Tenant, ti.InvoiceID.String())
			if err != nil {
				return err
			}
			if ti.StudentID == w.a.ID {
				w.invA = x
			} else {
				w.invB = x
			}
		}
		return nil
	})
	return w
}

func (w *cashWorld) post(student string, kobo int64, how, by string) (schools.Posting, error) {
	w.t.Helper()
	p, parts, err := w.s.PostPayment(ctx, w.env.Tenant, schools.PostingIn{Student: student, AmountKobo: kobo, How: how, Note: "At the desk"}, by)
	if err == nil {
		err = w.s.PostedAfter(ctx, w.env.Tenant, parts)
	}
	return p, err
}

func (w *cashWorld) invoiceOf(id uuid.UUID) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.s.Invoices.Get(ctx, tx, w.env.Tenant, id.String())
		return err
	})
	return x
}

func (w *cashWorld) receipts(f schools.ReceiptFilter) []schools.Receipt {
	w.t.Helper()
	var list []schools.Receipt
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = w.s.Receipts(ctx, tx, w.env.Tenant, f)
		return err
	})
	return list
}

func (w *cashWorld) setCashThreshold(kobo *int64) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetCashThreshold(ctx, tx, w.env.Tenant, kobo, "user:owner")
		return err
	})
}

func (w *cashWorld) decide(id uuid.UUID, approve bool, reason, by string) (schools.Decision, error) {
	w.t.Helper()
	var d schools.Decision
	err := w.try(func(tx pgx.Tx) error {
		var err error
		d, err = w.s.Decide(ctx, tx, w.env.Tenant, id, approve, reason, by)
		return err
	})
	if err == nil {
		err = w.s.DecidedAfter(ctx, w.env.Tenant, d)
	}
	return d, err
}

var receiptNumberRe = regexp.MustCompile(`^RCT-2026T1-\d{4}$`)

// Cash posted at the desk under the threshold is counted at once towards
// the student's open invoices, oldest first, with a receipt per part
// numbered per term; what is left is the family's credit; a part undone
// voids its receipt, which is never deleted.
func TestCashPostingsAndReceipts(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	// Nothing to pay at the desk for an unknown student or a bad amount.
	if _, err := w.post("GCA/999", 100_000, "cash", "user:ada"); !errors.Is(err, schools.ErrNotFound) {
		t.Fatalf("unknown student: %v", err)
	}
	if _, err := w.post("GCA/001", 50, "cash", "user:ada"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("50 kobo: %v", err)
	}
	if _, err := w.post("GCA/001", 100_000, "cheque", "user:ada"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("how: %v", err)
	}
	// Part of Ebube's fees in cash.
	p, err := w.post("GCA/001", 4_000_000, "cash", "user:ada")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != schools.StatePosted || p.Kind != schools.KindCashPosting || p.Result == nil || len(p.Result.Parts) != 1 || p.Result.Parts[0].AmountKobo != 4_000_000 ||
		p.Result.Parts[0].Number != w.invA.Number || p.Result.CreditKobo != 0 || p.AdmissionNumber != "GCA/001" || p.Name != "Ebube Eze" || p.Class != "JSS 1" {
		t.Fatalf("posting %+v %+v", p, p.Result)
	}
	rc := p.Result.Parts[0]
	if !receiptNumberRe.MatchString(rc.ReceiptNumber) || !strings.HasSuffix(rc.ReceiptNumber, "-0001") {
		t.Fatalf("receipt number %q", rc.ReceiptNumber)
	}
	if x := w.invoiceOf(w.invA.ID); x.Status != "part_paid" || x.PaidKobo != 4_000_000 || len(x.Payments) != 1 || x.Payments[0].How != "manual" || x.Payments[0].By != "user:ada" {
		t.Fatalf("Ebube's invoice %+v", x)
	}
	// The money log has the entry, entered, as the student's money.
	if n := w.count(`SELECT count(*) FROM money_entries WHERE kind = 'cash' AND channel = 'cash' AND source = 'entered' AND direction = 'in' AND amount_kobo = 4000000`); n != 1 {
		t.Fatalf("%d cash entries", n)
	}
	if n := w.count(`SELECT count(*) FROM money_entry_customers WHERE customer_id = $1`, "key:"+schools.StudentKey(w.a.ID)); n != 1 {
		t.Fatalf("%d customer rows", n)
	}
	// The receipt: its record, its link, the balance after it.
	list := w.receipts(schools.ReceiptFilter{Student: &w.a.ID})
	if len(list) != 1 || list[0].Number != rc.ReceiptNumber || list[0].AmountKobo != 4_000_000 || list[0].BalanceAfterKobo != 7_800_000 || list[0].Voided ||
		list[0].InvoiceNumber != w.invA.Number || list[0].Name != "Ebube Eze" || list[0].Class != "JSS 1" || list[0].Term != "First term 2026/2027" || !strings.HasPrefix(list[0].Link, "https://forms.test/r/") {
		t.Fatalf("receipts %+v", list)
	}
	token := strings.TrimPrefix(list[0].Link, "https://forms.test/r/")
	if len(token) != 32 || strings.Contains(token, rc.ReceiptNumber) {
		t.Fatalf("token %q", token)
	}
	// By number, in any case.
	w.in(func(tx pgx.Tx) error {
		got, err := w.s.ReceiptByID(ctx, tx, tn, strings.ToLower(rc.ReceiptNumber))
		if err != nil || got.ID != list[0].ID {
			t.Fatalf("by number: %v %+v", err, got)
		}
		if _, err := w.s.ReceiptByID(ctx, tx, tn, "RCT-2026T1-9999"); !errors.Is(err, schools.ErrNotFound) {
			t.Fatalf("a guessed number: %v", err)
		}
		// Its PDF renders, with its number in the document's title.
		pdf, r, err := w.s.ReceiptPDF(ctx, tx, tn, rc.ReceiptNumber)
		if err != nil || r.Number != rc.ReceiptNumber || !strings.HasPrefix(string(pdf), "%PDF-1.4") || !strings.Contains(string(pdf), "/Title (Receipt "+rc.ReceiptNumber+")") {
			t.Fatalf("pdf: %v %d bytes", err, len(pdf))
		}
		return nil
	})

	// A transfer for more than Ebube owes: the rest of her invoice, then
	// (the school does not share credit) the family's credit.
	p2, err := w.post(w.a.ID.String(), 9_000_000, "transfer", "user:ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Result.Parts) != 1 || p2.Result.Parts[0].AmountKobo != 7_800_000 || p2.Result.CreditKobo != 1_200_000 || !strings.HasSuffix(p2.Result.Parts[0].ReceiptNumber, "-0002") {
		t.Fatalf("second posting %+v", p2.Result)
	}
	if x := w.invoiceOf(w.invA.ID); x.Status != "paid" {
		t.Fatalf("Ebube's invoice %s", x.Status)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE kind = 'statement' AND channel = 'bank'`); n != 1 {
		t.Fatalf("%d transfer entries", n)
	}
	// Chidi's invoice is untouched; his own posting gets the next number.
	if x := w.invoiceOf(w.invB.ID); x.PaidKobo != 0 {
		t.Fatalf("Chidi paid %d", x.PaidKobo)
	}
	p3, err := w.post("GCA/002", 1_000_000, "pos", "user:ada")
	if err != nil || !strings.HasSuffix(p3.Result.Parts[0].ReceiptNumber, "-0003") {
		t.Fatalf("Chidi's posting %v %+v", err, p3.Result)
	}

	// The same key again is the same posting, not a second one.
	in := schools.PostingIn{Student: "GCA/002", AmountKobo: 500_000, How: "cash", Key: "req-1"}
	first, _, err := w.s.PostPayment(ctx, tn, in, "user:ada")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := w.s.PostPayment(ctx, tn, in, "user:ada")
	if err != nil || again.ID != first.ID || !first.Created || again.Created {
		t.Fatalf("retried: %v %v %v (created %v, %v)", err, first.ID, again.ID, first.Created, again.Created)
	}
	if n := w.count(`SELECT count(*) FROM school_receipts`); n != 4 {
		t.Fatalf("%d receipts", n)
	}

	// Undoing Chidi's first part voids its receipt; the number stays, and
	// the row cannot be deleted.
	w.in(func(tx pgx.Tx) error {
		x, err := w.s.Invoices.Get(ctx, tx, tn, w.invB.ID.String())
		if err != nil {
			return err
		}
		_, err = w.s.Invoices.UndoPayment(ctx, tx, tn, w.invB.ID.String(), x.Payments[0].ID, "user:ada")
		return err
	})
	voided := w.receipts(schools.ReceiptFilter{Number: p3.Result.Parts[0].ReceiptNumber})
	if len(voided) != 1 || !voided[0].Voided || voided[0].VoidReason != "the payment was undone" || voided[0].VoidedAt == nil {
		t.Fatalf("after the undo %+v", voided)
	}
	// Not even the database's owner deletes one (the app role may not try).
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM school_receipts WHERE id = $1`, voided[0].ID); err == nil || !strings.Contains(err.Error(), "never deleted") {
		t.Fatalf("deleting a receipt: %v", err)
	}
	// Money that went back (the matcher's hook) voids too, once.
	w.in(func(tx pgx.Tx) error {
		return w.s.ReceiptsReversed(ctx, tx, tn, uuid.Nil, []uuid.UUID{list[0].PartID})
	})
	if r := w.receipts(schools.ReceiptFilter{Number: rc.ReceiptNumber}); len(r) != 1 || !r[0].Voided || r[0].VoidReason != "the money went back to the payer" {
		t.Fatalf("reversed %+v", r)
	}
	// The list, filtered.
	if all := w.receipts(schools.ReceiptFilter{Term: &w.first.ID}); len(all) != 4 {
		t.Fatalf("%d receipts for the term", len(all))
	}
	if inv := w.receipts(schools.ReceiptFilter{Invoice: &w.invB.ID}); len(inv) != 2 {
		t.Fatalf("%d receipts for Chidi's invoice", len(inv))
	}
}

// At or above the cash threshold a posting waits for a second person:
// nothing reaches the fees until someone else approves it, once;
// declined, it records the reason and changes nothing; the requester
// never decides their own.
func TestCashPostingNeedsASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	fifty := int64(5_000_000)
	w.setCashThreshold(&fifty)
	// Under it: posted at once.
	if p, err := w.post("GCA/001", 4_999_900, "cash", "user:ada"); err != nil || p.Status != schools.StatePosted {
		t.Fatalf("under the threshold: %v %+v", err, p)
	}
	// At it: pending.
	p, _, err := w.s.PostPayment(ctx, tn, schools.PostingIn{Student: "GCA/002", AmountKobo: 5_000_000, How: "cash", Key: "big-1"}, "user:ada")
	var needs *schools.ApprovalNeeded
	if !errors.As(err, &needs) || !errors.Is(err, schools.ErrNeedsApproval) || p.Status != schools.StatePending || needs.Request.ID != p.ID {
		t.Fatalf("at the threshold: %v %+v", err, p)
	}
	if x := w.invoiceOf(w.invB.ID); x.PaidKobo != 0 || len(x.Payments) != 0 {
		t.Fatalf("pending money reached the fees: %+v", x)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE kind = 'cash'`); n != 1 {
		t.Fatalf("%d cash entries (the pending one must not be in the log)", n)
	}
	// The same key again: the same pending request.
	_, _, err = w.s.PostPayment(ctx, tn, schools.PostingIn{Student: "GCA/002", AmountKobo: 5_000_000, How: "cash", Key: "big-1"}, "user:ada")
	if !errors.As(err, &needs) || needs.Request.ID != p.ID {
		t.Fatalf("retried: %v", err)
	}
	// Listed as pending.
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Postings(ctx, tx, tn, schools.PostingFilter{Status: schools.StatePending})
		if err != nil || len(list) != 1 || list[0].ID != p.ID || list[0].Details["how"] != "cash" {
			t.Fatalf("pending %v %+v", err, list)
		}
		return nil
	})
	// Not by the requester; not declined without a reason.
	if _, err := w.decide(p.ID, true, "", "user:ada"); !errors.Is(err, schools.ErrState) || !strings.Contains(err.Error(), "own request") {
		t.Fatalf("self-approval: %v", err)
	}
	if _, err := w.decide(p.ID, false, "", "user:bola"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("declined without a reason: %v", err)
	}
	// Approved by a second person: posted, with its receipt; once.
	d, err := w.decide(p.ID, true, "", "user:bola")
	if err != nil {
		t.Fatal(err)
	}
	if d.Request.Status != schools.StatePosted || d.Request.DecidedBy != "user:bola" || d.Request.Result == nil || len(d.Request.Result.Parts) != 1 ||
		d.Request.Result.Parts[0].AmountKobo != 5_000_000 || d.Request.Result.Parts[0].ReceiptNumber == "" {
		t.Fatalf("approved %+v %+v", d.Request, d.Request.Result)
	}
	if x := w.invoiceOf(w.invB.ID); x.PaidKobo != 5_000_000 || x.Payments[0].By != "user:ada" {
		t.Fatalf("after approval %+v", x)
	}
	if _, err := w.decide(p.ID, true, "", "user:bola"); !errors.Is(err, schools.ErrState) {
		t.Fatalf("decided twice: %v", err)
	}
	if _, err := w.decide(p.ID, false, "no", "user:chi"); !errors.Is(err, schools.ErrState) {
		t.Fatalf("declined after approval: %v", err)
	}
	// Declined: the reason, and nothing posted.
	p2, _, err := w.s.PostPayment(ctx, tn, schools.PostingIn{Student: "GCA/002", AmountKobo: 6_000_000, How: "transfer"}, "user:ada")
	if !errors.As(err, &needs) {
		t.Fatalf("second request: %v", err)
	}
	d2, err := w.decide(p2.ID, false, "No transfer on the statement", "user:bola")
	if err != nil || d2.Request.Status != schools.StateDeclined || d2.Request.DecisionReason != "No transfer on the statement" || d2.Request.Result != nil {
		t.Fatalf("declined %v %+v", err, d2.Request)
	}
	if x := w.invoiceOf(w.invB.ID); x.PaidKobo != 5_000_000 {
		t.Fatalf("declined money reached the fees: %d", x.PaidKobo)
	}
	// A threshold of 0: every posting waits. Unset: none does.
	zero := int64(0)
	w.setCashThreshold(&zero)
	if _, err := w.post("GCA/002", 100, "cash", "user:ada"); !errors.Is(err, schools.ErrNeedsApproval) {
		t.Fatalf("threshold 0: %v", err)
	}
	w.setCashThreshold(nil)
	if p, err := w.post("GCA/002", 100_000_000, "cash", "user:ada"); err != nil || p.Status != schools.StatePosted {
		t.Fatalf("no threshold: %v %+v", err, p)
	}
}

// Discounts at or above the discount threshold wait the same way: a
// standing discount is given on approval, a discount amendment is made
// then, and a student held out of the term's plan gets their invoice when
// a second person approves.
func TestDiscountsNeedASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetDiscountThreshold(ctx, tx, tn, 2_000_000, "user:owner")
		return err
	})
	c := w.student("Nkem Eze", "GCA/003", w.j1, &w.fam.ID)
	two := int64(2_000_000)
	var needs *schools.ApprovalNeeded
	// A standing discount at the threshold: a request, not a discount.
	err := w.try(func(tx pgx.Tx) error {
		_, _, err := w.s.AddDiscount(ctx, tx, tn, c.ID, schools.DiscountIn{Kind: "hardship", AmountKobo: &two, Reason: "Hard year"}, "user:ada")
		if !errors.As(err, &needs) {
			t.Fatalf("AddDiscount: %v", err)
		}
		return nil // the request commits
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM school_discounts WHERE student_id = $1`, c.ID); n != 0 {
		t.Fatalf("%d discounts before approval", n)
	}
	req := needs.Request
	if req.Kind != schools.KindDiscount || req.AmountKobo != 2_000_000 || req.Details["kind"] != "hardship" {
		t.Fatalf("request %+v", req)
	}
	// Under it: given at once.
	one := int64(1_999_900)
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.s.AddDiscount(ctx, tx, tn, c.ID, schools.DiscountIn{Kind: "other", AmountKobo: &one, Reason: "Small"}, "user:ada")
		return err
	})
	// Approved: the discount stands.
	if _, err := w.decide(req.ID, true, "", "user:bola"); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		ds, err := w.s.Discounts(ctx, tx, tn, c.ID)
		if err != nil || len(ds) != 2 {
			t.Fatalf("discounts %v %+v", err, ds)
		}
		return nil
	})
	// The plan holds Nkem for the discount at the threshold; confirming it
	// records a request, once; approval makes the invoice.
	p := w.plan()
	if len(p.NeedsApproval) != 1 || p.NeedsApproval[0].ID != c.ID || p.ToInvoice != 0 {
		t.Fatalf("plan %+v", p)
	}
	if done := w.invoice(p.Hash); done.Requested != 1 || done.Created != 0 {
		t.Fatalf("invoiced %+v", done)
	}
	if done := w.invoice(""); done.Requested != 0 {
		t.Fatalf("requested twice %+v", done)
	}
	var held schools.Posting
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Postings(ctx, tx, tn, schools.PostingFilter{Kind: schools.KindTermInvoice})
		if err != nil || len(list) != 1 {
			t.Fatalf("held %v %+v", err, list)
		}
		held = list[0]
		return nil
	})
	if held.TermID == nil || *held.TermID != w.first.ID || held.AmountKobo != 11_800_000-2_000_000-1_999_900 {
		t.Fatalf("held request %+v", held)
	}
	d, err := w.decide(held.ID, true, "", "user:bola")
	if err != nil || d.Request.Result == nil || d.Request.Result.InvoiceID == nil {
		t.Fatalf("approving the held invoice: %v %+v", err, d.Request)
	}
	if n := w.count(`SELECT count(*) FROM school_term_invoices WHERE student_id = $1`, c.ID); n != 1 {
		t.Fatalf("%d term invoices for Nkem", n)
	}
	if p := w.plan(); p.AlreadyInvoiced != 3 || len(p.NeedsApproval) != 0 {
		t.Fatalf("plan after %+v", p)
	}
	// A discount amendment on a sent invoice at the threshold: a request;
	// under it, the caller amends.
	err = w.try(func(tx pgx.Tx) error {
		err := w.s.RequestDiscountAmendment(ctx, tx, tn, w.invA.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 2_500_000, Reason: "Hardship"}, "user:ada")
		if !errors.As(err, &needs) {
			t.Fatalf("amendment request: %v", err)
		}
		if err := w.s.RequestDiscountAmendment(ctx, tx, tn, w.invA.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 100_000, Reason: "Small"}, "user:ada"); err != nil {
			t.Fatalf("a small amendment: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if x := w.invoiceOf(w.invA.ID); len(x.Amendments) != 0 {
		t.Fatalf("amended before approval: %+v", x.Amendments)
	}
	if needs.Request.StudentID == nil || *needs.Request.StudentID != w.a.ID || needs.Request.InvoiceNumber != w.invA.Number {
		t.Fatalf("amendment request %+v", needs.Request)
	}
	if _, err := w.decide(needs.Request.ID, true, "", "user:bola"); err != nil {
		t.Fatal(err)
	}
	if x := w.invoiceOf(w.invA.ID); len(x.Amendments) != 1 || x.TotalKobo != 11_800_000-2_500_000 || x.Amendments[0].By != "user:ada" {
		t.Fatalf("amended %+v", x)
	}
}

// Receipt numbers are unique under concurrency: postings made at once
// for the same term never share a number.
func TestReceiptNumbersUnderConcurrency(t *testing.T) {
	w := newCashWorld(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			student := "GCA/001"
			if i%2 == 1 {
				student = "GCA/002"
			}
			if _, err := w.post(student, 100_000, "cash", "user:ada"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n, d := w.count(`SELECT count(*) FROM school_receipts`), w.count(`SELECT count(DISTINCT number) FROM school_receipts`); n != 12 || d != 12 {
		t.Fatalf("%d receipts, %d distinct numbers", n, d)
	}
	if top := w.count(`SELECT max(next) FROM school_receipt_counters`); top != 13 {
		t.Fatalf("counter at %d", top)
	}
}

// Money a person entered on Money (cash, or a statement line) is the same
// money as cash posted at the desk: matched by hand to a school's invoice
// at or above the cash threshold, it waits for a second person, and
// automatic matching never counts it on its own.
func TestEnteredMoneyMatchedByHandNeedsASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	fifty := int64(5_000_000)
	w.setCashThreshold(&fifty)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	m := &matching.Matcher{Invoices: w.s.Invoices, PII: w.env.Vault}
	enter := func(kobo int64) uuid.UUID {
		t.Helper()
		id, _, err := log.Enter(ctx, tn, "user:ada", moneylog.Entered{Kind: "cash", Direction: "in", AmountKobo: kobo, Description: "School fees"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	big := enter(6_000_000)
	_, err := m.Match(ctx, tn, big, w.invA.Number, "user:ada")
	var held *matching.Held
	if !errors.As(err, &held) || held.AmountKobo != 6_000_000 {
		t.Fatalf("matched by hand at the threshold: %v", err)
	}
	if x := w.invoiceOf(w.invA.ID); x.PaidKobo != 0 {
		t.Fatalf("held money reached the fees: %+v", x)
	}
	if _, err := m.Match(ctx, tn, big, w.invA.Number, "user:ada"); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("matched again while it waits: %v", err)
	}
	var req schools.Posting
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Postings(ctx, tx, tn, schools.PostingFilter{Status: schools.StatePending, Kind: schools.KindCashMatch})
		if err != nil || len(list) != 1 || list[0].ID != held.RequestID || list[0].InvoiceNumber != w.invA.Number || list[0].StudentID == nil || *list[0].StudentID != w.a.ID {
			t.Fatalf("pending %v %+v", err, list)
		}
		req = list[0]
		return nil
	})
	if _, err := w.decide(req.ID, true, "", "user:ada"); !errors.Is(err, schools.ErrState) {
		t.Fatalf("self-approval: %v", err)
	}
	d, err := w.decide(req.ID, true, "", "user:bola")
	if err != nil || d.Request.Status != schools.StatePosted || d.Request.Result == nil || len(d.Request.Result.Parts) != 1 {
		t.Fatalf("approved %v %+v", err, d.Request)
	}
	if x := w.invoiceOf(w.invA.ID); x.PaidKobo != 6_000_000 || x.Payments[0].By != "user:ada" {
		t.Fatalf("after approval %+v", x)
	}
	// Another of the business's invoices, not a student's: not held.
	var shop invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		shop, err = w.s.Invoices.Create(ctx, tx, tn, invoicing.Draft{Customer: invoicing.Customer{Name: "Tuck shop", Phone: "+2348031234567"}, AmountKobo: 9_000_000}, "user:ada")
		return err
	})
	if _, err := w.s.Invoices.Send(ctx, tn, shop.Number, "user:ada", "send:"+shop.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Match(ctx, tn, enter(9_000_000), shop.Number, "user:ada"); err != nil {
		t.Fatalf("a shop's invoice was held: %v", err)
	}
	// Under the threshold: matched at once.
	if _, err := m.Match(ctx, tn, enter(100_000), w.invA.Number, "user:ada"); err != nil {
		t.Fatalf("under the threshold: %v", err)
	}
	// Suggested by matching: not confirmed on its own; a person's
	// confirmation waits for a second person.
	enter(11_800_000)
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var sug uuid.UUID
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT s.id FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id WHERE e.amount_kobo = 11800000 AND s.invoice_id = $1`, w.invB.ID).Scan(&sug)
	})
	if _, err := m.Confirm(ctx, tn, sug, "system:matching", true); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("confirmed on its own: %v", err)
	}
	if _, err := m.Confirm(ctx, tn, sug, "user:ada", false); !errors.As(err, &held) {
		t.Fatalf("confirmed by a person: %v", err)
	}
	if x := w.invoiceOf(w.invB.ID); x.PaidKobo != 0 {
		t.Fatalf("held money reached the fees: %+v", x)
	}
}

// A line taken out lowers the invoice as a discount does, and waits for a
// second person the same way, by the line's amount. Reductions of one
// invoice add up: amendments split under the threshold are weighed with
// those the invoice took already without a second person.
func TestRemovalsAndSplitReductionsNeedASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetDiscountThreshold(ctx, tx, tn, 2_000_000, "user:owner")
		return err
	})
	line := 0
	amend := func(in invoicing.AmendmentIn) *schools.ApprovalNeeded {
		t.Helper()
		var needs *schools.ApprovalNeeded
		err := w.try(func(tx pgx.Tx) error {
			err := w.s.RequestDiscountAmendment(ctx, tx, tn, w.invA.Number, in, "user:ada")
			if errors.As(err, &needs) {
				return nil // the request commits
			}
			if err != nil {
				return err
			}
			_, err = w.s.Invoices.Amend(ctx, tx, tn, w.invA.Number, in, "user:ada")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return needs
	}
	// Taking out the ₦100,000 tuition line: a request for its amount.
	needs := amend(invoicing.AmendmentIn{Kind: invoicing.AmendRemove, Line: &line, Reason: "Scholarship"})
	if needs == nil || needs.Request.Kind != schools.KindDiscountAmendment || needs.Request.AmountKobo != 10_000_000 {
		t.Fatalf("removal: %+v", needs)
	}
	if x := w.invoiceOf(w.invA.ID); x.TotalKobo != 11_800_000 {
		t.Fatalf("removed before approval: %d", x.TotalKobo)
	}
	// ₦15,000 then ₦6,000: the second brings the invoice's reductions to
	// the threshold.
	if n := amend(invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 1_500_000, Reason: "Sibling"}); n != nil {
		t.Fatalf("under the threshold: %+v", n.Request)
	}
	split := amend(invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 600_000, Reason: "Sibling, again"})
	if split == nil || split.Request.AmountKobo != 600_000 {
		t.Fatal("a reduction split under the threshold was not held")
	}
	// An added line lowers nothing.
	if n := amend(invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Lost textbook", AmountKobo: 300_000, Reason: "Lost it"}); n != nil {
		t.Fatalf("an added line: %+v", n.Request)
	}
	// Approved reductions are the second person's: not weighed again.
	if _, err := w.decide(split.Request.ID, true, "", "user:bola"); err != nil {
		t.Fatal(err)
	}
	if n := amend(invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 400_000, Reason: "Small"}); n != nil {
		t.Fatalf("after approval: %+v", n.Request)
	}
	if _, err := w.decide(needs.Request.ID, false, "No scholarship this term", "user:bola"); err != nil {
		t.Fatal(err)
	}
	if x := w.invoiceOf(w.invA.ID); x.TotalKobo != 11_800_000-1_500_000-600_000+300_000-400_000 {
		t.Fatalf("amended %d", x.TotalKobo)
	}
}

// Cash split under the threshold adds up per invoice: entered money
// matched twice to one invoice, and desk postings for one student.
func TestSplitCashNeedsASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	fifty := int64(5_000_000)
	w.setCashThreshold(&fifty)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	m := &matching.Matcher{Invoices: w.s.Invoices, PII: w.env.Vault}
	match := func(kobo int64) error {
		t.Helper()
		id, _, err := log.Enter(ctx, tn, "user:ada", moneylog.Entered{Kind: "cash", Direction: "in", AmountKobo: kobo, Description: "School fees"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = m.Match(ctx, tn, id, w.invA.Number, "user:ada")
		return err
	}
	if err := match(3_000_000); err != nil {
		t.Fatalf("the first part: %v", err)
	}
	if err := match(3_000_000); !errors.Is(err, matching.ErrHeld) {
		t.Fatalf("the second part: %v", err)
	}
	if p, err := w.post("GCA/002", 3_000_000, "cash", "user:ada"); err != nil || p.Status != schools.StatePosted {
		t.Fatalf("the first posting: %v %+v", err, p)
	}
	if _, err := w.post("GCA/002", 3_000_000, "cash", "user:ada"); !errors.Is(err, schools.ErrNeedsApproval) {
		t.Fatalf("the second posting: %v", err)
	}
}

// The cash threshold weighs a student's entered money together across
// their invoices, within the window (the current term, and at least the
// last day): money split over two of a student's invoices adds up, and
// another student's money does not count towards theirs.
func TestCashSplitOverAStudentsInvoicesNeedsASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	fifty := int64(5_000_000)
	w.setCashThreshold(&fifty)
	// The second term started and invoiced: Ebube has two open invoices.
	w.start(w.second)
	w.setFees(w.second, w.j1, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required}})
	var second uuid.UUID
	w.in(func(tx pgx.Tx) error {
		if _, err := w.s.InvoiceTerm(ctx, tx, tn, w.second.ID, "", false, "user:ada"); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT invoice_id FROM school_term_invoices WHERE term_id = $1 AND student_id = $2`, w.second.ID, w.a.ID).Scan(&second)
	})
	if _, failed, err := w.s.SendTerm(ctx, tn, w.second.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("sending the second term: %v %v", err, failed)
	}
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	m := &matching.Matcher{Invoices: w.s.Invoices, PII: w.env.Vault}
	match := func(kobo int64, invoice string) error {
		t.Helper()
		id, _, err := log.Enter(ctx, tn, "user:ada", moneylog.Entered{Kind: "cash", Direction: "in", AmountKobo: kobo, Description: "School fees"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = m.Match(ctx, tn, id, invoice, "user:ada")
		return err
	}
	// Chidi's money is his: it does not count towards Ebube's.
	if err := match(3_000_000, w.invB.Number); err != nil {
		t.Fatalf("Chidi's part: %v", err)
	}
	if err := match(3_000_000, w.invA.Number); err != nil {
		t.Fatalf("Ebube's first part: %v", err)
	}
	// Ebube's second part, on his other invoice, brings his money to the
	// threshold.
	if err := match(3_000_000, second.String()); !errors.Is(err, matching.ErrHeld) {
		t.Fatalf("Ebube's second part, on another invoice: %v", err)
	}
	if x := w.invoiceOf(second); x.PaidKobo != 0 {
		t.Fatalf("held money reached the fees: %+v", x)
	}
}

// The discount threshold weighs a student's standing discounts and the
// reductions of their invoices together, within the window: discounts
// split under it add up, another student's do not count, nor do those a
// second person approved or those given before the window.
func TestSplitDiscountsNeedASecondPerson(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetDiscountThreshold(ctx, tx, tn, 2_000_000, "user:owner")
		return err
	})
	c := w.student("Nkem Eze", "GCA/003", w.j1, &w.fam.ID)
	d := w.student("Tobi Ade", "GCA/004", w.j1, nil)
	give := func(student uuid.UUID, kobo int64) *schools.ApprovalNeeded {
		t.Helper()
		var needs *schools.ApprovalNeeded
		err := w.try(func(tx pgx.Tx) error {
			_, _, err := w.s.AddDiscount(ctx, tx, tn, student, schools.DiscountIn{Kind: "other", AmountKobo: &kobo, Reason: "Help"}, "user:ada")
			if errors.As(err, &needs) {
				return nil // the request commits
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return needs
	}
	if n := give(c.ID, 1_500_000); n != nil {
		t.Fatalf("Nkem's first discount: %+v", n.Request)
	}
	if n := give(d.ID, 1_500_000); n != nil {
		t.Fatalf("Tobi's discount counted Nkem's: %+v", n.Request)
	}
	split := give(c.ID, 1_500_000)
	if split == nil || split.Request.Kind != schools.KindDiscount || split.Request.AmountKobo != 1_500_000 {
		t.Fatalf("a discount split under the threshold was not held: %+v", split)
	}
	// Approved, it is the second person's: not weighed again.
	if _, err := w.decide(split.Request.ID, true, "", "user:bola"); err != nil {
		t.Fatal(err)
	}
	if n := give(c.ID, 400_000); n != nil {
		t.Fatalf("after approval: %+v", n.Request)
	}
	// Given before the window, a discount no longer counts.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE school_discounts SET created_at = now() - interval '2 days' WHERE student_id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := give(c.ID, 1_900_000); n != nil {
		t.Fatalf("a discount before the window counted: %+v", n.Request)
	}
	// A standing discount and an amendment of the student's invoice add up.
	if n := give(w.a.ID, 1_000_000); n != nil {
		t.Fatalf("Ebube's discount: %+v", n.Request)
	}
	var needs *schools.ApprovalNeeded
	err := w.try(func(tx pgx.Tx) error {
		err := w.s.RequestDiscountAmendment(ctx, tx, tn, w.invA.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 1_000_000, Reason: "Hardship"}, "user:ada")
		if errors.As(err, &needs) {
			return nil
		}
		return err
	})
	if err != nil || needs == nil || needs.Request.AmountKobo != 1_000_000 {
		t.Fatalf("an amendment with a standing discount: %v %+v", err, needs)
	}
}
