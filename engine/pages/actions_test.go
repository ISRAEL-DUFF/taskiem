package pages

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// g2 is a page with every G2 part: the page's buttons, a figure and a
// list a tool works out, a list with a tool's button and a signal on each
// row (when), a table with a tick, and a status.
const g2 = `{"page": "page/v1", "name": "desk", "title": "Desk", "place": {"area": "orders"},
  "filters": [{"id": "q", "type": "search"}],
  "blocks": [
    {"id": "new", "type": "buttons", "actions": [{"label": "New ticket", "tool": "new-ticket", "inputs": {"q": "=filters.q"}}]},
    {"id": "open", "type": "figure", "label": "Open", "value": "count", "alert": true, "source": {"table": "tickets", "filters": [{"column": "status", "op": "eq", "value": "open"}]}},
    {"id": "spent", "type": "figure", "label": "Spent", "value": "output", "naira": true, "source": {"tool": "spend", "output": "total", "inputs": {"month": "=filters.q"}}},
    {"id": "who", "type": "list", "source": {"tool": "spend", "output": "rows"}, "item": {"title": "=item.name", "sub": "=outputs.total"},
     "actions": [{"label": "Pay", "tool": "new-ticket", "inputs": {"ref": "=item.name"}}]},
    {"id": "list", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"},
     "actions": [{"label": "Assign to me", "tool": "new-ticket", "inputs": {"ref": "=row.title", "phone": "=row.phone"}, "once": true},
                 {"label": "Confirm", "signal": {"workflow": "Deliveries", "name": "confirm", "to": "=row.title", "input": {"by": "page"}}, "when": "=row.status == 'open'"}]},
    {"id": "all", "type": "table", "source": {"table": "tickets"}, "columns": [{"column": "title"}, {"column": "done", "edit": true}],
     "actions": [{"label": "Approve", "approve": {"workflow": "Deliveries", "run": "=row.title", "step": "ok"}}]},
    {"id": "state", "type": "status", "workflow": "Deliveries"}
  ]}`

var ticketsDone = func() datatables.Table {
	t := tickets
	t.Columns = append(append([]datatables.Column{}, tickets.Columns...), datatables.Column{Name: "done", Title: "Done", Type: "boolean"})
	return t
}()

func g2Env() Env {
	return Env{Tables: []datatables.Table{ticketsDone},
		Tool: func(c string) (ToolInfo, bool) {
			switch c {
			case "new-ticket":
				return ToolInfo{Name: "New ticket", Care: wdcheck.CareYes, Inputs: []string{"ref", "phone", "q"}}, true
			case "spend":
				return ToolInfo{Name: "Spend", Care: wdcheck.CareAnswers, Outputs: map[string]string{"total": "integer", "rows": "list"}, Inputs: []string{"month"}}, true
			case "log":
				return ToolInfo{Name: "Log", Care: wdcheck.CareYes, Outputs: map[string]string{"n": "integer"}}, true
			}
			return ToolInfo{}, false
		},
		Workflow: func(n string) bool { return n == "Deliveries" },
		Signal: func(wf, ev string) string {
			if wf == "Deliveries" && ev == "confirm" {
				return ""
			}
			return wf + " does not let a page tell it " + ev
		},
	}
}

func TestG2Good(t *testing.T) {
	p := parseOK(t, g2)
	if ps := Check(p, g2Env()); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	if got := strings.Join(p.UsesTables(), ","); got != "tickets" {
		t.Errorf("tables: %s", got)
	}
}

func TestG2Refusals(t *testing.T) {
	parse := []struct{ from, to, path, says string }{
		{`"actions": [{"label": "New ticket", "tool": "new-ticket", "inputs": {"q": "=filters.q"}}]`,
			`"actions": [{"label": "A", "tool": "a"}, {"label": "B", "tool": "b"}, {"label": "C", "tool": "c"}, {"label": "D", "tool": "d"}]`, "/blocks/0/actions", "1 to 3"},
		{`{"label": "New ticket", "tool": "new-ticket", "inputs": {"q": "=filters.q"}}`, `{"label": "New ticket", "tool": "new-ticket", "inputs": {"q": "=row.title"}}`, "/blocks/0/actions/0/inputs/q", "filters only"},
		{`{"label": "Pay", "tool": "new-ticket", "inputs": {"ref": "=item.name"}}`, `{"label": "Pay", "tool": "new-ticket", "inputs": {"ref": "=row.name"}}`, "/blocks/3/actions/0/inputs/ref", "item.name"},
		{`"when": "=row.status == 'open'"`, `"when": "row.status == 'open'"`, "/blocks/4/actions/1/when", "condition"},
		{`"when": "=row.status == 'open'"`, `"when": "=steps.a.output == 1"`, "/blocks/4/actions/1/when", "cannot read"},
		{`"to": "=row.title",`, `"to": "=row.title", "run": "x",`, "/blocks/4/actions/1/signal", "one of them"},
		{`{"label": "Approve", "approve"`, `{"label": "Approve", "tool": "x", "approve"`, "/blocks/5/actions/0", "one of them"},
		{`"source": {"tool": "spend", "output": "rows"}`, `"source": {"tool": "spend", "output": "rows", "filters": [{"column": "a", "op": "eq", "value": 1}]}`, "/blocks/3/source", "values"},
		{`"value": "output", "naira": true`, `"value": "count", "naira": true`, "/blocks/2/value", "tool"},
		{`"value": "count", "alert": true`, `"value": "sum", "of": "amount_kobo", "alert": true`, "/blocks/1/alert", "counts"},
		{`{"id": "state", "type": "status", "workflow": "Deliveries"}`, `{"id": "state", "type": "status"}`, "/blocks/6/workflow", "which"},
		{`"place": {"area": "orders"},`, `"place": {"area": ""},`, "/place/area", "areas"},
	}
	for _, c := range parse {
		doc := strings.Replace(g2, c.from, c.to, 1)
		if doc == g2 {
			t.Fatalf("no change for %s", c.from)
		}
		_, ps := Parse([]byte(doc))
		if !hasProblem(ps, c.path, c.says) {
			t.Errorf("parse %s: want %s %q, got %v", c.to, c.path, c.says, ps)
		}
	}
	// More than three parts a tool feeds.
	many := strings.Replace(g2, `{"id": "state"`, `{"id": "s2", "type": "figure", "label": "B", "value": "output", "source": {"tool": "spend", "output": "total"}},
		{"id": "s3", "type": "heading", "title": "x"}, {"id": "s4", "type": "figure", "label": "C", "value": "output", "source": {"tool": "spend", "output": "total"}}, {"id": "state"`, 1)
	if _, ps := Parse([]byte(many)); !hasProblem(ps, "/blocks", "worked out by a tool") {
		t.Errorf("tool sources: %v", ps)
	}

	check := []struct{ from, to, path, says string }{
		{`"tool": "spend", "output": "total", "inputs"`, `"tool": "log", "output": "n", "inputs"`, "/blocks/2/source/tool", "asks before it runs"},
		{`"tool": "spend", "output": "total", "inputs"`, `"tool": "spend", "output": "nope", "inputs"`, "/blocks/2/source/output", "does not give"},
		{`"source": {"tool": "spend", "output": "rows"}`, `"source": {"tool": "spend", "output": "total"}`, "/blocks/3/source/output", "not a list"},
		{`"inputs": {"month": "=filters.q"}`, `"inputs": {"year": "=filters.q"}`, "/blocks/2/source/inputs/year", "does not ask for"},
		{`"tool": "new-ticket", "inputs": {"q"`, `"tool": "gone", "inputs": {"q"`, "/blocks/0/actions/0/tool", "does not have"},
		{`"workflow": "Deliveries", "name": "confirm"`, `"workflow": "Deliveries", "name": "cancel"`, "/blocks/4/actions/1/signal", "does not let a page"},
		{`"approve": {"workflow": "Deliveries"`, `"approve": {"workflow": "Other"`, "/blocks/5/actions/0/approve/workflow", "does not have"},
		{`{"id": "state", "type": "status", "workflow": "Deliveries"}`, `{"id": "state", "type": "status", "workflow": "Other"}`, "/blocks/6/workflow", "does not have"},
		{`"inputs": {"ref": "=row.title", "phone"`, `"inputs": {"ref": "=row.nope", "phone"`, "/blocks/4/actions/0/inputs/ref", "no column nope"},
		{`{"column": "done", "edit": true}`, `{"column": "title", "edit": true}`, "/blocks/5/columns/1/edit", "yes or no"},
		{`"place": {"area": "orders"},`, `"place": {"area": "garden"},`, "/place/area", "not one of Taskiem's areas"},
	}
	for _, c := range check {
		doc := strings.Replace(g2, c.from, c.to, 1)
		if doc == g2 {
			t.Fatalf("no change for %s", c.from)
		}
		p, ps := Parse([]byte(doc))
		if len(ps) == 0 {
			ps = Check(p, g2Env())
		}
		if !hasProblem(ps, c.path, c.says) {
			t.Errorf("check %s: want %s %q, got %v", c.to, c.path, c.says, ps)
		}
	}
}

func hasProblem(ps []Problem, path, says string) bool {
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, says) {
			return true
		}
	}
	return false
}

// A row's buttons: when hides one; a value from a personal column is the
// mask for a viewer without table.read (so it is asked, not filled), the
// value for one with it.
func TestG2RowActions(t *testing.T) {
	p := parseOK(t, g2)
	b := p.Blocks[4]
	rows := []map[string]any{
		{"id": float64(1), "title": "Printer jam", "status": "open", "phone": "2348012345678"},
		{"id": float64(2), "title": "No ink", "status": "done", "phone": "2348098765432"},
	}
	now := time.Now()
	staff, _ := p.FilterValues(nil, Viewer{}, now)
	shown := Shape(b, ticketsDone, rows, staff)
	if len(shown[0].Actions) != 2 || len(shown[1].Actions) != 1 || shown[1].Actions[0].Label != "Assign to me" {
		t.Fatalf("when: %+v", shown)
	}
	in := ActionValues(b, b.Actions[0].Inputs, RowAct(ticketsDone, rows[0], staff))
	if in["ref"] != "Printer jam" || !strings.Contains(in["phone"].(string), "•") || strings.Contains(in["phone"].(string), "2348012") {
		t.Errorf("masked for staff: %v", in)
	}
	reader, _ := p.FilterValues(nil, Viewer{Personal: true}, now)
	if in := ActionValues(b, b.Actions[0].Inputs, RowAct(ticketsDone, rows[0], reader)); in["phone"] != "2348012345678" {
		t.Errorf("open for table.read: %v", in)
	}
	if !b.ActionShows(1, RowAct(ticketsDone, rows[0], staff)) || b.ActionShows(1, RowAct(ticketsDone, rows[1], staff)) {
		t.Errorf("ActionShows")
	}
	// Ticks only for someone who may change the table's rows.
	all := p.Blocks[5]
	rows[0]["done"] = true
	if got := Shape(all, ticketsDone, rows, staff); got[0].Ticks != nil {
		t.Errorf("ticks without table.edit: %v", got[0].Ticks)
	}
	editor, _ := p.FilterValues(nil, Viewer{Edit: true}, now)
	if got := Shape(all, ticketsDone, rows, editor); len(got[0].Ticks) != 2 || got[0].Ticks[0] != nil || !*got[0].Ticks[1] {
		t.Errorf("ticks: %v", got[0].Ticks)
	}
}

// A tool's list: its words masked as an answer's are; a row's id is its
// place.
func TestG2ToolShapes(t *testing.T) {
	p := parseOK(t, g2)
	vals, _ := p.FilterValues(nil, Viewer{Personal: true}, time.Now())
	outputs := map[string]any{"total": float64(450000), "rows": []any{
		map[string]any{"name": "Call 08031234567"}, map[string]any{"name": "Ngozi"},
	}}
	rows, n := ShapeTool(p.Blocks[3], outputs, vals)
	if n != 2 || rows[1].ID != "1" || rows[1].Title != "Ngozi" || strings.Contains(rows[0].Title, "08031234567") || rows[0].Sub != "450000" {
		t.Errorf("tool rows: %+v", rows)
	}
	if len(rows[0].Actions) != 1 {
		t.Errorf("tool row actions: %+v", rows[0].Actions)
	}
	if shown, _ := ToolFigure(p.Blocks[2], outputs["total"]); shown != "₦4,500" {
		t.Errorf("figure: %s", shown)
	}
	if it, ok := ToolItem(p.Blocks[3], outputs, "1"); !ok || it.(map[string]any)["name"] != "Ngozi" {
		t.Errorf("item: %v", it)
	}
	if _, ok := ToolItem(p.Blocks[3], outputs, "7"); ok {
		t.Errorf("item past the list")
	}
	if got := ToolInputs(p.Blocks[2].Source, Values{Set: map[string]string{"q": "2026-11"}}); got["month"] != "2026-11" {
		t.Errorf("inputs: %v", got)
	}
}

// Pin as a page: a list answer, a card answer, and what cannot be pinned.
func TestG2FromTool(t *testing.T) {
	list := ToolInfo{Name: "Waiting list", Care: wdcheck.CareAnswers, Outputs: map[string]string{"rows": "list", "n": "integer"},
		Answer: &wd.Answer{Type: "list", Title: "Waiting", From: "rows", Item: &wd.AnswerItem{Title: "=item.name", State: &wd.AnswerState{Text: "=item.state"}},
			Actions: []wd.Button{{Label: "Call", Tool: "call", Inputs: map[string]any{"who": "=item.name"}}}}}
	list.Roles = []string{"manager"}
	list.PersonalInputs = []string{"phone"}
	given := map[string]any{"day": "today", "x": "=steps.secret", "phone": "+2348012345678",
		"deep": map[string]any{"list": []any{"=filters.q", "plain", `=a "quoted" \ word`}}}
	p, err := FromTool("waiting", list, given)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "waiting-page" || p.Blocks[0].Source.Tool != "waiting" || p.Blocks[0].Source.Output != "rows" || len(p.Blocks[0].Actions) != 1 {
		t.Errorf("list: %+v", p.Blocks[0])
	}
	// The page opens to the tool's roles; a personal value is not kept.
	if strings.Join(p.Roles, ",") != "manager" {
		t.Errorf("roles: %v", p.Roles)
	}
	if _, kept := p.Blocks[0].Source.Inputs["phone"]; kept {
		t.Errorf("a phone kept on the page: %v", p.Blocks[0].Source.Inputs)
	}
	// Values that read like expressions, at any depth, come back as the
	// very words given: nothing is evaluated.
	got := ToolInputs(p.Blocks[0].Source, Values{Set: map[string]string{"q": "leak"}})
	want := map[string]any{"day": "today", "x": "=steps.secret", "deep": map[string]any{"list": []any{"=filters.q", "plain", `=a "quoted" \ word`}}}
	if toJSON(got) != toJSON(want) {
		t.Errorf("literal values:\n got %s\nwant %s", toJSON(got), toJSON(want))
	}
	if _, ps := Parse(p.Marshal()); len(ps) > 0 {
		t.Errorf("pinned page: %v", ps)
	}
	card := ToolInfo{Name: "Leave", Care: wdcheck.CareAnswers, Outputs: map[string]string{"left": "integer", "who": "string"},
		Answer: &wd.Answer{Type: "card", Title: "=outputs.who", Lines: []wd.AnswerLine{{Label: "Days left", Value: "=outputs.left"}, {Label: "Words", Value: "=outputs.who + 'x'"}}}}
	p, err = FromTool("leave-left", card, nil)
	if err != nil || len(p.Blocks) != 1 || p.Blocks[0].Value != "output" || p.Blocks[0].Source.Output != "left" {
		t.Fatalf("card: %+v %v", p, err)
	}
	asks := list
	asks.Care = wdcheck.CareYes
	if _, err := FromTool("waiting", asks, nil); err == nil || !strings.Contains(err.Error(), "asks before") {
		t.Errorf("asks: %v", err)
	}
	text := list
	text.Answer = &wd.Answer{Type: "text", Text: "hi"}
	if _, err := FromTool("waiting", text, nil); err == nil {
		t.Errorf("text pinned")
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// A tool whose roles are narrower than the page's cannot feed it.
func TestG2ToolRolesNarrower(t *testing.T) {
	env := g2Env()
	tool := env.Tool
	env.Tool = func(c string) (ToolInfo, bool) {
		i, ok := tool(c)
		if c == "spend" {
			i.Roles = []string{"manager"}
		}
		return i, ok
	}
	if ps := Check(parseOK(t, g2), env); !hasProblem(ps, "/blocks/2/source/tool", "opens to more people") {
		t.Errorf("no roles on the page: %v", ps)
	}
	narrow := parseOK(t, strings.Replace(g2, `"title": "Desk",`, `"title": "Desk", "roles": ["manager"],`, 1))
	if ps := Check(narrow, env); hasProblem(ps, "/blocks/2/source/tool", "opens to more people") {
		t.Errorf("same roles: %v", ps)
	}
	wider := parseOK(t, strings.Replace(g2, `"title": "Desk",`, `"title": "Desk", "roles": ["manager", "support"],`, 1))
	if ps := Check(wider, env); !hasProblem(ps, "/blocks/2/source/tool", "opens to more people") {
		t.Errorf("wider: %v", ps)
	}
}
