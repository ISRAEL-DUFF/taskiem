// Package stock is what a business counts (docs/spec/stock.md,
// docs/spec/what-a-business-buys.md §4; decisions 0057 and 0070): stock
// items kept in Taskiem's database, apart from the products the business
// sells, and every change to a count a movement with its reason (an order
// accepted, a sale, goods received, an adjustment, a count).
//
// A stock item's count is the sum of its movements. A movement and the
// count it sets are written in one transaction, the transaction of what
// caused it: the database refuses any other change to a count (migration
// 00501). A product of the catalogue may be counted by a stock item
// (product_stock, engine/productstock): selling it lowers that stock
// item. A stock item no product is counted by is a material.
package stock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/productstock"
)

// Reasons a movement is made for.
const (
	Order      = "order"
	Sale       = "sale"
	Received   = "received"
	Adjustment = "adjustment"
	Count      = "count"
	// Moved is stock that went from one item to another of the business's
	// own (a product's count shared out among its sizes and colours): out
	// of one, into the other, neither received, sold nor lost.
	Moved = "moved"
)

var (
	// ErrInvalid refuses a movement that cannot be recorded.
	ErrInvalid = errors.New("stock: invalid")
	// ErrNotTracked: the business keeps no stock yet (no catalogue and no
	// stock item).
	ErrNotTracked = errors.New("stock: no stock is kept yet")
	// ErrNotCounted: a product sold or ordered is counted by no stock
	// item, so nothing moves.
	ErrNotCounted = errors.New("stock: the product is not counted")
)

// Stock records movements and reads stock items. Catalogue reads the
// business's products for workflows (stock@1) and the watch, which run
// outside the API; nil reads none.
type Stock struct {
	Pool      *pgxpool.Pool
	Catalogue Catalogue
	// MoneyLog takes a cash sale's takings (Sell, for workflows); the API
	// passes its own.
	MoneyLog *moneylog.Log
}

// Ready reports whether stock can be kept here.
func (s *Stock) Ready() bool { return s != nil && s.Pool != nil }

// Move is a movement asked for: a change (Delta) or a count (SetTo).
type Move struct {
	// ItemID names what moves: a stock item's id, or a product's (a
	// catalogue row's id), which moves the stock item it is counted by
	// (productstock.ItemOfTx).
	ItemID string
	Item   string
	Reason string
	// Delta is the change asked for: for a sale or an order, how many of
	// the product were sold (its stock item moves by that times the
	// product's uses), else how many of the stock item, unless Uses says.
	Delta *int64
	SetTo *int64
	// Uses is, on a change, how many of the stock item one of Delta is (a
	// purchase line of a product's: the uses it was ordered with); 0 is
	// one, or the product's uses for a sale or an order.
	Uses int64
	Note string
	// Reference names what it came from: an order's reference, a sale, a
	// purchase order, a count.
	Reference string
	Actor     string
	// Key makes recording it once: the same key is the same movement.
	Key string
	// CostKobo is the item's cost price when it moved, so a loss is valued
	// at cost (K4); nil when it has none, or it is not known.
	CostKobo *int64
}

// Movement is a recorded movement.
type Movement struct {
	ID uuid.UUID `json:"id"`
	// ItemID is the stock item that moved.
	ItemID string `json:"item_id"`
	Item   string `json:"item"`
	Reason string `json:"reason"`
	// Status is always done: a movement is made with its count.
	Status string `json:"status"`
	Delta  *int64 `json:"delta,omitempty"`
	SetTo  *int64 `json:"set_to,omitempty"`
	// Before and After are the stock item's count before and after it;
	// Change is After - Before.
	Before *int64 `json:"before"`
	After  *int64 `json:"after"`
	Change *int64 `json:"change"`
	// Short is what a decrease asked for beyond the stock there was.
	Short     int64      `json:"short,omitempty"`
	Note      string     `json:"note,omitempty"`
	Reference string     `json:"reference,omitempty"`
	Actor     string     `json:"by"`
	CreatedAt time.Time  `json:"created_at"`
	DoneAt    *time.Time `json:"done_at"`
}

// MaxDelta bounds a change asked for at once.
const MaxDelta = 1_000_000

func (m Move) check() error {
	switch m.Reason {
	case Order, Sale, Received, Adjustment, Count, Moved:
	default:
		return fmt.Errorf("%w: a movement is for an order, a sale, goods received, an adjustment, a count or stock moved", ErrInvalid)
	}
	if (m.Delta == nil) == (m.SetTo == nil) {
		return fmt.Errorf("%w: a movement is a change or a count", ErrInvalid)
	}
	if m.Reason == Count && m.SetTo == nil || m.Reason != Count && m.SetTo != nil {
		return fmt.Errorf("%w: only a count sets the stock", ErrInvalid)
	}
	if m.Uses < 0 || m.Uses > productstock.MaxUses || m.Uses > 1 && m.Delta == nil {
		return fmt.Errorf("%w: uses is a whole number from 1, on a change", ErrInvalid)
	}
	if m.Delta != nil && (*m.Delta == 0 || *m.Delta > MaxDelta || *m.Delta < -MaxDelta) {
		return fmt.Errorf("%w: a change is between -%d and %d, and not 0", ErrInvalid, MaxDelta, MaxDelta)
	}
	if m.SetTo != nil && (*m.SetTo < 0 || *m.SetTo > MaxDelta) {
		return fmt.Errorf("%w: a count is between 0 and %d", ErrInvalid, MaxDelta)
	}
	if m.ItemID == "" || len(m.ItemID) > 100 || strings.TrimSpace(m.Item) == "" || len(m.Item) > 200 {
		return fmt.Errorf("%w: the item", ErrInvalid)
	}
	if len([]rune(m.Note)) > 200 {
		return fmt.Errorf("%w: the note is up to 200 characters", ErrInvalid)
	}
	if len(m.Reference) > 100 || m.Actor == "" || m.Key == "" || len(m.Key) > 200 {
		return fmt.Errorf("%w: a reference up to 100 characters, who made it, and a key", ErrInvalid)
	}
	return nil
}

// FitKey is a caller's idempotency key as stock keeps it: as given up to
// 64 characters, else a hash of it, so the keys built from it (a sale's,
// "count:<key>:<item>", "po:<key>:<item>") fit however long the caller's
// key is (the API takes keys up to 255 characters).
func FitKey(key string) string {
	if len(key) <= 64 {
		return key
	}
	h := sha256.Sum256([]byte(key))
	return "h-" + hex.EncodeToString(h[:16])
}

const movementCols = `id, stock_item_id::text, item, reason, delta, set_to, count_before, count_after, change, short, COALESCE(note, ''),
	COALESCE(reference, ''), actor, created_at`

func scanMovement(r pgx.Row) (Movement, error) {
	var m Movement
	var before, after, change int64
	err := r.Scan(&m.ID, &m.ItemID, &m.Item, &m.Reason, &m.Delta, &m.SetTo, &before, &after, &change, &m.Short, &m.Note,
		&m.Reference, &m.Actor, &m.CreatedAt)
	m.Status = "done"
	m.Before, m.After, m.Change = &before, &after, &change
	at := m.CreatedAt
	m.DoneAt = &at
	return m, err
}

// RecordTx records a movement in tx, the transaction of what caused it,
// and sets its stock item's count with it; the same key again answers the
// movement it made. A sale or an order of a product moves its stock item
// by the quantity times the product's uses; a sale beyond the count is
// recorded, never refused (its short). A product sold or ordered that no
// stock item counts is ErrNotCounted; a product received, adjusted,
// counted or moved that none counts yet starts being counted, by a stock
// item of its name.
func RecordTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, mv Move) (Movement, error) {
	mv.Item = strings.TrimSpace(mv.Item)
	mv.Note = strings.TrimSpace(mv.Note)
	if len(mv.Key) > 200 {
		mv.Key = FitKey(mv.Key)
	}
	if err := mv.check(); err != nil {
		return Movement{}, err
	}
	if m, err := byKeyTx(ctx, tx, tenant, mv.Key); err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return m, err
	}
	c, ok, err := productstock.ItemOfTx(ctx, tx, tenant, mv.ItemID)
	if err != nil {
		return Movement{}, err
	}
	if !ok {
		if mv.Reason == Sale || mv.Reason == Order {
			return Movement{}, ErrNotCounted
		}
		if c.StockItemID, err = StartCountingTx(ctx, tx, tenant, mv.ItemID, mv.Item, "", mv.Actor); err != nil {
			return Movement{}, err
		}
	}
	// A sale or an order names how many of the product were sold: its
	// stock item moves by that times the product's uses (1 for a stock
	// item named directly, as an order put back names it). A purchase line
	// says its own.
	uses := mv.Uses
	if uses == 0 && (mv.Reason == Sale || mv.Reason == Order) {
		uses = c.Uses
	}
	if uses > 1 && mv.Delta != nil {
		d := *mv.Delta * uses
		mv.Delta = &d
	}
	// The stock item, locked: its movements are made one at a time.
	var n int64
	if err := tx.QueryRow(ctx, `SELECT count FROM stock_items WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, c.StockItemID).Scan(&n); err != nil {
		return Movement{}, err
	}
	// The same key, made meanwhile by another transaction holding the lock.
	if m, err := byKeyTx(ctx, tx, tenant, mv.Key); err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return m, err
	}
	to, short := target(n, mv)
	m, err := scanMovement(tx.QueryRow(ctx, `INSERT INTO stock_movements (id, tenant_id, stock_item_id, item, reason, delta, set_to, count_before, count_after, change,
			short, note, reference, actor, key, cost_kobo)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::bigint, $9::bigint, $9::bigint - $8::bigint, $10, NULLIF($11, ''), NULLIF($12, ''), $13, $14, $15) RETURNING `+movementCols,
		uuid.Must(uuid.NewV7()), tenant, c.StockItemID, cut(mv.Item, 200), mv.Reason, mv.Delta, mv.SetTo, n, to, short, mv.Note, mv.Reference, mv.Actor, mv.Key, mv.CostKobo))
	return m, err
}

func byKeyTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string) (Movement, error) {
	return scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND key = $2`, tenant, key))
}

// StartCountingTx makes the stock item a product is counted by, at 0, of
// the product's name, and links it; a product counted already answers
// its stock item.
func StartCountingTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, product, name, unit, actor string) (uuid.UUID, error) {
	if _, err := uuid.Parse(product); err == nil {
		return uuid.Nil, fmt.Errorf("%w: no such stock item", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('product_stock ' || $1::text || ' ' || $2, 0))`, tenant, product); err != nil {
		return uuid.Nil, err
	}
	if c, ok, err := productstock.ItemOfTx(ctx, tx, tenant, product); err != nil || ok {
		return c.StockItemID, err
	}
	it, err := CreateItemTx(ctx, tx, tenant, NewItem{Name: name, Unit: unit, Actor: actor})
	if err != nil {
		return uuid.Nil, err
	}
	return it.ID, productstock.LinkTx(ctx, tx, tenant, product, it.ID, 1)
}

// GetTx reads one movement.
func GetTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Movement, error) {
	return scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// target is the count a movement sets n to, and what a decrease asked
// for beyond the stock there was. A decrease never takes the count below
// zero, nor further below it when it already held less.
func target(n int64, m Move) (to, short int64) {
	if m.SetTo != nil {
		return *m.SetTo, 0
	}
	d := *m.Delta
	switch {
	case d >= 0 || n+d >= 0:
		return n + d, 0
	case n <= 0:
		return n, -d
	}
	return 0, -(n + d)
}

func (s *Stock) inTx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}

// Filter narrows a list of movements.
type Filter struct {
	// ItemID is a stock item's id, or a product's: its stock item's.
	ItemID string
	Reason string
	Since  *time.Time
	Until  *time.Time
	Limit  int
}

// ListTx lists movements, the latest first.
func ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter) ([]Movement, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var item *uuid.UUID
	if f.ItemID != "" {
		c, ok, err := productstock.ItemOfTx(ctx, tx, tenant, f.ItemID)
		if err != nil || !ok {
			return []Movement{}, err
		}
		item = &c.StockItemID
	}
	rows, err := tx.Query(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1
		AND ($2::uuid IS NULL OR stock_item_id = $2) AND ($3 = '' OR reason = $3)
		AND ($4::timestamptz IS NULL OR created_at >= $4) AND ($5::timestamptz IS NULL OR created_at < $5)
		ORDER BY created_at DESC, id DESC LIMIT $6`, tenant, item, f.Reason, f.Since, f.Until, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Movement, error) { return scanMovement(r) })
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
