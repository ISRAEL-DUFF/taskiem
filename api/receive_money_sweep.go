package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/templates"
)

// The sweep to the bank (docs/spec/iswallet-account-numbers.md N2): what a
// business's Taskiem wallet holds, sent to its saved bank account weekly,
// or whenever the wallet holds more than an amount (checked hourly),
// keeping an amount the owner chooses and room for the payout's fee.
//
// The sweep has no approval step: the founder chose it so (the plan's §10).
// It is safe without one because of what it cannot do. The bank account
// is written into its workflow, as into the withdrawal one, and a new
// account versions both in the same transaction under the same governance
// (four-eyes publishing holds both); the amount comes from the balance,
// never from input, so anyone who starts it by hand moves at most what the
// rule would, to the same account, earlier; and each scheduled time pays
// once. Its workflow is Taskiem's (receive_money_sweep.json), never the
// library's, whose payouts always take an approval.

//go:embed receive_money_sweep.json
var sweepDoc []byte

var sweepTemplate = sync.OnceValues(func() (*templates.Template, error) { return templates.Parse(sweepDoc) })

const (
	sweepName = "Send my money to my bank"
	// sweepMinimumNaira is the least one sweep sends.
	sweepMinimumNaira = 1000
	// defaultPayoutFeeKobo is the room left for the payout's fee while
	// iswallet's is unknown (needs-people RM-5).
	defaultPayoutFeeKobo = 10_000
)

// sweepSettings are a business's sweep (receive_money.sweep_*).
type sweepSettings struct {
	// Mode is off, weekly (Day at Time) or above (hourly, when the wallet
	// holds more than AboveKobo).
	Mode      string `json:"mode"`
	Day       string `json:"day,omitempty"`
	Time      string `json:"time,omitempty"`
	AboveKobo int64  `json:"above_kobo"`
	KeepKobo  int64  `json:"keep_kobo"`
}

// sweepView is the sweep as the Payments page shows it.
type sweepView struct {
	sweepSettings
	// FeeAllowanceKobo is what each sweep leaves for the bank's fee.
	FeeAllowanceKobo int64      `json:"fee_allowance_kobo"`
	MinimumKobo      int64      `json:"minimum_kobo"`
	WorkflowID       *uuid.UUID `json:"workflow_id,omitempty"`
	// Waiting says what a change to the sweep waits for, or "".
	Waiting string `json:"waiting,omitempty"`
}

var sweepTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

var sweepDays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// payoutFeeKobo is the room each sweep leaves for the payout's fee.
func (s *Server) payoutFeeKobo() int64 {
	if s.ReceivePayoutFeeKobo > 0 {
		return s.ReceivePayoutFeeKobo
	}
	return defaultPayoutFeeKobo
}

// check refuses settings the sweep cannot keep.
func (set sweepSettings) check() error {
	switch {
	case set.Mode != "off" && set.Mode != "weekly" && set.Mode != "above":
		return fmt.Errorf("%w: mode is off, weekly or above", errBadRequest)
	case set.KeepKobo < 0 || set.KeepKobo > maxWithdrawKobo:
		return fmt.Errorf("%w: the amount to keep is between NGN 0 and NGN 1,000,000,000", errBadRequest)
	case set.Mode == "weekly" && !slices.Contains(sweepDays, set.Day):
		return fmt.Errorf("%w: choose the day of the week", errBadRequest)
	case set.Mode == "weekly" && !sweepTime.MatchString(set.Time):
		return fmt.Errorf("%w: choose the time, such as 17:00", errBadRequest)
	case set.Mode == "above" && (set.AboveKobo < sweepMinimumNaira*100 || set.AboveKobo > maxWithdrawKobo):
		return fmt.Errorf("%w: the amount the wallet must hold is at least NGN 1,000", errBadRequest)
	}
	return nil
}

// sweepMinute spreads the hourly checks over the hour by business, so the
// businesses sharing Taskiem's key do not all ask in the same minute.
func sweepMinute(tenant uuid.UUID) int {
	h := fnv.New32a()
	_, _ = h.Write(tenant[:])
	return int(h.Sum32() % 60)
}

// sweepDefinition is the sweep made for the business: on its Taskiem
// connection and wallet, to the bank account given, on its schedule.
func sweepDefinition(tenant uuid.UUID, wallet string, to withdrawTo, set sweepSettings, feeKobo int64) ([]byte, error) {
	tpl, err := sweepTemplate()
	if err != nil {
		return nil, err
	}
	day, at, above := set.Day, set.Time, int64(0)
	if set.Mode == "above" {
		// The weekly schedule is replaced by an hourly one below.
		day, at, above = "monday", "00:00", set.AboveKobo
	}
	raw, err := tpl.Instantiate(map[string]any{
		"bank_code": to.BankCode, "account_number": to.AccountNumber, "day": day, "send_time": at,
		"keep": float64(set.KeepKobo) / 100, "above": float64(above) / 100, "minimum": sweepMinimumNaira, "fee_allowance": float64(feeKobo) / 100,
		"rail_connection": secrets.PlatformName, "funding_account": wallet,
	}, sweepName)
	if err != nil {
		return nil, err
	}
	if set.Mode == "above" {
		var def map[string]any
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, err
		}
		trig, _ := def["trigger"].(map[string]any)
		cfg, _ := trig["config"].(map[string]any)
		if cfg == nil {
			return nil, fmt.Errorf("sweep: the definition has no schedule")
		}
		cfg["cron"] = fmt.Sprintf("%d * * * *", sweepMinute(tenant))
		if raw, err = json.Marshal(def); err != nil {
			return nil, err
		}
	}
	return canonical(raw)
}

// sweepWorkflowTx makes the sweep's workflow, or its next version, and
// publishes it as the withdrawal one is.
func (s *Server) sweepWorkflowTx(r *http.Request, tx pgx.Tx, wallet string, have *uuid.UUID, to withdrawTo, set sweepSettings, fourEyes bool) (uuid.UUID, int, error) {
	doc, err := sweepDefinition(principalFrom(r.Context()).TenantID, wallet, to, set, s.payoutFeeKobo())
	if err != nil {
		return uuid.Nil, 0, err
	}
	return s.managedWorkflowTx(r, tx, managedWorkflow{Name: sweepName, What: "where your money is sent"}, doc, have, fourEyes)
}

// stopSweepTx takes the sweep out of prod at once (stopping needs no
// second person) and withdraws a version still waiting to be published,
// so approving it later cannot start the sweep again.
func stopSweepTx(r *http.Request, tx pgx.Tx, wf uuid.UUID) error {
	ctx := r.Context()
	if _, err := tx.Exec(ctx, `UPDATE publish_requests SET status = 'withdrawn', decided_at = now() WHERE workflow_id = $1 AND status = 'pending'`, wf); err != nil {
		return err
	}
	v, err := deployedVersion(ctx, tx, wf, "prod")
	if err != nil || v == 0 {
		return err
	}
	if err := undeployTx(ctx, tx, wf, "prod"); err != nil {
		return err
	}
	return auditTx(r, tx, "workflow.undeploy", fmt.Sprintf("%s/%d", wf, v), map[string]any{"environment": "prod", "via": "receive_money"})
}

// putSweep answers PUT /v1/settings/receive-money/sweep (owners): the
// sweep's mode, schedule and the amount kept. It needs the bank account
// saved under withdrawals, which it pays. Turning it off takes it out of
// prod at once; turning it on or changing it publishes a new version, held
// by four-eyes publishing like the withdrawal workflow.
func (s *Server) putSweep(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner chooses when money is sent to the bank")
		return
	}
	var set sweepSettings
	if err := decodeBody(r, &set); err != nil {
		s.fail(w, r, err)
		return
	}
	if set.Mode != "weekly" {
		set.Day, set.Time = "", ""
	}
	if set.Mode != "above" {
		set.AboveKobo = 0
	}
	if err := set.check(); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, _, err := s.receiveOn(r); err != nil {
		s.fail(w, r, err)
		return
	}
	var view receiveMoneyView
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// One change at a time, and with withdrawals: the account is read
		// under the same lock putWithdrawals takes.
		var m receiveMoney
		var have *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT wallet_id, payout_bank_code, payout_account_number, payout_account_name, sweep_workflow_id
			FROM receive_money WHERE tenant_id = $1 FOR UPDATE`, p.TenantID).Scan(&m.WalletID, &m.PayoutBankCode, &m.PayoutAccount, &m.PayoutName, &have); err != nil {
			return err
		}
		wf, v := have, 0
		if set.Mode == "off" {
			if have != nil {
				if err := stopSweepTx(r, tx, *have); err != nil {
					return err
				}
			}
		} else {
			if m.PayoutAccount == nil {
				return fmt.Errorf("%w: save the bank account under Withdraw to your bank first: it is where the money is sent", errConflict)
			}
			g, err := governanceTx(ctx, tx)
			if err != nil {
				return err
			}
			to := withdrawTo{BankCode: deref(m.PayoutBankCode), AccountNumber: *m.PayoutAccount, AccountName: deref(m.PayoutName)}
			made, n, err := s.sweepWorkflowTx(r, tx, m.WalletID, have, to, set, g.FourEyesPublish)
			if err != nil {
				return err
			}
			wf, v = &made, n
		}
		if _, err := tx.Exec(ctx, `UPDATE receive_money SET sweep_mode = $2, sweep_day = NULLIF($3, ''), sweep_time = NULLIF($4, ''), sweep_above_kobo = $5,
			sweep_keep_kobo = $6, sweep_workflow_id = $7, updated_at = now() WHERE tenant_id = $1`,
			p.TenantID, set.Mode, set.Day, set.Time, set.AboveKobo, set.KeepKobo, wf); err != nil {
			return err
		}
		detail := map[string]any{"mode": set.Mode, "keep_kobo": set.KeepKobo}
		switch set.Mode {
		case "weekly":
			detail["day"], detail["time"] = set.Day, set.Time
		case "above":
			detail["above_kobo"] = set.AboveKobo
		}
		if wf != nil {
			detail["workflow_id"] = *wf
		}
		if v > 0 {
			detail["version"] = v
		}
		if err := auditTx(r, tx, "settings.receive_money.sweep", receiveChannel, detail); err != nil {
			return err
		}
		var err error
		view, err = s.receiveMoneyView(ctx, tx, p.TenantID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// sweepOf is the business's sweep as the Payments page shows it.
func (s *Server) sweepOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (*sweepView, error) {
	v := sweepView{FeeAllowanceKobo: s.payoutFeeKobo(), MinimumKobo: sweepMinimumNaira * 100}
	var day, at *string
	if err := tx.QueryRow(ctx, `SELECT sweep_mode, sweep_day, sweep_time, sweep_above_kobo, sweep_keep_kobo, sweep_workflow_id FROM receive_money WHERE tenant_id = $1`, tenant).
		Scan(&v.Mode, &day, &at, &v.AboveKobo, &v.KeepKobo, &v.WorkflowID); err != nil {
		return nil, err
	}
	v.Day, v.Time = deref(day), deref(at)
	if v.Mode != "off" {
		w, err := workflowWaiting(ctx, tx, v.WorkflowID, sweepName, "the change to sending your money to the bank")
		if err != nil {
			return nil, err
		}
		if w != "" {
			v.Waiting = w + ". Until then it is sent as before."
		}
	}
	return &v, nil
}

// resweepTx writes the new bank account into the sweep when it is on, in
// the transaction that writes it into the withdrawal workflow, so the two
// never pay different accounts.
func (s *Server) resweepTx(r *http.Request, tx pgx.Tx, wallet string, to withdrawTo, fourEyes bool) error {
	ctx := r.Context()
	var set sweepSettings
	var day, at *string
	var have *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT sweep_mode, sweep_day, sweep_time, sweep_above_kobo, sweep_keep_kobo, sweep_workflow_id FROM receive_money WHERE tenant_id = $1`,
		principalFrom(ctx).TenantID).Scan(&set.Mode, &day, &at, &set.AboveKobo, &set.KeepKobo, &have); err != nil {
		return err
	}
	if set.Mode == "off" || have == nil {
		return nil
	}
	set.Day, set.Time = deref(day), deref(at)
	_, _, err := s.sweepWorkflowTx(r, tx, wallet, have, to, set, fourEyes)
	return err
}
