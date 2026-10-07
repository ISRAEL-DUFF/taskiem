package shadow_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/shadow"
	"github.com/israel-duff/taskiem/engine/wd"
)

func reg(t *testing.T) connector.Lookup {
	t.Helper()
	r := connector.NewRegistry()
	if err := r.Register(rt.NewProvider().Connector()); err != nil {
		t.Fatal(err)
	}
	return r
}

func ev(seq int64, typ, step string, attempt int, payload any) history.Event {
	raw, _ := json.Marshal(payload)
	return history.Event{Seq: seq, Type: typ, StepID: step, Attempt: attempt, Payload: raw, RecordedAt: time.Now()}
}

func doc(notify string) string {
	return `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[
	 {"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":"=trigger.body.amount","logical_id":"=trigger.body.id"}},
	 {"id":"notify","type":"connector","needs":["pay"],"connector":"fakepay@1","action":"notify","input":{"logical_id":` + notify + `}}]}`
}

// A failed run's history: pay completed (a write), notify failed on an
// expression over a field the trigger lacks.
func recording(t *testing.T, sealed bool) *shadow.Recording {
	t.Helper()
	email := any("ada@example.com")
	if sealed {
		email = map[string]any{"$pii": "email", "subject": "s", "ct": "x"}
	}
	h := []history.Event{
		ev(1, history.RunStarted, "", 0, history.RunStartedPayload{Trigger: map[string]any{"type": "manual", "body": map[string]any{"amount": 500, "id": "o-1", "email": email}}}),
		ev(2, history.StepScheduled, "pay", 1, history.ScheduledPayload{Kind: history.KindTask, Connector: "fakepay@1", Action: "transfer",
			Input: map[string]any{"amount": 500, "logical_id": "o-1"}}),
		ev(3, history.EffectIntent, "pay", 1, history.IntentPayload{Class: "idempotent_write"}),
		ev(4, history.StepCompleted, "pay", 1, history.CompletedPayload{Output: map[string]any{"reference": "=ref", "status": "success"}}),
		ev(5, history.StepFailed, "notify", 0, history.FailedPayload{Error: history.Error{Kind: "expression", Message: "no such key: customer", Next: "fail"}}),
		ev(6, history.RunFailed, "", 0, history.RunFailedPayload{Error: history.Error{Kind: "failed", Message: "run failed", Next: "fail"}}),
	}
	rec, err := shadow.Record(h)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func load(t *testing.T, d string) *wd.Definition {
	t.Helper()
	def, err := wd.Load([]byte(d))
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestShadowRun(t *testing.T) {
	r := reg(t)
	rec := recording(t, false)
	if rec.Failed != "notify" || rec.Status != "failed" || !rec.Steps["pay"].Write {
		t.Fatalf("recording: %+v", rec)
	}
	// The failing definition fails in the shadow too.
	bad := load(t, doc(`"=trigger.body.id + '-' + trigger.body.customer.name"`))
	if rep := shadow.Run(bad, r, rec, rec.Case("shadow", bad, r)); rep.Passed {
		t.Errorf("the failing definition passed: %+v", rep)
	}
	// The fix passes, replaying pay's recorded output (an "=" string stays a string).
	good := load(t, doc(`"=trigger.body.id + '-' + (has(trigger.body.customer) ? trigger.body.customer.name : 'x')"`))
	rep := shadow.Run(good, r, rec, rec.Case("shadow", good, r))
	if !rep.Passed || !rep.Resumable() || len(rep.Replayed) != 1 {
		t.Fatalf("fix: %+v", rep)
	}
	// A patch that changes what the completed write sends cannot resume.
	moved := load(t, strings.Replace(doc(`"=trigger.body.id"`), `"=trigger.body.amount"`, `"=trigger.body.amount * 2"`, 1))
	rep = shadow.Run(moved, r, rec, rec.Case("shadow", moved, r))
	if !rep.Passed || rep.Resumable() || len(rep.Mismatches) != 1 {
		t.Fatalf("moved: %+v", rep)
	}
	// An input the action's schema refuses fails the shadow run.
	wrong := load(t, doc(`"=1"`))
	if rep := shadow.Run(wrong, r, rec, rec.Case("shadow", wrong, r)); rep.Passed || len(rep.InputProblems) == 0 {
		t.Errorf("schema-breaking input passed: %+v", rep)
	}
}

func TestRedactedRecordingHoldsNoSealedValues(t *testing.T) {
	r := reg(t)
	rec := recording(t, true)
	good := load(t, doc(`"=trigger.body.id"`))
	red := rec.Redacted(good, r)
	raw, _ := json.Marshal(red)
	if strings.Contains(string(raw), `"$pii"`) || !strings.Contains(string(raw), "[email]") {
		t.Errorf("redacted: %s", raw)
	}
	if rep := shadow.RunCase(good, r, nil, red.Case("regression", good, r)); !rep.Passed() {
		t.Errorf("regression on redacted data: %v", rep.Failures)
	}
}
