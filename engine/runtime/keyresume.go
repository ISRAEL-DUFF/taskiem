package runtime

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
)

// ResumeKeyParked resumes the tenant's steps that a worker parked because
// its key could not be unwrapped (key_parked_steps; decision 0019). The key
// job calls it once the key works again. A step parked before anything was
// sent is retried; one parked while reconciling an earlier attempt is
// reconciled first. Neither spends the step's retry budget. A step someone
// already resolved, or a run that ended, is dropped from the list. It
// returns how many steps it resumed; the resumption is audited.
func (s *Store) ResumeKeyParked(ctx context.Context, tenant uuid.UUID) (int, error) {
	resumed := 0
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		type parked struct {
			run     uuid.UUID
			step    string
			attempt int
			mode    string
		}
		rows, err := tx.Query(ctx, `SELECT run_id, step_id, attempt, mode FROM key_parked_steps WHERE tenant_id = $1 ORDER BY parked_at, run_id LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		byRun := map[uuid.UUID][]parked{}
		var order []uuid.UUID
		for rows.Next() {
			var p parked
			if err := rows.Scan(&p.run, &p.step, &p.attempt, &p.mode); err != nil {
				rows.Close()
				return err
			}
			if _, ok := byRun[p.run]; !ok {
				order = append(order, p.run)
			}
			byRun[p.run] = append(byRun[p.run], p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var runs []string
		for _, runID := range order {
			run, err := lockRun(ctx, tx, runID)
			if err != nil {
				return err
			}
			n := 0
			if run.status == "needs_reconciliation" {
				hist, err := History(ctx, tx, runID)
				if err != nil {
					return err
				}
				still := parkedSteps(hist)
				for _, p := range byRun[runID] {
					last, ok := still[p.step]
					if !ok || last.attempt != p.attempt || last.err.Kind != history.KindKeyUnavailable {
						continue // resolved by a person, or parked again for another reason
					}
					e := history.Error{Kind: history.KindKeyRestored, Next: "retry",
						Message: "the tenant's encryption key is available again; retrying (nothing was sent while it was unavailable)"}
					if p.mode == "reconcile" {
						e = history.Error{Kind: history.KindKeyRestoredReconcile, Next: "reconcile",
							Message: "the tenant's encryption key is available again; checking with the provider what the earlier attempt did"}
					}
					if _, err := appendEvent(ctx, tx, runID, history.StepFailed, p.step, p.attempt, history.FailedPayload{Error: e}, history.OriginScheduler); err != nil {
						return err
					}
					delete(still, p.step)
					n++
				}
				if n > 0 && len(still) == 0 {
					if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'running' WHERE id = $1`, runID); err != nil {
						return err
					}
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM key_parked_steps WHERE tenant_id = $1 AND run_id = $2`, tenant, runID); err != nil {
				return err
			}
			if n > 0 {
				resumed += n
				runs = append(runs, runID.String())
				if err := s.decideInline(ctx, tx, RunRef{ID: runID, TenantID: tenant}); err != nil {
					return err
				}
			}
		}
		if resumed == 0 {
			return nil
		}
		detail, _ := json.Marshal(map[string]any{"steps": resumed, "runs": runs})
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'system', 'key.steps_resumed', 'tenant_key', $2)`, tenant, detail)
		return err
	})
	return resumed, err
}

type parkedStep struct {
	attempt int
	err     history.Error
}

// parkedSteps returns the steps whose latest outcome is a park.
func parkedSteps(hist []history.Event) map[string]parkedStep {
	out := map[string]parkedStep{}
	for _, e := range hist {
		switch e.Type {
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			if p.Error.Next == "park" {
				out[e.StepID] = parkedStep{attempt: e.Attempt, err: p.Error}
			} else {
				delete(out, e.StepID)
			}
		case history.StepCompleted, history.StepScheduled, history.StepCancelled:
			delete(out, e.StepID)
		}
	}
	return out
}
