package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

var expenses = datatables.Table{Name: "expenses", Title: "Expenses", Columns: []datatables.Column{
	{Name: "what", Title: "What", Type: "text"},
	{Name: "kind", Title: "Kind", Type: "text"},
	{Name: "amount_kobo", Title: "Amount", Type: "integer"},
	{Name: "spent_on", Title: "Spent on", Type: "date"},
	{Name: "card", Title: "Card", Type: "text", Personal: true},
}}

const g4Page = `{"page": "page/v1", "name": "desk", "title": "Desk", "blocks": [
  {"id": "board", "type": "board", "title": "Tickets", "source": {"table": "tickets"},
   "item": {"title": "=row.title", "sub": "=row.customer"},
   "board": {"column": "status", "columns": [{"value": "open", "label": "Open"}, {"value": "doing"}, {"value": "done", "label": "Done"}],
             "move": {"label": "Move", "tool": "move-ticket", "inputs": {"ref": "=row.id", "status": "=to"}}},
   "detail": {"title": "=row.title", "lines": [{"label": "Customer", "value": "=row.customer"}]}},
  {"id": "spend", "type": "chart", "title": "Spent by week", "naira": true, "source": {"table": "expenses"},
   "chart": {"kind": "bars", "of": "amount_kobo", "by": "spent_on", "period": "week", "last": 4}},
  {"id": "kinds", "type": "chart", "title": "By kind", "source": {"table": "expenses"}, "chart": {"kind": "bars", "by": "kind"}}
]}`

func g4Env() Env {
	return Env{Tables: []datatables.Table{tickets, expenses}, Tool: func(c string) (ToolInfo, bool) {
		switch c {
		case "move-ticket":
			return ToolInfo{Name: "Move ticket", Care: wdcheck.CareYes, Inputs: []string{"ref", "status"}}, true
		case "spend-by":
			return ToolInfo{Name: "Spend by", Care: wdcheck.CareAnswers, Outputs: map[string]string{"rows": "list", "n": "integer"}}, true
		}
		return ToolInfo{}, false
	}}
}

func TestBoardAndChartParse(t *testing.T) {
	p := parseOK(t, g4Page)
	if msgs := schema.Validate([]byte(g4Page)); len(msgs) > 0 {
		t.Fatalf("schema: %v", msgs)
	}
	if ps := Check(p, g4Env()); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	if got := strings.Join(Shows(p, g4Env().Tables), "|"); !strings.Contains(got, "Tickets: Status, Title, Customer") || !strings.Contains(got, "Expenses: Spent on, Amount, Kind") {
		t.Errorf("shows: %s", got)
	}
}

// Each row: a board or a chart wrong for one reason, refused in plain words
// at its path (by Parse, or by Check).
func TestBoardAndChartRefusals(t *testing.T) {
	rows := []struct {
		name, doc, path, want string
	}{
		{"one column", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"}]}}`,
			"/blocks/0/board/columns", "2 to 6 columns"},
		{"a column twice", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"},{"value":"Open"}]}}`,
			"/blocks/0/board/columns/1/value", "given once"},
		{"move a signal", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"},{"value":"done"}],"move":{"label":"Go","signal":{"workflow":"w","name":"go","run":"=row.id"}}}}`,
			"/blocks/0/board/move", "opens one of the business's tools"},
		{"move once", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"},{"value":"done"}],"move":{"label":"Go","tool":"move-ticket","once":true}}}`,
			"/blocks/0/board/move", "no once and no when"},
		{"move reads steps", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"},{"value":"done"}],"move":{"label":"Go","tool":"move-ticket","inputs":{"x":"=steps.a"}}}}`,
			"/blocks/0/board/move/inputs/x", "cannot read"},
		{"board over records", `{"id":"b","type":"board","source":{"records":"orders"},"item":{"title":"=row.number"},"board":{"column":"status","columns":[{"value":"open"},{"value":"done"}]}}`,
			"/blocks/0/source/records", "not Taskiem's own records"},
		{"board by a personal column", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"email","columns":[{"value":"a"},{"value":"b"}]}}`,
			"/blocks/0/board/column", "it is personal"},
		{"board by a number", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"amount_kobo","columns":[{"value":"1"},{"value":"2"}]}}`,
			"/blocks/0/board/column", "does not hold words"},
		{"board's move tool missing", `{"id":"b","type":"board","source":{"table":"tickets"},"item":{"title":"=row.title"},"board":{"column":"status","columns":[{"value":"open"},{"value":"done"}],"move":{"label":"Go","tool":"nope"}}}`,
			"/blocks/0/board/move/tool", "does not have"},
		{"chart with no by", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"bars"}}`,
			"/blocks/0/chart/by", "over time"},
		{"a pie", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"pie","by":"kind"}}`,
			"/blocks/0/chart/kind", "bars or a line"},
		{"a line by group", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"line","by":"kind"}}`,
			"/blocks/0/chart/kind", "draw bars"},
		{"too many weeks", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"bars","by":"spent_on","period":"week","last":30}}`,
			"/blocks/0/chart/last", "2 to 12"},
		{"over time by words", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"bars","by":"kind","period":"week"}}`,
			"/blocks/0/chart/by", "does not hold dates"},
		{"adds up words", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"bars","by":"kind","of":"what"}}`,
			"/blocks/0/chart/of", "does not hold numbers"},
		{"by a personal column", `{"id":"c","type":"chart","source":{"table":"expenses"},"chart":{"kind":"bars","by":"card"}}`,
			"/blocks/0/chart/by", "it is personal"},
		{"a chart with the page's filters", `{"id":"c","type":"chart","source":{"table":"expenses","uses":["q"]},"chart":{"kind":"bars","by":"kind"}}`,
			"/blocks/0/source/uses", "narrow its lists"},
		{"a tool's chart with no number", `{"id":"c","type":"chart","source":{"tool":"spend-by","output":"rows"},"chart":{"kind":"bars","label":"=item.week"}}`,
			"/blocks/0/chart", "a label and a number"},
		{"a tool's chart of a figure", `{"id":"c","type":"chart","source":{"tool":"spend-by","output":"n"},"chart":{"kind":"bars","label":"=item.week","value":"=item.total"}}`,
			"/blocks/0/source/output", "not a list"},
		{"a list with a chart", `{"id":"l","type":"list","source":{"table":"expenses"},"item":{"title":"=row.what"},"chart":{"kind":"bars","by":"kind"}}`,
			"/blocks/0/chart", "Only a chart"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			doc := `{"page":"page/v1","name":"desk","title":"Desk","filters":[{"id":"q","type":"search"}],"blocks":[` + r.doc + `]}`
			p, ps := Parse([]byte(doc))
			if p != nil && len(ps) == 0 {
				ps = Check(p, g4Env())
			}
			for _, pr := range ps {
				if pr.Path == r.path && strings.Contains(pr.Message, r.want) {
					return
				}
			}
			t.Errorf("want %q at %s, got %v", r.want, r.path, ps)
		})
	}
}

// Expenses show by week: each week's amounts added up, oldest first, the
// last the week now is in; placed on a scale of round naira marks.
func TestChartByWeek(t *testing.T) {
	p := parseOK(t, g4Page)
	b := p.Blocks[1]
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, lagos) // a Wednesday
	from, to := ChartSpan(*b.Chart, now)
	if from.Format("2006-01-02") != "2026-11-02" || to.Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("span: %s to %s", from, to)
	}
	rows := []map[string]any{
		{"spent_on": "2026-11-03", "amount_kobo": float64(250000)},
		{"spent_on": "2026-11-08", "amount_kobo": float64(100000)}, // a Sunday: still the week of 2 Nov
		{"spent_on": "2026-11-17", "amount_kobo": "450000"},
		{"spent_on": "2026-11-24", "amount_kobo": float64(120000)},
		{"spent_on": "2026-10-01", "amount_kobo": float64(999999)}, // before the span: left out
		{"spent_on": nil, "amount_kobo": float64(5)},
	}
	c := ChartFromRows(b, expenses, rows, now, false)
	var got []string
	for _, pt := range c.Points {
		got = append(got, pt.Label+"="+pt.Shown)
	}
	if strings.Join(got, " ") != "2 Nov=₦3,500 9 Nov=₦0 16 Nov=₦4,500 23 Nov=₦1,200" {
		t.Errorf("points: %v", got)
	}
	if c.LabelHead != "Week" || c.ValueHead != "Amount" || c.Points[3].Long != "Week of 23 November" {
		t.Errorf("heads: %+v", c)
	}
	if c.Zero != 0 || c.Points[2].Share != 0.75 || c.Ticks[len(c.Ticks)-1].Shown != "₦6k" || c.Ticks[0].Shown != "₦0" {
		t.Errorf("scale: zero %v, ticks %+v, points %+v", c.Zero, c.Ticks, c.Points)
	}
	if !strings.Contains(c.Summary, "Highest ₦4,500 (Week of 16 November); latest ₦1,200 (Week of 23 November)") {
		t.Errorf("summary: %s", c.Summary)
	}
}

func TestChartByGroupAndTool(t *testing.T) {
	p := parseOK(t, g4Page)
	var rows []map[string]any
	for i := 0; i < 14; i++ {
		rows = append(rows, map[string]any{"kind": string(rune('a' + i))})
	}
	rows = append(rows, map[string]any{"kind": "fuel"}, map[string]any{"kind": "Fuel"}, map[string]any{"kind": "fuel"})
	c := ChartFromRows(p.Blocks[2], expenses, rows, time.Now(), true)
	if len(c.Points) != MaxChartPoints || c.Points[0].Label != "Fuel" || c.Points[0].Value != 2 || c.Points[11].Label != "Others" || c.Over == "" {
		t.Errorf("by kind: %+v", c.Points)
	}
	if c.LabelHead != "Kind" || c.ValueHead != "How many" {
		t.Errorf("heads: %s %s", c.LabelHead, c.ValueHead)
	}

	tb := Block{ID: "c", Type: "chart", Source: &Source{Tool: "spend-by", Output: "rows"}, Chart: &Chart{Kind: "line", Label: "=item.week", Value: "=item.total"}}
	out := map[string]any{"rows": []any{
		map[string]any{"week": "W1", "total": 3.5}, map[string]any{"week": "W2", "total": "x"}, map[string]any{"week": "W3", "total": -2.0},
	}}
	tc := ChartFromTool(tb, out, Values{})
	if len(tc.Points) != 2 || tc.Points[1].Label != "W3" || tc.Zero <= 0 || tc.Ticks[0].Value >= 0 {
		t.Errorf("tool: %+v", tc)
	}
}

func TestBoardLanes(t *testing.T) {
	p := parseOK(t, g4Page)
	b := p.Blocks[0]
	if LaneOf(b, " Doing ") != "doing" || LaneOf(b, "gone") != "" {
		t.Errorf("lanes")
	}
	if b.Board.Columns[1].LabelOf() != "Doing" {
		t.Errorf("label: %s", b.Board.Columns[1].LabelOf())
	}
	act := RowAct(tickets, map[string]any{"id": float64(4), "status": "Open", "title": "Jam"}, Values{})
	if CardLane(b, act) != "open" {
		t.Errorf("card lane: %s", CardLane(b, act))
	}
	act["to"] = "doing"
	if got := ActionValues(b, b.Board.Move.Inputs, act); got["status"] != "doing" || got["ref"] != "4" {
		t.Errorf("move values: %v", got)
	}
	rows := Shape(b, tickets, []map[string]any{{"id": float64(4), "status": "open", "title": "Jam", "customer": "Bayo"}}, Values{})
	if rows[0].Title != "Jam" || rows[0].Sub != "Bayo" || rows[0].Detail == nil {
		t.Errorf("card: %+v", rows[0])
	}
	if f := LaneFilter(b, "do_ing%").(map[string]any); f["value"] != `do\_ing\%` || f["op"] != "ilike" {
		t.Errorf("lane filter: %v", f)
	}
}
