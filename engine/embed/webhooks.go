package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// VaultEnv is the partner's vault environment holding its apps' webhook
// signing secrets.
const VaultEnv = "_embed"

// SecretName is the vault name of an app's webhook signing secret.
func SecretName(app uuid.UUID) string { return "app_" + app.String() + "_webhook" }

// PurposeWebhookDeliver is recorded with each read of a signing secret.
const PurposeWebhookDeliver = "partner.webhook.deliver"

// MaxAttempts bounds a delivery's attempts; the delay between them grows
// from 30 seconds to six hours (about a day in all).
const MaxAttempts = 10

// Backoff is the delay after a delivery's n-th failed attempt.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		return 6 * time.Hour
	}
	return min(30*time.Second<<(2*(attempt-1)), 6*time.Hour) // 30s, 2m, 8m, 32m, ~2h, then 6h
}

// Webhooks queues usage events and delivers every partner's webhooks (the
// scheduler role runs it). Run and publish events are queued by database
// triggers (migration 00047).
type Webhooks struct {
	Pool    *pgxpool.Pool
	Secrets interface {
		Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error)
	}
	// Limits gives a tenant's effective limits (runtime.Store.LimitsFor),
	// for usage.threshold events.
	Limits   func(ctx context.Context, tenant uuid.UUID) (runtime.Limits, error)
	Egress   *egress.Guard
	Interval time.Duration // default 15s
	Logger   *slog.Logger
	// Client, when set, replaces the egress-guarded client (tests).
	Client *http.Client
	// Now is the clock (tests).
	Now func() time.Time
}

func (h *Webhooks) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Webhooks) log() *slog.Logger {
	if h.Logger == nil {
		return slog.Default()
	}
	return h.Logger
}

// Run ticks until ctx ends.
func (h *Webhooks) Run(ctx context.Context) error {
	every := h.Interval
	if every <= 0 {
		every = 15 * time.Second
	}
	for {
		if err := h.Tick(ctx); err != nil && ctx.Err() == nil {
			h.log().Error("partner webhooks", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick queues usage events and delivers what is due, for every partner.
func (h *Webhooks) Tick(ctx context.Context) error {
	rows, err := h.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_partner_tenants()`)
	if err != nil {
		return err
	}
	partners, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range partners {
		if h.Limits != nil {
			if err := h.Thresholds(ctx, p); err != nil {
				errs = append(errs, fmt.Errorf("partner %s: usage: %w", p, err))
			}
		}
		if err := h.Deliver(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("partner %s: deliver: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// Percents at which usage.threshold events are sent, once per period.
var thresholdPercents = []int64{80, 100}

type usageEvent struct {
	sub     *uuid.UUID
	limit   string
	period  string
	used    int64
	cap     int64
	percent int64
}

// Thresholds queues usage.threshold events for a partner: each sub-tenant's
// runs against its own day and month quotas, and all of them against the
// partner-wide caps, at 80% and 100%, once per period.
func (h *Webhooks) Thresholds(ctx context.Context, partner uuid.UUID) error {
	type usage struct {
		sub          uuid.UUID
		today, month int64
	}
	var subs []usage
	var capDay, capMonth int64
	err := db.InTenantTx(ctx, h.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT subtenant_runs_per_day, subtenant_runs_per_month FROM partners WHERE tenant_id = $1`, partner).Scan(&capDay, &capMonth); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT tenant_id, runs_today, runs_month FROM taskiem_partner_usage($1)`, partner)
		if err != nil {
			return err
		}
		subs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (usage, error) {
			var u usage
			return u, r.Scan(&u.sub, &u.today, &u.month)
		})
		return err
	})
	if err != nil {
		return err
	}
	now := h.now().UTC()
	day, month := now.Format("2006-01-02"), now.Format("2006-01")
	var events []usageEvent
	check := func(sub *uuid.UUID, limit, period string, used, limitCap int64) {
		if limitCap <= 0 {
			return
		}
		for _, pct := range thresholdPercents {
			if used*100 >= pct*limitCap {
				events = append(events, usageEvent{sub: sub, limit: limit, period: period, used: used, cap: limitCap, percent: pct})
			}
		}
	}
	var totalDay, totalMonth int64
	for i := range subs {
		u := &subs[i]
		totalDay += u.today
		totalMonth += u.month
		if u.today == 0 && u.month == 0 {
			continue
		}
		lim, err := h.Limits(ctx, u.sub)
		if err != nil {
			return err
		}
		check(&u.sub, "runs_per_day", day, u.today, lim.RunsPerDay)
		check(&u.sub, "runs_per_month", month, u.month, lim.RunsPerMonth)
	}
	check(nil, "subtenant_runs_per_day", day, totalDay, capDay)
	check(nil, "subtenant_runs_per_month", month, totalMonth, capMonth)
	if len(events) == 0 {
		return nil
	}
	return db.InTenantTx(ctx, h.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		for _, e := range events {
			scope, key := "partner", "partner"
			if e.sub != nil {
				scope, key = "sub_tenant", e.sub.String()
			}
			payload, _ := json.Marshal(map[string]any{"usage": map[string]any{"scope": scope, "limit": e.limit, "period": e.period,
				"used": e.used, "cap": e.cap, "percent": e.percent}})
			dedup := fmt.Sprintf("usage.threshold:%s:%s:%s:%d", key, e.limit, e.period, e.percent)
			if _, err := tx.Exec(ctx, `INSERT INTO partner_webhook_deliveries (id, tenant_id, app_id, event, sub_tenant_id, dedup_key, payload)
				SELECT gen_random_uuid(), a.tenant_id, a.id, 'usage.threshold', $2, $3, $4 FROM embed_apps a
				 WHERE a.tenant_id = $1 AND a.status = 'active' AND a.webhook_url IS NOT NULL AND 'usage.threshold' = ANY (a.webhook_events)
				ON CONFLICT (app_id, dedup_key) DO NOTHING`, partner, e.sub, dedup, payload); err != nil {
				return err
			}
		}
		return nil
	})
}

type delivery struct {
	id        uuid.UUID
	app       uuid.UUID
	event     string
	sub       *uuid.UUID
	payload   json.RawMessage
	createdAt time.Time
	attempts  int
}

// Message is the body of a partner webhook.
type Message struct {
	ID          uuid.UUID       `json:"id"`
	Event       string          `json:"event"`
	CreatedAt   time.Time       `json:"created_at"`
	SubTenantID *uuid.UUID      `json:"sub_tenant_id"`
	Data        json.RawMessage `json:"data"`
}

// Deliver sends a partner's due deliveries. Each is claimed first (so two
// schedulers never send one at once) and sent outside any transaction.
func (h *Webhooks) Deliver(ctx context.Context, partner uuid.UUID) error {
	var due []delivery
	err := db.InTenantTx(ctx, h.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE partner_webhook_deliveries d SET next_attempt_at = now() + interval '5 minutes', attempts = d.attempts + 1
			WHERE d.id IN (SELECT id FROM partner_webhook_deliveries WHERE tenant_id = $1 AND status = 'pending' AND next_attempt_at <= now()
			  ORDER BY next_attempt_at LIMIT 50 FOR UPDATE SKIP LOCKED)
			RETURNING d.id, d.app_id, d.event, d.sub_tenant_id, d.payload, d.created_at, d.attempts`, partner)
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (delivery, error) {
			var d delivery
			return d, r.Scan(&d.id, &d.app, &d.event, &d.sub, &d.payload, &d.createdAt, &d.attempts)
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, d := range due {
		code, sendErr := h.send(ctx, partner, d)
		err := db.InTenantTx(ctx, h.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
			var status *int
			if code > 0 {
				status = &code
			}
			switch {
			case sendErr == nil:
				_, err := tx.Exec(ctx, `UPDATE partner_webhook_deliveries SET status = 'delivered', delivered_at = now(), last_status = $2, last_error = NULL WHERE id = $1`, d.id, status)
				return err
			case d.attempts >= MaxAttempts:
				_, err := tx.Exec(ctx, `UPDATE partner_webhook_deliveries SET status = 'failed', last_status = $2, last_error = $3 WHERE id = $1`, d.id, status, truncate(sendErr.Error()))
				return err
			default:
				_, err := tx.Exec(ctx, `UPDATE partner_webhook_deliveries SET last_status = $2, last_error = $3, next_attempt_at = now() + make_interval(secs => $4) WHERE id = $1`,
					d.id, status, truncate(sendErr.Error()), Backoff(d.attempts).Seconds())
				return err
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// send posts one delivery, returning the endpoint's status (0 if none).
func (h *Webhooks) send(ctx context.Context, partner uuid.UUID, d delivery) (int, error) {
	var dest string
	var active bool
	err := db.InTenantTx(ctx, h.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		var u *string
		var status string
		if err := tx.QueryRow(ctx, `SELECT webhook_url, status FROM embed_apps WHERE id = $1`, d.app).Scan(&u, &status); err != nil {
			return err
		}
		if u != nil {
			dest = *u
		}
		active = status == "active"
		return nil
	})
	if err != nil {
		return 0, err
	}
	if dest == "" || !active {
		return 0, errors.New("the app has no webhook URL or is disabled")
	}
	key, err := h.Secrets.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindWebhook, Purpose: PurposeWebhookDeliver}), partner, VaultEnv, SecretName(d.app))
	if err != nil {
		return 0, fmt.Errorf("reading the signing secret: %w", err)
	}
	body, _ := json.Marshal(Message{ID: d.id, Event: d.event, CreatedAt: d.createdAt.UTC(), SubTenantID: d.sub, Data: d.payload})
	u, err := url.Parse(dest)
	if err != nil || u.Host == "" {
		return 0, errors.New("bad webhook URL")
	}
	client := h.Client
	if client == nil {
		g := h.Egress
		if g == nil {
			g = &egress.Guard{Logger: h.Logger}
		}
		client = g.Client(egress.Policy{Tenant: partner.String(), Hosts: []string{u.Hostname()}, Purpose: "partner_webhook"}, 15*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("posting to %s: bad request", u.Hostname())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Taskiem-Webhooks/1")
	req.Header.Set("Taskiem-Signature", alerts.Sign([]byte(key), h.now(), body))
	req.Header.Set("Taskiem-Event", d.event)
	req.Header.Set("Taskiem-Delivery-Id", d.id.String())
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("posting to %s: %w", u.Hostname(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s answered %s", u.Hostname(), resp.Status)
	}
	return resp.StatusCode, nil
}
