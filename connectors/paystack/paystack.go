// Package paystack is the first-party Paystack connector (spec 6.5).
package paystack

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector; BaseURL defaults to the manifest's.
type Options struct {
	BaseURL string
}

// New returns the Paystack connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	base := strings.TrimRight(m.BaseURL, "/")
	c := &client{base: base}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"check_balance":   connector.ActionFunc(c.checkBalance),
		"transfer":        connector.ActionFunc(c.transfer),
		"verify_transfer": connector.ActionFunc(c.verifyTransfer),
		"verify_charge":   connector.ActionFunc(c.verifyCharge),
	}}
}

type client struct{ base string }

// envelope is Paystack's response wrapper.
type envelope struct {
	Status  bool           `json:"status"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func (c *client) do(ctx context.Context, req connector.Request, method, path string, body any, out any) error {
	key := req.Credentials["secret_key"]
	if key == "" {
		return fmt.Errorf("missing secret_key: %w", effects.ErrFatal)
	}
	return connector.DoJSON(ctx, req.HTTP, method, c.base+path, map[string]string{"Authorization": "Bearer " + key}, body, out)
}

func (c *client) checkBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/balance", nil, &env); err != nil {
		return connector.Response{}, err
	}
	balances := make([]any, len(env.Data))
	for i, b := range env.Data {
		balances[i] = map[string]any{"currency": b["currency"], "balance": b["balance"]}
	}
	return connector.Response{Output: map[string]any{"balances": balances}}, nil
}

func transferOutput(d map[string]any) map[string]any {
	return map[string]any{
		"transfer_code": d["transfer_code"], "reference": d["reference"], "status": d["status"],
		"amount": d["amount"], "currency": d["currency"],
	}
}

// transfer sends money. The worker puts the derived idempotency key in
// "reference"; Paystack refuses or returns the existing transfer for a reused
// reference, and either way the result is the one transfer for that key.
func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, _ := in["reference"].(string)
	if ref == "" {
		return connector.Response{}, fmt.Errorf("transfer needs a reference: %w", effects.ErrFatal)
	}
	body := map[string]any{
		"source": "balance", "amount": in["amount"], "recipient": in["recipient"], "reference": ref,
		"currency": orDefault(in["currency"], "NGN"),
	}
	if r, ok := in["reason"]; ok {
		body["reason"] = r
	}
	var env envelope
	err := c.do(ctx, req, http.MethodPost, "/transfer", body, &env)
	if err != nil && connector.StatusOf(err) == http.StatusBadRequest && strings.Contains(strings.ToLower(errBody(err)), "duplicate") {
		// The transfer already exists under this reference: report it.
		return c.verifyTransfer(ctx, connector.Request{Input: map[string]any{"reference": ref}, Credentials: req.Credentials, HTTP: req.HTTP})
	}
	if err != nil {
		return connector.Response{}, err
	}
	if !env.Status {
		return connector.Response{}, fmt.Errorf("paystack: %s: %w", env.Message, effects.ErrFatal)
	}
	return connector.Response{Output: transferOutput(env.Data)}, nil
}

// verifyTransfer is also the reconcile action for transfer.
func (c *client) verifyTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref, _ := req.Input["reference"].(string)
	var env envelope
	err := c.do(ctx, req, http.MethodGet, "/transfer/verify/"+url.PathEscape(ref), nil, &env)
	if isNotFound(err) {
		return connector.Response{}, connector.ErrNotFound
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: transferOutput(env.Data)}, nil
}

func (c *client) verifyCharge(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref, _ := req.Input["reference"].(string)
	var env envelope
	err := c.do(ctx, req, http.MethodGet, "/transaction/verify/"+url.PathEscape(ref), nil, &env)
	if isNotFound(err) {
		return connector.Response{Output: map[string]any{"reference": ref, "status": "not_found"}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	d := env.Data
	return connector.Response{Output: map[string]any{"reference": d["reference"], "amount": d["amount"], "status": d["status"], "currency": d["currency"]}}, nil
}

func isNotFound(err error) bool {
	st := connector.StatusOf(err)
	return st == http.StatusNotFound || (st == http.StatusBadRequest && strings.Contains(strings.ToLower(errBody(err)), "not found"))
}

func errBody(err error) string {
	var he *connector.HTTPError
	if errors.As(err, &he) {
		return string(he.Body)
	}
	return ""
}

func orDefault(v any, d string) any {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return d
}
