package api_test

import (
	"strconv"
	"strings"
	"testing"
)

var hrTicketsTable = map[string]any{"name": "hr_tickets", "title": "HR requests", "readers": []string{"hr_lead", "hr"}, "columns": []map[string]any{
	{"name": "reference", "type": "text"},
	{"name": "subject", "type": "text", "personal": true},
}}

// A table naming readers is read and changed only by members holding one
// of its roles, and the owner, on top of table.read and table.edit:
// through the rows API, the query, the export and import, and the schema
// routes; others still see its name, columns and readers, without its row
// count. An API key reads as its owner. Readers change with role.manage,
// only by someone who reads the table, never to an empty list, audited.
func TestTableReaders(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	builder := addMember(t, w.world, owner, "bob", "builder")
	admin := addMember(t, w.world, owner, "ade", "admin")
	auditor := addMember(t, w.world, owner, "aud", "auditor")
	operator := addMember(t, w.world, owner, "opi", "operator")
	hrViewer := addMember(t, w.world, owner, "hana", "viewer", "hr")
	hrBuilder := addMember(t, w.world, owner, "hal", "builder", "hr_lead")

	// Readers on a new table need only table.edit; an empty list is nobody.
	if s, out := builder.do("POST", "/v1/tables", map[string]any{"name": "x", "readers": []string{}, "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 400 || !strings.Contains(out["error"].(string), "nobody") {
		t.Fatalf("empty readers: %d %v", s, out)
	}
	if s, out := builder.do("POST", "/v1/tables", map[string]any{"name": "x", "readers": []string{"HR people"}, "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 400 {
		t.Fatalf("a reader that is no role: %d %v", s, out)
	}
	tbl := builder.must(201, "POST", "/v1/tables", hrTicketsTable)
	id := tbl["id"].(string)
	if r := tbl["readers"].([]any); len(r) != 2 || r[0] != "hr" || r[1] != "hr_lead" || tbl["can_read"] != false {
		t.Fatalf("created %v", tbl)
	}
	plain := builder.must(201, "POST", "/v1/tables", debtorsTable)
	if plain["readers"] != nil || plain["can_read"] != true {
		t.Fatalf("an ordinary table: %v", plain)
	}
	rows := "/v1/tables/" + id + "/rows"
	ins := hrBuilder.must(201, "POST", rows, map[string]any{"row": map[string]any{"reference": "HR-1001", "subject": "My payslip"}})
	rowID := strconv.Itoa(int(ins["rows"].([]any)[0].(map[string]any)["id"].(float64)))

	// Everyone with table.read sees the table listed, not its rows.
	for name, c := range map[string]*client{"viewer": viewer, "builder": builder, "admin": admin, "auditor": auditor, "operator": operator} {
		var listed map[string]any
		for _, x := range c.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
			if m := x.(map[string]any); m["name"] == "hr_tickets" {
				listed = m
			}
		}
		if listed == nil || listed["can_read"] != false || listed["row_count"] != nil || len(listed["readers"].([]any)) != 2 {
			t.Errorf("%s: listed %v", name, listed)
		}
		if got := c.must(200, "GET", "/v1/tables/"+id, nil); got["row_count"] != nil || len(got["columns"].([]any)) != 2 {
			t.Errorf("%s: get %v", name, got)
		}
		for _, call := range []struct{ method, path string }{
			{"GET", rows}, {"GET", rows + "?q=pay"}, {"POST", rows + "/query"}, {"GET", "/v1/tables/" + id + "/export"},
		} {
			var body any
			if call.method == "POST" {
				body = map[string]any{}
			}
			if s, out := c.do(call.method, call.path, body); s != 403 || out["code"] != "table_restricted" {
				t.Errorf("%s: %s %s: %d %v", name, call.method, call.path, s, out)
			}
		}
	}
	// Changing it needs a reader too, whatever table.edit says.
	for name, call := range map[string]struct {
		method, path string
		body         any
	}{
		"insert":      {"POST", rows, map[string]any{"row": map[string]any{"reference": "HR-1002"}}},
		"update":      {"PATCH", rows + "/" + rowID, map[string]any{"set": map[string]any{"subject": "x"}}},
		"delete":      {"DELETE", rows + "/" + rowID, nil},
		"title":       {"PATCH", "/v1/tables/" + id, map[string]any{"title": "Mine"}},
		"add column":  {"POST", "/v1/tables/" + id + "/columns", map[string]any{"name": "note", "type": "text"}},
		"drop column": {"DELETE", "/v1/tables/" + id + "/columns/subject", map[string]any{"confirm": "subject"}},
		"drop table":  {"DELETE", "/v1/tables/" + id, map[string]any{"confirm": "hr_tickets"}},
	} {
		if s, out := builder.do(call.method, call.path, call.body); s != 403 || out["code"] != "table_restricted" {
			t.Errorf("builder without the role: %s: %d %v", name, s, out)
		}
	}
	if s, _, _ := builder.rawGet(t, "/v1/tables/"+id+"/export"); s != 403 {
		t.Errorf("export: %d", s)
	}
	if s, out := builder.uploadCSV(t, "/v1/tables/"+id+"/import", "x.csv", "reference\nHR-1003\n"); s != 403 || out["code"] != "table_restricted" {
		t.Errorf("import: %d %v", s, out)
	}

	// The readers, and the owner, read and change it.
	for name, c := range map[string]*client{"hr": hrViewer, "hr_lead": hrBuilder, "owner": owner} {
		page := c.must(200, "GET", rows, nil)
		if r := page["rows"].([]any); len(r) != 1 || r[0].(map[string]any)["subject"] != "My payslip" {
			t.Errorf("%s: rows %v", name, page)
		}
		if q := c.must(200, "POST", rows+"/query", map[string]any{"count": "exact"}); q["count"] != float64(1) {
			t.Errorf("%s: query %v", name, q)
		}
		if s, _, body := c.rawGet(t, "/v1/tables/"+id+"/export"); s != 200 || !strings.Contains(body, "HR-1001") {
			t.Errorf("%s: export %d", name, s)
		}
		if got := c.must(200, "GET", "/v1/tables/"+id, nil); got["can_read"] != true || got["row_count"] == nil {
			t.Errorf("%s: get %v", name, got)
		}
	}
	if s, _ := hrViewer.do("PATCH", rows+"/"+rowID, map[string]any{"set": map[string]any{"subject": "x"}}); s != 403 {
		t.Errorf("a reader without table.edit changed a row: %d", s)
	}
	hrBuilder.must(200, "PATCH", rows+"/"+rowID, map[string]any{"set": map[string]any{"subject": "My March payslip"}})
	if s, out := hrBuilder.uploadCSV(t, "/v1/tables/"+id+"/import", "x.csv", "reference\nHR-1003\n"); s != 200 {
		t.Errorf("a reader's import: %d %v", s, out)
	}

	// A key reads as its owner holds roles now.
	hrAdmin := addMember(t, w.world, owner, "kim", "admin", "hr")
	hrKey := keyFor(t, hrAdmin, "", "table.read")
	hrKey.must(200, "GET", rows, nil)
	plainKey := keyFor(t, admin, "", "table.read")
	if s, out := plainKey.do("GET", rows, nil); s != 403 || out["code"] != "table_restricted" {
		t.Errorf("an admin's key: %d %v", s, out)
	}

	// Changing readers: role.manage, someone who reads the table, a list
	// of one or more roles.
	readers := "/v1/tables/" + id + "/readers"
	if s, _ := hrBuilder.do("PUT", readers, map[string]any{"readers": []string{"hr"}}); s != 403 {
		t.Errorf("a builder set readers: %d", s)
	}
	if s, out := admin.do("PUT", readers, map[string]any{"readers": []string{"admin"}}); s != 403 || out["code"] != "table_restricted" {
		t.Errorf("an admin who cannot read it let themselves in: %d %v", s, out)
	}
	if s, out := owner.do("PUT", readers, map[string]any{"readers": []string{}}); s != 400 || !strings.Contains(out["error"].(string), "nobody") {
		t.Errorf("empty readers: %d %v", s, out)
	}
	if s, _ := owner.do("PUT", readers, map[string]any{}); s != 400 {
		t.Errorf("no readers given: %d", s)
	}
	set := owner.must(200, "PUT", readers, map[string]any{"readers": []string{"hr_lead"}})
	if r := set["readers"].([]any); len(r) != 1 || r[0] != "hr_lead" {
		t.Fatalf("set %v", set)
	}
	if s, _ := hrViewer.do("GET", rows, nil); s != 403 {
		t.Errorf("hr reads after readers became hr_lead: %d", s)
	}
	hrBuilder.must(200, "GET", rows, nil)
	cleared := owner.must(200, "DELETE", readers, nil)
	if cleared["readers"] != nil {
		t.Fatalf("cleared %v", cleared)
	}
	viewer.must(200, "GET", rows, nil)
	// A role the business does not have (a typo) is refused, naming it
	// and the business's roles, so nobody is locked out by mistake.
	s, out := owner.do("PUT", readers, map[string]any{"readers": []string{"hr", "hr_led"}})
	if s != 422 || out["code"] != "unknown_roles" || toJSON(out["unknown"]) != `["hr_led"]` || !strings.Contains(out["error"].(string), "hr_led") {
		t.Fatalf("an unknown role: %d %v", s, out)
	}
	roles := toJSON(out["roles"])
	for _, r := range []string{`"hr"`, `"hr_lead"`, `"owner"`, `"viewer"`, `"approver"`} {
		if !strings.Contains(roles, r) {
			t.Errorf("the business's roles %s lack %s", roles, r)
		}
	}
	// The Tables page offers the same roles to pick from.
	if got := owner.must(200, "GET", readers, nil); toJSON(got["roles"]) != roles || got["readers"] != nil {
		t.Errorf("readers and roles: %v, want roles %s", got, roles)
	}
	if s, _ := viewer.do("GET", readers, nil); s != 403 {
		t.Errorf("a viewer listed the roles to pick from: %d", s)
	}
	// A built-in role nobody holds yet is a role of the business.
	owner.must(200, "PUT", readers, map[string]any{"readers": []string{"approver"}})
	owner.must(200, "DELETE", readers, nil)
	// An admin who reads the table may not restrict it to roles they do
	// not hold: they would lock themselves out. One holding the role may;
	// the owner always may.
	if s, out := admin.do("PUT", readers, map[string]any{"readers": []string{"hr"}}); s != 422 || out["code"] != "readers_lock_out" {
		t.Fatalf("an admin locking themselves out: %d %v", s, out)
	}
	if s, _ := admin.do("GET", rows, nil); s != 200 {
		t.Errorf("the refused change took effect: %d", s)
	}
	hrAdmin.must(200, "PUT", readers, map[string]any{"readers": []string{"hr"}})
	if s, _ := admin.do("GET", rows, nil); s != 403 {
		t.Errorf("an admin without hr read it: %d", s)
	}
	if a := w.auditActions(t, "Acme"); a["table.readers.set"] != 3 || a["table.readers.clear"] != 2 {
		t.Errorf("audit %v", a)
	}
}

// The HR help desk's tables are made with their readers: hr and hr_lead,
// and hr_lead alone for the confidential ones; a table of that name the
// business made before is left as it is, with the readers the template
// wants reported.
func TestHRDeskTablesHaveReaders(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "hr_ticket_messages", "columns": []map[string]any{{"name": "reference", "type": "text"}}})
	out := owner.must(201, "POST", "/v1/templates/staff-hr-desk/instantiate", map[string]any{"params": map[string]any{}})
	results := map[string]map[string]any{}
	for _, x := range out["tables"].([]any) {
		m := x.(map[string]any)
		results[m["name"].(string)] = m
	}
	if r := results["hr_ticket_messages"]; r["status"] != "exists" || toJSON(r["readers"]) != `["hr","hr_lead"]` {
		t.Errorf("an existing table: %v", r)
	}
	for _, name := range []string{"hr_tickets", "hr_confidential_tickets", "hr_confidential_messages"} {
		if r := results[name]; r["status"] != "created" || r["readers"] != nil {
			t.Errorf("%s: %v", name, r)
		}
	}
	want := map[string]string{"hr_tickets": `["hr","hr_lead"]`, "hr_confidential_tickets": `["hr_lead"]`, "hr_confidential_messages": `["hr_lead"]`, "hr_ticket_messages": `null`}
	for _, x := range viewer.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		m := x.(map[string]any)
		if r, ok := want[m["name"].(string)]; ok {
			if toJSON(m["readers"]) != r {
				t.Errorf("%s: readers %v, want %s", m["name"], m["readers"], r)
			}
			if r != "null" {
				if s, _ := viewer.do("GET", "/v1/tables/"+m["id"].(string)+"/rows", nil); s != 403 {
					t.Errorf("%s: a viewer read its rows: %d", m["name"], s)
				}
			}
		}
	}
	// Nobody holds hr or hr_lead yet: readiness says so, naming a table
	// they read, and it does not stop publishing.
	wf := out["id"].(string)
	needs := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1/readiness", nil)["needs"].([]any)
	said := map[string]map[string]any{}
	for _, x := range needs {
		if n := x.(map[string]any); n["kind"] == "role" {
			said[n["name"].(string)] = n
		}
	}
	for _, role := range []string{"hr", "hr_lead"} {
		n := said[role]
		if n == nil || n["problem"] != "nobody" || n["blocking"] != false || !strings.Contains(n["message"].(string), "only owners can read the table") {
			t.Errorf("readiness on %s: %v", role, n)
		}
	}
	addMember(t, w.world, owner, "hana", "viewer", "hr")
	needs = owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1/readiness", nil)["needs"].([]any)
	for _, x := range needs {
		if n := x.(map[string]any); n["kind"] == "role" && n["name"] == "hr" {
			t.Errorf("hr is held now: %v", n)
		}
	}
}
