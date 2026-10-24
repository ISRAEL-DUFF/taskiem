package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// A workflow with outputs is callable (decision 0048): it keeps the
// callable rules, and its outputs read only its own top-level steps.
func TestCallableWorkflowOutputs(t *testing.T) {
	base := func(extra, steps, outputs string) []byte {
		return []byte(`{"schema":"wd/v1","id":"wf_c","version":1,"name":"C","trigger":{"type":"manual"},` + extra +
			`"inputs":{"schema":{"type":"object","required":["amount_kobo"],"properties":{"amount_kobo":{"type":"integer"}}}},` +
			`"steps":` + steps + `,"outputs":` + outputs + `}`)
	}
	steps := `[{"id":"total","type":"transform","config":{"output":{"kobo":"=trigger.body.amount_kobo * 2"}}},
	           {"id":"maybe","type":"transform","needs":["total"],"when":"=steps.total.output.kobo > 10","config":{"output":{"big":true}}}]`
	good := base("", steps, `[{"name":"kobo","type":"integer","title":"Total","description":"Twice the amount","value":"=steps.total.output.kobo"},
	  {"name":"big","type":"boolean","title":"Big","description":"Over ten","value":"=steps.?maybe.output.big.orValue(false)"}]`)
	if probs := wd.Validate(good); len(probs) != 0 {
		t.Fatalf("a callable workflow: %v", probs)
	}
	def, err := wd.Load(good)
	if err != nil || !def.Callable() || len(def.Outputs) != 2 {
		t.Fatalf("load: %v %+v", err, def)
	}
	// Without outputs it is an ordinary workflow, and may do what a
	// callable one may not.
	plain := []byte(`{"schema":"wd/v1","id":"wf_p","version":1,"name":"P","trigger":{"type":"manual"},"types":{"T":{"type":"string"}},
	  "steps":[{"id":"a_step_with_a_very_long_identifier_indeed","type":"transform","config":{"output":{}}}]}`)
	if probs := wd.Validate(plain); len(probs) != 0 {
		t.Fatalf("a plain workflow: %v", probs)
	}
	for name, c := range map[string]struct {
		doc  []byte
		want string
	}{
		"no inputs": {[]byte(`{"schema":"wd/v1","id":"wf_c","version":1,"name":"C","trigger":{"type":"manual"},"steps":` + steps +
			`,"outputs":[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.total.output.kobo"}]}`), "needs run inputs"},
		"types":        {base(`"types":{"T":{"type":"string"}},`, steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.total.output.kobo"}]`), "declares no types"},
		"long id":      {base("", `[{"id":"a_step_with_a_very_long_identifier_indeed","type":"transform","config":{"output":{"k":1}}}]`, `[{"name":"k","type":"integer","title":"T","description":"D","value":"=steps.a_step_with_a_very_long_identifier_indeed.output.k"}]`), "at most 31 characters"},
		"respond":      {base("", `[{"id":"total","type":"transform","config":{"output":{"k":1}}},{"id":"r","type":"respond","config":{"body":"ok"}}]`, `[{"name":"k","type":"integer","title":"T","description":"D","value":"=steps.total.output.k"}]`), "respond"},
		"trigger read": {base("", `[{"id":"total","type":"transform","config":{"output":{"k":"=trigger.fired_at"}}}]`, `[{"name":"k","type":"string","title":"T","description":"D","value":"=steps.total.output.k"}]`), "tests it with has()"},
		"bad name":     {base("", steps, `[{"name":"Kobo","type":"integer","title":"T","description":"D","value":"=steps.total.output.kobo"}]`), "/outputs/0"},
		"duplicate":    {base("", steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.total.output.kobo"},{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.total.output.kobo"}]`), "bad or duplicate name"},
		"bad type":     {base("", steps, `[{"name":"kobo","type":"money","title":"T","description":"D","value":"=steps.total.output.kobo"}]`), "/outputs/0/type"},
		"literal":      {base("", steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"5"}]`), "/outputs/0/value"},
		"other root":   {base("", steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=trigger.body.amount_kobo"}]`), "not trigger"},
		"nested step":  {base("", steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.nowhere.output.kobo"}]`), "not a top-level step"},
		"not output":   {base("", steps, `[{"name":"kobo","type":"integer","title":"T","description":"D","value":"=steps.total.error.message"}]`), "not the step's"},
		"may not run":  {base("", steps, `[{"name":"big","type":"boolean","title":"T","description":"D","value":"=steps.maybe.output.big"}]`), "optional chain"},
	} {
		probs := wd.Validate(c.doc)
		found := false
		for _, p := range probs {
			if strings.Contains(p.Path+": "+p.Message, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
	// A template step names a template or a workflow, not both or neither.
	for name, c := range map[string]struct{ step, want string }{
		"both":    {`{"id":"a","type":"template","template":"greet","workflow":"2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12"}`, "oneOf"},
		"neither": {`{"id":"a","type":"template"}`, "missing property 'workflow'"},
	} {
		probs := wd.Validate([]byte(`{"schema":"wd/v1","id":"wf_c","version":1,"name":"C","trigger":{"type":"manual"},"steps":[` + c.step + `]}`))
		found := false
		for _, p := range probs {
			if strings.Contains(p.Message, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}
