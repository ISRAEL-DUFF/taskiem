// Package capability is Taskiem's capability set (decision 0044,
// schemas/capabilities-v1.json): what a rail can do, with one input and
// one output shape whichever rail does it, and the payment events said the
// same way for every rail. Rails map capabilities to their own actions in
// their manifests (connector/v1 rule 16, engine/connector); templates use
// the capability.
package capability

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/schemas"
)

// Spec is one capability.
type Spec struct {
	Name        string
	Title       string
	Description string
	// Classes are the effect classes a rail's action may have to stand
	// for the capability.
	Classes []effects.Class
	// Input and Output are JSON Schemas, self-contained.
	Input, Output json.RawMessage

	input, output *schemacheck.Schema
}

// Event is one normalised payment event.
type Event struct {
	Name  string
	Title string
	// Correlation is the field a workflow waits on: the payout_id a pay
	// capability answered, the payment_id collect_link answered.
	Correlation string
	Fields      json.RawMessage

	fields *schemacheck.Schema
}

var (
	specs  = map[string]*Spec{}
	events = map[string]*Event{}
)

func init() {
	var doc struct {
		Capabilities map[string]struct {
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Classes     []effects.Class `json:"classes"`
			Input       json.RawMessage `json:"input"`
			Output      json.RawMessage `json:"output"`
		} `json:"capabilities"`
		Events map[string]struct {
			Title       string          `json:"title"`
			Correlation string          `json:"correlation"`
			Fields      json.RawMessage `json:"fields"`
		} `json:"events"`
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(schemas.CapabilitiesV1, &doc); err != nil {
		panic("capabilities-v1.json: " + err.Error())
	}
	inline := func(raw json.RawMessage) json.RawMessage {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			panic("capabilities-v1.json: " + err.Error())
		}
		b, err := json.Marshal(resolve(v, doc.Defs))
		if err != nil {
			panic(err)
		}
		return b
	}
	for name, c := range doc.Capabilities {
		s := &Spec{Name: name, Title: c.Title, Description: c.Description, Classes: c.Classes, Input: inline(c.Input), Output: inline(c.Output)}
		s.input = schemacheck.New("https://schemas.taskiem.dev/capabilities/v1/"+name+"/input.json", s.Input)
		s.output = schemacheck.New("https://schemas.taskiem.dev/capabilities/v1/"+name+"/output.json", s.Output)
		specs[name] = s
	}
	for name, e := range doc.Events {
		ev := &Event{Name: name, Title: e.Title, Correlation: e.Correlation, Fields: inline(e.Fields)}
		ev.fields = schemacheck.New("https://schemas.taskiem.dev/capabilities/v1/events/"+name+".json", ev.Fields)
		events[name] = ev
	}
}

// resolve replaces {"$ref": "#/$defs/<name>"} with the definition, so each
// schema stands alone.
func resolve(v any, defs map[string]json.RawMessage) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			name, found := strings.CutPrefix(ref, "#/$defs/")
			raw, known := defs[name]
			if !found || !known {
				panic("capabilities-v1.json: unknown $ref " + ref)
			}
			var d any
			if err := json.Unmarshal(raw, &d); err != nil {
				panic(err)
			}
			return resolve(d, defs)
		}
		for k, c := range t {
			t[k] = resolve(c, defs)
		}
	case []any:
		for i, c := range t {
			t[i] = resolve(c, defs)
		}
	}
	return v
}

// Get returns a capability by name.
func Get(name string) (*Spec, bool) {
	s, ok := specs[name]
	return s, ok
}

// Names are every capability's name, sorted.
func Names() []string {
	out := make([]string, 0, len(specs))
	for n := range specs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// GetEvent returns a normalised event by name.
func GetEvent(name string) (*Event, bool) {
	e, ok := events[name]
	return e, ok
}

// EventNames are every normalised event's name, sorted.
func EventNames() []string {
	out := make([]string, 0, len(events))
	for n := range events {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// PayoutEvents are the events that settle a payout.
var PayoutEvents = []string{"payout.settled", "payout.failed", "payout.reversed"}

// SignalName is the signal a rail's normalised event is delivered to runs
// as (engine/ingest), correlated on the event's Correlation field:
// paystack@1:payout.settled. Trigger names have no dot, so it never meets
// a trigger's own signal (paystack@1:transfer_event).
func SignalName(ref, event string) string { return ref + ":" + event }

// EventTitle is a normalised event's title, or its name.
func EventTitle(name string) string {
	if e, ok := events[name]; ok {
		return e.Title
	}
	return name
}

// WaitTitle names await_settlement, await_reversal and await_payment for
// people.
func WaitTitle(uses string) string {
	switch uses {
	case "await_reversal":
		return "Watch for the payout coming back"
	case "await_payment":
		return "Wait for the customer to pay"
	}
	return "Wait for the payout to settle"
}

// Allows reports whether an action of class c may stand for the capability.
func (s *Spec) Allows(c effects.Class) bool { return slices.Contains(s.Classes, c) }

// IsPayout reports whether the capability sends money out and answers a
// payout: a payout_id, and a status that may be pending.
func (s *Spec) IsPayout() bool {
	return slices.ContainsFunc(Fields(s.Output), func(f Field) bool { return len(f.Path) == 1 && f.Path[0] == "payout_id" })
}

// CheckInput validates a capability input.
func (s *Spec) CheckInput(v any) []string { return check(s.input, v) }

// CheckOutput validates a capability output, as a rail's mapping produced it.
func (s *Spec) CheckOutput(v any) []string { return check(s.output, v) }

// CheckFields validates an event's fields.
func (e *Event) CheckFields(v any) []string { return check(e.fields, v) }

func check(s *schemacheck.Schema, v any) []string {
	b, err := json.Marshal(v)
	if err != nil {
		return []string{err.Error()}
	}
	return s.Validate(b)
}

// Field is one property of a capability's schema, nested ones included:
// [customer email] is customer.email.
type Field struct {
	Path []string
	// Required: required at every level down to it.
	Required bool
	// Default is the schema's default, when HasDefault.
	Default    any
	HasDefault bool
	// PII is the x-pii category, or "".
	PII string
	// Leaf: no properties below it.
	Leaf bool
}

// Name is the field's dotted path.
func (f Field) Name() string { return strings.Join(f.Path, ".") }

// Fields lists the properties of an object schema, depth first, in name
// order; an array's items are "*".
func Fields(schema json.RawMessage) []Field {
	var out []Field
	var walk func(raw json.RawMessage, path []string, required bool)
	walk = func(raw json.RawMessage, path []string, required bool) {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			Items      json.RawMessage            `json:"items"`
		}
		if json.Unmarshal(raw, &s) != nil {
			return
		}
		if len(s.Items) > 0 {
			walk(s.Items, append(slices.Clip(path), "*"), false)
			return
		}
		names := make([]string, 0, len(s.Properties))
		for n := range s.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			var p struct {
				Default    *json.RawMessage           `json:"default"`
				PII        string                     `json:"x-pii"`
				Properties map[string]json.RawMessage `json:"properties"`
				Items      json.RawMessage            `json:"items"`
			}
			_ = json.Unmarshal(s.Properties[n], &p)
			f := Field{Path: append(slices.Clip(path), n), Required: required && slices.Contains(s.Required, n), PII: p.PII, Leaf: p.Properties == nil && len(p.Items) == 0}
			if p.Default != nil {
				f.HasDefault = true
				_ = json.Unmarshal(*p.Default, &f.Default)
			}
			out = append(out, f)
			walk(s.Properties[n], f.Path, f.Required)
		}
	}
	walk(schema, nil, true)
	return out
}

// Field returns the capability input's field at a dotted path.
func (s *Spec) Field(name string) (Field, bool) {
	for _, f := range Fields(s.Input) {
		if f.Name() == name {
			return f, true
		}
	}
	return Field{}, false
}

// WithDefaults returns the input with the schema's top-level defaults
// filled in where a field is absent: a mapping then always sees
// input.currency.
func (s *Spec) WithDefaults(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	for _, f := range Fields(s.Input) {
		if len(f.Path) == 1 && f.HasDefault {
			if _, ok := out[f.Path[0]]; !ok {
				out[f.Path[0]] = f.Default
			}
		}
	}
	return out
}

// MappingEngine evaluates a rail's capability mappings (connector/v1 rule
// 16): expressions over input, output and steps.
var MappingEngine = expr.MustNewWithRoots("input", "output", "steps")

// ResolveMapping evaluates a capability mapping (an expanded step's input
// or output mapping, wd-v1 capability steps) over input, output and steps;
// fields that come out null are left out.
func ResolveMapping(mp map[string]any, input, output any, steps map[string]any) (map[string]any, error) {
	if steps == nil {
		steps = map[string]any{}
	}
	act := map[string]any{"input": input, "output": output, "steps": steps}
	out := make(map[string]any, len(mp))
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, err := MappingEngine.Resolve(mp[k], act, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		if v != nil {
			out[k] = v
		}
	}
	return out, nil
}

// String names the capability for messages.
func (s *Spec) String() string { return fmt.Sprintf("%s (%s)", s.Name, s.Title) }
