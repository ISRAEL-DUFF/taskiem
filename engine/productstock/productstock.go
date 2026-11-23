// Package productstock is a product counted by a stock item
// (docs/spec/what-a-business-buys.md §4, decision 0070): a catalogue row
// of the owner's table, linked in product_stock to the stock item whose
// count selling it lowers, with how many of it one sale uses.
//
// It is the one path from a product to its count:
//
//   - Selling and ordering (engine/stock's RecordTx, through ItemOfTx) lower
//     a product's stock item by the quantity times its uses.
//   - A purchase line names the stock item and the uses it was made with
//     (ItemOfTx when the order is made): receiving moves the stock item by
//     the quantity times the uses, at the line's cost over the uses.
//   - How many are left of a product is how many whole sales its stock
//     item covers (Counted.Left, LeftTx). The business's own reads (the
//     storefront's catalogue read, the live price check, the Stock page)
//     read it here; the public page and a linked buyer, who may read
//     nothing else about stock items, read the same through
//     taskiem_product_left (migration 00502). No copy of the catalogue
//     holds a count.
//   - LinkTx says what a product is counted by, with its uses (whole, at
//     least 1); UnlinkTx that it is counted by none.
package productstock

import (
	"context"
	"errors"
	"time"

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

// MaxUses is the most of a stock item one sale may use.
const MaxUses = 1_000_000

// ErrUses refuses uses that are not a whole number from 1 to MaxUses.
var ErrUses = errors.New("productstock: uses is a whole number from 1 to 1,000,000")

// LinkTx counts a product by a stock item, uses of it a sale (a whole
// number, at least 1); a product counted by another is counted by this one
// instead.
func LinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, product string, item uuid.UUID, uses int64) error {
	if uses < 1 || uses > MaxUses {
		return ErrUses
	}
	// Shared with every other link being made; a catalogue refresh waits
	// for them to end before it reads (RefreshStartTx), and a link made
	// after is dated after its start, so the refresh never drops it.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('taskiem product links ' || $1::text, 0))`, tenant); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_stock (tenant_id, product_id, stock_item_id, uses, created_at) VALUES ($1, $2, $3, $4, clock_timestamp())
		ON CONFLICT (tenant_id, product_id) DO UPDATE SET stock_item_id = EXCLUDED.stock_item_id, uses = EXCLUDED.uses`, tenant, product, item, uses)
	return err
}

// LeftTx is how many whole sales are left of each counted product among
// products, by id (Counted.Left); products not counted are not in it.
// The business's own read of what taskiem_product_left answers others.
func LeftTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, products []string) (map[string]int64, error) {
	if products == nil {
		products = []string{}
	}
	counts, err := CountsTx(ctx, tx, tenant, products)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(counts))
	for id, c := range counts {
		out[id] = c.Left()
	}
	return out, nil
}

// UnlinkTx stops counting a product; its stock item stays.
func UnlinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, product string) error {
	_, err := tx.Exec(ctx, `DELETE FROM product_stock WHERE tenant_id = $1 AND product_id = $2`, tenant, product)
	return err
}

// RefreshStartTx is when a catalogue refresh starts, taken in a short
// transaction of its own before the catalogue is read: it waits for the
// links being made to end (LinkTx holds the same lock, shared), so every
// link dated before it was committed before the read began, and every
// link made after it is dated after it.
func RefreshStartTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (time.Time, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('taskiem product links ' || $1::text, 0))`, tenant); err != nil {
		return time.Time{}, err
	}
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at)
	return at, err
}

// DropGoneTx drops the links of products no longer in the catalogue, read
// whole as present after start (RefreshStartTx): their stock items stay,
// as materials, with their counts and history. A link made after start
// stays: its product may be newer than the read. It answers how many it
// dropped.
func DropGoneTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, present []string, start time.Time) (int64, error) {
	if present == nil {
		present = []string{}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM product_stock WHERE tenant_id = $1 AND NOT (product_id = ANY ($2)) AND created_at < $3`, tenant, present, start)
	return tag.RowsAffected(), err
}
