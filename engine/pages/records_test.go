package pages

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
)

const ordersPage = `{"page": "page/v1", "name": "shop-week", "title": "The shop this week",
  "filters": [{"id": "q", "type": "search"}, {"id": "st", "type": "choice", "column": "status", "options": [{"value": "accepted"}, {"value": "completed"}]},
              {"id": "when", "type": "dates", "column": "placed_at"}],
  "blocks": [
    {"id": "n", "type": "figure", "label": "Orders", "value": "count", "source": {"records": "orders"}},
    {"id": "owed", "type": "figure", "label": "Invoiced", "value": "sum", "of": "total", "naira": true, "source": {"records": "invoices", "filters": [{"column": "status", "op": "neq", "value": "void"}]}},
    {"id": "list", "type": "list", "title": "Orders",
     "source": {"records": "orders", "order": [{"column": "placed_at", "desc": true}], "uses": ["q", "st", "when"]},
     "item": {"title": "=row.number", "sub": "=row.customer + ' · ' + row.phone", "state": {"text": "=row.status"}},
     "detail": {"title": "=row.number", "lines": [{"label": "Total", "value": "=row.total", "naira": true}, {"label": "Email", "value": "=row.email"}]}},
    {"id": "inv", "type": "table", "title": "Invoices", "source": {"records": "invoices"}, "columns": [{"column": "number"}, {"column": "customer"}, {"column": "owed", "naira": true}]}
  ]}`

func TestRecordsParseAndCheck(t *testing.T) {
	p := parseOK(t, ordersPage)
	if msgs := schema.Validate([]byte(ordersPage)); len(msgs) > 0 {
		t.Fatalf("schema: %v", msgs)
	}
	if ps := Check(p, Env{}); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	if got := strings.Join(p.UsesTables(), ","); got != "records:orders,records:invoices" {
		t.Errorf("uses: %s", got)
	}
	if got := strings.Join(Shows(p, nil), "|"); !strings.HasPrefix(got, "Orders: Number, Customer, Phone, Status") || !strings.Contains(got, "Invoices: Total") {
		t.Errorf("shows: %s", got)
	}
	// A table named like a view's title is never taken for the view, nor
	// the view for it.
	orders := datatables.Table{Name: "orders", Title: "Orders", Columns: []datatables.Column{{Name: "x", Type: "text"}}}
	if tb, ok := Lookup([]datatables.Table{orders}, "orders"); !ok || tb.Name != "orders" {
		t.Errorf("own table: %+v", tb)
	}
	if tb, ok := Lookup(nil, "Orders"); ok {
		t.Errorf("a title found a view: %+v", tb)
	}
	if tb, ok := Lookup(nil, "records:orders"); !ok || tb.Title != "Orders" {
		t.Errorf("view: %+v", tb)
	}
}

func TestRecordsRefusals(t *testing.T) {
	rows := []struct {
		name, from, to, path, says string
	}{
		{"unknown view", `"source": {"records": "orders"}}`, `"source": {"records": "riders"}}`, "/blocks/0/source/records", "Taskiem's riders, which a page cannot show: it shows orders and invoices"},
		{"area off", `{"records": "orders"}, "value": "count"`, `{"records": "orders"}, "value": "count"`, "/blocks/0/source/records", "the business's orders, and Orders is turned off in Settings"},
		{"unknown column", `"of": "total"`, `"of": "amount"`, "/blocks/1/of", "which the table Invoices does not have"},
		{"sum of a personal column", `"value": "sum", "of": "total", "naira": true, "source": {"records": "invoices", "filters": [{"column": "status", "op": "neq", "value": "void"}]}`,
			`"value": "count", "source": {"records": "invoices", "filters": [{"column": "phone", "op": "eq", "value": "0803"}]}`, "/blocks/1/source/filters/0/column", "cannot pick invoices by Phone: it is personal"},
		{"order by a personal column", `"order": [{"column": "placed_at", "desc": true}]`, `"order": [{"column": "customer"}]`, "/blocks/2/source/order/0/column", "cannot put orders in order by Customer"},
		{"search on a personal column", `{"id": "q", "type": "search"}`, `{"id": "q", "type": "search", "columns": ["email"]}`, "/blocks/2/source/uses/0", "Email is personal"},
		{"choice on a personal column", `"column": "status", "options"`, `"column": "customer", "options"`, "/blocks/2/source/uses/1", "Customer is personal"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			doc := strings.Replace(ordersPage, r.from, r.to, 1)
			if doc == ordersPage && r.name != "area off" {
				t.Fatal("the page did not change")
			}
			p := parseOK(t, doc)
			var env Env
			if r.name == "area off" {
				env.AreaOn = func(a string) bool { return a != "orders" }
			}
			ps := Check(p, env)
			for _, pr := range ps {
				if pr.Path == r.path && strings.Contains(pr.Message, r.says) {
					for _, bad := range []string{"block", "schema", "query"} {
						if strings.Contains(pr.Message, bad) {
							t.Errorf("says %q: %s", bad, pr.Message)
						}
					}
					return
				}
			}
			t.Errorf("want %s %q, got %v", r.path, r.says, ps)
		})
	}
	// Parse: both a table and records.
	_, ps := Parse([]byte(strings.Replace(ordersPage, `"source": {"records": "orders"}}`, `"source": {"records": "orders", "table": "x"}}`, 1)))
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "one of them") {
		t.Errorf("both: %v", ps)
	}
	// Missing: the area off, and a view Taskiem lacks.
	p := parseOK(t, strings.Replace(ordersPage, `"records": "invoices", "filters"`, `"records": "riders", "filters"`, 1))
	got := strings.Join(Missing(p, nil, func(a string) bool { return a != "orders" }), " | ")
	if !strings.Contains(got, "This business has Orders turned off, so its orders cannot be shown.") || !strings.Contains(got, "Taskiem has no records called riders") {
		t.Errorf("missing: %s", got)
	}
}

func TestRecordsReadAndSQL(t *testing.T) {
	p := parseOK(t, ordersPage)
	v, _ := RecordViewNamed("orders")
	tb := v.Table()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	// A viewer with table.read is still no reader of a view's personal
	// columns: the search leaves them out.
	vals, ps := p.FilterValues(map[string]string{"q": "AD-1", "st": "accepted", "when": "today"}, Viewer{Personal: true}, now)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	read, err := p.ReadFor(p.Blocks[2], tb, vals, nil)
	if err != nil {
		t.Fatal(err)
	}
	sql, args, err := v.RecordsSelect(read, RowsShown, 25)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM storefront_orders o WHERE o.tenant_id = $1", "(o.reference)::text ILIKE $2", "o.status = $", "o.created_at >= $", "::text::timestamptz",
		"ORDER BY o.created_at DESC, o.id DESC LIMIT 25 OFFSET 25"} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "customer ILIKE") || strings.Contains(sql, "o.customer)::text ILIKE") {
		t.Errorf("searched a personal column: %s", sql)
	}
	if args[0] != nil || args[1] != "%AD-1%" {
		t.Errorf("args: %v", args)
	}
	// Ordered by a personal column: refused for every viewer.
	if _, err := p.ReadFor(p.Blocks[2], tb, vals, &Order{Column: "customer"}); !errors.Is(err, ErrPersonalOrder) {
		t.Errorf("personal order: %v", err)
	}
	// Words a page wrote are never SQL: a column the view lacks.
	if _, _, err := v.RecordsSelect(Read{Filters: []any{map[string]any{"column": "x; drop table", "op": "eq", "value": "1"}}}, 1, 0); !errors.Is(err, ErrRecordsRead) {
		t.Errorf("unknown column: %v", err)
	}
	if _, _, err := v.RecordsSelect(Read{Filters: []any{map[string]any{"column": "phone", "op": "eq", "value": "1"}}}, 1, 0); !errors.Is(err, ErrRecordsRead) {
		t.Errorf("personal column: %v", err)
	}
	inv, _ := RecordViewNamed("invoices")
	sql, _, err = inv.RecordsSum(Read{}, "total")
	if err != nil || !strings.Contains(sql, "sum(i.total_kobo)") {
		t.Errorf("sum: %s %v", sql, err)
	}
	if _, _, err := inv.RecordsSum(Read{}, "customer"); err == nil {
		t.Error("a sum of a personal column")
	}

	// Shaped: personal columns masked for everyone, even with table.read.
	row := v.RecordRow("AD-1", nil, []*string{ptr("AD-1"), nil, nil, nil, ptr("accepted"), nil, nil, ptr("250000")}, map[string]any{"name": "Ada Obi", "phone": "08031234567", "email": "ada@example.com"})
	shown := Shape(p.Blocks[2], tb, []map[string]any{row}, vals)
	if len(shown) != 1 || shown[0].ID != "AD-1" || shown[0].Title != "AD-1" || strings.Contains(shown[0].Sub, "Ada") || strings.Contains(shown[0].Sub, "1234567") || !strings.HasPrefix(shown[0].Sub, "A•••") {
		t.Errorf("shown: %+v", shown)
	}
	if d := shown[0].Detail; d == nil || d.Lines[0].Value != "₦2,500" || strings.Contains(d.Lines[1].Value, "ada@") {
		t.Errorf("detail: %+v", d)
	}
	if got := v.LinkOf("AD 1/2"); got != "/storefront/orders/AD%201%2F2" {
		t.Errorf("link: %s", got)
	}
	// The cursor: a count only, given for this order.
	c := Cursor{Skip: 25}
	if _, err := ReadRecordCursor(c.String(), nil); err != nil {
		t.Error(err)
	}
	for _, bad := range []Cursor{{Skip: 25, After: "x"}, {Skip: MaxRecordSkip + 1}, {Skip: 25, Order: "total"}} {
		if _, err := ReadRecordCursor(bad.String(), nil); err == nil {
			t.Errorf("taken: %+v", bad)
		}
	}
}

func ptr(s string) *string { return &s }

// Review of the records' reads: newest first when a part gives no order
// (an invoice's id is random), no NULLS LAST where an index orders by a
// column that always has a value, a status picked by equality, the count
// stopped past 10,000, and an order only by a column of the view's own.
func TestRecordsOrderAndCount(t *testing.T) {
	inv, _ := RecordViewNamed("invoices")
	tb := inv.Table()
	p := parseOK(t, ordersPage)
	vals, _ := p.FilterValues(map[string]string{"st": "accepted"}, Viewer{}, time.Now())
	read, err := p.ReadFor(p.Blocks[3], tb, vals, nil)
	if err != nil {
		t.Fatal(err)
	}
	sql, _, _ := inv.RecordsSelect(read, RowsShown, 0)
	if !strings.Contains(sql, "ORDER BY i.created_at DESC, i.id DESC LIMIT") {
		t.Errorf("the tie-breaker alone: %s", sql)
	}
	read, _ = p.ReadFor(p.Blocks[3], tb, vals, &Order{Column: "due_on"})
	if sql, _, _ = inv.RecordsSelect(read, RowsShown, 0); !strings.Contains(sql, "ORDER BY i.due_on ASC NULLS LAST, i.created_at DESC, i.id DESC") {
		t.Errorf("a column with no value: %s", sql)
	}
	ord, _ := RecordViewNamed("orders")
	read, _ = p.ReadFor(p.Blocks[2], ord.Table(), vals, nil)
	sql, args, _ := ord.RecordsSelect(read, RowsShown, 0)
	if !strings.Contains(sql, "o.status = $") || !strings.Contains(fmt.Sprint(args), "accepted") {
		t.Errorf("a choice on the status: %s %v", sql, args)
	}
	if _, args, _ := ord.RecordsSelect(Read{Filters: []any{map[string]any{"column": "status", "op": "ilike", "value": "Accepted"}}}, 1, 0); args[1] != "accepted" {
		t.Errorf("a status in capitals: %v", args)
	}
	contains, _, _ := ord.RecordsSelect(Read{Filters: []any{map[string]any{"column": "status", "op": "ilike", "value": "%acc%"}}}, 1, 0)
	if !strings.Contains(contains, "(o.status)::text ILIKE $2") {
		t.Errorf("contains: %s", contains)
	}
	count, _, _ := ord.RecordsCount(Read{})
	if count != "SELECT count(*) FROM (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = $1 LIMIT 10001) c" {
		t.Errorf("count: %s", count)
	}
	for col, want := range map[string]bool{"total": true, "created_at": true, "updated_at": false, "id": false, "customer": false, "nope": false} {
		if ord.Orderable(col) != want {
			t.Errorf("orderable %s: %v", col, !want)
		}
	}
}
