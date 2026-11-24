package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
// "saved" root: {"saved", name, field}). read says which run reads them,
// and its steps that do, for the record of each thing's use (build plan
// H6); its zero value records nothing.
type SavedResolver func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, read SavedRead, names []string) (map[string]any, []pii.Path, error)

// SavedRead is a run reading saved things at its start: the run, its
// workflow and version, and for each name the steps reading it
// (wd.Definition.SavedReaders).
type SavedRead struct {
	Run      uuid.UUID
	Workflow uuid.UUID
	Version  int
	Readers  map[string][]string
}

// SavedNames are the saved things a definition's expressions read, as
// written, in order.
func SavedNames(def *wd.Definition) []string { return def.SavedNames() }

// savedInputs are the saved things def reads, for its run's start.
func (s *Store) savedInputs(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition, read SavedRead) (map[string]any, []pii.Path, error) {
	if s.Saved == nil {
		return nil, nil, nil
	}
	names := SavedNames(def)
	if len(names) == 0 {
		return nil, nil, nil
	}
	read.Readers = def.SavedReaders()
	got, paths, err := s.Saved(ctx, tx, tenant, read, names)
	if err != nil {
		return nil, nil, err
	}
	// A thing it reads that is not saved (removed since it was published,
	// or a name no saved thing can have) refuses the start in words, as a
	// capability it cannot run does: a schedule skips the fire.
	var missing []string
	for _, n := range names {
		if _, ok := got[n]; !ok {
			missing = append(missing, "@"+strings.ReplaceAll(n, "_", "-"))
		}
	}
	if len(missing) > 0 {
		return nil, nil, &CapabilityError{Err: fmt.Errorf("the workflow reads %s, which the business has not saved: save it on the Saved page, or change the workflow", strings.Join(missing, ", "))}
	}
	return got, paths, nil
}

// forkSaved is a resumed run's saved things: the failed run's, kept,
// sealed again as they were (the categories a fresh read gives each field,
// else those every saved thing's personal fields have).
func (s *Store) forkSaved(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition, kept map[string]any) (map[string]any, []pii.Path, error) {
	cats := map[string]map[string]string{}
	if s.Saved != nil {
		if names := SavedNames(def); len(names) > 0 {
			_, fresh, err := s.Saved(ctx, tx, tenant, SavedRead{}, names)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range fresh {
				if len(p.Segments) == 3 {
					if cats[p.Segments[1]] == nil {
						cats[p.Segments[1]] = map[string]string{}
					}
					cats[p.Segments[1]][p.Segments[2]] = p.Category
				}
			}
		}
	}
	var paths []pii.Path
	for name, v := range kept {
		fields, _ := v.(map[string]any)
		kind, _ := fields["kind"].(string)
		for k, fv := range fields {
			if str, ok := fv.(string); !ok || str == "" {
				continue
			}
			cat := cats[name][k]
			if cat == "" {
				cat = savedFieldCategory(kind, k)
			}
			if cat != "" {
				paths = append(paths, pii.Path{Segments: []string{"saved", name, k}, Category: cat})
			}
		}
	}
	return kept, paths, nil
}

// savedFieldCategory is the category a saved thing's field is sealed
// under by its name alone; "" for one that is not personal.
func savedFieldCategory(kind, key string) string {
	switch key {
	case "phone":
		return "phone"
	case "email":
		return "email"
	case "address", "line", "landmark":
		return "address"
	case "account_name", "name_on_it":
		return "name"
	case "note", "birthday", "number":
		return "other"
	case "title":
		if kind == "person" {
			return "name"
		}
	}
	return ""
}
