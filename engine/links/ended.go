package links

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ending a link freezes the documents mirrored between the two businesses
// where they are (orders.go, bills.go): an order not yet delivered, a
// bill not yet paid. Nothing about them changes here. What changes is
// that both sides are told, in their conversation, at the moment of
// ending, which those were, so neither waits for an update that will not
// come.

// OpenDocs are the documents still open between two businesses when
// their link ends, by the references both sides know them by: orders by
// the buyer's purchase reference, invoices by the seller's number (the
// buyer's bill carries the same).
type OpenDocs struct {
	Orders   []string
	Invoices []string
}

// Any reports whether anything is open.
func (o OpenDocs) Any() bool { return len(o.Orders)+len(o.Invoices) > 0 }

// OpenDocsTx lists what is still open on a link, as the business in scope
// sees it: the orders it placed and the orders placed with it, the bills
// it owes and the invoices it is owed. other is the linked business.
func OpenDocsTx(ctx context.Context, tx pgx.Tx, tenant, link, other uuid.UUID) (OpenDocs, error) {
	var out OpenDocs
	list := func(q string, args ...any) ([]string, error) {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[string])
	}
	var err error
	if out.Orders, err = list(`SELECT reference FROM (
			SELECT p.reference, p.created_at FROM purchase_orders p WHERE p.tenant_id = $1 AND p.link_id = $2
			   AND p.status IN ('sent', 'confirmed', 'part_received')
			UNION
			SELECT lo.purchase_reference, o.created_at FROM link_orders lo JOIN storefront_orders o ON o.id = lo.order_id
			 WHERE lo.tenant_id = $1 AND lo.link_id = $2
			   AND o.status NOT IN ('completed', 'cancelled', 'declined', 'expired', 'refund_due', 'refunded')) x
		ORDER BY created_at, reference`, tenant, link); err != nil {
		return out, err
	}
	out.Invoices, err = list(`SELECT reference FROM (
			SELECT b.reference, b.created_at FROM bills b WHERE b.tenant_id = $1 AND b.link_id = $2 AND NOT b.voided AND b.paid_kobo < b.amount_kobo
			UNION
			SELECT i.number, i.created_at FROM customer_invoices i WHERE i.tenant_id = $1 AND i.customer_key = $3
			   AND i.status IN ('sent', 'part_paid', 'overdue')) x
		ORDER BY created_at, reference`, tenant, link, "link:"+other.String()) // the seller's own key for its linked customer
	return out, err
}

// TellEndedTx writes a line into both sides' conversations of a link the
// business in scope has just ended, in the transaction that ended it:
// mine into its own (a diary line), theirs into the other side's, through
// the one function that still reaches it (taskiem_link_ended_note, which
// answers only then).
func TellEndedTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, mine, theirs string, from Sender) error {
	if _, err := DiaryTx(ctx, tx, tenant, link, mine, from); err != nil {
		return err
	}
	var told bool
	return pgErr(tx.QueryRow(ctx, `SELECT taskiem_link_ended_note($1, $2)`, link, theirs).Scan(&told))
}
