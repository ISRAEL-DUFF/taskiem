package wdtext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

const changesV1 = `{"schema":"wd/v1","id":"wf_fuel","version":1,"name":"Log fuel","trigger":{"type":"manual"},
	"inputs":{"schema":{"type":"object","required":["amount"],"properties":{"amount":{"type":"number"}}}},
	"outputs":[{"name":"total","type":"number","title":"Total","description":"Spent","value":"=steps.keep.output.total"}],
	"tool":{"name":"Log fuel","command":"fuel","about":"Record fuel","says":["fuel"],
	  "answer":{"type":"text","text":"='Logged'"}},
	"steps":[{"id":"keep","name":"Keep it","type":"transform","config":{"output":{"total":1}}}]}`

// A tool's change said in plain words (made tools §17, build plan H6):
// a phrase added and one removed, who it is for, its answer, its command,
// who may start it, and a step added; nothing for a first version.
func TestChanges(t *testing.T) {
	v2 := strings.NewReplacer(
		`"says":["fuel"]`, `"says":["spent","diesel"]`,
		`"command":"fuel"`, `"command":"fuel-log"`,
		`"about":"Record fuel"`, `"about":"Record fuel","for":"customers"`,
		`"answer":{"type":"text","text":"='Logged'"}`, `"answer":{"type":"card","title":"='Logged'"}`,
		`"trigger":{"type":"manual"},`, `"trigger":{"type":"manual"},"roles":["manager"],`,
		`"config":{"output":{"total":1}}}]`, `"config":{"output":{"total":1}}},{"id":"tell","name":"Tell the owner","type":"transform","config":{"output":{}}}]`,
	).Replace(changesV1)
	got := Changes(json.RawMessage(changesV1), json.RawMessage(v2), nil, lang.EN)
	want := []string{
		"Its command is now /fuel-log (it was /fuel)",
		"It now also says 'spent'",
		"It now also says 'diesel'",
		"It no longer says 'fuel'",
		"Customers can now use it",
		"It now answers with a card",
		"Only these roles can start it now: manager",
		"Added: Tell the owner",
	}
	all := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
	if strings.Contains(all, "{") || strings.Contains(all, "=") {
		t.Errorf("an expression or placeholder shown:\n%s", all)
	}
	if got := Changes(nil, json.RawMessage(changesV1), nil, lang.EN); len(got) != 0 {
		t.Errorf("a first version: %v", got)
	}
	// Made a tool, then no longer one.
	plain := strings.Replace(changesV1, changesV1[strings.Index(changesV1, `"tool":`):strings.Index(changesV1, `"steps":`)], "", 1)
	if got := strings.Join(Changes(json.RawMessage(plain), json.RawMessage(changesV1), nil, lang.EN), "\n"); !strings.Contains(got, "It is now a tool: /fuel") || !strings.Contains(got, "It now answers with words") {
		t.Errorf("made a tool: %s", got)
	}
	if got := strings.Join(Changes(json.RawMessage(changesV1), json.RawMessage(plain), nil, lang.EN), "\n"); !strings.Contains(got, "It is no longer a tool (/fuel is gone)") {
		t.Errorf("no longer a tool: %s", got)
	}
	// In Pidgin (a draft): the business's words kept as written.
	if got := strings.Join(Changes(json.RawMessage(changesV1), json.RawMessage(v2), nil, lang.PCM), "\n"); !strings.Contains(got, "'spent'") || !strings.Contains(got, "Customers fit use am now") {
		t.Errorf("pidgin: %s", got)
	}
}
