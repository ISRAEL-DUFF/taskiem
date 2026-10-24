package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Payments counted towards orders (spec invoices-and-money-log 8.1 to 8.3;
// docs/storefronts.md#payments-and-matching; docs/matching.md). An order
// is paid along one path, whoever hears of the money first: the order's own
// payment check (CheckPayments), or matching (engine/matching) when the
// money log has the payment, exactly by the checkout's reference or as a
// suggestion a person confirmed. Both take the money log entry's lock and
// the order's row lock, and count the entry once; the order moves to paid
// once, and its paid flow (the run started, or the waiting run woken) runs
// once, after the transaction that moved it commits (Orders.Paid).

// PaymentIn is money counted towards an order. EntryID is the money log
// entry it counts; nil only when the order's payment check finds the
// order paid and there is no money log to record it in.
type PaymentIn struct {
	EntryID    *uuid.UUID
	AmountKobo int64
	How        string // exact, suggested, suggested_auto or manual
	By         string
	Reasons    []string
}

// Counted is what counting a payment towards an order did. Pass it to
// Paid once the transaction commits.
type Counted struct {
	Order Order
	// PaymentID is the storefront_order_payments row written (uuid.Nil
	// when the entry already counted towards this order, or none was given).
	PaymentID uuid.UUID
	// Became is true when this payment moved the order to paid.
	Became bool
	// wake: the paid flow starts the order's run (start) or wakes the run
	// waiting for its payment (!start).
	wake, start bool
}

// OrderPayment is a payment counted towards an order, or the undoing of
// one (Undoes set).
type OrderPayment struct {
	ID         uuid.UUID  `json:"id"`
	EntryID    uuid.UUID  `json:"entry_id"`
	AmountKobo int64      `json:"amount_kobo"`
	How        string     `json:"how"`
	By         string     `json:"by"`
	Reasons    []string   `json:"reasons"`
	Undoes     *uuid.UUID `json:"undoes,omitempty"`
	UndoneBy   *uuid.UUID `json:"undone_by,omitempty"`
	// Undoable is false for a payment whose order went on to be fulfilled.
	Undoable  bool      `json:"undoable"`
	CreatedAt time.Time `json:"created_at"`
}

// EntryLock is the lock one money log entry is counted under, shared with
// invoicing.RecordPayment and the database's check of an entry's parts
// (moneylog.EntryLockSQL).
func EntryLock(ctx context.Context, tx pgx.Tx, entry uuid.UUID) error {
	_, err := tx.Exec(ctx, moneylog.EntryLockSQL, entry)
	return err
}

// CountedTowards names what a money log entry counts towards now
// ("invoice INV-0104", "invoice INV-0104 and order AK-7QX4KD" for a split),
// or "" for nothing. Call it under EntryLock.
func CountedTowards(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID) (string, error) {
	return moneylog.CountedTowards(ctx, tx, tenant, entry)
}

// Matchable is whether an order waits for money: an order or booking with
// something to pay, not paid, that waits for its checkout payment
// (awaiting_payment), or was taken without online payment (payment none)
// and is not cancelled, expired or decided against. An order paid after
// acceptance waits for its payment link to be made first: its workflow
// sends the link and waits for it.
func (x Order) Matchable() bool {
	return x.PaidAt == nil && (x.Kind == "order" || x.Kind == "booking") && x.Payable() >= 100 &&
		(x.Status == "awaiting_payment" || x.Payment == "none" && (x.Status == "received" || x.Status == "accepted"))
}

// matchableSQL is Matchable in SQL.
const matchableSQL = `paid_at IS NULL AND kind IN ('order', 'booking') AND COALESCE(deposit_kobo, total_kobo) >= 100
	AND (status = 'awaiting_payment' OR payment = 'none' AND status IN ('received', 'accepted'))`

// CountPayment counts a payment towards the order with this reference, in
// the caller's transaction, and moves the order to paid when it is not
// yet: an order paid at once (or not yet accepted) is received and its run
// starts; one accepted is paid and its waiting run is woken. The same
// entry again is a no-op. Refused (ErrDecision) for an entry counted
// elsewhere, an order paid by another payment, cancelled, expired or
// decided against, or less money than is due.
func (o *Orders) CountPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string, p PaymentIn) (Counted, error) {
	switch p.How {
	case "exact", "suggested", "suggested_auto", "manual":
	default:
		return Counted{}, fmt.Errorf("%w: how is exact, suggested, suggested_auto or manual", ErrInvalid)
	}
	if p.AmountKobo <= 0 {
		return Counted{}, fmt.Errorf("%w: a payment needs an amount", ErrInvalid)
	}
	if p.EntryID != nil {
		if err := EntryLock(ctx, tx, *p.EntryID); err != nil {
			return Counted{}, err
		}
	}
	ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
	if err != nil {
		return Counted{}, err
	}
	c := Counted{Order: ord}
	if p.EntryID != nil {
		var here bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.order_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL
			AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id))`, ord.ID, *p.EntryID).Scan(&here); err != nil {
			return c, err
		}
		if here {
			return c, o.open(ctx, tx, tenant, &c.Order)
		}
		// One entry may count towards several invoices and orders (a
		// split), its parts never more than it counts for (the database
		// checks it too).
		why, err := moneylog.EntryHas(ctx, tx, tenant, *p.EntryID, p.AmountKobo)
		if err != nil {
			return c, err
		}
		if why != "" {
			return c, fmt.Errorf("%w: %s", ErrDecision, why)
		}
		var paidBy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.order_id = $1 AND p.undoes IS NULL
			AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id))`, ord.ID).Scan(&paidBy); err != nil {
			return c, err
		}
		if paidBy {
			return c, fmt.Errorf("%w: %s is already paid by another payment", ErrDecision, ord.Reference)
		}
	}
	if ord.Kind != "order" && ord.Kind != "booking" {
		return c, fmt.Errorf("%w: %s takes no payment", ErrDecision, ord.Reference)
	}
	if p.AmountKobo < ord.Payable() {
		return c, fmt.Errorf("%w: %s is less than the %s due on %s", ErrDecision, FormatNaira(p.AmountKobo), FormatNaira(ord.Payable()), ord.Reference)
	}
	var from *string
	if ord.PaidAt == nil {
		if !ord.Matchable() {
			return c, fmt.Errorf("%w: %s is not waiting for a payment (%s)", ErrDecision, ord.Reference, strings.ReplaceAll(ord.Status, "_", " "))
		}
		decided := ord.DecidedAt != nil
		next := ord.Status
		switch {
		case ord.Status == "awaiting_payment" && !decided:
			next = "received"
		case ord.Status == "awaiting_payment", ord.Status == "accepted":
			next = "paid"
		}
		// As the payment check always did, for an order waiting for its
		// checkout payment: one not yet decided starts its run; one
		// accepted wakes the run waiting for its payment, but a booking's
		// run carries on to the appointment. An order taken without online
		// payment has its run already, waiting for nothing.
		switch {
		case ord.Status != "awaiting_payment":
		case !decided:
			c.wake, c.start = true, true
		case ord.Kind != "booking":
			c.wake = true
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, paid_at = now(), expires_at = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, next); err != nil {
			return c, err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = NULL WHERE reference = $1`, ord.Reference); err != nil {
			return c, err
		}
		if err := audit(ctx, tx, tenant, "storefront.order_paid", ord.Reference, map[string]any{"total_kobo": ord.Payable(), "how": p.How, "by": p.By}); err != nil {
			return c, err
		}
		from, c.Became = &ord.Status, true
	}
	if p.EntryID != nil {
		reasons := p.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		raw, _ := json.Marshal(reasons)
		c.PaymentID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_order_payments (id, tenant_id, order_id, entry_id, amount_kobo, how, by, reasons, from_status, woke_run)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, c.PaymentID, tenant, ord.ID, *p.EntryID, p.AmountKobo, p.How, p.By, raw, from, c.wake); err != nil {
			return c, err
		}
	}
	c.Order, err = o.GetOrder(ctx, tx, tenant, ord.Reference)
	return c, err
}

// Paid runs an order's paid flow after the transaction that counted its
// payment commits: its run starts, or the run waiting for its payment is
// woken. Nothing when the payment did not move the order to paid.
func (o *Orders) Paid(ctx context.Context, tenant uuid.UUID, c Counted) error {
	if !c.Became || !c.wake {
		return nil
	}
	if c.start {
		return o.startRun(ctx, tenant, c.Order.Reference)
	}
	_, _, err := o.Signal(ctx, tenant, "prod", PaymentSignal, c.Order.Reference, "paid:"+c.Order.Reference,
		map[string]any{"reference": c.Order.Reference, "status": "paid"})
	return err
}

// ForCheckout names the order whose checkout page has this payment
// reference on channel ("paystack/main"); ErrNoOrder when none has.
func ForCheckout(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, ref string) (string, error) {
	if ref == "" {
		return "", ErrNoOrder
	}
	var reference string
	err := tx.QueryRow(ctx, `SELECT o.reference FROM storefront_orders o
		LEFT JOIN storefront_actions a ON a.storefront_id = o.storefront_id AND a.kind = o.kind
		WHERE o.tenant_id = $1 AND o.payment_ref = $2 AND 'paystack/' || COALESCE(NULLIF(a.options->>'connection', ''), 'main') = $3`, tenant, ref, channel).Scan(&reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoOrder
	}
	return reference, err
}

// checkoutChannel is the money log channel of a form's payments.
func checkoutChannel(connection string) string {
	if connection == "" {
		connection = "main"
	}
	return "paystack/" + connection
}

// Matchables lists the tenant's orders that can still be paid, newest
// first, with the customer opened: what matching suggests among.
func (o *Orders) Matchables(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := tx.Query(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND `+matchableSQL+`
		ORDER BY created_at DESC LIMIT $2`, tenant, limit)
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
	if list == nil {
		list = []Order{}
	}
	return list, nil
}

// PaymentsOf lists the payments counted towards an order and their
// undoings, oldest first.
func PaymentsOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, order uuid.UUID) ([]OrderPayment, error) {
	// Undoable as UndoPayment decides it.
	rows, err := tx.Query(ctx, `SELECT p.id, p.entry_id, p.amount_kobo, p.how, p.by, p.reasons, p.undoes, u.id, p.created_at,
			p.undoes IS NULL AND NOT p.woke_run AND (p.from_status IS NULL OR p.from_status <> 'awaiting_payment'
				AND o.status = CASE p.from_status WHEN 'accepted' THEN 'paid' ELSE p.from_status END)
		FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id LEFT JOIN storefront_order_payments u ON u.undoes = p.id
		WHERE p.tenant_id = $1 AND p.order_id = $2 ORDER BY p.created_at, p.id`, tenant, order)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderPayment, error) {
		var x OrderPayment
		err := r.Scan(&x.ID, &x.EntryID, &x.AmountKobo, &x.How, &x.By, &x.Reasons, &x.Undoes, &x.UndoneBy, &x.CreatedAt, &x.Undoable)
		if x.Reasons == nil {
			x.Reasons = []string{}
		}
		x.Undoable = x.Undoable && x.UndoneBy == nil
		return x, err
	})
	if out == nil {
		out = []OrderPayment{}
	}
	return out, err
}

// UndoPayment undoes a payment counted towards an order (a wrong match):
// its row stays, and a row naming it takes it out. When that payment moved
// the order to paid, the order goes back to unpaid (received, or accepted)
// — refused when the order went on: its run was started or woken by the
// payment, it was paid through its checkout page, or it has been decided
// since (completed, declined, refund due). It returns the order and the
// entry the payment counted, to be matched again.
func (o *Orders) UndoPayment(ctx context.Context, tx pgx.Tx, tenant, payment uuid.UUID, by string) (Order, uuid.UUID, error) {
	var orderID, entry uuid.UUID
	var amount int64
	var from *string
	var woke bool
	var undoes *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT order_id, entry_id, amount_kobo, from_status, woke_run, undoes FROM storefront_order_payments WHERE tenant_id = $1 AND id = $2`,
		tenant, payment).Scan(&orderID, &entry, &amount, &from, &woke, &undoes)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && undoes != nil {
		return Order{}, uuid.Nil, ErrNoOrder
	}
	if err != nil {
		return Order{}, uuid.Nil, err
	}
	if err := EntryLock(ctx, tx, entry); err != nil {
		return Order{}, uuid.Nil, err
	}
	ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, orderID))
	if err != nil {
		return Order{}, uuid.Nil, err
	}
	var undone bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_order_payments WHERE undoes = $1)`, payment).Scan(&undone); err != nil {
		return Order{}, uuid.Nil, err
	}
	if undone {
		ord, err = o.GetOrder(ctx, tx, tenant, ord.Reference)
		return ord, entry, err
	}
	if from != nil {
		want := *from
		if want == "accepted" {
			want = "paid"
		}
		switch {
		case woke:
			return ord, entry, fmt.Errorf("%w: %s went on with this payment (its workflow was started or told it was paid); cancel or refund the order instead", ErrDecision, ord.Reference)
		case *from == "awaiting_payment":
			return ord, entry, fmt.Errorf("%w: %s was waiting for its checkout payment, which this payment completed; cancel or refund the order instead", ErrDecision, ord.Reference)
		case ord.Status != want:
			return ord, entry, fmt.Errorf("%w: %s has moved on (now %s), so it cannot go back to unpaid", ErrDecision, ord.Reference, strings.ReplaceAll(ord.Status, "_", " "))
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, paid_at = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, *from); err != nil {
			return ord, entry, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO storefront_order_payments (id, tenant_id, order_id, entry_id, amount_kobo, how, by, undoes)
		VALUES ($1, $2, $3, $4, $5, 'manual', $6, $7)`, uuid.New(), tenant, ord.ID, entry, amount, by, payment)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		ord, err = o.GetOrder(ctx, tx, tenant, ord.Reference) // undone meanwhile
		return ord, entry, err
	}
	if err != nil {
		return ord, entry, err
	}
	if err := audit(ctx, tx, tenant, "storefront.order_payment_undone", ord.Reference, map[string]any{"amount_kobo": amount, "by": by, "reopened": from != nil}); err != nil {
		return ord, entry, err
	}
	ord, err = o.GetOrder(ctx, tx, tenant, ord.Reference)
	return ord, entry, err
}
