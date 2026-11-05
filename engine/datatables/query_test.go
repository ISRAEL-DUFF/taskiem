package datatables

import (
	"errors"
	"reflect"
	"testing"
)

func queryTables() []Table {
	return []Table{{Name: "customers", Title: "Customers", Columns: []Column{
		{Name: "name", Type: "text"}, {Name: "area", Type: "text"}, {Name: "phone", Type: "phone", Personal: true},
		{Name: "balance", Type: "number"}, {Name: "visits", Type: "integer"}, {Name: "active", Type: "boolean"},
		{Name: "joined", Type: "date"}, {Name: "meta", Type: "json"},
	}}}
}

// A spec is read only when its table, columns, ops, values, limit and
// aggregate all pass; each failure reads nothing.
func TestCheckQuery(t *testing.T) {
	tables := queryTables()
	ok := []QuerySpec{
		{Table: "customers", Filters: []QueryFilter{{Column: "area", Op: "eq", Value: "Ikeja"}}, Aggregate: &QueryAggregate{Fn: "count"}},
		{Table: "Customers", Filters: []QueryFilter{{Column: "balance", Op: "gt", Value: 10000}}, Order: []QueryOrder{{Column: "balance", Desc: true}}, Limit: 20},
		{Table: "customers", Aggregate: &QueryAggregate{Fn: "sum", Column: "balance"}},
		{Table: "customers", Filters: []QueryFilter{{Column: "active", Op: "eq", Value: false}, {Column: "phone", Op: "not_null"}}},
		{Table: "customers", Filters: []QueryFilter{{Column: "created_at", Op: "gte", Value: "2026-10-01T00:00:00Z"}, {Column: "visits", Op: "lte", Value: "3"}}},
	}
	for i, s := range ok {
		if _, err := CheckQuery(s, tables); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}
	bad := map[string]QuerySpec{
		"no table":       {Table: "suppliers"},
		"no column":      {Table: "customers", Filters: []QueryFilter{{Column: "town", Op: "eq", Value: "Ikeja"}}},
		"op on type":     {Table: "customers", Filters: []QueryFilter{{Column: "area", Op: "gt", Value: "A"}}},
		"unknown op":     {Table: "customers", Filters: []QueryFilter{{Column: "area", Op: "like", Value: "I%"}}},
		"bad value":      {Table: "customers", Filters: []QueryFilter{{Column: "balance", Op: "gt", Value: "lots"}}},
		"no value":       {Table: "customers", Filters: []QueryFilter{{Column: "balance", Op: "gt"}}},
		"empty text":     {Table: "customers", Filters: []QueryFilter{{Column: "area", Op: "eq", Value: " "}}},
		"limit":          {Table: "customers", Limit: 21},
		"sum of text":    {Table: "customers", Aggregate: &QueryAggregate{Fn: "sum", Column: "area"}},
		"other agg":      {Table: "customers", Aggregate: &QueryAggregate{Fn: "avg", Column: "balance"}},
		"json compared":  {Table: "customers", Filters: []QueryFilter{{Column: "meta", Op: "eq", Value: "x"}}},
		"order by json":  {Table: "customers", Order: []QueryOrder{{Column: "meta"}}},
		"order no col":   {Table: "customers", Order: []QueryOrder{{Column: "town"}}},
		"too many conds": {Table: "customers", Filters: []QueryFilter{{Column: "phone", Op: "not_null"}, {Column: "phone", Op: "not_null"}, {Column: "phone", Op: "not_null"}, {Column: "phone", Op: "not_null"}, {Column: "phone", Op: "not_null"}, {Column: "phone", Op: "not_null"}}},
	}
	for name, s := range bad {
		if _, err := CheckQuery(s, tables); !errors.Is(err, ErrQuery) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The checked spec as read_rows takes it: words matched without regard to
// case, a yes-or-no as is, numbers as they are, newest first last.
func TestQueryReadFilters(t *testing.T) {
	q, err := CheckQuery(QuerySpec{Table: "customers", Filters: []QueryFilter{
		{Column: "area", Op: "eq", Value: "Ike_ja"}, {Column: "name", Op: "contains", Value: "ada"},
		{Column: "active", Op: "eq", Value: "yes"}, {Column: "visits", Op: "gt", Value: 2.0}, {Column: "phone", Op: "is_null"},
	}, Order: []QueryOrder{{Column: "balance", Desc: true}}}, queryTables())
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"column": "area", "op": "ilike", "value": `Ike\_ja`},
		map[string]any{"column": "name", "op": "ilike", "value": "%ada%"},
		map[string]any{"column": "active", "op": "is", "value": true},
		map[string]any{"column": "visits", "op": "gt", "value": int64(2)},
		map[string]any{"column": "phone", "op": "is_null"},
	}
	if got := q.ReadFilters(); !reflect.DeepEqual(got, want) {
		t.Errorf("filters:\n%#v\nwant\n%#v", got, want)
	}
	if got := q.ReadOrder(); len(got) != 2 || got[1].(map[string]any)["column"] != "id" || q.Limit != QueryDefaultLimit {
		t.Errorf("order %v limit %d", got, q.Limit)
	}
}
