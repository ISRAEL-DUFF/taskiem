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

// Attachment is a photo, a PDF or a voice note sent with a message, in
// this side's own storage: each business keeps its own copy.
type Attachment struct {
	Kind string `json:"kind"` // photo, file or voice
	Type string `json:"type"`
	Size int64  `json:"size"`
	Name string `json:"name,omitempty"`
	// Path is where this side's copy is stored; never shown.
	Path string `json:"-"`
}

// Preview is a link's preview in a message: the page's host, title and
// description, fetched by the sender's side when it was sent.
type Preview struct {
	URL         string `json:"url"`
	Host        string `json:"host"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// Mention is a structured reference in a message, put in by the
// composer: a linked business ("@Dangote Stores") or an item of the
// conversation's price list ("#Rice, 50 kg", with its unit and price).
type Mention struct {
	Kind      string    `json:"kind"` // business or item
	Name      string    `json:"name"`
	Link      uuid.UUID `json:"link,omitzero"` // a business, on our copy only
	ItemID    string    `json:"item_id,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	PriceKobo int64     `json:"price_kobo,omitempty"`
}

// Quote is the message a reply quotes, as the reply shows it.
type Quote struct {
	PairID uuid.UUID `json:"pair_id"`
	Mine   bool      `json:"mine"`
	Kind   string    `json:"kind"`
	Body   string    `json:"body,omitempty"`
	Card   *DocCard  `json:"card,omitempty"`
	// Attachment is what the quoted message carried, without its path.
	Attachment *Attachment `json:"attachment,omitempty"`
}

// Message is one message of a conversation, as this side sees it.
type Message struct {
	PairID     uuid.UUID   `json:"id"`
	LinkID     uuid.UUID   `json:"link"`
	Mine       bool        `json:"mine"`
	Kind       string      `json:"kind"`
	Body       string      `json:"body,omitempty"`
	SenderName string      `json:"sender_name,omitempty"`
	Card       *DocCard    `json:"card,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	Forwarded  bool        `json:"forwarded,omitempty"`
	Preview    *Preview    `json:"preview,omitempty"`
	Mentions   []Mention   `json:"mentions,omitempty"`
	Reply      *Quote      `json:"reply,omitempty"`
	ReadAt     *time.Time  `json:"read_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
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
	Pinned   bool      `json:"pinned"`
	// Typing: someone on the other side is typing now.
	Typing   bool `json:"typing"`
	pinnedAt *time.Time
}

const messageCols = `m.pair_id, m.link_id, m.mine, m.kind, m.body, m.sender_name, m.card, m.attachment, m.forwarded, m.preview, m.mentions, m.read_at, m.created_at,
	q.pair_id, q.mine, q.kind, q.body, q.card, q.attachment`

const messageFrom = ` FROM link_messages m LEFT JOIN link_messages q ON q.tenant_id = m.tenant_id AND q.pair_id = m.reply_to`

func scanMessage(r pgx.Row) (Message, error) {
	var m Message
	var card, qcard, att, qatt, prev, ments []byte
	var qPair *uuid.UUID
	var qMine *bool
	var qKind, qBody *string
	if err := r.Scan(&m.PairID, &m.LinkID, &m.Mine, &m.Kind, &m.Body, &m.SenderName, &card, &att, &m.Forwarded, &prev, &ments, &m.ReadAt, &m.CreatedAt,
		&qPair, &qMine, &qKind, &qBody, &qcard, &qatt); err != nil {
		return m, err
	}
	if card != nil {
		m.Card = &DocCard{}
		if err := json.Unmarshal(card, m.Card); err != nil {
			return m, err
		}
	}
	if att != nil {
		m.Attachment = &Attachment{}
		if err := json.Unmarshal(att, m.Attachment); err != nil {
			return m, err
		}
	}
	if ments != nil {
		if err := json.Unmarshal(ments, &m.Mentions); err != nil {
			return m, err
		}
	}
	if prev != nil {
		m.Preview = &Preview{}
		if err := json.Unmarshal(prev, m.Preview); err != nil {
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
		if qatt != nil {
			q.Attachment = &Attachment{}
			if err := json.Unmarshal(qatt, q.Attachment); err != nil {
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
			(SELECT pinned_at FROM link_conversations WHERE tenant_id = $1 AND link_id = $2),
			(SELECT count(*) FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND NOT mine AND read_at IS NULL AND hidden_at IS NULL)::int,
			$3 AND taskiem_link_typing_of($2)`,
			tenant, l.ID, l.State == Active).Scan(&c.Muted, &c.Archived, &c.pinnedAt, &c.Unread, &c.Typing); err != nil {
			return nil, err
		}
		c.Pinned = c.pinnedAt != nil
		last, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.link_id = $2 AND m.hidden_at IS NULL
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
		// The pinned first, the last pinned at the top, as WhatsApp's.
		switch {
		case a.pinnedAt != nil && b.pinnedAt != nil:
			return b.pinnedAt.Compare(*a.pinnedAt)
		case a.pinnedAt != nil:
			return -1
		case b.pinnedAt != nil:
			return 1
		}
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
		WHERE m.tenant_id = $1 AND m.link_id = $2 AND m.hidden_at IS NULL AND ($3::timestamptz IS NULL OR m.created_at < $3) AND ($4::timestamptz IS NULL OR m.created_at > $4)
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
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.hidden_at IS NULL AND (m.body ILIKE '%' || $2 || '%' OR m.card ->> 'reference' ILIKE '%' || $2 || '%' OR m.card ->> 'note' ILIKE '%' || $2 || '%')
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
	return SendWithTx(ctx, tx, tenant, link, body, from, by, SendOpts{ReplyTo: replyTo})
}

// SendOpts is what a message carries besides its words.
type SendOpts struct {
	// ReplyTo quotes a message of the conversation (its id).
	ReplyTo *uuid.UUID
	// Ours and Theirs are a photo, a PDF or a voice note: our copy and
	// the other side's, each already in that side's own storage. The words
	// are then a caption, and may be empty.
	Ours, Theirs *Attachment
	// Forwarded marks a message forwarded from another conversation.
	Forwarded bool
	// Preview is the first link's preview, on both copies.
	Preview *Preview
	// Mentions are the message's structured references (checked by the
	// caller); the other side's copy names a business without our link.
	Mentions []Mention
}

// SendWithTx is SendTx with what o carries.
func SendWithTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, body string, from Sender, by Actor, o SendOpts) (Message, uuid.UUID, error) {
	replyTo, ourFile, theirFile := o.ReplyTo, o.Ours, o.Theirs
	var preview, ours, theirs []byte
	if o.Preview != nil {
		preview, _ = json.Marshal(o.Preview)
	}
	if len(o.Mentions) > 0 {
		ours, _ = json.Marshal(o.Mentions)
		other := make([]Mention, len(o.Mentions))
		for i, x := range o.Mentions {
			x.Link = uuid.Nil
			other[i] = x
		}
		theirs, _ = json.Marshal(other)
	}
	body = strings.TrimSpace(body)
	if (body == "" && ourFile == nil) || len([]rune(body)) > MaxMessage || (ourFile == nil) != (theirFile == nil) {
		return Message{}, uuid.Nil, fmt.Errorf("%w: a message says something, in up to %d characters", ErrInvalid, MaxMessage)
	}
	// The stored attachment keeps its path, which the API never shows.
	jsonOf := func(a *Attachment) []byte {
		if a == nil {
			return nil
		}
		a.Name = cutRunes(strings.TrimSpace(a.Name), 120)
		raw, _ := json.Marshal(map[string]any{"kind": a.Kind, "type": a.Type, "size": a.Size, "name": a.Name, "path": a.Path})
		return raw
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
	if _, err := tx.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, sender_name, sender_id, reply_to, attachment, forwarded, preview, mentions)
		VALUES ($1, $2, $3, $4, true, 'text', $5, $6, $7, $8, $9, $10, $11, $12)`, uuid.Must(uuid.NewV7()), tenant, link, pair, body, name, from.ID, replyTo, jsonOf(ourFile), o.Forwarded, preview, ours); err != nil {
		return Message{}, uuid.Nil, err
	}
	m, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.pair_id = $2`, tenant, pair))
	if err != nil {
		return m, uuid.Nil, err
	}
	fromBusiness, err := businessName(ctx, tx, tenant)
	if err != nil {
		return m, uuid.Nil, err
	}
	detail, _ := json.Marshal(map[string]any{"message": pair})
	other, err := EnterTx(ctx, tx, link, "link.message", detail, by)
	if err != nil {
		return m, uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, sender_name, reply_to, created_at, attachment, forwarded, preview, mentions)
		VALUES ($1, $2, $3, $4, false, 'text', $5, $6, $7, $8, $9, $10, $11, $12)`, uuid.Must(uuid.NewV7()), other, link, pair, body, name, replyTo, m.CreatedAt, jsonOf(theirFile), o.Forwarded, preview, theirs); err != nil {
		return m, other, err
	}
	// For the other side's workflows (L6): a message from a linked business.
	ev := map[string]any{"link": link, "business": fromBusiness, "text": body, "sender": name, "message_id": pair}
	if m.Reply != nil && m.Reply.Card != nil {
		ev["about"] = map[string]any{"doc": m.Reply.Card.Doc, "reference": m.Reply.Card.Reference}
	}
	if ourFile != nil {
		ev["attachment"] = ourFile.Kind
	}
	err = QueueEventTx(ctx, tx, other, EventThreadMessage, pair.String(), ev)
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
	Pinned   *bool `json:"pinned"`
}

// MaxPinned is how many conversations may be pinned, as WhatsApp's.
const MaxPinned = 3

// SetConversationTx mutes or archives the business in scope's
// conversation with a link (what it names).
func SetConversationTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, s ConversationSettings, by string) error {
	if _, err := GetTx(ctx, tx, link); err != nil {
		return err
	}
	if s.Pinned != nil && *s.Pinned {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM link_conversations WHERE tenant_id = $1 AND pinned_at IS NOT NULL AND link_id <> $2`, tenant, link).Scan(&n); err != nil {
			return err
		}
		if n >= MaxPinned {
			return fmt.Errorf("%w: up to %d chats can be pinned; unpin one first", ErrState, MaxPinned)
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO link_conversations (tenant_id, link_id, muted, archived, pinned_at, updated_by)
		VALUES ($1, $2, COALESCE($3, false), COALESCE($4, false), CASE WHEN $5 THEN now() END, $6)
		ON CONFLICT (tenant_id, link_id) DO UPDATE SET muted = COALESCE($3, link_conversations.muted), archived = COALESCE($4, link_conversations.archived),
			pinned_at = CASE WHEN $5 IS NULL THEN link_conversations.pinned_at WHEN $5 THEN COALESCE(link_conversations.pinned_at, now()) END,
			updated_by = EXCLUDED.updated_by, updated_at = now()`, tenant, link, s.Muted, s.Archived, s.Pinned, by)
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
	rows, err := tx.Query(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.hidden_at IS NULL AND (
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

// AttachmentTx is this side's copy of a message's attachment, by the
// message's id, in the business in scope's conversation with a link.
func AttachmentTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID) (Attachment, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT attachment FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3`, tenant, link, pair).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && raw == nil) {
		return Attachment{}, fmt.Errorf("%w: no such file", ErrNotFound)
	}
	if err != nil {
		return Attachment{}, err
	}
	var a struct {
		Attachment
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Attachment{}, err
	}
	a.Attachment.Path = a.Path
	return a.Attachment, nil
}

// HideTx deletes a message for the business in scope: its own copy is
// hidden from its view, the other side's copy stays (each side keeps the
// record of the trade). A document's card is never hidden.
func HideTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID) error {
	var kind string
	err := tx.QueryRow(ctx, `UPDATE link_messages SET hidden_at = COALESCE(hidden_at, now()) WHERE tenant_id = $1 AND link_id = $2 AND pair_id = $3
		RETURNING kind`, tenant, link, pair).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no such message", ErrNotFound)
	}
	if err != nil {
		return err
	}
	if kind == "card" {
		return fmt.Errorf("%w: a document's card stays in the conversation", ErrInvalid)
	}
	return nil
}

// ForwardableTx is a message of the business in scope's conversation
// with a link that can be forwarded: its words, this side's copy of its
// file, and its preview. A card is not forwarded (its document is this
// business's).
func ForwardableTx(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID) (Message, *Attachment, error) {
	m, err := scanMessage(tx.QueryRow(ctx, `SELECT `+messageCols+messageFrom+` WHERE m.tenant_id = $1 AND m.link_id = $2 AND m.pair_id = $3 AND m.hidden_at IS NULL`, tenant, link, pair))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil, fmt.Errorf("%w: no such message", ErrNotFound)
	}
	if err != nil {
		return m, nil, err
	}
	if m.Kind == "card" {
		return m, nil, fmt.Errorf("%w: a document's card is not forwarded; open the document instead", ErrInvalid)
	}
	if m.Attachment == nil {
		return m, nil, nil
	}
	a, err := AttachmentTx(ctx, tx, tenant, link, pair)
	if err != nil {
		return m, nil, err
	}
	return m, &a, nil
}

// TypingTx says the business in scope is typing in its conversation with
// a link, for a few seconds (not through the door: no words, not
// audited).
func TypingTx(ctx context.Context, tx pgx.Tx, link uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT taskiem_link_typing($1)`, link)
	return err
}

// TypingOfTx reports whether the other side of a link is typing now.
func TypingOfTx(ctx context.Context, tx pgx.Tx, link uuid.UUID) (bool, error) {
	var typing bool
	err := tx.QueryRow(ctx, `SELECT taskiem_link_typing_of($1)`, link).Scan(&typing)
	return typing, err
}
