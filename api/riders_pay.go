package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/templates"
)

// Paying riders (docs/dispatch.md D5): Riders > Pay shows what each rider
// is due (their fees since the last payout, less the cash they owe),
// keeps their bank accounts, sets up the weekly payout from the
// pay-riders-weekly template, and starts it now.

const payRidersTemplate = "pay-riders-weekly"

type ridersPayView struct {
	Lines []dispatch.PayLine `json:"lines"`
	// WorkflowID is the pay-riders workflow, once set up.
	WorkflowID *uuid.UUID        `json:"workflow_id"`
	Payouts    []dispatch.Payout `json:"payouts"`
}

// getRidersPay is GET /v1/riders/pay.
func (s *Server) getRidersPay(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var out ridersPayView
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Lines, err = s.Dispatch.PayLinesTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if out.Payouts, err = s.Dispatch.PayoutsTx(r.Context(), tx, p.TenantID, 50); err != nil {
			return err
		}
		err = tx.QueryRow(r.Context(), `SELECT payout_workflow_id FROM dispatch_settings WHERE tenant_id = $1`, p.TenantID).Scan(&out.WorkflowID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putRiderBank is PUT /v1/riders/{id}/bank: the account a rider's payouts
// go to; their bank confirms the name at the first payout.
func (s *Server) putRiderBank(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in dispatch.Bank
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var shown string
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := s.Riders.GetTx(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		var err error
		shown, err = s.Dispatch.SetBankTx(r.Context(), tx, p.TenantID, id, in, p.Actor())
		return err
	}); err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bank": shown})
}

var payTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// setupRidersPay is PUT /v1/riders/pay/setup: the weekly payout's day,
// time and payments account (the business's Taskiem wallet unless another
// is chosen), made (or a new version) from the pay-riders-weekly template
// and published, under the business's publishing rules.
func (s *Server) setupRidersPay(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	var in struct {
		PayDay         int    `json:"pay_day"`
		PayTime        string `json:"pay_time"`
		RailConnection string `json:"rail_connection"`
		FundingAccount string `json:"funding_account"`
		ApproverRole   string `json:"approver_role"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch {
	case in.PayDay < 0 || in.PayDay > 6:
		s.fail(w, r, fmt.Errorf("%w: the day is 0 (Sunday) to 6 (Saturday)", errBadRequest))
		return
	case !payTime.MatchString(in.PayTime):
		s.fail(w, r, fmt.Errorf("%w: the time is like 17:00", errBadRequest))
		return
	}
	if in.ApproverRole == "" {
		in.ApproverRole = "owner"
	}
	tpl, ok := templates.Default().Get(payRidersTemplate)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: paying riders is not available here", errConflict))
		return
	}
	p := principalFrom(r.Context())
	var wf uuid.UUID
	var version int
	var waiting bool
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// With no payments account chosen, riders are paid from the
		// business's Taskiem wallet.
		if in.RailConnection == "" || in.RailConnection == secrets.PlatformName {
			var wallet *string
			err := tx.QueryRow(ctx, `SELECT wallet_id FROM receive_money WHERE tenant_id = $1`, p.TenantID).Scan(&wallet)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && (wallet == nil || *wallet == "") {
				return fmt.Errorf("%w: choose the payments account riders are paid from, or turn on receiving money with Taskiem", errConflict)
			}
			if err != nil {
				return err
			}
			in.RailConnection, in.FundingAccount = secrets.PlatformName, *wallet
		}
		doc, err := tpl.Instantiate(map[string]any{"pay_day": in.PayDay, "pay_time": in.PayTime, "rail_connection": in.RailConnection,
			"funding_account": in.FundingAccount, "approver_role": in.ApproverRole}, "Pay riders")
		if err != nil {
			return fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dispatch_settings (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, p.TenantID); err != nil {
			return err
		}
		var have *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT payout_workflow_id FROM dispatch_settings WHERE tenant_id = $1 FOR UPDATE`, p.TenantID).Scan(&have); err != nil {
			return err
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		waiting = g.FourEyesPublish
		if wf, version, err = s.managedWorkflowTx(r, tx, managedWorkflow{Name: "Pay riders", Template: payRidersTemplate, What: "how riders are paid"},
			doc, have, g.FourEyesPublish); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE dispatch_settings SET payout_workflow_id = $2, updated_at = now() WHERE tenant_id = $1`, p.TenantID, wf)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Under four-eyes publishing, it runs once a second person approves.
	writeJSON(w, http.StatusOK, map[string]any{"workflow_id": wf, "version": version, "waiting": waiting})
}

// payRiders is POST /v1/riders/pay: the payout now, as the weekly one
// runs; the approver approves it in Approvals or chat.
func (s *Server) payRiders(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var wf *uuid.UUID
	var version int
	err := s.tx(r, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT payout_workflow_id FROM dispatch_settings WHERE tenant_id = $1`, p.TenantID).Scan(&wf)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && wf == nil {
			return fmt.Errorf("%w: set up paying riders first", errConflict)
		}
		if err != nil {
			return err
		}
		version, err = deployedVersion(r.Context(), tx, *wf, "prod")
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: paying riders waits to be published", errConflict)
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	batch := uuid.Must(uuid.NewV7())
	ref, _, err := s.Store.StartRun(r.Context(), runtime.StartRequest{TenantID: p.TenantID, WorkflowID: *wf, Version: version, Environment: "prod",
		Trigger: map[string]any{"type": "manual", "body": map[string]any{"batch_id": batch.String()}}, StartedBy: p.Actor(), TriggerID: "riders/pay",
		DedupKey: "riders-pay:" + batch.String()})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": ref.ID, "batch_id": batch})
}
