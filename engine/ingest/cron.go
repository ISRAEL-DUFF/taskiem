package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Cron fires schedule triggers (role "scheduler"). Each fire starts a run
// deduplicated on (workflow, scheduled time), then advances the schedule,
// so a crash between the two repeats the fire harmlessly. After downtime a
// schedule fires once for its latest missed time, not once per miss.
type Cron struct {
	Store    *runtime.Store
	Interval time.Duration
	Logger   *slog.Logger
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (c *Cron) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run fires due schedules until ctx ends.
func (c *Cron) Run(ctx context.Context) error {
	if c.Interval == 0 {
		c.Interval = time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		if _, err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.Logger.Error("schedule tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// claimBatch and claimPerTenant bound one Tick: no tenant takes more than
// claimPerTenant of a batch, so one tenant's backlog cannot starve others.
const (
	claimBatch     = 100
	claimPerTenant = 10
)

// Tick fires every schedule due now and returns how many fired. A schedule
// that fails is logged and left to its lease; the rest of the batch fires.
func (c *Cron) Tick(ctx context.Context) (int, error) {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	rows, err := c.Store.Pool.Query(ctx, `SELECT trigger_id, tenant_id FROM taskiem_claim_due_schedules($1, $2)`, claimBatch, claimPerTenant)
	if err != nil {
		return 0, err
	}
	type due struct{ id, tenant uuid.UUID }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.tenant); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		fired, err := c.fire(ctx, d.tenant, d.id)
		if err != nil {
			if ctx.Err() != nil {
				return n, err
			}
			c.Logger.Error("schedule failed to fire", "tenant", d.tenant, "trigger", d.id, "err", err)
			continue
		}
		if fired {
			n++
			telemetry.Ingest.WithLabelValues("schedule", "fired").Inc()
		}
	}
	return n, nil
}

func (c *Cron) fire(ctx context.Context, tenant, id uuid.UUID) (bool, error) {
	var wf uuid.UUID
	var version int
	var env, expr, tz string
	var at time.Time
	var resumed *time.Time
	err := db.InTenantTx(ctx, c.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT t.workflow_id, t.version, t.environment, t.cron, t.timezone, t.next_fire_at, te.resumed_at
			FROM triggers t JOIN tenants te ON te.id = t.tenant_id WHERE t.id = $1 AND t.type = 'schedule'`, id).
			Scan(&wf, &version, &env, &expr, &tz, &at, &resumed)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // replaced by a publish since it was claimed
			return false, nil
		}
		return false, err
	}
	sched, err := parseSchedule(map[string]any{"cron": expr, "timezone": tz})
	if err != nil || at.IsZero() {
		// It can never fire (registered before such schedules were refused
		// at publish): stop claiming it instead of refiring it forever.
		c.Logger.Warn("schedule disabled: it never fires", "tenant", tenant, "trigger", id, "cron", expr, "err", err)
		return false, c.disable(ctx, tenant, id)
	}
	fireAt := at.UTC().Format(time.RFC3339)
	fired := true
	if resumed != nil && at.Before(*resumed) {
		// Due while the tenant was suspended: resuming does not catch up
		// (no storm of missed fires); the schedule continues from now.
		c.Logger.Info("schedule fire skipped: missed while the tenant was suspended", "tenant", tenant, "trigger", id, "scheduled_time", fireAt)
		telemetry.Ingest.WithLabelValues("schedule", "skipped_suspended").Inc()
		fired = false
	} else if _, _, err := c.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: wf, Version: version, Environment: env,
		Trigger:   map[string]any{"type": "schedule", "scheduled_time": fireAt, "body": map[string]any{}},
		StartedBy: "schedule", TriggerID: "schedule/" + wf.String(), DedupKey: fireAt,
	}); err != nil {
		le, ok := runtime.IsLimit(err)
		if !ok {
			return false, err
		}
		// Beyond a quota or a full backlog this fire is skipped (logged,
		// counted, and seen by "limit" alert rules) and the schedule moves
		// on, rather than retrying every lease until the limit lifts.
		c.Logger.Warn("schedule fire skipped: tenant limit", "tenant", tenant, "trigger", id, "scheduled_time", fireAt, "limit", le.Limit, "err", le.Message)
		telemetry.Ingest.WithLabelValues("schedule", "refused").Inc()
		fired = false
	} else if late := c.now().Sub(at).Seconds(); late >= 0 {
		telemetry.SchedulerLateness.WithLabelValues("schedule").Observe(late)
	}
	from := c.now()
	if at.After(from) {
		from = at
	}
	next := sched.Next(from)
	if next.IsZero() {
		return fired, c.disable(ctx, tenant, id)
	}
	err = db.InTenantTx(ctx, c.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE triggers SET next_fire_at = $3, lease_until = NULL WHERE id = $1 AND next_fire_at = $2`, id, at, next)
		return err
	})
	return fired, err
}

// disable parks a schedule that has no next fire time: next_fire_at
// 'infinity' is never due. A publish replaces it.
func (c *Cron) disable(ctx context.Context, tenant, id uuid.UUID) error {
	return db.InTenantTx(ctx, c.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE triggers SET next_fire_at = 'infinity', lease_until = NULL WHERE id = $1`, id)
		return err
	})
}
