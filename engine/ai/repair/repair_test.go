package repair_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/repair"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
	"github.com/israel-duff/taskiem/templates"
)

const flow = `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[
 {"id":"notify","type":"transform","config":{"output":{"to":"=trigger.body.customer.name"}}}]}`

const fixed = `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[
 {"id":"notify","type":"transform","config":{"output":{"to":"=has(trigger.body.customer) ? trigger.body.customer.name : 'unknown'"}}}]}`

func def(t *testing.T, doc string) *wd.Definition {
	t.Helper()
	d, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestClassify(t *testing.T) {
	d := def(t, flow)
	e := func(kind, msg, next string) *history.Error {
		return &history.Error{Kind: kind, Message: msg, Next: next}
	}
	cases := []struct {
		name string
		f    repair.Failure
		want repair.Class
		sure bool
	}{
		{"503", repair.Failure{Step: "pay", Error: e("retryable", "http 503: down (gave up after 4 attempts)", "fail")}, repair.Transient, true},
		{"not sent", repair.Failure{Step: "pay", Error: e("not_sent", "connection refused", "fail")}, repair.Transient, true},
		{"401", repair.Failure{Step: "pay", Error: e("fatal", "http 401: invalid api key", "fail")}, repair.Credential, true},
		{"no connection", repair.Failure{Step: "pay", Error: e("fatal", "no active paystack@1 connection in prod: not found", "fail")}, repair.Credential, true},
		{"parked", repair.Failure{RunStatus: "needs_reconciliation", Step: "pay", Error: e("unknown_outcome", "reset after send", "park")}, repair.UnknownOutcome, true},
		{"maybe applied", repair.Failure{Step: "pay", Error: &history.Error{Kind: "retryable", Message: "x", Next: "fail", MaybeApplied: true}}, repair.UnknownOutcome, true},
		{"drift", repair.Failure{Step: "notify", Error: e("expression", "no such key: name", "fail"),
			Drift: []repair.Drift{{Connector: "x@1", Action: "get", Path: "/name", Kind: "missing"}}}, repair.SchemaDrift, true},
		{"drift reason", repair.Failure{Reason: "drift", RunStatus: "completed"}, repair.SchemaDrift, true},
		{"missing trigger field", repair.Failure{Step: "notify", Error: e("expression", "no such key: customer", "fail")}, repair.Data, true},
		{"422", repair.Failure{Step: "pay", Error: e("fatal", "http 422: amount is required", "fail")}, repair.Data, true},
		{"other expression", repair.Failure{Step: "other", Error: e("expression", "division by zero", "fail")}, repair.Logic, false},
		{"run timeout", repair.Failure{RunError: e("timeout", "run exceeded settings.timeout", "fail")}, repair.Transient, false},
	}
	for _, c := range cases {
		got := repair.Classify(c.f, d)
		if got.Class != c.want || got.Certain != c.sure {
			t.Errorf("%s: got %+v, want %s certain=%v", c.name, got, c.want, c.sure)
		}
	}
}

func envelope(class, doc, test string) ai.Response {
	raw, _ := json.Marshal(map[string]any{"class": class, "explanation": "The customer is missing; default it.", "workflow": doc,
		"test": map[string]any{"name": "missing customer", "case": test}})
	return ai.Response{Text: string(raw)}
}

func TestCertainActionsNeverCallTheModel(t *testing.T) {
	f := &ai.Fake{}
	r := &repair.Repairer{Provider: f, Connectors: connector.NewRegistry()}
	for _, cl := range []repair.Classification{{Class: repair.Transient, Certain: true}, {Class: repair.Credential, Certain: true}, {Class: repair.UnknownOutcome, Certain: true}} {
		res, err := r.Repair(context.Background(), repair.Request{Classification: cl, Definition: json.RawMessage(flow), Failure: repair.Failure{Step: "pay"}})
		if err != nil || res.Class != cl.Class || res.Definition != nil || res.Explanation == "" {
			t.Errorf("%s: %+v %v", cl.Class, res, err)
		}
	}
	if len(f.Requests()) != 0 {
		t.Errorf("the model was asked %d times", len(f.Requests()))
	}
}

func TestPatchIsCheckedAndRedacted(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope("data", fixed, `{"trigger":{"body":{}},"expect":{"status":"completed"}}`)}}
	r := &repair.Repairer{Provider: f, Connectors: connector.NewRegistry()}
	var checked int
	res, err := r.Repair(context.Background(), repair.Request{
		Classification: repair.Classification{Class: repair.Data, Certain: true},
		Failure:        repair.Failure{Step: "notify", Error: &history.Error{Kind: "expression", Message: "no such key: customer"}},
		Definition:     json.RawMessage(flow),
		Run:            map[string]any{"trigger": map[string]any{"body": map[string]any{"email": "ada@example.com", "bvn": "22198765432"}}},
		Check: func(doc []byte, test *wdtest.Case) repair.Check {
			checked++
			if test == nil || test.Name != "missing customer" {
				t.Errorf("test: %+v", test)
			}
			return repair.Check{Passed: true}
		},
	})
	if err != nil || !res.Passed || res.Attempts != 1 || checked != 1 || res.ClassifiedBy != "rules" {
		t.Fatalf("%+v %v", res, err)
	}
	sent := ai.PromptText(f.Requests()[0])
	for _, leak := range []string{"ada@example.com", "22198765432"} {
		if strings.Contains(sent, leak) {
			t.Errorf("%s reached the model", leak)
		}
	}
	if !strings.Contains(sent, "<workflow>") || !strings.Contains(sent, "class data") {
		t.Errorf("prompt: %.300s", sent)
	}
}

func TestShadowFailureIsFedBackThreeTimes(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope("data", fixed, "")}}
	r := &repair.Repairer{Provider: f, Connectors: connector.NewRegistry()}
	res, err := r.Repair(context.Background(), repair.Request{
		Classification: repair.Classification{Class: repair.Logic},
		Definition:     json.RawMessage(flow),
		Check: func([]byte, *wdtest.Case) repair.Check {
			return repair.Check{Feedback: []string{"the shadow run ended failed; failed steps: notify"}}
		},
	})
	if err != nil || res.Passed || res.Attempts != 3 || res.ClassifiedBy != "model" || res.Class != repair.Data {
		t.Fatalf("%+v %v", res, err)
	}
	reqs := f.Requests()
	if len(reqs) != 3 || !strings.Contains(ai.PromptText(reqs[2]), "failed steps: notify") {
		t.Errorf("feedback not sent: %d requests", len(reqs))
	}
}

func TestUnchangedOrRenamedPatchIsAProblem(t *testing.T) {
	renamed := strings.Replace(fixed, "wf_t", "wf_other", 1)
	f := &ai.Fake{Script: []ai.Response{envelope("data", flow, ""), envelope("data", renamed, ""), envelope("data", fixed, "")}}
	r := &repair.Repairer{Provider: f, Connectors: connector.NewRegistry()}
	checks := 0
	res, err := r.Repair(context.Background(), repair.Request{
		Classification: repair.Classification{Class: repair.Data, Certain: true}, Definition: json.RawMessage(flow),
		Check: func([]byte, *wdtest.Case) repair.Check { checks++; return repair.Check{Passed: true} },
	})
	if err != nil || !res.Passed || res.Attempts != 3 || checks != 1 {
		t.Fatalf("%+v %v (checks %d)", res, err, checks)
	}
}

type spent struct{}

func (spent) Allow(context.Context) error { return ai.ErrBudgetExhausted }

func TestBudget(t *testing.T) {
	f := &ai.Fake{}
	r := &repair.Repairer{Provider: f, Connectors: connector.NewRegistry(), Meter: spent{}}
	_, err := r.Repair(context.Background(), repair.Request{Classification: repair.Classification{Class: repair.Data, Certain: true}, Definition: json.RawMessage(flow)})
	if !errors.Is(err, ai.ErrBudgetExhausted) || len(f.Requests()) != 0 {
		t.Fatalf("%v, %d requests", err, len(f.Requests()))
	}
}

const payManifest = `
manifest: connector/v1
id: pay
version: 1.0.0
name: Pay
category: payments
auth: { type: none }
actions:
  transfer:
    title: Transfer
    class: unsafe_write
    input: { type: object, properties: { amount: { type: integer } } }
`

func payRegistry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	pay := connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) { return connector.Response{}, nil })
	if err := reg.Register(&connector.Connector{Manifest: connector.MustParse([]byte(payManifest)), Actions: map[string]connector.Action{"transfer": pay}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// policyProblems is the problems the repairer finds in a patch of orig.
func policyProblems(t *testing.T, r *repair.Repairer, orig, patch string) []string {
	t.Helper()
	r.Provider = &ai.Fake{Script: []ai.Response{envelope("data", patch, "")}}
	r.MaxAttempts = 1
	res, err := r.Repair(context.Background(), repair.Request{
		Classification: repair.Classification{Class: repair.Data, Certain: true}, Definition: json.RawMessage(orig),
		Check: func([]byte, *wdtest.Case) repair.Check { return repair.Check{Passed: true} },
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range res.Problems {
		out = append(out, p.Path)
	}
	return out
}

// Every payment that had an approval before keeps one: a patch that moves
// the approval from one payment to another is refused, though the number
// of unapproved payments stays the same.
func TestPatchKeepsEachPaymentsApproval(t *testing.T) {
	doc := func(aNeeds, bNeeds string) string {
		return `{"schema":"wd/v1","id":"wf_p","version":1,"name":"p","trigger":{"type":"manual"},"steps":[
		 {"id":"ok","type":"approval","config":{"role":"finance","count":1}},
		 {"id":"pay_a","type":"connector","connector":"pay@1","action":"transfer","needs":[` + aNeeds + `],"input":{"amount":1}},
		 {"id":"pay_b","type":"connector","connector":"pay@1","action":"transfer","needs":[` + bNeeds + `],"input":{"amount":2}}]}`
	}
	r := &repair.Repairer{Connectors: payRegistry(t)}
	if got := policyProblems(t, r, doc(`"ok"`, ``), doc(``, `"ok"`)); strings.Join(got, ",") != "/steps/pay_a" {
		t.Errorf("approval moved off pay_a: problems %v", got)
	}
	if got := policyProblems(t, r, doc(`"ok"`, ``), doc(`"ok"`, `"ok"`)); len(got) != 0 {
		t.Errorf("approval added to pay_b: problems %v", got)
	}
}

// A template step's template is found where the caller says (the tenant's
// callable workflows), so a patch that calls one that pays unapproved is
// refused.
func TestPatchPolicyReadsTheTenantsTemplates(t *testing.T) {
	fs := fstest.MapFS{"library/payall.json": &fstest.MapFile{Data: []byte(`{"id":"payall","title":"Pay all","summary":"S","description":"D",
	 "category":"notifications","tags":[],"connectors":["pay"],"params":[],"callable":true,
	 "outputs":[{"name":"x","type":"integer","title":"X","description":"X","value":"=1"}],
	 "definition":{"schema":"wd/v1","id":"wf_payall","version":1,"name":"Pay all","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{}}},
	  "steps":[{"id":"pay","type":"connector","connector":"pay@1","action":"transfer","input":{"amount":1}}]}}`)}}
	lib, err := templates.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	orig := `{"schema":"wd/v1","id":"wf_p","version":1,"name":"p","trigger":{"type":"manual"},"steps":[
	 {"id":"t","type":"transform","config":{"output":{"x":1}}}]}`
	patch := `{"schema":"wd/v1","id":"wf_p","version":1,"name":"p","trigger":{"type":"manual"},"steps":[
	 {"id":"t","type":"transform","config":{"output":{"x":1}}},
	 {"id":"all","type":"template","template":"payall","needs":["t"],"input":{}}]}`
	r := &repair.Repairer{Connectors: payRegistry(t), Find: expansion.Library(lib)}
	if got := policyProblems(t, r, orig, patch); len(got) == 0 {
		t.Errorf("the tenant's template's unapproved payment was not found")
	}
}
