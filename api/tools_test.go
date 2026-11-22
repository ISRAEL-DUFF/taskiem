package api_test

import (
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
	ver := strings.TrimSuffix(strings.TrimSuffix(toJSON(v), "\n"), "")
	owner.must(200, "POST", "/v1/workflows/"+other+"/versions/"+ver+"/publish", nil)
	if care := owner.must(200, "GET", "/v1/workflows/"+other+"/versions/"+ver, nil)["tool_care"].(map[string]any); care["level"] != "yes" || toJSON(care["because"]) != `["tell"]` {
		t.Errorf("care of a write: %v", care)
	}
	// A reserved word is refused when validated.
	if v := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": def("wf_x", "X", "invoice", read)}); v["valid"] != false || !strings.Contains(toJSON(v["problems"]), "Taskiem's own commands") {
		t.Errorf("reserved: %v", v)
	}
}
