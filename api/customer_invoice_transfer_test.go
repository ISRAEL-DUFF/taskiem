package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A customer pays an invoice by transfer from their chat with the
// business in the app (the payments flow, part 1, settled 4): an account
// number for what is due, shown only to the customer the invoice is for;
// the open one again; paid, it says so.
func TestCustomerInvoiceTransfer(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	rails := &chatRails{}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce, Rails: rails}
	tolu, _ := w.personIn(t, "+2348031110041")
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	other, _ := w.personIn(t, "+2348031110042")
	otherChat := other.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)

	// The business's invoice to Tolu, as its card would carry.
	x := w.owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Tolu", "phone": "+2348031110041"}, "amount_kobo": 4500000}, "Idempotency-Key", "ct-1")
	w.owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "ct-s-1")
	path := "/v1/me/chats/" + chat + "/invoices/" + x["id"].(string) + "/transfer"
	if got := tolu.must(200, "GET", path, nil); got["state"] != "none" || got["due_kobo"] != float64(4500000) {
		t.Fatalf("before: %v", got)
	}
	got := tolu.must(200, "POST", path, map[string]any{})
	if got["state"] != "open" || got["account_number"] != "9900112233" || got["amount_kobo"] != float64(4500000) || got["expires_at"] == nil {
		t.Fatalf("the number: %v", got)
	}
	if again := tolu.must(200, "POST", path, map[string]any{}); again["account_number"] != "9900112233" || rails.calls != 1 {
		t.Fatalf("again: %v (%d)", again, rails.calls)
	}
	// Another customer of the business: not theirs, from either chat.
	other.must(404, "POST", "/v1/me/chats/"+otherChat+"/invoices/"+x["id"].(string)+"/transfer", map[string]any{})
	other.must(404, "GET", "/v1/me/chats/"+chat+"/invoices/"+x["id"].(string)+"/transfer", nil)

	// Paid.
	var pid string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payment_id FROM customer_invoice_accounts WHERE invoice_id = $1`, x["id"]).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.Invoices.Received(ctx, w.biz, uuid.MustParse(x["id"].(string)), invoicing.Received{PaymentID: pid, AmountKobo: 4500000}); err != nil {
		t.Fatal(err)
	}
	if got := tolu.must(200, "GET", path, nil); got["state"] != "paid" {
		t.Fatalf("paid: %v", got)
	}
	tolu.must(409, "POST", path, map[string]any{})
}
