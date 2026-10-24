package builder

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

const leaveWF = `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave","trigger":{"type":"manual"},"steps":[
 {"id":"add","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","input":{"table":"leave","rows":[]}},
 {"id":"log","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","input":{"table":"audit_notes","rows":[]}},
 {"id":"each","type":"foreach","config":{"items":"=trigger.body.x","steps":[
   {"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"staff"}}]}},
 {"id":"dyn","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"=trigger.body.t"}},
 {"id":"other","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"own_project","input":{"table":"elsewhere"}}]}`

func TestTablesUsed(t *testing.T) {
	got := tablesUsed([]byte(leaveWF))
	if len(got) != 3 || !got["leave"] || !got["audit_notes"] || !got["staff"] {
		t.Fatalf("used: %v", got)
	}
}

func TestReviewTables(t *testing.T) {
	cols := func(names ...string) []ColumnSpec {
		var out []ColumnSpec
		for _, n := range names {
			out = append(out, ColumnSpec{Name: n, Title: n, Type: "text"})
		}
		return out
	}
	leave := TableSpec{Name: "leave", Title: "Leave", Columns: append(cols("name", "reason"), ColumnSpec{Name: "start_date", Type: "date", Required: true})}
	proposed := []TableSpec{
		leave,
		leave,                                  // twice: kept once
		{Name: "staff", Columns: cols("name")}, // exists: used as it is
		{Name: "audit_notes", Columns: cols("id", "note")}, // a reserved column
		{Name: "unused", Columns: cols("x")},               // not in the workflow
		{Name: "Bad Name", Columns: cols("x")},             // not used either: dropped quietly
	}
	existing := []DataTable{{Name: "staff", Columns: []DataTableColumn{{Name: "name", Type: "text"}}}}
	keep, warns := reviewTables(proposed, existing, []byte(leaveWF), nil)
	if len(keep) != 1 || keep[0].Name != "leave" {
		t.Fatalf("kept: %+v", keep)
	}
	msgs := ""
	for _, w := range warns {
		if w.Kind != "table" {
			t.Errorf("kind %q", w.Kind)
		}
		msgs += w.Message + "\n"
	}
	for _, want := range []string{`"staff" exists already`, `column name "id" is not allowed`, `uses a table "audit_notes" that does not exist`} {
		if !strings.Contains(msgs, want) {
			t.Errorf("no warning %q in:\n%s", want, msgs)
		}
	}
	// The platform's own check (the column limit, say) refuses too.
	_, warns = reviewTables([]TableSpec{leave}, nil, []byte(leaveWF), func(TableSpec) error { return errors.New("at most 2 columns") })
	if !strings.Contains(warns[0].Message, "at most 2 columns") {
		t.Fatalf("platform check: %+v", warns)
	}
	// Unknown types are refused before the platform is asked.
	bad := TableSpec{Name: "leave", Columns: []ColumnSpec{{Name: "days", Type: "float"}}}
	if _, warns = reviewTables([]TableSpec{bad}, nil, []byte(leaveWF), nil); !strings.Contains(warns[0].Message, `unknown type "float"`) {
		t.Fatalf("type: %+v", warns)
	}
}

// A missing table is described as the workflow uses it, typed from the
// form fields that fill it.
func TestTableFor(t *testing.T) {
	def, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave",
	 "trigger":{"type":"form","config":{"title":"Leave","access":"public"}},
	 "inputs":{"schema":{"type":"object","properties":{"name":{"type":"string"},"phone":{"type":"string","x-pii":"phone"},
	   "start_date":{"type":"string","format":"date"},"days":{"type":"integer"}}}},
	 "steps":[
	  {"id":"save","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables",
	   "input":{"table":"leave_requests","row":{"reference":"=trigger.form.reference","name":"=trigger.body.name","phone":"=trigger.body.phone",
	     "start_date":"=trigger.body.start_date","days":"=trigger.body.days","status":"pending","id":"x"}}},
	  {"id":"mark","type":"connector","connector":"xenibase@1","action":"update_rows","connection":"data_tables","needs":["save"],
	   "input":{"table":"leave_requests","filters":[{"column":"reference","op":"eq","value":"=trigger.form.reference"}],"set":{"approved":true}}},
	  {"id":"other","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","needs":["save"],
	   "input":{"table":"audit_log","row":{"what":"leave"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := TableFor(def, "leave_requests")
	want := "approved:boolean days:integer name:text phone:phone reference:text start_date:date status:text"
	var cols []string
	for _, c := range got.Columns {
		cols = append(cols, c.Name+":"+c.Type)
	}
	slices.Sort(cols)
	if strings.Join(cols, " ") != want || got.Title != "Leave requests" {
		t.Fatalf("got %q %v, want %q", got.Title, cols, want)
	}
	if err := checkTableShape(got); err != nil {
		t.Fatal(err)
	}
}

// A draft writing a table Taskiem keeps is warned about; one reading it
// is not.
func TestReviewTablesWarnsOfKeptWrites(t *testing.T) {
	existing := []DataTable{{Name: "leave", Kept: true}, {Name: "staff", Kept: true}, {Name: "audit_notes"}}
	_, warns := reviewTables(nil, existing, []byte(leaveWF), nil)
	var kept []string
	for _, w := range warns {
		if strings.Contains(w.Message, "which Taskiem keeps") {
			kept = append(kept, w.Message)
		}
	}
	if len(kept) != 1 || !strings.Contains(kept[0], `"leave"`) {
		t.Fatalf("warnings %v", warns)
	}
}

// The money log's tables are never proposed from a draft, and a draft
// reading them before they exist is told Taskiem makes them.
func TestReviewTablesMoneyLog(t *testing.T) {
	doc := []byte(`{"schema":"wd/v1","id":"wf_paid","version":1,"name":"Paid","trigger":{"type":"manual"},"steps":[
 {"id":"log","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"money_log"}},
 {"id":"matches","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"money_matches"}}]}`)
	keep, warns := reviewTables([]TableSpec{{Name: "money_log", Columns: []ColumnSpec{{Name: "amount", Type: "number"}}}}, nil, doc, nil)
	if len(keep) != 0 || len(warns) != 2 {
		t.Fatalf("kept %+v warnings %+v", keep, warns)
	}
	for _, w := range warns {
		if !strings.Contains(w.Message, "Taskiem makes and fills itself") || strings.Contains(w.Message, "create it on the Tables page") {
			t.Errorf("warning %q", w.Message)
		}
	}
}

// A proposed table of customers' invoices with a status is flagged;
// suppliers' bills, and invoice tables without a status, are not.
func TestReviewTablesInvoiceTable(t *testing.T) {
	doc := func(name string) []byte {
		return []byte(`{"schema":"wd/v1","id":"wf_inv","version":1,"name":"Inv","trigger":{"type":"manual"},"steps":[
 {"id":"add","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","input":{"table":"` + name + `","rows":[]}}]}`)
	}
	cols := func(names ...string) []ColumnSpec {
		var out []ColumnSpec
		for _, n := range names {
			out = append(out, ColumnSpec{Name: n, Title: n, Type: "text"})
		}
		return out
	}
	for _, c := range []struct {
		table TableSpec
		flag  bool
	}{
		{TableSpec{Name: "invoices", Columns: cols("customer", "amount", "status")}, true},
		{TableSpec{Name: "customer_invoices", Columns: cols("customer", "paid")}, true},
		{TableSpec{Name: "supplier_invoices", Columns: cols("supplier", "status")}, false},
		{TableSpec{Name: "invoices", Columns: cols("customer", "amount")}, false},
		{TableSpec{Name: "invoicing_notes", Columns: cols("note", "status")}, false},
		{TableSpec{Name: "orders", Columns: cols("customer", "status")}, false},
	} {
		keep, warns := reviewTables([]TableSpec{c.table}, nil, doc(c.table.Name), nil)
		flagged := false
		for _, w := range warns {
			flagged = flagged || (w.Rule == RuleInvoiceTable && strings.Contains(w.Message, "invoices@1"))
		}
		if flagged != c.flag || len(keep) != 1 {
			t.Errorf("%s %v: flagged %v, kept %d (%+v)", c.table.Name, c.table.Columns, flagged, len(keep), warns)
		}
	}
}
