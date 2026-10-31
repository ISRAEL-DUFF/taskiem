package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
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
		!strings.Contains(r, "Pay into 8800000001 (Wema Bank): money paid into it is counted towards your invoices from this supplier, oldest first.") || !strings.Contains(r, "pay by card or transfer on its page: https://forms.taskiem.test/i/") {
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
