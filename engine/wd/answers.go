package wd

import (
	"encoding/json"
	"sort"
)

// AnswerFields are the fields of trigger.answers a definition reads: the
// questions of its storefront action's form it needs (docs/spec/fronts.md
// §5.3). trigger.answers.size and trigger.answers["size"] read size; an
// expression reading trigger.answers whole reads no one field. Bad
// expressions are left to checkExpressions.
func AnswerFields(d *Definition) []string {
	seen := map[string]bool{}
	read := func(v any) {
		EachExpr(v, func(src string) {
			paths, err := outputExprs.Paths(src, "trigger")
			if err != nil {
				return
			}
			for _, p := range paths {
				if len(p) >= 3 && p[1] == "answers" {
					seen[p[2]] = true
				}
			}
		})
	}
	if d.Trigger.RawCfg != nil {
		var v any
		if json.Unmarshal(d.Trigger.RawCfg, &v) == nil {
			read(v)
		}
	}
	d.Walk(func(st *Step) {
		raw, err := json.Marshal(st)
		if err != nil {
			return
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			read(v)
		}
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
