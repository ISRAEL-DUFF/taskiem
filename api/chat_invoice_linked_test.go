package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// An invoice made in chat for a business the owner is linked with, which
// buys from them, asks for no phone or email: it goes into that business's
// bills. Several linked businesses that fit the name ask which; a name that
// fits none asks for a phone number as before.
func TestChatInvoiceToALinkedBusiness(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	seller, buyer, sellerID, _ := linkedPair(t, w) // Ada's Kitchen buys from Dangote Stores
	// A second business that buys from the seller, with a name that also fits "Ada".
	bakery := w.tenant(t, "Ada's Bakery", "bakery@bills.test")
	bakery.must(201, "POST", "/v1/links", map[string]any{"business": sellerID, "role": "supplier"})
	for _, l := range seller.must(200, "GET", "/v1/links", nil)["links"].([]any) {
		if m := l.(map[string]any); m["state"] == "asked_us" {
			seller.must(200, "POST", "/v1/links/"+m["id"].(string)+"/accept", nil)
		}
	}
	w.srv.WaitBackground()

	verifyPhone(t, w.world, seller, "phone", "business")
	const on = "+2348010000411"
	w.bind(t, seller, on)

	// One business fits: no phone asked, shown, sent as a bill.
	r := w.say(t, on, "invoice Ada's Kitchen 25,000 for rice")
	if strings.Contains(r, "phone number") || !strings.Contains(r, "Ada's Kitchen") || !strings.Contains(r, "linked with") || !strings.Contains(r, "bills") {
		t.Fatalf("a linked business: %q", r)
	}
	r = w.say(t, on, "yes")
	if !strings.Contains(r, "Sent INV-") || !strings.Contains(r, "Ada's Kitchen's bills") {
		t.Fatalf("sent: %q", r)
	}
	if n, err := w.srv.LinkMirrorTick(ctx); err != nil || n != 1 {
		t.Fatalf("mirrored %d: %v", n, err)
	}
	bills := buyer.must(200, "GET", "/v1/bills", nil)["bills"].([]any)
	if len(bills) != 1 || bills[0].(map[string]any)["amount_kobo"] != float64(2500000) {
		t.Fatalf("the bill: %v", bills)
	}

	// Two fit "Ada": which one?
	r = w.say(t, on, "invoice Ada 5,000 for bread")
	if !strings.Contains(r, "Which Ada") || !strings.Contains(r, "1. Ada's Bakery") && !strings.Contains(r, "2. Ada's Bakery") || !strings.Contains(r, "Ada's Kitchen") {
		t.Fatalf("which: %q", r)
	}
	pick := "1"
	if strings.Contains(r, "2. Ada's Bakery") {
		pick = "2"
	}
	r = w.say(t, on, pick)
	if strings.Contains(r, "phone number") || !strings.Contains(r, "Ada's Bakery") || !strings.Contains(r, "linked with") {
		t.Fatalf("picked: %q", r)
	}
	w.say(t, on, "no")

	// Nobody linked by that name: the phone number, as before.
	r = w.say(t, on, "invoice Chidi 3,000 for eggs")
	if !strings.Contains(r, "phone number or email") {
		t.Fatalf("not linked: %q", r)
	}
	w.say(t, on, "cancel")
}
