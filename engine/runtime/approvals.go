package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
)

// Approval policy errors.
var (
	ErrNotAllowed     = errors.New("not allowed")
	ErrAlreadyDecided = errors.New("already decided")
	ErrApprovalClosed = errors.New("approval is closed")
	// ErrStepUpRequired: the approval needs a fresh second factor (the
	// *StepUpError says which) before this person's vote counts.
	ErrStepUpRequired = errors.New("step-up authentication required")
)

// StepUpError names the second factor an approval needs.
type StepUpError struct{ Method string }

func (e *StepUpError) Error() string { return "this approval needs " + e.Method + " step-up" }
func (e *StepUpError) Unwrap() error { return ErrStepUpRequired }

// Vote is one approver's decision on an approval step.
type Vote struct {
	UserID   uuid.UUID
	Roles    []string // the approver's membership roles
	Decision string   // approved | rejected
	Channel  string   // web, api, whatsapp ...
	IP       string
	// StepUp is the second factor the approver just passed ("totp"), if any.
	StepUp string
}

// VoteResult is an approval step's state after a vote.
type VoteResult struct {
	Status string // open | approved | rejected
	Level  int    // the level now open (0-based), when open
	Levels int    // how many levels the step has
}

type approvalRow struct {
	role        *string
	required    int
	status      string
	levels      []history.ApprovalLevel
	level       int
	stepUp      *string
	constraints *history.ApprovalConstraints
}

// VoteApproval records one approver's decision and, once the step's policy
// is met, decides the step, all in one transaction (spec 9.1):
//
//   - the approver must hold the current level's role, themselves or by an
//     active delegation;
//   - an approval that needs step-up counts the vote only with it;
//   - whoever started the run, or wrote or published the workflow version,
//     cannot approve it, nor can anyone acting for them (maker-checker;
//     a policy may relax this);
//   - each approver counts once per level, and with distinct approvers
//     (the default) once per step;
//   - a level approves with "count" approvals and opens the next; any
//     rejection rejects the step.
func (s *Store) VoteApproval(ctx context.Context, ref RunRef, step string, v Vote) (VoteResult, error) {
	res := VoteResult{Status: "open"}
	if v.Decision != "approved" && v.Decision != "rejected" {
		return res, fmt.Errorf("decision must be approved or rejected, got %q: %w", v.Decision, ErrNotAllowed)
	}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		if _, err := lockRun(ctx, tx, ref.ID); err != nil {
			return err
		}
		var a approvalRow
		var levels, constraints []byte
		err := tx.QueryRow(ctx, `SELECT role, required, status, levels, level, step_up, constraints FROM approvals WHERE run_id = $1 AND step_id = $2`, ref.ID, step).
			Scan(&a.role, &a.required, &a.status, &levels, &a.level, &a.stepUp, &constraints)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no approval %q on run %s: %w", step, ref.ID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if len(levels) > 0 {
			if err := json.Unmarshal(levels, &a.levels); err != nil {
				return err
			}
		}
		if len(constraints) > 0 {
			a.constraints = &history.ApprovalConstraints{}
			if err := json.Unmarshal(constraints, a.constraints); err != nil {
				return err
			}
		}
		res.Level, res.Levels = a.level, max(1, len(a.levels))
		if a.status != "open" {
			return fmt.Errorf("approval %q is %s: %w", step, a.status, ErrApprovalClosed)
		}
		forbidSelf, distinct := true, true
		if a.constraints != nil {
			forbidSelf, distinct = a.constraints.ForbidSelfApproval, a.constraints.DistinctApprovers
		}

		// Role: the voter's own, or one delegated to them now.
		var onBehalfOf *uuid.UUID
		if a.role != nil && !slices.Contains(v.Roles, *a.role) {
			var from uuid.UUID
			err := tx.QueryRow(ctx, `SELECT from_user FROM delegations WHERE to_user = $1 AND $2 = ANY (roles)
				AND revoked_at IS NULL AND starts_at <= now() AND ends_at > now() ORDER BY created_at LIMIT 1`, v.UserID, *a.role).Scan(&from)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("approval %q needs role %q: %w", step, *a.role, ErrNotAllowed)
			}
			if err != nil {
				return err
			}
			onBehalfOf = &from
		}
		people := []string{v.UserID.String()}
		if onBehalfOf != nil {
			people = append(people, onBehalfOf.String())
		}
		if forbidSelf {
			var startedBy, createdBy, publishedBy *string
			if err := tx.QueryRow(ctx, `SELECT r.started_by, wv.created_by, wv.published_by::text FROM runs r
				JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, ref.ID).
				Scan(&startedBy, &createdBy, &publishedBy); err != nil {
				return err
			}
			for _, maker := range []*string{startedBy, createdBy, publishedBy} {
				if maker != nil && slices.Contains(people, *maker) {
					return fmt.Errorf("the maker of a run or workflow cannot approve it, nor can someone acting for them: %w", ErrNotAllowed)
				}
			}
		}
		// One voice per person per level, whether they vote or someone votes
		// for them under a delegation.
		var voiced bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND level = $3
			AND (user_id::text = ANY ($4) OR on_behalf_of::text = ANY ($4)))`, ref.ID, step, a.level, people).Scan(&voiced); err != nil {
			return err
		}
		if voiced {
			return ErrAlreadyDecided
		}
		if distinct && a.level > 0 {
			var earlier bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND level < $3
				AND (user_id::text = ANY ($4) OR on_behalf_of::text = ANY ($4)))`, ref.ID, step, a.level, people).Scan(&earlier); err != nil {
				return err
			}
			if earlier {
				return fmt.Errorf("an approver of an earlier level cannot approve this one too: %w", ErrNotAllowed)
			}
		}
		// Step-up last: nobody is asked for a second factor to cast a vote
		// that would be refused anyway.
		if a.stepUp != nil && *a.stepUp != v.StepUp {
			return &StepUpError{Method: *a.stepUp}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO approval_decisions (tenant_id, run_id, step_id, level, user_id, decision, channel, ip, shown, step_up, on_behalf_of)
			SELECT tenant_id, run_id, step_id, $3, $4, $5, $6, NULLIF($7, ''), subject, NULLIF($8, ''), $9 FROM approvals WHERE run_id = $1 AND step_id = $2
			ON CONFLICT DO NOTHING`, ref.ID, step, a.level, v.UserID, v.Decision, v.Channel, v.IP, v.StepUp, onBehalfOf)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrAlreadyDecided
		}
		if v.Decision == "rejected" {
			res.Status = "rejected"
			return s.decideApproval(ctx, tx, ref, step, "rejected", v.UserID.String(), v.Channel)
		}
		var approvals int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND level = $3 AND decision = 'approved'`,
			ref.ID, step, a.level).Scan(&approvals); err != nil {
			return err
		}
		if approvals < a.required {
			return nil
		}
		if next := a.level + 1; next < len(a.levels) {
			// This level is done: the next one opens for its role.
			res.Level = next
			_, err := tx.Exec(ctx, `UPDATE approvals SET level = $3, role = $4, required = $5 WHERE run_id = $1 AND step_id = $2`,
				ref.ID, step, next, a.levels[next].Role, a.levels[next].Count)
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id::text FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND decision = 'approved' ORDER BY level, decided_at`, ref.ID, step)
		if err != nil {
			return err
		}
		approvers, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		res.Status = "approved"
		return s.decideApproval(ctx, tx, ref, step, "approved", strings.Join(approvers, ","), v.Channel)
	})
	return res, err
}

func (s *Store) decideApproval(ctx context.Context, tx pgx.Tx, ref RunRef, step, status, by, channel string) error {
	if _, err := appendEvent(ctx, tx, ref.ID, history.ApprovalDecided, step, 0,
		history.ApprovalDecidedPayload{Decision: status, DecidedBy: by, Channel: channel}, history.OriginAPI); err != nil {
		return err
	}
	return s.decideInline(ctx, tx, ref)
}
