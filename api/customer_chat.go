package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/reach"
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
		ON CONFLICT (tenant_id, phone_number_id, subject_id) DO UPDATE
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
		tag, err := tx.Exec(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, wa_id)
			VALUES ($1, $2, $3, false, $4, $5, $6) ON CONFLICT DO NOTHING`,
			uuid.Must(uuid.NewV7()), wa.Tenant, id, kind, body, waID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		// A message after STOP is theirs to send; it does not undo STOP.
		_, err = tx.Exec(ctx, `UPDATE customer_conversations SET last_inbound_at = now(), last_message_at = now(), archived = false WHERE id = $1`, id)
		return err
	})
	return true, err
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

const customerChatCols = `c.id, c.subject_id, c.who, c.last_inbound_at, c.last_message_at, c.stopped_at, c.archived,
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
}

func scanCustomerChatRaw(r pgx.Row) (customerChatRaw, error) {
	var x customerChatRaw
	var last *struct {
		Mine bool
		Body []byte
	}
	if err := r.Scan(&x.v.ID, &x.subject, &x.who, &x.inbound, &x.v.LastAt, &x.stopped, &x.v.Archived, &x.v.Unread, &last); err != nil {
		return x, err
	}
	if last != nil {
		x.lastMine, x.lastBody = &last.Mine, last.Body
	}
	return x, nil
}

// openCustomerChat opens a thread's number, name and last message.
func (s *Server) openCustomerChat(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x customerChatRaw) (customerChatView, error) {
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
	if x.inbound != nil {
		until := x.inbound.Add(whatsapp.Window)
		v.WindowUntil = &until
		v.WindowOpen = time.Now().Before(until) && !v.Stopped
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
		rows, err := tx.Query(r.Context(), `SELECT `+customerChatCols+` FROM customer_conversations c
			WHERE c.tenant_id = $1 AND c.archived = $2 ORDER BY c.last_message_at DESC LIMIT 200`, p.TenantID, archived)
		if err != nil {
			return err
		}
		raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
		if err != nil {
			return err
		}
		for _, x := range raws {
			v, err := s.openCustomerChat(r.Context(), tx, p.TenantID, x)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
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
		var subject string
		var err error
		if v, subject, err = s.customerChatIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id")); err != nil {
			return err
		}
		due, n, err := owedBy(r.Context(), tx, p.TenantID, subject)
		v.OwesKobo, v.Invoices = &due, &n
		return err
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
		chat, _, err := s.customerChatIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		q := `SELECT id, mine, kind, body, sender_name, COALESCE(sent_as, ''), COALESCE(delivery, ''), COALESCE(failure, ''), read_at, created_at FROM customer_messages
			WHERE tenant_id = $1 AND conversation_id = $2`
		args := []any{p.TenantID, chat.ID}
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
		}
		raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rawMsg, error) {
			var x rawMsg
			return x, r.Scan(&x.v.ID, &x.v.Mine, &x.v.Kind, &x.body, &x.v.SenderName, &x.v.SentAs, &x.v.Delivery, &x.v.Failure, &x.v.ReadAt, &x.v.CreatedAt)
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
		tag, err := tx.Exec(r.Context(), `UPDATE customer_messages SET read_at = now()
			WHERE tenant_id = $1 AND conversation_id = $2 AND NOT mine AND read_at IS NULL`, p.TenantID, id)
		if err != nil || tag.RowsAffected() > 0 {
			return err
		}
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = $1 AND id = $2)`, p.TenantID, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errCustomerChatNone
		}
		return nil
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putCustomerChat archives a thread, or brings it back.
func (s *Server) putCustomerChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Archived *bool `json:"archived"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Archived == nil {
		writeErr(w, http.StatusBadRequest, "archived is required")
		return
	}
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			return errCustomerChatNone
		}
		tag, err := tx.Exec(r.Context(), `UPDATE customer_conversations SET archived = $3 WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, *req.Archived)
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
// business's own number and keeps it in the thread: as words within 24
// hours of the customer's last message, as the approved template after
// (up to customerTemplateMax characters, refused longer rather than cut).
// The reply is written (sending) before it goes, so two sends of one
// client_id cannot both go. When the business's number has changed since
// the customer wrote, the thread moves to the new number and the reply
// goes from it. Refused after STOP, and when the customer was erased.
func (s *Server) replyCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var req struct {
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || len([]rune(text)) > customerReplyMax {
		writeErr(w, http.StatusBadRequest, "the reply is required (up to "+strconv.Itoa(customerReplyMax)+" characters)")
		return
	}
	if len(req.ClientID) > 80 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters")
		return
	}
	var chat customerChatView
	var subject, pnid, business, sender string
	repeat := func(tx pgx.Tx) (*customerMessageView, error) {
		if req.ClientID == "" {
			return nil, nil
		}
		var m customerMessageView
		var body []byte
		err := tx.QueryRow(ctx, `SELECT id, mine, kind, body, sender_name, COALESCE(sent_as, ''), COALESCE(delivery, ''), COALESCE(failure, ''), read_at, created_at FROM customer_messages
			WHERE tenant_id = $1 AND conversation_id = $2 AND client_id = $3`, p.TenantID, chat.ID, req.ClientID).
			Scan(&m.ID, &m.Mine, &m.Kind, &body, &m.SenderName, &m.SentAs, &m.Delivery, &m.Failure, &m.ReadAt, &m.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if m.Body, err = s.openCustomerText(ctx, tx, p.TenantID, body); err != nil {
			return nil, err
		}
		return &m, nil
	}
	var again *customerMessageView
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if chat, subject, err = s.customerChatIn(ctx, tx, p.TenantID, chi.URLParam(r, "id")); err != nil {
			return err
		}
		if again, err = repeat(tx); err != nil || again != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT phone_number_id FROM customer_conversations WHERE id = $1`, chat.ID).Scan(&pnid); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&business); err != nil {
			return err
		}
		if p.UserID != uuid.Nil {
			var name string
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name); err != nil {
				return err
			}
			sender = firstWord(name)
		}
		return nil
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	if again != nil {
		writeJSON(w, http.StatusOK, again)
		return
	}
	switch {
	case chat.Stopped:
		writeErr(w, http.StatusConflict, "this customer asked not to get messages (they sent STOP); nothing is sent until they send START")
		return
	case chat.Phone == "":
		writeErr(w, http.StatusConflict, "this customer's details were erased")
		return
	}
	if !s.limiter("customer-reply:"+p.TenantID.String(), time.Second, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many replies at once; try again in a moment")
		return
	}
	wa, err := s.WhatsApp.ForTenant(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !wa.Own() {
		writeErr(w, http.StatusConflict, "the business has no WhatsApp number of its own connected now; connect it in Settings to reply")
		return
	}
	number := chat.Phone
	// The words as the template carries them: on one line.
	oneLine := strings.Join(strings.Fields(text), " ")
	contact, err := wa.Contact(ctx, number)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	open := wa.Config.PhoneNumberID == pnid && contact.LastInbound != nil && time.Since(*contact.LastInbound) < whatsapp.Window
	if !open && len([]rune(oneLine)) > customerTemplateMax {
		writeErr(w, http.StatusBadRequest, "more than a day has passed since this customer last wrote: a reply then goes as a WhatsApp message of up to "+
			strconv.Itoa(customerTemplateMax)+" characters, on one line. Send it in shorter parts")
		return
	}
	sentAs := "text"
	if !open {
		sentAs = "template"
	}
	// Written before it goes: the thread moves to the business's number
	// now if it changed, and a second send of the same client_id finds
	// this one rather than going again.
	out := customerMessageView{ID: uuid.Must(uuid.NewV7()), Mine: true, Kind: "text", Body: text, SenderName: sender, SentAs: sentAs, Delivery: "sending"}
	err = db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		if wa.Config.PhoneNumberID != pnid {
			var other bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = $1 AND phone_number_id = $2 AND subject_id = $3)`,
				p.TenantID, wa.Config.PhoneNumberID, subject).Scan(&other); err != nil {
				return err
			}
			if other {
				return errCustomerOnNewNumber
			}
			if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET phone_number_id = $3 WHERE tenant_id = $1 AND id = $2`, p.TenantID, chat.ID, wa.Config.PhoneNumberID); err != nil {
				return err
			}
		}
		body, err := pii.SealText(ctx, s.Store.PII, tx, p.TenantID, subject, "other", text)
		if err != nil {
			return err
		}
		var client *string
		if req.ClientID != "" {
			client = &req.ClientID
		}
		return tx.QueryRow(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, sender_id, sender_name, sent_as, client_id, delivery)
			VALUES ($1, $2, $3, true, 'text', $4, $5, $6, $7, $8, 'sending') RETURNING created_at`,
			out.ID, p.TenantID, chat.ID, body, p.Actor(), sender, sentAs, client).Scan(&out.CreatedAt)
	})
	if err != nil {
		if isUnique(err) {
			// The same send, at the same moment: that one goes.
			var m *customerMessageView
			if e := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
				var err error
				m, err = repeat(tx)
				return err
			}); e == nil && m != nil {
				writeJSON(w, http.StatusOK, m)
				return
			}
		}
		if errors.Is(err, errCustomerOnNewNumber) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	tpl := whatsapp.TplCustomerReply
	vars := map[string]string{"business": business, "message": oneLine}
	var waID string
	if open {
		waID, err = wa.Send(unkept(ctx), number, whatsapp.Message{Tenant: p.TenantID, Text: text})
		if errors.Is(err, whatsapp.ErrWindowClosed) && len([]rune(oneLine)) <= customerTemplateMax {
			// WhatsApp says the 24 hours are over after all: the template.
			sentAs = "template"
			waID, err = wa.SendTemplate(ctx, p.TenantID, number, tpl, vars, nil)
		}
	} else {
		waID, err = wa.SendTemplate(ctx, p.TenantID, number, tpl, vars, nil)
	}
	if err != nil {
		// Not sent: the reply is not kept, and the box gets it back.
		if derr := db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM customer_messages WHERE tenant_id = $1 AND id = $2`, p.TenantID, out.ID)
			return err
		}); derr != nil {
			s.Logger.Warn("customer chat: a reply not sent, not removed", "err", derr)
		}
		switch {
		case errors.Is(err, whatsapp.ErrTemplateNotApproved), errors.Is(err, whatsapp.ErrWindowClosed):
			writeErr(w, http.StatusConflict, "more than a day has passed since this customer last wrote, and WhatsApp has not yet approved the message that reaches them after that; your reply can go when they write again")
		default:
			writeErr(w, http.StatusBadGateway, "WhatsApp did not take the reply: "+err.Error())
		}
		return
	}
	out.SentAs, out.Delivery = sentAs, "sent"
	err = db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var wid *string
		if waID != "" {
			wid = &waID
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_messages SET wa_id = $3, sent_as = $4, delivery = 'sent' WHERE tenant_id = $1 AND id = $2 AND delivery = 'sending'`,
			p.TenantID, out.ID, wid, sentAs); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE customer_conversations SET last_message_at = now() WHERE id = $1`, chat.ID)
		return err
	})
	if err != nil {
		// Sent: it is in the thread as sending; it is not sent again.
		s.Logger.Warn("customer chat: a reply sent, not marked so", "err", err)
	}
	writeJSON(w, http.StatusCreated, out)
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
		return tx.QueryRow(r.Context(), `SELECT count(DISTINCT m.conversation_id), count(*) FROM customer_messages m
			JOIN customer_conversations c ON c.id = m.conversation_id AND c.tenant_id = m.tenant_id
			WHERE m.tenant_id = $1 AND NOT m.mine AND m.read_at IS NULL AND NOT c.archived`, p.TenantID).Scan(&chats, &msgs)
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
		rows, err := tx.Query(r.Context(), `SELECT `+customerChatCols+` FROM customer_conversations c
			WHERE c.tenant_id = $1 ORDER BY c.last_message_at DESC LIMIT 500`, p.TenantID)
		if err != nil {
			return err
		}
		raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
		if err != nil {
			return err
		}
		names := map[uuid.UUID]string{}
		for _, x := range raws {
			v, err := s.openCustomerChat(r.Context(), tx, p.TenantID, x)
			if err != nil {
				return err
			}
			names[v.ID] = v.Name
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, q)
			if strings.Contains(strings.ToLower(v.Name), q) || (len(digits) >= 4 && strings.Contains(v.Phone, digits)) {
				chats = append(chats, v)
			}
		}
		rows, err = tx.Query(r.Context(), `SELECT conversation_id, id, mine, body, created_at FROM customer_messages
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
			x.h.Body, x.h.Name = body, names[x.h.ChatID]
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
