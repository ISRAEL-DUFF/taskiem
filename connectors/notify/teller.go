package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// OutboxNotice is the chat outbox kind of a Tell me message: rendered,
// when it is sent, from the step's own input in the run's history, so the
// outbox holds no text (docs/whatsapp.md#delivery-and-retries).
const OutboxNotice = "notice"

// Notifier delivers Tell me messages: each person on the first chat
// channel they linked (in Chat's order), through the chat outbox the api
// role sends from; anyone who linked none by email through Messages.
type Notifier struct {
	Pool *pgxpool.Pool
	// Chat are the chat channels this deployment runs ("whatsapp",
	// "telegram"), in the order people are tried on.
	Chat []string
	// Messages sends the email to people who linked no chat; nil: they
	// are not reached.
	Messages *sender.Queue
}

// Tell queues the message to its people, once per person for the key.
func (n *Notifier) Tell(ctx context.Context, m Message) (Result, error) {
	var res Result
	err := db.InTenantTx(ctx, n.Pool, []uuid.UUID{m.Tenant}, func(tx pgx.Tx) error {
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, m.Tenant).Scan(&business); err != nil {
			return err
		}
		people, err := recipients(ctx, tx, m)
		if err != nil {
			return err
		}
		res.People = len(people)
		ids := make([]uuid.UUID, 0, len(people))
		for _, p := range people {
			ids = append(ids, p.id)
		}
		linked := map[uuid.UUID]string{}
		for _, ch := range n.Chat {
			q := `SELECT user_id FROM taskiem_wa_numbers($1)`
			if ch == "telegram" {
				q = `SELECT user_id FROM taskiem_tg_accounts($1)`
			}
			rows, err := tx.Query(ctx, q, ids)
			if err != nil {
				return err
			}
			users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			for _, u := range users {
				if _, ok := linked[u]; !ok {
					linked[u] = ch
				}
			}
		}
		for _, p := range people {
			if ch, ok := linked[p.id]; ok {
				run, step := m.Run, m.Step
				if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: m.Tenant, Channel: ch, Kind: OutboxNotice, User: p.id, Run: &run, Step: &step},
					"notice:"+m.Key+":"+p.id.String()); err != nil {
					return err
				}
				res.Chat++
				continue
			}
			if n.Messages == nil || p.email == "" {
				continue
			}
			subject := m.Title
			if subject == "" {
				subject = "A message about " + business
			}
			if _, err := n.Messages.Enqueue(ctx, tx, sender.Message{Tenant: m.Tenant, Channel: sender.Email, Purpose: sender.PurposeNotice, To: p.email,
				SenderName: business, Subject: subject, Text: text(m), Key: "notice:" + m.Key + ":" + p.id.String()}); err != nil {
				return fmt.Errorf("telling %s by email: %w", maskEmail(p.email), err)
			}
			res.Email++
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("%w: %w", effects.ErrRetryable, err)
	}
	return res, nil
}

// Text queues an SMS to a customer from the business's name, counted in
// the plan's monthly texts.
func (n *Notifier) Text(ctx context.Context, t Text) (bool, error) {
	if n.Messages == nil {
		return false, fmt.Errorf("texting customers is not set up on this Taskiem: %w", effects.ErrFatal)
	}
	over := false
	err := db.InTenantTx(ctx, n.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, t.Tenant).Scan(&business); err != nil {
			return err
		}
		_, err := n.Messages.Enqueue(ctx, tx, sender.Message{Tenant: t.Tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: t.To,
			SenderName: business, Text: t.Text, Key: "text:" + t.Key})
		if errors.Is(err, sender.ErrAllowance) {
			over = true
			return nil
		}
		return err
	})
	switch {
	case errors.Is(err, sender.ErrInvalid):
		return false, fmt.Errorf("%w: %w", effects.ErrFatal, err)
	case err != nil:
		return false, fmt.Errorf("%w: %w", effects.ErrRetryable, err)
	}
	return over, nil
}

type person struct {
	id    uuid.UUID
	email string
}

// recipients are the active members the message is for: the owners; the
// holders of a role (the owners when nobody holds it, so someone hears);
// or the members named by email. Never anyone outside the business.
func recipients(ctx context.Context, tx pgx.Tx, m Message) ([]person, error) {
	q, args := `SELECT DISTINCT u.id, u.email FROM memberships ms JOIN users u ON u.id = ms.user_id WHERE u.status = 'active' AND ms.role = 'owner'`, []any(nil)
	switch m.To {
	case "role":
		q = `SELECT DISTINCT u.id, u.email FROM memberships ms JOIN users u ON u.id = ms.user_id WHERE u.status = 'active'
			AND (ms.role = $1 OR (ms.role = 'owner' AND NOT EXISTS (SELECT 1 FROM memberships x JOIN users y ON y.id = x.user_id WHERE x.role = $1 AND y.status = 'active')))`
		args = []any{m.Role}
	case "members":
		lower := make([]string, 0, len(m.Members))
		for _, e := range m.Members {
			lower = append(lower, strings.ToLower(e))
		}
		q = `SELECT DISTINCT u.id, u.email FROM memberships ms JOIN users u ON u.id = ms.user_id WHERE u.status = 'active' AND lower(u.email) = ANY ($1)`
		args = []any{lower}
	}
	rows, err := tx.Query(ctx, q+` ORDER BY u.email LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (person, error) {
		var p person
		return p, r.Scan(&p.id, &p.email)
	})
}

// text is the message as sent: its title, a blank line, then the message.
func text(m Message) string {
	if m.Title == "" {
		return m.Text
	}
	return m.Title + "\n\n" + m.Text
}

func maskEmail(e string) string {
	if i := strings.IndexByte(e, '@'); i > 0 {
		return e[:1] + "•••" + e[i:]
	}
	return "•••"
}

// Late binds the connector to a Teller made after the registry.
type Late struct {
	mu sync.RWMutex
	t  Teller
}

// Set binds the teller.
func (l *Late) Set(t Teller) { l.mu.Lock(); l.t = t; l.mu.Unlock() }

// Tell tells through the bound teller.
func (l *Late) Tell(ctx context.Context, m Message) (Result, error) {
	l.mu.RLock()
	t := l.t
	l.mu.RUnlock()
	if t == nil {
		return Result{}, fmt.Errorf("Tell me is not available on this deployment: %w", effects.ErrFatal)
	}
	return t.Tell(ctx, m)
}

// Text texts through the bound teller.
func (l *Late) Text(ctx context.Context, t Text) (bool, error) {
	l.mu.RLock()
	tl := l.t
	l.mu.RUnlock()
	if tl == nil {
		return false, fmt.Errorf("texting is not available on this deployment: %w", effects.ErrFatal)
	}
	return tl.Text(ctx, t)
}
