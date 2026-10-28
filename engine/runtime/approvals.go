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

// Step-up factors, weakest first (spec 9.1, 11.2). A policy names the
// weakest it accepts: "whatsapp_pin" takes the WhatsApp approval PIN
// entered in a WhatsApp Flow bound to the decision, or an authenticator
// code or passkey; "totp" takes an authenticator code or a passkey;
// "passkey" only a passkey.
const (
	StepUpWhatsAppPIN = "whatsapp_pin"
	StepUpTOTP        = "totp"
	StepUpPasskey     = "passkey"
)

func stepUpRank(m string) int {
	switch m {
	case StepUpWhatsAppPIN:
		return 1
	case StepUpTOTP:
		return 2
	case StepUpPasskey:
		return 3
	}
	return 0
}

// StepUpSatisfies reports whether the factor given meets the step-up
// required: the same, or a stronger one.
func StepUpSatisfies(required, given string) bool {
	if required == "" || required == given {
		return true
	}
	r, g := stepUpRank(required), stepUpRank(given)
	return r > 0 && g >= r
}

// Vote is one approver's decision on an approval step.
type Vote struct {
	UserID   uuid.UUID
	Roles    []string // the approver's membership roles
	Decision string   // approved | rejected
	Channel  string   // web, api, whatsapp ...
	IP       string
	// StepUp is the second factor the approver just passed ("totp" or
	// "passkey"), if any.
	StepUp string
	// Audit, when set, records the vote in the tenant's audit chain inside
	// the vote's transaction, so a decision that continues the run is never
	// left without its audit entry.
	Audit func(tx pgx.Tx, res VoteResult) error
}

// VoteResult is an approval step's state after a vote.
type VoteResult struct {
	Status string // open | approved | rejected
	Level  int    // the level now open (0-based), when open
	Levels int    // how many levels the step has
	// SelfApproved: the voter was a maker, allowed as the only person who
	// can decide the approval.
	SelfApproved bool
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
//     a policy may relax this), unless they are the only active person who
//     can decide it (a one-person business), when the vote is marked
//     self-approved (a policy may forbid that too);
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
		if err := s.vote(ctx, tx, ref, step, v, &res); err != nil {
			return err
		}
		if v.Audit != nil {
			return v.Audit(tx, res)
		}
		return nil
	})
	return res, err
}

func (s *Store) vote(ctx context.Context, tx pgx.Tx, ref RunRef, step string, v Vote, res *VoteResult) error {
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
	forbidSelf, distinct, soleAllowed := true, true, true
	if a.constraints != nil {
		forbidSelf, distinct = a.constraints.ForbidSelfApproval, a.constraints.DistinctApprovers
		soleAllowed = !a.constraints.ForbidSoleApprover
	}

	// Role: the voter's own (an owner holds every role), or one
	// delegated to them now by someone who still holds it.
	var onBehalfOf *uuid.UUID
	if a.role != nil && !slices.Contains(v.Roles, *a.role) && !slices.Contains(v.Roles, "owner") {
		var from uuid.UUID
		err := tx.QueryRow(ctx, `SELECT g.from_user FROM delegations g WHERE g.to_user = $1 AND $2 = ANY (g.roles)
			AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now()
			AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = g.from_user AND m.role IN ($2, 'owner')) ORDER BY g.created_at LIMIT 1`, v.UserID, *a.role).Scan(&from)
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
		// Makers are the people behind them: a run started, a version
		// written or published with an API key is its owner's.
		if err := tx.QueryRow(ctx, `SELECT taskiem_actor_human(r.started_by), taskiem_actor_human(wv.created_by), taskiem_actor_human(wv.published_by::text) FROM runs r
			JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, ref.ID).
			Scan(&startedBy, &createdBy, &publishedBy); err != nil {
			return err
		}
		for _, maker := range []*string{startedBy, createdBy, publishedBy} {
			if maker == nil || !slices.Contains(people, *maker) {
				continue
			}
			// Nobody else could ever decide it: a one-person business
			// approves its own, on the record.
			if soleAllowed && onBehalfOf == nil {
				sole, err := SoleApprover(ctx, tx, a.role, v.UserID)
				if err != nil {
					return err
				}
				if sole {
					res.SelfApproved = true
					break
				}
			}
			return fmt.Errorf("the maker of a run or workflow cannot approve it, nor can someone acting for them: %w", ErrNotAllowed)
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
	if distinct {
		// Any other level, not only earlier ones: an escalation reopens the
		// step at its first level with its decisions kept, and someone who
		// approved a later level then must not approve the escalated one.
		var other bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND level <> $3
			AND (user_id::text = ANY ($4) OR on_behalf_of::text = ANY ($4)))`, ref.ID, step, a.level, people).Scan(&other); err != nil {
			return err
		}
		if other {
			return fmt.Errorf("an approver of another level cannot approve this one too: %w", ErrNotAllowed)
		}
	}
	// Step-up last: nobody is asked for a second factor to cast a vote
	// that would be refused anyway.
	// A stronger factor satisfies a weaker requirement.
	if a.stepUp != nil && !StepUpSatisfies(*a.stepUp, v.StepUp) {
		return &StepUpError{Method: *a.stepUp}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO approval_decisions (tenant_id, run_id, step_id, level, user_id, decision, channel, ip, shown, step_up, on_behalf_of, self_approved)
		SELECT tenant_id, run_id, step_id, $3, $4, $5, $6, NULLIF($7, ''), subject, NULLIF($8, ''), $9, $10 FROM approvals WHERE run_id = $1 AND step_id = $2
		ON CONFLICT DO NOTHING`, ref.ID, step, a.level, v.UserID, v.Decision, v.Channel, v.IP, v.StepUp, onBehalfOf, res.SelfApproved)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyDecided
	}
	// The evidence that outlives the run (approval_history, 00245).
	if _, err := tx.Exec(ctx, `INSERT INTO approval_history (tenant_id, run_id, step_id, level, user_id, on_behalf_of, decision, step_up, channel,
			self_approved, decided_at, workflow_id, policy, amount_naira)
		SELECT d.tenant_id, d.run_id, d.step_id, d.level, d.user_id, d.on_behalf_of, d.decision, d.step_up, d.channel, d.self_approved, d.decided_at,
			r.workflow_id, a.policy, taskiem_subject_amount(a.subject)
		FROM approval_decisions d JOIN approvals a ON a.run_id = d.run_id AND a.step_id = d.step_id JOIN runs r ON r.id = d.run_id
		WHERE d.run_id = $1 AND d.step_id = $2 AND d.level = $3 AND d.user_id = $4`, ref.ID, step, a.level, v.UserID); err != nil {
		return err
	}
	if v.Decision == "rejected" {
		res.Status = "rejected"
		return s.decideApproval(ctx, tx, ref, step, "rejected", v.UserID.String(), v.Channel, false)
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
	var self bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND self_approved)`, ref.ID, step).Scan(&self); err != nil {
		return err
	}
	res.Status = "approved"
	return s.decideApproval(ctx, tx, ref, step, "approved", strings.Join(approvers, ","), v.Channel, self)
}

// SoleApprover reports whether user is the only active person who can
// decide an approval for role (nil: any member with approval.decide): no
// other member holds the role or owner, and it is delegated to nobody else.
func SoleApprover(ctx context.Context, tx pgx.Tx, role *string, user uuid.UUID) (bool, error) {
	var sole bool
	err := tx.QueryRow(ctx, `SELECT taskiem_sole_approver($1, $2)`, role, user).Scan(&sole)
	return sole, err
}

func (s *Store) decideApproval(ctx context.Context, tx pgx.Tx, ref RunRef, step, status, by, channel string, self bool) error {
	if _, err := appendEvent(ctx, tx, ref.ID, history.ApprovalDecided, step, 0,
		history.ApprovalDecidedPayload{Decision: status, DecidedBy: by, Channel: channel, SelfApproved: self}, history.OriginAPI); err != nil {
		return err
	}
	return s.decideInline(ctx, tx, ref)
}
