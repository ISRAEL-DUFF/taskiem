// Package productstock is a product counted by a stock item
// (docs/spec/what-a-business-buys.md §4, decision 0070): a catalogue row
// of the owner's table, linked in product_stock to the stock item whose
// count selling it lowers, with how many of it one sale uses.
//
// It is the one path from a product to its count. In S1a every caller that
// sells, orders, receives or shows a count goes through it: the storefront
// copy and the live price check (CountsTx), and engine/stock's movements
// (ItemOfTx), which lower a product's stock item by the quantity sold. S1b
// replaces what it must here (uses above 1, taskiem_product_left), and the
// callers stay as they are.
package productstock

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Counted is a product's stock item as selling and the pages see it.
type Counted struct {
	StockItemID uuid.UUID
	// Uses is how many of the stock item one sale of the product takes.
	Uses  int64
	Count int64
	Unit  string
	// Retired: a size or colour taken off sale (its stock item kept for
	// its history).
	Retired bool
}

// Left is how many whole sales the stock item covers, never below 0.
func (c Counted) Left() int64 {
	if c.Count <= 0 || c.Uses <= 0 {
		return 0
	}
	return c.Count / c.Uses
}

// CountsTx is each counted product's stock item, by product id; products
// not counted are not in it. nil products reads every link.
func CountsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, products []string) (map[string]Counted, error) {
	out := map[string]Counted{}
	if products != nil && len(products) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT p.product_id, i.id, p.uses, i.count, i.unit, i.retired_at IS NOT NULL
		FROM product_stock p JOIN stock_items i ON i.tenant_id = p.tenant_id AND i.id = p.stock_item_id
		WHERE p.tenant_id = $1 AND ($2::text[] IS NULL OR p.product_id = ANY ($2))`, tenant, products)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c Counted
		if err := rows.Scan(&id, &c.StockItemID, &c.Uses, &c.Count, &c.Unit, &c.Retired); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ItemOfTx is the stock item a reference names: a stock item's own id (a
// material, or any stock item named directly), else the stock item the
// product with that id is counted by. ok is false when it names neither.
func ItemOfTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (c Counted, ok bool, err error) {
	if id, perr := uuid.Parse(ref); perr == nil {
		err = tx.QueryRow(ctx, `SELECT id, 1, count, unit, retired_at IS NOT NULL FROM stock_items WHERE tenant_id = $1 AND id = $2`, tenant, id).
			Scan(&c.StockItemID, &c.Uses, &c.Count, &c.Unit, &c.Retired)
		if err == nil {
			return c, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return c, false, err
		}
	}
	m, err := CountsTx(ctx, tx, tenant, []string{ref})
	if err != nil {
		return c, false, err
	}
	c, ok = m[ref]
	return c, ok, nil
}

// ProductsOfTx is the products a stock item counts, with how many of it
// each sale of them uses.
func ProductsOfTx(ctx context.Context, tx pgx.Tx, tenant, item uuid.UUID) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT product_id, uses FROM product_stock WHERE tenant_id = $1 AND stock_item_id = $2`, tenant, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var uses int64
		if err := rows.Scan(&id, &uses); err != nil {
			return nil, err
		}
		out[id] = uses
	}
	return out, rows.Err()
}

// LinkTx counts a product by a stock item, one of it a sale; a product
// counted by another is counted by this one instead.
func LinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, product string, item uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO product_stock (tenant_id, product_id, stock_item_id) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, product_id) DO UPDATE SET stock_item_id = EXCLUDED.stock_item_id, uses = 1`, tenant, product, item)
	return err
}

// UnlinkTx stops counting a product; its stock item stays.
func UnlinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, product string) error {
	_, err := tx.Exec(ctx, `DELETE FROM product_stock WHERE tenant_id = $1 AND product_id = $2`, tenant, product)
	return err
}

// DropGoneTx drops the links of products no longer in the catalogue, read
// whole as present: their stock items stay, as materials, with their
// counts and history. It answers how many it dropped.
func DropGoneTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, present []string) (int64, error) {
	if present == nil {
		present = []string{}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM product_stock WHERE tenant_id = $1 AND NOT (product_id = ANY ($2))`, tenant, present)
	return tag.RowsAffected(), err
}
