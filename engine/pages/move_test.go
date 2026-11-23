package pages

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/datatables"
)

func TestChanges(t *testing.T) {
	v1 := parseOK(t, good)
	if got := strings.Join(Changes(nil, v1), " "); got != "First published, with 6 parts. Opened by owners and support, manager." {
		t.Errorf("first: %s", got)
	}
	v2 := parseOK(t, good)
	v2.Blocks = append(v2.Blocks[:2], v2.Blocks[3:]...) // "Owed" taken away
	v2.Blocks = append(v2.Blocks, Block{ID: "late", Type: "heading", Title: "Late ones"})
	v2.Blocks[0].Text = "Changed words"
	v2.Roles = nil
	got := strings.Join(Changes(v1, v2), " ")
	for _, want := range []string{`Added "Late ones".`, `Took away "Owed".`, `Changed "Support desk".`, "Now opened by everyone in the business."} {
		if !strings.Contains(got, want) {
			t.Errorf("changes lack %q: %s", want, got)
		}
	}
	if got := Changes(v1, parseOK(t, good)); len(got) != 1 || got[0] != "Published again, unchanged." {
		t.Errorf("same: %v", got)
	}
}

func TestForExport(t *testing.T) {
	p := parseOK(t, good)
	p.Blocks[1].Source.Filters = append(p.Blocks[1].Source.Filters,
		Condition{Column: "phone", Op: "eq", Value: "2348012345678"}, Condition{Column: "phone", Op: "not_null"})
	out, left := ForExport(p, []datatables.Table{tickets})
	if left != 1 {
		t.Errorf("left out %d", left)
	}
	doc := string(out.Marshal())
	if strings.Contains(doc, "2348012345678") || !strings.Contains(doc, "not_null") {
		t.Errorf("export: %s", doc)
	}
	if !strings.Contains(string(p.Marshal()), "2348012345678") {
		t.Errorf("the page itself was changed")
	}
	if _, ps := Parse(out.Marshal()); len(ps) > 0 {
		t.Errorf("the export does not read back: %v", ps)
	}
}

func TestMissing(t *testing.T) {
	p := parseOK(t, good)
	if got := Missing(p, []datatables.Table{tickets}, nil); len(got) != 0 {
		t.Errorf("nothing missing: %v", got)
	}
	thin := datatables.Table{Name: "tickets", Title: "Tickets", Columns: []datatables.Column{{Name: "title", Type: "text"}, {Name: "status", Type: "text"}}}
	got := strings.Join(Missing(p, []datatables.Table{thin}, nil), " ")
	if !strings.Contains(got, "The table Tickets has no columns") || !strings.Contains(got, "amount_kobo") || !strings.Contains(got, "customer") {
		t.Errorf("columns: %s", got)
	}
	if got := Missing(p, nil, nil); len(got) != 1 || got[0] != "This business has no table called tickets." {
		t.Errorf("tables: %v", got)
	}
}

// Nothing personal leaves in a file (review of the export): a gone table's
// condition values, a choice filter's options on a personal column, a
// group order and state colours by one, and words of an expression's own
// beside one; each counted.
func TestForExportLeavesNothingPersonal(t *testing.T) {
	staff := datatables.Table{Name: "staff", Title: "Staff", Columns: []datatables.Column{
		{Name: "name", Type: "text", Personal: true}, {Name: "team", Type: "text"}, {Name: "phone", Type: "phone", Personal: true}}}
	doc := `{"page": "page/v1", "name": "team", "title": "Team",
	  "filters": [{"id": "who", "type": "choice", "column": "name", "options": [{"value": "Ada Obi"}, {"value": "Bayo Ade"}]}, {"id": "q", "type": "search"}],
	  "blocks": [
	    {"id": "l", "type": "list", "title": "People", "source": {"table": "staff", "uses": ["who", "q"]},
	     "item": {"title": "=row.name == 'Ada Obi' ? 'boss' : row.team", "sub": "=row.team + ' · ' + row.phone",
	              "state": {"text": "=row.name", "tones": {"Ada Obi": "good"}}},
	     "group": {"column": "name", "order": ["Ada Obi"]},
	     "detail": {"title": "=row.phone == '08031234567' ? 'me' : 'them'", "lines": [{"label": "Team", "value": "=row.team + ' (A)'"}]}},
	    {"id": "g", "type": "figure", "label": "Gone", "value": "count", "source": {"table": "gone", "filters": [{"column": "x", "op": "eq", "value": "Ada Obi"}, {"column": "y", "op": "not_null"}]}}
	  ]}`
	p, _ := Parse([]byte(doc))
	if p == nil {
		t.Fatal("not read")
	}
	out, left := ForExport(p, []datatables.Table{staff})
	raw := string(out.Marshal())
	for _, leak := range []string{"Ada Obi", "Bayo Ade", "08031234567"} {
		if strings.Contains(raw, leak) {
			t.Errorf("%q left in the file: %s", leak, raw)
		}
	}
	// 2 options, 1 group value, 1 tone, the title and the detail's title,
	// and the gone table's condition value.
	if left != 7 {
		t.Errorf("left out %d: %s", left, raw)
	}
	if !strings.Contains(raw, `"=row.team + ' · ' + row.phone"`) || !strings.Contains(raw, `"=row.team + ' (A)'"`) || !strings.Contains(raw, "not_null") {
		t.Errorf("too much left out: %s", raw)
	}
	if len(out.Filters) != 1 || len(out.Blocks[0].Source.Uses) != 1 {
		t.Errorf("the choice filter: %+v %+v", out.Filters, out.Blocks[0].Source.Uses)
	}
	if _, ps := Parse(out.Marshal()); len(ps) > 0 {
		t.Errorf("the export does not read back: %v", ps)
	}
}

// A choice filter no part uses is held to the tables the page reads too.
func TestUnusedChoiceFilterOnAPersonalColumn(t *testing.T) {
	p := parseOK(t, strings.Replace(good, `"uses": ["q", "st", "when", "mine"]`, `"uses": ["q", "when", "mine"]`, 1))
	p.Filters[1].Column = "phone"
	ps := Check(p, Env{Tables: []datatables.Table{tickets}})
	if len(ps) != 1 || ps[0].Path != "/filters/1/column" || !strings.Contains(ps[0].Message, "Phone: it is personal") {
		t.Errorf("problems: %v", ps)
	}
}
