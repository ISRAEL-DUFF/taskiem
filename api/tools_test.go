package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A tool's care is worked out from its steps when it is published and kept
// with the version; a command is one workflow's per environment (decision
// 0070).
func TestToolPublish(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@tools.test")
	def := func(id, name, command, step string) json.RawMessage {
		return json.RawMessage(`{"schema":"wd/v1","id":"` + id + `","version":1,"name":"` + name + `","trigger":{"type":"manual"},
			"outputs":[{"name":"left","type":"integer","title":"Left","description":"Days left","value":"=1"}],
			"inputs":{"schema":{"type":"object","properties":{}}},
			"tool":{"name":"` + name + `","command":"` + command + `","about":"Days of leave left","answer":{"type":"text","text":"=string(outputs.left) + ' days'"}},
			"steps":[` + step + `]}`)
	}
	read := `{"id":"look","type":"http","config":{"method":"GET","url":"https://api.example.com/leave"}}`
	tell := `{"id":"tell","type":"http","config":{"method":"POST","class":"unsafe_write","url":"https://api.example.com/leave","body":{"asked":true}}}`

	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Leave left", "definition": def("wf_leave", "Leave left", "leave-left", read)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	got := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)
	if care := got["tool_care"].(map[string]any); care["level"] != "answers" {
		t.Errorf("care of a read: %v", care)
	}

	// Another workflow may not take the same command.
	other := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Ask leave", "definition": def("wf_ask", "Ask leave", "leave-left", tell)})["id"].(string)
	if e := owner.must(409, "POST", "/v1/workflows/"+other+"/versions/1/publish", nil); !strings.Contains(e["error"].(string), "/leave-left is already Leave left's") {
		t.Errorf("same command: %v", e)
	}
	// Under its own, it asks yes: it writes.
	v := owner.must(201, "POST", "/v1/workflows/"+other+"/versions", map[string]any{"definition": def("wf_ask", "Ask leave", "ask-leave", tell)})["version"]
	ver := toJSON(v)
	owner.must(200, "POST", "/v1/workflows/"+other+"/versions/"+ver+"/publish", nil)
	if care := owner.must(200, "GET", "/v1/workflows/"+other+"/versions/"+ver, nil)["tool_care"].(map[string]any); care["level"] != "yes" || toJSON(care["because"]) != `["tell"]` {
		t.Errorf("care of a write: %v", care)
	}
	// A reserved word is refused when validated.
	if v := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": def("wf_x", "X", "invoice", read)}); v["valid"] != false || !strings.Contains(toJSON(v["problems"]), "Taskiem's own commands") {
		t.Errorf("reserved: %v", v)
	}
}

// A member uses a tool through the API (made tools M1): listed, started at
// once when it only reads, with the echo; asked yes once an owner makes it
// always ask, then confirmed once with the same values; its answer drawn
// from the run's outputs; held to the workflow's roles.
func TestToolUse(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@tooluse.test")
	def := json.RawMessage(`{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave left","trigger":{"type":"manual"},"roles":["operator"],
		"inputs":{"schema":{"type":"object","required":["who"],"properties":{"who":{"type":"string","title":"Who"}}}},
		"outputs":[{"name":"left","type":"integer","title":"Left","description":"Days left","value":"=steps.look.output.left"},
		           {"name":"who","type":"string","title":"Who","description":"Who","value":"=steps.look.output.who"}],
		"tool":{"name":"Leave left","command":"leave-left","about":"Days of leave left","answer":{"type":"text","text":"=outputs.who + ' has ' + string(outputs.left) + ' days of leave left'"}},
		"steps":[{"id":"look","type":"transform","config":{"output":{"left":12,"who":"=trigger.body.who"}}}]}`)
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Leave left", "definition": def})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	op := w.member(t, owner, "op@tooluse.test", "operator")
	builder := w.member(t, owner, "bo@tooluse.test", "builder")

	// Listed for whoever may start it.
	tools := op.must(200, "GET", "/v1/tools", nil)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["command"] != "leave-left" || tools[0].(map[string]any)["care"].(map[string]any)["level"] != "answers" {
		t.Fatalf("listed: %v", tools)
	}
	if got := builder.must(200, "GET", "/v1/tools", nil)["tools"].([]any); len(got) != 0 {
		t.Errorf("listed for a builder: %v", got)
	}
	if e := builder.must(403, "POST", "/v1/tools/leave-left/start", map[string]any{"input": map[string]any{"who": "Musa"}}); !strings.Contains(e["error"].(string), "only operator") {
		t.Errorf("builder: %v", e)
	}
	op.must(422, "POST", "/v1/tools/leave-left/start", map[string]any{"input": map[string]any{}})
	op.must(404, "POST", "/v1/tools/nope/start", map[string]any{"input": map[string]any{}})

	// It only reads: started at once, with the echo.
	got := op.must(201, "POST", "/v1/tools/leave-left/start", map[string]any{"input": map[string]any{"who": "Musa"}})
	if got["care"] != "answers" || toJSON(got["values"]) != `[{"label":"Who","name":"who","value":"Musa"}]` {
		t.Fatalf("at once: %v", got)
	}
	run := got["run_id"].(string)
	w.env.Drain(t)
	if a := op.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["type"] != "text" || a["text"] != "Musa has 12 days of leave left" || a["status"] != "completed" {
		t.Errorf("answer: %v", a)
	}

	// Always ask: yes, once, with the same values.
	op.must(403, "PUT", "/v1/tools/leave-left/settings", map[string]any{"always_ask": true})
	owner.must(204, "PUT", "/v1/tools/leave-left/settings", map[string]any{"always_ask": true})
	got = op.must(200, "POST", "/v1/tools/leave-left/start", map[string]any{"input": map[string]any{"who": "Ada"}})
	if got["care"] != "yes" || got["confirm"] == nil || got["run_id"] != nil {
		t.Fatalf("asks yes: %v", got)
	}
	confirm := got["confirm"].(string)
	op.must(409, "POST", "/v1/tools/leave-left/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"who": "Bayo"}})
	owner.must(404, "POST", "/v1/tools/leave-left/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"who": "Ada"}})
	first := op.must(201, "POST", "/v1/tools/leave-left/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"who": "Ada"}})
	again := op.must(200, "POST", "/v1/tools/leave-left/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"who": "Ada"}})
	if first["run_id"] != again["run_id"] {
		t.Errorf("confirmed twice: %v %v", first, again)
	}
	var tool string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT detail->>'tool' FROM audit_log WHERE action = 'run.start' ORDER BY id DESC LIMIT 1`).Scan(&tool); err != nil || tool != "leave-left" {
		t.Errorf("audited: %q %v", tool, err)
	}

	// Deployed without the block: no tool.
	plain := strings.Replace(string(def), `"tool":{"name":"Leave left","command":"leave-left","about":"Days of leave left","answer":{"type":"text","text":"=outputs.who + ' has ' + string(outputs.left) + ' days of leave left'"}},`, "", 1)
	v := owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(plain)})["version"]
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/"+toJSON(v)+"/publish", nil)
	if got := op.must(200, "GET", "/v1/tools", nil)["tools"].([]any); len(got) != 0 {
		t.Errorf("after a version without a tool: %v", got)
	}
}
