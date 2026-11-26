package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Editing a sent message (docs/spec/chat-previews-and-editing.md §3): one
// PATCH per chat, {text, edits}, by the person who sent it, within 30
// minutes. Every copy changes in one transaction, with what depends on the
// words (mentions, search, the link's preview); the live streams and the
// app's sync carry it as a changed message, with edited_at. Business chats
// keep the earlier wording for the business's audit log only.
//
// Answers: 200 with the message; 409 {stale: true} when edits is behind
// (edited since on another device); 403 {not_editable: reason}; 422 for
// words the rules refuse.

// notEditableWords are the refusals' plain words, by reason.
var notEditableWords = map[string]string{
	links.NotYours:     "Only the person who sent a message can edit it.",
	links.TooOld:       "This message can no longer be edited: messages can be edited for 30 minutes after they are sent.",
	links.NotCard:      "Cards cannot be edited.",
	links.NotMoney:     "A message about money cannot be edited.",
	links.TaskiemActed: "Taskiem acted on this message, so its words are kept as they were.",
	links.NotDeleted:   "This message was deleted.",
	links.NotForwarded: "A forwarded message cannot be edited.",
	links.NotVoice:     "A voice note cannot be edited.",
	notElsewhere:       "This message was sent on WhatsApp, so it cannot be edited here.",
}

// notElsewhere: a person's message to a business sent on WhatsApp (shown
// in the app as theirs), which Taskiem cannot change there.
const notElsewhere = "elsewhere"

// editIn is an edit's body: the new words, the count of edits the client
// last saw, and (where the chat has them) the mentions now.
type editIn struct {
	Text     string          `json:"text"`
	Edits    *int            `json:"edits"`
	Mentions json.RawMessage `json:"mentions"`
}

func (in editIn) mentionsSet() bool {
	return len(in.Mentions) > 0 && string(in.Mentions) != "null"
}

// errEditStale is an edit behind the message's count of edits.
var errEditStale = errors.New("edited since")

// editWordsErr is new words the rules refuse (422).
type editWordsErr struct{ msg string }

func (e editWordsErr) Error() string { return e.msg }

// readEdit reads an edit's body, or answers 400 and says false.
func readEdit(w http.ResponseWriter, r *http.Request, s *Server) (editIn, bool) {
	var in editIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return in, false
	}
	if in.Edits == nil || *in.Edits < 0 {
		writeErr(w, http.StatusBadRequest, "edits is the count of edits the message had when it was opened (0 for none)")
		return in, false
	}
	in.Text = strings.TrimSpace(in.Text)
	return in, true
}

// editFailed answers an edit's refusal: 403 not_editable, 409 stale or
// 422; false for any other error, which the caller answers.
func editFailed(w http.ResponseWriter, err error) bool {
	var ne *links.NotEditableError
	var we editWordsErr
	switch {
	case errors.As(err, &ne):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": notEditableWords[ne.Reason], "not_editable": ne.Reason})
	case errors.Is(err, errEditStale), errors.Is(err, links.ErrStale):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This message was edited on another device. Its newer words are shown.", "stale": true})
	case errors.As(err, &we):
		writeErr(w, http.StatusUnprocessableEntity, we.msg)
	case errors.Is(err, links.ErrInvalid):
		writeErr(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "))
	default:
		return false
	}
	return true
}

func notEditable(reason string) error { return &links.NotEditableError{Reason: reason} }

// editWords checks new words as a new message's: not empty (unless a
// caption), at most 4,000 characters.
func editWords(text string, caption bool) error {
	if text == "" && !caption {
		return editWordsErr{"A message says something: write the new words, or delete it."}
	}
	if len([]rune(text)) > links.MaxMessage {
		return editWordsErr{fmt.Sprintf("A message is up to %d characters.", links.MaxMessage)}
	}
	return nil
}

// --- business to business ---

// editLinkMessage is PATCH /v1/link-chats/{link}/messages/{id} {text,
// edits, mentions?}: the member who sent it changes its words on both
// businesses' copies; the earlier wording goes to the audit log only. The
// people on the other side who were told on their phones see it changed
// there (Telegram, Slack) or get "✏️ Edited" (WhatsApp).
func (s *Server) editLinkMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pair, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	in, ok := readEdit(w, r, s)
	if !ok {
		return
	}
	p := principalFrom(ctx)
	o := links.EditOpts{Editor: p.Actor(), Edits: *in.Edits, Now: s.now()}
	var before string
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(ctx, tx, link); err != nil {
			return err
		}
		why, words, err := links.EditableTx(ctx, tx, p.TenantID, link, pair, o.Editor, o.Now)
		if err != nil {
			return err
		}
		if why != "" {
			return notEditable(why)
		}
		before = words
		if in.mentionsSet() {
			var refs []mentionRef
			if err := json.Unmarshal(in.Mentions, &refs); err != nil {
				return editWordsErr{"mentions is a list of mentions"}
			}
			for _, m := range refs {
				if m.Kind != "business" {
					return editWordsErr{"A # tag of a price, an invoice or an order cannot be added by editing; send a new message."}
				}
			}
			ments, err := checkMentions(ctx, tx, p, link, in.Text, refs)
			if err != nil {
				return err
			}
			o.Mentions, o.MentionsSet = ments, true
		}
		return nil
	}); err != nil {
		if !editFailed(w, err) {
			s.fail(w, r, linkMessageErr(err))
		}
		return
	}
	// The preview is made again only when the first link changed.
	var kept []func()
	if previewURL(in.Text, s.previewAnyPort) != previewURL(before, s.previewAnyPort) {
		o.NewPreview = true
		if previewURL(in.Text, s.previewAnyPort) != "" {
			if made := s.previewForSend(ctx, p, "link:"+link.String(), "t:"+p.TenantID.String(), p.TenantID.String(), in.Text, previewChoice{}); made != nil {
				o.Preview = s.keepBusinessPreview(ctx, p.TenantID, made)
				ours := o.Preview
				kept = append(kept, func() { s.dropBusinessImage(ctx, p.TenantID, ours) })
				if made.img != nil && s.Media != nil {
					o.TheirImage = s.theirPreviewImage(made.img, &kept)
				}
			}
		}
	}
	var ed links.Edited
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		ed, err = links.EditTx(ctx, tx, p.TenantID, link, pair, in.Text, o, actorOf(p))
		return err
	})
	if err != nil {
		for _, undo := range kept {
			undo()
		}
		if !editFailed(w, err) {
			s.fail(w, r, linkMessageErr(err))
		}
		return
	}
	// The pictures of the previews it replaced, on both sides.
	s.dropBusinessImage(ctx, p.TenantID, ed.OldOurs)
	s.dropBusinessImage(ctx, ed.Other, ed.OldTheirs)
	s.tellLinkEdit(ctx, ed.Other, link, ed.Message)
	writeJSON(w, http.StatusOK, linkMessageOut{Message: ed.Message})
}

// linkMessageEdits is GET /v1/link-chats/{link}/messages/{id}/edits: the
// earlier wordings of one of the business's own messages, who changed it
// and when, for those who read the audit log (§3.1: never shown in the
// chat). The other business keeps none of them.
func (s *Server) linkMessageEdits(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pair, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var out []links.MessageEdit
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		var err error
		out, err = links.EditsTx(r.Context(), tx, p.TenantID, pair)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edits": out})
}

// chatEditor is a chat channel that changes a message it sent in place
// (Telegram's editMessageText, Slack's chat.update).
type chatEditor interface {
	Edit(ctx context.Context, addr, id string, m whatsapp.Message) error
}

// phoneCopy is one copy of a message told to a phone: the channel, the
// address and the channel's id of the message.
type phoneCopy struct{ channel, addr, id string }

// editOnPhone changes one phone's copy of an edited message: in place where
// the channel can (edited), else, or when that fails (too old, deleted
// there), the "✏️ Edited" follow-up, quoting the original on WhatsApp. It
// says whether it was changed in place.
func editOnPhone(ctx context.Context, ch chatChannel, c phoneCopy, edited, followUp whatsapp.Message) (bool, error) {
	if ed, ok := ch.(chatEditor); ok && c.channel != "whatsapp" {
		if err := ed.Edit(ctx, c.addr, c.id, edited); err == nil {
			return true, nil
		}
	}
	if c.channel == "whatsapp" {
		followUp.ReplyTo = c.id
	}
	_, err := ch.Send(ctx, c.addr, followUp)
	return false, err
}

// tellLinkEdit changes, on the phones of the other side's people who were
// told of a message, what they were told: after the edit is kept, and
// never undoing it when a phone cannot be reached.
func (s *Server) tellLinkEdit(ctx context.Context, to, link uuid.UUID, m links.Message) {
	s.background(ctx, "link message edit", func(ctx context.Context) {
		var copies []phoneCopy
		var from string
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{to}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT channel, address_hash, message_id FROM link_chat_sent WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3`, to, link, m.PairID)
			if err != nil {
				return err
			}
			type sent struct {
				channel, id string
				hash        []byte
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sent, error) {
				var x sent
				return x, r.Scan(&x.channel, &x.hash, &x.id)
			})
			if err != nil || len(list) == 0 {
				return err
			}
			l, err := links.GetTx(ctx, tx, link)
			if err != nil {
				return err
			}
			from = l.Name
			// The addresses are kept by their keyed hash: those of the
			// people who may be told now, matched to them.
			users, err := holdersTx(ctx, tx, PermChatTrade)
			if err != nil {
				return err
			}
			for _, channel := range []string{"whatsapp", "telegram", "slack"} {
				addrs, err := chatAddrsTx(ctx, tx, channel, users)
				if err != nil {
					return err
				}
				for _, a := range addrs {
					h := s.chatAddrHash(to, channel, a)
					for _, x := range list {
						if x.channel == channel && string(x.hash) == string(h) &&
							!slices.ContainsFunc(copies, func(c phoneCopy) bool { return c.channel == channel && c.addr == a && c.id == x.id }) {
							copies = append(copies, phoneCopy{channel: channel, addr: a, id: x.id})
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			s.Logger.Warn("links: finding an edited message's phones", "tenant", to, "err", err)
			return
		}
		for _, c := range copies {
			ch, err := s.chatChannelFor(ctx, c.channel, to)
			if err != nil {
				continue
			}
			l := s.chooseLang(ctx, to, c.addr, "").Tag
			title, body := "["+whatsapp.SafeText(from)+"]", whatsapp.SafeText(linkMessageLine(l, m, true))
			edited := linkPhoneMessage(to, ch.Prefix(""), title, body, nil)
			follow := linkPhoneMessage(to, ch.Prefix(""), title, editedLine(l, body), nil)
			if _, err := editOnPhone(ctx, ch, c, edited, follow); err != nil {
				s.Logger.Warn("links: an edited message on a phone", "tenant", to, "channel", c.channel, "err", err)
			}
		}
	})
}

// editedLine is the follow-up's words: "✏️ Edited: *new words*".
func editedLine(l lang.Tag, words string) string {
	words = strings.TrimSpace(words)
	if words != "" && !strings.ContainsAny(words, "*\n") {
		words = "*" + words + "*"
	}
	return tr(l, "chat.link.edited", "words", words)
}

// --- person to person ---

// editPeopleMessage is PATCH /v1/me/people/{id}/messages/{message} {text,
// edits}: the sender's words (or a caption) changed on both copies, each
// sealed again under its owner's key, with the search tokens and the link's
// preview. No audit entry and no notification.
func (s *Server) editPeopleMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	in, ok := readEdit(w, r, s)
	if !ok {
		return
	}
	chatID, msgID := chi.URLParam(r, "id"), chi.URLParam(r, "message")
	// The rules and the words before, outside the edit's transaction: a new
	// preview is fetched from the web before it.
	var before string
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chatID, false)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, msgID)
		if err != nil {
			return err
		}
		if err := s.peopleEditable(m, in); err != nil {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		v, _ := openSealed(key, person, m.ID, m.sealed)
		before = v.Body
		return nil
	})
	if err != nil {
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	pv, changed := s.editPreview(ctx, "people:"+chatID, person, before, in.Text)
	var out peopleMessage
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chatID, true)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, msgID)
		if err != nil {
			return err
		}
		if err := s.peopleEditable(m, in); err != nil {
			return err
		}
		keys := s.keys()
		myKey, err := keys.KeyTx(ctx, tx, person, true)
		if err != nil {
			return err
		}
		if err := s.resealPeopleTx(ctx, tx, myKey, person, c.Conversation, m.ID, in.Text, pv, changed); err != nil {
			return err
		}
		// The other's copy, when it reached them and they take changes now;
		// nothing crosses their block of the person.
		if peer, ok, err := reachIfDeliveredTx(ctx, tx, c, m); err != nil {
			return err
		} else if ok {
			theyBlocked, err := theyBlockedTx(ctx, tx, peer, person)
			if err != nil {
				return err
			}
			if !theyBlocked {
				peerKey, err := keys.KeyTx(ctx, tx, peer, true)
				if err != nil {
					return err
				}
				if err := s.resealPeopleTx(ctx, tx, peerKey, peer, c.Conversation, m.ID, in.Text, pv, changed); err != nil {
					return err
				}
			}
		}
		row, err := ownMessageTx(ctx, tx, person, c, msgID)
		if err != nil {
			return err
		}
		out, _ = row.view(myKey, person)
		return nil
	})
	if err != nil {
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// peopleEditable is why a people chat's message cannot be edited by its
// copy's owner now, with the edit's count checked.
func (s *Server) peopleEditable(m peopleRow, in editIn) error {
	kind, _ := m.attach["kind"].(string)
	switch {
	case m.Deleted:
		return notEditable(links.NotDeleted)
	case m.Kind != "text" && m.Kind != "media":
		return notEditable(links.NotCard)
	case !m.Mine:
		return notEditable(links.NotYours)
	case kind == "voice":
		return notEditable(links.NotVoice)
	case m.forwards > 0:
		return notEditable(links.NotForwarded)
	case s.now().Sub(m.CreatedAt) > links.EditWindow:
		return notEditable(links.TooOld)
	case *in.Edits < m.Edits:
		return errEditStale
	}
	return editWords(in.Text, m.Kind == "media")
}

// editPreview is the link preview an edit's words carry, made when the
// first link changed (changed); nil with changed means none.
func (s *Server) editPreview(ctx context.Context, chat string, person uuid.UUID, before, text string) (*madePreview, bool) {
	now := previewURL(text, s.previewAnyPort)
	if now == previewURL(before, s.previewAnyPort) {
		return nil, false
	}
	if now == "" {
		return nil, true
	}
	return s.previewForSend(ctx, principalFrom(ctx), chat, "p:"+person.String(), person.String(), text, previewChoice{}), true
}

// resealPeopleTx changes one copy of a people chat's message: its words
// (and preview when changed) sealed again under its owner's key, its
// search tokens made again, and its link picture replaced.
func (s *Server) resealPeopleTx(ctx context.Context, tx pgx.Tx, key []byte, owner, conv, id uuid.UUID, text string, pv *madePreview, changed bool) error {
	var sealed []byte
	err := tx.QueryRow(ctx, `SELECT sealed FROM people_messages WHERE person_id = $1 AND conversation_id = $2 AND id = $3 AND deleted_at IS NULL FOR UPDATE`, owner, conv, id).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // not in this copy (deleted for them)
	}
	if err != nil {
		return err
	}
	v, err := openSealed(key, owner, id, sealed)
	if err != nil {
		return err
	}
	v.Body = text
	if changed {
		v.Preview = pv.view()
	}
	if sealed, err = sealFor(key, owner, messageRow(id), v); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE people_messages SET sealed = $3, edited_at = now(), edits = edits + 1 WHERE person_id = $1 AND id = $2`, owner, id, sealed); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM people_search WHERE person_id = $1 AND message_id = $2`, owner, id); err != nil {
		return err
	}
	if err := indexPeopleMessageTx(ctx, tx, key, owner, conv, id, v); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM people_files WHERE person_id = $1 AND message_id = $2 AND kind = 'link_image'`, owner, id); err != nil {
		return err
	}
	if pv == nil || pv.img == nil {
		return nil
	}
	data, err := peoplechats.Seal(key, owner, linkImageRow(id), pv.img.Big)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO people_files (person_id, message_id, conversation_id, kind, type, size, sealed) VALUES ($1, $2, $3, 'link_image', $4, $5, $6)`,
		owner, id, conv, pv.img.Type, len(pv.img.Big), data)
	return err
}

// --- groups ---

// editGroupMessage is PATCH /v1/me/groups/{id}/messages/{message} {text,
// edits, mentions?}: the sender, while in the group, changes its words
// (sealed again under the group's key) and its @ mentions; the search
// tokens and the link's preview follow. No audit entry, no notification.
func (s *Server) editGroupMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	in, ok := readEdit(w, r, s)
	if !ok {
		return
	}
	var mentions []uuid.UUID
	if in.mentionsSet() {
		if err := json.Unmarshal(in.Mentions, &mentions); err != nil || len(mentions) > groupMax {
			writeErr(w, http.StatusBadRequest, "mentions are up to 256 people, by id")
			return
		}
	}
	groupID, msgID := chi.URLParam(r, "id"), chi.URLParam(r, "message")
	var before string
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, groupID, false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, msgID)
		if err != nil {
			return err
		}
		if err := s.groupEditable(person, g, m, in); err != nil {
			return err
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		v, _ := openGroupSealed(key, g.ID, m.id, m.sealed)
		before = v.Body
		return nil
	})
	if err != nil {
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	pv, changed := s.editPreview(ctx, "group:"+groupID, person, before, in.Text)
	var out groupMessage
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, groupID, true)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, msgID)
		if err != nil {
			return err
		}
		if err := s.groupEditable(person, g, m, in); err != nil {
			return err
		}
		tagged := m.mentions
		if in.mentionsSet() {
			// Only @ mentions, of members now in the group, as a send's.
			rows, err := tx.Query(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND left_at IS NULL AND person_id = ANY ($2) AND person_id <> $3 ORDER BY person_id`,
				g.ID, mentions, person)
			if err != nil {
				return err
			}
			if tagged, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
				return err
			}
		}
		if tagged == nil {
			tagged = []uuid.UUID{}
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		v, err := openGroupSealed(key, g.ID, m.id, m.sealed)
		if err != nil {
			return err
		}
		v.Body = in.Text
		if changed {
			v.Preview = pv.view()
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		sealed, err := peoplechats.Seal(key, g.ID, groupMsgKey(m.id), raw)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE group_messages SET sealed = $3, mentions = $4, edited_at = now(), edits = edits + 1 WHERE group_id = $1 AND id = $2 AND deleted_at IS NULL`,
			g.ID, m.id, sealed, tagged)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notEditable(links.NotYours) // no longer in the group
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_search WHERE group_id = $1 AND message_id = $2`, g.ID, m.id); err != nil {
			return err
		}
		if toks := peoplechats.Tokens(peoplechats.GroupSearchKey(key, g.ID), searchTexts(v)...); len(toks) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO group_search (group_id, message_id, token) SELECT $1, $2, unnest($3::bytea[]) ON CONFLICT DO NOTHING`, g.ID, m.id, toks); err != nil {
				return err
			}
		}
		if changed {
			if _, err := tx.Exec(ctx, `DELETE FROM group_files WHERE group_id = $1 AND message_id = $2 AND kind = 'link_image'`, g.ID, m.id); err != nil {
				return err
			}
			if pv != nil && pv.img != nil {
				data, err := peoplechats.Seal(key, g.ID, groupLinkImageRow(m.id), pv.img.Big)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO group_files (group_id, message_id, kind, type, size, sealed) VALUES ($1, $2, 'link_image', $3, $4, $5)`,
					g.ID, m.id, pv.img.Type, len(pv.img.Big), data); err != nil {
					return err
				}
			}
		}
		out, err = s.groupOneMessageTx(ctx, tx, person, g.ID, m.id)
		return err
	})
	if err != nil {
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// groupEditable is why the person may not edit a group's message now.
func (s *Server) groupEditable(person uuid.UUID, g groupRow, m groupMsgRow, in editIn) error {
	kind, _ := m.attach["kind"].(string)
	switch {
	case m.deletedAt != nil:
		return notEditable(links.NotDeleted)
	case m.kind != "text" && m.kind != "media" && m.kind != "event":
		return notEditable(links.NotCard)
	case m.sender == nil || *m.sender != person || m.kind == "event" || g.LeftAt != nil:
		return notEditable(links.NotYours)
	case kind == "voice":
		return notEditable(links.NotVoice)
	case m.forwards > 0:
		return notEditable(links.NotForwarded)
	case s.now().Sub(m.createdAt) > links.EditWindow:
		return notEditable(links.TooOld)
	case *in.Edits < m.edits:
		return errEditStale
	}
	return editWords(in.Text, m.kind == "media")
}

// --- a person and a business ---

// editPersonMessage is PATCH /v1/me/chats/{id}/messages/{message} {text,
// edits}: the person changes their message to a business, on their copy
// and, through the door (customer.chat, audited in the business's chain
// with the edit marked), the business's. A message Taskiem read an order
// from keeps its words.
func (s *Server) editPersonMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	in, ok := readEdit(w, r, s)
	if !ok {
		return
	}
	chatID := chi.URLParam(r, "id")
	msgID, err := uuid.Parse(chi.URLParam(r, "message"))
	if err != nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	var before string
	var business uuid.UUID
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chatID)
		if err != nil {
			return err
		}
		business = c.Business
		before, err = s.personEditableTx(ctx, tx, person, c.ID, msgID, in, false)
		return err
	})
	if err != nil {
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	pv, changed := s.editPreview(ctx, "door:"+chatID, person, before, in.Text)
	var bizPreview *links.Preview
	if changed && pv != nil {
		bizPreview = s.keepBusinessPreview(ctx, business, pv)
	}
	var out personMessage
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chatID)
		if err != nil {
			return err
		}
		if c.Blocked {
			return errDoorYouBlocked
		}
		if _, err := s.personEditableTx(ctx, tx, person, c.ID, msgID, in, true); err != nil {
			return err
		}
		if err := enterTx(ctx, tx, person, c.Business, "customer.chat", map[string]any{"message": msgID, "edit": true}); err != nil {
			return err
		}
		// The business's copy: Taskiem may have read an order from it.
		var acted bool
		var subject string
		err = tx.QueryRow(ctx, `SELECT m.acted_at IS NOT NULL OR EXISTS (SELECT 1 FROM order_drafts d WHERE d.tenant_id = m.tenant_id AND d.message_id = m.id), c.subject_id
			FROM customer_messages m JOIN customer_conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
			WHERE m.tenant_id = $1 AND m.id = $2 AND c.person_id = $3 FOR UPDATE OF m`, c.Business, msgID, person).Scan(&acted, &subject)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDoorNone
		}
		if err != nil {
			return err
		}
		if acted {
			return notEditable(links.TaskiemActed)
		}
		body, err := pii.SealText(ctx, s.Store.PII, tx, c.Business, subject, "other", in.Text)
		if errors.Is(err, pii.ErrErased) {
			return errPersonErased
		}
		if err != nil {
			return err
		}
		setBiz, bizArgs := `body = $3`, []any{c.Business, msgID, body}
		if changed {
			var sealed []byte
			if bizPreview != nil {
				if sealed, err = s.sealCustomerPreview(ctx, tx, c.Business, subject, bizPreview); err != nil {
					return err
				}
			}
			setBiz += `, preview = $4`
			bizArgs = append(bizArgs, sealed)
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_messages SET `+setBiz+`, edited_at = now(), edits = edits + 1 WHERE tenant_id = $1 AND id = $2`, bizArgs...); err != nil {
			return err
		}
		if err := s.editPersonCopyTx(ctx, tx, person, c.Business, msgID, in.Text, pv, changed); err != nil {
			return err
		}
		out, err = personMessageTx(ctx, tx, person, c.ID, msgID)
		return err
	})
	if err != nil {
		if bizPreview != nil {
			s.dropBusinessImage(ctx, business, bizPreview)
		}
		if !editFailed(w, err) {
			s.personFail(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// personEditableTx checks the rules on the person's copy of their message
// to a business, and answers its words.
func (s *Server) personEditableTx(ctx context.Context, tx pgx.Tx, person, conv, id uuid.UUID, in editIn, lock bool) (string, error) {
	var mine bool
	var kind, line, file, body string
	var card bool
	var at = s.now()
	var edits int
	q := `SELECT mine, kind, line, COALESCE(attachment ->> 'kind', ''), card IS NOT NULL, body, created_at, edits FROM person_messages
		WHERE person_id = $1 AND conversation_id = $2 AND id = $3`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, person, conv, id).Scan(&mine, &kind, &line, &file, &card, &body, &at, &edits)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errDoorNone
	}
	if err != nil {
		return "", err
	}
	switch {
	case card || (kind != "text" && kind != "media"):
		return "", notEditable(links.NotCard)
	case !mine:
		return "", notEditable(links.NotYours)
	case line == lineWhatsApp:
		return "", notEditable(notElsewhere)
	case file == "voice":
		return "", notEditable(links.NotVoice)
	case s.now().Sub(at) > links.EditWindow:
		return "", notEditable(links.TooOld)
	case *in.Edits < edits:
		return "", errEditStale
	}
	return body, editWords(in.Text, kind == "media")
}

// editPersonCopyTx changes the person's copy of a message in a chat with a
// business: its words, and its preview and picture when changed.
func (s *Server) editPersonCopyTx(ctx context.Context, tx pgx.Tx, person, tenant, id uuid.UUID, text string, pv *madePreview, changed bool) error {
	set, args := `body = $3`, []any{person, id, text}
	if changed {
		set += `, preview = $4`
		args = append(args, links.StoredPreview(pv.view()))
	}
	if _, err := tx.Exec(ctx, `UPDATE person_messages SET `+set+`, edited_at = now(), edits = edits + 1 WHERE person_id = $1 AND id = $2`, args...); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM person_files WHERE person_id = $1 AND message_id = $2 AND kind = 'link_image'`, person, id); err != nil {
		return err
	}
	if pv == nil || pv.img == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO person_files (id, person_id, tenant_id, message_id, kind, type, size, data) VALUES ($1, $2, $3, $4, 'link_image', $5, $6, $7)`,
		uuid.Must(uuid.NewV7()), person, tenant, id, pv.img.Type, len(pv.img.Big), pv.img.Big)
	return err
}

// personMessageTx is one message of the person's copy of a chat with a
// business, as personMessages shows it.
func personMessageTx(ctx context.Context, tx pgx.Tx, person, conv, id uuid.UUID) (personMessage, error) {
	var m personMessage
	err := tx.QueryRow(ctx, `SELECT id, mine, kind, body, sender_name, card, attachment, offer, read_at, created_at, line, preview, edited_at, edits FROM person_messages
		WHERE person_id = $1 AND conversation_id = $2 AND id = $3`, person, conv, id).
		Scan(&m.ID, &m.Mine, &m.Kind, &m.Body, &m.SenderName, &m.Card, &m.Attachment, &m.Offer, &m.ReadAt, &m.CreatedAt, &m.Line, &m.Preview, &m.EditedAt, &m.Edits)
	m.Card = maskCustomerCard(m.Mine, m.Card)
	return m, err
}

// editCustomerMessage is PATCH /v1/customer-chats/{id}/messages/{message}
// {text, edits}: the member who wrote a reply changes it; the business's
// audit log keeps the change (customer_chat.message.edit). In the app the
// customer's copy changes through the way back; on WhatsApp, where a
// message cannot be changed, the customer is sent "✏️ Edited: …".
func (s *Server) editCustomerMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	in, ok := readEdit(w, r, s)
	if !ok {
		return
	}
	threadID := chi.URLParam(r, "id")
	msgID, err := uuid.Parse(chi.URLParam(r, "message"))
	if err != nil {
		s.customerChatFail(w, r, errCustomerChatNone)
		return
	}
	var before string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		t, err := s.customerThreadIn(ctx, tx, p.TenantID, threadID)
		if err != nil {
			return err
		}
		c, err := s.customerEditableTx(ctx, tx, p, t, msgID, in, false)
		before = c.body
		return err
	})
	if err != nil {
		if !editFailed(w, err) {
			s.customerSendFail(w, r, err)
		}
		return
	}
	var pv *madePreview
	changed := previewURL(in.Text, s.previewAnyPort) != previewURL(before, s.previewAnyPort)
	if changed && previewURL(in.Text, s.previewAnyPort) != "" {
		pv = s.previewForSend(ctx, p, "customer:"+threadID, "t:"+p.TenantID.String(), p.TenantID.String(), in.Text, previewChoice{})
	}
	var bizPreview *links.Preview
	var out customerMessageView
	var waLine bool
	var person uuid.UUID
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		t, err := s.customerThreadIn(ctx, tx, p.TenantID, threadID)
		if err != nil {
			return err
		}
		c, err := s.customerEditableTx(ctx, tx, p, t, msgID, in, true)
		if err != nil {
			return err
		}
		waLine = c.line == lineWhatsApp
		body, err := pii.SealText(ctx, s.Store.PII, tx, p.TenantID, c.subject, "other", in.Text)
		if errors.Is(err, pii.ErrErased) {
			return errCustomerErased
		}
		if err != nil {
			return err
		}
		// WhatsApp draws its own previews: a card is kept in the app only.
		set, args := `body = $3`, []any{p.TenantID, msgID, body}
		if changed && !waLine {
			var sealed []byte
			if pv != nil {
				bizPreview = s.keepBusinessPreview(ctx, p.TenantID, pv)
				if sealed, err = s.sealCustomerPreview(ctx, tx, p.TenantID, c.subject, bizPreview); err != nil {
					return err
				}
			}
			set += `, preview = $4`
			args = append(args, sealed)
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_messages SET `+set+`, edited_at = now(), edits = edits + 1 WHERE tenant_id = $1 AND id = $2`, args...); err != nil {
			return err
		}
		detail := map[string]any{"message": msgID, "actor": p.Actor(), "actor_type": p.ActorType()}
		if waLine {
			if err := auditTx(r, tx, "customer_chat.message.edit", c.conv.String(), map[string]any{"message": msgID}); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(ctx, `SELECT person_id FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, p.TenantID, c.conv).Scan(&person); err != nil {
				return err
			}
			if err := reachTx(ctx, tx, p.TenantID, person, "customer_chat.message.edit", detail); err != nil {
				return err
			}
			if err := s.editPersonCopyTx(ctx, tx, person, p.TenantID, msgID, in.Text, pv, changed); err != nil {
				return err
			}
		}
		out, err = s.customerMessageByID(ctx, tx, p.TenantID, msgID, c.line)
		return err
	})
	if err != nil {
		if bizPreview != nil {
			s.dropBusinessImage(ctx, p.TenantID, bizPreview)
		}
		if !editFailed(w, err) {
			s.customerSendFail(w, r, err)
		}
		return
	}
	if waLine {
		// The customer read it on WhatsApp: the follow-up, as a reply of the
		// business's, on the same line.
		s.background(ctx, "customer message edit", func(ctx context.Context) {
			words := editedLine(lang.EN, in.Text)
			if _, _, err := s.sendCustomer(ctx, p.TenantID, threadID, customerSend{Text: words, SenderID: p.Actor(), SenderName: out.SenderName, Line: lineWhatsApp}); err != nil {
				s.Logger.Warn("customer chat: an edit's follow-up on WhatsApp", "tenant", p.TenantID, "err", err)
			}
		})
	}
	m := out
	m.Card = maskCustomerCard(m.Mine, m.Card)
	writeJSON(w, http.StatusOK, m)
}

// customerEdit is what the rules read of a business's message to a
// customer.
type customerEdit struct {
	conv          uuid.UUID
	line, subject string
	body          string
}

// customerEditableTx checks the rules on a business's message in a
// customer's thread: a member's own reply, within the window, not a card,
// an offer, a voice note, nor one Taskiem acted on.
func (s *Server) customerEditableTx(ctx context.Context, tx pgx.Tx, p *Principal, t customerThread, id uuid.UUID, in editIn, lock bool) (customerEdit, error) {
	var out customerEdit
	var mine, card, offer, acted bool
	var kind, sender, file string
	var raw []byte
	var edits int
	at := s.now()
	q := `SELECT m.conversation_id, m.mine, m.kind, COALESCE(m.sender_id, ''), COALESCE(m.attachment ->> 'kind', ''), m.card IS NOT NULL, m.offer,
		m.acted_at IS NOT NULL OR EXISTS (SELECT 1 FROM order_drafts d WHERE d.tenant_id = m.tenant_id AND d.message_id = m.id), m.body, m.created_at, m.edits
		FROM customer_messages m WHERE m.tenant_id = $1 AND m.conversation_id = ANY ($2) AND m.id = $3`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, p.TenantID, t.ids(), id).Scan(&out.conv, &mine, &kind, &sender, &file, &card, &offer, &acted, &raw, &at, &edits)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errCustomerChatNone
	}
	if err != nil {
		return out, err
	}
	out.line = t.lineOfRow()[out.conv]
	if row := t.row(out.line); row != nil {
		out.subject = row.subject
	}
	switch {
	case card || offer || (kind != "text" && kind != "media"):
		return out, notEditable(links.NotCard)
	case !mine || sender == "" || sender != p.Actor():
		return out, notEditable(links.NotYours)
	case file == "voice":
		return out, notEditable(links.NotVoice)
	case acted:
		return out, notEditable(links.TaskiemActed)
	case s.now().Sub(at) > links.EditWindow:
		return out, notEditable(links.TooOld)
	case *in.Edits < edits:
		return out, errEditStale
	}
	if out.body, err = s.openCustomerText(ctx, tx, p.TenantID, raw); err != nil {
		return out, err
	}
	return out, editWords(in.Text, kind == "media")
}

// customerMessageByID is one message of a customer's thread as the
// business's page shows it.
func (s *Server) customerMessageByID(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, line string) (customerMessageView, error) {
	var m customerMessageView
	var body, att, pv []byte
	err := tx.QueryRow(ctx, `SELECT id, mine, kind, body, sender_name, COALESCE(sent_as, ''), COALESCE(delivery, ''), COALESCE(failure, ''), read_at, created_at,
		card, attachment, offer, preview, edited_at, edits FROM customer_messages WHERE tenant_id = $1 AND id = $2`, tenant, id).
		Scan(&m.ID, &m.Mine, &m.Kind, &body, &m.SenderName, &m.SentAs, &m.Delivery, &m.Failure, &m.ReadAt, &m.CreatedAt, &m.Card, &att, &m.Offer, &pv, &m.EditedAt, &m.Edits)
	if err != nil {
		return m, err
	}
	m.Line = line
	if m.Body, err = s.openCustomerText(ctx, tx, tenant, body); err != nil {
		return m, err
	}
	if m.Attachment, err = s.openAttachment(ctx, tx, tenant, att); err != nil {
		return m, err
	}
	if m.Preview, err = s.openCustomerPreview(ctx, tx, tenant, pv); err != nil {
		return m, err
	}
	return m, s.openCustomerCard(ctx, tx, tenant, m.Card)
}
