package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Subscription statuses.
const (
	StatusTrial     = "trial"
	StatusActive    = "active"
	StatusPastDue   = "past_due"
	StatusDegraded  = "degraded"
	StatusCancelled = "cancelled"
	StatusComped    = "comped"
)

// Subscription is a tenant's plan and where it is in its billing cycle.
type Subscription struct {
	TenantID          uuid.UUID  `json:"-"`
	PlanID            string     `json:"plan"`
	Interval          string     `json:"interval"`
	Status            string     `json:"status"`
	PeriodStart       time.Time  `json:"period_start"`
	PeriodEnd         time.Time  `json:"period_end"`
	TrialEnd          *time.Time `json:"trial_end,omitempty"`
	CompUntil         *time.Time `json:"comp_until,omitempty"`
	PastDueSince      *time.Time `json:"past_due_since,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	PendingPlanID     string     `json:"pending_plan,omitempty"`
	PendingInterval   string     `json:"pending_interval,omitempty"`
	CreditKobo        int64      `json:"credit_kobo"`
	// OverageBilledThrough is the last UTC month (its first day) whose
	// pass-through overage has been invoiced.
	OverageBilledThrough *time.Time `json:"-"`
	BillingEmail         string     `json:"billing_email,omitempty"`
	Provider             string     `json:"provider,omitempty"`
	AuthorizationCode    string     `json:"-"` // never leaves the server
	CardBrand            string     `json:"card_brand,omitempty"`
	CardLast4            string     `json:"card_last4,omitempty"`
	CardExp              string     `json:"card_exp,omitempty"`
	DunningAttempts      int        `json:"dunning_attempts"`
	NextActionAt         *time.Time `json:"next_action_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
}

// Due actions of the billing job.
const (
	ActNone    = ""
	ActRenew   = "renew"   // the period (or trial, or comp) ended: invoice the next one
	ActCancel  = "cancel"  // cancelled at period end
	ActDun     = "dun"     // past due: retry the card, remind the owners
	ActDegrade = "degrade" // past due beyond the grace period
)

// Due is what the billing job must do with s at now. It is pure: the
// state machine of decision 0017, tested on a fake clock.
func (s *Subscription) Due(now time.Time, c *Config) string {
	switch s.Status {
	case StatusComped:
		if s.CompUntil != nil && !now.Before(*s.CompUntil) {
			return ActRenew
		}
	case StatusTrial:
		if s.TrialEnd != nil && !now.Before(*s.TrialEnd) {
			if s.CancelAtPeriodEnd {
				return ActCancel
			}
			return ActRenew
		}
	case StatusActive:
		if !now.Before(s.PeriodEnd) {
			if s.CancelAtPeriodEnd {
				return ActCancel
			}
			return ActRenew
		}
	case StatusPastDue:
		if s.PastDueSince == nil {
			return ActDegrade
		}
		if !now.Before(s.graceEnd(c)) {
			return ActDegrade
		}
		if at, ok := s.nextDun(c); ok && !now.Before(at) {
			return ActDun
		}
	}
	return ActNone
}

func (s *Subscription) graceEnd(c *Config) time.Time {
	return s.PastDueSince.Add(time.Duration(c.GraceDays) * 24 * time.Hour)
}

// nextDun is when the next reminder (and card retry) is due.
func (s *Subscription) nextDun(c *Config) (time.Time, bool) {
	if s.PastDueSince == nil || s.DunningAttempts >= len(c.DunningDays) {
		return time.Time{}, false
	}
	return s.PastDueSince.Add(time.Duration(c.DunningDays[s.DunningAttempts]) * 24 * time.Hour), true
}

// Schedule sets NextActionAt: when Due may next return an action.
func (s *Subscription) Schedule(c *Config) {
	var at *time.Time
	set := func(t time.Time) {
		if at == nil || t.Before(*at) {
			t := t
			at = &t
		}
	}
	switch s.Status {
	case StatusComped:
		if s.CompUntil != nil {
			set(*s.CompUntil)
		}
	case StatusTrial:
		if s.TrialEnd != nil {
			set(*s.TrialEnd)
		}
	case StatusActive:
		set(s.PeriodEnd)
	case StatusPastDue:
		if s.PastDueSince != nil {
			set(s.graceEnd(c))
			if t, ok := s.nextDun(c); ok {
				set(t)
			}
		}
	}
	s.NextActionAt = at
}

// MarkPastDue starts the grace period and the dunning schedule.
func (s *Subscription) MarkPastDue(now time.Time) {
	if s.Status == StatusPastDue || s.Status == StatusDegraded {
		return
	}
	s.Status = StatusPastDue
	s.PastDueSince = &now
	s.DunningAttempts = 0
}

// Activate puts s in good standing for a paid period. A period already
// over (paid long after it was due, while degraded) starts afresh now: the
// time the tenant was degraded is not charged.
func (s *Subscription) Activate(start, end time.Time, interval string, now time.Time) {
	if !end.After(now) {
		start, end = now, PeriodEnd(now, interval)
	}
	s.Status = StatusActive
	s.PeriodStart, s.PeriodEnd, s.Interval = start, end, interval
	s.PastDueSince = nil
	s.DunningAttempts = 0
	s.CompUntil = nil
}

// Refuses reports whether s stops new runs (spec 16: degraded service).
func (s *Subscription) Refuses() bool {
	return s.Status == StatusDegraded || s.Status == StatusCancelled
}

const subCols = `tenant_id, plan_id, billing_interval, status, period_start, period_end, trial_end, comp_until, past_due_since,
	cancel_at_period_end, COALESCE(pending_plan_id, ''), COALESCE(pending_interval, ''), credit_kobo, overage_billed_through,
	COALESCE(billing_email, ''), COALESCE(provider, ''), COALESCE(authorization_code, ''), COALESCE(card_brand, ''),
	COALESCE(card_last4, ''), COALESCE(card_exp, ''), dunning_attempts, next_action_at, created_at`

func scanSub(r pgx.Row) (*Subscription, error) {
	s := &Subscription{}
	err := r.Scan(&s.TenantID, &s.PlanID, &s.Interval, &s.Status, &s.PeriodStart, &s.PeriodEnd, &s.TrialEnd, &s.CompUntil, &s.PastDueSince,
		&s.CancelAtPeriodEnd, &s.PendingPlanID, &s.PendingInterval, &s.CreditKobo, &s.OverageBilledThrough,
		&s.BillingEmail, &s.Provider, &s.AuthorizationCode, &s.CardBrand, &s.CardLast4, &s.CardExp, &s.DunningAttempts, &s.NextActionAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// loadSub reads a tenant's subscription (nil: none), locked for update
// when lock is set.
func loadSub(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, lock bool) (*Subscription, error) {
	q := `SELECT ` + subCols + ` FROM subscriptions WHERE tenant_id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanSub(tx.QueryRow(ctx, q, tenant))
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// saveSub writes s (insert or update).
func saveSub(ctx context.Context, tx pgx.Tx, s *Subscription, by string) error {
	_, err := tx.Exec(ctx, `INSERT INTO subscriptions (tenant_id, plan_id, billing_interval, status, period_start, period_end, trial_end, comp_until,
		past_due_since, cancel_at_period_end, pending_plan_id, pending_interval, credit_kobo, overage_billed_through, billing_email, provider,
		authorization_code, card_brand, card_last4, card_exp, dunning_attempts, next_action_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		ON CONFLICT (tenant_id) DO UPDATE SET plan_id = EXCLUDED.plan_id, billing_interval = EXCLUDED.billing_interval, status = EXCLUDED.status,
		period_start = EXCLUDED.period_start, period_end = EXCLUDED.period_end, trial_end = EXCLUDED.trial_end, comp_until = EXCLUDED.comp_until,
		past_due_since = EXCLUDED.past_due_since, cancel_at_period_end = EXCLUDED.cancel_at_period_end, pending_plan_id = EXCLUDED.pending_plan_id,
		pending_interval = EXCLUDED.pending_interval, credit_kobo = EXCLUDED.credit_kobo, overage_billed_through = EXCLUDED.overage_billed_through,
		billing_email = EXCLUDED.billing_email, provider = EXCLUDED.provider, authorization_code = EXCLUDED.authorization_code,
		card_brand = EXCLUDED.card_brand, card_last4 = EXCLUDED.card_last4, card_exp = EXCLUDED.card_exp, dunning_attempts = EXCLUDED.dunning_attempts,
		next_action_at = EXCLUDED.next_action_at, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		s.TenantID, s.PlanID, s.Interval, s.Status, s.PeriodStart, s.PeriodEnd, s.TrialEnd, s.CompUntil, s.PastDueSince, s.CancelAtPeriodEnd,
		nullStr(s.PendingPlanID), nullStr(s.PendingInterval), s.CreditKobo, s.OverageBilledThrough, nullStr(s.BillingEmail), nullStr(s.Provider),
		nullStr(s.AuthorizationCode), nullStr(s.CardBrand), nullStr(s.CardLast4), nullStr(s.CardExp), s.DunningAttempts, s.NextActionAt, by)
	return err
}
