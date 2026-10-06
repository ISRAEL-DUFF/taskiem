// Package wdtest runs workflow tests (spec 10.5): a test declares a trigger,
// mocked step outcomes, signals and approval decisions, then asserts the
// path the run took and its outputs. Runs execute the real orchestrator
// (decide) over an in-memory history on a virtual clock, so a test of a
// 72-hour signal timeout takes milliseconds and never calls a provider.
package wdtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Schema identifies the test file format.
const Schema = "wd-test/v1"

// File is a test file: cases for one workflow.
type File struct {
	Schema   string         `json:"schema"`
	Workflow string         `json:"workflow"` // path to the *.wd.json, relative to the test file
	Env      map[string]any `json:"env,omitempty"`
	// Policies are the approval policies the workflow names, by name.
	Policies map[string]json.RawMessage `json:"policies,omitempty"`
	Cases    []Case                     `json:"cases"`

	path string
}

// Case is one test: a trigger, what the outside world does, and what must
// happen.
type Case struct {
	Name    string         `json:"name"`
	Trigger any            `json:"trigger"`
	Env     map[string]any `json:"env,omitempty"`
	// Mocks answer task steps (connector, http, code), keyed by step id or
	// by instance id ("pay_all[2].to_bank", "compensate:pay"); an instance
	// key wins. A list answers successive attempts; its last entry repeats.
	Mocks map[string]Mocks `json:"mocks,omitempty"`
	// Signals answer signal steps, keyed the same way.
	Signals map[string]SignalMock `json:"signals,omitempty"`
	// Approvals answer approval steps, keyed the same way.
	Approvals map[string]ApprovalMock `json:"approvals,omitempty"`
	Expect    Expect                  `json:"expect"`
}

// Mock is one attempt's outcome: an output (values may be expressions over
// "input", the step's resolved input) or an error.
type Mock struct {
	Output any        `json:"output,omitempty"`
	Error  *MockError `json:"error,omitempty"`
}

// MockError is a classified failure, as a connector would report it.
type MockError struct {
	Kind    string `json:"kind"` // retryable | fatal | unknown_outcome | not_sent | indeterminate
	Message string `json:"message,omitempty"`
}

// Mocks is one Mock or a list of them, per attempt.
type Mocks []Mock

func (m *Mocks) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var list []Mock
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("mock list is empty")
		}
		*m = list
		return nil
	}
	var one Mock
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*m = Mocks{one}
	return nil
}

// SignalMock delivers a payload, or lets the signal's timeout fire.
type SignalMock struct {
	Payload any  `json:"payload,omitempty"`
	Timeout bool `json:"timeout,omitempty"`
}

// ApprovalMock decides an approval, or lets its timeout fire.
type ApprovalMock struct {
	Decision string `json:"decision,omitempty"` // approved | rejected
	By       string `json:"by,omitempty"`
	Timeout  bool   `json:"timeout,omitempty"`
}

// Expect is what the run must have done. Every field is optional.
type Expect struct {
	// Status: completed, failed, needs_reconciliation (a step parked for an
	// operator), or blocked (waiting on something the case does not mock).
	Status string `json:"status,omitempty"`
	// Error must appear in the run's failure message.
	Error string `json:"error,omitempty"`
	// Steps: instance or step id -> completed, failed, skipped, cancelled,
	// or not_run.
	Steps map[string]string `json:"steps,omitempty"`
	// Outputs: instance id -> expected output. Objects match when every
	// expected key matches (extra keys are fine); lists and scalars match
	// exactly.
	Outputs map[string]any `json:"outputs,omitempty"`
	// Inputs: instance id -> what the step sent (its resolved input, for
	// http steps {method, url, headers, query, body}), matched as Outputs.
	// Secret references stay unresolved: tests never see secret values.
	Inputs map[string]any `json:"inputs,omitempty"`
	// Calls: instance or step id -> number of attempts sent (a step id
	// counts every instance of it).
	Calls map[string]int `json:"calls,omitempty"`
}

// Result is one case's outcome.
type Result struct {
	File     string
	Case     string
	Failures []string
	Status   string
	Trace    []string // events, for a failing case
	Duration time.Duration
}

func (r Result) Passed() bool { return len(r.Failures) == 0 }

// Load reads a test file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // reading the test files named is the point
	if err != nil {
		return nil, err
	}
	return Parse(path, raw)
}

// Parse reads a test file's contents; path locates the workflow it names.
func Parse(path string, raw []byte) (*File, error) {
	var f File
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Schema != Schema {
		return nil, fmt.Errorf("%s: schema must be %q", path, Schema)
	}
	if f.Workflow == "" || len(f.Cases) == 0 {
		return nil, fmt.Errorf("%s: workflow and at least one case are required", path)
	}
	names := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || names[c.Name] {
			return nil, fmt.Errorf("%s: every case needs a unique name (%q)", path, c.Name)
		}
		names[c.Name] = true
	}
	f.path = path
	return &f, nil
}

// WorkflowPath is the workflow file the tests run against.
func (f *File) WorkflowPath() string {
	if filepath.IsAbs(f.Workflow) {
		return f.Workflow
	}
	return filepath.Join(filepath.Dir(f.path), f.Workflow)
}

// Run runs every case in the file against its workflow. The registry gives
// connector action classes, so a mocked error is handled as the engine
// would handle it from the real connector; it may be nil when the workflow
// uses no connectors.
func (f *File) Run(reg *connector.Registry) ([]Result, error) {
	doc, err := os.ReadFile(f.WorkflowPath())
	if err != nil {
		return nil, err
	}
	return f.RunDefinition(doc, reg)
}

// RunDefinition runs every case against the given definition (the
// workflow file's contents, read from wherever it lives).
func (f *File) RunDefinition(doc []byte, reg *connector.Registry) ([]Result, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return nil, err
	}
	for name, doc := range f.Policies {
		if _, err := policy.Parse(doc); err != nil {
			return nil, fmt.Errorf("policies.%s: %w", name, err)
		}
	}
	out := make([]Result, 0, len(f.Cases))
	for _, c := range f.Cases {
		r := RunCaseWithPolicies(def, reg, f.Env, f.Policies, c)
		r.File = f.path
		out = append(out, r)
	}
	return out, nil
}

// Epoch is when every test run starts, on the virtual clock.
var Epoch = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// maxEvents bounds a run, so a workflow that retries forever fails its
// test instead of hanging it.
const maxEvents = 100_000

var mockExpr = expr.MustNewWithRoots("input")

// RunCase runs one case.
func RunCase(def *wd.Definition, reg *connector.Registry, env map[string]any, c Case) Result {
	return RunCaseWithPolicies(def, reg, env, nil, c)
}

// RunCaseWithPolicies runs one case with approval policies in force.
func RunCaseWithPolicies(def *wd.Definition, reg *connector.Registry, env map[string]any, policies map[string]json.RawMessage, c Case) Result {
	start := time.Now()
	r := &runner{def: def, reg: reg, c: c, now: Epoch, res: Result{Case: c.Name}, policies: policies}
	r.run(env)
	r.check()
	r.res.Duration = time.Since(start)
	if !r.res.Passed() {
		for _, e := range r.h {
			line := e.Type
			if e.StepID != "" {
				line += " " + e.StepID
			}
			if e.Attempt > 1 {
				line += fmt.Sprintf(" (attempt %d)", e.Attempt)
			}
			r.res.Trace = append(r.res.Trace, e.RecordedAt.Sub(Epoch).String()+"  "+line)
		}
	}
	return r.res
}

type runner struct {
	def *wd.Definition
	reg *connector.Registry
	c   Case
	h   []history.Event
	now time.Time
	res Result
	// errors in the test itself (a missing mock), reported as failures
	setup    []string
	policies map[string]json.RawMessage
}

func (r *runner) append(typ, inst string, attempt int, payload any, origin string) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			r.setup = append(r.setup, fmt.Sprintf("marshal %s: %v", typ, err))
			return
		}
		raw = b
	}
	r.h = append(r.h, history.Event{Seq: int64(len(r.h) + 1), Type: typ, StepID: inst, Attempt: attempt, Payload: raw, RecordedAt: r.now, Origin: origin})
}

func (r *runner) decide() bool {
	evs, err := decide.Decide(r.def, r.h)
	if err != nil {
		r.setup = append(r.setup, "decide: "+err.Error())
		return false
	}
	for _, e := range evs {
		r.append(e.Type, e.StepID, e.Attempt, e.Payload, history.OriginDecide)
	}
	return true
}

func (r *runner) run(env map[string]any) {
	merged := map[string]any{}
	for k, v := range env {
		merged[k] = v
	}
	for k, v := range r.c.Env {
		merged[k] = v
	}
	if probs := r.inputProblems(); len(probs) > 0 {
		for _, p := range probs {
			r.setup = append(r.setup, "trigger.body does not match the inputs schema: "+p)
		}
		return
	}
	var snaps map[string]history.PolicySnapshot
	for name, doc := range r.policies {
		if snaps == nil {
			snaps = map[string]history.PolicySnapshot{}
		}
		snaps[name] = history.PolicySnapshot{Version: 1, Document: doc}
	}
	r.append(history.RunStarted, "", 0, history.RunStartedPayload{
		Run:      history.RunInfo{ID: "test-run", TenantID: "test", WorkflowID: r.def.ID, Version: r.def.Version, Environment: "test", StartedAt: history.FormatTime(r.now)},
		Trigger:  r.c.Trigger,
		Env:      merged,
		Policies: snaps,
	}, history.OriginIngest)
	var runTimeout time.Time
	if d, err := wd.ParseDuration(r.def.Settings.Timeout); err == nil && d > 0 {
		runTimeout = r.now.Add(d)
	}
	for len(r.h) < maxEvents {
		if !r.decide() || r.terminal() {
			return
		}
		if r.act() {
			continue
		}
		// Nothing can happen now: move the clock to the next thing that can.
		next, fire := r.nextTimer()
		// A run with nothing left to wait for is reported as blocked rather
		// than left to its run timeout, which says less about why.
		if !runTimeout.IsZero() && !next.IsZero() && !runTimeout.After(next) {
			r.now = runTimeout
			r.append(history.TimerFired, "", 0, history.TimerPayload{Kind: "run_timeout"}, history.OriginScheduler)
			runTimeout = time.Time{}
			continue
		}
		if next.IsZero() {
			return // blocked
		}
		if next.After(r.now) {
			r.now = next
		}
		fire()
	}
	r.setup = append(r.setup, fmt.Sprintf("run did not finish within %d events", maxEvents))
}

func (r *runner) inputProblems() []string {
	m, ok := r.c.Trigger.(map[string]any)
	if !ok {
		return nil
	}
	body, ok := m["body"]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return []string{err.Error()}
	}
	return r.def.ValidateInputs(raw)
}

func (r *runner) terminal() bool {
	return len(r.h) > 0 && r.h[len(r.h)-1].IsTerminal()
}

// state is what the history says about each instance, for acting and for
// assertions.
type inst struct {
	id         string
	sched      map[int]history.ScheduledPayload
	last       int
	outcome    map[int]string
	failure    *history.Error // last failure
	completed  bool
	output     any
	skipped    bool
	cancelled  bool
	signal     bool
	approvalAt int64 // seq of the open approval request, 0 if none
	approval   history.ApprovalRequestedPayload
	decided    bool
	fired      map[string]int64 // timer kind -> seq of the last firing
	order      int
}

func (r *runner) state() (map[string]*inst, []*inst) {
	m := map[string]*inst{}
	var order []*inst
	get := func(id string) *inst {
		s, ok := m[id]
		if !ok {
			s = &inst{id: id, sched: map[int]history.ScheduledPayload{}, outcome: map[int]string{}, fired: map[string]int64{}, order: len(order)}
			m[id] = s
			order = append(order, s)
		}
		return s
	}
	for _, e := range r.h {
		if e.StepID == "" {
			continue
		}
		s := get(e.StepID)
		switch e.Type {
		case history.StepScheduled:
			var p history.ScheduledPayload
			_ = json.Unmarshal(e.Payload, &p)
			if v, err := expr.DecodeJSON(e.Payload); err == nil {
				p.Input = v.(map[string]any)["input"] // integers stay integers for expressions
			}
			a := e.Attempt
			if a == 0 {
				a = 1
			}
			s.sched[a] = p
			if a > s.last {
				s.last = a
			}
		case history.StepCompleted:
			if !s.completed {
				var p struct {
					Output any `json:"output"`
				}
				v, _ := expr.DecodeJSON(e.Payload)
				if mm, ok := v.(map[string]any); ok {
					p.Output = mm["output"]
				}
				s.completed, s.output = true, p.Output
			}
			s.outcome[e.Attempt] = e.Type
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			s.failure = &p.Error
			a := e.Attempt
			if a == 0 {
				a = s.last
			}
			s.outcome[a] = e.Type
		case history.StepSkipped:
			s.skipped = true
		case history.StepCancelled:
			s.cancelled = true
		case history.SignalReceived:
			s.signal = true
		case history.ApprovalRequested:
			_ = json.Unmarshal(e.Payload, &s.approval)
			s.approvalAt, s.decided = e.Seq, false
		case history.ApprovalDecided:
			s.decided = true
		case history.TimerFired:
			var p history.TimerPayload
			_ = json.Unmarshal(e.Payload, &p)
			s.fired[p.Kind] = e.Seq
		}
	}
	return m, order
}

func (s *inst) settled() bool {
	return s.completed || s.skipped || s.cancelled || (s.failure != nil && s.failure.Next == "fail")
}

// act answers the first thing that can happen now: a task due, a mocked
// signal, a mocked approval decision. It reports whether it did anything.
func (r *runner) act() bool {
	_, order := r.state()
	for _, s := range order {
		if s.settled() || s.last == 0 {
			continue
		}
		p := s.sched[s.last]
		switch p.Kind {
		case history.KindTask:
			if s.outcome[s.last] != "" {
				continue
			}
			if at, err := history.ParseTime(p.AvailableAt); err == nil && at.After(r.now) {
				continue
			}
			r.execute(s, p)
			return true
		case history.KindSignal:
			if s.signal {
				continue
			}
			if m, ok := r.signalMock(s.id); ok && !m.Timeout {
				r.append(history.SignalReceived, s.id, 0, map[string]any{"event": p.Event, "payload": m.Payload}, history.OriginSignal)
				return true
			}
		}
	}
	for _, s := range order {
		if s.approvalAt == 0 || s.decided || s.settled() {
			continue
		}
		if m, ok := r.approvalMock(s.id); ok && !m.Timeout {
			by := m.By
			if by == "" {
				by = "test-approver"
			}
			r.append(history.ApprovalDecided, s.id, 0, history.ApprovalDecidedPayload{Decision: m.Decision, DecidedBy: by, Channel: "test"}, history.OriginAPI)
			return true
		}
	}
	return false
}

// execute answers a task with its mock, classifying an error as the worker
// would for the step's action class.
func (r *runner) execute(s *inst, p history.ScheduledPayload) {
	m, ok := r.mock(s.id, s.last)
	if !ok {
		r.setup = append(r.setup, fmt.Sprintf("no mock for %s (attempt %d); add one under mocks.%s", s.id, s.last, stepKey(s.id)))
		// Fail it fatally so the run can end and the rest is still reported.
		r.append(history.StepFailed, s.id, s.last, history.FailedPayload{Error: history.Error{Kind: "fatal", Message: "no mock", Next: "fail"}}, history.OriginWorker)
		return
	}
	if m.Error == nil {
		out, err := mockExpr.Resolve(m.Output, map[string]any{"input": p.Input}, false)
		if err != nil {
			r.setup = append(r.setup, fmt.Sprintf("mock for %s: %v", s.id, err))
		}
		r.append(history.StepCompleted, s.id, s.last, history.CompletedPayload{Output: out}, history.OriginWorker)
		return
	}
	kind, err := parseKind(m.Error.Kind)
	if err != nil {
		r.setup = append(r.setup, fmt.Sprintf("mock for %s: %v", s.id, err))
		kind = effects.KindFatal
	}
	class := r.class(s.id, p)
	next := effects.AfterError(class, kind).String()
	msg := m.Error.Message
	if msg == "" {
		msg = "mocked " + kind.String()
	}
	r.append(history.StepFailed, s.id, s.last, history.FailedPayload{Error: history.Error{
		Kind: kind.String(), Message: msg, Next: next, MaybeApplied: class.MayHaveApplied(kind),
	}}, history.OriginWorker)
}

func parseKind(k string) (effects.ErrorKind, error) {
	for _, kind := range []effects.ErrorKind{effects.KindRetryable, effects.KindFatal, effects.KindUnknownOutcome, effects.KindNotSent, effects.KindIndeterminate} {
		if kind.String() == k {
			return kind, nil
		}
	}
	return 0, fmt.Errorf("unknown error kind %q (retryable, fatal, unknown_outcome, not_sent, indeterminate)", k)
}

// class is the effect class the worker would use for a task.
func (r *runner) class(id string, p history.ScheduledPayload) effects.Class {
	if p.Connector != "" {
		if r.reg != nil {
			if c, ok := r.reg.Get(p.Connector); ok {
				if a, ok := c.Manifest.Actions[p.Action]; ok {
					return a.Class
				}
			}
		}
		r.setup = append(r.setup, fmt.Sprintf("%s: connector %s action %s is not known here, so its error is treated as an unsafe write", id, p.Connector, p.Action))
		return effects.UnsafeWrite
	}
	if in, ok := p.Input.(map[string]any); ok {
		if cls, _ := in["class"].(string); cls != "" {
			if c, err := effects.ParseClass(cls); err == nil {
				return c
			}
		}
		if m, _ := in["method"].(string); m != "" && m != "GET" && m != "HEAD" {
			return effects.UnsafeWrite
		}
	}
	return effects.Read
}

// stepKey is the step id part of an instance id.
func stepKey(id string) string {
	_, s := history.SplitInstance(id)
	if strings.HasPrefix(id, history.CompensationPrefix) {
		return history.CompensationPrefix + s
	}
	return s
}

// lookup finds a value by instance id, then by step id.
func lookup[T any](m map[string]T, id string) (T, bool) {
	if v, ok := m[id]; ok {
		return v, true
	}
	v, ok := m[stepKey(id)]
	return v, ok
}

func (r *runner) mock(id string, attempt int) (Mock, bool) {
	ms, ok := lookup(r.c.Mocks, id)
	if !ok {
		return Mock{}, false
	}
	if attempt > len(ms) {
		return ms[len(ms)-1], true
	}
	return ms[attempt-1], true
}

func (r *runner) signalMock(id string) (SignalMock, bool)     { return lookup(r.c.Signals, id) }
func (r *runner) approvalMock(id string) (ApprovalMock, bool) { return lookup(r.c.Approvals, id) }

// nextTimer finds the earliest pending timer or delayed task, and returns
// how to fire it. A zero time means nothing is pending: the run is blocked.
func (r *runner) nextTimer() (time.Time, func()) {
	_, order := r.state()
	var best time.Time
	var fire func()
	consider := func(at time.Time, f func()) {
		if best.IsZero() || at.Before(best) {
			best, fire = at, f
		}
	}
	for _, s := range order {
		if s.settled() {
			continue
		}
		if s.last > 0 {
			p := s.sched[s.last]
			id := s.id
			switch p.Kind {
			case history.KindTask:
				if s.outcome[s.last] == "" {
					if at, err := history.ParseTime(p.AvailableAt); err == nil && at.After(r.now) {
						consider(at, func() {})
					}
				}
			case history.KindTimer:
				if _, done := s.fired["wait"]; !done {
					if at, err := history.ParseTime(p.FireAt); err == nil {
						consider(at, func() { r.fireTimer(id, "wait") })
					}
				}
			case history.KindSignal:
				if !s.signal && p.TimeoutAt != "" {
					if _, done := s.fired["signal_timeout"]; !done {
						if at, err := history.ParseTime(p.TimeoutAt); err == nil {
							consider(at, func() { r.fireTimer(id, "signal_timeout") })
						}
					}
				}
			}
		}
		if s.approvalAt != 0 && !s.decided && s.approval.TimeoutAt != "" && s.fired["approval_timeout"] < s.approvalAt {
			if at, err := history.ParseTime(s.approval.TimeoutAt); err == nil {
				id := s.id
				consider(at, func() { r.fireTimer(id, "approval_timeout") })
			}
		}
	}
	return best, fire
}

func (r *runner) fireTimer(id, kind string) {
	r.append(history.TimerFired, id, 0, history.TimerPayload{Kind: kind}, history.OriginScheduler)
}

// check compares the run with the case's expectations.
func (r *runner) check() {
	r.res.Failures = append(r.res.Failures, r.setup...)
	byID, order := r.state()
	r.res.Status = r.status(order)
	e := r.c.Expect
	if e.Status != "" && e.Status != r.res.Status {
		why := ""
		if r.res.Status == "blocked" {
			why = " (" + r.blockedOn(order) + ")"
		}
		r.res.Failures = append(r.res.Failures, fmt.Sprintf("status: want %s, got %s%s", e.Status, r.res.Status, why))
	}
	if e.Error != "" {
		msg := r.runError()
		if !strings.Contains(msg, e.Error) {
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("error: want it to contain %q, got %q", e.Error, msg))
		}
	}
	for _, k := range sortedKeys(e.Steps) {
		want := e.Steps[k]
		got := "not_run"
		if s, ok := byID[k]; ok {
			got = stepStatus(s)
		}
		if got != want {
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("steps.%s: want %s, got %s", k, want, got))
		}
	}
	for _, k := range sortedKeys(e.Outputs) {
		s, ok := byID[k]
		if !ok || !s.completed {
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("outputs.%s: step did not complete", k))
			continue
		}
		if path, ok := match(normalise(e.Outputs[k]), normalise(s.output), ""); !ok {
			got, _ := json.Marshal(s.output)
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("outputs.%s%s: does not match; got %s", k, path, got))
		}
	}
	for _, k := range sortedKeys(e.Inputs) {
		s, ok := byID[k]
		if !ok || s.last == 0 || s.sched[s.last].Kind != history.KindTask {
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("inputs.%s: step was not sent", k))
			continue
		}
		in := s.sched[s.last].Input
		if path, ok := match(normalise(e.Inputs[k]), normalise(in), ""); !ok {
			got, _ := json.Marshal(in)
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("inputs.%s%s: does not match; got %s", k, path, got))
		}
	}
	for _, k := range sortedKeys(e.Calls) {
		n := 0
		for _, s := range order {
			if s.id == k || (!strings.ContainsAny(k, "[.:") && stepKey(s.id) == k && !strings.HasPrefix(s.id, history.CompensationPrefix)) {
				for a, p := range s.sched {
					if p.Kind == history.KindTask && s.outcome[a] != "" {
						n++
					}
				}
			}
		}
		if n != e.Calls[k] {
			r.res.Failures = append(r.res.Failures, fmt.Sprintf("calls.%s: want %d, got %d", k, e.Calls[k], n))
		}
	}
}

func (r *runner) status(order []*inst) string {
	if len(r.h) > 0 {
		switch r.h[len(r.h)-1].Type {
		case history.RunCompleted:
			return "completed"
		case history.RunFailed:
			return "failed"
		case history.RunCancelled:
			return "cancelled"
		}
	}
	for _, s := range order {
		if !s.completed && s.failure != nil && s.failure.Next == "park" {
			return "needs_reconciliation"
		}
	}
	return "blocked"
}

func (r *runner) blockedOn(order []*inst) string {
	var waits []string
	for _, s := range order {
		if s.settled() || s.last == 0 && s.approvalAt == 0 {
			continue
		}
		switch {
		case s.approvalAt != 0 && !s.decided:
			waits = append(waits, "approval "+s.id+" has no decision in approvals")
		case s.sched[s.last].Kind == history.KindSignal && !s.signal:
			waits = append(waits, "signal "+s.id+" has no entry in signals")
		}
	}
	if len(waits) == 0 {
		return "waiting"
	}
	return strings.Join(waits, "; ")
}

func (r *runner) runError() string {
	for i := len(r.h) - 1; i >= 0; i-- {
		if r.h[i].Type == history.RunFailed {
			var p history.RunFailedPayload
			_ = json.Unmarshal(r.h[i].Payload, &p)
			return p.Error.Message
		}
	}
	return ""
}

func stepStatus(s *inst) string {
	switch {
	case s.completed:
		return "completed"
	case s.cancelled:
		return "cancelled"
	case s.skipped:
		return "skipped"
	case s.failure != nil && s.failure.Next == "fail":
		return "failed"
	case s.failure != nil && s.failure.Next == "park":
		return "parked"
	}
	return "running"
}

// normalise turns a value into its JSON form, so 1 and 1.0 and int64(1)
// compare equal.
func normalise(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// match reports whether got matches want: objects by subset, everything
// else exactly. It returns the path of the first mismatch.
func match(want, got any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		for _, k := range sortedKeys(w) {
			if p, ok := match(w[k], g[k], path+"."+k); !ok {
				return p, false
			}
			if _, present := g[k]; !present && w[k] != nil {
				return path + "." + k, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := match(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(want, got)
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Discover finds test files (*.test.json) under the given files and
// directories.
func Discover(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") && path != p) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".test.json") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
