package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Editing a sent message (docs/spec/chat-previews-and-editing.md §3): its
// sender, within EditWindow of sending, changes its words (or a file's
// caption) on both businesses' copies in one transaction. What it said
// before is kept for the sending business's audit log only
// (link_message_edits), never shown in the chat.

// EditWindow is how long after sending a message can be edited, by the
// server's clock.
const EditWindow = 30 * time.Minute

// Why a message cannot be edited (403 not_editable), the same in every chat.
const (
	NotYours     = "not_yours"
	TooOld       = "too_old"
	NotCard      = "card"
	NotMoney     = "money"
	TaskiemActed = "taskiem_acted"
	NotDeleted   = "deleted"
	NotForwarded = "forwarded"
	NotVoice     = "voice"
)

// NotEditableError is an edit the rules refuse, and why (one of the reasons above).
type NotEditableError struct{ Reason string }

func (e *NotEditableError) Error() string { return "links: not editable: " + e.Reason }

// ErrStale is an edit that names fewer edits than the message has had: it
// was edited since (on another device), and the newer words stand.
var ErrStale = errors.New("links: the message was edited since")

// moneyMentions are the # tags of a price, an invoice or an order: a
// message carrying one is not edited, and none is added by editing.
var moneyMentions = map[string]bool{"item": true, "service": true, "invoice": true, "bill": true, "order": true, "purchase": true}

// EditOpts is an edit: who makes it, the count of edits they saw, and what
// the words carry.
type EditOpts struct {
	// Editor is the member editing (their actor id): only the member who
	// sent it may.
	Editor string
	// Edits is the count of edits the editor last saw (ErrStale when behind).
	Edits int
	Now   time.Time
	// Mentions, with MentionsSet, are the message's references now (checked
	// by the caller); without it, those still named in the words are kept.
	Mentions    []Mention
	MentionsSet bool
	// NewPreview: the first link changed, so the preview is Preview (nil:
	// none); its picture is our copy and TheirImage makes the other's.
	// Otherwise the preview is kept as it was.
	NewPreview bool
	Preview    *Preview
	TheirImage func(ctx context.Context, other uuid.UUID) (*PreviewImage, error)
}

// Edited is an edit made: our copy of the message as it is now, the other
// business, the edit history's row, and the previews it replaced (whose
// pictures the caller removes once it commits).
type Edited struct {
	Message   Message
	Other     uuid.UUID
	EditID    uuid.UUID
	Before    string
	OldOurs   *Preview
	OldTheirs *Preview
}

// editRow is what the rules read of our copy of a message.
type editRow struct {
	id                         uuid.UUID
	mine, forwarded            bool
	kind, body, senderID, file string
	hidden, acted, paid        bool
	mentions                   []Mention
	preview                    *Preview
	createdAt                  time.Time
	edits                      int
}

// EditReason is why the rules refuse an edit of a message by editor now,
// or "" when it may be edited.
func (r editRow) reason(editor string, now time.Time) string {
	switch {
	case r.hidden:
		return NotDeleted
	case r.kind != KindText:
		return NotCard
	case !r.mine || r.senderID == "" || r.senderID != editor:
		return NotYours
	case r.paid:
		return NotMoney
	}
	for _, m := range r.mentions {
		if moneyMentions[m.Kind] {
			return NotMoney
		}
		if m.Kind == "saved" {
			return NotCard
		}
	}
	switch {
	case r.file == "voice":
		return NotVoice
	case r.forwarded:
		return NotForwarded
	case r.acted:
		return TaskiemActed
	case now.Sub(r.createdAt) > EditWindow:
		return TooOld
	}
	return ""
}

func editRowTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, lock bool) (editRow, error) {
	var r editRow
	var ments, prev []byte
	q := `SELECT id, mine, kind, body, COALESCE(sender_id, ''), COALESCE(attachment ->> 'kind', ''), forwarded, hidden_at IS NOT NULL, acted_at IS NOT NULL,
		payment_id IS NOT NULL OR batch_id IS NOT NULL, mentions, preview, created_at, edits
		FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, tenant, link, pair).Scan(&r.id, &r.mine, &r.kind, &r.body, &r.senderID, &r.file, &r.forwarded, &r.hidden, &r.acted,
		&r.paid, &ments, &prev, &r.createdAt, &r.edits)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("%w: no such message", ErrNotFound)
	}
	if err != nil {
		return r, err
	}
	if ments != nil {
		if err := json.Unmarshal(ments, &r.mentions); err != nil {
			return r, err
		}
	}
	if prev != nil {
		r.preview = &Preview{}
		if err := json.Unmarshal(prev, r.preview); err != nil {
			return r, err
		}
	}
	return r, nil
}

// EditableTx is why the business in scope's member editor may not edit a
// message now ("" when they may), and its words: what the API reads before
// it makes a new preview, outside the edit's transaction.
func EditableTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, editor string, now time.Time) (string, string, error) {
	r, err := editRowTx(ctx, tx, tenant, link, pair, false)
	if err != nil {
		return "", "", err
	}
	return r.reason(editor, now), r.body, nil
}

// EditTx edits a message the business in scope sent to a linked business:
// the words on both copies, the edit history on ours, through the door
// (link.message.edit, audited on both sides with the history's id). It is
// the caller's last write: after it the scope is the other side's.
func EditTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, text string, o EditOpts, by Actor) (Edited, error) {
	var out Edited
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return out, err
	}
	r, err := editRowTx(ctx, tx, tenant, link, pair, true)
	if err != nil {
		return out, err
	}
	if why := r.reason(o.Editor, o.Now); why != "" {
		return out, &NotEditableError{Reason: why}
	}
	if o.Edits < r.edits {
		return out, ErrStale
	}
	if l.State != Active {
		return out, fmt.Errorf("%w: you are no longer linked with %s", ErrState, l.Name)
	}
	text = strings.TrimSpace(text)
	if (text == "" && r.file == "") || len([]rune(text)) > MaxMessage {
		return out, fmt.Errorf("%w: a message says something, in up to %d characters", ErrInvalid, MaxMessage)
	}
	ments := o.Mentions
	if o.MentionsSet {
		for _, m := range ments {
			if moneyMentions[m.Kind] || m.Kind == "saved" {
				return out, fmt.Errorf("%w: a # tag of a price, an invoice or an order cannot be added by editing; send a new message", ErrInvalid)
			}
		}
	} else {
		lower := strings.ToLower(text)
		ments = nil
		for _, m := range r.mentions {
			if strings.Contains(lower, strings.ToLower(m.Name)) {
				ments = append(ments, m)
			}
		}
	}
	var ours, theirs []byte
	if len(ments) > 0 {
		ours, _ = json.Marshal(ments)
		other := make([]Mention, len(ments))
		for i, x := range ments {
			x.Link, x.Ref = uuid.Nil, ""
			other[i] = x
		}
		theirs, _ = json.Marshal(other)
	}
	out.Before = r.body
	out.EditID = uuid.Must(uuid.NewV7())
	var words, caption *string
	if r.file != "" {
		caption = &r.body
	} else {
		words = &r.body
	}
	if _, err := tx.Exec(ctx, `INSERT INTO link_message_edits (id, tenant_id, message_id, pair_id, edited_by, edited_at, words_before, caption_before)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, out.EditID, tenant, r.id, pair, o.Editor, o.Now, words, caption); err != nil {
		return out, err
	}
	// The preview: made again when the first link changed, else kept.
	setPreview := `preview`
	args := []any{tenant, pair, text, ours, o.Now}
	if o.NewPreview {
		setPreview = `$6::jsonb`
		args = append(args, StoredPreview(o.Preview))
		out.OldOurs = r.preview
	}
	if _, err := tx.Exec(ctx, `UPDATE link_messages SET body = $3, mentions = $4, edited_at = $5, edits = edits + 1, preview = `+setPreview+`
		WHERE tenant_id = $1 AND pair_id = $2`, args...); err != nil {
		return out, err
	}
	if out.Message, err = scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.pair_id = $2`, tenant, pair)); err != nil {
		return out, err
	}
	detail, _ := json.Marshal(map[string]any{"message": pair, "edit": out.EditID})
	if out.Other, err = EnterTx(ctx, tx, link, "link.message.edit", detail, by); err != nil {
		return out, err
	}
	args = []any{out.Other, pair, text, theirs, o.Now}
	if o.NewPreview {
		var prev []byte
		err := tx.QueryRow(ctx, `SELECT preview FROM link_messages WHERE tenant_id = $1 AND pair_id = $2`, out.Other, pair).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if prev != nil {
			out.OldTheirs = &Preview{}
			if err := json.Unmarshal(prev, out.OldTheirs); err != nil {
				return out, err
			}
		}
		var p *Preview
		if o.Preview != nil {
			c := *o.Preview
			c.Image = nil
			if o.Preview.Image != nil && o.TheirImage != nil {
				if img, err := o.TheirImage(ctx, out.Other); err == nil {
					c.Image = img
				}
			}
			p = &c
		}
		args = append(args, StoredPreview(p))
	}
	_, err = tx.Exec(ctx, `UPDATE link_messages SET body = $3, mentions = $4, edited_at = $5, edits = edits + 1, preview = `+setPreview+`
		WHERE tenant_id = $1 AND pair_id = $2`, args...)
	return out, err
}

// MessageEdit is one earlier wording of a message, for the audit log.
type MessageEdit struct {
	ID        uuid.UUID `json:"id"`
	EditedBy  string    `json:"edited_by"`
	EditedAt  time.Time `json:"edited_at"`
	Words     *string   `json:"words_before,omitempty"`
	Caption   *string   `json:"caption_before,omitempty"`
	MessageID uuid.UUID `json:"message"`
}

// EditsTx is the earlier wordings the business in scope keeps of a message
// (its own, sent by its members), the oldest first.
func EditsTx(ctx context.Context, tx pgx.Tx, tenant, pair uuid.UUID) ([]MessageEdit, error) {
	rows, err := tx.Query(ctx, `SELECT id, edited_by, edited_at, words_before, caption_before, pair_id FROM link_message_edits
		WHERE tenant_id = $1 AND pair_id = $2 ORDER BY edited_at, id`, tenant, pair)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MessageEdit, error) {
		var e MessageEdit
		return e, r.Scan(&e.ID, &e.EditedBy, &e.EditedAt, &e.Words, &e.Caption, &e.MessageID)
	})
	if out == nil {
		out = []MessageEdit{}
	}
	return out, err
}
