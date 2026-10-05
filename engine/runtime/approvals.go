package runtime

import (
	"context"
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
)

// Vote is one approver's decision on an approval step.
type Vote struct {
	UserID   uuid.UUID
	Roles    []string // the approver's membership roles
	Decision string   // approved | rejected
	Channel  string   // web, api, whatsapp ...
	IP       string
}

// VoteApproval records one approver's decision and, once the step's policy
// is met, decides the step, all in one transaction (spec 4.7, 13.3):
//
//   - the approver must hold the step's role, when it names one;
//   - whoever started the run, or wrote or published the workflow version,
//     cannot approve it (maker-checker);
//   - each approver counts once; "count" distinct approvals approve the
//     step, and any rejection rejects it.
//
// It returns the step's approval status after the vote: open, approved or
// rejected.
func (s *Store) VoteApproval(ctx context.Context, ref RunRef, step string, v Vote) (string, error) {
	if v.Decision != "approved" && v.Decision != "rejected" {
		return "", fmt.Errorf("decision must be approved or rejected, got %q: %w", v.Decision, ErrNotAllowed)
	}
	status := "open"
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		if _, err := lockRun(ctx, tx, ref.ID); err != nil {
			return err
		}
		var role *string
		var required int
		var st string
		err := tx.QueryRow(ctx, `SELECT role, required, status FROM approvals WHERE run_id = $1 AND step_id = $2`, ref.ID, step).Scan(&role, &required, &st)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no approval %q on run %s: %w", step, ref.ID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if st != "open" {
			return fmt.Errorf("approval %q is %s: %w", step, st, ErrApprovalClosed)
		}
		if role != nil && !slices.Contains(v.Roles, *role) {
			return fmt.Errorf("approval %q needs role %q: %w", step, *role, ErrNotAllowed)
		}
		var startedBy, createdBy, publishedBy *string
		if err := tx.QueryRow(ctx, `SELECT r.started_by, wv.created_by, wv.published_by::text FROM runs r
			JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, ref.ID).
			Scan(&startedBy, &createdBy, &publishedBy); err != nil {
			return err
		}
		me := v.UserID.String()
		for _, maker := range []*string{startedBy, createdBy, publishedBy} {
			if maker != nil && *maker == me {
				return fmt.Errorf("the maker of a run or workflow cannot approve it: %w", ErrNotAllowed)
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO approval_decisions (tenant_id, run_id, step_id, user_id, decision, channel, ip, shown)
			SELECT tenant_id, run_id, step_id, $3, $4, $5, NULLIF($6, ''), subject FROM approvals WHERE run_id = $1 AND step_id = $2
			ON CONFLICT DO NOTHING`, ref.ID, step, v.UserID, v.Decision, v.Channel, v.IP)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrAlreadyDecided
		}
		rows, err := tx.Query(ctx, `SELECT user_id::text FROM approval_decisions WHERE run_id = $1 AND step_id = $2 AND decision = 'approved' ORDER BY decided_at`, ref.ID, step)
		if err != nil {
			return err
		}
		approvers, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		decidedBy := ""
		switch {
		case v.Decision == "rejected":
			status, decidedBy = "rejected", me
		case len(approvers) >= required:
			status, decidedBy = "approved", strings.Join(approvers, ",")
		default:
			return nil
		}
		if _, err := appendEvent(ctx, tx, ref.ID, history.ApprovalDecided, step, 0,
			history.ApprovalDecidedPayload{Decision: status, DecidedBy: decidedBy, Channel: v.Channel}, history.OriginAPI); err != nil {
			return err
		}
		return s.decideInline(ctx, tx, ref)
	})
	return status, err
}
