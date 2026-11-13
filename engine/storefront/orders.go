package storefront

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/sender"
)

// PaymentTTL is how long an order paid at once waits for its payment
// before it expires (storefronts 6.4).
const PaymentTTL = 30 * time.Minute

// PaymentSignal is the signal a run waiting for an order's payment gets,
// with the order's reference as the correlation key.
const PaymentSignal = "storefront.payment"

// DeliveredSignal is the signal an order's run hears when its rider
// delivered it (dispatch D3), correlated by the reference.
const DeliveredSignal = "storefront.delivered"

var (
	// ErrNoOrder is a reference the caller's tenant has no order for.
	ErrNoOrder = errors.New("storefront: no such order")
	// ErrDecision refuses a decision the order's status does not allow.
	ErrDecision = errors.New("storefront: not allowed in the order's status")
)

// Payments makes and checks payment pages with the tenant's payment
// provider (Paystack first).
type Payments interface {
	Create(ctx context.Context, tenant uuid.UUID, connection string, r PaymentRequest) (url, providerRef string, err error)
	// Verify reports "paid", "pending" or "failed", and what was paid.
	Verify(ctx context.Context, tenant uuid.UUID, connection, providerRef string) (status string, paidKobo int64, err error)
	// Refund returns a payment in full; a second refund of the same
	// payment reports the first.
	Refund(ctx context.Context, tenant uuid.UUID, connection, providerRef, note string) (status string, err error)
}

// PaymentRequest is one checkout page.
type PaymentRequest struct {
	Reference   string // the provider's reference: the order's key
	AmountKobo  int64
	Email       string
	CallbackURL string
	Metadata    map[string]any
}

// Orders runs the storefront's orders: from the inbox to a run, payments,
// and the actions workflows take on them.
type Orders struct {
	Pool     *pgxpool.Pool
	Vault    *secrets.Vault
	InboxKey []byte
	Payments Payments
	// Stock lowers the catalogue's stock on Accept (nil: not tracked).
	Stock Stock
	// Moves records the lowering as stock movements and sends them
	// (engine/stock, docs/spec/stock.md §4); nil lowers through Stock
	// alone.
	Moves    Mover
	Messages *sender.Queue
	// Start starts a run (runtime.Store.Start); Signal delivers a signal
	// (runtime.Store.DeliverSignalOnce).
	Start  func(ctx context.Context, req runtime.StartRequest) (runtime.Started, error)
	Signal func(ctx context.Context, tenant uuid.UUID, env, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error)
	// Live reads catalogue and service rows from the tenant's table, for
	// the price check before a run (nil: prices are not checked).
	Live Live
	// Cancel cancels a run (runtime.Store.CancelRun), when a booking is
	// moved or cancelled.
	Cancel func(ctx context.Context, ref runtime.RunRef, by string, reverse bool) error
	// MoneyLog, when set, records a payment the payment check finds, as
	// the provider's event would, and the order's payment counts that
	// entry (matched.go): one entry and one match whichever is heard first.
	MoneyLog *moneylog.Log
	// IssueTransfer asks the business's rail for a one-time account number
	// for an order (N3; invoicing's IssueForOrder): nil offers none.
	IssueTransfer IssueTransfer
	// TradeNotify tells a linked business, the customer of an order of
	// the kind trade, a message meant for its customer (business links
	// §7): in its own Taskiem, not by SMS.
	TradeNotify func(ctx context.Context, tenant uuid.UUID, ord Order, text string) error
	// ChatNotify tells the customer of an order of the kind chat a message
	// meant for them, in their WhatsApp thread with the business (chat-
	// five-more-flows.md, phase 2), not by SMS; "{link}" is already the
	// order's status link.
	ChatNotify func(ctx context.Context, tenant uuid.UUID, ord Order, text string) error
	// AfterPaid hears of an order a payment moved to paid, after the
	// payment is counted (dispatch tells the rider at the door).
	AfterPaid func(ctx context.Context, tenant uuid.UUID, ord Order) error
	// PreviewDomain is where storefronts are served (<slug>.<domain>).
	PreviewDomain string
	Logger        *slog.Logger
	Now           func() time.Time
}

func (o *Orders) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Orders) log() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

// Order is an order as the owner sees it, the customer's details opened.
type Order struct {
	ID         uuid.UUID `json:"id"`
	Reference  string    `json:"reference"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	Customer   Customer  `json:"customer"`
	Basket     []Line    `json:"basket"`
	TotalKobo  int64     `json:"total_kobo"`
	Fulfilment string    `json:"fulfilment,omitempty"`
	// Zone is the delivery area the customer chose and DeliveryKobo its
	// fee, part of TotalKobo (dispatch D1).
	Zone         string     `json:"zone,omitempty"`
	DeliveryKobo int64      `json:"delivery_kobo,omitempty"`
	Payment      string     `json:"payment"`
	PaymentURL   string     `json:"payment_url,omitempty"`
	PaidAt       *time.Time `json:"paid_at"`
	// PaidBy is how it was paid: card (its Paystack checkout page),
	// transfer (into the account number given for it, N3), other (money
	// matched to it, or a manual match), or "" while unpaid. Only a card payment is refunded
	// through Paystack; the others by hand.
	PaidBy    string     `json:"paid_by,omitempty"`
	RunID     *uuid.UUID `json:"run_id"`
	Note      string     `json:"note,omitempty"`
	DecidedBy string     `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	// A booking's service and time; Version counts its moves.
	ServiceID   string     `json:"service_id,omitempty"`
	ServiceName string     `json:"service_name,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	Version     int        `json:"version,omitempty"`
	// DepositKobo is what is paid online when less than the total.
	DepositKobo *int64 `json:"deposit_kobo,omitempty"`
	// PriceChange is the basket at the table's prices, while the customer
	// is asked to confirm it (status price_changed).
	PriceChange *PriceChange `json:"price_change,omitempty"`
	// FrontID is the front the submission came from (fronts F3).
	FrontID *uuid.UUID `json:"front_id,omitempty"`
	// Part payments (fronts F5, §5.4): ExtraKobo is added to what it costs
	// (a late fee), PaidKobo is what was paid towards it, and PayToKobo,
	// while a later step (a balance, an instalment) is open, what PaidKobo
	// is to reach; PaySeq numbers the steps.
	ExtraKobo   int64  `json:"extra_kobo,omitempty"`
	PaidKobo    int64  `json:"paid_kobo"`
	PayToKobo   *int64 `json:"pay_to_kobo,omitempty"`
	PayStepKobo *int64 `json:"pay_step_kobo,omitempty"`
	PaySeq      int    `json:"pay_seq,omitempty"`
	// ReturnedAt is when a hire's items came back (fronts F5).
	ReturnedAt *time.Time `json:"returned_at,omitempty"`
	// Instalments is how many parts a class's fee is paid in, and
	// Waitlist an order on a full class's waiting list, holding no place
	// until the owner offers one (fronts F6).
	Instalments int  `json:"instalments,omitempty"`
	Waitlist    bool `json:"waitlist,omitempty"`
	subject     string
	storefront  uuid.UUID
	action      uuid.UUID
	paymentRef  string
	customerEnv map[string]any
}

// CustomerID is the order's customer as matching knows customers
// (invoicing.Invoice.CustomerID): the pseudonym of their phone, the same
// an invoice to that phone without a key carries, so a payer remembered
// for an order's customer is remembered for their invoices too. "" when
// the order has none.
func (x Order) CustomerID() string { return x.subject }

const orderCols = `id, reference, kind, status, customer, subject_id, basket, total_kobo, COALESCE(fulfilment, ''), payment, COALESCE(payment_url, ''),
	COALESCE(payment_ref, ''), paid_at, run_id, COALESCE(note, ''), COALESCE(decided_by, ''), decided_at, expires_at, created_at,
	COALESCE(storefront_id, '00000000-0000-0000-0000-000000000000'::uuid),
	COALESCE(service_id, ''), COALESCE(service_name, ''), starts_at, ends_at, version, deposit_kobo, price_change,
	COALESCE(zone, ''), COALESCE(delivery_kobo, 0), COALESCE(action_id, '00000000-0000-0000-0000-000000000000'::uuid), front_id, extra_kobo, paid_kobo, pay_to_kobo, pay_step_kobo, pay_seq, returned_at, instalments, waitlist,
	CASE WHEN paid_at IS NULL THEN ''
	  WHEN EXISTS (SELECT 1 FROM customer_invoice_accounts x WHERE x.order_id = storefront_orders.id AND x.status = 'paid') THEN 'transfer'
	  WHEN EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.order_id = storefront_orders.id AND p.undoes IS NULL
	    AND p.by NOT LIKE 'system:paystack/%' AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)) THEN 'other'
	  WHEN payment_ref IS NOT NULL THEN 'card'
	  ELSE 'other' END`

func scanOrder(r pgx.Row) (Order, error) {
	var x Order
	var basket, change []byte
	err := r.Scan(&x.ID, &x.Reference, &x.Kind, &x.Status, &x.customerEnv, &x.subject, &basket, &x.TotalKobo, &x.Fulfilment, &x.Payment, &x.PaymentURL,
		&x.paymentRef, &x.PaidAt, &x.RunID, &x.Note, &x.DecidedBy, &x.DecidedAt, &x.ExpiresAt, &x.CreatedAt, &x.storefront,
		&x.ServiceID, &x.ServiceName, &x.StartsAt, &x.EndsAt, &x.Version, &x.DepositKobo, &change, &x.Zone, &x.DeliveryKobo, &x.action, &x.FrontID,
		&x.ExtraKobo, &x.PaidKobo, &x.PayToKobo, &x.PayStepKobo, &x.PaySeq, &x.ReturnedAt, &x.Instalments, &x.Waitlist, &x.PaidBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNoOrder
	}
	if err != nil {
		return Order{}, err
	}
	_ = json.Unmarshal(basket, &x.Basket)
	if change != nil {
		x.PriceChange = &PriceChange{}
		_ = json.Unmarshal(change, x.PriceChange)
	}
	if x.Basket == nil {
		x.Basket = []Line{}
	}
	return x, nil
}

// open fills the order's customer from its sealed envelope; an erased
// customer reads as empty.
func (o *Orders) open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x *Order) error {
	v, err := o.Vault.OpenTx(ctx, tx, tenant, x.customerEnv)
	if errors.Is(err, pii.ErrErased) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	return json.Unmarshal(raw, &x.Customer)
}

// GetOrder reads one order by reference, the customer opened.
func (o *Orders) GetOrder(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string) (Order, error) {
	x, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, tenant, reference))
	if err != nil {
		return x, err
	}
	return x, o.open(ctx, tx, tenant, &x)
}

// ListOrders reads the newest orders, with the customer opened. Status
// "open" is every order still needing the owner; kind narrows to one form.
func (o *Orders) ListOrders(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, status, kind string, limit int) ([]Order, error) {
	return o.ListOrdersOn(ctx, tx, tenant, nil, status, kind, limit)
}

// ListOrdersOn is ListOrders for one front's submissions (fronts F3; nil
// for every front's).
func (o *Orders) ListOrdersOn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, front *uuid.UUID, status, kind string, limit int) ([]Order, error) {
	q := `SELECT ` + orderCols + ` FROM storefront_orders WHERE tenant_id = $1`
	args := []any{tenant}
	if front != nil {
		args = append(args, *front)
		q += fmt.Sprintf(` AND front_id = $%d`, len(args))
	}
	if kind != "" {
		args = append(args, kind)
		q += fmt.Sprintf(` AND kind = $%d`, len(args))
	}
	switch status {
	case "":
	case "open":
		q += ` AND status IN ('received', 'awaiting_payment', 'paid', 'accepted', 'refund_due', 'price_changed', 'dispatched', 'on_the_way', 'arrived', 'proposed', 'changed')`
	default:
		args = append(args, status)
		q += fmt.Sprintf(` AND status = $%d`, len(args))
	}
	rows, err := tx.Query(ctx, q+fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, max(1, min(limit, 200))), args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := o.open(ctx, tx, tenant, &list[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// --- the inbox ---

// actionOf is the SQL condition on storefront_actions a for the action
// id (a nil id: the built-in action of the kind).
func actionOf(id, kind string) string {
	return `(a.id = ` + id + `::uuid OR (` + id + `::uuid IS NULL AND a.kind = ` + kind + ` AND a.builtin))`
}

type inboxRow struct {
	id, storefront, tenant uuid.UUID
	kind, reference        string
	statusHash, payload    []byte
	// action is the action submitted to (nil only for a row an older
	// public host wrote for a kind with no built-in action).
	action *uuid.UUID
}

// Drain takes waiting submissions from the inbox and processes each. A
// submission is removed from the inbox only when processed: one that
// fails is retried after its claim lapses.
func (o *Orders) Drain(ctx context.Context) error {
	rows, err := o.Pool.Query(ctx, `SELECT id, storefront_id, tenant_id, kind, reference, status_hash, payload, action_id FROM taskiem_storefront_inbox_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inboxRow, error) {
		var x inboxRow
		return x, r.Scan(&x.id, &x.storefront, &x.tenant, &x.kind, &x.reference, &x.statusHash, &x.payload, &x.action)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := o.process(ctx, x); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", x.reference, err))
			continue
		}
		if _, err := o.Pool.Exec(ctx, `SELECT taskiem_storefront_inbox_done($1)`, x.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// process records a submission as an order, makes its payment page when it
// is paid at once, and starts its run when it may start. Each step is
// skipped when already done, so a retry carries on where it stopped. A new
// order or booking is first priced from the table (prices.go).
func (o *Orders) process(ctx context.Context, x inboxRow) error {
	sub, err := OpenSubmission(o.InboxKey, x.storefront, x.reference, x.payload)
	if err != nil {
		o.log().Error("storefront: an inbox payload that does not open; dropped", "reference", x.reference, "err", err)
		return nil
	}
	var ord Order
	var slug string
	var opts ActionOptions
	exists := false
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT COALESCE(s.published_slug, s.slug), a.options FROM storefronts s
			JOIN storefront_actions a ON a.storefront_id = s.id AND `+actionOf("$2", "$3")+` WHERE s.id = $1`, x.storefront, x.action, x.kind).Scan(&slug, &raw); err != nil {
			return err
		}
		_ = json.Unmarshal(raw, &opts)
		existing, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, x.tenant, x.reference))
		if err == nil {
			ord, exists = existing, true
			return nil
		}
		if errors.Is(err, ErrNoOrder) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if !exists {
		pc, changed, note, err := o.checkPrices(ctx, x, sub, opts)
		if err != nil {
			return err // the table did not answer: tried again shortly
		}
		err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
			var err error
			ord, err = o.record(ctx, tx, x, sub, pc, changed, note)
			return err
		})
		if err != nil {
			return err
		}
	}
	return o.carryOn(ctx, x.tenant, ord, slug, opts.Connection, sub.Customer, sub.StatusKey)
}

// carryOn takes a recorded order the next step: tells the customer when it
// waits for them or cannot go ahead, makes the payment page when it is
// paid at once, or starts the run.
func (o *Orders) carryOn(ctx context.Context, tenant uuid.UUID, ord Order, slug, connection string, c Customer, statusKey string) error {
	switch {
	case ord.Status == "price_changed":
		return o.tell(ctx, tenant, ord.Reference, priceChangedText(ord), "price:"+ord.Reference)
	case ord.Status == "cancelled" || ord.Status == "expired":
		if ord.Note == "" {
			return nil
		}
		return o.tell(ctx, tenant, ord.Reference, "Sorry, "+ord.Reference+" cannot go ahead: "+ord.Note+".", "gone:"+ord.Reference)
	case ord.Status == "awaiting_payment" && ord.PaymentURL == "":
		return o.createPayment(ctx, tenant, ord, c, statusKey, slug, connection) // the run starts once it is paid (CheckPayments)
	case ord.Status == "received" && ord.RunID == nil:
		return o.startRun(ctx, tenant, ord.Reference)
	}
	return nil
}

// checkPrices prices a submission from the table. It returns an error to
// be tried again while the table does not answer, for up to
// PriceCheckGrace; after that it goes ahead with a note.
func (o *Orders) checkPrices(ctx context.Context, x inboxRow, sub Submission, opts ActionOptions) (PriceChange, bool, string, error) {
	if o.Live == nil || (sub.Kind != "order" && sub.Kind != "booking") {
		return PriceChange{}, false, "", nil
	}
	var ids []string
	if sub.Booking != nil {
		ids = []string{sub.Booking.ServiceID}
	}
	for _, l := range sub.Basket {
		ids = append(ids, l.ID)
	}
	from := Source{Services: sub.Booking != nil}
	if sub.Booking == nil {
		// An order on a front other than the home page draws on its
		// front's list (fronts F3).
		var raw []byte
		err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT b.config FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id AND f.slug <> ''
				JOIN storefront_blocks b ON b.front_id = f.id AND b.kind = 'list' WHERE a.id = $1 ORDER BY b.position LIMIT 1`, x.action).Scan(&raw)
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return PriceChange{}, false, "", err
		}
		var lc ListConfig
		if raw != nil && json.Unmarshal(raw, &lc) == nil && lc.Table != nil {
			from.Table, from.Columns = lc.Table, lc.Columns
		}
	}
	live, err := o.Live.Items(ctx, x.tenant, from, ids)
	if err != nil {
		if o.now().Sub(sub.Submitted) < PriceCheckGrace {
			return PriceChange{}, false, "", fmt.Errorf("price check: %w", err)
		}
		o.log().Warn("storefront: prices not checked; the table did not answer", "reference", x.reference, "err", err)
		return PriceChange{}, false, "Prices were not checked: the table could not be read", nil
	}
	pc, changed := reprice(sub, live, opts)
	return pc, changed, "", nil
}

func priceChangedText(ord Order) string {
	pc := ord.PriceChange
	if pc == nil {
		return ""
	}
	t := "Prices have changed since you sent " + ord.Reference + "."
	if len(pc.Gone) > 0 {
		t += " No longer available: " + strings.Join(pc.Gone, ", ") + "."
	}
	t += " The total is now " + FormatNaira(pc.TotalKobo) + ". Confirm or cancel within a day: {link}"
	return t
}

// record seals the customer and writes the order, at the table's prices
// when they changed. A booking keeps the slot it held.
func (o *Orders) record(ctx context.Context, tx pgx.Tx, x inboxRow, sub Submission, pc PriceChange, changed bool, note string) (Order, error) {
	envPhone, err := o.Vault.SealTx(ctx, tx, x.tenant, "phone", sub.Customer.Phone)
	if err != nil {
		return Order{}, err
	}
	subject, _ := envPhone["subject"].(string)
	customer, err := o.Vault.SealUnderTx(ctx, tx, x.tenant, subject, "customer", sub.Customer)
	if err != nil {
		return Order{}, err
	}
	var key map[string]any
	if sub.StatusKey != "" {
		if key, err = o.Vault.SealUnderTx(ctx, tx, x.tenant, subject, "status_key", sub.StatusKey); err != nil {
			return Order{}, err
		}
	}
	status, payment := "received", sub.Payment
	var expires *time.Time
	payable := sub.TotalKobo
	if sub.DepositKobo > 0 {
		payable = sub.DepositKobo
	}
	if !takesPayment(sub.Kind) || payable < 100 || sub.Waitlist {
		payment = "none"
	}
	if payment == "on_delivery" && (sub.Kind != "order" || sub.Fulfilment != "delivery") {
		payment = "after_accept"
	}
	if payment == "at_order" {
		status = "awaiting_payment"
		e := o.now().Add(PaymentTTL)
		expires = &e
	}
	var change []byte
	switch {
	case changed && (len(pc.Gone) > 0 && (sub.Booking != nil || sub.Hire != nil || len(pc.Basket) == 0)):
		status, expires, note = "cancelled", nil, "no longer available: "+strings.Join(pc.Gone, ", ")
	case changed:
		status = "price_changed"
		e := o.now().Add(24 * time.Hour)
		expires = &e
		change, _ = json.Marshal(pc)
	}
	basket, _ := json.Marshal(sub.Basket)
	var fulfilment *string
	if sub.Fulfilment != "" {
		fulfilment = &sub.Fulfilment
	}
	var deposit *int64
	if sub.DepositKobo > 0 && sub.DepositKobo < sub.TotalKobo {
		deposit = &sub.DepositKobo
	}
	b := sub.Booking
	if sub.Kind == "booking" && b == nil {
		return Order{}, fmt.Errorf("%w: a booking without its time", ErrInvalid)
	}
	var serviceID, serviceName *string
	var starts, ends *time.Time
	if b != nil {
		serviceID, serviceName, starts, ends = &b.ServiceID, &b.ServiceName, &b.Start, &b.End
	}
	// A hire's item and dates in the same columns: from its first day to
	// the end of its last, the day it is due back (fronts F5).
	h := sub.Hire
	if (sub.Kind == "hire" || sub.Kind == "space" || sub.Kind == "tickets" || sub.Kind == "class") && h == nil {
		return Order{}, fmt.Errorf("%w: a hire without its dates", ErrInvalid)
	}
	if h != nil {
		serviceID, serviceName = &h.ItemID, &h.Item
		// A ticket or a place has its event's or class's start, and no end.
		if !h.From.IsZero() {
			starts = &h.From
		}
		if !h.Until.IsZero() {
			ends = &h.Until
		}
	}
	var noteArg *string
	if note != "" {
		noteArg = &note
	}
	var zone *string
	var delivery *int64
	if sub.Zone != "" && sub.Fulfilment == "delivery" {
		zone = &sub.Zone
		if sub.DeliveryKobo > 0 {
			delivery = &sub.DeliveryKobo
		}
	}
	// The answers to the action's own questions, sealed under the
	// customer as their details are (fronts §5.5).
	var answers map[string]any
	var form []byte
	if err := tx.QueryRow(ctx, `SELECT a.form FROM storefront_actions a WHERE a.storefront_id = $1 AND `+actionOf("$2", "$3"), x.storefront, x.action, x.kind).
		Scan(&form); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, err
	}
	if kept := keepAsked(sub.Answers, EffectiveForm(form, x.kind)); kept != nil {
		if answers, err = o.Vault.SealUnderTx(ctx, tx, x.tenant, subject, "answers", kept); err != nil {
			return Order{}, err
		}
	}
	id := uuid.Must(uuid.NewV7())
	// The submission names its action and front (fronts §5.5).
	if tag, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket,
		total_kobo, fulfilment, payment, expires_at, status_key, deposit_kobo, service_id, service_name, starts_at, ends_at, price_change, note, zone, delivery_kobo,
		action_id, front_id, answers, instalments, waitlist)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, a.id, a.front_id, $26, $27, $28
		  FROM storefront_actions a WHERE a.storefront_id = $3 AND `+actionOf("$25", "$4"),
		id, x.tenant, x.storefront, x.kind, x.reference, x.statusHash, status, customer, subject, basket, sub.TotalKobo, fulfilment, payment, expires,
		key, deposit, serviceID, serviceName, starts, ends, change, noteArg, zone, delivery, x.action, answers, max(1, min(MaxInstalments, sub.Instalments)), sub.Waitlist); err != nil {
		return Order{}, err
	} else if tag.RowsAffected() == 0 {
		return Order{}, fmt.Errorf("storefront: no %s action for this storefront", x.kind)
	}
	if b != nil {
		kept := true
		if status == "cancelled" {
			_, err = tx.Exec(ctx, `DELETE FROM storefront_claims WHERE reference = $1`, x.reference)
		} else {
			kept, err = keepHold(ctx, tx, x.tenant, x.storefront, x.reference, *b, expires)
		}
		if err != nil {
			return Order{}, err
		}
		if !kept {
			status, note = "expired", "that time was taken before the booking was recorded; please book another"
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2, note = $3, expires_at = NULL WHERE id = $1`, id, status, note); err != nil {
				return Order{}, err
			}
		}
	}
	if h != nil && !sub.Waitlist {
		// The claim taken with the submission is kept until the order's
		// payment is due, or for as long as the hire lives; one that lapsed
		// before the hire was recorded is not taken again.
		var tag pgconn.CommandTag
		if status == "cancelled" {
			_, err = tx.Exec(ctx, `DELETE FROM storefront_claims WHERE reference = $1`, x.reference)
		} else {
			tag, err = tx.Exec(ctx, `UPDATE storefront_claims SET expires_at = $2 WHERE reference = $1 AND (expires_at IS NULL OR expires_at > now())`, x.reference, expires)
		}
		if err != nil {
			return Order{}, err
		}
		if status != "cancelled" && tag.RowsAffected() == 0 {
			status, note = "expired", "those dates were taken before the hire was recorded; please choose others"
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2, note = $3, expires_at = NULL WHERE id = $1`, id, status, note); err != nil {
				return Order{}, err
			}
		}
	}
	if err := audit(ctx, tx, x.tenant, "storefront.order_received", x.reference, map[string]any{"kind": x.kind, "total_kobo": sub.TotalKobo, "payment": payment,
		"channel": sub.Channel, "status": status}); err != nil {
		return Order{}, err
	}
	return scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE id = $1`, id))
}

// keepHold keeps a booking's slot until expires (nil: for as long as the
// booking lives). A hold that lapsed before the booking was recorded is
// taken again if the slot is still free; kept is false when it is not.
func keepHold(ctx context.Context, tx pgx.Tx, tenant, storefront uuid.UUID, reference string, b Booked, expires *time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE storefront_claims SET expires_at = $2 WHERE reference = $1 AND (expires_at IS NULL OR expires_at > now())`, reference, expires)
	if err != nil || tag.RowsAffected() == 1 {
		return err == nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT taskiem_storefront_lock($1)`, storefront); err != nil {
		return false, err
	}
	var free bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_storefront_slot_free($1, $2, $3, $4, $5, $6)`, storefront, reference, b.ServiceID, b.Start, b.Until, b.Capacity).Scan(&free); err != nil || !free {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO storefront_claims (reference, storefront_id, tenant_id, item_id, starts_at, until_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (reference) DO UPDATE SET starts_at = EXCLUDED.starts_at, until_at = EXCLUDED.until_at, expires_at = EXCLUDED.expires_at`,
		reference, storefront, tenant, b.ServiceID, b.Start, b.Until, expires)
	return err == nil, err
}

// paymentKey is an order's reference at the payment provider: fixed for
// the order, so making its page twice cannot make two payments.
func paymentKey(id uuid.UUID) string { return "tsk_sf_" + hex.EncodeToString(id[:]) }

// statusURL is the customer's status page for an order.
func (o *Orders) statusURL(slug, reference, key string) string {
	u := "https://" + slug + "." + o.PreviewDomain + "/o/" + reference
	if key != "" {
		u += "?k=" + key
	}
	return u
}

// payerEmail is the customer's email, or a placeholder at the platform's
// domain: Paystack requires one.
func (o *Orders) payerEmail(c Customer, reference string) string {
	if c.Email != "" {
		return c.Email
	}
	return "orders+" + strings.ToLower(strings.ReplaceAll(reference, "-", "")) + "@" + o.PreviewDomain
}

func (o *Orders) createPayment(ctx context.Context, tenant uuid.UUID, ord Order, c Customer, statusKey, slug, connection string) error {
	name, card := paystackConnection(connection)
	if !card {
		// Another rail's form: the customer pays by bank transfer from the
		// status page (N3), and no card page is made.
		return nil
	}
	if o.Payments == nil {
		return errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, name, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.Payable(),
		Email: o.payerEmail(c, ord.Reference), CallbackURL: o.statusURL(slug, ord.Reference, statusKey),
		Metadata: map[string]any{"storefront_reference": ord.Reference}})
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET payment_url = $3, payment_ref = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, url, ref)
		return err
	})
}

// RefundKobo is what a refund gives back: what was paid.
func (x Order) RefundKobo() int64 {
	if x.PaidKobo > 0 {
		return x.PaidKobo
	}
	return x.Payable()
}

// Payable is what is paid online: the deposit, or the total.
func (x Order) Payable() int64 {
	if x.DepositKobo != nil {
		return *x.DepositKobo
	}
	return x.TotalKobo
}

// StartTrade starts the order workflow's run for a linked business's
// order (kind trade, business links §7), as a submission's starts: the
// order names the business's built-in order action, so its workflow.
func (o *Orders) StartTrade(ctx context.Context, tenant uuid.UUID, reference string) error {
	return o.startRun(ctx, tenant, reference)
}

// DueKobo is what the order costs: its total and anything added since (a
// late fee).
func (x Order) DueKobo() int64 { return x.TotalKobo + x.ExtraKobo }

// BalanceKobo is what is left to pay on it.
func (x Order) BalanceKobo() int64 { return max(x.DueKobo()-x.PaidKobo, 0) }

// AskedKobo is what the order asks for now: the rest of an open later
// step, or, while it waits for its first payment, the rest of its deposit
// or total; 0 when it asks for nothing.
func (x Order) AskedKobo() int64 {
	switch {
	case x.PayToKobo != nil:
		return max(*x.PayToKobo-x.PaidKobo, 0)
	case x.PaidAt == nil:
		return max(x.Payable()-x.PaidKobo, 0)
	}
	return 0
}

// PaymentState is unpaid, part_paid or paid, from what was paid against
// what it costs.
func (x Order) PaymentState() string {
	switch {
	case x.PaidKobo <= 0:
		return "unpaid"
	case x.PaidKobo < x.DueKobo():
		return "part_paid"
	}
	return "paid"
}

// startRun starts the run of the workflow deployed for the order's form,
// in prod. An order with no such workflow stays received, with a note.
func (o *Orders) startRun(ctx context.Context, tenant uuid.UUID, reference string) error {
	var ord Order
	var wf uuid.UUID
	var version int
	var slug, name string
	var found, autoConfirm bool
	var rec submissionRecord
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		// A linked business's order (kind trade, business links §7) names
		// the built-in order action of the business's storefront, and so
		// has a record like any order's; a business without a storefront
		// takes such an order too, under its own name, with no action and
		// no record (and with no status link in a run's trigger.body).
		if ord.action != uuid.Nil {
			if rec, err = o.recordOf(ctx, tx, tenant, ord); err != nil {
				return err
			}
		}
		if ord.storefront == uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
				return err
			}
		} else {
			var draft []byte
			if err := tx.QueryRow(ctx, `SELECT COALESCE(published_slug, slug), draft FROM storefronts WHERE id = $1`, ord.storefront).Scan(&slug, &draft); err != nil {
				return err
			}
			var c Card
			_ = json.Unmarshal(draft, &c)
			name = c.Name
		}
		if ord.Kind == "booking" {
			sf, err := Get(ctx, tx, tenant)
			if err != nil {
				return err
			}
			autoConfirm = sf.Availability.AutoConfirm
		}
		// The workflow deployed on the order's action; a trade order with
		// no action (a business without a storefront, or whose storefront
		// has no built-in order action yet) starts the one deployed on the
		// built-in order kind by name, as a trigger deployed before its
		// action exists is kept (fronts F1c).
		if ord.action != uuid.Nil {
			err = tx.QueryRow(ctx, `SELECT workflow_id, version FROM triggers WHERE type = 'storefront' AND storefront_action_id = $1 AND environment = 'prod'`, ord.action).Scan(&wf, &version)
		} else {
			err = tx.QueryRow(ctx, `SELECT workflow_id, version FROM triggers WHERE type = 'storefront' AND storefront_action = 'order' AND environment = 'prod'`).Scan(&wf, &version)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `UPDATE storefront_orders SET note = 'No workflow is deployed for this form: set it up under Storefront.', updated_at = now()
				WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID)
			return err
		}
		found = err == nil
		return err
	})
	if err != nil || !found {
		return err
	}
	paymentStatus := "none"
	switch {
	case ord.PaidAt != nil:
		paymentStatus = "paid"
	case ord.Payment == "after_accept":
		paymentStatus = "due_after_accept"
	case ord.Payment == "on_delivery":
		paymentStatus = "on_delivery"
	}
	body := map[string]any{"reference": ord.Reference, "kind": ord.Kind, "customer": ord.Customer, "basket": ord.Basket, "total_kobo": ord.TotalKobo,
		"total": FormatNaira(ord.TotalKobo), "summary": Summary(ord.Basket), "fulfilment": ord.Fulfilment, "payment": map[string]any{"mode": ord.Payment, "status": paymentStatus}}
	if ord.Zone != "" {
		// The delivery area and its fee (dispatch D1), in the summary too.
		body["zone"], body["delivery_kobo"], body["delivery_fee"] = ord.Zone, ord.DeliveryKobo, FormatNaira(ord.DeliveryKobo)
		body["summary"] = Summary(ord.Basket) + ", delivery to " + ord.Zone + " " + FormatNaira(ord.DeliveryKobo)
	}
	if k, v := reservedTime(ord, rec.instalmentDays); k != "" {
		body[k] = v
		body["summary"] = Summary(ord.Basket)
		if w, ok := v["when"].(string); ok {
			body["summary"] = ord.ServiceName + ", " + w
			if q, ok := v["qty"].(int); ok && q > 1 {
				body["summary"] = fmt.Sprintf("%d × %s, %s", q, ord.ServiceName, w)
			}
		}
	}
	dedup := ord.ID.String()
	if ord.Kind == "booking" {
		body["booking"] = o.bookingBody(ord, autoConfirm)
		body["summary"] = ord.ServiceName + ", " + When(*ord.StartsAt)
		// A move starts a new run; one already decided skips the decision.
		body["event"] = "new"
		if ord.Version > 0 {
			dedup += fmt.Sprintf(":%d", ord.Version)
			if ord.DecidedAt != nil {
				body["event"] = "rescheduled"
			}
		}
	}
	raw, _ := json.Marshal(body)
	var trig map[string]any
	_ = json.Unmarshal(raw, &trig)
	// The run is given trigger.body as before, and Taskiem's record of
	// the submission and the customer's answers apart (fronts §5.5).
	extra, _ := json.Marshal(map[string]any{"submission": rec.submission(ord, paymentStatus), "answers": rec.answers})
	var apart map[string]any
	_ = json.Unmarshal(extra, &apart)
	st, err := o.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: version, Environment: "prod",
		Trigger: map[string]any{"type": "storefront", "body": trig, "storefront": map[string]any{"slug": slug, "name": name},
			"submission": apart["submission"], "answers": apart["answers"]},
		StartedBy: "storefront:" + ord.Reference, TriggerID: "storefront/" + ord.Kind, DedupKey: dedup})
	note := ""
	if le, ok := runtime.IsLimit(err); ok {
		note, err = "Not started: the plan's limit "+le.Limit+" was reached.", nil
	}
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if note != "" {
			_, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, note)
			return err
		}
		// Started: the notes saying why it had not are cleared; others stay.
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET run_id = $3, note = CASE WHEN note LIKE 'No workflow%' OR note LIKE 'Not started:%' THEN NULL ELSE note END,
			updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, st.Ref.ID)
		return err
	})
}

// Summary is a basket in one line: "2 × Jollof rice, 1 × Zobo".
func Summary(lines []Line) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = fmt.Sprintf("%d × %s", l.Quantity, l.LineName())
	}
	return strings.Join(parts, ", ")
}

// --- payments ---

// CheckPayments asks the provider about every order waiting for its
// payment: paid ones move on (a run starts, or the waiting run is
// signalled), unpaid ones past their time expire.
func (o *Orders) CheckPayments(ctx context.Context) error {
	rows, err := o.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_storefront_payment_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := o.checkTenant(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

type due struct {
	id                         uuid.UUID
	reference, ref, connection string
	total                      int64
	expires                    *time.Time
	status, kind               string
	run                        *uuid.UUID
}

func (o *Orders) checkTenant(ctx context.Context, tenant uuid.UUID) error {
	var list []due
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// Orders waiting for their first payment, and later steps (fronts
		// F5) paid by card: a step asks its own amount and never expires.
		rows, err := tx.Query(ctx, `SELECT o.id, o.reference, COALESCE(o.payment_ref, ''), COALESCE(a.options->>'connection', ''),
				CASE WHEN o.pay_to_kobo IS NOT NULL THEN o.pay_step_kobo ELSE COALESCE(o.deposit_kobo, o.total_kobo) END,
				CASE WHEN o.pay_to_kobo IS NULL THEN o.expires_at END, o.status, o.kind, o.run_id
			FROM storefront_orders o LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE o.tenant_id = $1 AND (o.status = 'awaiting_payment' AND o.pay_to_kobo IS NULL OR o.pay_to_kobo IS NOT NULL AND o.payment_ref IS NOT NULL)
			ORDER BY o.created_at LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			return d, r.Scan(&d.id, &d.reference, &d.ref, &d.connection, &d.total, &d.expires, &d.status, &d.kind, &d.run)
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		paid := false
		var amount int64
		if name, card := paystackConnection(d.connection); card && d.ref != "" && o.Payments != nil {
			var status string
			var err error
			status, amount, err = o.Payments.Verify(ctx, tenant, name, d.ref)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			paid = status == "paid" && amount >= d.total
		}
		switch {
		case paid:
			if err := o.markPaid(ctx, tenant, d, amount); err != nil {
				errs = append(errs, err)
			}
		case d.expires != nil && o.now().After(*d.expires):
			var decided bool
			if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				err := tx.QueryRow(ctx, `UPDATE storefront_orders SET status = 'expired', updated_at = now() WHERE tenant_id = $1 AND id = $2 AND status = 'awaiting_payment'
					RETURNING decided_at IS NOT NULL`, tenant, d.id).Scan(&decided)
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM storefront_claims WHERE reference = $1`, d.reference); err != nil {
					return err
				}
				// Accepted and never paid for: the stock it took goes back.
				if err := o.returnStockTx(ctx, tx, tenant, Order{ID: d.id, Reference: d.reference, Kind: d.kind}); err != nil {
					return err
				}
				return audit(ctx, tx, tenant, "storefront.order_expired", d.reference, nil)
			}); err != nil {
				errs = append(errs, err)
				continue
			}
			o.sendReturns(ctx, tenant, d.id)
			// A booking's run is ended and the customer told: its slot is
			// free again.
			if d.kind == "booking" {
				if err := o.endBooking(ctx, tenant, d.reference, d.run, "The payment link for "+d.reference+" has expired, so the booking is cancelled. You are welcome to book again.", "expired:"+d.reference); err != nil {
					errs = append(errs, err)
				}
				continue
			}
			// An order confirmed from a customer's message (phase 2): they
			// are told in their thread; nothing of its stock went.
			if d.kind == "chat" {
				if o.ChatNotify != nil {
					var ord Order
					err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
						var err error
						ord, err = o.GetOrder(ctx, tx, tenant, d.reference)
						return err
					})
					if err == nil {
						text := lang.Default().Text(o.CustomerLanguage(ctx, tenant), "customer.chat_expired", "reference", d.reference)
						err = o.ChatNotify(ctx, tenant, ord, text)
					}
					if err != nil {
						o.log().Warn("storefront: telling a customer their order lapsed", "reference", d.reference, "err", err)
					}
				}
				continue
			}
			// A run waiting for the payment of an accepted order hears it
			// expired, and takes its expired path.
			if decided {
				if _, _, err := o.Signal(ctx, tenant, "prod", PaymentSignal, d.reference, "expired:"+d.reference, map[string]any{"reference": d.reference, "status": "expired"}); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// markPaid records a payment the provider confirmed, along the path
// matching takes too (CountPayment, matched.go): the money log entry for
// it, counted towards the order, and the order moved to paid, once,
// whichever of this check and the provider's event is first. An order
// paid at once starts its run; one paid after the owner accepted it wakes
// the run waiting for it.
func (o *Orders) markPaid(ctx context.Context, tenant uuid.UUID, d due, amount int64) error {
	var c Counted
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		in := PaymentIn{AmountKobo: amount, How: "exact", By: "system:" + checkoutChannel(d.connection), Reasons: []string{"paid through the order's checkout page"}}
		if o.MoneyLog != nil {
			id, _, err := o.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: checkoutChannel(d.connection),
				Reference: d.ref, Event: "payment.received", AmountKobo: &amount, Source: "lookup"})
			if err != nil {
				return err
			}
			in.EntryID = &id
		}
		var err error
		c, err = o.CountPayment(ctx, tx, tenant, d.reference, in)
		return err
	})
	if errors.Is(err, ErrDecision) {
		// Paid by other money meanwhile, or moved on: the payment waits in
		// the money log for a person.
		o.log().Warn("storefront: a checkout payment was not counted", "reference", d.reference, "err", err)
		return nil
	}
	if err != nil {
		return err
	}
	return o.Paid(ctx, tenant, c)
}

// --- what workflows do (the storefront platform connector) ---

// Decide moves an order: accept, decline, complete or cancel. Declining
// an order already paid leaves it refund_due: refunds are the owner's to
// make at the payment provider until the refund policy is decided.
// Accepting lowers the stock where the catalogue tracks it (short names
// the items there were too few of); a retry finishes what is left.
func (o *Orders) Decide(ctx context.Context, tenant uuid.UUID, reference, decision, note, actor string) (map[string]any, error) {
	var out map[string]any
	var accepted *Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, accepted, err = o.DecideTx(ctx, tx, tenant, reference, decision, note, actor)
		return err
	})
	if err != nil {
		return out, err
	}
	switch out["status"] {
	case "declined", "cancelled", "refund_due":
		o.sendReturnsOf(ctx, tenant, reference)
	}
	if accepted == nil {
		return out, nil
	}
	return o.afterAccept(ctx, tenant, *accepted, out)
}

// DecideTx is Decide's decision in the caller's transaction (a linked
// order's step takes it with its own record, business links §16.3): the
// order's status, its delivery and its slot. It answers the order when
// the decision accepts it, for the caller to lower its stock once the
// transaction commits (LowerStock), else nil.
func (o *Orders) DecideTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference, decision, note, actor string) (map[string]any, *Order, error) {
	var out map[string]any
	var accepted *Order
	err := func() error {
		ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
		if err != nil {
			return err
		}
		next := ""
		switch decision {
		case "accept":
			switch ord.Status {
			case "accepted", "awaiting_payment", "paid":
				next = ord.Status // already
			case "received":
				next = "accepted"
			}
		case "decline":
			switch ord.Status {
			case "declined", "refund_due":
				next = ord.Status
			case "received", "accepted", "dispatched", "changed":
				next = "declined"
				if ord.PaidAt != nil {
					next = "refund_due"
				}
			case "paid":
				next = "refund_due"
			}
		case "complete":
			switch ord.Status {
			case "completed":
				next = ord.Status
			case "received", "accepted", "paid", "dispatched", "on_the_way", "arrived":
				next = "completed"
			}
		case "no_show":
			switch ord.Status {
			case "no_show":
				next = ord.Status
			case "accepted", "paid":
				if ord.Kind == "booking" {
					next = "no_show"
				}
			}
		case "cancel":
			switch ord.Status {
			case "cancelled", "expired":
				next = ord.Status
			case "received", "accepted", "awaiting_payment", "dispatched", "on_the_way", "arrived", "proposed", "changed":
				next = "cancelled"
				if ord.PaidAt != nil {
					next = "refund_due"
				}
			}
		default:
			return fmt.Errorf("%w: decision %q is accept, decline, complete, cancel or no_show", ErrDecision, decision)
		}
		if next == "" {
			return fmt.Errorf("%w: %s cannot be %sed while %s", ErrDecision, reference, strings.TrimSuffix(decision, "e"), ord.Status)
		}
		if ord.Kind == "trade" && next == "accepted" && next != ord.Status {
			// A linked business's order is accepted only with every line
			// priced (business links §16.3), by whichever path accepts it:
			// its invoice charges each line's price.
			var unpriced bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM link_orders o, jsonb_array_elements(o.lines) l
				WHERE o.order_id = $1 AND COALESCE((l->>'price_kobo')::bigint, 0) < 1)`, ord.ID).Scan(&unpriced); err != nil {
				return err
			}
			if unpriced {
				return fmt.Errorf("%w: %s has an item with no price; change the order to give it yours", ErrDecision, reference)
			}
		}
		if next != ord.Status {
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, note = COALESCE(NULLIF($4, ''), note), decided_by = COALESCE(decided_by, $5),
				decided_at = COALESCE(decided_at, now()), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, next, note, actor); err != nil {
				return err
			}
			if err := audit(ctx, tx, tenant, "storefront.order_"+next, reference, map[string]any{"by": actor}); err != nil {
				return err
			}
			// Its delivery (dispatch): completed by the owner, the rider who
			// holds it delivered it; declined or cancelled, it is released
			// and any offer withdrawn.
			switch next {
			case "completed":
				if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'delivered', delivered_at = now() WHERE tenant_id = $1 AND order_id = $2
					AND status IN ('accepted', 'picked_up', 'arrived')`, tenant, ord.ID); err != nil {
					return err
				}
				if ord.Kind == "trade" {
					// A linked order delivered by a rider: delivered now.
					if _, err := tx.Exec(ctx, `UPDATE link_orders SET delivered_at = COALESCE(delivered_at, now()), updated_at = now() WHERE order_id = $1`, ord.ID); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'withdrawn', answered_at = now() WHERE tenant_id = $1 AND order_id = $2 AND status = 'offered'`,
					tenant, ord.ID); err != nil {
					return err
				}
			case "declined", "cancelled", "refund_due":
				if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = CASE WHEN status = 'offered' THEN 'withdrawn' ELSE 'released' END,
					released_at = CASE WHEN status = 'offered' THEN NULL ELSE now() END, answered_at = COALESCE(answered_at, now())
					WHERE tenant_id = $1 AND order_id = $2 AND status IN ('offered', 'accepted', 'picked_up', 'arrived')`, tenant, ord.ID); err != nil {
					return err
				}
			}
		}
		// The stock it took goes back to the shelf, unless its rider had
		// delivered it. Asked on a retry too: it is recorded once.
		switch next {
		case "declined", "cancelled", "refund_due":
			if err := o.returnStockTx(ctx, tx, tenant, ord); err != nil {
				return err
			}
		}
		if next != ord.Status {
			// A booking declined or cancelled frees its slot.
			if (ord.Kind == "booking" || ord.Kind == "hire" || ord.Kind == "space" || ord.Kind == "tickets" || ord.Kind == "class") && (next == "declined" || next == "cancelled" || next == "refund_due") {
				if _, err := tx.Exec(ctx, `DELETE FROM storefront_claims WHERE reference = $1`, reference); err != nil {
					return err
				}
			}
		}
		out = map[string]any{"reference": reference, "status": next, "refund_due": next == "refund_due", "total": FormatNaira(ord.TotalKobo), "short": []string{}}
		if decision == "accept" {
			accepted = &ord
		}
		return nil
	}()
	return out, accepted, err
}

// LowerStock lowers the stock for an order accepted in a caller's own
// transaction (DecideTx), once it committed; a retry finishes what is
// left. It answers the items there was too little of.
func (o *Orders) LowerStock(ctx context.Context, tenant uuid.UUID, reference string) ([]string, error) {
	var ord Order
	if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		ord, err = o.GetOrder(ctx, tx, tenant, reference)
		return err
	}); err != nil {
		return nil, err
	}
	out, err := o.afterAccept(ctx, tenant, ord, map[string]any{})
	if err != nil {
		return nil, err
	}
	short, _ := out["short"].([]string)
	return short, nil
}

// afterAccept lowers an accepted order's stock and notes what there was
// too little of on it.
func (o *Orders) afterAccept(ctx context.Context, tenant uuid.UUID, accepted Order, out map[string]any) (map[string]any, error) {
	short, err := o.lowerStock(ctx, tenant, accepted)
	if err != nil {
		return nil, err
	}
	if len(short) > 0 {
		note := "Accepted with too little stock of " + strings.Join(short, ", ")
		err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, accepted.ID, note)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	out["short"] = short
	return out, nil
}

// PaymentLink makes the payment page for an accepted order paid after
// acceptance (once: a second call returns the same page) and leaves the
// order waiting for it. On an order paid before, it asks for more (fronts
// F5): amountKobo (a deposit's balance in parts, an instalment), or the
// balance when 0.
func (o *Orders) PaymentLink(ctx context.Context, tenant uuid.UUID, reference string, amountKobo int64) (map[string]any, error) {
	var ord Order
	var slug, connection string
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(s.published_slug, s.slug), COALESCE(a.options->>'connection', '') FROM storefronts s
			JOIN storefront_actions a ON a.storefront_id = s.id AND a.id = $2 WHERE s.id = $1`, ord.storefront, ord.action).Scan(&slug, &connection)
	})
	if err != nil {
		return nil, err
	}
	if amountKobo < 0 {
		return nil, fmt.Errorf("%w: an amount is above 0", ErrInvalid)
	}
	if ord.PaidAt != nil || ord.PayToKobo != nil {
		return o.stepLink(ctx, tenant, ord, slug, connection, amountKobo)
	}
	if amountKobo > 0 && amountKobo != ord.Payable() {
		return nil, fmt.Errorf("%w: %s's first payment is its %s; ask for more once it is paid", ErrDecision, reference, FormatNaira(ord.Payable()))
	}
	if ord.PaymentURL != "" {
		return map[string]any{"reference": reference, "url": ord.PaymentURL, "total": FormatNaira(ord.Payable()), "amount_kobo": ord.Payable(), "balance_kobo": ord.BalanceKobo()}, nil
	}
	name, card := paystackConnection(connection)
	if !card && ord.Status == "awaiting_payment" && ord.Payment == "after_accept" {
		// Asked again: the status page, where it is paid by transfer.
		return o.statusLinkFor(ctx, tenant, ord)
	}
	if ord.Status != "accepted" || ord.Payment != "after_accept" || ord.TotalKobo < 100 {
		return nil, fmt.Errorf("%w: %s needs no payment link while %s", ErrDecision, reference, ord.Status)
	}
	if ord.Payable() < 100 {
		return nil, fmt.Errorf("%w: %s has nothing to pay online", ErrDecision, reference)
	}
	if !card {
		// Another rail's form: the link is the order's status page, where
		// the customer gets an account number to pay into (N3).
		if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'awaiting_payment', expires_at = now() + interval '24 hours', updated_at = now()
				WHERE tenant_id = $1 AND id = $2 AND status = 'accepted'`, tenant, ord.ID)
			return err
		}); err != nil {
			return nil, err
		}
		return o.statusLinkFor(ctx, tenant, ord)
	}
	if o.Payments == nil {
		return nil, errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, name, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.Payable(),
		Email: o.payerEmail(ord.Customer, reference), CallbackURL: "https://" + slug + "." + o.PreviewDomain + "/",
		Metadata: map[string]any{"storefront_reference": reference}})
	if err != nil {
		return nil, err
	}
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'awaiting_payment', payment_url = $3, payment_ref = $4, expires_at = now() + interval '24 hours',
			updated_at = now() WHERE tenant_id = $1 AND id = $2 AND status = 'accepted'`, tenant, ord.ID, url, ref)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"reference": reference, "url": url, "total": FormatNaira(ord.Payable()), "amount_kobo": ord.Payable(), "balance_kobo": ord.BalanceKobo()}, nil
}

// NotifyCustomer queues a message to the order's customer: an SMS, and an
// email when they gave one. The recipient comes from the order, never from
// the workflow, so a workflow can only ever write to its own customers.
// "{link}" in the text becomes the customer's status link (where a booking
// is moved or cancelled), which no run ever sees.
func (o *Orders) NotifyCustomer(ctx context.Context, tenant uuid.UUID, reference, text, subject, key string) (map[string]any, error) {
	if o.Messages == nil {
		return nil, errors.New("storefront: messages to customers are not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("%w: no text", sender.ErrInvalid)
	}
	var channels []string
	overAllowance := false
	var trade, chat *Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ord, err := o.GetOrder(ctx, tx, tenant, reference)
		if err != nil {
			return err
		}
		if ord.Kind == "trade" {
			trade = &ord
			return nil
		}
		if ord.Kind == "chat" {
			if strings.Contains(text, "{link}") {
				link, err := o.ChatStatusLink(ctx, tx, tenant, ord)
				if err != nil {
					return err
				}
				text = strings.ReplaceAll(text, "{link}", link)
			}
			chat = &ord
			return nil
		}
		var draft []byte
		if err := tx.QueryRow(ctx, `SELECT draft FROM storefronts WHERE id = $1`, ord.storefront).Scan(&draft); err != nil {
			return err
		}
		var c Card
		_ = json.Unmarshal(draft, &c)
		if strings.Contains(text, "{link}") {
			link, err := o.linkOf(ctx, tx, tenant, ord)
			if err != nil {
				return err
			}
			text = strings.ReplaceAll(text, "{link}", link)
		}
		if ord.Customer.Phone != "" {
			queued, err := o.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeConfirmation,
				To: ord.Customer.Phone, SenderName: c.Name, Text: text, Key: key + ":sms"})
			switch {
			case errors.Is(err, sender.ErrAllowance):
				overAllowance = true // the email, if any, still goes
			case err != nil:
				return err
			case queued:
				channels = append(channels, "sms")
			}
		}
		if ord.Customer.Email != "" {
			if subject == "" {
				subject = c.Name + ": " + reference
			}
			queued, err := o.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.Email, Purpose: sender.PurposeConfirmation,
				To: ord.Customer.Email, SenderName: c.Name, Subject: subject, Text: text, Key: key + ":email"})
			if err != nil {
				return err
			}
			if queued {
				channels = append(channels, "email")
			}
		}
		return nil
	})
	if err == nil && trade != nil {
		// A linked business is told in its own Taskiem, never by SMS.
		if o.TradeNotify == nil {
			return nil, errors.New("storefront: linked businesses cannot be told here")
		}
		if err := o.TradeNotify(ctx, tenant, *trade, strings.ReplaceAll(text, "{link}", "")); err != nil {
			return nil, err
		}
		channels = []string{"link"}
	}
	if err == nil && chat != nil {
		// A customer who wrote to the business's own number is told there.
		if o.ChatNotify == nil {
			return nil, errors.New("storefront: customers' threads cannot be written to here")
		}
		if err := o.ChatNotify(ctx, tenant, *chat, text); err != nil {
			return nil, err
		}
		channels = []string{"whatsapp"}
	}
	if channels == nil {
		channels = []string{}
	}
	out := map[string]any{"reference": reference, "queued": channels}
	if overAllowance {
		out["sms_over_allowance"] = true
	}
	return out, err
}

// ForgetExpired erases the customer's details of ended orders past their
// business's retention (storefronts.retention_days, 12 months by
// default; docs/spec/storefronts.md, Privacy), up to 1,000 at a time,
// and audits how many each business lost.
func (o *Orders) ForgetExpired(ctx context.Context) error {
	rows, err := o.Pool.Query(ctx, `SELECT tenant_id, forgotten FROM taskiem_storefront_orders_forget(1000)`)
	if err != nil {
		return err
	}
	type forgot struct {
		tenant uuid.UUID
		n      int
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (forgot, error) {
		var f forgot
		return f, r.Scan(&f.tenant, &f.n)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range list {
		if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{f.tenant}, func(tx pgx.Tx) error {
			return audit(ctx, tx, f.tenant, "storefront.retention", "orders", map[string]any{"forgotten": f.n})
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'storefront', $2, $3, $4)`, tenant, action, target, raw)
	return err
}

// CustomerLanguage is the language the business speaks to its customers
// in: its default chat language (Settings > Languages), English when it
// has none (no-technical-steps N6).
func (o *Orders) CustomerLanguage(ctx context.Context, tenant uuid.UUID) lang.Tag {
	l := lang.EN
	_ = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var def string
		if err := tx.QueryRow(ctx, `SELECT default_language FROM tenant_channel_settings WHERE tenant_id = $1`, tenant).Scan(&def); err == nil && lang.Valid(lang.Tag(def)) {
			l = lang.Tag(def)
		}
		return nil
	})
	return l
}

// stepLink asks for more on an order paid before (fronts F5): amount, or
// the balance when 0, as a new step with its own checkout reference, so a
// retried call gives the same page and a later one a new page. The order's
// status stays as it is; the run waits for the storefront.payment signal,
// which says what the step asked and what is left. Another rail's form
// gives the status page, where the step is paid by transfer.
func (o *Orders) stepLink(ctx context.Context, tenant uuid.UUID, ord Order, slug, connection string, amount int64) (map[string]any, error) {
	switch ord.Status {
	case "refunded", "refund_due", "cancelled", "declined", "expired":
		return nil, fmt.Errorf("%w: %s takes no more payments while %s", ErrDecision, ord.Reference, strings.ReplaceAll(ord.Status, "_", " "))
	}
	balance := ord.BalanceKobo()
	if amount == 0 || amount > balance {
		amount = balance
	}
	out := func(url string, asked int64) map[string]any {
		return map[string]any{"reference": ord.Reference, "url": url, "total": FormatNaira(asked), "amount_kobo": asked, "balance_kobo": balance}
	}
	// A step already open is the one asked for, whatever the amount: it is
	// paid (or its link made again) before another.
	if ord.PayToKobo != nil {
		asked := *ord.PayToKobo - ord.PaidKobo
		if ord.PaymentURL != "" {
			return out(ord.PaymentURL, asked), nil
		}
		return o.stepStatusLink(ctx, tenant, ord, asked, balance)
	}
	if amount < 100 {
		return nil, fmt.Errorf("%w: %s has nothing left to pay", ErrDecision, ord.Reference)
	}
	seq := ord.PaySeq + 1
	name, card := paystackConnection(connection)
	var url, ref *string
	if card {
		if o.Payments == nil {
			return nil, errors.New("storefront: no payment provider is configured")
		}
		u, r, err := o.Payments.Create(ctx, tenant, name, PaymentRequest{Reference: fmt.Sprintf("%s_%d", paymentKey(ord.ID), seq), AmountKobo: amount,
			Email: o.payerEmail(ord.Customer, ord.Reference), CallbackURL: "https://" + slug + "." + o.PreviewDomain + "/",
			Metadata: map[string]any{"storefront_reference": ord.Reference}})
		if err != nil {
			return nil, err
		}
		url, ref = &u, &r
	}
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE storefront_orders SET pay_to_kobo = paid_kobo + $4, pay_step_kobo = $4, pay_seq = $3, payment_url = $5, payment_ref = $6, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND pay_to_kobo IS NULL AND pay_seq = $3 - 1`, tenant, ord.ID, seq, amount, url, ref)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: %s changed meanwhile; ask again", ErrDecision, ord.Reference)
		}
		return audit(ctx, tx, tenant, "storefront.payment_step", ord.Reference, map[string]any{"amount_kobo": amount, "step": seq, "card": card})
	})
	if err != nil {
		return nil, err
	}
	if url != nil {
		return out(*url, amount), nil
	}
	return o.stepStatusLink(ctx, tenant, ord, amount, balance)
}

// stepStatusLink answers a step paid by transfer: the order's status page.
func (o *Orders) stepStatusLink(ctx context.Context, tenant uuid.UUID, ord Order, asked, balance int64) (map[string]any, error) {
	m, err := o.statusLinkFor(ctx, tenant, ord)
	if err != nil {
		return nil, err
	}
	m["total"], m["amount_kobo"], m["balance_kobo"] = FormatNaira(asked), asked, balance
	return m, nil
}

// Returned is what marking a hire returned found: the days it came back
// late and the fee added for them.
type Returned struct {
	Order       Order
	LateDays    int
	LateFeeKobo int64
}

// MarkReturned ends a hire's claim: its items are back (fronts F5). A hire
// back after the day it was due adds its action's late fee for each day
// late to what it costs, for a later payment step to ask. Marking it again
// changes nothing.
func (o *Orders) MarkReturned(ctx context.Context, tenant uuid.UUID, reference, actor string) (Returned, error) {
	var r Returned
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
		if err != nil {
			return err
		}
		r.Order = ord
		if ord.Kind != "hire" {
			return fmt.Errorf("%w: %s is not a hire", ErrDecision, reference)
		}
		back := o.now()
		if ord.ReturnedAt != nil {
			// Marked before: the same answer, from when it was.
			back = *ord.ReturnedAt
		} else {
			switch ord.Status {
			case "declined", "cancelled", "expired", "refunded", "refund_due", "price_changed":
				return fmt.Errorf("%w: %s has nothing out (%s)", ErrDecision, reference, strings.ReplaceAll(ord.Status, "_", " "))
			}
		}
		if ord.EndsAt != nil && back.After(*ord.EndsAt) {
			r.LateDays = int((back.Sub(*ord.EndsAt) + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT options FROM storefront_actions WHERE id = $1`, ord.action).Scan(&raw); err != nil {
			return err
		}
		var opts ActionOptions
		_ = json.Unmarshal(raw, &opts)
		r.LateFeeKobo = int64(r.LateDays) * opts.LateFeeKobo
		if ord.ReturnedAt != nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET returned_at = $4, extra_kobo = extra_kobo + $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, r.LateFeeKobo, back); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM storefront_claims WHERE reference = $1`, reference); err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant, "storefront.hire_returned", reference, map[string]any{"by": actor, "late_days": r.LateDays, "late_fee_kobo": r.LateFeeKobo}); err != nil {
			return err
		}
		r.Order, err = o.GetOrder(ctx, tx, tenant, reference)
		return err
	})
	return r, err
}
