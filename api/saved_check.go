package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The name check for saved accounts, the bank list, and the step to
// connect a payments provider (saved objects S0; build plan §1: built once
// here, used again by payments from P1).
//
// A business's account is checked through its payments connection: the
// Taskiem wallet (iswallet/taskiem, opened from Settings > Receive money
// with Taskiem) first, else the first of its own that can name an account
// (resolve_account). The bank is the one chosen from that connection's
// list; a bank chosen from another list, or by name alone before any
// connection, is found in the list by name. With no connection the
// account is saved "Name not checked yet", and checked once one is
// (POST /v1/saved/check). A person's own accounts are not checked in S0:
// a person has no payments connection of their own, and a business's is
// not used for them.

// savedCheckResult is the bank's name for an account, and through which
// connection it was asked.
type savedCheckResult struct {
	AccountName string
	Via         string
}

// railOrder is the order connections are tried in to check a name: the
// Taskiem wallet, then the providers the connect step offers.
var railOrder = []string{"iswallet/" + secrets.PlatformName, "iswallet", "paystack", "flutterwave", "moniepoint", "anchor"}

// connectProviders are the providers the connect step offers besides the
// Taskiem wallet ("I already have an account with…").
var connectProviders = []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}{{"paystack", "Paystack"}, {"flutterwave", "Flutterwave"}, {"moniepoint", "Moniepoint"}, {"anchor", "Anchor"}}

func railRank(rl invoicing.Rail) int {
	for i, k := range railOrder {
		if rl.Channel() == k || rl.Connector == k {
			return i
		}
	}
	return len(railOrder)
}

// savedRail is the connection a business's names are checked through; nil
// when it has none.
func (s *Server) savedRail(ctx context.Context, tenant uuid.UUID) (*invoicing.Rail, error) {
	rails := s.accountRails()
	if rails == nil {
		return nil, nil
	}
	list, err := rails.Capable(ctx, tenant, "resolve_account")
	if err != nil || len(list) == 0 {
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b invoicing.Rail) int { return railRank(a) - railRank(b) })
	return &list[0], nil
}

// railTitle is a connection as the owner knows it.
func railTitle(rl invoicing.Rail) string {
	if rl.Channel() == "iswallet/"+secrets.PlatformName {
		return "your Taskiem wallet"
	}
	if rl.Name != "" {
		return rl.Name
	}
	return rl.Connector
}

// bankItem is one bank in a list: its code in that list ("" in the list
// offered by name) and its name.
type bankItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Likely: an account number asked about can be at this bank (its
	// check digit fits), or the business pays it most (payments' list).
	Likely bool `json:"likely,omitempty"`
}

// savedBanks are the banks the connection rl itself (its connector and
// its connection, never another of the same name) pays and names accounts
// at: its list_banks capability when it maps one, else its connector's own
// list_banks action. BillBanks stands in for it in tests.
func (s *Server) savedBanks(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail) ([]map[string]any, error) {
	if s.BillBanks != nil {
		return s.BillBanks(ctx, tenant, rl.Connection)
	}
	var out map[string]any
	var err error
	if rl.Maps("list_banks") {
		out, err = s.accountRails().Call(ctx, tenant, rl, "list_banks", map[string]any{}, effects.KeyInput{})
	} else {
		reg, rerr := s.Registry.For(ctx, tenant.String())
		if rerr != nil {
			return nil, rerr
		}
		c, ok := connectorByID(reg, rl.Connector)
		if !ok || c.Actions["list_banks"] == nil {
			return nil, fmt.Errorf("%w: %s does not list its banks", errConflict, railTitle(rl))
		}
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "saved.banks"})
		creds, cerr := s.Vault.Credentials(use, tenant, "prod", rl.Connector, rl.Connection)
		if cerr != nil {
			return nil, cerr
		}
		out, err = s.runAction(ctx, tenant, c, creds, "list_banks", "saved.banks", map[string]any{}, "")
	}
	if err != nil {
		return nil, err
	}
	list, _ := out["banks"].([]any)
	banks := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			banks = append(banks, m)
		}
	}
	return banks, nil
}

// byNameForNow keeps a bank by name when the connection's list cannot be
// read just now: found in the list when the account is checked. The error
// is logged for the operators.
func (s *Server) byNameForNow(rl invoicing.Rail, acct saved.BankAccount, err error) (saved.BankAccount, error) {
	if s.Logger != nil {
		s.Logger.Warn("saved: bank list not read", "via", rl.Channel(), "err", plainConnectorError(err))
	}
	acct.BankCode, acct.CodeFrom = "", ""
	return acct, nil
}

// errBankCode is a bank code the payments connection's list does not have.
var errBankCode = customerSendErr{http.StatusBadRequest, "Choose the bank again from the list."}

// settleBank makes a business's account name its bank as its payments
// connection does, so the bank shown is always the bank its code is: a
// code from the connection's own list (code_from) must be in that list, and
// the bank's name is taken from it; a code from any other list (or with no
// connection) is dropped, the bank kept by name and found in the list when
// the account is checked. A person's own accounts keep their bank by name
// only. It answers errBankCode for a code the list does not have.
func (s *Server) settleBank(ctx context.Context, o savedOwner, acct saved.BankAccount) (saved.BankAccount, error) {
	if acct.BankCode == "" {
		return acct, nil
	}
	var rl *invoicing.Rail
	if !o.mine() {
		var err error
		if rl, err = s.savedRail(ctx, o.tenant); err != nil {
			return acct, err
		}
	}
	if rl == nil || acct.CodeFrom != rl.Channel() {
		acct.BankCode, acct.CodeFrom = "", ""
		return acct, nil
	}
	banks, err := s.savedBanks(ctx, o.tenant, *rl)
	if err != nil {
		return s.byNameForNow(*rl, acct, err)
	}
	for _, b := range bankItems(banks) {
		if b.Code == acct.BankCode {
			acct.BankName = b.Name
			if len([]rune(acct.BankName)) > saved.MaxBankName {
				acct.BankName = string([]rune(acct.BankName)[:saved.MaxBankName])
			}
			return acct, nil
		}
	}
	return acct, errBankCode
}

// bankItems are a connection's banks as [{code, name}], by name.
func bankItems(banks []map[string]any) []bankItem {
	str := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				return strings.TrimSpace(fmt.Sprint(v))
			}
		}
		return ""
	}
	out := make([]bankItem, 0, len(banks))
	for _, b := range banks {
		it := bankItem{Code: str(b, "code", "bank_code", "bankCode", "nip_code", "nipCode"), Name: str(b, "name", "bank_name", "bankName")}
		if it.Code != "" && it.Name != "" {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b bankItem) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// builtInBanks are the banks offered by name alone.
func builtInBanks() []bankItem {
	out := make([]bankItem, len(saved.Banks))
	for i, n := range saved.Banks {
		out[i] = bankItem{Name: n}
	}
	return out
}

// savedProblem is why an account's name was not checked, in plain words.
// A lasting one (the connection does not list the bank by that name; the
// bank has no such account) stays until the account changes, and the
// account is not asked about again; any other is tried again later.
type savedProblem struct {
	Words   string
	Lasting bool
	// Limited: not asked at all, the business having checked too many
	// accounts in the last minute.
	Limited bool
}

// checkSavedAccount asks the business's payments connection for the bank's
// name for acct. It answers nil, with no problem, when the business has no
// connection; nil with a problem when it could not check; and an error,
// refusing the account, when the bank has no such account.
func (s *Server) checkSavedAccount(ctx context.Context, tenant uuid.UUID, acct saved.BankAccount) (*savedCheckResult, savedProblem, error) {
	rl, err := s.savedRail(ctx, tenant)
	if err != nil || rl == nil {
		return nil, savedProblem{}, err
	}
	return s.checkWithRail(ctx, tenant, *rl, acct)
}

func (s *Server) checkWithRail(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail, acct saved.BankAccount) (*savedCheckResult, savedProblem, error) {
	later := savedProblem{Words: fmt.Sprintf("%s did not check the name just now; it is checked again later.", upperFirst(railTitle(rl)))}
	if !s.limiter("name-check:"+tenant.String(), time.Minute/30, 30).Allow() {
		limited := later
		limited.Limited = true
		return nil, limited, nil
	}
	code := acct.BankCode
	if code == "" || acct.CodeFrom != rl.Channel() {
		banks, err := s.savedBanks(ctx, tenant, rl)
		if err != nil {
			return s.checkLater(rl, acct, later, err)
		}
		var ok bool
		if code, ok = links.BankCode(banks, acct.BankName); !ok {
			return nil, savedProblem{Words: fmt.Sprintf("%s does not list %s by that name: choose the bank again from its list to check the name.", upperFirst(railTitle(rl)), acct.BankName), Lasting: true}, nil
		}
	}
	out, err := s.accountRails().Call(ctx, tenant, rl, "resolve_account", map[string]any{"account_number": acct.AccountNumber, "bank_code": code}, effects.KeyInput{})
	if errors.Is(err, effects.ErrFatal) {
		return nil, savedProblem{}, customerSendErr{http.StatusBadRequest, fmt.Sprintf("%s has no account %s. Check the number and the bank.", acct.BankName, saved.Masked(acct.AccountNumber))}
	}
	if err != nil {
		return s.checkLater(rl, acct, later, err)
	}
	name, _ := out["account_name"].(string)
	if name = strings.Join(strings.Fields(name), " "); name == "" {
		return nil, later, nil
	}
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	return &savedCheckResult{AccountName: name, Via: rl.Channel()}, savedProblem{}, nil
}

// checkLater is a check the connection could not make just now: the
// account is kept unchecked, with why in plain words, and the connection's
// own error is logged for the operators (never shown: it may name the
// provider's internals), with the account number masked in it.
func (s *Server) checkLater(rl invoicing.Rail, acct saved.BankAccount, why savedProblem, err error) (*savedCheckResult, savedProblem, error) {
	if s.Logger != nil {
		msg := plainConnectorError(err)
		if acct.AccountNumber != "" {
			msg = strings.ReplaceAll(msg, acct.AccountNumber, saved.Masked(acct.AccountNumber))
		}
		s.Logger.Warn("saved: name check not made", "via", rl.Channel(), "err", msg)
	}
	return nil, why, nil
}

// --- the bank list and the name check ---

// listSavedBanks is GET /v1/banks: the banks to choose an account's bank
// from. With a payments connection, its own list, each with its code
// (from: the connection, to send back as code_from); without one, or when
// it does not answer, the banks by name alone (from: "").
func (s *Server) listSavedBanks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := principalFrom(ctx).TenantID
	rl, err := s.savedRail(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rl != nil {
		if banks, err := s.savedBanks(ctx, tenant, *rl); err == nil {
			if items := bankItems(banks); len(items) > 0 {
				writeJSON(w, http.StatusOK, map[string]any{"banks": items, "from": rl.Channel()})
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"banks": builtInBanks(), "from": ""})
}

// listMyBanks is GET /v1/me/banks: the banks by name, for a person's own
// accounts.
func (s *Server) listMyBanks(w http.ResponseWriter, r *http.Request) {
	if _, err := personOf(r); err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"banks": builtInBanks(), "from": ""})
}

// nameCheck is POST /v1/name-check {bank_name, bank_code?, code_from?,
// account_number}: the bank's name for the account, through the business's
// payments connection, before it is saved. 409 {no_provider} without one;
// 400 when the bank has no such account; 502 when it could not check just
// now.
func (s *Server) nameCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in savedIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.BankName == nil || in.AccountNumber == nil {
		writeErr(w, http.StatusBadRequest, "Give the bank and the 10-digit account number.")
		return
	}
	acct, err := in.account(saved.BankAccount{})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	tenant := principalFrom(ctx).TenantID
	if acct, err = s.settleBank(ctx, savedOwner{tenant: tenant}, acct); err != nil {
		s.savedFail(w, r, err)
		return
	}
	rl, err := s.savedRail(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rl == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Connect a payments account to check names with the bank.", "no_provider": true})
		return
	}
	res, problem, err := s.checkWithRail(ctx, tenant, *rl, acct)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	if res == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": problem.Words, "code": "provider"})
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "saved.name_check", res.Via, map[string]any{"bank": acct.BankName, "account_last4": saved.Last4(acct.AccountNumber)})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_name": res.AccountName, "via": res.Via})
}

// checkAllSaved is POST /v1/saved/check: the business's accounts not
// checked yet, checked now (up to 25 at a time), once it has a payments
// connection. An account that will not be checked until it changes (the
// bank does not know it, or the connection does not list its bank by that
// name) keeps why (check.problem) and is not asked about again. Each
// result is kept only if the account was not changed while it was asked.
func (s *Server) checkAllSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := savedOwnerOf(r, false)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	rl, err := s.savedRail(ctx, o.tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rl == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Connect a payments account to check names with the bank.", "no_provider": true})
		return
	}
	type todo struct {
		id   uuid.UUID
		acct saved.BankAccount
		at   time.Time
	}
	var list []todo
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE tenant_id = $1 AND archived_at IS NULL
			AND kind = $2 AND check_state = $3 AND check_problem IS NULL ORDER BY created_at LIMIT 25`, o.tenant, saved.KindBankAccount, saved.Unchecked)
		if err != nil {
			return err
		}
		xs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		for _, x := range xs {
			a := saved.FromDetails(x.Details)
			if a.AccountNumber, err = s.openSaved(ctx, tx, o, x.ID, x.Sealed); err != nil {
				return err
			}
			list = append(list, todo{x.ID, a, x.UpdatedAt})
		}
		return nil
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	type problem struct {
		ID      uuid.UUID `json:"id"`
		Problem string    `json:"problem"`
	}
	problems := []problem{}
	checked := 0
	for _, t := range list {
		res, why, err := s.checkWithRail(ctx, o.tenant, *rl, t.acct)
		var se customerSendErr
		if errors.As(err, &se) {
			why, err = savedProblem{Words: se.msg, Lasting: true}, nil
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if res == nil {
			problems = append(problems, problem{t.id, why.Words})
			if !why.Lasting {
				continue
			}
		}
		// Kept only for the account as it was asked about: one changed
		// meanwhile (updated_at) is checked again as itself.
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{o.tenant}, func(tx pgx.Tx) error {
			if res == nil {
				_, err := tx.Exec(ctx, `UPDATE saved_objects SET check_problem = $3
					WHERE tenant_id = $1 AND id = $2 AND archived_at IS NULL AND check_state = 'unchecked' AND updated_at = $4`,
					o.tenant, t.id, why.Words, t.at)
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE saved_objects SET check_state = $3, checked_name = $4, checked_via = $5, checked_at = now(), check_problem = NULL, updated_at = now()
				WHERE tenant_id = $1 AND id = $2 AND archived_at IS NULL AND check_state = 'unchecked' AND updated_at = $6`,
				o.tenant, t.id, saved.Checked, res.AccountName, res.Via, t.at)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			checked++
			return s.savedEvent(r, tx, o, t.id, "checked", map[string]any{"via": res.Via})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var left int
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE tenant_id = $1 AND archived_at IS NULL AND kind = $2 AND check_state = $3`,
			o.tenant, saved.KindBankAccount, saved.Unchecked).Scan(&left)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checked": checked, "unchecked": left, "problems": problems, "via": rl.Channel()})
}

// --- the step to connect a payments provider ---

// savedProvider is GET /v1/saved/provider: what the business checks names
// through, and, for an owner of a business with none, the step to connect
// one (build plan S0): Open a Taskiem wallet first (POST
// /v1/settings/receive-money, with an email and the director's BVN), or I
// already have an account with… one of the providers (Connections). Only
// an owner is offered the step.
func (s *Server) savedProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if p.TenantID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "Open a business to see its payments account.")
		return
	}
	rl, err := s.savedRail(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	owner := slices.Contains(p.Roles, "owner")
	out := map[string]any{"connected": nil, "owner": owner}
	if rl != nil {
		out["connected"] = map[string]any{"channel": rl.Channel(), "name": upperFirst(railTitle(*rl)), "wallet": rl.Channel() == "iswallet/"+secrets.PlatformName}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if !owner {
		writeJSON(w, http.StatusOK, out)
		return
	}
	wallet := map[string]any{"available": s.receiveMode() != "", "plan": true}
	if s.Billing != nil {
		var fe *billing.FeatureError
		if err := s.Billing.Require(ctx, p.TenantID, billing.FeatureReceiveMoney); errors.As(err, &fe) {
			wallet["plan"], wallet["plan_note"] = false, fe.Error()
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	out["wallet"], out["providers"] = wallet, connectProviders
	out["words"] = "To check the bank's name for an account, connect a payments account. Open a Taskiem wallet with your email and the director's BVN, or connect an account you already have."
	writeJSON(w, http.StatusOK, out)
}
