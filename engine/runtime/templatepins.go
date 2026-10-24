package runtime

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/templates"
)

// A version that uses callable templates (decision 0047) keeps each one,
// at any depth, as the library had it when the version was saved
// (workflow_template_pins): its runs and its publishing checks read the
// pinned copies, so a library update never changes a version, and the
// workflow's page offers the update instead (TemplateUpdates).

// PinTemplates keeps with a version every template its template steps use.
// A template the library does not have is not pinned: checking the version
// says so.
func PinTemplates(ctx context.Context, tx pgx.Tx, tenant, workflow uuid.UUID, version int, doc []byte, lib *templates.Library) error {
	for _, t := range UsedTemplates(doc, lib) {
		raw, digest, err := t.Pinned()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflow_template_pins (tenant_id, workflow_id, version, template_id, digest, template)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`, tenant, workflow, version, t.ID, digest, raw); err != nil {
			return err
		}
	}
	return nil
}

// UsedTemplates are the library's templates a definition's template steps
// use, at any depth, in id order.
func UsedTemplates(doc []byte, lib *templates.Library) []*templates.Template {
	seen := map[string]*templates.Template{}
	var visit func(raw []byte)
	visit = func(raw []byte) {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		for _, id := range templateIDs(v) {
			if _, ok := seen[id]; ok {
				continue
			}
			t, ok := lib.Get(id)
			if !ok {
				continue
			}
			seen[id] = t
			visit(t.Definition)
		}
	}
	visit(doc)
	out := make([]*templates.Template, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// templateIDs are the templates the template steps in v name.
func templateIDs(v any) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if t["type"] == "template" {
				if id, ok := t["template"].(string); ok {
					out = append(out, id)
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	return out
}

// PinnedTemplates are the templates a version pinned. A template it did
// not pin (a version saved another way) is found in lib.
func PinnedTemplates(ctx context.Context, tx pgx.Tx, workflow uuid.UUID, version int, lib *templates.Library) (expansion.Templates, map[string]*templates.Template, error) {
	rows, err := tx.Query(ctx, `SELECT template FROM workflow_template_pins WHERE workflow_id = $1 AND version = $2`, workflow, version)
	if err != nil {
		return nil, nil, err
	}
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, nil, err
	}
	pinned := map[string]*templates.Template{}
	for _, raw := range raws {
		t, err := templates.Parse(raw)
		if err != nil {
			return nil, nil, err
		}
		pinned[t.ID] = t
	}
	fallback := expansion.Library(lib)
	return func(id string) (*templates.Template, error) {
		if t, ok := pinned[id]; ok {
			return t, nil
		}
		return fallback(id)
	}, pinned, nil
}

// TemplateUpdate is a template a version pinned that the library has
// changed since: its id and title, and what changed.
type TemplateUpdate struct {
	Template string   `json:"template"`
	Title    string   `json:"title"`
	Changes  []string `json:"changes"`
}

// TemplateUpdates lists the templates a version pinned that the library
// has changed since, with their differences: the definition's (wddiff
// lines, by step id), its outputs' and its parameters'.
func TemplateUpdates(pinned map[string]*templates.Template, lib *templates.Library, diff func(old, new []byte) []string) []TemplateUpdate {
	ids := make([]string, 0, len(pinned))
	for id := range pinned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []TemplateUpdate
	for _, id := range ids {
		old := pinned[id]
		cur, ok := lib.Get(id)
		if !ok {
			continue
		}
		_, a, err1 := old.Pinned()
		_, b, err2 := cur.Pinned()
		if err1 != nil || err2 != nil || string(a) == string(b) {
			continue
		}
		changes := diff(old.Definition, cur.Definition)
		changes = append(changes, named("output", outputNames(old), outputNames(cur))...)
		changes = append(changes, named("parameter", paramNames(old), paramNames(cur))...)
		if len(changes) == 0 {
			changes = []string{"~ description"}
		}
		out = append(out, TemplateUpdate{Template: id, Title: cur.Title, Changes: changes})
	}
	return out
}

func outputNames(t *templates.Template) map[string]string {
	m := map[string]string{}
	for _, o := range t.Outputs {
		raw, _ := json.Marshal(o)
		m[o.Name] = string(raw)
	}
	return m
}

func paramNames(t *templates.Template) map[string]string {
	m := map[string]string{}
	for _, p := range t.Params {
		raw, _ := json.Marshal(p)
		m[p.Name] = string(raw)
	}
	return m
}

// named lists the names added (+), removed (-) and changed (~) between two
// sets, as wddiff lists steps.
func named(kind string, old, cur map[string]string) []string {
	var out []string
	for n, v := range cur {
		switch ov, ok := old[n]; {
		case !ok:
			out = append(out, "+ "+kind+" "+n)
		case ov != v:
			out = append(out, "~ "+kind+" "+n)
		}
	}
	for n := range old {
		if _, ok := cur[n]; !ok {
			out = append(out, "- "+kind+" "+n)
		}
	}
	sort.Strings(out)
	return out
}

// definitionTemplates is a version's templates for expansion: the pinned
// ones, and lib for any not pinned.
func (s *Store) definitionTemplates(ctx context.Context, tx pgx.Tx, workflow uuid.UUID, version int) (expansion.Templates, error) {
	lib := s.Templates
	if lib == nil {
		lib = templates.Default()
	}
	find, _, err := PinnedTemplates(ctx, tx, workflow, version, lib)
	return find, err
}
