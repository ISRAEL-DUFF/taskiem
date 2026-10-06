// Package moniepoint is the Moniepoint connector, built on Monnify,
// Moniepoint's payment gateway: NGN disbursements, wallet balance, name
// enquiry, reserved (virtual) accounts, checkout and payment status. Built
// from Monnify's public documentation (docs/integrations/moniepoint.md).
//
// Auth: Monnify issues an hour-long bearer token from /api/v1/auth/login
// against Basic base64(apiKey:secretKey). The connector caches one token per
// credential set and host until shortly before it expires, and fetches a
// new one once if Monnify answers 401.
//
// Amounts: Taskiem works in kobo; Monnify takes and returns naira (20,
// "100.00"). Conversions are exact (connectors/internal/money).
//
// Duplicates: a transfer's reference is unique per merchant; a repeat is
// refused with response code D05 ("Supplied reference already exists"),
// after which the connector fetches the transfer by reference and reports
// it, so resending after a lost response never pays twice.
package moniepoint

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://sandbox.monnify.com"

// Options configure the connector; BaseURL overrides both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the Moniepoint (Monnify) connector.
func New(o Options) *connector.Connector { return newConnector(o, time.Now) }

// newConnector is New with the clock that times token expiry.
func newConnector(o Options, now func() time.Time) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL, now: now, tokens: map[string]token{}}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":             connector.ActionFunc(c.getBalance),
		"list_banks":              connector.ActionFunc(c.listBanks),
		"resolve_account":         connector.ActionFunc(c.resolveAccount),
		"transfer":                connector.ActionFunc(c.transfer),
		"get_transfer":            connector.ActionFunc(c.getTransfer),
		"create_reserved_account": connector.ActionFunc(c.createReservedAccount),
		"get_reserved_account":    connector.ActionFunc(c.getReservedAccount),
		"init_transaction":        connector.ActionFunc(c.initTransaction),
		"get_transaction":         connector.ActionFunc(c.getTransaction),
	}}
}

type token struct {
	value   string
	expires time.Time
}

type client struct {
	live, sandbox string
	now           func() time.Time

	mu     sync.Mutex
	tokens map[string]token // by host and credentials
}

func (c *client) base(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// envelope is Monnify's response wrapper.
type envelope struct {
	RequestSuccessful *bool           `json:"requestSuccessful"`
	ResponseMessage   string          `json:"responseMessage"`
	ResponseCode      string          `json:"responseCode"`
	ResponseBody      json.RawMessage `json:"responseBody"`
}

// apiError is a refusal Monnify explained.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("monnify %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("monnify %d: %s", e.status, e.message)
}

func (e *apiError) notFound() bool {
	if e == nil || e.status >= 500 {
		return false
	}
	m := strings.ToLower(e.message)
	return e.status == http.StatusNotFound || e.code == "D02" ||
		strings.Contains(m, "not found") || strings.Contains(m, "does not exist") || strings.Contains(m, "could not find") || strings.Contains(m, "no transaction")
}

// existing reports a refusal that names something already made under the
// caller's reference.
func (e *apiError) existing() bool {
	if e == nil || e.status >= 500 {
		return false
	}
	m := strings.ToLower(e.message)
	return e.code == "D05" || strings.Contains(m, "already exist") || strings.Contains(m, "duplicate") || strings.Contains(m, "existing")
}

func monnifyError(err error) *apiError {
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

// unreadable is a response whose amounts cannot be read exactly. Monnify
// may still have acted (a transfer that went through), so it is never a
// failure.
func unreadable(err error) error {
	return fmt.Errorf("monnify: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func (c *client) key(req connector.Request) string {
	return c.base(req) + "\x00" + req.Credentials["api_key"] + "\x00" + req.Credentials["secret_key"]
}

// bearer returns a cached token, or logs in for a new one.
func (c *client) bearer(ctx context.Context, req connector.Request) (string, error) {
	apiKey, secret := req.Credentials["api_key"], req.Credentials["secret_key"]
	if apiKey == "" || secret == "" {
		return "", fmt.Errorf("monnify: the connection needs api_key and secret_key: %w", effects.ErrFatal)
	}
	k := c.key(req)
	c.mu.Lock()
	t, ok := c.tokens[k]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires) {
		return t.value, nil
	}
	var env envelope
	basic := base64.StdEncoding.EncodeToString([]byte(apiKey + ":" + secret))
	err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base(req)+"/api/v1/auth/login", map[string]string{"Authorization": "Basic " + basic}, nil, &env)
	if err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			var e envelope
			_ = json.Unmarshal(he.Body, &e)
			// The login sends nothing to anyone: whatever went wrong, the
			// action has not been attempted.
			if he.Status >= 500 || he.Status == http.StatusTooManyRequests {
				return "", fmt.Errorf("monnify login: %s: %w", strings.TrimSpace(e.ResponseMessage+" "+he.Error()), effects.ErrRetryable)
			}
			return "", fmt.Errorf("monnify login refused (check api_key and secret_key): %s: %w", strings.TrimSpace(e.ResponseMessage+" "+he.Error()), effects.ErrFatal)
		}
		return "", fmt.Errorf("monnify login: %w: %w", err, effects.ErrRetryable)
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int64  `json:"expiresIn"`
	}
	_ = json.Unmarshal(env.ResponseBody, &body)
	if body.AccessToken == "" {
		return "", fmt.Errorf("monnify login returned no token (%s): %w", env.ResponseMessage, effects.ErrRetryable)
	}
	life := time.Duration(body.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	// Renew a minute early so a token never expires in flight.
	if life > 2*time.Minute {
		life -= time.Minute
	}
	c.mu.Lock()
	c.tokens[k] = token{value: body.AccessToken, expires: c.now().Add(life)}
	c.mu.Unlock()
	return body.AccessToken, nil
}

func (c *client) forget(req connector.Request) {
	c.mu.Lock()
	delete(c.tokens, c.key(req))
	c.mu.Unlock()
}

// do sends one request with a bearer token and decodes responseBody into
// out. A 401 renews the token once: Monnify refused before acting. Failures
// keep the transport's classification and carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	for try := 0; ; try++ {
		tok, err := c.bearer(ctx, req)
		if err != nil {
			return err
		}
		var env envelope
		err = connector.DoJSON(ctx, req.HTTP, method, c.base(req)+path, map[string]string{"Authorization": "Bearer " + tok}, body, &env)
		var he *connector.HTTPError
		if errors.As(err, &he) {
			if he.Status == http.StatusUnauthorized && try == 0 {
				c.forget(req)
				continue
			}
			var e envelope
			_ = json.Unmarshal(he.Body, &e)
			ae := &apiError{status: he.Status, code: e.ResponseCode, message: e.ResponseMessage}
			if ae.message == "" {
				ae.message = strings.TrimSpace(string(he.Body))
			}
			return fmt.Errorf("%w: %w", ae, he)
		}
		if err != nil {
			return err
		}
		if env.RequestSuccessful != nil && !*env.RequestSuccessful {
			// A 2xx that says it did not work: the caller decides; by
			// default the outcome is unknown.
			return fmt.Errorf("%w: %w", &apiError{status: 200, code: env.ResponseCode, message: env.ResponseMessage}, effects.ErrUnknownOutcome)
		}
		if out != nil && len(env.ResponseBody) > 0 && string(env.ResponseBody) != "null" {
			if err := json.Unmarshal(env.ResponseBody, out); err != nil {
				return fmt.Errorf("monnify: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
			}
		}
		return nil
	}
}

func (c *client) wallet(req connector.Request) (string, error) {
	if w := str(req.Input, "wallet_account_number"); w != "" {
		return w, nil
	}
	if w := req.Credentials["wallet_account_number"]; w != "" {
		return w, nil
	}
	return "", fmt.Errorf("monnify: no wallet_account_number in the step or the connection: %w", effects.ErrFatal)
}

func (c *client) contract(req connector.Request) (string, error) {
	if s := req.Credentials["contract_code"]; s != "" {
		return s, nil
	}
	return "", fmt.Errorf("monnify: the connection has no contract_code: %w", effects.ErrFatal)
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	w, err := c.wallet(req)
	if err != nil {
		return connector.Response{}, err
	}
	var b struct {
		AvailableBalance any `json:"availableBalance"`
		LedgerBalance    any `json:"ledgerBalance"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/api/v2/disbursements/wallet-balance?"+url.Values{"accountNumber": {w}}.Encode(), nil, &b); err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := map[string]any{"wallet_account_number": w, "available_balance": rd.Minor(b.AvailableBalance, 2), "ledger_balance": rd.Minor(b.LedgerBalance, 2)}
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
	if err := c.do(ctx, req, http.MethodGet, "/api/v1/banks", nil, &banks); err != nil {
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
	var a struct {
		AccountNumber string `json:"accountNumber"`
		AccountName   string `json:"accountName"`
		BankCode      string `json:"bankCode"`
	}
	err := c.do(ctx, req, http.MethodGet, "/api/v1/disbursements/account/validate?"+q.Encode(), nil, &a)
	if ae := monnifyError(err); ae != nil && ae.status == 200 {
		// "Request was not successful" on a read: the account did not
		// validate.
		return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if a.AccountName == "" {
		return connector.Response{}, fmt.Errorf("monnify: no account name for %s at bank %s: %w", q.Get("accountNumber"), q.Get("bankCode"), effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"account_name": a.AccountName, "account_number": a.AccountNumber, "bank_code": a.BankCode}}, nil
}

// transferData is a disbursement as Monnify returns it from initiation,
// the summary and the requery.
type transferData struct {
	Amount                   any    `json:"amount"`
	Reference                string `json:"reference"`
	Status                   string `json:"status"`
	TransactionStatus        string `json:"transactionStatus"`
	TotalFee                 any    `json:"totalFee"`
	Fee                      any    `json:"fee"`
	TransactionReference     string `json:"transactionReference"`
	TransactionDescription   string `json:"transactionDescription"`
	Comment                  string `json:"comment"`
	SessionID                string `json:"sessionId"`
	DestinationAccountName   string `json:"destinationAccountName"`
	DestinationAccountNumber string `json:"destinationAccountNumber"`
	DestinationBankCode      string `json:"destinationBankCode"`
	DestinationBankName      string `json:"destinationBankName"`
}

func (d transferData) output(reference string) (map[string]any, error) {
	status := d.Status
	if status == "" {
		status = d.TransactionStatus
	}
	if d.Reference != "" {
		reference = d.Reference
	}
	fee := d.Fee
	if fee == nil {
		fee = d.TotalFee
	}
	var rd money.Reader
	reason := d.Comment
	if reason == "" {
		reason = d.TransactionDescription
	}
	out := map[string]any{"reference": reference, "status": status, "transaction_reference": d.TransactionReference,
		"amount": rd.Minor(d.Amount, 2), "fee": rd.Ceil(fee, 2), "account_name": d.DestinationAccountName,
		"account_number": d.DestinationAccountNumber, "bank_code": d.DestinationBankCode, "bank_name": d.DestinationBankName,
		"session_id": d.SessionID, "reason": reason, "requires_authorization": status == "PENDING_AUTHORIZATION"}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// settled turns a transfer Monnify reports FAILED or REVERSED into a failed
// step: no money reached the beneficiary, and Monnify's reason is the error.
// EXPIRED is documented only for batches, so a person decides.
func settled(out map[string]any) (connector.Response, error) {
	switch out["status"] {
	case "FAILED":
		return connector.Response{}, fmt.Errorf("monnify: transfer %s failed: %s: %w", out["reference"], out["reason"], effects.ErrFatal)
	case "REVERSED":
		return connector.Response{}, fmt.Errorf("monnify: transfer %s was reversed: %s: %w", out["reference"], out["reason"], effects.ErrFatal)
	case "EXPIRED":
		return connector.Response{}, fmt.Errorf("monnify: transfer %s expired; Monnify documents EXPIRED only for batches. Check it in Monnify, then resolve the step: %w",
			out["reference"], effects.ErrIndeterminate)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("monnify: transfer needs the engine's reference: %w", effects.ErrFatal)
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("monnify: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	if str(in, "account_number") == "" || str(in, "bank_code") == "" {
		return connector.Response{}, fmt.Errorf("monnify: transfer needs account_number and bank_code: %w", effects.ErrFatal)
	}
	from, err := c.wallet(req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"amount": money.Number(amount, 2), "reference": ref, "narration": str(in, "narration"),
		"destinationBankCode": str(in, "bank_code"), "destinationAccountNumber": str(in, "account_number"),
		"currency": "NGN", "sourceAccountNumber": from}
	if n := str(in, "account_name"); n != "" {
		body["destinationAccountName"] = n
	}
	if a, ok := in["async"].(bool); ok && a {
		body["async"] = true
	}
	var d transferData
	err = c.do(ctx, req, http.MethodPost, "/api/v2/disbursements/single", body, &d)
	ae := monnifyError(err)
	switch {
	case ae != nil && (ae.existing() || ae.code == "D07"):
		// D05: already sent under this reference. D07: Monnify refused a
		// repeat to the same account and amount within two minutes, which
		// may be this same transfer. Report what is under the reference.
		found, gerr := c.byReference(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			if ae.code == "D07" {
				return connector.Response{}, fmt.Errorf("monnify refused transfer %s as a duplicate of a recent transfer of the same amount to the same account (%s); nothing was sent under this reference: %w",
					ref, ae.message, effects.ErrFatal)
			}
			// Refused, yet not listed (yet): never assume it did not happen.
			return connector.Response{}, fmt.Errorf("monnify refused reference %s (%s) and has no transfer under it: %w", ref, ae.message, effects.ErrUnknownOutcome)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return settled(found)
	case ae != nil && ae.code == "99":
		// "An unexpected error occurred... Re-query to ascertain status."
		return connector.Response{}, fmt.Errorf("monnify: %s: %w", ae.Error(), effects.ErrUnknownOutcome)
	case ae != nil && ae.status < 500 && (ae.code == "D01" || ae.code == "D03" || ae.code == "D04" || ae.code == "D06"):
		// Documented refusals: "Treat as FAILED".
		return connector.Response{}, fmt.Errorf("monnify: transfer %s refused: %s: %w", ref, ae.Error(), effects.ErrFatal)
	case err != nil:
		return connector.Response{}, err
	}
	out, err := d.output(ref)
	if err != nil {
		return connector.Response{}, err
	}
	return settled(out)
}

func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	var d transferData
	err := c.do(ctx, req, http.MethodGet, "/api/v2/disbursements/single/summary?"+url.Values{"reference": {ref}}.Encode(), nil, &d)
	if ae := monnifyError(err); ae.notFound() {
		return nil, fmt.Errorf("monnify: no transfer with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if d.Reference == "" && d.Status == "" && d.TransactionStatus == "" {
		return nil, fmt.Errorf("monnify: no transfer with reference %s: %w", ref, connector.ErrNotFound)
	}
	return d.output(ref)
}

func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.byReference(ctx, req, str(req.Input, "reference"))
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// reservation is a reserved account as Monnify returns it.
type reservation struct {
	AccountReference     string `json:"accountReference"`
	AccountName          string `json:"accountName"`
	CustomerEmail        string `json:"customerEmail"`
	ReservationReference string `json:"reservationReference"`
	Status               string `json:"status"`
	Accounts             []struct {
		BankCode      string `json:"bankCode"`
		BankName      string `json:"bankName"`
		AccountNumber string `json:"accountNumber"`
		AccountName   string `json:"accountName"`
	} `json:"accounts"`
}

func (r reservation) output() map[string]any {
	accts := make([]any, len(r.Accounts))
	for i, a := range r.Accounts {
		name := a.AccountName
		if name == "" {
			name = r.AccountName
		}
		accts[i] = map[string]any{"account_number": a.AccountNumber, "account_name": name, "bank_code": a.BankCode, "bank_name": a.BankName}
	}
	return map[string]any{"account_reference": r.AccountReference, "reservation_reference": r.ReservationReference,
		"account_name": r.AccountName, "customer_email": r.CustomerEmail, "status": r.Status, "accounts": accts}
}

func (c *client) reservedAccount(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	if ref == "" {
		return nil, fmt.Errorf("monnify: account_reference is required: %w", effects.ErrFatal)
	}
	var r reservation
	err := c.do(ctx, req, http.MethodGet, "/api/v2/bank-transfer/reserved-accounts/"+url.PathEscape(ref), nil, &r)
	if ae := monnifyError(err); ae.notFound() {
		return nil, fmt.Errorf("monnify: no reserved account with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if r.AccountReference == "" {
		return nil, fmt.Errorf("monnify: no reserved account with reference %s: %w", ref, connector.ErrNotFound)
	}
	return r.output(), nil
}

func (c *client) getReservedAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.reservedAccount(ctx, req, str(req.Input, "account_reference"))
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) createReservedAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "account_reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("monnify: create_reserved_account needs the engine's account_reference: %w", effects.ErrFatal)
	}
	contract, err := c.contract(req)
	if err != nil {
		return connector.Response{}, err
	}
	if req.Attempt > 1 {
		// A resend: report the account if the first attempt made it.
		out, err := c.reservedAccount(ctx, req, ref)
		if err == nil {
			return connector.Response{Output: out}, nil
		}
		if !errors.Is(err, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("monnify: checking for an earlier attempt before resending: %w", err)
		}
	}
	body := map[string]any{"accountReference": ref, "accountName": str(in, "account_name"), "currencyCode": "NGN",
		"contractCode": contract, "customerEmail": str(in, "customer_email"), "getAllAvailableBanks": true}
	for field, key := range map[string]string{"customer_name": "customerName", "bvn": "bvn", "nin": "nin"} {
		if v := str(in, field); v != "" {
			body[key] = v
		}
	}
	if banks, ok := in["preferred_banks"].([]any); ok && len(banks) > 0 {
		body["getAllAvailableBanks"] = false
		body["preferredBanks"] = banks
	}
	var r reservation
	err = c.do(ctx, req, http.MethodPost, "/api/v2/bank-transfer/reserved-accounts", body, &r)
	if ae := monnifyError(err); ae != nil && ae.existing() {
		out, gerr := c.reservedAccount(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("monnify refused account reference %s (%s) and has no account under it: %w", ref, ae.message, effects.ErrUnknownOutcome)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return connector.Response{Output: out}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: r.output()}, nil
}

// payment is a collection as /api/v2/merchant/transactions/query returns it.
type payment struct {
	TransactionReference string `json:"transactionReference"`
	PaymentReference     string `json:"paymentReference"`
	AmountPaid           any    `json:"amountPaid"`
	TotalPayable         any    `json:"totalPayable"`
	SettlementAmount     any    `json:"settlementAmount"`
	PaidOn               string `json:"paidOn"`
	PaymentStatus        string `json:"paymentStatus"`
	Currency             string `json:"currency"`
	PaymentMethod        string `json:"paymentMethod"`
	Product              struct {
		Type      string `json:"type"`
		Reference string `json:"reference"`
	} `json:"product"`
	Customer struct {
		Email string `json:"email"`
	} `json:"customer"`
}

func (p payment) output() (map[string]any, error) {
	var rd money.Reader
	out := map[string]any{"payment_reference": p.PaymentReference, "transaction_reference": p.TransactionReference,
		"status": p.PaymentStatus, "amount_paid": rd.Minor(p.AmountPaid, 2), "total_payable": rd.Minor(p.TotalPayable, 2),
		"settlement_amount": rd.Floor(p.SettlementAmount, 2), "currency": p.Currency, "payment_method": p.PaymentMethod,
		"paid_on": p.PaidOn, "product_type": p.Product.Type, "product_reference": p.Product.Reference, "customer_email": p.Customer.Email}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

func (c *client) queryPayment(ctx context.Context, req connector.Request, q url.Values) (payment, error) {
	var p payment
	err := c.do(ctx, req, http.MethodGet, "/api/v2/merchant/transactions/query?"+q.Encode(), nil, &p)
	if ae := monnifyError(err); ae.notFound() {
		return p, fmt.Errorf("monnify: no transaction for %s: %w", q.Encode(), connector.ErrNotFound)
	}
	if err == nil && p.TransactionReference == "" && p.PaymentReference == "" {
		return p, fmt.Errorf("monnify: no transaction for %s: %w", q.Encode(), connector.ErrNotFound)
	}
	return p, err
}

func (c *client) getTransaction(ctx context.Context, req connector.Request) (connector.Response, error) {
	q := url.Values{}
	if r := str(req.Input, "payment_reference"); r != "" {
		q.Set("paymentReference", r)
	} else if r := str(req.Input, "transaction_reference"); r != "" {
		q.Set("transactionReference", r)
	} else {
		return connector.Response{}, fmt.Errorf("monnify: get_transaction needs payment_reference or transaction_reference: %w", effects.ErrFatal)
	}
	p, err := c.queryPayment(ctx, req, q)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := p.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// started reports a checkout already started under ref; its checkout URL is
// not returned by the status query.
func started(p payment, ref string) connector.Response {
	return connector.Response{Output: map[string]any{"payment_reference": ref, "transaction_reference": p.TransactionReference,
		"checkout_url": "", "status": p.PaymentStatus}}
}

func (c *client) initTransaction(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "payment_reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("monnify: init_transaction needs the engine's payment_reference: %w", effects.ErrFatal)
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("monnify: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	contract, err := c.contract(req)
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{"paymentReference": {ref}}
	if req.Attempt > 1 {
		p, err := c.queryPayment(ctx, req, q)
		if err == nil {
			return started(p, ref), nil
		}
		if !errors.Is(err, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("monnify: checking for an earlier attempt before resending: %w", err)
		}
	}
	body := map[string]any{"amount": money.Number(amount, 2), "customerName": str(in, "customer_name"), "customerEmail": str(in, "customer_email"),
		"paymentReference": ref, "paymentDescription": str(in, "description"), "currencyCode": "NGN", "contractCode": contract}
	if u := str(in, "redirect_url"); u != "" {
		body["redirectUrl"] = u
	}
	if m, ok := in["payment_methods"].([]any); ok && len(m) > 0 {
		body["paymentMethods"] = m
	}
	var r struct {
		TransactionReference string `json:"transactionReference"`
		PaymentReference     string `json:"paymentReference"`
		CheckoutURL          string `json:"checkoutUrl"`
	}
	err = c.do(ctx, req, http.MethodPost, "/api/v1/merchant/transactions/init-transaction", body, &r)
	if ae := monnifyError(err); ae != nil && ae.existing() {
		p, gerr := c.queryPayment(ctx, req, q)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("monnify refused payment reference %s (%s) and has no transaction under it: %w", ref, ae.message, effects.ErrUnknownOutcome)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return started(p, ref), nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"payment_reference": ref, "transaction_reference": r.TransactionReference,
		"checkout_url": r.CheckoutURL, "status": "PENDING"}}, nil
}
