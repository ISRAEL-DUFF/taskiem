package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Subflow steps (decision 0049, wd-v1 subflow steps): a workflow runs
// another workflow of the business as a child run. The child is started
// in the transaction that records the step, so the two cannot disagree;
// it carries the parent's idempotency seed and the step, so a parent run
// resumed from a failure never repeats the child's writes; and when it
// ends it delivers history.SubflowEvent to the step, which the step's
// output is: {run_id, status, outputs, error}. A step that does not wait
// gets the signal at once, with status "started". A parent that ends
// cancels the children it was waiting for.

// startSubflow starts a subflow step's child run. What keeps the run
// from starting (no such workflow, no published version, a plan limit,
// too deep a chain) fails the step rather than the parent's decision.
func (s *Store) startSubflow(ctx context.Context, tx pgx.Tx, run runRow, ev decide.NewEvent, p history.ScheduledPayload) (func() error, error) {
	failStep := func(format string, args ...any) (func() error, error) {
		msg := fmt.Sprintf(format, args...)
		return func() error {
			_, err := appendEvent(ctx, tx, run.ref.ID, history.StepFailed, ev.StepID, ev.Attempt,
				history.FailedPayload{Error: history.Error{Kind: "subflow", Message: msg, Next: "fail"}}, history.OriginScheduler)
			return err
		}, nil
	}
	if p.Subflow == nil {
		return failStep("the step names no workflow")
	}
	wf, err := uuid.Parse(p.Subflow.Workflow)
	if err != nil {
		return failStep("%q is not a workflow id", p.Subflow.Workflow)
	}
	var name string
	var active *int
	switch err := tx.QueryRow(ctx, `SELECT name, active_version FROM workflows WHERE id = $1`, wf).Scan(&name, &active); {
	case errors.Is(err, pgx.ErrNoRows):
		return failStep("there is no workflow %s to run", wf)
	case err != nil:
		return nil, err
	}
	version := p.Subflow.Version
	if version == 0 {
		if active == nil {
			return failStep("the workflow %s has no published version to run", name)
		}
		version = *active
	} else {
		var state string
		switch err := tx.QueryRow(ctx, `SELECT state FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&state); {
		case errors.Is(err, pgx.ErrNoRows):
			return failStep("the workflow %s has no version %d", name, version)
		case err != nil:
			return nil, err
		case state != "published" && state != "deprecated":
			return failStep("version %d of the workflow %s is %s, not published", version, name, state)
		}
	}
	depth, err := subflowDepth(ctx, tx, run.ref.ID)
	if err != nil {
		return nil, err
	}
	if depth >= wd.MaxSubflowDepth {
		return failStep("workflows run by workflows may nest %d deep, and this one would be deeper", wd.MaxSubflowDepth)
	}
	var seed string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(idempotency_seed, id::text) FROM runs WHERE id = $1`, run.ref.ID).Scan(&seed); err != nil {
		return err2(err)
	}
	correlation := history.SubflowCorrelation(run.ref.ID.String(), ev.StepID)
	var then func() error
	if p.Subflow.Wait {
		// The wait comes first: a child that ends inside this decision
		// reports to it at once.
		if then, err = s.registerWait(ctx, tx, run.ref, run.env, ev.StepID, history.SubflowEvent, correlation); err != nil {
			return nil, err
		}
	}
	child := RunRef{ID: uuid.Must(uuid.NewV7()), TenantID: run.ref.TenantID}
	trigger := map[string]any{"type": "subflow", "body": p.Input,
		"parent": map[string]any{"run_id": run.ref.ID.String(), "step": ev.StepID, "workflow_id": run.workflowID.String()}}
	_, err = s.startIn(ctx, tx, StartRequest{TenantID: run.ref.TenantID, WorkflowID: wf, Version: version, Environment: run.env, Trigger: trigger,
		StartedBy: "subflow:" + run.ref.ID.String() + "/" + ev.StepID,
		Parent:    &ParentRun{Run: run.ref.ID, Step: ev.StepID, Waits: p.Subflow.Wait, Seed: seed + "/" + ev.StepID}}, &child)
	if err != nil {
		if _, limited := IsLimit(err); limited || errors.Is(err, ErrTenantSuspended) || errors.Is(err, ErrNotFound) {
			if p.Subflow.Wait {
				if _, err := tx.Exec(ctx, `DELETE FROM signal_waits WHERE run_id = $1 AND step_id = $2`, run.ref.ID, ev.StepID); err != nil {
					return nil, err
				}
			}
			return failStep("the workflow %s could not start: %v", name, err)
		}
		return nil, err
	}
	if !p.Subflow.Wait {
		raw, _ := json.Marshal(map[string]any{"run_id": child.ID.String(), "status": "started"})
		return func() error {
			_, err := appendEvent(ctx, tx, run.ref.ID, history.SignalReceived, ev.StepID, 0, signalPayload(history.SubflowEvent, raw), history.OriginSignal)
			return err
		}, nil
	}
	if then == nil {
		// Another round reads what the child may already have reported.
		then = func() error { return nil }
	}
	return then, nil
}

func err2(err error) (func() error, error) { return nil, err }

// subflowDepth is how many subflow steps deep a run is: 0 for a run
// nobody's step started.
func subflowDepth(ctx context.Context, tx pgx.Tx, run uuid.UUID) (int, error) {
	depth := 0
	for cur := run; depth <= wd.MaxSubflowDepth; {
		var parent *uuid.UUID
		var step *string
		if err := tx.QueryRow(ctx, `SELECT parent_run_id, parent_step FROM runs WHERE id = $1`, cur).Scan(&parent, &step); err != nil {
			return 0, err
		}
		if parent == nil || step == nil {
			return depth, nil
		}
		depth++
		cur = *parent
	}
	return depth, nil
}

// notifyParent tells the subflow step that started a run how it ended:
// completed with its outputs, failed with its error, or cancelled.
func (s *Store) notifyParent(ctx context.Context, tx pgx.Tx, r runRow, status string) error {
	var parent *uuid.UUID
	var step *string
	if err := tx.QueryRow(ctx, `SELECT parent_run_id, parent_step FROM runs WHERE id = $1`, r.ref.ID).Scan(&parent, &step); err != nil {
		return err
	}
	if parent == nil || step == nil {
		return nil
	}
	report := map[string]any{"run_id": r.ref.ID.String(), "status": status}
	var typ string
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT type, payload FROM run_events WHERE run_id = $1 AND type IN ($2, $3) ORDER BY seq DESC LIMIT 1`,
		r.ref.ID, history.RunCompleted, history.RunFailed).Scan(&typ, &payload)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var last map[string]any
	_ = json.Unmarshal(payload, &last)
	switch {
	case typ == history.RunCompleted && status == "completed":
		outs, _ := last["outputs"].(map[string]any)
		if outs == nil {
			outs = map[string]any{}
		}
		report["outputs"] = outs // sealed values stay sealed; the parent opens them as its own
	case typ == history.RunFailed:
		report["error"] = last["error"]
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = s.deliverSignalTx(ctx, tx, r.ref.TenantID, r.env, history.SubflowEvent, history.SubflowCorrelation(parent.String(), *step), raw, false)
	return err
}

// childrenToReverse returns, when a run cancelled with reversing
// withdraws one of its steps, the child runs that step waits for, which
// are cancelled with reversing too (reverseChildren). Each child tells the
// step when it has ended, as it would have anyway.
func (s *Store) childrenToReverse(ctx context.Context, tx pgx.Tx, r runRow, step string) ([]uuid.UUID, error) {
	if asked, err := cancelRequested(ctx, tx, r.ref.ID); err != nil || !asked {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM runs WHERE parent_run_id = $1 AND parent_step = $2 AND parent_waits
		AND status NOT IN ('completed', 'failed', 'cancelled') ORDER BY started_at`, r.ref.ID, step)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// reverseChildren cancels children with reversing.
func (s *Store) reverseChildren(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, children []uuid.UUID) error {
	for _, id := range children {
		if err := s.cancelTx(ctx, tx, RunRef{ID: id, TenantID: tenant}, "the workflow that started it was cancelled", true, history.OriginScheduler); err != nil {
			return err
		}
	}
	return nil
}

// cancelChildren cancels the child runs a run was waiting for when it
// ends; children it did not wait for finish on their own.
func (s *Store) cancelChildren(ctx context.Context, tx pgx.Tx, r runRow) error {
	rows, err := tx.Query(ctx, `SELECT id FROM runs WHERE parent_run_id = $1 AND parent_step IS NOT NULL AND parent_waits
		AND status NOT IN ('completed', 'failed', 'cancelled') ORDER BY started_at`, r.ref.ID)
	if err != nil {
		return err
	}
	children, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range children {
		child, err := lockRun(ctx, tx, id)
		if err != nil {
			return err
		}
		switch child.status {
		case "completed", "failed", "cancelled":
			continue
		}
		if _, err := appendEvent(ctx, tx, id, history.RunCancelled, "", 0, map[string]any{"by": "the workflow that started it ended"}, history.OriginScheduler); err != nil {
			return err
		}
		def, err := s.runDefinition(ctx, tx, child.workflowID, child.version, child.expansion)
		if err != nil {
			return err
		}
		if err := s.endRun(ctx, tx, child, def, "cancelled"); err != nil {
			return err
		}
	}
	return nil
}

// decidingKey marks, in a context, the runs whose decision is under way
// in the current call: a signal delivered to one of them in the same
// transaction is read by that decision's next round rather than decided
// again inside it.
type decidingKey struct{}

func withDeciding(ctx context.Context, run uuid.UUID) context.Context {
	prev, _ := ctx.Value(decidingKey{}).([]uuid.UUID)
	return context.WithValue(ctx, decidingKey{}, append(prev[:len(prev):len(prev)], run))
}

func deciding(ctx context.Context, run uuid.UUID) bool {
	for _, id := range ctxDeciding(ctx) {
		if id == run {
			return true
		}
	}
	return false
}

func ctxDeciding(ctx context.Context) []uuid.UUID {
	v, _ := ctx.Value(decidingKey{}).([]uuid.UUID)
	return v
}
