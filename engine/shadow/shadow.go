// Package shadow is the repair pipeline's sandbox (spec 12.2): it forks a
// failed run's recording into a wd-test case and runs a patched definition
// against it on the real orchestrator (engine/wdtest), with every task
// mocked. Steps the run completed answer with their recorded outputs (the
// outcome a resumed run would replay); steps it never reached answer with
// outputs sampled from their actions' output schemas; nothing is sent
// anywhere. The run passes when it completes, the connector inputs it
// would send match their actions' input schemas, and no write the run
// completed would now be sent with another input.
//
// The package holds no database or vault: the repair service opens the
// recording under the tenant's scope and hands it here. Recordings made
// from sealed history (Redacted) carry no personal values and are what a
// stored regression test is built from.
package shadow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// Step is one recorded step instance.
type Step struct {
	Kind      string         `json:"kind"` // task | approval | signal | timer | other
	Connector string         `json:"connector,omitempty"`
	Action    string         `json:"action,omitempty"`
	Input     any            `json:"input,omitempty"`
	Output    any            `json:"output,omitempty"`
	Completed bool           `json:"completed"`
	Write     bool           `json:"write,omitempty"`
	Decision  string         `json:"decision,omitempty"`
	DecidedBy string         `json:"decided_by,omitempty"`
	Signal    any            `json:"signal,omitempty"`
	Error     *history.Error `json:"error,omitempty"`
	Attempts  int            `json:"attempts,omitempty"`
}

// Recording is a run as recorded.
type Recording struct {
	Trigger  any                        `json:"trigger"`
	Env      map[string]any             `json:"env,omitempty"`
	Policies map[string]json.RawMessage `json:"policies,omitempty"`
	Steps    map[string]*Step           `json:"steps"`
	Order    []string                   `json:"order"`
	// Failed is the instance whose failure ended (or parked) the run.
	Failed string `json:"failed,omitempty"`
	// RunError is the run's failure.
	RunError *history.Error `json:"run_error,omitempty"`
	// Status is how the run ended: failed, needs_reconciliation, completed...
	Status string `json:"status,omitempty"`
}

// Record reads a run's history (opened or sealed).
func Record(hist []history.Event) (*Recording, error) {
	rec := &Recording{Steps: map[string]*Step{}}
	get := func(id string) *Step {
		s, ok := rec.Steps[id]
		if !ok {
			s = &Step{Kind: "other"}
			rec.Steps[id] = s
			rec.Order = append(rec.Order, id)
		}
		return s
	}
	parked := ""
	for _, e := range hist {
		switch e.Type {
		case history.RunStarted:
			var p history.RunStartedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			rec.Trigger, rec.Env = p.Trigger, p.Env
			for name, snap := range p.Policies {
				if rec.Policies == nil {
					rec.Policies = map[string]json.RawMessage{}
				}
				rec.Policies[name] = snap.Document
			}
			continue
		case history.RunFailed:
			var p history.RunFailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			rec.RunError, rec.Status = &p.Error, "failed"
			continue
		case history.RunCompleted:
			rec.Status = "completed"
			continue
		case history.RunCancelled:
			rec.Status = "cancelled"
			continue
		}
		if e.StepID == "" {
			continue
		}
		s := get(e.StepID)
		switch e.Type {
		case history.StepScheduled:
			var p history.ScheduledPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			switch p.Kind {
			case history.KindTask:
				s.Kind = "task"
			case history.KindTimer:
				s.Kind = "timer"
			case history.KindSignal:
				s.Kind = "signal"
			}
			s.Connector, s.Action, s.Input = p.Connector, p.Action, p.Input
			s.Attempts = max(s.Attempts, e.Attempt)
		case history.EffectIntent:
			var p history.IntentPayload
			_ = json.Unmarshal(e.Payload, &p)
			if p.Class != "" && p.Class != "read" {
				s.Write = true
			}
		case history.StepCompleted:
			if !s.Completed {
				var p struct {
					Output any `json:"output"`
				}
				_ = json.Unmarshal(e.Payload, &p)
				s.Completed, s.Output, s.Error = true, p.Output, nil
				if m, ok := p.Output.(map[string]any); ok && s.Kind == "approval" {
					s.Decision, _ = m["decision"].(string)
					s.DecidedBy, _ = m["decided_by"].(string)
				}
			}
			if parked == e.StepID {
				parked = ""
			}
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			s.Error = &p.Error
			switch {
			case p.Error.Next == "park":
				parked = e.StepID
			case p.Error.Next == "fail" && p.Error.Kind != "child_failed":
				rec.Failed = e.StepID
			}
		case history.ApprovalRequested:
			s.Kind = "approval"
		case history.ApprovalDecided:
			var p history.ApprovalDecidedPayload
			_ = json.Unmarshal(e.Payload, &p)
			s.Decision, s.DecidedBy = p.Decision, p.DecidedBy
		case history.SignalReceived:
			var p history.SignalPayload
			_ = json.Unmarshal(e.Payload, &p)
			s.Signal = p.Payload
		}
	}
	if parked != "" {
		rec.Failed = parked
		if rec.Status == "" {
			rec.Status = "needs_reconciliation"
		}
	}
	if rec.Failed == "" && rec.RunError != nil {
		// A run that failed outside a step (its timeout): the last step that
		// did not finish.
		for i := len(rec.Order) - 1; i >= 0; i-- {
			if s := rec.Steps[rec.Order[i]]; !s.Completed {
				rec.Failed = rec.Order[i]
				break
			}
		}
	}
	return rec, nil
}

// Case builds the shadow case: the recorded trigger, recorded outcomes for
// what completed, sampled outputs for the rest, recorded approvals and
// signals (approved and empty where none was recorded), and the
// expectation that the run completes.
func (rec *Recording) Case(name string, def *wd.Definition, reg connector.Lookup) wdtest.Case {
	reg = builder.ManifestOnly(reg)
	c := builder.HappyPath(def, reg)
	c.Name = name
	c.Trigger = rec.Trigger
	c.Env = rec.Env
	for _, id := range rec.Order {
		s := rec.Steps[id]
		switch s.Kind {
		case "task":
			if s.Completed {
				c.Mocks[id] = wdtest.Mocks{{Output: literal(s.Output)}}
			}
		case "approval":
			if s.Decision != "" {
				c.Approvals[id] = wdtest.ApprovalMock{Decision: s.Decision, By: s.DecidedBy}
			}
		case "signal":
			if s.Signal != nil {
				c.Signals[id] = wdtest.SignalMock{Payload: literal(s.Signal)}
			}
		}
	}
	c.Expect = wdtest.Expect{Status: "completed"}
	return c
}

// literal makes recorded values safe as mock outputs, which are resolved
// as expressions: a string starting with "=" becomes an expression that
// yields that string.
func literal(v any) any {
	switch t := v.(type) {
	case string:
		if strings.HasPrefix(t, "=") {
			return "=" + strconv.Quote(t)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = literal(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = literal(x)
		}
		return out
	}
	return v
}

// Report is a shadow run's outcome.
type Report struct {
	Passed bool              `json:"passed"`
	Status string            `json:"status"`
	Steps  map[string]string `json:"steps"`
	// Failures: the case's own failures (status, missing mocks).
	Failures []string `json:"failures,omitempty"`
	// InputProblems: connector inputs the run would send that do not match
	// their actions' input schemas.
	InputProblems []string `json:"input_problems,omitempty"`
	// Mismatches: writes the recorded run completed that the patched
	// definition would send with another input. The patch may still be
	// right, but the run cannot be resumed with it.
	Mismatches []string `json:"write_mismatches,omitempty"`
	// Replayed: completed steps answered from the recording.
	Replayed []string `json:"replayed"`
}

// Resumable reports whether the failed run can be resumed on the patch.
func (r Report) Resumable() bool { return r.Passed && len(r.Mismatches) == 0 }

// Run runs def against the recording's case.
func Run(def *wd.Definition, reg connector.Lookup, rec *Recording, c wdtest.Case) Report {
	reg = builder.ManifestOnly(reg)
	res := runCase(def, reg, rec.Policies, c)
	rep := Report{Status: res.Status, Steps: res.Steps, Failures: res.Failures, Replayed: []string{}}
	if rep.Steps == nil {
		rep.Steps = map[string]string{}
	}
	for _, id := range rec.Order {
		s := rec.Steps[id]
		if s.Kind != "task" || !s.Completed {
			continue
		}
		in, sent := res.Inputs[id]
		if !sent {
			continue
		}
		rep.Replayed = append(rep.Replayed, id)
		if s.Write && history.InputDigest(in) != history.InputDigest(s.Input) {
			rep.Mismatches = append(rep.Mismatches, id)
		}
	}
	rep.InputProblems = inputProblems(def, reg, res.Inputs)
	rep.Passed = res.Passed() && len(rep.InputProblems) == 0
	return rep
}

// RunCase runs one case (a regression or stored test) and reports it.
func RunCase(def *wd.Definition, reg connector.Lookup, policies map[string]json.RawMessage, c wdtest.Case) wdtest.Result {
	return runCase(def, builder.ManifestOnly(reg), policies, c)
}

func runCase(def *wd.Definition, reg connector.Lookup, policies map[string]json.RawMessage, c wdtest.Case) (res wdtest.Result) {
	defer func() {
		if v := recover(); v != nil {
			res = wdtest.Result{Case: c.Name, Status: "error", Failures: []string{fmt.Sprintf("the case could not run: %v", v)}}
		}
	}()
	return wdtest.RunCaseWithPolicies(def, reg, nil, policies, c)
}

// inputProblems checks what connector steps would send against their
// actions' input schemas. Schemas with references, and the field a
// connector fills with the idempotency key, are left out.
func inputProblems(def *wd.Definition, reg connector.Lookup, inputs map[string]any) []string {
	var out []string
	ids := make([]string, 0, len(inputs))
	for id := range inputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		_, sid := history.SplitInstance(id)
		st := def.Step(sid)
		if st == nil || st.Type != "connector" {
			continue
		}
		con, ok := reg.Get(st.Connector)
		if !ok {
			continue
		}
		a, ok := con.Manifest.Actions[st.Action]
		if !ok || len(a.Input) == 0 || strings.Contains(string(a.Input), `"$ref"`) {
			continue
		}
		in, _ := inputs[id].(map[string]any)
		in = withoutSecrets(in)
		if a.Idempotency != nil && a.Idempotency.Field != "" && in != nil {
			if _, set := in[a.Idempotency.Field]; !set {
				in[a.Idempotency.Field] = "key"
			}
		}
		raw, err := json.Marshal(in)
		if err != nil {
			continue
		}
		for _, p := range schemacheck.New("https://schemas.taskiem.dev/shadow/"+con.Ref()+"/"+st.Action+".json", a.Input).Validate(raw) {
			out = append(out, id+": "+p)
		}
	}
	return out
}

// withoutSecrets copies an input, dropping expressions left for the worker
// (secret references): their values are never known here.
func withoutSecrets(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok && strings.HasPrefix(s, "=") {
			continue
		}
		out[k] = v
	}
	return out
}

// Redacted returns a copy of a recording made from sealed history with
// every sealed value replaced by a sample of the same shape (from the
// trigger's inputs schema or the action's output schema) or by its
// category, and free text masked: data a model may see, and a regression
// test may keep.
func (rec *Recording) Redacted(def *wd.Definition, reg connector.Lookup) *Recording {
	hp := builder.HappyPath(def, builder.ManifestOnly(reg))
	out := &Recording{Env: map[string]any{}, Policies: rec.Policies, Steps: map[string]*Step{}, Order: append([]string(nil), rec.Order...),
		Failed: rec.Failed, RunError: redactError(rec.RunError), Status: rec.Status}
	out.Trigger = placeholder(rec.Trigger, hp.Trigger)
	for k := range rec.Env {
		out.Env[k] = "[variable]" // variable values never leave
	}
	for id, s := range rec.Steps {
		c := *s
		var sample any
		if ms, ok := hp.Mocks[stepKey(id)]; ok && len(ms) > 0 {
			sample = ms[0].Output
		}
		c.Output = placeholder(s.Output, sample)
		c.Input = placeholder(s.Input, nil)
		c.Signal = placeholder(s.Signal, nil)
		c.DecidedBy = ""
		if c.Decision != "" {
			c.DecidedBy = "approver"
		}
		c.Error = redactError(s.Error)
		out.Steps[id] = &c
	}
	return out
}

func redactError(e *history.Error) *history.Error {
	if e == nil {
		return nil
	}
	c := *e
	c.Message = pii.Redact(c.Message)
	return &c
}

func stepKey(id string) string {
	_, s := history.SplitInstance(id)
	return s
}

// placeholder replaces sealed values in v with the sample at the same
// place, or "[category]", and masks free text.
func placeholder(v, sample any) any {
	switch t := v.(type) {
	case map[string]any:
		if pii.IsEnvelope(t) {
			if sample != nil {
				if _, isMap := sample.(map[string]any); !isMap {
					return sample
				}
			}
			cat, _ := t["$pii"].(string)
			return "[" + cat + "]"
		}
		sm, _ := sample.(map[string]any)
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = placeholder(x, sm[k])
		}
		return out
	case []any:
		sl, _ := sample.([]any)
		out := make([]any, len(t))
		for i, x := range t {
			var s any
			if len(sl) > 0 {
				s = sl[0]
			}
			out[i] = placeholder(x, s)
		}
		return out
	case string:
		return pii.Redact(t)
	}
	return v
}
