package stock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Tracker tells whether a business's catalogue keeps stock
// (storefront.XenibaseStock).
type Tracker interface {
	Tracks(ctx context.Context, tenant uuid.UUID) (bool, error)
}

// tracks reports whether the business's catalogue keeps stock; a table
// that cannot tell is taken to.
func (s *Stock) tracks(ctx context.Context, tenant uuid.UUID) (bool, error) {
	if t, ok := s.Table.(Tracker); ok {
		return t.Tracks(ctx, tenant)
	}
	return true, nil
}

// OrderLines lowers the stock for an accepted order: one movement per
// catalogue row (its lines' quantities together), recorded under keys of
// the order and the row, then sent. It answers the items there were not
// enough of. A catalogue that keeps no stock records nothing.
func (s *Stock) OrderLines(ctx context.Context, tenant, order uuid.UUID, reference string, lines []storefront.Line) ([]string, error) {
	short := []string{}
	ok, err := s.tracks(ctx, tenant)
	if err != nil || !ok {
		return short, err
	}
	type row struct {
		id, name string
		qty      int64
	}
	var rows []*row
	byID := map[string]*row{}
	for _, l := range lines {
		if l.Quantity <= 0 {
			continue
		}
		r := byID[l.ID]
		if r == nil {
			r = &row{id: l.ID, name: l.Name}
			byID[l.ID] = r
			rows = append(rows, r)
		}
		r.qty += int64(l.Quantity)
	}
	var ids []uuid.UUID
	if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		ids = ids[:0]
		for _, r := range rows {
			d := -r.qty
			m, err := RecordTx(ctx, tx, tenant, Move{ItemID: r.id, Item: r.name, Reason: Order, Delta: &d, Reference: reference,
				Actor: "order " + reference, Key: "order:" + order.String() + ":" + r.id})
			if err != nil {
				return err
			}
			ids = append(ids, m.ID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for i, id := range ids {
		m, err := s.Apply(ctx, tenant, id)
		if err != nil {
			return nil, err
		}
		if m.Status == "pending" {
			return nil, fmt.Errorf("the stock of %s kept changing; try again", rows[i].name)
		}
		if m.Short > 0 {
			short = append(short, rows[i].name)
		}
	}
	return short, nil
}

// An order's stock put back. An accepted order that is then declined,
// cancelled, left refund due or not paid for in time still has its goods
// on the shelf, so each of its order movements is reversed by one more,
// keyed on the order and the row: recorded once however often the step is
// retried. An order whose rider delivered it keeps what it took.

// orderEndedSQL is true of a storefront order o that ended with its goods
// still on the shelf.
const orderEndedSQL = `o.status IN ('declined', 'cancelled', 'refund_due', 'refunded', 'expired')
	AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.tenant_id = o.tenant_id AND d.order_id = o.id AND d.status = 'delivered')`

func backKey(order uuid.UUID, item string) string {
	return "order-back:" + order.String() + ":" + item
}

// ReturnOrderTx puts back what an ended order took, in the transaction
// that ends it (storefront.Returner). A movement that landed is reversed
// by what landed. One never sent is void: it is not sent. One being sent
// is left, and is reversed when it lands (returnLandedTx). The reversals
// are sent by SendReturns, or the sweep.
func (s *Stock) ReturnOrderTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, reference string) error {
	var ended bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = $1 AND o.id = $2 AND `+orderEndedSQL+`)`,
		tenant, order).Scan(&ended); err != nil || !ended {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND reason = 'order'
		AND key LIKE $2 AND status <> 'void' ORDER BY created_at, id FOR UPDATE`, tenant, likePrefix("order:"+order.String()+":"))
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Movement, error) { return scanMovement(r) })
	if err != nil {
		return err
	}
	for _, m := range list {
		switch {
		case m.Status == "done":
			if err := backTx(ctx, tx, tenant, order, m); err != nil {
				return err
			}
		case m.Before == nil:
			if _, err := tx.Exec(ctx, `UPDATE stock_movements SET status = 'void', error = 'The order ended before its stock was taken'
				WHERE tenant_id = $1 AND id = $2 AND status = 'pending' AND count_before IS NULL`, tenant, m.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func likePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}

// backTx records the reversal of an order movement that landed.
func backTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, m Movement) error {
	if m.Change == nil || *m.Change >= 0 {
		return nil
	}
	d := -*m.Change
	_, err := RecordTx(ctx, tx, tenant, Move{ItemID: m.ItemID, Item: m.Item, Reason: Order, Delta: &d, Note: "Put back: the order did not go ahead",
		Reference: m.Reference, Actor: m.Actor, Key: backKey(order, m.ItemID)})
	return err
}

// returnLandedTx reverses an order movement that landed after its order
// had ended (it was on its way when the order was declined, or the order
// was declined before its stock was recorded).
func returnLandedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, m Movement) error {
	if m.Reason != Order || m.Change == nil || *m.Change >= 0 {
		return nil
	}
	var order uuid.UUID
	err := tx.QueryRow(ctx, `SELECT o.id FROM stock_movements m JOIN storefront_orders o ON o.tenant_id = m.tenant_id
			AND m.key = 'order:' || o.id::text || ':' || m.item_id
		WHERE m.tenant_id = $1 AND m.id = $2 AND `+orderEndedSQL, tenant, m.ID).Scan(&order)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return backTx(ctx, tx, tenant, order, m)
}

// SendReturns sends an ended order's reversals to the table now; what
// cannot be sent is the sweep's.
func (s *Stock) SendReturns(ctx context.Context, tenant, order uuid.UUID) error {
	var ids []uuid.UUID
	if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM stock_movements WHERE tenant_id = $1 AND status = 'pending' AND key LIKE $2 ORDER BY created_at, id`,
			tenant, likePrefix("order-back:"+order.String()+":"))
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if _, err := s.Apply(ctx, tenant, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}
