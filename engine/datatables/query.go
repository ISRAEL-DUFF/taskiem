package datatables

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Questions over a table's rows (decision 0060, R6; docs/spec/no-sheet-
// needed.md §6.5): a question asked in chat becomes a query spec, written
// by a word pattern or by the model from the question and the tables'
// names and columns alone, never a row. The spec is checked here against
// the table before anything is read, and turned into one read_rows call;
// the answer and its reading are written by code.

// QueryLimit is the most rows a question's answer lists, and
// QueryDefaultLimit how many it lists when the spec does not say.
const (
	QueryLimit        = 20
	QueryDefaultLimit = 10
	// QueryMaxFilters bounds a spec's conditions, all of which must hold.
	QueryMaxFilters = 5
	// QuerySumRows is how many matching rows a sum adds up at most; past
	// it the answer says it is over the first ones.
	QuerySumRows = 5000
)

// QuerySpec is a question over one table: the rows whose filters all
// hold, ordered and limited, or their count, or the sum of a column over
// them.
type QuerySpec struct {
	Table     string          `json:"table"`
	Filters   []QueryFilter   `json:"filters"`
	Order     []QueryOrder    `json:"order"`
	Limit     int             `json:"limit"`
	Aggregate *QueryAggregate `json:"aggregate"`
}

// QueryFilter is one condition: a column, an op (QueryOps) and, for the
// ops that compare, a value.
type QueryFilter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  any    `json:"value,omitempty"`
}

// QueryOrder orders the rows by a column.
type QueryOrder struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

// QueryAggregate is count, or sum of a number or integer column.
type QueryAggregate struct {
	Fn     string `json:"fn"`
	Column string `json:"column,omitempty"`
}

// QueryOps are the ops a spec may use. Which fit a column depends on its
// type (opsFor).
var QueryOps = []string{"eq", "neq", "lt", "lte", "gt", "gte", "contains", "is_null", "not_null"}

// ErrQuery is a spec that fails its checks: nothing is read for it.
var ErrQuery = errors.New("datatables: the question cannot be read from this table")

func queryErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrQuery}, a...)...)
}

// createdAt is the column every table has besides its own, which a
// question may filter or order by ("added this week").
var createdAt = Column{Name: "created_at", Title: "Added", Type: "datetime"}

// QueryColumns are the columns a question may name: the table's own and
// when a row was added.
func QueryColumns(t Table) []Column {
	return append(slices.Clone(t.Columns), createdAt)
}

// opsFor are the ops that fit a column's type: comparisons for numbers
// and dates, is and is not (and contains, for text) for words, is for a
// yes-or-no; empty and not empty for every column.
func opsFor(c Column) []string {
	switch c.Type {
	case "integer", "number", "date", "datetime":
		return []string{"eq", "neq", "lt", "lte", "gt", "gte", "is_null", "not_null"}
	case "text":
		return []string{"eq", "neq", "contains", "is_null", "not_null"}
	case "phone", "email":
		return []string{"eq", "neq", "is_null", "not_null"}
	case "boolean":
		return []string{"eq", "is_null", "not_null"}
	}
	return []string{"is_null", "not_null"}
}

// CheckedQuery is a spec that passed its checks against its table, its
// values as the data API takes them.
type CheckedQuery struct {
	Table     Table
	Filters   []CheckedFilter
	Order     []QueryOrder
	Limit     int
	Aggregate *QueryAggregate
}

// CheckedFilter is a condition with its column and checked value.
type CheckedFilter struct {
	Column Column
	Op     string
	Value  any
}

// FindQueryTable finds the table a spec names among the tenant's, by name
// or title, case-insensitively.
func FindQueryTable(tables []Table, name string) (Table, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, t := range tables {
		if strings.ToLower(t.Name) == key || strings.ToLower(t.Title) == key {
			return t, true
		}
	}
	return Table{}, false
}

// CheckQuery checks a spec against the tenant's tables: the table is
// theirs; every column is the table's (or created_at); each op fits its
// column's type and each value passes CheckValue for it; the limit is at
// most QueryLimit; the aggregate is count, or sum of a number or integer
// column. A spec that fails reads nothing (ErrQuery).
func CheckQuery(spec QuerySpec, tables []Table) (CheckedQuery, error) {
	t, ok := FindQueryTable(tables, spec.Table)
	if !ok {
		return CheckedQuery{}, queryErr("there is no table %q", spec.Table)
	}
	cols := QueryColumns(t)
	col := func(name string) (Column, bool) {
		for _, c := range cols {
			if c.Name == name {
				return c, true
			}
		}
		return Column{}, false
	}
	q := CheckedQuery{Table: t, Limit: spec.Limit}
	if len(spec.Filters) > QueryMaxFilters {
		return CheckedQuery{}, queryErr("at most %d conditions", QueryMaxFilters)
	}
	for _, f := range spec.Filters {
		c, ok := col(f.Column)
		if !ok {
			return CheckedQuery{}, queryErr("%s has no column %q", t.Name, f.Column)
		}
		if !slices.Contains(opsFor(c), f.Op) {
			return CheckedQuery{}, queryErr("%q does not fit column %s (%s)", f.Op, c.Name, c.Type)
		}
		cf := CheckedFilter{Column: c, Op: f.Op}
		if f.Op != "is_null" && f.Op != "not_null" {
			v, err := CheckValue(c, f.Value)
			if err != nil {
				return CheckedQuery{}, fmt.Errorf("%w: %w", ErrQuery, err)
			}
			if v == nil {
				return CheckedQuery{}, queryErr("%s %s needs a value", c.Name, f.Op)
			}
			if s, isText := v.(string); isText && c.Type == "text" && strings.TrimSpace(s) == "" {
				return CheckedQuery{}, queryErr("%s %s needs a value", c.Name, f.Op)
			}
			cf.Value = v
		}
		q.Filters = append(q.Filters, cf)
	}
	if len(spec.Order) > 2 {
		return CheckedQuery{}, queryErr("at most two orders")
	}
	for _, o := range spec.Order {
		c, ok := col(o.Column)
		if !ok {
			return CheckedQuery{}, queryErr("%s has no column %q", t.Name, o.Column)
		}
		if c.Type == "json" {
			return CheckedQuery{}, queryErr("rows cannot be ordered by %s (json)", c.Name)
		}
		q.Order = append(q.Order, o)
	}
	switch {
	case spec.Limit < 0 || spec.Limit > QueryLimit:
		return CheckedQuery{}, queryErr("the limit is 1 to %d", QueryLimit)
	case spec.Limit == 0:
		q.Limit = QueryDefaultLimit
	}
	if a := spec.Aggregate; a != nil {
		switch a.Fn {
		case "count":
			q.Aggregate = &QueryAggregate{Fn: "count"}
		case "sum":
			c, ok := col(a.Column)
			if !ok {
				return CheckedQuery{}, queryErr("%s has no column %q", t.Name, a.Column)
			}
			if c.Type != "number" && c.Type != "integer" {
				return CheckedQuery{}, queryErr("only a number column can be added up, not %s (%s)", c.Name, c.Type)
			}
			q.Aggregate = &QueryAggregate{Fn: "sum", Column: c.Name}
		case "":
		default:
			return CheckedQuery{}, queryErr("the aggregate is count or sum, not %q", a.Fn)
		}
	}
	return q, nil
}

// SumColumn is the column a sum adds up ("" for none).
func (q CheckedQuery) SumColumn() (Column, bool) {
	if q.Aggregate == nil || q.Aggregate.Fn != "sum" {
		return Column{}, false
	}
	for _, c := range q.Table.Columns {
		if c.Name == q.Aggregate.Column {
			return c, true
		}
	}
	return Column{}, false
}

// ReadFilters are the conditions as read_rows takes them. Text is matched
// without regard to case: "is" as the whole value, "contains" anywhere in
// it; a yes-or-no as is true or is false.
func (q CheckedQuery) ReadFilters() []any {
	out := make([]any, 0, len(q.Filters))
	like := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	for _, f := range q.Filters {
		m := map[string]any{"column": f.Column.Name, "op": f.Op}
		switch {
		case f.Op == "is_null" || f.Op == "not_null":
		case f.Column.Type == "boolean":
			m["op"], m["value"] = "is", f.Value
		case f.Column.Type == "text" && f.Op == "eq":
			m["op"], m["value"] = "ilike", like.Replace(f.Value.(string))
		case f.Column.Type == "text" && f.Op == "contains":
			m["op"], m["value"] = "ilike", "%"+like.Replace(f.Value.(string))+"%"
		default:
			m["value"] = f.Value
		}
		out = append(out, m)
	}
	return out
}

// ReadOrder is the order as read_rows takes it: the spec's, then newest
// first so a page is stable.
func (q CheckedQuery) ReadOrder() []any {
	out := make([]any, 0, len(q.Order)+1)
	for _, o := range q.Order {
		out = append(out, map[string]any{"column": o.Column, "desc": o.Desc})
	}
	return append(out, map[string]any{"column": "id", "desc": true})
}
