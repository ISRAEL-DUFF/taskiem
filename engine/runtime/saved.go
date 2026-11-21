package runtime

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Saved things as a workflow's inputs (saved objects S3; §6.4): a step
// reads saved.price_list.body (or saved["price-list"].body), the business's
// saved thing of that name, read when the run starts, so a changed thing is
// used from the next run. A person's own saved things are never a
// business's workflow's input.

// SavedResolver reads the business's saved things of the names a
// workflow reads (as written: "price_list" is @price-list): each name's
// fields under it, those that are absent left out, and the paths of its
// personal and sensitive values, sealed in the run's history (under the
// "saved" root: {"saved", name, field}).
type SavedResolver func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, names []string) (map[string]any, []pii.Path, error)

var savedRefs = expr.MustNew()

// SavedNames are the saved things a definition's expressions read, as
// written, in order.
func SavedNames(def *wd.Definition) []string {
	seen := map[string]bool{}
	read := func(v any) {
		walkExprs(v, func(src string) {
			refs, err := savedRefs.References(src)
			if err != nil {
				return
			}
			for n := range refs.Saved {
				seen[n] = true
			}
		})
	}
	var trig any
	_ = json.Unmarshal(def.Trigger.RawCfg, &trig)
	read(trig)
	def.Walk(func(st *wd.Step) {
		var raw any
		b, _ := json.Marshal(st)
		_ = json.Unmarshal(b, &raw)
		read(raw)
	})
	if len(def.Outputs) > 0 {
		var out any
		b, _ := json.Marshal(def.Outputs)
		_ = json.Unmarshal(b, &out)
		read(out)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// walkExprs calls fn with every expression string in v.
func walkExprs(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		if expr.IsExpr(t) {
			fn(t)
		}
	case map[string]any:
		for _, c := range t {
			walkExprs(c, fn)
		}
	case []any:
		for _, c := range t {
			walkExprs(c, fn)
		}
	}
}

// savedInputs are the saved things def reads, for its run's start.
func (s *Store) savedInputs(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition) (map[string]any, []pii.Path, error) {
	if s.Saved == nil {
		return nil, nil, nil
	}
	names := SavedNames(def)
	if len(names) == 0 {
		return nil, nil, nil
	}
	return s.Saved(ctx, tx, tenant, names)
}
