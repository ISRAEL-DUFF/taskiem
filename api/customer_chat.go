package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/voice"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Customers' conversations (docs/spec/chat-five-more-flows.md, flow 1
// step 1, phase 1). Someone who is not on the business's team, a rider or
// a linked business, writing to the business's own WhatsApp number, is a
// customer: what they write is a thread in the chat, one per customer
// number per business, and staff who handle orders read and answer it
// there. The answer goes from the business's number: as words within 24
// hours of the customer's last message, as the approved template after.
// The customer's number and every message are sealed under the
// customer's own subject (the phone's, as their invoices are).

// customerReplyMax is the longest reply a member may send (WhatsApp takes
// 4,096 characters in a text; a template's variable far less).
const customerReplyMax = 2000

// customerThreadTx is the thread for number on wa's own number, made when
// it is the customer's first message: its id and the customer's subject.
// The name they gave WhatsApp, when it came with the message, is kept
// (sealed) as what to call them until the business's book names them.
func (s *Server) customerThreadTx(ctx context.Context, tx pgx.Tx, wa *whatsapp.Platform, number, profile string) (uuid.UUID, string, error) {
	tenant := wa.Tenant
	phone := "+" + strings.TrimPrefix(number, "+")
	env, err := s.Store.PII.SealTx(ctx, tx, tenant, "phone", phone)
	if err != nil {
		return uuid.Nil, "", err
	}
	subject, _ := env["subject"].(string)
	if subject == "" {
		return uuid.Nil, "", errors.New("customer chat: the number has no subject")
	}
	whoMap := map[string]any{"number": env}
	profile = strings.Join(strings.Fields(profile), " ")
	if r := []rune(profile); len(r) > 80 {
		profile = string(r[:80])
	}
	if profile != "" {
		if whoMap["name"], err = pii.SealText(ctx, s.Store.PII, tx, tenant, subject, "name", profile); err != nil {
			return uuid.Nil, "", err
		}
	}
	who, err := json.Marshal(whoMap)
	if err != nil {
		return uuid.Nil, "", err
	}
	id := uuid.Must(uuid.NewV7())
	err = tx.QueryRow(ctx, `INSERT INTO customer_conversations (id, tenant_id, phone_number_id, subject_id, who)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, phone_number_id, subject_id) WHERE line = 'whatsapp' DO UPDATE
		SET who = CASE WHEN $6 THEN EXCLUDED.who ELSE customer_conversations.who END
		RETURNING id`, id, tenant, wa.Config.PhoneNumberID, subject, who, profile != "").Scan(&id)
	return id, subject, err
}

// riderOrSupplier reports whether number is one of the business's riders
// or suppliers: what they write that is not their words (off, busy, a
// delivery's) is not a customer's, and they are never made one.
func (s *Server) riderOrSupplier(ctx context.Context, tenant uuid.UUID, number string) (bool, error) {
	if !s.Reach.Ready() {
		return false, nil
	}
	hash := s.Reach.PhoneHash("+" + strings.TrimPrefix(number, "+"))
	var known bool
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM riders WHERE tenant_id = $1 AND phone_hash = $2 AND retired_at IS NULL)
			OR EXISTS (SELECT 1 FROM suppliers WHERE tenant_id = $1 AND phone_hash = $2 AND retired_at IS NULL)`, tenant, hash).Scan(&known)
	})
	return known, err
}

// phoneSubject is the subject a phone's details are sealed under, made
// when it has none yet.
func (s *Server) phoneSubject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, number string) (string, error) {
	phone := "+" + strings.TrimPrefix(number, "+")
	if v, ok := s.Store.PII.(interface {
		SubjectForTx(context.Context, pgx.Tx, uuid.UUID, string, any) (string, error)
	}); ok {
		return v.SubjectForTx(ctx, tx, tenant, "phone", phone)
	}
	env, err := s.Store.PII.SealTx(ctx, tx, tenant, "phone", phone)
	if err != nil {
		return "", err
	}
	subject, _ := env["subject"].(string)
	return subject, nil
}

// customerWords is what a customer sent that is not words: what it was,
// shown in the thread ("Photo", "Voice note").
func customerWords(in whatsapp.Inbound) (kind, text string) {
	switch {
	case in.Image != nil:
		return "media", strings.TrimSpace("Photo " + in.Text)
	case in.Media != nil:
		return "media", "Voice note"
	case in.Document != nil:
		return "media", strings.TrimSpace("File " + in.Text)
	case in.Location != "":
		return "media", "Location: " + in.Location
	}
	return "text", strings.TrimSpace(in.Text)
}

// customerStopStart takes STOP and START from a customer who has a thread
// with the business whose own number wa is: STOP deletes the thread's
// messages and sends nothing more until START. false when the words are
// neither, or no thread is theirs (the reach book answers then).
func (s *Server) customerStopStart(ctx context.Context, wa *whatsapp.Platform, number, text string) (bool, error) {
	if !wa.Own() {
		return false, nil
	}
	word := strings.Trim(strings.ToLower(strings.TrimSpace(text)), "/ .!")
	if word != "stop" && word != "unsubscribe" && word != "start" {
		return false, nil
	}
	var name string
	found := false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		subject, err := s.phoneSubject(ctx, tx, wa.Tenant, number)
		if err != nil {
			return err
		}
		var id uuid.UUID
		var stopped *time.Time
		err = tx.QueryRow(ctx, `SELECT id, stopped_at FROM customer_conversations WHERE tenant_id = $1 AND phone_number_id = $2 AND subject_id = $3`,
			wa.Tenant, wa.Config.PhoneNumberID, subject).Scan(&id, &stopped)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// START from someone who never said STOP is not theirs to answer
		// here (the reach book's START, if any).
		if word == "start" && stopped == nil {
			return nil
		}
		found = true
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, wa.Tenant).Scan(&name); err != nil {
			return err
		}
		if word == "start" {
			_, err = tx.Exec(ctx, `UPDATE customer_conversations SET stopped_at = NULL, last_inbound_at = now() WHERE id = $1`, id)
			return err
		}
		// Its orders being read go too (their words and address); orders
		// confirmed stay, as the business's records.
		if _, err := tx.Exec(ctx, `DELETE FROM order_drafts WHERE tenant_id = $1 AND conversation_id = $2`, wa.Tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM customer_messages WHERE tenant_id = $1 AND conversation_id = $2`, wa.Tenant, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE customer_conversations SET stopped_at = now(), last_inbound_at = now(), last_message_at = now() WHERE id = $1`, id)
		return err
	})
	if err != nil || !found {
		return found, err
	}
	// The business's other messages to them (updates, reminders) stop and
	// start with it: STOP is to the business, whatever it sends.
	if s.Reach.Ready() {
		var rerr error
		if word == "start" {
			_, rerr = s.Reach.Resume(ctx, reach.WhatsApp, number)
		} else {
			_, rerr = s.Reach.Stop(ctx, reach.WhatsApp, number)
		}
		if rerr != nil {
			s.Logger.Warn("customer chat: the reach book's stop or start", "err", rerr)
		}
	}
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	if word == "start" {
		return true, s.waSay(ctx, wa, number, "", tr(l, "customer.resumed", "business", name))
	}
	return true, s.waSay(ctx, wa, number, "", tr(l, "customer.stopped", "business", name))
}

// customerInbound keeps what a customer wrote to the business's own
// number in their thread; false on the shared number. Nothing is sent
// back: the business answers from the chat.
func (s *Server) customerInbound(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) (bool, error) {
	if !wa.Own() {
		return false, nil
	}
	if known, err := s.riderOrSupplier(ctx, wa.Tenant, number); err != nil || known {
		return false, err
	}
	kind, text := customerWords(in)
	if text == "" {
		return true, nil
	}
	if r := []rune(text); len(r) > 4000 {
		text = string(r[:4000])
	}
	type keptMessage struct {
		chat, message uuid.UUID
		text, subject string
		voice         *whatsapp.Media
	}
	var kept *keptMessage
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		id, subject, err := s.customerThreadTx(ctx, tx, wa, number, in.ProfileName)
		if err != nil {
			return err
		}
		body, err := pii.SealText(ctx, s.Store.PII, tx, wa.Tenant, subject, "other", text)
		if err != nil {
			return err
		}
		var waID *string
		if in.ID != "" {
			waID = &in.ID
		}
		msg := uuid.Must(uuid.NewV7())
		tag, err := tx.Exec(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, wa_id)
			VALUES ($1, $2, $3, false, $4, $5, $6) ON CONFLICT DO NOTHING`,
			msg, wa.Tenant, id, kind, body, waID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		switch {
		case kind == "text":
			kept = &keptMessage{chat: id, message: msg, text: text}
		case in.Media != nil && in.Image == nil && in.Document == nil:
			// A voice note: put in words after, where voice notes are on.
			kept = &keptMessage{chat: id, message: msg, subject: subject, voice: in.Media}
		}
		// A message after STOP is theirs to send; it does not undo STOP.
		if _, err = tx.Exec(ctx, `UPDATE customer_conversations SET last_inbound_at = now(), last_message_at = now(), archived = false WHERE id = $1`, id); err != nil {
			return err
		}
		// One customer across the app and WhatsApp (§11): the number may be
		// the verified phone of a person chatting with the business in the
		// app. Joined, the message shows in their copy, and they wait for an
		// answer on the joined thread (Needs you once per customer). In a
		// savepoint: if joining fails, the message is kept all the same.
		bell := id
		s.savepoint(ctx, tx, "customer chat: joining the lines", func(sp pgx.Tx) error {
			app, person, fresh, err := s.joinWhatsAppTx(ctx, sp, wa.Tenant, id, "+"+strings.TrimPrefix(number, "+"))
			if err != nil || app == nil {
				return err
			}
			bell = *app
			if _, err := sp.Exec(ctx, `UPDATE customer_conversations SET archived = false,
				wait_check_at = CASE WHEN waiting_since IS NULL THEN NULL ELSE wait_check_at END, waiting_since = COALESCE(waiting_since, now())
				WHERE tenant_id = $1 AND id = $2`, wa.Tenant, *app); err != nil {
				return err
			}
			if person == nil || fresh {
				return nil
			}
			return s.mirrorWhatsAppTx(ctx, sp, wa.Tenant, *person, msg, true, text, "", person.String(), "person")
		})
		// The bell's notice in a savepoint: if telling fails, the message
		// is kept all the same.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := s.tellCustomerMessageTx(ctx, sp, wa.Tenant, bell); err != nil {
			s.Logger.Warn("customer chat: telling the bell", "err", err)
			return sp.Rollback(ctx)
		}
		return sp.Commit(ctx)
	})
	if err == nil && kept != nil {
		// Read for an order (phase 2), away from the webhook: a voice note
		// put in words first.
		read := func() {
			text := kept.text
			if kept.voice != nil {
				if text = s.customerVoice(wa, kept.chat, kept.message, kept.subject, kept.voice); text == "" {
					return
				}
			}
			s.readCustomerOrder(wa.Tenant, kept.chat, kept.message, text)
		}
		if s.OrderReadInline {
			read()
		} else {
			go read()
		}
	}
	return true, err
}

// tellCustomerMessageTx tells the people who answer customers' chats that a
// customer wrote (docs/spec/mobile-app-m4.md N2): a bell item per
// conversation while it is unread ("Ade wrote", then "Ade wrote 3 messages"),
// in the message's own transaction, which also queues its push (migration
// 00446). WhatsApp is not told: the customer's own chat is the business's.
// Nothing of theirs is copied (their words, their number, the name the
// business keeps for them, all kept sealed): the bell and the push would
// keep it in the clear. The chat, opened, says who.
func (s *Server) tellCustomerMessageTx(ctx context.Context, tx pgx.Tx, tenant, chat uuid.UUID) error {
	var muted bool
	if err := tx.QueryRow(ctx, `SELECT muted FROM customer_conversations WHERE id = $1`, chat).Scan(&muted); err != nil || muted {
		return err
	}
	users, err := holdersTx(ctx, tx, PermOrders)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		// No one handles orders: the owner is told (taskiem-for-everyone §8.6).
		if users, err = ownersTx(ctx, tx); err != nil {
			return err
		}
	}
	return notices.AddTx(ctx, tx, tenant, users, notices.Notice{Kind: notices.Setup, Title: "A customer wrote", Many: "A customer wrote {n} messages",
		Link: "/chat?with=c-" + chat.String(), Group: "customer:" + chat.String()})
}

// --- the API ---

func (s *Server) customerChatRoutes(r chi.Router) {
	orders := s.need(PermOrders)
	r.With(orders).Get("/customer-chats", s.listCustomerChats)
	r.With(orders).Get("/customer-chats/unread", s.customerChatsUnread)
	r.With(orders).Get("/customer-chats/search", s.searchCustomerChats)
	r.With(orders).Get("/customer-chats/{id}", s.getCustomerChat)
	r.With(orders).Get("/customer-chats/{id}/messages", s.customerMessages)
	r.With(orders, s.tenantWide).Post("/customer-chats/{id}/messages", s.replyCustomer)
	r.With(orders).Post("/customer-chats/{id}/read", s.readCustomerChat)
	r.With(orders, s.tenantWide).Put("/customer-chats/{id}", s.putCustomerChat)
}

type customerChatView struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Phone       string     `json:"phone"`
	Last        string     `json:"last"`
	LastMine    bool       `json:"last_mine"`
	LastAt      time.Time  `json:"last_at"`
	Unread      int        `json:"unread"`
	WindowOpen  bool       `json:"window_open"`
	WindowUntil *time.Time `json:"window_until"`
	Stopped     bool       `json:"stopped"`
	Archived    bool       `json:"archived"`
	// Muted: no bell item and no push for it.
	Muted bool `json:"muted"`
	// Line is where the conversation runs: whatsapp (the business's own
	// number) or app (the Taskiem app or the browser chat,
	// taskiem-for-everyone §8.3). An app thread has no 24-hour window and
	// no template; Stopped there is the customer's Block, said in Notice.
	Line string `json:"line"`
	// Blocked: the business blocked this customer (app).
	Blocked bool `json:"blocked"`
	// Notice is what the thread says of itself ("This customer has
	// stopped messages").
	Notice string `json:"notice,omitempty"`
	// Offers is the customer's answer to offers (app): unasked, yes or no.
	Offers string `json:"offers,omitempty"`
	// Typing: the customer is typing now (app, one thread only).
	Typing *bool `json:"typing,omitempty"`
	// Lines are the customer's lines in this thread: one, or both once the
	// person in the app and the business's WhatsApp thread of their
	// verified phone are one customer (§11). ID and Line are then the app
	// thread's, which stands for the pair; the top-level window, stopped,
	// blocked and notice are those of ReplyLine.
	Lines []threadLine `json:"lines"`
	// ReplyLine is where a reply goes unless another line is chosen: the
	// line the customer last wrote on, or the other when that one cannot
	// take messages.
	ReplyLine string `json:"reply_line"`
	person    *uuid.UUID
	// linkedTo: a WhatsApp thread joined with this app thread; lastIn: when
	// the customer last wrote on this line; numberChanged: the customer's
	// app line moved to another number (§5.5).
	linkedTo      *uuid.UUID
	lastIn        *time.Time
	numberChanged *time.Time
	// The thread's page only: what the customer owes on open invoices.
	OwesKobo *int64 `json:"owes_kobo,omitempty"`
	Invoices *int   `json:"open_invoices,omitempty"`
}

type customerMessageView struct {
	ID         uuid.UUID `json:"id"`
	Mine       bool      `json:"mine"`
	Kind       string    `json:"kind"`
	Body       string    `json:"body"`
	SenderName string    `json:"sender_name,omitempty"`
	SentAs     string    `json:"sent_as,omitempty"`
	// A reply's way to the customer: sending, sent, delivered, read or
	// failed (with WhatsApp's reason).
	Delivery  string     `json:"delivery,omitempty"`
	Failure   string     `json:"failure,omitempty"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
	// An app thread's card (an order, a pay link, a booking offer, a
	// product), its photo, voice note or file, and whether it was an offer.
	Card       *customerCard   `json:"card,omitempty"`
	Attachment *chatAttachment `json:"attachment,omitempty"`
	Offer      bool            `json:"offer,omitempty"`
	// Line is where the message went: on WhatsApp (the business's own
	// number) or in the app (§11: one thread may hold both).
	Line string `json:"line,omitempty"`
}

// openCustomerText opens a sealed message or number, or "" when its
// customer was erased.
func (s *Server) openCustomerText(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, raw []byte) (string, error) {
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		var plain string
		if json.Unmarshal(raw, &plain) == nil {
			return plain, nil
		}
		return "", err
	}
	v, err := openText(ctx, s.Store.PII, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	t, _ := v.(string)
	return t, nil
}

// customerName is the customer's name in the business's book, else the
// name they gave WhatsApp, else their number.
func (s *Server) customerName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, profile, phone string) (string, error) {
	fallback := phone
	if profile != "" {
		fallback = profile
	}
	if s.Invoices == nil {
		return fallback, nil
	}
	c, err := s.Invoices.BookCustomerByCustomer(ctx, tx, tenant, subject)
	if errors.Is(err, invoicing.ErrNotFound) || (err == nil && c.Name == "") {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return c.Name, nil
}

const customerChatCols = `c.id, c.subject_id, c.who, c.last_inbound_at, c.last_message_at, c.stopped_at, c.archived, c.muted,
	c.line, c.person_id, c.person_blocked_at IS NOT NULL, c.blocked_at IS NOT NULL, c.linked_to, c.number_changed_at,
	(SELECT count(*) FROM customer_messages m WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id AND NOT m.mine AND m.read_at IS NULL),
	(SELECT row(m.mine, m.body) FROM customer_messages m WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id ORDER BY m.created_at DESC LIMIT 1)`

// customerChatRaw is a thread's row as read, before it is opened (the
// opening reads keys, which cannot share the connection with open rows).
type customerChatRaw struct {
	v        customerChatView
	subject  string
	who      []byte
	inbound  *time.Time
	stopped  *time.Time
	lastMine *bool
	lastBody []byte
	// personBlocked: the customer blocked the business (app).
	personBlocked bool
}

func scanCustomerChatRaw(r pgx.Row) (customerChatRaw, error) {
	var x customerChatRaw
	var last *struct {
		Mine bool
		Body []byte
	}
	if err := r.Scan(&x.v.ID, &x.subject, &x.who, &x.inbound, &x.v.LastAt, &x.stopped, &x.v.Archived, &x.v.Muted,
		&x.v.Line, &x.v.person, &x.personBlocked, &x.v.Blocked, &x.v.linkedTo, &x.v.numberChanged, &x.v.Unread, &last); err != nil {
		return x, err
	}
	x.v.lastIn = x.inbound
	if last != nil {
		x.lastMine, x.lastBody = &last.Mine, last.Body
	}
	return x, nil
}

// openCustomerChat opens a thread's number, name and last message, as a
// thread of its one line (combineLines joins two).
func (s *Server) openCustomerChat(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x customerChatRaw) (customerChatView, error) {
	v, err := s.openCustomerRow(ctx, tx, tenant, x)
	if err != nil {
		return v, err
	}
	v.Lines, v.ReplyLine = []threadLine{lineOf(v)}, v.Line
	return v, nil
}

// openCustomerRow opens one line's row.
func (s *Server) openCustomerRow(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x customerChatRaw) (customerChatView, error) {
	v := x.v
	var w struct {
		Number json.RawMessage `json:"number"`
		Name   json.RawMessage `json:"name"`
	}
	if err := json.Unmarshal(x.who, &w); err != nil {
		return v, err
	}
	phone, err := s.openCustomerText(ctx, tx, tenant, w.Number)
	if err != nil {
		return v, err
	}
	profile := ""
	if len(w.Name) > 0 {
		if profile, err = s.openCustomerText(ctx, tx, tenant, w.Name); err != nil {
			return v, err
		}
	}
	v.Phone = phone
	if v.Name, err = s.customerName(ctx, tx, tenant, x.subject, profile, phone); err != nil {
		return v, err
	}
	if v.Name == "" {
		v.Name = "A customer"
	}
	if x.lastMine != nil {
		v.LastMine = *x.lastMine
		if v.Last, err = s.openCustomerText(ctx, tx, tenant, x.lastBody); err != nil {
			return v, err
		}
	}
	v.Stopped = x.stopped != nil
	if v.Line == lineApp {
		// No window in the app: a reply is always a message, unless either
		// side blocked the other (§8.3).
		v.Stopped = x.personBlocked
		v.WindowOpen = !x.personBlocked && !v.Blocked
		if x.personBlocked {
			v.Notice = "This customer has stopped messages"
		}
		if v.person == nil {
			// The customer deleted their account (§14): the thread stays
			// the business's, and takes no more messages.
			v.Stopped, v.WindowOpen, v.Notice = true, false, customerLeftWords
		}
		return v, nil
	}
	if x.inbound != nil {
		until := x.inbound.Add(whatsapp.Window)
		v.WindowUntil = &until
		v.WindowOpen = time.Now().Before(until) && !v.Stopped
	}
	if v.numberChanged != nil && v.linkedTo == nil {
		v.Notice = customerNumberChangedWords
	}
	return v, nil
}

// listCustomerChats lists the business's customers' threads, the latest
// first (archived ones with ?archived=1).
func (s *Server) listCustomerChats(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	archived := r.URL.Query().Get("archived") == "1"
	out := []customerChatView{}
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		// One row per customer: a WhatsApp thread joined with an app thread
		// is shown with it (§11).
		var err error
		out, _, err = s.customerThreadsTx(r.Context(), tx, p.TenantID, `AND c.archived = $2`, []any{archived}, 200)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": out})
}

// customerChatIn reads the thread a request names, opened, with its
// subject; errCustomerChatNone when the business has no such thread.
var errCustomerChatNone = errors.New("no such customer conversation")

func (s *Server) customerChatIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idText string) (customerChatView, string, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return customerChatView{}, "", errCustomerChatNone
	}
	x, err := scanCustomerChatRaw(tx.QueryRow(ctx, `SELECT `+customerChatCols+` FROM customer_conversations c WHERE c.tenant_id = $1 AND c.id = $2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return customerChatView{}, "", errCustomerChatNone
	}
	if err != nil {
		return customerChatView{}, "", err
	}
	v, err := s.openCustomerChat(ctx, tx, tenant, x)
	return v, x.subject, err
}

func (s *Server) customerChatFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errCustomerChatNone) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.fail(w, r, err)
}

// getCustomerChat is one thread, with what the customer owes.
func (s *Server) getCustomerChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var v customerChatView
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		t, err := s.customerThreadIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		v = t.view
		// What they owe, under each line's subject (one phone's, unless
		// the number changed between them).
		var due int64
		var n int
		seen := map[string]bool{}
		for _, row := range []*customerRow{t.app, t.wa} {
			if row == nil || seen[row.subject] {
				continue
			}
			seen[row.subject] = true
			d, k, err := owedBy(r.Context(), tx, p.TenantID, row.subject)
			if err != nil {
				return err
			}
			due, n = due+d, n+k
		}
		v.OwesKobo, v.Invoices = &due, &n
		return appChatExtrasTx(r.Context(), tx, p.TenantID, &v)
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// customerMessages is a page of a thread, the latest last: 50 before
// ?before (a message's id), or the latest 50.
func (s *Server) customerMessages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := []customerMessageView{}
	more := false
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		// Both lines' messages, in time order, each marked with its line.
		t, err := s.customerThreadIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		lines := t.lineOfRow()
		q := `SELECT id, mine, kind, body, sender_name, COALESCE(sent_as, ''), COALESCE(delivery, ''), COALESCE(failure, ''), read_at, created_at, card, attachment, offer, conversation_id FROM customer_messages
			WHERE tenant_id = $1 AND conversation_id = ANY ($2)`
		args := []any{p.TenantID, t.ids()}
		if b, err := uuid.Parse(r.URL.Query().Get("before")); err == nil {
			q += ` AND (created_at, id) < (SELECT created_at, id FROM customer_messages WHERE tenant_id = $1 AND id = $3)`
			args = append(args, b)
		}
		rows, err := tx.Query(r.Context(), q+` ORDER BY created_at DESC, id DESC LIMIT 51`, args...)
		if err != nil {
			return err
		}
		type rawMsg struct {
			v    customerMessageView
			body []byte
			att  []byte
		}
		raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rawMsg, error) {
			var x rawMsg
			var conv uuid.UUID
			err := r.Scan(&x.v.ID, &x.v.Mine, &x.v.Kind, &x.body, &x.v.SenderName, &x.v.SentAs, &x.v.Delivery, &x.v.Failure, &x.v.ReadAt, &x.v.CreatedAt,
				&x.v.Card, &x.att, &x.v.Offer, &conv)
			x.v.Line = lines[conv]
			return x, err
		})
		if err != nil {
			return err
		}
		if len(raws) > 50 {
			more, raws = true, raws[:50]
		}
		for i := len(raws) - 1; i >= 0; i-- {
			x := raws[i]
			if x.v.Body, err = s.openCustomerText(r.Context(), tx, p.TenantID, x.body); err != nil {
				return err
			}
			if x.v.Attachment, err = s.openAttachment(r.Context(), tx, p.TenantID, x.att); err != nil {
				return err
			}
			out = append(out, x.v)
		}
		return nil
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "more": more})
}

// readCustomerChat marks the customer's messages read.
func (s *Server) readCustomerChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			return errCustomerChatNone
		}
		// Both lines of the customer's thread (§11).
		rows, err := tx.Query(r.Context(), `SELECT id FROM customer_conversations WHERE tenant_id = $1
			AND (id = $2 OR linked_to = $2 OR id = (SELECT x.linked_to FROM customer_conversations x WHERE x.tenant_id = $1 AND x.id = $2))`, p.TenantID, id)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return errCustomerChatNone
		}
		rows, err = tx.Query(r.Context(), `UPDATE customer_messages SET read_at = now()
			WHERE tenant_id = $1 AND conversation_id = ANY ($2) AND NOT mine AND read_at IS NULL RETURNING id`, p.TenantID, ids)
		if err != nil {
			return err
		}
		read, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(read) == 0 {
			return err
		}
		// In the app, the customer's copy shows them read (§8.3): their
		// app messages, and those from WhatsApp it shows.
		for _, thread := range ids {
			if err := s.mirrorBusinessReadTx(r.Context(), tx, p.TenantID, thread, read); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putCustomerChat archives a thread or brings it back, and mutes it or not.
func (s *Server) putCustomerChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Archived *bool `json:"archived"`
		Muted    *bool `json:"muted"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Archived == nil && req.Muted == nil {
		writeErr(w, http.StatusBadRequest, "archived or muted is required")
		return
	}
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			return errCustomerChatNone
		}
		// Both lines of the customer's thread (§11).
		tag, err := tx.Exec(r.Context(), `UPDATE customer_conversations SET archived = COALESCE($3, archived), muted = COALESCE($4, muted) WHERE tenant_id = $1
			AND (id = $2 OR linked_to = $2 OR id = (SELECT x.linked_to FROM customer_conversations x WHERE x.tenant_id = $1 AND x.id = $2))`,
			p.TenantID, id, req.Archived, req.Muted)
		if err == nil && tag.RowsAffected() == 0 {
			return errCustomerChatNone
		}
		return err
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// customerTemplateMax is the longest reply that can go after the 24
// hours: on one line, inside the approved template (WhatsApp takes 1,024
// characters in a variable; the rest of the template needs room).
const customerTemplateMax = 900

// replyCustomer sends a member's reply to the customer from the
// business's own number and keeps it in the thread (sendCustomer).
func (s *Server) replyCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var req struct {
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
		// In the app only: a rich reply (§8.3) and an offer (§15).
		Card  *cardAsk `json:"card"`
		Offer bool     `json:"offer"`
		// Line: whatsapp or app, for a customer with both (§11); left
		// out, the line they last wrote on.
		Line string `json:"line"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	switch req.Line {
	case "", lineApp, lineWhatsApp:
	default:
		writeErr(w, http.StatusBadRequest, "line is whatsapp or app")
		return
	}
	text := strings.TrimSpace(req.Text)
	var card *customerCard
	if req.Card != nil && req.Card.Type == "saved" {
		// A saved thing (saved_share.go): a card in the app; on WhatsApp, its
		// plain lines as the words, easy to copy.
		shared, inApp, err := s.savedForCustomer(r, chi.URLParam(r, "id"), req.Card.Saved, req.Line)
		if shareConfirmFail(w, err) {
			return
		}
		if err != nil {
			s.customerSendFail(w, r, err)
			return
		}
		if inApp {
			card = &customerCard{Type: "saved", Title: shared.Title, Saved: &shared}
			if text == "" {
				text = shared.Title
			}
		} else {
			text = strings.TrimSpace(text + "\n\n" + shared.Plain())
		}
		req.Card = nil
	}
	if req.Card != nil {
		var err error
		if card, err = s.cardFor(ctx, p.TenantID, chi.URLParam(r, "id"), *req.Card); err != nil {
			s.customerSendFail(w, r, err)
			return
		}
		if text == "" {
			text = card.Title
		}
	}
	if text == "" || len([]rune(text)) > customerReplyMax {
		writeErr(w, http.StatusBadRequest, "the reply is required (up to "+strconv.Itoa(customerReplyMax)+" characters)")
		return
	}
	if len(req.ClientID) > 80 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters")
		return
	}
	var sender string
	if p.UserID != uuid.Nil {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			var name string
			err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name)
			sender = firstWord(name)
			return err
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if !s.limiter("customer-reply:"+p.TenantID.String(), time.Second, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many replies at once; try again in a moment")
		return
	}
	m, fresh, err := s.sendCustomer(ctx, p.TenantID, chi.URLParam(r, "id"), customerSend{Text: text, ClientID: req.ClientID, SenderID: p.Actor(), SenderName: sender,
		Card: card, Offer: req.Offer, Line: req.Line, Default: req.Line == ""})
	if err != nil {
		s.customerSendFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !fresh {
		code = http.StatusOK
	}
	writeJSON(w, code, m)
}

// customerSend is a message to a customer: a member's reply, or the
// business's own words (an order, its questions, how it is going).
type customerSend struct {
	Text       string
	ClientID   string
	SenderID   string // the member, or "system:orders"
	SenderName string
	// In the app only (customer_app.go): a card, an offer (§15), a file.
	Card  *customerCard
	Offer bool
	File  *sentFile
	// Line is the customer's line it goes on (§11): whatsapp or app. Left
	// empty, the row named; with Default, the line they last wrote on.
	// Cards, offers and files go in the app.
	Line    string
	Default bool
	// pair: the rows of the customer's thread (both lines, §11), where a
	// client_id is looked for (set by sendCustomer).
	pair []uuid.UUID
}

// customerSendErr is a send refused, with the status and words to answer.
type customerSendErr struct {
	code int
	msg  string
}

func (e customerSendErr) Error() string { return e.msg }

func (s *Server) customerSendFail(w http.ResponseWriter, r *http.Request, err error) {
	var se customerSendErr
	if errors.As(err, &se) {
		writeErr(w, se.code, se.msg)
		return
	}
	s.customerChatFail(w, r, err)
}

// customerRepeat is the message a customer's thread already has under
// clientID, on either of its lines (chats: the thread's rows, §11): a
// client_id is one per customer, so a repeat after the reply line moved
// answers the first, on the line it went on (its Line).
func (s *Server) customerRepeat(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, chats []uuid.UUID, clientID string) (*customerMessageView, error) {
	if clientID == "" {
		return nil, nil
	}
	var m customerMessageView
	var body, att []byte
	err := tx.QueryRow(ctx, `SELECT m.id, m.mine, m.kind, m.body, m.sender_name, COALESCE(m.sent_as, ''), COALESCE(m.delivery, ''), COALESCE(m.failure, ''), m.read_at, m.created_at,
		m.card, m.attachment, m.offer, c.line FROM customer_messages m JOIN customer_conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
		WHERE m.tenant_id = $1 AND m.conversation_id = ANY ($2) AND m.client_id = $3 ORDER BY m.created_at LIMIT 1`, tenant, chats, clientID).
		Scan(&m.ID, &m.Mine, &m.Kind, &body, &m.SenderName, &m.SentAs, &m.Delivery, &m.Failure, &m.ReadAt, &m.CreatedAt, &m.Card, &att, &m.Offer, &m.Line)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if m.Body, err = s.openCustomerText(ctx, tx, tenant, body); err != nil {
		return nil, err
	}
	if m.Attachment, err = s.openAttachment(ctx, tx, tenant, att); err != nil {
		return nil, err
	}
	return &m, nil
}

// sendCustomer sends words to the customer of a thread from the business's
// own number and keeps them in the thread: as words within 24 hours of the
// customer's last message, as the approved template after (up to
// customerTemplateMax characters, refused longer rather than cut). The
// message is written (sending) before it goes, so two sends of one
// client_id cannot both go (the second answers the first, not fresh). When
// the business's number has changed since the customer wrote, the thread
// moves to the new number and the message goes from it. Refused after
// STOP, and when the customer was erased.
func (s *Server) sendCustomer(ctx context.Context, tenant uuid.UUID, threadID string, in customerSend) (customerMessageView, bool, error) {
	text := in.Text
	var chat customerChatView
	var subject, pnid, business string
	var again *customerMessageView
	var app *customerRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		t, err := s.customerThreadIn(ctx, tx, tenant, threadID)
		if err != nil {
			return err
		}
		// A repeat first, on either line, before the line is chosen: the
		// reply line may have moved since the first send.
		in.pair = t.ids()
		if again, err = s.customerRepeat(ctx, tx, tenant, in.pair, in.ClientID); err != nil || again != nil {
			return err
		}
		row := t.named
		line := in.Line
		if line == "" && (in.Card != nil || in.Offer || in.File != nil) && t.app != nil {
			line = lineApp
		}
		if line == "" && in.Default {
			line = t.view.ReplyLine
		}
		if line != "" {
			if row = t.row(line); row == nil {
				return noLineErr(line)
			}
		}
		chat, subject, app = row.view, row.subject, t.app
		if err := tx.QueryRow(ctx, `SELECT phone_number_id FROM customer_conversations WHERE id = $1`, chat.ID).Scan(&pnid); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business)
	})
	if err != nil {
		return customerMessageView{}, false, err
	}
	if again != nil {
		return *again, false, nil
	}
	if chat.Line == lineApp {
		// In the app: no window, no template, no WhatsApp (§8.3).
		m, fresh, err := s.sendCustomerApp(ctx, tenant, chat, subject, in)
		if m.Line == "" {
			m.Line = lineApp // a repeat says the line it went on
		}
		return m, fresh, err
	}
	if in.Card != nil || in.Offer || in.File != nil {
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "cards, offers and files go to customers who chat in the Taskiem app; this customer writes on WhatsApp"}
	}
	switch {
	case chat.Stopped:
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "this customer asked not to get messages (they sent STOP); nothing is sent until they send START"}
	case chat.Phone == "":
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "this customer's details were erased"}
	}
	wa, err := s.WhatsApp.ForTenant(ctx, tenant)
	if err != nil {
		return customerMessageView{}, false, err
	}
	if !wa.Own() {
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "the business has no WhatsApp number of its own connected now; connect it in Settings to reply"}
	}
	number := chat.Phone
	// The words as the template carries them: on one line.
	oneLine := strings.Join(strings.Fields(text), " ")
	contact, err := wa.Contact(ctx, number)
	if err != nil {
		return customerMessageView{}, false, err
	}
	open := wa.Config.PhoneNumberID == pnid && contact.LastInbound != nil && time.Since(*contact.LastInbound) < whatsapp.Window
	if !open && len([]rune(oneLine)) > customerTemplateMax {
		return customerMessageView{}, false, customerSendErr{http.StatusBadRequest, "more than a day has passed since this customer last wrote: a reply then goes as a WhatsApp message of up to " +
			strconv.Itoa(customerTemplateMax) + " characters, on one line. Send it in shorter parts"}
	}
	sentAs := "text"
	if !open {
		sentAs = "template"
	}
	// Written before it goes: the thread moves to the business's number
	// now if it changed, and a second send of the same client_id finds
	// this one rather than going again.
	out := customerMessageView{ID: uuid.Must(uuid.NewV7()), Mine: true, Kind: "text", Body: text, SenderName: in.SenderName, SentAs: sentAs, Delivery: "sending", Line: lineWhatsApp}
	err = db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// The same client_id sent at the same moment on the other line:
		// the first kept answers.
		if again, err = s.customerRepeatLocked(ctx, tx, tenant, in); err != nil || again != nil {
			return err
		}
		if wa.Config.PhoneNumberID != pnid {
			var other bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = $1 AND phone_number_id = $2 AND subject_id = $3)`,
				tenant, wa.Config.PhoneNumberID, subject).Scan(&other); err != nil {
				return err
			}
			if other {
				return errCustomerOnNewNumber
			}
			if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET phone_number_id = $3 WHERE tenant_id = $1 AND id = $2`, tenant, chat.ID, wa.Config.PhoneNumberID); err != nil {
				return err
			}
		}
		body, err := pii.SealText(ctx, s.Store.PII, tx, tenant, subject, "other", text)
		if err != nil {
			return err
		}
		var client *string
		if in.ClientID != "" {
			client = &in.ClientID
		}
		return tx.QueryRow(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, sender_id, sender_name, sent_as, client_id, delivery)
			VALUES ($1, $2, $3, true, 'text', $4, $5, $6, $7, $8, 'sending') RETURNING created_at`,
			out.ID, tenant, chat.ID, body, in.SenderID, in.SenderName, sentAs, client).Scan(&out.CreatedAt)
	})
	if err == nil && again != nil {
		return *again, false, nil
	}
	if err != nil {
		if isUnique(err) {
			// The same send, at the same moment: that one goes.
			var m *customerMessageView
			if e := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				var err error
				m, err = s.customerRepeat(ctx, tx, tenant, in.pair, in.ClientID)
				return err
			}); e == nil && m != nil {
				return *m, false, nil
			}
		}
		if errors.Is(err, errCustomerOnNewNumber) {
			return customerMessageView{}, false, customerSendErr{http.StatusConflict, err.Error()}
		}
		return customerMessageView{}, false, err
	}
	tpl := whatsapp.TplCustomerReply
	vars := map[string]string{"business": business, "message": oneLine}
	var waID string
	if open {
		waID, err = wa.Send(unkept(ctx), number, whatsapp.Message{Tenant: tenant, Text: text})
		if errors.Is(err, whatsapp.ErrWindowClosed) && len([]rune(oneLine)) <= customerTemplateMax {
			// WhatsApp says the 24 hours are over after all: the template.
			sentAs = "template"
			waID, err = wa.SendTemplate(ctx, tenant, number, tpl, vars, nil)
		}
	} else {
		waID, err = wa.SendTemplate(ctx, tenant, number, tpl, vars, nil)
	}
	if err != nil {
		// Not sent: the message is not kept, and the box gets it back.
		if derr := db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM customer_messages WHERE tenant_id = $1 AND id = $2`, tenant, out.ID)
			return err
		}); derr != nil {
			s.Logger.Warn("customer chat: a message not sent, not removed", "err", derr)
		}
		switch {
		case errors.Is(err, whatsapp.ErrTemplateNotApproved), errors.Is(err, whatsapp.ErrWindowClosed):
			return customerMessageView{}, false, customerSendErr{http.StatusConflict, "more than a day has passed since this customer last wrote, and WhatsApp has not yet approved the message that reaches them after that; your reply can go when they write again"}
		default:
			return customerMessageView{}, false, customerSendErr{http.StatusBadGateway, "WhatsApp did not take the message: " + err.Error()}
		}
	}
	out.SentAs, out.Delivery = sentAs, "sent"
	err = db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var wid *string
		if waID != "" {
			wid = &waID
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_messages SET wa_id = $3, sent_as = $4, delivery = 'sent' WHERE tenant_id = $1 AND id = $2 AND delivery = 'sending'`,
			tenant, out.ID, wid, sentAs); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE customer_conversations SET last_message_at = now() WHERE id = $1`, chat.ID)
		return err
	})
	if err != nil {
		// Sent: it is in the thread as sending; it is not sent again.
		s.Logger.Warn("customer chat: a message sent, not marked so", "err", err)
	}
	if app != nil {
		// Joined with the app (§11): shown in the person's copy, marked on
		// WhatsApp, in the words WhatsApp delivered (the template's, when
		// it went as the template); an answer ends their wait.
		shown := out
		if sentAs == "template" {
			shown.Body = templateWords(business, text)
		}
		s.afterWhatsAppReply(ctx, tenant, app.view, shown, in)
	}
	return out, true, nil
}

// customerClientLock serialises sends of one client_id in a business, so
// two sends of it at once on the customer's two lines (§11) never both go.
func customerClientLock(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, clientID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('customer-client:' || $1::text || ':' || $2, 0))`, tenant, clientID)
	return err
}

// customerRepeatLocked takes the client_id's lock and looks again, on
// either line, for a message already kept under it: in the transaction
// that keeps the message, so the look and the keeping are one.
func (s *Server) customerRepeatLocked(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in customerSend) (*customerMessageView, error) {
	if in.ClientID == "" {
		return nil, nil
	}
	if err := customerClientLock(ctx, tx, tenant, in.ClientID); err != nil {
		return nil, err
	}
	return s.customerRepeat(ctx, tx, tenant, in.pair, in.ClientID)
}

var errCustomerOnNewNumber = errors.New("this customer already has a conversation with the business's new WhatsApp number; answer them there")

// customerStatuses records what WhatsApp says became of replies to
// customers (sent, delivered, read, failed), for the numbers the webhook's
// app speaks for. A status only moves forward; failed replaces sending or
// sent.
func (s *Server) customerStatuses(ctx context.Context, hook *whatsapp.Platform, sts []whatsapp.StatusUpdate) {
	for _, u := range sts {
		wa := hook
		if u.PhoneNumberID != hook.Config.PhoneNumberID {
			if hook.Own() {
				continue
			}
			var err error
			if wa, err = s.WhatsApp.ForPhoneNumberID(ctx, u.PhoneNumberID); err != nil || wa == nil || wa.OwnApp {
				continue
			}
		}
		if !wa.Own() {
			continue
		}
		failure := u.Failure
		if r := []rune(failure); len(r) > 300 {
			failure = string(r[:300])
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE customer_messages SET delivery = $3, failure = NULLIF($4, '')
				WHERE tenant_id = $1 AND wa_id = $2 AND mine AND (
				  ($3 = 'failed' AND delivery IN ('sending', 'sent'))
				  OR ($3 <> 'failed' AND delivery <> 'failed' AND array_position(ARRAY['sending', 'sent', 'delivered', 'read'], delivery) < array_position(ARRAY['sending', 'sent', 'delivered', 'read'], $3::text)))`,
				wa.Tenant, u.ID, u.Status, failure)
			return err
		})
		if err != nil {
			s.Logger.Warn("customer chat: a reply's status", "err", err)
		}
	}
}

// customerChatsUnread is how many customers' threads have messages not
// yet read (for the Chat tab), and how many messages.
func (s *Server) customerChatsUnread(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var chats, msgs int
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		// A customer with both lines is one chat (§11), archived or not as
		// the list shows it: as its app thread is.
		return tx.QueryRow(r.Context(), `SELECT count(DISTINCT COALESCE(c.linked_to, c.id)), count(*) FROM customer_messages m
			JOIN customer_conversations c ON c.id = m.conversation_id AND c.tenant_id = m.tenant_id
			LEFT JOIN customer_conversations a ON a.tenant_id = c.tenant_id AND a.id = c.linked_to
			WHERE m.tenant_id = $1 AND NOT m.mine AND m.read_at IS NULL AND NOT COALESCE(a.archived, c.archived)`, p.TenantID).Scan(&chats, &msgs)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"chats": chats, "messages": msgs})
}

// customerSearchDepth is how many of the latest messages a search reads:
// they are sealed, so they are opened to be searched.
const customerSearchDepth = 1000

// searchCustomerChats finds words in customers' threads: in their names
// and numbers, and in the latest messages, newest first (30 at most).
func (s *Server) searchCustomerChats(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len([]rune(q)) < 2 || len(q) > 100 {
		writeErr(w, http.StatusBadRequest, "q is 2 to 100 characters")
		return
	}
	type hit struct {
		ChatID    uuid.UUID `json:"chat_id"`
		MessageID uuid.UUID `json:"message_id"`
		Name      string    `json:"name"`
		Mine      bool      `json:"mine"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	hits := []hit{}
	chats := []customerChatView{}
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		// One per customer (§11): a hit on either line is its thread's.
		threads, threadOf, err := s.customerThreadsTx(r.Context(), tx, p.TenantID, "", nil, 500)
		if err != nil {
			return err
		}
		names := map[uuid.UUID]string{}
		for _, v := range threads {
			names[v.ID] = v.Name
			phones := v.Phone
			for _, l := range v.Lines {
				phones += " " + l.Phone
			}
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, q)
			if strings.Contains(strings.ToLower(v.Name), q) || (len(digits) >= 4 && strings.Contains(phones, digits)) {
				chats = append(chats, v)
			}
		}
		rows, err := tx.Query(r.Context(), `SELECT conversation_id, id, mine, body, created_at FROM customer_messages
			WHERE tenant_id = $1 AND kind = 'text' ORDER BY created_at DESC LIMIT $2`, p.TenantID, customerSearchDepth)
		if err != nil {
			return err
		}
		type raw struct {
			h    hit
			body []byte
		}
		msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
			var x raw
			return x, r.Scan(&x.h.ChatID, &x.h.MessageID, &x.h.Mine, &x.body, &x.h.CreatedAt)
		})
		if err != nil {
			return err
		}
		for _, x := range msgs {
			if len(hits) >= 30 {
				break
			}
			body, err := s.openCustomerText(r.Context(), tx, p.TenantID, x.body)
			if err != nil {
				return err
			}
			if !strings.Contains(strings.ToLower(body), q) {
				continue
			}
			thread, ok := threadOf[x.h.ChatID]
			if !ok {
				continue // a thread past the latest 500
			}
			x.h.ChatID = thread
			x.h.Body, x.h.Name = body, names[thread]
			hits = append(hits, x.h)

		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats, "messages": hits})
}

// customerVoice puts a customer's voice note in words, where the business
// has voice notes on (Settings, Languages): the recording is fetched and
// transcribed in memory and not kept; the words replace "Voice note" in the
// thread, sealed as the message was. "" when it is not put in words (off,
// too long, a failure): the thread keeps "Voice note".
func (s *Server) customerVoice(wa *whatsapp.Platform, chat, message uuid.UUID, subject string, media *whatsapp.Media) string {
	if s.Voice == nil || s.Voice.Provider == nil || voice.Extension(media.MimeType) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), voiceTranscribeTimeout+30*time.Second)
	defer cancel()
	tc, err := s.tenantChannel(ctx, wa.Tenant)
	if err != nil || !s.voiceOn(tc) {
		return ""
	}
	// Transcription costs money and time: a pace per business.
	if !s.limiter("wa-voice-tenant:"+wa.Tenant.String(), 2*time.Second, 30).Allow() {
		return ""
	}
	lim := s.Voice.Limits.WithDefaults()
	info, err := wa.Client.MediaInfo(ctx, media.ID)
	if err != nil {
		s.Logger.Warn("customer chat: a voice note's file", "tenant", wa.Tenant, "err", err)
		return ""
	}
	mime := media.MimeType
	if info.MimeType != "" {
		mime = info.MimeType
	}
	if info.SHA256 == "" {
		info.SHA256 = media.SHA256
	}
	data, err := wa.Client.Download(ctx, info, lim.MaxBytes)
	if err != nil {
		return ""
	}
	audio := voice.Audio{Data: data, MimeType: mime}
	if info, ok := lang.Lookup(tc.Default); ok {
		audio.Language = info.Speech
	}
	if _, err := voice.Check(audio, lim); err != nil {
		clear(data)
		return ""
	}
	tctx, tcancel := context.WithTimeout(ctx, voiceTranscribeTimeout)
	got, err := s.Voice.Provider.Transcribe(tctx, audio)
	tcancel()
	clear(data) // the recording is not kept
	if err != nil {
		if !errors.Is(err, voice.ErrEmpty) {
			s.Logger.Warn("customer chat: transcribing a voice note", "provider", s.Voice.Provider.Name(), "err", err)
		}
		return ""
	}
	text := strings.TrimSpace(got.Text)
	if utf8.RuneCountInString(text) > voiceMaxTranscript {
		text = string([]rune(text)[:voiceMaxTranscript])
	}
	if text == "" {
		return ""
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		body, err := pii.SealText(ctx, s.Store.PII, tx, wa.Tenant, subject, "other", "Voice note: "+text)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE customer_messages SET body = $3 WHERE tenant_id = $1 AND id = $2 AND conversation_id = $4`, wa.Tenant, message, body, chat)
		return err
	})
	if err != nil {
		s.Logger.Warn("customer chat: keeping a voice note's words", "tenant", wa.Tenant, "err", err)
		return ""
	}
	return text
}
