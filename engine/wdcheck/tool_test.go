package wdcheck_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/templates"
)

// toolDoc is a manual workflow with steps, outputs and a tool block.
func toolDoc(tool, steps, extra string) []byte {
	if extra != "" {
		extra = "," + extra
	}
	return []byte(`{"schema":"wd/v1","id":"wf_t","version":1,"name":"T","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","properties":{"who":{"type":"string"}}}},
		"tool":` + tool + `,"steps":` + steps + extra + `}`)
}

const (
	readStep  = `[{"id":"look","type":"http","config":{"method":"GET","url":"https://api.example.com/x"}}]`
	tellStep  = `[{"id":"tell","type":"connector","connector":"notify@1","action":"tell","input":{"message":"Hello"}}]`
	payStep   = `[{"id":"pay","type":"capability","uses":"pay_bank","connection":"rail","input":{"amount_kobo":100,"bank_code":"058","account_number":"0123456789","account_name":"Ada","narration":"Fuel"}}]`
	codeStep  = `[{"id":"calc","type":"code","config":{"language":"javascript","source":"export default function (i) { return {} }"}}]`
	plainTool = `{"name":"Leave left","command":"leave-left","about":"Days of leave left"}`
)

// The rules that need the platform (made tools §12): Taskiem's own words
// are refused, and a tool for customers may not pay.
func TestToolPlatformRules(t *testing.T) {
	reg := registry(t)
	for name, c := range map[string]struct {
		doc        []byte
		path, want string
	}{
		"a tool":                  {doc: toolDoc(plainTool, readStep, "")},
		"a reserved command":      {doc: toolDoc(`{"name":"I","command":"invoice","about":"x"}`, readStep, ""), path: "/tool/command", want: "Taskiem's own commands"},
		"run is reserved":         {doc: toolDoc(`{"name":"R","command":"run","about":"x"}`, readStep, ""), path: "/tool/command", want: "Taskiem's own commands"},
		"a phrase like a command": {doc: toolDoc(`{"name":"L","command":"leave-left","about":"x","says":["days left","run leave"]}`, readStep, ""), path: "/tool/says/1", want: "starts like one of Taskiem's own commands"},
		"customers may not pay": {doc: toolDoc(`{"name":"P","command":"refund-me","about":"x","for":"customers"}`, payStep, ""),
			path: "/steps/pay", want: "moves money"},
	} {
		probs := wdcheck.Check(c.doc, reg)
		if c.want == "" {
			if len(probs) > 0 {
				t.Errorf("%s: %v", name, probs)
			}
			continue
		}
		if !slices.ContainsFunc(probs, func(p wdcheck.Problem) bool { return p.Path == c.path && strings.Contains(p.Message, c.want) }) {
			t.Errorf("%s: %v, want %s: %q", name, probs, c.path, c.want)
		}
	}
}

// Care is worked out from the steps, never declared (made tools §8).
func TestToolCare(t *testing.T) {
	reg := registry(t)
	find := expansion.Library(templates.Default())
	for name, c := range map[string]struct {
		doc     []byte
		level   string
		because []string
	}{
		"only reads: answers at once": {toolDoc(plainTool, readStep, ""), wdcheck.CareAnswers, nil},
		"tells someone: yes":          {toolDoc(plainTool, tellStep, ""), wdcheck.CareYes, []string{"tell"}},
		"code cannot be checked: yes": {toolDoc(plainTool, codeStep, ""), wdcheck.CareYes, []string{"calc"}},
		"pays: step-up":               {toolDoc(plainTool, payStep, ""), wdcheck.CareStepUp, []string{"pay"}},
		"a read inside a branch":      {toolDoc(plainTool, `[{"id":"b","type":"branch","config":{"paths":[{"name":"yes","when":"=true","steps":`+readStep+`}]}}]`, ""), wdcheck.CareAnswers, nil},
		"customers always say yes":    {toolDoc(`{"name":"L","command":"leave-left","about":"x","for":"customers"}`, readStep, ""), wdcheck.CareYes, nil},
		"a form asks a person: yes": {toolDoc(plainTool, `[{"id":"ask","type":"form","config":{"title":"Why?","schema":{"type":"object","properties":{"why":{"type":"string"}}},"to":{"email":"a@b.test"}}}]`, ""),
			wdcheck.CareYes, []string{"ask"}},
	} {
		got, err := wdcheck.ToolCare(c.doc, reg, find)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Level != c.level || !slices.Equal(got.Because, c.because) {
			t.Errorf("%s: %+v, want %s %v", name, got, c.level, c.because)
		}
	}
}

func TestReservedWords(t *testing.T) {
	for _, w := range []string{"invoice", "run", "yes", "help", "tools", "pay", "approvals"} {
		if !wdcheck.ReservedCommand(w) {
			t.Errorf("%q not reserved", w)
		}
	}
	for _, w := range []string{"leave-left", "fuel", "restart"} {
		if wdcheck.ReservedCommand(w) {
			t.Errorf("%q reserved", w)
		}
	}
	if wdcheck.ReservedPhrase("days left") || !wdcheck.ReservedPhrase("run payroll") {
		t.Error("phrases")
	}
}
