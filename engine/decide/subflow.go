package decide

import (
	"fmt"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Subflow steps (wd-v1 subflow steps) start another workflow of the
// business as a child run. Decide schedules the start with the child's
// run inputs; the runtime starts the run in the same transaction and,
// when the step waits, registers a wait for history.SubflowEvent, which
// the child delivers when it ends: {run_id, status, outputs, error}. A
// step that does not wait gets the same signal at once, with status
// "started". The step's output is that payload; a child that failed or
// was cancelled fails the step with kind "subflow".

func (d *decider) subflowBegin(sc scope, s *wd.Step, inst string, act map[string]any) status {
	in, err := engine.Resolve(orEmpty(s.Input), act, true)
	if err != nil {
		d.fail(inst, "expression", err)
		return d.onError(sc, s, inst, true)
	}
	c := s.Subflow
	if c == nil {
		d.fail(inst, "subflow", fmt.Errorf("step %s names no workflow", s.ID))
		return d.onError(sc, s, inst, true)
	}
	d.emit(history.StepScheduled, inst, 1, history.ScheduledPayload{Kind: history.KindSubflow, Input: in,
		Event: history.SubflowEvent, Correlation: history.SubflowCorrelation(d.runID(), inst),
		Subflow: &history.SubflowScheduled{Workflow: c.Workflow, Version: c.Version, Wait: c.Waits()}})
	return running
}

func (d *decider) subflowProgress(s *wd.Step, inst string, f *facts) status {
	if f.signal == nil {
		return running
	}
	out, _ := f.signal.Payload.(map[string]any)
	st, _ := out["status"].(string)
	switch st {
	case "completed", "started":
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: f.signal.Payload})
		return completed
	}
	msg := fmt.Sprintf("the workflow it ran %s", st)
	if e, ok := out["error"].(map[string]any); ok {
		if m, _ := e["message"].(string); m != "" {
			msg += ": " + m
		}
	}
	d.fail(inst, "subflow", fmt.Errorf("%s", msg))
	return failed
}

// outputs evaluates the workflow's outputs (wd-v1 callable workflows)
// over its top-level steps when the run completes, for a subflow step
// that started it.
func (d *decider) runOutputs() (map[string]any, error) {
	if len(d.def.Outputs) == 0 {
		return nil, nil
	}
	act := d.activation(scope{steps: d.def.Steps}, nil)
	out := map[string]any{}
	for _, o := range d.def.Outputs {
		v, err := engine.Eval(o.Value, act)
		if err != nil {
			return nil, fmt.Errorf("output %s: %w", o.Name, err)
		}
		out[o.Name] = v
	}
	return out, nil
}
