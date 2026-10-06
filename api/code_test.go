package api_test

import (
	"encoding/json"
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
