package storefront

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	// Add is a further part of the entry towards an order it already
	// counts towards (moneylog.Counting.Add): the order's own rule then
	// decides (one payment, so it is refused while that one stands).
	Add bool
	// Account is the one-time account number given for the order that
	// the money was paid into (N3): marked paid with the payment, which
	// also says the order was paid by bank transfer (refunds by hand).
	Account *uuid.UUID
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
// starts; one accepted is paid and its waiting run is woken. A payment
// with a money log entry is a part of it, counted along the path invoices
// take too (moneylog.Parts.Count). The same entry again is a no-op
// (unless Add, when the order's one-payment rule refuses it).
// Refused (ErrDecision) for an entry counted elsewhere, an order paid by
// another payment, cancelled, expired or decided against, or less money
// than is due.
func (o *Orders) CountPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string, p PaymentIn) (Counted, error) {
	switch p.How {
	case "exact", "suggested", "suggested_auto", "manual":
	default:
		return Counted{}, fmt.Errorf("%w: how is exact, suggested, suggested_auto or manual", ErrInvalid)
	}
	if p.AmountKobo <= 0 {
		return Counted{}, fmt.Errorf("%w: a payment needs an amount", ErrInvalid)
	}
	var c Counted
	k, err := moneylog.OrderParts.Count(ctx, tx, moneylog.Counting{Tenant: tenant, Entry: p.EntryID, AmountKobo: p.AmountKobo, How: p.How, By: p.By, Reasons: p.Reasons, Add: p.Add,
		Open: func() (uuid.UUID, error) {
			ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
			c.Order = ord
			return ord.ID, err
		},
		Refused: func(why string) error { return fmt.Errorf("%w: %s", ErrDecision, why) },
		Decide: func() (moneylog.Cols, bool, error) {
			from, err := o.payOrder(ctx, tx, tenant, &c, p)
			return moneylog.Cols{"from_status": from, "woke_run": c.wake}, err == nil && p.EntryID != nil, err
		}})
	if err != nil {
		return c, err
	}
	if p.Account != nil {
		if _, err := tx.Exec(ctx, `UPDATE customer_invoice_accounts SET status = 'paid', updated_at = now() WHERE tenant_id = $1 AND id = $2 AND order_id = $3`,
			tenant, *p.Account, c.Order.ID); err != nil {
			return c, err
		}
	}
	if k.Already {
		return c, o.open(ctx, tx, tenant, &c.Order)
	}
	c.PaymentID = k.ID
	c.Order, err = o.GetOrder(ctx, tx, tenant, c.Order.Reference)
	return c, err
}

// payOrder applies an order's own rules to a payment the entry has room
// for, and moves the order to paid when it is not yet; from is the status
// it moved from (nil when it was paid already).
func (o *Orders) payOrder(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c *Counted, p PaymentIn) (*string, error) {
	ord := c.Order
	if p.EntryID != nil {
		paidBy, err := moneylog.OrderParts.Paid(ctx, tx, ord.ID)
		if err != nil {
			return nil, err
		}
		if paidBy {
			var same bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.order_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL
				AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id))`, ord.ID, *p.EntryID).Scan(&same); err != nil {
				return nil, err
			}
			if same {
				return nil, fmt.Errorf("%w: %s is already paid by this payment; an order takes one payment", ErrDecision, ord.Reference)
			}
			return nil, fmt.Errorf("%w: %s is already paid by another payment", ErrDecision, ord.Reference)
		}
	}
	if ord.Kind != "order" && ord.Kind != "booking" {
		return nil, fmt.Errorf("%w: %s takes no payment", ErrDecision, ord.Reference)
	}
	if p.AmountKobo < ord.Payable() {
		return nil, fmt.Errorf("%w: %s is less than the %s due on %s", ErrDecision, FormatNaira(p.AmountKobo), FormatNaira(ord.Payable()), ord.Reference)
	}
	if ord.PaidAt != nil {
		return nil, nil
	}
	if !ord.Matchable() {
		return nil, fmt.Errorf("%w: %s is not waiting for a payment (%s)", ErrDecision, ord.Reference, strings.ReplaceAll(ord.Status, "_", " "))
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
	// checkout payment: one not yet decided starts its run; one accepted
	// wakes the run waiting for its payment, but a booking's run carries on
	// to the appointment. An order taken without online payment has its
	// run already, waiting for nothing.
	switch {
	case ord.Status != "awaiting_payment":
	case !decided:
		c.wake, c.start = true, true
	case ord.Kind != "booking":
		c.wake = true
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, paid_at = now(), expires_at = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, ord.ID, next); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = NULL WHERE reference = $1`, ord.Reference); err != nil {
		return nil, err
	}
	if err := audit(ctx, tx, tenant, "storefront.order_paid", ord.Reference, map[string]any{"total_kobo": ord.Payable(), "how": p.How, "by": p.By}); err != nil {
		return nil, err
	}
	c.Became = true
	return &ord.Status, nil
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
		WHERE o.tenant_id = $1 AND o.payment_ref = $2 AND `+channelSQL+` = $3`, tenant, ref, channel).Scan(&reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoOrder
	}
	return reference, err
}

// channelSQL is checkoutChannel in SQL, over a form's options (a).
const channelSQL = `CASE WHEN COALESCE(a.options->>'connection', '') LIKE '%/%' THEN a.options->>'connection'
	ELSE 'paystack/' || COALESCE(NULLIF(a.options->>'connection', ''), 'main') END`

// checkoutChannel is the money log channel of a form's payments: its
// connection as connector/name; a bare name is Paystack's, as forms named
// it before any other rail could take their payments.
func checkoutChannel(connection string) string {
	if strings.Contains(connection, "/") {
		return connection
	}
	if connection == "" {
		connection = "main"
	}
	return "paystack/" + connection
}

// paystackConnection is the Paystack connection a form's card payments
// use, and false when the form's connection is another rail's, which
// takes payment by bank transfer only.
func paystackConnection(connection string) (string, bool) {
	c, name, ok := strings.Cut(checkoutChannel(connection), "/")
	return name, ok && c == "paystack"
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
// its row stays, and a row naming it takes it out (moneylog.Parts.Undo).
// When that payment moved the order to paid, the order goes back to unpaid
// (received, or accepted) — refused when the order went on: its run was
// started or woken by the payment, it was paid through its checkout page,
// or it has been decided since (completed, declined, refund due). It
// returns the order and the entry the payment counted, to be matched
// again.
func (o *Orders) UndoPayment(ctx context.Context, tx pgx.Tx, tenant, payment uuid.UUID, by string) (Order, uuid.UUID, error) {
	var ord Order
	var from *string
	part, undone, err := moneylog.OrderParts.Undo(ctx, tx, moneylog.Undoing{Tenant: tenant, Part: payment, By: by,
		Open: func(p moneylog.Part) error {
			var err error
			ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, p.Target))
			return err
		},
		Decide: func(p moneylog.Part) (moneylog.Cols, error) {
			var woke bool
			if err := tx.QueryRow(ctx, `SELECT from_status, woke_run FROM storefront_order_payments WHERE id = $1`, p.ID).Scan(&from, &woke); err != nil {
				return nil, err
			}
			if from != nil {
				want := *from
				if want == "accepted" {
					want = "paid"
				}
				switch {
				case woke:
					return nil, fmt.Errorf("%w: %s went on with this payment (its workflow was started or told it was paid); cancel or refund the order instead", ErrDecision, ord.Reference)
				case *from == "awaiting_payment":
					return nil, fmt.Errorf("%w: %s was waiting for its checkout payment, which this payment completed; cancel or refund the order instead", ErrDecision, ord.Reference)
				case ord.Status != want:
					return nil, fmt.Errorf("%w: %s has moved on (now %s), so it cannot go back to unpaid", ErrDecision, ord.Reference, strings.ReplaceAll(ord.Status, "_", " "))
				}
				if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, paid_at = NULL, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
					tenant, ord.ID, *from); err != nil {
					return nil, err
				}
			}
			return moneylog.Cols{"entry_id": p.Entry}, nil
		}})
	var entry uuid.UUID
	if part.Entry != nil {
		entry = *part.Entry
	}
	switch {
	case errors.Is(err, moneylog.ErrNoPart):
		return Order{}, uuid.Nil, ErrNoOrder
	case err != nil && ord.ID == uuid.Nil:
		return Order{}, uuid.Nil, err
	case err != nil:
		return ord, entry, err
	}
	if undone {
		if err := audit(ctx, tx, tenant, "storefront.order_payment_undone", ord.Reference, map[string]any{"amount_kobo": part.AmountKobo, "by": by, "reopened": from != nil}); err != nil {
			return ord, entry, err
		}
	}
	ord, err = o.GetOrder(ctx, tx, tenant, ord.Reference)
	return ord, entry, err
}
