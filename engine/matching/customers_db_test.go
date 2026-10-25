package matching_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/wd"
)

// invoiceFor sends an invoice of kobo to a customer, due on due
// (YYYY-MM-DD, or "").
func (w *world) invoiceFor(c invoicing.Customer, kobo int64, due string) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.inv.Create(ctx, tx, w.env.Tenant, invoicing.Draft{Customer: c, AmountKobo: kobo, DueOn: due}, "user:ada")
		return err
	})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "send:"+x.Number); err != nil {
		w.t.Fatal(err)
	}
	return w.read(x.Number)
}

// register links a permanent account to a customer.
func (w *world) register(c invoicing.Customer, number, reference string) invoicing.CustomerAccount {
	w.t.Helper()
	var a invoicing.CustomerAccount
	w.in(func(tx pgx.Tx) error {
		var err error
		a, err = w.inv.RegisterAccount(ctx, tx, w.env.Tenant, invoicing.AccountIn{Customer: c, Channel: "paystack/main", AccountNumber: number, Reference: reference,
			BankName: "Wema Bank"}, "user:ada")
		return err
	})
	return a
}

// credit records a credit into an account, as the rail's
// virtual_account.credited event would be, and lets matching look.
func (w *world) credit(e moneylog.Entry) uuid.UUID {
	w.t.Helper()
	e.Direction, e.Kind, e.Status, e.Channel, e.Event, e.Source = "in", "account_credit", "settled", "paystack/main", "virtual_account.credited", "event"
	id, _, err := w.log.Record(ctx, w.env.Tenant, e)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.m.Tick(ctx); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func kobo(n int64) *int64 { return &n }

func (w *world) creditOf(customer string) int64 {
	w.t.Helper()
	var n int64
	w.in(func(tx pgx.Tx) error {
		var err error
		n, err = invoicing.CreditOf(ctx, tx, w.env.Tenant, customer)
		return err
	})
	return n
}

func (w *world) unmatched() []matching.Payment {
	w.t.Helper()
	var out []matching.Payment
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = matching.Unmatched(ctx, tx, w.env.Tenant, 50)
		return err
	})
	return out
}

// receipts publishes a workflow that starts when an invoice is paid, and
// counts the starts queued for an invoice (the invoice.paid outbox).
func (w *world) receipts() func(invoice uuid.UUID) int {
	w.t.Helper()
	const receiptWD = `{"schema":"wd/v1","id":"wf_receipt","version":1,"name":"Receipt","trigger":{"type":"invoice","config":{"event":"paid"}},
	 "steps":[{"id":"t","type":"transform","config":{"output":{"number":"=trigger.body.number"}}}]}`
	wf := w.env.Publish(w.t, receiptWD)
	def, err := wd.Load([]byte(receiptWD))
	if err != nil {
		w.t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, w.env.Tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	})
	return func(invoice uuid.UUID) int {
		w.t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM invoice_paid_starts WHERE invoice_id = $1`, invoice).Scan(&n); err != nil {
			w.t.Fatal(err)
		}
		return n
	}
}

// A credit to a customer's permanent account pays their oldest open
// invoice, then the next; the rest is their credit, counted towards the
// next invoice sent to them. Each invoice paid queues its workflows once.
func TestPermanentAccountPaysOldestFirst(t *testing.T) {
	w := newWorld(t)
	starts := w.receipts()
	ebube := invoicing.Customer{Name: "Ebube Eze", Phone: "+2348031230001", Key: "student:1"}
	sibling := invoicing.Customer{Name: "Chioma Eze", Phone: "+2348031230001", Key: "student:2"}
	// Sent newest first: the rule goes by the due date.
	second := w.invoiceFor(ebube, 3000000, "2099-12-01")
	first := w.invoiceFor(ebube, 2000000, "2099-09-01")
	other := w.invoiceFor(sibling, 1000000, "2099-08-01") // the same parent's phone, another child
	acct := w.register(ebube, "9912345678", "tnt:student:1")
	if acct.CustomerID() != "key:student:1" || first.CustomerID() != "key:student:1" || other.CustomerID() != "key:student:2" {
		t.Fatalf("customers %q %q %q", acct.CustomerID(), first.CustomerID(), other.CustomerID())
	}
	// Registering it again is the same account; for another child, refused.
	if again := w.register(ebube, "9912345678", "tnt:student:1"); again.ID != acct.ID {
		t.Errorf("registered twice: %s %s", again.ID, acct.ID)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := w.inv.RegisterAccount(ctx, tx, w.env.Tenant, invoicing.AccountIn{Customer: sibling, Channel: "paystack/main", AccountNumber: "9912345678",
			Reference: "tnt:student:1"}, "user:ada")
		return err
	}); !errors.Is(err, invoicing.ErrState) {
		t.Errorf("another child's account: %v", err)
	}

	w.credit(moneylog.Entry{Reference: "tnt:student:1", AccountNumber: "9912345678", AmountKobo: kobo(6000000), PayerName: "EZE NGOZI"})
	a, b, o := w.read(first.Number), w.read(second.Number), w.read(other.Number)
	if a.Status != "paid" || a.PaidKobo != 2000000 || b.Status != "paid" || b.PaidKobo != 3000000 || o.PaidKobo != 0 {
		t.Fatalf("after the credit: %s %d, %s %d, sibling %s", a.Status, a.PaidKobo, b.Status, b.PaidKobo, o.Status)
	}
	p := a.Payments[0]
	if p.How != "exact" || len(p.Reasons) != 2 || p.Reasons[0] != matching.ReasonOwnAccount || p.Reasons[1] != matching.ReasonOldest {
		t.Errorf("first part %+v", p)
	}
	if r := b.Payments[0].Reasons; len(r) != 2 || r[1] != matching.ReasonNext {
		t.Errorf("second part %q", r)
	}
	for _, r := range append(p.Reasons, b.Payments[0].Reasons...) {
		if strings.Contains(strings.ToLower(r), "eze") || strings.Contains(strings.ToLower(r), "ebube") {
			t.Errorf("a reason names someone: %q", r)
		}
	}
	if n := w.creditOf("key:student:1"); n != 1000000 {
		t.Fatalf("credit %d", n)
	}
	if len(w.open()) != 0 || len(w.unmatched()) != 0 {
		t.Errorf("an exact match left work: %+v %+v", w.open(), w.unmatched())
	}

	// The next invoice sent to the customer takes the credit.
	var third invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		third, err = w.inv.Create(ctx, tx, w.env.Tenant, invoicing.Draft{Customer: ebube, AmountKobo: 1500000}, "user:ada")
		return err
	})
	sent, err := w.inv.Send(ctx, w.env.Tenant, third.Number, "user:ada", "send:third")
	if err != nil {
		t.Fatal(err)
	}
	if sent.CreditKobo != 1000000 || sent.Invoice.Status != "part_paid" || sent.Invoice.PaidKobo != 1000000 || sent.Invoice.Payments[0].Reasons[0] != invoicing.CreditReason {
		t.Fatalf("sent with credit %d %+v", sent.CreditKobo, sent.Invoice)
	}
	if n := w.creditOf("key:student:1"); n != 0 {
		t.Errorf("credit left %d", n)
	}
	// Sent again: no credit left to count.
	if again, err := w.inv.Send(ctx, w.env.Tenant, third.Number, "user:ada", "send:third:2"); err != nil || again.CreditKobo != 0 || again.Invoice.PaidKobo != 1000000 {
		t.Errorf("sent again %v %+v", err, again)
	}
	// Each paid invoice's workflows are queued once, and undoing and
	// paying again queues nothing more.
	if starts(first.ID) != 1 || starts(second.ID) != 1 || starts(third.ID) != 0 {
		t.Errorf("starts %d %d %d", starts(first.ID), starts(second.ID), starts(third.ID))
	}
	var undone invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		undone, err = w.inv.UndoPayment(ctx, tx, w.env.Tenant, first.Number, a.Payments[0].ID, "user:ada")
		return err
	})
	// The undone part is the customer's credit again, not money to match.
	if undone.Status != "overdue" && undone.Status != "sent" || w.creditOf("key:student:1") != 2000000 || len(w.unmatched()) != 0 {
		t.Fatalf("undone %s, credit %d, unmatched %+v", undone.Status, w.creditOf("key:student:1"), w.unmatched())
	}
	var fourth invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		fourth, err = w.inv.Create(ctx, tx, w.env.Tenant, invoicing.Draft{Customer: ebube, AmountKobo: 500000}, "user:ada")
		return err
	})
	if s, err := w.inv.Send(ctx, w.env.Tenant, fourth.Number, "user:ada", ""); err != nil || s.Invoice.Status != "paid" || s.CreditKobo != 500000 {
		t.Fatalf("paid from credit %v %+v", err, s)
	}
	if starts(fourth.ID) != 1 || starts(first.ID) != 1 {
		t.Errorf("starts after credit %d %d", starts(fourth.ID), starts(first.ID))
	}
}

// A credit whose account number or reference disagrees with what was
// registered is held for a person with the reason, never posted; a credit
// to an account Taskiem does not know is suggested as any other money.
func TestPermanentAccountMismatchHeld(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230002"}
	x := w.invoiceFor(ada, 2500000, "")
	w.register(ada, "9900000001", "tnt:ada")
	cases := []struct {
		e      moneylog.Entry
		reason string
	}{
		{moneylog.Entry{Reference: "tnt:ada", AccountNumber: "9900000999", AmountKobo: kobo(2500000)}, matching.HeldOtherAccount},
		{moneylog.Entry{Reference: "tnt:someone", AccountNumber: "9900000001", AmountKobo: kobo(2500000)}, matching.HeldOtherRef},
	}
	for _, c := range cases {
		w.credit(c.e)
	}
	if y := w.read(x.Number); y.Status != "sent" || len(y.Payments) != 0 {
		t.Fatalf("posted: %+v", y)
	}
	held := w.unmatched()
	if len(held) != 2 {
		t.Fatalf("held %+v", held)
	}
	got := map[string]bool{held[0].HeldReason: true, held[1].HeldReason: true}
	for _, c := range cases {
		if !got[c.reason] {
			t.Errorf("no %q in %+v", c.reason, held)
		}
	}
	if len(w.open()) != 0 {
		t.Errorf("a held credit was suggested: %+v", w.open())
	}
	// An account Taskiem does not know: suggested (same amount).
	w.credit(moneylog.Entry{Reference: "VA-UNKNOWN", AccountNumber: "1234567890", AmountKobo: kobo(2500000)})
	if open := w.open(); len(open) != 1 || open[0].Number != x.Number {
		t.Errorf("unknown account %+v", open)
	}
	if n := w.creditOf(x.CustomerID()); n != 0 {
		t.Errorf("credit from held money: %d", n)
	}
	// Another business's account is not ours: its credit here is suggested.
	other := w.env.AddTenant(t)
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{other}, func(tx pgx.Tx) error {
		_, err := w.inv.RegisterAccount(ctx, tx, other, invoicing.AccountIn{Customer: ada, Channel: "paystack/main", AccountNumber: "9911111111", Reference: "tnt:other"}, "user:x")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.credit(moneylog.Entry{Reference: "tnt:other", AccountNumber: "9911111111", AmountKobo: kobo(2500000)})
	if y := w.read(x.Number); len(y.Payments) != 0 {
		t.Errorf("another tenant's account matched here: %+v", y.Payments)
	}
}

// Money a rail settled net of its fee counts for what the payer sent.
// A rail that does not say which account number was paid into
// (Flutterwave's and Moniepoint's notices) is taken at the reference it
// issued for the account.
func TestPermanentAccountWithoutNumber(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230003"}
	x := w.invoiceFor(ada, 2500000, "")
	w.register(ada, "9900000003", "tnt:ada3")
	w.credit(moneylog.Entry{Reference: "tnt:ada3", AmountKobo: kobo(2500000)})
	if y := w.read(x.Number); y.Status != "paid" || len(y.Payments) != 1 {
		t.Fatalf("not matched by its reference: %+v", y)
	}
	if held := w.unmatched(); len(held) != 0 {
		t.Errorf("held: %+v", held)
	}
}

func TestGrossCounting(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230003"}
	x := w.invoiceFor(ada, 500000, "")
	w.register(ada, "9900000002", "tnt:gross")
	id := w.credit(moneylog.Entry{Reference: "tnt:gross", AccountNumber: "9900000002", AmountKobo: kobo(495000), FeeKobo: kobo(5000), Net: true})
	if y := w.read(x.Number); y.Status != "paid" || y.PaidKobo != 500000 {
		t.Fatalf("net credit %s %d", y.Status, y.PaidKobo)
	}
	var countable, left int64
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_money_entry_countable(amount_kobo, fee_kobo, net), taskiem_money_entry_left(id) FROM money_entries WHERE id = $1`, id).
			Scan(&countable, &left)
	})
	if want := moneylog.CountableKobo(moneylog.Entry{AmountKobo: kobo(495000), FeeKobo: kobo(5000), Net: true}); countable != want || left != 0 {
		t.Errorf("countable %d (Go %d), left %d", countable, want, left)
	}
}

// A suggestion is confirmed split across a customer's invoices; each part
// is a match of its own, and undoing one leaves the others.
func TestSplitConfirmAndPartialUndo(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230004"}
	newer := w.invoiceFor(ada, 3000000, "2099-12-01")
	older := w.invoiceFor(ada, 2000000, "2099-09-01")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-SPLIT", "amount_kobo": int64(6000000), "payer_name": "ADA OBI"})
	open := w.open()
	if len(open) == 0 {
		t.Fatal("no suggestion")
	}
	var prop matching.Proposal
	w.in(func(tx pgx.Tx) error {
		var err error
		prop, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, false)
		return err
	})
	if len(prop.Parts) != 2 || prop.Parts[0].Number != older.Number || prop.Parts[0].AmountKobo != 2000000 || prop.Parts[1].AmountKobo != 3000000 || prop.CreditKobo != 1000000 || !prop.Over {
		t.Fatalf("proposal %+v", prop)
	}
	// For To match, the suggested invoice first, the same amounts.
	var first matching.Proposal
	w.in(func(tx pgx.Tx) error {
		var err error
		first, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, true)
		return err
	})
	if len(first.Parts) != 2 || first.Parts[0].Number != open[0].Number || first.Parts[0].AmountKobo != w.read(open[0].Number).DueKobo || first.CreditKobo != 1000000 {
		t.Fatalf("proposal, suggested first %+v (suggested %s)", first, open[0].Number)
	}
	// More than the payment: refused.
	if _, err := w.m.ConfirmParts(ctx, w.env.Tenant, open[0].ID, []matching.Part{{Invoice: older.Number, AmountKobo: 4000000}, {Invoice: newer.Number, AmountKobo: 3000000}}, false, "user:ada"); !errors.Is(err, invoicing.ErrInvalid) {
		t.Fatalf("too much: %v", err)
	}
	// Edited: the newer invoice in full, the older in part; the rest waits.
	s, err := w.m.ConfirmParts(ctx, w.env.Tenant, open[0].ID, []matching.Part{{Invoice: newer.Number, AmountKobo: 3000000}, {Invoice: older.Number, AmountKobo: 1500000}}, false, "user:ada")
	if err != nil || len(s.Parts) != 2 || s.LeftKobo != 1500000 || s.CreditKobo != 0 {
		t.Fatalf("split %v %+v", err, s)
	}
	n, o := w.read(newer.Number), w.read(older.Number)
	if n.Status != "paid" || o.Status != "part_paid" || o.PaidKobo != 1500000 || n.Payments[0].How != "suggested" {
		t.Fatalf("after the split %s %s %d", n.Status, o.Status, o.PaidKobo)
	}
	if o.Payments[0].Reasons[0] != matching.SplitReason && n.Payments[0].Reasons[0] != matching.SplitReason {
		t.Errorf("no split reason: %q %q", n.Payments[0].Reasons, o.Payments[0].Reasons)
	}
	left := w.unmatched()
	if len(left) != 1 || left[0].LeftKobo == nil || *left[0].LeftKobo != 1500000 || len(w.open()) != 0 {
		t.Fatalf("the rest %+v %+v", left, w.open())
	}
	// The rest by hand: a second part of the same payment towards the
	// older invoice adds to the first (a row of its own), and the rest
	// goes to a third invoice.
	third := w.invoiceFor(ada, 1000000, "")
	if s, err := w.m.MatchParts(ctx, w.env.Tenant, left[0].EntryID, []matching.Part{{Invoice: older.Number, AmountKobo: 500000}, {Invoice: third.Number, AmountKobo: 1000000}}, false, "user:ada"); err != nil || len(s.Parts) != 2 || s.LeftKobo != 0 {
		t.Fatalf("rest %v %+v", err, s)
	}
	if o := w.read(older.Number); o.Status != "paid" || o.PaidKobo != 2000000 || len(o.Payments) != 2 || o.Payments[0].AmountKobo+o.Payments[1].AmountKobo != 2000000 {
		t.Fatalf("a second part towards the same invoice: %s %d %+v", o.Status, o.PaidKobo, o.Payments)
	}
	if len(w.unmatched()) != 0 {
		t.Errorf("nothing should be left: %+v", w.unmatched())
	}
	// Nothing left: the same match again is a no-op, and any more towards
	// another invoice is refused, naming what the payment counts towards.
	if r, err := w.m.Match(ctx, w.env.Tenant, left[0].EntryID, third.Number, "user:ada"); err != nil || r.Invoice == nil || len(r.Invoice.Payments) != 1 {
		t.Fatalf("again: %v %+v", err, r)
	}
	fourth := w.invoiceFor(ada, 100, "")
	if _, err := w.m.Match(ctx, w.env.Tenant, left[0].EntryID, fourth.Number, "user:ada"); !errors.Is(err, invoicing.ErrState) || !strings.Contains(err.Error(), "already counted towards") {
		t.Fatalf("over: %v", err)
	}
	// Undo the newer invoice's part: the older stays paid (both its
	// parts); ₦30,000 waits.
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.UndoPayment(ctx, tx, w.env.Tenant, newer.Number, n.Payments[0].ID, "user:ada")
		return err
	})
	if n, o := w.read(newer.Number), w.read(older.Number); n.Status == "paid" || o.Status != "paid" || o.PaidKobo != 2000000 || w.read(third.Number).Status != "paid" {
		t.Fatalf("after undoing one part %s %s %d", n.Status, o.Status, o.PaidKobo)
	}
	if left := w.unmatched(); len(left) != 1 || *left[0].LeftKobo != 3000000 {
		t.Fatalf("after undo %+v", left)
	}
	// Each part is its own payment row (the undoing names the part).
	var parts int
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM customer_invoice_payments WHERE entry_id = $1`, left[0].EntryID).Scan(&parts)
	})
	if parts != 4 {
		t.Errorf("parts %d", parts)
	}
}

// Confirming a split as offered leaves the rest as the customer's credit.
func TestConfirmSplitLeavesCredit(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230005"}
	a := w.invoiceFor(ada, 2000000, "2099-09-01")
	b := w.invoiceFor(ada, 1000000, "2099-10-01")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-SPLIT2", "amount_kobo": int64(3500000), "payer_name": "ADA OBI"})
	open := w.open()
	if len(open) == 0 {
		t.Fatal("no suggestion")
	}
	s, err := w.m.ConfirmSplit(ctx, w.env.Tenant, open[0].ID, "user:ada")
	if err != nil || len(s.Parts) != 2 || s.CreditKobo != 500000 || s.LeftKobo != 0 {
		t.Fatalf("split %v %+v", err, s)
	}
	if w.read(a.Number).Status != "paid" || w.read(b.Number).Status != "paid" || w.creditOf(a.CustomerID()) != 500000 || len(w.unmatched()) != 0 {
		t.Fatalf("after: %s %s credit %d", w.read(a.Number).Status, w.read(b.Number).Status, w.creditOf(a.CustomerID()))
	}
}

// The parts of one payment never add up to more than it, however many are
// written at once: the database refuses the rest.
func TestPartsNeverExceedThePayment(t *testing.T) {
	w := newWorld(t)
	x := w.invoiceFor(invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230006"}, 50000000, "")
	id := w.credit(moneylog.Entry{Reference: "VA-RACE", AmountKobo: kobo(100000)})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, refused := 0, 0
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Raw rows, as any writer could: the database checks.
			err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO customer_invoice_payments (id, tenant_id, invoice_id, amount_kobo, payment_id, how, by, entry_id)
					VALUES (gen_random_uuid(), $1, $2, 30000, $3, 'manual', 'user:ada', $4)`, w.env.Tenant, x.ID, "race-"+string(rune('a'+i)), id)
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if strings.Contains(err.Error(), "23514") {
				refused++
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 3 || refused != 7 {
		t.Fatalf("ok %d refused %d", ok, refused)
	}
	if y := w.read(x.Number); y.PaidKobo != 90000 {
		t.Errorf("paid %d", y.PaidKobo)
	}
	// Through matching, too: only what is left can be matched.
	other := w.invoiceFor(invoicing.Customer{Name: "Bola", Phone: "+2348031230007"}, 50000, "")
	if _, err := w.m.MatchParts(ctx, w.env.Tenant, id, []matching.Part{{Invoice: other.Number, AmountKobo: 20000}}, false, "user:ada"); !errors.Is(err, invoicing.ErrState) {
		t.Errorf("more than is left: %v", err)
	}
	if r, err := w.m.Match(ctx, w.env.Tenant, id, other.Number, "user:ada"); err != nil || r.Invoice.PaidKobo != 10000 {
		t.Errorf("the rest: %v %+v", err, r)
	}
}

// A payer a person matched is remembered for the customer: their next
// payment is suggested for that customer with the reason, and matched on
// its own with automatic matching on. Forgotten, it is not.
func TestPayerMemory(t *testing.T) {
	w := newWorld(t)
	ebube := invoicing.Customer{Name: "Ebube Eze", Phone: "+2348031230008"}
	x := w.invoiceFor(ebube, 2500000, "")
	// From the aunt: another name, only the amount in common; a person
	// confirms it.
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-AUNT-1", "amount_kobo": int64(2500000), "payer_name": "OKAFOR NGOZI"})
	open := w.open()
	if len(open) != 1 || open[0].Score >= matching.Auto {
		t.Fatalf("first payment %+v", open)
	}
	if _, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false); err != nil {
		t.Fatal(err)
	}
	var payers []matching.Payer
	w.in(func(tx pgx.Tx) error {
		var err error
		payers, err = matching.PayersOf(ctx, tx, w.env.Vault, w.env.Tenant, x.CustomerID())
		return err
	})
	if len(payers) != 1 || payers[0].Payer != "OKAFOR NGOZI" || payers[0].Kind != "name" {
		t.Fatalf("payers %+v", payers)
	}
	var sealed string
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT payer::text || payer_key FROM money_payers WHERE id = $1`, payers[0].ID).Scan(&sealed)
	})
	if strings.Contains(strings.ToLower(sealed), "okafor") {
		t.Errorf("payer kept in the clear: %s", sealed)
	}

	// Automatic matching on: the aunt's next payment pays the next invoice.
	w.in(func(tx pgx.Tx) error {
		st, err := invoicing.GetSettings(ctx, tx, w.env.Tenant)
		if err != nil {
			return err
		}
		st.AutoMatch = true
		_, err = invoicing.SaveSettings(ctx, tx, w.env.Tenant, st)
		return err
	})
	y := w.invoiceFor(ebube, 1500000, "")
	w.invoiceFor(invoicing.Customer{Name: "Someone Else", Phone: "+2348031230009"}, 1500000, "")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-AUNT-2", "amount_kobo": int64(700000), "payer_name": "Ngozi Okafor"})
	z := w.read(y.Number)
	if len(z.Payments) != 1 || z.Payments[0].How != "suggested_auto" {
		t.Fatalf("remembered payer not matched: %+v open %+v", z, w.open())
	}
	found := false
	for _, r := range z.Payments[0].Reasons {
		found = found || r == "this payer paid for this customer before"
		if strings.Contains(strings.ToLower(r), "okafor") || strings.Contains(strings.ToLower(r), "ebube") {
			t.Errorf("a reason names someone: %q", r)
		}
	}
	if !found {
		t.Errorf("reasons %q", z.Payments[0].Reasons)
	}

	// Forgotten: the next payment from them is no one's in particular.
	w.in(func(tx pgx.Tx) error { return matching.Forget(ctx, tx, w.env.Tenant, payers[0].ID) })
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-AUNT-3", "amount_kobo": int64(300000), "payer_name": "OKAFOR NGOZI"})
	if len(w.open()) != 0 || w.read(y.Number).PaidKobo != 700000 {
		t.Errorf("forgotten payer still used: %+v", w.open())
	}
	w.in(func(tx pgx.Tx) error {
		if err := matching.Forget(ctx, tx, w.env.Tenant, payers[0].ID); !errors.Is(err, invoicing.ErrNotFound) {
			t.Errorf("forgotten twice: %v", err)
		}
		return nil
	})

	// Another business never uses this one's payers.
	other := w.env.AddTenant(t)
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 1`)
		return err
	})
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{other}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM money_payers`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("another tenant sees %d payers", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The split is offered whenever the customer has more than one open
// invoice, not only when the payment is more than is due on the suggested
// one: for To match the suggested invoice first with what it is due, the
// rest with 0 to choose from; for chat oldest first, and not offered
// (Over false) when the payment is no more than is due.
func TestSplitOfferedAcrossOpenInvoices(t *testing.T) {
	w := newWorld(t)
	ada := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031230006"}
	older := w.invoiceFor(ada, 2000000, "2099-09-01")
	newer := w.invoiceFor(ada, 3000000, "2099-12-01")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-SEVERAL", "amount_kobo": int64(3000000), "payer_name": "ADA OBI"})
	open := w.open()
	if len(open) == 0 || open[0].Number != newer.Number {
		t.Fatalf("suggestions %+v", open)
	}
	var first, oldest matching.Proposal
	w.in(func(tx pgx.Tx) error {
		var err error
		if first, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, true); err != nil {
			return err
		}
		oldest, err = w.m.Propose(ctx, tx, w.env.Tenant, open[0].ID, false)
		return err
	})
	if first.Over || len(first.Parts) != 2 || first.Parts[0].Number != newer.Number || first.Parts[0].AmountKobo != 3000000 || first.Parts[1].Number != older.Number || first.Parts[1].AmountKobo != 0 || first.CreditKobo != 0 {
		t.Fatalf("suggested first %+v", first)
	}
	if oldest.Over || len(oldest.Parts) != 2 || oldest.Parts[0].Number != older.Number || oldest.Parts[0].AmountKobo != 2000000 || oldest.Parts[1].AmountKobo != 1000000 {
		t.Fatalf("oldest first %+v", oldest)
	}
	// An invoice alone, and a payment no more than is due: nothing to split.
	bola := invoicing.Customer{Name: "Bola Ade", Phone: "+2348031230007"}
	only := w.invoiceFor(bola, 1000000, "")
	w.heard("virtual_account.credited", map[string]any{"reference": "PAY-ONE", "amount_kobo": int64(1000000), "payer_name": "BOLA ADE"})
	for _, s := range w.open() {
		if s.Number != only.Number {
			continue
		}
		w.in(func(tx pgx.Tx) error {
			var err error
			first, err = w.m.Propose(ctx, tx, w.env.Tenant, s.ID, true)
			return err
		})
		if len(first.Parts) != 0 || first.Over {
			t.Fatalf("one invoice %+v", first)
		}
	}
}
