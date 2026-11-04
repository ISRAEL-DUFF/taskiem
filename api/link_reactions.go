package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Reactions and stars in the conversation (WhatsApp's): a tap on a message
// adds one of six emoji to it, seen by both businesses; a star keeps a
// message for the person who starred it.

func messageParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

func viewerOf(p *Principal) links.Viewer { return links.Viewer{Actor: p.Actor(), UserID: p.UserID} }

type reactionsOut struct {
	Reactions []links.Reaction `json:"reactions"`
}

// reactionsOf is the reactions on a message of a conversation, as the
// business in scope sees them.
func (s *Server) reactionsOf(ctx context.Context, p *Principal, pair uuid.UUID) ([]links.Reaction, error) {
	m := []links.Message{{PairID: pair}}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		return links.DecorateTx(ctx, tx, p.TenantID, viewerOf(p), m)
	})
	if m[0].Reactions == nil {
		m[0].Reactions = []links.Reaction{}
	}
	return m[0].Reactions, err
}

// reactLinkMessage is PUT /v1/link-chats/{link}/messages/{id}/reaction: the
// caller's emoji on a message (one of 👍 ❤️ 😂 😮 😢 🙏), replacing the one
// they gave, written in both businesses at once; the other side's people
// are told on their phones, as for a message, unless they muted the
// conversation.
func (s *Server) reactLinkMessage(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pair, err := messageParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	lim, err := s.Store.LimitsFor(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var other uuid.UUID
	var changed bool
	var business, name string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var user string
		if p.UserID != uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&user); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&business); err != nil {
			return err
		}
		name = firstWord(user)
		var err error
		other, changed, err = links.ReactTx(ctx, tx, p.TenantID, link, pair, in.Emoji, links.Sender{ID: p.Actor(), FirstName: name}, actorOf(p), lim.LinkMessagesDaily)
		return err
	})
	if err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	if changed {
		who := name
		if who == "" {
			who = business
		}
		s.tellLinkReaction(ctx, other, link, pair, business, who, in.Emoji)
	}
	out, err := s.reactionsOf(ctx, p, pair)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reactionsOut{Reactions: out})
}

// unreactLinkMessage is DELETE /v1/link-chats/{link}/messages/{id}/reaction:
// takes the caller's reaction back, on both sides.
func (s *Server) unreactLinkMessage(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pair, err := messageParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var user string
		if p.UserID != uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&user); err != nil {
				return err
			}
		}
		return links.UnreactTx(ctx, tx, p.TenantID, link, pair, links.Sender{ID: p.Actor(), FirstName: firstWord(user)}, actorOf(p))
	})
	if err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	out, err := s.reactionsOf(ctx, p, pair)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reactionsOut{Reactions: out})
}

// tellLinkReaction tells the people of the business whose message was
// reacted to, on their phones, unless it muted the conversation: "[Dangote
// Stores] Musa reacted 👍 to “Ten bags”". A reply quoting it there is a
// reply to that message.
func (s *Server) tellLinkReaction(ctx context.Context, to, link, pair uuid.UUID, from, name, emoji string) {
	s.background(ctx, "link reaction", func(ctx context.Context) {
		var muted bool
		var snippet string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{to}, func(tx pgx.Tx) error {
			var err error
			if muted, err = links.MutedTx(ctx, tx, to, link); err != nil || muted {
				return err
			}
			return tx.QueryRow(ctx, `SELECT COALESCE(body, '') FROM link_messages WHERE tenant_id = $1 AND pair_id = $2`, to, pair).Scan(&snippet)
		}); err != nil || muted {
			return
		}
		snippet = strings.Join(strings.Fields(snippet), " ")
		if r := []rune(snippet); len(r) > 60 {
			snippet = string(r[:59]) + "…"
		}
		// A change of mind is told again; the same emoji twice is not.
		group := "react:" + pair.String() + ":" + emoji
		s.tellHoldersSent(ctx, to, []string{PermChatTrade}, group, "/chat?with="+link.String(), func(l lang.Tag) (string, string) {
			id, kv := "chat.link.reacted_plain", []string{"name", whatsapp.SafeText(name), "emoji", emoji}
			if snippet != "" {
				id, kv = "chat.link.reacted", append(kv, "what", whatsapp.SafeText(snippet))
			}
			return "[" + whatsapp.SafeText(from) + "]", tr(l, id, kv...)
		}, nil, s.keepSent(ctx, to, link, pair))
	})
}

// starLinkMessage is PUT and DELETE /v1/link-chats/{link}/messages/{id}/star:
// a message kept for the person who starred it; no one else is told or can see.
func (s *Server) starLinkMessage(w http.ResponseWriter, r *http.Request) { s.setStar(w, r, true) }

// unstarLinkMessage is DELETE .../star: the star taken off.
func (s *Server) unstarLinkMessage(w http.ResponseWriter, r *http.Request) { s.setStar(w, r, false) }

func (s *Server) setStar(w http.ResponseWriter, r *http.Request, on bool) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pair, err := messageParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errForbidden) // an API key has no stars of its own
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		return links.StarTx(r.Context(), tx, p.TenantID, p.UserID, link, pair, on)
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"starred": on})
}

// starredLinkMessages is GET /v1/link-chats/starred: the caller's starred
// messages across conversations, the most recently starred first.
func (s *Server) starredLinkMessages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := linkMessagesOut{Messages: []links.Message{}}
	if p.UserID == uuid.Nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Messages, err = links.StarredTx(r.Context(), tx, p.TenantID, p.UserID); err != nil {
			return err
		}
		return links.DecorateTx(r.Context(), tx, p.TenantID, viewerOf(p), out.Messages)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
