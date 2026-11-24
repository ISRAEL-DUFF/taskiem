package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// A value for a filter hidden from a viewer (a choice on a personal column,
// a page published before Check refused one) is let go before anything
// reads it; a viewer with table.read keeps it (review of H5).
func TestVisibleFilters(t *testing.T) {
	p := parseOK(t, `{"page": "page/v1", "name": "desk", "title": "Desk",
	  "filters": [{"id": "ph", "type": "choice", "column": "phone", "options": [{"value": "2348012345678"}]}, {"id": "q", "type": "search"}],
	  "blocks": [{"id": "list", "type": "list", "source": {"table": "tickets", "uses": ["ph", "q"]}, "item": {"title": "=row.title"}}]}`)
	set := map[string]string{"ph": "2348012345678", "q": "jam"}
	if got := p.VisibleFilters(set, []datatables.Table{tickets}, false); len(got) != 1 || got["q"] != "jam" {
		t.Errorf("a viewer without table.read: %v", got)
	}
	if got := p.VisibleFilters(set, []datatables.Table{tickets}, true); len(got) != 2 {
		t.Errorf("a viewer with table.read: %v", got)
	}
}

// A row's own status (build-plan-gaps H5, g-pages-rowstatus): a list's or
// a table's rows each name a run of a workflow, by a column's run number,
// by what it waits for, or by the page's address.
func TestRowStatus(t *testing.T) {
	doc := `{"page": "page/v1", "name": "desk", "title": "Desk",
	  "blocks": [
	    {"id": "list", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"},
	     "status": {"workflow": "Deliveries", "to": "=row.title"}},
	    {"id": "all", "type": "table", "source": {"table": "tickets"}, "columns": [{"column": "title"}],
	     "status": {"workflow": "Deliveries", "run": "=row.customer"}},
	    {"id": "page", "type": "table", "source": {"table": "tickets"}, "columns": [{"column": "title"}],
	     "status": {"workflow": "Deliveries"}}
	  ]}`
	if msgs := schema.Validate([]byte(doc)); len(msgs) > 0 {
		t.Fatalf("schema: %v", msgs)
	}
	p := parseOK(t, doc)
	if ps := Check(p, g2Env()); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	rows := []map[string]any{{"id": 7, "title": "AD-12", "customer": "9b2f6c1e-1111-4222-8333-944455556666", "phone": "2348012345678"}}
	v := Values{Viewer: Viewer{}, Now: time.Now()}
	list := Shape(p.Blocks[0], ticketsDone, rows, v)
	if list[0].RunTo != "AD-12" || list[0].RunID != "" {
		t.Errorf("to: %+v", list[0])
	}
	tbl := Shape(p.Blocks[1], ticketsDone, rows, v)
	if tbl[0].RunID != "9b2f6c1e-1111-4222-8333-944455556666" {
		t.Errorf("run: %+v", tbl[0])
	}

	for _, bad := range []struct{ name, doc, path, says string }{
		{"no workflow", `{"id": "l", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"}, "status": {"workflow": " "}}`, "/blocks/0/status/workflow", "say which workflow"},
		{"both", `{"id": "l", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"}, "status": {"workflow": "Deliveries", "run": "=row.title", "to": "=row.title"}}`, "/blocks/0/status", "not both"},
		{"reads steps", `{"id": "l", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"}, "status": {"workflow": "Deliveries", "to": "=steps.x"}}`, "/blocks/0/status/to", ""},
		{"on a figure", `{"id": "f", "type": "figure", "label": "N", "value": "count", "source": {"table": "tickets"}, "status": {"workflow": "Deliveries"}}`, "/blocks/0/status", "Only a list's or a table's"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			_, ps := Parse([]byte(`{"page": "page/v1", "name": "desk", "title": "Desk", "blocks": [` + bad.doc + `]}`))
			found := false
			for _, pr := range ps {
				if pr.Path == bad.path && strings.Contains(pr.Message, bad.says) {
					found = true
				}
			}
			if !found {
				t.Errorf("want %s %q, got %v", bad.path, bad.says, ps)
			}
		})
	}
	// Check: a workflow the business lacks, a column the table lacks.
	q := parseOK(t, `{"page": "page/v1", "name": "desk", "title": "Desk", "blocks": [
	  {"id": "l", "type": "list", "source": {"table": "tickets"}, "item": {"title": "=row.title"}, "status": {"workflow": "Nope", "to": "=row.colour"}}]}`)
	got := Check(q, Env{Tables: []datatables.Table{ticketsDone}, Workflow: func(n string) bool { return n == "Deliveries" }})
	var paths []string
	for _, pr := range got {
		paths = append(paths, pr.Path)
	}
	if j := strings.Join(paths, " "); !strings.Contains(j, "/blocks/0/status/workflow") || !strings.Contains(j, "/blocks/0/status/to") {
		t.Errorf("check: %v", got)
	}
}
