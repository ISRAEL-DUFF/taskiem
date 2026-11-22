package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Catalogue reads a business's catalogue table and its mapping
// (storefront.XenibaseCatalogue), for workflows (stock@1), which run
// outside the API. No catalogue reads no rows.
type Catalogue interface {
	Rows(ctx context.Context, tenant uuid.UUID) ([]map[string]any, storefront.Columns, error)
	Row(ctx context.Context, tenant uuid.UUID, id string) ([]map[string]any, storefront.Columns, error)
}

// ErrNoItem: no item has that id.
var ErrNoItem = errors.New("stock: no such item")

// Items reads the business's products, each with the stock item it is
// counted by, and its materials; ErrNotTracked when it has neither a
// catalogue nor a stock item.
func (s *Stock) Items(ctx context.Context, tenant uuid.UUID) ([]Item, error) {
	var rows []map[string]any
	var m storefront.Columns
	if s.Catalogue != nil {
		var err error
		if rows, m, err = s.Catalogue.Rows(ctx, tenant); err != nil {
			return nil, err
		}
	}
	var out []Item
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = ItemsTx(ctx, tx, tenant, rows, m)
		return err
	})
	if err == nil && len(out) == 0 && m.Name == "" {
		return nil, ErrNotTracked
	}
	return out, err
}

// Item reads one item by its id: a product's row id, or a material's
// stock item id.
func (s *Stock) Item(ctx context.Context, tenant uuid.UUID, id string) (Item, error) {
	if sid, err := uuid.Parse(id); err == nil {
		var x StockItem
		var counts bool
		if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
			var err error
			if x, err = StockItemTx(ctx, tx, tenant, sid); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_stock WHERE tenant_id = $1 AND stock_item_id = $2)`, tenant, sid).Scan(&counts)
		}); err != nil {
			return Item{}, err
		}
		if !counts {
			return FromStockItem(x), nil
		}
	}
	var rows []map[string]any
	var m storefront.Columns
	if s.Catalogue != nil {
		var err error
		if rows, m, err = s.Catalogue.Row(ctx, tenant, id); err != nil {
			return Item{}, err
		}
	}
	var items []Item
	if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		items, err = ItemsTx(ctx, tx, tenant, rows, m)
		return err
	}); err != nil {
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
// named in its actor).
func (s *Stock) Move(ctx context.Context, tenant uuid.UUID, mv Move) (Movement, error) {
	var m Movement
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if m, err = RecordTx(ctx, tx, tenant, mv); err != nil {
			return err
		}
		detail := map[string]any{"item_id": mv.ItemID, "reason": mv.Reason, "movement_id": m.ID, "by": mv.Actor}
		raw, _ := json.Marshal(detail)
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'stock', 'stock.move', $2, $3)`, tenant, mv.Item, raw)
		return err
	})
	return m, err
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

// SellLine is a line of a sale a workflow records: an item by its row id,
// how many, and its price (the item's when nil).
type SellLine struct {
	ItemID    string
	Quantity  int64
	PriceKobo *int64
}

// Sell records a sale for a workflow (stock@1 record_sale): the items'
// names, units and prices from the catalogue, then RecordSale.
func (s *Stock) Sell(ctx context.Context, tenant uuid.UUID, lines []SellLine, amount *int64, paidHow, note, actor, key string) (CounterSale, error) {
	if s.MoneyLog == nil {
		return CounterSale{}, errors.New("stock: sales are not available here")
	}
	items, err := s.Items(ctx, tenant)
	if err != nil {
		return CounterSale{}, err
	}
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	in := SaleIn{AmountKobo: amount, PaidHow: paidHow, Note: note, Actor: actor, Key: key}
	for _, l := range lines {
		it, ok := byID[l.ItemID]
		if !ok || it.Material {
			return CounterSale{}, ErrNoItem
		}
		price := int64(0)
		switch {
		case l.PriceKobo != nil:
			price = *l.PriceKobo
		case it.PriceKobo != nil:
			price = *it.PriceKobo
		case amount == nil:
			return CounterSale{}, fmt.Errorf("%w: %s has no price; give the amount", ErrSaleInvalid, it.Name)
		}
		in.Lines = append(in.Lines, SaleLine{ItemID: it.ID, Item: it.Name, Quantity: l.Quantity, Unit: it.SaleUnit(), PriceKobo: price, CostKobo: it.CostKobo})
	}
	return s.RecordSale(ctx, tenant, in, s.MoneyLog)
}

// SalesDay adds up a Lagos day's sales (DayTx), for workflows.
func (s *Stock) SalesDay(ctx context.Context, tenant uuid.UUID, day time.Time) (Day, error) {
	var d Day
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		d, err = DayTx(ctx, tx, tenant, day)
		return err
	})
	return d, err
}
