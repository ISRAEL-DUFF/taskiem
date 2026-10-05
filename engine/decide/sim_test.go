package decide

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// sim is an in-memory run: it appends events, calls Decide after each
// external event (as the store does inline), and lets tests play the worker,
// timers, signals, and approvers.
type sim struct {
	t   *testing.T
	def *wd.Definition
	h   []history.Event
	now time.Time
}

func newSim(t *testing.T, wdJSON string, trigger any) *sim {
	t.Helper()
	def, err := wd.Load([]byte(wdJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := &sim{t: t, def: def, now: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}
	s.append(history.RunStarted, "", 0, history.RunStartedPayload{
		Run:     history.RunInfo{ID: "run-1", TenantID: "t-1", WorkflowID: "wf", Version: 1, Environment: "prod"},
		Trigger: trigger,
		Env:     map[string]any{"api": "https://api.example", "payrolla_api_url": "https://payrolla.example"},
	}, history.OriginIngest)
	s.decide()
	return s
}

func (s *sim) append(typ, inst string, attempt int, payload any, origin string) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			s.t.Fatal(err)
		}
		raw = b
	}
	s.h = append(s.h, history.Event{Seq: int64(len(s.h) + 1), Type: typ, StepID: inst, Attempt: attempt, Payload: raw, RecordedAt: s.now, Origin: origin})
}

func (s *sim) decide() []NewEvent {
	s.t.Helper()
	out, err := Decide(s.def, s.h)
	if err != nil {
		s.t.Fatal(err)
	}
	// Replaying the same history must give the same events.
	again, err := Decide(s.def, s.h)
	if err != nil || !reflect.DeepEqual(out, again) {
		s.t.Fatalf("decide is not deterministic:\n%v\n%v", out, again)
	}
	for _, e := range out {
		s.append(e.Type, e.StepID, e.Attempt, e.Payload, history.OriginDecide)
	}
	return out
}

func (s *sim) advance(d time.Duration) { s.now = s.now.Add(d) }

// external appends an event from outside decide, then decides.
func (s *sim) external(typ, inst string, attempt int, payload any, origin string) []NewEvent {
	s.advance(time.Second)
	s.append(typ, inst, attempt, payload, origin)
	return s.decide()
}

func (s *sim) complete(inst string, output any) []NewEvent {
	return s.external(history.StepCompleted, inst, s.lastAttempt(inst), history.CompletedPayload{Output: output}, history.OriginWorker)
}

func (s *sim) failStep(inst, kind, next string) []NewEvent {
	return s.external(history.StepFailed, inst, s.lastAttempt(inst), history.FailedPayload{Error: history.Error{Kind: kind, Message: "boom", Next: next}}, history.OriginWorker)
}

func (s *sim) fire(inst, kind string) []NewEvent {
	return s.external(history.TimerFired, inst, 0, history.TimerPayload{Kind: kind}, history.OriginScheduler)
}

func (s *sim) lastAttempt(inst string) int {
	n := 0
	for _, e := range s.h {
		if e.Type == history.StepScheduled && e.StepID == inst && e.Attempt > n {
			n = e.Attempt
		}
	}
	return n
}

// pendingTasks lists task instances scheduled without an outcome.
func (s *sim) pendingTasks() []string {
	sched := map[string]int{}
	var order []string
	for _, e := range s.h {
		switch e.Type {
		case history.StepScheduled:
			var p history.ScheduledPayload
			_ = json.Unmarshal(e.Payload, &p)
			if p.Kind == history.KindTask {
				if _, ok := sched[e.StepID]; !ok {
					order = append(order, e.StepID)
				}
				sched[e.StepID] = e.Attempt
			}
		case history.StepCompleted, history.StepFailed:
			if a, ok := sched[e.StepID]; ok && (e.Attempt == a || e.Attempt == 0) {
				delete(sched, e.StepID)
			}
		}
	}
	var out []string
	for _, id := range order {
		if _, ok := sched[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func (s *sim) last() history.Event { return s.h[len(s.h)-1] }

func (s *sim) payload(typ, inst string) map[string]any {
	for i := len(s.h) - 1; i >= 0; i-- {
		if e := s.h[i]; e.Type == typ && e.StepID == inst {
			v, _ := expr.DecodeJSON(e.Payload)
			m, _ := v.(map[string]any)
			return m
		}
	}
	s.t.Fatalf("no %s for %s", typ, inst)
	return nil
}

func (s *sim) has(typ, inst string) bool {
	for _, e := range s.h {
		if e.Type == typ && e.StepID == inst {
			return true
		}
	}
	return false
}

func (s *sim) types() string {
	var b strings.Builder
	for _, e := range s.h {
		fmt.Fprintf(&b, "%s(%s) ", e.Type, e.StepID)
	}
	return b.String()
}

func wdDoc(steps string, settings string) string {
	if settings == "" {
		settings = "{}"
	}
	return `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[` + steps + `],"settings":` + settings + `}`
}
