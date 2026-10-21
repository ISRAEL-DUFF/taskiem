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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
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
	Stock    Stock
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
	Cancel func(ctx context.Context, ref runtime.RunRef, by string) error
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
	ID         uuid.UUID  `json:"id"`
	Reference  string     `json:"reference"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Customer   Customer   `json:"customer"`
	Basket     []Line     `json:"basket"`
	TotalKobo  int64      `json:"total_kobo"`
	Fulfilment string     `json:"fulfilment,omitempty"`
	Payment    string     `json:"payment"`
	PaymentURL string     `json:"payment_url,omitempty"`
	PaidAt     *time.Time `json:"paid_at"`
	RunID      *uuid.UUID `json:"run_id"`
	Note       string     `json:"note,omitempty"`
	DecidedBy  string     `json:"decided_by,omitempty"`
	DecidedAt  *time.Time `json:"decided_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
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
	subject     string
	storefront  uuid.UUID
	paymentRef  string
	customerEnv map[string]any
}

const orderCols = `id, reference, kind, status, customer, subject_id, basket, total_kobo, COALESCE(fulfilment, ''), payment, COALESCE(payment_url, ''),
	COALESCE(payment_ref, ''), paid_at, run_id, COALESCE(note, ''), COALESCE(decided_by, ''), decided_at, expires_at, created_at, storefront_id,
	COALESCE(service_id, ''), COALESCE(service_name, ''), starts_at, ends_at, version, deposit_kobo, price_change`

func scanOrder(r pgx.Row) (Order, error) {
	var x Order
	var basket, change []byte
	err := r.Scan(&x.ID, &x.Reference, &x.Kind, &x.Status, &x.customerEnv, &x.subject, &basket, &x.TotalKobo, &x.Fulfilment, &x.Payment, &x.PaymentURL,
		&x.paymentRef, &x.PaidAt, &x.RunID, &x.Note, &x.DecidedBy, &x.DecidedAt, &x.ExpiresAt, &x.CreatedAt, &x.storefront,
		&x.ServiceID, &x.ServiceName, &x.StartsAt, &x.EndsAt, &x.Version, &x.DepositKobo, &change)
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
	q := `SELECT ` + orderCols + ` FROM storefront_orders WHERE tenant_id = $1`
	args := []any{tenant}
	if kind != "" {
		args = append(args, kind)
		q += fmt.Sprintf(` AND kind = $%d`, len(args))
	}
	switch status {
	case "":
	case "open":
		q += ` AND status IN ('received', 'awaiting_payment', 'paid', 'accepted', 'refund_due', 'price_changed')`
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

type inboxRow struct {
	id, storefront, tenant uuid.UUID
	kind, reference        string
	statusHash, payload    []byte
}

// Drain takes waiting submissions from the inbox and processes each. A
// submission is removed from the inbox only when processed: one that
// fails is retried after its claim lapses.
func (o *Orders) Drain(ctx context.Context) error {
	rows, err := o.Pool.Query(ctx, `SELECT id, storefront_id, tenant_id, kind, reference, status_hash, payload FROM taskiem_storefront_inbox_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inboxRow, error) {
		var x inboxRow
		return x, r.Scan(&x.id, &x.storefront, &x.tenant, &x.kind, &x.reference, &x.statusHash, &x.payload)
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
			JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = $2 WHERE s.id = $1`, x.storefront, x.kind).Scan(&slug, &raw); err != nil {
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
	live, err := o.Live.Items(ctx, x.tenant, sub.Booking != nil, ids)
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
	if (sub.Kind != "order" && sub.Kind != "booking") || payable < 100 {
		payment = "none"
	}
	if payment == "at_order" {
		status = "awaiting_payment"
		e := o.now().Add(PaymentTTL)
		expires = &e
	}
	var change []byte
	switch {
	case changed && (len(pc.Gone) > 0 && (sub.Booking != nil || len(pc.Basket) == 0)):
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
	var noteArg *string
	if note != "" {
		noteArg = &note
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket,
		total_kobo, fulfilment, payment, expires_at, status_key, deposit_kobo, service_id, service_name, starts_at, ends_at, price_change, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		id, x.tenant, x.storefront, x.kind, x.reference, x.statusHash, status, customer, subject, basket, sub.TotalKobo, fulfilment, payment, expires,
		key, deposit, serviceID, serviceName, starts, ends, change, noteArg); err != nil {
		return Order{}, err
	}
	if b != nil {
		kept := true
		if status == "cancelled" {
			_, err = tx.Exec(ctx, `DELETE FROM storefront_holds WHERE reference = $1`, x.reference)
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
	tag, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = $2 WHERE reference = $1 AND (expires_at IS NULL OR expires_at > now())`, reference, expires)
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
	_, err = tx.Exec(ctx, `INSERT INTO storefront_holds (reference, storefront_id, tenant_id, service_id, starts_at, until_at, expires_at)
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
	if o.Payments == nil {
		return errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, connection, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.Payable(),
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

// Payable is what is paid online: the deposit, or the total.
func (x Order) Payable() int64 {
	if x.DepositKobo != nil {
		return *x.DepositKobo
	}
	return x.TotalKobo
}

// startRun starts the run of the workflow deployed for the order's form,
// in prod. An order with no such workflow stays received, with a note.
func (o *Orders) startRun(ctx context.Context, tenant uuid.UUID, reference string) error {
	var ord Order
	var wf uuid.UUID
	var version int
	var slug, name string
	var found, autoConfirm bool
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		var draft []byte
		if err := tx.QueryRow(ctx, `SELECT COALESCE(published_slug, slug), draft FROM storefronts WHERE id = $1`, ord.storefront).Scan(&slug, &draft); err != nil {
			return err
		}
		var c Card
		_ = json.Unmarshal(draft, &c)
		name = c.Name
		if ord.Kind == "booking" {
			sf, err := Get(ctx, tx, tenant)
			if err != nil {
				return err
			}
			autoConfirm = sf.Availability.AutoConfirm
		}
		err = tx.QueryRow(ctx, `SELECT workflow_id, version FROM triggers WHERE type = 'storefront' AND storefront_action = $1 AND environment = 'prod'`, ord.Kind).Scan(&wf, &version)
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
	}
	body := map[string]any{"reference": ord.Reference, "kind": ord.Kind, "customer": ord.Customer, "basket": ord.Basket, "total_kobo": ord.TotalKobo,
		"total": FormatNaira(ord.TotalKobo), "summary": Summary(ord.Basket), "fulfilment": ord.Fulfilment, "payment": map[string]any{"mode": ord.Payment, "status": paymentStatus}}
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
	st, err := o.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: version, Environment: "prod",
		Trigger:   map[string]any{"type": "storefront", "body": trig, "storefront": map[string]any{"slug": slug, "name": name}},
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
		parts[i] = fmt.Sprintf("%d × %s", l.Quantity, l.Name)
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
		rows, err := tx.Query(ctx, `SELECT o.id, o.reference, COALESCE(o.payment_ref, ''), COALESCE(a.options->>'connection', ''), COALESCE(o.deposit_kobo, o.total_kobo), o.expires_at, o.status, o.kind, o.run_id
			FROM storefront_orders o JOIN storefront_actions a ON a.storefront_id = o.storefront_id AND a.kind = o.kind
			WHERE o.tenant_id = $1 AND o.status = 'awaiting_payment' ORDER BY o.created_at LIMIT 100`, tenant)
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
		if d.ref != "" && o.Payments != nil {
			status, amount, err := o.Payments.Verify(ctx, tenant, d.connection, d.ref)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			paid = status == "paid" && amount >= d.total
		}
		switch {
		case paid:
			if err := o.markPaid(ctx, tenant, d); err != nil {
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
				if _, err := tx.Exec(ctx, `DELETE FROM storefront_holds WHERE reference = $1`, d.reference); err != nil {
					return err
				}
				return audit(ctx, tx, tenant, "storefront.order_expired", d.reference, nil)
			}); err != nil {
				errs = append(errs, err)
				continue
			}
			// A booking's run is ended and the customer told: its slot is
			// free again.
			if d.kind == "booking" {
				if err := o.endBooking(ctx, tenant, d.reference, d.run, "The payment link for "+d.reference+" has expired, so the booking is cancelled. You are welcome to book again.", "expired:"+d.reference); err != nil {
					errs = append(errs, err)
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

// markPaid records a payment. An order paid at once starts its run; one
// paid after the owner accepted it wakes the run waiting for it.
func (o *Orders) markPaid(ctx context.Context, tenant uuid.UUID, d due) error {
	var accepted bool
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE storefront_orders SET status = CASE WHEN decided_at IS NULL THEN 'received' ELSE 'paid' END,
			paid_at = now(), expires_at = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2 AND status = 'awaiting_payment'
			RETURNING decided_at IS NOT NULL`, tenant, d.id).Scan(&accepted); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = NULL WHERE reference = $1`, d.reference); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, "storefront.order_paid", d.reference, map[string]any{"total_kobo": d.total})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already moved on
	}
	if err != nil {
		return err
	}
	if accepted && d.kind == "booking" {
		return nil // its run carries on to the appointment
	}
	if accepted {
		_, _, err = o.Signal(ctx, tenant, "prod", PaymentSignal, d.reference, "paid:"+d.reference, map[string]any{"reference": d.reference, "status": "paid"})
		return err
	}
	return o.startRun(ctx, tenant, d.reference)
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
			case "received", "accepted":
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
			case "received", "accepted", "paid":
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
			case "received", "accepted", "awaiting_payment":
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
		if next != ord.Status {
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, note = COALESCE(NULLIF($4, ''), note), decided_by = COALESCE(decided_by, $5),
				decided_at = COALESCE(decided_at, now()), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, next, note, actor); err != nil {
				return err
			}
			if err := audit(ctx, tx, tenant, "storefront.order_"+next, reference, map[string]any{"by": actor}); err != nil {
				return err
			}
			// A booking declined or cancelled frees its slot.
			if ord.Kind == "booking" && (next == "declined" || next == "cancelled" || next == "refund_due") {
				if _, err := tx.Exec(ctx, `DELETE FROM storefront_holds WHERE reference = $1`, reference); err != nil {
					return err
				}
			}
		}
		out = map[string]any{"reference": reference, "status": next, "refund_due": next == "refund_due", "total": FormatNaira(ord.TotalKobo), "short": []string{}}
		if decision == "accept" {
			accepted = &ord
		}
		return nil
	})
	if err != nil || accepted == nil {
		return out, err
	}
	short, err := o.lowerStock(ctx, tenant, *accepted)
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
// order waiting for it.
func (o *Orders) PaymentLink(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error) {
	var ord Order
	var slug, connection string
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(s.published_slug, s.slug), COALESCE(a.options->>'connection', '') FROM storefronts s
			JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = $2 WHERE s.id = $1`, ord.storefront, ord.Kind).Scan(&slug, &connection)
	})
	if err != nil {
		return nil, err
	}
	if ord.PaymentURL != "" {
		return map[string]any{"reference": reference, "url": ord.PaymentURL, "total": FormatNaira(ord.Payable())}, nil
	}
	if ord.Status != "accepted" || ord.Payment != "after_accept" || ord.TotalKobo < 100 {
		return nil, fmt.Errorf("%w: %s needs no payment link while %s", ErrDecision, reference, ord.Status)
	}
	if ord.Payable() < 100 {
		return nil, fmt.Errorf("%w: %s has nothing to pay online", ErrDecision, reference)
	}
	if o.Payments == nil {
		return nil, errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, connection, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.Payable(),
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
	return map[string]any{"reference": reference, "url": url, "total": FormatNaira(ord.Payable())}, nil
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
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ord, err := o.GetOrder(ctx, tx, tenant, reference)
		if err != nil {
			return err
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
	if channels == nil {
		channels = []string{}
	}
	out := map[string]any{"reference": reference, "queued": channels}
	if overAllowance {
		out["sms_over_allowance"] = true
	}
	return out, err
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
