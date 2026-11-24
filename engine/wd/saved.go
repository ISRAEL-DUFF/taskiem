package wd

import (
	"bytes"
	"encoding/json"
	"sort"
)

// SavedNames are the business's saved things the definition's steps and
// outputs read (the saved root: saved.price_list, saved["price-list"];
// saved objects S3), as written, in order. They are read when a run
// starts.
func (d *Definition) SavedNames() []string {
	seen := map[string]bool{}
	read := func(v any) {
		walkExprs(v, func(src string) {
			refs, err := engine.References(src)
			if err != nil {
				return
			}
			for n := range refs.Saved {
				seen[n] = true
			}
		})
	}
	// Steps and outputs as written, read once; most workflows read no
	// saved thing, so the word is looked for first (this runs at each
	// run's start).
	raw, err := json.Marshal(struct {
		Steps   any `json:"steps"`
		Outputs any `json:"outputs"`
	}{d.Steps, d.Outputs})
	if err != nil || !bytes.Contains(raw, []byte("saved")) {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		read(v)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SavedReaders are, for each saved thing the definition's steps read (by
// name, as written), the steps that read it, by id, in the order they
// come: what a run's record of its use says (build plan H6). (An output
// reads only steps.) A step's own expressions are read, not those of the
// steps nested in it, which say themselves.
func (d *Definition) SavedReaders() map[string][]string {
	out := map[string][]string{}
	if len(d.SavedNames()) == 0 {
		return out
	}
	add := func(where string, v any) {
		seen := map[string]bool{}
		walkExprs(v, func(src string) {
			refs, err := engine.References(src)
			if err != nil {
				return
			}
			for n := range refs.Saved {
				if !seen[n] {
					seen[n] = true
					out[n] = append(out[n], where)
				}
			}
		})
	}
	var walk func(steps []*Step)
	walk = func(steps []*Step) {
		for _, st := range steps {
			if st == nil {
				continue
			}
			raw, err := json.Marshal(st)
			if err != nil {
				continue
			}
			var m any
			if json.Unmarshal(raw, &m) == nil {
				add(st.ID, withoutSteps(m))
			}
			if st.Foreach != nil {
				walk(st.Foreach.Steps)
			}
			if st.Parallel != nil {
				for _, b := range st.Parallel.Branches {
					walk(b.Steps)
				}
			}
			if st.Branch != nil {
				for _, p := range st.Branch.Paths {
					walk(p.Steps)
				}
				if st.Branch.Default != nil {
					walk(st.Branch.Default.Steps)
				}
			}
			if st.OnError != nil {
				walk(st.OnError.Steps)
			}
		}
	}
	walk(d.Steps)
	return out
}

// withoutSteps is a step as written with the steps nested in it left out.
func withoutSteps(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if k == "steps" {
				continue
			}
			out[k] = withoutSteps(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = withoutSteps(e)
		}
		return out
	}
	return v
}
