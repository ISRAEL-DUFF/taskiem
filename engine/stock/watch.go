package stock

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The owner's stock alerts (docs/spec/stock.md §7, K4), not leaks, as
// dispatch's quiet riders are: an item low for a week with nothing
// ordered, and an order confirmed and not received after its expected
// days.

// Alert kinds.
const (
	AlertLowWeek  = "stock.low_week"
	AlertLateBuy  = "stock.order_late"
	LowWeekAfter  = 7 * 24 * time.Hour
	WatchInterval = time.Hour
)

// Watch keeps which items are low since when, and raises the alerts.
type Watch struct {
	Stock    *Stock
	Pool     *pgxpool.Pool
	Interval time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

func (w *Watch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Watch) log() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// Run watches every hour until ctx ends.
func (w *Watch) Run(ctx context.Context) error {
	every := w.Interval
	if every <= 0 {
		every = WatchInterval
	}
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.log().Error("stock: watch", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick watches every business whose catalogue keeps stock.
func (w *Watch) Tick(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_stock_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if _, err := w.Tenant(ctx, t); err != nil && ctx.Err() == nil {
			w.log().Warn("stock: watch", "tenant", t, "err", err)
		}
	}
	return ctx.Err()
}

// Tenant keeps one business's low items and raises its alerts; how many
// alerts it raised.
func (w *Watch) Tenant(ctx context.Context, tenant uuid.UUID) (int, error) {
	items, err := w.Stock.Items(ctx, tenant)
	if err != nil {
		return 0, err
	}
	now := w.now()
	raised := 0
	err = db.InTenantTx(ctx, w.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		low := Low(items)
		ids := make([]string, 0, len(low))
		for _, it := range low {
			ids = append(ids, it.ID)
			if _, err := tx.Exec(ctx, `INSERT INTO stock_low (tenant_id, item_id, item, since) VALUES ($1, $2, $3, $4)
				ON CONFLICT (tenant_id, item_id) DO UPDATE SET item = EXCLUDED.item`, tenant, it.ID, cut(it.Name, 200), now); err != nil {
				return err
			}
		}
		// No longer low: forgotten, so a later fall starts a new week.
		if _, err := tx.Exec(ctx, `DELETE FROM stock_low WHERE tenant_id = $1 AND NOT (item_id = ANY ($2))`, tenant, ids); err != nil {
			return err
		}
		// Low a week, nothing ordered, not yet told.
		rows, err := tx.Query(ctx, `SELECT l.item_id, l.item, l.since FROM stock_low l
			WHERE l.tenant_id = $1 AND l.alerted_at IS NULL AND l.since <= $2
			  AND NOT EXISTS (SELECT 1 FROM purchase_orders o, jsonb_array_elements(o.lines) x
			                  WHERE o.tenant_id = l.tenant_id AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received') AND x->>'item_id' = l.item_id)`,
			tenant, now.Add(-LowWeekAfter))
		if err != nil {
			return err
		}
		type stale struct {
			id, item string
			since    time.Time
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stale, error) {
			var s stale
			return s, r.Scan(&s.id, &s.item, &s.since)
		})
		if err != nil {
			return err
		}
		for _, s := range list {
			fresh, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertLowWeek, Dedup: s.id + ":" + s.since.UTC().Format(time.RFC3339),
				Title: s.item + " has been low for a week",
				Body:  fmt.Sprintf("%s has been at or below its reorder level since %s, and nothing is ordered. Order it from its supplier on the Stock page, or send *order from* and the supplier's name.", s.item, s.since.In(storefront.Lagos).Format("2 Jan")),
				Link:  "/storefront/stock"})
			if err != nil {
				return err
			}
			if fresh {
				raised++
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_low SET alerted_at = $3 WHERE tenant_id = $1 AND item_id = $2`, tenant, s.id, now); err != nil {
				return err
			}
		}
		// Orders confirmed and not received after their expected days.
		rows, err = tx.Query(ctx, `SELECT o.id, o.reference, s.name, o.confirmed_at, o.expected_days FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
			WHERE o.tenant_id = $1 AND o.status = 'confirmed' AND o.confirmed_at + make_interval(days => o.expected_days) <= $2`, tenant, now)
		if err != nil {
			return err
		}
		type late struct {
			id            uuid.UUID
			ref, supplier string
			confirmed     time.Time
			days          int
		}
		lates, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (late, error) {
			var l late
			return l, r.Scan(&l.id, &l.ref, &l.supplier, &l.confirmed, &l.days)
		})
		if err != nil {
			return err
		}
		for _, l := range lates {
			fresh, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertLateBuy, Dedup: l.id.String(),
				Title: "Order " + l.ref + " from " + l.supplier + " has not come",
				Body:  fmt.Sprintf("%s confirmed %s on %s and it was expected within %d days. Nothing has come in on it yet.", l.supplier, l.ref, l.confirmed.In(storefront.Lagos).Format("2 Jan"), l.days),
				Link:  "/storefront/stock"})
			if err != nil {
				return err
			}
			if fresh {
				raised++
			}
		}
		return nil
	})
	return raised, err
}
