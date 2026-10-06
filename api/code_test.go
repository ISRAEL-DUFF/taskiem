package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCodeViewRoundTrip(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	def := json.RawMessage(`{"schema":"wd/v1","id":"wf_code","version":1,"name":"Code","trigger":{"type":"manual"},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.n + 1"}},
	           {"id":"b","type":"transform","needs":["a"],"config":{"output":"=steps.a.output * 2"}}]}`)
	code := owner.must(200, "POST", "/v1/code/generate", map[string]any{"definition": def})["code"].(string)
	if !strings.Contains(code, `.next("b", transform(({ steps }) => steps.a.output * 2))`) {
		t.Fatalf("code:\n%s", code)
	}
	edited := strings.Replace(code, "* 2", "* 3", 1)
	out := owner.must(200, "POST", "/v1/code/compile", map[string]any{"source": edited})
	steps := out["definition"].(map[string]any)["steps"].([]any)
	if steps[1].(map[string]any)["config"].(map[string]any)["output"] != "=steps.a.output * 3" || len(out["problems"].([]any)) != 0 {
		t.Errorf("compiled: %v", out)
	}

	// Author errors are 422 with the reason; other packages are refused.
	status, body := owner.do("POST", "/v1/code/compile", map[string]any{"source": `import cp from "node:child_process"; export default cp;`})
	if status != 422 || !strings.Contains(body["error"].(string), "not available to flow code") {
		t.Errorf("%d %v", status, body)
	}
	// Problems a definition would have at publish time are reported.
	out = owner.must(200, "POST", "/v1/code/compile", map[string]any{"source": `import { workflow, manual, connector } from "@taskiem/sdk";
export default workflow({ id: "wf_x", name: "x", trigger: manual() }).step("a", connector("nope@1", "go"));`})
	if len(out["problems"].([]any)) == 0 {
		t.Error("an unknown connector should be a problem")
	}
}

func TestConcurrentEditsMerge(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	doc := func(a, b int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"schema":"wd/v1","id":"wf_m","version":1,"name":"M","trigger":{"type":"manual"},
		  "steps":[{"id":"a","type":"transform","config":{"output":%d}},{"id":"b","type":"transform","config":{"output":%d}}]}`, a, b))
	}
	created := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "m", "definition": doc(1, 2)})
	wf, d1 := created["id"].(string), created["digest"].(string)

	// Alice and Bob both start from version 1. Alice saves first.
	alice := owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": doc(10, 2), "parent_digest": d1})
	if alice["merged"] != false {
		t.Errorf("alice: %v", alice)
	}
	// Bob changed another step: merged into version 3 with both edits.
	bob := owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": doc(1, 20), "parent_digest": d1})
	if bob["merged"] != true || bob["version"] != float64(3) {
		t.Fatalf("bob: %v", bob)
	}
	v3 := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/3", nil)["definition"].(map[string]any)["steps"].([]any)
	if v3[0].(map[string]any)["config"].(map[string]any)["output"] != float64(10) || v3[1].(map[string]any)["config"].(map[string]any)["output"] != float64(20) {
		t.Errorf("merged steps: %v", v3)
	}

	// Carol, also from version 1, changed step a differently: a conflict.
	status, body := owner.do("POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": doc(99, 2), "parent_digest": d1})
	conflicts, _ := body["conflicts"].([]any)
	if status != 409 || len(conflicts) != 1 || conflicts[0].(map[string]any)["path"] != "steps/a" || body["latest_version"] != float64(3) {
		t.Fatalf("%d %v", status, body)
	}
	// She keeps hers.
	carol := owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": doc(99, 2), "parent_digest": d1,
		"resolutions": map[string]string{"steps/a": "ours"}})
	v4 := owner.must(200, "GET", fmt.Sprintf("/v1/workflows/%s/versions/%v", wf, carol["version"]), nil)["definition"].(map[string]any)["steps"].([]any)
	if v4[0].(map[string]any)["config"].(map[string]any)["output"] != float64(99) || v4[1].(map[string]any)["config"].(map[string]any)["output"] != float64(20) {
		t.Errorf("resolved steps: %v", v4)
	}
	owner.must(409, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": doc(1, 2), "parent_digest": strings.Repeat("0", 64)})
}
