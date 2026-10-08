package billing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Snapshot is a tenant's usage on one UTC day against its limits (spec
// 16.3). Month-to-date counters (WhatsApp, AI) are as of the snapshot.
type Snapshot struct {
	Day               string         `json:"day"`
	Plan              string         `json:"plan,omitempty"`
	RunsStarted       int64          `json:"runs_started"`
	Steps             int64          `json:"steps"`
	ActiveWorkflows   int64          `json:"active_workflows"`
	RunningRuns       int64          `json:"running_runs"`
	StoredRuns        int64          `json:"stored_runs"`
	WhatsAppTemplates int64          `json:"whatsapp_templates"`
	WhatsAppOverage   int64          `json:"whatsapp_overage"`
	AITokens          int64          `json:"ai_tokens"`
	Limits            map[string]any `json:"limits,omitempty"`
}

// SnapshotAll snapshots every live tenant for today, and for yesterday on
// the first run of a day (so each day's row ends complete).
func (s *Service) SnapshotAll(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_billing_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	now := s.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var errs []error
	for _, t := range tenants {
		if now.Hour() == 0 {
			errs = append(errs, s.SnapshotTenant(ctx, t, today.AddDate(0, 0, -1)))
		}
		errs = append(errs, s.SnapshotTenant(ctx, t, today))
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
}

// SnapshotTenant writes (or rewrites) a tenant's snapshot of day.
func (s *Service) SnapshotTenant(ctx context.Context, tenant uuid.UUID, day time.Time) error {
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return err
	}
	plan, _ := lim.Plan()
	if plan == "" {
		plan = InternalPlanID
	}
	limits, _ := json.Marshal(lim)
	month := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := day.AddDate(0, 0, 1)
	return s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var sn Snapshot
		err := tx.QueryRow(ctx, `SELECT
			COALESCE((SELECT runs_started FROM tenant_usage WHERE tenant_id = $1 AND day = $2::date), 0),
			(SELECT count(*) FROM run_events e JOIN runs r ON r.id = e.run_id AND r.tenant_id = e.tenant_id
			  WHERE r.tenant_id = $1 AND r.started_at < $3 AND (r.ended_at IS NULL OR r.ended_at >= $2)
			    AND e.type = 'StepScheduled' AND e.recorded_at >= $2 AND e.recorded_at < $3),
			(SELECT count(DISTINCT workflow_id) FROM deployments WHERE tenant_id = $1),
			(SELECT count(*) FROM runs WHERE tenant_id = $1 AND status = 'running'),
			(SELECT count(*) FROM runs WHERE tenant_id = $1),
			COALESCE((SELECT sum(sent) FROM whatsapp_template_usage WHERE tenant_id = $1 AND month = $4::date), 0)::bigint,
			COALESCE((SELECT sum(over_allowance) FROM whatsapp_template_usage WHERE tenant_id = $1 AND month = $4::date), 0)::bigint,
			COALESCE((SELECT sum(total_tokens) FROM ai_interactions WHERE tenant_id = $1 AND created_at >= $4 AND created_at < $3), 0)::bigint`,
			tenant, day, next, month).Scan(&sn.RunsStarted, &sn.Steps, &sn.ActiveWorkflows, &sn.RunningRuns, &sn.StoredRuns,
			&sn.WhatsAppTemplates, &sn.WhatsAppOverage, &sn.AITokens)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO usage_snapshots (tenant_id, day, plan_id, runs_started, steps, active_workflows, running_runs, stored_runs,
			whatsapp_templates, whatsapp_overage, ai_tokens, limits, taken_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
			ON CONFLICT (tenant_id, day) DO UPDATE SET plan_id = EXCLUDED.plan_id, runs_started = EXCLUDED.runs_started, steps = EXCLUDED.steps,
			active_workflows = EXCLUDED.active_workflows, running_runs = EXCLUDED.running_runs, stored_runs = EXCLUDED.stored_runs,
			whatsapp_templates = EXCLUDED.whatsapp_templates, whatsapp_overage = EXCLUDED.whatsapp_overage, ai_tokens = EXCLUDED.ai_tokens,
			limits = EXCLUDED.limits, taken_at = now()`,
			tenant, day, plan, sn.RunsStarted, sn.Steps, sn.ActiveWorkflows, sn.RunningRuns, sn.StoredRuns, sn.WhatsAppTemplates, sn.WhatsAppOverage, sn.AITokens, limits)
		return err
	})
}

// Snapshots reads a tenant's last days of snapshots, oldest first.
func Snapshots(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, days int) ([]Snapshot, error) {
	rows, err := tx.Query(ctx, `SELECT day::text, COALESCE(plan_id, ''), runs_started, steps, active_workflows, running_runs, stored_runs,
		whatsapp_templates, whatsapp_overage, ai_tokens, limits FROM (
		  SELECT * FROM usage_snapshots WHERE tenant_id = $1 ORDER BY day DESC LIMIT $2) s ORDER BY day`, tenant, days)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Snapshot, error) {
		var sn Snapshot
		var limits []byte
		if err := r.Scan(&sn.Day, &sn.Plan, &sn.RunsStarted, &sn.Steps, &sn.ActiveWorkflows, &sn.RunningRuns, &sn.StoredRuns,
			&sn.WhatsAppTemplates, &sn.WhatsAppOverage, &sn.AITokens, &limits); err != nil {
			return sn, err
		}
		return sn, json.Unmarshal(limits, &sn.Limits)
	})
	if out == nil {
		out = []Snapshot{}
	}
	return out, err
}

// PartnerUsage is a partner's sub-tenants' usage on one day, summed.
type PartnerUsage struct {
	Day        string `json:"day"`
	SubTenants int64  `json:"subtenants"`
	Snapshot
}

// PartnerAggregate sums a partner's sub-tenants' snapshots per day (none
// for other tenants). Counts only: no sub-tenant data.
func PartnerAggregate(ctx context.Context, tx pgx.Tx, partner uuid.UUID, from, to time.Time) ([]PartnerUsage, error) {
	rows, err := tx.Query(ctx, `SELECT day::text, subtenants, runs_started, steps, active_workflows, running_runs, stored_runs,
		whatsapp_templates, whatsapp_overage, ai_tokens FROM taskiem_partner_usage_snapshots($1, $2::date, $3::date)`, partner, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PartnerUsage, error) {
		var p PartnerUsage
		err := r.Scan(&p.Day, &p.SubTenants, &p.RunsStarted, &p.Steps, &p.ActiveWorkflows, &p.RunningRuns, &p.StoredRuns,
			&p.WhatsAppTemplates, &p.WhatsAppOverage, &p.AITokens)
		p.Snapshot.Day = p.Day
		return p, err
	})
}
