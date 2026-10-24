package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A callable template (decision 0047) can be a step of another workflow:
// a template step names it, fills its parameters and gives its run inputs,
// and the template's steps run in the caller's run (engine/expansion).
// Its interface is its run inputs (the definition's inputs schema), its
// Outputs, and its Effect, computed from its steps.

// Output is one result a callable template gives the workflow that uses
// it. Value is an expression over the template's own top-level steps,
// read as steps.<id>.output; a step that may not run (it has a when, an
// on_error, or needs one that may not run) is read with an optional chain,
// steps.?<id>.output, so the result never fails on a step that was
// skipped.
type Output struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string | integer | number | boolean | list | object
	Title       string `json:"title"`
	Description string `json:"description"`
	Value       string `json:"value"`
}

// OutputTypes are the types an output may have.
var OutputTypes = []string{"string", "integer", "number", "boolean", "list", "object"}

// Effect is what a workflow, or a callable template, does at most,
// computed from its steps (never declared): whether it only reads, writes
// somewhere, or moves money; whether it waits for a person (an approval
// or a form) or for an event (a signal, a wait, a payment to settle); and
// how long its run may take.
type Effect struct {
	Class          string `json:"class"` // read | write | moves_money
	WaitsForPerson bool   `json:"waits_for_person"`
	WaitsForEvent  bool   `json:"waits_for_event"`
	Timeout        string `json:"timeout,omitempty"`
}

// Effect classes, weakest first.
const (
	EffectRead       = "read"
	EffectWrite      = "write"
	EffectMovesMoney = "moves_money"
)

var effectRank = map[string]int{EffectRead: 0, EffectWrite: 1, EffectMovesMoney: 2}

var exprs = expr.MustNew()

// checkCallable holds a callable template to its interface: run inputs to
// call it with, outputs that read only its own top-level steps (with an
// optional chain for any that may not run), and trigger reads only of the
// run inputs (trigger.body), which a template step gives instead.
func (t *Template) checkCallable() error {
	if !t.Callable {
		if len(t.Outputs) > 0 {
			return errors.New("outputs are only for a callable template")
		}
		return nil
	}
	doc, err := t.Instantiate(t.Examples(), "")
	if err != nil {
		return fmt.Errorf("callable: %w", err)
	}
	def, err := wd.Load(doc)
	if err != nil {
		return fmt.Errorf("callable: %w", err)
	}
	if def.RawInputs == nil || len(def.RawInputs.Schema) == 0 {
		return errors.New("callable: a callable template needs run inputs (definition.inputs.schema) to be called with")
	}
	if len(t.Outputs) == 0 {
		return errors.New("callable: a callable template needs at least one output")
	}
	if err := triggerReadsOnlyBody(doc); err != nil {
		return fmt.Errorf("callable: %w", err)
	}
	if len(def.RawTypes) > 0 {
		return errors.New("callable: a callable template declares no types: its steps go into another workflow, which has its own")
	}
	var badID error
	def.Walk(func(st *wd.Step) {
		switch {
		case badID != nil:
		case len(st.ID) > wd.MaxCalledStepID:
			badID = fmt.Errorf("callable: step %q: a callable template's step ids are at most %d characters, so two templates' prefixes fit", st.ID, wd.MaxCalledStepID)
		case st.Type == "capability" && len(st.ID) > wd.MaxCalledCapabilityID:
			badID = fmt.Errorf("callable: step %q: a callable template's capability step ids are at most %d characters, so two templates' prefixes and the rail's own steps fit", st.ID, wd.MaxCalledCapabilityID)
		case strings.Contains(st.ID, "__"):
			badID = fmt.Errorf("callable: step %q: a callable template's step ids may not contain __ (expansion uses it)", st.ID)
		case st.ID == "input" || st.ID == "body":
			badID = fmt.Errorf("callable: step %q: input and body are the ids expansion gives its own steps", st.ID)
		}
	})
	if badID != nil {
		return badID
	}
	top := map[string]bool{}
	for _, st := range def.Steps {
		top[st.ID] = true
	}
	absent := MayNotRun(def)
	names := map[string]bool{}
	for _, o := range t.Outputs {
		if !paramName.MatchString(o.Name) || names[o.Name] {
			return fmt.Errorf("output %q: bad or duplicate name", o.Name)
		}
		names[o.Name] = true
		if !slices.Contains(OutputTypes, o.Type) {
			return fmt.Errorf("output %q: unknown type %q", o.Name, o.Type)
		}
		if o.Title == "" || o.Description == "" {
			return fmt.Errorf("output %q: title and description are required", o.Name)
		}
		if !strings.HasPrefix(o.Value, "=") {
			return fmt.Errorf("output %q: the value must be an expression (=...)", o.Name)
		}
		refs, err := exprs.References(o.Value)
		if err != nil {
			return fmt.Errorf("output %q: %w", o.Name, err)
		}
		for r := range refs.Roots {
			if r != "steps" {
				return fmt.Errorf("output %q: an output reads only the template's steps, not %s", o.Name, r)
			}
		}
		reads, err := exprs.StepReads(o.Value)
		if err != nil {
			return fmt.Errorf("output %q: %w", o.Name, err)
		}
		tested := map[string]bool{}
		for _, r := range reads {
			if r.Test {
				tested[r.Step] = true
			}
		}
		for _, r := range reads {
			switch {
			case !top[r.Step]:
				return fmt.Errorf("output %q: %q is not a top-level step of the template", o.Name, r.Step)
			case r.Test:
			case r.Field != "output":
				return fmt.Errorf("output %q: read steps.%s.output, not the step's %q", o.Name, r.Step, r.Field)
			case absent[r.Step] && !r.Optional && !tested[r.Step]:
				return fmt.Errorf("output %q: step %q may not run, so read it with an optional chain (steps.?%s.output)", o.Name, r.Step, r.Step)
			}
		}
	}
	return nil
}

// TriggerOnly reports whether a parameter is used only by the template's
// trigger (run-payroll's pay day): used as a step, the template has no
// trigger of its own, so the caller leaves it out.
func (t *Template) TriggerOnly(name string) bool {
	var d map[string]json.RawMessage
	if json.Unmarshal(t.Definition, &d) != nil {
		return false
	}
	uses := func(raw []byte) bool {
		for _, m := range marker.FindAllStringSubmatch(string(raw), -1) {
			if m[1] == name {
				return true
			}
		}
		return false
	}
	if !uses(d["trigger"]) {
		return false
	}
	for k, raw := range d {
		if k != "trigger" && uses(raw) {
			return false
		}
	}
	for _, tb := range t.Tables {
		if uses([]byte(tb.Name)) {
			return false
		}
	}
	return true
}

// StepParams are the parameters a template step gives: every one but those
// only its trigger uses.
func (t *Template) StepParams() []Param {
	var out []Param
	for _, p := range t.Params {
		if !t.TriggerOnly(p.Name) {
			out = append(out, p)
		}
	}
	return out
}

// triggerReadsOnlyBody refuses an expression that reads the trigger other
// than its body (the run inputs a template step gives), unless it tests the
// field with has(): used as a step, the template reads the calling
// workflow's trigger, which may not have it (run-payroll's month comes
// from a schedule's fire time when there is one).
func triggerReadsOnlyBody(doc []byte) error {
	var d struct {
		Steps json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return err
	}
	var steps any
	if err := json.Unmarshal(d.Steps, &steps); err != nil {
		return err
	}
	var bad error
	EachExpr(steps, func(src string) {
		if bad != nil {
			return
		}
		paths, err := exprs.Paths(src, "trigger")
		if err != nil {
			return // wd.Load reports bad expressions
		}
		for _, p := range paths {
			switch {
			case len(p) >= 2 && p[1] == "body":
			case len(p) >= 2 && regexp.MustCompile(`\bhas\(\s*trigger\.`+regexp.QuoteMeta(p[1])+`\s*\)`).MatchString(src):
				// the calling workflow's trigger, read only where it is there
			default:
				bad = fmt.Errorf("the expression %q reads %s; a callable template reads its run inputs (trigger.body), and any other trigger field only where it tests it with has()", src, strings.Join(p, "."))
				return
			}
		}
	})
	return bad
}

// EachExpr calls fn with every expression (a string starting with "=") in
// a decoded JSON value.
func EachExpr(v any, fn func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for _, c := range t {
			EachExpr(c, fn)
		}
	case []any:
		for _, c := range t {
			EachExpr(c, fn)
		}
	case string:
		if strings.HasPrefix(t, "=") {
			fn(t)
		}
	}
}

// MayNotRun reports which top-level steps of def may not run: a step with
// a when, a step with an on_error (whose output its on_error steps then
// replace), and a step that needs one of these.
func MayNotRun(def *wd.Definition) map[string]bool {
	byID := map[string]*wd.Step{}
	for _, st := range def.Steps {
		byID[st.ID] = st
	}
	out := map[string]bool{}
	var may func(id string, seen map[string]bool) bool
	may = func(id string, seen map[string]bool) bool {
		if v, ok := out[id]; ok {
			return v
		}
		st := byID[id]
		if st == nil || seen[id] {
			return false
		}
		seen[id] = true
		res := st.When != "" || st.OnError != nil
		for _, n := range st.Needs {
			res = may(n, seen) || res
		}
		out[id] = res
		return res
	}
	for _, st := range def.Steps {
		may(st.ID, map[string]bool{})
	}
	return out
}

// EffectOf computes what a definition does at most, from its steps.
func EffectOf(def *wd.Definition, reg connector.Lookup) Effect {
	e := Effect{Class: EffectRead, Timeout: def.Settings.Timeout}
	def.Walk(func(st *wd.Step) {
		if c := StepEffect(st, reg); effectRank[c] > effectRank[e.Class] {
			e.Class = c
		}
		switch {
		case st.Type == "approval" || st.Type == "form":
			e.WaitsForPerson = true
		case st.Type == "signal" || st.Type == "wait" || (st.Type == "capability" && wd.IsWait(st.Uses)):
			e.WaitsForEvent = true
		}
	})
	return e
}

// StepEffect is what one step does: read, write, or moves money (a
// payments connector's write, or a capability that pays out or refunds).
func StepEffect(st *wd.Step, reg connector.Lookup) string {
	switch st.Type {
	case "connector":
		c, ok := reg.Get(st.Connector)
		if !ok {
			return EffectWrite
		}
		a, ok := c.Manifest.Actions[st.Action]
		switch {
		case !ok:
			return EffectWrite
		case a.Class.IsWrite() && c.Manifest.Category == "payments":
			return EffectMovesMoney
		case a.Class.IsWrite():
			return EffectWrite
		}
	case "capability":
		spec, ok := capability.Get(st.Uses)
		switch {
		case !ok:
			return EffectWrite
		case spec.IsPayout() || st.Uses == "refund":
			return EffectMovesMoney
		case slices.ContainsFunc(spec.Classes, effects.Class.IsWrite):
			return EffectWrite
		}
	case "http":
		if st.HTTP != nil && (st.HTTP.Class != "" && effects.Class(st.HTTP.Class).IsWrite() || st.HTTP.Class == "" && st.HTTP.Method != "" && st.HTTP.Method != "GET" && st.HTTP.Method != "HEAD") {
			return EffectWrite
		}
	case "container":
		if st.Container == nil || st.Container.Class != string(effects.Read) {
			return EffectWrite
		}
	case "subflow":
		return EffectWrite
	}
	return EffectRead
}

// MovesMoney reports whether the effect moves money.
func (e Effect) MovesMoney() bool { return e.Class == EffectMovesMoney }
