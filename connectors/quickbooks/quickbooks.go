// Package quickbooks is the QuickBooks Online connector: Intuit's
// Accounting API for one company, connected through Taskiem's Intuit app,
// with the books capabilities (connector/v1 rule 16) and the app's change
// notifications as a trigger. Built from Intuit's public developer
// documentation (docs/integrations/quickbooks.md).
//
// Tokens: an access token lasts an hour. The connector refreshes it with
// Taskiem's client (HTTP Basic) and stores what Intuit hands back, the
// refresh token included, once across every worker
// (connector.Request.RotateCredentials): Intuit may rotate the refresh
// token on any refresh, and only the latest is good.
//
// Writes are sent with Intuit's requestid (the step's idempotency key), so
// a repeated request is answered with the first one's result, and each is
// looked for first by its number or reference, so a retry after the
// requestid is forgotten still finds it.
//
// Errors: Intuit answers {"Fault": {"Error": [{"Message", "Detail",
// "code"}], "type"}}. 401 (3200) is an expired or revoked token: refreshed
// once, then the connection is expired; 403 (3100) is a permission the
// company did not grant; 429 is the throttle (500 a minute per company)
// and is retried; code 610 is a record that is not there; other
// validation faults are fatal; 5xx is an unknown outcome.
package quickbooks

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// minorVersion is the API version every call asks for: Intuit ignores
// minor versions below 75 since August 2025.
const minorVersion = "75"

// DefaultTokenURL is Intuit's token endpoint.
const DefaultTokenURL = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer" //nolint:gosec // an endpoint, not a credential

// Options configure the connector: LiveURL and TestURL replace the
// production and sandbox hosts, and TokenURL Intuit's token endpoint
// (tests, a proxy).
type Options struct {
	LiveURL, TestURL, TokenURL string
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// New returns the QuickBooks connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	if o.LiveURL != "" {
		m.OverrideBaseURL(o.LiveURL)
	}
	m.SetModeURL("live", o.LiveURL)
	m.SetModeURL("test", o.TestURL)
	c := &client{m: m, tokenURL: DefaultTokenURL, now: time.Now}
	if o.TokenURL != "" {
		c.tokenURL = o.TokenURL
	}
	// The token endpoint stays reachable when the API's host is replaced.
	if u, err := url.Parse(c.tokenURL); err == nil && u.Hostname() != "" && !slices.Contains(m.Egress, u.Hostname()) {
		m.Egress = append(m.Egress, u.Hostname())
	}
	if o.Now != nil {
		c.now = o.Now
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_company":          connector.ActionFunc(c.getCompany),
		"find_customer":        connector.ActionFunc(c.findCustomer),
		"create_customer":      connector.ActionFunc(c.createCustomer),
		"find_vendor":          connector.ActionFunc(c.findVendor),
		"create_vendor":        connector.ActionFunc(c.createVendor),
		"create_invoice":       connector.ActionFunc(c.createInvoice),
		"get_invoice":          connector.ActionFunc(c.getInvoice),
		"query_invoices":       connector.ActionFunc(c.queryInvoices),
		"create_payment":       connector.ActionFunc(c.createPayment),
		"create_sales_receipt": connector.ActionFunc(c.createSalesReceipt),
		"create_purchase":      connector.ActionFunc(c.createPurchase),
		"create_bill":          connector.ActionFunc(c.createBill),
		"list_items":           connector.ActionFunc(c.listItems),
		"list_accounts":        connector.ActionFunc(c.listAccounts),
		"list_tax_codes":       connector.ActionFunc(c.listTaxCodes),
		"get_entity":           connector.ActionFunc(c.getEntity),
		"changes_since":        connector.ActionFunc(c.changesSince),
	}}
}

type client struct {
	m        *connector.Manifest
	tokenURL string
	now      func() time.Time
}

// --- errors ---

// Fault is an Intuit refusal.
type Fault struct {
	Status  int
	Type    string // ValidationFault, SystemFault, AuthenticationFault, AuthorizationFault
	Code    string // 610, 6140, 6240, 3200...
	Message string
	Detail  string
}

func (f *Fault) Error() string {
	msg := f.Message
	if f.Detail != "" && f.Detail != f.Message {
		msg += ": " + f.Detail
	}
	if f.Code != "" {
		msg += " (code " + f.Code + ")"
	}
	return fmt.Sprintf("quickbooks: http %d: %s", f.Status, msg)
}

type faultBody struct {
	Fault *struct {
		Error []struct {
			Message string `json:"Message"`
			Detail  string `json:"Detail"`
			Code    string `json:"code"`
		} `json:"Error"`
		Type string `json:"type"`
	} `json:"Fault"`
}

// parseFault reads a Fault from a body (keys either case: Intuit's 401s
// spell them in lower case, which encoding/json matches).
func parseFault(status int, body []byte) *Fault {
	var fb faultBody
	if err := json.Unmarshal(body, &fb); err != nil || fb.Fault == nil {
		return nil //nolint:nilerr // a body that is no fault is no fault
	}
	f := &Fault{Status: status, Type: fb.Fault.Type}
	if len(fb.Fault.Error) > 0 {
		e := fb.Fault.Error[0]
		f.Code, f.Message, f.Detail = strings.TrimLeft(e.Code, "0"), e.Message, e.Detail
	}
	return f
}

// classify turns a call's failure into the engine's terms.
func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("quickbooks %s: %w", op, err)
	}
	f := parseFault(he.Status, he.Body)
	if f == nil {
		f = &Fault{Status: he.Status, Message: strings.TrimSpace(string(he.Body))}
		if len(f.Message) > 200 {
			f.Message = f.Message[:200]
		}
	}
	switch {
	case he.Status == http.StatusUnauthorized:
		return fmt.Errorf("quickbooks %s: %w: %w: %w", op, f, connector.ErrCredentialsRejected, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("quickbooks %s: %w: %w", op, f, he) // he carries the engine's kind and Retry-After
	case f.Code == "610" || he.Status == http.StatusNotFound:
		return fmt.Errorf("quickbooks %s: %w: %w", op, f, connector.ErrNotFound)
	}
	return fmt.Errorf("quickbooks %s: %w: %w", op, f, effects.ErrFatal)
}

func faultCode(err error) string {
	var f *Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("quickbooks: "+format+": %w", append(a, effects.ErrFatal)...)
}

// --- tokens ---

// session is one action's use of a connection: its company, where its
// calls go and the access token, refreshed at most once per action.
type session struct {
	c     *client
	req   connector.Request
	base  string
	realm string
	creds map[string]string
}

func (c *client) open(req connector.Request) (*session, error) {
	realm := strings.TrimSpace(req.Credentials["realm_id"])
	if realm == "" {
		return nil, fatalf("the connection has no company (realm_id): connect QuickBooks again")
	}
	base, err := c.m.BaseFor(req.Credentials)
	if err != nil {
		return nil, err
	}
	return &session{c: c, req: req, base: base, realm: realm, creds: req.Credentials}, nil
}

// fresh says whether credentials hold an access token good for another
// minute, other than stale (one the API just refused).
func (c *client) fresh(creds map[string]string, stale string) bool {
	tok := creds["access_token"]
	if tok == "" || tok == stale {
		return false
	}
	exp, err := time.Parse(time.RFC3339, creds["access_expires_at"])
	return err == nil && c.now().Add(time.Minute).Before(exp)
}

// token is an access token to send: the stored one while it lasts, or a
// new one, refreshed under the connection's lock and stored with the
// refresh token Intuit handed back.
func (s *session) token(ctx context.Context, stale string) (string, error) {
	if s.c.fresh(s.creds, stale) {
		return s.creds["access_token"], nil
	}
	creds, err := s.req.RotateCredentials(ctx, func(cur map[string]string) (map[string]string, error) {
		if s.c.fresh(cur, stale) {
			return cur, nil // another worker refreshed it
		}
		return s.c.refresh(ctx, s.req.HTTP, cur)
	})
	if errors.Is(err, connector.ErrNoRotate) {
		return "", fatalf("QuickBooks tokens are refreshed and kept by Taskiem: connect QuickBooks with the Connect button rather than pasting its details")
	}
	if err != nil {
		return "", err
	}
	s.creds = creds
	return creds["access_token"], nil
}

// refresh trades the refresh token for new tokens (grant_type
// refresh_token, the client in HTTP Basic).
func (c *client) refresh(ctx context.Context, hc *http.Client, cur map[string]string) (map[string]string, error) {
	id, secret, rt := cur["client_id"], cur["client_secret"], cur["refresh_token"]
	if id == "" || secret == "" || rt == "" {
		return nil, fmt.Errorf("quickbooks: the connection is missing its client or refresh token: connect QuickBooks again: %w: %w", connector.ErrCredentialsRejected, effects.ErrFatal)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("quickbooks: %w: %w", err, effects.ErrFatal)
	}
	r.SetBasicAuth(id, secret)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	resp, err := hc.Do(r)
	if err != nil {
		// Nothing was stored: the refresh token held is still the latest
		// Taskiem saw. Retried.
		return nil, fmt.Errorf("quickbooks: refreshing the token: %w: %w", connector.RedactURLError(err), effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("quickbooks: reading the token answer: %w: %w", err, effects.ErrRetryable)
	}
	var t struct {
		AccessToken           string  `json:"access_token"`
		RefreshToken          string  `json:"refresh_token"`
		ExpiresIn             float64 `json:"expires_in"`
		RefreshTokenExpiresIn float64 `json:"x_refresh_token_expires_in"`
		Error                 string  `json:"error"`
		Description           string  `json:"error_description"`
	}
	_ = json.Unmarshal(body, &t)
	switch {
	case resp.StatusCode == http.StatusOK && t.AccessToken != "":
	case resp.StatusCode == http.StatusBadRequest && t.Error == "invalid_grant":
		// Revoked, expired, or a spent token: the company must be
		// connected again.
		return nil, fmt.Errorf("quickbooks: Intuit refused the refresh token (%s): connect QuickBooks again: %w: %w", strings.TrimSpace(t.Error+" "+t.Description), connector.ErrCredentialsRejected, effects.ErrFatal)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("quickbooks: refreshing the token: http %d: %w", resp.StatusCode, effects.ErrRetryable)
	default:
		// invalid_client and the like: Taskiem's Intuit app, not the
		// business's connection.
		return nil, fatalf("Intuit refused the token refresh: http %d %s", resp.StatusCode, strings.TrimSpace(t.Error+" "+t.Description))
	}
	out := make(map[string]string, len(cur)+3)
	for k, v := range cur {
		out[k] = v
	}
	now := c.now().UTC()
	out["access_token"] = t.AccessToken
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	out["access_expires_at"] = now.Add(time.Duration(t.ExpiresIn) * time.Second).Format(time.RFC3339)
	if t.RefreshToken != "" {
		out["refresh_token"] = t.RefreshToken
	}
	if t.RefreshTokenExpiresIn > 0 {
		out["refresh_expires_at"] = now.Add(time.Duration(t.RefreshTokenExpiresIn) * time.Second).Format(time.RFC3339)
	}
	return out, nil
}

// --- calls ---

// do calls /v3/company/{realm}/{path}, refreshing the token once when it
// is refused.
func (s *session) do(ctx context.Context, op, method, path string, q url.Values, body, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("minorversion", minorVersion)
	u := s.base + "/v3/company/" + url.PathEscape(s.realm) + "/" + path + "?" + q.Encode()
	tok, err := s.token(ctx, "")
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		var raw json.RawMessage
		err = connector.DoJSON(ctx, s.req.HTTP, method, u, map[string]string{"Authorization": "Bearer " + tok}, body, &raw)
		var he *connector.HTTPError
		if try == 0 && errors.As(err, &he) && he.Status == http.StatusUnauthorized {
			if tok, err = s.token(ctx, tok); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return classify(op, err)
		}
		// A fault answered with 200 (Intuit's batch-style answers).
		if f := parseFault(http.StatusBadRequest, raw); f != nil {
			return classify(op, &connector.HTTPError{Status: http.StatusBadRequest, Body: raw})
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("quickbooks %s: decoding the answer: %w: %w", op, err, effects.ErrUnknownOutcome)
			}
		}
		return nil
	}
}

// query runs an Intuit query (SELECT … FROM <entity>) and decodes the
// entity's rows.
func (s *session) query(ctx context.Context, op, entity, q string, out any) error {
	var resp struct {
		QueryResponse map[string]json.RawMessage `json:"QueryResponse"`
	}
	if err := s.do(ctx, op, http.MethodGet, "query", url.Values{"query": {q}}, nil, &resp); err != nil {
		return err
	}
	rows, ok := resp.QueryResponse[entity]
	if !ok {
		return nil // none
	}
	if err := json.Unmarshal(rows, out); err != nil {
		return fmt.Errorf("quickbooks %s: decoding the rows: %w: %w", op, err, effects.ErrUnknownOutcome)
	}
	return nil
}

// create posts a record with requestid and decodes it.
func (s *session) create(ctx context.Context, op, entity string, record, out any) error {
	q := url.Values{}
	if k := s.req.IdempotencyKey; k != "" {
		q.Set("requestid", requestID(k))
	}
	resp := map[string]json.RawMessage{}
	if err := s.do(ctx, op, http.MethodPost, strings.ToLower(entity), q, record, &resp); err != nil {
		return err
	}
	raw, ok := resp[entity]
	if !ok {
		return fmt.Errorf("quickbooks %s: the answer has no %s: %w", op, entity, effects.ErrUnknownOutcome)
	}
	return json.Unmarshal(raw, out)
}

// requestID is the idempotency key as Intuit takes it: at most 50
// characters.
func requestID(key string) string {
	if len(key) <= 50 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:50]
}

// quote is a value in an Intuit query: single quotes, backslash-escaped.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// docNumber is a number as QuickBooks keeps it: at most 21 characters. A
// longer one is shortened the same way every time, so a retry finds it.
func docNumber(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 21 {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return s[:12] + "-" + hex.EncodeToString(sum[:])[:8]
}

// --- money ---

// amount is kobo as Intuit's decimal amount.
func amount(kobo int64) json.Number { return money.Number(kobo, 2) }

// kobo is an Intuit decimal amount in kobo, rounded half up.
func kobo(n json.Number) int64 {
	if n == "" {
		return 0
	}
	return money.Round(n, 2)
}

// --- input helpers ---

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func integer(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func number(m map[string]any, k string, def float64) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f
		}
	}
	return def
}

func objects(m map[string]any, k string) []map[string]any {
	list, _ := m[k].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		if o, ok := v.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

type ref struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

func (r *ref) id() string {
	if r == nil {
		return ""
	}
	return r.Value
}

// taxCalculation is Intuit's GlobalTaxCalculation for the input's
// tax_calculation: given, or TaxExcluded when a line names a tax code that
// is not a US one (TAX, NON), as the books capabilities' amounts are
// before tax. US companies take none.
func taxCalculation(in map[string]any, lines []map[string]any) string {
	switch str(in, "tax_calculation") {
	case "excluded":
		return "TaxExcluded"
	case "inclusive":
		return "TaxInclusive"
	case "not_applicable":
		return "NotApplicable"
	}
	for _, l := range lines {
		if c := str(l, "tax_code_id"); c != "" && c != "TAX" && c != "NON" {
			return "TaxExcluded"
		}
	}
	return ""
}

func today(c *client) string { return c.now().UTC().Format(time.DateOnly) }
