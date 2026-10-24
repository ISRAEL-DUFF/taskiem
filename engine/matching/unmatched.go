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
	// LeftKobo is what is left of it to match: less than the amount when
	// part of it counts towards an invoice or order already (a split, or a
	// part undone).
	LeftKobo  *int64    `json:"left_kobo"`
	Channel   string    `json:"channel"`
	Reference string    `json:"reference"`
	When      time.Time `json:"received_at"`
	Kind      string    `json:"kind"`
	// HeldReason says why matching held it for a person, when it did: a
	// credit to a customer's account whose account number or reference
	// disagrees with what Taskiem registered.
	HeldReason string `json:"held_reason,omitempty"`
}

// Unmatched lists the money-in entries matching looked at and left for a
// person (no open suggestion, none counted towards an invoice or order),
// newest first.
func Unmatched(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Payment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Left for a person: what is not counted yet of money matching did not
	// match, held, or a person matched only part of; never a customer's
	// credit (money_entry_customers), which counts towards their invoices.
	rows, err := tx.Query(ctx, `SELECT e.id, e.amount_kobo, taskiem_money_entry_left(e.id), e.channel, e.reference, COALESCE(e.occurred_at, e.recorded_at), e.kind,
			COALESCE(s.held_reason, '')
		FROM money_match_state s JOIN money_entries e ON e.id = s.entry_id
		WHERE s.tenant_id = $1 AND s.state = 'unmatched'
		  AND (taskiem_money_entry_left(e.id) > 0 OR e.amount_kobo IS NULL AND taskiem_money_entry_counted(e.id) = 0)
		  AND NOT EXISTS (SELECT 1 FROM money_entry_customers c WHERE c.entry_id = e.id)
		ORDER BY e.recorded_at DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
		var p Payment
		return p, r.Scan(&p.EntryID, &p.AmountKobo, &p.LeftKobo, &p.Channel, &p.Reference, &p.When, &p.Kind, &p.HeldReason)
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
