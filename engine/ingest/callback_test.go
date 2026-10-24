package ingest_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// callbackFlow sends the run's callback address out (here: records it in
// a transform, as an http step would send it), waits to be called back,
// and reads what the call brought.
const callbackFlow = `{"schema":"wd/v1","id":"wf_kyc","version":1,"name":"kyc",
  "trigger":{"type":"webhook","config":{"path":"/kyc/start","auth":"none"}},
  "steps":[
    {"id":"send","type":"transform","config":{"output":{"url":"=run.callbacks.verified","run":"=run.id"}}},
    {"id":"verified","type":"callback","needs":["send"],"config":{"timeout":"1h"}},
    {"id":"done","type":"transform","needs":["verified"],"config":{"output":{"status":"=steps.verified.output.body.status","ref":"=steps.verified.output.query.ref"}}}]}`

func (w *world) stepOutput(t *testing.T, ref runtime.RunRef, step string) map[string]any {
	t.Helper()
	evs, err := w.Store.RunHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type == history.StepCompleted && ev.StepID == step {
			var p struct {
				Output map[string]any `json:"output"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			return p.Output
		}
	}
	return nil
}

// A callback step pauses the run at an address only the run knows; a call
// to it with the right token resumes the run with what the call brought.
func TestCallbackStepResumesTheRun(t *testing.T) {
	w := newWorld(t)
	wf := w.publish(t, callbackFlow)
	st, out := w.post(t, "/kyc/start", []byte(`{"customer":"c1"}`))
	if st != 202 {
		t.Fatalf("start: %d %v", st, out)
	}
	ref := runtime.RunRef{TenantID: w.Tenant, ID: uuid.MustParse(out["run_id"].(string))}
	w.Drain(t)
	if s := w.Status(t, ref); s != "waiting" && s != "running" {
		t.Fatalf("status %s", s)
	}
	sent := w.stepOutput(t, ref, "send")
	url, _ := sent["url"].(string)
	prefix := "https://hooks.test/hooks/" + w.Tenant.String() + "/callbacks/" + ref.ID.String() + "/"
	if !strings.HasPrefix(url, prefix) || !strings.HasSuffix(url, "/verified") {
		t.Fatalf("callback address %q", url)
	}
	path := strings.TrimPrefix(url, "https://hooks.test/hooks/"+w.Tenant.String())

	// The key never shows: history holds it sealed, and run.* does not
	// carry it.
	evs, _ := w.Store.RunHistory(ctx, ref)
	if raw := string(evs[0].Payload); strings.Contains(raw, "callback_key\":\"") && !strings.Contains(raw, "callback_key\":{") {
		t.Errorf("the callback key is in history in the clear: %s", raw)
	}

	// A wrong token, a wrong run and a wrong step are refused.
	if st, _ := w.post(t, strings.Replace(path, "/verified", "/other", 1), []byte(`{}`)); st != 401 {
		t.Errorf("another step with this token: %d", st)
	}
	bad := strings.Split(path, "/")
	bad[3] = strings.Repeat("0", 32)
	if st, _ := w.post(t, strings.Join(bad, "/"), []byte(`{}`)); st != 401 {
		t.Errorf("a wrong token: %d", st)
	}
	if st, _ := w.post(t, "/callbacks/"+uuid.NewString()+"/"+strings.Repeat("0", 32)+"/verified", []byte(`{}`)); st != 404 {
		t.Errorf("an unknown run: %d", st)
	}

	// The right call resumes the run with its body and query.
	st, out = w.post(t, path+"?ref=KYC-9", []byte(`{"status":"verified","score":88}`))
	if st != 202 || out["delivered"] != true {
		t.Fatalf("callback: %d %v", st, out)
	}
	w.Drain(t)
	if s := w.Status(t, ref); s != "completed" {
		t.Fatalf("status %s", s)
	}
	if got := w.stepOutput(t, ref, "done"); got["status"] != "verified" || got["ref"] != "KYC-9" {
		t.Errorf("done: %v", got)
	}
	// Calling again after the run ended is told so.
	if st, _ := w.post(t, path, []byte(`{}`)); st != 410 {
		t.Errorf("after the run ended: %d", st)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("%d runs", n)
	}
}

// A callback that never comes times the step out; with on_timeout
// continue the run carries on and says so.
func TestCallbackTimesOut(t *testing.T) {
	w := newWorld(t)
	doc := strings.Replace(callbackFlow, `{"timeout":"1h"}`, `{"timeout":"1s","on_timeout":"continue"}`, 1)
	doc = strings.Replace(doc, `"=steps.verified.output.body.status"`, `"=has(steps.verified.output.timed_out)"`, 1)
	doc = strings.Replace(doc, `,"ref":"=steps.verified.output.query.ref"`, ``, 1)
	w.publish(t, doc)
	_, out := w.post(t, "/kyc/start", []byte(`{}`))
	ref := runtime.RunRef{TenantID: w.Tenant, ID: uuid.MustParse(out["run_id"].(string))}
	deadline := time.Now().Add(10 * time.Second)
	for w.Status(t, ref) != "completed" && time.Now().Before(deadline) {
		w.Drain(t)
		time.Sleep(200 * time.Millisecond)
	}
	if got := w.stepOutput(t, ref, "done"); got["status"] != true {
		t.Errorf("done after the timeout: %v (status %s)", got, w.Status(t, ref))
	}
}

// respondFlow answers the webhook call with what the run made of it.
const respondFlow = `{"schema":"wd/v1","id":"wf_quote","version":1,"name":"quote",
  "trigger":{"type":"webhook","config":{"path":"/quote","auth":"none","respond":true}},
  "inputs":{"schema":{"type":"object","required":["kg"],"properties":{"kg":{"type":"integer"}}}},
  "steps":[
    {"id":"price","type":"transform","config":{"output":{"naira":"=trigger.body.kg * 1500"}}},
    {"id":"answer","type":"respond","needs":["price"],"config":{"status":201,"headers":{"X-Quote":"=string(steps.price.output.naira)","Set-Cookie":"no"},
      "body":{"kg":"=trigger.body.kg","naira":"=steps.price.output.naira"}}},
    {"id":"after","type":"transform","needs":["answer"],"config":{"output":{"recorded":true}}}]}`

// A webhook with respond: true answers with the respond step's status,
// headers and body, and the run carries on after it.
func TestWebhookRespondsSynchronously(t *testing.T) {
	w := newWorld(t)
	w.publish(t, respondFlow)
	resp := w.rawPost(t, "/quote", []byte(`{"kg":4}`))
	if resp.status != 201 || resp.header("X-Quote") != "6000" || resp.header("Set-Cookie") != "" || resp.header("Content-Type") != "application/json" {
		t.Fatalf("answer: %d %v %q", resp.status, resp.headers, resp.body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.body), &body); err != nil || body["naira"] != float64(6000) || body["kg"] != float64(4) {
		t.Fatalf("body %q: %v", resp.body, err)
	}
	ref := runtime.RunRef{TenantID: w.Tenant, ID: uuid.MustParse(resp.header("X-Taskiem-Run"))}
	w.Drain(t)
	if s := w.Status(t, ref); s != "completed" {
		t.Errorf("status %s", s)
	}
	if got := w.stepOutput(t, ref, "after"); got["recorded"] != true {
		t.Errorf("the run stopped at the answer: %v", got)
	}

	// A text body is sent as text; a run that fails before answering says
	// so; a duplicate delivery gets the usual 202 with the first run.
	text := strings.Replace(respondFlow, `"body":{"kg":"=trigger.body.kg","naira":"=steps.price.output.naira"}`, `"body":"='NGN ' + string(steps.price.output.naira)"`, 1)
	text = strings.Replace(text, `"id":"wf_quote"`, `"id":"wf_quoteText"`, 1)
	text = strings.Replace(text, `"/quote"`, `"/quote-text"`, 1)
	w.publish(t, text)
	if resp := w.rawPost(t, "/quote-text", []byte(`{"kg":2}`)); resp.status != 201 || resp.body != "NGN 3000" || !strings.HasPrefix(resp.header("Content-Type"), "text/plain") {
		t.Errorf("text answer: %d %q %v", resp.status, resp.body, resp.headers)
	}
	failing := strings.Replace(respondFlow, `"=trigger.body.kg * 1500"`, `"=trigger.body.kg / 0"`, 1)
	failing = strings.Replace(failing, `"id":"wf_quote"`, `"id":"wf_quoteFail"`, 1)
	failing = strings.Replace(failing, `"/quote"`, `"/quote-fail"`, 1)
	w.publish(t, failing)
	if resp := w.rawPost(t, "/quote-fail", []byte(`{"kg":2}`)); resp.status != 500 || !strings.Contains(resp.body, "failed") {
		t.Errorf("failing answer: %d %q", resp.status, resp.body)
	}
}

// The answer waits for work a worker does first: the webhook holds the
// call until the respond step completes.
func TestWebhookRespondWaitsForTheRun(t *testing.T) {
	w := newWorld(t)
	doc := strings.Replace(respondFlow, `{"id":"price","type":"transform","config":{"output":{"naira":"=trigger.body.kg * 1500"}}}`,
		`{"id":"pause","type":"wait","config":{"duration":"300ms"}},{"id":"price","type":"transform","needs":["pause"],"config":{"output":{"naira":"=trigger.body.kg * 1500"}}}`, 1)
	w.publish(t, doc)
	done := make(chan rawResponse, 1)
	go func() { done <- w.rawPost(t, "/quote", []byte(`{"kg":1}`)) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case resp := <-done:
			if resp.status != 201 || !strings.Contains(resp.body, `"naira":1500`) {
				t.Fatalf("answer: %d %q", resp.status, resp.body)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no answer in time")
		}
		w.Drain(t)
		time.Sleep(100 * time.Millisecond)
	}
}

// Without respond: true the usual acknowledgement stands, and a version
// that names a respond step without it does not publish.
func TestRespondNeedsTheTrigger(t *testing.T) {
	w := newWorld(t)
	plain := strings.Replace(respondFlow, `,"respond":true`, ``, 1)
	probs := w.validate(plain)
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "respond: true") {
		t.Errorf("a respond step without the trigger: %v", probs)
	}
	noStep := strings.Replace(respondFlow, `{"id":"answer","type":"respond","needs":["price"],"config":{"status":201,"headers":{"X-Quote":"=string(steps.price.output.naira)","Set-Cookie":"no"},
      "body":{"kg":"=trigger.body.kg","naira":"=steps.price.output.naira"}}},`, ``, 1)
	noStep = strings.Replace(noStep, `"needs":["answer"]`, `"needs":["price"]`, 1)
	if probs := w.validate(noStep); len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "needs a respond step") {
		t.Errorf("respond: true without a step: %v", probs)
	}
	secret := strings.Replace(respondFlow, `"Set-Cookie":"no"`, `"X-Key":"=secrets.api_key"`, 1)
	if probs := w.validate(secret); len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "secrets") {
		t.Errorf("a secret in the answer: %v", probs)
	}
}

// Inside a foreach, run.callbacks.<id> names the iteration's own instance,
// so each item is called back on its own.
func TestCallbackInsideForeach(t *testing.T) {
	w := newWorld(t)
	w.publish(t, `{"schema":"wd/v1","id":"wf_each","version":1,"name":"each",
	  "trigger":{"type":"webhook","config":{"path":"/each","auth":"none"}},
	  "steps":[{"id":"each","type":"foreach","config":{"items":"=['a','b']","steps":[
	    {"id":"send","type":"transform","config":{"output":{"url":"=run.callbacks.cb","item":"=item"}}},
	    {"id":"cb","type":"callback","needs":["send"],"config":{"timeout":"1h"}}]}},
	   {"id":"done","type":"transform","needs":["each"],"config":{"output":{"got":"=steps.each.output.map(o, o.cb.body.n)"}}}]}`)
	_, out := w.post(t, "/each", []byte(`{}`))
	ref := runtime.RunRef{TenantID: w.Tenant, ID: uuid.MustParse(out["run_id"].(string))}
	w.Drain(t)
	for i, inst := range []string{"each[0].cb", "each[1].cb"} {
		sent := w.stepOutput(t, ref, "each["+string(rune('0'+i))+"].send")
		url, _ := sent["url"].(string)
		if !strings.HasSuffix(url, "/"+strings.ReplaceAll(strings.ReplaceAll(inst, "[", "%5B"), "]", "%5D")) {
			t.Fatalf("%s: address %q", inst, url)
		}
		path := strings.TrimPrefix(url, "https://hooks.test/hooks/"+w.Tenant.String())
		if st, out := w.post(t, path, []byte(`{"n":`+string(rune('1'+i))+`}`)); st != 202 || out["delivered"] != true {
			t.Fatalf("%s: %d %v", inst, st, out)
		}
	}
	w.Drain(t)
	if s := w.Status(t, ref); s != "completed" {
		t.Fatalf("status %s", s)
	}
	if got := w.stepOutput(t, ref, "done"); len(got["got"].([]any)) != 2 || got["got"].([]any)[1] != float64(2) {
		t.Errorf("done: %v", got)
	}
}
