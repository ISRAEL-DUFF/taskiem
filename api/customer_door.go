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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The customer door, the person's side (docs/spec/taskiem-for-everyone.md
// §8, §14, §15; migration 00452). A person chats with a business from
// their own side (You): their copy of each conversation is theirs
// (person_conversations, person_messages, person_files), read in their
// own scope with no business in it. Whatever reaches the business (a
// message, a read mark, their shop, their orders, placing an order or a
// booking, paying) goes through taskiem_customer_enter: one business, one
// action of a fixed list, refused when the business blocked them or
// turned the area off, audited in the business's chain. These routes are
// the person's own: a personal session reaches them, and so does a
// business session, for the same person (never an API key).

// The person's limits (§15): new conversations an hour and a day, and
// messages in one conversation a minute; lower in an account's first week.
var (
	personChatsPerHour      = 10
	personChatsPerDay       = 30
	personMessagesPerMinute = 20
	newPersonDivisor        = 2
)

// customerDoorRoutes are the person's routes (personalRoutes lists them).
func (s *Server) customerDoorRoutes(r chi.Router) {
	r.Get("/me/chats", s.listPersonChats)
	r.Post("/me/chats", s.startPersonChat)
	r.Get("/me/chats/{id}", s.getPersonChat)
	r.Put("/me/chats/{id}", s.putPersonChat)
	r.Get("/me/chats/{id}/messages", s.personMessages)
	r.Post("/me/chats/{id}/messages", s.sendPersonMessage)
	r.Post("/me/chats/{id}/files", s.sendPersonFile)
	r.Get("/me/chats/{id}/files/{message}", s.personFile)
	r.Post("/me/chats/{id}/read", s.readPersonChat)
	r.Post("/me/chats/{id}/typing", s.personTyping)
	r.Post("/me/chats/{id}/block", s.blockBusiness)
	r.Delete("/me/chats/{id}/block", s.blockBusiness)
	r.Post("/me/chats/{id}/report", s.reportBusiness)
	r.Get("/me/chats/{id}/shop", s.doorShop)
	r.Get("/me/chats/{id}/services", s.doorServices)
	r.Get("/me/chats/{id}/services/{service}/times", s.doorServiceTimes)
	r.Get("/me/chats/{id}/orders", s.doorOrders)
	r.Post("/me/chats/{id}/orders", s.doorPlaceOrder)
	r.Post("/me/chats/{id}/bookings", s.doorBook)
	r.Get("/me/chats/{id}/orders/{reference}/pay", s.doorPay)
}

// The door's refusals, in plain words.
var (
	errDoorBlocked    = errors.New("this business is not taking messages from you")
	errDoorOff        = errors.New("this business has turned this off")
	errDoorNone       = errors.New("no such conversation")
	errDoorYouBlocked = errors.New("you blocked this business: unblock it to write to them")
)

// doorErr reads the door's refusals.
func doorErr(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch {
	case pe.Hint == "blocked":
		return errDoorBlocked
	case pe.Hint == "area_off":
		return errDoorOff
	case pe.Code == "P0002":
		return errDoorNone
	}
	return err
}

// personFail answers the person's routes' refusals.
func (s *Server) personFail(w http.ResponseWriter, r *http.Request, err error) {
	var se customerSendErr
	switch {
	case errors.As(err, &se):
		writeErr(w, se.code, se.msg)
	case errors.Is(err, errDoorBlocked):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "This business is not taking messages from you.", "blocked": true})
	case errors.Is(err, errDoorYouBlocked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "You blocked this business. Unblock it to write to them.", "you_blocked": true})
	case errors.Is(err, errDoorOff):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This business has turned this off for now. Ask them here in the chat.", "area_off": true})
	case errors.Is(err, errDoorNone), errors.Is(err, pgx.ErrNoRows):
		writeErr(w, http.StatusNotFound, "not found")
	default:
		s.fail(w, r, err)
	}
}

// personOf is the person a request is for: a person's own session, or a
// business session's person; never an API key, a field agent or an
// embed app's user.
func personOf(r *http.Request) (uuid.UUID, error) {
	p := principalFrom(r.Context())
	if p == nil || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil || p.Invitee {
		return uuid.Nil, customerSendErr{http.StatusForbidden, "This is for a person signed in to their own account."}
	}
	return p.UserID, nil
}

// enterTx goes through the door into one business for one action.
func enterTx(ctx context.Context, tx pgx.Tx, person, tenant uuid.UUID, action string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, $3, $4)`, person, tenant, action, raw)
	return doorErr(err)
}

// personChat is one of the person's conversations, as their side shows it.
type personChat struct {
	ID       uuid.UUID `json:"id"`
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	Last     string    `json:"last"`
	LastMine bool      `json:"last_mine"`
	LastAt   time.Time `json:"last_at"`
	Unread   int       `json:"unread"`
	Muted    bool      `json:"muted"`
	Archived bool      `json:"archived"`
	// Blocked: you blocked the business; BlockedYou: it is not taking
	// your messages.
	Blocked    bool `json:"blocked"`
	BlockedYou bool `json:"blocked_you"`
	// Offers is your answer to "Get offers from {business}?": unasked,
	// yes or no. AskOffers: ask it now (once, at the first chat or order).
	Offers    string `json:"offers"`
	AskOffers bool   `json:"ask_offers"`
	// One conversation only: whether they are typing, and their hours'
	// line (open now, closes at 18:00) when they keep hours.
	Typing *bool      `json:"typing,omitempty"`
	Hours  *chatHours `json:"hours,omitempty"`
	// Notice is said once, on starting: what the business sees of you,
	// and that Taskiem may read business conversations to help (§14).
	Notice string `json:"notice,omitempty"`
}

// chatHours is a business's opening line under its name.
type chatHours struct {
	Open bool   `json:"open"`
	Line string `json:"line"`
}

// personMessage is one message of the person's copy.
type personMessage struct {
	ID         uuid.UUID       `json:"id"`
	Mine       bool            `json:"mine"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	SenderName string          `json:"sender_name,omitempty"`
	Card       *customerCard   `json:"card,omitempty"`
	Attachment *chatAttachment `json:"attachment,omitempty"`
	// Offer: marked Offer, with Stop offers under it (§15).
	Offer     bool       `json:"offer,omitempty"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

const personChatCols = `c.id, c.tenant_id, c.last_message_at, c.muted, c.archived, c.blocked_at IS NOT NULL, c.business_blocked_at IS NOT NULL, c.offers,
	(SELECT count(*) FROM person_messages m WHERE m.person_id = c.person_id AND m.conversation_id = c.id AND NOT m.mine AND m.read_at IS NULL),
	COALESCE((SELECT m.body FROM person_messages m WHERE m.person_id = c.person_id AND m.conversation_id = c.id ORDER BY m.created_at DESC LIMIT 1), ''),
	COALESCE((SELECT m.mine FROM person_messages m WHERE m.person_id = c.person_id AND m.conversation_id = c.id ORDER BY m.created_at DESC LIMIT 1), false)`

func scanPersonChat(r pgx.Row) (personChat, error) {
	var c personChat
	err := r.Scan(&c.ID, &c.Business, &c.LastAt, &c.Muted, &c.Archived, &c.Blocked, &c.BlockedYou, &c.Offers, &c.Unread, &c.Last, &c.LastMine)
	c.AskOffers = c.Offers == "unasked"
	return c, err
}

// namesTx fills in the businesses' names.
func namesTx(ctx context.Context, tx pgx.Tx, chats []personChat) error {
	ids := make([]uuid.UUID, len(chats))
	for i, c := range chats {
		ids[i] = c.Business
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id, name FROM taskiem_customer_business_names($1)`, ids)
	if err != nil {
		return err
	}
	names := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return err
		}
		names[id] = n
	}
	rows.Close()
	for i := range chats {
		chats[i].Name = names[chats[i].Business]
		if chats[i].Name == "" {
			chats[i].Name = "A business"
		}
	}
	return rows.Err()
}

// nameOneTx fills in one conversation's business's name.
func nameOneTx(ctx context.Context, tx pgx.Tx, c *personChat) error {
	list := []personChat{*c}
	if err := namesTx(ctx, tx, list); err != nil {
		return err
	}
	c.Name = list[0].Name
	return nil
}

// personChatTx is one of the person's conversations, by its id.
func personChatTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, idText string) (personChat, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return personChat{}, errDoorNone
	}
	c, err := scanPersonChat(tx.QueryRow(ctx, `SELECT `+personChatCols+` FROM person_conversations c WHERE c.person_id = $1 AND c.id = $2`, person, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errDoorNone
	}
	return c, err
}

// listPersonChats is GET /v1/me/chats: the person's conversations with
// businesses, the latest first (archived ones with ?archived=1).
func (s *Server) listPersonChats(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	archived := r.URL.Query().Get("archived") == "1"
	out := []personChat{}
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+personChatCols+` FROM person_conversations c WHERE c.person_id = $1 AND c.archived = $2
			ORDER BY c.last_message_at DESC LIMIT 200`, person, archived)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (personChat, error) { return scanPersonChat(r) }); err != nil {
			return err
		}
		return namesTx(r.Context(), tx, out)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var unread int
	for _, c := range out {
		unread += c.Unread
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": out, "unread": unread})
}

// personNewWeek is whether the person's account is in its first week, when
// their limits are lower (§15).
func personNewWeek(ctx context.Context, tx pgx.Tx, person uuid.UUID) (bool, error) {
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1`, person).Scan(&created); err != nil {
		return false, err
	}
	return time.Since(created) < 7*24*time.Hour, nil
}

func limitFor(n int, newWeek bool) int {
	if newWeek {
		return max(1, n/newPersonDivisor)
	}
	return n
}

// startPersonChat is POST /v1/me/chats {business_id | code}: opens the
// person's conversation with a business (from its chat link, its shop,
// Discover, or an order they placed), or answers the one they have. The
// business's thread is made in the app line, sealed under the subject of
// the person's phone in that business; the person needs a phone.
func (s *Server) startPersonChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		BusinessID *uuid.UUID `json:"business_id"`
		Code       string     `json:"code"`
		// From: where the person came from (link, shop, discover, order).
		From string `json:"from"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	via := in.From
	var tenant uuid.UUID
	switch {
	case in.BusinessID != nil && in.Code == "":
		tenant = *in.BusinessID
	case in.BusinessID == nil && in.Code != "":
		t, err := s.chatCodes().ResolveChatCode(ctx, strings.TrimSpace(in.Code))
		switch {
		case errors.Is(err, ErrChatCodeReplaced):
			writeJSON(w, http.StatusGone, map[string]any{"error": "This link no longer works. Ask the business for its new link.", "link_replaced": true})
			return
		case errors.Is(err, ErrChatCodeUnknown):
			writeErr(w, http.StatusNotFound, "This link does not open a chat with any business.")
			return
		case err != nil:
			s.fail(w, r, err)
			return
		}
		tenant, via = t, "code"
	default:
		writeErr(w, http.StatusBadRequest, "give a business_id or a chat link's code")
		return
	}
	switch via {
	case "", "link", "shop", "discover", "order", "code":
	default:
		writeErr(w, http.StatusBadRequest, "from is link, shop, discover or order")
		return
	}
	if !s.limiter("person-chat-start:"+person.String(), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, person).Scan(&phone); err != nil {
		s.fail(w, r, err)
		return
	}
	var out personChat
	fresh := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM person_conversations WHERE person_id = $1 AND tenant_id = $2`, person, tenant).Scan(&id)
		if err == nil {
			if out, err = personChatTx(ctx, tx, person, id.String()); err != nil {
				return err
			}
			return nameOneTx(ctx, tx, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if phone == nil {
			return customerSendErr{http.StatusConflict, "Add your phone number first: businesses need it for your orders and deliveries."}
		}
		newWeek, err := personNewWeek(ctx, tx, person)
		if err != nil {
			return err
		}
		var hour, day int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE created_at > now() - interval '1 hour'), count(*) FILTER (WHERE created_at > now() - interval '1 day')
			FROM person_conversations WHERE person_id = $1`, person).Scan(&hour, &day); err != nil {
			return err
		}
		if hour >= limitFor(personChatsPerHour, newWeek) || day >= limitFor(personChatsPerDay, newWeek) {
			return customerSendErr{http.StatusTooManyRequests, "You have started many new chats today. Try again later."}
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, person).Scan(&name); err != nil {
			return err
		}
		if err := enterTx(ctx, tx, person, tenant, "customer.start", map[string]any{"via": via}); err != nil {
			return err
		}
		if _, _, err := s.appThreadTx(ctx, tx, tenant, person, *phone, name); err != nil {
			return err
		}
		var answer *bool
		if err := tx.QueryRow(ctx, `SELECT answer FROM customer_offer_consents WHERE tenant_id = $1 AND person_id = $2`, tenant, person).Scan(&answer); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		offers := "unasked"
		if answer != nil {
			offers = map[bool]string{true: "yes", false: "no"}[*answer]
		}
		var bname string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&bname); err != nil {
			return err
		}
		id = uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id, offers) VALUES ($1, $2, $3, $4)`, id, person, tenant, offers); err != nil {
			return err
		}
		fresh = true
		if out, err = personChatTx(ctx, tx, person, id.String()); err != nil {
			return err
		}
		out.Name = bname
		out.Notice = bname + " will see your name and phone number, for your orders and deliveries. Messages with businesses can be read by Taskiem to help with your orders."
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	code := http.StatusOK
	if fresh {
		code = http.StatusCreated
	}
	writeJSON(w, code, out)
}

// getPersonChat is GET /v1/me/chats/{id}: one conversation, with the
// business's opening line and whether it is typing.
func (s *Server) getPersonChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out personChat
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		if out, err = personChatTx(ctx, tx, person, chi.URLParam(r, "id")); err != nil {
			return err
		}
		if err := nameOneTx(ctx, tx, &out); err != nil {
			return err
		}
		var typing bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_customer_typing_of($1, $2, 'business')`, out.Business, person).Scan(&typing); err != nil {
			return err
		}
		out.Typing = &typing
		return nil
	})
	if err == nil {
		out.Hours = s.hoursLine(ctx, person, out.Business)
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// hoursLine is the business's opening line (open now, closes at 18:00),
// read through the door; nil when it keeps no hours, or the door is shut.
func (s *Server) hoursLine(ctx context.Context, person, tenant uuid.UUID) *chatHours {
	var out *chatHours
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := enterTx(ctx, tx, person, tenant, "customer.chat", map[string]any{"read": "hours"}); err != nil {
			return err
		}
		h, known, err := businessHoursTx(ctx, tx, tenant)
		if err != nil || !known {
			return err
		}
		now := time.Now()
		open, _ := h.OpenNow(now)
		out = &chatHours{Open: open, Line: "Closed now"}
		if open {
			out.Line = "Open now"
			if c := closingAt(h, now); c != "" {
				out.Line = "Open now, closes at " + c
			}
		} else if _, ok := nextOpening(h, now); ok {
			// "Closed now. Opens tomorrow at 09:00"
			out.Line = "Closed now. Opens " + strings.TrimSuffix(strings.TrimPrefix(closedWords(h, now), "We're closed now. We open "), " and will answer then.")
		}
		return nil
	})
	return out
}

// closingAt is when the opening of today's hours that holds t closes, or
// "" when none of today's does.
func closingAt(h storefront.Hours, t time.Time) string {
	t = t.In(storefront.Lagos)
	mins := t.Hour()*60 + t.Minute()
	at := func(s string) int {
		if len(s) != 5 {
			return -1
		}
		hh, _ := strconv.Atoi(s[:2])
		mm, _ := strconv.Atoi(s[3:])
		return hh*60 + mm
	}
	for _, sp := range h.Days[(int(t.Weekday())+6)%7] {
		o, c := at(sp.Open), at(sp.Close)
		if o >= 0 && mins >= o && (c <= o || mins < c) {
			return sp.Close
		}
	}
	return ""
}

// putPersonChat is PUT /v1/me/chats/{id} {muted, archived, offers}: the
// person's own settings for a conversation, and their answer to offers,
// which the business records with when it was given (§15).
func (s *Server) putPersonChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Muted    *bool `json:"muted"`
		Archived *bool `json:"archived"`
		Offers   *bool `json:"offers"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Muted == nil && in.Archived == nil && in.Offers == nil {
		writeErr(w, http.StatusBadRequest, "muted, archived or offers is required")
		return
	}
	var out personChat
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		var offers *string
		if in.Offers != nil {
			o := map[bool]string{true: "yes", false: "no"}[*in.Offers]
			offers = &o
		}
		if _, err := tx.Exec(ctx, `UPDATE person_conversations SET muted = COALESCE($3, muted), archived = COALESCE($4, archived),
			offers = COALESCE($5, offers), offers_at = CASE WHEN $5::text IS NULL THEN offers_at ELSE now() END WHERE person_id = $1 AND id = $2`,
			person, c.ID, in.Muted, in.Archived, offers); err != nil {
			return err
		}
		if in.Offers != nil {
			if err := enterTx(ctx, tx, person, c.Business, "customer.chat", map[string]any{"offers": *in.Offers}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO customer_offer_consents (tenant_id, person_id, answer) VALUES ($1, $2, $3)
				ON CONFLICT (tenant_id, person_id) DO UPDATE SET answer = EXCLUDED.answer, answered_at = now()`, c.Business, person, *in.Offers); err != nil {
				return err
			}
		}
		if out, err = personChatTx(ctx, tx, person, c.ID.String()); err != nil {
			return err
		}
		return nameOneTx(ctx, tx, &out)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// personMessages is GET /v1/me/chats/{id}/messages: a page of the
// person's copy, the latest last: 50 before ?before (a message), or the
// latest 50. Their own copy only: nothing of the business is read.
func (s *Server) personMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []personMessage{}
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		q := `SELECT id, mine, kind, body, sender_name, card, attachment, offer, read_at, created_at FROM person_messages WHERE person_id = $1 AND conversation_id = $2`
		args := []any{person, c.ID}
		if b, err := uuid.Parse(r.URL.Query().Get("before")); err == nil {
			q += ` AND (created_at, id) < (SELECT created_at, id FROM person_messages WHERE person_id = $1 AND id = $3)`
			args = append(args, b)
		}
		rows, err := tx.Query(ctx, q+` ORDER BY created_at DESC, id DESC LIMIT 51`, args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (personMessage, error) {
			var m personMessage
			return m, r.Scan(&m.ID, &m.Mine, &m.Kind, &m.Body, &m.SenderName, &m.Card, &m.Attachment, &m.Offer, &m.ReadAt, &m.CreatedAt)
		})
		if err != nil {
			return err
		}
		if len(list) > 50 {
			more, list = true, list[:50]
		}
		for i := len(list) - 1; i >= 0; i-- {
			out = append(out, list[i])
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "more": more})
}

// sendPersonMessage is POST /v1/me/chats/{id}/messages {text, client_id}:
// the person writes to the business. Kept in both copies in one
// transaction through the door; the business's people who handle orders
// are told; a business closed by its hours answers once a day.
func (s *Server) sendPersonMessage(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || len([]rune(in.Text)) > 4000 {
		writeErr(w, http.StatusBadRequest, "the message is required (up to 4,000 characters)")
		return
	}
	if len(in.ClientID) > 80 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters")
		return
	}
	m, fresh, err := s.personSend(r.Context(), person, chi.URLParam(r, "id"), in.Text, in.ClientID, nil)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !fresh {
		code = http.StatusOK
	}
	writeJSON(w, code, m)
}

// sendPersonFile is POST /v1/me/chats/{id}/files: a photo, voice note or
// PDF to the business (multipart: file; text, a caption; client_id).
// Scanned for malware and checked before either copy is kept (§8.5); the
// business keeps its copy in its own storage, the person theirs.
func (s *Server) sendPersonFile(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	f, err := s.readChatFile(w, r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	text := strings.TrimSpace(r.FormValue("text"))
	if len([]rune(text)) > 4000 {
		writeErr(w, http.StatusBadRequest, "a caption is up to 4,000 characters")
		return
	}
	if text == "" {
		text = map[string]string{"photo": "Photo", "voice": "Voice note", "file": "File"}[media.ChatKind(f.Type)]
	}
	clientID := r.FormValue("client_id")
	if len(clientID) > 80 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters")
		return
	}
	m, fresh, err := s.personSend(r.Context(), person, chi.URLParam(r, "id"), text, clientID, f)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !fresh {
		code = http.StatusOK
	}
	writeJSON(w, code, m)
}

// personRepeat is the person's message under clientID, if they sent one.
func personRepeat(ctx context.Context, tx pgx.Tx, person, conv uuid.UUID, clientID string) (*personMessage, error) {
	if clientID == "" {
		return nil, nil
	}
	var m personMessage
	err := tx.QueryRow(ctx, `SELECT id, mine, kind, body, sender_name, card, attachment, offer, read_at, created_at FROM person_messages
		WHERE person_id = $1 AND conversation_id = $2 AND client_id = $3`, person, conv, clientID).
		Scan(&m.ID, &m.Mine, &m.Kind, &m.Body, &m.SenderName, &m.Card, &m.Attachment, &m.Offer, &m.ReadAt, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// personSend writes the person's message (and file) to both copies
// through the door, in one transaction; fresh is false for a repeat of a
// client_id.
func (s *Server) personSend(ctx context.Context, person uuid.UUID, chatID, text, clientID string, f *sentFile) (personMessage, bool, error) {
	out := personMessage{ID: uuid.Must(uuid.NewV7()), Mine: true, Kind: "text", Body: text}
	if f != nil {
		out.Kind, out.Attachment = "media", f.view()
	}
	var tenant uuid.UUID
	var bizFile map[string]any
	var again *personMessage
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chatID)
		if err != nil {
			return err
		}
		tenant = c.Business
		if again, err = personRepeat(ctx, tx, person, c.ID, clientID); err != nil || again != nil {
			return err
		}
		if c.Blocked {
			return errDoorYouBlocked
		}
		newWeek, err := personNewWeek(ctx, tx, person)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM person_messages WHERE person_id = $1 AND conversation_id = $2 AND mine AND created_at > now() - interval '1 minute'`,
			person, c.ID).Scan(&n); err != nil {
			return err
		}
		if n >= limitFor(personMessagesPerMinute, newWeek) {
			return customerSendErr{http.StatusTooManyRequests, "You are sending messages very fast. Wait a moment and try again."}
		}
		if err := enterTx(ctx, tx, person, tenant, "customer.chat", map[string]any{"message": out.ID, "kind": out.Kind}); err != nil {
			return err
		}
		var thread uuid.UUID
		var subject string
		if err := tx.QueryRow(ctx, `SELECT id, subject_id FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`,
			tenant, person).Scan(&thread, &subject); err != nil {
			return err
		}
		if f != nil {
			// The business's copy, in its own storage, once the door is
			// open; removed again if the message is not kept.
			bf, err := s.Media.PutChatFile(ctx, tenant, f.Data)
			if err != nil {
				return chatFileErr(err)
			}
			bizFile = map[string]any{"kind": out.Attachment.Kind, "type": bf.Type, "size": bf.Size, "name": f.Name, "path": bf.Path}
		}
		if out.CreatedAt, _, err = s.writeAppMessageTx(ctx, tx, appMessage{id: out.ID, tenant: tenant, thread: thread, subject: subject, person: person,
			fromPerson: true, kind: out.Kind, text: text, bizFile: bizFile, personFile: f, clientID: clientID}); err != nil {
			return err
		}
		// The bell, in a savepoint: if telling fails, the message is kept.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := s.tellCustomerMessageTx(ctx, sp, tenant, thread); err != nil {
			s.Logger.Warn("customer door: telling the bell", "err", err)
			if err := sp.Rollback(ctx); err != nil {
				return err
			}
		} else if err := sp.Commit(ctx); err != nil {
			return err
		}
		return s.closedReplyTx(ctx, tx, tenant, thread, person, subject)
	})
	if err != nil && bizFile != nil {
		s.dropChatFile(ctx, tenant, bizFile["path"].(string))
	}
	if again != nil {
		return *again, false, nil
	}
	if err != nil {
		if isUnique(err) && clientID != "" {
			var m *personMessage
			if e := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				c, err := personChatTx(ctx, tx, person, chatID)
				if err != nil {
					return err
				}
				m, err = personRepeat(ctx, tx, person, c.ID, clientID)
				return err
			}); e == nil && m != nil {
				return *m, false, nil
			}
		}
		return out, false, err
	}
	return out, true, nil
}

// personFile is GET /v1/me/chats/{id}/files/{message}: the person's own
// copy of a message's file.
func (s *Server) personFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	var ctype, name, kind string
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		msg, err := uuid.Parse(chi.URLParam(r, "message"))
		if err != nil {
			return errDoorNone
		}
		return tx.QueryRow(ctx, `SELECT f.data, f.type, f.name, m.attachment->>'kind' FROM person_files f JOIN person_messages m ON m.id = f.message_id AND m.person_id = f.person_id
			WHERE f.person_id = $1 AND m.conversation_id = $2 AND f.message_id = $3`, person, c.ID, msg).Scan(&data, &ctype, &name, &kind)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	serveChatFile(w, data, ctype, name, kind)
}

// readPersonChat is POST /v1/me/chats/{id}/read: the person read the
// business's messages; the business's copy shows them read.
func (s *Server) readPersonChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE person_messages SET read_at = now() WHERE person_id = $1 AND conversation_id = $2 AND NOT mine AND read_at IS NULL RETURNING id`,
			person, c.ID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(ids) == 0 || c.BlockedYou {
			return err
		}
		if err := enterTx(ctx, tx, person, c.Business, "customer.chat", map[string]any{"read": len(ids)}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE customer_messages SET read_at = COALESCE(read_at, now()), delivery = 'read' WHERE tenant_id = $1 AND id = ANY ($2) AND mine`,
			c.Business, ids)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// personTyping is POST /v1/me/chats/{id}/typing: the person is typing,
// for the business's typing line, for a few seconds.
func (s *Server) personTyping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_customer_typing($1, $2, 'person')`, c.Business, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// blockBusiness is POST (block) and DELETE (unblock) /v1/me/chats/{id}/block:
// the person stops the business writing to them (§8.3). Nothing is
// deleted; the business's thread says "This customer has stopped messages".
func (s *Server) blockBusiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	block := r.Method == http.MethodPost
	var out personChat
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE person_conversations SET blocked_at = CASE WHEN $3 THEN COALESCE(blocked_at, now()) END WHERE person_id = $1 AND id = $2`,
			person, c.ID, block); err != nil {
			return err
		}
		if err := enterTx(ctx, tx, person, c.Business, "customer.block", map[string]any{"blocked": block}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET person_blocked_at = CASE WHEN $3 THEN COALESCE(person_blocked_at, now()) END
			WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`, c.Business, person, block); err != nil {
			return err
		}
		if out, err = personChatTx(ctx, tx, person, c.ID.String()); err != nil {
			return err
		}
		return nameOneTx(ctx, tx, &out)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// reportBusiness is POST /v1/me/chats/{id}/report {reason, note, consent}:
// the conversation reported to Taskiem's moderators, with its last 20
// messages, which go only with the person's consent (§15).
func (s *Server) reportBusiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Reason  string `json:"reason"`
		Note    string `json:"note"`
		Consent bool   `json:"consent"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch in.Reason {
	case "spam", "scam", "abuse", "other":
	default:
		writeErr(w, http.StatusBadRequest, "reason is spam, scam, abuse or other")
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 1000 {
		writeErr(w, http.StatusBadRequest, "a note is up to 1,000 characters")
		return
	}
	if !in.Consent {
		writeErr(w, http.StatusBadRequest, "A report sends the last 20 messages to Taskiem's moderators: agree to send them (consent) to report.")
		return
	}
	if !s.limiter("person-report:"+person.String(), 2*time.Hour, 12).Allow() {
		writeErr(w, http.StatusTooManyRequests, "You have sent many reports. Try again later.")
		return
	}
	id := uuid.Must(uuid.NewV7())
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT mine, kind, body, offer, created_at FROM person_messages WHERE person_id = $1 AND conversation_id = $2
			ORDER BY created_at DESC LIMIT 20`, person, c.ID)
		if err != nil {
			return err
		}
		type sent struct {
			FromYou bool      `json:"from_customer"`
			Kind    string    `json:"kind"`
			Body    string    `json:"body"`
			Offer   bool      `json:"offer,omitempty"`
			At      time.Time `json:"at"`
		}
		msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sent, error) {
			var m sent
			return m, r.Scan(&m.FromYou, &m.Kind, &m.Body, &m.Offer, &m.At)
		})
		if err != nil {
			return err
		}
		if msgs == nil {
			msgs = []sent{}
		}
		raw, _ := json.Marshal(msgs)
		_, err = tx.Exec(ctx, `INSERT INTO customer_reports (id, person_id, tenant_id, reason, note, consent, messages) VALUES ($1, $2, $3, $4, $5, true, $6)`,
			id, person, c.Business, in.Reason, in.Note, raw)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "messages_sent": true,
		"said": "Thank you. Taskiem's moderators will look at this conversation. You can also block this business."})
}

// doorOnce runs fn through the door for one action, in the person's own
// transaction, which is kept: every entry is audited (§8.4).
func (s *Server) doorOnce(ctx context.Context, person uuid.UUID, chatID, action string, detail map[string]any, fn func(tx pgx.Tx, c personChat) error) error {
	return db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := personChatTx(ctx, tx, person, chatID)
		if err != nil {
			return err
		}
		if err := enterTx(ctx, tx, person, c.Business, action, detail); err != nil {
			return err
		}
		return fn(tx, c)
	})
}
