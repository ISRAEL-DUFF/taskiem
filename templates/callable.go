package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A callable template (decision 0047) can be a step of another workflow:
// a template step names it, fills its parameters and gives its run inputs,
// and the template's steps run in the caller's run (engine/expansion).
// Its interface is its run inputs (the definition's inputs schema), its
// Outputs, and its Effect, computed from its steps.

// Output is one result a callable template gives the workflow that uses
// it (wd.Output): an expression over the template's own top-level steps.
type Output = wd.Output

// OutputTypes are the types an output may have.
var OutputTypes = wd.OutputTypes

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

// checkCallable holds a callable template to its interface
// (wd.CheckCallable): run inputs to call it with, outputs that read only
// its own top-level steps (with an optional chain for any that may not
// run), and trigger reads only of the run inputs, which a template step
// gives instead.
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
	if probs := wd.CheckCallable(def, t.Outputs); len(probs) > 0 {
		return errors.New(strings.ReplaceAll(probs[0].Message, "callable workflow", "callable template"))
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

// EachExpr calls fn with every expression in a decoded JSON value
// (wd.EachExpr).
func EachExpr(v any, fn func(string)) { wd.EachExpr(v, fn) }

// MayNotRun reports which top-level steps of def may not run (wd.MayNotRun).
func MayNotRun(def *wd.Definition) map[string]bool { return wd.MayNotRun(def) }

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

// Words says the effect in English, for the AI builder's prompt: "moves
// money, waits for a person (its own approval), waits for an event".
func (e Effect) Words() string {
	does := map[string]string{EffectRead: "only reads", EffectWrite: "writes", EffectMovesMoney: "moves money"}[e.Class]
	if e.WaitsForPerson {
		does += ", waits for a person (its own approval)"
	}
	if e.WaitsForEvent {
		does += ", waits for an event"
	}
	return does
}
