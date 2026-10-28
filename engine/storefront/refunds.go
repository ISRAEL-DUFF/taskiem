package storefront

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Refunds (decision 0036, accepted 2026-10-20). An order or booking paid
// and then declined or cancelled is left refund_due, and its owner is
// asked to confirm the refund: in the default workflows' approval, in
// chat ("refund AK-7QX4KD") or in the web app. Taskiem never moves money
// unasked. Confirmed, the payment is refunded in full through the
// business's Paystack connection, from its Paystack balance; a refund that
// fails (too little balance) leaves the order refund_due with the reason.
// An order paid by bank transfer, or by money matched to it, has no card
// payment to refund: the owner sends it back and marks it refunded
// (MarkRefunded, transfers.go; N3).

// ErrRefund is a refund the provider refused; the order stays refund_due.
var ErrRefund = errors.New("storefront: the refund was refused")

// ErrRefundByHand is a refund Taskiem cannot make: the order was paid by
// bank transfer (or by money matched to it), so the owner sends it back
// from their bank and marks it refunded (MarkRefunded). The order stays
// refund_due, with a note saying so.
var ErrRefundByHand = errors.New("storefront: refund it by hand")

// Refund refunds a refund_due order in full and tells the customer. A
// second call on a refunded order reports it.
func (o *Orders) Refund(ctx context.Context, tenant uuid.UUID, reference, actor string) (Order, error) {
	var ord Order
	var connection string
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(options->>'connection', '') FROM storefront_actions WHERE storefront_id = $1 AND kind = $2`,
			ord.storefront, ord.Kind).Scan(&connection)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil // no form settings: the main connection
	}
	if err != nil {
		return ord, err
	}
	if ord.Status == "refunded" {
		return ord, nil
	}
	if ord.Status != "refund_due" {
		return ord, fmt.Errorf("%w: %s has no payment to refund (%s)", ErrDecision, reference, ord.Status)
	}
	name, card := paystackConnection(connection)
	if ord.PaidBy != "card" || !card || ord.paymentRef == "" {
		// Paid by bank transfer, or by money matched to it: Taskiem cannot
		// send it back (N3); the owner does, from their bank.
		return ord, o.askRefundByHand(ctx, tenant, ord)
	}
	if o.Payments == nil {
		return ord, errors.New("storefront: no payment provider is configured")
	}
	_, rerr := o.Payments.Refund(ctx, tenant, name, ord.paymentRef, "Refund for "+reference)
	if rerr != nil && !errors.Is(rerr, effects.ErrFatal) {
		return ord, rerr // tried again
	}
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if rerr != nil {
			note := "The refund could not be made: " + rerr.Error()
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, note); err != nil {
				return err
			}
			ord.Note = note
			return audit(ctx, tx, tenant, "storefront.refund_failed", reference, map[string]any{"by": actor})
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'refunded', refunded_at = now(), note = NULL, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND status = 'refund_due'`, tenant, ord.ID); err != nil {
			return err
		}
		ord.Status = "refunded"
		return audit(ctx, tx, tenant, "storefront.refunded", reference, map[string]any{"by": actor, "total_kobo": ord.Payable()})
	})
	if err != nil {
		return ord, err
	}
	if rerr != nil {
		return ord, fmt.Errorf("%w: %w", ErrRefund, rerr)
	}
	err = o.tell(ctx, tenant, reference, "Your refund of "+FormatNaira(ord.Payable())+" for "+reference+" has been started. It reaches you in about 5 to 10 working days.",
		"refund:"+reference)
	return ord, err
}
