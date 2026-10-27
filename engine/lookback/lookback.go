// Package lookback reads credits to customers' account numbers back from
// the rails that issued them (docs/spec/schools.md 6.5, B4;
// docs/schools.md#the-look-back): for each prod connection that issued
// customers' accounts and maps list_credits, every 15 minutes, the last
// 48 hours, an overlapping window. Each credit is recorded through the
// money log's writer as the rail's virtual_account.credited event would
// be, with the same key (connection, event, the account's reference and
// credit_id), so a credit heard by webhook and read back is one entry, and
// matching takes it like any other.
//
// When the look-back finds a credit the webhook should have brought (a
// rail with the event mapped, a credit more than Late old that the log did
// not have), the owners are told once per connection a day that the
// webhook may not be set up. A connection whose look-back fails Failing
// times in a row raises an alert, once a day while it fails.
package lookback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

const (
	// Window is how far back each look-back reads.
	Window = 48 * time.Hour
	// Every is how often a connection is read back (the database's
	// taskiem_money_lookback_targets says the same).
	Every = 15 * time.Minute
	// Late is how old a credit the look-back found first must be for the
	// webhook to have missed it, rather than be on its way.
	Late = 30 * time.Minute
	// Failing is how many failures in a row raise an alert.
	Failing = 3
)

// Alert kinds.
const (
	// AlertWebhookMissed: the look-back found money the webhook did not
	// bring; once per connection a day.
	AlertWebhookMissed = "lookback_webhook_missed"
	// AlertFailing: the look-back of a connection keeps failing; once per
	// connection a day.
	AlertFailing = "lookback_failing"
)

// Looker runs the look-back.
type Looker struct {
	Pool   *pgxpool.Pool
	Rails  invoicing.CapabilityRails
	Log    *moneylog.Log
	Logger *slog.Logger
	Now    func() time.Time
}

func (l *Looker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Looker) log() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

// Target is one connection (and wallet) read back.
type Target struct {
	Tenant  uuid.UUID
	Channel string
	Account string
}

// Tick reads back every connection that is due. One connection's failure
// is recorded on it and does not stop the others.
func (l *Looker) Tick(ctx context.Context) error {
	rows, err := l.Pool.Query(ctx, `SELECT tenant_id, channel, account FROM taskiem_money_lookback_targets()`)
	if err != nil {
		return err
	}
	targets, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Target, error) {
		var t Target
		return t, r.Scan(&t.Tenant, &t.Channel, &t.Account)
	})
	if err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := l.Look(ctx, t); err != nil && ctx.Err() == nil {
			l.log().Warn("look-back", "tenant", t.Tenant, "channel", t.Channel, "err", err)
		}
	}
	return nil
}

// Result is what one look-back did.
type Result struct {
	// Ran is false when it was not due (another server took it) or the
	// rail cannot be read back.
	Ran bool
	// Read are the credits the rail listed; Found those the log did not
	// have; Missed those of them the webhook should have brought.
	Read, Found, Missed int
}

// Look reads one connection back, when it is due (claimed in the database,
// so two servers do not both read it), and records what it found.
func (l *Looker) Look(ctx context.Context, t Target) (Result, error) {
	var res Result
	now := l.now()
	claimed := false
	err := db.InTenantTx(ctx, l.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE money_lookbacks SET last_run_at = $4 WHERE tenant_id = $1 AND channel = $2 AND account = $3
			AND (last_run_at IS NULL OR last_run_at <= $4::timestamptz - $5::interval)`, t.Tenant, t.Channel, t.Account, now, fmt.Sprintf("%d seconds", int(Every.Seconds())-5))
		claimed = err == nil && tag.RowsAffected() == 1
		return err
	})
	if err != nil || !claimed {
		return res, err
	}
	rail, err := l.Rails.ResolveChannel(ctx, t.Tenant, t.Channel, "list_credits")
	if errors.Is(err, invoicing.ErrNoRail) {
		return res, nil // this rail cannot list its credits
	}
	if err != nil {
		return res, l.failed(ctx, t, err)
	}
	if rail.Needs("list_credits", "account") && t.Account == "" {
		return res, nil // the wallet is not known: nothing to read
	}
	res.Ran = true
	credits, err := l.read(ctx, t, rail, now.Add(-Window), now)
	if err != nil {
		return res, l.failed(ctx, t, err)
	}
	res.Read = len(credits)
	webhook := rail.Events["virtual_account.credited"]
	err = db.InTenantTx(ctx, l.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		res.Found, res.Missed = 0, 0
		for _, c := range credits {
			e, ok := moneylog.FromCredit(t.Channel, c)
			if !ok {
				continue
			}
			_, fresh, err := l.Log.RecordTx(ctx, tx, t.Tenant, e)
			if err != nil {
				return err
			}
			if !fresh {
				continue
			}
			res.Found++
			if webhook && (e.OccurredAt == nil || now.Sub(*e.OccurredAt) >= Late) {
				res.Missed++
			}
		}
		var told *time.Time
		if err := tx.QueryRow(ctx, `UPDATE money_lookbacks SET last_ok_at = $4, failures = 0, last_error = NULL, found = found + $5
			WHERE tenant_id = $1 AND channel = $2 AND account = $3 RETURNING missed_on`, t.Tenant, t.Channel, t.Account, now, res.Found).Scan(&told); err != nil {
			return err
		}
		day := lagosDay(now)
		if res.Missed == 0 || told != nil && lagosDay(*told) == day {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE money_lookbacks SET missed_on = $4 WHERE tenant_id = $1 AND channel = $2 AND account = $3`, t.Tenant, t.Channel, t.Account, day); err != nil {
			return err
		}
		_, err := alerts.TellOwners(ctx, tx, t.Tenant, alerts.Alert{Kind: AlertWebhookMissed, Dedup: t.Channel + ":" + day,
			Title: fmt.Sprintf("%s's notices of payments may not be set up", rail.Name),
			Body: fmt.Sprintf("Taskiem found %s paid into customers' account numbers on %s that %s never told it about. It has recorded and matched them, but they arrive late this way: "+
				"set up %s's webhook for this connection (Connections, then the connection's page) so payments arrive at once.", plural(res.Missed, "payment"), t.Channel, rail.Name, rail.Name),
			Link: "/connections", Detail: map[string]any{"channel": t.Channel, "missed": res.Missed}})
		return err
	})
	return res, err
}

// read lists the rail's credits from since to until; when the rail says
// it had more than it answered, the window is read again in quarters.
func (l *Looker) read(ctx context.Context, t Target, rail invoicing.Rail, since, until time.Time) ([]map[string]any, error) {
	list, truncated, err := l.list(ctx, t, rail, since, until)
	if err != nil || !truncated {
		return list, err
	}
	step := until.Sub(since) / 4
	var all []map[string]any
	for i := range 4 {
		from := since.Add(time.Duration(i) * step)
		part, more, err := l.list(ctx, t, rail, from, from.Add(step))
		if err != nil {
			return nil, err
		}
		if more {
			l.log().Warn("look-back: still more credits than one read holds", "tenant", t.Tenant, "channel", t.Channel, "from", from)
		}
		all = append(all, part...)
	}
	return all, nil
}

func (l *Looker) list(ctx context.Context, t Target, rail invoicing.Rail, since, until time.Time) ([]map[string]any, bool, error) {
	in := map[string]any{"since": since.UTC().Format(time.RFC3339), "until": until.UTC().Format(time.RFC3339)}
	if t.Account != "" && rail.Takes("list_credits", "account") {
		in["account"] = t.Account
	}
	out, err := l.Rails.Call(ctx, t.Tenant, rail, "list_credits", in, effects.KeyInput{TenantID: t.Tenant.String(), Seed: "lookback", StepID: t.Channel})
	if err != nil {
		return nil, false, err
	}
	raw, _ := out["credits"].([]any)
	list := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			list = append(list, m)
		}
	}
	truncated, _ := out["truncated"].(bool)
	return list, truncated, nil
}

// failed records a failure; the Failing-th in a row (and each after, once
// a day) raises an alert.
func (l *Looker) failed(ctx context.Context, t Target, cause error) error {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	err := db.InTenantTx(ctx, l.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `UPDATE money_lookbacks SET failures = failures + 1, last_error = $4 WHERE tenant_id = $1 AND channel = $2 AND account = $3 RETURNING failures`,
			t.Tenant, t.Channel, t.Account, msg).Scan(&n); err != nil {
			return err
		}
		if n < Failing {
			return nil
		}
		_, err := alerts.TellOwners(ctx, tx, t.Tenant, alerts.Alert{Kind: AlertFailing, Dedup: t.Channel + ":" + lagosDay(l.now()),
			Title: "Taskiem cannot read back payments on " + t.Channel,
			Body: fmt.Sprintf("Reading back the payments into customers' account numbers on %s has failed %d times in a row (the last: %s). "+
				"Payments its webhook does not bring are not recorded until it works again: check the connection's keys on Connections.", t.Channel, n, msg),
			Link: "/connections", Detail: map[string]any{"channel": t.Channel, "failures": n}})
		return err
	})
	return errors.Join(cause, err)
}

// lagosDay is the date in Lagos, where the businesses are.
func lagosDay(t time.Time) string {
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		loc = time.FixedZone("WAT", 3600)
	}
	return t.In(loc).Format(time.DateOnly)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
