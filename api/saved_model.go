package api

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai/intent"
	"github.com/israel-duff/taskiem/engine/areas"
)

// The model reads @name (saved objects S2, after its review): in a
// language other than English, the model that routes a message is told the
// person's saved things by @name and kind, never a title or anything they
// hold, so "wetin be mama number?" can be "what is @mum's phone"; what it
// returns is read as the English words are, with the person's own reach.

// savedNamed are the saved things the person in the chat reaches, by name
// and kind only: their own (not on a business's own app) and their
// business's, the most recently changed first.
func (s *Server) savedNamed(ctx context.Context, c *chat) []intent.Named {
	if c.p == nil || c.p.UserID == uuid.Nil {
		return nil
	}
	var out []intent.Named
	seen := map[string]bool{}
	read := func(o savedOwner) {
		err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` ORDER BY updated_at DESC LIMIT $2`, owner, intent.MaxNamed)
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
					out = append(out, intent.Named{Name: x.Name, Kind: strings.TrimPrefix(strings.TrimPrefix(savedKindWords(x), "an "), "a ")})
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
	if len(out) > intent.MaxNamed {
		out = out[:intent.MaxNamed]
	}
	return out
}

// waSavedEnglish does what the model's English words about a saved thing
// ask, as the typed words would; false when they are none of them.
func (s *Server) waSavedEnglish(ctx context.Context, c *chat, text string) (bool, error) {
	for _, try := range []func(context.Context, *chat, string) (bool, error){
		s.waSavedQuestion, s.waSavedName, s.waShareSaved, s.waSavedReminders,
	} {
		if done, err := try(ctx, c, text); done || err != nil {
			return true, err
		}
	}
	if s.Purchases.Ready() && s.Stock.Ready() {
		if done, err := s.waDeliverToSaved(ctx, c, text); done || err != nil {
			return true, err
		}
	}
	if s.chatAreas(ctx, c).On(areas.Money) {
		return s.waInvoiceCommand(ctx, c, text, normalizeCommand(text))
	}
	return false, nil
}
