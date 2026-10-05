package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
)

type approvalItem struct {
	RunID       uuid.UUID  `json:"run_id"`
	StepID      string     `json:"step_id"`
	Workflow    string     `json:"workflow"`
	Environment string     `json:"environment"`
	Role        *string    `json:"role"`
	Required    int        `json:"required"`
	Approvals   int        `json:"approvals"`
	RequestedAt time.Time  `json:"requested_at"`
	TimeoutAt   *time.Time `json:"timeout_at"`
	Subject     any        `json:"subject"`
}

// listApprovals is the caller's inbox: open approvals for a role they hold
// (or naming no role) that they have not decided yet. Approvers see the
// subject opened: it is what they are deciding on, and what is recorded
// with their decision.
func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || !p.Can(PermApprovalDecide) {
		writeJSON(w, http.StatusOK, map[string]any{"approvals": []approvalItem{}})
		return
	}
	var out []approvalItem
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		rows, err := tx.Query(ctx, `SELECT a.run_id, a.step_id, w.name, r.environment, a.role, a.required,
				(SELECT count(*) FROM approval_decisions d WHERE d.run_id = a.run_id AND d.step_id = a.step_id AND d.decision = 'approved')::int,
				a.requested_at, a.timeout_at, a.subject
			FROM approvals a JOIN runs r ON r.id = a.run_id JOIN workflows w ON w.id = r.workflow_id
			WHERE a.status = 'open' AND (a.role IS NULL OR a.role = ANY ($1))
			  AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.run_id = a.run_id AND d.step_id = a.step_id AND d.user_id = $2)
			ORDER BY a.requested_at`, p.Roles, p.UserID)
		if err != nil {
			return err
		}
		var raws [][]byte
		for rows.Next() {
			var it approvalItem
			var subj []byte
			if err := rows.Scan(&it.RunID, &it.StepID, &it.Workflow, &it.Environment, &it.Role, &it.Required, &it.Approvals, &it.RequestedAt, &it.TimeoutAt, &subj); err != nil {
				return err
			}
			out = append(out, it)
			raws = append(raws, subj)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i, raw := range raws {
			var v any
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &v); err != nil {
					return err
				}
			}
			opened, err := pii.Open(ctx, s.Store.PII, tx, p.TenantID, v, pii.Taint{})
			if err != nil {
				return err
			}
			out[i].Subject = opened
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": nonNil(out)})
}

type decideReq struct {
	Decision string `json:"decision"` // approved | rejected
	Comment  string `json:"comment,omitempty"`
}

// decide records the caller's decision on an approval step. Only users
// decide (an API key is not a person), and the policy checks in
// runtime.Store.VoteApproval apply.
func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "approvals are decided by people, not API keys")
		return
	}
	run, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req decideReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	step := chi.URLParam(r, "step")
	ref := runtime.RunRef{ID: run, TenantID: p.TenantID}
	status, err := s.Store.VoteApproval(r.Context(), ref, step, runtime.Vote{
		UserID: p.UserID, Roles: p.Roles, Decision: req.Decision, Channel: "web", IP: clientIP(r),
	})
	switch {
	case errors.Is(err, runtime.ErrNotAllowed):
		s.fail(w, r, fmt.Errorf("%w: %w", errForbidden, err))
		return
	case errors.Is(err, runtime.ErrAlreadyDecided), errors.Is(err, runtime.ErrApprovalClosed):
		s.fail(w, r, fmt.Errorf("%w: %w", errConflict, err))
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	detail := map[string]any{"decision": req.Decision, "result": status}
	if req.Comment != "" {
		detail["comment"] = req.Comment
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "approval.decide", run.String()+"/"+step, detail)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": run, "step_id": step, "status": status})
}
