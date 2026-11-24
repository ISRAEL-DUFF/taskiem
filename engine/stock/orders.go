package stock

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// OrderLines lowers the stock for an accepted order: one movement per
// product counted by a stock item (its lines' quantities together), under
// keys of the order and the row, in one transaction. It answers the items
// there were not enough of. Products not counted move nothing.
func (s *Stock) OrderLines(ctx context.Context, tenant, order uuid.UUID, reference string, lines []storefront.Line) ([]string, error) {
	short := []string{}
	type row struct {
		id, name string
		qty      int64
	}
	var rows []*row
	byID := map[string]*row{}
	for _, l := range lines {
		if l.Quantity <= 0 || l.ID == "" {
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
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		short = short[:0]
		for _, r := range rows {
			d := -r.qty
			m, err := RecordTx(ctx, tx, tenant, Move{ItemID: r.id, Item: r.name, Reason: Order, Delta: &d, Reference: reference,
				Actor: "order " + reference, Key: "order:" + order.String() + ":" + r.id})
			if errors.Is(err, ErrNotCounted) {
				continue
			}
			if err != nil {
				return err
			}
			if m.Short > 0 {
				short = append(short, r.name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
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

// ReturnOrderTx puts back what an ended order took, in the transaction
// that ends it (storefront.Returner).
func (s *Stock) ReturnOrderTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, reference string) error {
	var ended bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = $1 AND o.id = $2 AND `+orderEndedSQL+`)`,
		tenant, order).Scan(&ended); err != nil || !ended {
		return err
	}
	prefix := "order:" + order.String() + ":"
	rows, err := tx.Query(ctx, `SELECT key, `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND reason = 'order'
		AND key LIKE $2 ORDER BY created_at, id`, tenant, likePrefix(prefix))
	if err != nil {
		return err
	}
	type taken struct {
		row string
		m   Movement
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (taken, error) {
		var t taken
		var key string
		var before, after, change int64
		err := r.Scan(&key, &t.m.ID, &t.m.ItemID, &t.m.Item, &t.m.Reason, &t.m.Delta, &t.m.SetTo, &before, &after, &change, &t.m.Short, &t.m.Note,
			&t.m.Reference, &t.m.Actor, &t.m.CreatedAt)
		t.row, t.m.Change = strings.TrimPrefix(key, prefix), &change
		return t, err
	})
	if err != nil {
		return err
	}
	for _, t := range list {
		if *t.m.Change >= 0 {
			continue
		}
		d := -*t.m.Change
		if _, err := RecordTx(ctx, tx, tenant, Move{ItemID: t.m.ItemID, Item: t.m.Item, Reason: Order, Delta: &d, Note: "Put back: the order did not go ahead",
			Reference: t.m.Reference, Actor: t.m.Actor, Key: "order-back:" + order.String() + ":" + t.row}); err != nil {
			return err
		}
	}
	return nil
}

func likePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}
