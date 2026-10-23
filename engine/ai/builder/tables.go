package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/wd"
)

// The data tables a draft needs (rule 12): the model describes the new
// ones, the builder keeps those that are new and well formed, and the API
// makes them when the person saves the draft (after they have seen and
// perhaps changed them).

var tableNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// reservedColumns are the columns every table already has.
var reservedColumns = []string{"id", "created_at", "updated_at"}

// checkTableShape is the builder's own check of a proposed table, before
// the platform's (Builder.CheckTable).
func checkTableShape(t TableSpec) error {
	if !tableNameRe.MatchString(t.Name) || strings.HasPrefix(t.Name, "xb_") || strings.HasPrefix(t.Name, "pg_") {
		return fmt.Errorf("the table name %q must be lower-case letters, digits and underscores, starting with a letter", t.Name)
	}
	if len(t.Columns) == 0 {
		return fmt.Errorf("the table %q has no columns", t.Name)
	}
	seen := map[string]bool{}
	for _, c := range t.Columns {
		switch {
		case !tableNameRe.MatchString(c.Name) || slices.Contains(reservedColumns, c.Name):
			return fmt.Errorf("the table %q: the column name %q is not allowed", t.Name, c.Name)
		case seen[c.Name]:
			return fmt.Errorf("the table %q has the column %q twice", t.Name, c.Name)
		case !slices.Contains(ColumnTypes, c.Type):
			return fmt.Errorf("the table %q: the column %q has the unknown type %q", t.Name, c.Name, c.Type)
		}
		seen[c.Name] = true
	}
	return nil
}

// reviewTables keeps the proposed tables that are new and well formed,
// and warns about the rest, and about any table the workflow uses that
// neither exists nor is proposed.
func reviewTables(proposed []TableSpec, existing []DataTable, doc []byte, check func(TableSpec) error) ([]TableSpec, []Warning) {
	have := map[string]bool{}
	for _, t := range existing {
		have[t.Name] = true
	}
	used := tablesUsed(doc)
	keep := []TableSpec{}
	var warns []Warning
	named := map[string]bool{}
	for _, t := range proposed {
		t.Name = strings.TrimSpace(t.Name)
		switch {
		case named[t.Name]:
			continue
		case have[t.Name]:
			warns = append(warns, Warning{Kind: "table", Message: fmt.Sprintf("the table %q exists already: the draft uses it as it is", t.Name)})
			continue
		case !used[t.Name]:
			continue // described but not used: nothing to make
		case slices.Contains(MoneyLogTables, t.Name):
			continue // Taskiem's own: never made from a draft (see below)
		}
		if invoiceTable(t) {
			warns = append(warns, Warning{Kind: "table", Rule: RuleInvoiceTable, Message: fmt.Sprintf("the table %q keeps invoices with a status: invoices to customers are Taskiem's own (invoices@1), "+
				"marked paid only by the payments counted towards them, with a page where the customer pays; use invoices@1 create and send, and wait for the invoice.paid signal, instead of a table", t.Name)})
		}
		err := checkTableShape(t)
		if err == nil && check != nil {
			err = check(t)
		}
		if err != nil {
			warns = append(warns, Warning{Kind: "table", Message: "a table the draft needs could not be proposed: " + err.Error() + "; create it on the Tables page"})
			continue
		}
		named[t.Name] = true
		keep = append(keep, t)
	}
	var missing []string
	for name := range used {
		if !have[name] && !named[name] {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	for _, name := range missing {
		if slices.Contains(MoneyLogTables, name) {
			warns = append(warns, Warning{Kind: "table", Message: fmt.Sprintf("the workflow reads the money log's table %q, which Taskiem makes and fills itself once the business has data tables and its first movement of money is recorded; it is not made on the Tables page", name)})
			continue
		}
		warns = append(warns, Warning{Kind: "table", Message: fmt.Sprintf("the workflow uses a table %q that does not exist: create it on the Tables page before publishing", name)})
	}
	written := tablesWritten(doc)
	for _, t := range existing {
		if t.Kept && written[t.Name] {
			warns = append(warns, Warning{Kind: "table", Message: fmt.Sprintf("the workflow writes the table %q, which Taskiem keeps: its rows are added by Taskiem and cannot be changed or deleted, so that step will fail; read it instead", t.Name)})
		}
	}
	return keep, warns
}

// MoneyLogTables are the money log's kept tables (engine/moneylog.Table
// and MatchesTable, which the builder may not import: moneylog's test
// keeps them the same). Taskiem makes and fills them; a draft only reads
// them (rule 12).
var MoneyLogTables = []string{"money_log", "money_matches"}

// RuleInvoiceTable flags a proposed table that keeps customers' invoices
// with a status: invoices are invoices@1's (rule 17).
const RuleInvoiceTable = "invoice_as_table"

// invoiceTable reports whether a proposed table keeps invoices with a
// status or paid column. A table of bills from suppliers is not one: the
// business pays those.
func invoiceTable(t TableSpec) bool {
	parts := strings.Split(t.Name, "_")
	if !slices.Contains(parts, "invoice") && !slices.Contains(parts, "invoices") {
		return false
	}
	for _, p := range parts {
		if slices.Contains([]string{"supplier", "suppliers", "vendor", "vendors", "purchase", "purchases", "payable", "payables", "bill", "bills", "expense", "expenses"}, p) {
			return false
		}
	}
	for _, c := range t.Columns {
		if slices.Contains([]string{"status", "paid", "is_paid", "payment_status", "invoice_status", "paid_at", "paid_on"}, c.Name) {
			return true
		}
	}
	return false
}

// rowWrites are the xenibase@1 actions that change a table's rows.
var rowWrites = []string{"insert_rows", "upsert_rows", "update_rows", "delete_rows"}

// tablesWritten are the data tables a definition's steps write, named
// as they are (not an expression).
func tablesWritten(doc []byte) map[string]bool {
	out := map[string]bool{}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return out
	}
	var walk func(any)
	walk = func(x any) {
		switch n := x.(type) {
		case map[string]any:
			action, _ := n["action"].(string)
			if n["connector"] == "xenibase@1" && n["connection"] == "data_tables" && slices.Contains(rowWrites, action) {
				if in, ok := n["input"].(map[string]any); ok {
					if name, ok := in["table"].(string); ok && name != "" && !strings.HasPrefix(name, "=") {
						out[name] = true
					}
				}
			}
			for _, c := range n {
				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(v)
	return out
}

// tablesUsed are the data tables a definition names: input.table of its
// xenibase@1 steps on the data_tables connection, where it is written as
// it is (not an expression), in every scope.
func tablesUsed(doc []byte) map[string]bool {
	out := map[string]bool{}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return out
	}
	var walk func(any)
	walk = func(x any) {
		switch n := x.(type) {
		case map[string]any:
			if n["connector"] == "xenibase@1" && n["connection"] == "data_tables" {
				if in, ok := n["input"].(map[string]any); ok {
					if name, ok := in["table"].(string); ok && name != "" && !strings.HasPrefix(name, "=") {
						out[name] = true
					}
				}
			}
			for _, c := range n {
				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(v)
	return out
}

// TableFor is the table a workflow needs and does not have, as it uses
// it: the columns its xenibase@1 steps on data_tables write (row, rows,
// set), filter and order by, each typed from what goes in it (a form
// field's format or personal-data kind, a literal's type), text when it
// cannot tell. The readiness check offers to make it in one tap.
func TableFor(def *wd.Definition, name string) TableSpec {
	spec := TableSpec{Name: name, Title: strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "_", " ")}
	schema, _ := def.InputsSchema()
	m, _ := schema.(map[string]any)
	props, _ := m["properties"].(map[string]any)
	types := map[string]string{}
	var order []string
	add := func(col string, v any) {
		if !tableNameRe.MatchString(col) || slices.Contains(reservedColumns, col) {
			return
		}
		t := columnType(v, props)
		if prev, ok := types[col]; ok {
			if prev == "text" && t != "text" {
				types[col] = t
			}
			return
		}
		types[col] = t
		order = append(order, col)
	}
	addRow := func(row any) {
		if m, ok := row.(map[string]any); ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				add(k, m[k])
			}
		}
	}
	def.Walk(func(st *wd.Step) {
		if st.Connector != "xenibase@1" || st.Connection != "data_tables" || st.Input["table"] != name {
			return
		}
		addRow(st.Input["row"])
		if rows, ok := st.Input["rows"].([]any); ok {
			for _, r := range rows {
				addRow(r)
			}
		}
		addRow(st.Input["set"])
		for _, key := range []string{"filters", "order"} {
			list, _ := st.Input[key].([]any)
			for _, f := range list {
				if m, ok := f.(map[string]any); ok {
					if col, ok := m["column"].(string); ok {
						add(col, m["value"])
					}
				}
			}
		}
	})
	for _, c := range order {
		spec.Columns = append(spec.Columns, ColumnSpec{Name: c, Title: strings.ToUpper(c[:1]) + strings.ReplaceAll(c[1:], "_", " "), Type: types[c]})
	}
	return spec
}

var bodyFieldRe = regexp.MustCompile(`^=\s*trigger\.body\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// columnType is the column type for a value written to it.
func columnType(v any, props map[string]any) string {
	switch x := v.(type) {
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case string:
		m := bodyFieldRe.FindStringSubmatch(x)
		if m == nil {
			return "text"
		}
		p, _ := props[m[1]].(map[string]any)
		format, _ := p["format"].(string)
		kind, _ := p["x-pii"].(string)
		typ, _ := p["type"].(string)
		switch {
		case format == "date":
			return "date"
		case format == "date-time":
			return "datetime"
		case kind == "phone" || strings.HasPrefix(format, "phone"):
			return "phone"
		case kind == "email" || format == "email":
			return "email"
		case typ == "integer":
			return "integer"
		case typ == "number":
			return "number"
		case typ == "boolean":
			return "boolean"
		}
	}
	return "text"
}
