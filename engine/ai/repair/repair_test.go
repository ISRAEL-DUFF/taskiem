package repair_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/repair"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
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
