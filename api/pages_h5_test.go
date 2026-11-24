package api_test

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// Pages H5 (build-plan-gaps §7), g-pages-mine: a page of tickets filtered
// by Mine on a phone column shows a staff member their own: the number
// they sign in by, matched as a table may hold it (with or without its
// plus, or written at home), and nobody else's.
func TestPagesMineOnAPhoneColumn(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5mine.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	sam := addMember(t, w.world, owner, "sam", "desk")
	ada := addMember(t, w.world, owner, "ada", "desk") // no phone on her account
	if _, err := w.env.DB.Admin.Exec(context.Background(),
		`INSERT INTO user_phones (user_id, number) SELECT id, '+2348012345678' FROM users WHERE email = 'sam@acme.test'`); err != nil {
		t.Fatal(err)
	}

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "status": "open", "phone": "2348012345678"},
		{"title": "Door stuck", "status": "open", "phone": "08012345678"},
		{"title": "No ink", "status": "open", "phone": "2348098765432"},
		{"title": "Screen dark", "status": "open"},
	}})

	// The editor's Mine on the phone column: Check takes it.
	doc := map[string]any{"page": "page/v1", "name": "my-tickets", "title": "My tickets", "roles": []string{"desk"},
		"filters": []map[string]any{{"id": "mine", "type": "mine", "column": "phone", "label": "Mine"}},
		"blocks": []map[string]any{
			{"id": "list", "type": "list", "title": "Tickets", "source": map[string]any{"table": "tickets", "order": []map[string]any{{"column": "title"}}, "uses": []string{"mine"}},
				"item": map[string]any{"title": "=row.title"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/my-tickets/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}

	mine := map[string]any{"mine": "on"}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", mine), "list")); len(got) != 2 || got[0] != "Door stuck" || got[1] != "Printer jam" {
		t.Errorf("Sam's own tickets: %v", got)
	}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", nil), "list")); len(got) != 4 {
		t.Errorf("without Mine, every ticket: %v", got)
	}
	if rows := blockOf(t, viewOf(t, ada, "my-tickets", mine), "list")["rows"].([]any); len(rows) != 0 {
		t.Errorf("Ada has no phone, so Mine shows her nothing: %v", rows)
	}
}

// g-pages-runs: the page's settings estimate its tools' runs a day: views
// a day over the last seven days (a viewer once a minute, as their answer
// is kept) times the tools its parts start, beside the plan's limits; only
// for those who change pages.
func TestPagesRunsEstimate(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5runs.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	desk := addMember(t, w.world, owner, "desk", "desk")
	ops := addMember(t, w.world, owner, "ops", "desk")
	publishWF(t, owner, "Ticket counts", toolDef("wf_counts", "Ticket counts", "ticket-counts", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=steps.look.output.n"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"text","text":"done"}`, `{"id":"look","type":"transform","config":{"output":{"n":7,"rows":[{"name":"Musa"}]}}}`))
	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"desk"},
		"blocks": []map[string]any{
			{"id": "open", "type": "figure", "label": "Open", "value": "output", "source": map[string]any{"tool": "ticket-counts", "output": "open"}},
			{"id": "rows", "type": "list", "title": "Waiting", "source": map[string]any{"tool": "ticket-counts", "output": "rows"}, "item": map[string]any{"title": "=item.name"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)

	// Desk opens it three times in a minute (one view), Ops once.
	for range 3 {
		viewOf(t, desk, "desk", nil)
	}
	viewOf(t, ops, "desk", nil)
	// Five days ago it was opened 26 times; ten days ago is past the week.
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO page_views (tenant_id, page_id, day, views)
		SELECT tenant_id, id, (now() AT TIME ZONE 'Africa/Lagos')::date - 5, 26 FROM pages WHERE name = 'desk'
		UNION ALL SELECT tenant_id, id, (now() AT TIME ZONE 'Africa/Lagos')::date - 10, 500 FROM pages WHERE name = 'desk'`); err != nil {
		t.Fatal(err)
	}
	runs := owner.must(200, "GET", "/v1/pages/desk", nil)["runs"].(map[string]any)
	// 28 views in the week: 4 a day; one tool feeds both parts with the
	// same values (one run a view): about 4 runs a day.
	if runs["views_a_day"] != float64(4) || runs["tools"] != float64(1) || runs["runs_a_day"] != float64(4) {
		t.Errorf("estimate: %v", runs)
	}
	if _, ok := runs["runs_per_day"]; !ok {
		t.Errorf("the plan's limit is beside it: %v", runs)
	}
	// Staff who only open the page are not told.
	if _, ok := desk.must(200, "GET", "/v1/pages/desk", nil)["runs"]; ok {
		t.Errorf("a viewer was given the estimate")
	}
	// A draft with a second tool counts it.
	doc["blocks"] = append(doc["blocks"].([]map[string]any), map[string]any{"id": "more", "type": "figure", "label": "More", "value": "output",
		"source": map[string]any{"tool": "ticket-counts", "output": "open", "inputs": map[string]any{"x": "1"}}})
	owner.do("PUT", "/v1/pages/desk/draft", map[string]any{"document": doc})
	if runs := owner.must(200, "GET", "/v1/pages/desk", nil)["runs"].(map[string]any); runs["tools"] != float64(2) || runs["runs_a_day"] != float64(8) {
		t.Errorf("the draft's tools: %v", runs)
	}
}
