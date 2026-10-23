package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// An invoice from chat (decision 0045, T3): who, how much and what for;
// the customer's number asked and checked; shown, then sent on Yes.
func TestWhatsAppInvoice(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const on, vn = "+2348010000019", "+2348010000017"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@adafoods.test", "viewer")
	w.bind(t, viewer, vn)

	if r := w.say(t, on, "help"); !strings.Contains(r, "*invoice*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, vn, "help"); strings.Contains(r, "*invoice*") {
		t.Errorf("viewer help: %q", r)
	}
	if r := w.say(t, vn, "invoice Bola 25,000 for rice"); !strings.Contains(r, "cannot make invoices") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "invoice Bola"); !strings.Contains(r, "*invoice Ada 25,000 for 5 bags of rice*") {
		t.Errorf("how: %q", r)
	}
	if r := w.say(t, on, "Invoice Bola Ade ₦25,000.50 for 5 bags of rice"); !strings.Contains(r, "Bola Ade's phone number or email") {
		t.Fatalf("start: %q", r)
	}
	if r := w.say(t, on, "call her"); !strings.Contains(r, "not a phone number or an email") {
		t.Errorf("bad address: %q", r)
	}
	confirm := w.send(t, on, whatsapptest.Text("0803 123 4567"))
	if len(confirm) != 1 || len(confirm[0].Buttons) != 2 {
		t.Fatalf("confirmation: %+v", confirm)
	}
	if ct := confirm[0].Text; !strings.Contains(ct, "Send Bola Ade (•••4567) an invoice for ₦25,000.50: 5 bags of rice?") || strings.Contains(ct, "8031234567") {
		t.Errorf("confirmation text: %q", ct)
	}
	// The number waits sealed in the session.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM chat_sessions ORDER BY updated_at DESC LIMIT 1`).Scan(&raw); err == nil && strings.Contains(raw, "8031234567") {
		t.Errorf("the number is in the session: %s", raw)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Sent INV-0001 to Bola Ade for ₦25,000.50") {
		t.Fatalf("sent: %q", r)
	}
	x := owner.must(200, "GET", "/v1/invoices/INV-0001?reveal=true", nil)["invoice"].(map[string]any)
	if x["status"] != "sent" || x["total_kobo"] != float64(2500050) || x["customer"].(map[string]any)["phone"] != "+2348031234567" ||
		x["lines"].([]any)[0].(map[string]any)["description"] != "5 bags of rice" {
		t.Errorf("invoice %v", x)
	}

	// No leaves nothing behind.
	w.say(t, on, "bill Chi 10k")
	w.say(t, on, "chi@example.com")
	if r := w.say(t, on, "no"); !strings.Contains(r, "Not sent") {
		t.Errorf("no: %q", r)
	}
	if l := owner.must(200, "GET", "/v1/invoices", nil)["invoices"].([]any); len(l) != 1 {
		t.Errorf("invoices %d", len(l))
	}
}
