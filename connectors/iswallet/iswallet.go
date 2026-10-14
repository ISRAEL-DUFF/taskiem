// Package iswallet is the iswallet connector: transfers between wallets,
// bank payouts, balances, and wallet events. Behaviour follows iswallet
// engineering's answers of 5 October 2026 (docs/integrations/iswallet.md).
//
// Duplicate protection on iswallet has two layers: for 24 hours a repeated
// Idempotency-Key replays the original response; after that the repeat is
// refused by a constraint, but reported as a 500. So this connector never
// resends a key more than ReplayWindow after it was first sent: it parks
// the step for an operator instead (effects.ErrIndeterminate).
package iswallet

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// ReplayWindow is how long after a key's first use a resend is still
// answered from iswallet's replay cache (24 hours), less a safety margin.
const ReplayWindow = 23 * time.Hour

// Options configure the connector; BaseURL defaults to the manifest's (sandbox).
type Options struct {
	BaseURL string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// New returns the iswallet connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/"), now: o.Now}
	if c.now == nil {
		c.now = time.Now
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":  connector.ActionFunc(c.getBalance),
		"transfer":     connector.ActionFunc(c.transfer),
		"payout":       connector.ActionFunc(c.payout),
		"get_payout":   connector.ActionFunc(c.getPayout),
		"name_enquiry": connector.ActionFunc(c.nameEnquiry),
		"list_banks":   connector.ActionFunc(c.listBanks),
	}, CheckCredentials: CheckCredentials}
}

// The allow-list's error codes. Both refusals are fatal and happen before
// any request leaves Taskiem.
var (
	// ErrWalletNotAllowed: the call names a wallet (or iswallet returned
	// one) outside the connection's wallet_ids.
	ErrWalletNotAllowed = errors.New("WALLET_NOT_ALLOWED")
	// ErrAllowListMissing: the connection says its key is a shared
	// client's but lists no wallets, so nothing that touches a wallet runs.
	ErrAllowListMissing = errors.New("WALLET_ALLOW_LIST_MISSING")
)

// CheckCredentials refuses a shared client's connection without wallets,
// and a shared_client value other than true or false.
func CheckCredentials(creds map[string]string) error {
	switch creds["shared_client"] {
	case "", "false":
		return nil
	case "true":
		if len(connector.ListValues(creds["wallet_ids"])) == 0 {
			return fmt.Errorf("iswallet: a shared client's key reaches every customer's wallets; list this customer's wallets in wallet_ids (%w)", ErrAllowListMissing)
		}
		return nil
	}
	return fmt.Errorf("iswallet: shared_client must be true or false, not %q", creds["shared_client"])
}

// allowWallet checks a wallet against the connection's allow-list (decision
// 0031). A connection without one may use any wallet its key reaches,
// unless it is marked as a shared client's.
func allowWallet(req connector.Request, field, wallet string) error {
	list := connector.ListValues(req.Credentials["wallet_ids"])
	if len(list) == 0 {
		if req.Credentials["shared_client"] == "true" {
			return fmt.Errorf("iswallet %w: this connection is a shared client's and lists no wallet_ids, so it may use no wallet; nothing was sent: %w", ErrAllowListMissing, effects.ErrFatal)
		}
		return nil
	}
	if wallet == "" || !slices.Contains(list, wallet) {
		return fmt.Errorf("iswallet %w: %s %q is not one of this connection's wallet_ids; nothing was sent: %w", ErrWalletNotAllowed, field, wallet, effects.ErrFatal)
	}
	return nil
}

type client struct {
	base string
	now  func() time.Time
}

// apiError is iswallet's error envelope.
type apiError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// Codes after which nothing happened and the same request may be sent again.
var retryable = map[string]bool{"RATE_LIMITED": true, "CUSTODY_UNAVAILABLE": true, "SUBACCOUNT_SYNCING": true, "LIQUIDITY_EXHAUSTED": true}

// do sends one request and classifies failures for the engine (§B4).
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	headers := map[string]string{"Authorization": "Bearer " + req.Credentials["api_key"]}
	write := method == http.MethodPost && req.IdempotencyKey != ""
	if write {
		headers["Idempotency-Key"] = req.IdempotencyKey
		if req.Attempt > 1 && !req.KeyFirstSent.IsZero() && c.now().Sub(req.KeyFirstSent) > ReplayWindow {
			return fmt.Errorf("iswallet: this request was first sent %s ago; past iswallet's 24-hour replay window a resend cannot tell whether it already happened. Check the wallet's ledger, then resolve the step: %w",
				c.now().Sub(req.KeyFirstSent).Round(time.Minute), effects.ErrIndeterminate)
		}
	}
	err := connector.DoJSON(ctx, req.HTTP, method, c.base+path, headers, body, out)
	var he *connector.HTTPError
	if err == nil || !errors.As(err, &he) {
		return err // success, or a transport error DoJSON already classified
	}
	var ae apiError
	_ = json.Unmarshal(he.Body, &ae)
	code, msg := ae.Error.Code, ae.Error.Message
	desc := fmt.Sprintf("iswallet %d %s: %s (request %s)", he.Status, code, msg, ae.Error.RequestID)
	switch {
	case retryable[code] || he.Status == http.StatusTooManyRequests || he.Status == http.StatusServiceUnavailable:
		return fmt.Errorf("%s: %w", desc, effects.ErrRetryable)
	case code == "IDEMPOTENCY_KEY_REUSED":
		// This key was already used for a request: the payment it carried
		// may have happened. Only a ledger check can say.
		return fmt.Errorf("%s: %w", desc, effects.ErrIndeterminate)
	case he.Status >= 500:
		if write && !req.KeyFirstSent.IsZero() && c.now().Sub(req.KeyFirstSent) > ReplayWindow {
			// After 24 hours a 500 on a repeated key means "already done".
			return fmt.Errorf("%s: %w", desc, effects.ErrIndeterminate)
		}
		return fmt.Errorf("%s: %w", desc, effects.ErrUnknownOutcome)
	case he.Status == http.StatusNotFound && (code == "OUTFLOW_NOT_FOUND" || code == "WALLET_NOT_FOUND"):
		return fmt.Errorf("%s: %w: %w", desc, connector.ErrNotFound, effects.ErrFatal)
	}
	return fmt.Errorf("%s: %w", desc, effects.ErrFatal)
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return s
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		WalletID string `json:"wallet_id"`
		Balances []struct {
			Currency  string `json:"currency"`
			Total     int64  `json:"total"`
			Available int64  `json:"available"`
			Pending   int64  `json:"pending"`
			Scale     int    `json:"scale"`
		} `json:"balances"`
	}
	if err := allowWallet(req, "wallet_id", str(req.Input, "wallet_id")); err != nil {
		return connector.Response{}, err
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/wallets/"+url.PathEscape(str(req.Input, "wallet_id"))+"/balance", nil, &r); err != nil {
		return connector.Response{}, err
	}
	bs := make([]any, len(r.Balances))
	for i, b := range r.Balances {
		bs[i] = map[string]any{"currency": b.Currency, "total": b.Total, "available": b.Available, "pending": b.Pending, "scale": b.Scale}
	}
	return connector.Response{Output: map[string]any{"wallet_id": r.WalletID, "balances": bs}}, nil
}

func currency(in map[string]any) string {
	if c := str(in, "currency"); c != "" {
		return c
	}
	return "NGN"
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := allowWallet(req, "from_wallet_id", str(in, "from_wallet_id")); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"from_wallet_id": in["from_wallet_id"], "to_wallet_id": in["to_wallet_id"], "amount": in["amount"], "currency": currency(in)}
	if n := str(in, "narration"); n != "" {
		body["narration"] = n
	}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/transfers", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: pick(r, "transfer_id", "txn_id", "status", "amount", "currency", "completed_at")}, nil
}

func (c *client) payout(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := allowWallet(req, "wallet_id", str(in, "wallet_id")); err != nil {
		return connector.Response{}, err
	}
	if cur := currency(in); cur != "NGN" {
		return connector.Response{}, fmt.Errorf("iswallet pays out to banks in NGN only, not %s: %w", cur, effects.ErrFatal)
	}
	// Never send fee_amount: iswallet would treat it as an assertion.
	body := map[string]any{
		"wallet_id": in["wallet_id"], "amount": in["amount"], "currency": "NGN",
		"destination": map[string]any{"bank_code": in["bank_code"], "account_number": in["account_number"], "account_name": in["account_name"]},
	}
	if n := str(in, "narration"); n != "" {
		body["narration"] = n
	}
	if m, ok := in["metadata"].(map[string]any); ok {
		body["metadata"] = m
	}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/outflows", body, &r); err != nil {
		return connector.Response{}, err
	}
	out := pick(r, "outflow_id", "status", "amount", "fee", "net_amount", "provider_reference")
	out["idempotency_key"] = req.IdempotencyKey
	return connector.Response{Output: out}, nil
}

func (c *client) getPayout(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r map[string]any
	// A shared client's connection without wallets reads no payout either.
	if err := allowWallet(req, "wallet_id", ""); errors.Is(err, ErrAllowListMissing) {
		return connector.Response{}, err
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/outflows/"+url.PathEscape(str(req.Input, "outflow_id")), nil, &r); err != nil {
		return connector.Response{}, err
	}
	// An outflow id names any payout of the client: one debited from a
	// wallet outside the allow-list is not this connection's to read.
	if len(connector.ListValues(req.Credentials["wallet_ids"])) > 0 {
		if w, ok := r["wallet_id"].(string); ok && !slices.Contains(connector.ListValues(req.Credentials["wallet_ids"]), w) {
			return connector.Response{}, fmt.Errorf("iswallet %w: payout %s was debited from wallet %q, which is not one of this connection's wallet_ids; its details are withheld: %w",
				ErrWalletNotAllowed, str(req.Input, "outflow_id"), w, effects.ErrFatal)
		}
	}
	return connector.Response{Output: pick(r, "outflow_id", "wallet_id", "status", "amount", "fee", "provider_reference", "confirmed_at")}, nil
}

func (c *client) nameEnquiry(ctx context.Context, req connector.Request) (connector.Response, error) {
	body := map[string]any{"bank_code": req.Input["bank_code"], "account_number": req.Input["account_number"]}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/name-enquiry", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: pick(r, "account_name", "account_number", "bank_code")}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Banks []any `json:"banks"`
		Data  []any `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/banks", nil, &r); err != nil {
		return connector.Response{}, err
	}
	banks := r.Banks
	if banks == nil {
		banks = r.Data
	}
	if banks == nil {
		banks = []any{}
	}
	return connector.Response{Output: map[string]any{"banks": banks}}, nil
}

// pick keeps the named fields that are present; numbers stay JSON numbers.
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
