package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
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
	if b := buyer.must(200, "GET", "/v1/bills/"+id, nil); b["status"] != "open" || b["paying_kobo"] != float64(0) || b["sent_kobo"] != float64(4250000) ||
		b["uncounted_kobo"] != float64(4250000) || b["due_kobo"] != float64(0) {
		t.Errorf("sent: %v", b)
	}
	// Sent and not yet counted by the supplier: not due again, so not paid
	// twice, with the supplier's bank still listed. Paired by the
	// supplier's match but not yet mirrored back, still not.
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/pay", nil); !strings.Contains(e["error"].(string), "has not counted") {
		t.Errorf("paid twice before the supplier counted it: %v", e)
	}
	// Nor does a workflow's link@1 list_bills offer it to pay.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(buyerID)}, func(tx pgx.Tx) error {
		b, err := links.GetBillTx(ctx, tx, id)
		if out := api.BillOut(b); err == nil && out["payable"] != false {
			t.Errorf("link@1 payable while sent and uncounted: %v", out)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET paired_at = now() WHERE id = $1`, pay["id"]); err != nil {
		t.Fatal(err)
	}
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/pay", nil); !strings.Contains(e["error"].(string), "has not counted") {
		t.Errorf("paid twice before the mirror: %v", e)
	}
	// The mirror brings the supplier's count: paid.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET paid_kobo = amount_kobo WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+id, nil); b["status"] != "paid" || b["due_kobo"] != float64(0) || b["uncounted_kobo"] != float64(0) {
		t.Errorf("counted: %v", b)
	}

	// Part-paid: what was sent and not counted is not due again; the rest
	// is.
	y := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"amount_kobo": 4250000, "allow_part_payments": true}, "Idempotency-Key", "b-2")
	seller.must(200, "POST", "/v1/invoices/"+y["id"].(string)+"/send", nil, "Idempotency-Key", "s-2")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	yid := buyer.must(200, "GET", "/v1/bills/"+y["number"].(string), nil)["id"].(string)
	part := buyer.must(202, "POST", "/v1/bills/"+yid+"/pay", map[string]any{"amount_kobo": 1000000})["payment"].(map[string]any)
	if _, err := payments.FinishBillPayment(ctx, uuid.MustParse(buyerID), uuid.MustParse(part["id"].(string)), "paid", "DANGOTE/ADA"); err != nil {
		t.Fatal(err)
	}
	// The supplier counted ₦5,000 paid another way.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET paid_kobo = 500000 WHERE id = $1`, yid); err != nil {
		t.Fatal(err)
	}
	if b := buyer.must(200, "GET", "/v1/bills/"+yid, nil); b["status"] != "part_paid" || b["due_kobo"] != float64(2750000) || b["uncounted_kobo"] != float64(1000000) {
		t.Errorf("part-paid, part sent: %v", b)
	}
	if e := buyer.must(400, "POST", "/v1/bills/"+yid+"/pay", map[string]any{"amount_kobo": 3000000}); !strings.Contains(e["error"].(string), "2750000") {
		t.Errorf("more than is left: %v", e)
	}
	if p := buyer.must(202, "POST", "/v1/bills/"+yid+"/pay", nil)["payment"].(map[string]any); p["amount_kobo"] != float64(2750000) {
		t.Errorf("the rest: %v", p)
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
	// The token answers once the rail took the money (the payout step
	// answered): not while the payment waits for its approver.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET sent_at = now() WHERE id = $1`, pay.ID); err != nil {
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

// linkSupplier links another seller to buyer as its supplier, and answers
// the seller's client and id.
func linkSupplier(t *testing.T, w *waWorld, buyer *client, name, email string) (*client, string) {
	t.Helper()
	seller := w.tenant(t, name, email)
	sellerID := tenantOf(t, seller)
	seller.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": sellerID, "role": "supplier"})
	for _, l := range seller.must(200, "GET", "/v1/links", nil)["links"].([]any) {
		if l := l.(map[string]any); l["state"] == "asked_us" {
			seller.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
		}
	}
	w.srv.WaitBackground()
	return seller, sellerID
}

// The pairing token (§5.3) is the calling seller's alone: a payout still
// waiting to be approved answers nothing, and the credit goes oldest
// first; a payout to another supplier of the same amount is never
// answered to this seller; sent, the token pays the invoice it names and
// pairs the payment; two equal payouts in the window pair neither; and
// each question is audited in the buyer's chain. A credit whose rail
// passed the narration on pays the invoice the narration names. A bill
// number two suppliers used is refused until the supplier is named.
func TestBillPayoutTokenScoped(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	mlog := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: mlog,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	dangote, buyer, dangoteID, buyerID := linkedPair(t, w) // Dangote issued 8800000001, lnk_1
	kano, kanoID := linkSupplier(t, w, buyer, "Kano Traders", "k@bills.test")
	buyerT := uuid.MustParse(buyerID)
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault}
	const buyerOn = "+2348010000371"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	n := 0
	invoice := func(seller *client, kobo int, due string) string {
		t.Helper()
		n++
		x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
			"amount_kobo": kobo, "due_on": due, "allow_part_payments": true}, "Idempotency-Key", fmt.Sprintf("i-%d", n))
		seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", fmt.Sprintf("s-%d", n))
		if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
		return x["number"].(string)
	}
	credit := func(seller string, ref, account string, kobo int64, narration string) {
		t.Helper()
		n++
		fields := map[string]any{"reference": ref, "credit_id": fmt.Sprintf("c-%d", n), "account_number": account, "amount_kobo": kobo}
		if narration != "" {
			fields["narration"] = narration
		}
		e, ok := moneylog.FromEvent("iswallet/main", "virtual_account.credited", fields)
		if !ok {
			t.Fatal("no entry")
		}
		if _, _, err := mlog.Record(ctx, uuid.MustParse(seller), e); err != nil {
			t.Fatal(err)
		}
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	paid := func(seller *client, number string) float64 {
		t.Helper()
		return seller.must(200, "GET", "/v1/invoices/"+number, nil)["invoice"].(map[string]any)["paid_kobo"].(float64)
	}
	pay := func(bill string, kobo int64, sent bool) links.BillPayment {
		t.Helper()
		var pay links.BillPayment
		if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{buyerT}, func(tx pgx.Tx) error {
			b, err := links.GetBillTx(ctx, tx, bill)
			if err != nil {
				return err
			}
			pay, err = links.StartBillPaymentTx(ctx, tx, buyerT, links.PayIn{Bill: b.ID, AmountKobo: kobo, Connection: "main", BankCode: "035", AccountNumber: b.PayAccountNumber, By: "user:ada"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if sent {
			if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET sent_at = now() WHERE id = $1`, pay.ID); err != nil {
				t.Fatal(err)
			}
		}
		return pay
	}
	paired := func(p links.BillPayment) bool {
		t.Helper()
		var yes bool
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT paired_at IS NOT NULL FROM bill_payments WHERE id = $1`, p.ID).Scan(&yes); err != nil {
			t.Fatal(err)
		}
		return yes
	}

	d1 := invoice(dangote, 2000000, "2099-01-01")
	d2 := invoice(dangote, 1000000, "2099-02-01")
	k1 := invoice(kano, 1000000, "2099-01-15")
	if d1 != k1 {
		t.Fatalf("the two suppliers' first numbers differ: %s, %s", d1, k1)
	}
	// Both suppliers' first invoice is INV-0001: the number alone is
	// refused, naming them; the supplier's name chooses; so does chat.
	if e := buyer.must(409, "GET", "/v1/bills/"+d1, nil); !strings.Contains(e["error"].(string), "Dangote Stores") || !strings.Contains(e["error"].(string), "Kano Traders") {
		t.Errorf("two suppliers' bill: %v", e)
	}
	if b := buyer.must(200, "GET", "/v1/bills/Kano%20"+d1, nil); b["supplier"] != kanoID || b["reference"] != d1 {
		t.Errorf("by supplier: %v", b)
	}
	if r := w.say(t, buyerOn, "bill "+d1); !strings.Contains(r, "say which supplier") {
		t.Errorf("chat, two suppliers: %q", r)
	}
	if r := w.say(t, buyerOn, "bill kano "+d1); !strings.Contains(r, "from *Kano Traders*") {
		t.Errorf("chat, by supplier: %q", r)
	}

	// A payout of the newer bill, waiting to be approved: the credit of
	// its amount answers no token and goes to the oldest invoice.
	waiting := pay("Dangote "+d2, 1000000, false)
	credit(dangoteID, "lnk_1", "8800000001", 1000000, "")
	if paid(dangote, d1) != 1000000 || paid(dangote, d2) != 0 || paired(waiting) {
		t.Fatalf("a payout waiting to be approved: %s %v, %s %v, paired %v", d1, paid(dangote, d1), d2, paid(dangote, d2), paired(waiting))
	}
	// Sent, it answers: the invoice it names is paid, the payment paired.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET sent_at = now() WHERE id = $1`, waiting.ID); err != nil {
		t.Fatal(err)
	}
	credit(dangoteID, "lnk_1", "8800000001", 1000000, "")
	if paid(dangote, d2) != 1000000 || paid(dangote, d1) != 1000000 || !paired(waiting) {
		t.Fatalf("by the token: %s %v, %s %v, paired %v", d1, paid(dangote, d1), d2, paid(dangote, d2), paired(waiting))
	}

	// A payout to Dangote for INV-0001, sent; a Kano credit of the same
	// amount into Kano's account never hears Dangote's token, though Kano's
	// INV-0001 would take it: Kano's invoice is paid oldest-first, and
	// Dangote's payment stays unpaired for Dangote's own credit.
	rest := pay("Dangote "+d1, 1000000, true)
	credit(kanoID, "lnk_2", "8800000002", 1000000, "")
	if paid(kano, k1) != 1000000 || paired(rest) {
		t.Fatalf("Kano's credit: %s %v, Dangote's payment paired %v", k1, paid(kano, k1), paired(rest))
	}
	credit(dangoteID, "lnk_1", "8800000001", 1000000, "")
	if paid(dangote, d1) != 2000000 || !paired(rest) {
		t.Fatalf("Dangote's credit: %s %v, paired %v", d1, paid(dangote, d1), paired(rest))
	}

	// Two equal payouts in the window pair neither: the credit goes oldest
	// first, and both payments stay unpaired (for the three-day alert).
	d3 := invoice(dangote, 500000, "2099-03-01")
	d4 := invoice(dangote, 500000, "2099-04-01")
	p3, p4 := pay(d3, 500000, true), pay(d4, 500000, true)
	credit(dangoteID, "lnk_1", "8800000001", 500000, "")
	if paid(dangote, d3) != 500000 || paid(dangote, d4) != 0 || paired(p3) || paired(p4) {
		t.Fatalf("two equal payouts: %s %v, %s %v, paired %v %v", d3, paid(dangote, d3), d4, paid(dangote, d4), paired(p3), paired(p4))
	}
	// The rail passed the narration on: the invoice it names is paid, and
	// the payout whose token it carries is paired.
	credit(dangoteID, "lnk_1", "8800000001", 500000, "TRF "+p4.Token+" ADA KITCHEN")
	if paid(dangote, d4) != 500000 || !paired(p4) || paired(p3) {
		t.Fatalf("by the narration: %s %v, paired %v %v", d4, paid(dangote, d4), paired(p3), paired(p4))
	}

	// Every question is in the buyer's audit chain, with the amount asked.
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'link.payout_token' AND (detail->>'amount_kobo')::bigint > 0`, buyerID).Scan(&audited); err != nil || audited < 5 {
		t.Errorf("audited %d times in the buyer's chain: %v", audited, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id <> $1 AND action = 'link.payout_token'`, buyerID).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("audited %d times elsewhere: %v", audited, err)
	}
}

// A bill payment is settled by what became of its money, not its run
// alone (§6.2, §16.2): a run that failed before the rail took the money
// fails the payment and the bill is open again; a run that failed after
// the money went makes the payment unknown, the bill stays paying (a
// second Pay refused) and the owner is told; a payment no run ever
// started is failed after ten minutes. The rail taking the money is
// recorded from the run, and the payout's money-log entry names the
// bill. A payout the bank confirmed three days ago that no match paired
// raises one alert, and the owner confirms the pair from it; the
// supplier is told which invoice the money was for.
func TestBillPaymentSettledByItsMoney(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	mlog := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: mlog,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	payments := &links.Payments{Pool: w.env.DB.App}
	for _, c := range []*connector.Connector{billsconn.New(payments), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) {
		return []map[string]any{{"name": "Wema Bank Plc", "code": "035"}}, nil
	}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	buyerT := uuid.MustParse(buyerID)
	const sellerOn, buyerOn = "+2348010000381", "+2348010000382"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	n := 0
	bill := func(kobo int) map[string]any {
		t.Helper()
		n++
		x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
			"amount_kobo": kobo}, "Idempotency-Key", fmt.Sprintf("b-%d", n))
		seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", fmt.Sprintf("s-%d", n))
		if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
		return buyer.must(200, "GET", "/v1/bills/"+x["number"].(string), nil)
	}
	buyer.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	buyer.must(200, "PUT", "/v1/bills/pay/setup", map[string]any{"rail_connection": "main"})
	status := func(bill map[string]any) (string, string) {
		t.Helper()
		b := buyer.must(200, "GET", "/v1/bills/"+bill["id"].(string), nil)
		ps := buyer.must(200, "GET", "/v1/bills/"+bill["id"].(string)+"/payments", nil)["payments"].([]any)
		return b["status"].(string), ps[0].(map[string]any)["status"].(string)
	}
	runEnds := func(run any, how string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE runs SET status = $2, ended_at = now() WHERE id = $1`, run, how); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		if _, err := w.srv.BillPaymentsTick(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
	}

	// The run failed before the money went: the payment failed, the bill
	// open again.
	a := bill(4250000)
	out := buyer.must(202, "POST", "/v1/bills/"+a["id"].(string)+"/pay", nil)
	runEnds(out["run_id"], "failed")
	tick()
	if b, p := status(a); b != "open" || p != "failed" {
		t.Fatalf("a run failed before the money went: bill %s, payment %s", b, p)
	}

	// The run died after the rail took the money: the payment is unknown,
	// the bill stays paying, a second Pay is refused, and the owner is told
	// once.
	out = buyer.must(202, "POST", "/v1/bills/"+a["id"].(string)+"/pay", nil)
	pay2 := out["payment"].(map[string]any)["id"].(string)
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{buyerT}, func(tx pgx.Tx) error {
		return links.MarkPayoutSentTx(ctx, tx, buyerT, uuid.MustParse(out["run_id"].(string)), "po_2")
	}); err != nil {
		t.Fatal(err)
	}
	runEnds(out["run_id"], "cancelled")
	before := len(w.graph.Sent(buyerOn))
	tick()
	if b, p := status(a); b != "paying" || p != "unknown" {
		t.Fatalf("a run died after the money went: bill %s, payment %s", b, p)
	}
	if e := buyer.must(409, "POST", "/v1/bills/"+a["id"].(string)+"/pay", nil); !strings.Contains(e["error"].(string), "already under way") {
		t.Errorf("paid twice: %v", e)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "could not record how it ended") {
		t.Errorf("the owner told: %+v", told)
	}
	tick()
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 {
		t.Errorf("told again: %+v", told)
	}
	// The bank's word, when it comes, settles it: paid.
	if _, err := payments.FinishBillPayment(ctx, buyerT, uuid.MustParse(pay2), "paid", "DANGOTE/ADA"); err != nil {
		t.Fatal(err)
	}
	if b, p := status(a); b != "open" || p != "paid" {
		t.Fatalf("the bank confirmed: bill %s, payment %s", b, p)
	}
	// The payment knows its payout's entry by the rail's id.
	var ref string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payout_ref FROM bill_payments WHERE id = $1`, pay2).Scan(&ref); err != nil || ref != "po_2" {
		t.Errorf("the payment's payout: %q %v", ref, err)
	}

	// A payment no run ever started is failed after ten minutes.
	c := bill(100000)
	var orphan links.BillPayment
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{buyerT}, func(tx pgx.Tx) error {
		var err error
		orphan, err = links.StartBillPaymentTx(ctx, tx, buyerT, links.PayIn{Bill: uuid.MustParse(c["id"].(string)), AmountKobo: 100000, Connection: "main", BankCode: "035", AccountNumber: "8800000001", By: "user:ada"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tick()
	if b, p := status(c); b != "paying" || p != "pending" {
		t.Fatalf("a fresh payment with no run: bill %s, payment %s", b, p)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET created_at = now() - interval '11 minutes' WHERE id = $1`, orphan.ID); err != nil {
		t.Fatal(err)
	}
	tick()
	if b, p := status(c); b != "open" || p != "failed" {
		t.Fatalf("a payment no run started: bill %s, payment %s", b, p)
	}

	// A payout the bank confirmed three days ago that no match paired: one
	// alert, with a button; pressed, the payment is paired and the
	// supplier is told which invoice the money was for.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bill_payments SET sent_at = now() - interval '4 days' WHERE id = $1`, pay2); err != nil {
		t.Fatal(err)
	}
	before = len(w.graph.Sent(buyerOn))
	tick()
	told := w.lastTo(t, buyerOn, before)
	if len(told) != 1 || !strings.Contains(told[0].Text, "reached *Dangote Stores* three days ago") || len(told[0].Buttons) != 1 || told[0].Buttons[0].ID != "bp:"+pay2 {
		t.Fatalf("the owner told of an unpaired payout: %+v", told)
	}
	tick()
	if again := w.lastTo(t, buyerOn, before); len(again) != 1 {
		t.Errorf("told again: %+v", again)
	}
	sellerBefore := len(w.graph.Sent(sellerOn))
	if r := w.press(t, buyerOn, "bp:"+pay2); len(r) != 1 || !strings.Contains(r[0].Text, "your payment was for "+a["reference"].(string)) {
		t.Fatalf("confirmed: %+v", r)
	}
	if ps := buyer.must(200, "GET", "/v1/bills/"+a["id"].(string)+"/payments", nil)["payments"].([]any); ps[0].(map[string]any)["paired_at"] == nil {
		t.Errorf("not paired: %v", ps[0])
	}
	if told := w.lastTo(t, sellerOn, sellerBefore); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* says its transfer of ₦42,500") || !strings.Contains(told[0].Text, "was for invoice "+a["reference"].(string)) {
		t.Errorf("the seller told: %+v", told)
	}
	var kind string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind FROM link_notices WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 1`, sellerID).Scan(&kind); err != nil || kind != "paired" {
		t.Errorf("the seller's notice: %q %v", kind, err)
	}
	// Confirmed already, or not yet confirmed by the bank: refused; by the
	// API, a confirmed payment is paired once.
	buyer.must(409, "POST", "/v1/bills/"+a["id"].(string)+"/payments/"+pay2+"/confirm-pair", nil)
	buyer.must(409, "POST", "/v1/bills/"+c["id"].(string)+"/payments/"+orphan.ID.String()+"/confirm-pair", nil)
	if r := w.press(t, buyerOn, "bp:"+pay2); len(r) != 1 || !strings.Contains(r[0].Text, "paired already") {
		t.Errorf("pressed again: %+v", r)
	}
	d := bill(200000)
	out = buyer.must(202, "POST", "/v1/bills/"+d["id"].(string)+"/pay", nil)
	payD := out["payment"].(map[string]any)["id"].(string)
	if _, err := payments.FinishBillPayment(ctx, buyerT, uuid.MustParse(payD), "paid", ""); err != nil {
		t.Fatal(err)
	}
	if p := buyer.must(200, "POST", "/v1/bills/"+d["id"].(string)+"/payments/"+payD+"/confirm-pair", nil); p["paired_at"] == nil || p["status"] != "paid" {
		t.Errorf("confirmed by the API: %v", p)
	}
}
