package templates_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/templates"
)

// One replacement is read and written as an object, as every template had
// it before decision 0060, so a template's pinned JSON does not change;
// several are a list.
func TestReplacementsJSON(t *testing.T) {
	var one templates.Replacements
	if err := json.Unmarshal([]byte(`{"by": "a", "when": "no_sheets"}`), &one); err != nil || len(one) != 1 || one[0].By != "a" {
		t.Fatalf("object: %v %v", one, err)
	}
	if raw, _ := json.Marshal(one); string(raw) != `{"by":"a","when":"no_sheets"}` {
		t.Errorf("one is written as %s", raw)
	}
	var two templates.Replacements
	if err := json.Unmarshal([]byte(`[{"by": "a", "when": "keeps_stock"}, {"by": "b", "when": "no_sheets"}]`), &two); err != nil || len(two) != 2 {
		t.Fatalf("list: %v %v", two, err)
	}
	if raw, _ := json.Marshal(two); string(raw) != `[{"by":"a","when":"keeps_stock"},{"by":"b","when":"no_sheets"}]` {
		t.Errorf("two are written as %s", raw)
	}
	if two.By(map[string]bool{"no_sheets": true}) != "b" || two.By(map[string]bool{}) != "" {
		t.Error("By picks the replacement whose condition holds")
	}
	var bad templates.Replacements
	if err := json.Unmarshal([]byte(`{"by": "a", "when": "x", "extra": 1}`), &bad); err == nil {
		t.Error("an unknown field is refused")
	}
}

// Every sheet template has a twin for a business without Google Sheets,
// except the debtor reminders (R5) and the one already superseded.
func TestEverySheetTemplateHasATwin(t *testing.T) {
	lib := templates.Default()
	noSheets := map[string]bool{templates.WhenNoSheets: true, templates.WhenNoSheetsStock: true}
	waiting := []string{"debtor-reminder-sms", "debtor-reminder-whatsapp"}
	for _, tpl := range lib.Offered() {
		if !slices.Contains(tpl.Connectors, "googlesheets@1") || slices.Contains(waiting, tpl.ID) {
			continue
		}
		by := tpl.ReplacedWhere.By(noSheets)
		if by == "" {
			t.Errorf("%s uses Google Sheets and has no twin for a business without it", tpl.ID)
			continue
		}
		twin, _ := lib.Get(by)
		if slices.Contains(twin.Connectors, "googlesheets@1") {
			t.Errorf("%s's twin %s uses Google Sheets too", tpl.ID, by)
		}
	}
}

func ids(list []*templates.Template) []string {
	var out []string
	for _, t := range list {
		out = append(out, t.ID)
	}
	return out
}

// The gallery leaves a replaced template out, and lists a twin just before
// the template it replaces where both are offered.
func TestForNoSheets(t *testing.T) {
	lib := templates.Default()
	none := ids(templates.For(lib.Offered(), map[string]bool{templates.WhenNoSheets: true}))
	for _, id := range []string{"new-customer-welcome", "sales-ledger-sheet", "transfer-received-notify"} {
		if slices.Contains(none, id) {
			t.Errorf("%s is offered to a business without Google Sheets", id)
		}
	}
	for _, id := range []string{"customer-welcome-table", "payment-received-tell-owner", "daily-sales-summary"} {
		if !slices.Contains(none, id) {
			t.Errorf("%s is not offered to a business without Google Sheets", id)
		}
	}
	if stock := ids(templates.For(lib.Offered(), map[string]bool{templates.WhenNoSheets: true, templates.WhenNoSheetsStock: true})); slices.Contains(stock, "daily-sales-summary") ||
		slices.Contains(stock, "stock-reorder-alert") {
		t.Error("the stock sheet templates are offered where stock is on and there is no sheet")
	}
	both := ids(templates.For(lib.Offered(), map[string]bool{}))
	tw, sh := slices.Index(both, "customer-welcome-table"), slices.Index(both, "new-customer-welcome")
	if tw < 0 || sh < 0 || tw != sh-1 {
		t.Errorf("with Sheets connected the twin is listed just before the sheet template: at %d and %d", tw, sh)
	}
	if len(both) != len(lib.Offered()) {
		t.Errorf("with no condition every template is offered once: %d of %d", len(both), len(lib.Offered()))
	}
}

// Retrieval offers the twin, never the sheet template, where no_sheets
// holds, and ranks the twin first where both are offered unless the goal
// names a sheet.
func TestMatchForNoSheets(t *testing.T) {
	lib := templates.Default()
	first := func(goal string, holds map[string]bool) []string {
		var out []string
		for _, m := range lib.MatchFor(goal, 2, holds) {
			out = append(out, m.Template.ID)
		}
		return out
	}
	noSheets := map[string]bool{templates.WhenNoSheets: true}
	for goal, want := range map[string]string{
		"welcome new customers when they sign up":            "customer-welcome-table",
		"tell me when a customer pays and log it in a sheet": "payment-received-tell-owner",
		"record every Paystack payment in my sales ledger":   "payment-received-tell-owner",
	} {
		got := first(goal, noSheets)
		if len(got) == 0 || got[0] != want {
			t.Errorf("%q without Sheets: got %v, want %s first", goal, got, want)
		}
		for _, id := range got {
			if tpl, _ := lib.Get(id); slices.Contains(tpl.Connectors, "googlesheets@1") {
				t.Errorf("%q without Sheets offered %s", goal, id)
			}
		}
	}
	if got := first("welcome new customers when they sign up", map[string]bool{}); len(got) == 0 || got[0] != "customer-welcome-table" {
		t.Errorf("with Sheets the twin ranks first: got %v", got)
	}
	if got := first("welcome new customers and add them to my Google Sheet", map[string]bool{}); len(got) == 0 || got[0] != "new-customer-welcome" {
		t.Errorf("a goal naming a sheet gets the sheet template first: got %v", got)
	}
}

// Known tables are defined once and fill the tables that name them.
func TestKnownTables(t *testing.T) {
	names := []string{}
	for _, kt := range templates.KnownTables() {
		names = append(names, kt.Name)
	}
	if !reflect.DeepEqual(names, []string{"customers", "staff"}) {
		t.Errorf("known tables: %v", names)
	}
	staff, _ := templates.KnownTable("staff")
	payroll, _ := templates.Default().Get("run-payroll")
	if got := payroll.Tables[0]; got.Name != "{{staff_table}}" || got.Title != staff.Title || !reflect.DeepEqual(got.Columns, staff.Columns) || got.Known != "" {
		t.Errorf("run-payroll's staff table is the known one, resolved: %+v", got)
	}
	welcome, _ := templates.Default().Get("customer-welcome-table")
	cust, _ := templates.KnownTable("customers")
	tb := welcome.TablesFor(map[string]any{})
	if len(tb) != 1 || tb[0].Name != "customers" || !reflect.DeepEqual(tb[0].Columns, cust.Columns) {
		t.Fatalf("customer-welcome-table's table: %+v", tb)
	}
	if p := tb[0].Columns[0].Personal; tb[0].Columns[0].Name != "name" || p == nil || !*p {
		t.Error("a customer's name is personal")
	}
	if _, err := templates.Parse([]byte(`{"id": "x", "title": "x", "summary": "x", "description": "x", "category": "x", "connectors": [], "params": [],
		"tables": [{"known": "nope", "name": "t"}], "definition": {"schema": "wd/v1"}}`)); err == nil {
		t.Error("a table naming no known table is refused")
	}
}
