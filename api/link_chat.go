package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
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
	"github.com/israel-duff/taskiem/engine/media"
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
	r.With(write, s.tenantWide).Post("/link-chats/{link}/files", s.sendLinkFile)
	r.Get("/link-chats/{link}/files/{id}", s.linkFile)
	r.Post("/link-chats/{link}/read", s.readLinkChat)
	r.With(write, s.tenantWide).Post("/link-chats/{link}/typing", s.linkTyping)
	r.With(write, s.tenantWide).Post("/link-chats/{link}/forward", s.forwardLinkMessage)
	r.With(write, s.tenantWide).Post("/link-chats/{link}/messages/{id}/hide", s.hideLinkMessage)
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
	// Typing: someone on the other side is typing now.
	Typing bool `json:"typing,omitempty"`
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
		if out.Messages, err = links.MessagesTx(r.Context(), tx, principalFrom(r.Context()).TenantID, link, p); err != nil {
			return err
		}
		out.Typing, err = links.TypingOfTx(r.Context(), tx, link)
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
	m, err := s.sendLink(r.Context(), p, link, in.Text, links.SendOpts{ReplyTo: in.ReplyTo})
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
func (s *Server) sendLink(ctx context.Context, p *Principal, link uuid.UUID, text string, o links.SendOpts) (links.Message, error) {
	// The first link's preview, fetched by our side before the message is
	// written (link_preview.go); a page that does not answer has none.
	if o.Preview == nil {
		o.Preview = s.linkPreview(ctx, p.TenantID, text)
	}
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
		m, other, err = links.SendWithTx(ctx, tx, p.TenantID, link, text, links.Sender{ID: p.Actor(), FirstName: firstWord(name)}, actorOf(p), o)
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
		s.tellHoldersSent(ctx, to, []string{PermChatTrade}, "msg:"+m.PairID.String(), "/chat?with="+link.String(), func(l lang.Tag) (string, string) {
			body := m.Body
			if a := m.Attachment; a != nil {
				what := tr(l, "chat.link."+a.Kind)
				if a.Kind == "file" && a.Name != "" {
					what = a.Name
				}
				body = strings.TrimSpace("(" + what + ") " + body)
			}
			if m.SenderName != "" {
				body = m.SenderName + ": " + body
			}
			if m.Reply != nil && m.Reply.Card != nil {
				body = tr(l, "chat.link.about", "reference", m.Reply.Card.Reference) + " " + body
			}
			return "[" + whatsapp.SafeText(from) + "]", whatsapp.SafeText(body)
		}, nil, s.keepSent(ctx, to, link, m.PairID))
	})
}

// chatAddrHash is the keyed hash a person's chat address is kept by.
func (s *Server) chatAddrHash(tenant uuid.UUID, channel, addr string) []byte {
	m := hmac.New(sha256.New, s.linkKey(tenant))
	m.Write([]byte("taskiem/chat-sent/v1\x00" + channel + "\x00" + addr))
	return m.Sum(nil)
}

// waQuotedReply takes a reply quoting a conversation's message on the
// phone into that conversation, quoting it, for whichever of the person's
// businesses it was told to (done false when the quoted message is none).
func (s *Server) waQuotedReply(ctx context.Context, c *chat) (bool, error) {
	text := strings.TrimSpace(c.in.Text)
	if c.in.Quoted == "" || c.in.Reply != "" || text == "" {
		return false, nil
	}
	tenants := append([]tenantRef{c.tenant}, slices.DeleteFunc(slices.Clone(c.tenants), func(t tenantRef) bool { return t.ID == c.tenant.ID })...)
	for _, t := range tenants {
		if t.ID == uuid.Nil {
			continue
		}
		var link, pair uuid.UUID
		var ok bool
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			var err error
			link, pair, ok, err = links.SentTx(ctx, tx, c.ch.Name(), s.chatAddrHash(t.ID, c.ch.Name(), c.addr), c.in.Quoted)
			return err
		}); err != nil {
			return true, err
		}
		if !ok {
			continue
		}
		p, err := s.principalOf(ctx, t.ID, c.p.UserID)
		if err != nil || !p.Can(PermChatTrade) {
			return true, c.say(ctx, s, c.t("chat.link.denied", "org", t.Name))
		}
		var name string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			l, err := links.GetTx(ctx, tx, link)
			name = l.Name
			return err
		}); err != nil {
			return true, err
		}
		if _, err := s.sendLink(ctx, p, link, text, links.SendOpts{ReplyTo: &pair}); err != nil {
			if errors.Is(err, links.ErrInvalid) || errors.Is(err, links.ErrState) {
				return true, c.say(ctx, s, c.t("chat.link.not_sent", "reason", strings.TrimPrefix(strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "), links.ErrState.Error()+": ")))
			}
			return true, err
		}
		return true, c.say(ctx, s, c.t("chat.link.sent", "name", whatsapp.SafeText(name)))
	}
	return false, nil
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
		return auditTx(r, tx, "link.conversation", link.String(), map[string]any{"muted": in.Muted, "archived": in.Archived, "pinned": in.Pinned})
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "muted": in.Muted, "archived": in.Archived, "pinned": in.Pinned})
}

// --- cards ---

// cardSide is one side's card of a document event.
type cardSide struct {
	tenant uuid.UUID
	mine   bool
	card   links.DocCard
}

// postCards writes a document event as a card into both sides'
// conversations, each in its own scope, the same message on both; it
// answers the card's id.
func (s *Server) postCards(ctx context.Context, link uuid.UUID, sides ...cardSide) uuid.UUID {
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
	return pair
}

// keepSent is a tellHoldersSent hearing that keeps what was sent as the
// conversation's message pair, so a reply quoting it on the phone goes
// into the conversation.
func (s *Server) keepSent(ctx context.Context, tenant, link, pair uuid.UUID) func(channel, addr, id string) {
	return func(channel, addr, id string) {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return links.RecordSentTx(ctx, tx, tenant, channel, s.chatAddrHash(tenant, channel, addr), id, link, pair)
		}); err != nil {
			s.Logger.Warn("links: keeping a message sent to a phone", "tenant", tenant, "err", err)
		}
	}
}

// orderCards writes an order's event into both sides' conversations: the
// seller's order, the buyer's purchase order. It answers the card's id.
func (s *Server) orderCards(ctx context.Context, t links.Told) uuid.UUID {
	if t.Link == uuid.Nil {
		return uuid.Nil
	}
	seller, buyer := t.Tenant, t.FromTenant
	if !sellerTold(t.Event) {
		seller, buyer = t.FromTenant, t.Tenant
	}
	return s.postCards(ctx, t.Link,
		cardSide{tenant: seller, mine: seller == t.FromTenant, card: links.DocCard{Doc: links.DocOrder, Event: t.Event, Reference: t.Reference, ID: t.Order.String()}},
		cardSide{tenant: buyer, mine: buyer == t.FromTenant, card: links.DocCard{Doc: links.DocPurchase, Event: t.Event, Reference: t.Reference, ID: t.Purchase.String()}})
}

// billCards writes a bill's event into both sides' conversations: the
// seller's invoice, the buyer's bill (each opened by its number).
func (s *Server) billCards(ctx context.Context, link, seller, buyer uuid.UUID, reference, event string, sellerActed bool, amount *int64, note string) uuid.UUID {
	return s.postCards(ctx, link,
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
	if _, err := s.sendLink(ctx, c.p, to.ID, text, links.SendOpts{ReplyTo: replyTo}); err != nil {
		if errors.Is(err, links.ErrInvalid) || errors.Is(err, links.ErrState) {
			return true, c.say(ctx, s, c.t("chat.link.not_sent", "reason", strings.TrimPrefix(strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "), links.ErrState.Error()+": ")))
		}
		return true, err
	}
	return true, c.say(ctx, s, c.t("chat.link.sent", "name", whatsapp.SafeText(to.Name)))
}

// sendLinkFile is POST /v1/link-chats/{link}/files: a photo, a PDF or a
// voice note to a linked business, as a multipart form (file; text, a
// caption; reply_to). Each business keeps its own copy in its own
// storage: the file is stored in ours and in theirs before the message is
// written in both, and if either copy cannot be stored, nothing is sent.
func (s *Server) sendLinkFile(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Media == nil {
		s.fail(w, r, fmt.Errorf("%w: storage for files is not set up", errConflict))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		s.fail(w, r, fmt.Errorf("%w: send the file as a multipart form with a file field (at most %d MB)", errBadRequest, media.MaxFileBytes>>20))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: the form has no file", errBadRequest))
		return
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || len(data) == 0 {
		s.fail(w, r, fmt.Errorf("%w: the file could not be read", errBadRequest))
		return
	}
	var replyTo *uuid.UUID
	if v := r.FormValue("reply_to"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: reply_to is a message's id", errBadRequest))
			return
		}
		replyTo = &id
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	// The other side, before anything is stored: only an active link.
	var other uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		l, err := links.GetTx(ctx, tx, link)
		if err != nil {
			return err
		}
		if l.State != "active" {
			return fmt.Errorf("%w: files are sent to a linked business", links.ErrState)
		}
		other = l.Business
		return nil
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	mine, err := s.Media.PutChatFile(ctx, p.TenantID, data)
	if errors.Is(err, media.ErrFile) || errors.Is(err, media.ErrImage) {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, plainError(err)))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	drop := func(t uuid.UUID, path string) {
		if err := s.Media.Delete(context.WithoutCancel(ctx), t, media.PrivateBucket, []string{path}); err != nil {
			s.Logger.Warn("link chat: a file copy not removed", "tenant", t, "err", err)
		}
	}
	theirs, err := s.Media.PutChatFile(ctx, other, data)
	if err != nil {
		drop(p.TenantID, mine.Path)
		s.fail(w, r, fmt.Errorf("%w: the file could not be kept for the other business; nothing was sent", errConflict))
		return
	}
	name := hdr.Filename
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	att := func(f media.File) *links.Attachment {
		return &links.Attachment{Kind: media.ChatKind(f.Type), Type: f.Type, Size: f.Size, Name: name, Path: f.Path}
	}
	m, err := s.sendLink(ctx, p, link, r.FormValue("text"), links.SendOpts{ReplyTo: replyTo, Ours: att(mine), Theirs: att(theirs)})
	if err != nil {
		drop(p.TenantID, mine.Path)
		drop(other, theirs.Path)
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, linkMessageOut{Message: m})
}

// linkFile is GET /v1/link-chats/{link}/files/{id}: this side's copy of a
// message's photo, PDF or voice note, by the message's id, streamed
// through the API (the web app shows files from its own origin only).
func (s *Server) linkFile(w http.ResponseWriter, r *http.Request) {
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
	if s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var a links.Attachment
	if err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		a, err = links.AttachmentTx(r.Context(), tx, p.TenantID, link, pair)
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	data, ctype, err := s.Media.GetFile(r.Context(), p.TenantID, a.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if a.Type != "" {
		ctype = a.Type
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	disp := "inline"
	if a.Kind == "file" {
		disp = "attachment"
	}
	if a.Name != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": a.Name}))
	} else {
		w.Header().Set("Content-Disposition", disp)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// linkTyping is POST /v1/link-chats/{link}/typing: we are typing in the
// conversation, for the other side's typing line, for a few seconds.
func (s *Server) linkTyping(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error { return links.TypingTx(r.Context(), tx, link) }); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hideLinkMessage is POST /v1/link-chats/{link}/messages/{id}/hide:
// delete for me. Our copy is hidden from our view; the other side's
// stays, and a document's card is never hidden.
func (s *Server) hideLinkMessage(w http.ResponseWriter, r *http.Request) {
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
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.HideTx(r.Context(), tx, p.TenantID, link, pair); err != nil {
			return err
		}
		return auditTx(r, tx, "link.message.hide", link.String(), map[string]any{"message": pair})
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxForward is how many conversations a message is forwarded to at once,
// as WhatsApp's.
const maxForward = 5

// forwardLinkMessage is POST /v1/link-chats/{link}/forward: one of our
// messages in this conversation, forwarded to other linked businesses'
// conversations ({message, to: [link ids]}), marked Forwarded. A file
// goes as a copy into each one's own storage and ours, from our copy; a
// document's card is not forwarded.
func (s *Server) forwardLinkMessage(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Message uuid.UUID   `json:"message"`
		To      []uuid.UUID `json:"to"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	slices.SortFunc(in.To, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	in.To = slices.Compact(in.To)
	if len(in.To) == 0 || len(in.To) > maxForward || slices.Contains(in.To, link) {
		s.fail(w, r, fmt.Errorf("%w: forward to 1 to %d other conversations", errBadRequest, maxForward))
		return
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	var m links.Message
	var file *links.Attachment
	targets := map[uuid.UUID]uuid.UUID{} // link -> its business
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if m, file, err = links.ForwardableTx(ctx, tx, p.TenantID, link, in.Message); err != nil {
			return err
		}
		for _, to := range in.To {
			l, err := links.GetTx(ctx, tx, to)
			if err != nil {
				return err
			}
			if l.State != links.Active {
				return fmt.Errorf("%w: you are no longer linked with %s", links.ErrState, l.Name)
			}
			targets[to] = l.Business
		}
		return nil
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	var data []byte
	if file != nil {
		if s.Media == nil {
			s.fail(w, r, fmt.Errorf("%w: storage for files is not set up", errConflict))
			return
		}
		if data, _, err = s.Media.GetFile(ctx, p.TenantID, file.Path); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	out := []links.Message{}
	for _, to := range in.To {
		o := links.SendOpts{Forwarded: true, Preview: m.Preview}
		var kept []func()
		if file != nil {
			att := func(t uuid.UUID) (*links.Attachment, error) {
				f, err := s.Media.PutChatFile(ctx, t, data)
				if err != nil {
					return nil, err
				}
				kept = append(kept, func() {
					if err := s.Media.Delete(context.WithoutCancel(ctx), t, media.PrivateBucket, []string{f.Path}); err != nil {
						s.Logger.Warn("link chat: a forwarded copy not removed", "tenant", t, "err", err)
					}
				})
				return &links.Attachment{Kind: file.Kind, Type: f.Type, Size: f.Size, Name: file.Name, Path: f.Path}, nil
			}
			var err1, err2 error
			o.Ours, err1 = att(p.TenantID)
			if err1 == nil {
				o.Theirs, err2 = att(targets[to])
			}
			if err1 != nil || err2 != nil {
				for _, undo := range kept {
					undo()
				}
				s.fail(w, r, fmt.Errorf("%w: the file could not be kept for every business; forwarded to %d of %d", errConflict, len(out), len(in.To)))
				return
			}
		}
		sent, err := s.sendLink(ctx, p, to, m.Body, o)
		if err != nil {
			for _, undo := range kept {
				undo()
			}
			s.fail(w, r, linkMessageErr(err))
			return
		}
		out = append(out, sent)
	}
	writeJSON(w, http.StatusCreated, linkMessagesOut{Messages: out})
}
