// Package spike is the throwaway Phase 0 prototype of decide() and the
// Postgres task queue (build plan, Phase 0 "Engine spike"). It runs against
// the real schema v0 and dispatch functions so the load test measures the
// design we will build on, but none of this code is carried into Phase 1.
package spike

// Event is the slice of run history decide() reads.
type Event struct {
	Seq    int64
	Type   string
	StepID string
}

// Command is what decide() asks the orchestrator to do.
type Command struct {
	Kind   string // "schedule" | "complete"
	StepID string
}

// Decide is a pure function of the definition (here: a linear chain of
// steps) and the history. No clock, randomness or I/O, so replaying the same
// history always yields the same commands.
func Decide(steps []string, history []Event) []Command {
	scheduled := map[string]bool{}
	completed := map[string]bool{}
	ended := false
	for _, e := range history {
		switch e.Type {
		case "StepScheduled":
			scheduled[e.StepID] = true
		case "StepCompleted":
			completed[e.StepID] = true
		case "RunCompleted", "RunFailed", "RunCancelled":
			ended = true
		}
	}
	if ended {
		return nil
	}
	for i, s := range steps {
		if completed[s] {
			continue
		}
		if scheduled[s] {
			return nil // waiting on the worker
		}
		if i == 0 || completed[steps[i-1]] {
			return []Command{{Kind: "schedule", StepID: s}}
		}
		return nil
	}
	return []Command{{Kind: "complete"}}
}
