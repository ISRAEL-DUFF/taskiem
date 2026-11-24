package api

// Saved things and services in the search (menus-everywhere.md §6,
// g-search-saved): search everywhere finds a saved thing by its name or
// kind, the person's own and their business's, and answers only its name,
// its kind and whose it is, as the picker does: never its title, its hint
// or anything it holds.

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// savedSearcher is the person whose saved things a search reads: a person
// signed in to their own account (as GET /v1/me/saved asks); uuid.Nil for
// an API key, a field agent or an embed app's user, who have none.
func savedSearcher(p *Principal) uuid.UUID {
	if p == nil || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil || p.Invitee {
		return uuid.Nil
	}
	return p.UserID
}

// maxSavedSearch bounds the saved things one search reads of each owner.
const maxSavedSearch = refSearchScan

// savedRefs are the person's own saved things, then their business's
// (tenant uuid.Nil: none), the most recently changed first, as refs.
func (s *Server) savedRefs(ctx context.Context, person, tenant uuid.UUID) ([]chatRef, error) {
	var out []chatRef
	read := func(o savedOwner) error {
		return s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			rows, err := tx.Query(ctx, `SELECT id, name, kind, COALESCE((SELECT k.name FROM saved_kinds k WHERE k.id = kind_id), ''), updated_at
				FROM saved_objects WHERE `+where+` ORDER BY updated_at DESC, id LIMIT $2`, owner, maxSavedSearch)
			if err != nil {
				return err
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (chatRef, error) {
				x := chatRef{Kind: "saved", Owner: o.label()}
				var id uuid.UUID
				err := r.Scan(&id, &x.Name, &x.SavedKind, &x.KindName, &x.When)
				x.ID = id.String()
				return x, err
			})
			out = append(out, list...)
			return err
		})
	}
	if err := read(savedOwner{person: person, by: person}); err != nil {
		return nil, err
	}
	if tenant != uuid.Nil {
		if err := read(savedOwner{tenant: tenant, by: person}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// pageOf is a page of a list read whole, from offset.
func pageOf(all []chatRef, offset int) []chatRef {
	if offset >= len(all) {
		return nil
	}
	return all[offset:min(len(all), offset+refSearchPage)]
}
