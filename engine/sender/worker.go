package sender

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/textproto"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// MaxAttempts is how many times a message is tried before it is dead.
const MaxAttempts = 6

// claimLease is how long a claimed message is left alone: a process that
// dies mid-send leaves it to be tried again after this, which can send it
// twice, the price of never losing one.
const claimLease = 10 * time.Minute

// backoff is the wait after each failed attempt.
var backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, 2 * time.Hour}

// Worker sends what is queued.
type Worker struct {
	Pool  *pgxpool.Pool
	Vault *secrets.Vault
	// SMS are tried in order: the first that does not refuse sends it.
	SMS []SMSProvider
	// Rate caps sends per second to each SMS provider (default 10).
	Rate rate.Limit
	// Mailer and From send email; From is the platform's no-reply address.
	Mailer alerts.Mailer
	From   string
	Logger *slog.Logger

	limits map[string]*rate.Limiter
	now    func() time.Time
}

// Run works the outbox until ctx ends.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("sender", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// Tick sends what is due now for every tenant.
func (w *Worker) Tick(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_platform_message_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := w.tenantTick(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

type claimed struct {
	id                         uuid.UUID
	channel, purpose, sender   string
	attempts                   int
	expires                    *time.Time
	to, subject, text, replyTo string
	erased                     bool
}

// outcome is what an attempt came to.
type outcome struct {
	status, provider, ref, err string
	segments                   int
	retry                      bool
}

func (w *Worker) tenantTick(ctx context.Context, tenant uuid.UUID) error {
	var due []claimed
	err := db.InTenantTx(ctx, w.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH due AS (
			  SELECT id FROM platform_messages WHERE tenant_id = $1 AND status = 'pending' AND next_attempt_at <= now()
			   ORDER BY next_attempt_at LIMIT 50 FOR UPDATE SKIP LOCKED)
			UPDATE platform_messages m SET attempts = m.attempts + 1, next_attempt_at = now() + $2::interval FROM due
			 WHERE m.id = due.id
			RETURNING m.id, m.channel, m.purpose, m.sender_name, m.attempts, m.expires_at, m.recipient, m.body`, tenant, claimLease.String())
		if err != nil {
			return err
		}
		type row struct {
			c               claimed
			recipient, body map[string]any
		}
		got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.c.id, &x.c.channel, &x.c.purpose, &x.c.sender, &x.c.attempts, &x.c.expires, &x.recipient, &x.body)
		})
		if err != nil {
			return err
		}
		for _, x := range got {
			c := x.c
			to, err := w.Vault.OpenTx(ctx, tx, tenant, x.recipient)
			if errors.Is(err, pii.ErrErased) {
				c.erased = true
				due = append(due, c)
				continue
			}
			if err != nil {
				return err
			}
			b, err := w.Vault.OpenTx(ctx, tx, tenant, x.body)
			if errors.Is(err, pii.ErrErased) {
				c.erased = true
				due = append(due, c)
				continue
			}
			if err != nil {
				return err
			}
			c.to, _ = to.(string)
			if m, ok := b.(map[string]any); ok {
				c.subject, _ = m["subject"].(string)
				c.text, _ = m["text"].(string)
				c.replyTo, _ = m["reply_to"].(string)
			}
			due = append(due, c)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range due {
		o := w.attempt(ctx, c)
		if err := w.record(ctx, tenant, c, o); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *Worker) attempt(ctx context.Context, c claimed) outcome {
	switch {
	case c.erased:
		return outcome{status: "dropped", err: "the recipient's data was erased"}
	case c.expires != nil && w.clock().After(*c.expires):
		return outcome{status: "dropped", err: "expired before it could be sent"}
	case c.channel == SMS:
		return w.sendSMS(ctx, c)
	case c.channel == Email:
		return w.sendEmail(ctx, c)
	}
	return outcome{status: "dead", err: "unknown channel " + c.channel}
}

func (w *Worker) sendSMS(ctx context.Context, c claimed) outcome {
	if len(w.SMS) == 0 {
		return outcome{retry: true, err: "no SMS provider is configured"}
	}
	var last error
	for _, p := range w.SMS {
		if err := w.limiter(p.Name()).Wait(ctx); err != nil {
			return outcome{retry: true, err: err.Error()}
		}
		ref, err := p.Send(ctx, c.to, c.text)
		switch {
		case err == nil:
			return outcome{status: "sent", provider: p.Name(), ref: ref, segments: Segments(c.text)}
		case errors.Is(err, ErrRecipient):
			return outcome{status: "dead", provider: p.Name(), err: err.Error()}
		case errors.Is(err, ErrNotSent), errors.Is(err, ErrProvider):
			w.logger().Warn("sender: SMS provider refused; trying the next", "provider", p.Name(), "err", pii.Redact(err.Error()))
			last = err
			continue
		default:
			// It may have gone: never send it again.
			return outcome{status: "unknown", provider: p.Name(), err: err.Error()}
		}
	}
	return outcome{retry: true, err: last.Error()}
}

func (w *Worker) sendEmail(ctx context.Context, c claimed) outcome {
	if w.Mailer == nil || w.From == "" {
		return outcome{retry: true, err: "no mail server is configured"}
	}
	id := uuid.NewString()
	err := w.Mailer.Send(ctx, w.From, []string{c.to}, buildEmail(w.From, c.sender, c.to, c.replyTo, c.subject, c.text, id, w.clock()))
	var tp *textproto.Error
	switch {
	case err == nil:
		return outcome{status: "sent", provider: "smtp", ref: id}
	case errors.As(err, &tp) && tp.Code >= 500:
		return outcome{status: "dead", provider: "smtp", err: err.Error()}
	}
	return outcome{retry: true, err: err.Error()}
}

// record stores an attempt's outcome. A finished message loses its
// recipient and text.
func (w *Worker) record(ctx context.Context, tenant uuid.UUID, c claimed, o outcome) error {
	if o.retry && c.attempts >= MaxAttempts {
		o.status, o.retry = "dead", false
	}
	errText := pii.Redact(o.err)
	if len(errText) > 500 {
		errText = errText[:500]
	}
	return db.InTenantTx(ctx, w.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if o.retry {
			wait := backoff[min(c.attempts, len(backoff))-1]
			_, err := tx.Exec(ctx, `UPDATE platform_messages SET next_attempt_at = now() + $3::interval, last_error = $4
				WHERE tenant_id = $1 AND id = $2`, tenant, c.id, wait.String(), errText)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE platform_messages SET status = $3, provider = NULLIF($4, ''), provider_ref = NULLIF($5, ''),
			segments = NULLIF($6, 0), last_error = NULLIF($7, ''), finished_at = now(), recipient = NULL, body = NULL
			WHERE tenant_id = $1 AND id = $2`, tenant, c.id, o.status, o.provider, o.ref, o.segments, errText)
		return err
	})
}

func (w *Worker) limiter(provider string) *rate.Limiter {
	if w.limits == nil {
		w.limits = map[string]*rate.Limiter{}
	}
	l, ok := w.limits[provider]
	if !ok {
		r := w.Rate
		if r <= 0 {
			r = 10
		}
		l = rate.NewLimiter(r, int(max(r, 1)))
		w.limits[provider] = l
	}
	return l
}

func (w *Worker) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// Usage counts what a tenant sent since a time: SMS segments and emails,
// for plan allowances (S8) and the tenant's own view.
func Usage(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, since time.Time) (smsSegments, emails int, err error) {
	err = db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(segments) FILTER (WHERE channel = 'sms'), 0), count(*) FILTER (WHERE channel = 'email')
			FROM platform_messages WHERE tenant_id = $1 AND status = 'sent' AND finished_at >= $2`, tenant, since).Scan(&smsSegments, &emails)
	})
	return smsSegments, emails, err
}
