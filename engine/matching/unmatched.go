package matching

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Payment is money received that no invoice counts and no suggestion
// waits on: what a person matches by hand.
type Payment struct {
	EntryID    uuid.UUID `json:"entry_id"`
	AmountKobo *int64    `json:"amount_kobo"`
	Channel    string    `json:"channel"`
	Reference  string    `json:"reference"`
	When       time.Time `json:"received_at"`
	Kind       string    `json:"kind"`
}

// Unmatched lists the money-in entries matching looked at and left for a
// person (no open suggestion, none counted towards an invoice or order),
// newest first.
func Unmatched(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Payment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT e.id, e.amount_kobo, e.channel, e.reference, COALESCE(e.occurred_at, e.recorded_at), e.kind
		FROM money_match_state s JOIN money_entries e ON e.id = s.entry_id
		WHERE s.tenant_id = $1 AND s.state = 'unmatched'
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL
		                    AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id))
		  AND NOT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL
		                    AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id))
		ORDER BY e.recorded_at DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
		var p Payment
		return p, r.Scan(&p.EntryID, &p.AmountKobo, &p.Channel, &p.Reference, &p.When, &p.Kind)
	})
	if out == nil {
		out = []Payment{}
	}
	return out, err
}

// OpenOrder is a storefront order money can still be matched to by hand.
type OpenOrder struct {
	TargetKind string `json:"target_kind"`
	Target     string `json:"target"`
	Number     string `json:"number"`
	// DueKobo is what is paid online: the deposit, or the total.
	DueKobo int64 `json:"due_kobo"`
	// Customer is the customer's name (masked by the API unless revealed).
	Customer string    `json:"customer"`
	PlacedAt time.Time `json:"placed_at"`
}

// OpenOrders lists the orders money can still be matched to (not paid,
// cancelled, expired or decided against), newest first; none without
// storefront orders.
func (m *Matcher) OpenOrders(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]OpenOrder, error) {
	out := []OpenOrder{}
	if m.Orders == nil {
		return out, nil
	}
	list, err := m.Orders.Matchables(ctx, tx, tenant, limit)
	for _, x := range list {
		out = append(out, OpenOrder{TargetKind: TargetOrder, Target: OrderTarget(x.Reference), Number: x.Reference, DueKobo: x.Payable(),
			Customer: x.Customer.Name, PlacedAt: x.CreatedAt})
	}
	return out, err
}
