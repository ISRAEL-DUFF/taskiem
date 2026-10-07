package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// The outbox (docs/whatsapp.md#delivery-and-retries) holds messages to
// people that must arrive even when the Graph API or the network fails for
// a while: approval requests, how runs started from WhatsApp ended, and
// alerts. A row names what to send and to whom (a person, not a number);
// the message is rendered at each attempt, so nothing personal, no
// decision token and no number is stored. Each attempt is claimed once.
// One-time codes and chat replies are sent directly and not retried: the
// person is waiting, and simply asks again.

// Outbox kinds.
const (
	OutboxApproval   = "approval"
	OutboxRunOutcome = "run_outcome"
	OutboxAlert      = "alert"
)

// OutboxMaxAttempts bounds retries; after the last a message is dead.
const OutboxMaxAttempts = 8

// OutboxBackoff is the wait after a failed attempt: 1, 4, 16, 64 minutes,
// then six hours.
func OutboxBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		return 6 * time.Hour
	}
	return min(time.Minute<<(2*(attempt-1)), 6*time.Hour)
}

// ErrNothingToSend says a queued message became moot (the approval was
// decided, the person unlinked their number): it is dropped, not retried.
var ErrNothingToSend = errors.New("whatsapp: nothing to send any more")

// OutboxItem is one queued message to one person.
type OutboxItem struct {
	ID       uuid.UUID
	Tenant   uuid.UUID
	Kind     string
	User     uuid.UUID
	Run      *uuid.UUID
	Step     *string
	Level    *int
	Alert    *uuid.UUID
	Attempts int
}

// Enqueue queues an item in tx unless one with the same dedup key exists
// for the tenant; it reports whether it was new.
func Enqueue(ctx context.Context, tx pgx.Tx, it OutboxItem, dedup string) (bool, error) {
	if it.ID == uuid.Nil {
		it.ID = uuid.Must(uuid.NewV7())
	}
	tag, err := tx.Exec(ctx, `INSERT INTO whatsapp_outbox (id, tenant_id, kind, user_id, run_id, step_id, level, alert_id, dedup_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (tenant_id, dedup_key) DO NOTHING`,
		it.ID, it.Tenant, it.Kind, it.User, it.Run, it.Step, it.Level, it.Alert, dedup)
	return err == nil && tag.RowsAffected() == 1, err
}

// ClaimOutbox claims up to limit due items of a tenant (only those in ids,
// when given) for five minutes, so no other sender takes them meanwhile.
func ClaimOutbox(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, limit int, ids []uuid.UUID) ([]OutboxItem, error) {
	var out []OutboxItem
	err := db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE whatsapp_outbox o SET attempts = o.attempts + 1, next_attempt_at = now() + interval '5 minutes'
			WHERE o.id IN (SELECT id FROM whatsapp_outbox WHERE status = 'pending' AND next_attempt_at <= now()
			  AND ($2::uuid[] IS NULL OR id = ANY ($2)) ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
			RETURNING o.id, o.tenant_id, o.kind, o.user_id, o.run_id, o.step_id, o.level, o.alert_id, o.attempts`, limit, ids)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (OutboxItem, error) {
			var it OutboxItem
			return it, r.Scan(&it.ID, &it.Tenant, &it.Kind, &it.User, &it.Run, &it.Step, &it.Level, &it.Alert, &it.Attempts)
		})
		return err
	})
	return out, err
}

// FinishOutbox records how an attempt went: sent; dropped when moot; dead
// when the error cannot get better by waiting (a template held back by
// the allowance, a reply with no template outside the window) or after
// the last attempt; otherwise due again after a backoff.
func FinishOutbox(ctx context.Context, pool *pgxpool.Pool, it OutboxItem, sendErr error, now time.Time) error {
	return db.InTenantTx(ctx, pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var err error
		switch {
		case sendErr == nil:
			_, err = tx.Exec(ctx, `UPDATE whatsapp_outbox SET status = 'sent', finished_at = now(), last_error = NULL WHERE id = $1`, it.ID)
		case errors.Is(sendErr, ErrNothingToSend):
			_, err = tx.Exec(ctx, `UPDATE whatsapp_outbox SET status = 'dropped', finished_at = now(), last_error = $2 WHERE id = $1`, it.ID, clipErr(sendErr))
		case errors.Is(sendErr, ErrTemplateBlocked), errors.Is(sendErr, ErrWindowClosed), it.Attempts >= OutboxMaxAttempts:
			_, err = tx.Exec(ctx, `UPDATE whatsapp_outbox SET status = 'dead', finished_at = now(), last_error = $2 WHERE id = $1`, it.ID, clipErr(sendErr))
		default:
			_, err = tx.Exec(ctx, `UPDATE whatsapp_outbox SET last_error = $2, next_attempt_at = $3 WHERE id = $1`, it.ID, clipErr(sendErr), now.Add(OutboxBackoff(it.Attempts)))
		}
		return err
	})
}

func clipErr(err error) string {
	s := err.Error()
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// NumberOf is the number a member of tenant has bound, or ErrNothingToSend.
func (p *Platform) NumberOf(ctx context.Context, tenant, user uuid.UUID) (string, error) {
	var number string
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT number FROM taskiem_wa_numbers($1)`, []uuid.UUID{user}).Scan(&number)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: the person has no linked number", ErrNothingToSend)
	}
	return number, err
}

// Queue queues an alert for those of members who have bound a number (the
// WhatsApp alert channel), one message each, and tries them at once. Each
// member's message is then retried on its own, so one member's failure
// never sends the alert again to the others. It fails only when no member
// has a number.
func (p *Platform) Queue(ctx context.Context, tenant, alert uuid.UUID, members []uuid.UUID) error {
	var ids []uuid.UUID
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT user_id FROM taskiem_wa_numbers($1)`, members)
		if err != nil {
			return err
		}
		bound, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if len(bound) == 0 {
			return errors.New("none of the channel's members has bound a WhatsApp number")
		}
		for _, u := range bound {
			it := OutboxItem{ID: uuid.Must(uuid.NewV7()), Tenant: tenant, Kind: OutboxAlert, User: u, Alert: &alert}
			fresh, err := Enqueue(ctx, tx, it, "alert:"+alert.String()+":"+u.String())
			if err != nil {
				return err
			}
			if fresh {
				ids = append(ids, it.ID)
			}
		}
		return nil
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	items, err := ClaimOutbox(ctx, p.Pool, tenant, len(ids), ids)
	if err != nil {
		// Queued all the same: the notifier sends them.
		if p.Logger != nil {
			p.Logger.Warn("whatsapp: claiming queued alerts", "err", err)
		}
		items = nil
	}
	for _, it := range items {
		sendErr := p.SendAlertItem(ctx, it)
		if ferr := FinishOutbox(ctx, p.Pool, it, sendErr, p.now()); ferr != nil && p.Logger != nil {
			p.Logger.Error("whatsapp: outbox", "err", ferr)
		}
	}
	return nil
}

// SendAlertItem sends a queued alert to its person, rendered from the
// alert as recorded.
func (p *Platform) SendAlertItem(ctx context.Context, it OutboxItem) error {
	if it.Alert == nil {
		return fmt.Errorf("%w: no alert", ErrNothingToSend)
	}
	var name, kind, title, body string
	var link *string
	var raw []byte
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&name); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT kind, title, body, link, detail FROM alerts WHERE id = $1`, *it.Alert).Scan(&kind, &title, &body, &link, &raw)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the alert is gone", ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	number, err := p.NumberOf(ctx, it.Tenant, it.User)
	if err != nil {
		return err
	}
	var detail map[string]any
	_ = json.Unmarshal(raw, &detail)
	l := ""
	if link != nil {
		l = *link
	}
	q, err := p.ForTenant(ctx, it.Tenant)
	if err != nil {
		return err
	}
	m := AlertMessage(name, kind, title, body, l, detail)
	m.Tenant = it.Tenant
	if q.Own() {
		m.Text = strings.TrimPrefix(m.Text, "["+name+"] ")
	}
	if _, err := q.Send(ctx, number, m); err != nil {
		return fmt.Errorf("to %s: %w", MaskNumber(number), err)
	}
	return nil
}
