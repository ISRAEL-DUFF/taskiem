// Package alerts watches each tenant's runs, approvals, connectors,
// credentials and audit anchors (spec 15.1), records what its rules find
// once, and delivers it to email, Slack, WhatsApp or a signed webhook, retrying with
// backoff until delivered or out of attempts.
package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// VaultEnv is the vault environment holding channels' secrets: the Slack
// webhook URL, or a webhook's URL signing key.
const VaultEnv = "_alerts"

// SecretName is the vault name of a channel's secret.
func SecretName(channel uuid.UUID) string { return "channel_" + channel.String() }

// Rule kinds.
const (
	RunFailed           = "run_failed"
	SlowRun             = "slow_run"
	StuckApproval       = "stuck_approval"
	NeedsReconciliation = "needs_reconciliation"
	ConnectorDrift      = "connector_drift"
	CredentialExpiry    = "credential_expiry" //nolint:gosec // a rule kind, not a credential
	AuditAnchor         = "audit_anchor"
	// LimitReached: the tenant reached a plan limit (a quota, the backlog,
	// the ingest rate...); once per limit per UTC day.
	LimitReached = "limit"
	// RepairProposed: the repair pipeline proposed a fix for a failed run,
	// or found something a person must do (docs/ai.md); once per proposal.
	RepairProposed = "repair_proposed"
	// KeyHealth: the tenant's customer key (BYOK) became unavailable, so
	// steps that need secrets park, or works again (docs/byok.md); once
	// per outage and once per recovery. Email and WhatsApp channels still
	// deliver while it is unavailable; Slack and webhook channels need
	// their own secret, which is then unreadable too.
	KeyHealth = "key_health"
)

// Kinds lists rule kinds with their default thresholds (zero: none).
var Kinds = map[string]time.Duration{
	RunFailed:           0,
	SlowRun:             time.Hour,
	StuckApproval:       24 * time.Hour,
	NeedsReconciliation: 0,
	ConnectorDrift:      0,
	CredentialExpiry:    7 * 24 * time.Hour,
	AuditAnchor:         0,
	LimitReached:        0,
	RepairProposed:      0,
	KeyHealth:           0,
}

// limitWhat says what reaching each limit did, for limit alerts.
var limitWhat = map[string]string{ //nolint:gosec // limit names and explanations, not credentials
	"runs_per_month":    "New runs are refused until next month (UTC) or until the plan's quota is raised. Deliveries get 429 so providers retry; schedules skip their fires.",
	"runs_per_day":      "New runs are refused until tomorrow (UTC) or until the plan's quota is raised. Deliveries get 429 so providers retry; schedules skip their fires.",
	"max_queued_runs":   "The backlog of queued runs is full: new starts are refused with 429 until queued runs start.",
	"ingest_ceiling":    "Deliveries arrived faster than the plan's ceiling and some were refused with 429 (providers retry).",
	"ingest_rate":       "Deliveries arrived faster than the plan's ingest rate: their runs were accepted and queued, and start at the plan's rate.",
	"max_running_runs":  "As many runs as the plan allows are running: new runs are accepted and queued until some finish.",
	"max_steps_per_run": "A run scheduled more steps than the plan allows one run, and was failed.",
	"max_payload_bytes": "A delivery was larger than the plan allows and was refused.",
	"max_workflows":     "Creating a workflow was refused: the plan's workflow limit is reached.",
	"max_secrets":       "Creating a secret was refused: the plan's secret limit is reached.",
	"max_connections":   "Creating a connection was refused: the plan's connection limit is reached.",
}

// RuleConfig narrows a rule.
type RuleConfig struct {
	WorkflowID  string `json:"workflow_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	Threshold   string `json:"threshold,omitempty"` // Go duration, e.g. "30m"
}

// ThresholdFor reads a rule's threshold, falling back to the kind's default.
func (c RuleConfig) ThresholdFor(kind string) (time.Duration, error) {
	if c.Threshold == "" {
		return Kinds[kind], nil
	}
	d, err := time.ParseDuration(c.Threshold)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("threshold %q is not a positive duration such as 30m or 48h", c.Threshold)
	}
	return d, nil
}

// Mailer sends an email.
type Mailer interface {
	Send(ctx context.Context, from string, to []string, msg []byte) error
}

// Secrets reads channels' secrets from the vault.
type Secrets interface {
	Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error)
}

// WhatsApp sends an alert to members' bound WhatsApp numbers: a template
// outside their 24-hour window (whatsapp.Platform).
type WhatsApp interface {
	Notify(ctx context.Context, tenant uuid.UUID, members []uuid.UUID, kind, title, body, link string, detail map[string]any) error
	// Queue hands a recorded alert to the WhatsApp outbox, one message per
	// member with a number, each retried on its own
	// (docs/whatsapp.md#delivery-and-retries).
	Queue(ctx context.Context, tenant, alert uuid.UUID, members []uuid.UUID) error
}

// Alerter evaluates rules and delivers alerts.
type Alerter struct {
	Pool    *pgxpool.Pool
	Secrets Secrets
	Egress  *egress.Guard
	// Mailer sends email; nil leaves email deliveries failing with a reason.
	Mailer Mailer
	From   string // sender address for email
	// WhatsApp delivers to whatsapp channels; nil leaves them failing with
	// a reason.
	WhatsApp WhatsApp
	// PublicURL builds links back into the web app.
	PublicURL string
	Interval  time.Duration // default 30s
	Logger    *slog.Logger
	// Client, when set, replaces the egress-guarded client (tests).
	Client *http.Client
	// Rewrite, when set, changes a destination URL before sending (tests).
	Rewrite func(string) string
	// Now is the clock (tests).
	Now func() time.Time
}

func (a *Alerter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Alerter) log() *slog.Logger {
	if a.Logger == nil {
		return slog.Default()
	}
	return a.Logger
}

// Run evaluates and delivers on a loop until ctx ends.
func (a *Alerter) Run(ctx context.Context) error {
	every := a.Interval
	if every <= 0 {
		every = 30 * time.Second
	}
	for {
		if err := a.Tick(ctx); err != nil && ctx.Err() == nil {
			a.log().Error("alerts", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick evaluates every tenant's rules and delivers what is due.
func (a *Alerter) Tick(ctx context.Context) error {
	rows, err := a.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_alert_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := a.Evaluate(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: evaluate: %w", t, err))
		}
		if err := a.Deliver(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: deliver: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

// Alert is one finding.
type Alert struct {
	Kind   string         `json:"kind"`
	Dedup  string         `json:"-"`
	Title  string         `json:"title"`
	Body   string         `json:"body"`
	Link   string         `json:"link,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

type rule struct {
	id       uuid.UUID
	name     string
	kind     string
	cfg      RuleConfig
	channels []uuid.UUID
	since    time.Time
}

// Evaluate runs a tenant's enabled rules and records new alerts, with a
// delivery to each of the rule's channels.
func (a *Alerter) Evaluate(ctx context.Context, tenant uuid.UUID) error {
	now := a.now()
	return db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, kind, config, channel_ids, checked_until FROM alert_rules WHERE enabled ORDER BY created_at FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		var rules []rule
		for rows.Next() {
			var r rule
			var raw []byte
			if err := rows.Scan(&r.id, &r.name, &r.kind, &raw, &r.channels, &r.since); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal(raw, &r.cfg)
			rules = append(rules, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range rules {
			found, err := a.find(ctx, tx, r, now)
			if err != nil {
				return fmt.Errorf("rule %s: %w", r.name, err)
			}
			for _, al := range found {
				if err := a.record(ctx, tx, tenant, &r, al); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE alert_rules SET checked_until = $2 WHERE id = $1`, r.id, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// record stores an alert once per rule and dedup key, queueing deliveries.
func (a *Alerter) record(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r *rule, al Alert) error {
	id := uuid.Must(uuid.NewV7())
	var rid *uuid.UUID
	if r != nil {
		rid = &r.id
	}
	detail, _ := json.Marshal(nonNil(al.Detail))
	tag, err := tx.Exec(ctx, `INSERT INTO alerts (id, tenant_id, rule_id, kind, dedup_key, title, body, link, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)
		ON CONFLICT (rule_id, dedup_key) WHERE rule_id IS NOT NULL DO NOTHING`, id, tenant, rid, al.Kind, al.Dedup, al.Title, al.Body, al.Link, detail)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if r == nil {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO alert_deliveries (alert_id, channel_id, tenant_id)
		SELECT $1, c.id, $2 FROM alert_channels c WHERE c.id = ANY ($3) AND c.disabled_at IS NULL`, id, tenant, r.channels)
	return err
}

// Notify records an alert that concerns the tenant whatever its rules say
// (a catalogue connector it installed was revoked), once per kind and
// dedup key, and queues it to every enabled channel. tx must have the
// tenant in scope.
func Notify(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, al Alert) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerts WHERE tenant_id = $1 AND rule_id IS NULL AND kind = $2 AND dedup_key = $3)`,
		tenant, al.Kind, al.Dedup).Scan(&exists); err != nil || exists {
		return false, err
	}
	id := uuid.Must(uuid.NewV7())
	detail, _ := json.Marshal(nonNil(al.Detail))
	if _, err := tx.Exec(ctx, `INSERT INTO alerts (id, tenant_id, rule_id, kind, dedup_key, title, body, link, detail) VALUES ($1, $2, NULL, $3, $4, $5, $6, NULLIF($7, ''), $8)`,
		id, tenant, al.Kind, al.Dedup, al.Title, al.Body, al.Link, detail); err != nil {
		return false, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO alert_deliveries (alert_id, channel_id, tenant_id)
		SELECT $1, c.id, $2 FROM alert_channels c WHERE c.tenant_id = $2 AND c.disabled_at IS NULL`, id, tenant)
	return err == nil, err
}

// ConnectorRevoked is the kind of the alert a tenant gets when a catalogue
// connector version it installed is revoked (Notify).
const ConnectorRevoked = "connector_revoked"

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (a *Alerter) link(path string) string {
	if a.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(a.PublicURL, "/") + path
}

// filters narrows run queries by workflow and environment; args are
// appended after those already given.
func filters(cfg RuleConfig, alias string, args []any) (string, []any) {
	where := ""
	if cfg.WorkflowID != "" {
		args = append(args, cfg.WorkflowID)
		where += fmt.Sprintf(" AND %s.workflow_id::text = $%d", alias, len(args))
	}
	if cfg.Environment != "" {
		args = append(args, cfg.Environment)
		where += fmt.Sprintf(" AND %s.environment = $%d", alias, len(args))
	}
	return where, args
}

// find looks for what a rule watches.
func (a *Alerter) find(ctx context.Context, tx pgx.Tx, r rule, now time.Time) ([]Alert, error) {
	threshold, err := r.cfg.ThresholdFor(r.kind)
	if err != nil {
		return nil, err
	}
	var out []Alert
	runAlert := func(q string, args []any, mk func(id uuid.UUID, wf, env string, at time.Time) Alert) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var wf, env string
			var at time.Time
			if err := rows.Scan(&id, &wf, &env, &at); err != nil {
				return err
			}
			out = append(out, mk(id, wf, env, at))
		}
		return rows.Err()
	}
	runDetail := func(id uuid.UUID, wf, env string) map[string]any {
		return map[string]any{"run_id": id, "workflow": wf, "environment": env}
	}
	switch r.kind {
	case RunFailed:
		where, args := filters(r.cfg, "r", []any{r.since, now})
		err = runAlert(`SELECT r.id, w.name, r.environment, r.ended_at FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.status = 'failed' AND r.ended_at > $1 AND r.ended_at <= $2`+where+` ORDER BY r.ended_at LIMIT 200`, args,
			func(id uuid.UUID, wf, env string, at time.Time) Alert {
				return Alert{Kind: r.kind, Dedup: id.String(), Title: fmt.Sprintf("%s failed in %s", wf, env),
					Body: fmt.Sprintf("Run %s of %s failed at %s.", id, wf, at.UTC().Format(time.RFC1123)), Link: a.link("/runs/" + id.String()), Detail: runDetail(id, wf, env)}
			})
	case NeedsReconciliation:
		where, args := filters(r.cfg, "r", nil)
		err = runAlert(`SELECT r.id, w.name, r.environment, r.started_at FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.status = 'needs_reconciliation'`+where+` ORDER BY r.started_at LIMIT 200`, args,
			func(id uuid.UUID, wf, env string, _ time.Time) Alert {
				return Alert{Kind: r.kind, Dedup: id.String(), Title: fmt.Sprintf("%s needs reconciliation in %s", wf, env),
					Body: fmt.Sprintf("Run %s of %s made a call whose outcome is unknown. Someone with run.resolve must check with the provider and resolve the step.", id, wf),
					Link: a.link("/runs/" + id.String()), Detail: runDetail(id, wf, env)}
			})
	case SlowRun:
		where, args := filters(r.cfg, "r", []any{now.Add(-threshold)})
		err = runAlert(`SELECT r.id, w.name, r.environment, r.started_at FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.status IN ('queued', 'running', 'waiting') AND r.started_at < $1`+where+` ORDER BY r.started_at LIMIT 200`, args,
			func(id uuid.UUID, wf, env string, at time.Time) Alert {
				return Alert{Kind: r.kind, Dedup: id.String(), Title: fmt.Sprintf("%s has been running over %s in %s", wf, threshold, env),
					Body: fmt.Sprintf("Run %s of %s started at %s and has not finished.", id, wf, at.UTC().Format(time.RFC1123)), Link: a.link("/runs/" + id.String()), Detail: runDetail(id, wf, env)}
			})
	case StuckApproval:
		where, args := filters(r.cfg, "r", []any{now.Add(-threshold)})
		rows, qerr := tx.Query(ctx, `SELECT ap.run_id, ap.step_id, w.name, r.environment, ap.requested_at, COALESCE(ap.role, ap.policy, '') FROM approvals ap
			JOIN runs r ON r.id = ap.run_id JOIN workflows w ON w.id = r.workflow_id
			WHERE ap.status = 'open' AND ap.requested_at < $1`+where+` ORDER BY ap.requested_at LIMIT 200`, args...)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var run uuid.UUID
			var step, wf, env, who string
			var at time.Time
			if err := rows.Scan(&run, &step, &wf, &env, &at, &who); err != nil {
				return nil, err
			}
			out = append(out, Alert{Kind: r.kind, Dedup: run.String() + "/" + step, Title: fmt.Sprintf("Approval waiting over %s: %s", threshold, wf),
				Body: fmt.Sprintf("Step %s of run %s (%s, %s) has waited for %s since %s.", step, run, wf, env, who, at.UTC().Format(time.RFC1123)),
				Link: a.link("/approvals"), Detail: map[string]any{"run_id": run, "step": step, "workflow": wf, "environment": env}})
		}
		err = rows.Err()
	case ConnectorDrift:
		rows, qerr := tx.Query(ctx, `SELECT connector, version, action, path, kind, expected, observed, first_seen FROM connector_drift
			WHERE acknowledged_at IS NULL AND first_seen > $1 ORDER BY first_seen LIMIT 200`, r.since)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var conn, ver, action, path, kind, expected, observed string
			var at time.Time
			if err := rows.Scan(&conn, &ver, &action, &path, &kind, &expected, &observed, &at); err != nil {
				return nil, err
			}
			out = append(out, Alert{Kind: r.kind, Dedup: strings.Join([]string{conn, ver, action, path, kind}, "|"), Title: fmt.Sprintf("%s changed its response to %s", conn, action),
				Body: fmt.Sprintf("%s %s: at %s the contract says %s, the provider sent %s (%s).", conn, action, path, expected, observed, kind),
				Link: a.link("/connections"), Detail: map[string]any{"connector": conn, "version": ver, "action": action, "path": path, "kind": kind}})
		}
		err = rows.Err()
	case CredentialExpiry:
		horizon := now.Add(threshold)
		rows, qerr := tx.Query(ctx, `SELECT 'connection', id, connector || ' / ' || name || ' (' || environment || ')', expires_at FROM connections
			WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at < $1
			UNION ALL
			SELECT 'api key', id, name, expires_at FROM api_keys WHERE revoked_at IS NULL AND expires_at > $2 AND expires_at < $1`, horizon, now)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var what, name string
			var id uuid.UUID
			var at time.Time
			if err := rows.Scan(&what, &id, &name, &at); err != nil {
				return nil, err
			}
			verb := "expires"
			if !at.After(now) {
				verb = "expired"
			}
			out = append(out, Alert{Kind: r.kind, Dedup: id.String() + "@" + at.UTC().Format(time.RFC3339), Title: fmt.Sprintf("The %s %s %s %s", what, name, verb, at.UTC().Format("2 Jan 2006")),
				Body:   fmt.Sprintf("The %s %s %s at %s. Renew it before work that uses it fails.", what, name, verb, at.UTC().Format(time.RFC1123)),
				Detail: map[string]any{"type": what, "id": id, "name": name, "expires_at": at}})
		}
		err = rows.Err()
	case AuditAnchor:
		rows, qerr := tx.Query(ctx, `SELECT tenant_id::text, chain_seq, encode(head_hash, 'hex'), to_char(anchored_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			key_id, translate(encode(signature, 'base64'), E'\n', '') FROM audit_anchors WHERE anchored_at > $1 ORDER BY chain_seq`, r.since)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var anchor struct {
				TenantID  string `json:"tenant_id"`
				Seq       int64  `json:"seq"`
				Hash      string `json:"hash"`
				At        string `json:"anchored_at"`
				KeyID     string `json:"key_id"`
				Signature string `json:"signature"`
			}
			if err := rows.Scan(&anchor.TenantID, &anchor.Seq, &anchor.Hash, &anchor.At, &anchor.KeyID, &anchor.Signature); err != nil {
				return nil, err
			}
			line, _ := json.Marshal(anchor)
			out = append(out, Alert{Kind: r.kind, Dedup: strconv.FormatInt(anchor.Seq, 10), Title: fmt.Sprintf("Audit anchor: entry %d", anchor.Seq),
				Body: "Keep this message: it is a signed record of your audit log as it stood, held outside Taskiem. " +
					"To check the log later, save the line below in a file and run `taskiem audit verify --anchors FILE` on an export.\n\n" + string(line),
				Link: a.link("/audit"), Detail: map[string]any{"anchor": json.RawMessage(line)}})
		}
		err = rows.Err()
	case LimitReached:
		rows, qerr := tx.Query(ctx, `SELECT limit_name, day::text, hits, last_at FROM tenant_limit_hits WHERE last_at > $1 ORDER BY last_at LIMIT 200`, r.since)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		for rows.Next() {
			var limit, day string
			var hits int64
			var at time.Time
			if err := rows.Scan(&limit, &day, &hits, &at); err != nil {
				return nil, err
			}
			out = append(out, Alert{Kind: r.kind, Dedup: limit + "@" + day, Title: fmt.Sprintf("Plan limit reached: %s", limit),
				Body: strings.TrimSpace(fmt.Sprintf("On %s (UTC) the %s limit was reached, most recently at %s. %s See Settings for your limits and usage; ask your operator to raise them.",
					day, limit, at.UTC().Format(time.RFC1123), limitWhat[limit])),
				Link: a.link("/settings"), Detail: map[string]any{"limit": limit, "day": day}})
		}
		err = rows.Err()
	case RepairProposed:
		out, err = a.findRepairs(ctx, tx, r, now)
	case KeyHealth:
		out, err = a.findKeyHealth(ctx, tx, r)
	default:
		return nil, fmt.Errorf("unknown rule kind %q", r.kind)
	}
	return out, err
}

// findKeyHealth reports customer keys that are unavailable now (once per
// outage, whenever it started) and those that recovered since the rule
// last looked.
func (a *Alerter) findKeyHealth(ctx context.Context, tx pgx.Tx, r rule) ([]Alert, error) {
	rows, err := tx.Query(ctx, `SELECT id, description, status, failing_since, recovered_at, COALESCE(last_error, '') FROM tenant_byok_keys
		WHERE (status = 'unavailable' AND failing_since IS NOT NULL) OR (status = 'active' AND recovered_at > $1) ORDER BY updated_at LIMIT 50`, r.since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var id uuid.UUID
		var desc, status, lastErr string
		var failing, recovered *time.Time
		if err := rows.Scan(&id, &desc, &status, &failing, &recovered, &lastErr); err != nil {
			return nil, err
		}
		if status == "unavailable" {
			out = append(out, Alert{Kind: r.kind, Dedup: id.String() + "@" + failing.UTC().Format(time.RFC3339), Title: "Your encryption key is unavailable",
				Body: fmt.Sprintf("Taskiem cannot use %s (failing since %s: %s). Steps that need secrets or personal data are paused, not failed, and resume by themselves when the key works again. "+
					"Check the key is enabled and Taskiem's access to it is allowed; see Settings > Encryption keys.", desc, failing.UTC().Format(time.RFC1123), truncate(lastErr)),
				Link: a.link("/settings/keys"), Detail: map[string]any{"byok_key_id": id, "status": status}})
			continue
		}
		out = append(out, Alert{Kind: r.kind, Dedup: id.String() + "@ok@" + recovered.UTC().Format(time.RFC3339), Title: "Your encryption key works again",
			Body: fmt.Sprintf("%s is available again since %s. Paused steps are resuming.", desc, recovered.UTC().Format(time.RFC1123)),
			Link: a.link("/settings/keys"), Detail: map[string]any{"byok_key_id": id, "status": status}})
	}
	return out, rows.Err()
}

// repairClass says in words what kind of failure a proposal is about.
var repairClass = map[string]string{ //nolint:gosec // repair classes and their descriptions, not credentials
	"transient":       "a temporary failure: retrying from the failed step is proposed",
	"credential":      "a credential problem: reconnect, then retry",
	"data":            "bad or unexpected data: a change to the workflow is proposed",
	"schema_drift":    "a provider changed its responses: a change to the workflow is proposed",
	"logic":           "a mistake in the workflow: a change is proposed",
	"unknown_outcome": "a call whose outcome is unknown: check with the provider",
}

// findRepairs finds proposals that became ready for a person since the
// rule last looked. The message is masked: the workflow, environment,
// class and a link only, never the explanation, diff or evidence (which
// may quote run data); people read those on the run's page.
func (a *Alerter) findRepairs(ctx context.Context, tx pgx.Tx, r rule, now time.Time) ([]Alert, error) {
	where, args := filters(r.cfg, "p", []any{r.since, now})
	rows, err := tx.Query(ctx, `SELECT p.id, p.run_id, w.name, p.environment, COALESCE(p.class, ''), p.status FROM repair_proposals p
		JOIN workflows w ON w.id = p.workflow_id
		WHERE p.status IN ('proposed', 'action') AND p.finished_at > $1 AND p.finished_at <= $2`+where+` ORDER BY p.finished_at LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var id, run uuid.UUID
		var wf, env, class, status string
		if err := rows.Scan(&id, &run, &wf, &env, &class, &status); err != nil {
			return nil, err
		}
		what := repairClass[class]
		if what == "" {
			what = "a failure"
		}
		title := fmt.Sprintf("Fix proposed for %s in %s", wf, env)
		if status == "action" {
			title = fmt.Sprintf("%s in %s needs a person to act", wf, env)
		}
		body := fmt.Sprintf("Run %s of %s failed with %s. Review it on the run's page: nothing changes until someone allowed to accepts it.", run, wf, what)
		out = append(out, Alert{Kind: r.kind, Dedup: id.String(), Title: title, Body: body, Link: a.link("/runs/" + run.String()),
			Detail: map[string]any{"run_id": run, "proposal_id": id, "workflow": wf, "environment": env, "class": class, "status": status}})
	}
	return out, rows.Err()
}

// --- delivery ---

// maxAttempts bounds retries; backoff grows from a minute to six hours.
const maxAttempts = 8

func backoff(attempt int) time.Duration {
	d := time.Minute << (2 * (attempt - 1)) // 1m, 4m, 16m, 64m, ...
	return min(d, 6*time.Hour)
}

type delivery struct {
	alert    uuid.UUID
	channel  uuid.UUID
	attempts int
}

// Deliver sends a tenant's due deliveries. Each is claimed first, so two
// alerters never send the same one at once, and sent outside the database
// transaction.
func (a *Alerter) Deliver(ctx context.Context, tenant uuid.UUID) error {
	var due []delivery
	err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE alert_deliveries d SET next_attempt_at = now() + interval '5 minutes', attempts = d.attempts + 1
			WHERE (d.alert_id, d.channel_id) IN (SELECT alert_id, channel_id FROM alert_deliveries
			  WHERE status = 'pending' AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT 50 FOR UPDATE SKIP LOCKED)
			RETURNING d.alert_id, d.channel_id, d.attempts`)
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (delivery, error) {
			var d delivery
			return d, r.Scan(&d.alert, &d.channel, &d.attempts)
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, d := range due {
		sendErr := a.send(ctx, tenant, d.alert, d.channel)
		err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			switch {
			case sendErr == nil:
				_, err := tx.Exec(ctx, `UPDATE alert_deliveries SET status = 'sent', sent_at = now(), last_error = NULL WHERE alert_id = $1 AND channel_id = $2`, d.alert, d.channel)
				return err
			case d.attempts >= maxAttempts:
				_, err := tx.Exec(ctx, `UPDATE alert_deliveries SET status = 'failed', last_error = $3 WHERE alert_id = $1 AND channel_id = $2`, d.alert, d.channel, truncate(sendErr.Error()))
				return err
			default:
				_, err := tx.Exec(ctx, `UPDATE alert_deliveries SET last_error = $3, next_attempt_at = $4 WHERE alert_id = $1 AND channel_id = $2`,
					d.alert, d.channel, truncate(sendErr.Error()), a.now().Add(backoff(d.attempts)))
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

// message is what is sent: the alert as recorded.
type message struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Rule      *string         `json:"rule"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Link      *string         `json:"link,omitempty"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

func (a *Alerter) send(ctx context.Context, tenant, alertID, channelID uuid.UUID) error {
	var m message
	var kind string
	var cfgRaw []byte
	err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT a.id, a.kind, r.name, a.title, a.body, a.link, a.detail, a.created_at FROM alerts a LEFT JOIN alert_rules r ON r.id = a.rule_id WHERE a.id = $1`, alertID).
			Scan(&m.ID, &m.Kind, &m.Rule, &m.Title, &m.Body, &m.Link, &m.Detail, &m.CreatedAt); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT kind, config FROM alert_channels WHERE id = $1`, channelID).Scan(&kind, &cfgRaw)
	})
	if err != nil {
		return err
	}
	return a.SendTo(ctx, tenant, channelID, kind, cfgRaw, m)
}

// SendTest delivers a test message to one channel now.
func (a *Alerter) SendTest(ctx context.Context, tenant, channelID uuid.UUID, kind string, cfg []byte) error {
	m := message{ID: uuid.Must(uuid.NewV7()), Kind: "test", Title: "Test alert from Taskiem", Body: "This channel receives Taskiem alerts.", Detail: json.RawMessage(`{}`), CreatedAt: a.now().UTC()}
	if a.WhiteLabel(ctx, tenant) {
		m.Title, m.Body = "Test alert", "This channel receives alerts."
	}
	return a.SendTo(ctx, tenant, channelID, kind, cfg, m)
}

// WhiteLabel reports whether messages sent on tenant's behalf leave out
// the platform's name (docs/embedding.md#white-label): a sub-tenant whose
// partner holds the white_label capability and has a white-label app.
// Errors count as no: the platform's name is the safe default.
func (a *Alerter) WhiteLabel(ctx context.Context, tenant uuid.UUID) bool {
	if a.Pool == nil {
		return false
	}
	var wl bool
	err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_tenant_white_label($1)`, tenant).Scan(&wl)
	})
	return err == nil && wl
}

// SendTo delivers a message to a channel of the given kind and config.
func (a *Alerter) SendTo(ctx context.Context, tenant, channelID uuid.UUID, kind string, cfgRaw []byte, m message) error {
	text := m.Body
	if m.Link != nil && *m.Link != "" {
		text += "\n\n" + *m.Link
	}
	switch kind {
	case "email":
		var cfg struct {
			To []string `json:"to"`
		}
		if err := json.Unmarshal(cfgRaw, &cfg); err != nil || len(cfg.To) == 0 {
			return errors.New("the channel has no recipients")
		}
		if a.Mailer == nil || a.From == "" {
			return errors.New("email is not configured on this deployment (TASKIEM_SMTP_URL, TASKIEM_ALERT_FROM)")
		}
		subject := "[Taskiem] " + m.Title
		if a.WhiteLabel(ctx, tenant) {
			subject = m.Title
		}
		return a.Mailer.Send(ctx, a.From, cfg.To, BuildEmail(a.From, cfg.To, subject, text, m.ID.String()))
	case "slack":
		hook, err := a.Secrets.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindAlertChannel, Purpose: secrets.PurposeAlertDeliver}), tenant, VaultEnv, SecretName(channelID))
		if err != nil {
			return fmt.Errorf("reading the Slack webhook: %w", err)
		}
		body, _ := json.Marshal(map[string]any{"text": "*" + slackEscape(m.Title) + "*\n" + slackEscape(text)})
		return a.post(ctx, tenant, hook, body, nil)
	case "whatsapp":
		var cfg struct {
			Members []uuid.UUID `json:"members"`
		}
		if err := json.Unmarshal(cfgRaw, &cfg); err != nil || len(cfg.Members) == 0 {
			return errors.New("the channel names no members")
		}
		if a.WhatsApp == nil {
			return errors.New("WhatsApp is not configured on this deployment (TASKIEM_WHATSAPP_*)")
		}
		if m.Kind != "test" {
			// Delivered once queued: each member's message is retried by
			// the outbox, so a failure for one never resends to the rest.
			return a.WhatsApp.Queue(ctx, tenant, m.ID, cfg.Members)
		}
		var detail map[string]any
		_ = json.Unmarshal(m.Detail, &detail)
		link := ""
		if m.Link != nil {
			link = *m.Link
		}
		return a.WhatsApp.Notify(ctx, tenant, cfg.Members, m.Kind, m.Title, m.Body, link, detail)
	case "webhook":
		var cfg struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(cfgRaw, &cfg)
		key, err := a.Secrets.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindAlertChannel, Purpose: secrets.PurposeAlertDeliver}), tenant, VaultEnv, SecretName(channelID))
		if err != nil {
			return fmt.Errorf("reading the signing key: %w", err)
		}
		body, _ := json.Marshal(m)
		return a.post(ctx, tenant, cfg.URL, body, http.Header{
			"Taskiem-Signature": {Sign([]byte(key), a.now(), body)},
			"Taskiem-Alert-Id":  {m.ID.String()},
		})
	}
	return fmt.Errorf("unknown channel kind %q", kind)
}

// Sign is the Taskiem-Signature header of a signed webhook body sent at
// now: "t=<unix seconds>,v1=<hex HMAC-SHA256 of '<t>.' + body>". Alert
// webhooks and partner webhooks are signed alike, so receivers verify both
// the same way (and refuse old timestamps against replays).
func Sign(key []byte, now time.Time, body []byte) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// slackEscape escapes the three characters Slack's mrkdwn treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (a *Alerter) post(ctx context.Context, tenant uuid.UUID, dest string, body []byte, hdr http.Header) error {
	if a.Rewrite != nil {
		dest = a.Rewrite(dest)
	}
	// The destination may itself be a secret (a Slack webhook URL): errors,
	// which are stored and shown, name only its host.
	u, err := url.Parse(dest)
	if err != nil || u.Host == "" {
		return errors.New("bad destination URL")
	}
	client := a.Client
	if client == nil {
		g := a.Egress
		if g == nil {
			g = &egress.Guard{Logger: a.Logger}
		}
		client = g.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "alert"}, 15*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("posting to %s: bad request", u.Hostname())
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Taskiem-Alerts/1")
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("posting to %s: %w", u.Hostname(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %s", u.Hostname(), resp.Status)
	}
	return nil
}
