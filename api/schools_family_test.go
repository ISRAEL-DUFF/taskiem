package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The family page's staff side (B6): the link life setting; a link sent
// to a family by hand; claims listed (masked), beside the unmatched money,
// and dismissed with a reason once; plan requests listed and decided by
// school.approve once, the family told.
func TestSchoolFamilyClaimsPlansAndLinks(t *testing.T) {
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Messages = w.srv.Invoices.Messages
	rec := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.srv.Invoices}
	schools.WireReceipts(w.srv.Invoices, rec)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")

	// The link life: 3 days by default, 1 to 30, the owner's, audited.
	if st := owner.must(200, "GET", "/v1/settings/schools", nil); st["family_link_days"] != float64(3) {
		t.Fatalf("default %v", st["family_link_days"])
	}
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "family_link_days": 31})
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "family_link_days": 7})
	if st := owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "family_link_days": 7}); st["family_link_days"] != float64(7) {
		t.Fatalf("set %v", st)
	}
	if n := schoolAudits(t, w, "Greenfield", "settings.schools.family_link_days"); n != 1 {
		t.Fatalf("%d audits", n)
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
	student := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)
	ebube, family := student["id"].(string), student["family"].(map[string]any)["id"].(string)
	// The invoices went out with the family page's link (7 days).
	var links int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM school_family_links WHERE purpose = 'invoice' AND expires_at > now() + interval '6 days'`).Scan(&links); err != nil || links != 2 {
		t.Fatalf("invoice links: %d %v", links, err)
	}

	// A link by hand: managers only; the link is answered and sent by SMS.
	viewer.must(403, "POST", "/v1/schools/families/"+family+"/link", nil)
	sent := admin.must(200, "POST", "/v1/schools/families/"+family+"/link", map[string]any{"message": "Your fees page:"})
	if !strings.HasPrefix(sent["link"].(string), "https://forms.taskiem.test/s/") || sent["channels"].(map[string]any)["sms"] != float64(1) {
		t.Fatalf("sent %v", sent)
	}
	admin.must(404, "POST", "/v1/schools/families/"+uuid.NewString()+"/link", nil)
	if n := schoolAudits(t, w, "Greenfield", "school.family.link"); n != 1 {
		t.Fatalf("%d link audits", n)
	}

	// A claim and a plan request, as the page's inbox would record them.
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Greenfield'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var claim schools.Claim
	var req schools.PlanRequest
	d1 := time.Now().AddDate(0, 1, 0).Format(time.DateOnly)
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if claim, err = rec.RecordClaim(context.Background(), tx, tenant, uuid.MustParse(family), schools.ClaimIn{StudentID: uuid.MustParse(ebube), AmountKobo: 4_000_000, PaidOn: "2026-10-20", How: "transfer", Reference: "GTB/1"}, "family"); err != nil {
			return err
		}
		req, err = rec.RequestPlan(context.Background(), tx, tenant, uuid.MustParse(family), schools.PlanIn{StudentID: uuid.MustParse(ebube), Instalments: []schools.Instalment{{AmountKobo: 6_000_000, DueOn: d1}}, Message: "Salary on the 25th"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Claims: listed masked, revealed with pii.reveal; in To match; dismissed once.
	list := viewer.must(200, "GET", "/v1/schools/claims", nil)["claims"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "E•••" || list[0].(map[string]any)["reference"] != "GTB/1" || list[0].(map[string]any)["status"] != "open" {
		t.Fatalf("claims %v", list)
	}
	if got := owner.must(200, "GET", "/v1/schools/claims?reveal=true&student="+ebube, nil)["claims"].([]any); got[0].(map[string]any)["name"] != "Ebube Eze" {
		t.Fatalf("revealed %v", got)
	}
	if um := viewer.must(200, "GET", "/v1/money/unmatched", nil); len(um["claims"].([]any)) != 1 || um["claims"].([]any)[0].(map[string]any)["student"] != "GCA/001" || um["claims"].([]any)[0].(map[string]any)["name"] != "E•••" {
		t.Fatalf("to match %v", um["claims"])
	}
	viewer.must(403, "POST", "/v1/schools/claims/"+claim.ID.String()+"/resolve", map[string]any{"reason": "no"})
	admin.must(400, "POST", "/v1/schools/claims/"+claim.ID.String()+"/resolve", map[string]any{})
	if got := admin.must(200, "POST", "/v1/schools/claims/"+claim.ID.String()+"/resolve", map[string]any{"reason": "No such transfer on the statement"})["claim"].(map[string]any); got["status"] != "dismissed" || got["resolution_reason"] != "No such transfer on the statement" {
		t.Fatalf("dismissed %v", got)
	}
	admin.must(409, "POST", "/v1/schools/claims/"+claim.ID.String()+"/resolve", map[string]any{"reason": "again"})
	if got := viewer.must(200, "GET", "/v1/schools/claims", nil)["claims"].([]any); len(got) != 0 {
		t.Fatalf("open after dismissing %v", got)
	}
	if got := viewer.must(200, "GET", "/v1/schools/claims?status=any", nil)["claims"].([]any); len(got) != 1 {
		t.Fatalf("any %v", got)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.claim.resolve"); n != 1 {
		t.Fatalf("%d resolve audits", n)
	}

	// Plans: listed; decided by school.approve, by a person, once; the
	// family told with a fresh link.
	plans := viewer.must(200, "GET", "/v1/schools/plans", nil)["plans"].([]any)
	if len(plans) != 1 || plans[0].(map[string]any)["message"] != "Salary on the 25th" || plans[0].(map[string]any)["name"] != "E•••" {
		t.Fatalf("plans %v", plans)
	}
	admin.must(403, "POST", "/v1/schools/plans/"+req.ID.String(), map[string]any{"decision": "approved"})
	owner.must(400, "POST", "/v1/schools/plans/"+req.ID.String(), map[string]any{"decision": "declined"})
	d2 := time.Now().AddDate(0, 2, 0).Format(time.DateOnly)
	got := owner.must(200, "POST", "/v1/schools/plans/"+req.ID.String(), map[string]any{"decision": "approved", "note": "Half now, half next month",
		"instalments": []any{map[string]any{"amount_kobo": 5_000_000, "due_on": d1}, map[string]any{"amount_kobo": 5_000_000, "due_on": d2}}})
	pl := got["plan"].(map[string]any)
	if pl["status"] != "approved" || len(pl["decided_instalments"].([]any)) != 2 || got["sent"].(map[string]any)["channels"].(map[string]any)["sms"] != float64(1) {
		t.Fatalf("decided %v", got)
	}
	owner.must(409, "POST", "/v1/schools/plans/"+req.ID.String(), map[string]any{"decision": "declined", "note": "late"})
	if got := viewer.must(200, "GET", "/v1/schools/plans", nil)["plans"].([]any); len(got) != 0 {
		t.Fatalf("requested after deciding %v", got)
	}
	if got := viewer.must(200, "GET", "/v1/schools/plans?status=approved&student="+ebube, nil)["plans"].([]any); len(got) != 1 {
		t.Fatalf("approved %v", got)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.plan.decide"); n != 1 {
		t.Fatalf("%d decide audits", n)
	}
}
