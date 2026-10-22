package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// People, not only channels (no-technical-steps N12, decision N0-10):
// what concerns a business reaches its people in the app's bell with
// nothing set up, and its owners on their linked chat (else by email) as
// their notification settings say. Alert rules and channels stay, for
// other places and thresholds.

// ownersTx are the business's active owners.
func ownersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND ms.role = 'owner' AND u.status = 'active'`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// membersTx keeps those of ids (user ids, as text) who are active members.
func membersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []string) ([]uuid.UUID, error) {
	var us []uuid.UUID
	for _, s := range ids {
		if u, err := uuid.Parse(s); err == nil {
			us = append(us, u)
		}
	}
	if len(us) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND u.status = 'active' AND ms.user_id = ANY ($2)`, tenant, us)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// queueChat queues a recorded alert to each of users on the chat they
// linked, WhatsApp before Telegram, and reports who it reached.
func queueChat(ctx context.Context, tx pgx.Tx, tenant, alert uuid.UUID, users []uuid.UUID) (map[uuid.UUID]bool, error) {
	told := map[uuid.UUID]bool{}
	if len(users) == 0 {
		return told, nil
	}
	for _, ch := range []struct{ name, q string }{
		{"whatsapp", `SELECT user_id FROM taskiem_wa_numbers($1)`},
		{"telegram", `SELECT user_id FROM taskiem_tg_accounts($1)`},
	} {
		rows, err := tx.Query(ctx, ch.q, users)
		if err != nil {
			return told, err
		}
		linked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return told, err
		}
		for _, u := range linked {
			if told[u] {
				continue
			}
			told[u] = true
			it := whatsapp.OutboxItem{Tenant: tenant, Channel: ch.name, Kind: whatsapp.OutboxAlert, User: u, Alert: &alert}
			key := "alert:" + alert.String() + ":" + u.String()
			if ch.name != "whatsapp" {
				key = ch.name + ":" + key
			}
			if _, err := whatsapp.Enqueue(ctx, tx, it, key); err != nil {
				return told, err
			}
		}
	}
	return told, nil
}

// setupNotice is an owner alert as a notification.
func setupNotice(al Alert, id uuid.UUID) notices.Notice {
	return notices.Notice{Kind: notices.Setup, Level: notices.Warning, Title: al.Title, Body: al.Body, Link: al.Link, Group: "setup:" + al.Kind, Alert: &id}
}

// ruleNotice tells the people a rule's channels name (members by email),
// or the owners when they name none, of what it found.
func ruleNotice(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r *rule, al Alert, id uuid.UUID) error {
	// Failed runs and runs that need a person reach the people involved
	// already (Outcomes): a rule on them adds its channels, not a second
	// item in the bell.
	if al.Kind == RunFailed || al.Kind == NeedsReconciliation {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT u.id FROM alert_channels c CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(c.config->'to', '[]')) e
		JOIN users u ON lower(u.email) = lower(e) AND u.status = 'active'
		JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $1
		WHERE c.id = ANY ($2) AND jsonb_typeof(COALESCE(c.config->'to', '[]')) = 'array'`, tenant, r.channels)
	if err != nil {
		return err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(users) == 0 {
		if users, err = ownersTx(ctx, tx, tenant); err != nil {
			return err
		}
	}
	level := notices.Warning
	if al.Kind == KeyHealth {
		level = notices.Problem
	}
	return notices.AddTx(ctx, tx, tenant, users, notices.Notice{Kind: notices.Alert, Level: level, Title: al.Title, Body: al.Body, Link: al.Link,
		Group: "rule:" + r.id.String() + ":" + al.Dedup, Alert: &id})
}

// --- how runs ended ---

// outcome is a run_outcomes row.
type outcome struct {
	run    uuid.UUID
	tenant uuid.UUID
	status string
}

// Outcomes tells people how runs ended (runtime.QueueOutcome): a failure
// or a run waiting for a person reaches the owners, whoever started it and
// whoever published its workflow; a run a member started that finished
// reaches them. Owners also hear on chat, once per workflow and hour,
// as their settings say.
func (a *Alerter) Outcomes(ctx context.Context) error {
	rows, err := a.Pool.Query(ctx, `SELECT run_id, tenant_id, status FROM taskiem_run_outcomes_claim(200)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (outcome, error) {
		var o outcome
		return o, r.Scan(&o.run, &o.tenant, &o.status)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range list {
		if err := a.outcome(ctx, o); err != nil {
			errs = append(errs, fmt.Errorf("run %s: %w", o.run, err))
		}
	}
	return errors.Join(errs...)
}

// mail is an email to send once the transaction is in.
type mail struct {
	to            []string
	subject, body string
	id            string
}

func (a *Alerter) outcome(ctx context.Context, o outcome) error {
	var mails []mail
	err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{o.tenant}, func(tx pgx.Tx) error {
		var wfID uuid.UUID
		var wf, env string
		var startedBy *string
		var publishedBy *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT w.id, w.name, r.environment, r.started_by, wv.published_by FROM runs r
			JOIN workflows w ON w.id = r.workflow_id JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version
			WHERE r.id = $1`, o.run).Scan(&wfID, &wf, &env, &startedBy, &publishedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `DELETE FROM run_outcomes WHERE run_id = $1 AND status = $2`, o.run, o.status)
			return err
		}
		if err != nil {
			return err
		}
		ids := []string{}
		if startedBy != nil {
			ids = append(ids, *startedBy)
		}
		starter, err := membersTx(ctx, tx, o.tenant, ids)
		if err != nil {
			return err
		}
		link := "/runs/" + o.run.String()
		detail := map[string]any{"run_id": o.run, "workflow": wf, "environment": env}
		hour := a.now().UTC().Format("2006-01-02T15")
		var users, owners []uuid.UUID
		var n notices.Notice
		var al Alert
		switch o.status {
		case "failed", "needs_reconciliation":
			if owners, err = ownersTx(ctx, tx, o.tenant); err != nil {
				return err
			}
			if publishedBy != nil {
				ids = append(ids, publishedBy.String())
			}
			involved, err := membersTx(ctx, tx, o.tenant, ids)
			if err != nil {
				return err
			}
			users = append(append(users, owners...), involved...)
			if o.status == "failed" {
				n = notices.Notice{Kind: notices.RunFailed, Level: notices.Problem, Title: wf + " failed", Many: wf + " failed {n} times",
					Body: "In " + env + ". Open the run to see what went wrong.", Link: link, Group: "failed:" + wfID.String() + ":" + env, Run: &o.run}
				al = Alert{Kind: RunFailed, Dedup: "outcome:" + wfID.String() + ":" + env + ":" + hour, Title: wf + " failed in " + env,
					Body: "Open the run to see what went wrong.", Link: a.link(link), Detail: detail}
			} else {
				n = notices.Notice{Kind: notices.RunNeedsCheck, Level: notices.Warning, Title: wf + " needs a person",
					Body: "A call's outcome is unknown: check with the provider, then resolve the step.", Link: link, Group: "check:" + o.run.String(), Run: &o.run}
				al = Alert{Kind: NeedsReconciliation, Dedup: "outcome:" + o.run.String(), Title: wf + " needs a person in " + env,
					Body: "A call's outcome is unknown: check with the provider, then resolve the step.", Link: a.link(link), Detail: detail}
			}
		case "completed":
			users, owners = starter, starter
			n = notices.Notice{Kind: notices.RunFinished, Level: notices.Success, Title: "Your run of " + wf + " finished", Body: "In " + env + ".",
				Link: link, Group: "finished:" + o.run.String(), Run: &o.run}
			al = Alert{Kind: "run_completed", Dedup: "outcome:" + o.run.String(), Title: "Your run of " + wf + " finished in " + env, Link: a.link(link), Detail: detail}
		}
		if err := notices.AddTx(ctx, tx, o.tenant, users, n); err != nil {
			return err
		}
		// Beyond the app: chat, else email, as each person chose.
		chat, email, err := notices.Wants(ctx, tx, owners, notices.GroupOf(n.Kind))
		if err != nil {
			return err
		}
		if len(chat)+len(email) > 0 {
			id, fresh, err := recordQuiet(ctx, tx, o.tenant, al)
			if err != nil {
				return err
			}
			if fresh {
				told, err := queueChat(ctx, tx, o.tenant, id, chat)
				if err != nil {
					return err
				}
				var to []string
				for _, u := range email {
					if told[u] {
						continue
					}
					var addr string
					if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, u).Scan(&addr); err == nil && addr != "" {
						to = append(to, addr)
					}
				}
				if len(to) > 0 {
					mails = append(mails, mail{to: to, subject: al.Title, body: al.Body + "\n\n" + al.Link, id: id.String()})
				}
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM run_outcomes WHERE run_id = $1 AND status = $2`, o.run, o.status)
		return err
	})
	if err != nil {
		return err
	}
	for _, m := range mails {
		if a.Mailer == nil || a.From == "" {
			break
		}
		for _, to := range m.to {
			if err := a.Mailer.Send(ctx, a.From, []string{to}, BuildEmail(a.From, []string{to}, m.subject, m.body, m.id)); err != nil {
				a.log().Warn("alerts: emailing an owner", "alert", m.id, "err", err)
			}
		}
	}
	return nil
}

// recordQuiet records an alert once per kind and dedup key, with no
// channel deliveries (those are the rules'); fresh is false when it was
// recorded before.
func recordQuiet(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, al Alert) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM alerts WHERE tenant_id = $1 AND rule_id IS NULL AND kind = $2 AND dedup_key = $3`, tenant, al.Kind, al.Dedup).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return id, false, err
	}
	id = uuid.Must(uuid.NewV7())
	if err := insertAlert(ctx, tx, tenant, id, al); err != nil {
		return id, false, err
	}
	return id, true, nil
}

// expireNotices deletes notifications older than 90 days, at most hourly.
func (a *Alerter) expireNotices(ctx context.Context) {
	if !a.lastExpiry.IsZero() && a.now().Sub(a.lastExpiry) < time.Hour {
		return
	}
	a.lastExpiry = a.now()
	if _, err := a.Pool.Exec(ctx, `SELECT taskiem_notifications_expire()`); err != nil {
		a.log().Warn("alerts: expiring notifications", "err", err)
	}
}
