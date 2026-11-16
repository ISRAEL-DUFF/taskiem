package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Customers in the app, the business's side (docs/spec/taskiem-for-
// everyone.md §8.3, §8.5, §8.6, §15; migration 00452). A person who chats
// with a business in the Taskiem app or the browser has a customer thread
// like a WhatsApp customer's, on the line app: the same thread in
// Customers, sealed under the customer's subject, with no 24-hour window
// and no template. Each message is written twice in one transaction: the
// business's copy (customer_messages), and, through the way back
// (taskiem_customer_reach), the person's (person_messages), with the same
// id. Staff may send rich replies (an order card, a pay link, a booking
// offer, a product), files, and offers to customers who agreed to them;
// either side may block the other, and nothing is deleted by it.

const lineApp = "app"

// customerCard is a card in an app conversation: an order (what, total,
// its state; Pay when due), a pay link, a booking offer (a service), a
// product, or the business's closed reply.
type customerCard struct {
	Type      string `json:"type"` // order | pay | booking | product
	Reference string `json:"reference,omitempty"`
	Title     string `json:"title"`
	Line      string `json:"line,omitempty"`
	Status    string `json:"status,omitempty"`
	TotalKobo *int64 `json:"total_kobo,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	Minutes   int    `json:"minutes,omitempty"`
	// URL is a pay link's page (the order's status page, where it is paid).
	URL string `json:"url,omitempty"`
}

// chatAttachment is a photo, voice note or file of an app message, as
// each side shows it; where its copy is kept is never shown.
type chatAttachment struct {
	Kind string `json:"kind"` // photo, voice or file
	Type string `json:"type"`
	Size int64  `json:"size"`
	Name string `json:"name,omitempty"`
}

// sentFile is a file sent in an app conversation, scanned and cleaned
// (media.CheckChatFile), before either copy is stored.
type sentFile struct {
	Type, Name string
	Data       []byte
}

func (f *sentFile) view() *chatAttachment {
	return &chatAttachment{Kind: media.ChatKind(f.Type), Type: f.Type, Size: int64(len(f.Data)), Name: f.Name}
}

// CustomerAnswer is a business's message to a person in the app, told to
// a CustomerAnswerHook once it is kept.
type CustomerAnswer struct {
	Person, Business, Conversation, Message uuid.UUID
	BusinessName                            string
	Offer                                   bool
	// Muted: the person muted the conversation.
	Muted bool
}

// CustomerAnswerHook hears when a business writes in a person's app
// conversation, after it is committed: the person told where the app
// cannot reach them (taskiem-for-everyone §9: the browser chat's SMS,
// "Mama's Kitchen answered you on Taskiem: {link}", at most one per
// conversation per hour). Stream B fills it; nil tells no one. It is
// called on the request's goroutine once the message is kept: it must not
// block (queue what it sends).
type CustomerAnswerHook interface {
	BusinessAnswered(ctx context.Context, a CustomerAnswer)
}

// ChatCodeResolver finds the business a chat link's code names
// (taskiem-for-everyone §8.2, /m/{code}). Stream B keeps the codes
// (customer_chat_codes); until it is set, no code is known.
type ChatCodeResolver interface {
	// ResolveChatCode answers the business, or ErrChatCodeUnknown (a code
	// that never was, or replaced more than 30 days ago), or
	// ErrChatCodeReplaced ("This link no longer works").
	ResolveChatCode(ctx context.Context, code string) (uuid.UUID, error)
}

// The chat link's refusals.
var (
	ErrChatCodeUnknown  = errors.New("no business has this chat link")
	ErrChatCodeReplaced = errors.New("this link no longer works")
)

// noChatCodes is the resolver before stream B's: it knows no code.
type noChatCodes struct{}

func (noChatCodes) ResolveChatCode(context.Context, string) (uuid.UUID, error) {
	return uuid.Nil, ErrChatCodeUnknown
}

func (s *Server) chatCodes() ChatCodeResolver {
	if s.ChatCodes != nil {
		return s.ChatCodes
	}
	if s.ChatLinks.Ready() {
		return linkCodes{s.ChatLinks}
	}
	return noChatCodes{}
}

// linkCodes resolves codes through the businesses' chat links
// (engine/chatlinks), in the door's words.
type linkCodes struct{ l *chatlinks.Links }

func (c linkCodes) ResolveChatCode(ctx context.Context, code string) (uuid.UUID, error) {
	t, err := c.l.Resolve(ctx, code)
	switch {
	case errors.Is(err, chatlinks.ErrReplaced):
		return uuid.Nil, ErrChatCodeReplaced
	case errors.Is(err, chatlinks.ErrNotFound), errors.Is(err, chatlinks.ErrOff):
		return uuid.Nil, ErrChatCodeUnknown
	}
	return t, err
}

// offersPerWeek is how many offers a customer gets from one business in
// seven days (§15), a broadcast counted for each.
const offersPerWeek = 2

// --- writing both copies ---

// appMessage is one message of an app conversation, written to both
// copies by writeAppMessageTx.
type appMessage struct {
	id         uuid.UUID
	tenant     uuid.UUID
	thread     uuid.UUID // the business's copy (customer_conversations)
	subject    string
	person     uuid.UUID
	fromPerson bool
	kind       string // text, media or card
	text       string
	card       *customerCard
	// The business's copy of a file (its storage path) and the person's.
	bizFile    map[string]any
	personFile *sentFile
	offer      bool
	senderID   string // the business's member, or system:*
	senderName string
	clientID   string
	// system: Taskiem's words for the business (the closed reply), which
	// are not an answer.
	system bool
}

// writeAppMessageTx writes a message to both copies, in a transaction
// that has entered the door one way or the other (both the business and
// the person are in scope): the business's sealed under the customer's
// subject, the person's in their own rows. It answers when it was kept,
// and the person's conversation.
func (s *Server) writeAppMessageTx(ctx context.Context, tx pgx.Tx, m appMessage) (at time.Time, conv uuid.UUID, err error) {
	body, err := pii.SealText(ctx, s.Store.PII, tx, m.tenant, m.subject, "other", m.text)
	if err != nil {
		return at, conv, err
	}
	var client, delivery, senderID *string
	if m.clientID != "" && !m.fromPerson {
		client = &m.clientID
	}
	if !m.fromPerson {
		d := "delivered"
		delivery, senderID = &d, &m.senderID
	}
	var bizAtt, personAtt []byte
	if m.bizFile != nil {
		bizAtt, _ = json.Marshal(m.bizFile)
	}
	fileID := uuid.Must(uuid.NewV7())
	if m.personFile != nil {
		v := m.personFile.view()
		personAtt, _ = json.Marshal(map[string]any{"kind": v.Kind, "type": v.Type, "size": v.Size, "name": v.Name, "file": fileID})
	}
	var card []byte
	if m.card != nil {
		card, _ = json.Marshal(m.card)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, sender_id, sender_name, client_id, delivery, card, attachment, offer)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING created_at`,
		m.id, m.tenant, m.thread, !m.fromPerson, m.kind, body, senderID, m.senderName, client, delivery, card, bizAtt, m.offer).Scan(&at); err != nil {
		return at, conv, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id) VALUES ($1, $2, $3)
		ON CONFLICT (person_id, tenant_id) DO UPDATE SET last_message_at = now() RETURNING id`, uuid.Must(uuid.NewV7()), m.person, m.tenant).Scan(&conv)
	if err != nil {
		return at, conv, err
	}
	var pclient *string
	if m.clientID != "" && m.fromPerson {
		pclient = &m.clientID
	}
	shown := m.senderName
	if _, err := tx.Exec(ctx, `INSERT INTO person_messages (id, person_id, conversation_id, tenant_id, mine, kind, body, sender_name, card, attachment, offer, client_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		m.id, m.person, conv, m.tenant, m.fromPerson, m.kind, m.text, shown, card, personAtt, m.offer, pclient, at); err != nil {
		return at, conv, err
	}
	if m.personFile != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO person_files (id, person_id, tenant_id, message_id, type, size, name, data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			fileID, m.person, m.tenant, m.id, m.personFile.Type, len(m.personFile.Data), m.personFile.Name, m.personFile.Data); err != nil {
			return at, conv, err
		}
	}
	switch {
	case m.fromPerson:
		_, err = tx.Exec(ctx, `UPDATE customer_conversations SET last_inbound_at = now(), last_message_at = now(), archived = false WHERE id = $1`, m.thread)
	case m.system:
		_, err = tx.Exec(ctx, `UPDATE customer_conversations SET last_message_at = now() WHERE id = $1`, m.thread)
	default:
		_, err = tx.Exec(ctx, `UPDATE customer_conversations SET last_message_at = now(), last_reply_at = now() WHERE id = $1`, m.thread)
	}
	if err != nil {
		return at, conv, err
	}
	_, err = tx.Exec(ctx, `UPDATE person_conversations SET last_message_at = now(), archived = archived AND $2 WHERE id = $1`, conv, m.fromPerson)
	return at, conv, err
}

// reachTx is the way back into the person's copy (taskiem_customer_reach),
// for the business in scope.
func reachTx(ctx context.Context, tx pgx.Tx, tenant, person uuid.UUID, action string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, $3, $4)`, tenant, person, action, raw)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Hint == "stopped" {
		return customerSendErr{http.StatusConflict, "This customer has stopped messages."}
	}
	return err
}

// sendCustomerApp writes a business's message to a customer in the app:
// words, a card, a file or an offer, to both copies; a repeat of a
// client_id answers the first. Refused when either side blocked the
// other; an offer only to a customer who agreed to offers, two a week.
func (s *Server) sendCustomerApp(ctx context.Context, tenant uuid.UUID, chat customerChatView, subject string, in customerSend) (customerMessageView, bool, error) {
	switch {
	case chat.person == nil:
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "This customer left Taskiem."}
	case chat.Blocked:
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "You blocked this customer. Unblock them to write to them."}
	case chat.Stopped:
		return customerMessageView{}, false, customerSendErr{http.StatusConflict, "This customer has stopped messages."}
	}
	person := *chat.person
	out := customerMessageView{ID: uuid.Must(uuid.NewV7()), Mine: true, Kind: "text", Body: in.Text, SenderName: in.SenderName, Delivery: "delivered",
		Card: in.Card, Offer: in.Offer}
	if in.Card != nil {
		out.Kind = "card"
	}
	var bizFile map[string]any
	if in.File != nil {
		if s.Media == nil {
			return out, false, customerSendErr{http.StatusConflict, "Files cannot be sent here: storage for files is not set up."}
		}
		f, err := s.Media.PutChatFile(ctx, tenant, in.File.Data)
		if err != nil {
			return out, false, chatFileErr(err)
		}
		v := in.File.view()
		bizFile = map[string]any{"kind": v.Kind, "type": f.Type, "size": f.Size, "name": v.Name, "path": f.Path}
		out.Kind, out.Attachment = "media", v
	}
	var again *customerMessageView
	var name string
	var muted bool
	var conv uuid.UUID
	err := db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if again, err = s.customerRepeat(ctx, tx, tenant, chat.ID, in.ClientID); err != nil || again != nil {
			return err
		}
		if in.Offer {
			if err := offerAllowedTx(ctx, tx, tenant, person, chat.ID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
			return err
		}
		actorType := "user"
		if strings.HasPrefix(in.SenderID, "system:") {
			actorType = "system"
		}
		if err := reachTx(ctx, tx, tenant, person, "customer.message", map[string]any{"actor": in.SenderID, "actor_type": actorType,
			"message": out.ID, "kind": out.Kind, "offer": in.Offer}); err != nil {
			return err
		}
		m := appMessage{id: out.ID, tenant: tenant, thread: chat.ID, subject: subject, person: person, kind: out.Kind, text: in.Text, card: in.Card,
			bizFile: bizFile, personFile: in.File, offer: in.Offer, senderID: in.SenderID, senderName: in.SenderName, clientID: in.ClientID,
			system: strings.HasPrefix(in.SenderID, "system:")}
		if out.CreatedAt, conv, err = s.writeAppMessageTx(ctx, tx, m); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT muted FROM person_conversations WHERE id = $1`, conv).Scan(&muted)
	})
	if err != nil || again != nil {
		if bizFile != nil {
			s.dropChatFile(ctx, tenant, bizFile["path"].(string))
		}
		if again != nil {
			return *again, false, nil
		}
		if isUnique(err) && in.ClientID != "" {
			var m *customerMessageView
			if e := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				var err error
				m, err = s.customerRepeat(ctx, tx, tenant, chat.ID, in.ClientID)
				return err
			}); e == nil && m != nil {
				return *m, false, nil
			}
		}
		return customerMessageView{}, false, err
	}
	if s.CustomerAnswered != nil {
		s.CustomerAnswered.BusinessAnswered(context.WithoutCancel(ctx), CustomerAnswer{Person: person, Business: tenant, Conversation: conv,
			Message: out.ID, BusinessName: name, Offer: in.Offer, Muted: muted})
	}
	// The person told where the app cannot reach them (§9): an answer, not
	// an offer, and never into a muted chat.
	if !in.Offer && !muted {
		s.tellAnswered(context.WithoutCancel(ctx), chatlinks.Answer{Tenant: tenant, Conversation: conv, Person: person})
	}
	return out, true, nil
}

// offerAllowedTx refuses an offer to a customer who has not said yes to
// offers, or who has had offersPerWeek in the last seven days (§15).
func offerAllowedTx(ctx context.Context, tx pgx.Tx, tenant, person, thread uuid.UUID) error {
	var yes bool
	err := tx.QueryRow(ctx, `SELECT answer FROM customer_offer_consents WHERE tenant_id = $1 AND person_id = $2`, tenant, person).Scan(&yes)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !yes {
		return customerSendErr{http.StatusConflict, "This customer has not agreed to get offers from you. Messages about their orders and bookings can still go."}
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_messages WHERE tenant_id = $1 AND conversation_id = $2 AND offer AND created_at > now() - interval '7 days'`,
		tenant, thread).Scan(&n); err != nil {
		return err
	}
	if n >= offersPerWeek {
		return customerSendErr{http.StatusTooManyRequests, "This customer has had " + strconv.Itoa(offersPerWeek) + " offers from you this week. Try again next week."}
	}
	return nil
}

// chatFileErr is a file refused, in plain words.
func chatFileErr(err error) error {
	switch {
	case errors.Is(err, media.ErrNoScanner):
		return customerSendErr{http.StatusConflict, "Files cannot be sent here: this deployment has no malware scanner."}
	case errors.Is(err, media.ErrThreat):
		return customerSendErr{http.StatusUnprocessableEntity, "This file did not pass the safety check, so it was not sent."}
	case errors.Is(err, media.ErrFile), errors.Is(err, media.ErrImage):
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
			if j := strings.Index(msg, ": "); j >= 0 {
				msg = msg[j+2:]
			}
		}
		return customerSendErr{http.StatusBadRequest, "This file cannot be sent: " + msg + "."}
	}
	return err
}

// readChatFile reads and checks a file from a multipart form: scanned for
// malware, then checked and cleaned (photos up to 10 MB, voice notes up to
// 5 minutes, files up to 25 MB, §8.5) before either copy is kept.
func (s *Server) readChatFile(w http.ResponseWriter, r *http.Request) (*sentFile, error) {
	if s.Media == nil {
		return nil, customerSendErr{http.StatusConflict, "Files cannot be sent here: storage for files is not set up."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxChatFileBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		return nil, customerSendErr{http.StatusBadRequest, "Send the file as a form with a file field, at most 25 MB."}
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, customerSendErr{http.StatusBadRequest, "The form has no file."}
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || len(data) == 0 {
		return nil, customerSendErr{http.StatusBadRequest, "The file could not be read."}
	}
	if err := s.Media.ScanChatFile(r.Context(), data); err != nil {
		return nil, chatFileErr(err)
	}
	ctype, clean, err := media.CheckChatFile(data)
	if err != nil {
		return nil, chatFileErr(err)
	}
	name := hdr.Filename
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return &sentFile{Type: ctype, Name: name, Data: clean}, nil
}

// --- the business's routes for the app line ---

func (s *Server) customerAppRoutes(r chi.Router) {
	orders := s.need(PermOrders)
	r.With(orders, s.tenantWide).Post("/customer-chats/app", s.openAppCustomerChat)
	r.With(orders, s.tenantWide).Post("/customer-chats/offers", s.broadcastOffer)
	r.With(orders, s.tenantWide).Post("/customer-chats/{id}/files", s.sendCustomerFile)
	r.With(orders).Get("/customer-chats/{id}/files/{message}", s.customerFile)
	r.With(orders, s.tenantWide).Post("/customer-chats/{id}/block", s.blockCustomer)
	r.With(orders, s.tenantWide).Delete("/customer-chats/{id}/block", s.blockCustomer)
	r.With(orders).Post("/customer-chats/{id}/typing", s.customerTyping)
	r.With(orders).Get("/settings/customer-chats", s.getCustomerChatSettings)
	r.With(s.need(access.StorefrontManage), s.tenantWide).Put("/settings/customer-chats", s.putCustomerChatSettings)
}

// cardAsk is the card a member asks to send: an order or its pay link (by
// reference), a booking offer (a service), a product (an item).
type cardAsk struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
	ItemID    string `json:"item_id"`
	ServiceID string `json:"service_id"`
}

// cardFor makes a card from the business's own data: an order of this
// customer's, its pay link (while something is due), a service it books,
// an item of its catalogue.
func (s *Server) cardFor(ctx context.Context, tenant uuid.UUID, thread string, ask cardAsk) (*customerCard, error) {
	var card *customerCard
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		chat, subject, err := s.customerChatIn(ctx, tx, tenant, thread)
		if err != nil {
			return err
		}
		if chat.Line != lineApp {
			return customerSendErr{http.StatusConflict, "cards go to customers who chat in the Taskiem app; this customer writes on WhatsApp"}
		}
		switch ask.Type {
		case "order", "pay":
			if s.Orders == nil {
				return customerSendErr{http.StatusConflict, "orders are not set up on this server"}
			}
			ord, err := s.Orders.GetOrder(ctx, tx, tenant, ask.Reference)
			if errors.Is(err, storefront.ErrNoOrder) {
				return customerSendErr{http.StatusNotFound, "No order has that reference."}
			}
			if err != nil {
				return err
			}
			var theirs bool
			if err := tx.QueryRow(ctx, `SELECT subject_id = $3 OR (person_id IS NOT NULL AND person_id = $4) FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`,
				tenant, ask.Reference, subject, chat.person).Scan(&theirs); err != nil {
				return err
			}
			if !theirs {
				return customerSendErr{http.StatusConflict, "That order is another customer's."}
			}
			total := ord.DueKobo()
			card = &customerCard{Type: "order", Reference: ord.Reference, Title: "Order " + ord.Reference, Line: storefront.Summary(ord.Basket), Status: ord.Status, TotalKobo: &total}
			if ord.ServiceName != "" && len(ord.Basket) == 0 {
				card.Title, card.Line = "Booking "+ord.Reference, ord.ServiceName
			}
			if ask.Type == "pay" {
				due := ord.BalanceKobo()
				if due <= 0 {
					return customerSendErr{http.StatusConflict, "Nothing is due on this order."}
				}
				link, err := s.Orders.ChatStatusLink(ctx, tx, tenant, ord)
				if err != nil {
					return err
				}
				if link == "" {
					return customerSendErr{http.StatusConflict, "This order has no page to pay on: your shop page is needed for pay links."}
				}
				card.Type, card.Title, card.TotalKobo, card.URL = "pay", "Pay for "+ord.Reference, &due, link
			}
		case "product":
			items, err := storefront.Catalogue(ctx, tx, tenant)
			if err != nil {
				return err
			}
			for _, it := range items {
				if it.ID == ask.ItemID && it.VariantOf == "" {
					card = &customerCard{Type: "product", ItemID: it.ID, Title: it.Name, Line: firstWords(it.Description, 20), TotalKobo: it.PriceKobo}
				}
			}
			if card == nil {
				return customerSendErr{http.StatusNotFound, "Your catalogue has no such item."}
			}
		case "booking":
			items, err := storefront.Services(ctx, tx, tenant)
			if err != nil {
				return err
			}
			for _, it := range items {
				if _, ok := storefront.ServiceOf(it); ok && it.ID == ask.ServiceID {
					card = &customerCard{Type: "booking", ServiceID: it.ID, Title: "Book " + it.Name, Minutes: it.DurationMinutes, TotalKobo: it.PriceKobo}
				}
			}
			if card == nil {
				return customerSendErr{http.StatusNotFound, "You have no such service to book."}
			}
		default:
			return customerSendErr{http.StatusBadRequest, "A card is an order, pay, booking or product."}
		}
		return nil
	})
	return card, err
}

// mirrorBusinessReadTx shows the customer, in their copy, that the
// business read their messages (an app thread only).
func (s *Server) mirrorBusinessReadTx(ctx context.Context, tx pgx.Tx, tenant, thread uuid.UUID, ids []uuid.UUID) error {
	var line string
	var person *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT line, person_id FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, thread).Scan(&line, &person); err != nil {
		return err
	}
	if line != lineApp || person == nil {
		return nil
	}
	if err := reachTx(ctx, tx, tenant, *person, "customer.read", nil); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE person_messages SET read_at = now() WHERE person_id = $1 AND tenant_id = $2 AND id = ANY ($3) AND read_at IS NULL`, *person, tenant, ids)
	return err
}

// appChatExtrasTx adds what one app thread's page shows: the customer's
// answer to offers, and whether they are typing.
func appChatExtrasTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, v *customerChatView) error {
	if v.Line != lineApp || v.person == nil {
		return nil
	}
	var answer *bool
	if err := tx.QueryRow(ctx, `SELECT answer FROM customer_offer_consents WHERE tenant_id = $1 AND person_id = $2`, tenant, *v.person).Scan(&answer); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	v.Offers = "unasked"
	if answer != nil {
		v.Offers = map[bool]string{true: "yes", false: "no"}[*answer]
	}
	var typing bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_customer_typing_of($1, $2, 'person')`, tenant, *v.person).Scan(&typing); err != nil {
		return err
	}
	v.Typing = &typing
	return nil
}

// appThreadIn is an app thread of the business in scope, with its
// person; errCustomerChatNone for a WhatsApp thread or none.
func (s *Server) appThreadIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) (customerChatView, string, error) {
	chat, subject, err := s.customerChatIn(ctx, tx, tenant, id)
	if err != nil {
		return chat, subject, err
	}
	if chat.Line != lineApp || chat.person == nil {
		return chat, subject, customerSendErr{http.StatusConflict, "This is for customers who chat in the Taskiem app; on WhatsApp they send STOP."}
	}
	return chat, subject, nil
}

// blockCustomer is POST (block) and DELETE (unblock)
// /v1/customer-chats/{id}/block: the business stops an app customer
// writing to it (§8.3). Nothing is deleted; the customer's copy says so.
func (s *Server) blockCustomer(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	block := r.Method == http.MethodPost
	err := s.tx(r, func(tx pgx.Tx) error {
		chat, _, err := s.appThreadIn(ctx, tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET blocked_at = CASE WHEN $3 THEN COALESCE(blocked_at, now()) END WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, chat.ID, block); err != nil {
			return err
		}
		if err := reachTx(ctx, tx, p.TenantID, *chat.person, "customer.block", map[string]any{"actor": p.Actor(), "actor_type": p.ActorType(), "blocked": block}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE person_conversations SET business_blocked_at = CASE WHEN $3 THEN COALESCE(business_blocked_at, now()) END
			WHERE person_id = $1 AND tenant_id = $2`, *chat.person, p.TenantID, block)
		return err
	})
	if err != nil {
		s.customerSendFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// customerTyping is POST /v1/customer-chats/{id}/typing: someone of the
// business is typing to an app customer, for a few seconds.
func (s *Server) customerTyping(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		chat, _, err := s.appThreadIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `SELECT taskiem_customer_typing($1, $2, 'business')`, p.TenantID, *chat.person)
		return err
	})
	if err != nil {
		s.customerSendFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendCustomerFile is POST /v1/customer-chats/{id}/files: a photo, voice
// note or PDF to an app customer (multipart: file; text, a caption;
// client_id). Scanned and checked before anything is kept; each side
// keeps its own copy.
func (s *Server) sendCustomerFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	f, err := s.readChatFile(w, r)
	if err != nil {
		s.customerSendFail(w, r, err)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	text := strings.TrimSpace(r.FormValue("text"))
	if len([]rune(text)) > customerReplyMax {
		writeErr(w, http.StatusBadRequest, "a caption is up to "+strconv.Itoa(customerReplyMax)+" characters")
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
	m, fresh, err := s.sendCustomer(ctx, p.TenantID, chi.URLParam(r, "id"), customerSend{Text: text, ClientID: clientID, SenderID: p.Actor(),
		SenderName: s.memberFirstName(ctx, p), File: f})
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

// customerFile is GET /v1/customer-chats/{id}/files/{message}: the
// business's own copy of an app message's file.
func (s *Server) customerFile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var path, ctype, name, kind string
	err := s.tx(r, func(tx pgx.Tx) error {
		chat, _, err := s.customerChatIn(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		msg, err := uuid.Parse(chi.URLParam(r, "message"))
		if err != nil {
			return pgx.ErrNoRows
		}
		return tx.QueryRow(r.Context(), `SELECT attachment->>'path', attachment->>'type', COALESCE(attachment->>'name', ''), attachment->>'kind' FROM customer_messages
			WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3 AND attachment ? 'path'`, p.TenantID, chat.ID, msg).Scan(&path, &ctype, &name, &kind)
	})
	if err != nil {
		s.customerChatFail(w, r, err)
		return
	}
	if s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	data, _, err := s.Media.GetFile(r.Context(), p.TenantID, path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	serveChatFile(w, data, ctype, name, kind)
}

// serveChatFile writes a conversation's file: shown in the page for a
// photo or voice note, downloaded for a file.
func serveChatFile(w http.ResponseWriter, data []byte, ctype, name, kind string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	disp := "inline"
	if kind == "file" {
		disp = "attachment"
	}
	if name != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	} else {
		w.Header().Set("Content-Disposition", disp)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// openAppCustomerChat is POST /v1/customer-chats/app {reference}: the
// business writes first to a person who ordered or booked with it in the
// app (§8.1, §15): their app thread, opened if it is not yet.
func (s *Server) openAppCustomerChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var in struct {
		Reference string `json:"reference"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "orders are not set up on this server")
		return
	}
	var v customerChatView
	err := s.tx(r, func(tx pgx.Tx) error {
		var person *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT person_id FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, p.TenantID, in.Reference).Scan(&person)
		if errors.Is(err, pgx.ErrNoRows) {
			return customerSendErr{http.StatusNotFound, "No order has that reference."}
		}
		if err != nil {
			return err
		}
		if person == nil {
			return customerSendErr{http.StatusConflict, "This order was not made in the Taskiem app, so there is no app conversation to open."}
		}
		ord, err := s.Orders.GetOrder(ctx, tx, p.TenantID, in.Reference)
		if err != nil {
			return err
		}
		var phone *string
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, *person).Scan(&phone); err != nil {
			return err
		}
		if phone == nil {
			phone = &ord.Customer.Phone
		}
		thread, _, err := s.appThreadTx(ctx, tx, p.TenantID, *person, *phone, ord.Customer.Name)
		if err != nil {
			return err
		}
		v, _, err = s.customerChatIn(ctx, tx, p.TenantID, thread.String())
		return err
	})
	if err != nil {
		s.customerSendFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// appThreadTx is the business's app thread with a person, made when they
// have none: sealed under the subject of their phone in this business,
// with their name. The business is in scope.
func (s *Server) appThreadTx(ctx context.Context, tx pgx.Tx, tenant, person uuid.UUID, phone, name string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var subject string
	err := tx.QueryRow(ctx, `SELECT id, subject_id FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`, tenant, person).Scan(&id, &subject)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return id, subject, err
	}
	env, err := s.Store.PII.SealTx(ctx, tx, tenant, "phone", phone)
	if err != nil {
		return id, "", err
	}
	subject, _ = env["subject"].(string)
	if subject == "" {
		return id, "", errors.New("customer door: the phone has no subject")
	}
	who := map[string]any{"number": env}
	if name = strings.Join(strings.Fields(name), " "); name != "" {
		if r := []rune(name); len(r) > 80 {
			name = string(r[:80])
		}
		if who["name"], err = pii.SealText(ctx, s.Store.PII, tx, tenant, subject, "name", name); err != nil {
			return id, "", err
		}
	}
	raw, err := json.Marshal(who)
	if err != nil {
		return id, "", err
	}
	id = uuid.Must(uuid.NewV7())
	err = tx.QueryRow(ctx, `INSERT INTO customer_conversations (id, tenant_id, phone_number_id, subject_id, who, line, person_id)
		VALUES ($1, $2, 'app', $3, $4, 'app', $5)
		ON CONFLICT (tenant_id, line, phone_number_id, subject_id) DO UPDATE SET person_id = EXCLUDED.person_id
		RETURNING id`, id, tenant, subject, raw, person).Scan(&id)
	return id, subject, err
}

// broadcastOffer is POST /v1/customer-chats/offers {text, client_id}: an
// offer to every app customer who agreed to offers (§15), marked Offer,
// counted against each one's two a week. Who has had two this week, or
// blocked either way, is skipped. A repeat of client_id sends each once.
func (s *Server) broadcastOffer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var in struct {
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || len([]rune(in.Text)) > 1000 {
		writeErr(w, http.StatusBadRequest, "the offer's words are required (up to 1,000 characters)")
		return
	}
	if len(in.ClientID) > 60 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 60 characters")
		return
	}
	if in.ClientID == "" {
		in.ClientID = uuid.NewString()
	}
	var threads []uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.id FROM customer_conversations c JOIN customer_offer_consents o ON o.tenant_id = c.tenant_id AND o.person_id = c.person_id AND o.answer
			WHERE c.tenant_id = $1 AND c.line = 'app' AND c.blocked_at IS NULL AND c.person_blocked_at IS NULL ORDER BY c.last_message_at DESC LIMIT 5000`, p.TenantID)
		if err != nil {
			return err
		}
		threads, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	sender := s.memberFirstName(ctx, p)
	sent, skipped := 0, 0
	for _, t := range threads {
		_, fresh, err := s.sendCustomer(ctx, p.TenantID, t.String(), customerSend{Text: in.Text, ClientID: "offer:" + in.ClientID, SenderID: p.Actor(), SenderName: sender, Offer: true})
		var se customerSendErr
		switch {
		case err == nil && fresh:
			sent++
		case err == nil, errors.As(err, &se):
			skipped++
		default:
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": sent, "skipped": skipped})
}

// --- when nobody answers (§8.6) ---

// customerChatSettings is what a business does when nobody answers.
type customerChatSettings struct {
	// ClosedReply: a message while closed gets the closed reply, once a
	// day; ClosedWords are the business's own words for it ("" for
	// Taskiem's: "We're closed now. We open at 9:00 and will answer then.").
	ClosedReply bool   `json:"closed_reply"`
	ClosedWords string `json:"closed_words"`
	// NeedsYou: a customer waiting 30 minutes in open hours is on Needs
	// you; TellOwner: after 2 hours, the owner is told.
	NeedsYou  bool `json:"needs_you"`
	TellOwner bool `json:"tell_owner"`
}

func customerChatSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (customerChatSettings, error) {
	st := customerChatSettings{ClosedReply: true, NeedsYou: true, TellOwner: true}
	var words *string
	err := tx.QueryRow(ctx, `SELECT closed_reply, closed_words, needs_you, tell_owner FROM customer_chat_settings WHERE tenant_id = $1`, tenant).
		Scan(&st.ClosedReply, &words, &st.NeedsYou, &st.TellOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if words != nil {
		st.ClosedWords = *words
	}
	return st, err
}

// getCustomerChatSettings is GET /v1/settings/customer-chats.
func (s *Server) getCustomerChatSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var st customerChatSettings
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		st, err = customerChatSettingsTx(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putCustomerChatSettings is PUT /v1/settings/customer-chats: each of
// the closed reply (and its words), Needs you and telling the owner, on
// or off.
func (s *Server) putCustomerChatSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var in struct {
		ClosedReply *bool   `json:"closed_reply"`
		ClosedWords *string `json:"closed_words"`
		NeedsYou    *bool   `json:"needs_you"`
		TellOwner   *bool   `json:"tell_owner"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var st customerChatSettings
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if st, err = customerChatSettingsTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if in.ClosedReply != nil {
			st.ClosedReply = *in.ClosedReply
		}
		if in.NeedsYou != nil {
			st.NeedsYou = *in.NeedsYou
		}
		if in.TellOwner != nil {
			st.TellOwner = *in.TellOwner
		}
		if in.ClosedWords != nil {
			words := strings.TrimSpace(*in.ClosedWords)
			if len([]rune(words)) > 300 {
				return fmt.Errorf("%w: the closed reply is up to 300 characters", errBadRequest)
			}
			if hasLinkText(words) {
				return fmt.Errorf("%w: the closed reply cannot hold links", errBadRequest)
			}
			st.ClosedWords = words
		}
		var words *string
		if st.ClosedWords != "" {
			words = &st.ClosedWords
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO customer_chat_settings (tenant_id, closed_reply, closed_words, needs_you, tell_owner, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (tenant_id) DO UPDATE SET closed_reply = EXCLUDED.closed_reply, closed_words = EXCLUDED.closed_words,
			needs_you = EXCLUDED.needs_you, tell_owner = EXCLUDED.tell_owner, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			p.TenantID, st.ClosedReply, words, st.NeedsYou, st.TellOwner, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.customer_chats", p.TenantID.String(), map[string]any{"closed_reply": st.ClosedReply, "needs_you": st.NeedsYou, "tell_owner": st.TellOwner})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// businessHoursTx is the business's opening hours, from its shop page;
// known is false when it has none.
func businessHoursTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (storefront.Hours, bool, error) {
	sf, err := storefront.Get(ctx, tx, tenant)
	if errors.Is(err, storefront.ErrNotFound) {
		return storefront.Hours{}, false, nil
	}
	if err != nil {
		return storefront.Hours{}, false, err
	}
	_, known := sf.Draft.Hours.OpenNow(time.Now())
	return sf.Draft.Hours, known, nil
}

// nextOpening is when the hours open next after t, within a week.
func nextOpening(h storefront.Hours, t time.Time) (time.Time, bool) {
	t = t.In(storefront.Lagos)
	day0 := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, storefront.Lagos)
	for d := range 8 {
		date := day0.AddDate(0, 0, d)
		wd := (int(date.Weekday()) + 6) % 7
		var best time.Time
		for _, sp := range h.Days[wd] {
			if len(sp.Open) != 5 {
				continue
			}
			hh, _ := strconv.Atoi(sp.Open[:2])
			mm, _ := strconv.Atoi(sp.Open[3:])
			at := date.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
			if at.After(t) && (best.IsZero() || at.Before(best)) {
				best = at
			}
		}
		if !best.IsZero() {
			return best, true
		}
	}
	return time.Time{}, false
}

// closedWords is Taskiem's closed reply in plain words.
func closedWords(h storefront.Hours, now time.Time) string {
	at, ok := nextOpening(h, now)
	if !ok {
		return "We're closed now and will answer when we open."
	}
	now = now.In(storefront.Lagos)
	when := "at " + at.Format("15:04")
	switch days := int(time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, storefront.Lagos).Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)).Hours() / 24); days {
	case 0:
	case 1:
		when = "tomorrow at " + at.Format("15:04")
	default:
		when = "on " + at.Format("Monday") + " at " + at.Format("15:04")
	}
	return "We're closed now. We open " + when + " and will answer then."
}

// closedReplyTx answers a customer's message, in the business's name,
// when the business is closed by its hours: once a day per thread, with
// its own words or Taskiem's, unless it turned the reply off (§8.6). The
// transaction has both the business and the person in scope.
func (s *Server) closedReplyTx(ctx context.Context, tx pgx.Tx, tenant, thread, person uuid.UUID, subject string) error {
	st, err := customerChatSettingsTx(ctx, tx, tenant)
	if err != nil || !st.ClosedReply {
		return err
	}
	h, known, err := businessHoursTx(ctx, tx, tenant)
	if err != nil || !known {
		return err
	}
	now := time.Now()
	if open, _ := h.OpenNow(now); open {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_conversations SET closed_reply_at = now() WHERE id = $1 AND (closed_reply_at IS NULL OR closed_reply_at < now() - interval '1 day')`, thread)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	words := st.ClosedWords
	if words == "" {
		words = closedWords(h, now)
	}
	_, _, err = s.writeAppMessageTx(ctx, tx, appMessage{id: uuid.Must(uuid.NewV7()), tenant: tenant, thread: thread, subject: subject, person: person,
		kind: "text", text: words, senderID: "system:closed", system: true})
	return err
}

// customerWaitingAlerts are app customers waiting for an answer for 30
// minutes or more while the business is open (Needs you, §8.6), for
// those who answer customers, unless the business turned it off.
func (s *Server) customerWaitingAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermOrders) {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		st, err := customerChatSettingsTx(ctx, tx, p.TenantID)
		if err != nil || !st.NeedsYou {
			return err
		}
		h, known, err := businessHoursTx(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if open, _ := h.OpenNow(time.Now()); known && !open {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id, last_inbound_at FROM customer_conversations WHERE tenant_id = $1 AND line = 'app'
			AND last_inbound_at < now() - interval '30 minutes' AND last_inbound_at > now() - interval '3 days'
			AND (last_reply_at IS NULL OR last_reply_at < last_inbound_at) AND blocked_at IS NULL AND NOT archived
			ORDER BY last_inbound_at DESC LIMIT $2`, p.TenantID, perKind)
		if err != nil {
			return err
		}
		type waiting struct {
			id uuid.UUID
			at time.Time
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (waiting, error) {
			var x waiting
			return x, r.Scan(&x.id, &x.at)
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			chat, _, err := s.customerChatIn(ctx, tx, p.TenantID, x.id.String())
			if err != nil {
				return err
			}
			mins := int(time.Since(x.at).Minutes())
			line := fmt.Sprintf("Wrote %d minutes ago", mins)
			if mins >= 120 {
				line = fmt.Sprintf("Wrote %d hours ago", mins/60)
			}
			out = append(out, chatAlert{Key: "customer_waiting:" + x.id.String() + ":" + strconv.FormatInt(x.at.Unix(), 10), Kind: "customer_waiting", At: x.at,
				ID: x.id.String(), Title: chat.Name + " is waiting for an answer", Line: line})
		}
		return nil
	})
	return out, err
}

// RunCustomerChats tells owners of customers waiting until ctx ends.
func (s *Server) RunCustomerChats(ctx context.Context) error {
	for {
		if err := s.CustomerChatsTick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("customer chats", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

// CustomerChatsTick tells each business's owners of an app customer who
// has waited 2 hours with no answer, once per wait, while the business is
// open (or keeps no hours), unless it turned this off (§8.6).
func (s *Server) CustomerChatsTick(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, conversation_id FROM taskiem_customer_waiting('2 hours')`)
	if err != nil {
		return err
	}
	type due struct{ tenant, thread uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.tenant, &d.thread)
	})
	if err != nil {
		return err
	}
	byTenant := map[uuid.UUID][]uuid.UUID{}
	for _, d := range list {
		byTenant[d.tenant] = append(byTenant[d.tenant], d.thread)
	}
	var errs []error
	for tenant, threads := range byTenant {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			st, err := customerChatSettingsTx(ctx, tx, tenant)
			if err != nil {
				return err
			}
			if st.TellOwner {
				h, known, err := businessHoursTx(ctx, tx, tenant)
				if err != nil {
					return err
				}
				if open, _ := h.OpenNow(time.Now()); known && !open {
					return nil // told once it opens, if still waiting
				}
				owners, err := ownersTx(ctx, tx)
				if err != nil {
					return err
				}
				for _, t := range threads {
					chat, _, err := s.customerChatIn(ctx, tx, tenant, t.String())
					if err != nil {
						return err
					}
					if err := notices.AddTx(ctx, tx, tenant, owners, notices.Notice{Kind: notices.Setup, Level: notices.Warning,
						Title: chat.Name + " has waited 2 hours for an answer", Link: "/chat?with=c-" + t.String(), Group: "customer_waiting:" + t.String()}); err != nil {
						return err
					}
				}
			}
			_, err = tx.Exec(ctx, `UPDATE customer_conversations SET owner_told_at = now() WHERE tenant_id = $1 AND id = ANY ($2)`, tenant, threads)
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", tenant, err))
		}
	}
	return errors.Join(errs...)
}

// ownersTx are the owners of the business in scope.
func ownersTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE role = 'owner'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
