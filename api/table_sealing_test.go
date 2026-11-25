package api_test

import (
	"context"
	"strings"
	"testing"
)

// A run seals only a data table's personal columns (the founder's
// decision of 24 November 2026; docs/privacy.md#data-tables-in-a-run):
// the look-up recipe answers with a product's name and price, while the
// customer's phone in the same row stays sealed in run_events and masked
// in the answer and in the run's history for a viewer who may not reveal
// it. A column made personal later is sealed in the rows read after.
func TestTableRowsSealedByColumn(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Ada Stores", "owner@sealing.test")
	viewer := w.member(t, owner, "vic@sealing.test", "viewer")
	ctx := context.Background()

	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "orders_book", "title": "Orders book", "columns": []map[string]any{
		{"name": "item", "type": "text"}, {"name": "price", "type": "number"}, {"name": "phone", "type": "phone"}, {"name": "notes", "type": "text"},
	}})
	id := tbl["id"].(string)
	if cols := toJSON(tbl["columns"]); strings.Count(cols, `"personal":true`) != 1 {
		t.Fatalf("phone is personal by default: %s", cols)
	}
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"item": "Rice 50kg", "price": 45000, "phone": "+2348031112222", "notes": "Paid half"}})

	out := owner.must(201, "POST", "/v1/templates/tool-look-up/instantiate", map[string]any{"params": map[string]any{
		"command": "price-of", "table": "orders_book", "match_column": "item", "first_column": "price", "second_column": "phone"}})
	owner.must(200, "POST", "/v1/workflows/"+out["id"].(string)+"/versions/1/publish", nil)
	lookUp := func() (string, map[string]any) {
		t.Helper()
		got := owner.must(201, "POST", "/v1/tools/price-of/start", map[string]any{"input": map[string]any{"word": "rice"}})
		w.env.Drain(t)
		run := got["run_id"].(string)
		return run, owner.must(200, "GET", "/v1/runs/"+run+"/answer", nil)
	}
	events := func(run, typ, step string) string {
		t.Helper()
		var raw string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(string_agg(payload::text, ''), '') FROM run_events WHERE run_id = $1 AND type = $2 AND COALESCE(step_id, '') = $3`,
			run, typ, step).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// (1) The answer shows the product's name and price.
	run, a := lookUp()
	lines := toJSON(a["lines"])
	if a["title"] != "Rice 50kg" || !strings.Contains(lines, `"value":"45000"`) {
		t.Errorf("answer: %v", a)
	}
	// (2) The phone in the same row: masked in the answer...
	if !strings.Contains(lines, `"value":"•••"`) || strings.Contains(toJSON(a), "1112222") {
		t.Errorf("the phone in the answer: %s", lines)
	}
	// ... sealed in run_events, in the step's rows and in the outputs
	// built from it, while the ordinary columns are kept as they are.
	step := events(run, "StepCompleted", "find")
	if !strings.Contains(step, `"item": "Rice 50kg"`) || !strings.Contains(step, `"notes": "Paid half"`) || !strings.Contains(step, `"price": 45000`) ||
		strings.Contains(step, "1112222") || !strings.Contains(step, `"phone": {"ct"`) && !strings.Contains(step, `"phone": {"$pii"`) {
		t.Errorf("the step in run_events: %s", step)
	}
	if done := events(run, "RunCompleted", ""); !strings.Contains(done, `"title": "Rice 50kg"`) || strings.Contains(done, "1112222") || !strings.Contains(done, `"$pii"`) {
		t.Errorf("the outputs in run_events: %s", done)
	}
	// ... and in the run's history for a viewer who may not reveal it.
	hist := toJSON(viewer.must(200, "GET", "/v1/runs/"+run, nil)["events"])
	if !strings.Contains(hist, "Rice 50kg") || strings.Contains(hist, "1112222") || !strings.Contains(hist, `"$pii"`) {
		t.Errorf("the viewer's history: %s", hist)
	}
	if st, _ := viewer.do("GET", "/v1/runs/"+run+"?reveal=true", nil); st != 403 {
		t.Errorf("a viewer revealing: %d", st)
	}
	if full := toJSON(owner.must(200, "GET", "/v1/runs/"+run+"?reveal=true", nil)["events"]); !strings.Contains(full, "1112222") {
		t.Errorf("the owner reveals the phone: %s", full)
	}

	// A column made personal later: rows read after the change are sealed
	// by the definition as it is then.
	owner.must(200, "DELETE", "/v1/tables/"+id+"/columns/notes", map[string]any{"confirm": "notes"})
	owner.must(201, "POST", "/v1/tables/"+id+"/columns", map[string]any{"name": "notes", "type": "text", "personal": true})
	rows := w.rows(t, "orders_book")
	owner.must(200, "PATCH", "/v1/tables/"+id+"/rows/"+toJSON(rows[0]["id"]), map[string]any{"set": map[string]any{"notes": "Owes 22,500 since March"}})
	run2, a2 := lookUp()
	if a2["title"] != "Rice 50kg" {
		t.Errorf("answer after: %v", a2)
	}
	if step := events(run2, "StepCompleted", "find"); strings.Contains(step, "Owes 22,500") || !strings.Contains(step, `"item": "Rice 50kg"`) {
		t.Errorf("a column made personal later: %s", step)
	}
	// The run before keeps what it recorded.
	if step := events(run, "StepCompleted", "find"); !strings.Contains(step, `"notes": "Paid half"`) {
		t.Errorf("the earlier run: %s", step)
	}

	// Writing rows: the row a step adds is sealed column by column too.
	added := owner.must(201, "POST", "/v1/templates/tool-add-a-record/instantiate", map[string]any{"params": map[string]any{"command": "add-expense", "table": "expenses"}})
	owner.must(200, "POST", "/v1/workflows/"+added["id"].(string)+"/versions/1/publish", nil)
	st, got := owner.do("POST", "/v1/tools/add-expense/start", map[string]any{"input": map[string]any{"what": "Diesel", "amount": 3000}})
	if st != 200 && st != 201 {
		t.Fatalf("add: %d %v", st, got)
	}
	if got["confirm"] != nil {
		got = owner.must(201, "POST", "/v1/tools/add-expense/confirm", map[string]any{"confirm": got["confirm"], "input": map[string]any{"what": "Diesel", "amount": 3000}})
	}
	w.env.Drain(t)
	var sched string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1 AND type = 'StepScheduled'`, got["run_id"]).Scan(&sched); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sched, `"what": "Diesel"`) {
		t.Errorf("the row written, in run_events: %s", sched)
	}
}
