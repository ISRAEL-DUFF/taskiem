package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The conversation between linked businesses, L3 (docs/spec/business-links.md
// §5.5, §8): the web's door to it (the Chat page), the cards Taskiem
// writes into both sides as documents move, telling the other side's
// people on their phones, and "ask Dangote …" from chat.

// PermChatTrade writes in conversations with linked businesses; every
// member reads them.
const PermChatTrade = access.ChatTrade

func (s *Server) linkChatRoutes(r chi.Router) {
	write := s.need(PermChatTrade)
	r.Get("/link-chats", s.listLinkChats)
	r.Get("/link-chats/search", s.searchLinkChats)
	r.Get("/link-chats/thread", s.linkThread)
	r.Get("/link-chats/{link}/messages", s.linkMessages)
	r.With(write, s.tenantWide).Post("/link-chats/{link}/messages", s.sendLinkMessage)
	r.Post("/link-chats/{link}/read", s.readLinkChat)
	r.With(write, s.tenantWide).Put("/link-chats/{link}", s.putLinkChat)
}

func linkParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "link"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

type linkChatsOut struct {
	Conversations []links.Conversation `json:"conversations"`
}

// listLinkChats is GET /v1/link-chats: a conversation per linked business,
// the newest first, with its last message and unread count.
func (s *Server) listLinkChats(w http.ResponseWriter, r *http.Request) {
	var out linkChatsOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Conversations, err = links.ConversationsTx(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type linkMessagesOut struct {
	Messages []links.Message `json:"messages"`
}

// linkMessages is GET /v1/link-chats/{link}/messages: a page of the
// conversation, oldest first: ?before= (older), ?after= (newer, for the
// open conversation to follow), ?q= (the messages saying it).
func (s *Server) linkMessages(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var p links.Page
	for k, to := range map[string]**time.Time{"before": &p.Before, "after": &p.After} {
		if v := r.URL.Query().Get(k); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				s.fail(w, r, fmt.Errorf("%w: %s is a time (RFC 3339)", errBadRequest, k))
				return
			}
			*to = &t
		}
	}
	p.Q = r.URL.Query().Get("q")
	if v := r.URL.Query().Get("limit"); v != "" {
		p.Limit, _ = strconv.Atoi(v)
	}
	var out linkMessagesOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		var err error
		out.Messages, err = links.MessagesTx(r.Context(), tx, principalFrom(r.Context()).TenantID, link, p)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// searchLinkChats is GET /v1/link-chats/search?q=: messages saying it,
// across conversations.
func (s *Server) searchLinkChats(w http.ResponseWriter, r *http.Request) {
	var out linkMessagesOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Messages, err = links.SearchMessagesTx(r.Context(), tx, principalFrom(r.Context()).TenantID, r.URL.Query().Get("q"), 20)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// linkThread is GET /v1/link-chats/thread?doc=bill&reference=INV-0042: a
// document's thread, its cards and the replies to them.
func (s *Server) linkThread(w http.ResponseWriter, r *http.Request) {
	doc, ref := r.URL.Query().Get("doc"), strings.TrimSpace(r.URL.Query().Get("reference"))
	switch doc {
	case links.DocOrder, links.DocPurchase, links.DocInvoice, links.DocBill:
	default:
		s.fail(w, r, fmt.Errorf("%w: doc is order, purchase, invoice or bill", errBadRequest))
		return
	}
	var out linkMessagesOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Messages, err = links.ThreadTx(r.Context(), tx, principalFrom(r.Context()).TenantID, doc, ref)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type linkMessageOut struct {
	Message links.Message `json:"message"`
}

// sendLinkMessage is POST /v1/link-chats/{link}/messages: a message to a
// linked business, written in both businesses at once; reply_to quotes a
// message (a card's: a reply about its document).
func (s *Server) sendLinkMessage(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Text    string     `json:"text"`
		ReplyTo *uuid.UUID `json:"reply_to"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	m, err := s.sendLink(r.Context(), p, link, in.Text, in.ReplyTo)
	if err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, linkMessageOut{Message: m})
}

func linkMessageErr(err error) error {
	switch {
	case errors.Is(err, links.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "))
	case errors.Is(err, links.ErrState):
		return fmt.Errorf("%w: %s", errConflict, strings.TrimPrefix(err.Error(), links.ErrState.Error()+": "))
	case errors.Is(err, links.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

// sendLink sends a message as the person (the web, the API, chat) and
// tells the other side's people on their phones.
func (s *Server) sendLink(ctx context.Context, p *Principal, link uuid.UUID, text string, replyTo *uuid.UUID) (links.Message, error) {
	var m links.Message
	var other uuid.UUID
	var from string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var name string
		if p.UserID != uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&from); err != nil {
			return err
		}
		var err error
		m, other, err = links.SendTx(ctx, tx, p.TenantID, link, text, replyTo, links.Sender{ID: p.Actor(), FirstName: firstWord(name)}, actorOf(p))
		return err
	})
	if err != nil {
		return m, err
	}
	s.tellLinkMessage(ctx, other, link, from, m)
	return m, nil
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// tellLinkMessage tells the people of the business a message went to who
// write in its conversations, on their phones, unless it muted the
// conversation: "[Dangote Stores] Musa: …".
func (s *Server) tellLinkMessage(ctx context.Context, to, link uuid.UUID, from string, m links.Message) {
	s.background(ctx, "link message", func(ctx context.Context) {
		var muted bool
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{to}, func(tx pgx.Tx) error {
			var err error
			muted, err = links.MutedTx(ctx, tx, to, link)
			return err
		}); err != nil || muted {
			return
		}
		s.tellHolders(ctx, to, []string{PermChatTrade}, "msg:"+m.PairID.String(), "/chat?with="+link.String(), func(l lang.Tag) (string, string) {
			body := m.Body
			if m.SenderName != "" {
				body = m.SenderName + ": " + body
			}
			if m.Reply != nil && m.Reply.Card != nil {
				body = tr(l, "chat.link.about", "reference", m.Reply.Card.Reference) + " " + body
			}
			return "[" + whatsapp.SafeText(from) + "]", whatsapp.SafeText(body)
		})
	})
}

// readLinkChat is POST /v1/link-chats/{link}/read: the conversation's
// messages read, and the other side's double tick.
func (s *Server) readLinkChat(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var n int
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		var err error
		n, err = links.MarkReadTx(r.Context(), tx, p.TenantID, link, actorOf(p))
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"read": n})
}

// putLinkChat is PUT /v1/link-chats/{link}: the business mutes or
// archives its conversation with a linked business (what it names).
func (s *Server) putLinkChat(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in links.ConversationSettings
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.SetConversationTx(r.Context(), tx, p.TenantID, link, in, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.conversation", link.String(), map[string]any{"muted": in.Muted, "archived": in.Archived})
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "muted": in.Muted, "archived": in.Archived})
}

// --- cards ---

// cardSide is one side's card of a document event.
type cardSide struct {
	tenant uuid.UUID
	mine   bool
	card   links.DocCard
}

// postCards writes a document event as a card into both sides'
// conversations, each in its own scope, the same message on both.
func (s *Server) postCards(ctx context.Context, link uuid.UUID, sides ...cardSide) {
	pair := uuid.Must(uuid.NewV7())
	for _, c := range sides {
		if c.tenant == uuid.Nil {
			continue
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
			return links.CardTx(ctx, tx, c.tenant, link, pair, c.mine, c.card)
		}); err != nil {
			s.Logger.Warn("links: a card in the conversation", "tenant", c.tenant, "err", err)
		}
	}
}

// orderCards writes an order's event into both sides' conversations: the
// seller's order, the buyer's purchase order.
func (s *Server) orderCards(ctx context.Context, t links.Told) {
	if t.Link == uuid.Nil {
		return
	}
	seller, buyer := t.Tenant, t.FromTenant
	if !sellerTold(t.Event) {
		seller, buyer = t.FromTenant, t.Tenant
	}
	s.postCards(ctx, t.Link,
		cardSide{tenant: seller, mine: seller == t.FromTenant, card: links.DocCard{Doc: links.DocOrder, Event: t.Event, Reference: t.Reference, ID: t.Order.String()}},
		cardSide{tenant: buyer, mine: buyer == t.FromTenant, card: links.DocCard{Doc: links.DocPurchase, Event: t.Event, Reference: t.Reference, ID: t.Purchase.String()}})
}

// billCards writes a bill's event into both sides' conversations: the
// seller's invoice, the buyer's bill (each opened by its number).
func (s *Server) billCards(ctx context.Context, link, seller, buyer uuid.UUID, reference, event string, sellerActed bool, amount *int64, note string) {
	s.postCards(ctx, link,
		cardSide{tenant: seller, mine: sellerActed, card: links.DocCard{Doc: links.DocInvoice, Event: event, Reference: reference, ID: reference, AmountKobo: amount, Note: note}},
		cardSide{tenant: buyer, mine: !sellerActed, card: links.DocCard{Doc: links.DocBill, Event: event, Reference: reference, ID: reference, AmountKobo: amount, Note: note}})
}

// --- from the phone ---

// waAsk answers "ask Dangote …" (a message to a linked business, by its
// name) and "ask Dangote about INV-0042 …" (a reply about that document).
// Words naming no linked business are not its (done false).
func (s *Server) waAsk(ctx context.Context, c *chat, rest string) (bool, error) {
	var list []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return true, err
	}
	words := strings.Fields(rest)
	// The longest run of words that names one linked business.
	var to links.Link
	n := 0
	for i := len(words); i > 0 && n == 0; i-- {
		name := strings.Join(words[:i], " ")
		var hit []links.Link
		for _, l := range list {
			if l.State == links.Active && strings.HasPrefix(strings.ToLower(l.Name), strings.ToLower(name)) {
				hit = append(hit, l)
			}
		}
		if len(hit) == 1 {
			to, n = hit[0], i
		}
	}
	if n == 0 {
		return false, nil
	}
	if !c.p.Can(PermChatTrade) {
		return true, c.say(ctx, s, c.t("chat.link.denied", "org", c.tenant.Name))
	}
	text := strings.Join(words[n:], " ")
	var replyTo *uuid.UUID
	if len(words) > n+1 && strings.EqualFold(words[n], "about") {
		ref := strings.ToUpper(strings.Trim(words[n+1], ".,:"))
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			for _, doc := range []string{links.DocBill, links.DocInvoice, links.DocPurchase, links.DocOrder} {
				m, err := links.CardForTx(ctx, tx, c.tenant.ID, doc, ref)
				if err != nil {
					return err
				}
				if m != nil && m.LinkID == to.ID {
					replyTo = &m.PairID
					return nil
				}
			}
			return nil
		}); err != nil {
			return true, err
		}
		if replyTo != nil {
			text = strings.Join(words[n+2:], " ")
		}
	}
	if strings.TrimSpace(text) == "" {
		return true, c.say(ctx, s, c.t("chat.link.ask_what", "name", whatsapp.SafeText(to.Name)))
	}
	if _, err := s.sendLink(ctx, c.p, to.ID, text, replyTo); err != nil {
		if errors.Is(err, links.ErrInvalid) || errors.Is(err, links.ErrState) {
			return true, c.say(ctx, s, c.t("chat.link.not_sent", "reason", strings.TrimPrefix(strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "), links.ErrState.Error()+": ")))
		}
		return true, err
	}
	return true, c.say(ctx, s, c.t("chat.link.sent", "name", whatsapp.SafeText(to.Name)))
}
