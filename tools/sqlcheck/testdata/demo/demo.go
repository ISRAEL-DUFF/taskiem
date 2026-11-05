// Package demo holds the calls TestCheckRules expects sqlcheck to flag or
// pass.
package demo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const cols = `id, name`

func calls(ctx context.Context, tx pgx.Tx, b *pgx.Batch, table string, limit int) {
	_, _ = tx.Exec(ctx, `DELETE FROM t WHERE id = $1`, 1)         // constant: passes
	_, _ = tx.Query(ctx, `SELECT `+cols+` FROM t`)                // constant expression: passes
	_, _ = tx.Exec(ctx, `DELETE FROM `+table)                     // flagged
	_ = tx.QueryRow(ctx, fmt.Sprintf(`SELECT 1 LIMIT %d`, limit)) // flagged
	b.Queue(`SELECT ` + table)                                    // flagged
	//sqlcheck:built table is one of two literals
	_, _ = tx.Exec(ctx, `TRUNCATE `+table) // marked on the line above: passes
	_, _ = tx.Exec(ctx, `TRUNCATE `+table) //sqlcheck:built same line: passes
}
