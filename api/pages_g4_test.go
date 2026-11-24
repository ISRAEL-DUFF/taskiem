package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/pages"
)

// lanesOf is a board's columns as drawn: each column's value and the
// titles of its cards.
func lanesOf(t *testing.T, b map[string]any) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	lanes, ok := b["lanes"].([]any)
	if !ok {
		t.Fatalf("no lanes: %v", b)
	}
	for _, l := range lanes {
		m := l.(map[string]any)
		titles := []string{}
		for _, r := range m["rows"].([]any) {
			titles = append(titles, r.(map[string]any)["title"].(string))
		}
		out[m["value"].(string)] = titles
	}
	return out
}

func cardID(t *testing.T, b map[string]any, title string) string {
	t.Helper()
	for _, l := range b["lanes"].([]any) {
		for _, r := range l.(map[string]any)["rows"].([]any) {
			if m := r.(map[string]any); m["title"] == title {
				return m["id"].(string)
			}
		}
	}
	t.Fatalf("no card %s in %v", title, b)
	return ""
}

// Pages G4, done when: a ticket dragged from Open to Doing runs its tool
// (the board's move: the card read again on the server, the tool's form
// filled in, held to the tool's roles and care, and the run that changes
// the row), and expenses show by week (a chart worked out on the server).
func TestPagesBoardAndChart(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@g4.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	owner.must(201, "PUT", "/v1/roles/support", map[string]any{"permissions": []string{"run.start"}})
	desk := addMember(t, w.world, owner, "desk", "desk")           // the move tool is not for them
	support := addMember(t, w.world, owner, "sup", "support")      // it is
	stranger := addMember(t, w.world, owner, "stranger", "viewer") // not in the page's roles

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "customer": "Bayo", "status": "open", "phone": "2348012345678"},
		{"title": "No ink", "customer": "Ada", "status": "Open"},
		{"title": "Paper", "customer": "Musa", "status": "done"},
		{"title": "Lost", "customer": "Uche", "status": "parked"}, // in none of the board's columns
	}})
	exp := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "expenses", "title": "Expenses", "columns": []map[string]any{
		{"name": "what", "title": "What", "type": "text", "required": true},
		{"name": "amount_kobo", "title": "Amount", "type": "integer"},
		{"name": "spent_on", "title": "Spent on", "type": "date"},
	}})["id"].(string)
	weeks := pages.ChartPeriods(pages.Chart{Period: "week", Last: 4}, time.Now())
	day := func(i, plus int) string { return weeks[i].From.AddDate(0, 0, plus).Format("2006-01-02") }
	owner.must(201, "POST", "/v1/tables/"+exp+"/rows", map[string]any{"rows": []map[string]any{
		{"what": "Diesel", "amount_kobo": 250000, "spent_on": day(0, 0)},
		{"what": "Diesel", "amount_kobo": 100000, "spent_on": day(0, 6)},
		{"what": "Rent", "amount_kobo": 450000, "spent_on": day(2, 1)},
		{"what": "Lunch", "amount_kobo": 120000, "spent_on": day(3, 0)},
		{"what": "Long ago", "amount_kobo": 999900, "spent_on": weeks[0].From.AddDate(0, 0, -30).Format("2006-01-02")},
	}})

	// The tool a card moved runs: it writes the ticket's status, so it asks
	// Yes before it runs; only support (and owners) may use it.
	publishWF(t, owner, "Move ticket", toolDef("wf_move", "Move ticket", "move-ticket", `["support"]`,
		`{"ref":{"type":"string","title":"Ticket"},"status":{"type":"string","title":"Status"}}`,
		`[{"name":"moved","type":"integer","title":"Moved","description":"Moved","value":"=steps.set.output.affected"}]`,
		`{"type":"text","text":"Moved."}`,
		`{"id":"set","type":"connector","connector":"xenibase@1","action":"update_rows","connection":"data_tables",
		  "input":{"table":"tickets","key":"=trigger.body.ref","set":{"status":"=trigger.body.status"}}}`))
	w.env.Drain(t)

	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"desk", "support"},
		"blocks": []map[string]any{
			{"id": "board", "type": "board", "title": "Tickets", "source": map[string]any{"table": "tickets", "order": []map[string]any{{"column": "title"}}},
				"item": map[string]any{"title": "=row.title", "sub": "=row.phone"},
				"board": map[string]any{"column": "status", "columns": []map[string]any{{"value": "open", "label": "Open"}, {"value": "doing", "label": "Doing"}, {"value": "done", "label": "Done"}},
					"move": map[string]any{"label": "Move", "tool": "move-ticket", "inputs": map[string]any{"ref": "=row.id", "status": "=to"}}},
				"detail": map[string]any{"title": "=row.title", "lines": []map[string]any{{"label": "Customer", "value": "=row.customer"}}}},
			{"id": "spend", "type": "chart", "title": "Spent by week", "naira": true, "source": map[string]any{"table": "expenses"},
				"chart": map[string]any{"kind": "bars", "of": "amount_kobo", "by": "spent_on", "period": "week", "last": 4}},
			{"id": "what", "type": "chart", "title": "By what", "source": map[string]any{"table": "expenses"}, "chart": map[string]any{"kind": "bars", "by": "what"}},
		}}

	// The validator refuses what is wrong, in plain words.
	bad := map[string]any{"page": "page/v1", "name": "bad", "title": "Bad", "blocks": []map[string]any{
		{"id": "b", "type": "board", "source": map[string]any{"table": "tickets"}, "item": map[string]any{"title": "=row.title"},
			"board": map[string]any{"column": "phone", "columns": []map[string]any{{"value": "a"}, {"value": "b"}}, "move": map[string]any{"label": "Go", "tool": "nope"}}},
	}}
	ps := toJSON(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": bad})["problems"])
	for _, want := range []string{"it is personal", "which the business does not have"} {
		if !strings.Contains(ps, want) {
			t.Errorf("validate: want %q in %s", want, ps)
		}
	}

	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/desk/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}
	if s, _ := stranger.do("GET", "/v1/pages/desk/view", nil); s != 404 {
		t.Errorf("a stranger opened it: %d", s)
	}

	// The board: a column each, the cards in it (any case), the phone
	// masked for someone without table.read; a card in no column is not on it.
	v := viewOf(t, desk, "desk", nil)
	board := blockOf(t, v, "board")
	lanes := lanesOf(t, board)
	if strings.Join(lanes["open"], ",") != "No ink,Printer jam" || len(lanes["doing"]) != 0 || strings.Join(lanes["done"], ",") != "Paper" || board["count"] != float64(3) {
		t.Fatalf("board: %v %v", lanes, board["count"])
	}
	if got := toJSON(board); strings.Contains(got, "2348012345678") || strings.Contains(got, "Lost") {
		t.Errorf("masked, and no card in no column: %s", got)
	}
	// The move is greyed for desk, who may not use its tool; and refused.
	if mv := board["move"].(map[string]any); mv["tool"] != "move-ticket" || mv["disabled"] == nil {
		t.Errorf("desk's move: %v", mv)
	}
	jam := cardID(t, board, "Printer jam")
	if s, out := actOn(desk, "desk", map[string]any{"block": "board", "row": jam, "action": 0, "to": "doing"}); s != 403 {
		t.Errorf("desk moved a card: %d %v", s, out)
	}

	// Support drags Printer jam from Open to Doing: the server reads the
	// card again and gives the move tool's form filled in, as a button.
	sb := blockOf(t, viewOf(t, support, "desk", nil), "board")
	if mv := sb["move"].(map[string]any); mv["disabled"] != nil {
		t.Errorf("support's move: %v", mv)
	}
	if s, out := actOn(support, "desk", map[string]any{"block": "board", "row": jam, "action": 0, "to": "Nowhere"}); s != 400 {
		t.Errorf("no such column: %d %v", s, out)
	}
	if s, out := actOn(support, "desk", map[string]any{"block": "board", "row": jam, "action": 0, "to": "Open"}); s != 409 || out["code"] != "moved_on" {
		t.Errorf("already there: %d %v", s, out)
	}
	if s, _ := actOn(support, "desk", map[string]any{"block": "board", "row": "99999", "action": 0, "to": "doing"}); s != 404 {
		t.Errorf("no such card: %d", s)
	}
	moved := support.must(200, "POST", "/v1/pages/desk/act?filters=%7B%7D", map[string]any{"block": "board", "row": jam, "action": 0, "to": "doing"})
	tool := moved["tool"].(map[string]any)
	if moved["kind"] != "tool" || tool["command"] != "move-ticket" || toJSON(tool["inputs"]) != `{"ref":"`+jam+`","status":"doing"}` {
		t.Fatalf("moved: %v", moved)
	}
	// The same drop again is the same button (one place a card and a column).
	again := support.must(200, "POST", "/v1/pages/desk/act?filters=%7B%7D", map[string]any{"block": "board", "row": jam, "action": 0, "to": "doing"})
	if again["tool"].(map[string]any)["button"] != tool["button"] {
		t.Errorf("a second drop minted another button: %v", again)
	}
	// Its care: it asks Yes (it writes), then runs once, however often the
	// Yes is sent.
	start := support.must(200, "POST", "/v1/tools/move-ticket/start", map[string]any{"input": tool["inputs"], "button": tool["button"]})
	if start["confirm"] == nil {
		t.Fatalf("a tool that writes asks first: %v", start)
	}
	run := support.must(201, "POST", "/v1/tools/move-ticket/confirm", map[string]any{"confirm": start["confirm"], "input": tool["inputs"]})["run_id"]
	if s, out := support.do("POST", "/v1/tools/move-ticket/confirm", map[string]any{"confirm": start["confirm"], "input": tool["inputs"]}); s >= 300 || out["run_id"] != run {
		t.Errorf("the same Yes again: %d %v", s, out)
	}
	w.env.Drain(t)
	var status string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT status FROM runs WHERE id = $1`, run).Scan(&status)
	if status != "completed" {
		t.Fatalf("the move's run: %s", status)
	}
	lanes = lanesOf(t, blockOf(t, viewOf(t, support, "desk", nil), "board"))
	if strings.Join(lanes["open"], ",") != "No ink" || strings.Join(lanes["doing"], ",") != "Printer jam" {
		t.Errorf("after the move: %v", lanes)
	}

	// More reads one column, and only by its name.
	one := support.must(200, "GET", "/v1/pages/desk/blocks/board?lane=doing&filters=%7B%7D", nil)
	if l := lanesOf(t, one); len(l) != 1 || strings.Join(l["doing"], ",") != "Printer jam" {
		t.Errorf("one column: %v", l)
	}
	support.must(400, "GET", "/v1/pages/desk/blocks/board?lane=nowhere", nil)

	// Expenses show by week: each week added up on the server, oldest
	// first, placed on a scale, a table's heads and a sentence for those
	// who cannot see it; the clients only draw.
	spend := blockOf(t, v, "spend")
	var got []string
	for _, p := range spend["points"].([]any) {
		m := p.(map[string]any)
		got = append(got, m["shown"].(string))
	}
	if strings.Join(got, " ") != "₦3,500 ₦0 ₦4,500 ₦1,200" {
		t.Errorf("by week: %v", got)
	}
	pts := spend["points"].([]any)
	if p := pts[2].(map[string]any); p["long"] != weeks[2].Long || p["share"] != 0.75 {
		t.Errorf("a week: %v", p)
	}
	if spend["kind"] != "bars" || spend["label_head"] != "Week" || spend["value_head"] != "Amount" || spend["zero"] != float64(0) ||
		!strings.Contains(spend["summary"].(string), "Highest ₦4,500") || len(spend["ticks"].([]any)) < 2 {
		t.Errorf("chart: %v", spend)
	}
	what := blockOf(t, v, "what")
	if p := what["points"].([]any)[0].(map[string]any); p["label"] != "Diesel" || p["value"] != float64(2) {
		t.Errorf("by what: %v", what["points"])
	}

	// Another business never reaches this page's board.
	other := w.tenant(t, "Beta", "owner@beta-g4.test")
	if s, _ := actOn(other, "desk", map[string]any{"block": "board", "row": jam, "action": 0, "to": "done"}); s != 404 {
		t.Errorf("another business: %d", s)
	}
}
