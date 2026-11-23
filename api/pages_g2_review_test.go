package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// A tool feeding a page is for whoever may use that tool: a viewer outside
// its roles sees the part as unavailable (view, More, a row's button), and
// an answer kept for them before does not get past it; a page cannot open
// to more people than its tool's roles; Pin as a page keeps the tool's
// roles.
func TestPagesToolSourceHeldToTheTool(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@g2roles.test")
	owner.must(201, "PUT", "/v1/roles/manager", map[string]any{"permissions": []string{"run.start"}})
	owner.must(201, "PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"run.start"}})
	mgr := addMember(t, w.world, owner, "mgr", "manager", "clerk")
	adm := addMember(t, w.world, owner, "adm", "admin") // page.edit: opens every page; not a manager
	publishWF(t, owner, "Pay summary", toolDef("wf_paysum", "Pay summary", "pay-summary", `["manager"]`, `{}`,
		`[{"name":"n","type":"integer","title":"N","description":"N","value":"=steps.look.output.n"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"list","title":"Pay","from":"rows","item":{"title":"=item.name"}}`,
		`{"id":"look","type":"transform","config":{"output":{"n":7,"rows":[{"name":"Musa"},{"name":"Ngozi"}]}}}`))

	doc := map[string]any{"page": "page/v1", "name": "payboard", "title": "Pay", "roles": []string{"manager"},
		"blocks": []map[string]any{
			{"id": "n", "type": "figure", "label": "Owed", "value": "output", "source": map[string]any{"tool": "pay-summary", "output": "n"}},
			{"id": "rows", "type": "list", "source": map[string]any{"tool": "pay-summary", "output": "rows"}, "item": map[string]any{"title": "=item.name"},
				"actions": []map[string]any{{"label": "Again", "tool": "pay-summary"}}},
		}}
	// Opened to everyone, it would show the managers' tool to all.
	wide := map[string]any{}
	for k, v := range doc {
		wide[k] = v
	}
	delete(wide, "roles")
	if ps := toJSON(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": wide})["problems"]); !strings.Contains(ps, "opens to more people") {
		t.Errorf("wider than its tool: %s", ps)
	}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/payboard/publish", nil)

	v := viewOf(t, mgr, "payboard", nil)
	if b := blockOf(t, v, "n"); b["shown"] != "7" {
		t.Fatalf("manager: %v", b)
	}
	// Someone who may open the page, but not use its tool.
	v = viewOf(t, adm, "payboard", nil)
	for _, id := range []string{"n", "rows"} {
		if b := blockOf(t, v, id); b["unavailable"] != "Only manager can see this." || b["shown"] != nil || b["rows"] != nil {
			t.Errorf("admin %s: %v", id, b)
		}
	}
	if b := adm.must(200, "GET", "/v1/pages/payboard/blocks/rows", nil); b["unavailable"] == nil || b["rows"] != nil {
		t.Errorf("admin More: %v", b)
	}
	if s, _ := actOn(adm, "payboard", map[string]any{"block": "rows", "row": "0", "action": 0}); s != 403 {
		t.Errorf("admin pressed a row of the managers' tool: %d", s)
	}
	// The manager loses the role: what was kept for them a moment ago is
	// not shown either.
	var me map[string]any
	_ = json.Unmarshal(mustJSONBytes(mgr.must(200, "GET", "/v1/me", nil)["user"]), &me)
	owner.must(204, "DELETE", "/v1/members/"+me["id"].(string)+"/roles/manager", nil)
	if s, _ := mgr.do("GET", "/v1/pages/payboard/view", nil); s != 404 {
		t.Errorf("no longer opens the page: %d", s)
	}
	runs := func() int {
		var n int
		_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE w.name = 'Pay summary'`).Scan(&n)
		return n
	}
	before := runs()
	viewOf(t, adm, "payboard", nil)
	if runs() != before {
		t.Errorf("a run started for someone the tool is not for")
	}

	// Pin as a page opens to the tool's roles.
	pg := owner.must(201, "POST", "/v1/pages", map[string]any{"from_tool": map[string]any{"command": "pay-summary"}})
	if toJSON(pg["draft"].(map[string]any)["roles"]) != `["manager"]` || len(pg["problems"].([]any)) != 0 {
		t.Errorf("pinned: %v %v", pg["draft"], pg["problems"])
	}
}

// A signal is moving a run on: it needs run.start besides the workflow's
// roles (of the run's own version); a once button on a tool's row is that
// row's, whatever place it has in the list.
func TestPagesSignalAndOnceButtons(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@g2once.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	owner.must(201, "PUT", "/v1/roles/watch", map[string]any{"permissions": []string{"workflow.read"}})
	desk := addMember(t, w.world, owner, "desk", "desk")
	watch := addMember(t, w.world, owner, "watch", "watch") // may open the page; no run.start
	other := addMember(t, w.world, owner, "oth", "desk")

	publishWF(t, owner, "Queue", toolDef("wf_queue", "Queue", "queue", "", `{"rev":{"type":"string"}}`,
		`[{"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"text","text":"ok"}`,
		`{"id":"look","type":"transform","config":{"output":{"rows":"=has(trigger.body.rev) && trigger.body.rev == 'yes' ? [{'name': 'Ngozi'}, {'name': 'Musa'}] : [{'name': 'Musa'}, {'name': 'Ngozi'}]"}}}`))
	publishWF(t, owner, "Take", toolDef("wf_take", "Take", "take-it", "", `{"who":{"type":"string","title":"Who"}}`,
		`[{"name":"done","type":"string","title":"Done","description":"Done","value":"=steps.look.output.done"}]`,
		`{"type":"text","text":"=outputs.done"}`, `{"id":"look","type":"transform","config":{"output":{"done":"=trigger.body.who"}}}`))
	confirm := publishWF(t, owner, "Hold", json.RawMessage(`{"schema":"wd/v1","id":"wf_hold","version":1,"name":"Hold","trigger":{"type":"manual"},
		"roles":["desk","watch"],"inputs":{"schema":{"type":"object","properties":{"ref":{"type":"string"}}}},
		"steps":[{"id":"wait","type":"signal","config":{"event":"go","correlation":"=trigger.body.ref","pages":true}}]}`))
	owner.must(201, "POST", "/v1/workflows/"+confirm+"/runs", map[string]any{"input": map[string]any{"ref": "Musa"}})
	w.env.Drain(t)

	doc := map[string]any{"page": "page/v1", "name": "queueboard", "title": "Queue", "roles": []string{"desk", "watch"},
		"filters": []map[string]any{{"id": "q", "type": "choice", "column": "x", "options": []map[string]any{{"value": "yes"}}}},
		"blocks": []map[string]any{
			{"id": "rows", "type": "list", "source": map[string]any{"tool": "queue", "output": "rows", "inputs": map[string]any{"rev": "=filters.q"}}, "item": map[string]any{"title": "=item.name"},
				"actions": []map[string]any{
					{"label": "Take", "tool": "take-it", "once": true},
					{"label": "Go", "signal": map[string]any{"workflow": "Hold", "name": "go", "to": "=item.name"}},
				}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/queueboard/publish", nil)
	viewOf(t, desk, "queueboard", nil)
	viewOf(t, other, "queueboard", nil)

	// A once button with no values of its own is still the row's: Musa's
	// taken does not spend Ngozi's.
	musa := desk.must(200, "POST", "/v1/pages/queueboard/act", map[string]any{"block": "rows", "row": "0", "action": 0})["tool"].(map[string]any)
	desk.must(201, "POST", "/v1/tools/take-it/start", map[string]any{"input": map[string]any{"who": "Musa"}, "button": musa["button"]})
	if s, out := actOn(other, "queueboard", map[string]any{"block": "rows", "row": "1", "action": 0}); s != 200 {
		t.Errorf("Ngozi's button spent by Musa's: %d %v", s, out)
	}
	viewOf(t, other, "queueboard", nil)
	if s, out := actOn(other, "queueboard", map[string]any{"block": "rows", "row": "0", "action": 0}); s != 409 || !strings.Contains(toJSON(out), "Already done") {
		t.Errorf("Musa's once button pressed again: %d %v", s, out)
	}
	viewOf(t, other, "queueboard", nil) // a press lets go of what the tool answered: the page is read again
	ngozi := other.must(200, "POST", "/v1/pages/queueboard/act", map[string]any{"block": "rows", "row": "1", "action": 0})["tool"].(map[string]any)
	if ngozi["button"] == musa["button"] {
		t.Errorf("one button for two rows")
	}
	// The list comes back the other way round: the first row is now Ngozi,
	// whose button is hers, not Musa's spent one.
	viewOf(t, other, "queueboard", map[string]any{"q": "yes"})
	q := "/v1/pages/queueboard/act?filters=" + url.QueryEscape(`{"q":"yes"}`)
	got := other.must(200, "POST", q, map[string]any{"block": "rows", "row": "0", "action": 0})["tool"].(map[string]any)
	if got["button"] != ngozi["button"] {
		t.Errorf("Ngozi first: %v, want her button %v", got["button"], ngozi["button"])
	}

	// A signal needs run.start, whatever the workflow's roles say.
	if s, _ := actOn(watch, "queueboard", map[string]any{"block": "rows", "row": "0", "action": 1, "yes": true}); s != 403 {
		t.Errorf("watch (no run.start) sent a signal: %d", s)
	}
	viewOf(t, desk, "queueboard", nil)
	if got := desk.must(200, "POST", "/v1/pages/queueboard/act", map[string]any{"block": "rows", "row": "0", "action": 1, "yes": true}); got["sent"] != true {
		t.Errorf("desk's signal: %v", got)
	}
}

// The short form on WhatsApp holds a tool's part to the tool: a person the
// page opens to, but who does not use tools, gets the page's other figures
// and not the tool's.
func TestPagesShortFormHoldsTheTool(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@pagechat2.test")
	owner.must(201, "PUT", "/v1/roles/manager", map[string]any{"permissions": []string{"workflow.read"}})
	publishWF(t, owner, "Pay summary", toolDef("wf_paysum", "Pay summary", "pay-summary", `["manager"]`, `{}`,
		`[{"name":"n","type":"integer","title":"N","description":"N","value":"=steps.look.output.n"}]`,
		`{"type":"text","text":"x"}`, `{"id":"look","type":"transform","config":{"output":{"n":4242}}}`))
	doc := map[string]any{"page": "page/v1", "name": "paydesk", "title": "Pay desk", "roles": []string{"manager"},
		"blocks": []map[string]any{
			{"id": "head", "type": "heading", "title": "Today"},
			{"id": "n", "type": "figure", "label": "Owed", "value": "output", "source": map[string]any{"tool": "pay-summary", "output": "n"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/paydesk/publish", nil)
	m := w.member(t, owner, "m@pagechat2.test", "manager")
	const on = "+2348010000061"
	w.bind(t, m, on)
	got := w.say(t, on, "paydesk")
	if !strings.Contains(got, "*Pay desk*") || strings.Contains(got, "4242") || strings.Contains(got, "4,242") {
		t.Errorf("short form: %q", got)
	}
}
