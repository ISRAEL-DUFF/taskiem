package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Limits are a tenant's plan caps (spec 8.3, 16). Zero means no limit.
type Limits struct {
	// IngestRate and IngestBurst: deliveries per second that start runs at
	// once. Above it deliveries are still accepted (202) but their runs
	// wait as queued, admitted at this rate (the soft limit).
	IngestRate  float64 `json:"ingest_rate"`
	IngestBurst int     `json:"ingest_burst"`
	// IngestCeiling and IngestCeilingBurst: above this, deliveries are
	// refused with 429 and Retry-After (the hard limit).
	IngestCeiling      float64 `json:"ingest_ceiling"`
	IngestCeilingBurst int     `json:"ingest_ceiling_burst"`
	// MaxRunningRuns: runs in status running at once (runs waiting on a
	// signal, approval or timer count: they hold their place). More queue.
	MaxRunningRuns int `json:"max_running_runs"`
	// MaxQueuedRuns: the backlog; a start that would queue beyond it is
	// refused with 429.
	MaxQueuedRuns int `json:"max_queued_runs"`
	// RunsPerDay and RunsPerMonth (UTC): runs accepted, queued or not.
	// Beyond them new starts are refused (quota_exceeded).
	RunsPerDay   int64 `json:"runs_per_day"`
	RunsPerMonth int64 `json:"runs_per_month"`
	// MaxWorkflows: workflows a tenant may create.
	MaxWorkflows int `json:"max_workflows"`
	// MaxStepsPerRun: step instances one run may schedule (foreach items
	// and retries count); a run going over fails.
	MaxStepsPerRun int `json:"max_steps_per_run"`
	// WorkerConcurrency: the tenant's tasks executing at once, per queue.
	WorkerConcurrency int `json:"worker_concurrency"`
	// MaxPayloadBytes: largest webhook or connector delivery accepted.
	MaxPayloadBytes int `json:"max_payload_bytes"`
	// MaxSecrets and MaxConnections: named secrets and active connections.
	MaxSecrets     int `json:"max_secrets"`
	MaxConnections int `json:"max_connections"`
	// AIMonthlyTokens: tokens AI building may use per UTC month (spec
	// 12.3). Beyond it AI building is refused and people build by hand;
	// runs are never affected.
	AIMonthlyTokens int64 `json:"ai_monthly_tokens"`
	// WhatsAppTemplatesMonthly: WhatsApp template messages included per UTC
	// month (spec 16). Beyond it they are still sent and counted as
	// overage for pass-through billing; only marketing templates are held
	// back (docs/whatsapp.md#template-costs).
	WhatsAppTemplatesMonthly int64 `json:"whatsapp_templates_monthly"`
	// ContainerMinutesMonthly: container-step minutes per UTC month (spec
	// 7.5). Unlike the other limits, 0 means off: container steps are
	// enabled per plan. Beyond it container steps fail (limit_exceeded).
	ContainerMinutesMonthly int64 `json:"container_minutes_monthly"`
	// ContainerConcurrency: the tenant's container steps running at once
	// (enforced when tasks are claimed).
	ContainerConcurrency int `json:"container_concurrency"`
	// MaxRetentionDays caps how long an ended run's history is kept, whatever
	// its workflow or the tenant's governance settings ask for (spec 16.2).
	MaxRetentionDays int `json:"max_retention_days"`

	// The plan (when billing is on) and its subscription status: a
	// sub-tenant's are its partner's. Set by the store.
	plan, billing string

	// A sub-tenant's partner and the partner-wide run caps it shares with
	// its siblings (spec 13.1); zero for other tenants. Set by the store.
	partner                  uuid.UUID
	partnerDay, partnerMonth int64
}

// Partner is the partner of a sub-tenant whose limits these are (zero
// otherwise).
func (l Limits) Partner() uuid.UUID { return l.partner }

// Plan is the plan these limits come from ("" without billing, or without
// a subscription: the internal plan) and its subscription status.
func (l Limits) Plan() (plan, status string) { return l.plan, l.billing }

// BillingRefusesRuns reports whether the subscription is degraded (unpaid
// past its grace period) or cancelled: new runs are refused, while what is
// already running continues (approvals, signals, reconciliation).
func (l Limits) BillingRefusesRuns() bool { return l.billing == "degraded" || l.billing == "cancelled" }

// CapTo lowers every limit to at most the same limit of ceiling: where
// the ceiling has a limit, none (0) or a higher one becomes the ceiling's.
// It is how a sub-tenant's limits stay within its partner's (spec 13.1).
func (l Limits) CapTo(ceiling Limits) Limits {
	m, c := l.toMap(), ceiling.toMap()
	for k, cv := range c {
		cf, _ := cv.(float64)
		v, _ := m[k].(float64)
		if zeroIsOff[k] {
			if v > cf {
				m[k] = cf // a ceiling of 0 (off) turns it off
			}
			continue
		}
		if cf > 0 && (v <= 0 || v > cf) {
			m[k] = cf
		}
	}
	raw, _ := json.Marshal(m)
	out := l
	_ = json.Unmarshal(raw, &out)
	return out
}

// Exceeds names the limits of l above ceiling's (none, or a higher one,
// where the ceiling has a limit), sorted.
func (l Limits) Exceeds(ceiling Limits) []string {
	var out []string
	m := l.toMap()
	for k, cv := range ceiling.toMap() {
		cf, _ := cv.(float64)
		v, _ := m[k].(float64)
		if zeroIsOff[k] {
			if v > cf {
				out = append(out, k)
			}
			continue
		}
		if cf > 0 && (v <= 0 || v > cf) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// zeroIsOff are the limits whose 0 turns a feature off rather than lifting
// the limit; a partner's 0 turns it off for its sub-tenants too.
var zeroIsOff = map[string]bool{"container_minutes_monthly": true}

// LimitKeys describes every limit, in display order.
var LimitKeys = []struct{ Key, Help string }{
	{"ingest_rate", "deliveries/s that start runs at once; above it runs queue, admitted at this rate (soft limit; 0: no limit)"},
	{"ingest_burst", "deliveries the soft limit lets through at once"},
	{"ingest_ceiling", "deliveries/s above which deliveries are refused with 429 (hard limit; 0: no limit)"},
	{"ingest_ceiling_burst", "deliveries the hard limit lets through at once"},
	{"max_running_runs", "runs running at once, waiting ones included; more queue (0: no limit)"},
	{"max_queued_runs", "queued runs; a start beyond it is refused with 429 (0: no limit)"},
	{"runs_per_day", "runs started per UTC day; more are refused (0: no limit)"},
	{"runs_per_month", "runs started per UTC month; more are refused (0: no limit)"},
	{"max_workflows", "workflows (0: no limit)"},
	{"max_steps_per_run", "steps one run may schedule; a run going over fails (0: no limit)"},
	{"worker_concurrency", "tasks executing at once per queue (0: no limit)"},
	{"max_payload_bytes", "largest webhook body accepted (0: the platform maximum)"},
	{"max_secrets", "named secrets (0: no limit)"},
	{"max_connections", "active connections (0: no limit)"},
	{"ai_monthly_tokens", "tokens AI building may use per UTC month; beyond it, build by hand (0: no limit)"},
	{"whatsapp_templates_monthly", "WhatsApp template messages included per UTC month; beyond it they are counted as overage, and only marketing ones are held back (0: no limit)"},
	{"container_minutes_monthly", "container-step minutes per UTC month; beyond it container steps fail (0: container steps are off)"},
	{"container_concurrency", "container steps running at once (0: the platform default)"},
	{"max_retention_days", "days an ended run's history is kept at most, whatever a workflow asks (0: no cap)"},
}

// DefaultContainerConcurrency is the platform's default cap on one
// tenant's container steps running at once.
const DefaultContainerConcurrency = 2

// DefaultWhatsAppTemplatesMonthly is the platform's default monthly
// allowance of WhatsApp template messages.
const DefaultWhatsAppTemplatesMonthly = 1000

// DefaultAIMonthlyTokens is the platform's default monthly AI budget: a
// few dozen builds with self-correction on a frontier model.
const DefaultAIMonthlyTokens = 2_000_000

// MaxPayload is the largest delivery any tenant may be allowed.
const MaxPayload = 10 << 20

// DefaultLimits are the platform defaults when no TASKIEM_DEFAULT_* is set.
func DefaultLimits() Limits {
	return Limits{
		IngestRate: 20, IngestBurst: 100,
		IngestCeiling: 50, IngestCeilingBurst: 200,
		MaxQueuedRuns:     10000,
		MaxStepsPerRun:    100000,
		WorkerConcurrency: 32,
		MaxPayloadBytes:   1 << 20,
		AIMonthlyTokens:   DefaultAIMonthlyTokens,

		WhatsAppTemplatesMonthly: DefaultWhatsAppTemplatesMonthly,
		ContainerConcurrency:     DefaultContainerConcurrency,
	}
}

func (l Limits) toMap() map[string]any {
	raw, _ := json.Marshal(l)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// With returns l with non-null overrides applied.
func (l Limits) With(over map[string]any) (Limits, error) {
	m := l.toMap()
	for k, v := range over {
		if v != nil {
			m[k] = v
		}
	}
	raw, _ := json.Marshal(m)
	var out Limits
	if err := json.Unmarshal(raw, &out); err != nil {
		return l, err
	}
	return out, nil
}

// ParseLimit parses a limit's value as an operator writes it: a number, or
// "default" (or nothing) for the platform default (nil).
func ParseLimit(key, s string) (any, error) {
	known := false
	for _, k := range LimitKeys {
		known = known || k.Key == key
	}
	if !known {
		keys := make([]string, len(LimitKeys))
		for i, k := range LimitKeys {
			keys[i] = k.Key
		}
		return nil, fmt.Errorf("unknown limit %q (one of %s)", key, strings.Join(keys, ", "))
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "default" {
		return nil, nil
	}
	if key == "ingest_rate" || key == "ingest_ceiling" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("%s: %q is not a non-negative number", key, s)
		}
		return f, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || (key != "runs_per_day" && key != "runs_per_month" && key != "ai_monthly_tokens" && key != "whatsapp_templates_monthly" && key != "container_minutes_monthly" && n > math.MaxInt32) {
		return nil, fmt.Errorf("%s: %q is not a non-negative whole number", key, s)
	}
	if key == "max_payload_bytes" && n > MaxPayload {
		return nil, fmt.Errorf("max_payload_bytes: at most %d", MaxPayload)
	}
	return n, nil
}

// LimitsFromEnv reads platform defaults from TASKIEM_DEFAULT_<KEY>.
func LimitsFromEnv(lookup func(string) (string, bool)) (Limits, error) {
	over := map[string]any{}
	for _, k := range LimitKeys {
		name := "TASKIEM_DEFAULT_" + strings.ToUpper(k.Key)
		s, ok := lookup(name)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		v, err := ParseLimit(k.Key, s)
		if err != nil {
			return Limits{}, fmt.Errorf("%s: %w", name, err)
		}
		over[k.Key] = v
	}
	return DefaultLimits().With(over)
}

// LimitError refuses work beyond a limit. Code is what clients see.
type LimitError struct {
	Limit      string // a LimitKeys key
	Code       string // quota_exceeded, backlog_full, limit_exceeded
	Message    string
	RetryAfter time.Duration // zero: retrying will not help until the limit changes
}

func (e *LimitError) Error() string { return e.Message }

// IsLimit reports whether err is a LimitError, and returns it.
func IsLimit(err error) (*LimitError, bool) {
	var le *LimitError
	ok := errors.As(err, &le)
	return le, ok
}

// limitsTTL is how long a tenant's limits are cached in a process; an
// operator's change takes effect within it.
const limitsTTL = 30 * time.Second

type cachedLimits struct {
	l  Limits
	at time.Time
}

func (s *Store) defaults() Limits {
	if s.Defaults != nil {
		return *s.Defaults
	}
	return DefaultLimits()
}

// PlatformLimits are the defaults tenants without overrides get.
func (s *Store) PlatformLimits() Limits { return s.defaults() }

// LimitsFor returns a tenant's effective limits, cached briefly.
func (s *Store) LimitsFor(ctx context.Context, tenant uuid.UUID) (Limits, error) {
	if c, ok := s.limits.Load(tenant); ok && time.Since(c.(cachedLimits).at) < limitsTTL {
		return c.(cachedLimits).l, nil
	}
	var l Limits
	err := dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		var err error
		l, err = s.limitsTx(ctx, tx, tenant)
		return err
	})
	return l, err
}

func (s *Store) limitsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Limits, error) {
	if c, ok := s.limits.Load(tenant); ok && time.Since(c.(cachedLimits).at) < limitsTTL {
		return c.(cachedLimits).l, nil
	}
	over, err := overrides(ctx, tx, tenant)
	if err != nil {
		return Limits{}, err
	}
	l, err := s.effective(ctx, tx, tenant, over)
	if err != nil {
		return l, err
	}
	s.limits.Store(tenant, cachedLimits{l: l, at: time.Now()})
	return l, nil
}

// ForgetLimits drops a tenant's cached limits in this process.
func (s *Store) ForgetLimits(tenant uuid.UUID) { s.limits.Delete(tenant) }

// overrides reads a tenant's own limits (non-null columns only).
func overrides(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[string]any, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(l) - 'tenant_id' - 'updated_by' - 'updated_at' FROM tenant_limits l WHERE tenant_id = $1`, tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		if v == nil {
			delete(m, k)
		}
	}
	return m, nil
}

// effective applies a tenant's own overrides to its base: the platform
// defaults, or for a sub-tenant its partner's effective limits, which its
// own can only lower (spec 13.1).
func (s *Store) effective(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, over map[string]any) (Limits, error) {
	plan, err := s.planOf(ctx, tx, tenant)
	if err != nil {
		return Limits{}, err
	}
	defaults, err := s.defaults().With(plan.limits)
	if err != nil {
		return Limits{}, err
	}
	var parent uuid.UUID
	var parentOver []byte
	var day, month int64
	err = tx.QueryRow(ctx, `SELECT parent_id, overrides, runs_per_day, runs_per_month FROM taskiem_parent_limits($1)`, tenant).Scan(&parent, &parentOver, &day, &month)
	if errors.Is(err, pgx.ErrNoRows) {
		l, err := defaults.With(over)
		l.plan, l.billing = plan.id, plan.status
		return l, err
	}
	if err != nil {
		return Limits{}, err
	}
	po := map[string]any{}
	if err := json.Unmarshal(parentOver, &po); err != nil {
		return Limits{}, err
	}
	base, err := defaults.With(po)
	if err != nil {
		return base, err
	}
	l, err := base.With(over)
	if err != nil {
		return l, err
	}
	l = l.CapTo(base)
	// Partner-wide caps an operator set win; otherwise the plan's (0: none).
	if day <= 0 {
		day = plan.partnerDay
	}
	if month <= 0 {
		month = plan.partnerMonth
	}
	l.partner, l.partnerDay, l.partnerMonth = parent, day, month
	l.plan, l.billing = plan.id, plan.status
	return l, nil
}

// tenantPlan is the plan whose limits are a tenant's base (spec 16).
type tenantPlan struct {
	id, status               string
	limits                   map[string]any
	partnerDay, partnerMonth int64
}

// planOf reads a tenant's plan (a sub-tenant's partner's) when billing is
// on. Without billing, or without a subscription, it is the internal plan:
// the platform defaults, no status.
func (s *Store) planOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (tenantPlan, error) {
	p := tenantPlan{limits: map[string]any{}}
	if !s.Billing {
		return p, nil
	}
	var limits, partner []byte
	err := tx.QueryRow(ctx, `SELECT plan_id, status, limits, partner FROM taskiem_tenant_plan($1)`, tenant).Scan(&p.id, &p.status, &limits, &partner)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(limits, &p.limits); err != nil {
		return p, err
	}
	var caps struct {
		Day   int64 `json:"subtenant_runs_per_day"`
		Month int64 `json:"subtenant_runs_per_month"`
	}
	if err := json.Unmarshal(partner, &caps); err != nil {
		return p, err
	}
	p.partnerDay, p.partnerMonth = caps.Day, caps.Month
	return p, nil
}

// LimitsView is what a tenant (GET /v1/limits) and an operator see.
type LimitsView struct {
	Limits    Limits         `json:"limits"`
	Overrides map[string]any `json:"overrides"` // set for this tenant; the rest are platform defaults
	Usage     Usage          `json:"usage"`
	Hits      []LimitHit     `json:"recent_hits"`
}

// Usage is a tenant's current consumption of its limits.
type Usage struct {
	RunsToday     int64          `json:"runs_today"`
	RunsThisMonth int64          `json:"runs_this_month"`
	RunningRuns   int64          `json:"running_runs"`
	QueuedRuns    int64          `json:"queued_runs"`
	Workflows     int64          `json:"workflows"`
	Secrets       int64          `json:"secrets"`
	Connections   int64          `json:"connections"`
	AITokens      int64          `json:"ai_tokens_this_month"`
	TasksInFlight map[string]int `json:"tasks_in_flight"` // per queue
	// WhatsApp template messages this UTC month (spec 16).
	WhatsAppTemplates WhatsAppTemplateUsage `json:"whatsapp_templates_this_month"`
	// Container-step time this UTC month, in seconds (spec 7.5).
	ContainerSeconds int64 `json:"container_seconds_this_month"`
}

// WhatsAppTemplateUsage is a month's WhatsApp template messages: sent by
// Meta's category, how many of them were beyond the allowance (overage,
// billed through), and how many were held back.
type WhatsAppTemplateUsage struct {
	Sent       int64            `json:"sent"`
	ByCategory map[string]int64 `json:"by_category"`
	Overage    int64            `json:"overage"`
	Blocked    int64            `json:"blocked"`
}

// WhatsAppTemplatesThisMonth reads a tenant's template usage this UTC month.
func WhatsAppTemplatesThisMonth(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (WhatsAppTemplateUsage, error) {
	u := WhatsAppTemplateUsage{ByCategory: map[string]int64{}}
	rows, err := tx.Query(ctx, `SELECT category, sent, over_allowance, blocked FROM whatsapp_template_usage
		WHERE tenant_id = $1 AND month = date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenant)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var sent, over, blocked int64
		if err := rows.Scan(&c, &sent, &over, &blocked); err != nil {
			return u, err
		}
		u.ByCategory[c] = sent
		u.Sent += sent
		u.Overage += over
		u.Blocked += blocked
	}
	return u, rows.Err()
}

// LimitHit is a limit reached on a day.
type LimitHit struct {
	Limit  string    `json:"limit"`
	Day    string    `json:"day"`
	Hits   int64     `json:"hits"`
	LastAt time.Time `json:"last_at"`
}

// ViewLimits reads a tenant's limits, usage and recent hits, uncached.
func (s *Store) ViewLimits(ctx context.Context, tenant uuid.UUID) (LimitsView, error) {
	var v LimitsView
	err := dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		over, err := overrides(ctx, tx, tenant)
		if err != nil {
			return err
		}
		v.Overrides = over
		if v.Limits, err = s.effective(ctx, tx, tenant, over); err != nil {
			return err
		}
		u := &v.Usage
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(runs_started) FILTER (WHERE day = (now() AT TIME ZONE 'UTC')::date), 0)::bigint,
			COALESCE(sum(runs_started), 0)::bigint FROM tenant_usage
			WHERE tenant_id = $1 AND day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenant).Scan(&u.RunsToday, &u.RunsThisMonth); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM runs WHERE tenant_id = $1 AND status = 'running'),
			(SELECT count(*) FROM runs WHERE tenant_id = $1 AND status = 'queued'),
			(SELECT count(*) FROM workflows WHERE tenant_id = $1),
			(SELECT count(*) FROM secrets WHERE tenant_id = $1 AND name IS NOT NULL),
			(SELECT count(*) FROM connections WHERE tenant_id = $1 AND status = 'active')`, tenant).
			Scan(&u.RunningRuns, &u.QueuedRuns, &u.Workflows, &u.Secrets, &u.Connections); err != nil {
			return err
		}
		if u.AITokens, err = AITokensThisMonth(ctx, tx, tenant); err != nil {
			return err
		}
		if u.WhatsAppTemplates, err = WhatsAppTemplatesThisMonth(ctx, tx, tenant); err != nil {
			return err
		}
		if u.ContainerSeconds, err = ContainerSecondsThisMonth(ctx, tx, tenant); err != nil {
			return err
		}
		u.TasksInFlight = map[string]int{}
		rows, err := tx.Query(ctx, `SELECT queue, count(*) FROM tasks WHERE tenant_id = $1 AND lease_owner IS NOT NULL GROUP BY queue`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var q string
			var n int
			if err := rows.Scan(&q, &n); err != nil {
				rows.Close()
				return err
			}
			u.TasksInFlight[q] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT limit_name, day::text, hits, last_at FROM tenant_limit_hits
			WHERE tenant_id = $1 AND day > (now() AT TIME ZONE 'UTC')::date - 30 ORDER BY last_at DESC LIMIT 50`, tenant)
		if err != nil {
			return err
		}
		v.Hits, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (LimitHit, error) {
			var h LimitHit
			return h, r.Scan(&h.Limit, &h.Day, &h.Hits, &h.LastAt)
		})
		return err
	})
	if v.Hits == nil {
		v.Hits = []LimitHit{}
	}
	return v, err
}

// AITokensThisMonth is what a tenant's AI building used this UTC month.
func AITokensThisMonth(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_tokens), 0)::bigint FROM ai_interactions
		WHERE tenant_id = $1 AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, tenant).Scan(&n)
	return n, err
}

// SetLimits changes a tenant's limits (operators only: no API calls it).
// A nil value returns that limit to the platform default. It is audited.
func (s *Store) SetLimits(ctx context.Context, tenant uuid.UUID, values map[string]any, by string) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	err = dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, tenant).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("tenant %s: %w", tenant, ErrNotFound)
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, $2, $3)`, tenant, raw, by); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, 'limits.set', $3, $4)`, tenant, by, strings.Join(keys, ","), raw)
		return err
	})
	s.ForgetLimits(tenant)
	return err
}

// hitEvery is how often one process records the same tenant's limit hit
// in the database; the metric counts every one.
const hitEvery = time.Minute

// LimitHit counts a limit being reached: always in the metric, and at most
// once a minute per tenant and limit in tenant_limit_hits, where alert
// rules of kind "limit" and GET /v1/limits see it.
func (s *Store) LimitHit(ctx context.Context, tenant uuid.UUID, limit string) {
	telemetry.LimitHits.WithLabelValues(limit).Inc()
	key := tenant.String() + "/" + limit
	now := time.Now()
	if last, ok := s.hits.Load(key); ok && now.Sub(last.(time.Time)) < hitEvery {
		return
	}
	s.hits.Store(key, now)
	if s.hitCount.Add(1)%1000 == 0 { // keep the map to recent keys
		s.hits.Range(func(k, v any) bool {
			if now.Sub(v.(time.Time)) > hitEvery {
				s.hits.Delete(k)
			}
			return true
		})
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tenant_limit_hits (tenant_id, limit_name, day) VALUES ($1, $2, (now() AT TIME ZONE 'UTC')::date)
			ON CONFLICT (tenant_id, limit_name, day) DO UPDATE SET hits = tenant_limit_hits.hits + 1, last_at = now()`, tenant, limit)
		return err
	})
}

// untilNextDay and untilNextMonth are Retry-After for the quotas (UTC).
func untilNextDay(now time.Time) time.Duration {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Sub(now)
}

func untilNextMonth(now time.Time) time.Duration {
	now = now.UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(now)
}

// checkQuota refuses a start beyond the day's or month's quota.
func checkQuota(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, l Limits) error {
	if err := checkPartnerQuota(ctx, tx, l); err != nil {
		return err
	}
	if l.RunsPerDay <= 0 && l.RunsPerMonth <= 0 {
		return nil
	}
	var today, month int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(runs_started) FILTER (WHERE day = (now() AT TIME ZONE 'UTC')::date), 0)::bigint,
		COALESCE(sum(runs_started), 0)::bigint FROM tenant_usage
		WHERE tenant_id = $1 AND day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenant).Scan(&today, &month); err != nil {
		return err
	}
	now := time.Now()
	if l.RunsPerMonth > 0 && month >= l.RunsPerMonth {
		return &LimitError{Limit: "runs_per_month", Code: "quota_exceeded", RetryAfter: untilNextMonth(now),
			Message: fmt.Sprintf("this month's run quota (%d) is used up; new runs start again next month or when the plan's quota is raised", l.RunsPerMonth)}
	}
	if l.RunsPerDay > 0 && today >= l.RunsPerDay {
		return &LimitError{Limit: "runs_per_day", Code: "quota_exceeded", RetryAfter: untilNextDay(now),
			Message: fmt.Sprintf("today's run quota (%d) is used up; new runs start again tomorrow (UTC) or when the plan's quota is raised", l.RunsPerDay)}
	}
	return nil
}

// checkPartnerQuota refuses a sub-tenant's start beyond its partner's
// partner-wide run caps, which all the partner's sub-tenants share.
func checkPartnerQuota(ctx context.Context, tx pgx.Tx, l Limits) error {
	if l.partner == uuid.Nil || (l.partnerDay <= 0 && l.partnerMonth <= 0) {
		return nil
	}
	var today, month int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(runs_today), 0)::bigint, COALESCE(sum(runs_month), 0)::bigint FROM taskiem_partner_usage($1)`, l.partner).
		Scan(&today, &month); err != nil {
		return err
	}
	now := time.Now()
	if l.partnerMonth > 0 && month >= l.partnerMonth {
		return &LimitError{Limit: "runs_per_month", Code: "quota_exceeded", RetryAfter: untilNextMonth(now),
			Message: fmt.Sprintf("this month's run quota shared by the partner's customers (%d) is used up; new runs start again next month", l.partnerMonth)}
	}
	if l.partnerDay > 0 && today >= l.partnerDay {
		return &LimitError{Limit: "runs_per_day", Code: "quota_exceeded", RetryAfter: untilNextDay(now),
			Message: fmt.Sprintf("today's run quota shared by the partner's customers (%d) is used up; new runs start again tomorrow (UTC)", l.partnerDay)}
	}
	return nil
}

// countUpTo counts rows of a query, stopping at max (0: does not count).
func countUpTo(ctx context.Context, tx pgx.Tx, from string, max int, args ...any) (int, error) {
	if max <= 0 {
		return 0, nil
	}
	var n int
	args = append(args, max)
	err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM %s LIMIT $%d) c`, from, len(args)), args...).Scan(&n)
	return n, err
}

// checkBacklog refuses to queue a run beyond the tenant's backlog cap.
func checkBacklog(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, l Limits) error {
	n, err := countUpTo(ctx, tx, `runs WHERE tenant_id = $1 AND status = 'queued'`, l.MaxQueuedRuns, tenant)
	if err != nil {
		return err
	}
	if l.MaxQueuedRuns > 0 && n >= l.MaxQueuedRuns {
		return &LimitError{Limit: "max_queued_runs", Code: "backlog_full", RetryAfter: 30 * time.Second,
			Message: fmt.Sprintf("%d runs are already queued, the most this plan allows; retry once they have started", l.MaxQueuedRuns)}
	}
	return nil
}

// CheckCount refuses creating one more of something a tenant has max of
// (workflows, secrets, connections); max 0 is no limit.
func CheckCount(ctx context.Context, tx pgx.Tx, limit, from string, max int, args ...any) error {
	n, err := countUpTo(ctx, tx, from, max, args...)
	if err != nil {
		return err
	}
	if max > 0 && n >= max {
		return &LimitError{Limit: limit, Code: "limit_exceeded", Message: fmt.Sprintf("this plan allows at most %d (%s)", max, limit)}
	}
	return nil
}
