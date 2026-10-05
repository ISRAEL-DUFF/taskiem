package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
)

// Resolutions an operator can give a parked step, after checking with the
// provider what actually happened.
const (
	ResolveCompleted = "completed" // the effect happened; record this output
	ResolveFailed    = "failed"    // it did not happen and should not be retried
	ResolveRetry     = "retry"     // it did not happen; send it again
)

// ResolveStep settles a step parked in needs_reconciliation (spec 4.4). It
// is audited; the decision and its evidence are recorded in the run.
func (s *Store) ResolveStep(ctx context.Context, ref RunRef, step, resolution string, output any, note, by string) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		run, err := lockRun(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		if run.status != "needs_reconciliation" {
			return fmt.Errorf("run %s is %s, not waiting for reconciliation: %w", ref.ID, run.status, ErrNotFound)
		}
		hist, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		attempt, parked := 0, false
		for _, e := range hist {
			if e.StepID != step {
				continue
			}
			switch e.Type {
			case history.StepScheduled:
				attempt = e.Attempt
			case history.StepFailed:
				var p history.FailedPayload
				_ = json.Unmarshal(e.Payload, &p)
				parked = p.Error.Next == "park" || (p.Error.Next == "fail" && isCompensation(step))
			case history.StepCompleted:
				parked = false
			}
		}
		if !parked {
			return fmt.Errorf("step %q is not parked: %w", step, ErrNotFound)
		}
		var typ string
		var payload any
		switch resolution {
		case ResolveCompleted:
			typ, payload = history.StepCompleted, map[string]any{"output": output, "resolved_by": by, "note": note}
		case ResolveFailed:
			typ, payload = history.StepFailed, history.FailedPayload{Error: history.Error{Kind: "resolved", Message: "operator confirmed it did not happen: " + note, Next: "fail"}}
		case ResolveRetry:
			// "not_sent": the operator confirmed nothing reached the provider, so
			// the worker may send again even for an unsafe write.
			typ, payload = history.StepFailed, history.FailedPayload{Error: history.Error{Kind: "not_sent", Message: "operator confirmed it did not happen and asked for a retry: " + note, Next: "retry"}}
		default:
			return fmt.Errorf("resolution must be completed, failed, or retry")
		}
		if _, err := appendEvent(ctx, tx, ref.ID, typ, step, attempt, payload, history.OriginAPI); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'running' WHERE id = $1`, ref.ID); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"run_id": ref.ID, "step": step, "resolution": resolution, "note": note})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'run.resolve', $3, $4)`, ref.TenantID, by, ref.ID.String()+"/"+step, detail); err != nil {
			return err
		}
		return s.decideInline(ctx, tx, ref)
	})
}
