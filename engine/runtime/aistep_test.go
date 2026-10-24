package runtime_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const aiFlow = `{"schema":"wd/v1","id":"wf_ai","version":1,"name":"ai","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","properties":{"name":{"type":"string","x-pii":"name"},"phone":{"type":"string","x-pii":"phone"},"note":{"type":"string"}}}},
  "steps":[
    {"id":"ask","type":"ai","retry":{"max":2,"initial":"10ms","backoff":"fixed"},
     "config":{"prompt":"=\"Write a short greeting for \" + trigger.body.name + \" and rate the note's priority.\"",
       "output_schema":{"type":"object","required":["greeting","priority"],"properties":{"greeting":{"type":"string"},"priority":{"enum":["low","high"]}}},
       "personal_data":["phone"]},
     "input":{"customer":"=trigger.body.name","phone":"=trigger.body.phone","note":"=trigger.body.note"}},
    {"id":"after","type":"transform","needs":["ask"],"config":{"output":{"greeting":"=steps.ask.output.greeting","priority":"=steps.ask.output.priority"}}}]}`

func aiAnswer(text string) ai.Response { return ai.Response{Text: text, JSON: json.RawMessage(text)} }

func failureOf(t *testing.T, e *rt.Env, ref runtime.RunRef, step string) map[string]any {
	t.Helper()
	var last map[string]any
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepFailed && ev.StepID == step {
			last = payloadOf(t, ev)["error"].(map[string]any)
		}
	}
	if last == nil {
		t.Fatalf("no %s failure: %s", step, types(events(t, e, ref)))
	}
	return last
}

// An ai step (decision 0050) asks the model with the run's personal data
// masked, except the fields it lets through; the answer's placeholders are
// put back and the output is sealed; every call is recorded and counted.
func TestAIStep(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, aiFlow)
	const name, phone = "Adaeze Okafor", "+2348031234567"
	start := func() runtime.RunRef {
		return e.Start(t, wf, map[string]any{"body": map[string]any{"name": name, "phone": phone, "note": "Urgent: mail ada@example.com today"}})
	}
	fake := &ai.Fake{Script: []ai.Response{aiAnswer(`{"greeting":"Hello [[name_1]]!","priority":"high"}`)}}
	e.AI = fake
	ref := start()
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run: %s %s", st, types(events(t, e, ref)))
	}
	req := fake.Requests()[0]
	q := req.Messages[0].Text
	if strings.Contains(q, name) || strings.Contains(q, "ada@example.com") || !strings.Contains(q, "[[name_1]]") || !strings.Contains(q, "[[email_1]]") {
		t.Errorf("the question leaks or lacks placeholders: %s", q)
	}
	if !strings.Contains(q, phone) {
		t.Errorf("the phone, let through, is missing: %s", q)
	}
	if req.Schema["additionalProperties"] != false {
		t.Errorf("the schema sent is not closed: %v", req.Schema)
	}
	// The answer has the name back, and history keeps it sealed.
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var greeting any
	for _, ev := range opened {
		if ev.Type == history.StepCompleted && ev.StepID == "after" {
			greeting = payloadOf(t, ev)["output"].(map[string]any)["greeting"]
		}
	}
	if greeting != "Hello "+name+"!" {
		t.Errorf("greeting: %v", greeting)
	}
	for _, ev := range events(t, e, ref) {
		if strings.Contains(string(ev.Payload), name) || strings.Contains(string(ev.Payload), phone) {
			t.Errorf("plaintext personal data in %s(%s): %s", ev.Type, ev.StepID, ev.Payload)
		}
	}
	// The call is recorded against the run and counted.
	var n, tokens int64
	var msgs string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*), COALESCE(sum(total_tokens), 0), COALESCE(string_agg(messages::text, ''), '') FROM ai_interactions WHERE run_id = $1 AND step_id = 'ask'`, ref.ID).Scan(&n, &tokens, &msgs); err != nil {
		t.Fatal(err)
	}
	if n != 1 || tokens == 0 || strings.Contains(msgs, name) || strings.Contains(msgs, phone) {
		t.Errorf("recorded %d calls, %d tokens: %s", n, tokens, msgs)
	}

	// An answer that does not match is sent back once.
	fake = &ai.Fake{Script: []ai.Response{aiAnswer(`{"greeting":"Hi"}`), aiAnswer(`{"greeting":"Hi","priority":"low"}`)}}
	e.AI = fake
	ref = start()
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" || len(fake.Requests()) != 2 || !strings.Contains(fake.Requests()[1].Messages[2].Text, "priority") {
		t.Errorf("a corrected answer: %s, %d requests", st, len(fake.Requests()))
	}
	// Twice wrong fails the step for good.
	e.AI = &ai.Fake{Script: []ai.Response{{Text: "not json"}}}
	ref = start()
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "invalid_output" || e.Status(t, ref) != "failed" {
		t.Errorf("twice wrong: %v", f)
	}
	// A refusal is not retried.
	e.AI = &ai.Fake{Script: []ai.Response{{StopReason: ai.StopRefusal}}}
	ref = start()
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "refused" {
		t.Errorf("refusal: %v", f)
	}
	// A rate limit is retried.
	calls := 0
	e.AI = &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		calls++
		if calls == 1 {
			return nil, &ai.StatusError{Status: 429, Err: errSlowDown}
		}
		r := aiAnswer(`{"greeting":"Hi","priority":"low"}`)
		return &r, nil
	}}
	ref = start()
	rt.WaitFor(t, 5*time.Second, "the retried ai step", func() bool { e.Drain(t); return e.Status(t, ref) == "completed" })

	// The business turns AI steps off.
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO governance_settings (tenant_id, updated_by, ai_steps) VALUES ($1, 'test', false)`, e.Tenant); err != nil {
		t.Fatal(err)
	}
	ref = start()
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "disabled" {
		t.Errorf("turned off: %v", f)
	}
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE governance_settings SET ai_steps = true WHERE tenant_id = $1`, e.Tenant); err != nil {
		t.Fatal(err)
	}
	// The month's budget is spent.
	if err := e.Store.SetLimits(ctx, e.Tenant, map[string]any{"ai_monthly_tokens": 1}, "test"); err != nil {
		t.Fatal(err)
	}
	ref = start()
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "limit" {
		t.Errorf("over budget: %v", f)
	}
	// No model on the deployment.
	if err := e.Store.SetLimits(ctx, e.Tenant, map[string]any{"ai_monthly_tokens": nil}, "test"); err != nil {
		t.Fatal(err)
	}
	e.AI = nil
	ref = start()
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "disabled" {
		t.Errorf("no model: %v", f)
	}
}

var errSlowDown = slowDown{}

type slowDown struct{}

func (slowDown) Error() string { return "slow down" }
