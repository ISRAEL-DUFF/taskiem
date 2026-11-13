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
		// A live run is accepted too, not only one marked as waiting for
		// reconciliation: what matters is that the step is parked. A run
		// resolved before the status was kept for its other parked steps
		// was left running with a step nobody could resolve.
		switch run.status {
		case "needs_reconciliation", "running", "waiting":
		default:
			return fmt.Errorf("run %s is %s, not waiting for reconciliation: %w", ref.ID, run.status, ErrNotFound)
		}
		hist, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		attempts, parked := waitingForOperator(hist)
		attempt := attempts[step]
		if !parked[step] {
			return fmt.Errorf("step %q is not parked: %w", step, ErrNotFound)
		}
		var typ string
		var payload any
		switch resolution {
		case ResolveCompleted:
			typ, payload = history.StepCompleted, map[string]any{"output": output, "resolved_by": by, "note": note}
		case ResolveFailed:
			typ, payload = history.StepFailed, history.FailedPayload{Error: history.Error{Kind: history.KindResolved, Message: "operator confirmed it did not happen: " + note, Next: "fail"}}
		case ResolveRetry:
			// "not_sent": the operator confirmed nothing reached the provider, so
			// the worker may send again even for an unsafe write. A person's
			// re-issue gets a new key (decision 0053).
			typ, payload = history.StepFailed, history.FailedPayload{Error: history.Error{Kind: "not_sent", Message: "operator confirmed it did not happen and asked for a retry: " + note, Next: "retry", Reissue: true}}
		default:
			return fmt.Errorf("resolution must be completed, failed, or retry")
		}
		if _, err := appendEvent(ctx, tx, ref.ID, typ, step, attempt, payload, history.OriginAPI); err != nil {
			return err
		}
		// The run waits for reconciliation for as long as any step is parked:
		// resolving one of two must leave the other resolvable.
		delete(parked, step)
		if resolution == ResolveFailed && isCompensation(step) {
			parked[step] = true // a compensation that did not happen still blocks the run
		}
		status := "running"
		if len(parked) > 0 {
			status = "needs_reconciliation"
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET status = $2 WHERE id = $1`, ref.ID, status); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"run_id": ref.ID, "step": step, "resolution": resolution, "note": note})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'run.resolve', $3, $4)`, ref.TenantID, by, ref.ID.String()+"/"+step, detail); err != nil {
			return err
		}
		return s.decideInline(ctx, tx, ref)
	})
}

// waitingForOperator returns each step's last scheduled attempt, and the
// steps whose latest outcome leaves them parked for an operator: a park,
// or a compensation that failed for good.
func waitingForOperator(hist []history.Event) (attempts map[string]int, parked map[string]bool) {
	attempts, parked = map[string]int{}, map[string]bool{}
	for _, e := range hist {
		switch e.Type {
		case history.StepScheduled:
			attempts[e.StepID] = e.Attempt
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			if p.Error.Next == "park" || (p.Error.Next == "fail" && isCompensation(e.StepID)) {
				parked[e.StepID] = true
			} else {
				delete(parked, e.StepID)
			}
		case history.StepCompleted:
			delete(parked, e.StepID)
		}
	}
	return attempts, parked
}
