// Package moniepointpos is the Moniepoint POS terminals connector: the
// transactions a business's terminals make, delivered as signed webhook
// events, and payment requests pushed to a terminal. It is Moniepoint's POS
// API, a different product from Monnify (package moniepoint): a shop with
// terminals has a Moniepoint Business account. Built from Moniepoint's
// public POS developer documentation (docs/integrations/moniepoint-pos.md).
//
// Auth: POST /v1/auth with the client id and secret answers a bearer token
// with expiresIn seconds. The connector caches one token per credential set
// and host until shortly before it expires, and signs in again once if
// Moniepoint answers 401.
//
// Amounts: integer kobo, as the webhook documents ("smallest currency
// units") and as the push request takes them.
//
// Duplicates: a merchantReference Moniepoint has seen is refused with 400
// "Transaction exists", after which the connector looks the request up by
// reference and reports it, so resending after a lost response never asks
// a terminal twice.
package moniepointpos

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
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

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox": the
// development host Moniepoint's push-payment reference links to.
const SandboxURL = "https://moniepoint-pos-backend-service.development.moniepoint.com"

// Options configure the connector; BaseURL overrides both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the Moniepoint POS connector.
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
		"check_access":    connector.ActionFunc(c.checkAccess),
		"request_payment": connector.ActionFunc(c.requestPayment),
		"get_transaction": connector.ActionFunc(c.getTransaction),
	}, Verifiers: map[string]connector.WebhookVerifier{"transaction": verifyDelivery}}
}

type token struct {
	value   string
	expires time.Time
	life    time.Duration
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

// apiError is Moniepoint's error body: {"message", "errors": [...],
// "status": "BAD_REQUEST"} (or the gateway's {"error": "..."} on a 401).
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("moniepoint pos: http %d", e.status)
	}
	return fmt.Sprintf("moniepoint pos: %s (http %d)", e.message, e.status)
}

func (e *apiError) notFound() bool { return e != nil && e.status == http.StatusNotFound }

// exists is the refusal of a merchantReference Moniepoint has seen.
func (e *apiError) exists() bool {
	return e != nil && e.status == http.StatusBadRequest && strings.Contains(strings.ToLower(e.message), "exist")
}

func posError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func message(body []byte) string {
	var e struct {
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
		Error   string   `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	switch {
	case e.Message != "":
		return e.Message
	case len(e.Errors) > 0:
		return strings.Join(e.Errors, "; ")
	case e.Error != "":
		return e.Error
	}
	return strings.TrimSpace(string(body))
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func (c *client) key(req connector.Request) string {
	return c.base(req) + "\x00" + req.Credentials["client_id"] + "\x00" + req.Credentials["client_secret"]
}

// bearer returns a cached token, or signs in for a new one.
func (c *client) bearer(ctx context.Context, req connector.Request) (token, error) {
	id, secret := strings.TrimSpace(req.Credentials["client_id"]), strings.TrimSpace(req.Credentials["client_secret"])
	if id == "" || secret == "" {
		return token{}, fmt.Errorf("moniepoint pos: the connection needs client_id and client_secret: %w", effects.ErrFatal)
	}
	k := c.key(req)
	c.mu.Lock()
	t, ok := c.tokens[k]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires) {
		return t, nil
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int64  `json:"expiresIn"`
	}
	err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base(req)+"/v1/auth", nil, map[string]string{"clientId": id, "clientSecret": secret}, &body)
	if err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			// Signing in sends nothing to anyone: whatever went wrong, the
			// action has not been attempted.
			if he.Status >= 500 || he.Status == http.StatusTooManyRequests {
				return token{}, fmt.Errorf("moniepoint pos sign-in: %s: %w", message(he.Body), effects.ErrRetryable)
			}
			return token{}, fmt.Errorf("moniepoint pos sign-in refused (check the client ID and secret): %s: %w", message(he.Body), effects.ErrFatal)
		}
		return token{}, fmt.Errorf("moniepoint pos sign-in: %w: %w", err, effects.ErrRetryable)
	}
	if body.AccessToken == "" {
		return token{}, fmt.Errorf("moniepoint pos sign-in returned no token: %w", effects.ErrRetryable)
	}
	life := time.Duration(body.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	t = token{value: body.AccessToken, life: life}
	// Renew a minute early so a token never expires in flight.
	if life > 2*time.Minute {
		life -= time.Minute
	}
	t.expires = c.now().Add(life)
	c.mu.Lock()
	c.tokens[k] = t
	c.mu.Unlock()
	return t, nil
}

func (c *client) forget(req connector.Request) {
	c.mu.Lock()
	delete(c.tokens, c.key(req))
	c.mu.Unlock()
}

// do sends one request with a bearer token and decodes the answer into
// out. A 401 renews the token once: Moniepoint refused before acting.
// Failures keep the transport's classification and carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	for try := 0; ; try++ {
		tok, err := c.bearer(ctx, req)
		if err != nil {
			return err
		}
		err = connector.DoJSON(ctx, req.HTTP, method, c.base(req)+path, map[string]string{"Authorization": "Bearer " + tok.value}, body, out)
		var he *connector.HTTPError
		if errors.As(err, &he) {
			if he.Status == http.StatusUnauthorized && try == 0 {
				c.forget(req)
				continue
			}
			return fmt.Errorf("%w: %w", &apiError{status: he.Status, message: message(he.Body)}, he)
		}
		return err
	}
}

func (c *client) checkAccess(ctx context.Context, req connector.Request) (connector.Response, error) {
	t, err := c.bearer(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"ok": true, "expires_in": int64(t.life / time.Second)}}, nil
}

// status is Moniepoint's answer to GET /v1/transactions/merchants/{ref}.
type status struct {
	ClientID             string `json:"clientId"`
	BusinessOwnerID      *int64 `json:"businessOwnerId"`
	TerminalSerial       string `json:"terminalSerial"`
	RequestAmount        *int64 `json:"requestAmount"`
	TransactionReference string `json:"transactionReference"`
	MerchantReference    string `json:"merchantReference"`
	TransactionType      string `json:"transactionType"`
	RequestPaymentMethod string `json:"requestPaymentMethod"`
	ActualPaymentMethod  string `json:"actualPaymentMethod"`
	ActualAmount         *int64 `json:"actualAmount"`
	ProcessingStatus     string `json:"processingStatus"`
	ResponseCode         string `json:"responseCode"`
	ResponseMessage      string `json:"responseMessage"`
}

func (s status) output() map[string]any {
	out := map[string]any{
		"merchant_reference": s.MerchantReference, "terminal_serial": s.TerminalSerial,
		"transaction_reference": s.TransactionReference, "transaction_type": s.TransactionType,
		"requested_payment_method": s.RequestPaymentMethod, "actual_payment_method": s.ActualPaymentMethod,
		"processing_status": s.ProcessingStatus, "response_code": s.ResponseCode, "response_message": s.ResponseMessage,
	}
	if s.RequestAmount != nil {
		out["request_amount"] = *s.RequestAmount
	}
	if s.ActualAmount != nil {
		out["actual_amount"] = *s.ActualAmount
	}
	if s.BusinessOwnerID != nil {
		out["business_owner_id"] = *s.BusinessOwnerID
	}
	return out
}

// byReference fetches a payment request; ErrNotFound when Moniepoint has
// none under the reference.
func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (status, error) {
	var s status
	err := c.do(ctx, req, http.MethodGet, "/v1/transactions/merchants/"+url.PathEscape(ref), nil, &s)
	if ae := posError(err); ae.notFound() {
		return status{}, fmt.Errorf("moniepoint pos: no payment request with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return status{}, err
	}
	if s.MerchantReference == "" {
		s.MerchantReference = ref
	}
	return s, nil
}

func (c *client) getTransaction(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref := str(req.Input, "merchant_reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("moniepoint pos: get_transaction needs merchant_reference: %w", effects.ErrFatal)
	}
	s, err := c.byReference(ctx, req, ref)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: s.output()}, nil
}

func (c *client) requestPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "merchant_reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("moniepoint pos: request_payment needs the engine's merchant_reference: %w", effects.ErrFatal)
	}
	serial := str(in, "terminal_serial")
	if serial == "" {
		return connector.Response{}, fmt.Errorf("moniepoint pos: request_payment needs terminal_serial: %w", effects.ErrFatal)
	}
	amount, ok := kobo(in["amount"])
	if !ok || amount < 1 {
		return connector.Response{}, fmt.Errorf("moniepoint pos: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	method := strings.ToUpper(str(in, "payment_method"))
	if method == "" {
		method = "ANY"
	}
	switch method {
	case "CARD_PURCHASE", "POS_TRANSFER", "ANY":
	default:
		return connector.Response{}, fmt.Errorf("moniepoint pos: payment_method must be CARD_PURCHASE, POS_TRANSFER or ANY, got %q: %w", method, effects.ErrFatal)
	}
	body := map[string]any{"terminalSerial": serial, "amount": amount, "merchantReference": ref, "transactionType": "PURCHASE", "paymentMethod": method}
	err := c.do(ctx, req, http.MethodPost, "/v1/transactions", body, nil)
	ae := posError(err)
	switch {
	case ae.exists():
		// Already asked under this reference (a resend after a lost
		// answer): report the request that exists.
		s, gerr := c.byReference(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			// Refused, yet not listed: never assume it did not happen.
			return connector.Response{}, fmt.Errorf("moniepoint pos refused reference %s as existing and has no request under it: %w", ref, effects.ErrUnknownOutcome)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return connector.Response{Output: s.output()}, nil
	case ae != nil && ae.status < 500:
		// A refusal: the terminal was not asked.
		return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	case err != nil:
		return connector.Response{}, err
	}
	// Accepted (202, no body). The request is PENDING until the terminal
	// answers; say so from Moniepoint's own record when it can be read,
	// else from what was sent: the push succeeded either way.
	if s, gerr := c.byReference(ctx, req, ref); gerr == nil {
		return connector.Response{Output: s.output()}, nil
	}
	return connector.Response{Output: map[string]any{"merchant_reference": ref, "terminal_serial": serial, "request_amount": amount,
		"transaction_type": "PURCHASE", "requested_payment_method": method, "processing_status": "PENDING"}}, nil
}

// kobo reads a whole number from JSON (float64 or json.Number) or Go ints.
func kobo(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if x != float64(int64(x)) {
			return 0, false
		}
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	}
	return 0, false
}

const (
	headerID        = "moniepoint-webhook-id"
	headerTimestamp = "moniepoint-webhook-timestamp"
	headerSignature = "moniepoint-webhook-signature"
)

// Signature is the signature Moniepoint puts in moniepoint-webhook-signature:
// the Base64 HMAC-SHA256, keyed with the subscription's secret, of the
// webhook id, the timestamp and the raw body joined with "__".
func Signature(secret, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id + "__" + timestamp + "__"))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// verifyDelivery checks a delivery to the transaction trigger.
func verifyDelivery(secret string, h http.Header, body []byte) error {
	id, ts, sig := h.Get(headerID), h.Get(headerTimestamp), h.Get(headerSignature)
	if secret == "" || id == "" || ts == "" || sig == "" {
		return connector.ErrBadSignature
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return connector.ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id + "__" + ts + "__"))
	mac.Write(body)
	if subtle.ConstantTimeCompare(got, mac.Sum(nil)) != 1 {
		return connector.ErrBadSignature
	}
	return nil
}
