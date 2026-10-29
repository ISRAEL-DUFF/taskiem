package stock

import (
	"context"
	"fmt"

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
