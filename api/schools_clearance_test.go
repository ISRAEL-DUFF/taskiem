package api_test

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// clearanceSchool is a school with three students in one class, the
// term invoiced and sent: GCA/001 paid in full, GCA/002 part-paid,
// GCA/003 unpaid. It answers the owner and the students' ids.
func clearanceSchool(t *testing.T, w *world) (*client, map[string]string, string, string) {
	t.Helper()
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Messages = w.srv.Invoices.Messages
	rec := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.srv.Invoices}
	schools.WireReceipts(w.srv.Invoices, rec)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	term := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-01", "ends_on": "2099-12-11", "fees_due_on": "2026-09-07"})
	tid := term["id"].(string)
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\nChidi Eze,GCA/002,JSS 1 A,Ngozi Eze,08031234567\nTolu Ade,GCA/003,JSS 1 A,Bola Ade,08039876543\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/start", nil)
	owner.must(200, "PUT", "/v1/schools/terms/"+tid+"/fees/"+j1["id"].(string), map[string]any{"items": []any{
		map[string]any{"name": "Tuition", "amount_kobo": 10_000_000, "kind": "required"}}})
	p := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p["hash"], "send": true})
	owner.must(201, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": 10_000_000, "how": "cash"}, "Idempotency-Key", "pay-1")
	owner.must(201, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/002", "amount_kobo": 4_000_000, "how": "cash"}, "Idempotency-Key", "pay-2")
	ids := map[string]string{}
	for _, n := range []string{"GCA/001", "GCA/002", "GCA/003"} {
		ids[n] = owner.must(200, "GET", "/v1/schools/students/"+strings.ReplaceAll(n, "/", "%2F"), nil)["student"].(map[string]any)["id"].(string)
	}
	return owner, ids, tid, j1["id"].(string)
}

// Clearance (B8): the list by the rule, masked, with totals and a CSV;
// a student's clearance; overrides by school.approve only, audited,
// ending and withdrawn; nothing written to the record.
func TestSchoolClearance(t *testing.T) {
	w := newWorld(t)
	owner, ids, tid, class := clearanceSchool(t, w)
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")

	l := viewer.must(200, "GET", "/v1/schools/clearance", nil)
	rows := l["students"].([]any)
	if len(rows) != 3 || l["term"] != "First term 2026/2027" || l["revealed"] != false {
		t.Fatalf("list %v", l)
	}
	want := map[string][2]string{"GCA/001": {"cleared", "paid"}, "GCA/002": {"partly_paid", "balance"}, "GCA/003": {"not_paid", "balance"}}
	for _, r := range rows {
		x := r.(map[string]any)
		if w := want[x["admission_number"].(string)]; x["status"] != w[0] || x["reason"] != w[1] || x["name"] == "Ebube Eze" || x["class"] != "JSS 1 A" {
			t.Fatalf("row %v", x)
		}
		if x["admission_number"] == "GCA/002" && (x["balance_kobo"] != float64(6_000_000) || x["paid_kobo"] != float64(4_000_000)) {
			t.Fatalf("part paid %v", x)
		}
	}
	if tot := l["totals"].(map[string]any); tot["students"] != float64(3) || tot["cleared"] != float64(1) || tot["partly_paid"] != float64(1) || tot["not_paid"] != float64(1) {
		t.Fatalf("totals %v", tot)
	}
	if got := viewer.must(200, "GET", "/v1/schools/clearance?term="+tid+"&class="+class, nil)["students"].([]any); len(got) != 3 {
		t.Fatalf("by term and class %v", got)
	}
	if got := viewer.must(200, "GET", "/v1/schools/clearance?class="+uuid.NewString(), nil)["students"].([]any); len(got) != 0 {
		t.Fatalf("another class %v", got)
	}
	viewer.must(400, "GET", "/v1/schools/clearance?term=soon", nil)
	viewer.must(404, "GET", "/v1/schools/clearance?term="+uuid.NewString(), nil)
	viewer.must(403, "GET", "/v1/schools/clearance?reveal=true", nil)
	if got := owner.must(200, "GET", "/v1/schools/clearance?reveal=true", nil)["students"].([]any); got[0].(map[string]any)["name"] != "Ebube Eze" {
		t.Fatalf("revealed %v", got)
	}
	if raw := viewer.must(200, "GET", "/v1/schools/clearance?format=csv", nil)["raw"].(string); !strings.HasPrefix(raw, "Class,Admission number,Name,Clearance,Why,Balance,Override ends") || !strings.Contains(raw, "GCA/002,C•••,Partly paid,a balance is left,\"₦60,000\"") || strings.Contains(raw, "Chidi") {
		t.Fatalf("csv %q", raw)
	}

	// A student's clearance, by admission number.
	c := viewer.must(200, "GET", "/v1/schools/students/GCA%2F003/clearance", nil)
	if cl := c["clearance"].(map[string]any); cl["status"] != "not_paid" || cl["balance_kobo"] != float64(10_000_000) || len(c["overrides"].([]any)) != 0 {
		t.Fatalf("student clearance %v", c)
	}
	viewer.must(404, "GET", "/v1/schools/students/GCA%2F009/clearance", nil)

	// Overrides: school.approve only; a reason; audited; listed; the
	// student cleared by it; withdrawn once; audited.
	viewer.must(403, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": "Bereavement"})
	admin.must(403, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": "Bereavement"})
	owner.must(400, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": ""})
	owner.must(400, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": "Bereavement", "ends_on": "2020-01-01"})
	owner.must(404, "POST", "/v1/schools/students/GCA%2F009/clearance-override", map[string]any{"reason": "Bereavement"})
	o := owner.must(201, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": "Bereavement in the family"})["override"].(map[string]any)
	if o["in_force"] != true || o["ends_on"] != "2099-12-11" || o["reason"] != "Bereavement in the family" || o["term_id"] != tid || o["student_id"] != ids["GCA/003"] {
		t.Fatalf("override %v", o)
	}
	owner.must(409, "POST", "/v1/schools/students/GCA%2F003/clearance-override", map[string]any{"reason": "Again"})
	if n := schoolAudits(t, w, "Greenfield", "school.clearance.override"); n != 1 {
		t.Fatalf("%d override audits", n)
	}
	c = viewer.must(200, "GET", "/v1/schools/students/"+ids["GCA/003"]+"/clearance", nil)
	if cl := c["clearance"].(map[string]any); cl["status"] != "cleared" || cl["reason"] != "override" || cl["override"].(map[string]any)["id"] != o["id"] || len(c["overrides"].([]any)) != 1 {
		t.Fatalf("overridden %v", c)
	}
	if got := viewer.must(200, "GET", "/v1/schools/clearance-overrides?in_force=true&term="+tid, nil)["overrides"].([]any); len(got) != 1 {
		t.Fatalf("overrides listed %v", got)
	}
	if got := viewer.must(200, "GET", "/v1/schools/clearance-overrides?student="+ids["GCA/001"], nil)["overrides"].([]any); len(got) != 0 {
		t.Fatalf("another student's %v", got)
	}
	if tot := viewer.must(200, "GET", "/v1/schools/clearance", nil)["totals"].(map[string]any); tot["cleared"] != float64(2) || tot["not_paid"] != float64(0) {
		t.Fatalf("totals with the override %v", tot)
	}
	admin.must(403, "POST", "/v1/schools/clearance-overrides/"+o["id"].(string)+"/withdraw", nil)
	if got := owner.must(200, "POST", "/v1/schools/clearance-overrides/"+o["id"].(string)+"/withdraw", nil)["override"].(map[string]any); got["in_force"] != false || got["withdrawn_at"] == nil {
		t.Fatalf("withdrawn %v", got)
	}
	owner.must(409, "POST", "/v1/schools/clearance-overrides/"+o["id"].(string)+"/withdraw", nil)
	owner.must(404, "POST", "/v1/schools/clearance-overrides/"+uuid.NewString()+"/withdraw", nil)
	if n := schoolAudits(t, w, "Greenfield", "school.clearance.withdraw"); n != 1 {
		t.Fatalf("%d withdraw audits", n)
	}
	if cl := viewer.must(200, "GET", "/v1/schools/students/GCA%2F003/clearance", nil)["clearance"].(map[string]any); cl["status"] != "not_paid" {
		t.Fatalf("after withdrawing %v", cl)
	}
	// Paying the rest clears at once: nothing is stored.
	owner.must(201, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/003", "amount_kobo": 10_000_000, "how": "cash"}, "Idempotency-Key", "pay-3")
	if cl := viewer.must(200, "GET", "/v1/schools/students/GCA%2F003/clearance", nil)["clearance"].(map[string]any); cl["status"] != "cleared" || cl["reason"] != "paid" {
		t.Fatalf("paid %v", cl)
	}
}

// forbiddenKey matches any field that could carry money or its share.
var forbiddenKey = regexp.MustCompile(`(?i)amount|kobo|naira|balance|paid|due|total|percent|invoice|payment|receipt|plan|override|discount|fee`)

// walkKeys calls fn with every key in a JSON value, at any depth.
func walkKeys(v any, fn func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			fn(k)
			walkKeys(child, fn)
		}
	case []any:
		for _, child := range x {
			walkKeys(child, fn)
		}
	}
}

// The gate (B8): a gate session searches by name (three letters, ten at
// most, names shown), checks a student and gets cleared or see the
// bursar, and nothing in any gate answer names an amount; the routes are
// open to school.read too and to nobody else; searches are rate-limited;
// each check is audited.
func TestGateRoutes(t *testing.T) {
	w := newWorld(t)
	owner, ids, _, _ := clearanceSchool(t, w)
	gate := addMember(t, w, owner, "musa", "gate")
	viewer := addMember(t, w, owner, "vic", "viewer")
	builder := addMember(t, w, owner, "bob", "builder")

	gate.must(400, "GET", "/v1/gate/search?q=eb", nil)
	gate.must(400, "GET", "/v1/gate/search", nil)
	found := gate.must(200, "GET", "/v1/gate/search?q=eze", nil)
	hits := found["students"].([]any)
	if len(hits) != 2 || hits[0].(map[string]any)["name"] != "Ebube Eze" || hits[0].(map[string]any)["class"] != "JSS 1 A" || hits[0].(map[string]any)["student_id"] != ids["GCA/001"] {
		t.Fatalf("search %v", found)
	}
	if got := gate.must(200, "GET", "/v1/gate/search?q=GCA", nil)["students"].([]any); len(got) != 0 {
		t.Fatalf("admission numbers are not searched: %v", got)
	}
	answers := map[string]string{"GCA/001": "cleared", "GCA/002": "see_bursar", "GCA/003": "see_bursar"}
	var responses []map[string]any
	responses = append(responses, found)
	for n, want := range answers {
		a := gate.must(200, "GET", "/v1/gate/students/"+ids[n], nil)
		if a["status"] != want || a["student_id"] != ids[n] || a["class"] != "JSS 1 A" || a["name"] == "" {
			t.Fatalf("%s: %v", n, a)
		}
		responses = append(responses, a)
	}
	gate.must(404, "GET", "/v1/gate/students/"+uuid.NewString(), nil)
	gate.must(404, "GET", "/v1/gate/students/GCA%2F001", nil)
	// No amount, ever: every key of every gate answer.
	for _, r := range responses {
		walkKeys(r, func(k string) {
			if forbiddenKey.MatchString(k) {
				t.Errorf("a gate answer carries %q: %v", k, r)
			}
		})
	}
	if n := schoolAudits(t, w, "Greenfield", "school.gate.check"); n != 3 {
		t.Fatalf("%d gate check audits", n)
	}
	// Only the two gate routes exist under /v1/gate.
	var gateRoutes []string
	_ = chi.Walk(w.srv.Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/v1/gate/") {
			gateRoutes = append(gateRoutes, method+" "+route)
		}
		return nil
	})
	if strings.Join(gateRoutes, "\n") != "GET /v1/gate/search\nGET /v1/gate/students/{id}" {
		t.Fatalf("gate routes changed; each needs the no-amount review: %v", gateRoutes)
	}
	// A bursar (school.read) uses the gate too; nobody else does.
	viewer.must(200, "GET", "/v1/gate/search?q=eze", nil)
	viewer.must(200, "GET", "/v1/gate/students/"+ids["GCA/001"], nil)
	builder.must(403, "GET", "/v1/gate/search?q=eze", nil)
	builder.must(403, "GET", "/v1/gate/students/"+ids["GCA/001"], nil)
	// Searches are limited per session: a burst past thirty is refused.
	limited := false
	for range 40 {
		code, _ := gate.do("GET", "/v1/gate/search?q=eze", nil)
		limited = limited || code == 429
	}
	if !limited {
		t.Fatal("forty searches at once were all answered")
	}
	// The document holds the gate answers to their fields: no other
	// property can appear.
	doc := loadSpec(t)
	for _, name := range []string{"GateHit", "GateAnswer"} {
		if sch := doc.Components.Schemas[name]; sch == nil || sch["additionalProperties"] != false {
			t.Errorf("schema %s does not refuse other properties: %v", name, sch)
		}
	}
}
