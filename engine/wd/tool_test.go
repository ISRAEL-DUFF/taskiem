package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// The tool block (decision 0070) is held to the rules that need only the
// definition (made tools §5, §7, §11, §12).
func TestToolBlock(t *testing.T) {
	doc := func(trigger, tool, inputs, outputs string) []byte {
		if outputs != "" {
			outputs = `,"outputs":` + outputs
		}
		return []byte(`{"schema":"wd/v1","id":"wf_t","version":1,"name":"T","trigger":` + trigger + `,
			"inputs":{"schema":` + inputs + `},"tool":` + tool + outputs + `,
			"steps":[{"id":"look","type":"transform","config":{"output":{"left":3,"rows":[]}}}]}`)
	}
	const (
		manual  = `{"type":"manual"}`
		inputs  = `{"type":"object","properties":{"who":{"type":"string"}}}`
		outs    = `[{"name":"left","type":"integer","title":"Days left","description":"Leave left","value":"=steps.look.output.left"},{"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows","public":true}]`
		textAns = `{"name":"Leave","command":"leave-left","about":"Days of leave left","answer":{"type":"text","text":"=string(outputs.left) + ' days left'"}}`
	)
	for name, c := range map[string]struct {
		doc        []byte
		path, want string
	}{
		"a tool with a text answer": {doc: doc(manual, textAns, inputs, outs)},
		"a tool with no answer":     {doc: doc(manual, `{"name":"Leave","command":"leave-left","about":"x"}`, inputs, "")},
		"a card": {doc: doc(manual, `{"name":"Leave","command":"leave-left","about":"x","answer":{"type":"card","title":"Leave","lines":[{"label":"Left","value":"=string(outputs.left)"}],
			"buttons":[{"label":"Ask for leave","tool":"ask-leave","inputs":{"days":"=outputs.left"}}]}}`, inputs, outs)},
		"a list": {doc: doc(manual, `{"name":"Rows","command":"rows","about":"x","answer":{"type":"list","from":"rows","item":{"title":"=item.title"},
			"actions":[{"label":"Take it","tool":"take-row","inputs":{"ref":"=item.ref"}}]}}`, inputs, outs)},
		"on a schedule": {doc: doc(`{"type":"schedule","config":{"cron":"0 8 * * *"}}`, `{"name":"Leave","command":"leave-left","about":"x"}`, inputs, ""),
			path: "/tool", want: "its trigger must be manual"},
		"a command with capitals": {doc: doc(manual, `{"name":"Leave","command":"Leave","about":"x"}`, inputs, ""), path: "/tool/command", want: "pattern"},
		"an answer, no outputs":   {doc: doc(manual, textAns, inputs, ""), path: "/tool/answer", want: "declares outputs"},
		"an output not declared": {doc: doc(manual, `{"name":"Leave","command":"leave-left","about":"x","answer":{"type":"text","text":"=outputs.nope"}}`, inputs, outs),
			path: "/tool/answer/text", want: `no output is called "nope"`},
		"another root": {doc: doc(manual, `{"name":"Leave","command":"leave-left","about":"x","answer":{"type":"text","text":"=steps.look.output.left"}}`, inputs, outs),
			path: "/tool/answer/text", want: "undeclared reference"},
		"item outside a list": {doc: doc(manual, `{"name":"Leave","command":"leave-left","about":"x","answer":{"type":"text","text":"=item.x"}}`, inputs, outs),
			path: "/tool/answer/text", want: "only a list's item"},
		"a list from a number": {doc: doc(manual, `{"name":"Rows","command":"rows","about":"x","answer":{"type":"list","from":"left","item":{"title":"=item.title"}}}`, inputs, outs),
			path: "/tool/answer/from", want: "of type list"},
		"seven lines": {doc: doc(manual, `{"name":"L","command":"leave-left","about":"x","answer":{"type":"card","title":"L","lines":[`+strings.Repeat(`{"label":"a","value":"b"},`, 6)+`{"label":"a","value":"b"}]}}`, inputs, outs),
			path: "/tool/answer", want: ""},
		"three row buttons": {doc: doc(manual, `{"name":"Rows","command":"rows","about":"x","answer":{"type":"list","from":"rows","item":{"title":"=item.title"},"actions":[`+
			strings.Repeat(`{"label":"a","tool":"take-row"},`, 2)+`{"label":"a","tool":"take-row"}]}}`, inputs, outs), path: "/tool/answer", want: ""},
		"customers see only public outputs": {doc: doc(manual, `{"name":"L","command":"leave-left","about":"x","for":"customers","answer":{"type":"text","text":"=string(outputs.left)"}}`, inputs, outs),
			path: "/tool/answer/text", want: "not marked public"},
		"customers, a public list": {doc: doc(manual, `{"name":"Rows","command":"rows","about":"x","for":"customers","answer":{"type":"list","from":"rows","item":{"title":"=item.title"}}}`, inputs, outs)},
		"customers, a nested input": {doc: doc(manual, `{"name":"L","command":"leave-left","about":"x","for":"customers"}`, `{"type":"object","properties":{"who":{"type":"object"}}}`, ""),
			path: "/inputs/schema/properties/who", want: "only plain fields"},
		"linked, later":  {doc: doc(manual, `{"name":"L","command":"leave-left","about":"x","for":"linked"}`, inputs, ""), path: "/tool/for", want: "come later"},
		"a phrase twice": {doc: doc(manual, `{"name":"L","command":"leave-left","about":"x","says":["days left","Days  left"]}`, inputs, ""), path: "/tool/says/1", want: "said twice"},
	} {
		probs := wd.Validate(c.doc)
		if c.path == "" {
			if len(probs) > 0 {
				t.Errorf("%s: %v", name, probs)
			}
			continue
		}
		found := false
		for _, p := range probs {
			found = found || strings.HasPrefix(p.Path, c.path) && strings.Contains(p.Message, c.want)
		}
		if !found {
			t.Errorf("%s: %v, want %s: %q", name, probs, c.path, c.want)
		}
	}
}
