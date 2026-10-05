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
	}}
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
	if err := c.do(ctx, req, http.MethodGet, "/v1/outflows/"+url.PathEscape(str(req.Input, "outflow_id")), nil, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: pick(r, "outflow_id", "status", "amount", "fee", "provider_reference", "confirmed_at")}, nil
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
