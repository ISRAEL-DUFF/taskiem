package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/templates"
)

// Receive money with Taskiem (decision 0052; docs/spec/iswallet-account-
// numbers.md W2 to W4). Taskiem holds an iswallet client of its own; an
// owner turns this on and the business gets a wallet under it, held
// segregated or pooled as the owner chooses, as the connection
// iswallet/taskiem (no key: the vault hands the platform's, kept to this
// wallet). Everything a business's own rail does then works on it:
// invoices with one-time numbers, customers' and students' numbers, the
// money log and matching. The business also gets a number of its own, its
// "pay me" number, whose credits reach To match; its balance; and
// "Withdraw to my bank", a payout to a bank account resolved by name and
// kept, under an approval rule (one owner, two above an amount the owner
// sets), run as the withdraw-to-bank template so the payout and its
// outcome are in the money log like any other.
//
// The director's BVN and the business email are sent to iswallet once,
// when the wallet is made, and kept nowhere in Taskiem.

const (
	receiveConnector = "iswallet"
	// receiveChannel is the connection, as connector/name.
	receiveChannel = receiveConnector + "/" + secrets.PlatformName
	// withdrawPolicy is the approval rule Taskiem keeps for withdrawals.
	withdrawPolicy = "taskiem_withdrawals"
	// withdrawTemplate makes the workflow that withdraws.
	withdrawTemplate = "withdraw-to-bank"
)

func (s *Server) receiveMoneyRoutes(r chi.Router) {
	r.Get("/settings/receive-money", s.getReceiveMoney)
	r.With(s.tenantWide, s.feature(billing.FeatureReceiveMoney, false)).Post("/settings/receive-money", s.turnOnReceiveMoney)
	r.With(s.tenantWide).Put("/settings/receive-money/withdrawals", s.putWithdrawals)
	r.With(s.need(PermInvoiceRead)).Get("/receive-money/balance", s.receiveMoneyBalance)
	r.With(s.need(PermInvoiceRead)).Get("/receive-money/banks", s.receiveMoneyBanks)
	r.With(s.need(PermRunStart)).Post("/receive-money/withdraw", s.withdrawToBank)
}

// receiveMoney is a business's row (receive_money).
type receiveMoney struct {
	ConnectionID     uuid.UUID
	Mode             string
	WalletID         string
	OwnerType        string
	Reference        string
	AccountNumber    *string
	BankName         *string
	AccountName      *string
	AccountProblem   *string
	PayoutBankCode   *string
	PayoutBankName   *string
	PayoutAccount    *string
	PayoutName       *string
	TwoAboveKobo     int64
	WithdrawWorkflow *uuid.UUID
}

func loadReceiveMoney(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (*receiveMoney, error) {
	var m receiveMoney
	err := tx.QueryRow(ctx, `SELECT connection_id, mode, wallet_id, owner_type, account_reference, account_number, bank_name, account_name, account_problem,
		payout_bank_code, payout_bank_name, payout_account_number, payout_account_name, two_approvers_above_kobo, withdraw_workflow_id
		FROM receive_money WHERE tenant_id = $1`, tenant).Scan(&m.ConnectionID, &m.Mode, &m.WalletID, &m.OwnerType, &m.Reference, &m.AccountNumber, &m.BankName,
		&m.AccountName, &m.AccountProblem, &m.PayoutBankCode, &m.PayoutBankName, &m.PayoutAccount, &m.PayoutName, &m.TwoAboveKobo, &m.WithdrawWorkflow)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// receiveMode is the mode a new business's wallet is made in: live when
// this Taskiem holds the live client, else test (a staging Taskiem); ""
// when it holds neither.
func (s *Server) receiveMode() string {
	if s.Vault == nil {
		return ""
	}
	for _, m := range []string{"live", "test"} {
		if _, ok := s.Vault.Platform.For(receiveConnector, m); ok {
			return m
		}
	}
	return ""
}

type receiveAccount struct {
	AccountNumber string `json:"account_number"`
	BankName      string `json:"bank_name,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
}

type withdrawTo struct {
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name,omitempty"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

// receiveMoneyView answers GET /v1/settings/receive-money.
type receiveMoneyView struct {
	// Available: this Taskiem holds its own iswallet client.
	Available bool `json:"available"`
	// Plan: the business's plan includes it; PlanNote says otherwise.
	Plan     bool   `json:"plan"`
	PlanNote string `json:"plan_note,omitempty"`
	On       bool   `json:"on"`
	// DefaultOwnerType is what is offered first: segregated for a school,
	// pooled otherwise.
	DefaultOwnerType string          `json:"default_owner_type"`
	Mode             string          `json:"mode,omitempty"`
	OwnerType        string          `json:"owner_type,omitempty"`
	Connection       string          `json:"connection,omitempty"`
	Account          *receiveAccount `json:"account,omitempty"`
	AccountProblem   string          `json:"account_problem,omitempty"`
	WithdrawTo       *withdrawTo     `json:"withdraw_to,omitempty"`
	TwoAboveKobo     int64           `json:"two_approvers_above_kobo"`
	WithdrawWorkflow *uuid.UUID      `json:"withdraw_workflow_id,omitempty"`
}

func (s *Server) receiveMoneyView(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (receiveMoneyView, error) {
	out := receiveMoneyView{Available: s.receiveMode() != "", Plan: true, DefaultOwnerType: "client_end_business"}
	var school bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_settings WHERE tenant_id = $1 AND enabled)`, tenant).Scan(&school); err != nil {
		return out, err
	}
	if school {
		out.DefaultOwnerType = "client_end_user_business"
	}
	m, err := loadReceiveMoney(ctx, tx, tenant)
	if err != nil || m == nil {
		return out, err
	}
	out.On, out.Mode, out.OwnerType, out.Connection = true, m.Mode, m.OwnerType, receiveChannel
	if m.AccountNumber != nil {
		out.Account = &receiveAccount{AccountNumber: *m.AccountNumber, BankName: deref(m.BankName), AccountName: deref(m.AccountName)}
	}
	out.AccountProblem = deref(m.AccountProblem)
	if m.PayoutAccount != nil {
		out.WithdrawTo = &withdrawTo{BankCode: deref(m.PayoutBankCode), BankName: deref(m.PayoutBankName), AccountNumber: *m.PayoutAccount, AccountName: deref(m.PayoutName)}
	}
	out.TwoAboveKobo, out.WithdrawWorkflow = m.TwoAboveKobo, m.WithdrawWorkflow
	return out, nil
}

func (s *Server) getReceiveMoney(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out receiveMoneyView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.receiveMoneyView(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Billing != nil {
		var fe *billing.FeatureError
		if err := s.Billing.Require(r.Context(), p.TenantID, billing.FeatureReceiveMoney); errors.As(err, &fe) {
			out.Plan, out.PlanNote = false, fe.Error()
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type receiveMoneyReq struct {
	OwnerType     string `json:"owner_type"`
	BusinessEmail string `json:"business_email"`
	BVN           string `json:"bvn"`
	BusinessName  string `json:"business_name"`
}

// turnOnReceiveMoney answers POST /v1/settings/receive-money (owners): the
// business's wallet is made under Taskiem's client, at most once (the key
// is the tenant's), its connection and number with it. Asking again after
// it is on makes nothing new; it asks for the number again if iswallet
// could not issue it the first time.
func (s *Server) turnOnReceiveMoney(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner turns on receiving money with Taskiem")
		return
	}
	var req receiveMoneyReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	mode := s.receiveMode()
	if mode == "" {
		s.fail(w, r, fmt.Errorf("%w: receiving money with Taskiem is not set up on this Taskiem yet", errConflict))
		return
	}
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := connectorByID(reg, receiveConnector)
	if !ok || c.Actions["create_wallet"] == nil || c.Actions["issue_virtual_account"] == nil {
		s.fail(w, r, fmt.Errorf("%w: receiving money with Taskiem is not available here", errConflict))
		return
	}
	var have *receiveMoney
	var school string
	var schoolOn bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if have, err = loadReceiveMoney(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(NULLIF(name, ''), 'My business') FROM tenants WHERE id = $1`, p.TenantID).Scan(&school); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM school_settings WHERE tenant_id = $1 AND enabled)`, p.TenantID).Scan(&schoolOn)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if have == nil {
		if err := s.makeReceiveWallet(r, c, mode, school, schoolOn, req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	// The business's own number: asked for now, and again on a later press
	// if iswallet could not issue it (idempotent on its reference).
	if err := s.issueBusinessNumber(r.Context(), p.TenantID, c, school); err != nil {
		s.fail(w, r, err)
		return
	}
	var out receiveMoneyView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.receiveMoneyView(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if have != nil {
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// makeReceiveWallet creates the wallet at iswallet and records the
// connection and the business's row in one transaction.
func (s *Server) makeReceiveWallet(r *http.Request, c *connector.Connector, mode, business string, schoolOn bool, req receiveMoneyReq) error {
	p := principalFrom(r.Context())
	req.BVN, req.BusinessEmail, req.BusinessName = strings.TrimSpace(req.BVN), strings.TrimSpace(req.BusinessEmail), strings.TrimSpace(req.BusinessName)
	if req.OwnerType == "" {
		req.OwnerType = "client_end_business"
		if schoolOn {
			req.OwnerType = "client_end_user_business"
		}
	}
	switch {
	case !slices.Contains(walletOwnerTypes, req.OwnerType):
		return fmt.Errorf("%w: owner_type is client_end_user_business (other people's money, such as fees, in your own account) or client_end_business (only your own money, pooled)", errBadRequest)
	case !strings.Contains(req.BusinessEmail, "@"):
		return fmt.Errorf("%w: give the business's email", errBadRequest)
	case len(req.BVN) != 11 || strings.Trim(req.BVN, "0123456789") != "":
		return fmt.Errorf("%w: a BVN is 11 digits: the director's", errBadRequest)
	}
	if req.BusinessName == "" {
		req.BusinessName = business
	}
	spec := c.Manifest.Actions["create_wallet"]
	keySpec := connector.DefaultKeySpec
	if spec.Idempotency != nil {
		keySpec = *spec.Idempotency
	}
	key, err := keySpec.Key(effects.KeyInput{TenantID: p.TenantID.String(), Seed: "receive-money:" + p.TenantID.String(), StepID: "create_wallet"})
	if err != nil {
		return err
	}
	// Taskiem making a wallet under its own client is not a business's
	// shared-client call, which is kept to that business's wallets.
	keys, _ := s.Vault.Platform.For(receiveConnector, mode)
	creds := map[string]string{}
	for k, v := range keys {
		if k != "shared_client" {
			creds[k] = v
		}
	}
	creds = withMode(creds, mode)
	in := map[string]any{"owner_type": req.OwnerType, "owner_ref": "tenant:" + p.TenantID.String(), "currency": "NGN",
		"business_email": req.BusinessEmail, "bvn": req.BVN, "business_name": req.BusinessName}
	out, err := s.runAction(r.Context(), p.TenantID, c, creds, "create_wallet", "receive_money.create_wallet", in, key)
	if err != nil {
		if effects.Classify(err) == effects.KindFatal {
			return fmt.Errorf("%w: iswallet did not create the wallet: %s", errBadRequest, plainConnectorError(err))
		}
		return fmt.Errorf("iswallet did not answer whether it created the wallet; asking again is safe: %s", plainConnectorError(err))
	}
	wallet, _ := out["wallet_id"].(string)
	if wallet == "" {
		return errors.New("iswallet answered without a wallet")
	}
	return s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		id, err := s.Vault.CreatePlatformConnectionTx(ctx, tx, p.TenantID, "prod", receiveConnector, secrets.PlatformName, mode, wallet,
			map[string]string{"wallet_ids": wallet}, p.Actor())
		if err != nil {
			return err
		}
		ref := "biz_" + hex.EncodeToString(p.TenantID[:])
		if _, err := tx.Exec(ctx, `INSERT INTO receive_money (tenant_id, connection_id, mode, wallet_id, owner_type, account_reference, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.TenantID, id, mode, wallet, req.OwnerType, ref, p.Actor()); err != nil {
			return err
		}
		// Invoices are paid on it unless they already name a rail that
		// can take payments.
		st, err := invoicing.GetSettings(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		_, name, found := strings.Cut(st.Connection, "/")
		if !found {
			name = st.Connection
		}
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections WHERE environment = 'prod' AND status = 'active' AND name = $1 AND platform IS NULL)`, name).Scan(&has); err != nil {
			return err
		}
		if !has {
			st.Connection = receiveChannel
			if _, err := invoicing.SaveSettings(ctx, tx, p.TenantID, st); err != nil {
				return err
			}
		}
		// A school issues its students' numbers on it unless it already
		// issues on a rail of its own.
		if schoolOn {
			was, err := schools.GetIssuing(ctx, tx, p.TenantID)
			if err != nil {
				return err
			}
			if was.Channel == "" {
				if _, err := schools.SetIssuing(ctx, tx, p.TenantID, schools.Issuing{Channel: receiveChannel, Account: wallet, SiblingsShareCredit: was.SiblingsShareCredit}, p.Actor()); err != nil {
					return err
				}
			}
		}
		return auditTx(r, tx, "settings.receive_money.on", receiveChannel, map[string]any{"mode": mode, "owner_type": req.OwnerType,
			"wallet_id": wallet, "request_id": out["request_id"], "invoices": !has})
	})
}

// issueBusinessNumber asks for the business's own number when it has none
// yet, and records it, or iswallet's reason for not issuing it.
func (s *Server) issueBusinessNumber(ctx context.Context, tenant uuid.UUID, c *connector.Connector, business string) error {
	var m *receiveMoney
	if err := s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		m, err = loadReceiveMoney(ctx, tx, tenant)
		return err
	}); err != nil || m == nil || m.AccountNumber != nil {
		return err
	}
	name := business
	if len(name) > 31 {
		name = strings.TrimSpace(name[:31])
	}
	out, err := s.connectionAction(ctx, tenant, c, "issue_virtual_account", "receive_money.issue",
		map[string]any{"wallet_id": m.WalletID, "reference": m.Reference, "account_name": name}, m.Reference)
	return s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		if err != nil {
			if effects.Classify(err) != effects.KindFatal {
				return fmt.Errorf("iswallet did not answer with your account number; asking again is safe: %s", plainConnectorError(err))
			}
			_, e := tx.Exec(ctx, `UPDATE receive_money SET account_problem = $2, updated_at = now() WHERE tenant_id = $1`, tenant, plainConnectorError(err))
			return e
		}
		num, _ := out["account_number"].(string)
		bank, _ := out["bank_name"].(string)
		acct, _ := out["account_name"].(string)
		if num == "" {
			return errors.New("iswallet answered without an account number")
		}
		_, e := tx.Exec(ctx, `UPDATE receive_money SET account_number = $2, bank_name = NULLIF($3, ''), account_name = NULLIF($4, ''), account_problem = NULL, updated_at = now()
			WHERE tenant_id = $1`, tenant, num, bank, acct)
		return e
	})
}

// runAction runs one of a connector's actions with the credentials given,
// for the platform's own calls outside a run.
func (s *Server) runAction(ctx context.Context, tenant uuid.UUID, c *connector.Connector, creds map[string]string, action, purpose string, in map[string]any, key string) (map[string]any, error) {
	a := c.Actions[action]
	if a == nil {
		return nil, fmt.Errorf("%w: %s cannot %s", errConflict, c.Manifest.Name, action)
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + c.Manifest.ID}, 20*time.Second)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	resp, err := a.Execute(cctx, connector.Request{Input: in, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1, IdempotencyKey: key})
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// connectionAction runs an action on the business's iswallet/taskiem
// connection: the platform's key, kept to the business's own wallet.
func (s *Server) connectionAction(ctx context.Context, tenant uuid.UUID, c *connector.Connector, action, purpose string, in map[string]any, key string) (map[string]any, error) {
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose})
	creds, err := s.Vault.Credentials(use, tenant, "prod", receiveConnector, secrets.PlatformName)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, tenant, c, creds, action, purpose, in, key)
}

// receiveOn is the business's row and its connector, or a refusal when it
// has not turned receiving money with Taskiem on.
func (s *Server) receiveOn(r *http.Request) (*receiveMoney, *connector.Connector, error) {
	p := principalFrom(r.Context())
	var m *receiveMoney
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		m, err = loadReceiveMoney(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		return nil, nil, err
	}
	if m == nil {
		return nil, nil, fmt.Errorf("%w: turn on receiving money with Taskiem first", errConflict)
	}
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		return nil, nil, err
	}
	c, ok := connectorByID(reg, receiveConnector)
	if !ok {
		return nil, nil, fmt.Errorf("%w: receiving money with Taskiem is not available here", errConflict)
	}
	return m, c, nil
}

// receiveMoneyBalance answers GET /v1/receive-money/balance: what the
// wallet holds now, from iswallet.
func (s *Server) receiveMoneyBalance(w http.ResponseWriter, r *http.Request) {
	m, c, err := s.receiveOn(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.connectionAction(r.Context(), principalFrom(r.Context()).TenantID, c, "get_balance", "receive_money.balance", map[string]any{"wallet_id": m.WalletID}, "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "iswallet did not say what the wallet holds: " + plainConnectorError(err), "code": "provider"})
		return
	}
	var available, ledger int64
	list, _ := out["balances"].([]any)
	for _, x := range list {
		b, _ := x.(map[string]any)
		if cur, _ := b["currency"].(string); cur != "" && cur != "NGN" {
			continue
		}
		available += kobo(b["available"])
		ledger += kobo(b["ledger"])
	}
	writeJSON(w, http.StatusOK, map[string]any{"available_kobo": available, "ledger_kobo": ledger, "currency": "NGN", "at": time.Now().UTC()})
}

func kobo(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// receiveMoneyBanks answers GET /v1/receive-money/banks: the banks a
// withdrawal can go to.
func (s *Server) receiveMoneyBanks(w http.ResponseWriter, r *http.Request) {
	_, c, err := s.receiveOn(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.connectionAction(r.Context(), principalFrom(r.Context()).TenantID, c, "list_banks", "receive_money.banks", map[string]any{}, "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "iswallet did not list the banks: " + plainConnectorError(err), "code": "provider"})
		return
	}
	banks, _ := out["banks"].([]any)
	if banks == nil {
		banks = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"banks": banks})
}

type withdrawalsReq struct {
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	// TwoAboveKobo: withdrawals of this much or more take two people (0:
	// one, the owner).
	TwoAboveKobo int64 `json:"two_approvers_above_kobo"`
}

// putWithdrawals answers PUT /v1/settings/receive-money/withdrawals
// (owners): the bank account withdrawals go to, its name found by name
// enquiry and kept, and the approval rule; the workflow that withdraws is
// made and published the first time.
func (s *Server) putWithdrawals(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner chooses where withdrawals go")
		return
	}
	var req withdrawalsReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.BankCode, req.AccountNumber = strings.TrimSpace(req.BankCode), strings.TrimSpace(req.AccountNumber)
	switch {
	case len(req.BankCode) < 3 || len(req.BankCode) > 6:
		s.fail(w, r, fmt.Errorf("%w: choose your bank", errBadRequest))
		return
	case len(req.AccountNumber) != 10 || strings.Trim(req.AccountNumber, "0123456789") != "":
		s.fail(w, r, fmt.Errorf("%w: an account number is 10 digits", errBadRequest))
		return
	case req.TwoAboveKobo < 0:
		s.fail(w, r, fmt.Errorf("%w: the amount for two approvers cannot be below zero", errBadRequest))
		return
	}
	m, c, err := s.receiveOn(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.connectionAction(r.Context(), p.TenantID, c, "name_enquiry", "receive_money.name_enquiry",
		map[string]any{"bank_code": req.BankCode, "account_number": req.AccountNumber}, "")
	if err != nil {
		if effects.Classify(err) == effects.KindFatal {
			s.fail(w, r, fmt.Errorf("%w: the bank did not find that account: %s", errBadRequest, plainConnectorError(err)))
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the bank did not answer; try again: " + plainConnectorError(err), "code": "provider"})
		return
	}
	name, _ := out["account_name"].(string)
	if name == "" {
		s.fail(w, r, fmt.Errorf("%w: the bank gave no name for that account", errBadRequest))
		return
	}
	tpl, ok := templates.Default().Get(withdrawTemplate)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: withdrawals are not available here", errConflict))
		return
	}
	var view receiveMoneyView
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := writeWithdrawPolicyTx(r, tx, p.TenantID, req.TwoAboveKobo, p.Actor()); err != nil {
			return err
		}
		wf := m.WithdrawWorkflow
		if wf == nil {
			id, err := s.makeWithdrawWorkflowTx(r, tx, tpl, m.WalletID)
			if err != nil {
				return err
			}
			wf = &id
		}
		if _, err := tx.Exec(ctx, `UPDATE receive_money SET payout_bank_code = $2, payout_bank_name = NULLIF($3, ''), payout_account_number = $4, payout_account_name = $5,
			two_approvers_above_kobo = $6, withdraw_workflow_id = $7, updated_at = now() WHERE tenant_id = $1`,
			p.TenantID, req.BankCode, strings.TrimSpace(req.BankName), req.AccountNumber, name, req.TwoAboveKobo, *wf); err != nil {
			return err
		}
		if err := auditTx(r, tx, "settings.receive_money.withdrawals", receiveChannel, map[string]any{"bank_code": req.BankCode,
			"account_number": req.AccountNumber, "account_name": name, "two_approvers_above_kobo": req.TwoAboveKobo, "workflow_id": *wf}); err != nil {
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

// writeWithdrawPolicyTx keeps the approval rule withdrawals use: an owner
// approves; at or above twoAbove kobo, two different owners do. The maker
// may approve their own withdrawal (a one-owner business could not
// otherwise withdraw at all).
func writeWithdrawPolicyTx(r *http.Request, tx pgx.Tx, tenant uuid.UUID, twoAbove int64, by string) error {
	ctx := r.Context()
	no, yes := false, true
	pol := policy.Policy{Constraints: policy.Constraints{ForbidSelfApproval: &no, DistinctApprovers: &yes}, Timeout: "48h", OnTimeout: "reject",
		Description: "Withdrawals to the business's bank (Receive money with Taskiem)"}
	if twoAbove > 0 {
		pol.Rules = append(pol.Rules, policy.Rule{When: fmt.Sprintf("=subject.amount_kobo >= %d", twoAbove), Levels: []policy.Level{{Role: "owner"}, {Role: "owner"}}})
	}
	pol.Rules = append(pol.Rules, policy.Rule{Levels: []policy.Level{{Role: "owner"}}})
	doc, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	if _, err := policy.Parse(doc); err != nil {
		return err
	}
	var current []byte
	err = tx.QueryRow(ctx, `SELECT document FROM approval_policies WHERE name = $1 AND state = 'active'`, withdrawPolicy).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current != nil {
		var a, b any
		_ = json.Unmarshal(current, &a)
		_ = json.Unmarshal(doc, &b)
		if same, _ := json.Marshal(a); string(same) == func() string { x, _ := json.Marshal(b); return string(x) }() {
			return nil
		}
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy:' || $1))`, withdrawPolicy); err != nil {
		return err
	}
	var v int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM approval_policies WHERE name = $1`, withdrawPolicy).Scan(&v); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, withdrawPolicy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO approval_policies (tenant_id, name, version, document, state, created_by) VALUES ($1, $2, $3, $4, 'active', $5)`,
		tenant, withdrawPolicy, v, doc, by); err != nil {
		return err
	}
	return auditTx(r, tx, "policy.write", fmt.Sprintf("%s/%d", withdrawPolicy, v), map[string]any{"state": "active", "via": "receive_money"})
}

// makeWithdrawWorkflowTx makes the workflow that withdraws, from the
// withdraw-to-bank template on the business's connection and wallet, with
// its approval under the approval rule, and publishes it.
func (s *Server) makeWithdrawWorkflowTx(r *http.Request, tx pgx.Tx, tpl *templates.Template, wallet string) (uuid.UUID, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	raw, err := tpl.Instantiate(map[string]any{"approver_role": "owner", "rail_connection": secrets.PlatformName, "funding_account": wallet}, "Withdraw to my bank")
	if err != nil {
		return uuid.Nil, err
	}
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return uuid.Nil, err
	}
	steps, _ := def["steps"].([]any)
	for _, x := range steps {
		if st, _ := x.(map[string]any); st["type"] == "approval" {
			if cfg, ok := st["config"].(map[string]any); ok {
				cfg["policy"] = withdrawPolicy
			}
		}
	}
	patched, err := json.Marshal(def)
	if err != nil {
		return uuid.Nil, err
	}
	doc, err := canonical(patched)
	if err != nil {
		return uuid.Nil, err
	}
	wf, err := s.createWorkflowTx(ctx, tx, p, "Withdraw to my bank")
	if err != nil {
		return uuid.Nil, err
	}
	if err := insertVersionTx(ctx, tx, p.TenantID, wf, 1, doc, nil, p.Actor(), ""); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET template_id = $2 WHERE workflow_id = $1 AND version = 1`, wf, tpl.ID); err != nil {
		return uuid.Nil, err
	}
	if err := auditTx(r, tx, "workflow.create", wf.String(), map[string]any{"name": "Withdraw to my bank", "template": tpl.ID, "state": "draft", "via": "receive_money"}); err != nil {
		return uuid.Nil, err
	}
	var by *uuid.UUID
	if p.UserID != uuid.Nil {
		by = &p.UserID
	}
	probs, _, _, err := s.publishTx(ctx, tx, p.TenantID, wf, 1, by, "")
	if len(probs) > 0 {
		msgs := make([]string, len(probs))
		for i, pr := range probs {
			msgs[i] = pr.Message
		}
		return uuid.Nil, fmt.Errorf("%w: the withdrawal workflow could not be published: %s", errConflict, strings.Join(msgs, "; "))
	}
	if err != nil {
		return uuid.Nil, err
	}
	return wf, nil
}

type withdrawReq struct {
	AmountKobo int64 `json:"amount_kobo"`
}

// withdrawToBank answers POST /v1/receive-money/withdraw: a run of the
// withdrawal workflow for the amount, to the kept bank account. It waits
// for the approval rule's approvers before anything is paid. The request's
// Idempotency-Key (or one made here) is the payment's key: asking again
// with it never pays twice.
func (s *Server) withdrawToBank(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req withdrawReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.AmountKobo < 100 {
		s.fail(w, r, fmt.Errorf("%w: withdraw at least NGN 1", errBadRequest))
		return
	}
	m, _, err := s.receiveOn(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.PayoutAccount == nil || m.WithdrawWorkflow == nil {
		s.fail(w, r, fmt.Errorf("%w: choose the bank account withdrawals go to first", errConflict))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 80 {
		s.fail(w, r, fmt.Errorf("%w: Idempotency-Key is longer than 80 characters", errBadRequest))
		return
	}
	if key == "" {
		key = uuid.Must(uuid.NewV7()).String()
	}
	var version int
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		version, err = deployedVersion(r.Context(), tx, *m.WithdrawWorkflow, "prod")
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if version == 0 {
		s.fail(w, r, fmt.Errorf("%w: the withdrawal workflow is not published", errConflict))
		return
	}
	input := map[string]any{"amount_naira": float64(req.AmountKobo) / 100, "bank_code": deref(m.PayoutBankCode), "account_number": *m.PayoutAccount, "request_id": key}
	ref, created, err := s.Store.StartRun(r.Context(), runtime.StartRequest{
		TenantID: p.TenantID, WorkflowID: *m.WithdrawWorkflow, Version: version, Environment: "prod",
		Trigger:   map[string]any{"type": "manual", "body": input},
		StartedBy: p.Actor(), TriggerID: "receive-money/withdraw", DedupKey: "withdraw:" + key,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "receive_money.withdraw", ref.ID.String(), map[string]any{"amount_kobo": req.AmountKobo, "created": created})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"run_id": ref.ID, "workflow_id": *m.WithdrawWorkflow, "request_id": key, "created": created})
}

// tenantTx runs fn in a transaction scoped to tenant, outside a request.
func (s *Server) tenantTx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, fn)
}
