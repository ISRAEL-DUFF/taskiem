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

// Matching in chat (T5): a payment with suggestions that is not matched
// on its own is put to the members who manage invoices, with Yes, match
// and No; No offers the next suggestion; forged and misdirected presses
// are refused; exact and automatic matches ask no one.
func TestWhatsAppMatching(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	manager := w.member(t, owner, "op@adafoods.test", "operator")
	viewer := w.member(t, owner, "vic@adafoods.test", "viewer")
	other := w.tenant(t, "Bee Ltd", "bee@bee.test")
	const on, mn, vn, bn = "+2348010000041", "+2348010000042", "+2348010000043", "+2348010000044"
	w.bind(t, owner, on)
	w.bind(t, manager, mn)
	w.bind(t, viewer, vn)
	w.bind(t, other, bn)
	for _, n := range []string{on, mn, vn, bn} {
		w.say(t, n, "help") // the conversation window is open
	}
	tenant := tenantIDNamed(t, w.world, "Ada Foods")
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault, Suggested: w.srv.MatchSuggested}

	invoice := func(name string, kobo int) map[string]any {
		x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": name, "phone": "+2348031234567"}, "amount_kobo": kobo})
		owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-"+name)
		return x
	}
	heard := func(event string, fields map[string]any) {
		t.Helper()
		if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", event, fields); err != nil {
			t.Fatal(err)
		}
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		w.tick(t)
	}
	asked := func(n string) []whatsapptest.Sent {
		var out []whatsapptest.Sent
		for _, s := range w.graph.Sent(n) {
			if strings.HasPrefix(s.Text, "[Ada Foods] ₦") && strings.Contains(s.Text, "came in on") {
				out = append(out, s)
			}
		}
		return out
	}
	status := func(number string) string {
		return owner.must(200, "GET", "/v1/invoices/"+number, nil)["invoice"].(map[string]any)["status"].(string)
	}

	first, second := invoice("Bola Ade", 900000), invoice("Chidi Eze", 900000)
	exact, auto, single := invoice("Dayo Bello", 1250000), invoice("Efe Okon", 700000), invoice("Funmi Ojo", 1100000)

	// Exact: a payment into the account Taskiem gave asks no one.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
		VALUES (gen_random_uuid(), $1, $2, 1, 'transfer', 'paystack', 'main', 'tsk_acct_9', 1250000, 'open')`, tenant, exact["id"]); err != nil {
		t.Fatal(err)
	}
	heard("payment.received", map[string]any{"payment_id": "tsk_acct_9", "amount_kobo": int64(1250000)})
	if st := status(exact["number"].(string)); st != "paid" {
		t.Fatalf("exact: %s", st)
	}
	// Automatic: a strong, clear suggestion with the setting on asks no one.
	st := owner.must(200, "GET", "/v1/invoice-settings", nil)
	st["auto_match"] = true
	owner.must(200, "PUT", "/v1/invoice-settings", st)
	heard("virtual_account.credited", map[string]any{"credit_id": "C-1", "reference": "VA-AUTO", "amount_kobo": int64(700000), "payer_name": "EFE OKON"})
	if st := status(auto["number"].(string)); st != "paid" {
		t.Fatalf("automatic: %s", st)
	}
	for _, n := range []string{on, mn, vn} {
		if a := asked(n); len(a) != 0 {
			t.Fatalf("asked %s about an exact or automatic match: %+v", n, a)
		}
	}
	st["auto_match"] = false
	owner.must(200, "PUT", "/v1/invoice-settings", st)

	// Suggested: the managers are asked, the viewer is not; once.
	heard("virtual_account.credited", map[string]any{"credit_id": "C-1", "reference": "VA-1", "amount_kobo": int64(900000), "payer_name": "BOLA ADE"})
	ma, oa := asked(mn), asked(on)
	if len(ma) != 1 || len(oa) != 1 || len(asked(vn)) != 0 {
		t.Fatalf("asked: manager %+v, owner %+v, viewer %+v", ma, oa, asked(vn))
	}
	q := ma[0]
	if !strings.HasPrefix(q.Text, "[Ada Foods] ₦9,000 came in on ") || !strings.Contains(q.Text, "through Paystack (main)") ||
		!strings.Contains(q.Text, "Is this for "+first["number"].(string)+", ₦9,000 due from the customer?") ||
		!strings.Contains(q.Text, "the payer's name matches") || !strings.Contains(q.Text, "No shows the next") {
		t.Errorf("question: %q", q.Text)
	}
	if strings.Contains(strings.ToLower(q.Text), "bola") || strings.Contains(strings.ToLower(q.Text), "ade ") {
		t.Errorf("the question names someone: %q", q.Text)
	}
	if len(q.Buttons) != 2 || !strings.HasPrefix(q.Buttons[0].ID, "mt1.") || q.Buttons[0].Title != "Yes, match" || q.Buttons[1].Title != "No" {
		t.Fatalf("buttons: %+v", q.Buttons)
	}
	w.tick(t)
	if len(asked(mn)) != 1 {
		t.Error("asked twice")
	}
	press := func(from, id string) string {
		t.Helper()
		got := w.send(t, from, whatsapptest.ButtonReply(id, "x"))
		if len(got) != 1 {
			t.Fatalf("press: %+v", got)
		}
		return got[0].Text
	}

	// Forged, someone else's, another business's.
	yes, no := q.Buttons[0].ID, q.Buttons[1].ID
	forged := yes[:len(yes)-2] + "AA"
	if forged == yes {
		forged = yes[:len(yes)-2] + "BB"
	}
	if r := press(mn, forged); !strings.Contains(r, "not valid") {
		t.Errorf("forged: %q", r)
	}
	if r := press(on, yes); !strings.Contains(r, "sent to someone else") {
		t.Errorf("another member: %q", r)
	}
	if r := press(bn, yes); !strings.Contains(r, "not valid") {
		t.Errorf("another business: %q", r)
	}
	if r := press(vn, oa[0].Buttons[0].ID); !strings.Contains(r, "someone else") {
		t.Errorf("viewer: %q", r)
	}
	if st := status(first["number"].(string)); st != "sent" {
		t.Fatalf("a refused press changed the invoice: %s", st)
	}

	// No: turned down, and the next one offered.
	got := w.send(t, mn, whatsapptest.ButtonReply(no, "No"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Turned down.") || !strings.Contains(got[0].Text, "Is this for "+second["number"].(string)) ||
		len(got[0].Buttons) != 2 {
		t.Fatalf("no: %+v", got)
	}
	if r := press(mn, no); !strings.Contains(r, "Already turned down") {
		t.Errorf("no again: %q", r)
	}
	// Yes on the next: matched and paid.
	next := got[0].Buttons[0].ID
	if r := press(mn, next); !strings.Contains(r, "Matched to "+second["number"].(string)+". It is now paid.") {
		t.Errorf("yes: %q", r)
	}
	if st := status(second["number"].(string)); st != "paid" {
		t.Errorf("after yes: %s", st)
	}
	if r := press(mn, next); !strings.Contains(r, "Already matched to "+second["number"].(string)) {
		t.Errorf("yes again: %q", r)
	}
	// The owner's buttons, for the suggestion turned down.
	if r := press(on, oa[0].Buttons[0].ID); !strings.Contains(r, "Already turned down") {
		t.Errorf("owner's yes: %q", r)
	}
	var by string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT decided_by FROM money_suggestions WHERE invoice_id = $1`, second["id"]).Scan(&by); err != nil || by == "" || by == "system:matching" {
		t.Errorf("decided by %q: %v", by, err)
	}

	// One suggestion, turned down: left for the web app.
	heard("virtual_account.credited", map[string]any{"credit_id": "C-1", "reference": "VA-3", "amount_kobo": int64(1100000)})
	a := asked(mn)
	if len(a) != 2 || !strings.Contains(a[1].Text, single["number"].(string)) || strings.Contains(a[1].Text, "No shows the next") {
		t.Fatalf("single: %+v", a)
	}
	if r := press(mn, a[1].Buttons[1].ID); !strings.Contains(r, "left for you to match in the web app: "+waPublicURL+"/invoices") {
		t.Errorf("left: %q", r)
	}
	// Nothing is left open.
	if l := owner.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any); len(l) != 0 {
		t.Errorf("open: %v", l)
	}

	// Part of an invoice that takes part payments: part-paid, with what is due.
	part := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Gbenga Ola", "phone": "+2348039876543"},
		"amount_kobo": 2000000, "allow_part_payments": true})
	owner.must(200, "POST", "/v1/invoices/"+part["id"].(string)+"/send", nil, "Idempotency-Key", "send-part")
	heard("virtual_account.credited", map[string]any{"credit_id": "C-1", "reference": "VA-4", "amount_kobo": int64(500000), "payer_name": "OLA GBENGA"})
	a = asked(mn)
	if len(a) != 3 || !strings.Contains(a[2].Text, part["number"].(string)) {
		t.Fatalf("part: %+v", a)
	}
	if r := press(mn, a[2].Buttons[0].ID); !strings.Contains(r, "Matched to "+part["number"].(string)+". It is now part-paid (₦15,000 still due).") {
		t.Errorf("part: %q", r)
	}
}
