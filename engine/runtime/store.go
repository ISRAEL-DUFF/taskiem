// Package runtime runs workflows: it persists events, runs decide() inline
// in the transaction that appends each event (decision 0002 amendment), and
// hosts the worker and scheduler loops.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Store is the engine's view of Postgres. Every method runs in a tenant-scoped
// transaction (spec 5.3).
type Store struct {
	Pool     *pgxpool.Pool
	Registry *connector.Registry
	// PII seals declared personal data before it is written (spec 4.9);
	// nil stores it in plaintext (development without a vault).
	PII pii.Cipher

	defs sync.Map // "workflow_id/version" -> *wd.Definition; versions are immutable
}

// maxInlineRounds bounds decide -> effects -> decide loops in one transaction
// (a buffered signal consumed by a new wait triggers another round).
const maxInlineRounds = 16

// RunRef identifies a run.
type RunRef struct {
	ID, TenantID uuid.UUID
}

// StartRequest starts a run of a published workflow version.
type StartRequest struct {
	TenantID    uuid.UUID
	WorkflowID  uuid.UUID
	Version     int
	Environment string
	Trigger     any
	Env         map[string]any // nil loads the environment's tenant variables
	StartedBy   string         // user, API key, or trigger that started the run (separation of duties)
	TriggerID   string         // with DedupKey, makes starting idempotent (spec 8.2)
	DedupKey    string
}

// ErrNotFound is returned when a run, step, or workflow is not visible.
var ErrNotFound = errors.New("not found")

// StartRun records RunStarted and the first decision in one transaction.
// A repeated (TriggerID, DedupKey) returns the original run.
func (s *Store) StartRun(ctx context.Context, req StartRequest) (RunRef, bool, error) {
	ref := RunRef{ID: uuid.Must(uuid.NewV7()), TenantID: req.TenantID}
	created := true
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{req.TenantID}, func(tx pgx.Tx) error {
		def, err := s.definition(ctx, tx, req.WorkflowID, req.Version)
		if err != nil {
			return err
		}
		if req.DedupKey != "" {
			tag, err := tx.Exec(ctx, `INSERT INTO trigger_receipts (tenant_id, trigger_id, dedup_key, run_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				req.TenantID, req.TriggerID, req.DedupKey, ref.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				created = false
				return tx.QueryRow(ctx, `SELECT run_id FROM trigger_receipts WHERE tenant_id = $1 AND trigger_id = $2 AND dedup_key = $3`,
					req.TenantID, req.TriggerID, req.DedupKey).Scan(&ref.ID)
			}
		}
		var startedAt time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, started_at, started_by) VALUES ($1, $2, $3, $4, $5, now(), NULLIF($6, '')) RETURNING started_at`,
			ref.ID, req.TenantID, req.WorkflowID, req.Version, req.Environment, req.StartedBy).Scan(&startedAt); err != nil {
			return err
		}
		if req.Env == nil {
			if req.Env, err = variables(ctx, tx, req.TenantID, req.Environment); err != nil {
				return err
			}
		}
		pins := map[string]string{}
		if s.Registry != nil {
			pins = s.Registry.Pins()
		}
		policies, err := policySnapshots(ctx, tx, def)
		if err != nil {
			return err
		}
		payload := history.RunStartedPayload{
			Run: history.RunInfo{ID: ref.ID.String(), TenantID: req.TenantID.String(), WorkflowID: req.WorkflowID.String(),
				Version: req.Version, Environment: req.Environment, StartedAt: history.FormatTime(startedAt)},
			Trigger: req.Trigger, Env: req.Env, Connectors: pins, Policies: policies,
		}
		sealed, err := s.sealPayload(ctx, tx, req.TenantID, payload, triggerPaths(def), pii.Taint{})
		if err != nil {
			return err
		}
		if _, err := appendEvent(ctx, tx, ref.ID, history.RunStarted, "", 0, sealed, history.OriginIngest); err != nil {
			return err
		}
		admitted, err := s.admit(ctx, tx, ref, req.WorkflowID, def, payload)
		if err != nil || !admitted {
			return err
		}
		return s.start(ctx, tx, ref, def, startedAt)
	})
	return ref, created, err
}

// Definition returns a stored workflow version's parsed definition.
func (s *Store) Definition(ctx context.Context, tenant, workflowID uuid.UUID, version int) (*wd.Definition, error) {
	var d *wd.Definition
	err := dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		var err error
		d, err = s.definition(ctx, tx, workflowID, version)
		return err
	})
	return d, err
}

func (s *Store) definition(ctx context.Context, tx pgx.Tx, workflowID uuid.UUID, version int) (*wd.Definition, error) {
	key := workflowID.String() + "/" + strconv.Itoa(version)
	if d, ok := s.defs.Load(key); ok {
		return d.(*wd.Definition), nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, workflowID, version).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("workflow %s version %d: %w", workflowID, version, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	d, err := wd.Load(raw)
	if err != nil {
		return nil, err
	}
	s.defs.Store(key, d)
	return d, nil
}

// start arms the run timeout and makes the first decision.
func (s *Store) start(ctx context.Context, tx pgx.Tx, ref RunRef, def *wd.Definition, from time.Time) error {
	if def.Settings.Timeout != "" {
		d, err := wd.ParseDuration(def.Settings.Timeout)
		if err != nil {
			return err
		}
		if err := insertTimer(ctx, tx, ref.TenantID, ref.ID, "", "run_timeout", from.Add(d)); err != nil {
			return err
		}
	}
	return s.decideInline(ctx, tx, ref)
}

// runRow is the part of a run the engine needs.
type runRow struct {
	ref        RunRef
	workflowID uuid.UUID
	version    int
	status     string
	env        string
}

func lockRun(ctx context.Context, tx pgx.Tx, id uuid.UUID) (runRow, error) {
	r := runRow{ref: RunRef{ID: id}}
	err := tx.QueryRow(ctx, `SELECT tenant_id, workflow_id, version, status, environment FROM runs WHERE id = $1 FOR UPDATE`, id).
		Scan(&r.ref.TenantID, &r.workflowID, &r.version, &r.status, &r.env)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("run %s: %w", id, ErrNotFound)
	}
	return r, err
}

// History loads a run's events in order.
func History(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]history.Event, error) {
	rows, err := tx.Query(ctx, `SELECT seq, type, COALESCE(step_id, ''), COALESCE(attempt, 0), payload, recorded_at, origin
		FROM run_events WHERE run_id = $1 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []history.Event
	for rows.Next() {
		var e history.Event
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.Type, &e.StepID, &e.Attempt, &payload, &e.RecordedAt, &e.Origin); err != nil {
			return nil, err
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, rows.Err()
}

func appendEvent(ctx context.Context, tx pgx.Tx, run uuid.UUID, typ, step string, attempt int, payload any, origin string) (int64, error) {
	var raw []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		raw = b
	}
	var stepArg, attemptArg any
	if step != "" {
		stepArg = step
	}
	if attempt != 0 {
		attemptArg = attempt
	}
	var seq int64
	err := tx.QueryRow(ctx, `SELECT taskiem_append_event($1, $2, $3, $4, $5, $6)`, run, typ, stepArg, attemptArg, raw, origin).Scan(&seq)
	return seq, err
}

// decideInline runs decide() over the run's history and applies the result,
// repeating while applying effects appends further events.
func (s *Store) decideInline(ctx context.Context, tx pgx.Tx, ref RunRef) error {
	run, err := lockRun(ctx, tx, ref.ID)
	if err != nil {
		return err
	}
	def, err := s.definition(ctx, tx, run.workflowID, run.version)
	if err != nil {
		return err
	}
	if run.status == "queued" {
		// Not admitted yet: nothing is decided until it gets its slot.
		_, err := tx.Exec(ctx, `UPDATE runs SET decided_seq = last_seq, orch_lease_owner = NULL, orch_lease_until = NULL WHERE id = $1`, ref.ID)
		return err
	}
	for round := 0; round < maxInlineRounds; round++ {
		raw, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		hist, taint, err := s.openHistory(ctx, tx, ref.TenantID, raw)
		if err != nil {
			return err
		}
		evs, err := decide.Decide(def, hist)
		if err != nil {
			return err
		}
		// Events that effects cause (a buffered signal delivered to a new
		// wait) are appended after decide's batch, so the batch stays
		// contiguous and replayable.
		var deferred []func() error
		for _, ev := range evs {
			var paths []pii.Path
			if p, ok := ev.Payload.(history.ScheduledPayload); ok {
				paths = s.connectorPIIPaths(p)
			}
			sealed, err := s.sealPayload(ctx, tx, ref.TenantID, ev.Payload, paths, taint)
			if err != nil {
				return err
			}
			if _, err := appendEvent(ctx, tx, ref.ID, ev.Type, ev.StepID, ev.Attempt, sealed, history.OriginDecide); err != nil {
				return err
			}
			then, err := s.applyEffects(ctx, tx, run, def, ev, sealed)
			if err != nil {
				return fmt.Errorf("apply %s(%s): %w", ev.Type, ev.StepID, err)
			}
			if then != nil {
				deferred = append(deferred, then)
			}
		}
		for _, f := range deferred {
			if err := f(); err != nil {
				return err
			}
		}
		if len(deferred) == 0 {
			_, err := tx.Exec(ctx, `UPDATE runs SET decided_seq = last_seq, orch_lease_owner = NULL, orch_lease_until = NULL WHERE id = $1`, ref.ID)
			return err
		}
	}
	return fmt.Errorf("run %s: decision did not settle after %d rounds", ref.ID, maxInlineRounds)
}

// applyEffects performs the side effects an event implies: tasks, timers,
// signal waits, run status. It may return a follow-up that appends further
// events once decide's batch is written.
func (s *Store) applyEffects(ctx context.Context, tx pgx.Tx, run runRow, def *wd.Definition, ev decide.NewEvent, sealed any) (func() error, error) {
	tenant := run.ref.TenantID
	switch ev.Type {
	case history.StepScheduled:
		p := ev.Payload.(history.ScheduledPayload)
		switch p.Kind {
		case history.KindTask:
			at, err := history.ParseTime(p.AvailableAt)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO tasks (id, tenant_id, run_id, step_id, attempt, queue, available_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				uuid.Must(uuid.NewV7()), tenant, run.ref.ID, ev.StepID, ev.Attempt, p.Queue, at)
			return nil, err
		case history.KindTimer:
			at, err := history.ParseTime(p.FireAt)
			if err != nil {
				return nil, err
			}
			return nil, insertTimer(ctx, tx, tenant, run.ref.ID, ev.StepID, "wait", at)
		case history.KindSignal:
			if p.TimeoutAt != "" {
				at, err := history.ParseTime(p.TimeoutAt)
				if err != nil {
					return nil, err
				}
				if err := insertTimer(ctx, tx, tenant, run.ref.ID, ev.StepID, "signal_timeout", at); err != nil {
					return nil, err
				}
			}
			return s.registerWait(ctx, tx, run.ref, ev.StepID, p.Event, p.Correlation)
		}
	case history.ApprovalRequested:
		p := ev.Payload.(history.ApprovalRequestedPayload)
		var subject any
		if m, ok := sealed.(map[string]any); ok {
			subject = m["subject"]
		}
		subj, _ := json.Marshal(subject)
		required := p.Count
		if required <= 0 {
			required = 1
		}
		var timeoutAt *time.Time
		if p.TimeoutAt != "" {
			t, err := history.ParseTime(p.TimeoutAt)
			if err != nil {
				return nil, err
			}
			timeoutAt = &t
		}
		var levels, constraints []byte
		if len(p.Levels) > 0 {
			levels, _ = json.Marshal(p.Levels)
		}
		if p.Constraints != nil {
			constraints, _ = json.Marshal(p.Constraints)
		}
		// An escalation re-opens the step at its first level with new approvers.
		if _, err := tx.Exec(ctx, `INSERT INTO approvals (tenant_id, run_id, step_id, role, policy, required, subject, timeout_at, levels, level, step_up, constraints, policy_version)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, $9, 0, NULLIF($10, ''), $11, NULLIF($12, 0))
			ON CONFLICT (run_id, step_id) DO UPDATE SET role = EXCLUDED.role, required = EXCLUDED.required, timeout_at = EXCLUDED.timeout_at,
			  levels = EXCLUDED.levels, level = 0, status = 'open', requested_at = now()`,
			tenant, run.ref.ID, ev.StepID, p.Role, p.Policy, required, subj, timeoutAt, levels, p.StepUp, constraints, p.PolicyVersion); err != nil {
			return nil, err
		}
		if p.TimeoutAt != "" {
			at, err := history.ParseTime(p.TimeoutAt)
			if err != nil {
				return nil, err
			}
			return nil, insertTimer(ctx, tx, tenant, run.ref.ID, ev.StepID, "approval_timeout", at)
		}
	case history.StepCompleted:
		// Closes an approval, if this step was one (decision or timeout).
		if out, ok := ev.Payload.(history.CompletedPayload).Output.(map[string]any); ok {
			if d, _ := out["decision"].(string); d != "" {
				st := d
				if out["reason"] == "timeout" {
					st = "expired"
				}
				if _, err := tx.Exec(ctx, `UPDATE approvals SET status = $3, closed_at = now() WHERE run_id = $1 AND step_id = $2 AND status = 'open'`,
					run.ref.ID, ev.StepID, st); err != nil {
					return nil, err
				}
			}
		}
	case history.StepFailed:
		p := ev.Payload.(history.FailedPayload)
		if p.Error.Next == "park" || (p.Error.Next == "fail" && isCompensation(ev.StepID)) {
			_, err := tx.Exec(ctx, `UPDATE runs SET status = 'needs_reconciliation' WHERE id = $1`, run.ref.ID)
			return nil, err
		}
	case history.StepCancelled:
		// A task a worker already holds is left to it: the worker checks for
		// cancellation before recording intent, and decide waits for a write
		// already under way.
		for _, q := range []string{
			`DELETE FROM tasks WHERE run_id = $1 AND step_id = $2 AND lease_owner IS NULL`,
			`DELETE FROM timers WHERE run_id = $1 AND step_id = $2 AND fired_at IS NULL`,
			`DELETE FROM signal_waits WHERE run_id = $1 AND step_id = $2`,
			`UPDATE approvals SET status = 'cancelled', closed_at = now() WHERE run_id = $1 AND step_id = $2 AND status = 'open'`,
		} {
			if _, err := tx.Exec(ctx, q, run.ref.ID, ev.StepID); err != nil {
				return nil, err
			}
		}
	case history.RunCompleted:
		return nil, s.endRun(ctx, tx, run, def, "completed")
	case history.RunFailed:
		return nil, s.endRun(ctx, tx, run, def, "failed")
	}
	return nil, nil
}

func isCompensation(inst string) bool {
	return len(inst) > len(history.CompensationPrefix) && inst[:len(history.CompensationPrefix)] == history.CompensationPrefix
}

func insertTimer(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, step, kind string, at time.Time) error {
	var stepArg any
	if step != "" {
		stepArg = step
	}
	_, err := tx.Exec(ctx, `INSERT INTO timers (id, tenant_id, run_id, step_id, kind, fire_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.Must(uuid.NewV7()), tenant, run, stepArg, kind, at)
	return err
}

// policySnapshots loads the active versions of the approval policies a
// definition names. A missing one is left out: the approval step fails with
// the reason when it is reached.
func policySnapshots(ctx context.Context, tx pgx.Tx, def *wd.Definition) (map[string]history.PolicySnapshot, error) {
	var names []string
	var walk func([]*wd.Step)
	walk = func(steps []*wd.Step) {
		for _, st := range steps {
			if st.Approval != nil && st.Approval.Policy != "" {
				names = append(names, st.Approval.Policy)
			}
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT name, version, document FROM approval_policies WHERE state = 'active' AND name = ANY ($1)`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]history.PolicySnapshot{}
	for rows.Next() {
		var name string
		var ps history.PolicySnapshot
		if err := rows.Scan(&name, &ps.Version, &ps.Document); err != nil {
			return nil, err
		}
		out[name] = ps
	}
	return out, rows.Err()
}

// DefaultRetention applies when a workflow sets no settings.retention.
const DefaultRetention = 90 * 24 * time.Hour

// endRun marks a run terminal, starts its retention clock (spec 9.4), clears
// what it no longer needs, and admits queued runs its slot was holding back.
// Tasks a worker holds are left alone: its result is still recorded.
func (s *Store) endRun(ctx context.Context, tx pgx.Tx, r runRow, def *wd.Definition, status string) error {
	run := r.ref.ID
	retention := DefaultRetention
	if d, err := wd.ParseDuration(def.Settings.Retention); err == nil && d > 0 {
		retention = d
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET status = $2, ended_at = now(), retain_until = now() + $3::interval WHERE id = $1`,
		run, status, fmt.Sprintf("%d seconds", int64(retention.Seconds()))); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM concurrency_slots WHERE run_id = $1`, run); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approvals SET status = 'cancelled', closed_at = now() WHERE run_id = $1 AND status = 'open'`, run); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM tasks WHERE run_id = $1 AND lease_owner IS NULL`, run); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM timers WHERE run_id = $1 AND fired_at IS NULL`, run); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM signal_waits WHERE run_id = $1`, run); err != nil {
		return err
	}
	return s.promote(ctx, tx, r.ref.TenantID, r.workflowID)
}

// signalLock serialises waiting and delivery for one (tenant, event,
// correlation), so a signal can never slip between the two.
func signalLock(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, event, correlation string) error {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tenant.String() + "\x00" + event + "\x00" + correlation))
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(h.Sum64())) //nolint:gosec // bit-for-bit reinterpretation as a lock key
	return err
}

// registerWait records that a step waits for a signal, or delivers a
// buffered one immediately.
func (s *Store) registerWait(ctx context.Context, tx pgx.Tx, ref RunRef, step, event, correlation string) (func() error, error) {
	if err := signalLock(ctx, tx, ref.TenantID, event, correlation); err != nil {
		return nil, err
	}
	var id uuid.UUID
	var payload []byte
	err := tx.QueryRow(ctx, `DELETE FROM signals WHERE id = (
		SELECT id FROM signals WHERE tenant_id = $1 AND event = $2 AND correlation = $3 AND expires_at > now()
		 ORDER BY received_at LIMIT 1) RETURNING id, payload`, ref.TenantID, event, correlation).Scan(&id, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err := tx.Exec(ctx, `INSERT INTO signal_waits (tenant_id, run_id, step_id, event, correlation) VALUES ($1, $2, $3, $4, $5)`,
			ref.TenantID, ref.ID, step, event, correlation)
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return func() error {
		_, err := appendEvent(ctx, tx, ref.ID, history.SignalReceived, step, 0, signalPayload(event, payload), history.OriginSignal)
		return err
	}, nil
}

func signalPayload(event string, payload []byte) map[string]any {
	var v any
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &v)
	}
	return map[string]any{"event": event, "payload": v}
}

// SignalTTL is how long an unmatched signal stays buffered.
const SignalTTL = 7 * 24 * time.Hour

// DeliverSignal hands an external event to every run waiting for it, or
// buffers it. It returns the runs it woke.
func (s *Store) DeliverSignal(ctx context.Context, tenant uuid.UUID, event, correlation string, payload any) ([]uuid.UUID, error) {
	woke, _, err := s.deliverSignal(ctx, tenant, event, correlation, "", payload)
	return woke, err
}

// DeliverSignalOnce is DeliverSignal for provider deliveries that may
// repeat: a dedupKey already received for this event is ignored, in the
// same transaction that delivers it. It reports whether it was new.
func (s *Store) DeliverSignalOnce(ctx context.Context, tenant uuid.UUID, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error) {
	return s.deliverSignal(ctx, tenant, event, correlation, dedupKey, payload)
}

func (s *Store) deliverSignal(ctx context.Context, tenant uuid.UUID, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error) {
	fresh := true
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	var woke []uuid.UUID
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		woke, fresh = woke[:0], true
		if dedupKey != "" {
			// The receipt marks the delivery as seen; no run is started (uuid.Nil).
			tag, err := tx.Exec(ctx, `INSERT INTO trigger_receipts (tenant_id, trigger_id, dedup_key, run_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				tenant, "signal/"+event, dedupKey, uuid.Nil)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				fresh = false
				return nil
			}
		}
		if err := signalLock(ctx, tx, tenant, event, correlation); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `DELETE FROM signal_waits WHERE tenant_id = $1 AND event = $2 AND correlation = $3 RETURNING run_id, step_id`,
			tenant, event, correlation)
		if err != nil {
			return err
		}
		type wait struct {
			run  uuid.UUID
			step string
		}
		var waits []wait
		for rows.Next() {
			var w wait
			if err := rows.Scan(&w.run, &w.step); err != nil {
				return err
			}
			waits = append(waits, w)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(waits) == 0 {
			_, err := tx.Exec(ctx, `INSERT INTO signals (id, tenant_id, event, correlation, payload, expires_at) VALUES ($1, $2, $3, $4, $5, now() + $6::interval)`,
				uuid.Must(uuid.NewV7()), tenant, event, correlation, raw, SignalTTL.String())
			return err
		}
		for _, w := range waits {
			if _, err := lockRun(ctx, tx, w.run); err != nil {
				return err
			}
			if _, err := appendEvent(ctx, tx, w.run, history.SignalReceived, w.step, 0, signalPayload(event, raw), history.OriginSignal); err != nil {
				return err
			}
			if err := s.decideInline(ctx, tx, RunRef{ID: w.run, TenantID: tenant}); err != nil {
				return err
			}
			woke = append(woke, w.run)
		}
		return nil
	})
	return woke, fresh, err
}

// DecideApproval records a decision on an open approval step. Policy checks
// (role, separation of duties, step-up) belong to the API layer.
func (s *Store) DecideApproval(ctx context.Context, ref RunRef, step, decision, decidedBy, channel string) error {
	if decision != "approved" && decision != "rejected" {
		return fmt.Errorf("decision must be approved or rejected, got %q", decision)
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		if _, err := lockRun(ctx, tx, ref.ID); err != nil {
			return err
		}
		hist, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		open := false
		for _, e := range hist {
			if e.StepID != step {
				continue
			}
			switch e.Type {
			case history.ApprovalRequested:
				open = true
			case history.ApprovalDecided, history.StepCompleted, history.StepFailed:
				open = false
			}
		}
		if !open {
			return fmt.Errorf("no open approval %q on run %s: %w", step, ref.ID, ErrNotFound)
		}
		if _, err := appendEvent(ctx, tx, ref.ID, history.ApprovalDecided, step, 0,
			history.ApprovalDecidedPayload{Decision: decision, DecidedBy: decidedBy, Channel: channel}, history.OriginAPI); err != nil {
			return err
		}
		return s.decideInline(ctx, tx, ref)
	})
}

// CancelRun ends a run now. Steps a worker is executing still record their
// results; nothing new starts (spec 4.10).
func (s *Store) CancelRun(ctx context.Context, ref RunRef, by string) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		run, err := lockRun(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		switch run.status {
		case "completed", "failed", "cancelled":
			return nil
		}
		if _, err := appendEvent(ctx, tx, ref.ID, history.RunCancelled, "", 0, map[string]any{"by": by}, history.OriginAPI); err != nil {
			return err
		}
		def, err := s.definition(ctx, tx, run.workflowID, run.version)
		if err != nil {
			return err
		}
		return s.endRun(ctx, tx, run, def, "cancelled")
	})
}

// RunStatus reports a run's status.
func (s *Store) RunStatus(ctx context.Context, ref RunRef) (string, error) {
	var st string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, ref.ID).Scan(&st)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return st, err
}

// RunHistory returns a run's events.
func (s *Store) RunHistory(ctx context.Context, ref RunRef) ([]history.Event, error) {
	var h []history.Event
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		var err error
		h, err = History(ctx, tx, ref.ID)
		return err
	})
	return h, err
}

func dbTx(ctx context.Context, s *Store, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}
