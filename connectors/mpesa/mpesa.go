// Package mpesa is the M-Pesa connector, built on Safaricom's Daraja APIs:
// M-Pesa Express (STK push) and its query, C2B URL registration, B2C
// payments, transaction status, account balance and reversals. Built from
// Safaricom's public Daraja documentation (docs/integrations/mpesa.md).
//
// Auth: Daraja issues an hour-long bearer token from
// /oauth/v1/generate?grant_type=client_credentials against Basic
// base64(consumerKey:consumerSecret). A new token invalidates the previous
// one, so the connector caches one token per credential set and host until
// shortly before it expires, and fetches a new one once if Daraja says the
// token is invalid (another process may have replaced it).
//
// Asynchronous results: STK push, B2C, status, balance and reversal answer
// only "accepted"; M-Pesa posts the result to the callback URLs in the
// request. The connector builds them from the connection's hooks_url,
// adding the callback_token (query_secret verification: Daraja signs
// nothing) and the reference the result trigger correlates on.
//
// Amounts: Taskiem works in KES cents; Daraja takes whole shillings, so
// amounts that are not whole shillings are refused before sending.
//
// Duplicates: B2C requests carry the engine's key as
// OriginatorConversationID, which M-Pesa refuses to see twice; a repeat is
// reported as already accepted, never paid twice. STK push and reversals
// take no such reference: they are unsafe writes, parked when an answer is
// lost.
package mpesa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://sandbox.safaricom.co.ke"

// eat is Kenya's time zone (UTC+3, no daylight saving), for the STK
// password timestamp.
var eat = time.FixedZone("EAT", 3*60*60)

// Options configure the connector; BaseURL overrides both environments
// (tests, a local fake).
type Options struct {
	BaseURL string
}

// New returns the M-Pesa (Daraja) connector.
func New(o Options) *connector.Connector { return newConnector(o, time.Now, rand.Reader) }

func newConnector(o Options, now func() time.Time, random io.Reader) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL, now: now, random: random, tokens: map[string]token{}}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"check_credentials":  connector.ActionFunc(c.checkCredentials),
		"stk_push":           connector.ActionFunc(c.stkPush),
		"stk_query":          connector.ActionFunc(c.stkQuery),
		"register_c2b_urls":  connector.ActionFunc(c.registerC2B),
		"b2c_payment":        connector.ActionFunc(c.b2cPayment),
		"transaction_status": connector.ActionFunc(c.transactionStatus),
		"account_balance":    connector.ActionFunc(c.accountBalance),
		"reversal":           connector.ActionFunc(c.reversal),
	}}
}

type token struct {
	value   string
	expires time.Time
}

type client struct {
	live, sandbox string
	now           func() time.Time
	random        io.Reader

	mu     sync.Mutex
	tokens map[string]token // by host and credentials
}

func (c *client) sandboxed(req connector.Request) bool {
	return strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox")
}

func (c *client) base(req connector.Request) string {
	if c.sandboxed(req) {
		return c.sandbox
	}
	return c.live
}

// apiError is a refusal Daraja explained ({requestId, errorCode,
// errorMessage}).
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("daraja %d (%s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("daraja %d: %s", e.status, e.message)
}

func darajaError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// invalidToken: Daraja's codes for a token it does not accept (expired, or
// replaced by a newer one).
func (e *apiError) invalidToken() bool {
	if e == nil {
		return false
	}
	switch e.code {
	case "404.001.03", "400.003.01", "401.002.01":
		return true
	}
	return e.status == http.StatusUnauthorized
}

// refusedBeforeProcessing: Daraja turned the request away before M-Pesa
// acted ("system is busy", spike arrest, quota): safe to send again.
func (e *apiError) refusedBeforeProcessing() bool {
	if e == nil {
		return false
	}
	m := strings.ToLower(e.message)
	return e.code == "500.003.02" || e.code == "500.003.03" ||
		strings.Contains(m, "spike arrest") || strings.Contains(m, "quota violation") || strings.Contains(m, "system is busy")
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

// text reads a field Daraja sends as a string or a number.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("mpesa: "+format+": %w", append(args, effects.ErrFatal)...)
}

func (c *client) key(req connector.Request) string {
	return c.base(req) + "\x00" + req.Credentials["consumer_key"] + "\x00" + req.Credentials["consumer_secret"]
}

// bearer returns a cached token, or generates a new one.
func (c *client) bearer(ctx context.Context, req connector.Request) (string, error) {
	ck, cs := req.Credentials["consumer_key"], req.Credentials["consumer_secret"]
	if ck == "" || cs == "" {
		return "", fatalf("the connection needs consumer_key and consumer_secret")
	}
	k := c.key(req)
	c.mu.Lock()
	t, ok := c.tokens[k]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires) {
		return t.value, nil
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"` // "3599" or 3599
	}
	basic := base64.StdEncoding.EncodeToString([]byte(ck + ":" + cs))
	err := connector.DoJSON(ctx, req.HTTP, http.MethodGet, c.base(req)+"/oauth/v1/generate?grant_type=client_credentials",
		map[string]string{"Authorization": "Basic " + basic}, nil, &body)
	if err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			ae := parseError(he)
			// The token request sends nothing to anyone: whatever went
			// wrong, the action has not been attempted.
			if he.Status >= 500 || he.Status == http.StatusTooManyRequests {
				return "", fmt.Errorf("daraja token: %s: %w", ae.Error(), effects.ErrRetryable)
			}
			return "", fmt.Errorf("daraja refused the app's keys (check consumer_key, consumer_secret and environment): %s: %w", ae.Error(), effects.ErrFatal)
		}
		return "", fmt.Errorf("daraja token: %w: %w", err, effects.ErrRetryable)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("daraja returned no access token: %w", effects.ErrRetryable)
	}
	secs, _ := strconv.Atoi(strings.TrimSpace(text(body.ExpiresIn)))
	life := time.Duration(secs) * time.Second
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

func parseError(he *connector.HTTPError) *apiError {
	var e struct {
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	_ = json.Unmarshal(he.Body, &e)
	ae := &apiError{status: he.Status, code: e.ErrorCode, message: e.ErrorMessage}
	if ae.message == "" {
		ae.message = strings.TrimSpace(string(he.Body))
	}
	return ae
}

// post sends one API request with a bearer token. A token Daraja does not
// accept is replaced once: Daraja refused before acting. Failures keep the
// transport's classification and carry *apiError; refusals Daraja documents
// as "try again" become not_sent.
func (c *client) post(ctx context.Context, req connector.Request, path string, body, out any) error {
	for try := 0; ; try++ {
		tok, err := c.bearer(ctx, req)
		if err != nil {
			return err
		}
		err = connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base(req)+path, map[string]string{"Authorization": "Bearer " + tok}, body, out)
		var he *connector.HTTPError
		if !errors.As(err, &he) {
			return err
		}
		ae := parseError(he)
		if ae.invalidToken() && try == 0 {
			c.forget(req)
			continue
		}
		if ae.invalidToken() {
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		}
		if ae.refusedBeforeProcessing() {
			return fmt.Errorf("%w: %w", ae, effects.ErrNotSent)
		}
		return fmt.Errorf("%w: %w", ae, he)
	}
}

// ack is Daraja's synchronous answer to an asynchronous request.
type ack struct {
	OriginatorConversationID string `json:"OriginatorConversationID"`
	OriginatorCoversationID  string `json:"OriginatorCoversationID"` // C2B register spells it so
	ConversationID           string `json:"ConversationID"`
	ResponseCode             any    `json:"ResponseCode"`
	ResponseDescription      string `json:"ResponseDescription"`
}

func (a ack) originator() string {
	if a.OriginatorConversationID != "" {
		return a.OriginatorConversationID
	}
	return a.OriginatorCoversationID
}

// accepted checks ResponseCode 0; anything else is a refusal in a 200.
func (a ack) accepted() error {
	if code := text(a.ResponseCode); code != "0" && code != "" {
		return &apiError{status: http.StatusOK, code: code, message: a.ResponseDescription}
	}
	return nil
}

// shillings converts KES cents to the whole shillings Daraja takes.
func shillings(v any) (string, error) {
	cents, err := money.Minor(v)
	if err != nil || cents < 100 {
		return "", fatalf("amount must be a whole number of KES cents, at least 100 (KES 1), got %v", v)
	}
	if cents%100 != 0 {
		return "", fatalf("Daraja takes whole shillings; %d cents is KES %s", cents, money.Format(cents, 2))
	}
	return strconv.FormatInt(cents/100, 10), nil
}

var msisdnRE = regexp.MustCompile(`^254\d{9}$`)

// msisdn checks a Kenyan number in Daraja's 2547XXXXXXXX form, accepting a
// leading + and spaces.
func msisdn(v string) (string, error) {
	n := strings.TrimPrefix(strings.NewReplacer(" ", "", "-", "").Replace(v), "+")
	if !msisdnRE.MatchString(n) {
		return "", fatalf("phone must be a Kenyan number in the form 2547XXXXXXXX (or 2541XXXXXXXX)")
	}
	return n, nil
}

// hookURL is the URL M-Pesa calls back: the connection's hooks_url with the
// trigger appended to its path, the callback token, and extra parameters.
func hookURL(req connector.Request, trigger string, extra url.Values) (string, error) {
	base, tok := strings.TrimSpace(req.Credentials["hooks_url"]), req.Credentials["callback_token"]
	if base == "" || tok == "" {
		return "", fatalf("the connection needs hooks_url and callback_token to receive M-Pesa's results (or the step must give the callback URL)")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fatalf("hooks_url is not a URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + trigger
	q := u.Query()
	for k, v := range extra {
		q[k] = v
	}
	q.Set("token", tok)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// asyncURLs are the ResultURL and QueueTimeOutURL of an asynchronous
// request, both carrying kind and the reference results correlate on.
func asyncURLs(req connector.Request, trigger, kind, ref string) (result, timeout string, err error) {
	extra := url.Values{"kind": {kind}}
	if ref != "" {
		extra.Set("ref", ref)
	}
	if result, err = hookURL(req, trigger, extra); err != nil {
		return "", "", err
	}
	timeout, err = hookURL(req, "queue_timeout", extra)
	return result, timeout, err
}

func shortcode(req connector.Request) (string, error) {
	if s := strings.TrimSpace(req.Credentials["shortcode"]); s != "" {
		return s, nil
	}
	return "", fatalf("the connection has no shortcode")
}

func b2cShortcode(req connector.Request) (string, error) {
	if s := strings.TrimSpace(req.Credentials["b2c_shortcode"]); s != "" {
		return s, nil
	}
	return shortcode(req)
}

// initiator returns the API operator and its security credential: the one
// the connection holds already encrypted, or initiator_password encrypted
// with Safaricom's certificate (RSA, PKCS #1 v1.5 padding, base64), as
// Daraja documents.
func (c *client) initiator(req connector.Request) (name, credential string, err error) {
	name = strings.TrimSpace(req.Credentials["initiator_name"])
	if name == "" {
		return "", "", fatalf("the connection has no initiator_name")
	}
	if sc := strings.TrimSpace(req.Credentials["security_credential"]); sc != "" {
		return name, sc, nil
	}
	pw, certPEM := req.Credentials["initiator_password"], req.Credentials["certificate"]
	if pw == "" || strings.TrimSpace(certPEM) == "" {
		return "", "", fatalf("the connection needs security_credential, or initiator_password and Safaricom's certificate")
	}
	pub, err := publicKey(certPEM)
	if err != nil {
		return "", "", err
	}
	//nolint:staticcheck // Daraja requires PKCS #1 v1.5 padding ("not OAEP") for the security credential.
	enc, err := rsa.EncryptPKCS1v15(c.random, pub, []byte(pw))
	if err != nil {
		return "", "", fatalf("encrypting the initiator password: %v", err)
	}
	return name, base64.StdEncoding.EncodeToString(enc), nil
}

func publicKey(certPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(certPEM)))
	if block == nil {
		return nil, fatalf("certificate is not PEM")
	}
	var key any
	switch block.Type {
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fatalf("certificate: %v", err)
		}
		key = cert.PublicKey
	case "PUBLIC KEY":
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fatalf("certificate: %v", err)
		}
		key = k
	default:
		return nil, fatalf("certificate: unexpected PEM block %q", block.Type)
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fatalf("certificate does not hold an RSA key")
	}
	return pub, nil
}

func (c *client) checkCredentials(ctx context.Context, req connector.Request) (connector.Response, error) {
	if _, err := c.bearer(ctx, req); err != nil {
		return connector.Response{}, err
	}
	env := "production"
	if c.sandboxed(req) {
		env = "sandbox"
	}
	return connector.Response{Output: map[string]any{"ok": true, "environment": env}}, nil
}

// password is the STK password and its timestamp:
// base64(shortcode + passkey + timestamp), timestamp YYYYMMDDHHmmss.
func (c *client) password(req connector.Request, code string) (pw, ts string, err error) {
	pk := req.Credentials["passkey"]
	if pk == "" {
		return "", "", fatalf("the connection has no passkey (Lipa na M-Pesa Online)")
	}
	ts = c.now().In(eat).Format("20060102150405")
	return base64.StdEncoding.EncodeToString([]byte(code + pk + ts)), ts, nil
}

func (c *client) stkPush(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	amount, err := shillings(in["amount"])
	if err != nil {
		return connector.Response{}, err
	}
	phone, err := msisdn(str(in, "phone"))
	if err != nil {
		return connector.Response{}, err
	}
	ref := str(in, "account_reference")
	if ref == "" || len(ref) > 12 {
		return connector.Response{}, fatalf("account_reference is required, 12 characters at most")
	}
	desc := str(in, "description")
	if len(desc) > 13 {
		return connector.Response{}, fatalf("description is 13 characters at most")
	}
	if desc == "" {
		desc = ref
	}
	code, err := shortcode(req)
	if err != nil {
		return connector.Response{}, err
	}
	pw, ts, err := c.password(req, code)
	if err != nil {
		return connector.Response{}, err
	}
	kind := str(in, "transaction_type")
	if kind == "" {
		kind = "CustomerPayBillOnline"
	}
	if kind != "CustomerPayBillOnline" && kind != "CustomerBuyGoodsOnline" {
		return connector.Response{}, fatalf("transaction_type must be CustomerPayBillOnline or CustomerBuyGoodsOnline")
	}
	partyB := str(in, "party_b")
	if partyB == "" {
		partyB = code
	}
	cb := str(in, "callback_url")
	if cb == "" {
		if cb, err = hookURL(req, "stk_callback", nil); err != nil {
			return connector.Response{}, err
		}
	}
	body := map[string]any{"BusinessShortCode": code, "Password": pw, "Timestamp": ts, "TransactionType": kind,
		"Amount": amount, "PartyA": phone, "PartyB": partyB, "PhoneNumber": phone, "CallBackURL": cb,
		"AccountReference": ref, "TransactionDesc": desc}
	var r struct {
		MerchantRequestID   string `json:"MerchantRequestID"`
		CheckoutRequestID   string `json:"CheckoutRequestID"`
		ResponseCode        any    `json:"ResponseCode"`
		ResponseDescription string `json:"ResponseDescription"`
		CustomerMessage     string `json:"CustomerMessage"`
	}
	err = c.post(ctx, req, "/mpesa/stkpush/v1/processrequest", body, &r)
	if ae := darajaError(err); ae != nil {
		m := strings.ToLower(ae.message)
		switch {
		case strings.Contains(m, "unable to lock subscriber"):
			// Another prompt is open on the phone: nothing was sent.
			return connector.Response{}, fmt.Errorf("%w: the customer has a payment prompt open; try again in a minute: %w", ae, effects.ErrNotSent)
		case strings.Contains(m, "merchant does not exist"), strings.Contains(m, "wrong credentials"):
			return connector.Response{}, fmt.Errorf("%w (check shortcode, passkey and environment): %w", ae, effects.ErrFatal)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	if code := text(r.ResponseCode); code != "0" {
		// Daraja answered but did not accept the request.
		return connector.Response{}, fmt.Errorf("mpesa: STK push not accepted (%s): %s: %w", code, r.ResponseDescription, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"merchant_request_id": r.MerchantRequestID, "checkout_request_id": r.CheckoutRequestID,
		"response_code": text(r.ResponseCode), "response_description": r.ResponseDescription, "customer_message": r.CustomerMessage}}, nil
}

func (c *client) stkQuery(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "checkout_request_id")
	if id == "" {
		return connector.Response{}, fatalf("checkout_request_id is required")
	}
	code, err := shortcode(req)
	if err != nil {
		return connector.Response{}, err
	}
	pw, ts, err := c.password(req, code)
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		MerchantRequestID string `json:"MerchantRequestID"`
		CheckoutRequestID string `json:"CheckoutRequestID"`
		ResponseCode      any    `json:"ResponseCode"`
		ResultCode        any    `json:"ResultCode"`
		ResultDesc        string `json:"ResultDesc"`
	}
	err = c.post(ctx, req, "/mpesa/stkpushquery/v1/query", map[string]any{"BusinessShortCode": code, "Password": pw, "Timestamp": ts, "CheckoutRequestID": id}, &r)
	if ae := darajaError(err); ae != nil && strings.Contains(strings.ToLower(ae.message), "being processed") {
		// The customer has not answered yet.
		return connector.Response{Output: map[string]any{"checkout_request_id": id, "merchant_request_id": "", "status": "pending",
			"result_code": ae.code, "result_desc": ae.message}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	result := text(r.ResultCode)
	status := "failed"
	switch result {
	case "0":
		status = "completed"
	case "1032":
		status = "cancelled"
	case "":
		status = "pending"
	}
	if r.CheckoutRequestID == "" {
		r.CheckoutRequestID = id
	}
	return connector.Response{Output: map[string]any{"checkout_request_id": r.CheckoutRequestID, "merchant_request_id": r.MerchantRequestID,
		"status": status, "result_code": result, "result_desc": r.ResultDesc}}, nil
}

func (c *client) registerC2B(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	code := str(in, "shortcode")
	if code == "" {
		var err error
		if code, err = shortcode(req); err != nil {
			return connector.Response{}, err
		}
	}
	rt := str(in, "response_type")
	if rt == "" {
		rt = "Completed"
	}
	if rt != "Completed" && rt != "Cancelled" {
		return connector.Response{}, fatalf("response_type must be Completed or Cancelled")
	}
	conf, val := str(in, "confirmation_url"), str(in, "validation_url")
	var err error
	if conf == "" {
		if conf, err = hookURL(req, "c2b_confirmation", nil); err != nil {
			return connector.Response{}, err
		}
	}
	if val == "" {
		if val, err = hookURL(req, "c2b_validation", nil); err != nil {
			return connector.Response{}, err
		}
	}
	var a ack
	err = c.post(ctx, req, "/mpesa/c2b/v2/registerurl", map[string]any{"ShortCode": code, "ResponseType": rt, "ConfirmationURL": conf, "ValidationURL": val}, &a)
	if ae := darajaError(err); ae != nil && strings.Contains(strings.ToLower(ae.message), "already registered") {
		return connector.Response{Output: map[string]any{"registered": false, "already_registered": true,
			"originator_conversation_id": "", "response_description": ae.message}}, nil
	}
	if err == nil {
		err = a.accepted()
		if err != nil {
			err = fmt.Errorf("%w: %w", err, effects.ErrFatal)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"registered": true, "already_registered": false,
		"originator_conversation_id": a.originator(), "response_description": a.ResponseDescription}}, nil
}

func (c *client) b2cPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id := str(in, "originator_conversation_id")
	if id == "" {
		return connector.Response{}, fatalf("b2c_payment needs the engine's originator_conversation_id")
	}
	amount, err := shillings(in["amount"])
	if err != nil {
		return connector.Response{}, err
	}
	phone, err := msisdn(str(in, "phone"))
	if err != nil {
		return connector.Response{}, err
	}
	remarks := str(in, "remarks")
	if len(remarks) < 2 || len(remarks) > 100 {
		return connector.Response{}, fatalf("remarks must be 2 to 100 characters")
	}
	command := str(in, "command_id")
	if command == "" {
		command = "BusinessPayment"
	}
	switch command {
	case "BusinessPayment", "SalaryPayment", "PromotionPayment":
	default:
		return connector.Response{}, fatalf("command_id must be BusinessPayment, SalaryPayment or PromotionPayment")
	}
	partyA, err := b2cShortcode(req)
	if err != nil {
		return connector.Response{}, err
	}
	name, cred, err := c.initiator(req)
	if err != nil {
		return connector.Response{}, err
	}
	result, timeout, err := asyncURLs(req, "b2c_result", "b2c", id)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"OriginatorConversationID": id, "InitiatorName": name, "SecurityCredential": cred, "CommandID": command,
		"Amount": amount, "PartyA": partyA, "PartyB": phone, "Remarks": remarks, "QueueTimeOutURL": timeout, "ResultURL": result}
	if o := str(in, "occasion"); o != "" {
		body["Occassion"] = o //nolint:misspell // Daraja spells it so
	}
	var a ack
	err = c.post(ctx, req, "/mpesa/b2c/v3/paymentrequest", body, &a)
	if ae := darajaError(err); ae != nil && (ae.code == "500.002.1001" || strings.Contains(strings.ToLower(ae.message), "duplicate originatorconversationid")) {
		// M-Pesa has already seen a request under this id (an earlier
		// attempt whose answer was lost): its result is on its way to
		// b2c_result. Nothing is sent twice.
		return connector.Response{Output: map[string]any{"originator_conversation_id": id, "conversation_id": "",
			"response_code": ae.code, "response_description": ae.message, "status": "accepted", "duplicate": true}}, nil
	}
	if err == nil {
		if aerr := a.accepted(); aerr != nil {
			// A 200 that does not accept: M-Pesa did not take the payment.
			err = fmt.Errorf("%w: %w", aerr, effects.ErrFatal)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	if o := a.originator(); o != "" {
		id = o
	}
	return connector.Response{Output: map[string]any{"originator_conversation_id": id, "conversation_id": a.ConversationID,
		"response_code": text(a.ResponseCode), "response_description": a.ResponseDescription, "status": "accepted", "duplicate": false}}, nil
}

// query sends an asynchronous read or reversal and reports Daraja's
// acknowledgement.
func (c *client) query(ctx context.Context, req connector.Request, path, trigger, kind, ref string, body map[string]any) (connector.Response, error) {
	name, cred, err := c.initiator(req)
	if err != nil {
		return connector.Response{}, err
	}
	result, timeout, err := asyncURLs(req, trigger, kind, ref)
	if err != nil {
		return connector.Response{}, err
	}
	body["Initiator"], body["SecurityCredential"], body["ResultURL"], body["QueueTimeOutURL"] = name, cred, result, timeout
	var a ack
	err = c.post(ctx, req, path, body, &a)
	if err == nil {
		if aerr := a.accepted(); aerr != nil {
			err = fmt.Errorf("%w: %w", aerr, effects.ErrFatal)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	corr := ref
	if corr == "" {
		corr = a.originator()
	}
	return connector.Response{Output: map[string]any{"originator_conversation_id": a.originator(), "conversation_id": a.ConversationID,
		"response_code": text(a.ResponseCode), "response_description": a.ResponseDescription, "correlation": corr}}, nil
}

func remarks(in map[string]any, def string) string {
	if r := str(in, "remarks"); r != "" {
		return r
	}
	return def
}

func (c *client) transactionStatus(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	tx, orig := str(in, "transaction_id"), str(in, "original_conversation_id")
	if tx == "" && orig == "" {
		return connector.Response{}, fatalf("transaction_status needs transaction_id or original_conversation_id")
	}
	party := str(in, "party_a")
	if party == "" {
		var err error
		if party, err = b2cShortcode(req); err != nil {
			return connector.Response{}, err
		}
	}
	idType := str(in, "identifier_type")
	if idType == "" {
		idType = "4"
	}
	ref := tx
	if ref == "" {
		ref = orig
	}
	body := map[string]any{"CommandID": "TransactionStatusQuery", "PartyA": party, "IdentifierType": idType, "Remarks": remarks(in, "Status")}
	if tx != "" {
		body["TransactionID"] = tx
	}
	if orig != "" {
		body["OriginalConversationID"] = orig
	}
	if o := str(in, "occasion"); o != "" {
		body["Occasion"] = o
	}
	return c.query(ctx, req, "/mpesa/transactionstatus/v1/query", "status_result", "status", ref, body)
}

func (c *client) accountBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	party := str(in, "party_a")
	if party == "" {
		var err error
		if party, err = b2cShortcode(req); err != nil {
			return connector.Response{}, err
		}
	}
	idType := str(in, "identifier_type")
	if idType == "" {
		idType = "4"
	}
	body := map[string]any{"CommandID": "AccountBalance", "PartyA": party, "IdentifierType": idType, "Remarks": remarks(in, "Balance")}
	return c.query(ctx, req, "/mpesa/accountbalance/v1/query", "balance_result", "balance", "", body)
}

func (c *client) reversal(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	tx := str(in, "transaction_id")
	if tx == "" {
		return connector.Response{}, fatalf("reversal needs transaction_id")
	}
	amount, err := shillings(in["amount"])
	if err != nil {
		return connector.Response{}, err
	}
	receiver := str(in, "receiver_party")
	if receiver == "" {
		if receiver, err = shortcode(req); err != nil {
			return connector.Response{}, err
		}
	}
	body := map[string]any{"CommandID": "TransactionReversal", "TransactionID": tx, "Amount": amount,
		"ReceiverParty": receiver, "RecieverIdentifierType": "11", "Remarks": remarks(in, "Reversal")} // Daraja's spelling
	if o := str(in, "occasion"); o != "" {
		body["Occasion"] = o
	}
	return c.query(ctx, req, "/mpesa/reversal/v1/request", "reversal_result", "reversal", tx, body)
}
