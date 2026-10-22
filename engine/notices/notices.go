// Package notices is what each person needs to know, in the app's bell
// (no-technical-steps N12, decision N0-10): a run that failed or needs a
// person, a run they started that finished, a setup problem, an alert
// rule's finding. Nothing needs setting up. Repeats of one thing are one
// item while it is unread, with a count; items are kept 90 days.
//
// Producers are in the alerts package (Notify, TellOwners, the run outcome
// notifier); this package stores and reads.
package notices

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Levels.
const (
	Info    = "info"
	Success = "success"
	Warning = "warning"
	Problem = "problem"
)

// Kinds of notification.
const (
	RunFailed     = "run_failed"
	RunNeedsCheck = "run_needs_check"
	RunFinished   = "run_finished"
	Setup         = "setup"
	Alert         = "alert"
)

// Groups are what a person chooses chat and email for (Settings >
// Notifications); the app always shows everything.
const (
	GroupFailures = "failures"
	GroupSetup    = "setup"
	GroupFinished = "finished"
)

// GroupOf is the settings group of a kind.
func GroupOf(kind string) string {
	switch kind {
	case RunFailed, RunNeedsCheck:
		return GroupFailures
	case RunFinished:
		return GroupFinished
	}
	return GroupSetup
}

// Notice is one thing to tell.
type Notice struct {
	Kind  string
	Level string
	// Title is the first line; Many, when set, is the title once the same
	// group repeats, with {n} for how many times ("Payroll failed {n}
	// times").
	Title string
	Many  string
	Body  string
	Link  string
	// Group keeps repeats together while unread (failed:<workflow>:<env>).
	Group string
	Run   *uuid.UUID
	Alert *uuid.UUID
}

// AddTx gives n to each of users in tx (the tenant in scope): a new item,
// or, while one of the same group is unread, that item counted again and
// brought to the top.
func AddTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID, n Notice) error {
	if len(users) == 0 {
		return nil
	}
	if n.Level == "" {
		n.Level = Info
	}
	if n.Group == "" {
		n.Group = n.Kind + ":" + uuid.NewString()
	}
	many := n.Many
	if many == "" {
		many = n.Title + " ({n} times)"
	}
	seen := map[uuid.UUID]bool{}
	for _, u := range users {
		if u == uuid.Nil || seen[u] {
			continue
		}
		seen[u] = true
		if _, err := tx.Exec(ctx, `INSERT INTO notifications (id, tenant_id, user_id, kind, level, title, body, link, group_key, run_id, alert_id)
			VALUES ($1, $2, $3, $4, $5, left($6, 200), left($7, 1000), NULLIF(left($8, 500), ''), left($9, 200), $10, $11)
			ON CONFLICT (tenant_id, user_id, group_key) WHERE read_at IS NULL DO UPDATE SET
			  count = notifications.count + 1, title = left(replace($12, '{n}', (notifications.count + 1)::text), 200),
			  body = EXCLUDED.body, link = EXCLUDED.link, level = EXCLUDED.level, run_id = EXCLUDED.run_id, alert_id = EXCLUDED.alert_id, updated_at = now()`,
			uuid.Must(uuid.NewV7()), tenant, u, n.Kind, n.Level, n.Title, n.Body, n.Link, n.Group, n.Run, n.Alert, many); err != nil {
			return err
		}
	}
	return nil
}

// Item is a notification as its person sees it.
type Item struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Level     string     `json:"level"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link,omitempty"`
	Count     int        `json:"count"`
	RunID     *uuid.UUID `json:"run_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// ListTx is a person's notifications, newest first; before pages (an
// updated_at), unread keeps the unread only.
func ListTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, unread bool, before *time.Time, limit int) ([]Item, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT id, kind, level, title, body, COALESCE(link, ''), count, run_id, created_at, updated_at, read_at
		FROM notifications WHERE user_id = $1 AND (NOT $2 OR read_at IS NULL) AND ($3::timestamptz IS NULL OR updated_at < $3)
		ORDER BY updated_at DESC LIMIT $4`, user, unread, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var it Item
		return it, r.Scan(&it.ID, &it.Kind, &it.Level, &it.Title, &it.Body, &it.Link, &it.Count, &it.RunID, &it.CreatedAt, &it.UpdatedAt, &it.ReadAt)
	})
}

// UnreadTx is how many of a person's notifications are unread.
func UnreadTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, user).Scan(&n)
	return n, err
}

// MarkReadTx marks a person's notifications read: those of ids, or all.
func MarkReadTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, ids []uuid.UUID, all bool) (int64, error) {
	if !all && len(ids) == 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL AND ($2 OR id = ANY ($3))`, user, all, ids)
	return tag.RowsAffected(), err
}

// Setting is a person's choice for a group: chat and email besides the app.
type Setting struct {
	Group string `json:"group"`
	Chat  bool   `json:"chat"`
	Email bool   `json:"email"`
}

// Defaults: failures and setup problems reach owners on chat (else
// email); finished runs only in the app.
var Defaults = []Setting{{GroupFailures, true, true}, {GroupSetup, true, true}, {GroupFinished, false, false}}

// SettingsTx are a person's settings, defaults filled in.
func SettingsTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) ([]Setting, error) {
	rows, err := tx.Query(ctx, `SELECT kind, chat, email FROM notification_settings WHERE user_id = $1`, user)
	if err != nil {
		return nil, err
	}
	have := map[string]Setting{}
	for rows.Next() {
		var s Setting
		if err := rows.Scan(&s.Group, &s.Chat, &s.Email); err != nil {
			rows.Close()
			return nil, err
		}
		have[s.Group] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Setting, 0, len(Defaults))
	for _, d := range Defaults {
		if s, ok := have[d.Group]; ok {
			out = append(out, s)
		} else {
			out = append(out, d)
		}
	}
	return out, nil
}

// ErrUnknownGroup: a setting for a group that does not exist.
var ErrUnknownGroup = errors.New("notices: unknown group")

// SetTx saves a person's settings.
func SetTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, settings []Setting) error {
	for _, s := range settings {
		if s.Group != GroupFailures && s.Group != GroupSetup && s.Group != GroupFinished {
			return ErrUnknownGroup
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notification_settings (tenant_id, user_id, kind, chat, email) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, user_id, kind) DO UPDATE SET chat = EXCLUDED.chat, email = EXCLUDED.email, updated_at = now()`,
			tenant, user, s.Group, s.Chat, s.Email); err != nil {
			return err
		}
	}
	return nil
}

// Wants splits users by how they want a group beyond the app: chat (on
// the chat they linked; email when they linked none, if they take email)
// and email only.
func Wants(ctx context.Context, tx pgx.Tx, users []uuid.UUID, group string) (chat, email []uuid.UUID, err error) {
	if len(users) == 0 {
		return nil, nil, nil
	}
	def := Setting{}
	for _, d := range Defaults {
		if d.Group == group {
			def = d
		}
	}
	rows, err := tx.Query(ctx, `SELECT u, COALESCE(s.chat, $3), COALESCE(s.email, $4) FROM unnest($1::uuid[]) u
		LEFT JOIN notification_settings s ON s.user_id = u AND s.kind = $2`, users, group, def.Chat, def.Email)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u uuid.UUID
		var c, e bool
		if err := rows.Scan(&u, &c, &e); err != nil {
			return nil, nil, err
		}
		if c {
			chat = append(chat, u)
		}
		if e {
			email = append(email, u)
		}
	}
	return chat, email, rows.Err()
}
