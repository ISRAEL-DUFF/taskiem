// Package breet is the Breet connector: crypto deposit addresses with
// optional automatic settlement to a bank, crypto and bank withdrawals,
// and their events. Built from Breet's public documentation and OpenAPI
// description (docs/integrations/breet.md).
//
// Withdrawals carry the engine's key as externalId, and Breet finds a
// withdrawal by it, so an unknown outcome is settled by asking (reconcile)
// rather than by sending again. Breet has no idempotency key: a repeat
// with the same externalId is not documented as refused.
//
// Bank withdrawal amounts: Breet's documentation says both "in NGN or GHS"
// and "in USD". The connector does not choose: the connection records the
// unit Breet confirmed (bank_withdrawal_unit), and the step's currency must
// match it.
package breet

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector; BaseURL defaults to the manifest's.
type Options struct {
	BaseURL string
}

// New returns the Breet connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_assets":         connector.ActionFunc(c.listAssets),
		"get_balances":        connector.ActionFunc(c.getBalances),
		"generate_address":    connector.ActionFunc(c.generateAddress),
		"get_deposit":         connector.ActionFunc(c.getDeposit),
		"list_banks":          connector.ActionFunc(c.listBanks),
		"verify_bank_account": connector.ActionFunc(c.verifyBankAccount),
		"add_bank":            connector.ActionFunc(c.addBank),
		"withdraw_crypto":     connector.ActionFunc(c.withdrawCrypto),
		"withdraw_to_bank":    connector.ActionFunc(c.withdrawToBank),
		"get_withdrawal":      connector.ActionFunc(c.getWithdrawal),
	}}
}

type client struct{ base string }

// apiError is Breet's error envelope. Messages may change; they are shown,
// and matched only where Breet documents no other signal.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("breet %d: %s", e.status, e.message) }

func (e *apiError) says(s string) bool {
	return e != nil && strings.Contains(strings.ToLower(e.message), s)
}

func env(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return "sandbox"
	}
	return "production"
}

func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	id, secret := req.Credentials["app_id"], req.Credentials["app_secret"]
	if id == "" || secret == "" {
		return fmt.Errorf("breet: the connection needs app_id and app_secret: %w", effects.ErrFatal)
	}
	var e struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	headers := map[string]string{"x-app-id": id, "x-app-secret": secret, "X-Breet-Env": env(req)}
	err := connector.DoJSON(ctx, req.HTTP, method, c.base+path, headers, body, &e)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(he.Body, &m)
		ae := &apiError{status: he.Status, message: m.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		return fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return err
	}
	if !e.Success {
		return fmt.Errorf("%w: %w", &apiError{status: 200, message: e.Message}, effects.ErrUnknownOutcome)
	}
	if out != nil && len(e.Data) > 0 && string(e.Data) != "null" {
		if err := json.Unmarshal(e.Data, out); err != nil {
			return fmt.Errorf("breet: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func breetError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func unreadable(err error) error {
	return fmt.Errorf("breet: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func fatal(format string, args ...any) error {
	return fmt.Errorf("breet: "+format+": %w", append(args, effects.ErrFatal)...)
}

func (c *client) listAssets(ctx context.Context, req connector.Request) (connector.Response, error) {
	var assets []struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
		Symbol     string `json:"symbol"`
		Network    string `json:"network"`
		Minimum    any    `json:"minimum"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/trades/assets", nil, &assets); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(assets))
	for i, a := range assets {
		out[i] = map[string]any{"id": a.ID, "identifier": a.Identifier, "name": a.Name, "symbol": a.Symbol, "network": a.Network, "minimum": money.Decimal(a.Minimum)}
	}
	return connector.Response{Output: map[string]any{"assets": out}}, nil
}

// getBalances reads the fiat wallets from the integration's details. The
// same response holds the webhook secret and the owner's contact details;
// nothing else is taken from it.
func (c *client) getBalances(ctx context.Context, req connector.Request) (connector.Response, error) {
	var d struct {
		FiatWallets []struct {
			Currency string `json:"currency"`
			Balance  any    `json:"balance"`
		} `json:"fiatWallets"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/users/fetch-integration", nil, &d); err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := make([]any, len(d.FiatWallets))
	for i, w := range d.FiatWallets {
		out[i] = map[string]any{"currency": strings.ToUpper(w.Currency), "balance": rd.Floor(w.Balance, 2), "balance_exact": money.Decimal(w.Balance)}
	}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: map[string]any{"balances": out}}, nil
}

type wallet struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Label   string `json:"label"`
	Asset   string `json:"asset"`
}

func (w wallet) output(assetID string) map[string]any {
	if w.Asset != "" {
		assetID = w.Asset
	}
	return map[string]any{"wallet_id": w.ID, "address": w.Address, "label": w.Label, "asset_id": assetID}
}

func (c *client) generateAddress(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	asset, label := str(in, "asset_id"), str(in, "label")
	if asset == "" || label == "" {
		return connector.Response{}, fatal("generate_address needs asset_id and label")
	}
	body := map[string]any{"label": label}
	if b, n := str(in, "bank_id"), str(in, "account_number"); b != "" || n != "" {
		if b == "" || n == "" {
			return connector.Response{}, fatal("bank_id and account_number go together")
		}
		body["bankId"], body["accountNumber"] = b, n
		if s := str(in, "narration"); s != "" {
			body["narration"] = s
		}
	}
	if a, ok := in["auto_settlement"].(bool); ok {
		body["autoSettlement"] = a
	}
	var w wallet
	err := c.do(ctx, req, http.MethodPost, "/trades/sell/assets/"+url.PathEscape(asset)+"/generate-address", body, &w)
	if ae := breetError(err); ae != nil && ae.status < 500 && ae.says("already exists") {
		// One address per label and asset: report the existing one.
		found, ferr := c.findWallet(ctx, req, asset, label)
		if ferr != nil {
			return connector.Response{}, ferr
		}
		return connector.Response{Output: found.output(asset)}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	if w.Label == "" {
		w.Label = label
	}
	return connector.Response{Output: w.output(asset)}, nil
}

// findWallet looks for the address of a label and asset. Breet prefixes
// stored labels with the merchant reference ("Partner_B-…_<label>").
func (c *client) findWallet(ctx context.Context, req connector.Request, asset, label string) (wallet, error) {
	for page := 1; page <= 50; page++ {
		var ws []wallet
		if err := c.do(ctx, req, http.MethodGet, "/trades/wallets?"+url.Values{"page": {strconv.Itoa(page)}, "size": {"100"}}.Encode(), nil, &ws); err != nil {
			return wallet{}, err
		}
		for _, w := range ws {
			if w.Asset == asset && (w.Label == label || strings.HasSuffix(w.Label, "_"+label)) {
				return w, nil
			}
		}
		if len(ws) < 100 {
			break
		}
	}
	return wallet{}, fmt.Errorf("breet: Breet says %s already has an address for %s, but it is not in the wallet list: %w", label, asset, effects.ErrUnknownOutcome)
}

func (c *client) getDeposit(ctx context.Context, req connector.Request) (connector.Response, error) {
	var t struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		Address        string `json:"address"`
		AmountReceived any    `json:"amountReceived"`
		CryptoReceived any    `json:"cryptoReceived"`
		Rate           any    `json:"rate"`
		Currency       string `json:"currency"`
		FeeAmount      any    `json:"feeAmount"`
		TxHash         string `json:"txHash"`
		WalletCredited bool   `json:"walletCredited"`
	}
	err := c.do(ctx, req, http.MethodGet, "/trades/transactions/"+url.PathEscape(str(req.Input, "trade_id")), nil, &t)
	if ae := breetError(err); ae != nil && ae.status == http.StatusNotFound {
		return connector.Response{}, fmt.Errorf("%w: %w: %w", err, connector.ErrNotFound, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"trade_id": t.ID, "status": t.Status, "address": t.Address,
		"amount_usd": money.Decimal(t.AmountReceived), "crypto_received": money.Decimal(t.CryptoReceived), "rate": money.Decimal(t.Rate),
		"currency": strings.ToUpper(t.Currency), "fee": money.Decimal(t.FeeAmount), "tx_hash": t.TxHash, "wallet_credited": t.WalletCredited}}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	cur := strings.ToLower(str(req.Input, "currency"))
	if cur == "" {
		cur = "ngn"
	}
	var banks []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/payments/banks?"+url.Values{"currency": {cur}}.Encode(), nil, &banks); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(banks))
	for i, b := range banks {
		out[i] = map[string]any{"id": b.ID, "name": b.Name, "currency": b.Currency}
	}
	return connector.Response{Output: map[string]any{"banks": out}}, nil
}

func bankBody(in map[string]any) map[string]any {
	body := map[string]any{"id": str(in, "bank_id"), "accountNumber": str(in, "account_number")}
	if cur := strings.ToLower(str(in, "currency")); cur != "" {
		body["currency"] = cur
	}
	return body
}

func (c *client) verifyBankAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	var a struct {
		AccountNumber string `json:"accountNumber"`
		AccountName   string `json:"accountName"`
		BankName      string `json:"bankName"`
	}
	if err := c.do(ctx, req, http.MethodPost, "/payments/banks/validate", bankBody(req.Input), &a); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"account_name": a.AccountName, "account_number": a.AccountNumber, "bank_name": a.BankName}}, nil
}

type savedBank struct {
	ID            string `json:"id"`
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	BankID        string `json:"bankId"`
	BankName      string `json:"bankName"`
	Currency      string `json:"currency"`
}

func (b savedBank) output() map[string]any {
	return map[string]any{"saved_bank_id": b.ID, "account_name": b.AccountName, "account_number": b.AccountNumber,
		"bank_name": b.BankName, "currency": strings.ToUpper(b.Currency)}
}

func (c *client) addBank(ctx context.Context, req connector.Request) (connector.Response, error) {
	body := bankBody(req.Input)
	if n := str(req.Input, "narration"); n != "" {
		body["narration"] = n
	}
	var b savedBank
	err := c.do(ctx, req, http.MethodPost, "/payments/banks/add", body, &b)
	if ae := breetError(err); ae != nil && ae.status < 500 && ae.says("already exists") {
		var list []savedBank
		if lerr := c.do(ctx, req, http.MethodGet, "/payments/integration-banks?page=1&size=50", nil, &list); lerr != nil {
			return connector.Response{}, lerr
		}
		for _, s := range list {
			if s.BankID == str(req.Input, "bank_id") && s.AccountNumber == str(req.Input, "account_number") {
				return connector.Response{Output: s.output()}, nil
			}
		}
		return connector.Response{}, fmt.Errorf("breet: %s, but it is not among the saved banks: %w", ae.Error(), effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: b.output()}, nil
}

func (c *client) pin(req connector.Request) (string, error) {
	p := req.Credentials["pin"]
	if p == "" {
		return "", fatal("withdrawals need the connection's pin (set on the Breet dashboard)")
	}
	return p, nil
}

func (c *client) withdrawCrypto(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "external_id")
	if ref == "" {
		return connector.Response{}, fatal("withdraw_crypto needs the engine's external_id")
	}
	pin, err := c.pin(req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"token": str(in, "token"), "walletAddress": str(in, "wallet_address"), "pin": pin, "externalId": ref}
	cents, hasUSD := in["amount_usd"]
	crypto := str(in, "crypto_amount")
	switch {
	case hasUSD && cents != nil && crypto != "":
		return connector.Response{}, fatal("send amount_usd or crypto_amount, not both")
	case hasUSD && cents != nil:
		n, err := money.Minor(cents)
		if err != nil || n < 1 {
			return connector.Response{}, fatal("amount_usd must be a positive whole number of cents, got %v", cents)
		}
		body["amount"] = money.Number(n, 2)
	case crypto != "":
		if _, err := money.Parse(crypto, 8); err != nil {
			return connector.Response{}, fatal("crypto_amount %q: %v", crypto, err)
		}
		body["cryptoAmount"] = json.Number(crypto)
	default:
		return connector.Response{}, fatal("send amount_usd or crypto_amount")
	}
	if n := str(in, "network"); n != "" {
		body["network"] = n
	}
	if f := str(in, "fee_level"); f != "" {
		body["feeLevel"] = f
	}
	return c.withdraw(ctx, req, "/payments/withdraw/address", body, ref)
}

func (c *client) withdrawToBank(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "external_id")
	if ref == "" {
		return connector.Response{}, fatal("withdraw_to_bank needs the engine's external_id")
	}
	cur := strings.ToUpper(str(in, "currency"))
	switch unit := strings.ToLower(strings.TrimSpace(req.Credentials["bank_withdrawal_unit"])); {
	case unit == "":
		return connector.Response{}, fatal("Breet's documentation gives the bank withdrawal amount both in local currency and in USD. " +
			"Confirm the unit with Breet for this account, then set bank_withdrawal_unit (local or usd) on the connection")
	case unit == "local" && cur != "NGN" && cur != "GHS":
		return connector.Response{}, fatal("this connection sends bank withdrawals in local currency (NGN or GHS), not %s", cur)
	case unit == "usd" && cur != "USD":
		return connector.Response{}, fatal("this connection sends bank withdrawals in USD, not %s", cur)
	case unit != "local" && unit != "usd":
		return connector.Response{}, fatal("bank_withdrawal_unit must be local or usd, not %q", unit)
	}
	n, err := money.Minor(in["amount"])
	if err != nil || n < 1 {
		return connector.Response{}, fatal("amount must be a positive whole number of minor units, got %v", in["amount"])
	}
	pin, err := c.pin(req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"amount": money.Number(n, 2), "pin": pin, "externalId": ref}
	if s := str(in, "narration"); s != "" {
		body["narration"] = s
	}
	return c.withdraw(ctx, req, "/payments/withdraw/bank/"+url.PathEscape(str(in, "saved_bank_id")), body, ref)
}

// withdraw submits a withdrawal and reads it back, so the output is the
// withdrawal itself. A refusal as a duplicate means an earlier attempt got
// through: that withdrawal is reported.
func (c *client) withdraw(ctx context.Context, req connector.Request, path string, body map[string]any, ref string) (connector.Response, error) {
	var created struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, req, http.MethodPost, path, body, &created)
	if ae := breetError(err); ae != nil && ae.status < 500 && ae.says("duplicate withdrawal") {
		out, ferr := c.fetchWithdrawal(ctx, req, ref)
		if errors.Is(ferr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("breet: %s, and no withdrawal has reference %s: %w", ae.Error(), ref, effects.ErrFatal)
		}
		if ferr != nil {
			return connector.Response{}, ferr
		}
		return settled(out)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := c.fetchWithdrawal(ctx, req, created.ID)
	if err != nil {
		// Accepted and debited; only the read-back failed, which is not the
		// withdrawal's outcome.
		return connector.Response{Output: map[string]any{"withdrawal_id": created.ID, "external_id": ref, "status": "pending", //nolint:nilerr // see above
			"amount": int64(0), "fee": int64(0), "currency": "", "crypto_amount": "", "tx_hash": "", "reason": ""}}, nil
	}
	return settled(out)
}

// settled fails the step for a withdrawal Breet rejected or reversed: the
// balance was refunded and nothing reached the destination.
func settled(out map[string]any) (connector.Response, error) {
	switch out["status"] {
	case "rejected", "reversed":
		return connector.Response{}, fmt.Errorf("breet: withdrawal %s was %s (refunded): %s: %w", out["withdrawal_id"], out["status"], out["reason"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) fetchWithdrawal(ctx context.Context, req connector.Request, id string) (map[string]any, error) {
	var w struct {
		ID           string `json:"id"`
		ExternalID   string `json:"externalId"`
		Status       string `json:"status"`
		Amount       any    `json:"amount"`
		Fee          any    `json:"fee"`
		Currency     string `json:"currency"`
		CryptoAmount any    `json:"cryptoAmount"`
		TxHash       string `json:"txHash"`
		Reason       string `json:"reason"`
	}
	err := c.do(ctx, req, http.MethodGet, "/payments/withdrawal/"+url.PathEscape(id), nil, &w)
	if ae := breetError(err); ae != nil && ae.status == http.StatusNotFound {
		return nil, fmt.Errorf("breet: no withdrawal %s: %w", id, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var rd money.Reader
	out := map[string]any{"withdrawal_id": w.ID, "external_id": w.ExternalID, "status": w.Status, "amount": rd.Minor(w.Amount, 2),
		"fee": rd.Ceil(w.Fee, 2), "currency": strings.ToUpper(w.Currency), "crypto_amount": money.Decimal(w.CryptoAmount),
		"tx_hash": w.TxHash, "reason": w.Reason}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// getWithdrawal is also the reconcile action: it is given the engine's key
// as external_id.
func (c *client) getWithdrawal(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "withdrawal_id")
	if id == "" {
		id = str(req.Input, "external_id")
	}
	if id == "" {
		return connector.Response{}, fatal("get_withdrawal needs withdrawal_id or external_id")
	}
	out, err := c.fetchWithdrawal(ctx, req, id)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}
