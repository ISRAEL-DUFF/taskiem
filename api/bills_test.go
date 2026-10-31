package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	billsconn "github.com/israel-duff/taskiem/connectors/bills"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// linkedPair is a seller and a buyer, linked: the buyer asked the seller
// as a supplier, and the seller accepted.
func linkedPair(t *testing.T, w *waWorld) (seller, buyer *client, sellerID, buyerID string) {
	t.Helper()
	buyer = w.tenant(t, "Ada's Kitchen", "ada@bills.test")
	seller = w.tenant(t, "Dangote Stores", "d@bills.test")
	sellerID, buyerID = tenantOf(t, seller), tenantOf(t, buyer)
	seller.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": sellerID, "role": "supplier"})
	l := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	seller.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	return seller, buyer, sellerID, buyerID
}

// Bills (L1, §6.1): an invoice whose customer is a linked business is
// mirrored into that business as a bill once sent, with the seller's
// number, lines, total and due date; the buyer's people are told; what
// the seller's match counts paid, and a void, follow; an invoice to a
// business it is not linked with mirrors nothing.
func TestBillsMirrored(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{} // the seller issues the buyer an account at acceptance
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000351"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	viewer := addMember(t, w.world, buyer, "vic", "viewer")

	tick := func(want int) {
		t.Helper()
		n, err := w.srv.LinkMirrorTick(ctx)
		if err != nil || n != want {
			t.Fatalf("mirrored %d, want %d: %v", n, want, err)
		}
		w.srv.WaitBackground()
	}
	draft := map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Rice, 50kg", "quantity": 5, "unit_kobo": 8500000}}, "due_on": "2099-01-31"}
	x := seller.must(201, "POST", "/v1/invoices", draft, "Idempotency-Key", "b-1")
	tick(0) // a draft is not a bill
	before := len(w.graph.Sent(buyerOn))
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	tick(1)

	bills := buyer.must(200, "GET", "/v1/bills", nil)["bills"].([]any)
	if len(bills) != 1 {
		t.Fatalf("bills: %v", bills)
	}
	b := bills[0].(map[string]any)
	if b["reference"] != x["number"] || b["supplier_name"] != "Dangote Stores" || b["amount_kobo"] != float64(42500000) || b["due_kobo"] != float64(42500000) ||
		b["status"] != "open" || b["due_on"] != "2099-01-31" || b["supplier"] != sellerID ||
		b["pay_account_number"] != "8800000001" || b["pay_bank_name"] != "Wema Bank" || !strings.Contains(b["pay_link"].(string), "/i/") {
		t.Fatalf("the bill: %v", b)
	}
	if got := buyer.must(200, "GET", "/v1/bills/"+x["number"].(string), nil); got["id"] != b["id"] {
		t.Errorf("by number: %v", got)
	}
	viewer.must(200, "GET", "/v1/bills", nil)
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* sent invoice "+x["number"].(string)+": ₦425,000 to pay") {
		t.Errorf("the buyer told: %+v", told)
	}
	// In chat: the bills to pay, and one bill.
	if r := w.say(t, buyerOn, "bills"); !strings.Contains(r, "• "+x["number"].(string)+" from Dangote Stores: ₦425,000 Due 31 Jan 2099.") || !strings.Contains(r, "In all: ₦425,000") {
		t.Errorf("bills: %q", r)
	}
	if r := w.say(t, buyerOn, "bill "+strings.ToLower(x["number"].(string))); !strings.Contains(r, "*"+x["number"].(string)+"* from *Dangote Stores*: to pay") ||
		!strings.Contains(r, "• Rice, 50kg: ₦425,000") || !strings.Contains(r, "Total ₦425,000 · paid ₦0 · ₦425,000 to pay.") ||
		!strings.Contains(r, "Pay into 8800000001 (Wema Bank) with "+x["number"].(string)+" in the transfer's note: it is counted towards that invoice by itself") || !strings.Contains(r, "pay by card or transfer on its page: https://forms.taskiem.test/i/") {
		t.Errorf("bill: %q", r)
	}
	if r := w.say(t, buyerOn, "bill INV-9999"); !strings.Contains(r, "No bill INV-9999") {
		t.Errorf("no bill: %q", r)
	}
	// The seller sees nothing of it: bills are the buyer's.
	if got := seller.must(200, "GET", "/v1/bills", nil)["bills"].([]any); len(got) != 0 {
		t.Errorf("the seller's bills: %v", got)
	}
	// The mirror is audited in both businesses.
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(DISTINCT tenant_id) FROM audit_log WHERE action = 'link.bill' AND tenant_id IN ($1, $2)`, sellerID, buyerID).Scan(&n); err != nil || n != 2 {
		t.Errorf("audited in %d businesses: %v", n, err)
	}

	// A payment the seller counts follows; so does a void.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(sellerID)}, func(tx pgx.Tx) error {
		_, _, err := w.srv.Invoices.RecordPayment(ctx, tx, uuid.MustParse(sellerID), x["number"].(string),
			invoicing.PaymentIn{AmountKobo: 10000000, PaymentID: "pay-1", How: "manual", By: "user:d"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tick(1)
	if b := buyer.must(200, "GET", "/v1/bills/"+b["id"].(string), nil); b["status"] != "part_paid" || b["paid_kobo"] != float64(10000000) || b["due_kobo"] != float64(32500000) {
		t.Errorf("after a payment: %v", b)
	}
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/void", map[string]any{"reason": "Sent twice"})
	tick(1)
	if b := buyer.must(200, "GET", "/v1/bills/"+b["id"].(string), nil); b["status"] != "void" || b["due_kobo"] != float64(0) {
		t.Errorf("after a void: %v", b)
	}

	// The reconciler: a bill changed by hand is put back and mirrored again.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET paid_kobo = 1 WHERE id = $1`, b["id"]); err != nil {
		t.Fatal(err)
	}
	if n, err := w.srv.LinkReconcile(ctx); err != nil || n != 1 {
		t.Fatalf("reconciled %d: %v", n, err)
	}
	tick(1)
	if b := buyer.must(200, "GET", "/v1/bills/"+b["id"].(string), nil); b["paid_kobo"] != float64(10000000) || b["status"] != "void" {
		t.Errorf("after the reconciler: %v", b)
	}
	if n, err := w.srv.LinkReconcile(ctx); err != nil || n != 0 {
		t.Errorf("reconciled again: %d %v", n, err)
	}

	// To a business it is not linked with: nothing, and nothing left due.
	stranger := uuid.Must(uuid.NewV7())
	y := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Someone", "key": "link:" + stranger.String()},
		"amount_kobo": 100000}, "Idempotency-Key", "b-2")
	seller.must(200, "POST", "/v1/invoices/"+y["id"].(string)+"/send", nil, "Idempotency-Key", "s-2")
	tick(0)
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(sellerID)}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM link_mirrors_due`).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("left due: %d %v", n, err)
	}
}

// Paying a bill from Taskiem (§6.2): set up once with the payments
// account; Pay finds the supplier's bank by name among the banks that
// account pays, records the payment under way (the bill paying, a second
// Pay refused) and starts the workflow with the bill's token; the
// workflow's outcome is recorded on the bill. A bank the account does not
// list is refused plainly.
func TestBillPay(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	payments := &links.Payments{Pool: w.env.DB.App}
	for _, c := range []*connector.Connector{billsconn.New(payments), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Wema Bank Plc", "code": "035"}, {"name": "Access Bank", "code": "044"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	seller, buyer, _, buyerID := linkedPair(t, w)
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"amount_kobo": 4250000}, "Idempotency-Key", "b-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	bill := buyer.must(200, "GET", "/v1/bills/"+x["number"].(string), nil)
	id := bill["id"].(string)

	// Not set up: refused, saying what to do.
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/pay", nil); !strings.Contains(e["error"].(string), "set up paying bills first") {
		t.Errorf("not set up: %v", e)
	}
	if st := buyer.must(200, "GET", "/v1/bills/pay", nil); st["workflow_id"] != nil {
		t.Errorf("settings before: %v", st)
	}
	buyer.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	st := buyer.must(200, "PUT", "/v1/bills/pay/setup", map[string]any{"rail_connection": "main"})
	if st["workflow_id"] == nil || st["connection"] != "main" || st["approver_role"] != "owner" {
		t.Fatalf("set up: %v", st)
	}

	out := buyer.must(202, "POST", "/v1/bills/"+id+"/pay", nil)
	pay := out["payment"].(map[string]any)
	if pay["amount_kobo"] != float64(4250000) || pay["status"] != "pending" || !strings.HasPrefix(pay["token"].(string), x["number"].(string)+"/") || out["run_id"] == "" {
		t.Fatalf("pay: %v", out)
	}
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, out["run_id"]).Scan(&body); err != nil {
		t.Fatalf("the run: %v", err)
	}
	for _, want := range []string{`"bank_code": "035"`, `"$pii": "account_number"`, `"token": "` + pay["token"].(string) + `"`, `"amount_kobo": 4250000`} {
		if !strings.Contains(body, want) {
			t.Errorf("the run's input has no %s: %s", want, body)
		}
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+id, nil); b["status"] != "paying" || b["paying_kobo"] != float64(4250000) {
		t.Errorf("paying: %v", b)
	}
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/pay", nil); !strings.Contains(e["error"].(string), "already under way") {
		t.Errorf("paid twice: %v", e)
	}

	// The workflow records the bank's confirmation: sent, no longer paying;
	// paid when the supplier's match counts it.
	if _, err := payments.FinishBillPayment(ctx, uuid.MustParse(buyerID), uuid.MustParse(pay["id"].(string)), "paid", "DANGOTE/ADA"); err != nil {
		t.Fatal(err)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+id, nil); b["status"] != "open" || b["paying_kobo"] != float64(0) || b["sent_kobo"] != float64(4250000) {
		t.Errorf("sent: %v", b)
	}

	// A bank the payments account does not list: refused.
	banks = []map[string]any{{"name": "Access Bank", "code": "044"}}
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/pay", nil); !strings.Contains(e["error"].(string), "does not list Wema Bank as one bank") {
		t.Errorf("unknown bank: %v", e)
	}
}

// Matching a linked buyer's money (§6.3): money into the account the
// seller issued it goes first to the invoice its note names; with no
// note, to the invoice of the buyer's one payout of that amount from
// Taskiem (its pairing token, through taskiem_link_payout_token), which is
// then marked paired on the buyer's side; oldest-first passes over an
// invoice the buyer queried.
func TestLinkedMoneyNamesItsInvoice(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	mlog := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: mlog,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	sellerT, buyerT := uuid.MustParse(sellerID), uuid.MustParse(buyerID)
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault}
	n := 0
	invoice := func(kobo int, due string) string {
		n++
		x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
			"amount_kobo": kobo, "due_on": due}, "Idempotency-Key", fmt.Sprintf("i-%d", n))
		seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", fmt.Sprintf("s-%d", n))
		return x["number"].(string)
	}
	queried := invoice(3000000, "2099-01-01")
	oldest := invoice(1000000, "2099-02-01")
	newest := invoice(2000000, "2099-03-01")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET disputed = true WHERE tenant_id = $1 AND number = $2`, sellerID, queried); err != nil {
		t.Fatal(err)
	}
	credit := func(kobo int64, note, event string) {
		t.Helper()
		if _, _, err := mlog.Record(ctx, sellerT, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "iswallet/main",
			Reference: "lnk_1", AccountNumber: "8800000001", Event: event, Source: "event", AmountKobo: &kobo, Description: note}); err != nil {
			t.Fatal(err)
		}
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	paid := func(number string) float64 {
		return seller.must(200, "GET", "/v1/invoices/"+number, nil)["invoice"].(map[string]any)["paid_kobo"].(float64)
	}

	// Named in the note: the newest, not the oldest.
	credit(2000000, "Transfer from ADA KITCHEN "+newest+" thanks", "credit-1")
	if paid(newest) != 2000000 || paid(oldest) != 0 || paid(queried) != 0 {
		t.Fatalf("named: newest %v, oldest %v, queried %v", paid(newest), paid(oldest), paid(queried))
	}

	// A payout from Taskiem whose rail dropped the note: its token, by
	// amount, names the oldest; the buyer's payment is marked paired.
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	var pay links.BillPayment
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{buyerT}, func(tx pgx.Tx) error {
		b, err := links.GetBillTx(ctx, tx, oldest)
		if err != nil {
			return err
		}
		pay, err = links.StartBillPaymentTx(ctx, tx, buyerT, links.PayIn{Bill: b.ID, AmountKobo: 1000000, Connection: "main", BankCode: "035", AccountNumber: "8800000001", By: "user:ada"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	credit(1000000, "", "credit-2")
	if paid(oldest) != 1000000 {
		t.Fatalf("by the token: oldest %v", paid(oldest))
	}
	var paired bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT paired_at IS NOT NULL FROM bill_payments WHERE id = $1`, pay.ID).Scan(&paired); err != nil || !paired {
		t.Errorf("the buyer's payment paired: %v %v", paired, err)
	}

	// Naming nothing, matching no payout: oldest first, passing over the
	// queried invoice; what is left is the buyer's credit.
	credit(500000, "", "credit-3")
	if paid(queried) != 0 {
		t.Errorf("a queried invoice was paid oldest-first: %v", paid(queried))
	}
	// Named, a queried invoice is still paid.
	credit(3000000, queried, "credit-4")
	if paid(queried) != 3000000 {
		t.Errorf("a queried invoice named: %v", paid(queried))
	}
	_ = buyer
}

// What each side says (§6.2, §8): the buyer says it paid outside Taskiem
// (its own figure, the seller told); queries a bill (the bill queried,
// Pay refused, the seller's invoice passed over by oldest-first, the
// seller told); the seller settles the query (both cleared, the buyer
// told).
func TestBillDeclareAndQuery(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	const sellerOn, buyerOn = "+2348010000361", "+2348010000362"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"amount_kobo": 4250000}, "Idempotency-Key", "b-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	num := x["number"].(string)

	// Paid outside Taskiem: the buyer's own figure; the seller told.
	before := len(w.graph.Sent(sellerOn))
	b := buyer.must(200, "POST", "/v1/bills/"+num+"/declare", map[string]any{"amount_kobo": 1000000, "how": "transfer"})
	if b["declared_paid_kobo"] != float64(1000000) || b["paid_kobo"] != float64(0) || b["status"] != "open" {
		t.Errorf("declared: %v", b)
	}
	buyer.must(400, "POST", "/v1/bills/"+num+"/declare", map[string]any{"amount_kobo": 1000000, "how": "barter"})
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* says it paid ₦10,000 by transfer for "+num) {
		t.Errorf("the seller told: %+v", told)
	}

	// Queried: the bill, the seller's invoice; Pay refused; the seller told.
	before = len(w.graph.Sent(sellerOn))
	if b := buyer.must(200, "POST", "/v1/bills/"+num+"/query", map[string]any{"reason": "We got 4 cartons, not 5"}); b["status"] != "disputed" || b["disputed"] != true {
		t.Errorf("queried: %v", b)
	}
	buyer.must(409, "POST", "/v1/bills/"+num+"/query", map[string]any{"reason": "again"})
	var disputed bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT disputed FROM customer_invoices WHERE tenant_id = $1 AND number = $2`, sellerID, num).Scan(&disputed); err != nil || !disputed {
		t.Errorf("the seller's invoice flagged: %v %v", disputed, err)
	}
	if told := w.lastTo(t, sellerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* queried "+num+": We got 4 cartons, not 5") {
		t.Errorf("the seller told of the query: %+v", told)
	}
	// The mirror after the flag keeps the query (the buyer's own).
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+num, nil); b["status"] != "disputed" {
		t.Errorf("after the mirror: %v", b)
	}

	// Settled by the seller: both cleared; the buyer told.
	before = len(w.graph.Sent(buyerOn))
	seller.must(200, "POST", "/v1/invoices/"+num+"/settle-query", map[string]any{"note": "A credit note is coming for the fifth carton."})
	seller.must(409, "POST", "/v1/invoices/"+num+"/settle-query", nil)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT disputed FROM customer_invoices WHERE tenant_id = $1 AND number = $2`, sellerID, num).Scan(&disputed); err != nil || disputed {
		t.Errorf("the seller's invoice after settling: %v %v", disputed, err)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+num, nil); b["status"] != "open" || b["disputed"] != false {
		t.Errorf("the bill after settling: %v", b)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* settled your query on "+num+". A credit note is coming") {
		t.Errorf("the buyer told: %+v", told)
	}
}
