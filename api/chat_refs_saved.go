package api

// Saved things and services in the search (menus-everywhere.md §6,
// g-search-saved): search everywhere finds a saved thing by its name or
// kind, the person's own and their business's, and answers only its name,
// its kind and whose it is, as the picker does: never its title, its hint
// or anything it holds.
//
// A saved thing's name and kind are not sealed, so the words are matched
// in SQL (each word somewhere in the name, the made-up kind's name, or a
// kind or owner whose words it starts), and refMatches then keeps those
// that match as every other ref does. The person's own are read in their
// own scope before the request's business transaction opens (a request
// holds one pooled connection at a time); the business's on that
// transaction. Both lists come newest first and are merged by date.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
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

// likeEscaped is s for LIKE … ESCAPE '\': its %, _ and \ taken as
// themselves.
func likeEscaped(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// savedMatchSQL is the WHERE clause, after the owner's, for words: each
// word in the name or the made-up kind's name, or naming the kind or the
// owner; args continue from n (the next placeholder's number).
func savedMatchSQL(words []string, owner string, n int) (string, []any) {
	var b strings.Builder
	var args []any
	ownerWords := strings.Fields(map[string]string{"mine": "mine yours", "business": "business"}[owner])
	for _, w := range words {
		var kinds []string
		for k, kw := range savedKindSearchWords {
			for _, f := range strings.Fields(kw + " saved") {
				if strings.HasPrefix(f, w) {
					kinds = append(kinds, k)
					break
				}
			}
		}
		slices.Sort(kinds)
		ownerHit := slices.ContainsFunc(append(ownerWords, "saved"), func(f string) bool { return strings.HasPrefix(f, w) })
		fmt.Fprintf(&b, ` AND (lower(o.name) LIKE $%d ESCAPE '\' OR lower(COALESCE(k.name, '')) LIKE $%d ESCAPE '\' OR o.kind = ANY($%d) OR $%d)`, n, n, n+1, n+2)
		args = append(args, "%"+likeEscaped(w)+"%", kinds, ownerHit)
		n += 3
	}
	return b.String(), args
}

// savedMatches are the owner's saved things that may match words, newest
// first, at most limit, read on tx (in the owner's scope).
func savedMatches(ctx context.Context, tx pgx.Tx, o savedOwner, words []string, limit int) ([]chatRef, error) {
	where, owner := ownerWhere(o)
	where = strings.ReplaceAll(where, "person_id", "o.person_id")
	where = strings.ReplaceAll(where, "tenant_id", "o.tenant_id")
	where = strings.ReplaceAll(where, "archived_at", "o.archived_at")
	match, args := savedMatchSQL(words, o.label(), 3)
	rows, err := tx.Query(ctx, `SELECT o.id, o.name, o.kind, COALESCE(k.name, ''), o.updated_at
		FROM saved_objects o LEFT JOIN saved_kinds k ON k.id = o.kind_id
		WHERE `+where+match+` ORDER BY o.updated_at DESC, o.id DESC LIMIT $2`, append([]any{owner, limit}, args...)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (chatRef, error) {
		x := chatRef{Kind: "saved", Owner: o.label()}
		var id uuid.UUID
		err := r.Scan(&id, &x.Name, &x.SavedKind, &x.KindName, &x.When)
		x.ID = id.String()
		return x, err
	})
}

// savedReach is how many matches a search may read of each owner from
// where its cursor stands: as far as searchRefs reads, and a page more.
func savedReach(from int) int { return from + refSearchScan + refSearchPage }

// ownSavedMatches are the person's own saved things that may match a
// search, read in their own scope; run before the business's transaction.
func (s *Server) ownSavedMatches(ctx context.Context, person uuid.UUID, q refSearch) ([]chatRef, error) {
	var out []chatRef
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		out, err = savedMatches(ctx, tx, savedOwner{person: person, by: person}, q.words, savedReach(q.from["saved"]))
		return err
	})
	return out, err
}

// savedPage is the saved things' list for a search: the person's own (read
// already) and the business's (on the request's transaction), merged
// newest first, a page from offset.
func savedPage(own []chatRef, tenant, person uuid.UUID, words []string) refPage {
	var biz []chatRef
	read := false
	return func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
		if !read && tenant != uuid.Nil {
			var err error
			if biz, err = savedMatches(ctx, tx, savedOwner{tenant: tenant, by: person}, words, savedReach(offset)); err != nil {
				return nil, err
			}
		}
		read = true
		all := make([]chatRef, 0, len(own)+len(biz))
		i, j := 0, 0
		for i < len(own) || j < len(biz) {
			if j >= len(biz) || (i < len(own) && !own[i].When.Before(biz[j].When)) {
				all = append(all, own[i])
				i++
			} else {
				all = append(all, biz[j])
				j++
			}
		}
		return pageOf(all, offset), nil
	}
}

// pageOf is a page of a list read whole, from offset.
func pageOf(all []chatRef, offset int) []chatRef {
	if offset >= len(all) {
		return nil
	}
	return all[offset:min(len(all), offset+refSearchPage)]
}
