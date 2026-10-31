package builder_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/templates"
)

// Decision 0060: a business with no Google Sheets is offered the table
// twin, never the sheet template, and a draft made from a template makes
// the tables the template declares, with its own columns (personal
// markings included).
func TestNoSheetsTwinAndItsTables(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{templateAnswer(t, "customer-welcome-table", map[string]any{"business_name": "Femi Fitness"})}}
	b := &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
	p, err := b.Build(context.Background(), builder.Request{Goal: "welcome new customers when they sign up, my business is Femi Fitness",
		Context: builder.Context{Holds: map[string]bool{templates.WhenNoSheets: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range p.TemplatesOffered {
		if id == "new-customer-welcome" {
			t.Errorf("the sheet template was offered to a business without Google Sheets: %v", p.TemplatesOffered)
		}
	}
	if len(p.TemplatesOffered) == 0 || p.TemplatesOffered[0] != "customer-welcome-table" {
		t.Fatalf("offered: %v", p.TemplatesOffered)
	}
	if !p.Valid() || p.Template == nil || p.Template.ID != "customer-welcome-table" {
		t.Fatalf("valid %v template %+v problems %v", p.Valid(), p.Template, p.Problems)
	}
	if len(p.Tables) != 1 || p.Tables[0].Name != "customers" {
		t.Fatalf("tables: %+v", p.Tables)
	}
	name := p.Tables[0].Columns[0]
	if name.Name != "name" || name.Personal == nil || !*name.Personal {
		t.Errorf("the name column keeps its personal marking: %+v", name)
	}

	// The same draft for a business whose customers table exists without
	// a phone column: nothing is proposed, and what it lacks is said.
	f = &ai.Fake{Script: []ai.Response{templateAnswer(t, "customer-welcome-table", map[string]any{"business_name": "Femi Fitness"})}}
	b = &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
	p, err = b.Build(context.Background(), builder.Request{Goal: "welcome new customers when they sign up, my business is Femi Fitness",
		Context: builder.Context{Holds: map[string]bool{templates.WhenNoSheets: true},
			Tables: []builder.DataTable{{Name: "customers", Columns: []builder.DataTableColumn{{Name: "name", Type: "text"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tables) != 0 {
		t.Errorf("an existing table is proposed again: %+v", p.Tables)
	}
	said := false
	for _, w := range p.Warnings {
		said = said || (w.Kind == "table" && strings.Contains(w.Message, `"customers" exists already without the column(s) phone`))
	}
	if !said {
		t.Errorf("warnings: %+v", p.Warnings)
	}
}
