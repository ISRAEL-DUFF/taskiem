package runtimetest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Workers get seconds per provider call unless a test asks for less: a
// loaded machine must not turn a slow fake into a timed-out step.
func TestWorkerCallTimeoutDefault(t *testing.T) {
	e := &Env{}
	if got := e.Worker("w").CallTimeout; got != 5*time.Second {
		t.Errorf("default call timeout %v, want 5s", got)
	}
	e.CallTimeout = 50 * time.Millisecond
	if got := e.Worker("w").CallTimeout; got != 50*time.Millisecond {
		t.Errorf("set call timeout %v, want 50ms", got)
	}
}

// errs records what replayHistories reports instead of failing the test.
type errs struct {
	testing.TB
	got []string
}

func (r *errs) Errorf(format string, args ...any) {
	r.got = append(r.got, fmt.Sprintf(format, args...))
}
func (r *errs) Failed() bool  { return false }
func (r *errs) Skipped() bool { return false }

// When a test ends, the harness replays every settled run's history and
// reports one that decide would not have written.
func TestCleanupReplaysEveryRun(t *testing.T) {
	e := New(t)
	e.NoReplay = true // it replays by hand, and leaves a bad history
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[
		{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":"=trigger.amount","logical_id":"=run.id + ':pay'"}}],"settings":{}}`)
	a, b := e.Start(t, wf, map[string]any{"amount": 1}), e.Start(t, wf, map[string]any{"amount": 2})
	e.Drain(t)
	if e.Status(t, a) != "completed" || e.Status(t, b) != "completed" {
		t.Fatalf("runs: %s %s", e.Status(t, a), e.Status(t, b))
	}
	r := &errs{TB: t}
	e.replayHistories(r)
	if len(r.got) != 0 {
		t.Fatalf("clean histories: %v", r.got)
	}
	// Rewrite what decide scheduled in one of them.
	tag, err := e.DB.Admin.Exec(context.Background(), `UPDATE run_events SET attempt = COALESCE(attempt, 0) + 7
		WHERE run_id = $1 AND origin = 'decide' AND seq = (SELECT min(seq) FROM run_events WHERE run_id = $1 AND origin = 'decide' AND seq > 1)`, b.ID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("tamper: %v %v", tag, err)
	}
	e.replayHistories(r)
	if len(r.got) != 1 || !strings.Contains(r.got[0], b.ID.String()) || !strings.Contains(r.got[0], "does not replay") {
		t.Fatalf("replay reported %v, want run %s", r.got, b.ID)
	}
}
