package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
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
		"blocks": []any{map[string]any{"id": "b", "type": "board"}}}}); !strings.Contains(mustString(t, out), "does not draw a part") {
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
