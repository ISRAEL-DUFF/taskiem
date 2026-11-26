package api

// Link previews in each kind of chat (docs/spec/chat-previews-and-editing.md
// §2.2): the composer's "preview this link" request, whether a person may
// write where they ask, and each chat's picture route. The words of a
// preview are kept as each chat keeps its words (sealed in people chats
// and groups, sealed under the customer's subject on a business's copy of
// a customer chat); the picture as each keeps its files.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/pii"
)

func (s *Server) linkPreviewRoutes(r chi.Router) {
	r.Post("/link-preview", s.previewLink)
	r.Get("/link-preview/{id}/image", s.heldPreviewImage)
}

// peopleMaySend is whether the person can write in a people chat now.
func (s *Server) peopleMaySend(ctx context.Context, person uuid.UUID, chat string) bool {
	ok := false
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chat, false)
		ok = err == nil && c.Peer != nil && !c.Blocked
		return nil
	})
	return ok
}

// groupMaySend is whether the person is in a group now.
func (s *Server) groupMaySend(ctx context.Context, person uuid.UUID, group string) bool {
	ok := false
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, group, false)
		ok = err == nil && g.current()
		return nil
	})
	return ok
}

// doorMaySend is whether the person can write to a business in their chat
// with it now.
func (s *Server) doorMaySend(ctx context.Context, person uuid.UUID, chat string) bool {
	ok := false
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chat)
		ok = err == nil && !c.Blocked && !c.BlockedYou
		return nil
	})
	return ok
}

// previewChatOf is whose a chat's previews are (scope and label, as
// makePreview takes them), when the person may write in it: chat is
// "link:<link>" (a linked business), "customer:<thread>" (a business's
// customer), "people:<chat>", "group:<group>" or "door:<chat>" (a person's
// chat with a business).
func (s *Server) previewChatOf(ctx context.Context, p *Principal, chat string) (scope, label string, ok bool) {
	kind, id, _ := strings.Cut(chat, ":")
	switch kind {
	case "link":
		link, err := uuid.Parse(id)
		if err != nil || p.TenantID == uuid.Nil || !p.Can(PermChatTrade) {
			return "", "", false
		}
		err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			l, err := links.GetTx(ctx, tx, link)
			if err == nil && l.State != links.Active {
				err = links.ErrState
			}
			return err
		})
		return "t:" + p.TenantID.String(), p.TenantID.String(), err == nil
	case "customer":
		if p.TenantID == uuid.Nil || !p.Can(PermOrders) {
			return "", "", false
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			_, err := s.customerThreadIn(ctx, tx, p.TenantID, id)
			return err
		})
		return "t:" + p.TenantID.String(), p.TenantID.String(), err == nil
	case "people", "group", "door":
		if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil {
			return "", "", false
		}
		me := p.UserID
		may := map[string]func(context.Context, uuid.UUID, string) bool{"people": s.peopleMaySend, "group": s.groupMaySend, "door": s.doorMaySend}[kind]
		return "p:" + me.String(), me.String(), may(ctx, me, id)
	}
	return "", "", false
}

// previewOut is the composer's preview: what the card shows, its id to
// send with, and its picture's size and where to load it.
type previewOut struct {
	ID string `json:"id"`
	links.Preview
	Image *previewOutImage `json:"image,omitempty"`
}

type previewOutImage struct {
	ID string `json:"id"`
	W  int    `json:"w"`
	H  int    `json:"h"`
}

// previewLink is POST /v1/link-preview {url, chat}: the card of a link
// typed in the composer, for a chat the person can write in, with the
// same limits and remembered pages as sending; 204 when there is none.
// The preview, and its picture, are held 30 minutes for the message sent
// with it (preview: {id}); a picture never sent is never stored.
func (s *Server) previewLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL  string `json:"url"`
		Chat string `json:"chat"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	raw := previewURL(in.URL, s.previewAnyPort)
	if raw == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	scope, label, ok := s.previewChatOf(r.Context(), p, in.Chat)
	if !ok {
		writeErr(w, http.StatusForbidden, "You cannot write in that chat.")
		return
	}
	made := s.makePreview(r.Context(), scope, p.Actor(), label, raw)
	if made == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	id := s.holds.put(p.Actor(), in.Chat, made, s.now())
	out := previewOut{ID: id, Preview: made.Preview}
	if made.img != nil {
		out.Image = &previewOutImage{ID: id, W: made.img.Width, H: made.img.Height}
	}
	writeJSON(w, http.StatusOK, out)
}

// heldPreviewImage is GET /v1/link-preview/{id}/image (?size=small): the
// picture of a preview held for the person's composer.
func (s *Server) heldPreviewImage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	h, ok := s.holds.get(chi.URLParam(r, "id"), p.Actor(), s.now())
	if !ok || h.made == nil || h.made.img == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	data := h.made.img.Big
	if r.URL.Query().Get("size") == "small" {
		data = h.made.img.Small
	}
	writePreviewImage(w, data)
}

// peoplePreviewImage is GET /v1/me/people/{id}/previews/{message}: the
// person's own copy of the picture of a message's link preview.
func (s *Server) peoplePreviewImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		msg, err := uuid.Parse(chi.URLParam(r, "message"))
		if err != nil {
			return errDoorNone
		}
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT f.sealed FROM people_files f JOIN people_messages m ON m.person_id = f.person_id AND m.id = f.message_id
			WHERE f.person_id = $1 AND m.chat_id = $2 AND f.message_id = $3 AND f.kind = 'link_image' AND m.deleted_at IS NULL`, person, c.ID, msg).Scan(&sealed); err != nil {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		data, err = peoplechats.Open(key, person, linkImageRow(msg), sealed)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writePreviewImage(w, data)
}

// groupPreviewImage is GET /v1/me/groups/{id}/previews/{message}: the
// picture of a group message's link preview, for a member who sees it.
func (s *Server) groupPreviewImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.deletedAt != nil {
			return errDoorNone
		}
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT sealed FROM group_files WHERE group_id = $1 AND message_id = $2 AND kind = 'link_image'`, g.ID, m.id).Scan(&sealed); err != nil {
			return err
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		data, err = peoplechats.Open(key, g.ID, groupLinkImageRow(m.id), sealed)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writePreviewImage(w, data)
}

// personPreviewImage is GET /v1/me/chats/{id}/previews/{message}: the
// person's own copy of the picture of a message's link preview, in their
// chat with a business.
func (s *Server) personPreviewImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		msg, err := uuid.Parse(chi.URLParam(r, "message"))
		if err != nil {
			return errDoorNone
		}
		return tx.QueryRow(ctx, `SELECT f.data FROM person_files f JOIN person_messages m ON m.id = f.message_id AND m.person_id = f.person_id
			WHERE f.person_id = $1 AND m.conversation_id = $2 AND f.message_id = $3 AND f.kind = 'link_image'`, person, c.ID, msg).Scan(&data)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writePreviewImage(w, data)
}

// customerPreview is a business's copy of a customer chat message's
// preview as kept: its words sealed under the customer's subject (they
// came from a message, maybe the customer's), its picture's place in the
// business's own storage beside it, as an attachment's path is.
type customerPreview struct {
	Sealed json.RawMessage     `json:"sealed"`
	Image  *links.PreviewImage `json:"image,omitempty"`
}

// sealCustomerPreview is the business's copy of a preview, to keep.
func (s *Server) sealCustomerPreview(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, p *links.Preview) ([]byte, error) {
	words := *p
	words.Image = nil
	env, err := pii.SealText(ctx, s.Store.PII, tx, tenant, subject, "other", string(links.StoredPreview(&words)))
	if err != nil {
		return nil, err
	}
	sealed, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"sealed": json.RawMessage(sealed)}
	if p.Image != nil {
		out["image"] = p.Image.Stored()
	}
	return json.Marshal(out)
}

// openCustomerPreview opens a business's copy of a preview; nil without
// one, or once its customer was erased.
func (s *Server) openCustomerPreview(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, raw []byte) (*links.Preview, error) {
	if raw == nil {
		return nil, nil
	}
	var kept customerPreview
	if err := json.Unmarshal(raw, &kept); err != nil {
		return nil, err
	}
	words, err := s.openCustomerText(ctx, tx, tenant, kept.Sealed)
	if err != nil || words == "" {
		return nil, err
	}
	var p links.Preview
	if err := json.Unmarshal([]byte(words), &p); err != nil {
		return nil, err
	}
	p.Image = kept.Image
	return &p, nil
}

// customerPreviewImage is GET /v1/customer-chats/{id}/previews/{message}
// (?size=small): the business's own copy of the picture of a message's
// link preview.
func (s *Server) customerPreviewImage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var img links.PreviewImage
	err := s.tx(r, func(tx pgx.Tx) error {
		t, err := s.customerThreadIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		msg, err := uuid.Parse(chi.URLParam(r, "message"))
		if err != nil {
			return pgx.ErrNoRows
		}
		var raw []byte
		if err := tx.QueryRow(r.Context(), `SELECT preview->'image' FROM customer_messages
			WHERE tenant_id = $1 AND conversation_id = ANY ($2) AND id = $3 AND preview ? 'image'`, p.TenantID, t.ids(), msg).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &img)
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	if s.Media == nil || img.Path == "" {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	path := img.Path
	if r.URL.Query().Get("size") == "small" && img.Small != "" {
		path = img.Small
	}
	data, _, err := s.Media.GetFile(r.Context(), p.TenantID, path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePreviewImage(w, data)
}

// previewArrived makes the preview of a link in a customer's message that
// came in from WhatsApp, as if it were sent in Taskiem, and keeps it on
// the business's copy (it shows when the chat is next read). The customer
// counts against the same ten pages a minute as anyone.
func (s *Server) previewArrived(tenant, chat, msg uuid.UUID, subject, number, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*previewTimeout)
	defer cancel()
	made := s.makePreview(ctx, "t:"+tenant.String(), "whatsapp:"+number, tenant.String(), previewURL(text, s.previewAnyPort))
	if made == nil {
		return
	}
	pv := s.keepBusinessPreview(ctx, tenant, made)
	kept := false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		raw, err := s.sealCustomerPreview(ctx, tx, tenant, subject, pv)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE customer_messages SET preview = $4 WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3 AND preview IS NULL`,
			tenant, chat, msg, raw)
		kept = err == nil && tag.RowsAffected() == 1
		return err
	})
	if err != nil {
		s.Logger.Warn("link preview: a customer's message's preview not kept", "tenant", tenant, "err", err)
	}
	if !kept {
		s.dropBusinessImage(ctx, tenant, pv)
	}
}
