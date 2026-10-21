// Package paystack is the first-party Paystack connector (spec 6.5).
package paystack

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
		"check_balance":          connector.ActionFunc(c.checkBalance),
		"list_transactions":      connector.ActionFunc(c.listSince("/transaction", "success")),
		"list_banks":             connector.ActionFunc(c.listBanks),
		"resolve_account":        connector.ActionFunc(c.resolveAccount),
		"create_recipient":       connector.ActionFunc(c.createRecipient),
		"list_transfers":         connector.ActionFunc(c.listSince("/transfer", "")),
		"transfer":               connector.ActionFunc(c.transfer),
		"verify_transfer":        connector.ActionFunc(c.verifyTransfer),
		"verify_charge":          connector.ActionFunc(c.verifyCharge),
		"initialize_transaction": connector.ActionFunc(c.checkout),
		"refund_transaction":     connector.ActionFunc(c.refund),
		"refund_status":          connector.ActionFunc(c.refundStatus),
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

// listSince lists a resource's records created since input.from, up to a
// hundred, with Paystack's status filter when given.
func (c *client) listSince(path, status string) func(context.Context, connector.Request) (connector.Response, error) {
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		from, _ := req.Input["from"].(string)
		if _, err := time.Parse(time.RFC3339, from); err != nil {
			return connector.Response{}, fmt.Errorf("paystack: from must be a time (RFC 3339): %w", effects.ErrFatal)
		}
		q := url.Values{"from": {from}, "perPage": {"100"}}
		if status != "" {
			q.Set("status", status)
		}
		var env struct {
			Data []map[string]any `json:"data"`
		}
		if err := c.do(ctx, req, http.MethodGet, path+"?"+q.Encode(), nil, &env); err != nil {
			return connector.Response{}, err
		}
		data := make([]any, len(env.Data))
		for i, d := range env.Data {
			data[i] = d
		}
		return connector.Response{Output: map[string]any{"data": data}}, nil
	}
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

// checkout creates a checkout page under the request's reference
// (Paystack's /transaction/initialize).
func (c *client) checkout(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, _ := in["reference"].(string)
	if ref == "" {
		return connector.Response{}, fmt.Errorf("initialize_transaction needs a reference: %w", effects.ErrFatal)
	}
	body := map[string]any{"amount": in["amount"], "email": in["email"], "reference": ref, "currency": orDefault(in["currency"], "NGN")}
	for _, k := range []string{"callback_url", "metadata"} {
		if v, ok := in[k]; ok {
			body[k] = v
		}
	}
	var env envelope
	if err := c.do(ctx, req, http.MethodPost, "/transaction/initialize", body, &env); err != nil {
		return connector.Response{}, err
	}
	if !env.Status {
		return connector.Response{}, fmt.Errorf("paystack: %s: %w", env.Message, effects.ErrFatal)
	}
	out := map[string]any{"reference": ref}
	for _, k := range []string{"authorization_url", "access_code"} {
		if v, ok := env.Data[k].(string); ok {
			out[k] = v
		}
	}
	return connector.Response{Output: out}, nil
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

// refund refunds a payment in full (Paystack's POST /refund, no amount).
// Paystack refuses to refund a payment twice; that refusal is reported as
// the refund already made (status "already"), so a retry never pays twice.
func (c *client) refund(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, _ := in["reference"].(string)
	if ref == "" {
		return connector.Response{}, fmt.Errorf("refund_transaction needs a reference: %w", effects.ErrFatal)
	}
	body := map[string]any{"transaction": ref}
	for _, k := range []string{"customer_note", "merchant_note"} {
		if v, ok := in[k].(string); ok && v != "" {
			body[k] = v
		}
	}
	var env envelope
	err := c.do(ctx, req, http.MethodPost, "/refund", body, &env)
	if err != nil && connector.StatusOf(err) == http.StatusBadRequest && alreadyRefunded(errBody(err)) {
		return connector.Response{Output: map[string]any{"reference": ref, "status": "already"}}, nil
	}
	if err != nil && connector.StatusOf(err) == http.StatusBadRequest {
		// Not enough balance, a payment not refundable: retrying will not help.
		return connector.Response{}, fmt.Errorf("paystack: %s: %w", paystackMessage(err), effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if !env.Status {
		return connector.Response{}, fmt.Errorf("paystack: %s: %w", env.Message, effects.ErrFatal)
	}
	out := map[string]any{"reference": ref, "status": env.Data["status"], "amount": env.Data["amount"]}
	if id, ok := env.Data["id"]; ok {
		out["refund_id"] = id
	}
	return connector.Response{Output: out}, nil
}

// refundStatus is refund_transaction's reconcile: a payment shown as
// reversed was refunded; one that was not is not found, so the refund is
// sent (again).
func (c *client) refundStatus(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref, _ := req.Input["reference"].(string)
	var env envelope
	err := c.do(ctx, req, http.MethodGet, "/transaction/verify/"+url.PathEscape(ref), nil, &env)
	if err != nil {
		return connector.Response{}, err
	}
	st, _ := env.Data["status"].(string)
	if !strings.Contains(st, "reversed") {
		return connector.Response{}, connector.ErrNotFound
	}
	return connector.Response{Output: map[string]any{"reference": ref, "status": st}}, nil
}

func alreadyRefunded(body string) bool {
	b := strings.ToLower(body)
	return strings.Contains(b, "fully reversed") || strings.Contains(b, "already been refunded") || strings.Contains(b, "pending refund") ||
		strings.Contains(b, "refund already")
}

// paystackMessage is the message of Paystack's error body.
func paystackMessage(err error) string {
	var e envelope
	if json.Unmarshal([]byte(errBody(err)), &e) == nil && e.Message != "" {
		return e.Message
	}
	return "refused"
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

// listBanks lists Nigerian banks by name and code (GET /bank).
func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var env struct {
		Data []struct {
			Name string `json:"name"`
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/bank?country=nigeria&perPage=200", nil, &env); err != nil {
		return connector.Response{}, err
	}
	banks := make([]any, 0, len(env.Data))
	for _, b := range env.Data {
		banks = append(banks, map[string]any{"name": b.Name, "code": b.Code})
	}
	return connector.Response{Output: map[string]any{"banks": banks}}, nil
}

var accountNumber = regexp.MustCompile(`^\d{10}$`)

// resolveAccount reads the name on an account (GET /bank/resolve).
func (c *client) resolveAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	acct, _ := req.Input["account_number"].(string)
	bank, _ := req.Input["bank_code"].(string)
	if !accountNumber.MatchString(acct) || bank == "" {
		return connector.Response{}, fmt.Errorf("paystack: an account number is 10 digits, with its bank: %w", effects.ErrFatal)
	}
	var env struct {
		Data struct {
			AccountName   string `json:"account_name"`
			AccountNumber string `json:"account_number"`
		} `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/bank/resolve?account_number="+url.QueryEscape(acct)+"&bank_code="+url.QueryEscape(bank), nil, &env); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"account_name": env.Data.AccountName, "account_number": env.Data.AccountNumber}}, nil
}

// createRecipient saves a NUBAN transfer recipient (POST /transferrecipient);
// Paystack answers the existing recipient for an account saved before.
func (c *client) createRecipient(ctx context.Context, req connector.Request) (connector.Response, error) {
	acct, _ := req.Input["account_number"].(string)
	bank, _ := req.Input["bank_code"].(string)
	name, _ := req.Input["name"].(string)
	acct = strings.TrimSpace(acct)
	if bank == "" {
		// A bank by its name, as a staff sheet has it.
		bankName, _ := req.Input["bank"].(string)
		code, err := c.bankCode(ctx, req, bankName)
		if err != nil {
			return connector.Response{}, err
		}
		bank = code
	}
	if !accountNumber.MatchString(acct) || bank == "" || strings.TrimSpace(name) == "" {
		return connector.Response{}, fmt.Errorf("paystack: a recipient needs a 10-digit account number, its bank and its name: %w", effects.ErrFatal)
	}
	var env struct {
		Data struct {
			RecipientCode string `json:"recipient_code"`
			Name          string `json:"name"`
		} `json:"data"`
	}
	body := map[string]any{"type": "nuban", "name": name, "account_number": acct, "bank_code": bank, "currency": "NGN"}
	if err := c.do(ctx, req, http.MethodPost, "/transferrecipient", body, &env); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"recipient_code": env.Data.RecipientCode, "name": env.Data.Name}}, nil
}

// bankCode finds a bank's code by the name people write: the same name,
// or the only bank whose name starts with or contains it ("GTBank",
// "first bank", "opay").
func (c *client) bankCode(ctx context.Context, req connector.Request, name string) (string, error) {
	want := BankKey(name)
	if want == "" {
		return "", fmt.Errorf("paystack: a recipient needs its bank: %w", effects.ErrFatal)
	}
	resp, err := c.listBanks(ctx, req)
	if err != nil {
		return "", err
	}
	banks, _ := resp.Output.(map[string]any)["banks"].([]any)
	var prefix, contains []string
	for _, b := range banks {
		m, _ := b.(map[string]any)
		n, _ := m["name"].(string)
		code, _ := m["code"].(string)
		k := BankKey(n)
		switch {
		case k == want:
			return code, nil
		case strings.HasPrefix(k, want):
			prefix = append(prefix, code)
		case strings.Contains(k, want):
			contains = append(contains, code)
		}
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	if len(prefix) == 0 && len(contains) == 1 {
		return contains[0], nil
	}
	return "", fmt.Errorf("paystack: no single bank is called %q; write its full name: %w", name, effects.ErrFatal)
}

var bankAliases = map[string]string{"gtbank": "guaranty trust", "gtb": "guaranty trust", "uba": "united for africa", "fcmb": "first city monument",
	"firstbank": "first", "stanbic": "stanbic ibtc", "fidelity": "fidelity", "zenith": "zenith", "access": "access", "opay": "opay", "palmpay": "palmpay",
	"kuda": "kuda", "moniepoint": "moniepoint", "wema": "wema", "sterling": "sterling", "union": "union", "polaris": "polaris", "ecobank": "ecobank"}

var bankNoise = regexp.MustCompile(`\b(bank|plc|limited|ltd|nigeria|of|mfb|microfinance)\b|[^a-z0-9 ]`)

// BankKey is a bank's name for matching: lower case, without "bank",
// "plc" and the like, with the common short names spelt out.
func BankKey(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	if a, ok := bankAliases[strings.ReplaceAll(strings.ReplaceAll(k, " ", ""), "bank", "")]; ok {
		return a
	}
	if a, ok := bankAliases[strings.ReplaceAll(k, " ", "")]; ok {
		return a
	}
	k = bankNoise.ReplaceAllString(k, " ")
	return strings.Join(strings.Fields(k), " ")
}
