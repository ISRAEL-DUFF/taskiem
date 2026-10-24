package wd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Callable workflows and templates (decisions 0047 and 0048): a
// definition with an inputs schema and Outputs can be a template step of
// another workflow, which gets its outputs back. The rules below hold the
// definition to that interface; the template library applies the same to
// each callable template once it is filled in.

// Output is one result a callable definition gives the workflow that uses
// it. Value is an expression over the definition's own top-level steps,
// read as steps.<id>.output; a step that may not run (it has a when, an
// on_error, or needs one that may not run) is read with an optional
// chain, steps.?<id>.output, so the result never fails on a step that was
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

var outputName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

var outputExprs = expr.MustNew()

// Callable reports whether the definition declares outputs, so another
// workflow may use it as a template step.
func (d *Definition) Callable() bool { return len(d.Outputs) > 0 }

// checkOutputs holds a definition with outputs to the callable rules.
func checkOutputs(d *Definition) []Problem {
	if !d.Callable() {
		return nil
	}
	return CheckCallable(d, d.Outputs)
}

// CheckCallable is every rule a callable definition keeps: run inputs to
// be called with, no types of its own (its steps go into another
// workflow, which has its own), step ids short enough for two template
// steps' prefixes, trigger reads only of the run inputs (or under has()),
// and outputs that read only its own top-level steps, with an optional
// chain for any that may not run. The template library checks each
// callable template with it; wd.Validate checks a workflow with outputs.
func CheckCallable(d *Definition, outputs []Output) []Problem {
	var out []Problem
	add := func(path, format string, args ...any) {
		out = append(out, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if d.RawInputs == nil || len(d.RawInputs.Schema) == 0 {
		add("/inputs", "callable: a callable workflow needs run inputs (inputs.schema) to be called with")
	}
	if len(outputs) == 0 {
		add("/outputs", "callable: a callable workflow needs at least one output")
	}
	if len(d.RawTypes) > 0 {
		add("/types", "callable: a callable workflow declares no types: its steps go into another workflow, which has its own")
	}
	d.Walk(func(st *Step) {
		path := "step " + st.ID
		switch {
		case st.Origin != nil:
			// A step expansion made from a template step: the rules on
			// ids hold for the definition as written.
		case len(st.ID) > MaxCalledStepID:
			add(path, "callable: a callable workflow's step ids are at most %d characters, so two template steps' prefixes fit", MaxCalledStepID)
		case st.Type == "capability" && len(st.ID) > MaxCalledCapabilityID:
			add(path, "callable: a callable workflow's capability step ids are at most %d characters, so two template steps' prefixes and the rail's own steps fit", MaxCalledCapabilityID)
		case strings.Contains(st.ID, "__"):
			add(path, "callable: a callable workflow's step ids may not contain __ (expansion uses it)")
		case st.ID == "input" || st.ID == "body":
			add(path, "callable: input and body are the ids expansion gives its own steps")
		case st.Type == "respond":
			add(path, "callable: a respond step answers a webhook, which a workflow used as a step has no call to")
		}
	})
	if p := triggerReadsOnlyBody(d); p != nil {
		out = append(out, *p)
	}
	top := map[string]bool{}
	for _, st := range d.Steps {
		top[st.ID] = true
	}
	absent := MayNotRun(d)
	names := map[string]bool{}
	for i, o := range outputs {
		path := fmt.Sprintf("/outputs/%d", i)
		switch {
		case !outputName.MatchString(o.Name) || names[o.Name]:
			add(path, "output %q: bad or duplicate name", o.Name)
			continue
		case !slices.Contains(OutputTypes, o.Type):
			add(path, "output %q: unknown type %q", o.Name, o.Type)
			continue
		case o.Title == "" || o.Description == "":
			add(path, "output %q: title and description are required", o.Name)
			continue
		case !strings.HasPrefix(o.Value, "="):
			add(path, "output %q: the value must be an expression (=...)", o.Name)
			continue
		}
		names[o.Name] = true
		refs, err := outputExprs.References(o.Value)
		if err != nil {
			add(path, "output %q: %v", o.Name, err)
			continue
		}
		bad := false
		for r := range refs.Roots {
			if r != "steps" {
				add(path, "output %q: an output reads only the workflow's steps, not %s", o.Name, r)
				bad = true
			}
		}
		if bad {
			continue
		}
		reads, err := outputExprs.StepReads(o.Value)
		if err != nil {
			add(path, "output %q: %v", o.Name, err)
			continue
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
				add(path, "output %q: %q is not a top-level step of the workflow", o.Name, r.Step)
			case r.Test:
			case r.Field != "output":
				add(path, "output %q: read steps.%s.output, not the step's %q", o.Name, r.Step, r.Field)
			case absent[r.Step] && !r.Optional && !tested[r.Step]:
				add(path, "output %q: step %q may not run, so read it with an optional chain (steps.?%s.output)", o.Name, r.Step, r.Step)
			}
		}
	}
	return out
}

// triggerReadsOnlyBody refuses an expression that reads the trigger other
// than its body (the run inputs a template step gives), unless it tests
// the field with has(): used as a step, the workflow reads the calling
// workflow's trigger, which may not have it (run-payroll's month comes
// from a schedule's fire time when there is one).
func triggerReadsOnlyBody(d *Definition) *Problem {
	var bad *Problem
	d.Walk(func(st *Step) {
		if bad != nil {
			return
		}
		raw, err := json.Marshal(st)
		if err != nil {
			return
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		// Nested steps are walked on their own.
		if m, ok := v.(map[string]any); ok {
			delete(m, "on_error")
			if cfg, ok := m["config"].(map[string]any); ok {
				for _, k := range []string{"steps", "paths", "branches", "default"} {
					delete(cfg, k)
				}
			}
		}
		EachExpr(v, func(src string) {
			if bad != nil {
				return
			}
			paths, err := outputExprs.Paths(src, "trigger")
			if err != nil {
				return // checkExpressions reports bad expressions
			}
			for _, p := range paths {
				switch {
				case len(p) >= 2 && p[1] == "body":
				case len(p) >= 2 && regexp.MustCompile(`\bhas\(\s*trigger\.`+regexp.QuoteMeta(p[1])+`\s*\)`).MatchString(src):
					// the calling workflow's trigger, read only where it is there
				default:
					bad = &Problem{Path: "step " + st.ID, Message: fmt.Sprintf("callable: the expression %q reads %s; a callable workflow reads its run inputs (trigger.body), and any other trigger field only where it tests it with has()", src, strings.Join(p, "."))}
					return
				}
			}
		})
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

// MayNotRun reports which top-level steps of d may not run: a step with a
// when, a step with an on_error (whose output its on_error steps then
// replace), and a step that needs one of these.
func MayNotRun(d *Definition) map[string]bool {
	byID := map[string]*Step{}
	for _, st := range d.Steps {
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
	for _, st := range d.Steps {
		may(st.ID, map[string]bool{})
	}
	return out
}
