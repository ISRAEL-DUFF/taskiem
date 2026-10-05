// Package ingest turns outside events into runs (spec 8): webhooks,
// connector webhooks (which also deliver signals), and schedules. Every
// path verifies, deduplicates, and records before acknowledging.
package ingest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // schedules name IANA zones; containers may lack zoneinfo

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// DefaultTimezone is where schedules run unless they name another (spec 8.1).
const DefaultTimezone = "Africa/Lagos"

// ScheduleEnvironment is the only environment schedules fire in; other
// environments start scheduled workflows manually.
const ScheduleEnvironment = "prod"

// WebhookSecret is the environment secret holding a webhook trigger's HMAC
// key or bearer token: "webhook_" plus the WD id.
func WebhookSecret(def *wd.Definition) string { return "webhook_" + def.ID }

type webhookConfig struct {
	path, auth, dedup string
}

type eventConfig struct {
	connector, trigger, connection string
	events                         []string
}

type scheduleConfig struct {
	sched cron.Schedule
	cron  string
	tz    *time.Location
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func parseWebhook(c map[string]any) (webhookConfig, error) {
	w := webhookConfig{path: str(c, "path"), auth: str(c, "auth"), dedup: str(c, "dedup")}
	switch {
	case w.auth == "mtls":
		return w, fmt.Errorf("mtls webhooks need the edge proxy, which is not available yet; use hmac or bearer")
	case c["respond"] == true:
		return w, fmt.Errorf("synchronous webhooks (respond) are not available yet")
	case strings.HasPrefix(w.path, "/connectors/") || w.path == "/connectors":
		return w, fmt.Errorf("webhook paths under /connectors are reserved for connector events")
	}
	return w, nil
}

func parseEvent(c map[string]any, reg *connector.Registry) (eventConfig, error) {
	e := eventConfig{connector: str(c, "connector"), trigger: str(c, "trigger"), connection: str(c, "connection")}
	if evs, ok := c["events"].([]any); ok {
		for _, v := range evs {
			if s, ok := v.(string); ok {
				e.events = append(e.events, s)
			}
		}
	}
	conn, ok := reg.Get(e.connector)
	if !ok {
		return e, fmt.Errorf("connector %q is not available", e.connector)
	}
	spec, ok := conn.Manifest.Triggers[e.trigger]
	if !ok {
		return e, fmt.Errorf("connector %q has no trigger %q", e.connector, e.trigger)
	}
	for _, ev := range e.events {
		if len(spec.Events) > 0 && !slices.Contains(spec.Events, ev) {
			return e, fmt.Errorf("trigger %q does not send %q (it sends %s)", e.trigger, ev, strings.Join(spec.Events, ", "))
		}
	}
	return e, nil
}

func parseSchedule(c map[string]any) (scheduleConfig, error) {
	s := scheduleConfig{cron: str(c, "cron")}
	tz := str(c, "timezone")
	if tz == "" {
		tz = DefaultTimezone
	}
	var err error
	if s.tz, err = time.LoadLocation(tz); err != nil {
		return s, fmt.Errorf("unknown timezone %q", tz)
	}
	if s.sched, err = cron.ParseStandard(s.cron); err != nil {
		return s, fmt.Errorf("cron %q: %w", s.cron, err)
	}
	return s, nil
}

// Next is the first fire time strictly after t, in the schedule's zone.
func (s scheduleConfig) Next(t time.Time) time.Time { return s.sched.Next(t.In(s.tz)).UTC() }

// Check reports why a definition's trigger cannot be registered here.
func Check(def *wd.Definition, reg *connector.Registry) error {
	c := def.Trigger.Config
	switch def.Trigger.Type {
	case "manual":
		return nil
	case "webhook":
		_, err := parseWebhook(c)
		return err
	case "connector_event":
		_, err := parseEvent(c, reg)
		return err
	case "schedule":
		_, err := parseSchedule(c)
		return err
	}
	return fmt.Errorf("%s triggers are not available yet", def.Trigger.Type)
}

// Sync replaces a workflow's registered triggers with those of the version
// being published, inside the publishing transaction. Webhook and connector
// triggers are registered in every environment (deliveries name theirs with
// ?env=, default prod); schedules fire in prod only.
func Sync(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, version int, def *wd.Definition, reg *connector.Registry, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM triggers WHERE workflow_id = $1`, wf); err != nil {
		return err
	}
	if err := Check(def, reg); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT name FROM environments ORDER BY name`)
	if err != nil {
		return err
	}
	envs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	c := def.Trigger.Config
	for _, env := range envs {
		id := uuid.Must(uuid.NewV7())
		switch def.Trigger.Type {
		case "webhook":
			w, _ := parseWebhook(c)
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, path, auth, dedup, secret_name)
				VALUES ($1, $2, $3, $4, $5, 'webhook', $6, $7, NULLIF($8, ''), $9)`, id, tenant, wf, version, env, w.path, w.auth, w.dedup, WebhookSecret(def))
		case "connector_event":
			e, _ := parseEvent(c, reg)
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, connector, trigger_name, events, connection)
				VALUES ($1, $2, $3, $4, $5, 'connector_event', $6, $7, $8, NULLIF($9, ''))`, id, tenant, wf, version, env, e.connector, e.trigger, e.events, e.connection)
		case "schedule":
			if env != ScheduleEnvironment {
				continue
			}
			s, _ := parseSchedule(c)
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, cron, timezone, next_fire_at)
				VALUES ($1, $2, $3, $4, $5, 'schedule', $6, $7, $8)`, id, tenant, wf, version, env, s.cron, s.tz.String(), s.Next(now))
		}
		if err != nil {
			return err
		}
	}
	return nil
}
