package api

import (
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// The ticket kit chooses its tables by expression (the desk, or the
// reference's prefix): readiness finds each table a step can touch, and
// only those, so a ticket workflow published without its tables is
// flagged, and the customers' desk is not asked for HR's tables nor HR's
// for the customers'.
func TestTablesTheKitCanTouch(t *testing.T) {
	cases := map[string][]string{
		"helpdesk-web-form": {"ticket_messages", "tickets"},
		"helpdesk-email":    {"ticket_messages", "tickets"},
		"ticket-reply":      {"ticket_messages", "tickets"},
		"staff-it-desk":     {"ticket_messages", "tickets"},
		"staff-hr-desk":     {"hr_confidential_messages", "hr_confidential_tickets", "hr_ticket_messages", "hr_tickets"},
		"hr-ticket-reply":   {"hr_ticket_messages", "hr_tickets"},
	}
	for id, want := range cases {
		tpl, ok := templates.Default().Get(id)
		if !ok {
			t.Fatalf("no template %s", id)
		}
		doc, err := tpl.Instantiate(tpl.Examples(), "")
		if err != nil {
			t.Fatal(err)
		}
		d, err := wd.Load(doc)
		if err != nil {
			t.Fatal(err)
		}
		if d, err = expansion.ExpandDefinition(d, expansion.Library(templates.Default())); err != nil {
			t.Fatal(err)
		}
		var got []string
		d.Walk(func(st *wd.Step) {
			name, _ := st.Input["table"].(string)
			if st.Connection != "data_tables" || name == "" {
				return
			}
			names := []string{name}
			if expr.IsExpr(name) {
				names = tablesOf(readyExpr, d, st, name)
				if len(names) == 0 {
					t.Errorf("%s: step %s: no table found for %s", id, st.ID, name)
				}
			}
			for _, n := range names {
				if !slices.Contains(got, n) {
					got = append(got, n)
				}
			}
		})
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s touches %v, want %v", id, got, want)
		}
	}
}
