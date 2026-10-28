package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	// All of it is the business's prod wallet: a key limited to another
	// environment reaches none of it.
	r.With(s.need(PermInvoiceRead), s.tenantWide).Get("/settings/receive-money", s.getReceiveMoney)
	r.With(s.tenantWide, s.feature(billing.FeatureReceiveMoney, false)).Post("/settings/receive-money", s.turnOnReceiveMoney)
	r.With(s.tenantWide).Put("/settings/receive-money/withdrawals", s.putWithdrawals)
	r.With(s.tenantWide).Put("/settings/receive-money/sweep", s.putSweep)
	r.With(s.need(PermInvoiceRead), s.tenantWide).Get("/receive-money/balance", s.receiveMoneyBalance)
	r.With(s.need(PermInvoiceRead), s.tenantWide).Get("/receive-money/banks", s.receiveMoneyBanks)
	r.With(s.need(PermRunStart), s.tenantWide).Post("/receive-money/withdraw", s.withdrawToBank)
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
	// WithdrawWaiting says what a change to withdrawals waits for (a
	// second person, a promotion), in plain words; "" when none.
	WithdrawWaiting string `json:"withdraw_waiting,omitempty"`
	// Sweep is the sweep to the bank (N2), once it is on.
	Sweep *sweepView `json:"sweep,omitempty"`
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
	if out.WithdrawWaiting, err = withdrawWaiting(ctx, tx, m.WithdrawWorkflow); err != nil {
		return out, err
	}
	out.Sweep, err = s.sweepOf(ctx, tx, tenant)
	return out, err
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
	// The key is the tenant's and what was asked: a retry of the same
	// request (a lost answer) is the same wallet, a corrected email or BVN
	// a new request (iswallet keeps one wallet per owner_ref regardless).
	asked := sha256.Sum256([]byte(req.OwnerType + "\x00" + strings.ToLower(req.BusinessEmail) + "\x00" + req.BVN))
	key, err := keySpec.Key(effects.KeyInput{TenantID: p.TenantID.String(), Seed: "receive-money:" + p.TenantID.String() + ":" + hex.EncodeToString(asked[:8]), StepID: "create_wallet"})
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
		return fmt.Errorf("%w: iswallet did not answer whether it created the wallet; asking again is safe: %s", errUpstream, plainConnectorError(err))
	}
	wallet, _ := out["wallet_id"].(string)
	if wallet == "" {
		return fmt.Errorf("%w: iswallet answered without a wallet; asking again is safe", errUpstream)
	}
	// Invoices are paid on it unless they already name a rail that can
	// take payments.
	var settings invoicing.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		settings, err = invoicing.GetSettings(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		return err
	}
	pointInvoices := true
	if s.Invoices != nil && s.Invoices.Rails != nil {
		if _, err := s.Invoices.Rails.Resolve(r.Context(), p.TenantID, settings.Connection); err == nil {
			pointInvoices = false
		}
	}
	return s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// Two presses at once: the second finds the first's wallet.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('receive-money:' || $1::text))`, p.TenantID); err != nil {
			return err
		}
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM receive_money WHERE tenant_id = $1)`, p.TenantID).Scan(&done); err != nil || done {
			return err
		}
		id, err := s.Vault.CreatePlatformConnectionTx(ctx, tx, p.TenantID, "prod", receiveConnector, secrets.PlatformName, mode, wallet,
			map[string]string{"wallet_ids": wallet}, p.Actor())
		if err != nil {
			return err
		}
		// Random, so nobody can guess it and claim it first.
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		ref := "biz_" + hex.EncodeToString(b)
		if _, err := tx.Exec(ctx, `INSERT INTO receive_money (tenant_id, connection_id, mode, wallet_id, owner_type, account_reference, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.TenantID, id, mode, wallet, req.OwnerType, ref, p.Actor()); err != nil {
			return err
		}
		// Its credits are read back (the look-back) if a webhook is lost.
		if _, err := tx.Exec(ctx, `INSERT INTO money_lookbacks (tenant_id, channel, account) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, p.TenantID, receiveChannel, wallet); err != nil {
			return err
		}
		if pointInvoices {
			st, err := invoicing.GetSettings(ctx, tx, p.TenantID)
			if err != nil {
				return err
			}
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
			"wallet_id": wallet, "request_id": out["request_id"], "invoices": pointInvoices})
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
	if r := []rune(name); len(r) > 31 {
		name = strings.TrimSpace(string(r[:31]))
	}
	out, err := s.connectionAction(ctx, tenant, c, "issue_virtual_account", "receive_money.issue",
		map[string]any{"wallet_id": m.WalletID, "reference": m.Reference, "account_name": name}, m.Reference)
	return s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		if err != nil {
			if effects.Classify(err) != effects.KindFatal {
				return fmt.Errorf("%w: iswallet did not answer with your account number; asking again is safe: %s", errUpstream, plainConnectorError(err))
			}
			_, e := tx.Exec(ctx, `UPDATE receive_money SET account_problem = $2, updated_at = now() WHERE tenant_id = $1`, tenant, plainConnectorError(err))
			return e
		}
		num, _ := out["account_number"].(string)
		bank, _ := out["bank_name"].(string)
		acct, _ := out["account_name"].(string)
		if num == "" {
			return fmt.Errorf("%w: iswallet answered without an account number; asking again is safe", errUpstream)
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
	// Every business shares Taskiem's key and its limit at iswallet: one
	// business's pages and chats take no more than their share.
	if !s.limiter("receive-money:"+tenant.String(), time.Minute/receiveCallsPerMinute, receiveCallsPerMinute).Allow() {
		return nil, fmt.Errorf("too many requests for your wallet just now; try again in a minute: %w", effects.ErrRetryable)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose})
	creds, err := s.Vault.Credentials(use, tenant, "prod", receiveConnector, secrets.PlatformName)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, tenant, c, creds, action, purpose, in, key)
}

// errUpstream is iswallet not answering, or answering what Taskiem
// cannot read: 502, and asking again is safe.
var errUpstream = errors.New("upstream")

// receiveCallsPerMinute is one business's share of Taskiem's key at
// iswallet (100 a minute for every business) for its own pages and chats.
const receiveCallsPerMinute = 20

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
		ledger += kobo(b["total"])
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
	// TwoAboveKobo: withdrawals of this much or more take two owners (0:
	// one).
	TwoAboveKobo int64 `json:"two_approvers_above_kobo"`
}

// putWithdrawals answers PUT /v1/settings/receive-money/withdrawals
// (owners): the bank account withdrawals go to, its name found by name
// enquiry, and the approval rule. The account is written into the
// withdrawal workflow itself (made from the withdraw-to-bank template the
// first time, a new version after), so a run's starter cannot send the
// money elsewhere and its approver sees where it goes. The business's
// governance holds: with four-eyes on approval rules the rule waits for a
// second person, with four-eyes on publishing so does the new account, and
// a gated prod takes it only when promoted. Until then the account in
// force is the one before.
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
	case len(req.BankCode) < 3 || len(req.BankCode) > 6 || strings.Trim(req.BankCode, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") != "":
		s.fail(w, r, fmt.Errorf("%w: choose your bank", errBadRequest))
		return
	case len(req.AccountNumber) != 10 || strings.Trim(req.AccountNumber, "0123456789") != "":
		s.fail(w, r, fmt.Errorf("%w: an account number is 10 digits", errBadRequest))
		return
	case req.TwoAboveKobo < 0:
		s.fail(w, r, fmt.Errorf("%w: the amount for two owners cannot be below zero", errBadRequest))
		return
	}
	_, c, err := s.receiveOn(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.TwoAboveKobo > 0 {
		var owners int
		if err := s.tx(r, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT count(DISTINCT user_id) FROM memberships WHERE role = 'owner'`).Scan(&owners)
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if owners < 2 {
			s.fail(w, r, fmt.Errorf("%w: your business has one owner, so two owners cannot approve: invite a second owner first, or leave the amount empty", errBadRequest))
			return
		}
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
		// One change at a time: two owners saving at once make one workflow.
		var m receiveMoney
		if err := tx.QueryRow(ctx, `SELECT wallet_id, withdraw_workflow_id FROM receive_money WHERE tenant_id = $1 FOR UPDATE`, p.TenantID).
			Scan(&m.WalletID, &m.WithdrawWorkflow); err != nil {
			return err
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := writeWithdrawPolicyTx(r, tx, p.TenantID, req.TwoAboveKobo, g.FourEyesPolicies, p.Actor()); err != nil {
			return err
		}
		to := withdrawTo{BankCode: req.BankCode, AccountNumber: req.AccountNumber, AccountName: name}
		wf, v, err := s.withdrawWorkflowTx(r, tx, tpl, m.WalletID, m.WithdrawWorkflow, to, g.FourEyesPublish)
		if err != nil {
			return err
		}
		// A sweep that is on pays the new account too, from the same change.
		if err := s.resweepTx(r, tx, m.WalletID, to, g.FourEyesPublish); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE receive_money SET payout_bank_code = $2, payout_bank_name = NULLIF($3, ''), payout_account_number = $4, payout_account_name = $5,
			two_approvers_above_kobo = $6, withdraw_workflow_id = $7, updated_at = now() WHERE tenant_id = $1`,
			p.TenantID, req.BankCode, strings.TrimSpace(req.BankName), req.AccountNumber, name, req.TwoAboveKobo, wf); err != nil {
			return err
		}
		if err := auditTx(r, tx, "settings.receive_money.withdrawals", receiveChannel, map[string]any{"bank_code": req.BankCode,
			"account_number": req.AccountNumber, "account_name": name, "two_approvers_above_kobo": req.TwoAboveKobo, "workflow_id": wf, "version": v}); err != nil {
			return err
		}
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
// otherwise withdraw at all). With four-eyes on approval rules, a changed
// rule waits for a second person, as one written by hand does.
func writeWithdrawPolicyTx(r *http.Request, tx pgx.Tx, tenant uuid.UUID, twoAbove int64, fourEyes bool, by string) error {
	ctx := r.Context()
	no, yes := false, true
	pol := policy.Policy{Constraints: policy.Constraints{ForbidSelfApproval: &no, DistinctApprovers: &yes}, Timeout: "48h", OnTimeout: "reject",
		Description: "Withdrawals to the business's bank (Receive money with Taskiem)"}
	if twoAbove > 0 {
		pol.Rules = append(pol.Rules, policy.Rule{When: fmt.Sprintf("=subject.amount_kobo >= %d", twoAbove), Levels: []policy.Level{{Role: "owner"}, {Role: "owner"}}})
	}
	pol.Rules = append(pol.Rules, policy.Rule{Levels: []policy.Level{{Role: "owner"}}})
	raw, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	if _, err := policy.Parse(raw); err != nil {
		return err
	}
	doc, err := canonical(raw)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy:' || $1))`, withdrawPolicy); err != nil {
		return err
	}
	// The same rule as in force (or already waiting) is not written again.
	var same bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_policies WHERE name = $1 AND state IN ('active', 'pending') AND document = $2::jsonb
		AND version = (SELECT max(version) FROM approval_policies WHERE name = $1))`, withdrawPolicy, doc).Scan(&same); err != nil || same {
		return err
	}
	var v int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM approval_policies WHERE name = $1`, withdrawPolicy).Scan(&v); err != nil {
		return err
	}
	state := "active"
	if fourEyes {
		state = "pending"
	} else if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, withdrawPolicy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO approval_policies (tenant_id, name, version, document, state, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, withdrawPolicy, v, doc, state, by); err != nil {
		return err
	}
	return auditTx(r, tx, "policy.write", fmt.Sprintf("%s/%d", withdrawPolicy, v), map[string]any{"state": state, "via": "receive_money"})
}

// withdrawDefinition is the withdraw-to-bank template made for the
// business: on its Taskiem connection and wallet, its approval under the
// approval rule, and the bank account written in, so a run takes only an
// amount and a reference and its approver sees the account.
func withdrawDefinition(tpl *templates.Template, wallet string, to withdrawTo) ([]byte, error) {
	raw, err := tpl.Instantiate(map[string]any{"approver_role": "owner", "rail_connection": secrets.PlatformName, "funding_account": wallet}, "Withdraw to my bank")
	if err != nil {
		return nil, err
	}
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	fixed := map[string]any{"=trigger.body.bank_code": to.BankCode, "=trigger.body.account_number": to.AccountNumber}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		case string:
			if f, ok := fixed[x]; ok {
				return f
			}
		}
		return v
	}
	walk(def["steps"])
	steps, _ := def["steps"].([]any)
	for _, x := range steps {
		if st, _ := x.(map[string]any); st["type"] == "approval" {
			if cfg, ok := st["config"].(map[string]any); ok {
				cfg["policy"] = withdrawPolicy
			}
		}
	}
	// The run's input is the amount and its reference only.
	if in, ok := def["inputs"].(map[string]any); ok {
		if sc, ok := in["schema"].(map[string]any); ok {
			sc["required"] = []any{"amount_naira", "request_id"}
			if props, ok := sc["properties"].(map[string]any); ok {
				delete(props, "bank_code")
				delete(props, "account_number")
			}
		}
	}
	patched, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	return canonical(patched)
}

// withdrawWorkflowTx makes the workflow that withdraws (or its next
// version, for a new account) and publishes it: or, with four-eyes on
// publishing, asks for it to be published, which a second person approves.
func (s *Server) withdrawWorkflowTx(r *http.Request, tx pgx.Tx, tpl *templates.Template, wallet string, have *uuid.UUID, to withdrawTo, fourEyes bool) (uuid.UUID, int, error) {
	doc, err := withdrawDefinition(tpl, wallet, to)
	if err != nil {
		return uuid.Nil, 0, err
	}
	return s.managedWorkflowTx(r, tx, managedWorkflow{Name: "Withdraw to my bank", Template: tpl.ID, What: "where withdrawals go"}, doc, have, fourEyes)
}

// managedWorkflow is a workflow Taskiem makes and keeps for a business
// (Receive money with Taskiem): its name, the library template it is made
// from ("" for none) and, for a refusal, what changing it changes.
type managedWorkflow struct {
	Name, Template, What string
}

// managedWorkflowTx makes a workflow Taskiem keeps for the business, or
// its next version, and publishes it: or, with four-eyes on publishing,
// asks for it to be published, which a second person approves.
func (s *Server) managedWorkflowTx(r *http.Request, tx pgx.Tx, mw managedWorkflow, doc []byte, have *uuid.UUID, fourEyes bool) (uuid.UUID, int, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var wf uuid.UUID
	var err error
	v := 1
	if have != nil {
		wf = *have
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&v); err != nil {
			return uuid.Nil, 0, err
		}
	} else if wf, err = s.createWorkflowTx(ctx, tx, p, mw.Name); err != nil {
		return uuid.Nil, 0, err
	}
	if err := insertVersionTx(ctx, tx, p.TenantID, wf, v, doc, nil, p.Actor(), ""); err != nil {
		return uuid.Nil, 0, err
	}
	if mw.Template != "" {
		if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET template_id = $3 WHERE workflow_id = $1 AND version = $2`, wf, v, mw.Template); err != nil {
			return uuid.Nil, 0, err
		}
	}
	detail := map[string]any{"name": mw.Name, "version": v, "state": "draft", "via": "receive_money"}
	if mw.Template != "" {
		detail["template"] = mw.Template
	}
	if err := auditTx(r, tx, "workflow.create", wf.String(), detail); err != nil {
		return uuid.Nil, 0, err
	}
	if fourEyes {
		if p.UserID == uuid.Nil {
			return uuid.Nil, 0, fmt.Errorf("%w: with four-eyes publishing, a person changes %s and another approves", errForbidden, mw.What)
		}
		// Only the newest change waits: approving an older one would
		// publish settings (an account) since replaced.
		if _, err := tx.Exec(ctx, `UPDATE publish_requests SET status = 'withdrawn', decided_at = now() WHERE workflow_id = $1 AND version < $2 AND status = 'pending'`, wf, v); err != nil {
			return uuid.Nil, 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, $3, $4)
			ON CONFLICT (workflow_id, version) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL`,
			p.TenantID, wf, v, p.Human()); err != nil {
			return uuid.Nil, 0, err
		}
		return wf, v, auditTx(r, tx, "publish_request.create", fmt.Sprintf("%s/%d", wf, v), map[string]any{"via": "receive_money"})
	}
	var by *uuid.UUID
	if p.UserID != uuid.Nil {
		by = &p.UserID
	}
	probs, _, _, err := s.publishTx(ctx, tx, p.TenantID, wf, v, by, "")
	if len(probs) > 0 {
		msgs := make([]string, len(probs))
		for i, pr := range probs {
			msgs[i] = pr.Message
		}
		return uuid.Nil, 0, fmt.Errorf("%w: %s could not be published: %s", errConflict, mw.Name, strings.Join(msgs, "; "))
	}
	return wf, v, err
}

// withdrawWaiting says what a change to withdrawals still waits for, or
// "": the approval rule or the new account waiting for a second person,
// or a gated prod waiting for the version to be promoted.
func withdrawWaiting(ctx context.Context, tx pgx.Tx, wf *uuid.UUID) (string, error) {
	var waits []string
	var rule bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_policies WHERE name = $1 AND state = 'pending')`, withdrawPolicy).Scan(&rule); err != nil {
		return "", err
	}
	if rule {
		waits = append(waits, "the new approval rule waits for a second person to approve it (Approval policies)")
	}
	w, err := workflowWaiting(ctx, tx, wf, "Withdraw to my bank", "the new bank account")
	if err != nil {
		return "", err
	}
	if w != "" {
		waits = append(waits, w)
	}
	if len(waits) == 0 {
		return "", nil
	}
	return strings.Join(waits, "; ") + ". Until then the account and rule before stay in force.", nil
}

// workflowWaiting says what the latest version of a workflow Taskiem keeps
// waits for before it is in prod (a second person, a promotion), with
// change naming it, or "".
func workflowWaiting(ctx context.Context, tx pgx.Tx, wf *uuid.UUID, name, change string) (string, error) {
	if wf == nil {
		return "", nil
	}
	var latest int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) FROM workflow_versions WHERE workflow_id = $1`, *wf).Scan(&latest); err != nil {
		return "", err
	}
	deployed, err := deployedVersion(ctx, tx, *wf, "prod")
	if err != nil || deployed == latest {
		return "", err
	}
	var asked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM publish_requests WHERE workflow_id = $1 AND version = $2 AND status = 'pending')`, *wf, latest).Scan(&asked); err != nil {
		return "", err
	}
	switch {
	case asked:
		return change + " waits for a second person to approve publishing it (Approvals)", nil
	case deployed == 0:
		return "your prod environment takes only what is promoted to it: promote " + name + " there", nil
	default:
		return change + " is not in prod yet: promote " + name + " there", nil
	}
}

type withdrawReq struct {
	AmountKobo int64 `json:"amount_kobo"`
}

// maxWithdrawKobo is the most one withdrawal moves: the template's
// maximum, ₦1,000,000,000.
const maxWithdrawKobo = 100_000_000_000

// withdrawToBank answers POST /v1/receive-money/withdraw: a run of the
// withdrawal workflow as published in prod, for the amount, to the bank
// account written in it. It waits for the approval rule's approvers
// before anything is paid. The request's Idempotency-Key (or one made
// here) is the payment's key: asking again with it never pays twice.
func (s *Server) withdrawToBank(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req withdrawReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.AmountKobo < 100 || req.AmountKobo > maxWithdrawKobo {
		s.fail(w, r, fmt.Errorf("%w: withdraw between NGN 1 and NGN 1,000,000,000", errBadRequest))
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
		s.fail(w, r, fmt.Errorf("%w: withdrawals are not ready yet: the change to them waits for approval or promotion", errConflict))
		return
	}
	input := map[string]any{"amount_naira": float64(req.AmountKobo) / 100, "request_id": key}
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

// waReceiveCommand answers "my account number" and "balance" in chat for
// a business that receives money with Taskiem (W3, W4); false when the
// text is neither.
func (s *Server) waReceiveCommand(ctx context.Context, c *chat, cmd string) (bool, error) {
	var balance bool
	switch cmd {
	case "my account number", "account number", "my account", "my bank account", "pay me", "my number for payments":
	case "balance", "my balance", "wallet balance", "my wallet":
		balance = true
	default:
		return false, nil
	}
	if !c.p.Can(PermInvoiceRead) {
		return true, c.say(ctx, s, c.t("wa.money.denied", "org", c.tenant.Name))
	}
	var m *receiveMoney
	if err := s.tenantTx(ctx, c.tenant.ID, func(tx pgx.Tx) error {
		var err error
		m, err = loadReceiveMoney(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return true, err
	}
	if m == nil {
		return true, c.say(ctx, s, c.t("wa.receive.off", "link", s.PublicURL+"/settings/payments"))
	}
	if !balance {
		if m.AccountNumber == nil {
			return true, c.say(ctx, s, c.t("wa.receive.no_number", "link", s.PublicURL+"/settings/payments"))
		}
		return true, c.say(ctx, s, c.t("wa.receive.account", "account", *m.AccountNumber, "bank", deref(m.BankName), "name", deref(m.AccountName)))
	}
	available, ok := s.receiveBalance(ctx, c.tenant.ID)
	if !ok {
		return true, c.say(ctx, s, c.t("wa.receive.balance_unknown"))
	}
	return true, c.say(ctx, s, c.t("wa.receive.balance", "amount", invoicing.Naira(available)))
}

// receiveBalance is what the business's Taskiem wallet holds now, in
// naira's kobo; ok is false when it does not receive money with Taskiem or
// iswallet did not answer.
func (s *Server) receiveBalance(ctx context.Context, tenant uuid.UUID) (int64, bool) {
	var m *receiveMoney
	if err := s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		m, err = loadReceiveMoney(ctx, tx, tenant)
		return err
	}); err != nil || m == nil {
		return 0, false
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return 0, false
	}
	conn, ok := connectorByID(reg, receiveConnector)
	if !ok {
		return 0, false
	}
	out, err := s.connectionAction(ctx, tenant, conn, "get_balance", "receive_money.balance", map[string]any{"wallet_id": m.WalletID}, "")
	if err != nil {
		return 0, false
	}
	var available int64
	list, _ := out["balances"].([]any)
	for _, x := range list {
		b, _ := x.(map[string]any)
		if cur, _ := b["currency"].(string); cur == "" || cur == "NGN" {
			available += kobo(b["available"])
		}
	}
	return available, true
}
