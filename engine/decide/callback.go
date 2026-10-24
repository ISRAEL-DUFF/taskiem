package decide

import (
	"fmt"
	"strings"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Callback steps (wd-v1 callback steps) wait as signal steps do, for
// history.CallbackEvent correlated by the run and step instance. The
// step's address, run.callbacks.<id>, is in every expression's scope from
// the start, so an earlier step can send it to the system that calls
// back; ingest verifies its token and delivers the call as the signal.
//
// Respond steps answer the webhook call that started the run: they
// complete at once with the status, headers and body the caller gets,
// which ingest reads from history (docs/contracts/wd-v1.md).

func (d *decider) callbackBegin(s *wd.Step, inst string) status {
	timeout := wd.CallbackTimeout
	if s.Callback != nil && s.Callback.Timeout != "" {
		timeout, _ = wd.ParseDuration(s.Callback.Timeout)
	}
	d.emit(history.StepScheduled, inst, 1, history.ScheduledPayload{Kind: history.KindSignal, Event: history.CallbackEvent,
		Correlation: history.CallbackCorrelation(d.runID(), inst), TimeoutAt: history.FormatTime(d.now.Add(timeout))})
	return running
}

func (d *decider) callbackProgress(s *wd.Step, inst string, f *facts) status {
	if f.signal != nil {
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: f.signal.Payload})
		return completed
	}
	for _, t := range f.timerFired {
		if t.kind != "signal_timeout" {
			continue
		}
		if s.Callback != nil && s.Callback.OnTimeout == "continue" {
			d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"timed_out": true}})
			return completed
		}
		d.fail(inst, "timeout", fmt.Errorf("nothing called %s back in time", s.ID))
		return failed
	}
	return running
}

func (d *decider) respondBegin(sc scope, s *wd.Step, inst string, act map[string]any) status {
	r := s.Respond
	out := map[string]any{"status": int64(200), "headers": map[string]any{}, "body": nil}
	if r.Status != 0 {
		out["status"] = int64(r.Status)
	}
	if len(r.Headers) > 0 {
		h, err := engine.Resolve(r.Headers, act, false)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		out["headers"] = h
	}
	body, err := engine.Resolve(r.Body, act, false)
	if err != nil {
		d.fail(inst, "expression", err)
		return d.onError(sc, s, inst, true)
	}
	out["body"] = body
	d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: out})
	return completed
}

// runRoot is the run as expressions see it in a scope: what RunStarted
// recorded, without the callback key, plus callbacks: each callback
// step's address for the instance the scope is in.
func (d *decider) runRoot(sc scope) map[string]any {
	at, _ := d.runInfo["callbacks_at"].(string)
	key, _ := d.runInfo["callback_key"].(string)
	if at == "" {
		return d.runInfo
	}
	root := make(map[string]any, len(d.runInfo)+1)
	for k, v := range d.runInfo {
		if k != "callback_key" {
			root[k] = v
		}
	}
	callbacks := map[string]any{}
	d.def.Walk(func(s *wd.Step) {
		if s.Type == "callback" {
			callbacks[s.ID] = history.CallbackURL(at, key, d.instanceOf(s, sc))
		}
	})
	root["callbacks"] = callbacks
	return root
}

// instanceOf is the instance id a step has in a scope: its id under the
// scope's iteration prefixes, as many as the foreach steps enclosing it,
// so a step inside the body being run names that iteration's instance.
func (d *decider) instanceOf(s *wd.Step, sc scope) string {
	depth := 0
	for p := d.def.Parent(s.ID); p != nil; p = d.def.Parent(p.ID) {
		if p.Type == "foreach" {
			depth++
		}
	}
	if depth == 0 || sc.prefix == "" {
		return s.ID
	}
	segs := strings.Split(strings.TrimSuffix(sc.prefix, "."), ".")
	if len(segs) > depth {
		segs = segs[:depth]
	}
	return strings.Join(segs, ".") + "." + s.ID
}
