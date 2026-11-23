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
	if got := Missing(p, []datatables.Table{tickets}); len(got) != 0 {
		t.Errorf("nothing missing: %v", got)
	}
	thin := datatables.Table{Name: "tickets", Title: "Tickets", Columns: []datatables.Column{{Name: "title", Type: "text"}, {Name: "status", Type: "text"}}}
	got := strings.Join(Missing(p, []datatables.Table{thin}), " ")
	if !strings.Contains(got, "The table Tickets has no columns") || !strings.Contains(got, "amount_kobo") || !strings.Contains(got, "customer") {
		t.Errorf("columns: %s", got)
	}
	if got := Missing(p, nil); len(got) != 1 || got[0] != "This business has no table called tickets." {
		t.Errorf("tables: %v", got)
	}
}
