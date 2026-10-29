package runtime

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Archiver stores an ended run's history before it is purged (spec 9.4:
// hot for the retention period, then archived to object storage).
type Archiver interface {
	Archive(ctx context.Context, tenant, run uuid.UUID, events []history.Event) error
}

// FileArchiver writes gzipped JSON lines under Dir/<tenant>/<run>.jsonl.gz,
// for single-node installs with a mounted volume. Object storage archivers
// implement the same interface.
type FileArchiver struct{ Dir string }

func (f FileArchiver) Archive(_ context.Context, tenant, run uuid.UUID, events []history.Event) error {
	dir := filepath.Join(f.Dir, tenant.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(dir, run.String()+".jsonl.gz.tmp")
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) //nolint:gosec // path built from ids
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(file)
	enc := json.NewEncoder(zw)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, run.String()+".jsonl.gz"))
}

// Purge pacing. A pass claims batches while they come back full, until
// PurgeBudget has passed, so the scheduler's other work waits at most
// that plus one batch; a pass the budget cuts short carries on at the
// next tick, and one that empties the backlog waits purgeEvery. Batches
// are small so that one batch, archived a run at a time, stays short.
const (
	defaultPurgeBatch  = 25
	defaultPurgeBudget = time.Second
	purgeEvery         = 10 * time.Minute
	// purgeReport bounds how often the backlog is counted while a pass is
	// still behind (counting a large backlog is not free).
	purgeReport = time.Minute
)

// PurgeExpired archives and then deletes up to limit runs past their
// retention, and returns how many it purged. Without an archiver nothing
// is deleted: history is never dropped unarchived. A run that cannot be
// read, archived or deleted is skipped, not fatal: the error is recorded
// on the run (taskiem_purge_run_failed) and it is retried after a backoff,
// so it cannot hold back the runs behind it. The error returned is for
// the claim alone.
func (s *Scheduler) PurgeExpired(ctx context.Context, limit int) (int, error) {
	s.defaults()
	_, purged, err := s.purgeBatch(ctx, limit)
	if err == nil && purged > 0 {
		_, err = s.Store.Pool.Exec(ctx, `SELECT taskiem_drop_empty_run_event_partitions()`)
	}
	return purged, err
}

// purgeBatch claims up to limit runs and purges them one at a time. It
// returns the runs claimed and purged.
func (s *Scheduler) purgeBatch(ctx context.Context, limit int) (claimed, purged int, err error) {
	if s.Archiver == nil {
		return 0, 0, nil
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT run_id, tenant_id FROM taskiem_claim_purgeable_runs($1)`, limit)
	if err != nil {
		return 0, 0, err
	}
	refs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[RunRef])
	if err != nil {
		return 0, 0, err
	}
	var failed int
	var firstErr error
	var firstRun uuid.UUID
	for _, r := range refs {
		ok, perr := s.purgeRun(ctx, r)
		if ok {
			purged++
		}
		if perr == nil {
			continue
		}
		if ctx.Err() != nil {
			return len(refs), purged, ctx.Err()
		}
		failed++
		telemetry.PurgeFailures.Inc()
		if firstErr == nil {
			firstErr, firstRun = perr, r.ID
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{r.TenantID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_purge_run_failed($1, $2)`, r.ID, perr.Error())
			return err
		})
		if err != nil {
			s.Logger.Warn("could not record a failed purge; the run is retried when its claim expires", "run", r.ID, "err", err)
		}
	}
	telemetry.RunsPurged.Add(float64(purged))
	if failed > 0 {
		s.Logger.Warn("retention purge skipped runs it could not purge; each is retried after a backoff",
			"failed", failed, "purged", purged, "first_run", firstRun, "err", firstErr)
	}
	return len(refs), purged, nil
}

// purgeRun archives one run's history and deletes it, with its audit
// record. ok is false (and err nil) when the run was already gone.
func (s *Scheduler) purgeRun(ctx context.Context, r RunRef) (ok bool, err error) {
	hist, err := s.Store.RunHistory(ctx, r)
	if err != nil {
		return false, fmt.Errorf("read run %s: %w", r.ID, err)
	}
	if err := s.Archiver.Archive(ctx, r.TenantID, r.ID, hist); err != nil {
		return false, fmt.Errorf("archive run %s: %w", r.ID, err)
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{r.TenantID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_purge_run($1)`, r.ID).Scan(&ok); err != nil || !ok {
			return err
		}
		// In the purge's transaction: no run leaves without a record.
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'retention', 'run.purge', $2, jsonb_build_object('events', $3::int, 'archived', true))`,
			r.TenantID, r.ID.String(), len(hist))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("delete run %s: %w", r.ID, err)
	}
	return ok, nil
}

// purgeRetention is the tick's purge pass: batches while they come back
// full (and the last one purged something: a batch that failed whole
// points at the archive, not at a run), within the budget. It reports
// whether the backlog is cleared, so the next pass can wait.
func (s *Scheduler) purgeRetention(ctx context.Context) (done bool, err error) {
	if s.Archiver == nil {
		return true, nil
	}
	batch, budget := s.PurgeBatch, s.PurgeBudget
	if batch <= 0 {
		batch = defaultPurgeBatch
	}
	if budget <= 0 {
		budget = defaultPurgeBudget
	}
	deadline := time.Now().Add(budget)
	total := 0
	for {
		claimed, purged, err := s.purgeBatch(ctx, batch)
		total += purged
		if err != nil {
			return false, err
		}
		if claimed < batch || purged == 0 {
			done = true
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	if !done && time.Since(s.lastPurgeReport) < purgeReport {
		return false, nil
	}
	s.lastPurgeReport = time.Now()
	if total > 0 || done {
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_drop_empty_run_event_partitions()`); err != nil {
			return done, err
		}
	}
	return done, s.reportPurgeBacklog(ctx)
}

// reportPurgeBacklog sets the retention gauges.
func (s *Scheduler) reportPurgeBacklog(ctx context.Context) error {
	var backlog, failing int64
	var oldest float64
	if err := s.Store.Pool.QueryRow(ctx, `SELECT runs, oldest_overdue_seconds, failing FROM taskiem_purge_backlog()`).Scan(&backlog, &oldest, &failing); err != nil {
		return err
	}
	telemetry.PurgeBacklog.Set(float64(backlog))
	telemetry.PurgeOldestOverdue.Set(oldest)
	telemetry.PurgeFailing.Set(float64(failing))
	return nil
}

// purgeReceipts deletes trigger receipts past the 30-day dedup window, a
// day of them per call, up to a month of backlog per pass.
func (s *Scheduler) purgeReceipts(ctx context.Context) error {
	for range 31 {
		var n int64
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_purge_trigger_receipts()`).Scan(&n); err != nil || n == 0 {
			return err
		}
	}
	return nil
}
