package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// Personal data in an input's keys is masked like a value, and the call's
// record holds the question as it was sent: only the fields let through
// are redacted in it.
func TestAIStepMasksKeysAndRecordsWhatWasSent(t *testing.T) {
	e := rt.New(t)
	flow := strings.Replace(aiFlow, `"input":{"customer":"=trigger.body.name",`, `"input":{"balances":"={trigger.body.name: 5000}",`, 1)
	wf := e.Publish(t, flow)
	const name, phone = "Adaeze Okafor", "+2348031234567"
	fake := &ai.Fake{Script: []ai.Response{aiAnswer(`{"greeting":"Hi","priority":"low"}`)}}
	e.AI = fake
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"name": name, "phone": phone, "note": "n"}})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run: %s %s", st, types(events(t, e, ref)))
	}
	q := fake.Requests()[0].Messages[0].Text
	if strings.Contains(q, name) || !strings.Contains(q, `"[[name_1]]": 5000`) {
		t.Errorf("the name in a key reached the model: %s", q)
	}
	var raw []byte
	if err := e.DB.Admin.QueryRow(ctx, `SELECT messages FROM ai_interactions WHERE run_id = $1`, ref.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var msgs []ai.Message
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(q, phone, "[phone]"); len(msgs) != 1 || msgs[0].Text != want {
		t.Errorf("recorded:\n%v\nwant:\n%s", msgs, want)
	}
}

// A business moved to a plan without AI after publishing an ai step: the
// step fails for good, without asking the model.
func TestAIStepNeedsThePlan(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, aiFlow)
	fake := &ai.Fake{Script: []ai.Response{aiAnswer(`{"greeting":"Hi","priority":"low"}`)}}
	e.AI = fake
	e.AIPlan = func(_ context.Context, tenant uuid.UUID) (bool, error) { return tenant != e.Tenant, nil }
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"name": "Ada", "phone": "+2348031234567", "note": "n"}})
	e.Drain(t)
	f := failureOf(t, e, ref, "ask")
	if f["kind"] != "disabled" || !strings.Contains(f["message"].(string), "plan") || e.Status(t, ref) != "failed" {
		t.Errorf("without the plan: %v %s", f, e.Status(t, ref))
	}
	if len(fake.Requests()) != 0 {
		t.Error("the model was asked without the plan")
	}
}

// A call that cannot be recorded is neither audited nor counted, so its
// answer is not used: the attempt is tried again.
func TestAIStepUnrecordedCallIsRetried(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, aiFlow)
	e.AI = &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		r := aiAnswer(`{"greeting":"Hi","priority":"low"}`)
		return &r, nil
	}}
	if _, err := e.DB.Admin.Exec(ctx, `CREATE FUNCTION test_no_ai_record() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'no room'; END $$;
		CREATE TRIGGER test_no_ai_record BEFORE INSERT ON ai_interactions FOR EACH ROW EXECUTE FUNCTION test_no_ai_record()`); err != nil {
		t.Fatal(err)
	}
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"name": "Ada", "phone": "+2348031234567", "note": "n"}})
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "retryable" || !strings.Contains(f["message"].(string), "recording") {
		t.Errorf("an unrecorded call: %v", f)
	}
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepCompleted && ev.StepID == "ask" {
			t.Fatalf("the answer of an unrecorded call was used: %s", types(events(t, e, ref)))
		}
	}
	// Once it can be recorded, the retry completes the step.
	if _, err := e.DB.Admin.Exec(ctx, `DROP TRIGGER test_no_ai_record ON ai_interactions`); err != nil {
		t.Fatal(err)
	}
	ref = e.Start(t, wf, map[string]any{"body": map[string]any{"name": "Ada", "phone": "+2348031234567", "note": "n"}})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Errorf("recorded: %s", st)
	}
}

// Steps calling at once reserve their share of the budget, so they cannot
// all pass the same check; a reservation lapses if its worker dies, and
// the question sent is bounded.
func TestAIStepReservesBudget(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, aiFlow)
	fake := &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		r := aiAnswer(`{"greeting":"Hi","priority":"low"}`)
		r.Usage = ai.Usage{InputTokens: 10, OutputTokens: 5}
		return &r, nil
	}}
	e.AI = fake
	start := func(note string) runtime.RunRef {
		return e.Start(t, wf, map[string]any{"body": map[string]any{"name": "Ada", "phone": "+2348031234567", "note": note}})
	}
	if err := e.Store.SetLimits(ctx, e.Tenant, map[string]any{"ai_monthly_tokens": 100000}, "test"); err != nil {
		t.Fatal(err)
	}
	// Another step is calling, and has reserved what is left.
	other := uuid.Must(uuid.NewV7())
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO ai_reservations (id, tenant_id, run_id, step_id, tokens, expires_at) VALUES ($1, $2, $1, 'ask', 100000, now() + interval '5 minutes')`, other, e.Tenant); err != nil {
		t.Fatal(err)
	}
	ref := start("n")
	e.Drain(t)
	f := failureOf(t, e, ref, "ask")
	if f["kind"] != "limit" || f["next"] != "fail" || !strings.Contains(f["message"].(string), "not tried again") || len(fake.Requests()) != 0 {
		t.Errorf("over budget with the other step's reservation: %v, %d calls", f, len(fake.Requests()))
	}
	// Its worker died: the reservation lapses.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE ai_reservations SET expires_at = now() - interval '1 second' WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	ref = start("n")
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" || len(fake.Requests()) != 1 {
		t.Fatalf("after the reservation lapsed: %s, %d calls", st, len(fake.Requests()))
	}
	var left int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM ai_reservations WHERE tenant_id = $1`, e.Tenant).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d reservations left after the step", left)
	}
	// A question over the bound is not sent. (An input over it cannot be
	// scheduled, but sent as indented JSON a list of small items grows
	// several times over.)
	big := e.Publish(t, strings.Replace(strings.Replace(aiFlow, `"id":"wf_ai"`, `"id":"wf_aiBig"`, 1),
		`"note":"=trigger.body.note"}`, `"note":"=trigger.body.note","items":"=trigger.body.items"}`, 1))
	items := make([]any, 50000)
	for i := range items {
		items[i] = ""
	}
	ref = e.Start(t, big, map[string]any{"body": map[string]any{"name": "Ada", "phone": "+2348031234567", "note": "n", "items": items}})
	e.Drain(t)
	if f := failureOf(t, e, ref, "ask"); f["kind"] != "input_too_large" || len(fake.Requests()) != 1 {
		t.Errorf("a question over the bound: %v, %d calls", f, len(fake.Requests()))
	}
}

var errSlowDown = slowDown{}

type slowDown struct{}

func (slowDown) Error() string { return "slow down" }
