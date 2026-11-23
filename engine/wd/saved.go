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
