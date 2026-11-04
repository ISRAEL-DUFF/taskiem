package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reactions are WhatsApp's: one emoji per member per message, from the six
// it offers, seen by both businesses. A star is a member's own.

// ReactionEmoji are the emoji a message may be reacted to with.
var ReactionEmoji = []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}

// ValidReaction reports whether emoji is one of the six.
func ValidReaction(emoji string) bool {
	for _, e := range ReactionEmoji {
		if e == emoji {
			return true
		}
	}
	return false
}

// Reaction is the people's emoji on a message, as the business in scope
// sees it: how many gave it, whether the viewer did, and by first name.
type Reaction struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Mine  bool     `json:"mine,omitempty"`
	By    []string `json:"by,omitempty"`
}

// Viewer is who a thread is read for: their reactions and stars are marked.
type Viewer struct {
	// Actor is the member as the audit names them (their reactions are "mine").
	Actor string
	// UserID is the person (their stars); uuid.Nil for an API key, who has none.
	UserID uuid.UUID
}

// DecorateTx puts the reactions on the messages, and the viewer's stars.
func DecorateTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, v Viewer, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(msgs))
	at := make(map[uuid.UUID]int, len(msgs))
	for i, m := range msgs {
		ids[i] = m.PairID
		at[m.PairID] = i
	}
	rows, err := tx.Query(ctx, `SELECT pair_id, emoji, mine, COALESCE(reactor_id, ''), reactor_name FROM link_message_reactions
		WHERE tenant_id = $1 AND pair_id = ANY ($2) ORDER BY created_at, id`, tenant, ids)
	if err != nil {
		return err
	}
	type row struct {
		pair      uuid.UUID
		emoji     string
		mine      bool
		who, name string
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.pair, &x.emoji, &x.mine, &x.who, &x.name)
	})
	if err != nil {
		return err
	}
	for _, x := range got {
		i := at[x.pair]
		var rx *Reaction
		for k := range msgs[i].Reactions {
			if msgs[i].Reactions[k].Emoji == x.emoji {
				rx = &msgs[i].Reactions[k]
			}
		}
		if rx == nil {
			msgs[i].Reactions = append(msgs[i].Reactions, Reaction{Emoji: x.emoji})
			rx = &msgs[i].Reactions[len(msgs[i].Reactions)-1]
		}
		rx.Count++
		if x.mine && v.Actor != "" && x.who == v.Actor {
			rx.Mine = true
		}
		if x.name != "" {
			rx.By = append(rx.By, x.name)
		}
	}
	if v.UserID == uuid.Nil {
		return nil
	}
	srows, err := tx.Query(ctx, `SELECT pair_id FROM link_message_stars WHERE tenant_id = $1 AND user_id = $2 AND pair_id = ANY ($3)`, tenant, v.UserID, ids)
	if err != nil {
		return err
	}
	starred, err := pgx.CollectRows(srows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, p := range starred {
		msgs[at[p]].Starred = true
	}
	return nil
}

// ReactTx puts a member's emoji on a message of a conversation (replacing
// the one they gave), and the other side's copy of it through the door, in
// the transaction. It answers the business told, and whether anything
// changed (the same emoji again is nothing, and tells no one). A reaction
// counts against the plan's messages per day when it is first given
// (maxPerDay 0: no limit).
func ReactTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, emoji string, from Sender, by Actor, maxPerDay int64) (uuid.UUID, bool, error) {
	if !ValidReaction(emoji) {
		return uuid.Nil, false, fmt.Errorf("%w: a reaction is one of %v", ErrInvalid, ReactionEmoji)
	}
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return uuid.Nil, false, err
	}
	if l.State != Active {
		return uuid.Nil, false, fmt.Errorf("%w: you are no longer linked with %s", ErrState, l.Name)
	}
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3 AND hidden_at IS NULL`, tenant, link, pair).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, ErrNotFound
		}
		return uuid.Nil, false, err
	}
	name := cutRunes(from.FirstName, 80)
	var reaction uuid.UUID
	var was string
	err = tx.QueryRow(ctx, `SELECT reaction_id, emoji FROM link_message_reactions WHERE tenant_id = $1 AND pair_id = $2 AND mine AND reactor_id = $3`, tenant, pair, from.ID).Scan(&reaction, &was)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if maxPerDay > 0 {
			n, err := SentTodayTx(ctx, tx, tenant)
			if err != nil {
				return uuid.Nil, false, err
			}
			if n >= maxPerDay {
				return uuid.Nil, false, fmt.Errorf("%w: this plan allows at most %d messages to linked businesses a day", ErrLimit, maxPerDay)
			}
		}
		reaction = uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO link_message_reactions (id, tenant_id, link_id, pair_id, reaction_id, mine, reactor_id, reactor_name, emoji)
			VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8)`, uuid.Must(uuid.NewV7()), tenant, link, pair, reaction, from.ID, name, emoji); err != nil {
			return uuid.Nil, false, err
		}
	case err != nil:
		return uuid.Nil, false, err
	case was == emoji:
		return uuid.Nil, false, nil
	default:
		if _, err := tx.Exec(ctx, `UPDATE link_message_reactions SET emoji = $3 WHERE tenant_id = $1 AND reaction_id = $2`, tenant, reaction, emoji); err != nil {
			return uuid.Nil, false, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"message": pair, "emoji": emoji})
	other, err := EnterTx(ctx, tx, link, "link.reaction", detail, by)
	if err != nil {
		return uuid.Nil, false, err
	}
	// The other side's copy: the same reaction, by first name, not theirs.
	_, err = tx.Exec(ctx, `INSERT INTO link_message_reactions (id, tenant_id, link_id, pair_id, reaction_id, mine, reactor_name, emoji)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7)
		ON CONFLICT (tenant_id, reaction_id) DO UPDATE SET emoji = EXCLUDED.emoji`, uuid.Must(uuid.NewV7()), other, link, pair, reaction, name, emoji)
	return other, true, err
}

// UnreactTx takes a member's reaction back, on both sides.
func UnreactTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, from Sender, by Actor) error {
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return err
	}
	if l.State != Active {
		return fmt.Errorf("%w: you are no longer linked with %s", ErrState, l.Name)
	}
	var reaction uuid.UUID
	err = tx.QueryRow(ctx, `DELETE FROM link_message_reactions WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3 AND mine AND reactor_id = $4 RETURNING reaction_id`,
		tenant, link, pair, from.ID).Scan(&reaction)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // none to take back
	}
	if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"message": pair, "removed": true})
	other, err := EnterTx(ctx, tx, link, "link.reaction", detail, by)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM link_message_reactions WHERE tenant_id = $1 AND reaction_id = $2`, other, reaction)
	return err
}

// StarTx stars (or unstars) a message of a conversation for a person: theirs
// alone, never copied to the other side.
func StarTx(ctx context.Context, tx pgx.Tx, tenant, user, link, pair uuid.UUID, on bool) error {
	if !on {
		_, err := tx.Exec(ctx, `DELETE FROM link_message_stars WHERE tenant_id = $1 AND user_id = $2 AND pair_id = $3`, tenant, user, pair)
		return err
	}
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3 AND hidden_at IS NULL`, tenant, link, pair).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO link_message_stars (tenant_id, user_id, link_id, pair_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, tenant, user, link, pair)
	return err
}

// StarredTx is a person's starred messages across conversations, the most
// recently starred first.
func StarredTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) ([]Message, error) {
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+`
		JOIN link_message_stars s ON s.tenant_id = m.tenant_id AND s.pair_id = m.pair_id AND s.user_id = $2
		WHERE m.tenant_id = $1 AND m.hidden_at IS NULL ORDER BY s.created_at DESC LIMIT 100`, tenant, user)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}
