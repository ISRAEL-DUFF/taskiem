package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func pagesAudits(t *testing.T, w *tablesWorld, action string) []string {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT detail::text FROM audit_log WHERE action = $1`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		out = append(out, d)
	}
	return out
}

// A page's versions are listed for page.edit holders with who published
// them and what changed; bringing one back makes it the draft, never what
// people see, and publishing it is checked again: against today's tables,
// and under four-eyes by another person.
func TestPagesVersionsBringBack(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	adm := addMember(t, w.world, owner, "ada", "admin")
	support := addMember(t, w.world, owner, "sam", "support")
	owner.must(201, "POST", "/v1/tables", ticketsTable)

	v1 := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"support"},
		"blocks": []any{
			map[string]any{"id": "h", "type": "heading", "title": "Support desk"},
			map[string]any{"id": "all", "type": "table", "title": "All", "source": map[string]any{"table": "tickets"},
				"columns": []any{map[string]any{"column": "title"}, map[string]any{"column": "customer"}}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": v1})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)
	v2 := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk",
		"blocks": []any{
			v1["blocks"].([]any)[0],
			map[string]any{"id": "open", "type": "figure", "label": "Open", "value": "count", "source": map[string]any{"table": "tickets"}},
		}}
	owner.must(200, "PUT", "/v1/pages/desk/draft", map[string]any{"document": v2})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)

	// The list: newest first, who and what changed.
	if s, _ := support.do("GET", "/v1/pages/desk/versions", nil); s != 403 {
		t.Errorf("staff without page.edit listed versions: %d", s)
	}
	vs := owner.must(200, "GET", "/v1/pages/desk/versions", nil)["versions"].([]any)
	if len(vs) != 2 {
		t.Fatalf("versions: %v", vs)
	}
	newest, first := vs[0].(map[string]any), vs[1].(map[string]any)
	if newest["version"] != float64(2) || newest["current"] != true || first["current"] != false {
		t.Errorf("order: %v", vs)
	}
	for _, want := range []string{`Added "Open".`, `Took away "All".`, "Now opened by everyone in the business."} {
		if !strings.Contains(newest["summary"].(string), want) {
			t.Errorf("summary lacks %q: %v", want, newest["summary"])
		}
	}
	if !strings.Contains(first["summary"].(string), "First published") || first["who"] != "owners and support" {
		t.Errorf("first: %v", first)
	}
	if by, _ := newest["published_by"].(string); by == "" || strings.Contains(by, "-") && len(by) == 36 {
		t.Errorf("published by: %q", by)
	}

	// A column v1 shows goes. Bringing v1 back makes a draft with the
	// problem said; staff still see v2; publishing refuses it.
	tid := owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any)[0].(map[string]any)["id"].(string)
	owner.must(200, "DELETE", "/v1/tables/"+tid+"/columns/customer", map[string]any{"confirm": "customer"})
	back := adm.must(200, "POST", "/v1/pages/desk/versions/1/bring-back", nil)
	if back["has_draft"] != true || back["version"] != float64(2) || !strings.Contains(mustString(t, back["problems"]), "customer") {
		t.Errorf("brought back: %v", back)
	}
	if v := viewOf(t, support, "desk", nil); blockOf(t, v, "open") == nil || blockOf(t, v, "all") != nil {
		t.Errorf("staff see the brought-back version: %v", v["blocks"])
	}
	if s, out := owner.do("POST", "/v1/pages/desk/publish", nil); s != 400 || !strings.Contains(mustString(t, out), "customer") {
		t.Errorf("published a brought-back version against today's tables: %d %v", s, out)
	}
	if got := pagesAudits(t, w, "page.bring_back"); len(got) != 1 || !strings.Contains(got[0], `"version": 1`) && !strings.Contains(got[0], `"version":1`) {
		t.Errorf("audit: %v", got)
	}

	// Four-eyes: the person who brought it back saved the draft.
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	adm.must(200, "POST", "/v1/pages/desk/versions/2/bring-back", nil)
	if s, out := adm.do("POST", "/v1/pages/desk/publish", nil); s != 403 || !strings.Contains(out["error"].(string), "another person") {
		t.Errorf("published what they brought back under four-eyes: %d %v", s, out)
	}
	if pub := owner.must(200, "POST", "/v1/pages/desk/publish", nil); pub["version"] != float64(3) {
		t.Errorf("published: %v", pub)
	}

	// Nothing to bring back: the version people see with no draft; none such.
	if s, _ := owner.do("POST", "/v1/pages/desk/versions/3/bring-back", nil); s != 409 {
		t.Errorf("brought back the current version: %d", s)
	}
	if s, _ := owner.do("POST", "/v1/pages/desk/versions/99/bring-back", nil); s != 404 {
		t.Errorf("version 99: %d", s)
	}
	if s, _ := support.do("POST", "/v1/pages/desk/versions/1/bring-back", nil); s != 403 {
		t.Errorf("staff brought a version back: %d", s)
	}
}

// A page goes to a file with no rows and nothing personal, and comes back
// in as another page's draft: read as a page, named afresh when its name
// is taken, held against this business's tables (what it lacks said in
// plain words), and counted against max_pages.
func TestPagesExportImport(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	other := w.tenant(t, "Other", "owner@other.test")
	support := addMember(t, w.world, owner, "sam", "support")
	id := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "customer": "Bayo", "status": "open", "phone": "2348012345678", "amount_kobo": 250000},
	}})
	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk",
		"blocks": []any{
			map[string]any{"id": "bayo", "type": "figure", "label": "Bayo's", "value": "count", "source": map[string]any{"table": "tickets",
				"filters": []any{map[string]any{"column": "phone", "op": "eq", "value": "2348012345678"}, map[string]any{"column": "status", "op": "eq", "value": "open"}}}},
			map[string]any{"id": "all", "type": "table", "source": map[string]any{"table": "tickets"},
				"columns": []any{map[string]any{"column": "title"}, map[string]any{"column": "amount_kobo", "naira": true}}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)

	if s, _ := support.do("GET", "/v1/pages/desk/export", nil); s != 403 {
		t.Errorf("staff exported: %d", s)
	}
	status, hdr, body := owner.rawGet(t, "/v1/pages/desk/export")
	if status != 200 || !strings.Contains(hdr.Get("Content-Disposition"), `desk.page.json`) || hdr.Get("Taskiem-Left-Out") != "1" {
		t.Fatalf("export: %d %v %s", status, hdr, body)
	}
	if strings.Contains(body, "2348012345678") || strings.Contains(body, "Printer jam") || strings.Contains(body, "250000") {
		t.Errorf("the file carries a row or a personal value: %s", body)
	}
	var file map[string]any
	if err := json.Unmarshal([]byte(body), &file); err != nil || file["page"] != "page/v1" {
		t.Fatalf("file: %v %s", err, body)
	}
	if got := pagesAudits(t, w, "page.export"); len(got) != 1 {
		t.Errorf("export audit: %v", got)
	}

	// Back into the same business: the name is taken, so desk-2.
	imp := owner.must(201, "POST", "/v1/pages/import", map[string]any{"document": file})
	pg := imp["page"].(map[string]any)
	if pg["name"] != "desk-2" || imp["renamed_from"] != "desk" || pg["published"] != false || len(imp["missing"].([]any)) != 0 || len(pg["problems"].([]any)) != 0 {
		t.Errorf("import here: %v", imp)
	}

	// Into another business, which has no such table: made as a draft, the
	// table it lacks said in plain words; publishing refuses it.
	imp = other.must(201, "POST", "/v1/pages/import", map[string]any{"document": file})
	if imp["page"].(map[string]any)["name"] != "desk" || imp["renamed_from"] != nil || mustString(t, imp["missing"]) != `["This business has no table called tickets."]` {
		t.Errorf("import there: %v", imp)
	}
	if s, _ := other.do("POST", "/v1/pages/desk/publish", nil); s != 400 {
		t.Errorf("published a page over a table the business lacks: %d", s)
	}
	// A table with fewer columns: which are missing.
	other.must(201, "POST", "/v1/tables", map[string]any{"name": "tickets", "title": "Tickets", "columns": []map[string]any{{"name": "title", "type": "text"}}})
	imp = other.must(201, "POST", "/v1/pages/import", map[string]any{"document": file})
	if got := mustString(t, imp["missing"]); !strings.Contains(got, "The table Tickets has no columns") || !strings.Contains(got, "amount_kobo") {
		t.Errorf("missing columns: %v", got)
	}

	// Not a page: refused, nothing saved.
	if s, out := owner.do("POST", "/v1/pages/import", map[string]any{"document": map[string]any{"page": "page/v1", "name": "x", "blocks": []any{}}}); s != 400 || out["code"] != "page_problems" {
		t.Errorf("a bad file: %d %v", s, out)
	}
	if s, _ := support.do("POST", "/v1/pages/import", map[string]any{"document": file}); s != 403 {
		t.Errorf("staff imported: %d", s)
	}
	if got := pagesAudits(t, w, "page.import"); len(got) != 3 {
		t.Errorf("import audits: %v", got)
	}

	// The plan's limit.
	if err := w.env.Store.SetLimits(context.Background(), tenantIDNamed(t, w.world, "Acme"), map[string]any{"max_pages": 2}, "operator"); err != nil {
		t.Fatal(err)
	}
	if s, out := owner.do("POST", "/v1/pages/import", map[string]any{"document": file}); s != 429 || out["limit"] != "max_pages" {
		t.Errorf("over the limit: %d %v", s, out)
	}
}

// A table part says which of its columns the viewer may put the rows in
// order by: never a personal one for someone without table.read.
func TestPagesSortableColumns(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	support := addMember(t, w.world, owner, "sam", "support")
	owner.must(201, "POST", "/v1/tables", staffTable)
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": "people", "title": "People",
		"blocks": []any{map[string]any{"id": "all", "type": "table", "source": map[string]any{"table": "staff"},
			"columns": []any{map[string]any{"column": "name"}, map[string]any{"column": "team"}}}}}})
	owner.must(200, "POST", "/v1/pages/people/publish", nil)
	sortable := func(c *client) string {
		var out []string
		for _, col := range blockOf(t, viewOf(t, c, "people", nil), "all")["columns"].([]any) {
			m := col.(map[string]any)
			if m["sortable"] == true {
				out = append(out, m["column"].(string))
			}
		}
		return strings.Join(out, ",")
	}
	if got := sortable(support); got != "team" {
		t.Errorf("staff may order by %q", got)
	}
	if got := sortable(owner); got != "name,team" {
		t.Errorf("the owner may order by %q", got)
	}
}
