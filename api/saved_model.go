package api

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// The model reads @name (saved objects S2, folded into the chat menus'
// model in S3): the model that reads a message nothing recognised
// (chat_understand.go) is told the person's saved things by @name and
// kind, never a title or anything they hold, so "wetin be mama number?"
// can be the saved.ask action for @mum; what it picks is done as the
// catalogue's actions are, with the person's own reach.

// savedName is a saved thing as the model is told it.
type savedName struct{ name, kind string }

// maxSavedNamed bounds the saved things the model is told.
const maxSavedNamed = 100

// savedNamed are the saved things the person in the chat reaches, by name
// and kind only: their own (not on a business's own app) and their
// business's, the most recently changed first.
func (s *Server) savedNamed(ctx context.Context, c *chat) []savedName {
	if c.p == nil || c.p.UserID == uuid.Nil {
		return nil
	}
	var out []savedName
	seen := map[string]bool{}
	read := func(o savedOwner) {
		err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` ORDER BY updated_at DESC LIMIT $2`, owner, maxSavedNamed)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				x, err := scanSaved(rows)
				if err != nil {
					return err
				}
				if !seen[x.Name] {
					seen[x.Name] = true
					out = append(out, savedName{x.Name, strings.TrimPrefix(strings.TrimPrefix(savedKindWords(x), "an "), "a ")})
				}
			}
			return rows.Err()
		})
		if err != nil {
			s.Logger.Warn("saved: naming saved things for the model", "err", err)
		}
	}
	if !ownAppChat(c) {
		read(savedOwner{person: c.p.UserID, by: c.p.UserID})
	}
	if c.tenant.ID != uuid.Nil {
		read(savedOwner{tenant: c.tenant.ID, by: c.p.UserID})
	}
	if len(out) > maxSavedNamed {
		out = out[:maxSavedNamed]
	}
	return out
}

// savedNamesLine is the line the model is told the saved things in: names
// and kinds, nothing they hold; "" when there are none.
func (s *Server) savedNamesLine(ctx context.Context, c *chat) string {
	named := s.savedNamed(ctx, c)
	if len(named) == 0 {
		return ""
	}
	list := make([]string, len(named))
	for i, n := range named {
		kind := n.kind
		if r := []rune(kind); len(r) > 40 {
			kind = string(r[:40])
		}
		list[i] = "@" + n.name + " (" + kind + ")"
	}
	return "Things they saved, by @name (kind), for the saved actions' values and for @name in the other commands: " + strings.Join(list, ", ")
}

// savedChoices are the saved things offered for a menu's field (chat
// menus' choices; saved objects): the most recently changed of the kind
// the field takes, as @name, the person's own (not on a business's own
// app) and the business's, five at most.
func (s *Server) savedChoices(ctx context.Context, c *chat, kind string) []string {
	if c.p == nil || c.p.UserID == uuid.Nil {
		return nil
	}
	want := map[string]string{"saved_place": saved.KindPlace, "person": saved.KindPerson, "payee": saved.KindBankAccount}[kind]
	var out []string
	seen := map[string]bool{}
	read := func(o savedOwner) {
		_ = s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			q := `SELECT name FROM saved_objects WHERE ` + where
			args := []any{owner}
			if want != "" {
				q += ` AND kind = $2`
				args = append(args, want)
			}
			rows, err := tx.Query(ctx, q+` ORDER BY updated_at DESC LIMIT 5`, args...)
			if err != nil {
				return err
			}
			names, err := pgx.CollectRows(rows, pgx.RowTo[string])
			for _, n := range names {
				if !seen[n] && len(out) < 5 {
					seen[n] = true
					out = append(out, "@"+n)
				}
			}
			return err
		})
	}
	if c.tenant.ID != uuid.Nil {
		read(savedOwner{tenant: c.tenant.ID, by: c.p.UserID})
	}
	// A payee is the business's: an owner's own is paid by its name too,
	// but not offered.
	if !ownAppChat(c) && kind != "payee" {
		read(savedOwner{person: c.p.UserID, by: c.p.UserID})
	}
	return out
}
