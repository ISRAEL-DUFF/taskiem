package storefront

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Paying a refund back through Taskiem's own payments (paying people H7,
// Pay it back): the order a payment pays back is read, held, and marked
// refunded once the payment is paid, as Mark refunded does by hand.

// RefundOwedTx is what an order owes back to its customer, held for the
// transaction: ErrDecision when it is not waiting for a refund.
func RefundOwedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string) (int64, error) {
	ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNoOrder
	}
	if err != nil {
		return 0, err
	}
	if ord.Status != "refund_due" {
		return 0, fmt.Errorf("%w: %s is not waiting for a refund (%s)", ErrDecision, reference, strings.ReplaceAll(ord.Status, "_", " "))
	}
	return ord.RefundKobo(), nil
}

// RefundedByPaymentTx marks an order waiting for a refund refunded, by a
// payment paid back to the customer; false when it was not waiting (one
// marked by hand meanwhile).
func RefundedByPaymentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string, payment uuid.UUID, kobo int64) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'refunded', refunded_at = now(), updated_at = now()
		WHERE tenant_id = $1 AND reference = $2 AND status = 'refund_due'`, tenant, reference)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	return true, audit(ctx, tx, tenant, "storefront.refunded", reference, map[string]any{"by": "payment:" + payment.String(), "payment": payment, "total_kobo": kobo})
}

// TellRefunded tells the customer their payment was sent back, as Mark
// refunded does.
func (o *Orders) TellRefunded(ctx context.Context, tenant uuid.UUID, reference string, kobo int64) error {
	return o.tell(ctx, tenant, reference, "Your payment of "+FormatNaira(kobo)+" for "+reference+" has been sent back to you. Contact us if it has not reached you.",
		"refund:"+reference)
}
