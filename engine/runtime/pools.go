package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// SharedPool is the worker pool every tenant is in unless an operator
// routes it elsewhere (decision 0024).
const SharedPool = "shared"

var poolName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidPool reports whether name can name a worker pool: lowercase
// letters, digits and dashes, up to 32 characters.
func ValidPool(name string) bool { return poolName.MatchString(name) }

// ErrNoWorkers refuses routing work to a pool no live worker serves.
var ErrNoWorkers = errors.New("no live worker serves this pool")

// ReadTx runs fn read-only, scoped to the tenant, on the read replica when
// one is configured and keeping up, else on the primary. Only for reads
// that tolerate being a few seconds stale (run lists, dashboards); never
// for anything that decides, leases or writes. fn may run twice.
func (s *Store) ReadTx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.ReadTx(ctx, s.Pool, s.Read, []uuid.UUID{tenant}, fn)
}

// PoolAssignment is one operator routing.
type PoolAssignment struct {
	Tenant     *uuid.UUID `json:"tenant,omitempty"`
	Plan       string     `json:"plan,omitempty"`
	Pool       string     `json:"pool"`
	AssignedBy string     `json:"assigned_by"`
	AssignedAt time.Time  `json:"assigned_at"`
}

// PoolWorkers is what live workers serve: a pool, a queue and how many
// worker processes reported in the last few minutes.
type PoolWorkers struct {
	Pool     string    `json:"pool"`
	Queue    string    `json:"queue"`
	Workers  int       `json:"workers"`
	LastSeen time.Time `json:"last_seen"`
}

// PoolDepth is the queue depth of one pool and queue.
type PoolDepth struct {
	Pool          string  `json:"pool"`
	Queue         string  `json:"queue"`
	Ready         int64   `json:"ready"`
	Leased        int64   `json:"leased"`
	OldestSeconds float64 `json:"oldest_ready_seconds"`
}

// liveWindow is how recently a worker must have reported to count as live.
const liveWindow = 5 * time.Minute

// LiveWorkers lists the pools and queues live workers serve.
func (s *Store) LiveWorkers(ctx context.Context) ([]PoolWorkers, error) {
	rows, err := s.Pool.Query(ctx, `SELECT pool, queue, count(*)::int, max(seen_at) FROM worker_pool_workers
		WHERE seen_at > now() - $1::interval GROUP BY pool, queue ORDER BY pool, queue`, liveWindow.String())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PoolWorkers])
}

// PoolStats is the queue depth by pool and queue.
func (s *Store) PoolStats(ctx context.Context) ([]PoolDepth, error) {
	rows, err := s.Pool.Query(ctx, `SELECT pool, queue, ready, leased, oldest_ready_seconds FROM taskiem_pool_stats() ORDER BY pool, queue`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PoolDepth])
}

// PoolAssignments lists every plan and tenant routing. Tenants' come from
// the change log (operators' records: tenant ids and pool names, no tenant
// data), so listing needs no cross-tenant access to tenant tables.
func (s *Store) PoolAssignments(ctx context.Context) ([]PoolAssignment, error) {
	rows, err := s.Pool.Query(ctx, `SELECT plan_id, pool, assigned_by, assigned_at FROM plan_worker_pools ORDER BY plan_id`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PoolAssignment, error) {
		var a PoolAssignment
		err := row.Scan(&a.Plan, &a.Pool, &a.AssignedBy, &a.AssignedAt)
		return a, err
	})
	if err != nil {
		return nil, err
	}
	// The latest change per tenant is its routing; NULL: removed.
	rows, err = s.Pool.Query(ctx, `SELECT DISTINCT ON (tenant_id) tenant_id, pool, changed_by, changed_at FROM worker_pool_changes
		WHERE tenant_id IS NOT NULL ORDER BY tenant_id, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a PoolAssignment
		var tenant uuid.UUID
		var pool *string
		if err := rows.Scan(&tenant, &pool, &a.AssignedBy, &a.AssignedAt); err != nil {
			return nil, err
		}
		if pool == nil {
			continue
		}
		a.Tenant, a.Pool = &tenant, *pool
		out = append(out, a)
	}
	return out, rows.Err()
}

// TenantPool is the pool a tenant's work runs in.
func (s *Store) TenantPool(ctx context.Context, tenant uuid.UUID) (string, error) {
	var pool string
	err := s.Pool.QueryRow(ctx, `SELECT taskiem_tenant_worker_pool($1)`, tenant).Scan(&pool)
	return pool, err
}

// checkLive refuses a pool with no live workers, unless force.
func (s *Store) checkLive(ctx context.Context, pool string, force bool) error {
	if force || pool == SharedPool {
		return nil
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM worker_pool_workers WHERE pool = $1 AND seen_at > now() - $2::interval`,
		pool, liveWindow.String()).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("pool %q: %w (start workers with TASKIEM_WORKER_POOL=%s first, or force)", pool, ErrNoWorkers, pool)
	}
	return nil
}

// AssignTenantPool routes a tenant's work (and its sub-tenants' without
// their own) to pool; "" removes its own routing, back to its partner's,
// its plan's or the shared pool. Audited in the tenant's chain. Unless
// force, a pool no live worker serves is refused, so work is never
// stranded.
func (s *Store) AssignTenantPool(ctx context.Context, tenant uuid.UUID, pool, by string, force bool) error {
	var p *string
	if pool != "" {
		if !ValidPool(pool) {
			return fmt.Errorf("%q is not a pool name (lowercase letters, digits and dashes, up to 32)", pool)
		}
		if err := s.checkLive(ctx, pool, force); err != nil {
			return err
		}
		p = &pool
	}
	return dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, tenant).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("tenant %s: %w", tenant, ErrNotFound)
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_worker_pool($1, $2, $3)`, tenant, p, by); err != nil {
			return err
		}
		action, target := "worker_pool.assign", pool
		if p == nil {
			action, target = "worker_pool.unassign", SharedPool
		}
		raw, _ := json.Marshal(map[string]any{"pool": p, "forced": force})
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'platform_admin', $2, $3, $4, $5)`, tenant, by, action, target, raw)
		return err
	})
}

// AssignPlanPool routes every tenant on a plan (that has no routing of its
// own) to pool; "" removes the plan's routing. Kept in the change log.
func (s *Store) AssignPlanPool(ctx context.Context, plan, pool, by string, force bool) error {
	var p *string
	if pool != "" {
		if !ValidPool(pool) {
			return fmt.Errorf("%q is not a pool name (lowercase letters, digits and dashes, up to 32)", pool)
		}
		if err := s.checkLive(ctx, pool, force); err != nil {
			return err
		}
		p = &pool
	}
	_, err := s.Pool.Exec(ctx, `SELECT taskiem_set_plan_worker_pool($1, $2, $3)`, plan, p, by)
	return err
}
