// Package wddiff summarises what changed between two workflow definitions:
// top-level fields, and steps (nested ones included) added, removed or
// changed. The CLI's diff and the change-history report use it.
package wddiff

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Diff lists what changed between two definitions: top-level
// fields, and steps (nested ones included) added, removed, or changed.
func Diff(oldDoc, newDoc json.RawMessage) []string {
	var o, n map[string]any
	_ = json.Unmarshal(oldDoc, &o)
	_ = json.Unmarshal(newDoc, &n)
	var out []string
	for _, k := range []string{"name", "description", "trigger", "inputs", "types", "settings"} {
		if !jsonEqual(o[k], n[k]) {
			out = append(out, "~ "+k)
		}
	}
	before, after := flattenSteps(o), flattenSteps(n)
	ids := map[string]bool{}
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		a, inOld := before[id]
		b, inNew := after[id]
		switch {
		case !inOld:
			out = append(out, "+ step "+id)
		case !inNew:
			out = append(out, "- step "+id)
		case !jsonEqual(a, b):
			out = append(out, "~ step "+id)
		}
	}
	return out
}

// flattenSteps maps every step id to the step with its nested step lists
// replaced by their ids, so a change inside a foreach is reported on the
// nested step, not on the foreach too.
func flattenSteps(def map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(list any)
	walk = func(list any) {
		steps, _ := list.([]any)
		for _, s := range steps {
			m, ok := s.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			out[id] = stripNested(m, walk)
		}
	}
	walk(def["steps"])
	return out
}

func stripNested(step map[string]any, walk func(any)) map[string]any {
	ids := func(list any) []any {
		walk(list)
		var out []any
		steps, _ := list.([]any)
		for _, s := range steps {
			if m, ok := s.(map[string]any); ok {
				out = append(out, m["id"])
			}
		}
		return out
	}
	cp := map[string]any{}
	for k, v := range step {
		cp[k] = v
	}
	if oe, ok := step["on_error"].(map[string]any); ok {
		cp["on_error"] = ids(oe["steps"])
	}
	cfg, ok := step["config"].(map[string]any)
	if !ok {
		return cp
	}
	c := map[string]any{}
	for k, v := range cfg {
		c[k] = v
	}
	if s, ok := cfg["steps"]; ok {
		c["steps"] = ids(s)
	}
	if d, ok := cfg["default"].(map[string]any); ok {
		c["default"] = ids(d["steps"])
	}
	for _, field := range []string{"paths", "branches"} {
		list, ok := cfg[field].([]any)
		if !ok {
			continue
		}
		var parts []any
		for _, p := range list {
			pm, _ := p.(map[string]any)
			q := map[string]any{}
			for k, v := range pm {
				q[k] = v
			}
			q["steps"] = ids(pm["steps"])
			parts = append(parts, q)
		}
		c[field] = parts
	}
	cp["config"] = c
	return cp
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}
