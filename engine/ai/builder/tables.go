package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
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
		warns = append(warns, Warning{Kind: "table", Message: fmt.Sprintf("the workflow uses a table %q that does not exist: create it on the Tables page before publishing", name)})
	}
	return keep, warns
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
