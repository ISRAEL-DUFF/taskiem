package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The conversation (L3, docs/spec/business-links.md §5.5, §8): what two
// linked businesses say to each other, and every document between them
// as a card at the moment it happened. Each side keeps the whole of it
// under its own tenancy: a message is the sender's own copy, then the
// other side's through the one door, in one transaction; a card is
// written into each side by Taskiem. A message quoting a card is a reply
// about that card's document: the document's thread.

// Message kinds.
const (
	KindText = "text"
	KindCard = "card"
)

// MaxMessage is the most a message may say.
const MaxMessage = 4000

// DocCard is a document event in a conversation, naming this side's own
// document: an order (the seller's), a purchase order (the buyer's), an
// invoice (the seller's) or a bill (the buyer's).
type DocCard struct {
	Doc        string `json:"doc"`
	Event      string `json:"event"`
	Reference  string `json:"reference"`
	ID         string `json:"id,omitempty"`
	AmountKobo *int64 `json:"amount_kobo,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Card documents.
const (
	DocOrder    = "order"
	DocPurchase = "purchase"
	DocInvoice  = "invoice"
	DocBill     = "bill"
)

// Quote is the message a reply quotes, as the reply shows it.
type Quote struct {
	PairID uuid.UUID `json:"pair_id"`
	Mine   bool      `json:"mine"`
	Kind   string    `json:"kind"`
	Body   string    `json:"body,omitempty"`
	Card   *DocCard  `json:"card,omitempty"`
}

// Message is one message of a conversation, as this side sees it.
type Message struct {
	PairID     uuid.UUID  `json:"id"`
	LinkID     uuid.UUID  `json:"link"`
	Mine       bool       `json:"mine"`
	Kind       string     `json:"kind"`
	Body       string     `json:"body,omitempty"`
	SenderName string     `json:"sender_name,omitempty"`
	Card       *DocCard   `json:"card,omitempty"`
	Reply      *Quote     `json:"reply,omitempty"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Conversation is one linked business's conversation, as the list shows
// it: the business, its last message, how many of its messages are
// unread, and this side's settings.
type Conversation struct {
	LinkID   uuid.UUID `json:"link"`
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	State    string    `json:"state"`
	Last     *Message  `json:"last,omitempty"`
	Unread   int       `json:"unread"`
	Muted    bool      `json:"muted"`
	Archived bool      `json:"archived"`
}

const messageCols = `m.pair_id, m.link_id, m.mine, m.kind, m.body, m.sender_name, m.card, m.read_at, m.created_at,
	q.pair_id, q.mine, q.kind, q.body, q.card`

const messageFrom = ` FROM link_messages m LEFT JOIN link_messages q ON q.tenant_id = m.tenant_id AND q.pair_id = m.reply_to`

func scanMessage(r pgx.Row) (Message, error) {
	var m Message
	var card, qcard []byte
	var qPair *uuid.UUID
	var qMine *bool
	var qKind, qBody *string
	if err := r.Scan(&m.PairID, &m.LinkID, &m.Mine, &m.Kind, &m.Body, &m.SenderName, &card, &m.ReadAt, &m.CreatedAt,
		&qPair, &qMine, &qKind, &qBody, &qcard); err != nil {
		return m, err
	}
	if card != nil {
		m.Card = &DocCard{}
		if err := json.Unmarshal(card, m.Card); err != nil {
			return m, err
		}
	}
	if qPair != nil {
		q := &Quote{PairID: *qPair, Mine: *qMine, Kind: *qKind, Body: cutRunes(*qBody, 160)}
		if qcard != nil {
			q.Card = &DocCard{}
			if err := json.Unmarshal(qcard, q.Card); err != nil {
				return m, err
			}
		}
		m.Reply = q
	}
	return m, nil
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ConversationsTx is the business in scope's conversations: one per
// active link, and one per ended link that has messages, the newest
// first.
func ConversationsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Conversation, error) {
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []Conversation{}
	for _, l := range list {
		if l.State != Active && l.State != Ended {
			continue
		}
		c := Conversation{LinkID: l.ID, Business: l.Business, Name: l.Name, State: l.State}
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT muted FROM link_conversations WHERE tenant_id = $1 AND link_id = $2), false),
			COALESCE((SELECT archived FROM link_conversations WHERE tenant_id = $1 AND link_id = $2), false),
			(SELECT count(*) FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND NOT mine AND read_at IS NULL)::int`,
			tenant, l.ID).Scan(&c.Muted, &c.Archived, &c.Unread); err != nil {
			return nil, err
		}
		last, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.link_id = $2
			ORDER BY m.created_at DESC, m.id DESC LIMIT 1`, tenant, l.ID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if l.State == Ended {
				continue
			}
		case err != nil:
			return nil, err
		default:
			c.Last = &last
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b Conversation) int {
		at, bt := time.Time{}, time.Time{}
		if a.Last != nil {
			at = a.Last.CreatedAt
		}
		if b.Last != nil {
			bt = b.Last.CreatedAt
		}
		return bt.Compare(at)
	})
	return out, nil
}

// Page is a page of a conversation: messages before or after a time, or
// those saying q.
type Page struct {
	Before *time.Time
	After  *time.Time
	Q      string
	Limit  int
}

// MessagesTx is a page of the business in scope's conversation with a
// link, oldest first.
func MessagesTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, p Page) ([]Message, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	q := strings.TrimSpace(p.Q)
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+`
		WHERE m.tenant_id = $1 AND m.link_id = $2 AND ($3::timestamptz IS NULL OR m.created_at < $3) AND ($4::timestamptz IS NULL OR m.created_at > $4)
		  AND ($5 = '' OR m.body ILIKE '%' || $5 || '%' OR m.card ->> 'reference' ILIKE '%' || $5 || '%' OR m.card ->> 'note' ILIKE '%' || $5 || '%')
		ORDER BY m.created_at DESC, m.id DESC LIMIT $6`, tenant, link, p.Before, p.After, likeSafe(q), p.Limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	slices.Reverse(out) // the newest page, oldest first
	return out, err
}

// likeSafe takes the wildcards out of a search's words.
func likeSafe(q string) string {
	return strings.NewReplacer(`\`, ``, `%`, ``, `_`, ``).Replace(q)
}

// SearchTx is the business in scope's messages that say q, across its
// conversations, the newest first.
func SearchMessagesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, q string, limit int) ([]Message, error) {
	q = likeSafe(strings.TrimSpace(q))
	if len([]rune(q)) < 2 {
		return []Message{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND (m.body ILIKE '%' || $2 || '%' OR m.card ->> 'reference' ILIKE '%' || $2 || '%' OR m.card ->> 'note' ILIKE '%' || $2 || '%')
		ORDER BY m.created_at DESC LIMIT $3`, tenant, q, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}

// Sender is who sends a message: the member, and the first name the
// other side sees.
type Sender struct {
	ID        string
	FirstName string
}

// SendTx sends a message from the business in scope to a linked
// business: its own copy, then the other side's through the door, in the
// transaction (the caller's last writes). replyTo quotes a message of the
// conversation (its id). It answers the message and the business told.
func SendTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, body string, replyTo *uuid.UUID, from Sender, by Actor) (Message, uuid.UUID, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > MaxMessage {
		return Message{}, uuid.Nil, fmt.Errorf("%w: a message says something, in up to %d characters", ErrInvalid, MaxMessage)
	}
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return Message{}, uuid.Nil, err
	}
	if l.State != Active {
		return Message{}, uuid.Nil, fmt.Errorf("%w: you are no longer linked with %s", ErrState, l.Name)
	}
	if replyTo != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3)`, tenant, link, *replyTo).Scan(&ok); err != nil {
			return Message{}, uuid.Nil, err
		}
		if !ok {
			return Message{}, uuid.Nil, fmt.Errorf("%w: that message is not in this conversation", ErrInvalid)
		}
	}
	name := cutRunes(strings.TrimSpace(from.FirstName), 80)
	pair := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, sender_name, sender_id, reply_to)
		VALUES ($1, $2, $3, $4, true, 'text', $5, $6, $7, $8)`, uuid.Must(uuid.NewV7()), tenant, link, pair, body, name, from.ID, replyTo); err != nil {
		return Message{}, uuid.Nil, err
	}
	m, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.pair_id = $2`, tenant, pair))
	if err != nil {
		return m, uuid.Nil, err
	}
	detail, _ := json.Marshal(map[string]any{"message": pair})
	other, err := EnterTx(ctx, tx, link, "link.message", detail, by)
	if err != nil {
		return m, uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, sender_name, reply_to, created_at)
		VALUES ($1, $2, $3, $4, false, 'text', $5, $6, $7, $8)`, uuid.Must(uuid.NewV7()), other, link, pair, body, name, replyTo, m.CreatedAt)
	return m, other, err
}

// MarkReadTx marks the business in scope's unread messages from a link read,
// and the other side's copies of them read too, through the door: the
// double tick. It answers how many it marked.
func MarkReadTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, by Actor) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE link_messages SET read_at = now() WHERE tenant_id = $1 AND link_id = $2 AND NOT mine AND kind = 'text' AND read_at IS NULL
		RETURNING pair_id`, tenant, link)
	if err != nil {
		return 0, err
	}
	pairs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	// Cards are Taskiem's: read with the rest, and nothing to tell.
	if _, err := tx.Exec(ctx, `UPDATE link_messages SET read_at = now() WHERE tenant_id = $1 AND link_id = $2 AND NOT mine AND read_at IS NULL`, tenant, link); err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}
	ok, err := active(ctx, tx, link)
	if err != nil || !ok {
		return len(pairs), err
	}
	detail, _ := json.Marshal(map[string]any{"read": len(pairs)})
	if _, err := EnterTx(ctx, tx, link, "link.read", detail, by); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE link_messages SET read_at = now() WHERE link_id = $1 AND mine AND pair_id = ANY ($2) AND read_at IS NULL`, link, pairs)
	return len(pairs), err
}

// Settings are this side's settings for a conversation.
type ConversationSettings struct {
	Muted    *bool `json:"muted"`
	Archived *bool `json:"archived"`
}

// SetConversationTx mutes or archives the business in scope's
// conversation with a link (what it names).
func SetConversationTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, s ConversationSettings, by string) error {
	if _, err := GetTx(ctx, tx, link); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO link_conversations (tenant_id, link_id, muted, archived, updated_by) VALUES ($1, $2, COALESCE($3, false), COALESCE($4, false), $5)
		ON CONFLICT (tenant_id, link_id) DO UPDATE SET muted = COALESCE($3, link_conversations.muted), archived = COALESCE($4, link_conversations.archived),
			updated_by = EXCLUDED.updated_by, updated_at = now()`, tenant, link, s.Muted, s.Archived, by)
	return err
}

// MutedTx reports whether the business in scope muted its conversation
// with a link.
func MutedTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (bool, error) {
	var muted bool
	err := tx.QueryRow(ctx, `SELECT muted FROM link_conversations WHERE tenant_id = $1 AND link_id = $2`, tenant, link).Scan(&muted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return muted, err
}

// CardTx writes a card into the business in scope's conversation with a
// link (Taskiem's, in that side's own scope): pair is the same on both
// sides, so a reply quoting it on one side quotes it on the other. A card
// already written answers as it was.
func CardTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, mine bool, c DocCard) error {
	raw, _ := json.Marshal(c)
	_, err := tx.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, card) VALUES ($1, $2, $3, $4, $5, 'card', $6)
		ON CONFLICT (tenant_id, pair_id) DO NOTHING`, uuid.Must(uuid.NewV7()), tenant, link, pair, mine, raw)
	return err
}

// ThreadTx is a document's thread in the business in scope: the cards of
// the document (doc and reference, ours), and the messages quoting them,
// oldest first.
func ThreadTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, doc, reference string) ([]Message, error) {
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND (
			(m.kind = 'card' AND m.card ->> 'doc' = $2 AND m.card ->> 'reference' = $3)
			OR m.reply_to IN (SELECT c.pair_id FROM link_messages c WHERE c.tenant_id = $1 AND c.kind = 'card' AND c.card ->> 'doc' = $2 AND c.card ->> 'reference' = $3))
		ORDER BY m.created_at, m.pair_id LIMIT 200`, tenant, doc, reference)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}

// LinkWith is the business in scope's active link with another business,
// by that business's id; ErrNotFound when there is none.
func LinkWith(ctx context.Context, tx pgx.Tx, business uuid.UUID) (Link, error) {
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return Link{}, err
	}
	for _, l := range list {
		if l.Business == business && l.State == Active {
			return l, nil
		}
	}
	return Link{}, ErrNotFound
}

// CardForTx is the newest card of a document in the business in scope
// (to quote it), nil when there is none.
func CardForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, doc, reference string) (*Message, error) {
	m, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.kind = 'card'
		AND m.card ->> 'doc' = $2 AND upper(m.card ->> 'reference') = upper($3) ORDER BY m.created_at DESC LIMIT 1`, tenant, doc, reference))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// RecordSentTx keeps the chat app's id of a conversation's message told
// to a person's phone (by the keyed hash of their address), so their
// reply quoting it there goes into the conversation.
func RecordSentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string, addrHash []byte, id string, link, pair uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO link_chat_sent (tenant_id, channel, address_hash, message_id, link_id, pair_id) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`, tenant, channel, addrHash, id, link, pair)
	return err
}

// SentTx is the conversation's message a chat app's message to this
// address was, in the business in scope: its link and id; ok false when
// it was none.
func SentTx(ctx context.Context, tx pgx.Tx, channel string, addrHash []byte, id string) (uuid.UUID, uuid.UUID, bool, error) {
	var link, pair uuid.UUID
	err := tx.QueryRow(ctx, `SELECT link_id, pair_id FROM link_chat_sent WHERE channel = $1 AND address_hash = $2 AND message_id = $3`, channel, addrHash, id).Scan(&link, &pair)
	if errors.Is(err, pgx.ErrNoRows) {
		return link, pair, false, nil
	}
	return link, pair, err == nil, err
}
