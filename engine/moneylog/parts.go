package moneylog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The parts of an entry (docs/spec/schools.md 7.2; docs/matching.md): one
// money-in entry may count towards several invoices and storefront orders,
// each part a payment row of its own (customer_invoice_payments,
// storefront_order_payments) undone on its own. The parts never add up to
// more than the entry counts for (CountableKobo); the database refuses a
// part that would (00210), under the lock both writers take
// (EntryLockSQL). Both count and undo parts along one path (Parts.Count,
// Parts.Undo, count.go).

// EntryLockSQL takes the lock one money log entry is counted under ($1 the
// entry's id), shared by invoicing, storefront orders and the database's
// own check of an entry's parts.
const EntryLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended('taskiem money entry ' || $1::text, 0))`

// EntryLeft is what is left of an entry to count: what it counts for less
// its parts not undone. ok is false for an entry with no amount. Call it
// under the entry's lock.
func EntryLeft(ctx context.Context, tx pgx.Tx, entry uuid.UUID) (left int64, ok bool, err error) {
	var v *int64
	if err := tx.QueryRow(ctx, `SELECT taskiem_money_entry_left($1)`, entry).Scan(&v); err != nil {
		return 0, false, err
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}

// EntryHas says why a part of amount is refused when less than that is
// left of the entry ("this payment is already counted towards invoice
// INV-0104; undo that first", "only ₦5,000 of this payment is left to
// count"), or "" when it fits. Call it under the entry's lock.
func EntryHas(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, amount int64) (string, error) {
	left, ok, err := EntryLeft(ctx, tx, entry)
	if err != nil || !ok || left >= amount {
		return "", err
	}
	if left <= 0 {
		other, err := CountedTowards(ctx, tx, tenant, entry)
		if err != nil {
			return "", err
		}
		return "this payment is already counted towards " + other + "; undo that first", nil
	}
	return "only " + naira(left) + " of this payment is left to count", nil
}

// CountedTowards names what an entry counts towards now ("invoice
// INV-0104", "invoice INV-0104 and order AK-7QX4KD" for a split, "sale
// S-7QX4KD" for a counter sale's), or ""
// for nothing. Call it under the entry's lock.
func CountedTowards(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID) (string, error) {
	rows, err := tx.Query(ctx, `SELECT what FROM (
		SELECT 'invoice ' || i.number AS what, p.created_at FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
			WHERE p.tenant_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
		UNION ALL
		SELECT 'order ' || o.reference, p.created_at FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
			WHERE p.tenant_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)
		UNION ALL
		SELECT 'sale ' || x.reference, x.created_at FROM stock_sales x WHERE x.tenant_id = $1 AND x.entry_id = $2) m
		ORDER BY created_at, what`, tenant, entry)
	if err != nil {
		return "", err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	switch len(list) {
	case 0:
		return "", nil
	case 1:
		return list[0], nil
	}
	return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1], nil
}

// naira writes kobo as ₦25,000 or ₦25,000.50.
func naira(kobo int64) string {
	whole := fmt.Sprint(kobo / 100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if kobo%100 != 0 {
		return fmt.Sprintf("₦%s.%02d", whole, kobo%100)
	}
	return "₦" + whole
}
