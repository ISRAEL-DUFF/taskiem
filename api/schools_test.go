package api_test

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func schoolAudits(t *testing.T, w *world, tenant, action string) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id = (SELECT id FROM tenants WHERE name = $1) AND action = $2`, tenant, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The Schools area through the API (docs/spec/schools.md B2): only an
// owner turns it on; the Bursar role comes with it; off, its routes
// answer 409 and nothing is deleted.
func TestSchoolsSwitchAndRoles(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")

	if me := owner.must(200, "GET", "/v1/me", nil); me["schools"] != false {
		t.Fatalf("schools on before it was turned on: %v", me)
	}
	owner.must(409, "GET", "/v1/schools/classes", nil)
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	got := owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "bursar_role": true})
	if got["enabled"] != true || got["bursar_role"] != true {
		t.Fatalf("turned on %v", got)
	}
	if viewer.must(200, "GET", "/v1/settings/schools", nil)["enabled"] != true || viewer.must(200, "GET", "/v1/me", nil)["schools"] != true {
		t.Fatal("the viewer does not see it on")
	}
	if n := schoolAudits(t, w, "Greenfield", "settings.schools"); n != 1 {
		t.Errorf("%d settings.schools audits", n)
	}
	// The Bursar role: the four permissions, granted like any custom role.
	var bursar map[string]any
	for _, r := range owner.must(200, "GET", "/v1/roles", nil)["roles"].([]any) {
		role := r.(map[string]any)
		if role["name"] == "bursar" {
			bursar = role
		}
		if role["name"] == "gate" && (role["built_in"] != true || len(role["permissions"].([]any)) != 1) {
			t.Errorf("gate role %v", role)
		}
	}
	if bursar == nil || len(bursar["permissions"].([]any)) != 4 {
		t.Fatalf("bursar %v", bursar)
	}
	bur := addMember(t, w, owner, "bisi", "bursar")
	bur.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	viewer.must(200, "GET", "/v1/schools/classes", nil)
	viewer.must(403, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 2", "order": 8})
	// Off: hidden, nothing deleted.
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": false})
	if c, _ := owner.do("GET", "/v1/schools/classes", nil); c != 409 {
		t.Fatalf("off: %d", c)
	}
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	if l := owner.must(200, "GET", "/v1/schools/classes", nil)["classes"].([]any); len(l) != 1 {
		t.Fatalf("classes after off and on %v", l)
	}
}

// A gate session is refused everything but who it is and signing out,
// by the server.
func TestGateSessionIsRefusedEverythingElse(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	gate := addMember(t, w, owner, "musa", "gate")
	me := gate.must(200, "GET", "/v1/me", nil)
	if me["gate_only"] != true || !slices.Equal(toStrings(me["permissions"]), []string{"school.gate"}) {
		t.Fatalf("me %v", me)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/schools/students", nil}, {"GET", "/v1/schools/classes", nil}, {"GET", "/v1/settings/schools", nil}, {"GET", "/v1/workflows", nil},
		{"GET", "/v1/invoices", nil}, {"GET", "/v1/onboarding", nil}, {"GET", "/v1/billing/status", nil}, {"GET", "/v1/me/passkeys", nil},
		{"POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "order": 7}},
	} {
		code, out := gate.do(r.method, r.path, r.body)
		if code != 403 || out["gate_only"] != true {
			t.Errorf("%s %s: %d %v", r.method, r.path, code, out)
		}
	}
	gate.must(204, "POST", "/v1/auth/logout", nil)
	// A member with the gate role and another is not a gate session.
	both := addMember(t, w, owner, "bola", "gate", "viewer")
	both.must(200, "GET", "/v1/schools/classes", nil)
}

// The record through the API: classes, a session and terms, students
// masked unless revealed, the import (a bad line adds nothing; a good
// file is shown, then saved; again, nobody twice), starting a term once,
// and promotion once.
func TestSchoolsRecord(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	viewer := addMember(t, w, owner, "vic", "viewer")
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 2", "arm": "A", "order": 8})
	owner.must(409, "POST", "/v1/schools/classes", map[string]any{"name": "jss 1", "arm": "a", "order": 7})
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	owner.must(400, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2029"})
	first := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-14", "ends_on": "2026-12-11", "fees_due_on": "2026-09-07"})

	// The import.
	bad := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\nChidi Eze,GCA/002,JSS 9,Ngozi Eze,08031234567\n"
	code, out := owner.do("POST", "/v1/schools/students/import", map[string]any{"csv": bad})
	if code != 422 || out["code"] != "import_unreadable" || len(out["problems"].([]any)) != 1 {
		t.Fatalf("bad file: %d %v", code, out)
	}
	good := "Name,Admission number,Class,Guardian name,Phone,Email,Relationship\n" +
		"Ebube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567,ngozi@example.com,Mother\n" +
		"Chidi Eze,GCA/002,JSS 2 A,Ngozi Eze,08031234567,,\n" +
		"Musa Bello,GCA/003,JSS 1 A,Aisha Bello,08051112222,,\n"
	viewer.must(403, "POST", "/v1/schools/students/import", map[string]any{"csv": good})
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": good})
	if plan["saved"] != false || plan["added"] != float64(3) || len(plan["families"].([]any)) != 2 {
		t.Fatalf("plan %v", plan)
	}
	if l := owner.must(200, "GET", "/v1/schools/students", nil)["students"].([]any); len(l) != 0 {
		t.Fatalf("the plan saved %v", l)
	}
	owner.must(409, "POST", "/v1/schools/students/import", map[string]any{"csv": good, "confirm": "not-the-hash"})
	saved := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": good, "confirm": plan["hash"]})
	if saved["saved"] != true {
		t.Fatalf("saved %v", saved)
	}
	again := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": good})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": good, "confirm": again["hash"]})
	list := viewer.must(200, "GET", "/v1/schools/students", nil)
	if list["total"] != float64(3) || again["updated"] != float64(3) {
		t.Fatalf("after re-upload %v %v", list, again)
	}
	st := list["students"].([]any)[0].(map[string]any)
	if st["name"] != "E•••" || st["admission_number"] != "GCA/001" {
		t.Fatalf("masked %v", st)
	}
	// Searching names needs them revealed.
	if l := viewer.must(200, "GET", "/v1/schools/students?q=musa", nil)["students"].([]any); len(l) != 0 {
		t.Fatalf("a masked list searched names: %v", l)
	}
	viewer.must(403, "GET", "/v1/schools/students?reveal=true", nil)
	if l := owner.must(200, "GET", "/v1/schools/students?q=musa&reveal=true", nil)["students"].([]any); len(l) != 1 || l[0].(map[string]any)["name"] != "Musa Bello" {
		t.Fatalf("revealed search %v", l)
	}
	d := owner.must(200, "GET", "/v1/schools/students/gca%2F001?reveal=true", nil)["student"].(map[string]any)
	fam := d["family"].(map[string]any)
	if g := fam["guardians"].([]any)[0].(map[string]any); g["phone"] != "+2348031234567" || len(fam["students"].([]any)) != 2 {
		t.Fatalf("family %v", fam)
	}
	if g := viewer.must(200, "GET", "/v1/schools/families/"+fam["id"].(string), nil)["family"].(map[string]any)["guardians"].([]any)[0].(map[string]any); g["phone"] != "•••4567" {
		t.Fatalf("masked guardian %v", g)
	}
	if n := schoolAudits(t, w, "Greenfield", "pii.reveal"); n < 2 {
		t.Errorf("%d reveals audited", n)
	}
	// One family per guardian.
	owner.must(409, "POST", "/v1/schools/families", map[string]any{"guardians": []any{map[string]any{"name": "Ngozi", "phone": "+2348031234567"}}})

	// Starting the term enrols each student once.
	started := owner.must(200, "POST", "/v1/schools/terms/"+first["id"].(string)+"/start", nil)
	if started["enrolled"] != float64(3) || started["term"].(map[string]any)["state"] != "current" {
		t.Fatalf("started %v", started)
	}
	if again := owner.must(200, "POST", "/v1/schools/terms/"+first["id"].(string)+"/start", nil); again["enrolled"] != float64(0) {
		t.Fatalf("started again %v", again)
	}
	if l := viewer.must(200, "GET", "/v1/schools/terms/"+first["id"].(string)+"/enrollments", nil)["enrollments"].([]any); len(l) != 3 {
		t.Fatalf("enrollments %v", l)
	}
	// One by one, during the term: enrolled at once.
	one := owner.must(201, "POST", "/v1/schools/students", map[string]any{"name": "Ada Obi", "admission_number": "GCA/004", "class_id": j1["id"],
		"guardians": []any{map[string]any{"name": "Obi Obi", "phone": "08099990000"}}})
	if one["name"] != "A•••" || len(one["enrollments"].([]any)) != 1 {
		t.Fatalf("one by one %v", one)
	}
	ch := owner.must(200, "PATCH", "/v1/schools/students/"+one["id"].(string), map[string]any{"status": "withdrawn"})
	if ch["status"] != "withdrawn" || schoolAudits(t, w, "Greenfield", "school.student.change") != 1 {
		t.Fatalf("changed %v", ch)
	}

	// Promotion, once.
	p := owner.must(200, "GET", "/v1/schools/sessions/"+sess["id"].(string)+"/promotion", nil)
	if p["done"] != false || p["counts"].(map[string]any)["promote"] != float64(2) || p["counts"].(map[string]any)["graduate"] != float64(1) {
		t.Fatalf("plan %v", p)
	}
	musa := ""
	for _, x := range p["students"].([]any) {
		if s := x.(map[string]any); s["admission_number"] == "GCA/003" {
			musa = s["id"].(string)
		}
	}
	done := owner.must(200, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/promotion", map[string]any{"exceptions": map[string]any{musa: "repeat"}})
	if done["done"] != true || done["counts"].(map[string]any)["repeat"] != float64(1) {
		t.Fatalf("promoted %v", done)
	}
	owner.must(409, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/promotion", map[string]any{})
	if c := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)["class"]; c != "JSS 2 A" {
		t.Errorf("promoted into %v", c)
	}
	for _, a := range []string{"school.class.add", "school.session.add", "school.term.add", "school.term.start", "school.students.import", "school.student.add", "school.promote"} {
		if schoolAudits(t, w, "Greenfield", a) == 0 {
			t.Errorf("%s not audited", a)
		}
	}
	if !strings.Contains(owner.must(200, "GET", "/v1/schools/sessions", nil)["sessions"].([]any)[0].(map[string]any)["promoted_at"].(string), "T") {
		t.Error("the session is not marked promoted")
	}
}
