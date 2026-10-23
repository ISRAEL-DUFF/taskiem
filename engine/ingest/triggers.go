// Package ingest turns outside events into runs (spec 8): webhooks,
// connector webhooks (which also deliver signals), and schedules. Every
// path verifies, deduplicates, and records before acknowledging.
package ingest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // schedules name IANA zones; containers may lack zoneinfo

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/wd"
)

// ErrTriggerTaken means another workflow already holds a trigger's
// address in the environment (a USSD service code).
var ErrTriggerTaken = errors.New("trigger taken")

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
	options                        map[string]any
	remote                         bool
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

func parseEvent(c map[string]any, reg connector.Lookup) (eventConfig, error) {
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
	e.remote = spec.Remote()
	opts, given := c["options"]
	if given {
		if e.options, ok = opts.(map[string]any); !ok {
			return e, fmt.Errorf("trigger options must be an object")
		}
	}
	if len(spec.Options) == 0 {
		if given {
			return e, fmt.Errorf("trigger %q of %s takes no options", e.trigger, e.connector)
		}
		return e, nil
	}
	if e.options == nil {
		e.options = map[string]any{}
	}
	raw, _ := json.Marshal(e.options)
	if msgs := schemacheck.New("https://schemas.taskiem.dev/connectors/"+e.connector+"/triggers/"+e.trigger+"/options.json", spec.Options).Validate(raw); len(msgs) > 0 {
		return e, fmt.Errorf("trigger options: %s", strings.Join(msgs, "; "))
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
	// "0 0 30 2 *" parses but never comes round: Next is the zero time.
	if s.Next(time.Now()).IsZero() {
		return s, fmt.Errorf("cron %q never fires", s.cron)
	}
	return s, nil
}

// Next is the first fire time strictly after t, in the schedule's zone.
func (s scheduleConfig) Next(t time.Time) time.Time { return s.sched.Next(t.In(s.tz)).UTC() }

// Check reports why a definition's trigger cannot be registered here.
func Check(def *wd.Definition, reg connector.Lookup) error {
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
	case "capability_event":
		return checkCapabilityEvent(c, reg)
	case "schedule":
		_, err := parseSchedule(c)
		return err
	case "ussd":
		// The menu itself is checked by wd.Validate (docs/ussd.md).
		if sc := str(c, "service_code"); sc == "" {
			return fmt.Errorf("a ussd trigger needs a service_code")
		}
		return nil
	case "storefront":
		switch str(c, "action") {
		case "order", "enquiry", "callback", "booking":
			return nil
		}
		return fmt.Errorf("a storefront trigger's action is order, enquiry, callback or booking")
	case "invoice":
		if str(c, "event") != "paid" {
			return fmt.Errorf("an invoice trigger's event is paid")
		}
		return nil
	case "form":
		// The pages, conditions and fields are checked by wd.Validate.
		_, err := def.FormTriggerConfig()
		return err
	case "email":
		// The senders and attachments are checked by the schema.
		return nil
	}
	return fmt.Errorf("%s triggers are not available yet", def.Trigger.Type)
}

// Sync replaces a workflow's registered triggers in every environment with
// those of the version being published, inside the publishing transaction.
// Webhook and connector triggers are registered per environment
// (deliveries name theirs with ?env=, default prod); schedules fire in prod
// only.
func Sync(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, version int, def *wd.Definition, reg connector.Lookup, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT name FROM environments ORDER BY name`)
	if err != nil {
		return err
	}
	envs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	return SyncEnvironments(ctx, tx, tenant, wf, envs, version, def, reg, now)
}

// SyncEnvironments replaces a workflow's triggers in the given environments
// only, with those of the version deployed there.
func SyncEnvironments(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, envs []string, version int, def *wd.Definition, reg connector.Lookup, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM triggers WHERE workflow_id = $1 AND environment = ANY ($2)`, wf, envs); err != nil {
		return err
	}
	if err := Check(def, reg); err != nil {
		return err
	}
	var err error
	c := def.Trigger.Config
	// A remotely registered trigger's subscription is declared here, in the
	// deploying transaction; one this version no longer has is released.
	// The reconciler (engine/remote) calls the provider afterwards.
	var ev eventConfig
	if def.Trigger.Type == "connector_event" {
		ev, _ = parseEvent(c, reg)
	}
	if !ev.remote {
		if err := remote.Release(ctx, tx, wf, envs); err != nil {
			return err
		}
	}
	// A workflow that no longer has a form trigger closes its form.
	if def.Trigger.Type != "form" {
		if _, err := tx.Exec(ctx, `UPDATE form_endpoints SET status = 'closed', updated_at = now()
			WHERE workflow_id = $1 AND environment = ANY ($2) AND status IN ('live', 'paused')`, wf, envs); err != nil {
			return err
		}
	}
	// One that no longer starts from email stops taking mail.
	if def.Trigger.Type != "email" {
		if _, err := tx.Exec(ctx, `UPDATE email_endpoints SET status = 'closed', updated_at = now()
			WHERE workflow_id = $1 AND environment = ANY ($2) AND status = 'live'`, wf, envs); err != nil {
			return err
		}
	}
	for _, env := range envs {
		id := uuid.Must(uuid.NewV7())
		switch def.Trigger.Type {
		case "webhook":
			w, _ := parseWebhook(c)
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, path, auth, dedup, secret_name)
				VALUES ($1, $2, $3, $4, $5, 'webhook', $6, $7, NULLIF($8, ''), $9)`, id, tenant, wf, version, env, w.path, w.auth, w.dedup, WebhookSecret(def))
		case "connector_event":
			e := ev
			var opts []byte
			if e.options != nil {
				opts, _ = json.Marshal(e.options)
			}
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, connector, trigger_name, events, connection, options)
				VALUES ($1, $2, $3, $4, $5, 'connector_event', $6, $7, $8, NULLIF($9, ''), $10)`, id, tenant, wf, version, env, e.connector, e.trigger, e.events, e.connection, opts)
			if err == nil && e.remote {
				_, err = remote.Declare(ctx, tx, tenant, wf, env, version, remote.Declaration{
					Connector: e.connector, Trigger: e.trigger, Connection: e.connection, Events: e.events, Options: e.options})
			}
		case "capability_event":
			err = registerCapabilityEvent(ctx, tx, tenant, wf, version, env, c, reg)
		case "schedule":
			if env != ScheduleEnvironment {
				continue
			}
			s, _ := parseSchedule(c)
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, cron, timezone, next_fire_at)
				VALUES ($1, $2, $3, $4, $5, 'schedule', $6, $7, $8)`, id, tenant, wf, version, env, s.cron, s.tz.String(), s.Next(now))
		case "ussd":
			// One workflow per service code in an environment: the edge
			// routes a USSD session by the code the caller dialled.
			code := str(c, "service_code")
			var other string
			e := tx.QueryRow(ctx, `SELECT w.name FROM triggers t JOIN workflows w ON w.id = t.workflow_id
				WHERE t.type = 'ussd' AND t.environment = $1 AND t.service_code = $2 AND t.workflow_id <> $3`, env, code, wf).Scan(&other)
			if e == nil {
				return fmt.Errorf("%w: USSD service code %s is already used by workflow %q in %s", ErrTriggerTaken, code, other, env)
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, service_code)
				VALUES ($1, $2, $3, $4, $5, 'ussd', $6)`, id, tenant, wf, version, env, code)
		case "storefront":
			// One workflow per storefront form in an environment: a
			// submission starts the one deployed for its form.
			action := str(c, "action")
			var other string
			e := tx.QueryRow(ctx, `SELECT w.name FROM triggers t JOIN workflows w ON w.id = t.workflow_id
				WHERE t.type = 'storefront' AND t.environment = $1 AND t.storefront_action = $2 AND t.workflow_id <> $3`, env, action, wf).Scan(&other)
			if e == nil {
				return fmt.Errorf("%w: the storefront's %s form already starts workflow %q in %s", ErrTriggerTaken, action, other, env)
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action)
				VALUES ($1, $2, $3, $4, $5, 'storefront', $6)`, id, tenant, wf, version, env, action)
		case "invoice":
			// Any number of workflows may start on an invoice being paid.
			// Payments are counted in prod only, so only prod's start; the
			// other environments' rows say what is deployed there.
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, events)
				VALUES ($1, $2, $3, $4, $5, 'invoice', $6)`, id, tenant, wf, version, env, []string{str(c, "event")})
		case "form":
			// The form is live at its endpoint's public id, which stays
			// the same across versions; a paused or closed form stays so.
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type)
				VALUES ($1, $2, $3, $4, $5, 'form')`, id, tenant, wf, version, env)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO form_endpoints (id, tenant_id, workflow_id, environment, public_id, version, form)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
					ON CONFLICT (workflow_id, environment) DO UPDATE SET version = EXCLUDED.version, form = EXCLUDED.form, updated_at = now()`,
					uuid.Must(uuid.NewV7()), tenant, wf, env, NewFormID(), version, formCopy(def))
			}
		case "email":
			// Mail is taken at the endpoint's address, which stays the same
			// across versions; a closed one opens again (a suspended one
			// stays so).
			_, err = tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type)
				VALUES ($1, $2, $3, $4, $5, 'email')`, id, tenant, wf, version, env)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO email_endpoints (id, tenant_id, workflow_id, environment, public_id, version)
					VALUES ($1, $2, $3, $4, $5, $6)
					ON CONFLICT (workflow_id, environment) DO UPDATE SET version = EXCLUDED.version,
					  status = CASE WHEN email_endpoints.status = 'suspended' THEN 'suspended' ELSE 'live' END, updated_at = now()`,
					uuid.Must(uuid.NewV7()), tenant, wf, env, NewFormID(), version)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// NewFormID is a form's public id: 12 random lowercase letters and digits
// (60 bits; 32 letters, so unbiased), in the form's address.
func NewFormID() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// formCopy is what the form host may read of a form's definition: its
// trigger, inputs schema and the types it uses, never the steps.
func formCopy(def *wd.Definition) []byte {
	out := map[string]json.RawMessage{}
	if t, err := json.Marshal(def.Trigger); err == nil {
		out["trigger"] = t
	}
	if def.RawInputs != nil {
		if in, err := json.Marshal(def.RawInputs); err == nil {
			out["inputs"] = in
		}
	}
	if len(def.RawTypes) > 0 {
		if ty, err := json.Marshal(def.RawTypes); err == nil {
			out["types"] = ty
		}
	}
	if f := ReachFields(def); len(f) > 0 {
		out["reach"], _ = json.Marshal(f)
	}
	raw, _ := json.Marshal(out)
	return raw
}

var (
	bodyFieldRe  = regexp.MustCompile(`trigger\.body\.([A-Za-z_][A-Za-z0-9_]*)`)
	respondentRe = regexp.MustCompile(`trigger\.form\.respondent\.phone`)
)

// ReachFields are the form fields holding phones the workflow messages
// with notify@1 message ("$respondent" for the phone the form verified):
// the form's thank-you page offers to send those updates on WhatsApp or
// Telegram (no-technical-steps N11).
func ReachFields(def *wd.Definition) []string {
	var out []string
	def.Walk(func(st *wd.Step) {
		if st.Type != "connector" || st.Connector != "notify@1" || st.Action != "message" {
			return
		}
		phone, _ := st.Input["phone"].(string)
		for _, m := range bodyFieldRe.FindAllStringSubmatch(phone, -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
		if respondentRe.MatchString(phone) && !slices.Contains(out, "$respondent") {
			out = append(out, "$respondent")
		}
	})
	return out
}
