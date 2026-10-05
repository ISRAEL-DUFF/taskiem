// Package decide is the orchestrator's pure function (spec 4.2):
//
//	Decide(definition, history) -> new events
//
// It reads no clock, randomness, or external state. Time enters only through
// the recorded_at of events, so replaying a history always yields the same
// events, byte for byte (the determinism suite checks this).
package decide

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

var engine = expr.MustNew()

// NewEvent is an event decide wants appended, in order.
type NewEvent struct {
	Type    string `json:"type"`
	StepID  string `json:"step_id,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

// MaxPasses bounds the fixpoint loop; each pass can only move steps forward.
const MaxPasses = 10_000

// Decide returns the events that follow from the history. A run that has
// ended yields nothing.
func Decide(def *wd.Definition, h []history.Event) ([]NewEvent, error) {
	if len(h) == 0 || h[0].Type != history.RunStarted {
		return nil, fmt.Errorf("decide: history must start with RunStarted")
	}
	d := &decider{def: def, facts: map[string]*facts{}}
	if err := d.load(h); err != nil {
		return nil, err
	}
	if d.terminal {
		return nil, nil
	}
	for pass := 0; pass < MaxPasses; pass++ {
		before := len(d.out)
		d.run()
		if len(d.out) == before || d.terminal {
			return d.out, nil
		}
	}
	return nil, fmt.Errorf("decide: no fixpoint after %d passes", MaxPasses)
}

// facts are what the history says about one step instance.
type facts struct {
	scheduled     map[int]history.ScheduledPayload // by attempt
	firstSchedAt  time.Time
	lastAttempt   int
	outcome       map[int]string // attempt -> StepCompleted | StepFailed
	failures      map[int]history.Error
	retries       map[int]bool // attempt -> RetryScheduled for it
	completed     bool
	output        any
	skipped       bool
	control       *history.ControlStartedPayload
	timerFired    []timerFact
	signal        *history.SignalPayload
	approvalReqs  []history.ApprovalRequestedPayload
	approvalReqAt []int64
	approval      *history.ApprovalDecidedPayload
	finalFailure  *history.Error // failure decided by decide itself, or a worker failure with next=fail
	lastSeq       int64
}

type timerFact struct {
	kind string
	seq  int64
}

type decider struct {
	def                   *wd.Definition
	facts                 map[string]*facts
	order                 []string // instances in first-seen order, for deterministic compensation
	complSeq              map[string]int64
	now                   time.Time
	seq                   int64 // seq of the last loaded or emitted event, for ordering
	started               history.RunStartedPayload
	trigger               any
	env                   map[string]any
	runInfo               map[string]any
	terminal              bool
	runTimeout            bool
	compStarted, compDone bool
	out                   []NewEvent

	memo map[string]status
}

func (d *decider) f(inst string) *facts {
	f, ok := d.facts[inst]
	if !ok {
		f = &facts{scheduled: map[int]history.ScheduledPayload{}, outcome: map[int]string{}, failures: map[int]history.Error{}, retries: map[int]bool{}}
		d.facts[inst] = f
		d.order = append(d.order, inst)
	}
	return f
}

func (d *decider) load(h []history.Event) error {
	d.complSeq = map[string]int64{}
	for _, e := range h {
		if err := d.apply(e.Type, e.StepID, e.Attempt, e.Payload, e.Seq); err != nil {
			return fmt.Errorf("decide: event %d (%s): %w", e.Seq, e.Type, err)
		}
		d.now = e.RecordedAt.UTC()
	}
	return nil
}

// apply folds one event into the facts. It is used both for loaded history
// and for events decide emits, so a pass sees its own output.
func (d *decider) apply(typ, inst string, attempt int, raw json.RawMessage, seq int64) error {
	d.seq = seq
	switch typ {
	case history.RunStarted:
		if err := json.Unmarshal(raw, &d.started); err != nil {
			return err
		}
		v, err := expr.DecodeJSON(raw)
		if err != nil {
			return err
		}
		m := v.(map[string]any)
		d.trigger = m["trigger"]
		d.env, _ = m["env"].(map[string]any)
		if d.env == nil {
			d.env = map[string]any{}
		}
		d.runInfo, _ = m["run"].(map[string]any)
		return nil
	case history.RunCompleted, history.RunFailed, history.RunCancelled:
		d.terminal = true
		return nil
	case history.CompensationStarted:
		d.compStarted = true
		return nil
	case history.CompensationCompleted:
		d.compDone = true
		return nil
	}
	if typ == history.TimerFired && inst == "" {
		var p history.TimerPayload
		_ = json.Unmarshal(raw, &p)
		if p.Kind == "run_timeout" {
			d.runTimeout = true
		}
		return nil
	}
	if inst == "" {
		return nil
	}
	f := d.f(inst)
	f.lastSeq = seq
	switch typ {
	case history.StepScheduled:
		var p history.ScheduledPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if attempt == 0 {
			attempt = 1
		}
		if len(f.scheduled) == 0 {
			f.firstSchedAt = d.now
		}
		f.scheduled[attempt] = p
		if attempt > f.lastAttempt {
			f.lastAttempt = attempt
		}
	case history.StepStarted:
		var p history.ControlStartedPayload
		if len(raw) > 0 && json.Unmarshal(raw, &p) == nil && (p.Path != nil || p.Count != nil) {
			f.control = &p
		}
	case history.StepCompleted:
		if f.completed {
			return nil // a late duplicate after reconciliation; the first wins
		}
		v, err := expr.DecodeJSON(raw)
		if err != nil {
			return err
		}
		m, _ := v.(map[string]any)
		f.completed = true
		f.output = m["output"]
		f.outcome[attempt] = typ
		d.complSeq[inst] = seq
	case history.StepFailed:
		var p history.FailedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if attempt == 0 {
			attempt = f.lastAttempt
		}
		f.outcome[attempt] = typ
		f.failures[attempt] = p.Error
		if p.Error.Next == "fail" {
			e := p.Error
			f.finalFailure = &e
		}
	case history.StepSkipped:
		f.skipped = true
	case history.RetryScheduled:
		f.retries[attempt] = true
	case history.TimerFired:
		var p history.TimerPayload
		_ = json.Unmarshal(raw, &p)
		f.timerFired = append(f.timerFired, timerFact{kind: p.Kind, seq: seq})
	case history.SignalReceived:
		if f.signal == nil {
			var p history.SignalPayload
			v, err := expr.DecodeJSON(raw)
			if err != nil {
				return err
			}
			m, _ := v.(map[string]any)
			p.Event, _ = m["event"].(string)
			p.Payload = m["payload"]
			f.signal = &p
		}
	case history.ApprovalRequested:
		var p history.ApprovalRequestedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		f.approvalReqs = append(f.approvalReqs, p)
		f.approvalReqAt = append(f.approvalReqAt, seq)
	case history.ApprovalDecided:
		if f.approval == nil {
			var p history.ApprovalDecidedPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			f.approval = &p
		}
	}
	return nil
}

// emit records a new event and folds it into the facts immediately.
func (d *decider) emit(typ, inst string, attempt int, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("decide: marshal %s payload: %v", typ, err))
	}
	if payload == nil {
		raw = nil
	}
	d.out = append(d.out, NewEvent{Type: typ, StepID: inst, Attempt: attempt, Payload: payload})
	if err := d.apply(typ, inst, attempt, raw, d.seq+1); err != nil {
		panic(fmt.Sprintf("decide: apply own %s: %v", typ, err))
	}
}

type status int

const (
	pending status = iota
	running
	parked
	completed
	skipped
	handled // failed, then its on_error flow completed
	failed  // failed with nothing left to handle it
)

// scope is one list of steps evaluated under a prefix (foreach iterations add
// "<foreach>[i]." to the prefix).
type scope struct {
	prefix string
	steps  []*wd.Step
	iter   *iteration
}

type iteration struct {
	item   any
	index  int
	parent *iteration
}

func (d *decider) run() {
	d.memo = map[string]status{}
	top := scope{steps: d.def.Steps}
	done, failedTop, why := d.scope(top, !d.runTimeout)
	switch {
	case d.runTimeout || failedTop:
		if d.runTimeout && why == nil {
			why = &history.Error{Kind: "timeout", Message: "run exceeded settings.timeout", Next: "fail"}
		}
		if d.inFlight() {
			return
		}
		d.compensate(why)
	case done:
		d.emit(history.RunCompleted, "", 0, map[string]any{})
	}
}

// scope evaluates one scope. It reports whether every step is settled, and
// whether one failed with nothing to handle it. A failed scope starts no new
// steps and is done once its in-flight steps settle.
func (d *decider) scope(sc scope, canStart bool) (done, failedScope bool, why *history.Error) {
	statuses := func(start bool) []status {
		out := make([]status, len(sc.steps))
		for i, s := range sc.steps {
			out[i] = d.status(sc, s, start)
		}
		return out
	}
	st := statuses(false)
	failing := false
	for i, s := range st {
		if s == failed {
			failing = true
			if why == nil {
				why = d.failureOf(sc.prefix + sc.steps[i].ID)
			}
		}
	}
	if canStart && !failing {
		d.memo = map[string]status{}
		st = statuses(true)
		for i, s := range st {
			if s == failed {
				failing = true
				if why == nil {
					why = d.failureOf(sc.prefix + sc.steps[i].ID)
				}
			}
		}
	}
	done = true
	for _, s := range st {
		if s == running || s == parked || (s == pending && !failing) {
			done = false
		}
	}
	return done, failing, why
}

func (d *decider) failureOf(inst string) *history.Error {
	f := d.facts[inst]
	if f == nil {
		return nil
	}
	if f.finalFailure != nil {
		e := *f.finalFailure
		e.Message = inst + ": " + e.Message
		return &e
	}
	if e, ok := f.failures[f.lastAttempt]; ok {
		e.Message = inst + ": " + e.Message
		return &e
	}
	return nil
}

// status computes (and, when start is set, advances) one step instance.
func (d *decider) status(sc scope, s *wd.Step, start bool) status {
	inst := sc.prefix + s.ID
	key := inst + "|" + strconv.FormatBool(start)
	if st, ok := d.memo[key]; ok {
		return st
	}
	st := d.statusOf(sc, s, inst, start)
	d.memo[key] = st
	return st
}

func (d *decider) statusOf(sc scope, s *wd.Step, inst string, start bool) status {
	f := d.f(inst)
	switch {
	case f.completed:
		return completed
	case f.skipped:
		return skipped
	}
	if d.begun(s, f) {
		st := d.progress(sc, s, inst, f, start)
		if st == failed {
			return d.onError(sc, s, inst, start)
		}
		return st
	}
	// Not begun: check needs.
	for _, n := range s.Needs {
		ns := d.status(sc, d.def.Step(n), start)
		switch ns {
		case pending, running, parked:
			return pending
		case failed:
			return pending // the scope is failing; this step will never start
		case skipped, handled:
			if start {
				d.emit(history.StepSkipped, inst, 0, map[string]any{"reason": "need " + n + " " + map[status]string{skipped: "was skipped", handled: "failed"}[ns]})
				return skipped
			}
			return pending
		}
	}
	if !start {
		return pending
	}
	act := d.activation(sc, nil)
	if s.When != "" {
		ok, err := engine.EvalBool(s.When, act)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, start)
		}
		if !ok {
			d.emit(history.StepSkipped, inst, 0, map[string]any{"reason": "when was false"})
			return skipped
		}
	}
	return d.begin(sc, s, inst, act)
}

// begun reports whether the step has left the pending state.
func (d *decider) begun(s *wd.Step, f *facts) bool {
	return len(f.scheduled) > 0 || f.control != nil || len(f.approvalReqs) > 0 || f.finalFailure != nil
}

func (d *decider) fail(inst, kind string, err error) {
	d.emit(history.StepFailed, inst, 0, history.FailedPayload{Error: history.Error{Kind: kind, Message: err.Error(), Next: "fail"}})
}

func isTask(t string) bool {
	return t == "connector" || t == "http" || t == "code" || t == "ai"
}

// begin starts a ready step.
func (d *decider) begin(sc scope, s *wd.Step, inst string, act map[string]any) status {
	switch {
	case isTask(s.Type):
		p, err := d.taskPayload(s, inst, act)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		p.AvailableAt = history.FormatTime(d.now)
		d.emit(history.StepScheduled, inst, 1, p)
		return running
	case s.Type == "transform":
		out, err := engine.Resolve(s.Transform.Output, act, false)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: out})
		return completed
	case s.Type == "wait":
		at, err := d.waitUntil(s, act)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		d.emit(history.StepScheduled, inst, 1, history.ScheduledPayload{Kind: history.KindTimer, FireAt: history.FormatTime(at)})
		return running
	case s.Type == "signal":
		corr, err := engine.Eval(s.Signal.Correlation, act)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		p := history.ScheduledPayload{Kind: history.KindSignal, Event: s.Signal.Event, Correlation: stringify(corr)}
		if s.Signal.Timeout != "" {
			dur, _ := wd.ParseDuration(s.Signal.Timeout)
			p.TimeoutAt = history.FormatTime(d.now.Add(dur))
		}
		d.emit(history.StepScheduled, inst, 1, p)
		return running
	case s.Type == "approval":
		subj, err := engine.Resolve(s.Approval.Subject, act, false)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		p := history.ApprovalRequestedPayload{Policy: s.Approval.Policy, Role: s.Approval.Role, Count: s.Approval.Count}
		if m, ok := subj.(map[string]any); ok {
			p.Subject = m
		}
		if s.Approval.Timeout != "" {
			dur, _ := wd.ParseDuration(s.Approval.Timeout)
			p.TimeoutAt = history.FormatTime(d.now.Add(dur))
		}
		d.emit(history.ApprovalRequested, inst, 0, p)
		return running
	case s.Type == "branch":
		path := ""
		for _, bp := range s.Branch.Paths {
			ok, err := engine.EvalBool(bp.When, act)
			if err != nil {
				d.fail(inst, "expression", err)
				return d.onError(sc, s, inst, true)
			}
			if ok {
				path = bp.Name
				break
			}
		}
		if path == "" && s.Branch.Default != nil {
			path = "default"
		}
		d.emit(history.StepStarted, inst, 0, history.ControlStartedPayload{Path: &path})
		return d.progress(sc, s, inst, d.facts[inst], true)
	case s.Type == "foreach":
		items, err := d.items(s, act)
		if err != nil {
			d.fail(inst, "expression", err)
			return d.onError(sc, s, inst, true)
		}
		n := len(items)
		d.emit(history.StepStarted, inst, 0, history.ControlStartedPayload{Count: &n})
		return d.progress(sc, s, inst, d.facts[inst], true)
	}
	d.fail(inst, "unsupported", fmt.Errorf("step type %q is not supported by this engine version", s.Type))
	return d.onError(sc, s, inst, true)
}

// progress advances a step that has begun.
func (d *decider) progress(sc scope, s *wd.Step, inst string, f *facts, start bool) status {
	if f.finalFailure != nil {
		return failed
	}
	switch {
	case isTask(s.Type):
		return d.taskProgress(s, inst, f, start)
	case s.Type == "wait":
		for _, t := range f.timerFired {
			if t.kind == "wait" {
				d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"fired_at": history.FormatTime(d.now)}})
				return completed
			}
		}
		return running
	case s.Type == "signal":
		if f.signal != nil {
			d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: f.signal.Payload})
			return completed
		}
		for _, t := range f.timerFired {
			if t.kind == "signal_timeout" {
				d.fail(inst, "timeout", fmt.Errorf("no %s signal within %s", s.Signal.Event, s.Signal.Timeout))
				return failed
			}
		}
		return running
	case s.Type == "approval":
		return d.approvalProgress(s, inst, f)
	case s.Type == "branch":
		return d.branchProgress(sc, s, inst, f, start)
	case s.Type == "foreach":
		return d.foreachProgress(sc, s, inst, f, start)
	}
	return failed
}

func (d *decider) taskProgress(s *wd.Step, inst string, f *facts, start bool) status {
	n := f.lastAttempt
	switch f.outcome[n] {
	case "":
		return running
	case history.StepCompleted:
		return completed
	}
	e := f.failures[n]
	switch e.Next {
	case "park":
		return parked
	case "retry", "reconcile":
	default:
		return failed
	}
	r := retryPolicy(s)
	if n > r.max {
		d.finalise(inst, f, e, fmt.Sprintf("gave up after %d attempts", n))
		return failed
	}
	delay := backoff(r, n, d.runID(), inst)
	if r.maxDuration > 0 && d.now.Add(delay).Sub(f.firstSchedAt) > r.maxDuration {
		d.finalise(inst, f, e, "retry budget exhausted")
		return failed
	}
	if !start {
		return running
	}
	at := d.now.Add(delay)
	p := f.scheduled[1]
	p.AvailableAt = history.FormatTime(at)
	d.emit(history.RetryScheduled, inst, n+1, history.RetryPayload{At: history.FormatTime(at)})
	d.emit(history.StepScheduled, inst, n+1, p)
	return running
}

// finalise records that a worker failure is final, so later passes and
// replays do not reconsider it.
func (d *decider) finalise(inst string, f *facts, e history.Error, why string) {
	e.Message = e.Message + " (" + why + ")"
	e.Next = "fail"
	d.emit(history.StepFailed, inst, f.lastAttempt, history.FailedPayload{Error: e})
}

func (d *decider) approvalProgress(s *wd.Step, inst string, f *facts) status {
	if f.approval != nil {
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{
			"decision": f.approval.Decision, "decided_by": f.approval.DecidedBy,
		}})
		return completed
	}
	lastReq := f.approvalReqAt[len(f.approvalReqAt)-1]
	timedOut := false
	for _, t := range f.timerFired {
		if t.kind == "approval_timeout" && t.seq > lastReq {
			timedOut = true
		}
	}
	if !timedOut {
		return running
	}
	switch on := s.Approval.OnTimeout; {
	case on == "fail":
		d.fail(inst, "timeout", fmt.Errorf("approval timed out after %s", s.Approval.Timeout))
		return failed
	case strings.HasPrefix(on, "escalate:") && len(f.approvalReqs) == 1:
		p := f.approvalReqs[0]
		p.Role = strings.TrimPrefix(on, "escalate:")
		p.Escalated = true
		dur, _ := wd.ParseDuration(s.Approval.Timeout)
		p.TimeoutAt = history.FormatTime(d.now.Add(dur))
		d.emit(history.ApprovalRequested, inst, 0, p)
		return running
	}
	d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"decision": "rejected", "reason": "timeout"}})
	return completed
}

func (d *decider) branchProgress(sc scope, s *wd.Step, inst string, f *facts, start bool) status {
	path := *f.control.Path
	var steps []*wd.Step
	switch path {
	case "":
		d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"path": nil, "steps": map[string]any{}}})
		return completed
	case "default":
		steps = s.Branch.Default.Steps
	default:
		for _, p := range s.Branch.Paths {
			if p.Name == path {
				steps = p.Steps
			}
		}
	}
	child := scope{prefix: sc.prefix, steps: steps, iter: sc.iter}
	done, failedChild, why := d.scope(child, start)
	if !done {
		return running
	}
	if failedChild {
		d.failChild(inst, why)
		return failed
	}
	d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: map[string]any{"path": path, "steps": d.outputs(child)}})
	return completed
}

func (d *decider) foreachProgress(sc scope, s *wd.Step, inst string, f *facts, start bool) status {
	items, err := d.items(s, d.activation(sc, nil))
	if err != nil || len(items) != *f.control.Count {
		d.failChild(inst, &history.Error{Kind: "expression", Message: fmt.Sprintf("foreach items changed or failed: %v", err)})
		return failed
	}
	limit := s.Foreach.MaxConcurrency
	if limit <= 0 {
		limit = 10
	}
	active, allDone, anyFailed := 0, true, false
	var why *history.Error
	for i, it := range items {
		child := scope{prefix: fmt.Sprintf("%s%s[%d].", sc.prefix, s.ID, i), steps: s.Foreach.Steps, iter: &iteration{item: it, index: i, parent: sc.iter}}
		begunIter := d.iterationBegun(child)
		canStartIter := start && !anyFailed && (begunIter || active < limit)
		if !begunIter && !canStartIter {
			allDone = false
			continue
		}
		done, failedIter, w := d.scope(child, canStartIter)
		if failedIter {
			anyFailed = true
			if why == nil {
				why = w
			}
		}
		if !done {
			allDone = false
			active++
		} else if !d.iterationBegun(child) {
			allDone = false
		}
	}
	if anyFailed {
		if active > 0 {
			return running
		}
		d.failChild(inst, why)
		return failed
	}
	if !allDone {
		return running
	}
	outs := make([]any, len(items))
	for i := range items {
		outs[i] = d.outputs(scope{prefix: fmt.Sprintf("%s%s[%d].", sc.prefix, s.ID, i), steps: s.Foreach.Steps})
	}
	d.emit(history.StepCompleted, inst, 0, history.CompletedPayload{Output: outs})
	return completed
}

func (d *decider) iterationBegun(child scope) bool {
	for _, s := range child.steps {
		if f, ok := d.facts[child.prefix+s.ID]; ok && (d.begun(s, f) || f.completed || f.skipped) {
			return true
		}
	}
	return false
}

func (d *decider) failChild(inst string, why *history.Error) {
	msg := "a nested step failed"
	if why != nil {
		msg = why.Message
	}
	d.emit(history.StepFailed, inst, 0, history.FailedPayload{Error: history.Error{Kind: "child_failed", Message: msg, Next: "fail"}})
}

// onError runs a failed step's on_error flow, if it has one.
func (d *decider) onError(sc scope, s *wd.Step, inst string, start bool) status {
	if s.OnError == nil {
		return failed
	}
	child := scope{prefix: sc.prefix, steps: s.OnError.Steps, iter: sc.iter}
	done, failedChild, _ := d.scope(child, start)
	switch {
	case !done:
		return running
	case failedChild:
		return failed
	}
	return handled
}

// outputs collects the outputs of a scope's settled steps, keyed by step id.
func (d *decider) outputs(sc scope) map[string]any {
	out := map[string]any{}
	for _, s := range sc.steps {
		if f, ok := d.facts[sc.prefix+s.ID]; ok && f.completed {
			out[s.ID] = f.output
		}
	}
	return out
}

func (d *decider) inFlight() bool {
	for _, inst := range d.order {
		f := d.facts[inst]
		if len(f.scheduled) > 0 && f.outcome[f.lastAttempt] == "" && !f.completed {
			if p := f.scheduled[f.lastAttempt]; p.Kind == history.KindTask {
				return true
			}
		}
	}
	return false
}

// compensate runs compensating actions for completed steps in reverse
// completion order (spec 4.7), then fails the run.
func (d *decider) compensate(why *history.Error) {
	if why == nil {
		why = &history.Error{Kind: "failed", Message: "run failed", Next: "fail"}
	}
	var todo []string
	for _, inst := range d.order {
		if strings.HasPrefix(inst, history.CompensationPrefix) {
			continue
		}
		_, id := history.SplitInstance(inst)
		s := d.def.Step(id)
		if s != nil && s.Compensate != nil && d.facts[inst].completed {
			todo = append(todo, inst)
		}
	}
	sort.SliceStable(todo, func(i, j int) bool { return d.complSeq[todo[i]] > d.complSeq[todo[j]] })
	if len(todo) > 0 && !d.compDone {
		if !d.compStarted {
			d.emit(history.CompensationStarted, "", 0, map[string]any{"steps": todo})
		}
		for _, inst := range todo {
			cinst := history.CompensationPrefix + inst
			f := d.f(cinst)
			if f.completed {
				continue
			}
			if len(f.scheduled) == 0 {
				_, id := history.SplitInstance(inst)
				s := d.def.Step(id)
				act := d.activationFor(inst)
				in, err := engine.Resolve(s.Compensate.Input, act, true)
				if err != nil {
					d.fail(cinst, "expression", err)
					return
				}
				d.emit(history.StepScheduled, cinst, 1, history.ScheduledPayload{
					Kind: history.KindTask, Queue: "connector", Connector: s.Connector, Action: s.Compensate.Action,
					Input: in, KeyStep: cinst, Compensates: inst, AvailableAt: history.FormatTime(d.now),
				})
				return
			}
			_, id := history.SplitInstance(inst)
			st := d.taskProgress(d.def.Step(id), cinst, f, true)
			if st != completed {
				return // running, retrying, or parked: compensation waits
			}
		}
		d.emit(history.CompensationCompleted, "", 0, map[string]any{})
	}
	d.emit(history.RunFailed, "", 0, history.RunFailedPayload{Error: *why})
}

// activationFor builds the activation for the scope an instance lives in.
func (d *decider) activationFor(inst string) map[string]any {
	prefix, _ := history.SplitInstance(inst)
	return d.activation(scope{prefix: prefix}, nil)
}

// activation builds the expression variables visible in a scope: outputs of
// settled steps in this scope and its ancestors (inner iterations win).
func (d *decider) activation(sc scope, _ map[string]any) map[string]any {
	steps := map[string]any{}
	depth := map[string]int{}
	for _, inst := range d.order {
		if strings.HasPrefix(inst, history.CompensationPrefix) {
			continue
		}
		f := d.facts[inst]
		p, id := history.SplitInstance(inst)
		if !strings.HasPrefix(sc.prefix, p) {
			continue
		}
		if prev, ok := depth[id]; ok && prev > len(p) {
			continue
		}
		switch {
		case f.completed:
			steps[id] = map[string]any{"output": f.output}
		case f.finalFailure != nil:
			steps[id] = map[string]any{"error": map[string]any{"kind": f.finalFailure.Kind, "message": f.finalFailure.Message}}
		default:
			continue
		}
		depth[id] = len(p)
	}
	act := map[string]any{
		"trigger": d.trigger,
		"steps":   steps,
		"run":     d.runInfo,
		"env":     d.env,
	}
	if sc.iter != nil {
		act["item"] = sc.iter.item
		act["index"] = int64(sc.iter.index)
	}
	return act
}

func (d *decider) items(s *wd.Step, act map[string]any) ([]any, error) {
	v, err := engine.Eval(s.Foreach.Items, act)
	if err != nil {
		return nil, err
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("foreach items must be a list, got %T", v)
	}
	max := s.Foreach.MaxItems
	if max <= 0 {
		max = 10_000
	}
	if len(items) > max {
		return nil, fmt.Errorf("foreach has %d items, more than max_items %d", len(items), max)
	}
	return items, nil
}

func (d *decider) waitUntil(s *wd.Step, act map[string]any) (time.Time, error) {
	if s.Wait.Duration != "" {
		dur, err := wd.ParseDuration(s.Wait.Duration)
		return d.now.Add(dur), err
	}
	v, err := engine.Eval(s.Wait.Until, act)
	if err != nil {
		return time.Time{}, err
	}
	str, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("wait.until must be an RFC 3339 timestamp, got %T", v)
	}
	return time.Parse(time.RFC3339Nano, str)
}

func (d *decider) taskPayload(s *wd.Step, inst string, act map[string]any) (history.ScheduledPayload, error) {
	p := history.ScheduledPayload{Kind: history.KindTask, Queue: s.Queue(), KeyStep: inst}
	var err error
	switch s.Type {
	case "connector":
		p.Connector, p.Action, p.Connection = s.Connector, s.Action, s.Connection
		p.Input, err = engine.Resolve(orEmpty(s.Input), act, true)
	case "http":
		c := s.HTTP
		cfg := map[string]any{"method": c.Method, "url": c.URL}
		if c.Headers != nil {
			cfg["headers"] = c.Headers
		}
		if c.Query != nil {
			cfg["query"] = c.Query
		}
		if c.Body != nil {
			cfg["body"] = c.Body
		}
		if c.Class != "" {
			cfg["class"] = c.Class
		}
		if c.IdempotencyHeader != "" {
			cfg["idempotency_header"] = c.IdempotencyHeader
		}
		p.Input, err = engine.Resolve(cfg, act, true)
	default:
		p.Input, err = engine.Resolve(orEmpty(s.Input), act, true)
	}
	if err != nil {
		return p, err
	}
	if s.Effect != nil && s.Effect.IdempotencySeed != "" {
		v, err := engine.Eval(s.Effect.IdempotencySeed, act)
		if err != nil {
			return p, err
		}
		p.Seed = stringify(v)
		p.KeyStep = s.ID // a business seed identifies the item; keep the key stable across runs
	}
	return p, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (d *decider) runID() string {
	return d.started.Run.ID
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

type retry struct {
	max         int
	exponential bool
	initial     time.Duration
	maxDelay    time.Duration
	maxDuration time.Duration
}

// Defaults when a step declares no retry policy.
var defaultRetry = retry{max: 3, exponential: true, initial: 2 * time.Second, maxDelay: 5 * time.Minute}

func retryPolicy(s *wd.Step) retry {
	if s.Retry == nil {
		if s.Type == "code" {
			return retry{}
		}
		return defaultRetry
	}
	r := retry{max: s.Retry.Max, exponential: s.Retry.Backoff != "fixed", initial: defaultRetry.initial, maxDelay: defaultRetry.maxDelay}
	if d, err := wd.ParseDuration(s.Retry.Initial); err == nil {
		r.initial = d
	}
	if d, err := wd.ParseDuration(s.Retry.MaxDelay); err == nil {
		r.maxDelay = d
	}
	if d, err := wd.ParseDuration(s.Retry.MaxDuration); err == nil {
		r.maxDuration = d
	}
	return r
}

// backoff is the delay before attempt n+1. Jitter (up to 20%) is derived from
// the run, step, and attempt, so replays compute the same delay (spec 4.5).
func backoff(r retry, n int, runID, inst string) time.Duration {
	d := r.initial
	if r.exponential {
		d = time.Duration(float64(r.initial) * math.Pow(2, float64(n-1)))
	}
	if r.maxDelay > 0 && d > r.maxDelay {
		d = r.maxDelay
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(runID + "\x00" + inst + "\x00" + strconv.Itoa(n)))
	jitter := time.Duration(float64(d) * 0.2 * float64(h.Sum64()%1000) / 1000)
	return (d + jitter).Truncate(time.Millisecond)
}
