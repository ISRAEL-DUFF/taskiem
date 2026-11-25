package wdtest_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// A test states the answer a tool gives (made tools §17, build plan H6):
// the text, a card's fields, a list's rows; it fails when the answer
// changes, says so when the workflow is no tool or the run did not end,
// and a customers' tool's answer is drawn from its public outputs alone.
func TestCaseStatesTheAnswer(t *testing.T) {
	const doc = `{"schema":"wd/v1","id":"wf_status","version":1,"name":"Order status","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string"}}}},
	  "outputs":[
	    {"name":"state","type":"string","title":"State","description":"Where it is","value":"=steps.look.output.state","public":true},
	    {"name":"ref","type":"string","title":"Ref","description":"The order","value":"=steps.look.output.ref","public":true},
	    {"name":"cost","type":"string","title":"Cost","description":"What it cost us","value":"=steps.look.output.cost"}],
	  "tool":{"name":"Order status","command":"order-status","about":"Where your order is","for":"customers",
	    "answer":{"type":"card","title":"=outputs.state","ref":"=outputs.ref","lines":[{"label":"Order","value":"=outputs.ref"}]}},
	  "steps":[{"id":"look","type":"transform","config":{"output":{"ref":"=trigger.body.ref","state":"On its way","cost":"4000"}}}]}`
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	trigger := map[string]any{"body": map[string]any{"ref": "A-1"}}
	card := map[string]any{"title": "On its way", "ref": "A-1", "lines": []any{map[string]any{"label": "Order", "value": "A-1"}}}
	if r := wdtest.RunCase(def, nil, nil, wdtest.Case{Name: "card", Trigger: trigger, Expect: wdtest.Expect{Status: "completed", Answer: card}}); !r.Passed() {
		t.Fatalf("the answer as stated: %v", r.Failures)
	}
	// The answer changes: the test fails, and says what it got.
	card["title"] = "Delivered"
	r := wdtest.RunCase(def, nil, nil, wdtest.Case{Name: "changed", Trigger: trigger, Expect: wdtest.Expect{Answer: card}})
	if r.Passed() || !strings.Contains(strings.Join(r.Failures, "\n"), "answer.title: does not match") || !strings.Contains(strings.Join(r.Failures, "\n"), "On its way") {
		t.Fatalf("a changed answer: %v", r.Failures)
	}

	// A list's rows, and words.
	list, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_list","version":1,"name":"Orders","trigger":{"type":"manual"},"inputs":{"schema":{"type":"object","properties":{}}},
	  "outputs":[{"name":"orders","type":"list","title":"Orders","description":"Open","value":"=steps.look.output.orders"}],
	  "tool":{"name":"Orders","command":"orders-open","about":"Open orders","answer":{"type":"list","from":"orders","title":"Open orders","item":{"title":"=item.ref","sub":"=item.state"}}},
	  "steps":[{"id":"look","type":"transform","config":{"output":{"orders":[{"ref":"A-1","state":"Packed"},{"ref":"A-2","state":"Sent"}]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]any{"title": "Open orders", "rows": []any{map[string]any{"title": "A-1", "sub": "Packed"}, map[string]any{"title": "A-2"}}}
	if r := wdtest.RunCase(list, nil, nil, wdtest.Case{Name: "list", Trigger: map[string]any{"body": map[string]any{}}, Expect: wdtest.Expect{Answer: rows}}); !r.Passed() {
		t.Fatalf("rows: %v", r.Failures)
	}
	rows["rows"] = []any{map[string]any{"title": "A-1"}}
	if r := wdtest.RunCase(list, nil, nil, wdtest.Case{Name: "list", Trigger: map[string]any{"body": map[string]any{}}, Expect: wdtest.Expect{Answer: rows}}); r.Passed() {
		t.Fatal("a row more than stated passed")
	}

	// No tool, or a run that does not end: said.
	plain, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_p","version":1,"name":"p","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":{}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := wdtest.RunCase(plain, nil, nil, wdtest.Case{Name: "no tool", Trigger: map[string]any{"body": map[string]any{}}, Expect: wdtest.Expect{Answer: map[string]any{"text": "x"}}}); !strings.Contains(strings.Join(r.Failures, ""), "declares no tool") {
		t.Fatalf("no tool: %v", r.Failures)
	}
}
