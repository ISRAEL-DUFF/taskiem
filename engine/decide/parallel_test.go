package decide

import (
	"reflect"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
)

func TestParallelJoinAllRunsBranchesTogether(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"kyc","steps":[{"id":"bvn","type":"connector","connector":"dojah@1","action":"bvn"}]},
	  {"name":"credit","steps":[
	    {"id":"score","type":"connector","connector":"bureau@1","action":"score"},
	    {"id":"band","type":"transform","needs":["score"],"config":{"output":"=steps.score.output.value > 600 ? 'A' : 'B'"}}]}]}},
	  {"id":"after","type":"transform","needs":["p"],"config":{"output":"=steps.p.output.credit.band"}}`, ""), map[string]any{})
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"bvn", "score"}) {
		t.Fatalf("both branches should start at once, got %v", got)
	}
	s.complete("score", map[string]any{"value": 700})
	if s.has(history.StepCompleted, "p") {
		t.Fatal("parallel completed before every branch did")
	}
	s.complete("bvn", map[string]any{"ok": true})
	out := s.payload(history.StepCompleted, "p")["output"].(map[string]any)
	if out["kyc"].(map[string]any)["bvn"].(map[string]any)["ok"] != true || out["credit"].(map[string]any)["band"] != "A" {
		t.Errorf("output = %v", out)
	}
	if s.payload(history.StepCompleted, "after")["output"] != "A" || s.last().Type != history.RunCompleted {
		t.Errorf("run did not finish: %s", s.types())
	}
}

func TestParallelJoinAllFailsAfterInFlightSettle(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]},
	  {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y"},
	                       {"id":"b2","type":"connector","connector":"x@1","action":"y","needs":["b1"]}]}]}}`, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	if s.has(history.StepFailed, "p") {
		t.Fatal("failed while b1 was still in flight")
	}
	s.complete("b1", nil)
	if s.has(history.StepScheduled, "b2") {
		t.Error("a failing parallel started a new step")
	}
	if e := s.payload(history.StepFailed, "p")["error"].(map[string]any); e["kind"] != "child_failed" || !strings.Contains(e["message"].(string), "a1") {
		t.Errorf("error = %v", e)
	}
	if s.last().Type != history.RunFailed {
		t.Errorf("run should fail: %s", s.types())
	}
}

func TestParallelJoinAnyCancelsAndCompensatesLosers(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"join":"any","branches":[
	  {"name":"hold","steps":[
	    {"id":"reserve","type":"connector","connector":"bank@1","action":"hold","compensate":{"action":"release","input":{"id":"=steps.reserve.output.id"}}},
	    {"id":"confirm","type":"signal","needs":["reserve"],"config":{"event":"bank@1:confirmed","correlation":"=steps.reserve.output.id","timeout":"1h"}}]},
	  {"name":"manual","steps":[{"id":"ok","type":"approval","config":{"role":"ops"}}]}]}},
	  {"id":"after","type":"transform","needs":["p"],"config":{"output":"=has(steps.p.output.manual)"}}`, ""), map[string]any{})
	s.complete("reserve", map[string]any{"id": "h1"})
	if !s.has(history.StepScheduled, "confirm") || !s.has(history.ApprovalRequested, "ok") {
		t.Fatalf("branches did not start: %s", s.types())
	}
	s.external(history.ApprovalDecided, "ok", 0, history.ApprovalDecidedPayload{Decision: "approved", DecidedBy: "u2"}, history.OriginAPI)
	if w := s.payload(history.StepStarted, "p")["winner"]; w != "manual" {
		t.Fatalf("winner = %v", w)
	}
	if !s.has(history.StepCancelled, "confirm") {
		t.Errorf("waiting signal in the losing branch was not cancelled: %s", s.types())
	}
	if s.has(history.StepCancelled, "reserve") {
		t.Error("cancelled a step that had already completed")
	}
	if s.has(history.StepCompleted, "p") {
		t.Fatal("completed before the loser's hold was released")
	}
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"compensate:reserve"}) {
		t.Fatalf("pending = %v", got)
	}
	if in := s.payload(history.StepScheduled, "compensate:reserve")["input"].(map[string]any); in["id"] != "h1" {
		t.Errorf("compensation input = %v", in)
	}
	s.complete("compensate:reserve", nil)
	if out := s.payload(history.StepCompleted, "p")["output"].(map[string]any); len(out) != 1 || out["manual"] == nil {
		t.Errorf("output = %v", out)
	}
	if s.payload(history.StepCompleted, "after")["output"] != true || s.last().Type != history.RunCompleted {
		t.Errorf("run did not finish: %s", s.types())
	}
	// A late signal for the cancelled step changes nothing.
	if out := s.external(history.SignalReceived, "confirm", 0, map[string]any{"event": "bank@1:confirmed"}, history.OriginSignal); len(out) != 0 {
		t.Errorf("decided after the run ended: %v", out)
	}
}

func TestParallelJoinAnyWaitsForALoserWriteUnderWay(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"join":"any","branches":[
	  {"name":"fast","steps":[{"id":"f","type":"transform","needs":["slow_gate"],"config":{"output":1}},
	                          {"id":"slow_gate","type":"wait","config":{"duration":"1m"}}]},
	  {"name":"slow","steps":[{"id":"pay","type":"connector","connector":"bank@1","action":"pay","compensate":{"action":"refund"}},
	                          {"id":"read","type":"connector","connector":"bank@1","action":"get"}]}]}}`, ""), map[string]any{})
	// pay's worker has recorded its intent; read's task has not started.
	s.append(history.EffectIntent, "pay", 1, map[string]any{"key": "k"}, history.OriginWorker)
	s.advance(61e9)
	s.fire("slow_gate", "wait")
	if !s.has(history.StepCancelled, "pay") || !s.has(history.StepCancelled, "read") {
		t.Fatalf("losers not cancelled: %s", s.types())
	}
	if s.has(history.StepCompleted, "p") {
		t.Fatal("completed while a cancelled write could still take effect")
	}
	s.complete("pay", map[string]any{"ref": "p1"})
	if !s.has(history.StepScheduled, "compensate:pay") || s.has(history.StepCompleted, "p") {
		t.Fatalf("the late write was not compensated first: %s", s.types())
	}
	s.complete("compensate:pay", nil)
	if s.last().Type != history.RunCompleted {
		t.Errorf("run did not finish: %s", s.types())
	}
}

func TestParallelJoinAnyFailsOnlyWhenEveryBranchFails(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"join":"any","branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]},
	  {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]}]}}`, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	if s.has(history.StepFailed, "p") {
		t.Fatal("failed while branch b could still win")
	}
	s.failStep("b1", "fatal", "fail")
	if !s.has(history.StepFailed, "p") || s.last().Type != history.RunFailed {
		t.Errorf("should fail once both branches have: %s", s.types())
	}
}

func TestParallelMaxConcurrency(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"max_concurrency":1,"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y"}]},
	  {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y"}]}]}}`, ""), map[string]any{})
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("pending = %v", got)
	}
	s.complete("a1", nil)
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Fatalf("pending = %v", got)
	}
	s.complete("b1", nil)
	if s.last().Type != history.RunCompleted {
		t.Errorf("run did not finish: %s", s.types())
	}
}

func TestParallelInsideForeach(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"each","type":"foreach","config":{"items":"=trigger.xs","steps":[
	  {"id":"p","type":"parallel","config":{"join":"any","branches":[
	    {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y"}]},
	    {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y"}]}]}}]}}`, ""), map[string]any{"xs": []any{1, 2}})
	s.complete("each[0].a1", "first")
	if !s.has(history.StepCancelled, "each[0].b1") || s.has(history.StepCancelled, "each[1].b1") {
		t.Fatalf("cancellation leaked across iterations: %s", s.types())
	}
	s.complete("each[1].b1", "second")
	out := s.payload(history.StepCompleted, "each")["output"].([]any)
	if out[0].(map[string]any)["p"].(map[string]any)["a"] == nil || out[1].(map[string]any)["p"].(map[string]any)["b"] == nil {
		t.Errorf("output = %v", out)
	}
}

func TestForeachFailureDoesNotWaitForUnstartedSteps(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"each","type":"foreach","config":{"items":"=trigger.xs","steps":[
	  {"id":"a","type":"connector","connector":"x@1","action":"y","retry":{"max":0}},
	  {"id":"b","type":"connector","connector":"x@1","action":"y","needs":["a"]}]}}`, ""), map[string]any{"xs": []any{1, 2}})
	s.failStep("each[0].a", "fatal", "fail")
	s.complete("each[1].a", nil) // b must not start now, and must not be waited for
	if s.has(history.StepScheduled, "each[1].b") {
		t.Error("a failing foreach started a new step")
	}
	if !s.has(history.StepFailed, "each") || s.last().Type != history.RunFailed {
		t.Errorf("foreach should fail once in-flight steps settle: %s", s.types())
	}
}
