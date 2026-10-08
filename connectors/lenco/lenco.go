// Package lenco is the Lenco connector (API v1): NGN bank transfers,
// balances, name enquiry and virtual accounts. Built from Lenco's public API
// reference (docs/integrations/lenco.md).
//
// Amounts: Taskiem works in kobo; Lenco v1 takes and returns naira as
// decimal strings ("2000.00"). Conversions are exact (connectors/internal/money).
//
// Duplicates: a transfer's reference is unique per client. A repeat is
// refused with "Duplicate client reference" (error code 04), after which the
// connector fetches the transfer by reference and reports it, so resending
// after a lost response never pays twice.
package lenco

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://sandbox.lenco.co/access/v1"

// Options configure the connector; BaseURL overrides both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the Lenco connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_accounts":          connector.ActionFunc(c.listAccounts),
		"get_balance":            connector.ActionFunc(c.getBalance),
		"list_banks":             connector.ActionFunc(c.listBanks),
		"resolve_account":        connector.ActionFunc(c.resolveAccount),
		"transfer":               connector.ActionFunc(c.transfer),
		"get_transfer":           connector.ActionFunc(c.getTransfer),
		"create_virtual_account": connector.ActionFunc(c.createVirtualAccount),
	}}
}

type client struct{ live, sandbox string }

func (c *client) base(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// envelope is Lenco's response wrapper.
type envelope struct {
	Status    bool            `json:"status"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	ErrorCode string          `json:"errorCode"`
}

// apiError is a refusal Lenco explained.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("lenco %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("lenco %d: %s", e.status, e.message)
}

func (e *apiError) notFound() bool {
	return e != nil && strings.Contains(strings.ToLower(e.message), "not found")
}

// do sends one request and returns the envelope's data. Failures are
// classified for the engine; refusals Lenco explains carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	tok := req.Credentials["api_token"]
	if tok == "" {
		return fmt.Errorf("lenco: the connection has no api_token: %w", effects.ErrFatal)
	}
	var env envelope
	err := connector.DoJSON(ctx, req.HTTP, method, c.base(req)+path, map[string]string{"Authorization": "Bearer " + tok}, body, &env)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e envelope
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{status: he.Status, code: e.ErrorCode, message: e.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		// Keep the transport's classification (429/503 retryable, other
		// 5xx unknown outcome, 4xx fatal) and add Lenco's explanation.
		return fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return err
	}
	if !env.Status {
		// A 2xx that says it did not work. Lenco does not document these;
		// for a write the outcome is unknown, so let the caller decide.
		return fmt.Errorf("%w: %w", &apiError{status: 200, code: env.ErrorCode, message: env.Message}, effects.ErrUnknownOutcome)
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("lenco: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func lencoError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// unreadable is a response whose amounts cannot be read exactly. Lenco may
// still have acted (a payout that went through), so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("lenco: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func (c *client) accountID(req connector.Request) (string, error) {
	if id := str(req.Input, "account_id"); id != "" {
		return id, nil
	}
	if id := req.Credentials["account_id"]; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("lenco: no account_id in the step or the connection: %w", effects.ErrFatal)
}

type bankAccount struct {
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	Bank          struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"bank"`
}

func (c *client) listAccounts(ctx context.Context, req connector.Request) (connector.Response, error) {
	var accounts []struct {
		ID               string      `json:"id"`
		Name             string      `json:"name"`
		Currency         string      `json:"currency"`
		BankAccount      bankAccount `json:"bankAccount"`
		Status           string      `json:"status"`
		AvailableBalance any         `json:"availableBalance"`
		CurrentBalance   any         `json:"currentBalance"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/accounts", nil, &accounts); err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := make([]any, len(accounts))
	for i, a := range accounts {
		out[i] = map[string]any{"id": a.ID, "name": a.Name, "currency": a.Currency, "account_number": a.BankAccount.AccountNumber,
			"bank_code": a.BankAccount.Bank.Code, "status": a.Status, "available_balance": rd.Minor(a.AvailableBalance, 2), "current_balance": rd.Minor(a.CurrentBalance, 2)}
	}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: map[string]any{"accounts": out}}, nil
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := c.accountID(req)
	if err != nil {
		return connector.Response{}, err
	}
	var b struct {
		AvailableBalance any    `json:"availableBalance"`
		CurrentBalance   any    `json:"currentBalance"`
		Currency         string `json:"currency"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/account/"+url.PathEscape(id)+"/balance", nil, &b); err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := map[string]any{"account_id": id, "currency": b.Currency,
		"available_balance": rd.Minor(b.AvailableBalance, 2), "current_balance": rd.Minor(b.CurrentBalance, 2)}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var banks []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/banks", nil, &banks); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(banks))
	for i, b := range banks {
		out[i] = map[string]any{"code": b.Code, "name": b.Name}
	}
	return connector.Response{Output: map[string]any{"banks": out}}, nil
}

func (c *client) resolveAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	q := url.Values{"accountNumber": {str(req.Input, "account_number")}, "bankCode": {str(req.Input, "bank_code")}}
	var a bankAccount
	if err := c.do(ctx, req, http.MethodGet, "/resolve?"+q.Encode(), nil, &a); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"account_name": a.AccountName, "account_number": a.AccountNumber,
		"bank_code": a.Bank.Code, "bank_name": a.Bank.Name}}, nil
}

// transferData is what /transfer and /transfer/by-reference return.
type transferData struct {
	Request struct {
		Reference string `json:"reference"`
		Status    string `json:"status"` // queued, created
	} `json:"request"`
	Transaction *struct {
		ID               string      `json:"id"`
		Amount           any         `json:"amount"`
		Fee              any         `json:"fee"`
		Details          bankAccount `json:"details"`
		Status           string      `json:"status"` // pending, successful, failed, declined
		ReasonForFailure *string     `json:"reasonForFailure"`
		ClientReference  string      `json:"clientReference"`
		NIPSessionID     *string     `json:"nipSessionId"`
	} `json:"transaction"`
}

func (d transferData) output(reference string, amount int64) (map[string]any, error) {
	out := map[string]any{"reference": reference, "status": "pending", "transaction_id": "", "amount": amount, "fee": int64(0),
		"account_name": "", "nip_session_id": "", "reason_for_failure": ""}
	if t := d.Transaction; t != nil {
		out["status"], out["transaction_id"] = t.Status, t.ID
		var rd money.Reader
		out["amount"], out["fee"] = rd.Minor(t.Amount, 2), rd.Ceil(t.Fee, 2)
		if rd.Err != nil {
			return nil, unreadable(rd.Err)
		}
		out["account_name"] = t.Details.AccountName
		if t.NIPSessionID != nil {
			out["nip_session_id"] = *t.NIPSessionID
		}
		if t.ReasonForFailure != nil {
			out["reason_for_failure"] = *t.ReasonForFailure
		}
	}
	return out, nil
}

// settled turns a transfer Lenco reports failed into a failed step: no
// money moved, and Lenco's reason is the error. "declined" is not
// explained in Lenco's documentation, so a person decides.
func settled(out map[string]any) (connector.Response, error) {
	switch out["status"] {
	case "failed":
		return connector.Response{}, fmt.Errorf("lenco: transfer %s failed: %s: %w", out["reference"], out["reason_for_failure"], effects.ErrFatal)
	case "declined":
		return connector.Response{}, fmt.Errorf("lenco: transfer %s was declined (%s); Lenco does not document whether that is final. Check it in Lenco, then resolve the step: %w",
			out["reference"], out["reason_for_failure"], effects.ErrIndeterminate)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("lenco: transfer needs the engine's reference: %w", effects.ErrFatal)
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("lenco: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	from, err := c.accountID(req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"accountId": from, "amount": money.Format(amount, 2), "narration": str(in, "narration"), "reference": ref}
	if r := str(in, "recipient_id"); r != "" {
		body["recipientId"] = r
	} else {
		if str(in, "account_number") == "" || str(in, "bank_code") == "" {
			return connector.Response{}, fmt.Errorf("lenco: transfer needs recipient_id, or account_number and bank_code: %w", effects.ErrFatal)
		}
		body["accountNumber"], body["bankCode"] = str(in, "account_number"), str(in, "bank_code")
	}
	var d transferData
	err = c.do(ctx, req, http.MethodPost, "/transfer", body, &d)
	if ae := lencoError(err); ae != nil && (ae.code == "04" || strings.Contains(strings.ToLower(ae.message), "duplicate")) {
		// Already sent under this reference: report that transfer.
		r, gerr := c.byReference(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("lenco refused reference %s (%s) and has no transfer under it: %w", ref, ae.message, effects.ErrFatal)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return settled(r)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := d.output(ref, amount)
	if err != nil {
		return connector.Response{}, err
	}
	return settled(out)
}

func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	var d transferData
	err := c.do(ctx, req, http.MethodGet, "/transfer/by-reference/"+url.PathEscape(ref), nil, &d)
	if ae := lencoError(err); ae != nil && ae.status < 500 && ae.notFound() {
		return nil, fmt.Errorf("lenco: no transfer with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return d.output(ref, 0)
}

func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.byReference(ctx, req, str(req.Input, "reference"))
	if err != nil {
		if errors.Is(err, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
		}
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) createVirtualAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	body := map[string]any{"accountName": str(in, "account_name")}
	if s, ok := in["is_static"].(bool); ok && s {
		body["isStatic"] = true
		if str(in, "bvn") == "" {
			return connector.Response{}, fmt.Errorf("lenco: a static virtual account needs the BVN of the person it is for: %w", effects.ErrFatal)
		}
		body["bvn"] = str(in, "bvn")
	}
	if r := str(in, "transaction_reference"); r != "" {
		body["transactionReference"] = r
	}
	for field, key := range map[string]string{"amount": "amount", "min_amount": "minAmount"} {
		if v, ok := in[field]; ok && v != nil {
			n, err := money.Minor(v)
			if err != nil {
				return connector.Response{}, fmt.Errorf("lenco: %s: %w: %w", field, err, effects.ErrFatal)
			}
			body[key] = money.Number(n, 2)
		}
	}
	var v struct {
		ID               string      `json:"id"`
		AccountReference string      `json:"accountReference"`
		BankAccount      bankAccount `json:"bankAccount"`
		Type             string      `json:"type"`
		Status           string      `json:"status"`
		ExpiresAt        *string     `json:"expiresAt"`
	}
	if err := c.do(ctx, req, http.MethodPost, "/virtual-accounts", body, &v); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"id": v.ID, "account_reference": v.AccountReference, "account_number": v.BankAccount.AccountNumber,
		"account_name": v.BankAccount.AccountName, "bank_code": v.BankAccount.Bank.Code, "bank_name": v.BankAccount.Bank.Name,
		"type": v.Type, "status": v.Status, "expires_at": ""}
	if v.ExpiresAt != nil {
		out["expires_at"] = *v.ExpiresAt
	}
	return connector.Response{Output: out}, nil
}
