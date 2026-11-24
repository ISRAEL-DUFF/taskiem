package pages

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/wd"
)

// starterEnv is what a starter's page is checked against once installed:
// its own tables and tools (a recipe's as the library makes it, an input
// for each of its columns).
func starterEnv(s Starter) Env {
	var tables []datatables.Table
	for _, t := range s.Tables {
		tables = append(tables, t.Table())
	}
	return Env{Tables: tables, Tool: func(c string) (ToolInfo, bool) {
		for _, spec := range s.Tools {
			if spec.Command != c {
				continue
			}
			if spec.Kind == "recipe" {
				return ToolInfo{Name: spec.Name, Care: "yes", Inputs: []string{"what", "amount", "note"}, Outputs: map[string]string{"what": "string"}}, true
			}
			t, _ := s.TableNamed(spec.Table)
			return spec.Info(t), true
		}
		return ToolInfo{}, false
	}}
}

// G5: the seven starters the plan names, each a page that reads and checks
// against the tables and tools it brings, with tools that are workflows
// Taskiem can load, and the parts that fit it (a board for tickets, a chart
// by week for expenses).
func TestStarters(t *testing.T) {
	list := Starters()
	var names []string
	for _, s := range list {
		names = append(names, s.Title)
	}
	if got := strings.Join(names, ", "); got != "Tickets desk, Expenses this month, Leave, Stock count, Deliveries today, Waiting list, Service log" {
		t.Fatalf("starters: %s", got)
	}
	kinds := map[string][]string{}
	for _, s := range list {
		page, ps := Parse(s.Page)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", s.ID, ps)
		}
		if ps := Check(page, starterEnv(s)); len(ps) > 0 {
			t.Errorf("%s: %v", s.ID, ps)
		}
		for _, spec := range s.Tools {
			if ReservedName(spec.Command) {
				t.Errorf("%s: /%s is Taskiem's own word", s.ID, spec.Command)
			}
		}
		if len(s.Tables) == 0 || len(s.Tools) == 0 || s.About == "" || s.Icon == "" {
			t.Errorf("%s brings too little: %+v", s.ID, s)
		}
		for _, b := range page.Blocks {
			kinds[s.ID] = append(kinds[s.ID], b.Type)
		}
		for _, c := range page.ToolsPressed() {
			if _, ok := starterEnv(s).Tool(c); !ok {
				t.Errorf("%s presses /%s, which it does not bring", s.ID, c)
			}
		}
		for _, spec := range s.Tools {
			if spec.Kind == "recipe" {
				continue
			}
			tbl, _ := s.TableNamed(spec.Table)
			raw, err := spec.Definition(tbl)
			if err != nil {
				t.Fatalf("%s /%s: %v", s.ID, spec.Command, err)
			}
			d, err := wd.Load(raw)
			if err != nil {
				t.Fatalf("%s /%s: %v\n%s", s.ID, spec.Command, err, raw)
			}
			if d.Tool == nil || d.Tool.Command != spec.Command {
				t.Errorf("%s /%s: %s", s.ID, spec.Command, raw)
			}
		}
		if w := strings.Join(s.Words(), "\n"); !strings.Contains(w, "The table ") || !strings.Contains(w, "/"+s.Tools[0].Command) {
			t.Errorf("%s words: %s", s.ID, w)
		}
	}
	if !strings.Contains(strings.Join(kinds["tickets-desk"], ","), "board") || !strings.Contains(strings.Join(kinds["expenses-month"], ","), "chart") {
		t.Errorf("parts: %v", kinds)
	}
	exp, _ := StarterNamed("expenses-month")
	page, _ := Parse(exp.Page)
	for _, b := range page.Blocks {
		if b.Type == "chart" && (b.Chart.Period != "week" || b.Chart.By != "created_at") {
			t.Errorf("expenses by week: %+v", b.Chart)
		}
	}
	// A copy is the caller's own.
	a, _ := StarterNamed("tickets-desk")
	a.Tools[0].Command = "changed"
	if b, _ := StarterNamed("tickets-desk"); b.Tools[0].Command != "new-ticket" {
		t.Error("a starter was changed through a copy")
	}
}

func TestToolSpecRefusals(t *testing.T) {
	tbl := TableSpec{Name: "tickets", Title: "Tickets", Columns: []ColumnSpec{{Name: "title", Type: "text", Required: true}, {Name: "status", Type: "text"}}}
	for spec, want := range map[*ToolSpec]string{
		{Command: "x", Name: "X", Kind: "add", Table: "tickets"}:                                          "2 to 24 small letters",
		{Command: "new-ticket", Name: "", Kind: "add", Table: "tickets"}:                                  "needs a name",
		{Command: "new-ticket", Name: "New", Kind: "delete", Table: "tickets"}:                            "adds a row or changes one",
		{Command: "new-ticket", Name: "New", Kind: "add", Table: "jobs"}:                                  "not there",
		{Command: "new-ticket", Name: "New", Kind: "add", Table: "tickets", Asks: []string{"nope"}}:       "does not have",
		{Command: "new-ticket", Name: "New", Kind: "add", Table: "tickets", Sets: map[string]any{"x": 1}}: "sets x",
		{Command: "move-ticket", Name: "Move", Kind: "change", Table: "tickets"}:                          "say what it changes",
	} {
		if got := spec.Check(tbl); !strings.Contains(got, want) {
			t.Errorf("%+v: %q, want %q", spec, got, want)
		}
	}
	// A change asks for the row by its number and the new value; an add
	// leaves out what it sets, and an optional column may be left empty.
	move := ToolSpec{Command: "move-ticket", Name: "Move", Kind: "change", Table: "tickets", Asks: []string{"status"}}
	raw, err := move.Definition(tbl)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); !strings.Contains(s, `"key":"=trigger.body.ref"`) || !strings.Contains(s, `"required":["ref","status"]`) || !strings.Contains(s, `"id":"wf_moveTicket"`) {
		t.Errorf("change: %s", s)
	}
	add := ToolSpec{Command: "new-ticket", Name: "New", Kind: "add", Table: "tickets", Sets: map[string]any{"status": "=open"}}
	raw, _ = add.Definition(tbl)
	var def map[string]any
	_ = json.Unmarshal(raw, &def)
	row := def["steps"].([]any)[0].(map[string]any)["input"].(map[string]any)["row"].(map[string]any)
	if row["status"] != `="=open"` || row["title"] != "=trigger.body.title" {
		t.Errorf("add: %v", row)
	}
	if info := add.Info(tbl); info.Care != "yes" || strings.Join(info.Inputs, ",") != "title" {
		t.Errorf("info: %+v", info)
	}
}

// Saved things of one kind as a source: the kind's fields as columns, a
// sensitive one personal; things in the order their date comes, with when
// in words and a tone; within keeps the near and the late; a search reads
// what is not private; and what may not be done with saved things refused.
func TestSavedSource(t *testing.T) {
	kind := SavedKind{Name: "Generator", Fields: []SavedField{
		{Key: "size", Label: "Size", Type: "text"},
		{Key: "next_service", Label: "Next service", Type: "date"},
		{Key: "serial", Label: "Serial", Type: "text", Sensitive: true},
		{Key: "bought", Label: "Bought", Type: "date", Sensitive: true},
		{Key: "manual", Label: "Manual", Type: "file"},
	}}
	tbl := kind.Table()
	if tbl.Name != "saved:generator" || len(tbl.Columns) != 7 {
		t.Fatalf("table: %+v", tbl)
	}
	if c, _ := columnOf(tbl, "serial"); !c.Personal {
		t.Errorf("a sensitive field is personal: %+v", c)
	}
	env := Env{SavedKind: func(n string) (SavedKind, bool) { return kind, strings.EqualFold(n, "generator") },
		Tool: func(c string) (ToolInfo, bool) {
			return ToolInfo{Name: "Log a service", Care: "yes", Inputs: []string{"what"}}, c == "log-service"
		}}
	doc := `{"page":"page/v1","name":"gens","title":"Generators","filters":[{"id":"q","type":"search"}],"blocks":[
	 {"id":"soon","type":"figure","label":"Due within a week","value":"count","source":{"saved":"Generator","coming":"next_service","within":7}},
	 {"id":"list","type":"list","title":"Generators","source":{"saved":"Generator","coming":"next_service","uses":["q"]},
	  "item":{"title":"=row.title","sub":"=row.size + ' · ' + row.serial","state":{"text":"=row.coming"}},
	  "detail":{"title":"=row.title","lines":[{"label":"Next service","value":"=row.next_service"}]},
	  "actions":[{"label":"Log a service","tool":"log-service","inputs":{"what":"=row.title"}}]}]}`
	page, ps := Parse([]byte(doc))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if ps := Check(page, env); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	things := []SavedThing{
		{ID: "a", Name: "big-gen", Title: "Big gen", Values: map[string]string{"size": "60kVA", "next_service": "2026-12-20", "serial": "••3456"}},
		{ID: "b", Name: "small-gen", Title: "Small gen", Values: map[string]string{"size": "5kVA", "next_service": "2026-11-26"}},
		{ID: "c", Name: "old-gen", Title: "Old gen", Values: map[string]string{"next_service": "2026-11-20"}},
		{ID: "d", Name: "spare", Title: "Spare", Values: map[string]string{}},
	}
	v := Values{Set: map[string]string{}, Now: now}
	read := page.ReadSaved(page.Blocks[1], kind, things, v)
	shown := ShapeSaved(page.Blocks[1], kind, read, v)
	var got []string
	for _, s := range shown {
		tone := ""
		if s.State != nil {
			tone = s.State.Text + "/" + s.State.Tone
		}
		got = append(got, s.ID+":"+s.Title+":"+tone)
	}
	if strings.Join(got, " | ") != "c:Old gen:4 days ago/bad | b:Small gen:In 2 days/warn | a:Big gen:In 26 days/plain | d:Spare:" {
		t.Errorf("in the order they come: %v", got)
	}
	if shown[2].Sub != "60kVA · ••3456" || shown[2].Detail.Lines[0].Value != "20 Dec 2026" || shown[2].Actions[0].Tool != "log-service" {
		t.Errorf("a row: %+v", shown[2])
	}
	if n := len(page.ReadSaved(page.Blocks[0], kind, things, v).Rows); n != 2 {
		t.Errorf("within a week, and the late: %d", n)
	}
	v.Set["q"] = "small"
	if r := page.ReadSaved(page.Blocks[1], kind, things, v); len(r.Rows) != 1 || r.Rows[0]["id"] != "b" {
		t.Errorf("search: %v", r.Rows)
	}
	v.Set["q"] = "3456"
	if r := page.ReadSaved(page.Blocks[1], kind, things, v); len(r.Rows) != 0 {
		t.Errorf("a search never reads what is private: %v", r.Rows)
	}
	if w, tone := ComingWords(90, time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)); w != "On 22 Feb 2027" || tone != "plain" {
		t.Errorf("far: %s %s", w, tone)
	}

	// Refused: a kind the business lacks, a date that is private or not a
	// date, conditions, a sum, a board, a choice filter, within alone.
	for doc, want := range map[string]string{
		`{"id":"x","type":"list","source":{"saved":"Car"},"item":{"title":"=row.title"}}`:                                                                          "has not made",
		`{"id":"x","type":"list","source":{"saved":"Generator","coming":"bought"},"item":{"title":"=row.title"}}`:                                                  "kept private",
		`{"id":"x","type":"list","source":{"saved":"Generator","coming":"size"},"item":{"title":"=row.title"}}`:                                                    "not a date",
		`{"id":"x","type":"list","source":{"saved":"Generator","coming":"nope"},"item":{"title":"=row.title"}}`:                                                    "do not have",
		`{"id":"x","type":"list","source":{"saved":"Generator","order":[{"column":"size"}]},"item":{"title":"=row.title"}}`:                                        "no conditions or order",
		`{"id":"x","type":"figure","label":"S","value":"sum","of":"size","source":{"saved":"Generator"}}`:                                                          "adds up nothing",
		`{"id":"x","type":"board","source":{"saved":"Generator"},"item":{"title":"=row.title"},"board":{"column":"size","columns":[{"value":"a"},{"value":"b"}]}}`: "not a board",
		`{"id":"x","type":"list","source":{"saved":"Generator","uses":["c"]},"item":{"title":"=row.title"}}`:                                                       "search only",
		`{"id":"x","type":"list","source":{"saved":"Generator","within":5},"item":{"title":"=row.title"}}`:                                                         "say which date",
		`{"id":"x","type":"list","source":{"table":"t","coming":"d"},"item":{"title":"=row.title"}}`:                                                               "only saved things",
		`{"id":"x","type":"list","source":{"saved":"Generator"},"item":{"title":"=row.title"},"group":{"column":"serial"}}`:                                        "personal",
	} {
		full := `{"page":"page/v1","name":"bad","title":"Bad","filters":[{"id":"c","type":"choice","column":"size","options":[{"value":"a"}]}],"blocks":[` + doc + `]}`
		p, ps := Parse([]byte(full))
		if p != nil && len(ps) == 0 {
			ps = Check(p, env)
		}
		all := ""
		for _, x := range ps {
			all += x.Message + " "
		}
		if !strings.Contains(all, want) {
			t.Errorf("%s: %q, want %q", doc, all, want)
		}
	}
	if s := Shows(page, nil); !strings.Contains(strings.Join(s, ";"), "Saved Generator things") {
		t.Errorf("shows: %v", s)
	}
}
