package stock

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Catalogue reads a business's catalogue table and its mapping
// (storefront.XenibaseStock), for workflows (stock@1), which run outside
// the API.
type Catalogue interface {
	Rows(ctx context.Context, tenant uuid.UUID) ([]map[string]any, storefront.Columns, error)
}

// ErrNoItem: no item has that id.
var ErrNoItem = errors.New("stock: no such item")

// Items reads the business's items from the catalogue table itself;
// ErrNotTracked when its catalogue keeps no stock.
func (s *Stock) Items(ctx context.Context, tenant uuid.UUID) ([]Item, error) {
	c, ok := s.Table.(Catalogue)
	if !ok {
		return nil, ErrNotTracked
	}
	rows, m, err := c.Rows(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if m.Stock == "" {
		return nil, ErrNotTracked
	}
	return Items(rows, m), nil
}

// Item reads one item by its row id.
func (s *Stock) Item(ctx context.Context, tenant uuid.UUID, id string) (Item, error) {
	items, err := s.Items(ctx, tenant)
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return Item{}, ErrNoItem
}

// Move records a movement with an audit entry by the system (a workflow,
// named in its actor), then sends it; an error sending leaves it for the
// sweep.
func (s *Stock) Move(ctx context.Context, tenant uuid.UUID, mv Move) (Movement, error) {
	var m Movement
	if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if m, err = RecordTx(ctx, tx, tenant, mv); err != nil {
			return err
		}
		detail := map[string]any{"item_id": mv.ItemID, "reason": mv.Reason, "movement_id": m.ID, "by": mv.Actor}
		raw, _ := json.Marshal(detail)
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'stock', 'stock.move', $2, $3)`, tenant, mv.Item, raw)
		return err
	}); err != nil {
		return m, err
	}
	// Recorded: a send that fails is sent again by the sweep.
	if out, err := s.Apply(ctx, tenant, m.ID); err == nil && out.ID != uuid.Nil {
		return out, nil
	}
	return m, nil
}

// Movements lists a business's movements, the latest first.
func (s *Stock) Movements(ctx context.Context, tenant uuid.UUID, f Filter) ([]Movement, error) {
	var out []Movement
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = ListTx(ctx, tx, tenant, f)
		return err
	})
	return out, err
}
