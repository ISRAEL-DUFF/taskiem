package pages

import (
	"strings"
	"testing"
	"time"
)

// Deliveries, customers and staff as record views: a rider's name is read
// from its envelope and never narrowed or ordered by; the customer is
// masked as everywhere; staff are in no area; each reads with SQL of the
// view's own columns.
func TestRecordViewsMore(t *testing.T) {
	doc := `{"page": "page/v1", "name": "riders-day", "title": "Riders today",
	  "filters": [{"id": "q", "type": "search"}],
	  "blocks": [
	    {"id": "owed", "type": "figure", "label": "Owed to riders", "value": "sum", "of": "owed", "naira": true, "source": {"records": "deliveries"}},
	    {"id": "list", "type": "list", "source": {"records": "deliveries", "uses": ["q"], "filters": [{"column": "status", "op": "eq", "value": "delivered"}]},
	     "item": {"title": "=row.order + ' · ' + row.rider", "sub": "=row.customer"}},
	    {"id": "who", "type": "table", "source": {"records": "customers"}, "columns": [{"column": "customer"}, {"column": "owed", "naira": true}]},
	    {"id": "team", "type": "table", "source": {"records": "staff"}, "columns": [{"column": "name"}, {"column": "roles"}]}
	  ]}`
	p := parseOK(t, doc)
	off := func(a string) bool { return false } // every area off but none
	if ps := Check(p, Env{AreaOn: func(a string) bool { return true }}); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	ps := Check(p, Env{AreaOn: off})
	if !hasProblem(ps, "/blocks/0/source/records", "turned off") || !hasProblem(ps, "/blocks/2/source/records", "turned off") || hasProblem(ps, "/blocks/3/source/records", "") {
		t.Errorf("areas: %v", ps)
	}
	// A rider's name is not a condition, an order or a search's column.
	for _, c := range []struct{ from, to, path string }{
		{`"filters": [{"column": "status", "op": "eq", "value": "delivered"}]`, `"filters": [{"column": "rider", "op": "eq", "value": "Musa"}]`, "/blocks/1/source/filters/0/column"},
		{`"source": {"records": "staff"}`, `"source": {"records": "deliveries", "order": [{"column": "rider"}]}`, "/blocks/3/source/order/0/column"},
	} {
		p := parseOK(t, strings.Replace(doc, c.from, c.to, 1))
		if ps := Check(p, Env{}); !hasProblem(ps, c.path, "") {
			t.Errorf("%s: %v", c.to, ps)
		}
	}
	searched := parseOK(t, strings.Replace(doc, `{"id": "q", "type": "search"}`, `{"id": "q", "type": "search", "columns": ["rider"]}`, 1))
	if ps := Check(searched, Env{}); !hasProblem(ps, "/blocks/1/source/uses/0", "kept sealed") {
		t.Errorf("search a rider: %v", ps)
	}
	dv, _ := RecordViewNamed("deliveries")
	for _, c := range SearchColumns(Filter{Type: "search"}, dv.Table(), false) {
		if c.Name == "rider" || c.Name == "customer" {
			t.Errorf("searched %s", c.Name)
		}
	}
	// Each view reads with SQL of its own columns.
	for _, name := range []string{"deliveries", "customers", "staff"} {
		v, _ := RecordViewNamed(name)
		r := Read{Filters: []any{map[string]any{"column": "id", "op": "eq", "value": "x"}}}
		sql, args, err := v.RecordsSelect(r, RowsShown, 0)
		if err != nil || len(args) != 2 || !strings.Contains(sql, v.Key+" = $2") {
			t.Errorf("%s: %s %v %v", name, sql, args, err)
		}
		if _, _, err := v.RecordsCount(r); err != nil {
			t.Errorf("%s count: %v", name, err)
		}
	}
	// A row: the customer from the order's envelope, the rider's name from
	// its own; masked for everyone (the customer), shown (the rider).
	row := dv.RecordRow("d1", nil, make([]*string, len(dv.Columns)), map[string]any{
		"customer": map[string]any{"name": "Bayo", "phone": "+2348012345678"}, "rider": "Musa"})
	if row["rider"] != "Musa" || row["customer"] != "Bayo" {
		t.Fatalf("row: %v", row)
	}
	vals, _ := p.FilterValues(nil, Viewer{Personal: true}, time.Now())
	got := Shape(p.Blocks[1], dv.Table(), []map[string]any{row}, vals)
	if !strings.Contains(got[0].Title, "Musa") || strings.Contains(got[0].Sub, "Bayo") {
		t.Errorf("shown: %+v", got[0])
	}
	if !dv.Orderable("offered_at") || dv.Orderable("rider") || dv.Orderable("id") {
		t.Errorf("orderable")
	}
}
