package api_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A school's cash posting at or above the threshold reaches the approvers
// on WhatsApp with Approve and Decline buttons, as a run's approval does;
// the requester is not asked; a tap decides it once; a second request can
// be declined.
func TestWhatsAppSchoolApprovals(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Greenfield", "owner@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "cash_threshold_naira": 50000})
	bursar := w.member(t, owner, "bursar@greenfield.test", "admin")
	second := w.member(t, owner, "second@greenfield.test", "owner")
	const on, bn, sn = "+2348010000011", "+2348010000012", "+2348010000013"
	w.bind(t, owner, on)
	w.bind(t, bursar, bn)
	w.bind(t, second, sn)
	w.say(t, on, "hi") // the owner's window is open
	owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})

	code, out := bursar.do("POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 5_000_000, "how": "cash"})
	if code != 202 {
		t.Fatalf("request: %d %v", code, out)
	}
	req := out["request"].(map[string]any)["id"].(string)
	w.tick(t)
	// The owner: buttons; the second owner (window closed): the template.
	// The bursar, who asked and cannot approve, hears nothing.
	om := w.graph.Last(t, on)
	if om.Type != "interactive" || len(om.Buttons) != 2 || !strings.HasPrefix(om.Buttons[0].ID, "tk1.") ||
		!strings.Contains(om.Text, "Approval needed: a cash posting · ₦50,000 (Schools)") || !strings.Contains(om.Text, "₦50,000 paid in cash for GCA/001 (JSS 1 A), posted by bursar@…") {
		t.Fatalf("the owner's request: %+v", om)
	}
	sm := w.graph.Last(t, sn)
	if sm.Type != "template" || len(sm.Payloads) != 2 {
		t.Fatalf("the second owner's request: %+v", sm)
	}
	for _, s := range w.graph.Sent(bn) {
		if strings.Contains(s.Text, "Approval needed") {
			t.Errorf("the requester was asked: %q", s.Text)
		}
	}
	before := len(w.graph.Sent(""))
	w.tick(t)
	if len(w.graph.Sent("")) != before {
		t.Error("a second tick sent again")
	}
	tap := func(from, tok string) string {
		t.Helper()
		got := w.send(t, from, whatsapptest.ButtonReply(tok, "Approve"))
		if len(got) != 1 {
			t.Fatalf("tap: %+v", got)
		}
		return got[0].Text
	}
	// The owner's button from the second owner's number: refused.
	if r := tap(sn, om.Buttons[0].ID); !strings.Contains(r, "someone else") {
		t.Errorf("wrong sender: %q", r)
	}
	// The owner approves: posted, once.
	if r := tap(on, om.Buttons[0].ID); !strings.Contains(r, "a cash posting · ₦50,000") || !strings.Contains(r, "approved") {
		t.Errorf("approved: %q", r)
	}
	if got := owner.must(200, "GET", "/v1/schools/payments?status=posted", nil)["postings"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != req {
		t.Fatalf("posted %v", got)
	}
	if r := tap(on, om.Buttons[1].ID); !strings.Contains(r, "already") {
		t.Errorf("the pair, spent: %q", r)
	}
	// The second owner's buttons now find it decided.
	if r := tap(sn, sm.Payloads[0]); !strings.Contains(r, "already") {
		t.Errorf("decided already: %q", r)
	}
	// A second request, declined from the phone.
	code, out = bursar.do("POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 7_000_000, "how": "transfer"})
	if code != 202 {
		t.Fatalf("second request: %d %v", code, out)
	}
	w.tick(t)
	om2 := w.graph.Last(t, on)
	if !strings.Contains(om2.Text, "₦70,000 paid by transfer") {
		t.Fatalf("second request's text: %q", om2.Text)
	}
	if r := tap(on, om2.Buttons[1].ID); !strings.Contains(r, "rejected") && !strings.Contains(r, "declined") {
		t.Errorf("declined: %q", r)
	}
	if got := owner.must(200, "GET", "/v1/schools/payments?status=declined", nil)["postings"].([]any); len(got) != 1 || got[0].(map[string]any)["decision_reason"] != "Declined from whatsapp" {
		t.Fatalf("declined %v", got)
	}
	if n := schoolAudits(t, w.world, "Greenfield", "school.approval.decide"); n != 2 {
		t.Errorf("%d decisions audited", n)
	}
}
