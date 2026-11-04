package links

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// What changed in the conversations of the business in scope since a time,
// for the Chat page's live stream: from the indexes on updated_at (migration
// 00306), so asking costs a lookup when nothing has.

// ChangeRef is a message that changed: when, and whether it is now deleted
// for this side (then there is no message to send, only that it is gone).
type ChangeRef struct {
	Pair   uuid.UUID
	Link   uuid.UUID
	At     time.Time
	Hidden bool
}

// ChangedRefsTx lists the messages changed after a time, oldest first
// (up to limit). A reader that has seen some of them asks for the rest
// with MessagesByPairTx.
func ChangedRefsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, since time.Time, limit int) ([]ChangeRef, error) {
	rows, err := tx.Query(ctx, `SELECT pair_id, link_id, updated_at, hidden_at IS NOT NULL FROM link_messages
		WHERE tenant_id = $1 AND updated_at > $2 ORDER BY updated_at, id LIMIT $3`, tenant, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChangeRef, error) {
		var c ChangeRef
		return c, r.Scan(&c.Pair, &c.Link, &c.At, &c.Hidden)
	})
}

// MessagesByPairTx are the messages of the business in scope with these ids
// (not those deleted for it), oldest first.
func MessagesByPairTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, pairs []uuid.UUID) ([]Message, error) {
	if len(pairs) == 0 {
		return []Message{}, nil
	}
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+`
		WHERE m.tenant_id = $1 AND m.pair_id = ANY ($2) AND m.hidden_at IS NULL ORDER BY m.created_at, m.id`, tenant, pairs)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}

// ConversationChange is a conversation whose settings changed.
type ConversationChange struct {
	Link     uuid.UUID `json:"link"`
	Muted    bool      `json:"muted"`
	Archived bool      `json:"archived"`
	Pinned   bool      `json:"pinned"`
	At       time.Time `json:"-"`
}

// ConversationChangesTx lists the conversations whose settings changed after a time.
func ConversationChangesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, since time.Time) ([]ConversationChange, error) {
	rows, err := tx.Query(ctx, `SELECT link_id, muted, archived, pinned_at IS NOT NULL, updated_at FROM link_conversations
		WHERE tenant_id = $1 AND updated_at > $2 ORDER BY updated_at LIMIT 100`, tenant, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConversationChange, error) {
		var c ConversationChange
		return c, r.Scan(&c.Link, &c.Muted, &c.Archived, &c.Pinned, &c.At)
	})
}

// ActiveLinksTx are the links the business in scope has an active conversation on.
func ActiveLinksTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, l := range list {
		if l.State == Active {
			out = append(out, l.ID)
		}
	}
	return out, nil
}

// TypingNowTx are those of the links whose other side is typing now.
func TypingNowTx(ctx context.Context, tx pgx.Tx, links []uuid.UUID) ([]uuid.UUID, error) {
	if len(links) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM unnest($1::uuid[]) AS id WHERE taskiem_link_typing_of(id) ORDER BY id`, links)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
