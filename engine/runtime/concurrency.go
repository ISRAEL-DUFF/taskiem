package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// admissionLock serialises admission decisions for one workflow.
func admissionLock(ctx context.Context, tx pgx.Tx, tenant, workflow uuid.UUID) error {
	h := fnv.New64a()
	_, _ = h.Write([]byte("admit\x00" + tenant.String() + "\x00" + workflow.String()))
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(h.Sum64())) //nolint:gosec // bit-for-bit reinterpretation as a lock key
	return err
}

// concurrencyKey evaluates settings.concurrency_key for a run.
func concurrencyKey(def *wd.Definition, started history.RunStartedPayload) (string, error) {
	if def.Settings.ConcurrencyKey == "" {
		return "", nil
	}
	raw, err := jsonRoundTrip(started)
	if err != nil {
		return "", err
	}
	m := raw.(map[string]any)
	v, err := expr.MustNew().Eval(def.Settings.ConcurrencyKey, map[string]any{"trigger": m["trigger"], "env": m["env"], "run": m["run"], "steps": map[string]any{}})
	if err != nil {
		return "", fmt.Errorf("concurrency_key: %w", err)
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return fmt.Sprint(v), nil
}

// admit decides whether a new run may start now (spec 4.8): it needs its
// concurrency_key slot, and the workflow must be under max_concurrency.
// A run that may not start is recorded as queued; it is refused only when
// the tenant's backlog is full (a *LimitError).
func (s *Store) admit(ctx context.Context, tx pgx.Tx, ref RunRef, workflow uuid.UUID, def *wd.Definition, started history.RunStartedPayload, lim Limits) (bool, error) {
	if def.Settings.ConcurrencyKey == "" && def.Settings.MaxConcurrency <= 0 {
		return true, nil
	}
	key, err := concurrencyKey(def, started)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET concurrency_key = NULLIF($2, '') WHERE id = $1`, ref.ID, key); err != nil {
		return false, err
	}
	ok, err := s.tryAdmit(ctx, tx, ref.TenantID, workflow, ref.ID, key, def)
	if err != nil || ok {
		return ok, err
	}
	if err := checkBacklog(ctx, tx, ref.TenantID, lim); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET status = 'queued', queue_reason = 'workflow' WHERE id = $1`, ref.ID)
	return false, err
}

func (s *Store) tryAdmit(ctx context.Context, tx pgx.Tx, tenant, workflow, run uuid.UUID, key string, def *wd.Definition) (bool, error) {
	if err := admissionLock(ctx, tx, tenant, workflow); err != nil {
		return false, err
	}
	if max := def.Settings.MaxConcurrency; max > 0 {
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE tenant_id = $1 AND workflow_id = $2 AND id <> $3
			AND status NOT IN ('queued', 'completed', 'failed', 'cancelled')`, tenant, workflow, run).Scan(&active); err != nil {
			return false, err
		}
		if active >= max {
			return false, nil
		}
	}
	if key != "" {
		tag, err := tx.Exec(ctx, `INSERT INTO concurrency_slots (tenant_id, workflow_id, key, run_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			tenant, workflow, key, run)
		if err != nil || tag.RowsAffected() == 0 {
			return false, err
		}
	}
	return true, nil
}

// promote admits the oldest queued runs of a workflow that can now start.
func (s *Store) promote(ctx context.Context, tx pgx.Tx, tenant, workflow uuid.UUID) error {
	// A suspended tenant's queued runs wait until it is resumed.
	if ok, err := tenantActive(ctx, tx, tenant); err != nil || !ok {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id, COALESCE(concurrency_key, ''), version FROM runs
		WHERE tenant_id = $1 AND workflow_id = $2 AND status = 'queued' AND queue_reason IS DISTINCT FROM 'tenant' ORDER BY started_at LIMIT 100`, tenant, workflow)
	if err != nil {
		return err
	}
	type queued struct {
		id      uuid.UUID
		key     string
		version int
	}
	var qs []queued
	for rows.Next() {
		var q queued
		if err := rows.Scan(&q.id, &q.key, &q.version); err != nil {
			rows.Close()
			return err
		}
		qs = append(qs, q)
	}
	rows.Close()
	for _, q := range qs {
		def, err := s.definition(ctx, tx, workflow, q.version)
		if err != nil {
			return err
		}
		ok, err := s.tryAdmit(ctx, tx, tenant, workflow, q.id, q.key, def)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := s.startQueued(ctx, tx, RunRef{ID: q.id, TenantID: tenant}, def); err != nil {
			return err
		}
	}
	return nil
}

// startQueued starts a queued run that got its place.
func (s *Store) startQueued(ctx context.Context, tx pgx.Tx, ref RunRef, def *wd.Definition) error {
	if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'running', queue_reason = NULL WHERE id = $1`, ref.ID); err != nil {
		return err
	}
	if _, err := appendEvent(ctx, tx, ref.ID, history.RunAdmitted, "", 0, map[string]any{}, history.OriginIngest); err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return err
	}
	return s.start(ctx, tx, ref, def, now)
}

// jsonRoundTrip converts a struct to JSON-compatible values (int64 numbers).
func jsonRoundTrip(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return expr.DecodeJSON(raw)
}
