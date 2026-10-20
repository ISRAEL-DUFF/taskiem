package decide

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Form steps (docs/spec/forms.md 6.7) wait as signal steps do, for
// history.FormEvent correlated by the request id; the store records the
// request and its link is sent by the api role. The answers are the
// step's output.

// FormTimeout is a form step's default wait: a one-time link lasts 7 days.
const FormTimeout = 7 * 24 * time.Hour

// formNamespace derives request ids: the same run and step instance give
// the same id on every replay.
var formNamespace = uuid.MustParse("7b0d1f0e-2c55-4f3a-9a8e-5f6b2c1d4e70")

// FormRequestID is a form step instance's request id.
func FormRequestID(run, inst string) string {
	return uuid.NewSHA1(formNamespace, []byte(run+"/"+inst)).String()
}

func (d *decider) formBegin(sc scope, s *wd.Step, inst string, act map[string]any) status {
	f := s.Form
	to := map[string]any{}
	for k, v := range map[string]string{"phone": f.To.Phone, "email": f.To.Email} {
		if v == "" {
			continue
		}
		r, err := engine.Resolve(v, act, false)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		if str := stringify(r); str != "" {
			to[k] = str
		}
	}
	if f.To.Role != "" {
		to["role"] = f.To.Role
	}
	if len(to) == 0 {
		d.fail(inst, "form", fmt.Errorf("the form step's recipient is empty"))
		return d.onError(sc, s, inst, true)
	}
	prefill, err := engine.Resolve(f.Prefill, act, false)
	if err != nil {
		d.fail(inst, "expression", err)
		return d.onError(sc, s, inst, true)
	}
	pm, _ := prefill.(map[string]any)
	timeout := FormTimeout
	if f.Timeout != "" {
		timeout, _ = wd.ParseDuration(f.Timeout)
	}
	via := f.Via
	if len(via) == 0 {
		via = []string{"sms", "email"}
	}
	req := FormRequestID(d.runID(), inst)
	d.emit(history.StepScheduled, inst, 1, history.ScheduledPayload{Kind: history.KindSignal, Event: history.FormEvent, Correlation: req,
		TimeoutAt: history.FormatTime(d.now.Add(timeout)),
		Form: &history.FormScheduled{Request: req, Title: f.Title, Description: f.Description, To: to, Via: via, Verify: f.Verify,
			Schema: f.Schema, Prefill: pm, Remind: f.Remind}})
	return running
}

func (d *decider) formProgress(s *wd.Step, inst string, f *facts) status {
	if f.signal != nil {
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: f.signal.Payload})
		return completed
	}
	for _, t := range f.timerFired {
		if t.kind != "signal_timeout" {
			continue
		}
		if s.Form.OnTimeout == "continue" {
			d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"timed_out": true}})
			return completed
		}
		d.fail(inst, "timeout", fmt.Errorf("no answer to %q in time", s.Form.Title))
		return failed
	}
	return running
}
