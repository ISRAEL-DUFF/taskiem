package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/notify"
)

// toList is a JSON list, or nothing.
func toList(v any) []any {
	l, _ := v.([]any)
	return l
}

// The eight recipes (made tools §13, build plan M6): each installs from
// the template library with the command chosen at install (the next free
// one when it is taken, said so), publishes, and runs as a tool, its
// answer drawn from the run; the daily report and watch and act install
// their schedule beside the tool, whose button opens it.
func TestRecipesInstallPublishAndRun(t *testing.T) {
	w := newWATablesWorld(t)
	told := &recordTeller{}
	if err := w.env.Registry.Register(notify.New(told)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "owner@recipes.test")

	// The tables the recipes work on.
	table := func(name string, cols ...map[string]any) string {
		t.Helper()
		return owner.must(201, "POST", "/v1/tables", map[string]any{"name": name, "title": name, "columns": cols})["id"].(string)
	}
	col := func(name, typ string) map[string]any { return map[string]any{"name": name, "title": name, "type": typ} }
	products := table("products", col("item", "text"), col("price", "number"), col("size", "text"))
	owner.must(201, "POST", "/v1/tables/"+products+"/rows", map[string]any{"rows": []map[string]any{{"item": "Rice 50kg", "price": 45000, "size": "bag"}, {"item": "Beans", "price": 12000, "size": "paint"}}})
	tickets := table("tickets", col("title", "text"), col("status", "text"))
	owner.must(201, "POST", "/v1/tables/"+tickets+"/rows", map[string]any{"rows": []map[string]any{{"title": "Printer jam", "status": "open"}, {"title": "No Wi-Fi", "status": "open"}, {"title": "Old", "status": "done"}}})
	sales := table("sales", col("what", "text"), col("amount", "number"))
	owner.must(201, "POST", "/v1/tables/"+sales+"/rows", map[string]any{"rows": []map[string]any{{"what": "Rice", "amount": 45000}, {"what": "Beans", "amount": 12000}}})
	items := table("items", col("name", "text"), col("quantity", "integer"), col("status", "text"))
	owner.must(201, "POST", "/v1/tables/"+items+"/rows", map[string]any{"rows": []map[string]any{{"name": "Rice", "quantity": 2, "status": "ok"}}})

	// The library shows recipes with their tool.
	recipes := owner.must(200, "GET", "/v1/templates?tool=true", nil)["templates"].([]any)
	if len(recipes) != 8 {
		t.Fatalf("recipes: %d", len(recipes))
	}
	for _, raw := range recipes {
		r := raw.(map[string]any)
		tool, _ := r["tool"].(map[string]any)
		if tool == nil || tool["command"] == "" || tool["care"] == "" || r["category"] != "tools" {
			t.Errorf("%s: %v", r["id"], tool)
		}
	}
	if look := owner.must(200, "GET", "/v1/templates/tool-look-up", nil)["tool"].(map[string]any); look["care"] != "answers" || look["answer"] != "card" {
		t.Errorf("look up's tool: %v", look)
	}

	install := func(id string, params map[string]any) map[string]any {
		t.Helper()
		out := owner.must(201, "POST", "/v1/templates/"+id+"/instantiate", map[string]any{"params": params})
		owner.must(200, "POST", "/v1/workflows/"+out["id"].(string)+"/versions/1/publish", nil)
		return out
	}
	start := func(command string, input map[string]any) map[string]any {
		t.Helper()
		st, got := owner.do("POST", "/v1/tools/"+command+"/start", map[string]any{"input": input})
		if st != 200 && st != 201 {
			t.Fatalf("start %s: %d %v", command, st, got)
		}
		if got["confirm"] != nil {
			got = owner.must(201, "POST", "/v1/tools/"+command+"/confirm", map[string]any{"confirm": got["confirm"], "input": input})
		}
		w.env.Drain(t)
		return owner.must(200, "GET", "/v1/runs/"+got["run_id"].(string)+"/answer", nil)
	}

	// Look up: a card, at once.
	got := install("tool-look-up", map[string]any{"command": "Price Of", "table": "products", "match_column": "item", "first_column": "price", "second_column": "size"})
	if tool := got["tool"].(map[string]any); tool["command"] != "price-of" || tool["changed"] != false {
		t.Fatalf("installed: %v", got)
	}
	// A run seals only a table's personal columns (none here), so the
	// row's words show in the answer as its numbers do (docs/privacy.md).
	if a := start("price-of", map[string]any{"word": "rice"}); a["state"].(map[string]any)["text"] != "Found" || a["title"] != "Rice 50kg" ||
		!strings.Contains(toJSON(a["lines"]), `"value":"45000"`) || !strings.Contains(toJSON(a["lines"]), `"value":"bag"`) || strings.Contains(toJSON(a), "•••") {
		t.Errorf("look up: %v", a)
	}
	if a := start("price-of", map[string]any{"word": "yam"}); a["state"].(map[string]any)["text"] != "Not found" {
		t.Errorf("look up, none: %v", a)
	}
	// The same recipe again: the next free command, said so.
	again := owner.must(201, "POST", "/v1/templates/tool-look-up/instantiate", map[string]any{"params": map[string]any{"command": "price-of"}})
	if tool := again["tool"].(map[string]any); tool["command"] != "price-of-2" || tool["changed"] != true || tool["asked"] != "price-of" {
		t.Errorf("taken: %v", again["tool"])
	}
	// One of Taskiem's own words is never given.
	if tool := owner.must(201, "POST", "/v1/templates/tool-add-a-record/instantiate", map[string]any{"params": map[string]any{"command": "invoice"}})["tool"].(map[string]any); tool["command"] != "invoice-2" {
		t.Errorf("reserved: %v", tool)
	}

	// Add a record: a sentence back, the row written.
	// Its table is made with it.
	added := install("tool-add-a-record", map[string]any{"command": "add-expense", "table": "expenses"})
	if !strings.Contains(toJSON(added["tables"]), `"status":"created"`) {
		t.Fatalf("its table: %v", added)
	}
	if a := start("add-expense", map[string]any{"what": "Diesel", "amount": 3000}); a["text"] != "Added to expenses: 3000." {
		t.Errorf("add: %v", a)
	}
	if rows := w.rows(t, "expenses"); len(rows) != 1 || rows[0]["what"] != "Diesel" {
		t.Errorf("expense rows: %v", rows)
	}

	// List and act: the open rows, each with a button that marks it done.
	install("tool-list-and-act", map[string]any{"command": "open-tickets"})
	a := start("open-tickets", map[string]any{})
	rows, _ := a["rows"].([]any)
	if a["type"] != "list" || len(rows) != 2 {
		t.Fatalf("list: %v", a)
	}
	// Each row's title, as the table holds it.
	if j := toJSON(rows); !strings.Contains(j, "Printer jam") || !strings.Contains(j, "No Wi-Fi") || strings.Contains(j, "•••") {
		t.Errorf("list's rows: %s", j)
	}
	token := rows[0].(map[string]any)["actions"].([]any)[0].(map[string]any)["token"].(string)
	pressed := owner.must(200, "POST", "/v1/tools/buttons/"+token, nil)
	if pressed["command"] != "open-tickets" {
		t.Fatalf("pressed: %v", pressed)
	}
	if a := start("open-tickets", pressed["inputs"].(map[string]any)); len(toList(a["rows"])) != 1 {
		t.Errorf("after marking one: %v", a)
	}

	// Ask for approval: waits for the approver, then the asker is told.
	install("tool-ask-for-approval", map[string]any{"command": "ask-approval"})
	asked := owner.must(200, "POST", "/v1/tools/ask-approval/start", map[string]any{"input": map[string]any{"what": "A printer", "why": "Ours broke"}})
	asked = owner.must(201, "POST", "/v1/tools/ask-approval/confirm", map[string]any{"confirm": asked["confirm"], "input": map[string]any{"what": "A printer", "why": "Ours broke"}})
	run := asked["run_id"].(string)
	w.env.Drain(t)
	approver := w.member(t, owner, "boss@recipes.test", "owner")
	approver.must(200, "POST", "/v1/approvals/"+run+"/decide", map[string]any{"decision": "approved"})
	w.env.Drain(t)
	if a := owner.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["text"] != "Approved: A printer" {
		t.Errorf("approval: %v", a)
	}

	// Tell the team: how many it went to.
	install("tool-tell-the-team", map[string]any{"command": "tell-team"})
	if a := start("tell-team", map[string]any{"message": "Open at 10"}); a["text"] != "Sent to 1 people." {
		t.Errorf("tell: %v", a)
	}
	if msgs := told.all(); len(msgs) != 1 || msgs[0].Text != "Open at 10" {
		t.Errorf("told: %+v", msgs)
	}

	// Call a system: two chosen fields of what it answers.
	sys := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{"status": "shipped " + r.URL.Query().Get("q"), "updated": "today", "hidden": "x"})
	}))
	t.Cleanup(sys.Close)
	owner.must(204, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	install("tool-call-a-system", map[string]any{"command": "check-order", "address": sys.URL + "/orders"})
	if a := start("check-order", map[string]any{"value": "AD-12"}); a["title"] != "AD-12" || !strings.Contains(toJSON(a["lines"]), "shipped AD-12") || strings.Contains(toJSON(a), `"x"`) {
		t.Errorf("call: %v", a)
	}

	// Daily report: the tool lists today's rows; its schedule is installed
	// beside it, its button opening the tool.
	rep := install("tool-daily-report", map[string]any{"command": "sales-today", "table": "sales"})
	comp := rep["tool"].(map[string]any)["companion"].(map[string]any)
	if comp["name"] != "Daily report" {
		t.Fatalf("companion: %v", rep)
	}
	v := owner.must(200, "GET", "/v1/workflows/"+comp["id"].(string)+"/versions/1", nil)
	if !strings.Contains(toJSON(v), `"tool":"sales-today"`) || !strings.Contains(toJSON(v), `"schedule"`) {
		t.Errorf("the schedule's button: %v", v)
	}
	owner.must(200, "POST", "/v1/workflows/"+comp["id"].(string)+"/versions/1/publish", nil)
	// The stand-in data API keeps no created_at, so no row is today's
	// here; templates/tests/tool-daily-report.test.json lists them.
	if a := start("sales-today", map[string]any{}); a["type"] != "list" || a["status"] != "completed" || a["empty"] != "Nothing yet today" {
		t.Errorf("today: %v", a)
	}

	// Watch and act: the button's tool marks the row handled.
	watch := install("tool-watch-and-act", map[string]any{"command": "handle", "table": "items"})
	if watch["tool"].(map[string]any)["companion"] == nil {
		t.Fatalf("watch's schedule: %v", watch)
	}
	id := w.rows(t, "items")[0]["id"]
	if a := start("handle", map[string]any{"row": id}); a["text"] != "Marked handled." {
		t.Errorf("handle: %v", a)
	}
	if st := w.rows(t, "items")[0]["status"]; st != "handled" {
		t.Errorf("item: %v", st)
	}
}
