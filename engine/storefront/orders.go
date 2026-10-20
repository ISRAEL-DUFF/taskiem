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
	ID          uuid.UUID  `json:"id"`
	Reference   string     `json:"reference"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Customer    Customer   `json:"customer"`
	Basket      []Line     `json:"basket"`
	TotalKobo   int64      `json:"total_kobo"`
	Fulfilment  string     `json:"fulfilment,omitempty"`
	Payment     string     `json:"payment"`
	PaymentURL  string     `json:"payment_url,omitempty"`
	PaidAt      *time.Time `json:"paid_at"`
	RunID       *uuid.UUID `json:"run_id"`
	Note        string     `json:"note,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	DecidedAt   *time.Time `json:"decided_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	subject     string
	storefront  uuid.UUID
	paymentRef  string
	customerEnv map[string]any
}

const orderCols = `id, reference, kind, status, customer, subject_id, basket, total_kobo, COALESCE(fulfilment, ''), payment, COALESCE(payment_url, ''),
	COALESCE(payment_ref, ''), paid_at, run_id, COALESCE(note, ''), COALESCE(decided_by, ''), decided_at, expires_at, created_at, storefront_id`

func scanOrder(r pgx.Row) (Order, error) {
	var x Order
	var basket []byte
	err := r.Scan(&x.ID, &x.Reference, &x.Kind, &x.Status, &x.customerEnv, &x.subject, &basket, &x.TotalKobo, &x.Fulfilment, &x.Payment, &x.PaymentURL,
		&x.paymentRef, &x.PaidAt, &x.RunID, &x.Note, &x.DecidedBy, &x.DecidedAt, &x.ExpiresAt, &x.CreatedAt, &x.storefront)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNoOrder
	}
	if err != nil {
		return Order{}, err
	}
	_ = json.Unmarshal(basket, &x.Basket)
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
// "open" is every order still needing the owner.
func (o *Orders) ListOrders(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, status string, limit int) ([]Order, error) {
	q := `SELECT ` + orderCols + ` FROM storefront_orders WHERE tenant_id = $1`
	args := []any{tenant}
	switch status {
	case "":
	case "open":
		q += ` AND status IN ('received', 'awaiting_payment', 'paid', 'accepted', 'refund_due')`
	default:
		q += ` AND status = $2`
		args = append(args, status)
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
// skipped when already done, so a retry carries on where it stopped.
func (o *Orders) process(ctx context.Context, x inboxRow) error {
	sub, err := OpenSubmission(o.InboxKey, x.storefront, x.reference, x.payload)
	if err != nil {
		o.log().Error("storefront: an inbox payload that does not open; dropped", "reference", x.reference, "err", err)
		return nil
	}
	var ord Order
	var slug, connection string
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		var opts []byte
		if err := tx.QueryRow(ctx, `SELECT COALESCE(s.published_slug, s.slug), a.options FROM storefronts s
			JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = $2 WHERE s.id = $1`, x.storefront, x.kind).Scan(&slug, &opts); err != nil {
			return err
		}
		var o2 struct {
			Connection string `json:"connection"`
		}
		_ = json.Unmarshal(opts, &o2)
		connection = o2.Connection
		existing, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, x.tenant, x.reference))
		if err == nil {
			ord = existing
			return nil
		}
		if !errors.Is(err, ErrNoOrder) {
			return err
		}
		ord, err = o.record(ctx, tx, x, sub)
		return err
	})
	if err != nil {
		return err
	}
	if ord.Status == "awaiting_payment" && ord.PaymentURL == "" {
		if err := o.createPayment(ctx, x.tenant, ord, sub, slug, connection); err != nil {
			return err
		}
		return nil // the run starts once it is paid (CheckPayments)
	}
	if ord.Status == "received" && ord.RunID == nil {
		return o.startRun(ctx, x.tenant, ord.Reference)
	}
	return nil
}

// record seals the customer and writes the order.
func (o *Orders) record(ctx context.Context, tx pgx.Tx, x inboxRow, sub Submission) (Order, error) {
	envPhone, err := o.Vault.SealTx(ctx, tx, x.tenant, "phone", sub.Customer.Phone)
	if err != nil {
		return Order{}, err
	}
	subject, _ := envPhone["subject"].(string)
	customer, err := o.Vault.SealUnderTx(ctx, tx, x.tenant, subject, "customer", sub.Customer)
	if err != nil {
		return Order{}, err
	}
	status, payment := "received", sub.Payment
	var expires *time.Time
	if sub.Kind != "order" || sub.TotalKobo < 100 {
		payment = "none"
	}
	if payment == "at_order" {
		status = "awaiting_payment"
		e := o.now().Add(PaymentTTL)
		expires = &e
	}
	basket, _ := json.Marshal(sub.Basket)
	var fulfilment *string
	if sub.Fulfilment != "" {
		fulfilment = &sub.Fulfilment
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket, total_kobo, fulfilment, payment, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		id, x.tenant, x.storefront, x.kind, x.reference, x.statusHash, status, customer, subject, basket, sub.TotalKobo, fulfilment, payment, expires); err != nil {
		return Order{}, err
	}
	if err := audit(ctx, tx, x.tenant, "storefront.order_received", x.reference, map[string]any{"kind": x.kind, "total_kobo": sub.TotalKobo, "payment": payment, "channel": sub.Channel}); err != nil {
		return Order{}, err
	}
	return Order{ID: id, Reference: x.reference, Kind: x.kind, Status: status, Payment: payment, TotalKobo: sub.TotalKobo, storefront: x.storefront}, nil
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

func (o *Orders) createPayment(ctx context.Context, tenant uuid.UUID, ord Order, sub Submission, slug, connection string) error {
	if o.Payments == nil {
		return errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, connection, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.TotalKobo,
		Email: o.payerEmail(sub.Customer, ord.Reference), CallbackURL: o.statusURL(slug, ord.Reference, sub.StatusKey),
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

// startRun starts the run of the workflow deployed for the order's form,
// in prod. An order with no such workflow stays received, with a note.
func (o *Orders) startRun(ctx context.Context, tenant uuid.UUID, reference string) error {
	var ord Order
	var wf uuid.UUID
	var version int
	var slug, name string
	var found bool
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
	raw, _ := json.Marshal(body)
	var trig map[string]any
	_ = json.Unmarshal(raw, &trig)
	st, err := o.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: version, Environment: "prod",
		Trigger:   map[string]any{"type": "storefront", "body": trig, "storefront": map[string]any{"slug": slug, "name": name}},
		StartedBy: "storefront:" + ord.Reference, TriggerID: "storefront/" + ord.Kind, DedupKey: ord.ID.String()})
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
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET run_id = $3, note = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, st.Ref.ID)
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
	status                     string
}

func (o *Orders) checkTenant(ctx context.Context, tenant uuid.UUID) error {
	var list []due
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT o.id, o.reference, COALESCE(o.payment_ref, ''), COALESCE(a.options->>'connection', ''), o.total_kobo, o.expires_at, o.status
			FROM storefront_orders o JOIN storefront_actions a ON a.storefront_id = o.storefront_id AND a.kind = o.kind
			WHERE o.tenant_id = $1 AND o.status = 'awaiting_payment' ORDER BY o.created_at LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			return d, r.Scan(&d.id, &d.reference, &d.ref, &d.connection, &d.total, &d.expires, &d.status)
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
				return audit(ctx, tx, tenant, "storefront.order_expired", d.reference, nil)
			}); err != nil {
				errs = append(errs, err)
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
		return audit(ctx, tx, tenant, "storefront.order_paid", d.reference, map[string]any{"total_kobo": d.total})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already moved on
	}
	if err != nil {
		return err
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
			return fmt.Errorf("%w: decision %q is accept, decline, complete or cancel", ErrDecision, decision)
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
		return map[string]any{"reference": reference, "url": ord.PaymentURL, "total": FormatNaira(ord.TotalKobo)}, nil
	}
	if ord.Status != "accepted" || ord.Payment != "after_accept" || ord.TotalKobo < 100 {
		return nil, fmt.Errorf("%w: %s needs no payment link while %s", ErrDecision, reference, ord.Status)
	}
	if o.Payments == nil {
		return nil, errors.New("storefront: no payment provider is configured")
	}
	url, ref, err := o.Payments.Create(ctx, tenant, connection, PaymentRequest{Reference: paymentKey(ord.ID), AmountKobo: ord.TotalKobo,
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
	return map[string]any{"reference": reference, "url": url, "total": FormatNaira(ord.TotalKobo)}, nil
}

// NotifyCustomer queues a message to the order's customer: an SMS, and an
// email when they gave one. The recipient comes from the order, never from
// the workflow, so a workflow can only ever write to its own customers.
func (o *Orders) NotifyCustomer(ctx context.Context, tenant uuid.UUID, reference, text, subject, key string) (map[string]any, error) {
	if o.Messages == nil {
		return nil, errors.New("storefront: messages to customers are not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("%w: no text", sender.ErrInvalid)
	}
	var channels []string
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
		if ord.Customer.Phone != "" {
			queued, err := o.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeConfirmation,
				To: ord.Customer.Phone, SenderName: c.Name, Text: text, Key: key + ":sms"})
			if err != nil {
				return err
			}
			if queued {
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
	return map[string]any{"reference": reference, "queued": channels}, err
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'storefront', $2, $3, $4)`, tenant, action, target, raw)
	return err
}
