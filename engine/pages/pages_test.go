package pages

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
)

var tickets = datatables.Table{Name: "tickets", Title: "Tickets", Columns: []datatables.Column{
	{Name: "title", Title: "Title", Type: "text"},
	{Name: "customer", Title: "Customer", Type: "text"},
	{Name: "status", Title: "Status", Type: "text"},
	{Name: "phone", Title: "Phone", Type: "phone", Personal: true},
	{Name: "email", Title: "Email", Type: "email", Personal: true},
	{Name: "amount_kobo", Title: "Amount", Type: "integer"},
	{Name: "due", Title: "Due", Type: "date"},
	{Name: "data", Type: "json"},
}}

const good = `{"page": "page/v1", "name": "tickets", "title": "Tickets", "icon": "tables", "roles": ["support", "manager"],
  "filters": [{"id": "q", "type": "search"}, {"id": "st", "type": "choice", "column": "status", "options": [{"value": "open"}, {"value": "done", "label": "Done"}]},
              {"id": "when", "type": "dates", "column": "due"}, {"id": "mine", "type": "mine", "column": "email", "label": "Mine"}],
  "blocks": [
    {"id": "head", "type": "heading", "title": "Support desk", "text": "Every ticket, newest first"},
    {"id": "open", "type": "figure", "label": "Open", "source": {"table": "tickets", "filters": [{"column": "status", "op": "eq", "value": "open"}]}, "value": "count", "tone": "warn", "lead": true},
    {"id": "owed", "type": "figure", "label": "Owed", "source": {"table": "tickets"}, "value": "sum", "of": "amount_kobo", "naira": true},
    {"id": "week", "type": "figure", "label": "This week", "source": {"table": "tickets"}, "value": "count", "change": {"column": "created_at", "period": "week"}},
    {"id": "list", "type": "list", "title": "Open tickets",
     "source": {"table": "tickets", "order": [{"column": "created_at", "desc": true}], "uses": ["q", "st", "when", "mine"]},
     "item": {"title": "=row.title", "sub": "=row.customer + ' · ' + row.phone", "state": {"text": "=row.status", "tones": {"open": "warn", "done": "good"}}},
     "group": {"column": "status", "order": ["open", "doing", "done"]},
     "detail": {"title": "=row.title", "lines": [{"label": "Customer", "value": "=row.customer"}, {"label": "Amount", "value": "=row.amount_kobo", "naira": true}]},
     "empty": "No open tickets"},
    {"id": "all", "type": "table", "title": "All", "source": {"table": "tickets"}, "columns": [{"column": "title"}, {"column": "amount_kobo", "label": "Amount", "naira": true}]}
  ]}`

func parseOK(t *testing.T, doc string) *Page {
	t.Helper()
	p, ps := Parse([]byte(doc))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	return p
}

func TestParseGood(t *testing.T) {
	p := parseOK(t, good)
	if msgs := schema.Validate([]byte(good)); len(msgs) > 0 {
		t.Fatalf("schema: %v", msgs)
	}
	if ps := Check(p, Env{Tables: []datatables.Table{tickets}}); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	if got := strings.Join(Shows(p, []datatables.Table{tickets}), "|"); got != "Tickets: Amount, Title, Customer, Phone, Status" {
		t.Errorf("shows: %s", got)
	}
}

// edit changes the good page by a function over its decoded JSON.
func edit(t *testing.T, fn func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(good), &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	b, _ := json.Marshal(m)
	return string(b)
}

func block(m map[string]any, i int) map[string]any { return m["blocks"].([]any)[i].(map[string]any) }

// Each row: a page that is wrong for one reason, refused by Parse (or by
// Check against the tickets table) with a plain sentence at its path.
func TestRefusals(t *testing.T) {
	rows := []struct {
		name, path, says string
		fn               func(m map[string]any)
		check            bool
	}{
		{"not a page", "/page", `"page": "page/v1"`, func(m map[string]any) { m["page"] = "wd/v1" }, false},
		{"bad name", "/name", "2 to 24 small letters", func(m map[string]any) { m["name"] = "Tickets!" }, false},
		{"no title", "/title", "needs a name", func(m map[string]any) { m["title"] = " " }, false},
		{"unknown field", "/", "does not know", func(m map[string]any) { m["colour"] = "red" }, false},
		{"unknown kind", "/blocks/0/type", "does not draw a part of the kind \"board\"", func(m map[string]any) { block(m, 0)["type"] = "board" }, false},
		{"two leads", "/blocks", "Only one figure", func(m map[string]any) { block(m, 2)["lead"] = true }, false},
		{"five figures", "/blocks/5", "At most 4 figures", func(m map[string]any) {
			bs := m["blocks"].([]any)
			m["blocks"] = append(bs[:4:4], block(m, 3), block(m, 3), bs[4])
			for i, b := range m["blocks"].([]any) {
				b.(map[string]any)["id"] = "b" + string(rune('a'+i))
				delete(b.(map[string]any), "lead")
			}
		}, false},
		{"too many parts", "/blocks", "at most 12 parts", func(m map[string]any) {
			var bs []any
			for i := 0; i < 13; i++ {
				bs = append(bs, map[string]any{"id": "h" + string(rune('a'+i)), "type": "heading", "title": "x"})
			}
			m["blocks"] = bs
		}, false},
		{"sum of nothing", "/blocks/2/of", "adds up a column", func(m map[string]any) { delete(block(m, 2), "of") }, false},
		{"unknown filter used", "/blocks/4/source/uses/0", "a filter the page does not have: nope", func(m map[string]any) {
			block(m, 4)["source"].(map[string]any)["uses"] = []any{"nope"}
		}, false},
		{"choice without options", "/filters/1/options", "1 to 20 choices", func(m map[string]any) { delete(m["filters"].([]any)[1].(map[string]any), "options") }, false},
		{"expression reads steps", "/blocks/4/item/title", "a page shows a row's columns", func(m map[string]any) {
			block(m, 4)["item"].(map[string]any)["title"] = "=steps.a.output"
		}, false},
		{"too many columns", "/blocks/5/columns", "1 to 6 columns", func(m map[string]any) {
			var cs []any
			for i := 0; i < 7; i++ {
				cs = append(cs, map[string]any{"column": "title"})
			}
			block(m, 5)["columns"] = cs
		}, false},
		{"reserved name", "/name", "Taskiem's chat already uses", func(m map[string]any) { m["name"] = "stock" }, true},
		{"no such table", "/blocks/1/source/table", "the table orders2, which the business does not have", func(m map[string]any) {
			block(m, 1)["source"].(map[string]any)["table"] = "orders2"
		}, true},
		{"no such column in a condition", "/blocks/1/source", "has no column \"colour\"", func(m map[string]any) {
			block(m, 1)["source"].(map[string]any)["filters"] = []any{map[string]any{"column": "colour", "op": "eq", "value": "x"}}
		}, true},
		{"op unfit for the column", "/blocks/1/source", "is more than does not fit column status", func(m map[string]any) {
			block(m, 1)["source"].(map[string]any)["filters"] = []any{map[string]any{"column": "status", "op": "gt", "value": "x"}}
		}, true},
		{"sum of words", "/blocks/2/of", "does not hold numbers", func(m map[string]any) { block(m, 2)["of"] = "title" }, true},
		{"change by words", "/blocks/3/change/column", "does not hold dates", func(m map[string]any) {
			block(m, 3)["change"].(map[string]any)["column"] = "title"
		}, true},
		{"grouped by personal", "/blocks/4/group/column", "it is personal", func(m map[string]any) { block(m, 4)["group"].(map[string]any)["column"] = "phone" }, true},
		{"row reads a missing column", "/blocks/4/item/sub", "row.colour", func(m map[string]any) { block(m, 4)["item"].(map[string]any)["sub"] = "=row.colour" }, true},
		{"table shows a missing column", "/blocks/5/columns/0/column", "shows the column colour", func(m map[string]any) {
			block(m, 5)["columns"].([]any)[0].(map[string]any)["column"] = "colour"
		}, true},
		{"mine on words", "/blocks/4/source/uses/3", "Mine reads a column of email addresses or phone numbers, and Customer", func(m map[string]any) {
			m["filters"].([]any)[3].(map[string]any)["column"] = "customer"
		}, true},
		{"dates on words", "/blocks/4/source/uses/2", "does not hold dates", func(m map[string]any) {
			m["filters"].([]any)[2].(map[string]any)["column"] = "title"
		}, true},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			doc := edit(t, r.fn)
			p, ps := Parse([]byte(doc))
			if r.check {
				if len(ps) > 0 {
					t.Fatalf("Parse refused it: %v", ps)
				}
				ps = Check(p, Env{Tables: []datatables.Table{tickets}})
			}
			found := false
			for _, pr := range ps {
				if pr.Path == r.path && strings.Contains(pr.Message, r.says) {
					found = true
				}
				for _, w := range []string{"block", "schema", "query", "JSON", "expression"} {
					if strings.Contains(pr.Message, w) {
						t.Errorf("not plain: %q says %s", pr.Message, w)
					}
				}
			}
			if !found {
				t.Fatalf("want %s: %q, got %v", r.path, r.says, ps)
			}
			// Whatever Parse refuses, the schema refuses too (but for the
			// rules only Go can hold: limits across parts, expressions).
			if !r.check && len(schema.Validate([]byte(doc))) == 0 {
				switch r.name {
				case "two leads", "five figures", "expression reads steps", "unknown filter used":
				default:
					t.Errorf("the schema took what Parse refused")
				}
			}
		})
	}
}

func TestTaken(t *testing.T) {
	p := parseOK(t, good)
	ps := Check(p, Env{Tables: []datatables.Table{tickets}, Taken: func(n string) string { return "Another page is called " + n + "." }})
	if len(ps) != 1 || ps[0].Path != "/name" || !strings.Contains(ps[0].Message, "Another page") {
		t.Fatalf("%v", ps)
	}
}

func TestFilterValuesAndReads(t *testing.T) {
	p := parseOK(t, good)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) // a Thursday
	_, ps := p.FilterValues(map[string]string{"st": "lost", "when": "fortnight", "nope": "x", "mine": "maybe"}, Viewer{}, now)
	if len(ps) != 4 {
		t.Fatalf("want 4 problems, got %v", ps)
	}
	v, ps := p.FilterValues(map[string]string{"q": "50%", "st": "open", "when": "week", "mine": "on"}, Viewer{Email: "ada@x.test"}, now)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r, err := p.ReadFor(p.Blocks[4], tickets, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(r.Filters)
	for _, want := range []string{
		`{"column":"title","op":"ilike","value":"%50\\%%"}`, // the search, a viewer without table.read: text columns only
		`{"column":"status","op":"ilike","value":"open"}`,
		`{"column":"due","op":"gte","value":"2026-11-16"}`, `{"column":"due","op":"lt","value":"2026-11-23"}`,
		`{"column":"email","op":"ilike","value":"ada@x.test"}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("reads %s, want %s", got, want)
		}
	}
	if strings.Contains(string(got), `"phone"`) {
		t.Errorf("a search by a viewer who may not read personal columns reads them: %s", got)
	}
	// Mine for a viewer with no email reads nothing.
	v.Viewer.Email = ""
	if r, _ := p.ReadFor(p.Blocks[4], tickets, v, nil); !r.None {
		t.Errorf("Mine without an email should match nothing")
	}
	// A press on a column orders by it; a column the table lacks is refused.
	if r, err := p.ReadFor(p.Blocks[5], tickets, v, &Order{Column: "amount_kobo", Desc: true}); err != nil || !strings.Contains(mustJSON(r.Order), `"amount_kobo"`) {
		t.Errorf("order: %v %s", err, mustJSON(r.Order))
	}
	if _, err := p.ReadFor(p.Blocks[5], tickets, v, &Order{Column: "colour"}); err == nil {
		t.Errorf("ordered by a missing column")
	}
}

// Mine on a phone column (g-pages-mine): Check takes it, and a viewer's
// own number is matched as a table may hold it.
func TestMineOnPhone(t *testing.T) {
	doc := strings.Replace(good, `"column": "email", "label": "Mine"`, `"column": "phone", "label": "Mine"`, 1)
	p := parseOK(t, doc)
	if ps := Check(p, Env{Tables: []datatables.Table{tickets}}); len(ps) > 0 {
		t.Fatalf("check: %v", ps)
	}
	v, ps := p.FilterValues(map[string]string{"mine": "on"}, Viewer{Phone: "+2348012345678", Email: "ada@x.test"}, time.Now())
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	r, err := p.ReadFor(p.Blocks[4], tickets, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := mustJSON(r.Filters)
	for _, want := range []string{`"value":"+2348012345678"`, `"value":"2348012345678"`, `"value":"08012345678"`} {
		if !strings.Contains(got, want) {
			t.Errorf("reads %s, want %s", got, want)
		}
	}
	if strings.Contains(got, "ada@x.test") {
		t.Errorf("Mine on a phone column matched the email: %s", got)
	}
	v.Viewer.Phone = ""
	if r, _ := p.ReadFor(p.Blocks[4], tickets, v, nil); !r.None {
		t.Errorf("Mine without a phone should match nothing")
	}
	if got := PhoneForms("+447700900123"); len(got) != 2 {
		t.Errorf("a number from elsewhere has no home form: %v", got)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestShapeMasks(t *testing.T) {
	p := parseOK(t, good)
	rows := []map[string]any{
		{"id": float64(7), "title": "Printer jam", "customer": "Bayo", "status": "open", "phone": "2348012345678", "amount_kobo": float64(2500050), "created_at": "2026-11-19T09:00:00Z"},
		{"id": float64(8), "title": "No ink", "customer": "Ada", "status": "done", "phone": nil},
	}
	staff := Shape(p.Blocks[4], tickets, rows, Values{Viewer: Viewer{Personal: false}})
	if staff[0].Sub == "Bayo · 2348012345678" || !strings.Contains(staff[0].Sub, "5678") || strings.Contains(staff[0].Sub, "80123") {
		t.Errorf("phone not masked for a viewer without table.read: %q", staff[0].Sub)
	}
	if staff[0].State.Tone != "warn" || staff[1].State.Tone != "good" || staff[0].Group != "open" {
		t.Errorf("state: %+v %+v", staff[0], staff[1])
	}
	if staff[0].Detail.Lines[1].Value != "₦25,000.50" {
		t.Errorf("amount: %+v", staff[0].Detail.Lines)
	}
	owner := Shape(p.Blocks[4], tickets, rows, Values{Viewer: Viewer{Personal: true}})
	if owner[0].Sub != "Bayo · 2348012345678" {
		t.Errorf("a viewer with table.read sees the column: %q", owner[0].Sub)
	}
	tbl := Shape(p.Blocks[5], tickets, rows, Values{})
	if strings.Join(tbl[0].Cells, "|") != "Printer jam|₦25,000.50" || tbl[1].Cells[1] != "" {
		t.Errorf("cells: %v %v", tbl[0].Cells, tbl[1].Cells)
	}
	if g := Groups(p.Blocks[4], []Shown{{Group: "done"}, {Group: "open"}, {Group: ""}}); strings.Join(g, ",") != "open,done," {
		t.Errorf("groups: %q", g)
	}
}

func TestDatesAndChange(t *testing.T) {
	now := time.Date(2026, 11, 1, 23, 30, 0, 0, time.UTC) // already 2 November in Lagos
	from, to, ok := DateRange("today", now)
	if !ok || from.Format("2006-01-02") != "2026-11-02" || to.Sub(from) != 24*time.Hour {
		t.Errorf("today: %v %v", from, to)
	}
	from, _, _ = DateRange("month", now)
	if from.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("month: %v", from)
	}
	if _, _, ok := DateRange("2026-11-30..2026-11-01", now); ok {
		t.Errorf("a range backwards was taken")
	}
	_, _, prev := Period("week", now)
	if prev.Format("2006-01-02") != "2026-10-26" {
		t.Errorf("last week from %v", prev)
	}
	for _, c := range []struct {
		now, before float64
		want        string
	}{{12, 10, "up 20% on last week"}, {9, 10, "down 10% on last week"}, {3, 3, "the same as last week"}, {2, 0, "up from none last week"}} {
		if got := ChangeWords("week", c.now, c.before); got != c.want {
			t.Errorf("%v on %v: %q", c.now, c.before, got)
		}
	}
	if FigureShown(Block{}, 12345) != "12,345" || FigureShown(Block{Naira: true}, 250000) != "₦2,500" {
		t.Errorf("figures: %s %s", FigureShown(Block{}, 12345), FigureShown(Block{Naira: true}, 250000))
	}
}

func TestFromTable(t *testing.T) {
	p := FromTable(tickets)
	doc := p.Marshal()
	q, ps := Parse(doc)
	if len(ps) > 0 {
		t.Fatalf("%s: %v", doc, ps)
	}
	if ps := Check(q, Env{Tables: []datatables.Table{tickets}}); len(ps) > 0 {
		t.Fatalf("%v", ps)
	}
	if q.Name != "tickets" || q.Blocks[0].Value != "count" || q.Blocks[2].Type != "list" || q.Blocks[2].Group == nil || q.Blocks[2].Group.Column != "status" {
		t.Errorf("%s", doc)
	}
	if PageName("stock") != "stock-page" || PageName("a_very_long_table_name_indeed_yes") != "a-very-long-table-name-i" {
		t.Errorf("names: %s %s", PageName("stock"), PageName("a_very_long_table_name_indeed_yes"))
	}
	// A table of numbers only still makes a page.
	bare := datatables.Table{Name: "readings", Columns: []datatables.Column{{Name: "litres", Type: "number"}}}
	if _, ps := Parse(FromTable(bare).Marshal()); len(ps) > 0 {
		t.Errorf("%v", ps)
	}
}

// people has personal columns of each kind a page could narrow, add up
// or rank by.
var people = datatables.Table{Name: "people", Title: "People", Columns: []datatables.Column{
	{Name: "name", Title: "Name", Type: "text", Personal: true},
	{Name: "team", Title: "Team", Type: "text"},
	{Name: "born", Title: "Born", Type: "date", Personal: true},
	{Name: "salary", Title: "Salary", Type: "integer", Personal: true},
	{Name: "joined", Title: "Joined", Type: "date"},
}}

// Nothing on a page narrows, adds up or compares by a personal column
// (pages §8, §18): Check refuses it, as it refuses a group by one.
func TestPersonalColumnsRefused(t *testing.T) {
	doc := func(filters, blocks string) *Page {
		return parseOK(t, `{"page": "page/v1", "name": "team-desk", "title": "Staff", "filters": [`+filters+`], "blocks": [`+blocks+`]}`)
	}
	list := func(uses string) string {
		return `{"id": "l", "type": "list", "source": {"table": "people", "uses": [` + uses + `]}, "item": {"title": "=row.team"}}`
	}
	for _, c := range []struct {
		name, path, says string
		page             *Page
	}{
		{"dates on a personal column", "/blocks/0/source/uses/0", "Born is personal", doc(`{"id": "when", "type": "dates", "column": "born"}`, list(`"when"`))},
		{"choice on a personal column", "/blocks/0/source/uses/0", "Name is personal", doc(`{"id": "who", "type": "choice", "column": "name", "options": [{"value": "Ada"}]}`, list(`"who"`))},
		{"sum of a personal column", "/blocks/0/of", "cannot add up Salary: it is personal", doc(``, `{"id": "f", "type": "figure", "label": "Pay", "value": "sum", "of": "salary", "source": {"table": "people"}}`)},
		{"change by a personal date", "/blocks/0/change/column", "cannot compare by Born: it is personal", doc(``, `{"id": "f", "type": "figure", "label": "Birthdays", "value": "count", "change": {"column": "born", "period": "week"}, "source": {"table": "people"}}`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			ps := Check(c.page, Env{Tables: []datatables.Table{people}})
			if len(ps) != 1 || ps[0].Path != c.path || !strings.Contains(ps[0].Message, c.says) {
				t.Fatalf("want %s %q, got %v", c.path, c.says, ps)
			}
		})
	}
	// The same over columns that are not personal is fine.
	ok := doc(`{"id": "when", "type": "dates", "column": "joined"}, {"id": "t", "type": "choice", "column": "team", "options": [{"value": "a"}]}`,
		`{"id": "f", "type": "figure", "label": "Joined", "value": "count", "change": {"column": "joined", "period": "week"}, "source": {"table": "people"}}, `+list(`"when", "t"`))
	if ps := Check(ok, Env{Tables: []datatables.Table{people}}); len(ps) > 0 {
		t.Fatalf("%v", ps)
	}
}

// A figure stays whole: Parse refuses uses on one (and the schema too),
// and ReadFor ignores it besides.
func TestFigureStaysWhole(t *testing.T) {
	doc := edit(t, func(m map[string]any) { block(m, 2)["source"].(map[string]any)["uses"] = []any{"q"} })
	_, ps := Parse([]byte(doc))
	if len(ps) != 1 || ps[0].Path != "/blocks/2/source/uses" || !strings.Contains(ps[0].Message, "narrow its lists and tables only") {
		t.Fatalf("%v", ps)
	}
	if len(schema.Validate([]byte(doc))) == 0 {
		t.Errorf("the schema took uses on a figure")
	}
	p := parseOK(t, good)
	p.Blocks[2].Source.Uses = []string{"q", "st"}
	v, _ := p.FilterValues(map[string]string{"q": "ink", "st": "open"}, Viewer{}, time.Now())
	r, err := p.ReadFor(p.Blocks[2], tickets, v, nil)
	if err != nil || len(r.Filters) != 0 {
		t.Errorf("a figure narrowed by the page's filters: %v %s", err, mustJSON(r.Filters))
	}
}

// Defence in depth: a published page Check would now refuse still never
// narrows by a personal column for a viewer without table.read, nor puts
// rows in order by one.
func TestReadForPersonal(t *testing.T) {
	p := parseOK(t, `{"page": "page/v1", "name": "team-desk", "title": "Staff",
	  "filters": [{"id": "when", "type": "dates", "column": "born"}, {"id": "who", "type": "choice", "column": "name", "options": [{"value": "Ada"}]}],
	  "blocks": [{"id": "l", "type": "table", "source": {"table": "people", "uses": ["when", "who"]}, "columns": [{"column": "name"}, {"column": "team"}]}]}`)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	set := map[string]string{"when": "1990-01-01..1990-01-01", "who": "Ada"}
	staff, _ := p.FilterValues(set, Viewer{}, now)
	r, err := p.ReadFor(p.Blocks[0], people, staff, nil)
	if err != nil || len(r.Filters) != 0 {
		t.Errorf("a viewer without table.read narrowed by personal columns: %v %s", err, mustJSON(r.Filters))
	}
	owner, _ := p.FilterValues(set, Viewer{Personal: true}, now)
	if r, _ := p.ReadFor(p.Blocks[0], people, owner, nil); !strings.Contains(mustJSON(r.Filters), "born") || !strings.Contains(mustJSON(r.Filters), "Ada") {
		t.Errorf("table.read: %s", mustJSON(r.Filters))
	}
	tables := []datatables.Table{people}
	if !p.HiddenFilter(p.Filters[0], tables, false) || !p.HiddenFilter(p.Filters[1], tables, false) || p.HiddenFilter(p.Filters[1], tables, true) {
		t.Errorf("a personal choice or dates filter is drawn for a viewer without table.read")
	}
	if _, err := p.ReadFor(p.Blocks[0], people, staff, &Order{Column: "name"}); !errors.Is(err, ErrPersonalOrder) {
		t.Errorf("ordered by a personal column: %v", err)
	}
	if _, err := p.ReadFor(p.Blocks[0], people, owner, &Order{Column: "name"}); err != nil {
		t.Errorf("table.read may order by it: %v", err)
	}
}

// More's cursor is Taskiem's: taken back only for the order it was given
// for, and for rows ranked by a personal column, only a skip.
func TestCursor(t *testing.T) {
	b := Block{Type: "list", Source: &Source{Table: "people", Order: []Order{{Column: "name"}}}}
	if !RankedByPersonal(b, people, nil, false) || RankedByPersonal(b, people, nil, true) || RankedByPersonal(b, people, &Order{Column: "team"}, false) {
		t.Errorf("ranked")
	}
	byTeam := &Order{Column: "team", Desc: true}
	keyset := Cursor{Order: OrderKey(byTeam), After: "abc"}.String()
	if c, err := ReadCursor(keyset, byTeam, false); err != nil || c.After != "abc" {
		t.Errorf("keyset: %v %v", c, err)
	}
	for _, bad := range []struct {
		s      string
		order  *Order
		ranked bool
	}{
		{keyset, &Order{Column: "team"}, false},          // another order
		{keyset, nil, false},                             // the part's own order
		{"abc", byTeam, false},                           // the data API's own cursor, as is
		{Cursor{Skip: 25}.String(), nil, false},          // a skip where a cursor is due
		{Cursor{After: "x"}.String(), nil, true},         // a data API cursor for rows ranked by a personal column
		{Cursor{Skip: MaxSkip + 25}.String(), nil, true}, // past the last
		{Cursor{Skip: -1}.String(), nil, true},           // before the first
	} {
		if _, err := ReadCursor(bad.s, bad.order, bad.ranked); err == nil {
			t.Errorf("took %q for %v ranked=%v", bad.s, bad.order, bad.ranked)
		}
	}
	if c, err := ReadCursor(Cursor{Skip: 25}.String(), nil, true); err != nil || c.Skip != 25 {
		t.Errorf("skip: %v %v", c, err)
	}
}
