package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var ticketsTable = map[string]any{"name": "tickets", "title": "Tickets", "columns": []map[string]any{
	{"name": "title", "title": "Title", "type": "text", "required": true},
	{"name": "customer", "title": "Customer", "type": "text"},
	{"name": "status", "title": "Status", "type": "text"},
	{"name": "phone", "title": "Phone", "type": "phone"},
	{"name": "amount_kobo", "title": "Amount", "type": "integer"},
	{"name": "opened", "title": "Opened", "type": "date"},
}}

func viewOf(t *testing.T, c *client, name string, filters map[string]any, extra ...string) map[string]any {
	t.Helper()
	q := url.Values{}
	if filters != nil {
		b, _ := json.Marshal(filters)
		q.Set("filters", string(b))
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return c.must(200, "GET", "/v1/pages/"+name+"/view?"+q.Encode(), nil)
}

func blockOf(t *testing.T, view map[string]any, id string) map[string]any {
	t.Helper()
	for _, b := range view["blocks"].([]any) {
		if m := b.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func titles(b map[string]any) []string {
	var out []string
	for _, r := range b["rows"].([]any) {
		out = append(out, r.(map[string]any)["title"].(string))
	}
	return out
}

// The plan's G1, done when: an owner turns the tickets table into a page
// with a count and a list, publishes it, and staff open it. Held to the
// page's roles, masked for those without table.read, filtered on the
// server, the draft apart from what is published.
func TestPagesFromATable(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	support := addMember(t, w.world, owner, "sam", "support") // a business role: no permission at all
	viewer := addMember(t, w.world, owner, "vic", "viewer")   // table.read, not support
	cashier := addMember(t, w.world, owner, "cas", "cashier")

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)
	id := tbl["id"].(string)
	lagos, _ := time.LoadLocation("Africa/Lagos")
	today := time.Now().In(lagos)
	lastWeek := today.AddDate(0, 0, -7).Format("2006-01-02")
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "customer": "Bayo", "status": "open", "phone": "2348012345678", "amount_kobo": 250000, "opened": today.Format("2006-01-02")},
		{"title": "No ink", "customer": "Ada", "status": "done", "phone": "2348098765432", "amount_kobo": 100000, "opened": lastWeek},
		{"title": "Screen dark", "customer": "Chi", "status": "open", "amount_kobo": 50, "opened": today.Format("2006-01-02")},
	}})

	// Who may make one.
	if s, _ := support.do("POST", "/v1/pages", map[string]any{"from_table": id}); s != 403 {
		t.Errorf("support made a page: %d", s)
	}
	if s, _ := cashier.do("POST", "/v1/pages", map[string]any{"from_table": id}); s != 403 {
		t.Errorf("a cashier (table.edit, no page.edit) made a page: %d", s)
	}

	// Show as a page: one call makes a draft with a count and a list.
	pg := owner.must(201, "POST", "/v1/pages", map[string]any{"from_table": id})
	if pg["name"] != "tickets" || pg["published"] != false || pg["has_draft"] != true || len(pg["problems"].([]any)) != 0 {
		t.Fatalf("made: %v", pg)
	}
	if !strings.Contains(strings.Join(toStrings(pg["shows"]), "|"), "Tickets: Title, Customer, Status") {
		t.Errorf("shows: %v", pg["shows"])
	}
	// A second from the same table takes the next name.
	if again := owner.must(201, "POST", "/v1/pages", map[string]any{"from_table": id}); again["name"] != "tickets-2" {
		t.Errorf("second: %v", again["name"])
	}
	owner.must(204, "DELETE", "/v1/pages/tickets-2", nil)

	// Not published: staff cannot see it; the owner previews the draft.
	if s, _ := support.do("GET", "/v1/pages/tickets/view", nil); s != 404 {
		t.Errorf("staff opened a draft: %d", s)
	}
	if list := support.must(200, "GET", "/v1/pages", nil); len(list["pages"].([]any)) != 0 {
		t.Errorf("staff list a draft: %v", list)
	}
	draft := viewOf(t, owner, "tickets", nil, "draft", "1")
	if blockOf(t, draft, "all")["shown"] != "3" || draft["draft"] != true {
		t.Fatalf("draft view: %v", draft)
	}

	// Roles: support only. Then publish.
	var doc map[string]any
	_ = json.Unmarshal(mustJSONBytes(pg["draft"]), &doc)
	doc["roles"] = []string{"support"}
	owner.must(200, "PUT", "/v1/pages/tickets/draft", map[string]any{"document": doc})
	if s, out := support.do("POST", "/v1/pages/tickets/publish", nil); s != 403 {
		t.Errorf("support published: %d %v", s, out)
	}
	pub := owner.must(200, "POST", "/v1/pages/tickets/publish", nil)
	if pub["published"] != true || pub["version"] != float64(1) || pub["has_draft"] != false {
		t.Fatalf("published: %v", pub)
	}
	owner.must(200, "PUT", "/v1/pages/tickets/pin", map[string]any{"pinned": true})

	// Staff open it: the count, the list grouped by status, the phone masked
	// for someone without table.read.
	list := support.must(200, "GET", "/v1/pages", nil)["pages"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["pinned"] != true {
		t.Fatalf("staff list: %v", list)
	}
	v := viewOf(t, support, "tickets", nil)
	if blockOf(t, v, "all")["shown"] != "3" {
		t.Errorf("count: %v", blockOf(t, v, "all"))
	}
	// (The stand-in Xenibase keeps no created_at: the change is tested below
	// by a date column of the table's own.)
	if wk := blockOf(t, v, "this_week"); !strings.Contains(wk["change"].(map[string]any)["words"].(string), "last week") {
		t.Errorf("this week: %v", wk)
	}
	rows := blockOf(t, v, "rows")
	if g := rows["groups"].([]any); len(g) != 2 || g[0].(map[string]any)["label"] != "Open" {
		t.Errorf("groups: %v", g)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "2348012345678") || !strings.Contains(string(raw), "5678") {
		t.Errorf("staff without table.read see a phone unmasked: %s", raw)
	}
	// The owner holds table.read: the card shows it.
	if !strings.Contains(mustString(t, viewOf(t, owner, "tickets", nil)), "2348012345678") {
		t.Errorf("the owner's card has no phone")
	}
	// A viewer outside the page's roles cannot open it, whatever table.read.
	if s, _ := viewer.do("GET", "/v1/pages/tickets/view", nil); s != 404 {
		t.Errorf("a viewer outside the roles opened it: %d", s)
	}

	// Filters are applied on the server.
	found := blockOf(t, viewOf(t, support, "tickets", map[string]any{"q": "ink"}), "rows")
	if got := titles(found); len(got) != 1 || got[0] != "No ink" || found["count"] != float64(1) {
		t.Errorf("search: %v", found)
	}
	// Staff cannot search the personal column they cannot read.
	if got := titles(blockOf(t, viewOf(t, support, "tickets", map[string]any{"q": "80123"}), "rows")); len(got) != 0 {
		t.Errorf("a search reached a masked column: %v", got)
	}
	if s, out := support.do("GET", "/v1/pages/tickets/view?filters="+url.QueryEscape(`{"nope":"x"}`), nil); s != 400 || !strings.Contains(out["error"].(string), "no filter nope") {
		t.Errorf("unknown filter: %d %v", s, out)
	}

	// A draft changed after publishing: staff still see the published page.
	doc["title"] = "Support tickets"
	doc["blocks"] = append(doc["blocks"].([]any), map[string]any{"id": "owed", "type": "figure", "label": "Owed",
		"source": map[string]any{"table": "tickets", "filters": []any{map[string]any{"column": "status", "op": "eq", "value": "open"}}},
		"value":  "sum", "of": "amount_kobo", "naira": true},
		map[string]any{"id": "opened_week", "type": "figure", "label": "Opened this week", "source": map[string]any{"table": "tickets"},
			"value": "count", "change": map[string]any{"column": "opened", "period": "week"}})
	owner.must(200, "PUT", "/v1/pages/tickets/draft", map[string]any{"document": doc})
	if wk := blockOf(t, viewOf(t, owner, "tickets", nil, "draft", "1"), "opened_week"); wk["shown"] != "2" || wk["change"].(map[string]any)["words"] != "up 100% on last week" {
		t.Errorf("opened this week: %v", wk)
	}
	if v := viewOf(t, support, "tickets", nil); v["page"].(map[string]any)["title"] != "Tickets" || blockOf(t, v, "owed") != nil {
		t.Errorf("staff see the draft: %v", v["page"])
	}
	if v := viewOf(t, support, "tickets", nil, "draft", "1"); v["draft"] != false {
		t.Errorf("staff got the draft by asking")
	}
	if owed := blockOf(t, viewOf(t, owner, "tickets", nil, "draft", "1"), "owed"); owed["shown"] != "₦2,500.50" {
		t.Errorf("sum: %v", owed)
	}
	owner.must(200, "POST", "/v1/pages/tickets/publish", nil)
	if v := viewOf(t, support, "tickets", nil); blockOf(t, v, "owed")["shown"] != "₦2,500.50" {
		t.Errorf("after publishing: %v", v)
	}
	if s, _ := owner.do("POST", "/v1/pages/tickets/publish", nil); s != 409 {
		t.Errorf("published with no draft: %d", s)
	}

	// More rows: a table part ordered by a press on a column.
	doc["blocks"] = append(doc["blocks"].([]any), map[string]any{"id": "all_rows", "type": "table", "source": map[string]any{"table": "tickets"},
		"columns": []any{map[string]any{"column": "title"}, map[string]any{"column": "amount_kobo", "naira": true}}})
	owner.must(200, "PUT", "/v1/pages/tickets/draft", map[string]any{"document": doc})
	tb := owner.must(200, "GET", "/v1/pages/tickets/blocks/all_rows?draft=1&order=amount_kobo&desc=1", nil)
	if r0 := tb["rows"].([]any)[0].(map[string]any)["cells"].([]any); r0[0] != "Printer jam" || r0[1] != "₦2,500" {
		t.Errorf("ordered: %v", tb["rows"])
	}
	if s, _ := owner.do("GET", "/v1/pages/tickets/blocks/all_rows?draft=1&order=phone", nil); s != 400 {
		t.Errorf("ordered by a column the table part does not show: %d", s)
	}

	// Publishing refuses problems, in plain words.
	doc["blocks"] = append(doc["blocks"].([]any), map[string]any{"id": "gone", "type": "figure", "label": "Gone",
		"source": map[string]any{"table": "nope"}, "value": "count"})
	owner.must(200, "PUT", "/v1/pages/tickets/draft", map[string]any{"document": doc})
	if s, out := owner.do("POST", "/v1/pages/tickets/publish", nil); s != 400 || !strings.Contains(mustString(t, out), "which the business does not have") {
		t.Errorf("published a page with a missing table: %d %v", s, out)
	}
	// The broken part is marked for its editor and left out for staff.
	if b := blockOf(t, viewOf(t, owner, "tickets", nil, "draft", "1"), "gone"); b == nil || !strings.Contains(b["broken"].(string), "needs fixing") {
		t.Errorf("editor: %v", b)
	}

	// A reserved word, and a bad document, are refused at once.
	if s, out := owner.do("POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": "stock", "title": "Stock",
		"blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}}); s != 400 || !strings.Contains(mustString(t, out), "already uses") {
		t.Errorf("reserved: %d %v", s, out)
	}
	if out := owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": map[string]any{"page": "page/v1", "name": "x1", "title": "X",
		"blocks": []any{map[string]any{"id": "b", "type": "carousel"}}}}); !strings.Contains(mustString(t, out), "does not draw a part") {
		t.Errorf("validate: %v", out)
	}

	// Pins: five at most, published pages only.
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5"} {
		owner.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": n, "title": n,
			"blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}})
		if n == "a1" {
			if s, _ := owner.do("PUT", "/v1/pages/a1/pin", map[string]any{"pinned": true}); s != 409 {
				t.Errorf("pinned a draft: %d", s)
			}
		}
		owner.must(200, "POST", "/v1/pages/"+n+"/publish", nil)
		if n != "a5" {
			owner.must(200, "PUT", "/v1/pages/"+n+"/pin", map[string]any{"pinned": true})
		}
	}
	if s, out := owner.do("PUT", "/v1/pages/a5/pin", map[string]any{"pinned": true}); s != 409 || !strings.Contains(out["error"].(string), "unpin one") {
		t.Errorf("sixth pin: %d %v", s, out)
	}

	// The plan's limit counts drafts.
	if err := w.env.Store.SetLimits(context.Background(), tenantIDNamed(t, w.world, "Acme"), map[string]any{"max_pages": 6}, "operator"); err != nil {
		t.Fatal(err)
	}
	if s, out := owner.do("POST", "/v1/pages", map[string]any{"from_table": id}); s != 429 || out["limit"] != "max_pages" {
		t.Errorf("over the limit: %d %v", s, out)
	}

	// Every change is audited, with what a page shows and never a row.
	actions := map[string]int{}
	rowsQ, err := w.env.DB.Admin.Query(context.Background(), `SELECT action, detail::text FROM audit_log WHERE action LIKE 'page.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsQ.Close()
	for rowsQ.Next() {
		var a, d string
		_ = rowsQ.Scan(&a, &d)
		actions[a]++
		if strings.Contains(d, "Printer jam") || strings.Contains(d, "2348012345678") {
			t.Errorf("audit %s carries a row: %s", a, d)
		}
	}
	for _, a := range []string{"page.create", "page.draft", "page.publish", "page.pin", "page.delete"} {
		if actions[a] == 0 {
			t.Errorf("no %s in the audit log: %v", a, actions)
		}
	}
}

// Another business's page is never reached.
func TestPagesIsolation(t *testing.T) {
	w := newTablesWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	other := w.tenant(t, "Other", "owner@other.test")
	acme.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": "secret", "title": "Secret",
		"blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}})
	acme.must(200, "POST", "/v1/pages/secret/publish", nil)
	for _, path := range []string{"/v1/pages/secret", "/v1/pages/secret/view"} {
		if s, _ := other.do("GET", path, nil); s != 404 {
			t.Errorf("%s from another business: %d", path, s)
		}
	}
	if list := other.must(200, "GET", "/v1/pages", nil); len(list["pages"].([]any)) != 0 {
		t.Errorf("listed: %v", list)
	}
	// The same name is free in another business.
	other.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": "secret", "title": "Mine",
		"blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}})
}

func mustJSONBytes(v any) []byte { b, _ := json.Marshal(v); return b }

func mustString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var staffTable = map[string]any{"name": "staff", "title": "Staff", "columns": []map[string]any{
	{"name": "name", "title": "Name", "type": "text", "personal": true},
	{"name": "team", "title": "Team", "type": "text"},
	{"name": "born", "title": "Born", "type": "date", "personal": true},
	{"name": "salary", "title": "Salary", "type": "integer", "personal": true},
}}

// A viewer without table.read is never shown a personal column by a page
// (§8, §18): not by a dates or choice filter on it, a sum of it, an order
// by it, or More's cursor. Pages that would are refused at publishing, and
// one published before the rule draws without those parts.
func TestPagesPersonalColumns(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	support := addMember(t, w.world, owner, "sam", "support") // opens pages; no table.read
	id := owner.must(201, "POST", "/v1/tables", staffTable)["id"].(string)
	var rows []map[string]any
	for i := 0; i < 30; i++ {
		rows = append(rows, map[string]any{"name": fmt.Sprintf("Person %02d", i), "team": []string{"desk", "floor"}[i%2],
			"born": fmt.Sprintf("1990-01-%02d", i%28+1), "salary": 1000 + i})
	}
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"rows": rows})

	risky := map[string]any{"page": "page/v1", "name": "people", "title": "People", "roles": []string{"support"},
		"filters": []any{map[string]any{"id": "when", "type": "dates", "column": "born"},
			map[string]any{"id": "who", "type": "choice", "column": "name", "options": []any{map[string]any{"value": "Person 03"}}}},
		"blocks": []any{
			map[string]any{"id": "pay", "type": "figure", "label": "Pay", "value": "sum", "of": "salary", "source": map[string]any{"table": "staff"}},
			map[string]any{"id": "found", "type": "list", "source": map[string]any{"table": "staff", "uses": []any{"when", "who"}}, "item": map[string]any{"title": "=row.team"}},
			map[string]any{"id": "all", "type": "table", "source": map[string]any{"table": "staff", "order": []any{map[string]any{"column": "name"}}},
				"columns": []any{map[string]any{"column": "name"}, map[string]any{"column": "team"}}},
		}}
	// Saved as a draft with its problems; publishing refuses it.
	pg := owner.must(201, "POST", "/v1/pages", map[string]any{"document": risky})
	says := mustString(t, pg["problems"])
	for _, want := range []string{"cannot add up Salary: it is personal", "Born is personal", "Name is personal"} {
		if !strings.Contains(says, want) {
			t.Errorf("problems lack %q: %s", want, says)
		}
	}
	if s, out := owner.do("POST", "/v1/pages/people/publish", nil); s != 400 {
		t.Fatalf("published: %d %v", s, out)
	}
	// Figures stay whole: uses on one is refused outright.
	figUses := map[string]any{"page": "page/v1", "name": "fig", "title": "Fig", "filters": []any{map[string]any{"id": "q", "type": "search"}},
		"blocks": []any{map[string]any{"id": "n", "type": "figure", "label": "N", "value": "count", "source": map[string]any{"table": "staff", "uses": []any{"q"}}}}}
	if s, out := owner.do("POST", "/v1/pages", map[string]any{"document": figUses}); s != 400 || !strings.Contains(mustString(t, out), "lists and tables only") {
		t.Errorf("uses on a figure: %d %v", s, out)
	}

	// Published before the rule (written as it would have been): the parts
	// that would show a personal column are left out for staff.
	safe := map[string]any{"page": "page/v1", "name": "people", "title": "People", "roles": []string{"support"},
		"blocks": []any{risky["blocks"].([]any)[2]}}
	owner.must(200, "PUT", "/v1/pages/people/draft", map[string]any{"document": safe})
	owner.must(200, "POST", "/v1/pages/people/publish", nil)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE page_versions SET document = $1 WHERE version = 1 AND page_id = (SELECT id FROM pages WHERE name = 'people')`, mustJSONBytes(risky)); err != nil {
		t.Fatal(err)
	}
	v := viewOf(t, support, "people", map[string]any{"when": "1990-01-04..1990-01-04", "who": "Person 03"})
	if blockOf(t, v, "pay") != nil || blockOf(t, v, "found") != nil {
		t.Errorf("staff were drawn a personal sum or a personal filter: %s", mustString(t, v["blocks"]))
	}
	raw := mustString(t, v)
	if strings.Contains(raw, "Person 03") || strings.Contains(raw, "1003") {
		t.Errorf("staff see a personal value: %s", raw)
	}

	// Order: staff cannot put the rows in order by a personal column.
	if s, out := support.do("GET", "/v1/pages/people/blocks/all?order=name", nil); s != 400 || !strings.Contains(out["error"].(string), "personal") {
		t.Errorf("staff ordered by a personal column: %d %v", s, out)
	}
	support.must(200, "GET", "/v1/pages/people/blocks/all?order=team", nil)
	owner.must(200, "GET", "/v1/pages/people/blocks/all?order=name", nil)

	// More: the part's own order is by a personal column, so staff page on
	// by a skip of the rows shown; the data API's cursor is never taken
	// from them, and a cursor holds only for the order it was given for.
	all := blockOf(t, v, "all")
	more, _ := all["more"].(string)
	if len(all["rows"].([]any)) != 25 || more == "" {
		t.Fatalf("first rows: %v", all)
	}
	next := support.must(200, "GET", "/v1/pages/people/blocks/all?cursor="+url.QueryEscape(more), nil)
	if len(next["rows"].([]any)) != 5 || next["more"] != nil {
		t.Errorf("more: %v", next)
	}
	ownMore := blockOf(t, viewOf(t, owner, "people", nil), "all")["more"].(string)
	if s, _ := support.do("GET", "/v1/pages/people/blocks/all?cursor="+url.QueryEscape(ownMore), nil); s != 400 {
		t.Errorf("staff used a data API cursor over a personal order: %d", s)
	}
	if got := owner.must(200, "GET", "/v1/pages/people/blocks/all?cursor="+url.QueryEscape(ownMore), nil); len(got["rows"].([]any)) != 5 {
		t.Errorf("owner's more: %v", got)
	}
	if s, _ := owner.do("GET", "/v1/pages/people/blocks/all?order=team&cursor="+url.QueryEscape(ownMore), nil); s != 400 {
		t.Errorf("a cursor taken for another order: %d", s)
	}
}

// Pins stop at five however many are asked at once; a pin that changes
// nothing is not audited. Four-eyes: the person who saved a page's draft
// does not publish it. Deleting a published page needs page.publish.
func TestPagesPinsPublishDelete(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	adm := addMember(t, w.world, owner, "ada", "admin")
	owner.must(201, "PUT", "/v1/roles/page_maker", map[string]any{"permissions": []string{"page.edit"}})
	maker := addMember(t, w.world, owner, "max", "page_maker", "builder") // page.edit and table.read, not page.publish
	heading := func(n string) map[string]any {
		return map[string]any{"document": map[string]any{"page": "page/v1", "name": n, "title": n,
			"blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}}
	}
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}
	for _, n := range names {
		owner.must(201, "POST", "/v1/pages", heading(n))
		owner.must(200, "POST", "/v1/pages/"+n+"/publish", nil)
	}
	var wg sync.WaitGroup
	codes := make([]int, len(names))
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = owner.do("PUT", "/v1/pages/"+n+"/pin", map[string]any{"pinned": true})
		}()
	}
	wg.Wait()
	pinned := 0
	for _, c := range codes {
		if c == 200 {
			pinned++
		} else if c != 409 {
			t.Errorf("pin answered %d", c)
		}
	}
	var n, distinct int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*), count(DISTINCT pin_order) FROM pages WHERE pinned`).Scan(&n, &distinct); err != nil {
		t.Fatal(err)
	}
	if pinned != 5 || n != 5 || distinct != 5 {
		t.Errorf("pinned %d (stored %d, %d places): %v", pinned, n, distinct, codes)
	}
	audits := func() int {
		var k int
		_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'page.pin'`).Scan(&k)
		return k
	}
	before := audits()
	owner.must(200, "PUT", "/v1/pages/p8/pin", map[string]any{"pinned": false}) // whether or not it was pinned
	owner.must(200, "PUT", "/v1/pages/p8/pin", map[string]any{"pinned": false})
	if got := audits() - before; got > 1 {
		t.Errorf("a pin that changed nothing was audited: %d", got)
	}

	// Four-eyes.
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	adm.must(201, "POST", "/v1/pages", heading("eyes"))
	if s, out := adm.do("POST", "/v1/pages/eyes/publish", nil); s != 403 || !strings.Contains(out["error"].(string), "another person") {
		t.Errorf("published their own draft under four-eyes: %d %v", s, out)
	}
	owner.must(200, "POST", "/v1/pages/eyes/publish", nil)
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": false})
	adm.must(201, "POST", "/v1/pages", heading("solo"))
	adm.must(200, "POST", "/v1/pages/solo/publish", nil)

	// Deleting.
	if s, out := maker.do("DELETE", "/v1/pages/solo", nil); s != 403 {
		t.Errorf("page.edit alone deleted a published page: %d %v", s, out)
	}
	adm.must(204, "DELETE", "/v1/pages/solo", nil)
	maker.must(201, "POST", "/v1/pages", heading("scratch"))
	maker.must(204, "DELETE", "/v1/pages/scratch", nil)
}
