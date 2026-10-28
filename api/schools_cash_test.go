package api_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Cash at the desk, approvals and receipts through the API
// (docs/spec/schools.md B5): a posting under the cash threshold is
// counted at once with a receipt; at or above it, it waits for a second
// person, who is never the requester; declined, nothing reaches the fees;
// discounts at or above the discount threshold wait the same way;
// receipts are listed, found by number, rendered as PDF and voided when
// a payment is undone.
func TestSchoolCashApprovalsAndReceipts(t *testing.T) {
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	schools.WireReceipts(w.srv.Invoices, &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.srv.Invoices})
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")
	second := addMember(t, w, owner, "bola", "owner")

	// The cash threshold is the owner's: null for none, 0 for every posting.
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "cash_threshold_naira": 50000})
	if st := owner.must(200, "GET", "/v1/settings/schools", nil); st["cash_threshold_naira"] != nil {
		t.Fatalf("threshold before %v", st["cash_threshold_naira"])
	}
	if st := owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "cash_threshold_naira": 50000}); st["cash_threshold_naira"] != float64(50000) {
		t.Fatalf("threshold set %v", st)
	}
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "cash_threshold_naira": -1})
	if n := schoolAudits(t, w, "Greenfield", "settings.schools.cash_threshold"); n != 1 {
		t.Fatalf("%d threshold audits", n)
	}

	// A class, two siblings, a started term with fees, invoiced and sent.
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	term := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-14", "ends_on": "2099-12-11", "fees_due_on": "2099-09-07"})
	tid := term["id"].(string)
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\nChidi Eze,GCA/002,JSS 1 A,Ngozi Eze,08031234567\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/start", nil)
	owner.must(200, "PUT", "/v1/schools/terms/"+tid+"/fees/"+j1["id"].(string), map[string]any{"items": []any{
		map[string]any{"name": "Tuition", "amount_kobo": 10_000_000, "kind": "required"}}})
	p := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p["hash"], "send": true})
	ebube := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)["id"].(string)
	chidi := owner.must(200, "GET", "/v1/schools/students/GCA%2F002", nil)["student"].(map[string]any)["id"].(string)

	// Under the threshold: posted at once, with its receipt. Only managers
	// post; readers see.
	viewer.must(403, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 2_000_000, "how": "cash"})
	admin.must(400, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 2_000_000, "how": "cheque"})
	posted := admin.must(201, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 2_000_000, "how": "cash", "note": "Term fees"}, "Idempotency-Key", "post-1")["posting"].(map[string]any)
	res := posted["result"].(map[string]any)
	parts := res["parts"].([]any)
	if posted["status"] != "posted" || posted["name"] != "E•••" || len(parts) != 1 || parts[0].(map[string]any)["amount_kobo"] != float64(2_000_000) {
		t.Fatalf("posted %v", posted)
	}
	number := parts[0].(map[string]any)["receipt_number"].(string)
	if !strings.HasPrefix(number, "RCT-2026T1-0001") {
		t.Fatalf("receipt number %q", number)
	}
	// Again with the same key: the same posting, nothing counted twice.
	if again := admin.must(200, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 2_000_000, "how": "cash"}, "Idempotency-Key", "post-1")["posting"].(map[string]any); again["id"] != posted["id"] {
		t.Fatalf("retried: %v", again)
	}
	inv := owner.must(200, "GET", "/v1/invoices/"+parts[0].(map[string]any)["number"].(string), nil)["invoice"].(map[string]any)
	if inv["paid_kobo"] != float64(2_000_000) || inv["status"] != "part_paid" {
		t.Fatalf("Ebube's invoice %v", inv)
	}

	// Receipts: listed, by student and number, masked; revealed with
	// pii.reveal; the PDF renders.
	list := viewer.must(200, "GET", "/v1/schools/receipts?student="+ebube, nil)["receipts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["number"] != number || list[0].(map[string]any)["name"] != "E•••" || list[0].(map[string]any)["voided"] != false ||
		!strings.HasPrefix(list[0].(map[string]any)["link"].(string), "https://forms.taskiem.test/r/") {
		t.Fatalf("receipts %v", list)
	}
	rid := list[0].(map[string]any)["id"].(string)
	if byNumber := viewer.must(200, "GET", "/v1/schools/receipts?number="+strings.ToLower(number), nil)["receipts"].([]any); len(byNumber) != 1 {
		t.Fatalf("by number %v", byNumber)
	}
	viewer.must(403, "GET", "/v1/schools/receipts/"+rid+"?reveal=true", nil)
	if got := owner.must(200, "GET", "/v1/schools/receipts/"+number+"?reveal=true", nil)["receipt"].(map[string]any); got["name"] != "Ebube Eze" || got["balance_after_kobo"] != float64(8_000_000) {
		t.Fatalf("revealed %v", got)
	}
	viewer.must(404, "GET", "/v1/schools/receipts/RCT-2026T1-9999", nil)
	status, hdr, body := viewer.rawGet(t, "/v1/schools/receipts/"+rid+"/pdf")
	if status != 200 || hdr.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(body, "%PDF-1.4") || !strings.Contains(body, "/Title (Receipt "+number+")") {
		t.Fatalf("pdf: %d %s %d bytes", status, hdr.Get("Content-Type"), len(body))
	}
	if n := schoolAudits(t, w, "Greenfield", "school.receipt.pdf"); n != 1 {
		t.Fatalf("%d pdf audits", n)
	}

	// At the threshold: pending, nothing on the fees, a request for a
	// second person. The requester never decides it; someone without
	// school.approve cannot; a second owner can, once.
	code, out := admin.do("POST", "/v1/schools/payments", map[string]any{"student": "GCA/002", "amount_kobo": 5_000_000, "how": "transfer"})
	if code != 202 || out["code"] != "needs_approval" {
		t.Fatalf("at the threshold: %d %v", code, out)
	}
	req := out["request"].(map[string]any)
	if req["status"] != "pending" || req["kind"] != "cash_posting" || req["name"] != "C•••" {
		t.Fatalf("request %v", req)
	}
	if x := owner.must(200, "GET", "/v1/invoices?status=sent", nil); x == nil {
		t.Fatal("invoices")
	}
	if n := owner.must(200, "GET", "/v1/schools/receipts?student="+chidi, nil)["receipts"].([]any); len(n) != 0 {
		t.Fatalf("a pending posting made %d receipts", len(n))
	}
	admin.must(403, "GET", "/v1/schools/approvals", nil)
	admin.must(403, "POST", "/v1/schools/approvals/"+req["id"].(string), map[string]any{"decision": "approved"})
	inbox := owner.must(200, "GET", "/v1/schools/approvals", nil)["requests"].([]any)
	if len(inbox) != 1 || inbox[0].(map[string]any)["id"] != req["id"] || inbox[0].(map[string]any)["yours"] == true {
		t.Fatalf("inbox %v", inbox)
	}
	if pend := admin.must(200, "GET", "/v1/schools/payments?status=pending", nil)["postings"].([]any); len(pend) != 1 || pend[0].(map[string]any)["yours"] != true {
		t.Fatalf("the requester's view %v", pend)
	}
	// The owner's own request: nobody approves their own.
	code, out = owner.do("POST", "/v1/schools/payments", map[string]any{"student": "GCA/002", "amount_kobo": 6_000_000, "how": "cash"})
	if code != 202 {
		t.Fatalf("the owner's request: %d %v", code, out)
	}
	own := out["request"].(map[string]any)["id"].(string)
	if mine := owner.must(200, "GET", "/v1/schools/approvals", nil)["requests"].([]any); len(mine) != 2 {
		t.Fatalf("inbox with the owner's own %v", mine)
	}
	owner.must(409, "POST", "/v1/schools/approvals/"+own, map[string]any{"decision": "approved"})
	second.must(400, "POST", "/v1/schools/approvals/"+own, map[string]any{"decision": "declined"})
	second.must(400, "POST", "/v1/schools/approvals/"+own, map[string]any{"decision": "maybe"})
	declined := second.must(200, "POST", "/v1/schools/approvals/"+own, map[string]any{"decision": "declined", "reason": "No cash in the box"})["request"].(map[string]any)
	if declined["status"] != "declined" || declined["decision_reason"] != "No cash in the box" || declined["result"] != nil {
		t.Fatalf("declined %v", declined)
	}
	// Admin's request approved by the owner: posted, with a receipt.
	approved := owner.must(200, "POST", "/v1/schools/approvals/"+req["id"].(string), map[string]any{"decision": "approved"})["request"].(map[string]any)
	ares := approved["result"].(map[string]any)
	if approved["status"] != "posted" || len(ares["parts"].([]any)) != 1 || ares["parts"].([]any)[0].(map[string]any)["receipt_number"] == "" {
		t.Fatalf("approved %v", approved)
	}
	owner.must(409, "POST", "/v1/schools/approvals/"+req["id"].(string), map[string]any{"decision": "approved"})
	if left := owner.must(200, "GET", "/v1/schools/approvals", nil)["requests"].([]any); len(left) != 0 {
		t.Fatalf("inbox after %v", left)
	}
	chidiReceipts := owner.must(200, "GET", "/v1/schools/receipts?student="+chidi, nil)["receipts"].([]any)
	if len(chidiReceipts) != 1 || chidiReceipts[0].(map[string]any)["amount_kobo"] != float64(5_000_000) {
		t.Fatalf("Chidi's receipts %v", chidiReceipts)
	}
	// The requester was told, in their notifications.
	if items := admin.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any); len(items) == 0 || !strings.HasPrefix(items[0].(map[string]any)["title"].(string), "Approved: a cash posting") {
		t.Fatalf("the requester's notifications %v", items)
	}

	// Discounts at or above the discount threshold wait the same way.
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "discount_threshold_naira": 20000})
	code, out = admin.do("POST", "/v1/schools/students/"+ebube+"/discounts", map[string]any{"kind": "hardship", "amount_kobo": 2_000_000, "reason": "Hard year"})
	if code != 202 || out["code"] != "needs_approval" || out["request"].(map[string]any)["kind"] != "discount" {
		t.Fatalf("discount at the threshold: %d %v", code, out)
	}
	if ds := owner.must(200, "GET", "/v1/schools/students/"+ebube+"/discounts", nil)["discounts"].([]any); len(ds) != 0 {
		t.Fatalf("given before approval %v", ds)
	}
	second.must(200, "POST", "/v1/schools/approvals/"+out["request"].(map[string]any)["id"].(string), map[string]any{"decision": "approved"})
	if ds := owner.must(200, "GET", "/v1/schools/students/"+ebube+"/discounts", nil)["discounts"].([]any); len(ds) != 1 || ds[0].(map[string]any)["reason"] != "Hard year" {
		t.Fatalf("after approval %v", ds)
	}
	code, out = admin.do("POST", "/v1/invoices/"+inv["number"].(string)+"/amendments", map[string]any{"kind": "discount", "amount_kobo": 2_000_000, "reason": "Hardship"})
	if code != 202 || out["request"].(map[string]any)["kind"] != "discount_amendment" || out["request"].(map[string]any)["invoice_number"] != inv["number"] {
		t.Fatalf("amendment at the threshold: %d %v", code, out)
	}
	second.must(200, "POST", "/v1/schools/approvals/"+out["request"].(map[string]any)["id"].(string), map[string]any{"decision": "approved"})
	if x := owner.must(200, "GET", "/v1/invoices/"+inv["number"].(string), nil)["invoice"].(map[string]any); len(x["amendments"].([]any)) != 1 || x["total_kobo"] != float64(8_000_000) {
		t.Fatalf("amended %v", x)
	}
	// A line taken out lowers the invoice as a discount does: it waits too.
	admin.must(201, "POST", "/v1/invoices/"+inv["number"].(string)+"/amendments", map[string]any{"kind": "add", "description": "School bus", "amount_kobo": 3_000_000, "reason": "Took the bus"})
	code, out = admin.do("POST", "/v1/invoices/"+inv["number"].(string)+"/amendments", map[string]any{"kind": "remove", "line": 2, "reason": "Never took the bus"})
	if code != 202 || out["request"].(map[string]any)["kind"] != "discount_amendment" || out["request"].(map[string]any)["amount_kobo"] != float64(3_000_000) {
		t.Fatalf("removal at the threshold: %d %v", code, out)
	}
	second.must(200, "POST", "/v1/schools/approvals/"+out["request"].(map[string]any)["id"].(string), map[string]any{"decision": "declined", "reason": "The bus log has them"})

	// Cash entered on Money and matched by hand at the threshold waits the
	// same way: 202, nothing on the fees.
	entered := admin.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 5_000_000, "description": "Fees at the gate"})
	code, out = admin.do("POST", "/v1/money/entries/"+entered["entry_id"].(string)+"/match", map[string]any{"invoice": inv["number"]})
	if code != 202 || out["code"] != "needs_approval" || out["request"].(map[string]any)["kind"] != "cash_match" || out["request"].(map[string]any)["invoice_number"] != inv["number"] {
		t.Fatalf("matched by hand at the threshold: %d %v", code, out)
	}
	if x := owner.must(200, "GET", "/v1/invoices/"+inv["number"].(string), nil)["invoice"].(map[string]any); x["paid_kobo"] != float64(2_000_000) {
		t.Fatalf("held money reached the fees %v", x)
	}
	second.must(200, "POST", "/v1/schools/approvals/"+out["request"].(map[string]any)["id"].(string), map[string]any{"decision": "approved"})
	if x := owner.must(200, "GET", "/v1/invoices/"+inv["number"].(string), nil)["invoice"].(map[string]any); x["paid_kobo"] != float64(7_000_000) {
		t.Fatalf("after approval %v", x)
	}

	// Undoing Ebube's payment voids its receipt, which stays.
	pid := inv["payments"].([]any)[0].(map[string]any)["id"].(string)
	owner.must(200, "POST", "/v1/invoices/"+inv["number"].(string)+"/payments/"+pid+"/undo", nil)
	if got := viewer.must(200, "GET", "/v1/schools/receipts/"+rid, nil)["receipt"].(map[string]any); got["voided"] != true || got["void_reason"] != "the payment was undone" || got["number"] != number {
		t.Fatalf("voided %v", got)
	}
	for _, a := range []string{"school.payment.post", "school.approval.request", "school.approval.decide", "school.receipt.pdf"} {
		if schoolAudits(t, w, "Greenfield", a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}
}
