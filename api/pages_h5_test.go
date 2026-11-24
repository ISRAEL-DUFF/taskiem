package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// g-pages-notif: a Tell me message carries a button that opens a page
// filtered to today; drawn only for someone the page's roles let open
// it; pressed, the page's short form with that filter and its link with
// it; someone else pressing it is told they cannot open it.
func TestPagesFromANotification(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newWAWorld(t)
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@h5notif.test")
	publishWF(t, owner, "Disk alerts", toolDef("wf_disks", "Disk alerts", "disk-alerts", "", `{"day":{"type":"string","title":"Day"}}`,
		`[{"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"text","text":"done"}`,
		`{"id":"look","type":"transform","config":{"output":{"rows":[{"name":"=\"db-2 full \" + trigger.body.day"}]}}}`))
	doc := map[string]any{"page": "page/v1", "name": "disks", "title": "Disks", "roles": []string{"operator"},
		"filters": []map[string]any{{"id": "day", "type": "search", "label": "Day"}},
		"blocks": []map[string]any{
			{"id": "list", "type": "list", "title": "Alerts", "source": map[string]any{"tool": "disk-alerts", "output": "rows", "inputs": map[string]any{"day": "=filters.day"}},
				"item": map[string]any{"title": "=item.name"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/disks/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}
	op := w.member(t, owner, "op@h5notif.test", "operator")
	bo := w.member(t, owner, "bo@h5notif.test", "builder") // not in the page's roles
	const on, bn = "+2348010000061", "+2348010000062"
	w.bind(t, op, on)
	w.bind(t, bo, bn)
	// Each has written in the last day: WhatsApp's window is open, so a
	// message carries its buttons.
	w.say(t, on, "help")
	w.say(t, bn, "help")

	// The validator holds a page button's shape.
	bad := `{"schema":"wd/v1","id":"wf_badtell","version":1,"name":"Bad tell","trigger":{"type":"manual"},
	  "steps":[{"id":"tell","type":"connector","connector":"notify@1","action":"tell",
	    "input":{"message":"x","buttons":[{"label":"See","page":"Disks!","once":true}]}}]}`
	if s, out := owner.do("POST", "/v1/workflows", map[string]any{"name": "Bad tell", "definition": json.RawMessage(bad)}); s == 201 {
		wf := out["id"].(string)
		if s, out := owner.do("POST", "/v1/workflows/"+wf+"/versions/1/publish", nil); s == 200 || !strings.Contains(toJSON(out), "short name") {
			t.Errorf("a bad page button published: %d %v", s, out)
		}
	} else if !strings.Contains(toJSON(out), "short name") || !strings.Contains(toJSON(out), "no once") {
		t.Errorf("a bad page button: %d %v", s, out)
	}

	tell := `{"schema":"wd/v1","id":"wf_tellpage","version":1,"name":"Disk full","trigger":{"type":"manual"},
	  "steps":[{"id":"tell","type":"connector","connector":"notify@1","action":"tell",
	    "input":{"to":"members","members":["op@h5notif.test","bo@h5notif.test"],"message":"db-2 is nearly full",
	      "buttons":[{"label":"See today's","page":"disks","filters":{"day":"today"}}]}}]}`
	wf := publishFlow(t, owner, tell)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	got := w.graph.Last(t, on)
	if !strings.Contains(got.Text, "db-2 is nearly full") || len(got.Buttons) != 1 || !strings.HasPrefix(got.Buttons[0].ID, "pg:") || got.Buttons[0].Title != "See today's" {
		t.Fatalf("the operator's message: %+v", got)
	}
	if theirs := w.graph.Last(t, bn); len(theirs.Buttons) != 0 {
		t.Errorf("a builder was given the page's button: %+v", theirs)
	}

	// Pressed: the page's short form, filtered to today, and its link with it.
	pressed := w.send(t, on, whatsapptest.ButtonReply(got.Buttons[0].ID, "See today's"))
	if len(pressed) != 1 {
		t.Fatalf("pressed: %+v", pressed)
	}
	if pressed[0].Text != "" && !strings.Contains(pressed[0].Text, "*Disks*") {
		t.Errorf("short form: %q", pressed[0].Text)
	}
	w.env.Drain(t)
	again := w.send(t, on, whatsapptest.ButtonReply(got.Buttons[0].ID, "See today's"))
	text := again[0].Text
	link := waPublicURL + "/pages/disks?" + url.Values{"filters": {`{"day":"today"}`}}.Encode()
	if !strings.Contains(text, "db-2 full today") || !strings.Contains(text, link) {
		t.Errorf("the page filtered to today: %q (want %s)", text, link)
	}
	// Someone the page's roles do not let in cannot open it with it.
	if r := w.send(t, bn, whatsapptest.ButtonReply(got.Buttons[0].ID, "See today's")); len(r) != 1 || !strings.Contains(r[0].Text, "not one you can open") {
		t.Errorf("a builder pressed it: %+v", r)
	}
}

// Pages H5 (build-plan-gaps §7), g-pages-mine: a page of tickets filtered
// by Mine on a phone column shows a staff member their own: the number
// they sign in by, matched as a table may hold it (with or without its
// plus, or written at home), and nobody else's.
func TestPagesMineOnAPhoneColumn(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5mine.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	sam := addMember(t, w.world, owner, "sam", "desk")
	ada := addMember(t, w.world, owner, "ada", "desk") // no phone on her account
	if _, err := w.env.DB.Admin.Exec(context.Background(),
		`INSERT INTO user_phones (user_id, number) SELECT id, '+2348012345678' FROM users WHERE email = 'sam@acme.test'`); err != nil {
		t.Fatal(err)
	}

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "status": "open", "phone": "2348012345678"},
		{"title": "Door stuck", "status": "open", "phone": "08012345678"},
		{"title": "No ink", "status": "open", "phone": "2348098765432"},
		{"title": "Screen dark", "status": "open"},
	}})

	// The editor's Mine on the phone column: Check takes it.
	doc := map[string]any{"page": "page/v1", "name": "my-tickets", "title": "My tickets", "roles": []string{"desk"},
		"filters": []map[string]any{{"id": "mine", "type": "mine", "column": "phone", "label": "Mine"}},
		"blocks": []map[string]any{
			{"id": "list", "type": "list", "title": "Tickets", "source": map[string]any{"table": "tickets", "order": []map[string]any{{"column": "title"}}, "uses": []string{"mine"}},
				"item": map[string]any{"title": "=row.title"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/my-tickets/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}

	mine := map[string]any{"mine": "on"}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", mine), "list")); len(got) != 2 || got[0] != "Door stuck" || got[1] != "Printer jam" {
		t.Errorf("Sam's own tickets: %v", got)
	}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", nil), "list")); len(got) != 4 {
		t.Errorf("without Mine, every ticket: %v", got)
	}
	if rows := blockOf(t, viewOf(t, ada, "my-tickets", mine), "list")["rows"].([]any); len(rows) != 0 {
		t.Errorf("Ada has no phone, so Mine shows her nothing: %v", rows)
	}
}

// g-pages-runs: the page's settings estimate its tools' runs a day: views
// a day over the last seven days (a viewer once a minute, as their answer
// is kept) times the tools its parts start, beside the plan's limits; only
// for those who change pages.
func TestPagesRunsEstimate(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5runs.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	desk := addMember(t, w.world, owner, "desk", "desk")
	ops := addMember(t, w.world, owner, "ops", "desk")
	publishWF(t, owner, "Ticket counts", toolDef("wf_counts", "Ticket counts", "ticket-counts", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=steps.look.output.n"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"text","text":"done"}`, `{"id":"look","type":"transform","config":{"output":{"n":7,"rows":[{"name":"Musa"}]}}}`))
	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"desk"},
		"blocks": []map[string]any{
			{"id": "open", "type": "figure", "label": "Open", "value": "output", "source": map[string]any{"tool": "ticket-counts", "output": "open"}},
			{"id": "rows", "type": "list", "title": "Waiting", "source": map[string]any{"tool": "ticket-counts", "output": "rows"}, "item": map[string]any{"title": "=item.name"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)

	// The editor's choices (g-pages-editor): the tool, which only looks
	// things up, with its answers by name and type; only for page.edit.
	ch := toJSON(owner.must(200, "GET", "/v1/page-choices", nil))
	for _, want := range []string{`"command":"ticket-counts"`, `"answers":true`, `"name":"rows","title":"Rows","type":"list"`} {
		if !strings.Contains(ch, want) {
			t.Errorf("choices: want %s in %s", want, ch)
		}
	}
	if s, _ := desk.do("GET", "/v1/page-choices", nil); s != 403 {
		t.Errorf("a viewer read the editor's choices: %d", s)
	}

	// Desk opens it three times in a minute (one view), Ops once.
	for range 3 {
		viewOf(t, desk, "desk", nil)
	}
	viewOf(t, ops, "desk", nil)
	// Five days ago it was opened 26 times; ten days ago is past the week.
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO page_views (tenant_id, page_id, day, views)
		SELECT tenant_id, id, (now() AT TIME ZONE 'Africa/Lagos')::date - 5, 26 FROM pages WHERE name = 'desk'
		UNION ALL SELECT tenant_id, id, (now() AT TIME ZONE 'Africa/Lagos')::date - 10, 500 FROM pages WHERE name = 'desk'`); err != nil {
		t.Fatal(err)
	}
	runs := owner.must(200, "GET", "/v1/pages/desk", nil)["runs"].(map[string]any)
	// 28 views in the week: 4 a day; one tool feeds both parts with the
	// same values (one run a view): about 4 runs a day.
	if runs["views_a_day"] != float64(4) || runs["tools"] != float64(1) || runs["runs_a_day"] != float64(4) {
		t.Errorf("estimate: %v", runs)
	}
	if _, ok := runs["runs_per_day"]; !ok {
		t.Errorf("the plan's limit is beside it: %v", runs)
	}
	// Staff who only open the page are not told.
	if _, ok := desk.must(200, "GET", "/v1/pages/desk", nil)["runs"]; ok {
		t.Errorf("a viewer was given the estimate")
	}
	// A draft with a second tool counts it.
	doc["blocks"] = append(doc["blocks"].([]map[string]any), map[string]any{"id": "more", "type": "figure", "label": "More", "value": "output",
		"source": map[string]any{"tool": "ticket-counts", "output": "open", "inputs": map[string]any{"x": "1"}}})
	owner.do("PUT", "/v1/pages/desk/draft", map[string]any{"document": doc})
	if runs := owner.must(200, "GET", "/v1/pages/desk", nil)["runs"].(map[string]any); runs["tools"] != float64(2) || runs["runs_a_day"] != float64(8) {
		t.Errorf("the draft's tools: %v", runs)
	}
}

// g-pages-editor, done when: the deliveries fixture page as the editor
// saves it (web/e2e/pages-editor.spec.ts makes it by pressing and picking,
// and checks the document it saves is this) publishes and works: what is
// owed to riders, today's deliveries with Confirm shown only while one is
// delivered and not confirmed, each row's run, the customers and staff.
func TestPagesEditorFixture(t *testing.T) {
	w := recordsWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5editor.test")
	addOrder(t, w, owner, "AC-12", "dispatched", "Bayo Ade", "+2348012345678", 990000)
	addDelivery(t, w, owner, "AC-12", "Musa", 150000)
	publishWF(t, owner, "Confirm deliveries", json.RawMessage(`{"schema":"wd/v1","id":"wf_editorok","version":1,"name":"Confirm deliveries",
		"trigger":{"type":"manual"},"inputs":{"schema":{"type":"object","properties":{"delivery":{"type":"string"}}}},
		"steps":[{"id":"wait","type":"signal","config":{"event":"confirm","correlation":"=trigger.body.delivery","pages":true}}]}`))
	var lines []map[string]any
	for _, l := range [][2]string{{"Order", "order"}, {"Customer", "customer"}, {"Phone", "phone"}, {"Rider", "rider"}, {"Status", "status"},
		{"Delivery area", "zone"}, {"Rider's fee", "fee"}, {"Owed to the rider", "owed"}, {"Paid at the door", "paid_how"}, {"Offered", "offered_at"}, {"Delivered", "delivered_at"}} {
		lines = append(lines, map[string]any{"label": l[0], "value": "=row." + l[1]})
	}
	doc := map[string]any{"page": "page/v1", "name": "deliveries-today", "title": "Deliveries today", "icon": "tables",
		"filters": []map[string]any{{"id": "dates", "type": "dates", "column": "offered_at", "label": "When"}},
		"blocks": []map[string]any{
			{"id": "total", "type": "figure", "label": "Owed to riders", "value": "sum", "of": "owed", "source": map[string]any{"records": "deliveries"}, "naira": true},
			{"id": "list", "type": "list", "title": "Today's deliveries",
				"source": map[string]any{"order": []map[string]any{{"column": "offered_at", "desc": true}}, "uses": []string{"dates"}, "records": "deliveries"},
				"item":   map[string]any{"title": "=row.order + ' · ' + row.rider", "sub": "=row.customer", "state": map[string]any{"text": "=row.status"}},
				"detail": map[string]any{"title": "=row.order + ' · ' + row.rider", "lines": lines},
				"actions": []map[string]any{{"label": "Confirm", "when": "=row.status == 'delivered' && row.confirmed_at == ''",
					"signal": map[string]any{"workflow": "Confirm deliveries", "name": "confirm", "to": "=row.id"}}},
				"status": map[string]any{"workflow": "Confirm deliveries", "to": "=row.id"}},
			{"id": "table", "type": "table", "title": "Customers (Taskiem's)", "source": map[string]any{"records": "customers"},
				"columns": []map[string]any{{"column": "customer"}, {"column": "owed", "naira": true}}},
			{"id": "table_2", "type": "table", "title": "Staff (Taskiem's)", "source": map[string]any{"records": "staff"},
				"columns": []map[string]any{{"column": "name"}, {"column": "roles"}}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/deliveries-today/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}
	v := viewOf(t, owner, "deliveries-today", map[string]any{"dates": "today"})
	if b := blockOf(t, v, "total"); b["shown"] != "₦1,500" {
		t.Errorf("owed: %v", b)
	}
	rows := blockOf(t, v, "list")["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("today: %v", blockOf(t, v, "list"))
	}
	row := rows[0].(map[string]any)
	if row["title"] != "AC-12 · Musa" || len(row["actions"].([]any)) != 1 {
		t.Errorf("a delivery: %v", row)
	}
	if st, _ := row["run_status"].(map[string]any); st == nil || st["words"] != "Not started" {
		t.Errorf("no run waits for it yet: %v", row["run_status"])
	}
	if got := toJSON(blockOf(t, v, "table_2")["rows"]); !strings.Contains(got, "owner") {
		t.Errorf("staff: %s", got)
	}
}
