// Package mtnmomo is the MTN MoMo connector, built on MTN's Mobile Money
// Open API: Collection (request to pay), Disbursement and Remittance
// (transfers), balances, account holder checks, and callbacks. Built from
// MTN's public developer documentation (docs/integrations/mtnmomo.md).
//
// Auth: each product (collection, disbursement, remittance) has its own
// subscription key (Ocp-Apim-Subscription-Key) and issues its own bearer
// token from POST /{product}/token/ against Basic base64(apiUser:apiKey).
// The connector caches one token per product, credential set and host
// until shortly before it expires, and fetches a new one once if MTN
// answers 401. Every call names the country's wallet platform in
// X-Target-Environment.
//
// Never twice: the engine's key becomes the X-Reference-Id, a UUID (the
// key's 128 bits laid out as a version-4 UUID, so 122 of them survive). MTN
// refuses a repeated one (409 RESOURCE_ALREADY_EXIST); the connector then
// reads the request by that id and reports it. It is also sent as
// externalId, which callbacks carry, so they correlate on it.
//
// Amounts: Taskiem works in minor units; MTN takes and returns decimal
// strings in the currency's major unit. The scale comes from ISO 4217
// (UGX, XAF, XOF and RWF have none), and conversions are exact.
package mtnmomo

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
const SandboxURL = "https://sandbox.momodeveloper.mtn.com"

// Options configure the connector; BaseURL overrides both environments
// (tests, a local fake).
type Options struct {
	BaseURL string
}

// New returns the MTN MoMo connector.
func New(o Options) *connector.Connector { return newConnector(o, time.Now) }

func newConnector(o Options, now func() time.Time) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL, now: now, tokens: map[string]token{}}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":             connector.ActionFunc(c.getBalance),
		"validate_account_holder": connector.ActionFunc(c.validateAccountHolder),
		"get_account_holder_name": connector.ActionFunc(c.accountHolderName),
		"request_to_pay":          connector.ActionFunc(c.requestToPay),
		"get_payment":             connector.ActionFunc(c.getPayment),
		"transfer":                connector.ActionFunc(c.transfer),
		"get_transfer":            connector.ActionFunc(c.getTransfer),
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
	tokens map[string]token
}

var products = []string{"collection", "disbursement", "remittance"}

func (c *client) base(req connector.Request) string {
	if sandboxed(req) {
		return c.sandbox
	}
	return c.live
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// text reads a field MTN sends as a string or a number.
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
	return fmt.Errorf("mtnmomo: "+format+": %w", append(args, effects.ErrFatal)...)
}

// apiError is a refusal MTN explained ({code, message}).
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("mtn momo %d (%s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("mtn momo %d: %s", e.status, e.message)
}

func momoError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func parseError(he *connector.HTTPError) *apiError {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(he.Body, &e)
	ae := &apiError{status: he.Status, code: e.Code, message: e.Message}
	if ae.message == "" {
		ae.message = e.Error
	}
	if ae.message == "" {
		ae.message = strings.TrimSpace(string(he.Body))
	}
	return ae
}

// creds are one product's credentials.
type creds struct {
	product, user, key, sub string
}

func productCreds(req connector.Request, product string) (creds, error) {
	cr := creds{product: product, user: req.Credentials[product+"_api_user"], key: req.Credentials[product+"_api_key"],
		sub: req.Credentials[product+"_subscription_key"]}
	if cr.user == "" {
		cr.user, cr.key = req.Credentials["api_user"], req.Credentials["api_key"]
	}
	if cr.sub == "" {
		return cr, fatalf("the connection has no %s_subscription_key", product)
	}
	if cr.user == "" || cr.key == "" {
		return cr, fatalf("the connection needs api_user and api_key (or %s_api_user and %s_api_key)", product, product)
	}
	return cr, nil
}

func (c *client) target(req connector.Request) (string, error) {
	if t := strings.TrimSpace(req.Credentials["target_environment"]); t != "" {
		return t, nil
	}
	if sandboxed(req) {
		return "sandbox", nil
	}
	return "", fatalf("the connection has no target_environment (mtnuganda, mtnghana…)")
}

func (c *client) tokenKey(req connector.Request, cr creds) string {
	return c.base(req) + "\x00" + cr.product + "\x00" + cr.user + "\x00" + cr.key + "\x00" + cr.sub
}

// bearer returns a cached token for the product, or creates one.
func (c *client) bearer(ctx context.Context, req connector.Request, cr creds) (string, error) {
	k := c.tokenKey(req, cr)
	c.mu.Lock()
	t, ok := c.tokens[k]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires) {
		return t.value, nil
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	basic := base64.StdEncoding.EncodeToString([]byte(cr.user + ":" + cr.key))
	err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base(req)+"/"+cr.product+"/token/",
		map[string]string{"Authorization": "Basic " + basic, "Ocp-Apim-Subscription-Key": cr.sub}, nil, &body)
	if err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			ae := parseError(he)
			// The token request moves no money: whatever went wrong, the
			// action has not been attempted.
			if he.Status >= 500 || he.Status == http.StatusTooManyRequests {
				return "", fmt.Errorf("mtn momo %s token: %w: %w", cr.product, ae, effects.ErrRetryable)
			}
			return "", fmt.Errorf("mtn momo refused the %s credentials (check the API user, API key and subscription key): %w: %w", cr.product, ae, effects.ErrFatal)
		}
		return "", fmt.Errorf("mtn momo %s token: %w: %w", cr.product, err, effects.ErrRetryable)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("mtn momo returned no %s token: %w", cr.product, effects.ErrRetryable)
	}
	secs, _ := strconv.Atoi(strings.TrimSpace(text(body.ExpiresIn)))
	life := time.Duration(secs) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	if life > 2*time.Minute {
		life -= time.Minute // renewed before it lapses in flight
	}
	c.mu.Lock()
	c.tokens[k] = token{value: body.AccessToken, expires: c.now().Add(life)}
	c.mu.Unlock()
	return body.AccessToken, nil
}

func (c *client) forget(req connector.Request, cr creds) {
	c.mu.Lock()
	delete(c.tokens, c.tokenKey(req, cr))
	c.mu.Unlock()
}

// do sends one call for a product. A 401 renews the token once (MTN
// refused before acting). Failures keep the transport's classification and
// carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, product, method, path string, extra map[string]string, body, out any) error {
	cr, err := productCreds(req, product)
	if err != nil {
		return err
	}
	target, err := c.target(req)
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		tok, err := c.bearer(ctx, req, cr)
		if err != nil {
			return err
		}
		h := map[string]string{"Authorization": "Bearer " + tok, "Ocp-Apim-Subscription-Key": cr.sub, "X-Target-Environment": target}
		for k, v := range extra {
			h[k] = v
		}
		err = connector.DoJSON(ctx, req.HTTP, method, c.base(req)+"/"+product+path, h, body, out)
		var he *connector.HTTPError
		if !errors.As(err, &he) {
			return err
		}
		if he.Status == http.StatusUnauthorized && try == 0 {
			c.forget(req, cr)
			continue
		}
		ae := parseError(he)
		if method == http.MethodGet && refusals[ae.code] {
			// A configuration MTN refuses (environment, permission):
			// asking again will not help.
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		}
		return fmt.Errorf("%w: %w", ae, he)
	}
}

func defaultProduct(req connector.Request) string {
	for _, p := range products {
		if req.Credentials[p+"_subscription_key"] != "" {
			return p
		}
	}
	return "collection"
}

func product(in map[string]any, req connector.Request, allowed ...string) (string, error) {
	p := str(in, "product")
	if p == "" {
		if len(allowed) > 0 {
			return allowed[0], nil
		}
		return defaultProduct(req), nil
	}
	list := allowed
	if len(list) == 0 {
		list = products
	}
	for _, a := range list {
		if a == p {
			return p, nil
		}
	}
	return "", fatalf("product %q is not one of %v", p, list)
}

var hexKeyRE = regexp.MustCompile(`^[0-9a-f]{32}$`)
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// referenceID turns the engine's key (32 hex characters) into the version-4
// UUID MTN requires as X-Reference-Id, deterministically, so a resend and a
// reconcile use the same one. A UUID is taken as it is.
func referenceID(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if uuidRE.MatchString(v) {
		return v, nil
	}
	if !hexKeyRE.MatchString(v) {
		return "", fatalf("reference_id must be a UUID or the engine's 32-character key")
	}
	b := []byte(v)
	b[12] = '4' // version 4
	n, _ := strconv.ParseUint(string(b[16]), 16, 8)
	b[16] = "89ab"[n&3] // RFC 4122 variant
	s := string(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32], nil
}

var msisdnRE = regexp.MustCompile(`^[1-9]\d{7,14}$`)

func msisdn(v string) (string, error) {
	n := strings.TrimPrefix(strings.NewReplacer(" ", "", "-", "").Replace(v), "+")
	if !msisdnRE.MatchString(n) {
		return "", fatalf("msisdn must be a number with its country code, digits only (E.164 without +)")
	}
	return n, nil
}

// note checks a message or note: 160 characters, no apostrophe (MTN
// refuses both with 400).
func note(in map[string]any, k string) (string, error) {
	s := str(in, k)
	if len([]rune(s)) > 160 {
		return "", fatalf("%s is 160 characters at most", k)
	}
	if strings.ContainsAny(s, "'’") {
		return "", fatalf("%s may not contain an apostrophe (MTN refuses it)", k)
	}
	return s, nil
}

func currency(in map[string]any, req connector.Request) (string, int, error) {
	cur := strings.ToUpper(str(in, "currency"))
	if cur == "" {
		cur = strings.ToUpper(strings.TrimSpace(req.Credentials["currency"]))
	}
	if cur == "" && sandboxed(req) {
		cur = "EUR" // the sandbox's currency
	}
	if cur == "" {
		return "", 0, fatalf("no currency in the step or the connection")
	}
	scale, ok := money.Scale(cur)
	if !ok {
		return "", 0, fatalf("unknown currency %s", cur)
	}
	return cur, scale, nil
}

func sandboxed(req connector.Request) bool {
	return strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox")
}

// major renders minor units as MTN's amount string: whole amounts without
// decimals, as in MTN's examples ("1000"), others with the currency's.
func major(minor int64, scale int) string {
	if scale == 0 {
		return strconv.FormatInt(minor, 10)
	}
	s := money.Format(minor, scale)
	if strings.Trim(s[len(s)-scale:], "0") == "" {
		return s[:len(s)-scale-1]
	}
	return s
}

// callbackURL is <hooks_url>/<trigger>/<env>/<connection>/<token>, or ""
// when the connection takes no callbacks (status is then polled).
func callbackURL(req connector.Request, trigger string) (string, error) {
	base := strings.TrimSpace(req.Credentials["hooks_url"])
	if base == "" {
		return "", nil
	}
	conn, tok := strings.TrimSpace(req.Credentials["hooks_connection"]), req.Credentials["callback_token"]
	if conn == "" || tok == "" {
		return "", fatalf("callbacks need hooks_connection and callback_token on the connection")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.RawQuery != "" {
		return "", fatalf("hooks_url must be an https URL without a query string")
	}
	env := strings.TrimSpace(req.Credentials["hooks_env"])
	if env == "" {
		env = "prod"
	}
	return strings.TrimRight(base, "/") + "/" + trigger + "/" + url.PathEscape(env) + "/" + url.PathEscape(conn) + "/" + url.PathEscape(tok), nil
}

// status is a request to pay or transfer as MTN reports it.
type status struct {
	Amount                 any    `json:"amount"`
	Currency               string `json:"currency"`
	FinancialTransactionID any    `json:"financialTransactionId"`
	ExternalID             any    `json:"externalId"`
	Payer                  *party `json:"payer"`
	Payee                  *party `json:"payee"`
	Status                 string `json:"status"`
	Reason                 any    `json:"reason"`
}

type party struct {
	PartyIDType string `json:"partyIdType"`
	PartyID     any    `json:"partyId"`
}

func (s status) output(ref string) (map[string]any, error) {
	out := map[string]any{"reference_id": ref, "external_id": text(s.ExternalID), "status": s.Status, "currency": s.Currency,
		"financial_transaction_id": text(s.FinancialTransactionID), "msisdn": "", "reason_code": "", "reason_message": "", "amount": int64(0)}
	for _, p := range []*party{s.Payer, s.Payee} {
		if p != nil {
			out["msisdn"] = text(p.PartyID)
		}
	}
	switch r := s.Reason.(type) {
	case string:
		out["reason_code"] = r
	case map[string]any:
		out["reason_code"], out["reason_message"] = text(r["code"]), text(r["message"])
	}
	if s.Amount != nil {
		scale, ok := money.Scale(s.Currency)
		if !ok {
			return nil, fmt.Errorf("mtnmomo: unknown currency %q in MTN's answer: %w", s.Currency, effects.ErrUnknownOutcome)
		}
		n, err := money.Parse(s.Amount, scale)
		if err != nil {
			return nil, fmt.Errorf("mtnmomo: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
		}
		out["amount"] = n
	}
	return out, nil
}

// lookup reads a request to pay or transfer by its reference id; MTN's 404
// is connector.ErrNotFound.
func (c *client) lookup(ctx context.Context, req connector.Request, product, path, ref string) (map[string]any, error) {
	var s status
	err := c.do(ctx, req, product, http.MethodGet, path+"/"+ref, nil, nil, &s)
	if ae := momoError(err); ae != nil && (ae.status == http.StatusNotFound || ae.code == "RESOURCE_NOT_FOUND") {
		return nil, fmt.Errorf("mtnmomo: no %s %s: %w", strings.TrimPrefix(path, "/v1_0/"), ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if s.Status == "" {
		return nil, fmt.Errorf("mtnmomo: %s %s has no status: %w", strings.TrimPrefix(path, "/v1_0/"), ref, effects.ErrUnknownOutcome)
	}
	return s.output(ref)
}

// settled turns a request MTN reports FAILED into a failed step: no money
// moved, and MTN's reason is the error.
func settled(kind string, out map[string]any) (connector.Response, error) {
	if out["status"] == "FAILED" {
		return connector.Response{}, fmt.Errorf("mtnmomo: %s %s failed: %s %s: %w", kind, out["reference_id"], out["reason_code"], out["reason_message"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

// refusals MTN reports with a 5xx although nothing can have been created
// (wrong currency, environment, callback host, unknown party, not allowed).
// The connector still looks the reference up before failing the step.
var refusals = map[string]bool{"INVALID_CURRENCY": true, "NOT_ALLOWED_TARGET_ENVIRONMENT": true, "INVALID_CALLBACK_URL_HOST": true,
	"PAYEE_NOT_FOUND": true, "PAYER_NOT_FOUND": true, "NOT_ALLOWED": true}

// create sends a request to pay or a transfer under its reference id, and
// settles duplicates and refusals by reading the reference back.
func (c *client) create(ctx context.Context, req connector.Request, kind, product, path, trigger string, partyKey string) (connector.Response, error) {
	in := req.Input
	ref, err := referenceID(str(in, "reference_id"))
	if err != nil {
		return connector.Response{}, err
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fatalf("amount must be a positive whole number of minor units, got %v", in["amount"])
	}
	cur, scale, err := currency(in, req)
	if err != nil {
		return connector.Response{}, err
	}
	who, err := msisdn(str(in, "msisdn"))
	if err != nil {
		return connector.Response{}, err
	}
	msg, err := note(in, "payer_message")
	if err != nil {
		return connector.Response{}, err
	}
	pn, err := note(in, "payee_note")
	if err != nil {
		return connector.Response{}, err
	}
	cb, err := callbackURL(req, trigger)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"amount": major(amount, scale), "currency": cur, "externalId": ref,
		partyKey: map[string]any{"partyIdType": "MSISDN", "partyId": who}, "payerMessage": msg, "payeeNote": pn}
	h := map[string]string{"X-Reference-Id": ref}
	if cb != "" {
		h["X-Callback-Url"] = cb
	}
	err = c.do(ctx, req, product, http.MethodPost, path, h, body, nil)
	ae := momoError(err)
	switch {
	case ae != nil && (ae.status == http.StatusConflict || ae.code == "RESOURCE_ALREADY_EXIST"):
		// Already made under this reference (a resend after a lost
		// answer): report it.
		out, gerr := c.lookup(ctx, req, product, path, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("mtnmomo refused reference %s as a duplicate (%s) and has no %s under it: %w", ref, ae.message, kind, effects.ErrUnknownOutcome)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return settled(kind, out)
	case ae != nil && ae.status >= 500 && refusals[ae.code]:
		out, gerr := c.lookup(ctx, req, product, path, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("%w; nothing was created under %s: %w", ae, ref, effects.ErrFatal)
		}
		if gerr != nil {
			return connector.Response{}, fmt.Errorf("%w; and reading %s back: %w", ae, ref, gerr)
		}
		return settled(kind, out)
	case err != nil:
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"reference_id": ref, "external_id": ref, "status": "PENDING", "amount": amount,
		"currency": cur, "financial_transaction_id": "", "msisdn": who, "reason_code": "", "reason_message": ""}}, nil
}

func (c *client) requestToPay(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.create(ctx, req, "request to pay", "collection", "/v1_0/requesttopay", "payment_callback", "payer")
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := product(req.Input, req, "disbursement", "remittance")
	if err != nil {
		return connector.Response{}, err
	}
	return c.create(ctx, req, "transfer", p, "/v1_0/transfer", "transfer_callback", "payee")
}

func (c *client) get(ctx context.Context, req connector.Request, product, path string) (connector.Response, error) {
	ref, err := referenceID(str(req.Input, "reference_id"))
	if err != nil {
		return connector.Response{}, err
	}
	out, err := c.lookup(ctx, req, product, path, ref)
	if errors.Is(err, connector.ErrNotFound) {
		// Read directly, not found is final; as a reconcile, the engine
		// sees ErrNotFound and may send again under a new key.
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) getPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.get(ctx, req, "collection", "/v1_0/requesttopay")
}

// getTransfer reads a transfer. Without a product (a reconcile passes only
// the key) it looks in Disbursement, then in Remittance if the connection
// has that product, so a remittance transfer is never taken for missing
// and paid again.
func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	if str(req.Input, "product") != "" {
		p, err := product(req.Input, req, "disbursement", "remittance")
		if err != nil {
			return connector.Response{}, err
		}
		return c.get(ctx, req, p, "/v1_0/transfer")
	}
	var last error
	for _, p := range []string{"disbursement", "remittance"} {
		if req.Credentials[p+"_subscription_key"] == "" {
			continue
		}
		resp, err := c.get(ctx, req, p, "/v1_0/transfer")
		if !errors.Is(err, connector.ErrNotFound) {
			return resp, err
		}
		last = err
	}
	if last == nil {
		return connector.Response{}, fatalf("the connection has no disbursement_subscription_key or remittance_subscription_key")
	}
	return connector.Response{}, last
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := product(req.Input, req)
	if err != nil {
		return connector.Response{}, err
	}
	var b struct {
		AvailableBalance any    `json:"availableBalance"`
		Currency         string `json:"currency"`
	}
	if err := c.do(ctx, req, p, http.MethodGet, "/v1_0/account/balance", nil, nil, &b); err != nil {
		return connector.Response{}, err
	}
	scale, ok := money.Scale(b.Currency)
	if !ok {
		return connector.Response{}, fmt.Errorf("mtnmomo: unknown currency %q in the balance: %w", b.Currency, effects.ErrFatal)
	}
	// Report what can be spent: finer fractions are dropped.
	var rd money.Reader
	avail := rd.Floor(b.AvailableBalance, scale)
	if rd.Err != nil {
		return connector.Response{}, fmt.Errorf("mtnmomo: unreadable balance: %w: %w", rd.Err, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"product": p, "available_balance": avail, "currency": b.Currency}}, nil
}

func (c *client) validateAccountHolder(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := product(req.Input, req)
	if err != nil {
		return connector.Response{}, err
	}
	who, err := msisdn(str(req.Input, "msisdn"))
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		Result *bool `json:"result"`
	}
	err = c.do(ctx, req, p, http.MethodGet, "/v1_0/accountholder/msisdn/"+who+"/active", nil, nil, &r)
	if ae := momoError(err); ae != nil && (ae.status == http.StatusNotFound || ae.code == "PAYEE_NOT_FOUND" || ae.code == "PAYER_NOT_FOUND") {
		return connector.Response{Output: map[string]any{"msisdn": who, "active": false}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"msisdn": who, "active": r.Result != nil && *r.Result}}, nil
}

func (c *client) accountHolderName(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := product(req.Input, req, products...)
	if err != nil {
		return connector.Response{}, err
	}
	who, err := msisdn(str(req.Input, "msisdn"))
	if err != nil {
		return connector.Response{}, err
	}
	var u struct {
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		Status     string `json:"status"`
	}
	err = c.do(ctx, req, p, http.MethodGet, "/v1_0/accountholder/msisdn/"+who+"/basicuserinfo", nil, nil, &u)
	if ae := momoError(err); ae != nil && ae.status < 500 && ae.status != http.StatusUnauthorized {
		return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"given_name": u.GivenName, "family_name": u.FamilyName, "status": u.Status}}, nil
}
