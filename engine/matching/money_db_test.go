package matching_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// The money rules, each shown by the case that once broke it: every bank
// credit is its own entry, a refund reopens what its payment paid however
// it was heard, and an invoice counts no more than its total.

func (w *world) account(invoice uuid.UUID, seq int, ref string, amount int64) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
			VALUES (gen_random_uuid(), $1, $2, $3, 'transfer', 'paystack', 'main', $4, $5, 'open')`, w.env.Tenant, invoice, seq, ref, amount)
		return err
	})
}

type loggedEntry struct {
	id                  uuid.UUID
	kind, status, state string
	amount, left        int64
	customers           int
}

// entries are the money log's entries with a reference, oldest first, with
// what matching made of each.
func (w *world) entries(ref string) []loggedEntry {
	w.t.Helper()
	var out []loggedEntry
	w.in(func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.id, e.kind, e.status, COALESCE(e.amount_kobo, 0), COALESCE(s.state, ''), COALESCE(taskiem_money_entry_left(e.id), 0),
			(SELECT count(*) FROM money_entry_customers c WHERE c.entry_id = e.id)
			FROM money_entries e LEFT JOIN money_match_state s ON s.entry_id = e.id WHERE e.reference = $1 ORDER BY e.recorded_at, e.id`, ref)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (loggedEntry, error) {
			var a loggedEntry
			return a, r.Scan(&a.id, &a.kind, &a.status, &a.amount, &a.state, &a.left, &a.customers)
		})
		return err
	})
	return out
}

func sum(list []loggedEntry) int64 {
	var n int64
	for _, e := range list {
		if e.status == "settled" {
			n += e.amount
		}
	}
	return n
}

// Two transfers (₦20,000 then ₦30,000) into one one-time number reach the
// money log as two payment.received events with the same payment_id (the
// number's tot_ reference) and each its own credit_id, as iswallet's
// manifest maps them: two entries, both counted towards the number's
// invoice. Either heard again is still one entry.
func TestSecondTransferIntoOneTimeNumber(t *testing.T) {
	const ref = "tot_ab12cd34ef56gh78ij90kl12mn34op56"
	w := newWorld(t)
	x := w.invoice("Ada Obi", 5000000)
	w.account(x.ID, 1, ref, 5000000)
	first := map[string]any{"payment_id": ref, "credit_id": "100004261027091402000001", "amount_kobo": int64(2000000), "paid_at": "2026-10-27T09:14:02Z"}
	second := map[string]any{"payment_id": ref, "credit_id": "100004261027102000000002", "amount_kobo": int64(3000000), "paid_at": "2026-10-27T10:20:00Z"}
	w.heard("payment.received", first)
	w.heard("payment.received", second)
	got := w.entries(ref)
	y := w.read(x.Number)
	if len(got) != 2 || sum(got) != 5000000 {
		t.Errorf("money log: %d entries summing %d kobo, want 2 summing 5000000: %+v", len(got), sum(got), got)
	}
	if y.PaidKobo != 5000000 && len(w.unmatched()) == 0 {
		t.Errorf("the second ₦30,000 is neither on the invoice (paid %d, %s) nor in To match", y.PaidKobo, y.Status)
	}
	if y.Status != "paid" || y.PaidKobo != 5000000 || len(y.Payments) != 2 {
		t.Errorf("the invoice: %s, %d paid, %d payments", y.Status, y.PaidKobo, len(y.Payments))
	}
	// A webhook retry of each, the look-back reading each back, and the
	// payment asked about by its reference alone: nothing more.
	w.heard("payment.received", first)
	w.heard("payment.received", second)
	for _, f := range []map[string]any{first, second} {
		e, ok := moneylog.FromCredit("paystack/main", map[string]any{"event": "payment.received", "reference": ref, "credit_id": f["credit_id"],
			"amount_kobo": f["amount_kobo"], "occurred_at": f["paid_at"]})
		if !ok {
			t.Fatal("the look-back's credit is not an entry")
		}
		if _, fresh, err := w.log.Record(ctx, w.env.Tenant, e); err != nil || fresh {
			t.Errorf("read back: fresh %v, %v", fresh, err)
		}
	}
	w.heard("payment.received", map[string]any{"payment_id": ref, "amount_kobo": int64(2000000)})
	if got, y := w.entries(ref), w.read(x.Number); len(got) != 2 || sum(got) != 5000000 || y.PaidKobo != 5000000 || len(y.Payments) != 2 {
		t.Errorf("heard again: %d entries summing %d, invoice paid %d in %d payments", len(got), sum(got), y.PaidKobo, len(y.Payments))
	}

	// A third, late transfer into the number, the invoice now paid: its
	// own entry, kept as the customer's credit, not swallowed.
	w.heard("payment.received", map[string]any{"payment_id": ref, "credit_id": "100004261028080000000003", "amount_kobo": int64(700000), "paid_at": "2026-10-28T08:00:00Z"})
	got = w.entries(ref)
	if y := w.read(x.Number); len(got) != 3 || y.PaidKobo != 5000000 || got[2].left != 700000 || got[2].customers != 1 || w.creditOf(y.CustomerID()) != 700000 {
		t.Errorf("a late transfer: %d entries, invoice paid %d, the third %+v, credit %d", len(got), y.PaidKobo, got[len(got)-1], w.creditOf(y.CustomerID()))
	}

	// The rail takes the second transfer back, naming it: that one's part
	// is undone, the first's stays.
	w.heard("payment.returned", map[string]any{"payment_id": ref, "credit_id": "100004261027102000000002"})
	if y := w.read(x.Number); y.PaidKobo != 2000000 || y.Status == "paid" {
		t.Errorf("after the second transfer went back: %s, %d paid", y.Status, y.PaidKobo)
	}
}

// Entries the old key wrote (a one-time number's payment known by its
// reference alone) are not recorded again when the same credit comes back
// with its id; a transfer the rail's own clock puts later is another.
func TestCreditHeardBeforeCreditsHadIDs(t *testing.T) {
	const ref = "tot_zz12cd34ef56gh78ij90kl12mn34op56"
	w := newWorld(t)
	x := w.invoice("Ada Obi", 5000000)
	w.account(x.ID, 1, ref, 5000000)
	// As the old code wrote the first transfer: no credit id.
	w.heard("payment.received", map[string]any{"payment_id": ref, "amount_kobo": int64(2000000), "paid_at": "2026-10-27T09:14:02Z"})
	// The look-back reads it back with its id (what landed, so another
	// amount), and a webhook retry carries the event's time, seconds on.
	w.heard("payment.received", map[string]any{"payment_id": ref, "credit_id": "P-1", "amount_kobo": int64(1990000), "paid_at": "2026-10-27T09:14:02Z"})
	w.heard("payment.received", map[string]any{"payment_id": ref, "credit_id": "P-1", "amount_kobo": int64(2000000), "paid_at": "2026-10-27T09:14:40Z"})
	// A credit with no time of its own is never taken as another.
	w.heard("payment.received", map[string]any{"payment_id": ref, "credit_id": "P-1", "amount_kobo": int64(2000000)})
	if got := w.entries(ref); len(got) != 1 || sum(got) != 2000000 {
		t.Fatalf("the same credit with its id: %d entries summing %d", len(got), sum(got))
	}
	// The second transfer, an hour later, the old code dropped: recorded
	// now, once, and counted.
	later := map[string]any{"payment_id": ref, "credit_id": "P-2", "amount_kobo": int64(3000000), "paid_at": "2026-10-27T10:20:00Z"}
	w.heard("payment.received", later)
	w.heard("payment.received", later)
	if got, y := w.entries(ref), w.read(x.Number); len(got) != 2 || sum(got) != 5000000 || y.PaidKobo != 5000000 {
		t.Errorf("the later transfer: %d entries summing %d, invoice paid %d", len(got), sum(got), y.PaidKobo)
	}
}

// A refund reopens the invoice its payment paid whether it is heard as
// refund.processed alone, or first as pending or failed: the reversal
// names the payment, not the refund's earlier status.
func TestRefundReopensInvoiceWhateverItsFirstStatus(t *testing.T) {
	for _, tc := range []struct{ name, before string }{{"processed alone", ""}, {"pending then processed", "refund.pending"}, {"failed then processed", "refund.failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			x := w.invoice("Ada Obi", 2500000)
			w.account(x.ID, 1, "PAY-9", 2500000)
			w.heard("payment.received", map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(2500000)})
			if y := w.read(x.Number); y.Status != "paid" {
				t.Fatalf("set-up: not paid: %s %d", y.Status, y.PaidKobo)
			}
			payment := w.entries("PAY-9")[0].id
			if tc.before != "" {
				w.heard(tc.before, map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(2500000)})
				if y := w.read(x.Number); y.Status != "paid" {
					t.Fatalf("%s alone reopened the invoice", tc.before)
				}
			}
			w.heard("refund.processed", map[string]any{"payment_id": "PAY-9", "refund_id": "RF-1", "amount_kobo": int64(2500000)})
			if err := w.m.Tick(ctx); err != nil { // looked at again: nothing more
				t.Fatal(err)
			}
			var n, undone int
			var linked *uuid.UUID
			w.in(func(tx pgx.Tx) error {
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM money_reversals`).Scan(&n); err != nil || n != 1 {
					return err
				}
				return tx.QueryRow(ctx, `SELECT entry_id, undone FROM money_reversals`).Scan(&linked, &undone)
			})
			if n != 1 {
				t.Fatalf("%d reversal rows, want 1", n)
			}
			if linked == nil || *linked != payment || undone != 1 {
				t.Errorf("the reversal names entry %v (undone %d), want the payment %v", linked, undone, payment)
			}
			if y := w.read(x.Number); y.Status == "paid" || y.PaidKobo != 0 {
				t.Errorf("the invoice is still %s with %d kobo paid after a whole refund", y.Status, y.PaidKobo)
			}
			if got := w.alerts(matching.AlertReversed); got != 1 {
				t.Errorf("%d alerts, want 1", got)
			}
			if len(w.unmatched()) != 0 {
				t.Error("refunded money waits in To match")
			}
		})
	}
}

// A reversal the old code recorded as taking back nothing (entry_id NULL:
// the refund had been heard as pending first) is looked at again and tied
// to its payment: the invoice reopens, once.
func TestUnlinkedReversalIsRepaired(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 2500000)
	w.account(x.ID, 1, "PAY-9", 2500000)
	w.heard("payment.received", map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(2500000)})
	payment := w.entries("PAY-9")[0].id
	// The refund's two statuses, recorded and not yet looked at; then the
	// row as the old code left it.
	for _, ev := range []string{"refund.pending", "refund.processed"} {
		if _, err := w.log.RecordEvent(ctx, w.env.Tenant, "prod", "paystack", "main", ev, map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(2500000)}); err != nil {
			t.Fatal(err)
		}
	}
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO money_reversals (by_entry, tenant_id, entry_id, amount_kobo, created_at)
			SELECT id, tenant_id, NULL, amount_kobo, now() - interval '30 days' FROM money_entries WHERE kind = 'refund' AND status = 'settled'`)
		return err
	})
	if y := w.read(x.Number); y.Status != "paid" {
		t.Fatalf("set-up: %s", y.Status)
	}
	for range 2 {
		if err := w.m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var linked *uuid.UUID
	var undone int
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT entry_id, undone FROM money_reversals`).Scan(&linked, &undone)
	})
	if y := w.read(x.Number); linked == nil || *linked != payment || undone != 1 || y.PaidKobo != 0 || y.Status == "paid" {
		t.Errorf("repaired: names %v (payment %v), undone %d; invoice %s paid %d", linked, payment, undone, y.Status, y.PaidKobo)
	}
	if got := w.alerts(matching.AlertReversed); got != 1 {
		t.Errorf("%d alerts, want 1", got)
	}
	// A reversal names its payment once: the app role cannot move it.
	w.in(func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := sp.Exec(ctx, `UPDATE money_reversals SET entry_id = by_entry`); err == nil {
			t.Error("the app role may move a reversal to another entry")
		}
		return sp.Rollback(ctx)
	})
}

// A refund of a payment the money log does not have is recorded as taking
// back nothing and tried again, not for ever: after matching.RetryFor it
// is closed and the owners told, once. A payment that turns up in time is
// taken back.
func TestUnmatchedRefundIsTriedAgainThenClosed(t *testing.T) {
	w := newWorld(t)
	now := time.Now()
	w.m.Now = func() time.Time { return now }
	state := func() (linked bool, checked time.Time, closed bool) {
		w.in(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT r.entry_id IS NOT NULL, r.checked_at, r.closed_at IS NOT NULL FROM money_reversals r
				JOIN money_entries e ON e.id = r.by_entry WHERE e.reference = 'PAY-GONE'`).Scan(&linked, &checked, &closed)
		})
		return
	}
	w.heard("refund.processed", map[string]any{"payment_id": "PAY-GONE", "amount_kobo": int64(100000)})
	_, first, closed := state()
	if closed || w.alerts(matching.AlertUnmatched) != 0 {
		t.Fatal("closed at once")
	}
	// Not before it is due again.
	if err := w.m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := state(); !again.Equal(first) {
		t.Error("tried again at once")
	}
	now = now.Add(matching.RetryEvery + time.Minute)
	if err := w.m.TenantReversals(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if linked, again, closed := state(); linked || closed || !again.After(first) {
		t.Errorf("after a quarter of an hour: linked %v, closed %v, tried %v then %v", linked, closed, first, again)
	}
	// Past the limit: closed, the owners told once, and not tried again.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE money_reversals SET checked_at = $1`, now.Add(-time.Hour))
		return err
	})
	w.env.DB.Admin.Exec(ctx, `UPDATE money_reversals SET created_at = now() - interval '8 days' WHERE tenant_id = $1`, w.env.Tenant) //nolint:errcheck // checked below
	for range 2 {
		if err := w.m.TenantReversals(ctx, w.env.Tenant); err != nil {
			t.Fatal(err)
		}
	}
	if linked, _, closed := state(); linked || !closed || w.alerts(matching.AlertUnmatched) != 1 {
		t.Errorf("past the limit: linked %v, closed %v, %d alerts", linked, closed, w.alerts(matching.AlertUnmatched))
	}
	var due int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM taskiem_money_unreversed_tenants() WHERE tenant_id = $1`, w.env.Tenant).Scan(&due); err != nil || due != 0 {
		t.Errorf("a closed reversal is still due: %d %v", due, err)
	}

	// Another refund, heard before its payment: taken back when the
	// payment is recorded.
	x := w.invoice("Ada Obi", 2500000)
	w.account(x.ID, 1, "PAY-LATE", 2500000)
	w.heard("refund.processed", map[string]any{"payment_id": "PAY-LATE", "amount_kobo": int64(2500000)})
	w.heard("payment.received", map[string]any{"payment_id": "PAY-LATE", "amount_kobo": int64(2500000)})
	now = now.Add(matching.RetryEvery + time.Minute)
	if err := w.m.TenantReversals(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if y := w.read(x.Number); y.PaidKobo != 0 || y.Status == "paid" {
		t.Errorf("a refund heard before its payment: invoice %s paid %d", y.Status, y.PaidKobo)
	}
}

// A second exact-reference payment of ₦50,000 for an invoice of ₦50,000
// already paid in full (a second account given for it): the invoice
// counts no more than its total, and the money is the customer's credit,
// which pays the next invoice sent to them.
func TestPaymentToPaidInvoice(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 5000000)
	w.account(x.ID, 1, "tsk_acct_a", 5000000)
	w.account(x.ID, 2, "tsk_acct_b", 5000000)
	w.heard("payment.received", map[string]any{"payment_id": "tsk_acct_a", "amount_kobo": int64(5000000)})
	if y := w.read(x.Number); y.Status != "paid" || y.PaidKobo != 5000000 {
		t.Fatalf("set-up: %s %d", y.Status, y.PaidKobo)
	}
	w.heard("payment.received", map[string]any{"payment_id": "tsk_acct_b", "amount_kobo": int64(5000000)})
	y := w.read(x.Number)
	second := w.entries("tsk_acct_b")
	if len(second) != 1 {
		t.Fatalf("second entry: %+v", second)
	}
	var parts int
	var partsSum int64
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount_kobo), 0) FROM customer_invoice_payments WHERE entry_id = $1 AND undoes IS NULL`, second[0].id).Scan(&parts, &partsSum)
	})
	inToMatch := false
	for _, p := range w.unmatched() {
		inToMatch = inToMatch || p.Reference == "tsk_acct_b"
	}
	// Customer credit is what is left of the entries held as a customer's.
	credit := second[0].customers > 0 && second[0].left > 0
	if y.PaidKobo > y.TotalKobo {
		t.Errorf("the invoice shows %d kobo paid of %d", y.PaidKobo, y.TotalKobo)
	}
	if !credit && !inToMatch {
		t.Errorf("the second ₦50,000 is neither the customer's credit nor in To match (state %q, left %d)", second[0].state, second[0].left)
	}
	if y.PaidKobo != 5000000 || len(y.Payments) != 1 || parts != 0 || partsSum != 0 || second[0].left != 5000000 || !credit || inToMatch ||
		second[0].state != "matched" || w.creditOf(y.CustomerID()) != 5000000 {
		t.Errorf("invoice paid %d in %d payments; the second entry: %d parts summing %d, %+v; credit %d", y.PaidKobo, len(y.Payments), parts, partsSum, second[0], w.creditOf(y.CustomerID()))
	}
	// Looked at again, and found again by the account's own check: the same.
	var account uuid.UUID
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM customer_invoice_accounts WHERE payment_id = 'tsk_acct_b'`).Scan(&account)
	})
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "tsk_acct_b", AmountKobo: 5000000, Connection: "paystack/main", Account: account}); err != nil {
		t.Errorf("found again: %v", err)
	}
	if got, y := w.entries("tsk_acct_b"), w.read(x.Number); len(got) != 1 || got[0].left != 5000000 || y.PaidKobo != 5000000 {
		t.Errorf("found again: %+v, invoice paid %d", got, y.PaidKobo)
	}
	// The credit pays the next invoice sent to the customer.
	next := w.invoice("Ada Obi", 3000000)
	if z := w.read(next.Number); z.Status != "paid" || w.creditOf(z.CustomerID()) != 2000000 {
		t.Errorf("the next invoice: %s paid %d, credit left %d", z.Status, z.PaidKobo, w.creditOf(z.CustomerID()))
	}
}

// A payment of more than is due, into the invoice's own account: the
// invoice takes what is due, the rest is the customer's credit. By hand,
// a paid invoice is refused and a larger payment counts for what is due,
// the rest waiting in To match.
func TestInvoiceTakesWhatIsDue(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 5000000)
	w.account(x.ID, 1, "tsk_acct_c", 5000000)
	w.heard("payment.received", map[string]any{"payment_id": "tsk_acct_c", "amount_kobo": int64(6500000)})
	y := w.read(x.Number)
	got := w.entries("tsk_acct_c")
	if y.Status != "paid" || y.PaidKobo != 5000000 || len(y.Payments) != 1 || y.Payments[0].AmountKobo != 5000000 {
		t.Fatalf("the invoice: %s, %d paid, %+v", y.Status, y.PaidKobo, y.Payments)
	}
	if len(got) != 1 || got[0].left != 1500000 || got[0].customers != 1 || got[0].state != "matched" || w.creditOf(y.CustomerID()) != 1500000 || len(w.unmatched()) != 0 {
		t.Errorf("what was over: %+v, credit %d, To match %d", got, w.creditOf(y.CustomerID()), len(w.unmatched()))
	}

	// By hand: other money, with nothing to suggest it for.
	stray := func(ref string, amount int64) uuid.UUID {
		w.heard("payment.received", map[string]any{"payment_id": ref, "amount_kobo": amount})
		return w.entries(ref)[0].id
	}
	a := stray("STRAY-1", 900000)
	if _, err := w.m.Match(ctx, w.env.Tenant, a, x.Number, "user:ada"); !errors.Is(err, invoicing.ErrNothingDue) {
		t.Errorf("matching by hand to a paid invoice: %v", err)
	}
	if e := w.entries("STRAY-1")[0]; e.left != 900000 || w.read(x.Number).PaidKobo != 5000000 {
		t.Errorf("refused, yet %+v", e)
	}
	// An invoice of another customer, so no credit pays it when sent.
	other := w.invoiceFor(invoicing.Customer{Name: "Chidi Okeke", Phone: "+2348031239999"}, 2000000, "")
	b := stray("STRAY-2", 2600000)
	if _, err := w.m.Match(ctx, w.env.Tenant, b, other.Number, "user:ada"); err != nil {
		t.Fatal(err)
	}
	z := w.read(other.Number)
	e := w.entries("STRAY-2")[0]
	if z.Status != "paid" || z.PaidKobo != 2000000 || e.left != 600000 || e.state != "unmatched" || e.customers != 0 {
		t.Errorf("a larger payment matched by hand: invoice %s paid %d; entry %+v", z.Status, z.PaidKobo, e)
	}
	waiting := false
	for _, p := range w.unmatched() {
		waiting = waiting || p.Reference == "STRAY-2" && p.LeftKobo != nil && *p.LeftKobo == 600000
	}
	if !waiting {
		t.Error("what the invoice did not take is not in To match")
	}
	// The database refuses more than is due too, whoever writes.
	w.in(func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := sp.Exec(ctx, `INSERT INTO customer_invoice_payments (id, tenant_id, invoice_id, amount_kobo, payment_id, how, by)
			VALUES (gen_random_uuid(), $1, $2, 1, 'direct', 'manual', 'user:ada')`, w.env.Tenant, other.ID); err == nil {
			t.Error("the database took a payment for an invoice with nothing due")
		}
		return sp.Rollback(ctx)
	})
}

// Two payments for one invoice arriving at once do not both count in
// full: what is due is read under the invoice's lock.
func TestPaymentsAtOnceCountNoMoreThanIsDue(t *testing.T) {
	w := newWorld(t)
	for round, amounts := range [][2]int64{{5000000, 5000000}, {3000000, 3000000}} {
		x := w.invoice("Ada Obi", 5000000)
		refs := [2]string{"tsk_once_a" + string(rune('0'+round)), "tsk_once_b" + string(rune('0'+round))}
		var ids [2]uuid.UUID
		for i, ref := range refs {
			w.account(x.ID, i+1, ref, 5000000)
			if _, err := w.log.RecordEvent(ctx, w.env.Tenant, "prod", "paystack", "main", "payment.received", map[string]any{"payment_id": ref, "amount_kobo": amounts[i]}); err != nil {
				t.Fatal(err)
			}
			ids[i] = w.entries(ref)[0].id
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i := range refs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: refs[i], AmountKobo: amounts[i], Connection: "paystack/main", Entry: &ids[i]})
			}()
		}
		close(start)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: %v, %v", round, errs[0], errs[1])
		}
		if err := w.m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		y := w.read(x.Number)
		var left int64
		for _, ref := range refs {
			e := w.entries(ref)[0]
			if e.state != "matched" || e.left > 0 && e.customers != 1 {
				t.Errorf("round %d: %s: %+v", round, ref, e)
			}
			left += e.left
		}
		if y.Status != "paid" || y.PaidKobo != 5000000 || left != amounts[0]+amounts[1]-5000000 {
			t.Errorf("round %d: invoice %s paid %d of %d; %d left of the two payments", round, y.Status, y.PaidKobo, y.TotalKobo, left)
		}
		// The credit left would pay the next round's invoice when sent:
		// take it out of the way.
		w.in(func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM money_entry_customers WHERE tenant_id = $1`, w.env.Tenant)
			return err
		})
	}
}
