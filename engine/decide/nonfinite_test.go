package decide

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
)

// An expression that divides doubles by zero gives infinity or NaN, which
// JSON cannot hold: it is the step's expression error (as an integer
// division by zero is), not a panic that takes the process down before the
// event is recorded.
func TestNonFiniteExpressionFailsTheStep(t *testing.T) {
	for _, expr := range []string{
		"=1.0 / 0.0",
		"=-1.0 / 0.0",
		"=double(trigger.body.a) / double(trigger.body.b)",
		"=double(0) / double(0)",
		"=[1.0, 1.0 / 0.0]",
		"={'x': 1.0 / 0.0}",
	} {
		t.Run(expr, func(t *testing.T) {
			s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"`+strings.ReplaceAll(expr, `"`, `\"`)+`"}}`, ""),
				map[string]any{"body": map[string]any{"a": 1.0, "b": 0.0}})
			if !s.has(history.StepFailed, "pay") {
				t.Fatalf("the step should fail: %s", s.types())
			}
			e, _ := s.payload(history.StepFailed, "pay")["error"].(map[string]any)
			if e["kind"] != "expression" || !strings.Contains(e["message"].(string), "not a finite number") {
				t.Errorf("failure = %v", e)
			}
			if s.last().Type != history.RunFailed {
				t.Errorf("the run should end: %s", s.types())
			}
		})
	}
}

// A finite double is as before.
func TestFiniteDoubleIsKept(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"=double(trigger.body.a) / 4.0"}}`, ""),
		map[string]any{"body": map[string]any{"a": 10.0}})
	if in := s.payload(history.StepScheduled, "pay")["input"]; in.(map[string]any)["amount"] != 2.5 {
		t.Errorf("input = %v", in)
	}
}

// The event that cannot be recorded is an error of the decision, with
// nothing recorded, never a panic; once one has failed no more are taken.
func TestEmitThatCannotBeRecordedIsAnErrorNotAPanic(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"a","type":"transform","config":{"output":"x"}}`, ""), map[string]any{})
	_ = s
	st, err := Fold(s.def, s.h)
	if err != nil {
		t.Fatal(err)
	}
	d := st.d.clone()
	before := len(d.out)
	d.emit(history.StepCompleted, "zz", 0, history.CompletedPayload{Output: map[string]any{"v": math.Inf(1)}})
	if d.fault == nil || !strings.Contains(d.fault.Error(), "marshal") {
		t.Fatalf("fault = %v", d.fault)
	}
	if len(d.out) != before {
		t.Errorf("an event that cannot be written was kept: %v", d.out)
	}
	first := d.fault
	d.emit(history.StepCompleted, "zz", 0, history.CompletedPayload{Output: "fine"})
	if !errors.Is(d.fault, first) || len(d.out) != before {
		t.Errorf("after a fault nothing more is recorded: %v %v", d.fault, d.out)
	}
}
