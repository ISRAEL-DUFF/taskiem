package api_test

import (
	"bytes"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Fees and term invoices through the API (docs/spec/schools.md B3): set a
// class's fees, preview the term, invoice it, run it again (nobody
// twice), amend a sent invoice, and a discount over the owner's threshold
// refused with needs_approval.
func TestSchoolFeesAndTermInvoices(t *testing.T) {
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	term := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-14", "ends_on": "2099-12-11", "fees_due_on": "2099-09-07"})
	tid := term["id"].(string)
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\nChidi Eze,GCA/002,JSS 1 A,Ngozi Eze,08031234567\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/start", nil)

	items := []any{
		map[string]any{"name": "Tuition", "amount_kobo": 10_000_000, "kind": "required"},
		map[string]any{"name": "Transport", "amount_kobo": 2_000_000, "kind": "optional"},
		map[string]any{"name": "WAEC fee", "amount_kobo": 1_800_000, "pass_through_for": "WAEC"},
	}
	viewer.must(403, "PUT", "/v1/schools/terms/"+tid+"/fees/"+j1["id"].(string), map[string]any{"items": items})
	set := owner.must(200, "PUT", "/v1/schools/terms/"+tid+"/fees/"+j1["id"].(string), map[string]any{"items": items})
	if f := set["fees"].(map[string]any); f["required_kobo"] != float64(11_800_000) || f["pass_through_kobo"] != float64(1_800_000) {
		t.Fatalf("fees %v", set)
	}
	if l := viewer.must(200, "GET", "/v1/schools/terms/"+tid+"/fees", nil)["classes"].([]any); len(l) != 1 {
		t.Fatalf("listed %v", l)
	}
	transport := set["fees"].(map[string]any)["items"].([]any)[1].(map[string]any)["id"]
	ebube := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)
	owner.must(200, "PUT", "/v1/schools/students/"+ebube["id"].(string)+"/options", map[string]any{"term": tid, "items": []any{transport}})
	fees := viewer.must(200, "GET", "/v1/schools/students/"+ebube["id"].(string)+"/fees", nil)
	if fees["total_kobo"] != float64(13_800_000) {
		t.Fatalf("Ebube's fees %v", fees)
	}

	// The threshold is the owner's, in naira; over it a discount is
	// refused with needs_approval.
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "discount_threshold_naira": 1})
	if got := owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "discount_threshold_naira": 20000}); got["discount_threshold_naira"] != float64(20000) {
		t.Fatalf("threshold %v", got)
	}
	code, out := owner.do("POST", "/v1/schools/students/"+ebube["id"].(string)+"/discounts", map[string]any{"kind": "hardship", "amount_kobo": 2_000_000, "reason": "Hard year"})
	if code != 202 || out["code"] != "needs_approval" {
		t.Fatalf("over the threshold: %d %v", code, out)
	}
	chidi := owner.must(200, "GET", "/v1/schools/students/GCA%2F002", nil)["student"].(map[string]any)
	owner.must(201, "POST", "/v1/schools/students/"+chidi["id"].(string)+"/discounts", map[string]any{"kind": "sibling", "percent": 10, "reason": "Second child"})

	// The preview, then the confirm; again, nobody twice.
	p := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	if p["to_invoice"] != float64(2) || p["total_kobo"] != float64(13_800_000+11_800_000-1_000_000) || p["already_invoiced"] != float64(0) {
		t.Fatalf("plan %v", p)
	}
	viewer.must(403, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})
	owner.must(409, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": "stale"})
	done := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p["hash"], "send": true})
	if done["created"] != float64(2) || done["sent"] != float64(2) {
		t.Fatalf("invoiced %v", done)
	}
	p2 := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	again := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p2["hash"]})
	if again["created"] != float64(0) || again["already_invoiced"] != float64(2) {
		t.Fatalf("again %v", again)
	}
	list := viewer.must(200, "GET", "/v1/schools/terms/"+tid+"/invoices?class="+j1["id"].(string), nil)["invoices"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["status"] != "sent" || list[0].(map[string]any)["name"] != "E•••" {
		t.Fatalf("term invoices %v", list)
	}
	number := list[0].(map[string]any)["number"].(string)
	inv := owner.must(200, "GET", "/v1/invoices/"+number, nil)["invoice"].(map[string]any)
	if inv["customer"].(map[string]any)["key"] != "student:"+ebube["id"].(string) {
		t.Fatalf("customer %v", inv["customer"])
	}

	// An amendment, with its reason, is a line of its own; a discount over
	// the threshold is refused.
	am := owner.must(201, "POST", "/v1/invoices/"+number+"/amendments", map[string]any{"kind": "add", "description": "Lost textbook", "amount_kobo": 500_000, "reason": "Replaced the maths text"})
	if x := am["invoice"].(map[string]any); x["total_kobo"] != float64(14_300_000) || len(x["lines"].([]any)) != 4 || len(x["amendments"].([]any)) != 1 {
		t.Fatalf("amended %v", am)
	}
	code, out = owner.do("POST", "/v1/invoices/"+number+"/amendments", map[string]any{"kind": "discount", "amount_kobo": 2_000_000, "reason": "Hardship"})
	if code != 202 || out["code"] != "needs_approval" {
		t.Fatalf("an amendment over the threshold: %d %v", code, out)
	}
	owner.must(201, "POST", "/v1/invoices/"+number+"/amendments", map[string]any{"kind": "discount", "amount_kobo": 100_000, "reason": "Goodwill"})
	viewer.must(403, "POST", "/v1/invoices/"+number+"/amendments", map[string]any{"kind": "discount", "amount_kobo": 100_000, "reason": "x"})
	for _, a := range []string{"school.fees.set", "school.student.options", "school.discount.add", "school.term.invoice", "invoice.amended", "settings.schools.discount_threshold"} {
		if schoolAudits(t, w, "Greenfield", a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}
}
