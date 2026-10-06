// Package interswitch is the Interswitch connector (Quickteller Business):
// bank payouts from a prefunded wallet, receiving banks, name enquiry and
// Web Checkout payment confirmation. Built from Interswitch's public
// documentation (docs/integrations/interswitch.md).
//
// Auth: payouts take a bearer token from Interswitch Passport's
// client-credentials grant (Basic base64(clientId:secret)). The connector
// caches one token per credential set and Passport host until shortly
// before expires_in runs out, and fetches a new one once on a 401.
//
// Amounts: Taskiem works in kobo. Payouts take and return naira decimals;
// Web Checkout reports kobo. Conversions are exact
// (connectors/internal/money).
//
// Duplicates: Interswitch does not document what a repeated payout
// transactionReference does. Before any resend the connector asks for the
// payout by reference and reports it if it exists, and a refusal that
// mentions a duplicate is answered the same way, so a resend after a lost
// response never pays twice.
package interswitch

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Hosts are one environment's endpoints.
type Hosts struct {
	Passport string // OAuth tokens
	Payouts  string // payouts, receiving institutions, lookup
	Webpay   string // Web Checkout requery
}

var (
	// Live are the production hosts.
	Live = Hosts{Passport: "https://passport.interswitchng.com", Payouts: "https://payouts.interswitchng.com", Webpay: "https://webpay.interswitchng.com"}
	// Sandbox is used for connections whose environment is "sandbox".
	Sandbox = Hosts{Passport: "https://passport-sandbox.interswitchng.com", Payouts: "https://payouts-sandbox.interswitchng.com", Webpay: "https://sandbox.interswitchng.com"}
)

// Options configure the connector; BaseURL replaces every host of both
// environments (tests).
type Options struct {
	BaseURL string
}

// New returns the Interswitch connector.
func New(o Options) *connector.Connector { return newConnector(o, time.Now) }

// newConnector is New with the clock that times token expiry.
func newConnector(o Options, now func() time.Time) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: Live, sandbox: Sandbox, now: now, tokens: map[string]token{}}
	if o.BaseURL != "" {
		b := strings.TrimRight(o.BaseURL, "/")
		all := Hosts{Passport: b, Payouts: b, Webpay: b}
		c.live, c.sandbox = all, all
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_banks":      connector.ActionFunc(c.listBanks),
		"resolve_account": connector.ActionFunc(c.resolveAccount),
		"transfer":        connector.ActionFunc(c.transfer),
		"get_transfer":    connector.ActionFunc(c.getTransfer),
		"get_payment":     connector.ActionFunc(c.getPayment),
	}}
}

type token struct {
	value   string
	expires time.Time
}

type client struct {
	live, sandbox Hosts
	now           func() time.Time

	mu     sync.Mutex
	tokens map[string]token // by Passport host and credentials
}

func (c *client) hosts(req connector.Request) Hosts {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// apiError is a refusal Interswitch explained.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("interswitch %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("interswitch %d: %s", e.status, e.message)
}

func iswError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// errorBody reads the fields Interswitch's services use to explain a
// refusal.
func errorBody(raw []byte) (code, message string) {
	var e struct {
		ResponseCode        string `json:"responseCode"`
		ResponseDescription string `json:"responseDescription"`
		ResponseMessage     string `json:"responseMessage"`
		Message             string `json:"message"`
		Error               string `json:"error"`
		ErrorDescription    string `json:"error_description"`
		Errors              []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &e)
	code = e.ResponseCode
	for _, m := range []string{e.ResponseDescription, e.ResponseMessage, e.Message, e.ErrorDescription, e.Error} {
		if m != "" {
			message = m
			break
		}
	}
	if len(e.Errors) > 0 {
		if code == "" {
			code = e.Errors[0].Code
		}
		if message == "" {
			message = e.Errors[0].Message
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(raw))
	}
	return code, message
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// unreadable is a response whose amounts cannot be read exactly.
// Interswitch may still have acted, so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("interswitch: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func (c *client) key(req connector.Request) string {
	return c.hosts(req).Passport + "\x00" + req.Credentials["client_id"] + "\x00" + req.Credentials["client_secret"]
}

// bearer returns a cached Passport token, or asks Passport for a new one.
func (c *client) bearer(ctx context.Context, req connector.Request) (string, error) {
	id, secret := req.Credentials["client_id"], req.Credentials["client_secret"]
	if id == "" || secret == "" {
		return "", fmt.Errorf("interswitch: the connection needs client_id and client_secret: %w", effects.ErrFatal)
	}
	k := c.key(req)
	c.mu.Lock()
	t, ok := c.tokens[k]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires) {
		return t.value, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"profile"}}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.hosts(req).Passport+"/passport/oauth/token?"+form.Encode(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("interswitch passport: %w: %w", err, effects.ErrFatal)
	}
	hr.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(id+":"+secret)))
	hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hr.Header.Set("Accept", "application/json")
	resp, err := req.HTTP.Do(hr)
	if err != nil {
		// No payment has been attempted yet: whatever happened, retrying
		// the action is safe.
		return "", fmt.Errorf("interswitch passport: %w: %w", err, effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("interswitch passport: %w: %w", err, effects.ErrRetryable)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("interswitch passport %d: %w", resp.StatusCode, effects.ErrRetryable)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, msg := errorBody(raw)
		return "", fmt.Errorf("interswitch passport refused the client credentials (%d: %s): %w", resp.StatusCode, msg, effects.ErrFatal)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.Unmarshal(raw, &body) != nil || body.AccessToken == "" {
		return "", fmt.Errorf("interswitch passport returned no token: %w", effects.ErrRetryable)
	}
	life := time.Duration(body.ExpiresIn) * time.Second
	if life <= 0 {
		life = 5 * time.Minute
	}
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

// payouts sends one request to the payouts service with a bearer token. A
// 401 renews the token once: Interswitch refused before acting. Failures
// keep the transport's classification and carry *apiError and the raw body
// in rawErr.
func (c *client) payouts(ctx context.Context, req connector.Request, method, path string, body, out any) (rawErr []byte, err error) {
	for try := 0; ; try++ {
		tok, err := c.bearer(ctx, req)
		if err != nil {
			return nil, err
		}
		err = connector.DoJSON(ctx, req.HTTP, method, c.hosts(req).Payouts+"/api/v1/payouts"+path, map[string]string{"Authorization": "Bearer " + tok}, body, out)
		var he *connector.HTTPError
		if !errors.As(err, &he) {
			return nil, err
		}
		if he.Status == http.StatusUnauthorized && try == 0 {
			c.forget(req)
			continue
		}
		code, msg := errorBody(he.Body)
		return he.Body, fmt.Errorf("%w: %w", &apiError{status: he.Status, code: code, message: msg}, he)
	}
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	path := "/receiving-institutions"
	if n := str(req.Input, "name"); n != "" {
		path += "?" + url.Values{"name": {n}}.Encode()
	}
	var banks []struct {
		Code    string `json:"code"`
		Name    string `json:"name"`
		NIPCode string `json:"nipCode"`
		CBNCode string `json:"cbnCode"`
	}
	if _, err := c.payouts(ctx, req, http.MethodGet, path, nil, &banks); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(banks))
	for i, b := range banks {
		out[i] = map[string]any{"code": b.Code, "name": b.Name, "nip_code": b.NIPCode, "cbn_code": b.CBNCode}
	}
	return connector.Response{Output: map[string]any{"banks": out}}, nil
}

// lookupRef is a fresh reference for a name enquiry, which Interswitch
// requires and which never becomes a payout (payouts are sent with
// singleCall, so they do their own lookup).
func lookupRef() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "tsk-lookup-" + hex.EncodeToString(b)
}

func (c *client) resolveAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	number, bank := str(req.Input, "account_number"), str(req.Input, "bank_code")
	if number == "" || bank == "" {
		return connector.Response{}, fmt.Errorf("interswitch: resolve_account needs account_number and bank_code: %w", effects.ErrFatal)
	}
	body := map[string]any{"payoutChannel": "BANK_TRANSFER", "transactionReference": lookupRef(),
		"recipient": map[string]any{"recipientAccount": number, "recipientBank": bank, "currencyCode": "NGN"}}
	// The lookup's response is not in Interswitch's documentation; these
	// are the names its payout records use for the same values.
	var r struct {
		RecipientName    string `json:"recipientName"`
		AccountName      string `json:"accountName"`
		RecipientAccount string `json:"recipientAccount"`
		RecipientBank    string `json:"recipientBank"`
		ResponseCode     string `json:"responseCode"`
		Description      string `json:"responseDescription"`
		Recipient        *struct {
			RecipientName    string `json:"recipientName"`
			RecipientAccount string `json:"recipientAccount"`
			RecipientBank    string `json:"recipientBank"`
		} `json:"recipient"`
	}
	if _, err := c.payouts(ctx, req, http.MethodPost, "/customer-lookup", body, &r); err != nil {
		return connector.Response{}, err
	}
	name := r.RecipientName
	if name == "" && r.Recipient != nil {
		name = r.Recipient.RecipientName
	}
	if name == "" {
		name = r.AccountName
	}
	if name == "" {
		return connector.Response{}, fmt.Errorf("interswitch: no account name for %s at bank %s (%s %s): %w", number, bank, r.ResponseCode, r.Description, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"account_name": name, "account_number": number, "bank_code": bank}}, nil
}

// payout is a payout transaction as Interswitch returns it.
type payout struct {
	ID                   any    `json:"id"`
	Reference            string `json:"reference"`
	TransactionReference string `json:"transactionReference"`
	RecipientAccount     string `json:"recipientAccount"`
	RecipientBank        string `json:"recipientBank"`
	RecipientName        string `json:"recipientName"`
	Amount               any    `json:"amount"`
	Fee                  any    `json:"fee"`
	ResponseCode         string `json:"responseCode"`
	ResponseDescription  string `json:"responseDescription"`
	Status               string `json:"status"`
	ProcessingReference  string `json:"processingReference"`
	Reversed             bool   `json:"reversed"`
}

func (p payout) output(reference string) (map[string]any, error) {
	var rd money.Reader
	id := ""
	switch v := p.ID.(type) {
	case string:
		id = v
	case float64:
		id = strconv.FormatFloat(v, 'f', -1, 64)
	}
	status := p.Status
	if status == "" && p.ResponseCode == "09" {
		status = "PROCESSING"
	}
	out := map[string]any{"reference": reference, "status": status, "payout_id": id, "amount": rd.Minor(p.Amount, 2),
		"fee": rd.Ceil(p.Fee, 2), "account_name": p.RecipientName, "account_number": p.RecipientAccount, "bank_code": p.RecipientBank,
		"response_code": p.ResponseCode, "response_description": p.ResponseDescription,
		"processing_reference": p.ProcessingReference, "reversed": p.Reversed}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// settled fails the step for a payout Interswitch reports FAILED:
// Interswitch reverses any debit, and its description is the error.
func settled(out map[string]any) (connector.Response, error) {
	switch out["status"] {
	case "FAILED":
		return connector.Response{}, fmt.Errorf("interswitch: payout %s failed (code %s): %s: %w", out["reference"], out["response_code"], out["response_description"], effects.ErrFatal)
	case "PROCESSING", "SUCCESSFUL":
		return connector.Response{Output: out}, nil
	}
	return connector.Response{}, fmt.Errorf("interswitch: payout %s has status %q (code %s), which Interswitch does not document: %w",
		out["reference"], out["status"], out["response_code"], effects.ErrUnknownOutcome)
}

// failedCodes are the payout error codes Interswitch documents as FAILED.
var failedCodes = map[string]bool{"59": true, "61": true, "51": true, "52": true, "53": true, "13": true, "20": true}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("interswitch: transfer needs the engine's reference: %w", effects.ErrFatal)
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("interswitch: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	if str(in, "account_number") == "" || str(in, "bank_code") == "" {
		return connector.Response{}, fmt.Errorf("interswitch: transfer needs account_number and bank_code: %w", effects.ErrFatal)
	}
	wallet := str(in, "wallet_id")
	if wallet == "" {
		wallet = req.Credentials["wallet_id"]
	}
	if wallet == "" || req.Credentials["wallet_pin"] == "" {
		return connector.Response{}, fmt.Errorf("interswitch: payouts need the connection's wallet_id and wallet_pin: %w", effects.ErrFatal)
	}
	if req.Attempt > 1 {
		// A resend: ask first. Interswitch does not document what a
		// repeated transactionReference does.
		out, err := c.byReference(ctx, req, ref)
		if err == nil {
			return settled(out)
		}
		if !errors.Is(err, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("interswitch: checking for an earlier attempt before resending: %w", err)
		}
	}
	body := map[string]any{"transactionReference": ref, "payoutChannel": "BANK_TRANSFER", "currencyCode": "NGN",
		"amount": money.Number(amount, 2), "narration": str(in, "narration"),
		"walletDetails": map[string]any{"pin": req.Credentials["wallet_pin"], "walletId": wallet},
		"recipient":     map[string]any{"recipientAccount": str(in, "account_number"), "recipientBank": str(in, "bank_code"), "currencyCode": "NGN"},
		"singleCall":    true}
	if s := str(in, "source_account_name"); s != "" {
		body["sourceAccountName"] = s
	}
	if s := str(in, "source_account_number"); s != "" {
		body["sourceAccountNumber"] = s
	}
	var p payout
	raw, err := c.payouts(ctx, req, http.MethodPost, "", body, &p)
	if ae := iswError(err); ae != nil {
		var refused payout
		_ = json.Unmarshal(raw, &refused)
		m := strings.ToLower(ae.message)
		switch {
		case ae.status < 500 && (ae.status == http.StatusConflict || strings.Contains(m, "duplicate") || strings.Contains(m, "already exist")):
			// Already sent under this reference: report that payout.
			found, gerr := c.byReference(ctx, req, ref)
			if errors.Is(gerr, connector.ErrNotFound) {
				return connector.Response{}, fmt.Errorf("interswitch refused reference %s (%s) and has no payout under it: %w", ref, ae.message, effects.ErrUnknownOutcome)
			}
			if gerr != nil {
				return connector.Response{}, gerr
			}
			return settled(found)
		case refused.Status == "FAILED" || (ae.status < 500 && failedCodes[ae.code]):
			return connector.Response{}, fmt.Errorf("interswitch: payout %s refused: %s: %w", ref, ae.Error(), effects.ErrFatal)
		case ae.code == "09" || refused.Status == "PROCESSING":
			// "Transaction not yet final, call status endpoint."
			return connector.Response{}, fmt.Errorf("interswitch: payout %s is processing (%s); its outcome is not known yet: %w", ref, ae.Error(), effects.ErrUnknownOutcome)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	if p.Status == "" && p.ResponseCode == "" {
		return connector.Response{}, fmt.Errorf("interswitch: payout %s answered without a status: %w", ref, effects.ErrUnknownOutcome)
	}
	out, err := p.output(ref)
	if err != nil {
		return connector.Response{}, err
	}
	return settled(out)
}

func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	if ref == "" {
		return nil, fmt.Errorf("interswitch: reference is required: %w", effects.ErrFatal)
	}
	var p payout
	_, err := c.payouts(ctx, req, http.MethodGet, "/"+url.PathEscape(ref), nil, &p)
	if ae := iswError(err); ae != nil && ae.status == http.StatusNotFound {
		return nil, fmt.Errorf("interswitch: no payout with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if p.Status == "" && p.ResponseCode == "" && p.TransactionReference == "" {
		return nil, fmt.Errorf("interswitch: no payout with reference %s: %w", ref, connector.ErrNotFound)
	}
	return p.output(ref)
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

// paymentStatus groups Interswitch's payment response codes.
func paymentStatus(code string) string {
	switch code {
	case "00", "11":
		return "successful"
	case "10":
		return "partial"
	case "09", "S0", "Z0":
		return "pending"
	}
	return "failed"
}

func (c *client) getPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref := str(req.Input, "reference")
	amount, err := money.Minor(req.Input["amount"])
	if ref == "" || err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("interswitch: get_payment needs reference and the expected amount in kobo: %w", effects.ErrFatal)
	}
	merchant := str(req.Input, "merchant_code")
	if merchant == "" {
		merchant = req.Credentials["merchant_code"]
	}
	if merchant == "" {
		return connector.Response{}, fmt.Errorf("interswitch: no merchant_code in the step or the connection: %w", effects.ErrFatal)
	}
	q := url.Values{"merchantcode": {merchant}, "transactionreference": {ref}, "amount": {strconv.FormatInt(amount, 10)}}
	var p struct {
		Amount                   any    `json:"Amount"`
		MerchantReference        string `json:"MerchantReference"`
		PaymentReference         string `json:"PaymentReference"`
		RetrievalReferenceNumber string `json:"RetrievalReferenceNumber"`
		TransactionDate          string `json:"TransactionDate"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
	}
	// Documented without authentication.
	err = connector.DoJSON(ctx, req.HTTP, http.MethodGet, c.hosts(req).Webpay+"/collections/api/v1/gettransaction.json?"+q.Encode(), nil, nil, &p)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		code, msg := errorBody(he.Body)
		var body struct {
			ResponseCode        string `json:"ResponseCode"`
			ResponseDescription string `json:"ResponseDescription"`
		}
		if json.Unmarshal(he.Body, &body) == nil && body.ResponseCode != "" {
			code, msg = body.ResponseCode, body.ResponseDescription
		}
		if code == "Z25" || he.Status == http.StatusNotFound {
			return connector.Response{}, fmt.Errorf("interswitch: no transaction %s (%s): %w: %w", ref, msg, connector.ErrNotFound, effects.ErrFatal)
		}
		return connector.Response{}, fmt.Errorf("%w: %w", &apiError{status: he.Status, code: code, message: msg}, he)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if p.ResponseCode == "Z25" {
		return connector.Response{}, fmt.Errorf("interswitch: no transaction %s (%s): %w: %w", ref, p.ResponseDescription, connector.ErrNotFound, effects.ErrFatal)
	}
	var rd money.Reader
	got := rd.Minor(p.Amount, 0)
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	if p.ResponseCode == "" {
		return connector.Response{}, fmt.Errorf("interswitch: transaction %s answered without a response code: %w", ref, effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"reference": ref, "status": paymentStatus(p.ResponseCode), "amount": got,
		"amount_matches": got == amount, "response_code": p.ResponseCode, "response_description": p.ResponseDescription,
		"payment_reference": p.PaymentReference, "retrieval_reference": p.RetrievalReferenceNumber, "transaction_date": p.TransactionDate}}, nil
}
