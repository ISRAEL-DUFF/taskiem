package container

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Fake is a Runner for tests. Handle decides each run; by default the
// output is {"input": <the input>}. It records every spec it was given.
type Fake struct {
	Handle func(ctx context.Context, s Spec) (Result, error)

	mu    sync.Mutex
	specs []Spec
}

// Run records s and hands it to Handle.
func (f *Fake) Run(ctx context.Context, s Spec) (Result, error) {
	f.mu.Lock()
	f.specs = append(f.specs, s)
	f.mu.Unlock()
	if f.Handle != nil {
		return f.Handle(ctx, s)
	}
	out, _ := json.Marshal(map[string]json.RawMessage{"input": s.Input})
	return Result{Output: out, Elapsed: time.Second}, nil
}

// Specs returns the specs run so far.
func (f *Fake) Specs() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Spec(nil), f.specs...)
}

// Exited is the error a runner reports for a program that exited with a
// status (for fakes that imitate one).
func Exited(code int, logs string) error {
	_, err := Settle(Outcome{Exit: code, Logs: logs}, 0)
	return err
}

// TimedOut is the error for a program that ran out of time.
func TimedOut(after time.Duration) error {
	_, err := Settle(Outcome{Error: "timeout"}, after)
	return err
}

// NotStarted is the error for a program that never started.
func NotStarted(why string) error { return notStarted("%s", why) }
