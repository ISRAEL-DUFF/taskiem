package runtime

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
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

// PurgeExpired archives and then deletes runs past their retention. Without
// an archiver nothing is deleted: history is never dropped unarchived.
func (s *Scheduler) PurgeExpired(ctx context.Context, limit int) (int, error) {
	if s.Archiver == nil {
		return 0, nil
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT run_id, tenant_id FROM taskiem_claim_purgeable_runs($1)`, limit)
	if err != nil {
		return 0, err
	}
	refs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[RunRef])
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, r := range refs {
		hist, err := s.Store.RunHistory(ctx, r)
		if err != nil {
			return purged, err
		}
		if err := s.Archiver.Archive(ctx, r.TenantID, r.ID, hist); err != nil {
			return purged, fmt.Errorf("archive run %s: %w", r.ID, err)
		}
		err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{r.TenantID}, func(tx pgx.Tx) error {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT taskiem_purge_run($1)`, r.ID).Scan(&ok); err != nil {
				return err
			}
			if ok {
				purged++
			}
			return nil
		})
		if err != nil {
			return purged, err
		}
	}
	if purged > 0 {
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_drop_empty_run_event_partitions()`); err != nil {
			return purged, err
		}
	}
	return purged, nil
}
