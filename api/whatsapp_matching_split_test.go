package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A payment for more than is due on the invoice suggested is asked about
// as a split across the customer's open invoices, oldest first, with the
// rest as credit; Yes, match matches each part.
func TestWhatsAppMatchingSplit(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const on = "+2348010000051"
	w.bind(t, owner, on)
	w.say(t, on, "help")
	tenant := tenantIDNamed(t, w.world, "Ada Foods")
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault, Suggested: w.srv.MatchSuggested}
	invoice := func(kobo int, due string) string {
		x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola Ade", "phone": "+2348031234567"}, "amount_kobo": kobo, "due_on": due})
		owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-"+x["number"].(string))
		return x["number"].(string)
	}
	older, newer := invoice(200000, "2099-09-01"), invoice(300000, "2099-10-01")
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"reference": "VA-SPLIT", "amount_kobo": int64(600000), "payer_name": "BOLA ADE"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	var q *whatsapptest.Sent
	for _, s := range w.graph.Sent(on) {
		if strings.Contains(s.Text, "came in on") {
			q = &s
		}
	}
	if q == nil {
		t.Fatal("not asked")
	}
	want := "oldest first: " + older + " ₦2,000, " + newer + " ₦3,000, ₦1,000 left as the customer's credit."
	if !strings.Contains(q.Text, "It is more than that, so it would be split") || !strings.Contains(q.Text, want) {
		t.Fatalf("question: %q", q.Text)
	}
	if strings.Contains(strings.ToLower(q.Text), "bola") {
		t.Errorf("the question names someone: %q", q.Text)
	}
	got := w.send(t, on, whatsapptest.ButtonReply(q.Buttons[0].ID, "Yes, match"))
	if len(got) != 1 || got[0].Text != "[Ada Foods] Matched in parts: "+older+" ₦2,000, "+newer+" ₦3,000, ₦1,000 left as the customer's credit." {
		t.Fatalf("yes: %+v", got)
	}
	for _, n := range []string{older, newer} {
		if st := owner.must(200, "GET", "/v1/invoices/"+n, nil)["invoice"].(map[string]any)["status"]; st != "paid" {
			t.Errorf("%s %v", n, st)
		}
	}
}
