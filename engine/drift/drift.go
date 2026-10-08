// Package drift is the contract-drift monitor (spec 6.4): it compares what
// a connector action returned with the output schema its manifest
// declares, so a provider that changes shape (an amount that becomes a
// string, a status nobody has seen, a field that disappears) is noticed
// the first time it happens rather than when a workflow misbehaves.
//
// Findings name a path and what was expected, never the value itself,
// except an unexpected enum value that is short and not personal data:
// that value is the news ("a new status: settled").
package drift

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Kinds of drift.
const (
	KindType    = "type"    // a value of another JSON type than declared
	KindEnum    = "enum"    // a value outside the declared set
	KindMissing = "missing" // a required field absent
)

// Finding is one way an output departs from its schema.
type Finding struct {
	Path     string `json:"path"` // "/balances/*/scale"; array items are "*"
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
}

// MaxFindings bounds what one output reports.
const MaxFindings = 20

// Check compares an action's output with its declared output schema.
func Check(m *connector.Manifest, action string, output any) []Finding {
	spec, ok := m.Actions[action]
	if !ok || len(spec.Output) == 0 {
		return nil
	}
	var schema map[string]any
	if json.Unmarshal(spec.Output, &schema) != nil {
		return nil
	}
	// Handlers return Go values (int64, typed maps); compare as JSON.
	raw, err := json.Marshal(output)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	c := &checker{m: m, seen: map[string]bool{}}
	c.walk(schema, v, "", 0)
	sort.Slice(c.out, func(i, j int) bool {
		if c.out[i].Path != c.out[j].Path {
			return c.out[i].Path < c.out[j].Path
		}
		return c.out[i].Kind < c.out[j].Kind
	})
	return c.out
}

type checker struct {
	m    *connector.Manifest
	out  []Finding
	seen map[string]bool
}

func (c *checker) add(f Finding) {
	if len(c.out) >= MaxFindings || c.seen[f.Path+"\x00"+f.Kind] {
		return
	}
	c.seen[f.Path+"\x00"+f.Kind] = true
	c.out = append(c.out, f)
}

// resolve follows "#/actions/<name>/(input|output)" references.
func (c *checker) resolve(s map[string]any, depth int) map[string]any {
	for depth < 8 {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		if len(parts) != 3 || parts[0] != "actions" {
			return nil
		}
		a, ok := c.m.Actions[parts[1]]
		if !ok {
			return nil
		}
		var raw json.RawMessage
		switch parts[2] {
		case "output":
			raw = a.Output
		case "input":
			raw = a.Input
		default:
			return nil
		}
		var next map[string]any
		if json.Unmarshal(raw, &next) != nil {
			return nil
		}
		s = next
		depth++
	}
	return nil
}

func (c *checker) walk(s map[string]any, v any, path string, depth int) {
	if s = c.resolve(s, depth); s == nil || depth > 32 {
		return
	}
	at := path
	if at == "" {
		at = "/"
	}
	if types := typesOf(s["type"]); len(types) > 0 && !matches(types, v) {
		c.add(Finding{Path: at, Kind: KindType, Expected: strings.Join(types, " or "), Observed: jsonType(v)})
		return // a value of the wrong type has no sensible children to check
	}
	if enum, ok := s["enum"].([]any); ok && v != nil && !slices.ContainsFunc(enum, func(e any) bool { return equal(e, v) }) {
		c.add(Finding{Path: at, Kind: KindEnum, Expected: enumText(enum), Observed: safeValue(v)})
	}
	switch val := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := val[name]; name != "" && !present {
					c.add(Finding{Path: path + "/" + name, Kind: KindMissing, Expected: "present", Observed: "absent"})
				}
			}
		}
		for name, sub := range props {
			sm, ok := sub.(map[string]any)
			if fv, present := val[name]; ok && present {
				c.walk(sm, fv, path+"/"+name, depth+1)
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for _, it := range val {
				c.walk(items, it, path+"/*", depth+1)
			}
		}
	}
}

func typesOf(t any) []string {
	switch t := t.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func matches(types []string, v any) bool {
	got := jsonType(v)
	for _, t := range types {
		if t == got || (t == "number" && got == "integer") {
			return true
		}
	}
	return false
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func equal(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func enumText(enum []any) string {
	parts := make([]string, 0, len(enum))
	for _, e := range enum {
		b, _ := json.Marshal(e)
		parts = append(parts, string(b))
	}
	return "one of " + strings.Join(parts, ", ")
}

// safeValue shows a value outside an enum when it is a short word that is
// not personal data; otherwise only its type.
func safeValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return jsonType(v)
	}
	if len(s) > 40 || len(pii.Detect(s)) > 0 || pii.Redact(s) != s {
		return "a string (not shown)"
	}
	b, _ := json.Marshal(s)
	return string(b)
}
