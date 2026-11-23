package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// toolDef is a made tool's workflow: inputs, outputs from a transform, and
// its answer.
func toolDef(id, name, command, roles, inputs, outputs, answer, step string) json.RawMessage {
	if roles != "" {
		roles = `"roles":` + roles + `,`
	}
	return json.RawMessage(`{"schema":"wd/v1","id":"` + id + `","version":1,"name":"` + name + `","trigger":{"type":"manual"},` + roles + `
		"inputs":{"schema":{"type":"object","properties":` + inputs + `}},
		"outputs":` + outputs + `,
		"tool":{"name":"` + name + `","command":"` + command + `","about":"` + name + `","answer":` + answer + `},
		"steps":[` + step + `]}`)
}

func publishWF(t *testing.T, owner *client, name string, def json.RawMessage) string {
	t.Helper()
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": def})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	return wf
}

func actOn(c *client, page string, body map[string]any) (int, map[string]any) {
	return c.do("POST", "/v1/pages/"+page+"/act?"+url.Values{"filters": {"{}"}}.Encode(), body)
}

// Pages G2: buttons on a page and its rows, a tool feeding a part, a
// signal to a run, a status; held to the tool's and the workflow's roles,
// a personal value asked not filled for someone without table.read, a
// signal only to a run of the workflow named that declares it for pages.
func TestPagesWithTools(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@g2.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	desk := addMember(t, w.world, owner, "desk", "desk")      // run.start, not table.read
	viewer := addMember(t, w.world, owner, "vic", "operator") // run.start and table.read
	stranger := addMember(t, w.world, owner, "str", "viewer") // not in the page's roles

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "customer": "Bayo", "status": "open", "phone": "2348012345678", "amount_kobo": 250000},
		{"title": "No ink", "customer": "Ada", "status": "done", "phone": "2348098765432", "amount_kobo": 100000},
	}})

	transform := func(out string) string {
		return `{"id":"look","type":"transform","config":{"output":` + out + `}}`
	}
	publishWF(t, owner, "Assign ticket", toolDef("wf_assign", "Assign ticket", "assign-ticket", "",
		`{"ref":{"type":"string","title":"Ticket"},"phone":{"type":"string","title":"Phone"}}`,
		`[{"name":"done","type":"string","title":"Done","description":"Done","value":"=steps.look.output.done"}]`,
		`{"type":"text","text":"=outputs.done"}`, transform(`{"done":"=trigger.body.ref"}`)))
	publishWF(t, owner, "Close ticket", toolDef("wf_close", "Close ticket", "close-ticket", `["manager"]`,
		`{"ref":{"type":"string","title":"Ticket"}}`,
		`[{"name":"done","type":"string","title":"Done","description":"Done","value":"=steps.look.output.done"}]`,
		`{"type":"text","text":"=outputs.done"}`, transform(`{"done":"=trigger.body.ref"}`)))
	publishWF(t, owner, "Ticket counts", toolDef("wf_counts", "Ticket counts", "ticket-counts", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=steps.look.output.n"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"list","title":"Waiting","from":"rows","item":{"title":"=item.name","sub":"=item.phone"},"actions":[{"label":"Assign","tool":"assign-ticket","inputs":{"ref":"=item.name"}}]}`,
		transform(`{"n":7,"rows":[{"name":"Musa","phone":"2348031112222"},{"name":"Ngozi","phone":"2348033334444"}]}`)))
	publishWF(t, owner, "Log it", toolDef("wf_log", "Log it", "log-it", "", `{}`,
		`[{"name":"n","type":"integer","title":"N","description":"N","value":"=1"}]`,
		`{"type":"text","text":"done"}`,
		`{"id":"tell","type":"http","config":{"method":"POST","class":"unsafe_write","url":"https://api.example.com/log","body":{"a":1}}}`))
	signalWF := func(id, name string, pages bool) json.RawMessage {
		flag := ""
		if pages {
			flag = `,"pages":true`
		}
		return json.RawMessage(`{"schema":"wd/v1","id":"` + id + `","version":1,"name":"` + name + `","trigger":{"type":"manual"},"roles":["support","desk","operator"],
			"inputs":{"schema":{"type":"object","properties":{"ref":{"type":"string"}}}},
			"steps":[{"id":"wait","type":"signal","config":{"event":"confirm","correlation":"=trigger.body.ref"` + flag + `}}]}`)
	}
	confirmWF := publishWF(t, owner, "Delivery confirm", signalWF("wf_confirm", "Delivery confirm", true))
	otherWF := publishWF(t, owner, "Other confirm", signalWF("wf_other", "Other confirm", false))
	run := owner.must(201, "POST", "/v1/workflows/"+confirmWF+"/runs", map[string]any{"input": map[string]any{"ref": "Printer jam"}})["run_id"].(string)
	other := owner.must(201, "POST", "/v1/workflows/"+otherWF+"/runs", map[string]any{"input": map[string]any{"ref": "Printer jam"}})["run_id"].(string)
	w.env.Drain(t)

	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"desk", "operator"},
		"blocks": []map[string]any{
			{"id": "new", "type": "buttons", "actions": []map[string]any{{"label": "New ticket", "tool": "assign-ticket"}}},
			{"id": "open", "type": "figure", "label": "Open (worked out)", "value": "output", "source": map[string]any{"tool": "ticket-counts", "output": "open"}},
			{"id": "waiting", "type": "list", "title": "Waiting", "source": map[string]any{"tool": "ticket-counts", "output": "rows"},
				"item": map[string]any{"title": "=item.name", "sub": "=item.phone"}},
			{"id": "list", "type": "list", "title": "Tickets", "source": map[string]any{"table": "tickets", "order": []map[string]any{{"column": "title"}}},
				"item": map[string]any{"title": "=row.title", "sub": "=row.phone"},
				"actions": []map[string]any{
					{"label": "Assign to me", "tool": "assign-ticket", "inputs": map[string]any{"ref": "=row.title", "phone": "=row.phone"}, "once": true},
					{"label": "Confirm", "signal": map[string]any{"workflow": "Delivery confirm", "name": "confirm", "to": "=row.title", "input": map[string]any{"by": "page"}},
						"when": "=row.status == 'open'"},
				}},
			{"id": "close", "type": "table", "source": map[string]any{"table": "tickets"}, "columns": []map[string]any{{"column": "title"}},
				"actions": []map[string]any{{"label": "Close", "tool": "close-ticket", "inputs": map[string]any{"ref": "=row.title"}}}},
			{"id": "state", "type": "status", "workflow": "Delivery confirm"},
		}}

	// The validator: a tool that writes feeds no part; a signal the
	// workflow does not declare for pages is no button; a tool the
	// business lacks.
	bad := map[string]any{"page": "page/v1", "name": "bad", "title": "Bad", "blocks": []map[string]any{
		{"id": "n", "type": "figure", "label": "N", "value": "output", "source": map[string]any{"tool": "log-it", "output": "n"}},
		{"id": "b", "type": "buttons", "actions": []map[string]any{
			{"label": "Other", "signal": map[string]any{"workflow": "Other confirm", "name": "confirm", "run": "x"}},
			{"label": "Gone", "tool": "no-such-tool"}}},
	}}
	ps := toJSON(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": bad})["problems"])
	for _, want := range []string{"asks before it runs", "does not let a page tell it confirm", "which the business does not have"} {
		if !strings.Contains(ps, want) {
			t.Errorf("validate: want %q in %s", want, ps)
		}
	}

	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/desk/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}
	if s, _ := stranger.do("GET", "/v1/pages/desk/view", nil); s != 404 {
		t.Errorf("a stranger opened it: %d", s)
	}

	// The tool's parts: drawn from the run (still working until it ends),
	// its words masked as an answer's are.
	v := viewOf(t, desk, "desk", nil)
	if b := blockOf(t, v, "open"); b["working"] != nil {
		w.env.Drain(t)
		v = viewOf(t, desk, "desk", nil)
	}
	if b := blockOf(t, v, "open"); b["shown"] != "7" || b["worked_at"] == nil {
		t.Errorf("figure from a tool: %v", b)
	}
	waiting := blockOf(t, v, "waiting")
	if got := toJSON(waiting["rows"]); !strings.Contains(got, "Musa") || strings.Contains(got, "2348031112222") {
		t.Errorf("tool's rows, masked: %s", got)
	}
	// Kept: drawn again, no second run started.
	var runs int
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE w.name = 'Ticket counts'`).Scan(&runs)
	viewOf(t, desk, "desk", nil)
	var again int
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE w.name = 'Ticket counts'`).Scan(&again)
	if runs != 1 || again != 1 {
		t.Errorf("kept for a while: %d then %d runs", runs, again)
	}
	// Refresh lets it go: the next view starts it again.
	desk.must(204, "POST", "/v1/pages/desk/refresh", nil)
	viewOf(t, desk, "desk", nil)
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE w.name = 'Ticket counts'`).Scan(&again)
	if again != 2 {
		t.Errorf("refreshed: %d runs", again)
	}

	// Rows' buttons: Confirm only on the open ticket (when); Close greyed
	// for someone outside its roles.
	list := blockOf(t, v, "list")["rows"].([]any)
	if len(list) != 2 {
		t.Fatalf("rows: %v", list)
	}
	var jam, ink map[string]any
	for _, r := range list {
		m := r.(map[string]any)
		if m["title"] == "Printer jam" {
			jam = m
		} else {
			ink = m
		}
	}
	if n := len(jam["actions"].([]any)); n != 2 {
		t.Errorf("open ticket's buttons: %v", jam["actions"])
	}
	if n := len(ink["actions"].([]any)); n != 1 {
		t.Errorf("done ticket's buttons: %v", ink["actions"])
	}
	closeRow := blockOf(t, v, "close")["rows"].([]any)[0].(map[string]any)
	if d := closeRow["actions"].([]any)[0].(map[string]any)["disabled"]; d == nil || !strings.Contains(d.(string), "manager") {
		t.Errorf("Close greyed: %v", closeRow["actions"])
	}
	// An action cannot start a tool the viewer may not.
	if s, out := actOn(desk, "desk", map[string]any{"block": "close", "row": closeRow["id"], "action": 0}); s != 403 {
		t.Errorf("Close pressed by desk: %d %v", s, out)
	}

	// A tool's button: its values from the row, the phone (personal) asked
	// not filled for someone without table.read, filled for one with it.
	got := desk.must(200, "POST", "/v1/pages/desk/act?filters=%7B%7D", map[string]any{"block": "list", "row": jam["id"], "action": 0})
	tool := got["tool"].(map[string]any)
	if tool["command"] != "assign-ticket" || toJSON(tool["inputs"]) != `{"ref":"Printer jam"}` || tool["once"] != true {
		t.Errorf("desk's press: %v", got)
	}
	got = viewer.must(200, "POST", "/v1/pages/desk/act?filters=%7B%7D", map[string]any{"block": "list", "row": jam["id"], "action": 0})
	if in := got["tool"].(map[string]any)["inputs"].(map[string]any); in["phone"] != "2348012345678" {
		t.Errorf("viewer's press: %v", got)
	}
	// Started with the button, as an answer's: once, then spent.
	start := desk.must(201, "POST", "/v1/tools/assign-ticket/start", map[string]any{"input": map[string]any{"ref": "Printer jam"}, "button": tool["button"]})
	if start["run_id"] == nil {
		t.Errorf("started: %v", start)
	}
	if s, out := actOn(viewer, "desk", map[string]any{"block": "list", "row": jam["id"], "action": 0}); s != 409 || !strings.Contains(toJSON(out), "Already done") {
		t.Errorf("once, someone else: %d %v", s, out)
	}
	// A row not on the part, and a button hidden by when.
	if s, _ := actOn(desk, "desk", map[string]any{"block": "list", "row": "999", "action": 0}); s != 404 {
		t.Errorf("no such row: %d", s)
	}
	if s, _ := actOn(desk, "desk", map[string]any{"block": "list", "row": ink["id"], "action": 1}); s != 409 {
		t.Errorf("when false: %d", s)
	}

	// The page's own button.
	if got := desk.must(200, "POST", "/v1/pages/desk/act", map[string]any{"block": "new", "action": 0}); got["tool"].(map[string]any)["command"] != "assign-ticket" {
		t.Errorf("page button: %v", got)
	}

	// A signal: asks Yes first, then reaches the run of the workflow named
	// and no other run waiting for the same thing.
	ask := desk.must(200, "POST", "/v1/pages/desk/act", map[string]any{"block": "list", "row": jam["id"], "action": 1})
	if ask["asks"] != "Confirm?" || ask["sent"] != false {
		t.Errorf("asks: %v", ask)
	}
	sent := desk.must(200, "POST", "/v1/pages/desk/act", map[string]any{"block": "list", "row": jam["id"], "action": 1, "yes": true})
	if sent["sent"] != true || sent["run"] != run {
		t.Errorf("sent: %v", sent)
	}
	w.env.Drain(t)
	status := func(id string) string {
		var s string
		_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT status FROM runs WHERE id = $1`, id).Scan(&s)
		return s
	}
	if status(run) != "completed" || status(other) != "waiting" {
		t.Errorf("signal reached: %s, other %s", status(run), status(other))
	}
	if s, out := actOn(desk, "desk", map[string]any{"block": "list", "row": jam["id"], "action": 1, "yes": true}); s != 409 || out["code"] != "moved_on" {
		t.Errorf("moved on: %d %v", s, out)
	}
	// The status part shows the workflow's latest run, and only its own.
	v = viewOf(t, desk, "desk", nil)
	if st := blockOf(t, v, "state"); st["words"] != "Done" || st["run"] != run {
		t.Errorf("status: %v", st)
	}
	if st := blockOf(t, viewOf(t, desk, "desk", nil, "run", other), "state"); st["run"] != nil {
		t.Errorf("another workflow's run shown: %v", st)
	}

	// Another business never reaches this page's buttons.
	other2 := w.tenant(t, "Beta", "owner@beta-g2.test")
	if s, _ := actOn(other2, "desk", map[string]any{"block": "list", "row": jam["id"], "action": 1, "yes": true}); s != 404 {
		t.Errorf("another business: %d", s)
	}
}

// Pin as a page: a tool's list answer becomes a page fed by the tool.
func TestPagesPinAsAPage(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@pin.test")
	publishWF(t, owner, "Ticket counts", toolDef("wf_counts", "Ticket counts", "ticket-counts", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=7"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"list","title":"Waiting","from":"rows","item":{"title":"=item.name"},"actions":[{"label":"Assign","tool":"ticket-counts"}]}`,
		`{"id":"look","type":"transform","config":{"output":{"rows":[]}}}`))
	pg := owner.must(201, "POST", "/v1/pages", map[string]any{"from_tool": map[string]any{"command": "ticket-counts"}})
	if pg["name"] != "ticket-counts-page" || len(pg["problems"].([]any)) != 0 {
		t.Fatalf("pinned: %v", pg)
	}
	if got := toJSON(pg["draft"]); !strings.Contains(got, `"tool":"ticket-counts"`) || !strings.Contains(got, `"output":"rows"`) {
		t.Errorf("draft: %s", got)
	}
	owner.must(404, "POST", "/v1/pages", map[string]any{"from_tool": map[string]any{"command": "nope"}})
}
