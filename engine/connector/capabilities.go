package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

// CapabilityMap is how a rail does one capability (connector/v1 rule 16,
// decision 0044): the action (or actions, in order) that stand for it,
// with the capability's input mapped to theirs and their output mapped
// back to the capability's. A mapping value starting with "=" is an
// expression; anything else is taken as it is.
type CapabilityMap struct {
	Action string         `json:"action,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	// Steps, instead of Action and Input, for a capability that takes
	// several calls; a step's input may read steps.<id>.output.
	Steps  []CapabilityStep `json:"steps,omitempty"`
	Output map[string]any   `json:"output"`
	// Requires are optional capability inputs this rail cannot do
	// without; Unsupported are ones it cannot honour, and Fixed ones it
	// takes with a single value. A step that breaks one is refused.
	// Ignores are inputs it does not need (Paystack's checkout asks no
	// phone number): dropped when given.
	Requires    []string       `json:"requires,omitempty"`
	Unsupported []string       `json:"unsupported,omitempty"`
	Ignores     []string       `json:"ignores,omitempty"`
	Fixed       map[string]any `json:"fixed,omitempty"`
	// Unsafe declares an unsafe_write action, for a capability that
	// allows one.
	Unsafe    bool       `json:"unsafe,omitempty"`
	SettlesBy *SettlesBy `json:"settles_by,omitempty"`
	// Transfer is what the rail does with a one-time account
	// (collect_transfer only, which needs it): the page and chat tell the
	// customer what happens to a late or wrong-amount transfer.
	Transfer *TransferFacts `json:"transfer,omitempty"`
	Notes    string         `json:"notes,omitempty"`
}

// TransferFacts are a rail's documented behaviour for one-time accounts.
type TransferFacts struct {
	// OnLate is what happens to a transfer after expires_at: returned to
	// the sender, credited, or unknown (not documented).
	OnLate string `json:"on_late"`
	// OnWrongAmount: returned, credited (any amount is taken),
	// excess_returned (less is returned, more is taken and the excess
	// returned), or unknown.
	OnWrongAmount string `json:"on_wrong_amount"`
	// The lifetimes the rail allows, in minutes, and its default.
	MinMinutes     int `json:"min_minutes"`
	MaxMinutes     int `json:"max_minutes"`
	DefaultMinutes int `json:"default_minutes,omitempty"`
}

// checkTransfer checks a collect_transfer mapping's facts.
func checkTransfer(p string, t *TransferFacts, add func(string, ...any)) {
	if t == nil {
		add("%s/transfer: collect_transfer must say what the rail does with late and wrong-amount transfers, and the lifetimes it allows", p)
		return
	}
	if !slices.Contains([]string{"returned", "credited", "unknown"}, t.OnLate) {
		add("%s/transfer/on_late: %q is not returned, credited or unknown", p, t.OnLate)
	}
	if !slices.Contains([]string{"returned", "credited", "excess_returned", "unknown"}, t.OnWrongAmount) {
		add("%s/transfer/on_wrong_amount: %q is not returned, credited, excess_returned or unknown", p, t.OnWrongAmount)
	}
	if t.MinMinutes < 1 || t.MaxMinutes < t.MinMinutes {
		add("%s/transfer: min_minutes must be at least 1 and max_minutes at least min_minutes", p)
	}
	if t.DefaultMinutes != 0 && (t.DefaultMinutes < t.MinMinutes || t.DefaultMinutes > t.MaxMinutes) {
		add("%s/transfer/default_minutes: %d is outside %d to %d", p, t.DefaultMinutes, t.MinMinutes, t.MaxMinutes)
	}
}

// CapabilityStep is one action of a composite capability.
type CapabilityStep struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Input  map[string]any `json:"input"`
}

// SettlesBy is how a pending payout is followed to its end: the payout
// events of Trigger, correlated on the payout_id, and Lookup, a
// capability (get_payout) asked when no event comes.
type SettlesBy struct {
	Trigger string `json:"trigger,omitempty"`
	Lookup  string `json:"lookup"`
}

// EventMap is how one of a rail's triggers produces a normalised event.
type EventMap struct {
	Trigger string         `json:"trigger"`
	When    string         `json:"when"`
	Fields  map[string]any `json:"fields"`
}

// Plan is the capability's actions in order; a single action has no id.
func (c CapabilityMap) Plan() []CapabilityStep {
	if c.Action != "" {
		return []CapabilityStep{{Action: c.Action, Input: c.Input}}
	}
	return c.Steps
}

var (
	mapExprs   = capability.MappingEngine
	eventExprs = expr.MustNewTriggerEngine("body", "headers", "query", "item")
)

// checkCapabilities is rule 16.
func (m *Manifest) checkCapabilities(add func(string, ...any)) {
	// Rails pay and collect; products keep the books (decision 0051).
	for _, name := range sortedKeys(m.Capabilities) {
		if spec, ok := capability.Get(name); ok && spec.Kind != m.KindOf() {
			add("/capabilities/%s: only a connector of kind %s declares it (this one is %s)", name, spec.Kind, m.KindOf())
		}
	}
	for _, name := range sortedKeys(m.Events) {
		if ev, ok := capability.GetEvent(name); ok && ev.Kind != m.KindOf() {
			add("/events/%s: only a connector of kind %s sends it (this one is %s)", name, ev.Kind, m.KindOf())
		}
	}
	for _, name := range sortedKeys(m.Events) {
		m.checkEvent(name, m.Events[name], add)
	}
	for _, name := range sortedKeys(m.Capabilities) {
		m.checkCapability(name, m.Capabilities[name], add)
	}
}

func (m *Manifest) checkEvent(name string, ev EventMap, add func(string, ...any)) {
	p := "/events/" + name
	spec, ok := capability.GetEvent(name)
	if !ok {
		add("%s: not a Taskiem event (one of %s)", p, strings.Join(capability.EventNames(), ", "))
		return
	}
	if _, ok := m.Triggers[ev.Trigger]; !ok {
		add("%s/trigger: unknown trigger %q", p, ev.Trigger)
	}
	if err := eventExprs.Check(ev.When); err != nil {
		add("%s/when: %v", p, err)
	}
	fields := capability.Fields(spec.Fields)
	for _, k := range sortedKeys(ev.Fields) {
		if !slices.ContainsFunc(fields, func(f capability.Field) bool { return len(f.Path) == 1 && f.Path[0] == k }) {
			add("%s/fields/%s: %s has no such field", p, k, name)
		} else if s, ok := ev.Fields[k].(string); ok && expr.IsExpr(s) {
			if err := eventExprs.Check(s); err != nil {
				add("%s/fields/%s: %v", p, k, err)
			}
		}
	}
	for _, f := range fields {
		if len(f.Path) == 1 && f.Required {
			if _, ok := ev.Fields[f.Path[0]]; !ok {
				add("%s/fields: %s needs %s", p, name, f.Name())
			}
		}
	}
}

func (m *Manifest) checkCapability(name string, c CapabilityMap, add func(string, ...any)) {
	p := "/capabilities/" + name
	spec, ok := capability.Get(name)
	if !ok {
		add("%s: not a Taskiem capability (one of %s)", p, strings.Join(capability.Names(), ", "))
		return
	}
	if name == "collect_transfer" {
		checkTransfer(p, c.Transfer, add)
	} else if c.Transfer != nil {
		add("%s/transfer: only collect_transfer says how transfers are handled", p)
	}
	capFields := capability.Fields(spec.Input)
	field := func(n string) (capability.Field, bool) {
		i := slices.IndexFunc(capFields, func(f capability.Field) bool { return f.Name() == n })
		if i < 0 {
			return capability.Field{}, false
		}
		return capFields[i], true
	}
	for _, n := range c.Ignores {
		if _, ok := field(n); !ok {
			add("%s/ignores: %s has no input %q", p, name, n)
		}
	}
	// Requires, unsupported and fixed name optional inputs.
	for _, list := range []struct {
		key   string
		names []string
	}{{"requires", c.Requires}, {"unsupported", c.Unsupported}, {"fixed", sortedKeys(c.Fixed)}} {
		for _, n := range list.names {
			if f, ok := field(n); !ok {
				add("%s/%s: %s has no input %q", p, list.key, name, n)
			} else if f.Required {
				add("%s/%s: %q is a required input of %s already", p, list.key, n, name)
			}
		}
	}

	// Every input path the mappings read, for the checks below.
	var read [][]string
	paths := func(at string, v any, roots ...string) [][]string {
		var out [][]string
		walkExprs(v, func(src string) {
			ps, err := mapExprs.Paths(src, "input", "output", "steps")
			if err == nil {
				err = mapExprs.Check(src)
			}
			if err != nil {
				add("%s: %v", at, err)
				return
			}
			for _, q := range ps {
				if !slices.Contains(roots, q[0]) {
					add("%s: %s cannot be read here (only %s)", at, q[0], strings.Join(roots, ", "))
				}
			}
			out = append(out, ps...)
		})
		return out
	}

	plan := c.Plan()
	stepOut := map[string]string{} // step id -> action
	var lastAction string
	writes := 0
	for i, st := range plan {
		sp := p
		if c.Action == "" {
			sp = fmt.Sprintf("%s/steps/%d", p, i)
			if _, dup := stepOut[st.ID]; dup {
				add("%s/id: %q appears twice", sp, st.ID)
			}
		}
		a, ok := m.Actions[st.Action]
		if !ok {
			add("%s/action: unknown action %q", sp, st.Action)
			continue
		}
		switch {
		case a.Class == effects.Read:
		case a.Class == effects.UnsafeWrite && !c.Unsafe && spec.Allows(a.Class):
			add("%s/action: %q is an unsafe_write; declare unsafe: true to let it stand for %s", sp, st.Action, name)
		case !spec.Allows(a.Class):
			add("%s/action: %q is %s, but %s needs %s", sp, st.Action, a.Class, name, joinClasses(spec.Classes))
		}
		if a.Class.IsWrite() {
			writes++
		}
		idem := ""
		if a.Idempotency != nil {
			idem = a.Idempotency.Field
		}
		props, required := schemaProps(a.Input)
		for _, k := range sortedKeys(st.Input) {
			at := sp + "/input/" + k
			if k == idem {
				add("%s: the idempotency field is Taskiem's to set", at)
				continue
			}
			if props != nil && !slices.Contains(props, k) {
				add("%s: %q has no input %q", at, st.Action, k)
			}
			ps := paths(at, st.Input[k], "input", "steps")
			for _, q := range ps {
				if q[0] == "steps" {
					if len(q) < 3 || q[2] != "output" {
						add("%s: read an earlier step's output as steps.<id>.output", at)
					} else if _, ok := stepOut[q[1]]; !ok {
						add("%s: no earlier step %q", at, q[1])
					}
				}
			}
			read = append(read, ps...)
			if slices.Contains(required, k) {
				m.checkRequires(at, ps, field, c.Requires, add)
			}
			m.checkInputPII(at, st.Action, k, ps, capFields, add)
		}
		for _, k := range required {
			if _, ok := st.Input[k]; !ok && k != idem {
				add("%s/input: %q needs %s", sp, st.Action, k)
			}
		}
		stepOut[st.ID] = st.Action
		lastAction = st.Action
	}
	if spec.Allows(effects.Read) && writes > 0 {
		add("%s: %s only reads; its actions must be read actions", p, name)
	}
	if !spec.Allows(effects.Read) && writes == 0 && len(plan) > 0 {
		add("%s: %s changes something; one of its actions must be a write", p, name)
	}

	// Output: exactly the capability's fields, the required ones all there.
	outFields := capability.Fields(spec.Output)
	for _, k := range sortedKeys(c.Output) {
		at := p + "/output/" + k
		i := slices.IndexFunc(outFields, func(f capability.Field) bool { return len(f.Path) == 1 && f.Path[0] == k })
		if i < 0 {
			add("%s: %s has no output %q", at, name, k)
			continue
		}
		ps := paths(at, c.Output[k], "input", "output", "steps")
		read = append(read, ps...)
		if outFields[i].PII != "" || slices.ContainsFunc(outFields, func(f capability.Field) bool { return f.PII != "" && f.Path[0] == k }) {
			m.checkOutputPII(at, lastAction, stepOut, ps, add)
		}
	}
	for _, f := range outFields {
		if len(f.Path) == 1 && f.Required {
			if _, ok := c.Output[f.Path[0]]; !ok {
				add("%s/output: %s must answer %s", p, name, f.Name())
			}
		}
	}

	// Every capability input is used, or said to be ignored, unsupported or fixed.
	for _, f := range capFields {
		if !f.Leaf || slices.Contains(c.Unsupported, f.Name()) || slices.ContainsFunc(c.Ignores, func(n string) bool { return n == f.Name() || strings.HasPrefix(f.Name(), n+".") }) {
			continue
		}
		if _, ok := c.Fixed[f.Name()]; ok {
			continue
		}
		full := append([]string{"input"}, f.Path...)
		if !slices.ContainsFunc(read, func(q []string) bool { return prefixed(q, full) || prefixed(full, q) }) {
			add("%s: input %s is never used; map it, or list it under ignores, unsupported or fixed", p, f.Name())
		}
	}

	m.checkSettles(p, name, spec, c, add)
}

// checkRequires: a required action input that reads an optional
// capability input with no default must list it under requires.
func (m *Manifest) checkRequires(at string, ps [][]string, field func(string) (capability.Field, bool), requires []string, add func(string, ...any)) {
	for _, q := range ps {
		if q[0] != "input" {
			continue
		}
		for n := len(q) - 1; n >= 1; n-- {
			f, ok := field(strings.Join(q[1:n+1], "."))
			if !ok {
				continue
			}
			if !f.Required && !f.HasDefault && !slices.Contains(requires, f.Name()) {
				add("%s: reads the optional input %s; list it under requires", at, f.Name())
			}
			break
		}
	}
}

// checkInputPII: an action input fed from a personal capability input is
// itself declared personal, so it is sealed on this rail too.
func (m *Manifest) checkInputPII(at, action, key string, ps [][]string, capFields []capability.Field, add func(string, ...any)) {
	for _, q := range ps {
		if q[0] != "input" {
			continue
		}
		for _, f := range capFields {
			full := append([]string{"input"}, f.Path...)
			if f.PII == "" || (!prefixed(full, q) && !prefixed(q, full)) {
				continue
			}
			if !slices.ContainsFunc(m.Actions[action].PII, func(pf PIIField) bool {
				if _, out := OutputPIIPath(pf.Field); out {
					return false
				}
				return InputPIIPath(pf.Field)[0] == key
			}) {
				add("%s: carries %s (personal: %s), but %q does not mark %s as pii", at, f.Name(), f.PII, action, key)
			}
			return
		}
	}
}

// checkOutputPII: a personal capability output comes from an action
// output the action marks personal.
func (m *Manifest) checkOutputPII(at, last string, steps map[string]string, ps [][]string, add func(string, ...any)) {
	for _, q := range ps {
		action, rest := "", []string(nil)
		switch {
		case q[0] == "output":
			action, rest = last, q[1:]
		case q[0] == "steps" && len(q) >= 3 && q[2] == "output":
			action, rest = steps[q[1]], q[3:]
		default:
			continue
		}
		a, ok := m.Actions[action]
		if !ok {
			continue
		}
		if !slices.ContainsFunc(a.PII, func(pf PIIField) bool {
			segs, out := OutputPIIPath(pf.Field)
			return out && (prefixed(segs, rest) || prefixed(rest, segs))
		}) {
			add("%s: is personal, but %q does not mark output.%s as pii", at, action, strings.Join(rest, "."))
		}
	}
}

func (m *Manifest) checkSettles(p, name string, spec *capability.Spec, c CapabilityMap, add func(string, ...any)) {
	sb := c.SettlesBy
	if !spec.IsPayout() {
		if sb != nil {
			add("%s/settles_by: only a payout settles; %s is not one", p, name)
		}
		return
	}
	if sb == nil {
		if s, ok := c.Output["status"].(string); ok && !expr.IsExpr(s) && s != "pending" {
			return // final when it returns
		}
		add("%s/settles_by: a payout that can be pending says how it settles (trigger, lookup)", p)
		return
	}
	if _, ok := m.Capabilities[sb.Lookup]; !ok || sb.Lookup != "get_payout" {
		add("%s/settles_by/lookup: must be get_payout, declared by this rail", p)
	}
	if sb.Trigger == "" {
		return
	}
	if _, ok := m.Triggers[sb.Trigger]; !ok {
		add("%s/settles_by/trigger: unknown trigger %q", p, sb.Trigger)
		return
	}
	for _, ev := range []string{"payout.settled", "payout.failed"} {
		if e, ok := m.Events[ev]; !ok || e.Trigger != sb.Trigger {
			add("%s/settles_by/trigger: %q must produce %s (events)", p, sb.Trigger, ev)
		}
	}
}

// CapabilityInput checks a capability step's input against the rail's
// mapping (requires, unsupported, fixed) and the capability's schema, and
// returns it with the schema's defaults.
func (m *Manifest) CapabilityInput(name string, input map[string]any) (map[string]any, error) {
	c, ok := m.Capabilities[name]
	spec, known := capability.Get(name)
	if !ok || !known {
		return nil, fmt.Errorf("%s cannot %s", m.Name, name)
	}
	in := spec.WithDefaults(input)
	if msgs := spec.CheckInput(in); len(msgs) > 0 {
		return nil, fmt.Errorf("%s: %s", name, strings.Join(msgs, "; "))
	}
	for _, n := range c.Requires {
		if _, ok := lookupPath(in, n); !ok {
			return nil, fmt.Errorf("%s needs %s for %s", m.Name, n, name)
		}
	}
	for _, n := range c.Unsupported {
		if _, ok := lookupPath(input, n); ok {
			return nil, fmt.Errorf("%s cannot take %s for %s", m.Name, n, name)
		}
	}
	for _, n := range sortedKeys(c.Fixed) {
		if v, ok := lookupPath(in, n); ok && !reflect.DeepEqual(expr.Normalise(v), expr.Normalise(c.Fixed[n])) {
			return nil, fmt.Errorf("%s takes %s %v only for %s", m.Name, n, c.Fixed[n], name)
		}
	}
	return in, nil
}

// CapabilityStepInput is step i's action input: the mapping evaluated over
// the capability's input and the outputs of the steps before it (keyed by
// step id). A mapping that comes out null (input.?source when no source
// was given) is left out.
func (m *Manifest) CapabilityStepInput(name string, i int, input map[string]any, steps map[string]any) (map[string]any, error) {
	plan := m.Capabilities[name].Plan()
	if i < 0 || i >= len(plan) {
		return nil, fmt.Errorf("%s: no step %d", name, i)
	}
	return resolveMapping(plan[i].Input, map[string]any{"input": input, "steps": steps, "output": nil})
}

// CapabilityOutput maps the last action's output (and every step's) to
// the capability's output.
func (m *Manifest) CapabilityOutput(name string, input, output map[string]any, steps map[string]any) (map[string]any, error) {
	return resolveMapping(m.Capabilities[name].Output, map[string]any{"input": input, "output": output, "steps": steps})
}

// ErrNoEvent: the delivery is none of the rail's normalised events.
var ErrNoEvent = errors.New("not a normalised event")

// EventOf is the normalised event a delivery of trigger is, with its
// fields: act holds body and headers as the trigger's expressions see them.
func (m *Manifest) EventOf(trigger string, act map[string]any) (string, map[string]any, error) {
	for _, name := range sortedKeys(m.Events) {
		ev := m.Events[name]
		if ev.Trigger != trigger {
			continue
		}
		ok, err := eventExprs.EvalBool(ev.When, act)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", name, err)
		}
		if !ok {
			continue
		}
		fields, err := resolveWith(eventExprs, ev.Fields, act)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", name, err)
		}
		return name, fields, nil
	}
	return "", nil, ErrNoEvent
}

func resolveMapping(mp map[string]any, act map[string]any) (map[string]any, error) {
	return capability.ResolveMapping(mp, act["input"], act["output"], asSteps(act["steps"]))
}

func asSteps(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func resolveWith(e *expr.Engine, mp map[string]any, act map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(mp))
	for _, k := range sortedKeys(mp) {
		v, err := e.Resolve(mp[k], act, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		if v != nil {
			out[k] = v
		}
	}
	return out, nil
}

// schemaProps are an object schema's property names (nil when it leaves
// them open or refers elsewhere) and its required ones.
func schemaProps(raw json.RawMessage) (props, required []string) {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
		Ref        string                     `json:"$ref"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Ref != "" {
		return nil, nil
	}
	if s.Properties != nil {
		props = sortedKeys(s.Properties)
	}
	return props, s.Required
}

func walkExprs(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		if expr.IsExpr(t) {
			fn(t)
		}
	case map[string]any:
		for _, k := range sortedKeys(t) {
			walkExprs(t[k], fn)
		}
	case []any:
		for _, c := range t {
			walkExprs(c, fn)
		}
	}
}

func lookupPath(m map[string]any, dotted string) (any, bool) {
	var cur any = m
	for _, seg := range strings.Split(dotted, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[seg]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

// prefixed reports whether p starts with q, "*" matching any segment.
func prefixed(p, q []string) bool {
	if len(q) > len(p) {
		return false
	}
	for i := range q {
		if p[i] != q[i] && p[i] != "*" && q[i] != "*" {
			return false
		}
	}
	return true
}

func joinClasses(cs []effects.Class) string {
	s := make([]string, len(cs))
	for i, c := range cs {
		s[i] = string(c)
	}
	return strings.Join(s, " or ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultKeySpec is the key an action that declares no idempotency field
// is given (the worker's default): "tsk_" and 32 base32 characters.
var DefaultKeySpec = effects.Spec{Encoding: effects.Base32Lower, Length: 32, Prefix: "tsk_"}

// CapabilityKey is the idempotency key the step of capability name with
// id step (the action name for a single action) is called with, from key
// material: the action's own key spec, or DefaultKeySpec.
func (m *Manifest) CapabilityKey(name, step string, in effects.KeyInput) (string, error) {
	for _, st := range m.Capabilities[name].Plan() {
		id := st.ID
		if id == "" {
			id = st.Action
		}
		if id != step {
			continue
		}
		if spec := m.Actions[st.Action]; spec.Idempotency != nil {
			return spec.Idempotency.Key(in)
		}
		return DefaultKeySpec.Key(in)
	}
	return "", fmt.Errorf("%s has no step %q", name, step)
}

// CallCapability does a capability with c outside a workflow run (the
// invoice page's accounts, for example), as expansion would: the input
// checked and defaulted, each action called with its mapped input and its
// own key (keyOf, by step id: the action name for a single action), and
// the last output mapped back. base carries the credentials and the HTTP
// client.
func CallCapability(ctx context.Context, c *Connector, name string, input map[string]any, base Request, keyOf func(step string) (string, error)) (map[string]any, error) {
	m := c.Manifest
	cm, ok := m.Capabilities[name]
	if !ok {
		return nil, fmt.Errorf("%s cannot %s: %w", m.Name, name, effects.ErrFatal)
	}
	in, err := m.CapabilityInput(name, input)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	steps := map[string]any{}
	var last map[string]any
	for i, st := range cm.Plan() {
		act := c.Actions[st.Action]
		if act == nil {
			return nil, fmt.Errorf("no handler for %s: %w", st.Action, effects.ErrFatal)
		}
		ain, err := m.CapabilityStepInput(name, i, in, steps)
		if err != nil {
			return nil, fmt.Errorf("%s input: %w: %w", st.Action, err, effects.ErrFatal)
		}
		id := st.ID
		if id == "" {
			id = st.Action
		}
		req := base
		req.Input = ain
		if spec := m.Actions[st.Action]; spec.Class.IsWrite() {
			key, err := keyOf(id)
			if err != nil {
				return nil, err
			}
			req.IdempotencyKey = key
			if spec.Idempotency != nil {
				ain[spec.Idempotency.Field] = key
			}
		}
		r, err := act.Execute(ctx, req)
		if err != nil {
			return nil, err
		}
		o, ok := r.Output.(map[string]any)
		if !ok {
			raw, _ := json.Marshal(r.Output)
			if err := json.Unmarshal(raw, &o); err != nil || o == nil {
				return nil, fmt.Errorf("%s output is not an object: %w", st.Action, effects.ErrUnknownOutcome)
			}
		}
		steps[id] = map[string]any{"output": o}
		last = o
	}
	return m.CapabilityOutput(name, in, last, steps)
}
