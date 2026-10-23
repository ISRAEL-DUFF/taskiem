package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/datatables"
)

var moneyLogColumns = []datatables.Column{
	{Name: "entry_id", Type: "text", Required: true},
	{Name: "amount_kobo", Type: "integer"},
	{Name: "direction", Type: "text"},
}

// keptCode reports a refusal as the API answers a kept table.
func keptCode(t *testing.T, what string, status int, out map[string]any) {
	t.Helper()
	msg, _ := out["error"].(string)
	if status != 409 || out["code"] != "table_kept" || !strings.Contains(msg, "kept by Taskiem") {
		t.Errorf("%s: %d %v", what, status, out)
	}
}

// A kept table through the API (docs/data-tables.md#kept-tables): listed
// and read, searched and exported like any table, with kept_by; every
// write, schema change and delete refused with table_kept before anything
// reaches Xenibase; its name not to be taken; a workflow writing it
// flagged; never a storefront's catalogue.
func TestKeptTablesThroughTheAPI(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	debtors := owner.must(201, "POST", "/v1/tables", debtorsTable)
	if debtors["kept_by"] != nil {
		t.Fatalf("an ordinary table is kept: %v", debtors)
	}
	tenant := tenantID(t, owner)
	kept, err := w.svc.EnsureKept(context.Background(), tenant, "money_log", "money_log", moneyLogColumns)
	if err != nil {
		t.Fatal(err)
	}
	project := w.pg.Projects()[0]
	w.pg.Begin(project).Insert("money_log", map[string]any{"id": 1, "entry_id": "e1", "amount_kobo": 250000, "direction": "in"}).Commit()
	id := kept.ID.String()
	base := "/v1/tables/" + id

	// Read, search and export work.
	list := owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any)
	found := false
	for _, x := range list {
		m := x.(map[string]any)
		if m["name"] == "money_log" {
			found = m["kept_by"] == "money_log"
		}
	}
	if !found {
		t.Fatalf("listed %v", list)
	}
	if got := owner.must(200, "GET", base, nil); got["kept_by"] != "money_log" || len(got["columns"].([]any)) != 3 {
		t.Fatalf("got %v", got)
	}
	if rows := owner.must(200, "GET", base+"/rows?q=entry_id:e1", nil)["rows"].([]any); len(rows) != 1 {
		t.Fatalf("search %v", rows)
	}
	if rows := owner.must(200, "POST", base+"/rows/query", map[string]any{"filters": []any{map[string]any{"column": "direction", "op": "eq", "value": "in"}}})["rows"].([]any); len(rows) != 1 {
		t.Fatalf("query %v", rows)
	}
	if st, _, body := owner.rawGet(t, base+"/export"); st != 200 || !strings.Contains(body, "e1") {
		t.Fatalf("export %d %s", st, body)
	}

	// Every change is refused, and nothing is sent.
	before := len(w.pg.Requests)
	st, out := owner.do("POST", base+"/rows", map[string]any{"row": map[string]any{"entry_id": "e2"}})
	keptCode(t, "insert", st, out)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "Corrections are new rows") {
		t.Errorf("insert message: %q", msg)
	}
	st, out = owner.do("PATCH", base+"/rows/1", map[string]any{"set": map[string]any{"amount_kobo": 1}})
	keptCode(t, "update", st, out)
	st, out = owner.do("DELETE", base+"/rows/1", nil)
	keptCode(t, "delete row", st, out)
	st, out = owner.uploadCSV(t, base+"/import", "log.csv", "entry_id,amount_kobo\ne9,100\n")
	keptCode(t, "import", st, out)
	st, out = owner.do("PATCH", base, map[string]any{"title": "Mine now"})
	keptCode(t, "patch table", st, out)
	st, out = owner.do("DELETE", base, map[string]any{"confirm": "money_log"})
	keptCode(t, "delete table", st, out)
	st, out = owner.do("POST", base+"/columns", map[string]any{"name": "extra", "type": "text"})
	keptCode(t, "add column", st, out)
	st, out = owner.do("DELETE", base+"/columns/direction", map[string]any{"confirm": "direction"})
	keptCode(t, "drop column", st, out)
	for _, r := range w.pg.Requests[before:] {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("a refused change reached Xenibase: %s", r)
		}
	}
	if rows := w.pg.Rows(project, "money_log"); len(rows) != 1 || rows[0]["amount_kobo"] == float64(1) {
		t.Fatalf("rows now %v", rows)
	}
	if got := owner.must(200, "GET", base, nil); got["title"] != "Money log" || len(got["columns"].([]any)) != 3 {
		t.Fatalf("the table changed: %v", got)
	}
	// The ordinary table is written as before.
	owner.must(201, "POST", "/v1/tables/"+debtors["id"].(string)+"/rows", map[string]any{"row": map[string]any{"name": "Ada"}})

	// Names Taskiem keeps are not to be taken, made or not.
	st, out = owner.do("POST", "/v1/tables", map[string]any{"name": "money_matches", "columns": []map[string]any{{"name": "a", "type": "text"}}})
	if st != 409 || out["code"] != "table_kept" {
		t.Errorf("money_matches made: %d %v", st, out)
	}
	if st, out = owner.do("POST", "/v1/tables", map[string]any{"name": "money_log", "columns": []map[string]any{{"name": "a", "type": "text"}}}); st != 409 || !strings.Contains(out["error"].(string), "already exists") {
		t.Errorf("money_log made again: %d %v", st, out)
	}

	// A workflow writing it is flagged in the editor (the worker refuses
	// it when it runs: engine/datatables); one reading it is not.
	writes := `{"schema":"wd/v1","id":"wf_w","version":1,"name":"w","trigger":{"type":"manual"},"steps":[{"id":"w","type":"connector","connector":"xenibase@1","action":"delete_rows","connection":"data_tables","input":{"table":"money_log","key":1}}]}`
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "writes", "definition": json.RawMessage(writes)})
	problems, _ := json.Marshal(wf["problems"])
	if !strings.Contains(string(problems), "kept by Taskiem") || !strings.Contains(string(problems), "/steps/0/input/table") {
		t.Errorf("problems %s", problems)
	}
	reads := strings.Replace(strings.Replace(writes, `"delete_rows"`, `"read_rows"`, 1), `"wf_w"`, `"wf_r"`, 1)
	wf = owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "reads", "definition": json.RawMessage(reads)})
	if problems, _ := json.Marshal(wf["problems"]); strings.Contains(string(problems), "kept") {
		t.Errorf("a read flagged: %s", problems)
	}

	// Never a storefront's catalogue.
	st, out = owner.do("PUT", "/v1/storefront", map[string]any{"slug": "acme-shop", "draft": map[string]any{"name": "Acme"}, "catalogue_table": id,
		"catalogue_columns": map[string]any{"name": "entry_id"}})
	if st != 400 || !strings.Contains(out["error"].(string), "Taskiem keeps") {
		t.Errorf("kept catalogue: %d %v", st, out)
	}
}

// Chat reads a kept table and finds in it, but never adds to it: asking
// is answered with why, and a row already waiting for yes (the session
// pointed at it) is not sent.
func TestKeptTableInChat(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000019"
	w.bind(t, owner, on)
	owner.must(201, "POST", "/v1/tables", debtorsTable)
	kept, err := w.srv.Tables.EnsureKept(context.Background(), tenantID(t, owner), "money_log", "money_log", moneyLogColumns)
	if err != nil {
		t.Fatal(err)
	}
	project := w.pg.Projects()[0]
	w.pg.Begin(project).Insert("money_log", map[string]any{"id": 1, "entry_id": "e1", "amount_kobo": 250000, "direction": "in"}).Commit()

	if r := w.say(t, on, "tables"); !strings.Contains(r, "Money log") {
		t.Errorf("list: %q", r)
	}
	if r := w.say(t, on, "find money log e1"); !strings.Contains(r, "entry_id: e1") {
		t.Errorf("find: %q", r)
	}
	if r := w.say(t, on, "add to money log"); !strings.Contains(r, "Money log is kept by Taskiem") || !strings.Contains(r, "Corrections are new rows") {
		t.Errorf("add: %q", r)
	}
	// A row waiting for yes, its session then pointed at the kept table.
	w.say(t, on, "add to debtors")
	w.say(t, on, "Ada")
	w.say(t, on, "skip")
	w.say(t, on, "skip")
	if r := w.say(t, on, "skip"); !strings.Contains(r, "Add this row to Debtors?") {
		t.Fatalf("confirmation: %q", r)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE chat_sessions SET data = jsonb_set(data, '{table}', to_jsonb($2::text)) WHERE number = $1`, on, kept.ID.String()); err != nil {
		t.Fatal(err)
	}
	before := len(w.pg.Requests)
	if r := w.say(t, on, "yes"); !strings.Contains(r, "kept by Taskiem") {
		t.Errorf("yes: %q", r)
	}
	for _, r := range w.pg.Requests[before:] {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("sent %s", r)
		}
	}
	if rows := w.pg.Rows(project, "money_log"); len(rows) != 1 {
		t.Errorf("rows %v", rows)
	}
}
