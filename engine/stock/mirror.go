package stock

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// KeptTable names the kept data table movements are copied to, in the
// business's project: the record of reference is Taskiem's, the table a
// copy for reading, exporting and workflows (as money_log is).
const KeptTable = "stock_movements"

// KeptColumns are the kept table's columns.
var KeptColumns = []datatables.Column{
	{Name: "movement_id", Title: "Movement", Type: "text", Required: true},
	{Name: "item_id", Title: "Item row", Type: "text", Required: true},
	{Name: "item", Title: "Item", Type: "text", Required: true},
	{Name: "reason", Title: "Why", Type: "text", Required: true},
	{Name: "change", Title: "Change", Type: "integer", Required: true},
	{Name: "count_before", Title: "Count before", Type: "integer"},
	{Name: "count_after", Title: "Count after", Type: "integer"},
	{Name: "short", Title: "Asked beyond the stock", Type: "integer"},
	{Name: "note", Title: "Note", Type: "text"},
	{Name: "reference", Title: "From", Type: "text"},
	{Name: "by", Title: "By", Type: "text", Required: true},
	{Name: "at", Title: "When", Type: "datetime", Required: true},
}

// Rows writes and finds rows of a business's project (moneylog.XenibaseRows).
type Rows interface {
	Call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error)
}

// Mirror copies done movements to each business's kept stock_movements
// table, once each: a movement is looked for by its id before it is
// inserted, the insert carries a key derived from the id, and the
// movement is marked copied only after.
type Mirror struct {
	Stock  *Stock
	Tables *datatables.Service
	Rows   Rows
	Logger *slog.Logger

	mu   sync.Mutex
	kept map[uuid.UUID]bool
}

// KeptRow is a movement as a row of the kept table.
func KeptRow(m Movement) map[string]any {
	r := map[string]any{"movement_id": m.ID.String(), "item_id": m.ItemID, "item": m.Item, "reason": m.Reason, "by": m.Actor}
	if m.Change != nil {
		r["change"] = *m.Change
	}
	if m.Before != nil {
		r["count_before"] = *m.Before
	}
	if m.After != nil {
		r["count_after"] = *m.After
	}
	if m.Short > 0 {
		r["short"] = m.Short
	}
	if m.Note != "" {
		r["note"] = m.Note
	}
	if m.Reference != "" {
		r["reference"] = m.Reference
	}
	at := m.CreatedAt
	if m.DoneAt != nil {
		at = *m.DoneAt
	}
	r["at"] = at.UTC().Format(time.RFC3339Nano)
	return r
}

// Tenant copies up to 200 of a business's done movements not yet copied.
func (m *Mirror) Tenant(ctx context.Context, tenant uuid.UUID) error {
	if m.Tables == nil || m.Rows == nil {
		return nil
	}
	if err := m.ensure(ctx, tenant); err != nil {
		return err
	}
	var list []Movement
	if err := m.Stock.inTx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND status = 'done' AND mirrored_at IS NULL
			ORDER BY done_at, id LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Movement, error) { return scanMovement(r) })
		return err
	}); err != nil {
		return err
	}
	for _, mv := range list {
		found, err := m.Rows.Call(ctx, tenant, "read_rows", map[string]any{"table": KeptTable, "limit": 1, "select": "movement_id",
			"filters": []any{map[string]any{"column": "movement_id", "op": "eq", "value": mv.ID.String()}}}, "")
		if err != nil {
			return err
		}
		if rows, _ := found["rows"].([]any); len(rows) == 0 {
			if _, err := m.Rows.Call(ctx, tenant, "insert_rows", map[string]any{"table": KeptTable, "rows": []any{KeptRow(mv)}, "return": "minimal"},
				rowKey(mv.ID)); err != nil {
				return err
			}
		}
		if err := m.Stock.inTx(ctx, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE stock_movements SET mirrored_at = now() WHERE tenant_id = $1 AND id = $2 AND mirrored_at IS NULL`, tenant, mv.ID)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) ensure(ctx context.Context, tenant uuid.UUID) error {
	m.mu.Lock()
	done := m.kept[tenant]
	m.mu.Unlock()
	if done {
		return nil
	}
	if _, err := m.Tables.EnsureKept(ctx, tenant, KeptTable, KeptTable, KeptColumns); err != nil {
		return fmt.Errorf("the stock_movements table: %w", err)
	}
	m.mu.Lock()
	if m.kept == nil {
		m.kept = map[uuid.UUID]bool{}
	}
	m.kept[tenant] = true
	m.mu.Unlock()
	return nil
}

// rowKey is the insert's idempotency key: the same movement, the same key.
func rowKey(id uuid.UUID) string {
	h := sha256.Sum256([]byte("taskiem stock_movements " + id.String()))
	return "tsk-" + base64.RawURLEncoding.EncodeToString(h[:])[:22]
}

// Sweeper sends movements still on their way, matches sales waiting for
// their money, and copies done movements, for every business with some,
// every 30 seconds.
type Sweeper struct {
	Stock  *Stock
	Mirror *Mirror
	Logger *slog.Logger
}

func (w *Sweeper) log() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// Run sweeps until ctx ends.
func (w *Sweeper) Run(ctx context.Context) error {
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.log().Error("stock: sweeping movements", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
		}
	}
}

// Tick sweeps once. A business whose table cannot be reached waits for
// the next tick; the others go on.
func (w *Sweeper) Tick(ctx context.Context) error {
	rows, err := w.Stock.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_stock_due_tenants(now())`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := w.Stock.ApplyPending(ctx, t); err != nil && ctx.Err() == nil {
			w.log().Warn("stock: sending movements", "tenant", t, "err", err)
		}
		if _, err := w.Stock.MatchSales(ctx, t); err != nil && ctx.Err() == nil {
			w.log().Warn("stock: matching sales to their money", "tenant", t, "err", err)
		}
		if w.Mirror == nil {
			continue
		}
		if err := w.Mirror.Tenant(ctx, t); err != nil && !errors.Is(err, datatables.ErrUnavailable) && ctx.Err() == nil {
			w.log().Warn("stock: copying movements", "tenant", t, "err", err)
		}
	}
	return nil
}
