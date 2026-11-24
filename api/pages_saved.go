package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Saved things of one kind on a page (G5; pages §5; the contract's rule
// 18; engine/pages/saved.go): `"source": {"saved": "Generator", "coming":
// "next_service"}` lists the business's saved things of a kind it made.
//
// They are shown exactly as the business's saved things show to its people
// everywhere else (saved objects §5.2, §8; api/saved.go's list): the
// business's own only, never a person's Mine; to a person only (an API key,
// a field agent or an embed app's user has no saved things, so the part
// says so); each sensitive value as the list shows it (••3456), never
// opened here, so no reveal, no step-up and nothing sealed reaches a page.
// The kind's fields, never their values, are what Check reads.

// maxPageSaved bounds the things a part reads: a business keeps 2,000 on
// the lowest plan.
const maxPageSaved = 5000

// savedUnseen is what a part of saved things says to a caller with none.
const savedUnseen = "Saved things are for the business's own people."

// savedViewer says whether the caller may see the business's saved things:
// a person signed in to their own account (personOf's rule).
func savedViewer(p *Principal) bool {
	return p != nil && p.UserID != uuid.Nil && p.KeyID == uuid.Nil && p.EndUser == nil && p.Agent == nil && !p.Invitee
}

// pageSavedKinds is the business's own live kinds, by name in lower case,
// read in its transaction.
func pageSavedKinds(ctx context.Context, tx pgx.Tx) (map[string]pages.SavedKind, map[string]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id, name, fields FROM saved_kinds WHERE tenant_id = $1 AND archived_at IS NULL ORDER BY name`, principalFrom(ctx).TenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	kinds, ids := map[string]pages.SavedKind{}, map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var raw []byte
		if err := rows.Scan(&id, &name, &raw); err != nil {
			return nil, nil, err
		}
		var fields []saved.Field
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, nil, err
		}
		kinds[strings.ToLower(name)], ids[strings.ToLower(name)] = pageKindOf(name, fields), id
	}
	return kinds, ids, rows.Err()
}

func pageKindOf(name string, fields []saved.Field) pages.SavedKind {
	k := pages.SavedKind{Name: name}
	for _, f := range fields {
		k.Fields = append(k.Fields, pages.SavedField{Key: f.Key, Label: f.Label, Type: f.Type, Sensitive: f.Sensitive || f.Type == "bank_account"})
	}
	return k
}

// pageEnvSaved gives Check the business's kinds, read once.
func pageEnvSaved(ctx context.Context, tx pgx.Tx, env *pages.Env) {
	var kinds map[string]pages.SavedKind
	read := false
	env.SavedKind = func(name string) (pages.SavedKind, bool) {
		if !read {
			read = true
			var err error
			if kinds, _, err = pageSavedKinds(ctx, tx); err != nil {
				kinds = nil
			}
		}
		k, ok := kinds[strings.ToLower(strings.TrimSpace(name))]
		return k, ok
	}
}

// savedThings reads the business's live saved things of a kind, as its
// list shows them to its people (sensitive values masked), in the
// business's own transaction.
func (s *Server) savedThings(ctx context.Context, kind string) (pages.SavedKind, []pages.SavedThing, bool, error) {
	p := principalFrom(ctx)
	var k pages.SavedKind
	var things []pages.SavedThing
	found := false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		kinds, ids, err := pageSavedKinds(ctx, tx)
		if err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(kind))
		if k, found = kinds[key]; !found {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE tenant_id = $1 AND archived_at IS NULL AND kind = $2 AND kind_id = $3
			ORDER BY lower(title), name LIMIT `+fmt.Sprint(maxPageSaved), p.TenantID, saved.KindCustom, ids[key])
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		owner := savedOwner{tenant: p.TenantID}
		for _, x := range list {
			v := x.view(owner, false)
			things = append(things, pages.SavedThing{ID: x.ID.String(), Name: x.Name, Title: x.Title, Values: v.Values, Created: x.CreatedAt, Updated: x.UpdatedAt})
		}
		return nil
	})
	return k, things, found, err
}

// drawSaved works out a part of saved things for the viewer.
func (s *Server) drawSaved(ctx context.Context, pc *pageCtx, b pages.Block, cursor pages.Cursor, out map[string]any) map[string]any {
	if b.Type == "figure" {
		out["label"], out["tone"], out["lead"], out["naira"] = b.Label, nonEmpty(b.Tone, "plain"), b.Lead, false
	}
	if !savedViewer(principalFrom(ctx)) {
		if b.Type != "figure" {
			partHead(pc, b, pages.SavedKind{Name: b.Source.Saved}.Table(), nil, out)
			noSort(out)
		}
		out["unavailable"] = savedUnseen
		return out
	}
	k, things, found, err := s.savedThings(ctx, b.Source.Saved)
	switch {
	case err != nil:
		s.Logger.Warn("pages: reading saved things", "page", pc.page.Name, "part", b.ID, "err", err)
		out["error"] = "Taskiem could not read this part just now. Pull to try again."
		return out
	case !found:
		out["broken"] = "This part needs fixing."
		return out
	}
	read := pc.page.ReadSaved(b, k, things, pc.values)
	count := len(read.Rows)
	if b.Type == "figure" {
		out["value"], out["shown"] = float64(count), pages.FigureShown(b, float64(count))
		return out
	}
	partHead(pc, b, k.Table(), nil, out)
	noSort(out)
	from := min(cursor.Skip, count)
	to := min(from+pages.RowsShown, count)
	shown := pages.ShapeSaved(b, k, pages.SavedRead{Rows: read.Rows[from:to], Tones: read.Tones[from:to]}, pc.values)
	var more any
	if to < count {
		more = pages.Cursor{Skip: to}.String()
	}
	out["rows"], out["more"], out["count"] = shown, more, count
	if b.Type == "list" && b.Group != nil {
		groups := []map[string]any{}
		for _, g := range pages.Groups(b, shown) {
			groups = append(groups, map[string]any{"key": g, "label": pages.GroupLabel(g)})
		}
		out["groups"] = groups
	}
	return out
}

// noSort keeps a table of saved things in its own order: nobody puts them
// in another (a column of theirs may be private).
func noSort(out map[string]any) {
	if cols, ok := out["columns"].([]map[string]any); ok {
		for _, c := range cols {
			c["sortable"] = false
		}
	}
}

// savedRowAct reads a saved thing a button was pressed on again, as the
// part shows it to this viewer.
func (s *Server) savedRowAct(ctx context.Context, pc *pageCtx, b pages.Block, row string) (map[string]any, error) {
	gone := fmt.Errorf("%w: this row is not on the page any more", pgx.ErrNoRows)
	if !savedViewer(principalFrom(ctx)) {
		return nil, fmt.Errorf("%w: %s", errForbidden, savedUnseen)
	}
	k, things, found, err := s.savedThings(ctx, b.Source.Saved)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, gone
	}
	read := pc.page.ReadSaved(b, k, things, pc.values)
	for _, raw := range read.Rows {
		if raw["id"] == row {
			return pages.SavedRowAct(k, raw, pc.values), nil
		}
	}
	return nil, gone
}

// pageSavedChoices are the business's kinds for the editor's Add a part:
// each with its fields' keys, labels and types and whether each is kept
// private (never its values, nor how many things there are of it).
func pageSavedChoices(ctx context.Context, tx pgx.Tx) ([]map[string]any, error) {
	kinds, _, err := pageSavedKinds(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(kinds))
	for n := range kinds {
		names = append(names, n)
	}
	slices.Sort(names)
	out := []map[string]any{}
	for _, n := range names {
		k := kinds[n]
		fields := []map[string]any{}
		for _, f := range k.Fields {
			if f.Type == "file" {
				continue
			}
			fields = append(fields, map[string]any{"key": f.Key, "label": f.Label, "type": f.Type, "private": f.Sensitive})
		}
		out = append(out, map[string]any{"name": k.Name, "fields": fields})
	}
	return out, nil
}
