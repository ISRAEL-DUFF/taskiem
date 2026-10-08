package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Admission of runs a tenant's limits held back (spec 8.3, 16.2). Each
// tenant with such runs has a token bucket in tenant_admission refilled at
// its soft ingest rate; every scheduler tick admits, per tenant, as many
// of its oldest held runs as its bucket and its running-runs cap allow.
// Tenants are admitted independently, so one tenant's backlog does not
// delay another's. Everything is in the database: a restart loses nothing,
// and several schedulers share the work (the bucket row is claimed with
// SKIP LOCKED).

const (
	admitTenants = 500 // tenants looked at per tick
	admitPerTick = 200 // runs one tenant may be admitted per tick
)

// AdmitQueued admits held runs across tenants and returns how many started
// (or moved on to wait for their workflow's concurrency).
func (s *Store) AdmitQueued(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_backlog_tenants($1)`, admitTenants)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, t := range tenants {
		n, err := s.admitTenant(ctx, t)
		total += n
		if err != nil {
			if ctx.Err() != nil {
				return total, err
			}
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	telemetry.RunsAdmitted.Add(float64(total))
	return total, errors.Join(errs...)
}

func (s *Store) admitTenant(ctx context.Context, tenant uuid.UUID) (int, error) {
	admitted := 0
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		admitted = 0
		lim, err := s.limitsTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		// A new bucket starts full, as the edge's does.
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_admission (tenant_id, tokens) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, max(lim.IngestBurst, 1)); err != nil {
			return err
		}
		var tokens, elapsed float64
		err = tx.QueryRow(ctx, `SELECT tokens, GREATEST(EXTRACT(EPOCH FROM now() - refilled_at), 0)::float8 FROM tenant_admission WHERE tenant_id = $1 FOR UPDATE SKIP LOCKED`, tenant).
			Scan(&tokens, &elapsed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // another scheduler is admitting this tenant
		}
		if err != nil {
			return err
		}
		n := admitPerTick
		if lim.IngestRate > 0 {
			tokens = math.Min(math.Max(float64(lim.IngestBurst), 1), tokens+lim.IngestRate*elapsed)
			n = min(int(tokens), admitPerTick)
		}
		if lim.MaxRunningRuns > 0 {
			running, err := countUpTo(ctx, tx, `runs WHERE tenant_id = $1 AND status = 'running'`, lim.MaxRunningRuns, tenant)
			if err != nil {
				return err
			}
			n = min(n, lim.MaxRunningRuns-running)
		}
		if n > 0 {
			rows, err := tx.Query(ctx, `SELECT id, workflow_id, version, COALESCE(concurrency_key, '') FROM runs
				WHERE tenant_id = $1 AND status = 'queued' AND queue_reason = 'tenant' ORDER BY started_at, id LIMIT $2 FOR UPDATE SKIP LOCKED`, tenant, n)
			if err != nil {
				return err
			}
			type held struct {
				id, workflow uuid.UUID
				version      int
				key          string
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (held, error) {
				var h held
				return h, r.Scan(&h.id, &h.workflow, &h.version, &h.key)
			})
			if err != nil {
				return err
			}
			for _, h := range list {
				if err := s.admitHeld(ctx, tx, RunRef{ID: h.id, TenantID: tenant}, h.workflow, h.version, h.key); err != nil {
					return fmt.Errorf("run %s: %w", h.id, err)
				}
				admitted++
			}
			tokens -= float64(admitted)
		}
		_, err = tx.Exec(ctx, `UPDATE tenant_admission SET tokens = $2, refilled_at = now() WHERE tenant_id = $1`, tenant, tokens)
		return err
	})
	if err != nil {
		admitted = 0
	}
	return admitted, err
}

// admitHeld lets a held run through its tenant's limits: it starts, or
// waits on for its workflow's concurrency (spec 4.8) like any other run.
func (s *Store) admitHeld(ctx context.Context, tx pgx.Tx, ref RunRef, workflow uuid.UUID, version int, key string) error {
	def, err := s.definition(ctx, tx, workflow, version)
	if err != nil {
		return err
	}
	if def.Settings.ConcurrencyKey != "" || def.Settings.MaxConcurrency > 0 {
		ok, err := s.tryAdmit(ctx, tx, ref.TenantID, workflow, ref.ID, key, def)
		if err != nil {
			return err
		}
		if !ok {
			_, err := tx.Exec(ctx, `UPDATE runs SET queue_reason = 'workflow' WHERE id = $1`, ref.ID)
			return err
		}
	}
	return s.startQueued(ctx, tx, ref, def)
}
