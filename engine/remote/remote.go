// Package remote keeps providers' subscriptions in step with deployed
// workflows, for connector triggers registered remotely (decision 0021,
// connector/v1 registration: remote): a PGDock webhook pointed at
// Taskiem's ingest URL, for example.
//
// The lifecycle has two halves. Deploying records intent: the deploying
// transaction (ingest.SyncEnvironments) declares what the provider should
// hold, or that a subscription should go, in remote_subscriptions, before
// anything is sent. The Reconciler then makes the provider agree: it
// creates the subscription (keeping the secret the provider returns in the
// vault), updates and repairs it on a redeploy, deletes it when the
// workflow leaves the environment, and checks its health. Every call is
// retried until it succeeds, so a crash anywhere leaves a row that the
// next pass finishes. A subscription is named after its row
// ("taskiem-<id>"), so one created just before a crash is found and
// adopted rather than left behind, and the row is deleted only once the
// provider's subscription is gone.
package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// SecretEnv is the vault environment holding subscriptions' signing
// secrets: a platform environment, never listed to the tenant.
const SecretEnv = "_remote"

// SecretName is the vault name of a subscription's signing secret.
func SecretName(id uuid.UUID) string { return "sub_" + strings.ReplaceAll(id.String(), "-", "") }

// Name is the provider-side name of a subscription: Taskiem's tag, by
// which it is found again.
func Name(id uuid.UUID) string { return "taskiem-" + id.String() }

// Declaration is a remote trigger a deployment wants.
type Declaration struct {
	Connector  string // "pgdock@1"
	Trigger    string
	Connection string // "" for the only active connection
	Events     []string
	Options    map[string]any
}

// Declare records, in the deploying transaction, that wf's version in env
// wants d at the provider. A live subscription for the same connector,
// trigger and connection is kept and marked to be applied again (which
// also repairs it); any other is marked to be removed and a new one is
// recorded. Nothing is sent here.
func Declare(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, env string, version int, d Declaration) (uuid.UUID, error) {
	opts, err := json.Marshal(nonNilMap(d.Options))
	if err != nil {
		return uuid.Nil, err
	}
	events := d.Events
	if events == nil {
		events = []string{}
	}
	var id uuid.UUID
	var conn, trig string
	var connection *string
	err = tx.QueryRow(ctx, `SELECT id, connector, trigger_name, connection FROM remote_subscriptions
		WHERE workflow_id = $1 AND environment = $2 AND desired = 'present' FOR UPDATE`, wf, env).Scan(&id, &conn, &trig, &connection)
	switch {
	case err == nil && conn == d.Connector && trig == d.Trigger && deref(connection) == d.Connection:
		_, err = tx.Exec(ctx, `UPDATE remote_subscriptions SET version = $2, events = $3, options = $4, applied_hash = NULL,
			next_attempt_at = now(), attempts = 0, updated_at = now() WHERE id = $1`, id, version, events, opts)
		return id, err
	case err == nil:
		if err := release(ctx, tx, `id = $1`, id); err != nil {
			return uuid.Nil, err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, err
	}
	id = uuid.Must(uuid.NewV7())
	_, err = tx.Exec(ctx, `INSERT INTO remote_subscriptions (id, tenant_id, workflow_id, environment, connector, trigger_name, connection, version, events, options, desired, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, 'present', now())`, id, tenant, wf, env, d.Connector, d.Trigger, d.Connection, version, events, opts)
	return id, err
}

// Release records, in the transaction that removes wf's triggers from envs
// (a redeploy without a remote trigger, an undeploy), that their
// subscriptions should be deleted at the provider.
func Release(ctx context.Context, tx pgx.Tx, wf uuid.UUID, envs []string) error {
	return release(ctx, tx, `workflow_id = $1 AND environment = ANY ($2)`, wf, envs)
}

func release(ctx context.Context, tx pgx.Tx, where string, args ...any) error {
	_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET desired = 'absent', next_attempt_at = now(), attempts = 0, updated_at = now()
		WHERE desired = 'present' AND `+where, args...)
	return err
}

// Repair marks wf's live subscriptions to be applied again, in every
// environment: republishing a version puts back a webhook someone paused,
// broke or deleted at the provider.
func Repair(ctx context.Context, tx pgx.Tx, wf uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET applied_hash = NULL, next_attempt_at = now(), attempts = 0, updated_at = now()
		WHERE workflow_id = $1 AND desired = 'present'`, wf)
	return err
}

// Retry makes a connector's failed subscriptions in env due now (a
// connection was just added, so the refusal may be gone).
func Retry(ctx context.Context, tx pgx.Tx, env, connector string) error {
	_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET next_attempt_at = now(), attempts = 0
		WHERE environment = $1 AND connector = $2 AND state = 'failed'`, env, connector)
	return err
}

// Status is a subscription as people see it (no secret, no provider token).
type Status struct {
	ID           uuid.UUID  `json:"id"`
	WorkflowID   uuid.UUID  `json:"workflow_id"`
	Environment  string     `json:"environment"`
	Connector    string     `json:"connector"`
	Trigger      string     `json:"trigger"`
	Connection   *string    `json:"connection"`
	Desired      string     `json:"desired"`
	State        string     `json:"state"`
	RemoteID     *string    `json:"remote_id"`
	RemoteStatus *string    `json:"remote_status"`
	StatusReason *string    `json:"status_reason"`
	LastError    *string    `json:"last_error"`
	Attempts     int        `json:"attempts"`
	NextAttempt  *time.Time `json:"next_attempt_at"`
	CheckedAt    *time.Time `json:"checked_at"`
	LastTestAt   *time.Time `json:"last_test_at"`
	// Health sums it up: ok, pending, removing, failed, or the provider's
	// failing, paused, broken or missing.
	Health string `json:"health"`
}

func (s *Status) summarise() {
	switch {
	case s.Desired == "absent":
		s.Health = "removing"
	case s.State == "failed":
		s.Health = "failed"
	case s.RemoteStatus != nil && *s.RemoteStatus != connector.RemoteHealthy:
		s.Health = *s.RemoteStatus
	case s.State == "pending":
		s.Health = "pending"
	default:
		s.Health = "ok"
	}
}

const statusCols = `id, workflow_id, environment, connector, trigger_name, connection, desired, state, remote_id, remote_status,
	status_reason, last_error, attempts, next_attempt_at, checked_at, last_test_at`

// List returns the tenant's subscriptions matching where (on
// remote_subscriptions), in a transaction scoped to the tenant.
func List(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]Status, error) {
	q := `SELECT ` + statusCols + ` FROM remote_subscriptions`
	if where != "" {
		q += ` WHERE ` + where
	}
	rows, err := tx.Query(ctx, q+` ORDER BY environment, created_at`, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Status, error) {
		var s Status
		err := r.Scan(&s.ID, &s.WorkflowID, &s.Environment, &s.Connector, &s.Trigger, &s.Connection, &s.Desired, &s.State, &s.RemoteID,
			&s.RemoteStatus, &s.StatusReason, &s.LastError, &s.Attempts, &s.NextAttempt, &s.CheckedAt, &s.LastTestAt)
		s.summarise()
		return s, err
	})
	if out == nil {
		out = []Status{}
	}
	return out, err
}

// Subscription is what ingest needs to verify and route a delivery.
type Subscription struct {
	ID          uuid.UUID
	WorkflowID  uuid.UUID
	Environment string
	Connector   string
	Trigger     string
	Connection  string
	Present     bool
}

// Lookup reads a subscription for ingest (nil when there is none).
func Lookup(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Subscription, error) {
	var s Subscription
	var conn *string
	var desired string
	err := tx.QueryRow(ctx, `SELECT id, workflow_id, environment, connector, trigger_name, connection, desired FROM remote_subscriptions WHERE id = $1`, id).
		Scan(&s.ID, &s.WorkflowID, &s.Environment, &s.Connector, &s.Trigger, &conn, &desired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	s.Connection, s.Present = deref(conn), desired == "present"
	return &s, err
}

// TestReceived records that the provider's test event reached the subscription.
func TestReceived(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET last_test_at = now() WHERE id = $1`, id)
	return err
}

// --- the reconciler ---

// Reconciler makes providers hold what remote_subscriptions says (role
// "scheduler", and right after a publish in the API).
type Reconciler struct {
	Pool     *pgxpool.Pool
	Vault    *secrets.Vault
	Registry *connector.Registry
	Egress   *egress.Guard
	// HooksURL is where providers reach Taskiem's /hooks (TASKIEM_HOOKS_URL,
	// default TASKIEM_PUBLIC_URL + "/hooks"). Without it nothing is created.
	HooksURL string
	Logger   *slog.Logger
	// Interval between passes (default 5s); Check between health checks
	// of a subscription (default 10m).
	Interval, Check time.Duration
	// Timeout of one provider call (default 20s).
	Timeout time.Duration
}

func (r *Reconciler) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.Default()
	}
	return r.Logger
}

// Run reconciles until ctx ends.
func (r *Reconciler) Run(ctx context.Context) error {
	if r.Interval == 0 {
		r.Interval = 5 * time.Second
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		if _, err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.logger().Error("remote subscriptions: pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

const (
	tenantsPerTick = 50
	rowsPerTenant  = 20
	lease          = 2 * time.Minute
)

func (r *Reconciler) check() time.Duration {
	if r.Check == 0 {
		return 10 * time.Minute
	}
	return r.Check
}

// Tick reconciles every subscription due now and returns how many it handled.
func (r *Reconciler) Tick(ctx context.Context) (int, error) {
	rows, err := r.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_remote_tenants($1, $2)`, time.Now().Add(-r.check()), tenantsPerTick)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tenants {
		m, err := r.reconcileTenant(ctx, t, uuid.Nil)
		n += m
		if err != nil {
			if ctx.Err() != nil {
				return n, err
			}
			r.logger().Error("remote subscriptions: tenant pass failed", "tenant", t, "err", err)
		}
	}
	return n, nil
}

// ReconcileWorkflow applies wf's pending subscriptions now (after a
// publish or undeploy), so the answer can show their state. What it does
// not finish, the next Tick does.
func (r *Reconciler) ReconcileWorkflow(ctx context.Context, tenant, wf uuid.UUID) error {
	_, err := r.reconcileTenant(ctx, tenant, wf)
	return err
}

type row struct {
	id, wf       uuid.UUID
	env, ref     string
	trigger      string
	connection   string
	events       []string
	options      map[string]any
	desired      string
	remoteID     string
	appliedHash  string
	attempts     int
	healthCheck  bool // nothing to apply: check the provider's view
	remoteStatus string
	updatedAt    time.Time // the intent this pass works on
}

// claim leases a tenant's due rows (of one workflow, when wf is set).
func (r *Reconciler) claim(ctx context.Context, tenant, wf uuid.UUID) ([]row, error) {
	var out []row
	err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE remote_subscriptions SET lease_until = now() + $3::interval WHERE id IN (
			SELECT id FROM remote_subscriptions
			 WHERE ((next_attempt_at IS NOT NULL AND next_attempt_at <= now())
			    OR (desired = 'present' AND remote_id IS NOT NULL AND (checked_at IS NULL OR checked_at < $4)))
			   AND (lease_until IS NULL OR lease_until < now())
			   AND ($1::uuid IS NULL OR workflow_id = $1)
			 ORDER BY next_attempt_at NULLS LAST LIMIT $2 FOR UPDATE SKIP LOCKED)
			RETURNING id, workflow_id, environment, connector, trigger_name, COALESCE(connection, ''), events, options, desired,
			  COALESCE(remote_id, ''), COALESCE(applied_hash, ''), attempts, next_attempt_at IS NULL OR next_attempt_at > now(), COALESCE(remote_status, ''), updated_at`,
			nullUUID(wf), rowsPerTenant, lease.String(), time.Now().Add(-r.check()))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(cr pgx.CollectableRow) (row, error) {
			var x row
			var opts []byte
			err := cr.Scan(&x.id, &x.wf, &x.env, &x.ref, &x.trigger, &x.connection, &x.events, &opts, &x.desired,
				&x.remoteID, &x.appliedHash, &x.attempts, &x.healthCheck, &x.remoteStatus, &x.updatedAt)
			if err == nil {
				err = json.Unmarshal(opts, &x.options)
			}
			return x, err
		})
		return err
	})
	return out, err
}

func (r *Reconciler) reconcileTenant(ctx context.Context, tenant, wf uuid.UUID) (int, error) {
	rows, err := r.claim(ctx, tenant, wf)
	if err != nil {
		return 0, err
	}
	for _, x := range rows {
		if err := r.reconcile(ctx, tenant, x); err != nil {
			r.logger().Warn("remote subscription not reconciled", "tenant", tenant, "subscription", x.id, "err", err)
		}
	}
	return len(rows), nil
}

// outcome is what one reconcile learned, written back in one transaction.
type outcome struct {
	remoteID     *string // nil: unchanged
	appliedHash  string
	remoteStatus string // "" leaves it unknown
	reason       string
	secret       string // a new signing secret to keep
	deleted      bool   // the provider's subscription is gone and so is the row
	audit        string // the audit action, if the provider changed
}

// url is the ingest URL of a subscription.
func (r *Reconciler) url(tenant uuid.UUID, x row) (string, error) {
	if r.HooksURL == "" {
		return "", fmt.Errorf("this deployment has no public hooks URL (TASKIEM_HOOKS_URL or TASKIEM_PUBLIC_URL): %w", effects.ErrFatal)
	}
	q := url.Values{"env": {x.env}, "subscription": {x.id.String()}}
	if x.connection != "" {
		q.Set("connection", x.connection)
	}
	return strings.TrimRight(r.HooksURL, "/") + "/" + tenant.String() + "/connectors/" + url.PathEscape(x.ref) + "/" + url.PathEscape(x.trigger) + "?" + q.Encode(), nil
}

func hash(spec connector.RemoteSpec) string {
	ev := slices.Clone(spec.Events)
	slices.Sort(ev)
	raw, _ := json.Marshal([]any{spec.Name, spec.URL, ev, spec.Options})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (r *Reconciler) reconcile(ctx context.Context, tenant uuid.UUID, x row) error {
	out, err := r.apply(ctx, tenant, x)
	return r.record(ctx, tenant, x, out, err)
}

func (r *Reconciler) apply(ctx context.Context, tenant uuid.UUID, x row) (outcome, error) {
	var out outcome
	reg, err := r.Registry.For(ctx, tenant.String())
	if err != nil {
		return out, err
	}
	conn, ok := reg.Get(x.ref)
	var registrar connector.Registrar
	if ok {
		registrar = conn.Registrars[x.trigger]
	}
	if registrar == nil {
		return out, fmt.Errorf("connector %s has no remotely registered trigger %q: %w", x.ref, x.trigger, effects.ErrFatal)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeRemoteRegister})
	creds, err := r.Vault.Credentials(use, tenant, x.env, conn.Manifest.ID, x.connection)
	if errors.Is(err, secrets.ErrNotFound) || errors.Is(err, secrets.ErrAmbiguous) {
		return out, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return out, err
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	call := connector.RemoteCall{Credentials: creds,
		HTTP: r.Egress.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID + ":registration"}, timeout)}
	name := Name(x.id)

	if x.desired == "absent" {
		id := x.remoteID
		if id == "" {
			// Never recorded (a crash right after creating it, or never
			// created): find it by name so nothing is left behind.
			st, found, err := registrar.Find(ctx, call, name)
			if err != nil || !found {
				out.deleted = err == nil
				return out, err
			}
			id = st.ID
		}
		if err := registrar.Delete(ctx, call, id); err != nil {
			return out, err
		}
		out.deleted, out.audit = true, "remote_subscription.delete"
		return out, nil
	}

	ingest, err := r.url(tenant, x)
	if err != nil {
		return out, err
	}
	spec := connector.RemoteSpec{Name: name, URL: ingest, Events: x.events, Options: x.options}
	h := hash(spec)
	if x.healthCheck && x.remoteID != "" && x.appliedHash == h {
		st, err := registrar.Get(ctx, call, x.remoteID)
		if errors.Is(err, connector.ErrNotFound) {
			out.remoteStatus, out.reason, out.appliedHash = connector.RemoteMissing, "the provider no longer has this subscription; republish to create it again", h
			return out, nil
		}
		out.remoteStatus, out.reason, out.appliedHash = st.Status, st.Reason, h
		return out, err
	}
	var st connector.RemoteState
	if x.remoteID != "" {
		st, err = registrar.Update(ctx, call, x.remoteID, spec)
		if errors.Is(err, connector.ErrNotFound) {
			x.remoteID = "" // gone at the provider: create it again
		} else if err != nil {
			return out, err
		} else {
			out.audit = "remote_subscription.update"
		}
	}
	if x.remoteID == "" {
		found, ok, err := registrar.Find(ctx, call, name)
		if err != nil {
			return out, err
		}
		if ok {
			// Created before a crash: adopt it. Its secret was never kept,
			// so replace it, and make it match.
			if st.Secret, err = registrar.RotateSecret(ctx, call, found.ID); err != nil {
				return out, err
			}
			secret := st.Secret
			if st, err = registrar.Update(ctx, call, found.ID, spec); err != nil {
				return out, err
			}
			st.Secret = secret
			out.audit = "remote_subscription.adopt"
		} else {
			if st, err = registrar.Create(ctx, call, spec); err != nil {
				return out, err
			}
			out.audit = "remote_subscription.create"
		}
		if st.ID == "" || st.Secret == "" {
			return out, fmt.Errorf("the provider returned no id or secret: %w", effects.ErrUnknownOutcome)
		}
		out.secret = st.Secret
	}
	out.remoteID, out.appliedHash, out.remoteStatus, out.reason = &st.ID, h, st.Status, st.Reason
	return out, nil
}

// backoff is the wait before attempt n+1 of a reconcile that failed.
func backoff(attempts int, fatal bool) time.Duration {
	if fatal {
		// A refusal people must fix (a token, a permission): try now and then.
		return min(time.Hour*time.Duration(1<<min(attempts, 4)), 24*time.Hour)
	}
	return min(10*time.Second*time.Duration(1<<min(attempts, 10)), time.Hour)
}

// record writes what a reconcile learned. The secret is stored before the
// row records the subscription's id: a crash in between leaves a row
// without an id, and the next pass adopts the subscription by its name.
func (r *Reconciler) record(ctx context.Context, tenant uuid.UUID, x row, out outcome, callErr error) error {
	if out.secret != "" {
		if _, err := r.Vault.Put(ctx, tenant, SecretEnv, SecretName(x.id), []byte(out.secret), "system"); err != nil {
			return err
		}
	}
	return db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if callErr != nil {
			fatal := effects.Classify(callErr) == effects.KindFatal
			state := "pending"
			if fatal {
				state = "failed"
			}
			msg := callErr.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			// An intent changed while this pass ran (a redeploy) is due at once.
			_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET state = CASE WHEN $5 THEN state ELSE $2 END, last_error = $3, attempts = attempts + 1,
				next_attempt_at = CASE WHEN updated_at = $6 THEN now() + $4::interval ELSE next_attempt_at END,
				checked_at = CASE WHEN $5 THEN now() ELSE checked_at END, lease_until = NULL WHERE id = $1`,
				x.id, state, msg, backoff(x.attempts, fatal).String(), x.healthCheck && x.desired == "present" && x.appliedHash != "", x.updatedAt)
			if err == nil {
				err = audit(ctx, tx, tenant, "remote_subscription.error", x, map[string]any{"error": msg, "fatal": fatal})
			}
			return err
		}
		if out.deleted {
			if _, err := tx.Exec(ctx, `DELETE FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3`, tenant, SecretEnv, SecretName(x.id)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM remote_subscriptions WHERE id = $1 AND desired = 'absent'`, x.id); err != nil {
				return err
			}
			if out.audit == "" {
				return nil
			}
			return audit(ctx, tx, tenant, out.audit, x, map[string]any{"remote_id": x.remoteID})
		}
		var status, reason *string
		if out.remoteStatus != "" {
			status = &out.remoteStatus
		}
		if out.reason != "" {
			reason = &out.reason
		}
		// A row redeclared while this pass ran keeps its new intent, due at once.
		_, err := tx.Exec(ctx, `UPDATE remote_subscriptions SET remote_id = COALESCE($2, remote_id), state = 'active', remote_status = $3, status_reason = $4,
			last_error = NULL, attempts = 0, checked_at = now(), lease_until = NULL,
			applied_hash = CASE WHEN updated_at = $6 THEN $5 ELSE applied_hash END,
			next_attempt_at = CASE WHEN updated_at = $6 THEN NULL ELSE next_attempt_at END
			WHERE id = $1`, x.id, out.remoteID, status, reason, out.appliedHash, x.updatedAt)
		if err != nil || out.audit == "" {
			return err
		}
		detail := map[string]any{}
		if out.remoteID != nil {
			detail["remote_id"] = *out.remoteID
		}
		return audit(ctx, tx, tenant, out.audit, x, detail)
	})
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action string, x row, detail map[string]any) error {
	detail["subscription"] = x.id
	detail["workflow_id"] = x.wf
	detail["environment"] = x.env
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'system', $2, $3, $4)`, tenant, action, x.ref+"/"+x.trigger, raw)
	return err
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
