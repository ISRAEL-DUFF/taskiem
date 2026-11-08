package api

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// Row expiry (decision 0062 V5, M3 and M6): a table that keeps its rows
// for so many days, or as long as the business keeps its conversations,
// has its older rows deleted by this sweep, a batch at a time, through
// the table's own connection as the rows API does. Only ids are read.

// rowExpiryEvery is how often the api role sweeps.
const rowExpiryEvery = time.Hour

// rowExpiryBatch is how many rows one delete takes; the data API refuses
// a delete over its max_affected, so the sweep deletes exactly the ids it
// read.
const rowExpiryBatch = 1000

// RunRowExpiry sweeps every hour until ctx ends.
func (s *Server) RunRowExpiry(ctx context.Context) {
	t := time.NewTicker(rowExpiryEvery)
	defer t.Stop()
	for {
		if n, err := s.ExpireRows(ctx); err != nil {
			s.Logger.Warn("row expiry: a sweep did not finish", "err", err, "deleted", n)
		} else if n > 0 {
			s.Logger.Info("row expiry: older rows deleted", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ExpireRows deletes the rows past their time in every table that keeps
// rows for a time, and answers how many it deleted. A table whose
// project cannot be reached is tried again next sweep.
func (s *Server) ExpireRows(ctx context.Context) (int, error) {
	type due struct {
		tenant, table uuid.UUID
		name          string
		days          int
		conversations bool
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, table_id, name, keep_days, conversations FROM taskiem_tables_to_expire(1000)`)
	if err != nil {
		return 0, err
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.tenant, &d.table, &d.name, &d.days, &d.conversations); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	var firstErr error
	for _, d := range list {
		days := d.days
		if d.conversations {
			// The plan caps how long conversations are kept, as it caps
			// runs (a plan changed since the owner chose).
			if lim, err := s.Store.LimitsFor(ctx, d.tenant); err == nil && lim.MaxRetentionDays > 0 && lim.MaxRetentionDays < days {
				days = lim.MaxRetentionDays
			}
		}
		n, err := s.expireTable(ctx, d.tenant, d.name, time.Now().Add(-time.Duration(days)*24*time.Hour))
		total += n
		if err != nil {
			s.Logger.Warn("row expiry: a table's older rows were not all deleted", "tenant", d.tenant, "table", d.table, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return total, firstErr
}

// expireTable deletes a table's rows added before cutoff, oldest first.
func (s *Server) expireTable(ctx context.Context, tenant uuid.UUID, name string, cutoff time.Time) (int, error) {
	tr, err := s.tableRowsFor(ctx, tenant, datatables.Table{Name: name}, "tables.expire")
	if err != nil {
		return 0, err
	}
	before := cutoff.UTC().Format(time.RFC3339)
	total := 0
	for range 100 { // at most 100,000 rows a sweep per table
		page, err := tr.call(ctx, s, "read_rows", map[string]any{"table": name, "select": "id",
			"filters": []any{map[string]any{"column": "created_at", "op": "lt", "value": before}},
			"order":   []any{map[string]any{"column": "id"}}, "limit": rowExpiryBatch}, "")
		if err != nil {
			return total, err
		}
		got, _ := page["rows"].([]any)
		ids := make([]any, 0, len(got))
		for _, r := range got {
			if m, ok := r.(map[string]any); ok && m["id"] != nil {
				ids = append(ids, m["id"])
			}
		}
		if len(ids) == 0 {
			return total, nil
		}
		key := fmt.Sprintf("expire-%s-%v-%d", name, ids[0], len(ids))
		if _, err := tr.call(ctx, s, "delete_rows", map[string]any{"table": name,
			"filters": []any{map[string]any{"column": "id", "op": "in", "values": ids}}, "max_affected": len(ids)}, key); err != nil {
			return total, err
		}
		total += len(ids)
		if len(ids) < rowExpiryBatch {
			return total, nil
		}
	}
	return total, nil
}
