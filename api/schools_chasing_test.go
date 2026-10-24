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

// Chasing (B7): the ladder's wording and the plan grace in Settings;
// Debtors with aging, masked, as JSON and CSV; reminders at most fifty,
// once per key, a promise and a plan in good standing skipped and said
// so; promises recorded, withdrawn and found missed by the check; a plan
// in arrears marked once and the bursar told.
func TestSchoolChasing(t *testing.T) {
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

	// The ladder's wording: defaults answered, placeholders checked, the
	// owner's, audited; the plan grace 0 to 60.
	st := owner.must(200, "GET", "/v1/settings/schools", nil)
	if len(st["reminder_defaults"].(map[string]any)) != 3 || len(st["reminder_wording"].(map[string]any)) != 0 || st["plan_grace_days"] != float64(0) || len(st["reminder_placeholders"].([]any)) != 5 {
		t.Fatalf("settings %v", st)
	}
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "reminder_wording": map[string]any{"1": "Pay {oops}"}})
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "reminder_wording": map[string]any{"x": "Pay"}})
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "reminder_wording": map[string]any{"1": "Pay"}})
	st = owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "reminder_wording": map[string]any{"1": "Dear parent, {balance} is due for {child}."}, "plan_grace_days": 3})
	if got := st["reminder_wording"].(map[string]any)["1"]; got != "Dear parent, {balance} is due for {child}. Your fees page: {link}" || st["plan_grace_days"] != float64(3) {
		t.Fatalf("set %v", st)
	}
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "plan_grace_days": 61})
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "plan_grace_days": 0})
	if n := schoolAudits(t, w, "Greenfield", "settings.schools.reminder_wording"); n != 1 {
		t.Fatalf("%d wording audits", n)
	}
	if n := schoolAudits(t, w, "Greenfield", "settings.schools.plan_grace_days"); n != 2 {
		t.Fatalf("%d grace audits", n)
	}

	// A class, two siblings and a third student, a term whose fees were
	// due in September, invoiced and sent.
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
	ids := map[string]string{}
	var family uuid.UUID
	for _, n := range []string{"GCA/001", "GCA/002", "GCA/003"} {
		s := owner.must(200, "GET", "/v1/schools/students/"+strings.ReplaceAll(n, "/", "%2F"), nil)["student"].(map[string]any)
		ids[n] = s["id"].(string)
		if n == "GCA/001" {
			family = uuid.MustParse(s["family"].(map[string]any)["id"].(string))
		}
	}

	// Debtors: three, aged over 30 days, masked; the totals; the CSV.
	d := viewer.must(200, "GET", "/v1/schools/debtors", nil)
	list := d["debtors"].([]any)
	if len(list) != 3 || d["students"] != float64(3) || d["balance_kobo"] != float64(30_000_000) || d["revealed"] != false {
		t.Fatalf("debtors %v", d)
	}
	first := list[0].(map[string]any)
	if first["name"] != "E•••" || first["bucket"] != "over_30" || first["days_past_due"].(float64) < 40 || first["balance_kobo"] != float64(10_000_000) || first["class"] != "JSS 1 A" {
		t.Fatalf("first %v", first)
	}
	if tot := d["totals"].(map[string]any); tot["over_30"].(map[string]any)["students"] != float64(3) || tot["0_7"].(map[string]any)["students"] != float64(0) {
		t.Fatalf("totals %v", tot)
	}
	if d := viewer.must(200, "GET", "/v1/schools/debtors?bucket=0_7", nil); len(d["debtors"].([]any)) != 0 || d["students"] != float64(3) {
		t.Fatalf("bucket filter %v", d)
	}
	viewer.must(400, "GET", "/v1/schools/debtors?bucket=soon", nil)
	viewer.must(403, "GET", "/v1/schools/debtors?reveal=true", nil)
	if got := owner.must(200, "GET", "/v1/schools/debtors?reveal=true&class="+j1["id"].(string), nil)["debtors"].([]any); got[0].(map[string]any)["name"] != "Ebube Eze" {
		t.Fatalf("revealed %v", got)
	}
	if raw := viewer.must(200, "GET", "/v1/schools/debtors?format=csv", nil)["raw"].(string); !strings.HasPrefix(raw, "Class,Admission number,Name,Balance") || !strings.Contains(raw, "GCA/001,E•••,\"₦100,000\",2026-09-07") || strings.Contains(raw, "Ebube") {
		t.Fatalf("csv %q", raw)
	}
	if raw := owner.must(200, "GET", "/v1/schools/debtors?format=csv&reveal=true", nil)["raw"].(string); !strings.Contains(raw, "Ebube Eze") {
		t.Fatalf("csv revealed %q", raw)
	}
	if n := schoolAudits(t, w, "Greenfield", "pii.reveal"); n < 2 {
		t.Fatalf("%d reveal audits", n)
	}

	// A promise to pay: managers, from today on, one open per student.
	tomorrow := time.Now().AddDate(0, 0, 2).Format(time.DateOnly)
	viewer.must(403, "POST", "/v1/schools/students/GCA%2F003/promise", map[string]any{"date": tomorrow})
	admin.must(400, "POST", "/v1/schools/students/GCA%2F003/promise", map[string]any{"date": "2020-01-01", "note": "long ago"})
	pr := admin.must(201, "POST", "/v1/schools/students/GCA%2F003/promise", map[string]any{"date": tomorrow, "note": "Salary on the 25th"})["promise"].(map[string]any)
	if pr["status"] != "open" || pr["balance_kobo"] != float64(10_000_000) || pr["note"] != "Salary on the 25th" || pr["admission_number"] != "GCA/003" {
		t.Fatalf("promise %v", pr)
	}
	admin.must(409, "POST", "/v1/schools/students/GCA%2F003/promise", map[string]any{"date": tomorrow})
	admin.must(404, "POST", "/v1/schools/students/GCA%2F009/promise", map[string]any{"date": tomorrow})
	if got := viewer.must(200, "GET", "/v1/schools/promises?student="+ids["GCA/003"], nil)["promises"].([]any); len(got) != 1 {
		t.Fatalf("promises %v", got)
	}
	ch := viewer.must(200, "GET", "/v1/schools/students/GCA%2F003/chasing", nil)
	if ch["balance_kobo"] != float64(10_000_000) || ch["bucket"] != "over_30" || len(ch["promises"].([]any)) != 1 || len(ch["reminders"].([]any)) != 0 || ch["plan"] != nil {
		t.Fatalf("chasing %v", ch)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.promise.record"); n != 1 {
		t.Fatalf("%d promise audits", n)
	}

	// Reminders: a key, at most fifty, rung 1 to 3; the promise skipped
	// and said so; once per key; audited.
	all := []any{ids["GCA/001"], ids["GCA/002"], ids["GCA/003"]}
	viewer.must(403, "POST", "/v1/schools/reminders", map[string]any{"students": all, "rung": 1}, "Idempotency-Key", "k0")
	admin.must(400, "POST", "/v1/schools/reminders", map[string]any{"students": all, "rung": 1})
	admin.must(400, "POST", "/v1/schools/reminders", map[string]any{"students": all, "rung": 4}, "Idempotency-Key", "k0")
	admin.must(400, "POST", "/v1/schools/reminders", map[string]any{"students": []any{}, "rung": 1}, "Idempotency-Key", "k0")
	many := make([]any, 51)
	for i := range many {
		many[i] = ids["GCA/001"]
	}
	if got := admin.must(400, "POST", "/v1/schools/reminders", map[string]any{"students": many, "rung": 1}, "Idempotency-Key", "k0"); !strings.Contains(got["error"].(string), "at most 50") {
		t.Fatalf("51: %v", got)
	}
	sent := admin.must(200, "POST", "/v1/schools/reminders", map[string]any{"students": all, "rung": 1}, "Idempotency-Key", "k1")
	if sent["sent"] != float64(2) || sent["skipped"] != float64(1) || sent["replayed"] != nil || sent["by_rung"].(map[string]any)["1"] != float64(2) {
		t.Fatalf("sent %v", sent)
	}
	results := sent["results"].([]any)
	if r := results[2].(map[string]any); r["status"] != "skipped" || !strings.HasPrefix(r["reason"].(string), "a promise to pay by ") {
		t.Fatalf("skipped %v", r)
	}
	if r := results[0].(map[string]any); r["status"] != "sent" || r["channels"].(map[string]any)["sms"] != float64(1) || r["rung"] != float64(1) {
		t.Fatalf("sent to %v", r)
	}
	if again := admin.must(200, "POST", "/v1/schools/reminders", map[string]any{"students": all, "rung": 1}, "Idempotency-Key", "k1"); again["replayed"] != true || again["sent"] != float64(2) {
		t.Fatalf("replayed %v", again)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.reminders.send"); n != 1 {
		t.Fatalf("%d send audits", n)
	}
	if twice := admin.must(200, "POST", "/v1/schools/reminders", map[string]any{"students": []any{ids["GCA/001"]}, "rung": 1}, "Idempotency-Key", "k2"); twice["sent"] != float64(0) || !strings.Contains(twice["results"].([]any)[0].(map[string]any)["reason"].(string), "today already") {
		t.Fatalf("twice a day %v", twice)
	}
	d = viewer.must(200, "GET", "/v1/schools/debtors?has=promise", nil)
	if list := d["debtors"].([]any); len(list) != 1 || list[0].(map[string]any)["promise"].(map[string]any)["status"] != "open" {
		t.Fatalf("has promise %v", list)
	}
	if lr := viewer.must(200, "GET", "/v1/schools/debtors", nil)["debtors"].([]any)[0].(map[string]any)["last_reminder"].(map[string]any); lr["rung"] != float64(1) {
		t.Fatalf("last reminder %v", lr)
	}

	// Withdrawn, once.
	viewer.must(403, "POST", "/v1/schools/promises/"+pr["id"].(string)+"/withdraw", nil)
	if got := admin.must(200, "POST", "/v1/schools/promises/"+pr["id"].(string)+"/withdraw", nil)["promise"].(map[string]any); got["status"] != "withdrawn" {
		t.Fatalf("withdrawn %v", got)
	}
	admin.must(409, "POST", "/v1/schools/promises/"+pr["id"].(string)+"/withdraw", nil)
	admin.must(404, "POST", "/v1/schools/promises/"+uuid.NewString()+"/withdraw", nil)

	// The check: a promise whose date passed with the balance unmoved is
	// missed, once; the bursar told; the student flagged.
	if c := admin.must(200, "POST", "/v1/schools/chasing/check", nil); c["promises_missed"] != float64(0) || c["arrears"] != float64(0) {
		t.Fatalf("nothing to check %v", c)
	}
	pr = admin.must(201, "POST", "/v1/schools/students/GCA%2F003/promise", map[string]any{"date": tomorrow, "note": "Again"})["promise"].(map[string]any)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE school_promises SET promised_on = current_date - 1 WHERE id = $1`, pr["id"]); err != nil {
		t.Fatal(err)
	}
	if c := admin.must(200, "POST", "/v1/schools/chasing/check", nil); c["promises_missed"] != float64(1) {
		t.Fatalf("missed %v", c)
	}
	if c := admin.must(200, "POST", "/v1/schools/chasing/check", nil); c["promises_missed"] != float64(0) {
		t.Fatalf("missed once %v", c)
	}
	d = viewer.must(200, "GET", "/v1/schools/debtors?has=missed", nil)
	if list := d["debtors"].([]any); len(list) != 1 || list[0].(map[string]any)["promise_missed"] != true || list[0].(map[string]any)["admission_number"] != "GCA/003" {
		t.Fatalf("has missed %v", list)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.chasing.check"); n != 3 {
		t.Fatalf("%d check audits", n)
	}
	notes := owner.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any)
	found := false
	for _, n := range notes {
		found = found || n.(map[string]any)["title"] == "A promise to pay was missed"
	}
	if !found {
		t.Fatalf("no notice of the missed promise: %v", notes)
	}

	// A plan approved: good standing skips the family; an instalment past
	// due and not covered puts it in arrears, marked once, the bursar
	// told; a reminder then goes by the days past the instalment.
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Greenfield'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var req schools.PlanRequest
	d1, d2 := time.Now().AddDate(0, 0, 2).Format(time.DateOnly), time.Now().AddDate(0, 0, 60).Format(time.DateOnly)
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		req, err = rec.RequestPlan(context.Background(), tx, tenant, family, schools.PlanIn{StudentID: uuid.MustParse(ids["GCA/001"]), Instalments: []schools.Instalment{{AmountKobo: 5_000_000, DueOn: d1}, {AmountKobo: 5_000_000, DueOn: d2}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	owner.must(200, "POST", "/v1/schools/plans/"+req.ID.String(), map[string]any{"decision": "approved"})
	d = viewer.must(200, "GET", "/v1/schools/debtors?has=plan", nil)
	if list := d["debtors"].([]any); len(list) != 1 || list[0].(map[string]any)["plan"].(map[string]any)["state"] != "good" || list[0].(map[string]any)["plan"].(map[string]any)["next_due_on"] != d1 {
		t.Fatalf("has plan %v", list)
	}
	if skipped := admin.must(200, "POST", "/v1/schools/reminders", map[string]any{"students": []any{ids["GCA/001"]}, "rung": 2}, "Idempotency-Key", "k3"); skipped["sent"] != float64(0) || !strings.HasPrefix(skipped["results"].([]any)[0].(map[string]any)["reason"].(string), "a payment plan in good standing") {
		t.Fatalf("good standing %v", skipped)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE school_plan_requests SET decided_instalments = jsonb_build_array(jsonb_build_object('amount_kobo', 5000000, 'due_on', (current_date - 5)::text), jsonb_build_object('amount_kobo', 5000000, 'due_on', $2::text)) WHERE id = $1`, req.ID, d2); err != nil {
		t.Fatal(err)
	}
	if c := admin.must(200, "POST", "/v1/schools/chasing/check", nil); c["arrears"] != float64(1) {
		t.Fatalf("arrears %v", c)
	}
	if c := admin.must(200, "POST", "/v1/schools/chasing/check", nil); c["arrears"] != float64(0) {
		t.Fatalf("arrears once %v", c)
	}
	d = viewer.must(200, "GET", "/v1/schools/debtors?has=arrears", nil)
	if list := d["debtors"].([]any); len(list) != 1 || list[0].(map[string]any)["plan"].(map[string]any)["state"] != "arrears" {
		t.Fatalf("has arrears %v", list)
	}
	ch = viewer.must(200, "GET", "/v1/schools/students/GCA%2F001/chasing", nil)
	if ch["plan"].(map[string]any)["state"] != "arrears" || len(ch["arrears"].([]any)) != 1 || ch["arrears"].([]any)[0].(map[string]any)["short_kobo"] != float64(5_000_000) || len(ch["reminders"].([]any)) != 1 {
		t.Fatalf("chasing with arrears %v", ch)
	}
	notes = owner.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any)
	found = false
	for _, n := range notes {
		found = found || n.(map[string]any)["title"] == "A payment plan instalment was missed"
	}
	if !found {
		t.Fatalf("no notice of the arrears: %v", notes)
	}
	if again := admin.must(200, "POST", "/v1/schools/reminders", map[string]any{"students": []any{ids["GCA/001"]}, "rung": 2}, "Idempotency-Key", "k4"); again["sent"] != float64(1) {
		t.Fatalf("in arrears, reminded %v", again)
	}
}
