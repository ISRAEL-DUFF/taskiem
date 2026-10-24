package wd

import (
	"strings"
	"testing"
)

func TestCallbackAndRespondRules(t *testing.T) {
	const respond = `{"schema":"wd/v1","id":"wf_r","version":1,"name":"r","trigger":{"type":"webhook","config":{"path":"/q","auth":"none","respond":true}},
	 "steps":[{"id":"wait","type":"callback","config":{"timeout":"2h"}},
	          {"id":"answer","type":"respond","needs":["wait"],"config":{"status":201,"body":{"ok":"=steps.wait.output.body"}}}]}`
	if probs := Validate([]byte(respond)); len(probs) > 0 {
		t.Fatalf("a good definition: %v", probs)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"respond without the trigger":  {`,"respond":true`, ``, "respond: true"},
		"respond: true without a step": {`{"id":"answer","type":"respond","needs":["wait"],"config":{"status":201,"body":{"ok":"=steps.wait.output.body"}}}`, `{"id":"answer","type":"transform","needs":["wait"],"config":{"output":{}}}`, "needs a respond step"},
		"a secret in the answer":       {`"body":{"ok":"=steps.wait.output.body"}`, `"body":{"ok":"=secrets.key"}`, "secrets"},
		"a bad timeout":                {`"timeout":"2h"`, `"timeout":"soon"`, "schema"},
		"a bad status":                 {`"status":201`, `"status":99`, "schema"},
		"on_error on a respond step":   {`"config":{"status":201,"body":{"ok":"=steps.wait.output.body"}}`, `"config":{"status":201,"body":{}},"on_error":{"steps":[{"id":"x","type":"transform","config":{"output":{}}}]}`, "no on_error"},
	} {
		doc := strings.Replace(respond, c.old, c.new, 1)
		if doc == respond {
			t.Fatalf("%s: the case changes nothing", name)
		}
		probs := Validate([]byte(doc))
		found := false
		for _, p := range probs {
			found = found || strings.Contains(p.Message, c.want)
		}
		if !found {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}
