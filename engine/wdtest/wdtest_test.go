package wdtest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// The repository's own workflow tests (flows/) all pass.
func TestRepositoryWorkflowTests(t *testing.T) {
	files, err := wdtest.Discover([]string{"../../flows"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow tests found under flows/")
	}
	reg := registry(t)
	for _, path := range files {
		f, err := wdtest.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		results, err := f.Run(reg)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if !r.Passed() {
				t.Errorf("%s: %s:\n  %s\n  trace:\n    %s", path, r.Case, strings.Join(r.Failures, "\n  "), strings.Join(r.Trace, "\n    "))
			}
		}
	}
}

const wfDoc = `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[
  {"id":"ok","type":"approval","config":{"role":"ops"}},
  {"id":"pay","type":"connector","connector":"iswallet@1","action":"transfer","needs":["ok"],
   "input":{"from_wallet_id":"a","to_wallet_id":"b","amount":"=trigger.amount"},"retry":{"max":2,"initial":"1m"}}]}`

func run(t *testing.T, c wdtest.Case) wdtest.Result {
	t.Helper()
	def, err := wd.Load([]byte(wfDoc))
	if err != nil {
		t.Fatal(err)
	}
	return wdtest.RunCase(def, registry(t), nil, c)
}

// Failing expectations are reported, each with a reason.
func TestFailuresAreExplained(t *testing.T) {
	r := run(t, wdtest.Case{
		Name:    "wrong",
		Trigger: map[string]any{"amount": 5},
		Expect:  wdtest.Expect{Status: "completed"},
	})
	if r.Passed() || r.Status != "blocked" || !strings.Contains(r.Failures[0], "approval ok has no decision") {
		t.Errorf("got %s %v", r.Status, r.Failures)
	}
	r = run(t, wdtest.Case{
		Name:      "no mock",
		Trigger:   map[string]any{"amount": 5},
		Approvals: map[string]wdtest.ApprovalMock{"ok": {Decision: "approved"}},
		Expect:    wdtest.Expect{Status: "completed", Outputs: map[string]any{"pay": map[string]any{"x": 1}}},
	})
	if r.Passed() || !strings.Contains(strings.Join(r.Failures, "|"), "no mock for pay") || len(r.Trace) == 0 {
		t.Errorf("got %v", r.Failures)
	}
}

// A write whose outcome stays unknown parks the run once retries run out,
// on the virtual clock (minutes of backoff take no time).
func TestUnknownOutcomeParks(t *testing.T) {
	r := run(t, wdtest.Case{
		Name:      "park",
		Trigger:   map[string]any{"amount": 5},
		Approvals: map[string]wdtest.ApprovalMock{"ok": {Decision: "approved"}},
		Mocks:     map[string]wdtest.Mocks{"pay": {{Error: &wdtest.MockError{Kind: "unknown_outcome"}}}},
		Expect:    wdtest.Expect{Status: "needs_reconciliation", Steps: map[string]string{"pay": "parked"}, Calls: map[string]int{"pay": 3}},
	})
	if !r.Passed() {
		t.Errorf("%v\n%v", r.Failures, r.Trace)
	}
}

// A test supplies the approval policies its workflow names; the levels
// show in what the step requested.
func TestPolicies(t *testing.T) {
	doc := `{"schema":"wd/v1","id":"wf_p","version":1,"name":"p","trigger":{"type":"manual"},"steps":[
	  {"id":"ok","type":"approval","config":{"policy":"two_step","subject":{"n":"=trigger.n"}}}]}`
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	pol := map[string]json.RawMessage{"two_step": json.RawMessage(`{"rules":[{"levels":[{"role":"a"},{"role":"b"}]}]}`)}
	r := wdtest.RunCaseWithPolicies(def, registry(t), nil, pol, wdtest.Case{Name: "approved", Trigger: map[string]any{"n": 1},
		Approvals: map[string]wdtest.ApprovalMock{"ok": {Decision: "approved"}}, Expect: wdtest.Expect{Status: "completed"}})
	if !r.Passed() {
		t.Errorf("%v", r.Failures)
	}
	r = wdtest.RunCase(def, registry(t), nil, wdtest.Case{Name: "no policy", Trigger: map[string]any{"n": 1}, Expect: wdtest.Expect{Status: "failed", Error: "was not active"}})
	if !r.Passed() {
		t.Errorf("%v", r.Failures)
	}
}
