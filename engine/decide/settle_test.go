package decide

import (
	"encoding/json"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
)

// failWith records a worker's failure of the step's last attempt.
func (s *sim) failWith(inst string, e history.Error) []NewEvent {
	if e.Message == "" {
		e.Message = "boom"
	}
	return s.external(history.StepFailed, inst, s.lastAttempt(inst), history.FailedPayload{Error: e}, history.OriginWorker)
}

// lastFailure is the error of the step's latest StepFailed.
func (s *sim) lastFailure(inst string) history.Error {
	var p history.FailedPayload
	for _, e := range s.h {
		if e.Type == history.StepFailed && e.StepID == inst {
			p = history.FailedPayload{}
			_ = json.Unmarshal(e.Payload, &p)
		}
	}
	return p.Error
}

// settling reports whether the step's last scheduled attempt only settles.
func (s *sim) settling(inst string) bool {
	var p history.ScheduledPayload
	for _, e := range s.h {
		if e.Type == history.StepScheduled && e.StepID == inst {
			p = history.ScheduledPayload{}
			_ = json.Unmarshal(e.Payload, &p)
		}
	}
	return p.Settle
}

var unknown = history.Error{Kind: "unknown_outcome", Next: "retry", MaybeApplied: true}

const twoBranches = `{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]},
	  {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y","compensate":{"action":"undo"}}]}]}}`

// A step whose retry falls due after a step beside it failed for good is
// given up: nothing would ever schedule the retry, and the parallel step
// would wait for it for ever.
func TestFailingScopeGivesUpARetryThatIsDue(t *testing.T) {
	s := newSim(t, wdDoc(twoBranches, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	s.failStep("b1", "retryable", "retry")
	if got := s.pendingTasks(); len(got) != 0 {
		t.Fatalf("a failing parallel scheduled %v: %s", got, s.types())
	}
	if e := s.lastFailure("b1"); e.Next != "fail" {
		t.Errorf("b1 was not given up: %+v", e)
	}
	if !s.has(history.StepFailed, "p") || s.last().Type != history.RunFailed {
		t.Errorf("the run should fail: %s", s.types())
	}
}

func TestFailingForeachGivesUpARetryThatIsDue(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"each","type":"foreach","config":{"items":"=[0,1]","steps":[
	  {"id":"n","type":"connector","connector":"x@1","action":"y","retry":{"max":1}}]}}`, ""), map[string]any{})
	s.failStep("each[0].n", "fatal", "fail")
	s.failStep("each[1].n", "retryable", "retry")
	if got := s.pendingTasks(); len(got) != 0 || s.last().Type != history.RunFailed {
		t.Fatalf("pending %v, the run should fail: %s", got, s.types())
	}
}

// In a failing scope a write that may have been applied is not given up:
// its next attempt is kept, to settle only, and what it completes is
// reversed with the rest.
func TestFailingScopeSettlesAWriteThatMayHaveApplied(t *testing.T) {
	s := newSim(t, wdDoc(twoBranches, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	s.failWith("b1", unknown)
	if got := s.pendingTasks(); len(got) == 0 || !s.settling("b1") {
		t.Fatalf("b1 should have an attempt that settles; pending %v: %s", got, s.types())
	}
	if s.has(history.StepFailed, "p") {
		t.Fatal("the parallel failed while b1's outcome was unknown")
	}
	s.complete("b1", map[string]any{"ref": "r"})
	if got := s.pendingTasks(); len(got) != 1 || got[0] != "compensate:b1" {
		t.Fatalf("b1 took effect and must be reversed; pending %v: %s", got, s.types())
	}
	s.complete("compensate:b1", nil)
	if s.last().Type != history.RunFailed {
		t.Errorf("the run should fail: %s", s.types())
	}
}

// A reconcile that finds nothing settles the step: it failed, and nothing
// is owed, although an earlier attempt's outcome was once unknown.
func TestNotAppliedSettlesAnUnknownOutcome(t *testing.T) {
	s := newSim(t, wdDoc(twoBranches, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	s.failWith("b1", history.Error{Kind: "unknown_outcome", Next: "reconcile", MaybeApplied: true})
	if !s.settling("b1") {
		t.Fatalf("b1 should be reconciled: %s", s.types())
	}
	s.failWith("b1", history.Error{Kind: history.KindNotApplied, Next: "fail"})
	if s.last().Type != history.RunFailed || s.has(history.CompensationStarted, "") {
		t.Errorf("the run should fail with nothing to reverse: %s", s.types())
	}
}

// A failure for good after an attempt that may have been applied says
// nothing about that attempt: the step parks, as it does when its retries
// run out.
func TestFatalAfterUnknownOutcomeParks(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"x@1","action":"y"}`, ""), map[string]any{})
	s.failWith("pay", unknown)
	s.failStep("pay", "fatal", "fail")
	if e := s.lastFailure("pay"); e.Next != "park" || !e.MaybeApplied {
		t.Fatalf("pay should park: %+v: %s", e, s.types())
	}
	if s.has(history.RunFailed, "") {
		t.Fatal("the run failed with a write unaccounted for")
	}
	// The operator checks and says it did not happen: now it has failed.
	s.external(history.StepFailed, "pay", 2, history.FailedPayload{Error: history.Error{Kind: history.KindResolved, Message: "checked", Next: "fail"}}, history.OriginAPI)
	if s.last().Type != history.RunFailed {
		t.Errorf("the run should fail once resolved: %s", s.types())
	}
}

const noteThenPay = `{"id":"note","type":"connector","connector":"x@1","action":"y","retry":{"max":0}},
	  {"id":"pay","type":"connector","connector":"x@1","action":"y"}`

// A run that is failing does not end while a write is owed a reconcile.
func TestFailingRunSettlesBeforeItEnds(t *testing.T) {
	s := newSim(t, wdDoc(noteThenPay, ""), map[string]any{})
	s.failStep("note", "fatal", "fail")
	s.failWith("pay", history.Error{Kind: "unknown_outcome", Next: "reconcile", MaybeApplied: true})
	if s.has(history.RunFailed, "") || !s.settling("pay") {
		t.Fatalf("pay should be reconciled before the run ends: %s", s.types())
	}
	s.complete("pay", nil)
	if s.last().Type != history.RunFailed {
		t.Errorf("the run should fail: %s", s.types())
	}
}

// Nor while a step is parked for an operator: once the run has ended
// nobody can resolve it.
func TestFailingRunWaitsForAParkedStep(t *testing.T) {
	s := newSim(t, wdDoc(noteThenPay, ""), map[string]any{})
	s.failWith("pay", history.Error{Kind: "unknown_outcome", Next: "park", MaybeApplied: true})
	s.failStep("note", "fatal", "fail")
	if s.has(history.RunFailed, "") {
		t.Fatalf("the run ended with pay parked: %s", s.types())
	}
	s.external(history.StepCompleted, "pay", 1, history.CompletedPayload{Output: map[string]any{}}, history.OriginAPI)
	if s.last().Type != history.RunFailed {
		t.Errorf("the run should fail once pay is resolved: %s", s.types())
	}
}

const payWithUndo = `{"id":"pay","type":"connector","connector":"x@1","action":"y","compensate":{"action":"undo"}}`

// A run cancelled while a write waits for its retry, after an attempt that
// may have been applied, settles the write first: the attempt that was
// waiting is withdrawn, and one that only settles takes its place.
func TestCancelDuringBackoffSettlesAnUnknownOutcome(t *testing.T) {
	s := newSim(t, wdDoc(payWithUndo, ""), map[string]any{})
	s.failWith("pay", unknown) // attempt 2 is scheduled, after a backoff
	s.external(history.CancelRequested, "", 0, history.CancelPayload{By: "u1"}, history.OriginAPI)
	if s.has(history.RunCancelled, "") {
		t.Fatalf("cancelled with pay's outcome unknown: %s", s.types())
	}
	if s.lastAttempt("pay") != 3 || !s.settling("pay") {
		t.Fatalf("attempt %d, settling %v: %s", s.lastAttempt("pay"), s.settling("pay"), s.types())
	}
	s.complete("pay", map[string]any{"ref": "r"})
	s.complete("compensate:pay", nil)
	if s.last().Type != history.RunCancelled {
		t.Errorf("the run should be cancelled with pay reversed: %s", s.types())
	}
}

// The same when the attempt under way at the cancel then fails with an
// unknown outcome.
func TestCancelSettlesAnAttemptThatFailsAfterIt(t *testing.T) {
	s := newSim(t, wdDoc(payWithUndo, ""), map[string]any{})
	s.append(history.EffectIntent, "pay", 1, history.IntentPayload{Key: "k"}, history.OriginWorker)
	s.external(history.CancelRequested, "", 0, history.CancelPayload{By: "u1"}, history.OriginAPI)
	s.failWith("pay", unknown)
	if s.has(history.RunCancelled, "") || s.lastAttempt("pay") != 2 || !s.settling("pay") {
		t.Fatalf("pay should be settled before the run is cancelled: %s", s.types())
	}
	s.failWith("pay", history.Error{Kind: history.KindNotApplied, Next: "fail"})
	if s.last().Type != history.RunCancelled {
		t.Errorf("the run should be cancelled: %s", s.types())
	}
}

// A cancelled step that never reached a provider is not waited for.
func TestCancelDuringBackoffOfAStepNeverSent(t *testing.T) {
	s := newSim(t, wdDoc(payWithUndo, ""), map[string]any{})
	s.failStep("pay", "retryable", "retry")
	s.external(history.CancelRequested, "", 0, history.CancelPayload{By: "u1"}, history.OriginAPI)
	if s.last().Type != history.RunCancelled || s.lastAttempt("pay") != 2 {
		t.Errorf("the run should be cancelled at once: %s", s.types())
	}
}

// A branch step inside a failing parallel, with a step left to start and
// none in flight, will never finish; the parallel does not wait for it.
func TestFailingParallelDoesNotWaitForAStalledBranchStep(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]},
	  {"name":"b","steps":[{"id":"br","type":"branch","config":{"paths":[{"name":"yes","when":"=true","steps":[
	    {"id":"b1","type":"connector","connector":"x@1","action":"y"},
	    {"id":"b2","type":"connector","connector":"x@1","action":"y","needs":["b1"]}]}]}}]}]}}`, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	if s.has(history.StepFailed, "p") {
		t.Fatal("failed while b1 was in flight")
	}
	s.complete("b1", nil)
	if s.has(history.StepScheduled, "b2") || s.has(history.StepCompleted, "br") {
		t.Errorf("a failing parallel started or completed something: %s", s.types())
	}
	if s.last().Type != history.RunFailed {
		t.Errorf("the run should fail: %s", s.types())
	}
}

// Once the worker has moved to a new key, the old one is known not to
// have taken effect (a reconcile found nothing): a failure for good after
// that is final, as it always was.
func TestFatalUnderANewKeyFails(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"x@1","action":"y"}`, ""), map[string]any{})
	s.append(history.EffectIntent, "pay", 1, history.IntentPayload{Key: "k0"}, history.OriginWorker)
	s.failWith("pay", history.Error{Kind: "unknown_outcome", Next: "reconcile", MaybeApplied: true})
	s.append(history.EffectIntent, "pay", 2, history.IntentPayload{Key: "k1", AttemptGroup: 1}, history.OriginWorker)
	s.failStep("pay", "fatal", "fail")
	if e := s.lastFailure("pay"); e.Next != "fail" || s.last().Type != history.RunFailed {
		t.Errorf("pay should fail and the run with it: %+v: %s", e, s.types())
	}
}

// Nor for an on_error flow that will never start.
func TestFailingParallelDoesNotWaitForAnOnErrorFlow(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"connector","connector":"x@1","action":"y","retry":{"max":0}}]},
	  {"name":"b","steps":[{"id":"b1","type":"connector","connector":"x@1","action":"y",
	    "on_error":{"steps":[{"id":"tell","type":"connector","connector":"x@1","action":"y"}]}}]}]}}`, ""), map[string]any{})
	s.failStep("a1", "fatal", "fail")
	s.failStep("b1", "fatal", "fail")
	if s.has(history.StepScheduled, "tell") || s.last().Type != history.RunFailed {
		t.Errorf("the run should fail without starting the flow: %s", s.types())
	}
}
