package matching_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
)

// A linked buyer's money (business links §5.3, §6.3) goes first to the
// invoice it names: in the rail's narration, or by the token of the
// buyer's one payout of that amount from Taskiem that was sent to this
// seller within three days. A payout still waiting to be approved or sent
// answers nothing; two equal payouts answer nothing and pair neither; a
// payout to another supplier is never this seller's; and each question is
// audited in the buyer's chain.
func TestLinkedPayoutPairsOnlyWhatItNamed(t *testing.T) {
	w := newWorld(t)
	seller := w.env.Tenant
	buyer := w.env.AddTenant(t)
	other := w.env.AddTenant(t) // another supplier of the buyer's
	admin := w.env.DB.Admin
	link := func(a, b uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		lo, hi := a, b
		if hi.String() < lo.String() {
			lo, hi = hi, lo
		}
		if _, err := admin.Exec(ctx, `INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at, answered_at, answered_actor)
			VALUES ($1, $2, $3, 'active', $4, 'user:ada', 'search', 'supplier', now() + interval '30 days', now(), 'user:d')`, id, lo, hi, a); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ours, theirs := link(buyer, seller), link(buyer, other)
	ada := invoicing.Customer{Name: "Ada's Kitchen", Key: "link:" + buyer.String()}
	older := w.invoiceFor(ada, 2000000, "2099-01-01")
	newer := w.invoiceFor(ada, 1000000, "2099-02-01")
	third := w.invoiceFor(ada, 500000, "2099-03-01")
	fourth := w.invoiceFor(ada, 500000, "2099-04-01")
	w.register(ada, "8800000001", "lnk_1")

	// The buyer's bills and payments, as its side would write them.
	bill := func(link, supplier uuid.UUID, x invoicing.Invoice) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := admin.Exec(ctx, `INSERT INTO bills (id, tenant_id, link_id, supplier_tenant, supplier_name, source_id, reference, lines, amount_kobo)
			VALUES ($1, $2, $3, $4, 'Supplier', $5, $6, '[]', $7)`, id, buyer, link, supplier, x.ID, x.Number, x.TotalKobo); err != nil {
			t.Fatal(err)
		}
		return id
	}
	payment := func(bill uuid.UUID, kobo int64, token string, sent *time.Time) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := admin.Exec(ctx, `INSERT INTO bill_payments (id, tenant_id, bill_id, amount_kobo, token, connection, bank_code, account_number, created_by, status, sent_at)
			VALUES ($1, $2, $3, $4, $5, 'main', '035', '8800000001', 'user:ada', 'pending', $6)`, id, buyer, bill, kobo, token, sent); err != nil {
			t.Fatal(err)
		}
		return id
	}
	paired := func(id uuid.UUID) bool {
		t.Helper()
		var yes bool
		if err := admin.QueryRow(ctx, `SELECT paired_at IS NOT NULL FROM bill_payments WHERE id = $1`, id).Scan(&yes); err != nil {
			t.Fatal(err)
		}
		return yes
	}
	paid := func(x invoicing.Invoice) int64 { return w.read(x.Number).PaidKobo }
	now := time.Now()
	credit := func(kobo int64, narration string, n int) {
		t.Helper()
		fields := map[string]any{"reference": "lnk_1", "credit_id": "C" + string(rune('0'+n)), "account_number": "8800000001", "amount_kobo": kobo}
		if narration != "" {
			fields["narration"] = narration
		}
		w.heard("virtual_account.credited", fields)
	}

	// A payout of the newer bill's amount, not yet sent (waiting for its
	// approver): the credit of that amount names nothing and goes to the
	// oldest invoice; the payment stays unpaired.
	newerBill := bill(ours, seller, newer)
	waiting := payment(newerBill, 1000000, newer.Number+"/AAAA", nil)
	credit(1000000, "", 1)
	if paid(older) != 1000000 || paid(newer) != 0 || paired(waiting) {
		t.Fatalf("a payout waiting to be approved: older %d, newer %d, paired %v", paid(older), paid(newer), paired(waiting))
	}
	// Sent, it answers: the credit pays the invoice the token names, and
	// the payment is paired.
	if _, err := admin.Exec(ctx, `UPDATE bill_payments SET sent_at = $2 WHERE id = $1`, waiting, now); err != nil {
		t.Fatal(err)
	}
	credit(1000000, "", 2)
	if paid(newer) != 1000000 || paid(older) != 1000000 || !paired(waiting) {
		t.Fatalf("by the token: older %d, newer %d, paired %v", paid(older), paid(newer), paired(waiting))
	}

	// A payout to another supplier of the same amount is never this
	// seller's token: a credit of that amount here goes oldest first.
	otherBill := bill(theirs, other, third) // the other supplier's bill, as the buyer's rows
	elsewhere := payment(otherBill, 500000, "INV-0099/BBBB", &now)
	credit(500000, "", 3)
	if paid(older) != 1500000 || paid(third) != 0 || paired(elsewhere) {
		t.Fatalf("another supplier's payout: older %d, third %d, paired %v", paid(older), paid(third), paired(elsewhere))
	}

	// Two equal payouts in the window pair neither.
	thirdBill, fourthBill := bill(ours, seller, third), bill(ours, seller, fourth)
	p3, p4 := payment(thirdBill, 500000, third.Number+"/CCCC", &now), payment(fourthBill, 500000, fourth.Number+"/DDDD", &now)
	credit(500000, "", 4)
	if paid(older) != 2000000 || paid(third) != 0 || paid(fourth) != 0 || paired(p3) || paired(p4) {
		t.Fatalf("two equal payouts: older %d, third %d, fourth %d, paired %v %v", paid(older), paid(third), paid(fourth), paired(p3), paired(p4))
	}

	// The rail's narration names the invoice: the credit pays it, and the
	// payout whose token the narration carries is paired.
	credit(500000, "Transfer "+fourth.Number+"/DDDD from ADA", 5)
	if paid(fourth) != 500000 || paid(third) != 0 || !paired(p4) || paired(p3) {
		t.Fatalf("by the narration: third %d, fourth %d, paired %v %v", paid(third), paid(fourth), paired(p3), paired(p4))
	}
	if x := w.read(fourth.Number); len(x.Payments) != 1 || x.Payments[0].Reasons[1] != matching.ReasonNamed {
		t.Errorf("the named part: %+v", x.Payments)
	}

	// Every question to the buyer's side is in the buyer's audit chain,
	// with the amount asked, and in no one else's.
	var n, elsewhereN int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'link.payout_token' AND (detail->>'amount_kobo')::bigint > 0`, buyer).Scan(&n); err != nil || n < 4 {
		t.Errorf("audited %d times in the buyer's chain: %v", n, err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id <> $1 AND action = 'link.payout_token'`, buyer).Scan(&elsewhereN); err != nil || elsewhereN != 0 {
		t.Errorf("audited %d times elsewhere: %v", elsewhereN, err)
	}
	// The definer answers for the business in scope alone: the other
	// supplier, asked with the buyer's key and the same amount, hears of
	// its own payout and nothing of ours.
	var token string
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{other}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(taskiem_link_payout_token($1, $2), '')`, "key:link:"+buyer.String(), int64(500000)).Scan(&token)
	}); err != nil || token != "INV-0099/BBBB" {
		t.Errorf("the other supplier was answered %q: %v", token, err)
	}
}
