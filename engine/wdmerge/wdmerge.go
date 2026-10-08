// Package wdmerge merges concurrent edits of a workflow definition (spec
// 10.2): each save names the version it was based on, and when another save
// landed in between, the two are merged three ways against that base.
//
// The unit of merging is a top-level field (name, trigger, settings, ...)
// or a top-level step, nested steps included: two edits of different steps
// merge cleanly; two different edits of the same step are a conflict for a
// person to resolve, never a guess.
package wdmerge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Conflict is one unit both sides changed differently.
type Conflict struct {
	// Path is "steps/<id>" or a top-level field name such as "trigger".
	Path string `json:"path"`
	// Kind: both_changed, or changed_and_deleted (one side changed it, the
	// other removed it), or both_added (each side added a different step
	// with the same id).
	Kind string `json:"kind"`
	// The unit in each version; absent where that side does not have it.
	Base   json.RawMessage `json:"base,omitempty"`
	Ours   json.RawMessage `json:"ours,omitempty"`
	Theirs json.RawMessage `json:"theirs,omitempty"`
}

// Resolution picks a side for a conflict.
type Resolution string

const (
	Ours   Resolution = "ours"
	Theirs Resolution = "theirs"
)

// fields are the merged top-level fields, besides steps.
var fields = []string{"schema", "id", "version", "name", "description", "trigger", "inputs", "types", "settings"}

// Merge merges ours and theirs, both edited from base. Conflicts listed in
// resolutions take the side given; the others are returned, and merged is
// then nil.
func Merge(base, ours, theirs []byte, resolutions map[string]Resolution) (merged []byte, conflicts []Conflict, err error) {
	var b, o, t map[string]any
	for _, x := range []struct {
		doc []byte
		to  *map[string]any
		who string
	}{{base, &b, "base"}, {ours, &o, "ours"}, {theirs, &t, "theirs"}} {
		dec := json.NewDecoder(bytes.NewReader(x.doc))
		dec.UseNumber()
		if err := dec.Decode(x.to); err != nil {
			return nil, nil, fmt.Errorf("wdmerge: %s is not a JSON object: %w", x.who, err)
		}
	}
	out := map[string]any{}
	for k := range o {
		if k != "steps" && !contains(fields, k) {
			return nil, nil, fmt.Errorf("wdmerge: unknown top-level field %q", k)
		}
	}
	for _, f := range fields {
		v, c := merge3(f, b[f], o[f], t[f], resolutions)
		if c != nil {
			conflicts = append(conflicts, *c)
			continue
		}
		if v != nil {
			out[f] = v
		}
	}

	bs, os, ts := index(b), index(o), index(t)
	stepsOut := map[string]any{}
	for _, id := range union(bs, os, ts) {
		v, c := merge3("steps/"+id, bs.byID[id], os.byID[id], ts.byID[id], resolutions)
		if c != nil {
			conflicts = append(conflicts, *c)
			continue
		}
		if v != nil {
			stepsOut[id] = v
		}
	}
	if len(conflicts) > 0 {
		return nil, conflicts, nil
	}
	steps := make([]any, 0, len(stepsOut))
	for _, id := range order(os, ts) {
		if s, ok := stepsOut[id]; ok {
			steps = append(steps, s)
		}
	}
	out["steps"] = steps
	merged, err = json.Marshal(out)
	return merged, nil, err
}

// merge3 merges one unit (nil means absent).
func merge3(path string, base, ours, theirs any, res map[string]Resolution) (any, *Conflict) {
	switch {
	case equal(ours, theirs):
		return ours, nil
	case equal(ours, base):
		return theirs, nil
	case equal(theirs, base):
		return ours, nil
	}
	switch res[path] {
	case Ours:
		return ours, nil
	case Theirs:
		return theirs, nil
	}
	kind := "both_changed"
	switch {
	case base == nil:
		kind = "both_added"
	case ours == nil || theirs == nil:
		kind = "changed_and_deleted"
	}
	return nil, &Conflict{Path: path, Kind: kind, Base: raw(base), Ours: raw(ours), Theirs: raw(theirs)}
}

type stepIndex struct {
	byID  map[string]any
	order []string
}

func index(def map[string]any) stepIndex {
	idx := stepIndex{byID: map[string]any{}}
	list, _ := def["steps"].([]any)
	for _, s := range list {
		m, _ := s.(map[string]any)
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		idx.byID[id] = s
		idx.order = append(idx.order, id)
	}
	return idx
}

func union(xs ...stepIndex) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		for _, id := range x.order {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// order is theirs' step order, with steps only ours has placed after the
// step that precedes them in ours (or first).
func order(ours, theirs stepIndex) []string {
	out := append([]string(nil), theirs.order...)
	pos := func(id string) int {
		for i, x := range out {
			if x == id {
				return i
			}
		}
		return -1
	}
	prev := ""
	for _, id := range ours.order {
		if pos(id) < 0 {
			at := 0
			if prev != "" {
				at = pos(prev) + 1
			}
			out = append(out[:at], append([]string{id}, out[at:]...)...)
		}
		prev = id
	}
	return out
}

func equal(a, b any) bool { return reflect.DeepEqual(a, b) }

func raw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
