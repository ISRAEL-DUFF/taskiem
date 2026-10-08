// Package flutterwave is the Flutterwave connector, API v3: NGN bank
// payouts, balances, name enquiry, fee quotes and payment verification.
// Built from Flutterwave's public documentation
// (docs/integrations/flutterwave.md).
//
// Amounts: Taskiem works in minor units; Flutterwave v3 takes and returns
// major units (naira), and its payout amount is an integer, so payouts must
// be whole naira. Conversions are exact (connectors/internal/money).
//
// Duplicates: a payout's reference is unique; a repeat is refused ("Payout
// with this ref already exists"), after which the connector fetches the
// payout by reference and reports it. Flutterwave answers a request that
// timed out on its side with 503 after about 28 seconds, so a 503 on a
// payout is an unknown outcome, not a refusal.
package flutterwave

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

// New returns the Flutterwave connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":               connector.ActionFunc(c.getBalance),
		"list_banks":                connector.ActionFunc(c.listBanks),
		"resolve_account":           connector.ActionFunc(c.resolveAccount),
		"get_transfer_fee":          connector.ActionFunc(c.getTransferFee),
		"transfer":                  connector.ActionFunc(c.transfer),
		"get_transfer":              connector.ActionFunc(c.getTransfer),
		"get_transfer_by_reference": connector.ActionFunc(c.getTransferByReference),
		"verify_payment":            connector.ActionFunc(c.verifyPayment),
	}}
}

type client struct{ base string }

// scales are the decimal places of currencies' minor units; others are 2.
var scales = map[string]int{"UGX": 0, "RWF": 0, "XOF": 0, "XAF": 0, "JPY": 0}

func scaleOf(currency string) int {
	if s, ok := scales[strings.ToUpper(currency)]; ok {
		return s
	}
	return 2
}

// apiError is Flutterwave's error envelope.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("flutterwave %d: %s", e.status, e.message) }

// do sends one request and decodes the envelope's data into out.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	key := req.Credentials["secret_key"]
	if key == "" {
		return fmt.Errorf("flutterwave: the connection has no secret_key: %w", effects.ErrFatal)
	}
	var env struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	err := connector.DoJSON(ctx, req.HTTP, method, c.base+path, map[string]string{"Authorization": "Bearer " + key}, body, &env)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{status: he.Status, message: e.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		if he.Status == http.StatusServiceUnavailable && method == http.MethodPost {
			// Flutterwave's timeout: the request may have been acted on.
			return fmt.Errorf("flutterwave: %s: %w", ae.Error(), effects.ErrUnknownOutcome)
		}
		return fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return err
	}
	if env.Status != "success" {
		return fmt.Errorf("%w: %w", &apiError{status: 200, message: env.Message}, effects.ErrUnknownOutcome)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("flutterwave: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func apiErr(err error) *apiError {
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

// unreadable is a response whose amounts cannot be read exactly.
// Flutterwave may still have acted, so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("flutterwave: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	cur := strings.ToUpper(str(req.Input, "currency"))
	if cur == "" {
		cur = "NGN"
	}
	var b struct {
		Currency         string `json:"currency"`
		AvailableBalance any    `json:"available_balance"`
		LedgerBalance    any    `json:"ledger_balance"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/balances/"+url.PathEscape(cur), nil, &b); err != nil {
		return connector.Response{}, err
	}
	s := scaleOf(cur)
	var rd money.Reader
	out := map[string]any{"currency": cur, "available_balance": rd.Minor(b.AvailableBalance, s), "ledger_balance": rd.Minor(b.LedgerBalance, s), "scale": s}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	country := strings.ToUpper(str(req.Input, "country"))
	if country == "" {
		country = "NG"
	}
	var banks []struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/banks/"+url.PathEscape(country), nil, &banks); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(banks))
	for i, b := range banks {
		out[i] = map[string]any{"id": b.ID, "code": b.Code, "name": b.Name}
	}
	return connector.Response{Output: map[string]any{"banks": out}}, nil
}

func (c *client) resolveAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	body := map[string]any{"account_number": str(req.Input, "account_number"), "account_bank": str(req.Input, "bank_code")}
	var a struct {
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
	}
	if err := c.do(ctx, req, http.MethodPost, "/accounts/resolve", body, &a); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"account_name": a.AccountName, "account_number": a.AccountNumber}}, nil
}

// naira converts kobo to whole naira, refusing fractions: Flutterwave's
// payout amount is an integer, and rounding someone's pay is not ours to do.
func naira(v any) (int64, error) {
	k, err := money.Minor(v)
	if err != nil || k < 100 || k%100 != 0 {
		return 0, fmt.Errorf("flutterwave: amount must be whole naira in kobo (a multiple of 100, at least 100), got %v: %w", v, effects.ErrFatal)
	}
	return k / 100, nil
}

func (c *client) getTransferFee(ctx context.Context, req connector.Request) (connector.Response, error) {
	n, err := money.Minor(req.Input["amount"])
	if err != nil {
		return connector.Response{}, fmt.Errorf("flutterwave: %w: %w", err, effects.ErrFatal)
	}
	q := url.Values{"amount": {money.Format(n, 2)}, "currency": {"NGN"}, "type": {"account"}}
	var fees []struct {
		Currency string `json:"currency"`
		Fee      any    `json:"fee"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/transfers/fee?"+q.Encode(), nil, &fees); err != nil {
		return connector.Response{}, err
	}
	if len(fees) == 0 {
		return connector.Response{}, fmt.Errorf("flutterwave: no fee quoted: %w", effects.ErrFatal)
	}
	var rd money.Reader
	fee := rd.Ceil(fees[0].Fee, 2)
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: map[string]any{"fee": fee, "currency": fees[0].Currency}}, nil
}

type transfer struct {
	ID               int64  `json:"id"`
	Reference        string `json:"reference"`
	Status           string `json:"status"`
	Amount           any    `json:"amount"`
	Fee              any    `json:"fee"`
	Currency         string `json:"currency"`
	FullName         string `json:"full_name"`
	BankName         string `json:"bank_name"`
	CompleteMessage  string `json:"complete_message"`
	RequiresApproval any    `json:"requires_approval"`
	IsApproved       any    `json:"is_approved"`
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || x == "true"
	}
	return false
}

func (t transfer) output() (map[string]any, error) {
	s := scaleOf(t.Currency)
	var rd money.Reader
	out := map[string]any{"transfer_id": t.ID, "reference": t.Reference, "status": t.Status, "amount": rd.Minor(t.Amount, s),
		"fee": rd.Ceil(t.Fee, 2), "currency": t.Currency, "account_name": t.FullName, "bank_name": t.BankName,
		"complete_message": t.CompleteMessage, "requires_approval": truthy(t.RequiresApproval) && !truthy(t.IsApproved)}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// settled fails the step for a payout Flutterwave reports FAILED: no money
// moved, and its message is the error.
func settled(t transfer) (connector.Response, error) {
	if t.Status == "FAILED" {
		return connector.Response{}, fmt.Errorf("flutterwave: payout %s failed: %s: %w", t.Reference, t.CompleteMessage, effects.ErrFatal)
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("flutterwave: transfer needs the engine's reference: %w", effects.ErrFatal)
	}
	amount, err := naira(in["amount"])
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"account_bank": str(in, "bank_code"), "account_number": str(in, "account_number"), "amount": amount,
		"currency": "NGN", "debit_currency": "NGN", "reference": ref}
	if n := str(in, "narration"); n != "" {
		body["narration"] = n
	}
	if n := str(in, "beneficiary_name"); n != "" {
		body["beneficiary_name"] = n
	}
	var t transfer
	err = c.do(ctx, req, http.MethodPost, "/transfers", body, &t)
	if ae := apiErr(err); ae != nil && ae.status < 500 && strings.Contains(strings.ToLower(ae.message), "already exists") {
		found, ferr := c.byReference(ctx, req, ref)
		if errors.Is(ferr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("flutterwave refused reference %s (%s) and has no payout under it: %w", ref, ae.message, effects.ErrUnknownOutcome)
		}
		if ferr != nil {
			return connector.Response{}, ferr
		}
		return settled(found)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return settled(t)
}

func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (transfer, error) {
	var list []transfer
	if err := c.do(ctx, req, http.MethodGet, "/transfers?"+url.Values{"reference": {ref}}.Encode(), nil, &list); err != nil {
		return transfer{}, err
	}
	for _, t := range list {
		if t.Reference == ref {
			return t, nil
		}
	}
	return transfer{}, fmt.Errorf("flutterwave: no payout with reference %s: %w", ref, connector.ErrNotFound)
}

func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := money.Minor(req.Input["transfer_id"])
	if err != nil {
		return connector.Response{}, fmt.Errorf("flutterwave: transfer_id: %w: %w", err, effects.ErrFatal)
	}
	var t transfer
	err = c.do(ctx, req, http.MethodGet, "/transfers/"+strconv.FormatInt(id, 10), nil, &t)
	if ae := apiErr(err); ae != nil && ae.status < 500 && strings.Contains(strings.ToLower(ae.message), "not found") {
		return connector.Response{}, fmt.Errorf("%w: %w: %w", err, connector.ErrNotFound, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) getTransferByReference(ctx context.Context, req connector.Request) (connector.Response, error) {
	t, err := c.byReference(ctx, req, str(req.Input, "reference"))
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) verifyPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	var path string
	if id, ok := req.Input["transaction_id"]; ok && id != nil {
		n, err := money.Minor(id)
		if err != nil {
			return connector.Response{}, fmt.Errorf("flutterwave: transaction_id: %w: %w", err, effects.ErrFatal)
		}
		path = "/transactions/" + strconv.FormatInt(n, 10) + "/verify"
	} else if ref := str(req.Input, "tx_ref"); ref != "" {
		path = "/transactions/verify_by_reference?" + url.Values{"tx_ref": {ref}}.Encode()
	} else {
		return connector.Response{}, fmt.Errorf("flutterwave: verify_payment needs transaction_id or tx_ref: %w", effects.ErrFatal)
	}
	var p struct {
		ID            int64  `json:"id"`
		TxRef         string `json:"tx_ref"`
		FlwRef        string `json:"flw_ref"`
		Status        string `json:"status"`
		Amount        any    `json:"amount"`
		ChargedAmount any    `json:"charged_amount"`
		Currency      string `json:"currency"`
		PaymentType   string `json:"payment_type"`
		Customer      struct {
			Email string `json:"email"`
		} `json:"customer"`
	}
	err := c.do(ctx, req, http.MethodGet, path, nil, &p)
	if ae := apiErr(err); ae != nil && ae.status < 500 && strings.Contains(strings.ToLower(ae.message), "no transaction") {
		return connector.Response{}, fmt.Errorf("%w: %w: %w", err, connector.ErrNotFound, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	s := scaleOf(p.Currency)
	var rd money.Reader
	out := map[string]any{"transaction_id": p.ID, "tx_ref": p.TxRef, "flw_ref": p.FlwRef, "status": p.Status,
		"amount": rd.Minor(p.Amount, s), "charged_amount": rd.Ceil(p.ChargedAmount, s), "currency": p.Currency,
		"payment_type": p.PaymentType, "customer_email": p.Customer.Email}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}
