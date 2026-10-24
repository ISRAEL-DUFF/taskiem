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
	// Now is the clock for one-time accounts' expiry; nil uses time.Now.
	Now func() time.Time
}

// New returns the Paystack connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	base := strings.TrimRight(m.BaseURL, "/")
	c := &client{base: base, now: o.Now}
	if c.now == nil {
		c.now = time.Now
	}
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
		"pay_with_transfer":      connector.ActionFunc(c.payWithTransfer),
		"refund_transaction":     connector.ActionFunc(c.refund),
		"refund_status":          connector.ActionFunc(c.refundStatus),

		"create_dedicated_account": connector.ActionFunc(c.createDedicatedAccount),
		"list_payments":            connector.ActionFunc(c.listPayments),
	}}
}

type client struct {
	base string
	now  func() time.Time
}

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

// Pay with Transfer keeps an account between 15 minutes and 8 hours
// (account_expires_at, docs: payments/payment-channels).
const (
	transferMinMinutes = 15
	transferMaxMinutes = 8 * 60
)

// payWithTransfer opens a one-time account for one payment: Paystack's
// Pay with Transfer (POST /charge with bank_transfer), which Paystack
// must have turned on for the business. A transfer of another amount, or
// after the account expires, is sent back by Paystack. A reference used
// before reads the waiting charge back (GET /charge/:reference), so a
// retry never opens a second account.
func (c *client) payWithTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, _ := in["reference"].(string)
	if ref == "" {
		return connector.Response{}, fmt.Errorf("pay_with_transfer needs a reference: %w", effects.ErrFatal)
	}
	minutes := transferMaxMinutes
	if m, ok := in["expires_minutes"].(float64); ok {
		minutes = int(m)
	} else if m, ok := in["expires_minutes"].(int); ok {
		minutes = m
	}
	minutes = min(max(minutes, transferMinMinutes), transferMaxMinutes)
	body := map[string]any{
		"amount": in["amount"], "email": in["email"], "reference": ref,
		"bank_transfer": map[string]any{"account_expires_at": c.now().UTC().Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)},
	}
	if v, ok := in["metadata"]; ok {
		body["metadata"] = v
	}
	var env envelope
	err := c.do(ctx, req, http.MethodPost, "/charge", body, &env)
	if err != nil && connector.StatusOf(err) == http.StatusBadRequest && strings.Contains(strings.ToLower(errBody(err)), "duplicate") {
		err = c.do(ctx, req, http.MethodGet, "/charge/"+url.PathEscape(ref), nil, &env)
	}
	if err != nil {
		if connector.StatusOf(err) >= 400 && connector.StatusOf(err) < 500 {
			return connector.Response{}, fmt.Errorf("paystack: %s (if Pay with Transfer is not on for this business, Paystack's support turns it on): %w", paystackMessage(err), err)
		}
		return connector.Response{}, err
	}
	if !env.Status {
		return connector.Response{}, fmt.Errorf("paystack: %s: %w", env.Message, effects.ErrFatal)
	}
	d := env.Data
	number, _ := d["account_number"].(string)
	if number == "" {
		return connector.Response{}, fmt.Errorf("paystack: the charge %s has no account number to show (status %v): %w", ref, d["status"], effects.ErrIndeterminate)
	}
	out := map[string]any{"reference": ref, "account_number": number, "amount": d["amount"], "status": d["status"]}
	if v, ok := d["account_name"].(string); ok {
		out["account_name"] = v
	}
	if b, ok := d["bank"].(map[string]any); ok {
		out["bank_name"] = b["name"]
	}
	if v, ok := d["account_expires_at"].(string); ok {
		out["expires_at"] = v
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

// createDedicatedAccount gives a customer a dedicated virtual account
// (docs-v2.paystack.com/docs/payments/dedicated-virtual-accounts/, the
// multi-step flow). Paystack knows a customer by email and documents no
// reference of ours on either call, so it first fetches the customer by
// email (GET /customer/:email_or_code): an account already there is
// answered, and only a customer Paystack does not know is created
// (POST /customer). Then POST /dedicated_account with the customer's
// code and the bank. What Paystack does with a second create for the same
// customer is not documented, so an unknown outcome parks (unsafe).
func (c *client) createDedicatedAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	email, _ := in["email"].(string)
	if strings.TrimSpace(email) == "" {
		return connector.Response{}, fmt.Errorf("paystack: a dedicated account needs the customer's email: %w", effects.ErrFatal)
	}
	var cust envelope
	err := c.do(ctx, req, http.MethodGet, "/customer/"+url.PathEscape(email), nil, &cust)
	switch {
	case isNotFound(err):
		body := map[string]any{"email": email}
		for _, k := range []string{"first_name", "last_name", "phone"} {
			if v, ok := in[k].(string); ok && v != "" {
				body[k] = v
			}
		}
		if err := c.do(ctx, req, http.MethodPost, "/customer", body, &cust); err != nil {
			return connector.Response{}, err
		}
	case err != nil:
		return connector.Response{}, err
	default:
		if out, ok := dedicatedOutput(cust.Data["dedicated_account"], cust.Data); ok {
			return connector.Response{Output: out}, nil
		}
	}
	code, _ := cust.Data["customer_code"].(string)
	if code == "" {
		return connector.Response{}, fmt.Errorf("paystack: the customer %s has no customer code: %w", email, effects.ErrIndeterminate)
	}
	bank, err := c.dedicatedBank(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"customer": code, "preferred_bank": bank}
	for _, k := range []string{"first_name", "last_name", "phone"} {
		if v, ok := in[k].(string); ok && v != "" {
			body[k] = v
		}
	}
	var env envelope
	if err := c.do(ctx, req, http.MethodPost, "/dedicated_account", body, &env); err != nil {
		if st := connector.StatusOf(err); st >= 400 && st < 500 {
			return connector.Response{}, fmt.Errorf("paystack: %s (dedicated accounts are for registered Nigerian businesses that have gone live; some kinds of business must validate the customer first): %w", paystackMessage(err), err)
		}
		return connector.Response{}, err
	}
	out, ok := dedicatedOutput(env.Data, map[string]any{"customer_code": code})
	if !ok {
		return connector.Response{}, fmt.Errorf("paystack: the dedicated account for %s has no number yet: %w", code, effects.ErrIndeterminate)
	}
	return connector.Response{Output: out}, nil
}

// dedicatedOutput reads a dedicated account (as Create Dedicated Virtual
// Account answers it, or as Fetch Customer carries it) with its customer.
func dedicatedOutput(v any, customer map[string]any) (map[string]any, bool) {
	d, _ := v.(map[string]any)
	number, _ := d["account_number"].(string)
	if number == "" {
		return nil, false
	}
	if c, ok := d["customer"].(map[string]any); ok {
		customer = c
	}
	out := map[string]any{"account_number": number, "customer_code": customer["customer_code"]}
	if n, ok := d["account_name"].(string); ok && n != "" {
		out["account_name"] = n
	}
	if b, ok := d["bank"].(map[string]any); ok {
		out["bank_name"] = b["name"]
	} else if p, ok := d["provider"].(map[string]any); ok {
		out["bank_name"] = p["bank_name"]
	}
	if id, ok := d["id"]; ok {
		out["id"] = fmt.Sprint(id)
	}
	return out, true
}

// dedicatedBank is the bank a new dedicated account is opened at: the
// connection's choice, Paystack's test bank with a test key, or else the
// first provider Paystack lists (GET /dedicated_account/available_providers).
func (c *client) dedicatedBank(ctx context.Context, req connector.Request) (string, error) {
	if b := strings.TrimSpace(req.Credentials["dedicated_account_bank"]); b != "" {
		return b, nil
	}
	if strings.HasPrefix(req.Credentials["secret_key"], "sk_test_") {
		return "test-bank", nil
	}
	var env struct {
		Data []struct {
			Slug string `json:"provider_slug"`
		} `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/dedicated_account/available_providers", nil, &env); err != nil {
		return "", err
	}
	if len(env.Data) == 0 || env.Data[0].Slug == "" {
		return "", fmt.Errorf("paystack lists no bank for dedicated accounts on this business; Paystack's support turns them on: %w", effects.ErrFatal)
	}
	return env.Data[0].Slug, nil
}

// maxPaymentPages bounds one listing of payments: 2,000 at 100 a page.
const maxPaymentPages = 20

// listPayments lists successful payments in a window, every page
// (GET /transaction with from, to, status=success, 100 a page).
func (c *client) listPayments(ctx context.Context, req connector.Request) (connector.Response, error) {
	q := url.Values{"status": {"success"}, "perPage": {"100"}}
	for _, k := range []string{"from", "to"} {
		v, _ := req.Input[k].(string)
		if v == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return connector.Response{}, fmt.Errorf("paystack: %s must be a time (RFC 3339): %w", k, effects.ErrFatal)
		}
		q.Set(k, v)
	}
	if q.Get("from") == "" {
		return connector.Response{}, fmt.Errorf("paystack: list_payments needs from: %w", effects.ErrFatal)
	}
	data := []any{}
	truncated := false
	for page := 1; ; page++ {
		if page > maxPaymentPages {
			truncated = true
			break
		}
		q.Set("page", fmt.Sprint(page))
		var env struct {
			Data []map[string]any `json:"data"`
			Meta struct {
				PageCount int `json:"pageCount"`
			} `json:"meta"`
		}
		if err := c.do(ctx, req, http.MethodGet, "/transaction?"+q.Encode(), nil, &env); err != nil {
			return connector.Response{}, err
		}
		for _, d := range env.Data {
			data = append(data, d)
		}
		if len(env.Data) == 0 || page >= env.Meta.PageCount {
			break
		}
	}
	return connector.Response{Output: map[string]any{"data": data, "truncated": truncated}}, nil
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
