package datatables

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Rows changed (pages §9, migration 00564): a table's rows are kept by
// Xenibase, so Taskiem records when it last wrote them, for an open page
// over the table to be told (api/pages_live.go). One row a table, written
// after the write, in a short transaction of its own; never a row's
// values.

// MarkRowsChanged records that the tenant's table name had its rows
// written now. tx must have the tenant in scope.
func MarkRowsChanged(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) error {
	if name == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO table_row_changes (tenant_id, table_name, changed_at) VALUES ($1, $2, clock_timestamp())
		ON CONFLICT (tenant_id, table_name) DO UPDATE SET changed_at = clock_timestamp()`, tenant, name)
	return err
}

// MarkStepRowsChanged is MarkRowsChanged for a workflow's xenibase@1 step
// that wrote rows (its input's table), in its own transaction; a step
// that names no table (call_function) marks nothing.
func MarkStepRowsChanged(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, action string, input map[string]any) error {
	name := stepTable(input)
	if !WritesRows(action) || name == "" {
		return nil
	}
	return db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return MarkRowsChanged(ctx, tx, tenant, name)
	})
}

// RowsChanged are when the tenant's tables named last had their rows
// written, by name (a table never written is not there). tx must have
// the tenant in scope.
func RowsChanged(ctx context.Context, tx pgx.Tx, names []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(names) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT table_name, changed_at FROM table_row_changes WHERE table_name = ANY($1)`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var at time.Time
		if err := rows.Scan(&n, &at); err != nil {
			return nil, err
		}
		out[n] = at
	}
	return out, rows.Err()
}
