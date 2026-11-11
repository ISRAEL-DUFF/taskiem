package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/orders/read"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Orders from customers' messages (docs/spec/chat-five-more-flows.md, flow
// 1, phase 2). What a customer writes to the business's own number is read
// (engine/orders/read) against the business's catalogue into a draft order:
// a card in their thread, for staff who handle orders. Staff answer its
// questions, change it, send its questions to the customer as one message,
// and confirm it: then it is an order of the kind chat (storefront), and
// the customer gets the order, its total and how to pay, in their thread.
// Nothing is confirmed, priced or sent by the model.

// draftBody is a draft as kept (sealed under the customer's subject): what
// was read, and what staff set.
type draftBody struct {
	read.Draft
	Zone         string `json:"zone,omitempty"`
	DeliveryKobo int64  `json:"delivery_kobo,omitempty"`
	Pay          string `json:"pay,omitempty"`
	// Priced are the lines whose price staff set: kept when the catalogue's
	// change.
	Priced []int `json:"priced,omitempty"`
}

func (b draftBody) total() int64 {
	t := b.Total()
	if b.Fulfilment == "delivery" {
		t += b.DeliveryKobo
	}
	return t
}

func (b draftBody) priced(i int) bool {
	for _, p := range b.Priced {
		if p == i {
			return true
		}
	}
	return false
}

// orderDraftView is a draft as its card shows it.
type orderDraftView struct {
	ID           uuid.UUID   `json:"id"`
	ChatID       uuid.UUID   `json:"chat_id"`
	MessageID    *uuid.UUID  `json:"message_id,omitempty"`
	Status       string      `json:"status"`
	Lines        []read.Line `json:"lines"`
	Fulfilment   string      `json:"fulfilment,omitempty"`
	Address      string      `json:"address,omitempty"`
	When         string      `json:"when,omitempty"`
	Note         string      `json:"note,omitempty"`
	Zone         string      `json:"zone,omitempty"`
	DeliveryKobo int64       `json:"delivery_kobo"`
	Pay          string      `json:"pay,omitempty"`
	ItemsKobo    int64       `json:"items_kobo"`
	TotalKobo    int64       `json:"total_kobo"`
	// Questions is how many lines wait on a question; Ready, whether it
	// can be confirmed as it stands.
	Questions int  `json:"questions"`
	Ready     bool `json:"ready"`
	// Waits is why it cannot be confirmed yet, in a sentence.
	Waits       string     `json:"waits,omitempty"`
	Version     int        `json:"version"`
	Reference   string     `json:"reference,omitempty"`
	OrderStatus string     `json:"order_status,omitempty"`
	ByReader    bool       `json:"by_reader"`
	AskedAt     *time.Time `json:"asked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// draftRow is a draft as read, opened.
type draftRow struct {
	id, chat, tenant uuid.UUID
	message          *uuid.UUID
	status           string
	body             draftBody
	version          int
	order            *uuid.UUID
	reference        string
	madeBy           string
	askedAt          *time.Time
	createdAt        time.Time
	updatedAt        time.Time
	subject          string // the customer's, from the thread
	orderStatus      string
}

const draftCols = `d.id, d.conversation_id, d.tenant_id, d.message_id, d.status, d.body, d.version, d.order_id, COALESCE(d.reference, ''), d.made_by,
	d.asked_at, d.created_at, d.updated_at, c.subject_id, COALESCE(o.status, '')`

const draftFrom = ` FROM order_drafts d JOIN customer_conversations c ON c.id = d.conversation_id LEFT JOIN storefront_orders o ON o.id = d.order_id`

// errDraftNone is a draft the business does not have.
var errDraftNone = errors.New("no such order")

// errDraftChanged is a change made on a draft someone changed since.
var errDraftChanged = customerSendErr{http.StatusConflict, "someone changed this order since you opened it; look at it again"}

func (s *Server) scanDrafts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]draftRow, error) {
	type raw struct {
		r    draftRow
		body []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var x raw
		err := r.Scan(&x.r.id, &x.r.chat, &x.r.tenant, &x.r.message, &x.r.status, &x.body, &x.r.version, &x.r.order, &x.r.reference, &x.r.madeBy,
			&x.r.askedAt, &x.r.createdAt, &x.r.updatedAt, &x.r.subject, &x.r.orderStatus)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]draftRow, 0, len(list))
	for _, x := range list {
		text, err := s.openCustomerText(ctx, tx, tenant, x.body)
		if err != nil {
			return nil, err
		}
		if text != "" {
			_ = json.Unmarshal([]byte(text), &x.r.body)
		}
		out = append(out, x.r)
	}
	return out, nil
}

// draftTx reads one draft, locked for a change when lock says so.
func (s *Server) draftTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idText string, lock bool) (draftRow, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return draftRow{}, errDraftNone
	}
	q := `SELECT ` + draftCols + draftFrom + ` WHERE d.tenant_id = $1 AND d.id = $2`
	if lock {
		q += ` FOR UPDATE OF d`
	}
	rows, err := tx.Query(ctx, q, tenant, id)
	if err != nil {
		return draftRow{}, err
	}
	list, err := s.scanDrafts(ctx, tx, tenant, rows)
	if err != nil {
		return draftRow{}, err
	}
	if len(list) == 0 {
		return draftRow{}, errDraftNone
	}
	return list[0], nil
}

// saveDraftTx writes a draft's body (sealed), total and status, one
// version on, by who.
func (s *Server) saveDraftTx(ctx context.Context, tx pgx.Tx, d *draftRow, by string) error {
	read.EnsureIDs(&d.body.Draft)
	raw, _ := json.Marshal(d.body)
	sealed, err := pii.SealText(ctx, s.Store.PII, tx, d.tenant, d.subject, "other", string(raw))
	if err != nil {
		return err
	}
	d.version++
	var ref, confirmedBy *string
	if d.order != nil {
		ref, confirmedBy = &d.reference, &by
	}
	return tx.QueryRow(ctx, `UPDATE order_drafts SET body = $3, total_kobo = $4, status = $5, version = $6, changed_by = $7, asked_at = $8,
		order_id = COALESCE($9, order_id), reference = COALESCE($10, reference), confirmed_by = COALESCE(confirmed_by, $11), updated_at = now()
		WHERE tenant_id = $1 AND id = $2 RETURNING updated_at`,
		d.tenant, d.id, sealed, d.body.total(), d.status, d.version, by, d.askedAt, d.order, ref, confirmedBy).Scan(&d.updatedAt)
}

func (d draftRow) view() orderDraftView {
	lines := d.body.Lines
	if lines == nil {
		lines = []read.Line{}
	}
	v := orderDraftView{ID: d.id, ChatID: d.chat, MessageID: d.message, Status: d.status, Lines: lines,
		Fulfilment: d.body.Fulfilment, Address: d.body.Address, When: d.body.When, Note: d.body.Note, Zone: d.body.Zone,
		DeliveryKobo: d.body.DeliveryKobo, Pay: d.body.Pay, ItemsKobo: d.body.Total(), TotalKobo: d.body.total(),
		Questions: len(d.body.Open()), Version: d.version, Reference: d.reference, OrderStatus: d.orderStatus,
		ByReader: d.madeBy == "reader", AskedAt: d.askedAt, CreatedAt: d.createdAt, UpdatedAt: d.updatedAt}
	if d.body.Fulfilment != "delivery" {
		v.DeliveryKobo = 0
	}
	if why := d.ready(); why != "" {
		if d.status == "open" || d.status == "asked" {
			v.Waits = capitalFirst(why) + "."
		}
	} else {
		v.Ready = true
	}
	return v
}

// ready says why a draft cannot be confirmed yet, or "".
func (d draftRow) ready() string {
	b := d.body
	switch {
	case d.status != "open" && d.status != "asked":
		return "this order is " + d.status
	case len(b.Lines) == 0:
		return "the order has no items"
	case len(b.Open()) > 0:
		return "answer its questions first"
	case !b.Complete():
		return "every line needs an item from your catalogue, how many, and a price"
	case b.Fulfilment == "":
		return "say whether it is delivered or picked up"
	case b.Fulfilment == "delivery" && b.Address == "" && b.Zone == "":
		return "say where to deliver it"
	}
	for _, l := range b.Lines {
		if l.Out {
			if l.Left != nil && *l.Left > 0 {
				return fmt.Sprintf("only %d %s left: change how many, or remove it", *l.Left, l.Name)
			}
			return l.Name + " is not available now: remove it, or pick another"
		}
	}
	return ""
}

// --- reading ---

// readCustomerOrder reads what a customer wrote into the thread's order:
// their answers, and anything more they ask for, onto the draft being
// worked on; else a new draft, when it orders anything. It runs after the
// message is kept, away from the webhook (WhatsApp waits for no one), and
// only within the plan's AI, the business's AI budget and a pace per
// thread. Nothing is said to the customer.
func (s *Server) readCustomerOrder(tenant, chat, message uuid.UUID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// A few at a time on this server, so a burst of messages never runs
	// many model calls at once; one waits its turn within its time.
	select {
	case orderReads <- struct{}{}:
		defer func() { <-orderReads }()
	case <-ctx.Done():
		s.Logger.Warn("orders: a customer's message not read in time", "tenant", tenant)
		return
	}
	if err := s.readCustomerOrderNow(ctx, tenant, chat, message, text); err != nil && !errors.Is(err, context.Canceled) {
		s.Logger.Warn("orders: reading a customer's message", "tenant", tenant, "err", err)
	}
}

// orderReads holds the background reads (and voice notes put in words)
// running at once on this server.
var orderReads = make(chan struct{}, 8)

// customerReadable reports whether a business's customers' messages are
// read for orders: Orders on, a model, the plan's AI and budget left.
func (s *Server) customerReadable(ctx context.Context, tenant uuid.UUID) (ai.Provider, bool) {
	model := s.chatModel()
	if model == nil || s.Orders == nil {
		return nil, false
	}
	if s.Billing != nil && s.Billing.Require(ctx, tenant, billing.FeatureAI) != nil {
		return nil, false
	}
	var on areas.State
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) (err error) {
		on, err = areas.Load(ctx, tx, tenant)
		return err
	}); err != nil || !on.On(areas.Orders) {
		return nil, false
	}
	limit, used, err := s.aiBudget(ctx, tenant)
	if err != nil || (limit > 0 && used >= limit) {
		return nil, false
	}
	// A customer's words leave with their numbers and emails masked.
	return ai.Redacted(model), true
}

func (s *Server) readCustomerOrderNow(ctx context.Context, tenant, chat, message uuid.UUID, text string) error {
	if !s.limiter("order-read:"+chat.String(), time.Minute, 6).Allow() || !s.limiter("order-read-tenant:"+tenant.String(), 2*time.Second, 30).Allow() {
		return nil
	}
	model, ok := s.customerReadable(ctx, tenant)
	if !ok {
		return nil
	}
	items, err := s.catalogueFor(ctx, tenant)
	if err != nil || len(items) == 0 {
		return err
	}
	// The draft being worked on, if any: the message may answer it. A
	// customer who sent STOP is not read: nothing can be sent to them.
	var open *draftRow
	var stopped bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT stopped_at IS NOT NULL FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, chat).Scan(&stopped); err != nil || stopped {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+draftCols+draftFrom+` WHERE d.tenant_id = $1 AND d.conversation_id = $2 AND d.status IN ('open', 'asked')`, tenant, chat)
		if err != nil {
			return err
		}
		list, err := s.scanDrafts(ctx, tx, tenant, rows)
		if len(list) > 0 {
			open = &list[0]
		}
		return err
	})
	if err != nil || stopped {
		return err
	}
	if open == nil && !read.MightOrder(text, items) {
		return nil
	}
	// The questions as numbered for the model, by the lines they wait on:
	// an answer goes to its own line, whatever staff change meanwhile.
	var asked []read.Question
	var askedIDs []string
	if open != nil {
		read.EnsureIDs(&open.body.Draft)
		askedIDs = open.body.AskedIDs()
		for _, id := range askedIDs {
			asked = append(asked, *open.body.Lines[open.body.LineAt(id)].Ask)
		}
	}
	req := read.Request(text, items, asked)
	release, err := s.reserveAI(ctx, tenant, "order_read", req.Estimate(), chatAIReservation)
	if err != nil {
		// Over the business's AI budget: the message is not read, and that
		// is not a failure to log.
		return nil //nolint:nilerr // no budget left is not an error here
	}
	resp, err := model.Complete(ctx, req)
	said, perr := read.Parse(resp)
	outcome, errText := "valid", ""
	switch {
	case err != nil:
		outcome, errText = "error", err.Error()
	case perr != nil:
		outcome = "unparseable"
	case !said.Orderable && len(said.Answers) == 0:
		outcome = "unknown"
	}
	s.recordModelCallFor(ctx, tenant, "system:orders", "order_read", "orderread", model, resp, outcome, errText)
	release()
	if outcome != "valid" {
		return err
	}
	onto := func(tx pgx.Tx, id uuid.UUID, said read.Said, asked []string) error {
		d, err := s.draftTx(ctx, tx, tenant, id.String(), true)
		if err != nil {
			return err
		}
		if d.status != "open" && d.status != "asked" {
			return nil
		}
		if !read.ApplyAnswers(&d.body.Draft, said, items, asked) {
			return nil
		}
		read.Refresh(&d.body.Draft, items, d.body.priced)
		if d.status == "asked" && len(d.body.Open()) == 0 {
			// Every question answered: back with staff.
			d.status = "open"
		}
		return s.saveDraftTx(ctx, tx, &d, "reader")
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if open != nil {
			return onto(tx, open.id, said, askedIDs)
		}
		draft := read.Match(said, items)
		if len(draft.Lines) == 0 {
			return nil
		}
		var subject string
		if err := tx.QueryRow(ctx, `SELECT subject_id FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, chat).Scan(&subject); err != nil {
			return err
		}
		d := draftRow{id: uuid.Must(uuid.NewV7()), chat: chat, tenant: tenant, message: &message, status: "open", subject: subject,
			body: draftBody{Draft: draft}}
		return s.insertDraftTx(ctx, tx, &d, "reader")
	})
	if !errors.Is(err, errDraftOpen) {
		return err
	}
	// Another message of theirs made the thread's order meanwhile (an order
	// sent in two messages): this one's lines go onto it.
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM order_drafts WHERE tenant_id = $1 AND conversation_id = $2 AND status IN ('open', 'asked')`, tenant, chat).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		return onto(tx, id, read.Said{Lines: said.Lines, Fulfilment: said.Fulfilment, Address: said.Address, When: said.When}, nil)
	})
}

// insertDraftTx writes a new draft; a thread already working on one keeps
// that one (errDraftOpen).
func (s *Server) insertDraftTx(ctx context.Context, tx pgx.Tx, d *draftRow, by string) error {
	read.EnsureIDs(&d.body.Draft)
	raw, _ := json.Marshal(d.body)
	sealed, err := pii.SealText(ctx, s.Store.PII, tx, d.tenant, d.subject, "other", string(raw))
	if err != nil {
		return err
	}
	d.version, d.madeBy = 1, by
	tag, err := tx.Exec(ctx, `INSERT INTO order_drafts (id, tenant_id, conversation_id, message_id, status, body, total_kobo, made_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (conversation_id) WHERE status IN ('open', 'asked') DO NOTHING`,
		d.id, d.tenant, d.chat, d.message, d.status, sealed, d.body.total(), by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errDraftOpen
	}
	return nil
}

var errDraftOpen = customerSendErr{http.StatusConflict, "this customer already has an order being worked on; finish or drop it first"}

// catalogueFor is the business's catalogue, refreshed from its table first
// (the copy kept when the table cannot be read).
func (s *Server) catalogueFor(ctx context.Context, tenant uuid.UUID) ([]storefront.Item, error) {
	// From the table at most once a minute per business: a card refreshes
	// every few seconds while a thread is open, and a read per message.
	if catalogueSyncDue(tenant, time.Now()) {
		if err := s.SyncStorefront(ctx, tenant); err != nil {
			s.Logger.Warn("orders: refreshing the catalogue", "tenant", tenant, "err", err)
		}
	}
	var items []storefront.Item
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		items, err = storefront.Catalogue(ctx, tx, tenant)
		return err
	})
	return items, err
}

var (
	catalogueSyncMu sync.Mutex
	catalogueSynced = map[uuid.UUID]time.Time{}
)

// catalogueSyncDue reports whether a business's catalogue copy is due a
// refresh from its table (a minute since the last), and marks it done.
func catalogueSyncDue(tenant uuid.UUID, now time.Time) bool {
	catalogueSyncMu.Lock()
	defer catalogueSyncMu.Unlock()
	if last, ok := catalogueSynced[tenant]; ok && now.Sub(last) < time.Minute {
		return false
	}
	if len(catalogueSynced) > 10000 {
		clear(catalogueSynced)
	}
	catalogueSynced[tenant] = now
	return true
}

// --- the API ---

func (s *Server) orderDraftRoutes(r chi.Router) {
	r.With(s.need(PermOrders)).Get("/customer-chats/{id}/orders", s.listOrderDrafts)
	r.With(s.need(PermOrders), s.tenantWide).Post("/customer-chats/{id}/orders", s.startOrderDraft)
	r.With(s.need(PermOrders), s.tenantWide).Put("/order-drafts/{id}", s.changeOrderDraft)
	r.With(s.need(PermOrders), s.tenantWide).Post("/order-drafts/{id}/ask", s.askOrderDraft)
	r.With(s.need(PermOrders), s.tenantWide).Post("/order-drafts/{id}/confirm", s.confirmOrderDraft)
	r.With(s.need(PermOrders), s.tenantWide).Post("/order-drafts/{id}/tell", s.tellOrderDraft)
	r.With(s.need(PermOrders), s.tenantWide).Post("/order-drafts/{id}/drop", s.dropOrderDraft)
}

func (s *Server) orderDraftFail(w http.ResponseWriter, r *http.Request, err error) {
	var se customerSendErr
	switch {
	case errors.Is(err, errDraftNone):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.As(err, &se):
		writeErr(w, se.code, se.msg)
	case errors.Is(err, storefront.ErrNoPage):
		writeErr(w, http.StatusConflict, "paying by transfer needs your shop's page; choose pay when it arrives, or arrange payment yourself")
	case errors.Is(err, storefront.ErrInvalid):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), storefront.ErrInvalid.Error()+": "))
	default:
		s.customerChatFail(w, r, err)
	}
}

// draftOptions are what a card offers beside its lines: the business's
// delivery areas and fees, and whether it can send a page to pay on.
type draftOptions struct {
	Zones   []storefront.Zone `json:"zones"`
	PayPage bool              `json:"pay_page"`
}

func (s *Server) draftOptionsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (draftOptions, error) {
	o := draftOptions{Zones: []storefront.Zone{}}
	zones, err := storefront.Zones(ctx, tx, tenant)
	if err != nil {
		return o, err
	}
	if zones != nil {
		o.Zones = zones
	}
	var connection *string
	err = tx.QueryRow(ctx, `SELECT a.options->>'connection' FROM storefronts s LEFT JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = 'order' AND a.builtin
		WHERE s.tenant_id = $1`, tenant).Scan(&connection)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	o.PayPage = s.orderTransfers(ctx, tenant, deref(connection))
	return o, nil
}

// orderTransfers reports whether the business's payment rail can give a
// customer an account number for an order (its page then takes the
// payment); OrderTransfers says it instead when set (tests).
func (s *Server) orderTransfers(ctx context.Context, tenant uuid.UUID, connection string) bool {
	if s.OrderTransfers != nil {
		return s.OrderTransfers(ctx, tenant)
	}
	if s.Orders == nil || s.Orders.IssueTransfer == nil || s.Invoices == nil || s.Invoices.Rails == nil {
		return false
	}
	if i := strings.Index(connection, "/"); i >= 0 {
		connection = connection[i+1:]
	}
	rail, err := s.Invoices.Rails.Resolve(ctx, tenant, connection)
	return err == nil && rail.Transfer != nil
}

// listOrderDrafts answers GET /v1/customer-chats/{id}/orders: the thread's
// orders read or started, the latest 20, oldest first.
func (s *Server) listOrderDrafts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var out []orderDraftView
	var opts draftOptions
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		chat, _, err := s.customerChatIn(ctx, tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT * FROM (SELECT `+draftCols+draftFrom+` WHERE d.tenant_id = $1 AND d.conversation_id = $2
			ORDER BY d.created_at DESC LIMIT 20) x ORDER BY created_at`, p.TenantID, chat.ID)
		if err != nil {
			return err
		}
		list, err := s.scanDrafts(ctx, tx, p.TenantID, rows)
		if err != nil {
			return err
		}
		out = make([]orderDraftView, len(list))
		for i, d := range list {
			out[i] = d.view()
		}
		opts, err = s.draftOptionsTx(ctx, tx, p.TenantID)
		return err
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	items, err := s.catalogueFor(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out, "zones": opts.Zones, "pay_page": opts.PayPage, "items": pickable(items)})
}

// pickItem is an item staff may put on an order: each product on sale, and
// each size of one sold in sizes (a line names the size).
type pickItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
	Left      *int64 `json:"left,omitempty"`
}

func pickable(items []storefront.Item) []pickItem {
	out := []pickItem{}
	for _, it := range items {
		if !it.Available {
			continue
		}
		if len(it.Sizes) == 0 {
			out = append(out, pickItem{ID: it.ID, Name: it.Name, PriceKobo: it.PriceKobo, Left: it.Stock})
			continue
		}
		for _, z := range storefront.SizeItems(it) {
			out = append(out, pickItem{ID: z.ID, Name: z.Name, PriceKobo: z.PriceKobo, Left: z.Stock})
		}
	}
	return out
}

// startOrderDraft answers POST /v1/customer-chats/{id}/orders: an order
// staff start by hand in a customer's thread, with nothing in it yet.
func (s *Server) startOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var d draftRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		chat, subject, err := s.customerChatIn(ctx, tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if chat.Phone == "" {
			return customerSendErr{http.StatusConflict, "this customer's details were erased"}
		}
		d = draftRow{id: uuid.Must(uuid.NewV7()), chat: chat.ID, tenant: p.TenantID, status: "open", subject: subject}
		if err := s.insertDraftTx(ctx, tx, &d, p.Actor()); err != nil {
			return err
		}
		d, err = s.draftTx(ctx, tx, p.TenantID, d.id.String(), false)
		return err
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d.view())
}

// draftChange is one change to a draft, on the version it was made on.
type draftChange struct {
	Version int    `json:"version"`
	Op      string `json:"op"` // choose, quantity, price, remove, add, details
	Line    int    `json:"line"`
	ItemID  string `json:"item_id"`
	// Answer is the customer's or staff's answer to the line's question
	// (choose): a choice's number or words.
	Quantity     int64   `json:"quantity"`
	PriceKobo    int64   `json:"price_kobo"`
	Fulfilment   *string `json:"fulfilment"`
	Address      *string `json:"address"`
	When         *string `json:"when"`
	Note         *string `json:"note"`
	Zone         *string `json:"zone"`
	DeliveryKobo *int64  `json:"delivery_kobo"`
	Pay          *string `json:"pay"`
}

// changeOrderDraft answers PUT /v1/order-drafts/{id}: one change, made on
// the version the card shows (409 when it moved since).
func (s *Server) changeOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var c draftChange
	if err := decodeBody(r, &c); err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := s.catalogueFor(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d draftRow
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, p.TenantID, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		if d.status != "open" && d.status != "asked" {
			return customerSendErr{http.StatusConflict, "this order is " + d.status + "; it cannot be changed"}
		}
		if c.Version != d.version {
			return errDraftChanged
		}
		if err := applyDraftChange(&d.body, c, items); err != nil {
			return err
		}
		read.Refresh(&d.body.Draft, items, d.body.priced)
		if d.status == "asked" && len(d.body.Open()) == 0 {
			// Staff settled what the customer was asked: back with staff.
			d.status = "open"
		}
		return s.saveDraftTx(ctx, tx, &d, p.Actor())
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}

func badDraft(msg string) error { return customerSendErr{http.StatusBadRequest, msg} }

// applyDraftChange makes one change on a draft's body.
func applyDraftChange(b *draftBody, c draftChange, items []storefront.Item) error {
	line := func() error {
		if c.Line < 0 || c.Line >= len(b.Lines) {
			return badDraft("no such line")
		}
		return nil
	}
	switch c.Op {
	case "choose":
		if err := line(); err != nil {
			return err
		}
		if !read.Choose(&b.Draft, c.Line, c.ItemID, items) {
			return badDraft("that item is not in your catalogue, or not on sale")
		}
		b.unprice(c.Line)
	case "quantity":
		if err := line(); err != nil {
			return err
		}
		if c.Quantity < 1 || c.Quantity > read.MaxQuantity {
			return badDraft(fmt.Sprintf("how many is 1 to %d", read.MaxQuantity))
		}
		l := &b.Lines[c.Line]
		if l.ItemID == "" {
			return badDraft("pick the item first")
		}
		l.Quantity = c.Quantity
		if l.Ask != nil && l.Ask.Kind == read.AskHowMany {
			l.Ask = nil
		}
	case "price":
		if err := line(); err != nil {
			return err
		}
		if c.PriceKobo < 1 || c.PriceKobo > 1_000_000_000 {
			return badDraft("a price is more than ₦0")
		}
		l := &b.Lines[c.Line]
		if l.ItemID == "" {
			return badDraft("pick the item first")
		}
		l.PriceKobo, l.NoPrice = c.PriceKobo, false
		if !b.priced(c.Line) {
			b.Priced = append(b.Priced, c.Line)
		}
	case "remove":
		if err := line(); err != nil {
			return err
		}
		b.Lines = append(b.Lines[:c.Line], b.Lines[c.Line+1:]...)
		var kept []int
		for _, i := range b.Priced {
			switch {
			case i < c.Line:
				kept = append(kept, i)
			case i > c.Line:
				kept = append(kept, i-1)
			}
		}
		b.Priced = kept
	case "add":
		if len(b.Lines) >= read.MaxLines {
			return badDraft(fmt.Sprintf("an order has up to %d lines", read.MaxLines))
		}
		b.Lines = append(b.Lines, read.Line{Quantity: max(c.Quantity, 0)})
		if !read.Choose(&b.Draft, len(b.Lines)-1, c.ItemID, items) {
			b.Lines = b.Lines[:len(b.Lines)-1]
			return badDraft("that item is not in your catalogue, or not on sale")
		}
	case "details":
		if c.Fulfilment != nil {
			switch *c.Fulfilment {
			case "delivery", "pickup", "":
				b.Fulfilment = *c.Fulfilment
			default:
				return badDraft("delivery or pickup")
			}
		}
		for _, f := range []struct {
			to  *string
			in  *string
			max int
		}{{&b.Address, c.Address, 300}, {&b.When, c.When, 120}, {&b.Note, c.Note, 500}} {
			if f.in == nil {
				continue
			}
			v := strings.Join(strings.Fields(*f.in), " ")
			if len([]rune(v)) > f.max {
				return badDraft(fmt.Sprintf("up to %d characters", f.max))
			}
			*f.to = v
		}
		if c.Zone != nil {
			z := strings.TrimSpace(*c.Zone)
			if len([]rune(z)) > storefront.MaxZoneName {
				return badDraft(fmt.Sprintf("an area is up to %d characters", storefront.MaxZoneName))
			}
			b.Zone = z
		}
		if c.DeliveryKobo != nil {
			if *c.DeliveryKobo < 0 || *c.DeliveryKobo > storefront.MaxZoneFeeKobo {
				return badDraft("a delivery fee up to " + storefront.FormatNaira(storefront.MaxZoneFeeKobo))
			}
			b.DeliveryKobo = *c.DeliveryKobo
		}
		if c.Pay != nil {
			switch *c.Pay {
			case storefront.ChatPayTransfer, storefront.ChatPayOnDelivery, storefront.ChatPayLater, "":
				b.Pay = *c.Pay
			default:
				return badDraft("paid by transfer, when it arrives, or later")
			}
		}
	default:
		return badDraft("op is choose, quantity, price, remove, add or details")
	}
	return nil
}

// unprice forgets a price staff set on a line whose item changed.
func (b *draftBody) unprice(line int) {
	var kept []int
	for _, i := range b.Priced {
		if i != line {
			kept = append(kept, i)
		}
	}
	b.Priced = kept
}

// versioned is a request that names the version it was made on.
type versioned struct {
	Version int `json:"version"`
}

// askOrderDraft answers POST /v1/order-drafts/{id}/ask: the draft's open
// questions go to the customer as one message, and their answer is read
// onto it.
func (s *Server) askOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var req versioned
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var d draftRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		d, err = s.draftTx(ctx, tx, p.TenantID, chi.URLParam(r, "id"), false)
		return err
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	switch {
	case d.status != "open" && d.status != "asked":
		s.orderDraftFail(w, r, customerSendErr{http.StatusConflict, "this order is " + d.status})
		return
	case req.Version != d.version:
		s.orderDraftFail(w, r, errDraftChanged)
		return
	case len(d.body.Open()) == 0:
		s.orderDraftFail(w, r, badDraft("the order has no questions to ask"))
		return
	}
	text := s.customerQuestions(s.Orders.CustomerLanguage(ctx, p.TenantID), d.body.Draft)
	if _, _, err := s.sendCustomer(ctx, p.TenantID, d.chat.String(), customerSend{Text: text, ClientID: fmt.Sprintf("ask:%s:%d", d.id, d.version),
		SenderID: p.Actor(), SenderName: s.memberFirstName(ctx, p)}); err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	err = db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, p.TenantID, d.id.String(), true); err != nil {
			return err
		}
		if d.status != "open" && d.status != "asked" {
			return nil
		}
		now := time.Now()
		d.status, d.askedAt = "asked", &now
		return s.saveDraftTx(ctx, tx, &d, p.Actor())
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}

// customerQuestions are a draft's open questions as one message to the
// customer, in the business's language for customers, numbered when there
// are several (their answers are read by those numbers).
func (s *Server) customerQuestions(l lang.Tag, d read.Draft) string {
	t := func(id string, kv ...string) string { return lang.Default().Text(l, id, kv...) }
	open := d.Open()
	var b strings.Builder
	b.WriteString(t("customer.order_ask"))
	for n, i := range open {
		q := d.Lines[i].Ask
		parts := make([]string, len(q.Choices))
		for j, c := range q.Choices {
			parts[j] = c.Name
			if c.PriceKobo != nil {
				parts[j] += " (" + storefront.FormatNaira(*c.PriceKobo) + ")"
			}
		}
		choices := strings.Join(parts, ", ")
		if len(parts) > 1 {
			choices = strings.Join(parts[:len(parts)-1], ", ") + " " + t("customer.order_or") + " " + parts[len(parts)-1]
		}
		about := q.About
		if about == "" {
			about = d.Lines[i].Said
		}
		var line string
		switch q.Kind {
		case read.AskHowMany:
			line = t("customer.order_how_many", "item", about)
		case read.AskSize:
			line = t("customer.order_size", "product", about, "choices", choices)
		default:
			line = t("customer.order_which", "said", capitalFirst(about), "choices", choices)
		}
		b.WriteByte('\n')
		if len(open) > 1 {
			fmt.Fprintf(&b, "%d. ", n+1)
		}
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

func capitalFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// memberFirstName is the person's first name, as their replies show it.
func (s *Server) memberFirstName(ctx context.Context, p *Principal) string {
	if p.UserID == uuid.Nil {
		return ""
	}
	var name string
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name)
	})
	return firstWord(name)
}

// confirmOrderDraft answers POST /v1/order-drafts/{id}/confirm: a person
// confirms the order. It becomes an order of the kind chat (decided), its
// stock goes (at payment when it is paid by transfer), and the customer is
// sent the order, its total and how to pay. A send that fails leaves the
// order confirmed; tell sends it again.
func (s *Server) confirmOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var req versioned
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "orders are not set up on this server")
		return
	}
	items, err := s.catalogueFor(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d draftRow
	var ord storefront.Order
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, p.TenantID, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		if d.status == "confirmed" {
			return nil // confirmed already: told below as before
		}
		if req.Version != d.version {
			return errDraftChanged
		}
		// The catalogue's prices and stock as they are now.
		read.Refresh(&d.body.Draft, items, d.body.priced)
		if why := d.ready(); why != "" {
			return badDraft(why)
		}
		chat, _, err := s.customerChatIn(ctx, tx, p.TenantID, d.chat.String())
		if err != nil {
			return err
		}
		if chat.Phone == "" {
			return customerSendErr{http.StatusConflict, "this customer's details were erased"}
		}
		if chat.Stopped {
			return customerSendErr{http.StatusConflict, "this customer asked not to get messages (they sent STOP); the order cannot be sent to them"}
		}
		opts, err := s.draftOptionsTx(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		pay := d.body.Pay
		switch {
		case pay == storefront.ChatPayTransfer && !opts.PayPage:
			return customerSendErr{http.StatusConflict, "your shop cannot take this payment by transfer now; choose pay when it arrives, or arrange payment yourself"}
		case pay == storefront.ChatPayOnDelivery && d.body.Fulfilment != "delivery":
			pay = ""
		}
		if pay == "" {
			pay = storefront.ChatPayLater
			if opts.PayPage {
				pay = storefront.ChatPayTransfer
			}
		}
		lines := make([]storefront.Line, len(d.body.Lines))
		for i, l := range d.body.Lines {
			lines[i] = storefront.Line{ID: l.ItemID, Name: l.Name, Quantity: int(l.Quantity), PriceKobo: l.PriceKobo}
		}
		name := chat.Name
		if name == chat.Phone {
			name = ""
		}
		// The customer's words (where, when, what else) are theirs: sealed
		// with their details, never in the order's plain columns. The zone
		// is only a delivery area of the business's.
		ord, err = s.Orders.PlaceChatTx(ctx, tx, p.TenantID, storefront.ChatOrder{
			Customer: storefront.Customer{Name: name, Phone: chat.Phone, Address: d.body.Address,
				BestTime: d.body.When, Message: d.body.Note},
			Lines:      lines,
			Fulfilment: d.body.Fulfilment, Zone: zoneOf(d.body.Zone, opts.Zones), DeliveryKobo: d.body.DeliveryKobo,
			Pay: pay, By: p.Actor(),
		})
		if err != nil {
			return err
		}
		d.body.Pay = pay
		d.status = "confirmed"
		d.order, d.reference, d.orderStatus = &ord.ID, ord.Reference, ord.Status
		return s.saveDraftTx(ctx, tx, &d, p.Actor())
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	if d.body.Pay != storefront.ChatPayTransfer {
		// Paid later or at the door: its stock goes now.
		if _, err := s.Orders.LowerStock(context.WithoutCancel(ctx), p.TenantID, d.reference); err != nil {
			s.Logger.Warn("orders: lowering a chat order's stock", "reference", d.reference, "err", err)
		}
	}
	out := map[string]any{"order": d.view(), "told": true}
	if err := s.tellChatOrder(ctx, p, d); err != nil {
		out["told"] = false
		out["told_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// zoneOf is a delivery area of the business's, or "".
func zoneOf(z string, zones []storefront.Zone) string {
	for _, x := range zones {
		if x.Name == z {
			return z
		}
	}
	return ""
}

// tellOrderDraft answers POST /v1/order-drafts/{id}/tell: the confirmed
// order is sent to the customer again (once: the same send is not repeated).
func (s *Server) tellOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var d draftRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		d, err = s.draftTx(ctx, tx, p.TenantID, chi.URLParam(r, "id"), false)
		return err
	})
	if err == nil && d.status != "confirmed" {
		err = customerSendErr{http.StatusConflict, "only a confirmed order is sent"}
	}
	if err == nil {
		err = s.tellChatOrder(ctx, p, d)
	}
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}

// tellChatOrder sends the customer their confirmed order: its lines, the
// delivery fee, the total, and how to pay, in the business's language for
// customers.
func (s *Server) tellChatOrder(ctx context.Context, p *Principal, d draftRow) error {
	l := s.Orders.CustomerLanguage(ctx, p.TenantID)
	t := func(id string, kv ...string) string { return lang.Default().Text(l, id, kv...) }
	var b strings.Builder
	b.WriteString(t("customer.chat_order", "reference", d.reference))
	for _, x := range d.body.Lines {
		fmt.Fprintf(&b, "\n%d × %s  %s", x.Quantity, x.Name, storefront.FormatNaira(x.PriceKobo*x.Quantity))
	}
	if d.body.Fulfilment == "delivery" && d.body.DeliveryKobo > 0 {
		fmt.Fprintf(&b, "\n%s  %s", t("customer.chat_delivery"), storefront.FormatNaira(d.body.DeliveryKobo))
	}
	fmt.Fprintf(&b, "\n%s  %s", t("customer.chat_total"), storefront.FormatNaira(d.body.total()))
	switch d.body.Pay {
	case storefront.ChatPayTransfer:
		var link string
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			ord, err := s.Orders.GetOrder(ctx, tx, p.TenantID, d.reference)
			if err != nil {
				return err
			}
			link, err = s.Orders.ChatStatusLink(ctx, tx, p.TenantID, ord)
			return err
		})
		if err != nil {
			return err
		}
		if link != "" {
			b.WriteString("\n\n" + t("customer.chat_pay_link", "link", link))
		}
	case storefront.ChatPayOnDelivery:
		b.WriteString("\n\n" + t("customer.chat_pay_door"))
	}
	_, _, err := s.sendCustomer(ctx, p.TenantID, d.chat.String(), customerSend{Text: b.String(), ClientID: "order:" + d.reference,
		SenderID: p.Actor(), SenderName: s.memberFirstName(ctx, p)})
	return err
}

// dropOrderDraft answers POST /v1/order-drafts/{id}/drop: staff drop a
// draft (nothing is sent).
func (s *Server) dropOrderDraft(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var req versioned
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var d draftRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, p.TenantID, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		if d.status != "open" && d.status != "asked" {
			return customerSendErr{http.StatusConflict, "this order is " + d.status}
		}
		if req.Version != d.version {
			return errDraftChanged
		}
		d.status = "dropped"
		return s.saveDraftTx(ctx, tx, &d, p.Actor())
	})
	if err != nil {
		s.orderDraftFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d.view())
}

// orderDraftAlerts are the orders read from customers' messages that wait
// for staff (Needs you): the newest first, at most perKind; the card opens
// the customer's thread (ID). Asked ones wait for the customer, not here.
func (s *Server) orderDraftAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermOrders) {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		on, err := areas.Load(ctx, tx, p.TenantID)
		if err != nil || !on.On(areas.Orders) {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+draftCols+draftFrom+` WHERE d.tenant_id = $1 AND d.status = 'open' ORDER BY d.updated_at DESC LIMIT $2`, p.TenantID, perKind)
		if err != nil {
			return err
		}
		list, err := s.scanDrafts(ctx, tx, p.TenantID, rows)
		if err != nil {
			return err
		}
		for _, d := range list {
			chat, _, err := s.customerChatIn(ctx, tx, p.TenantID, d.chat.String())
			if err != nil {
				return err
			}
			if len(d.body.Lines) == 0 {
				continue
			}
			v := d.view()
			title := "Order from " + chat.Name
			if chat.Name == chat.Phone {
				title = "Order from a customer"
			}
			var parts []string
			n := len(d.body.Lines)
			parts = append(parts, fmt.Sprintf("%d item%s", n, map[bool]string{true: "", false: "s"}[n == 1]))
			if v.TotalKobo > 0 {
				parts = append(parts, storefront.FormatNaira(v.TotalKobo))
			}
			switch {
			case v.Questions == 1:
				parts = append(parts, "1 question")
			case v.Questions > 1:
				parts = append(parts, fmt.Sprintf("%d questions", v.Questions))
			case v.Ready:
				parts = append(parts, "ready to confirm")
			}
			total := v.TotalKobo
			out = append(out, chatAlert{Key: "order_draft:" + d.id.String(), Kind: "order_draft", At: d.updatedAt, ID: d.chat.String(), Reference: d.id.String(),
				Title: title, Line: strings.Join(parts, " · "), AmountKobo: &total})
		}
		// Confirmed orders that lapsed unpaid in the last two days: staff
		// may ask again, or let it go.
		lapsed, err := tx.Query(ctx, `SELECT d.conversation_id, d.reference, o.total_kobo, o.updated_at FROM order_drafts d JOIN storefront_orders o ON o.id = d.order_id
			WHERE d.tenant_id = $1 AND d.status = 'confirmed' AND o.status = 'expired' AND o.updated_at > now() - interval '2 days'
			ORDER BY o.updated_at DESC LIMIT $2`, p.TenantID, perKind)
		if err != nil {
			return err
		}
		type gone struct {
			chat  uuid.UUID
			ref   string
			total int64
			at    time.Time
		}
		list2, err := pgx.CollectRows(lapsed, func(r pgx.CollectableRow) (gone, error) {
			var g gone
			return g, r.Scan(&g.chat, &g.ref, &g.total, &g.at)
		})
		if err != nil {
			return err
		}
		for _, g := range list2 {
			total := g.total
			out = append(out, chatAlert{Key: "order_lapsed:" + g.ref, Kind: "order_draft", At: g.at, ID: g.chat.String(), Reference: g.ref,
				Title: "Order " + g.ref + " was not paid in time", Line: "It is cancelled, and the customer was told", AmountKobo: &total})
		}
		return nil
	})
	return out, err
}

// --- the customer told how their order is going ---

// ChatNotify is storefront's ChatNotify: a message about an order of the
// kind chat (paid, on its way, arrived, delivered), sent in the customer's
// thread with the business. The same words about one order go once.
func (s *Server) ChatNotify(ctx context.Context, tenant uuid.UUID, ord storefront.Order, text string) error {
	var thread uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM customer_conversations WHERE tenant_id = $1 AND subject_id = $2 ORDER BY last_message_at DESC LIMIT 1`,
			tenant, ord.CustomerID()).Scan(&thread)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("orders: the customer has no conversation with the business")
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(text))
	_, _, err = s.sendCustomer(ctx, tenant, thread.String(), customerSend{Text: text,
		ClientID: "notify:" + ord.Reference + ":" + hex.EncodeToString(sum[:6]), SenderID: "system:orders"})
	return err
}
