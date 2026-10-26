// Package zohobooks is the Zoho Books connector: Books' API v3 for one
// organisation, connected through Taskiem's Zoho client in the account's
// own data centre (connector/v1 rule 19), with the books capabilities
// (rule 16) and a polled trigger for invoices and payments. Built from
// Zoho's public developer documentation (docs/integrations/zoho-books.md).
//
// Tokens: an access token lasts an hour, and Zoho issues at most ten
// every ten minutes per refresh token, so the connector keeps
// the one it got with the credentials and refreshes under the
// connection's lock (connector.Request.RotateCredentials), at the
// account's accounts server. Zoho's refresh tokens do not rotate.
//
// Writes: Zoho Books has no idempotency key. Each write looks first for
// what it would make by the business's own number or reference (an
// invoice's reference_number, a payment's reference, a bill's number), so
// a retry after an unknown outcome finds the first attempt's record.
//
// Errors: Books answers {"code": n, "message": "…"}, code 0 for success.
// 401 is a refused token: refreshed once, then the connection is expired;
// 429 (codes 44, 45, 1070) is a rate limit and is retried; 404 or code
// 1002 is a record that is not there; other refusals are fatal; 5xx is an
// unknown outcome.
package zohobooks

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector: BaseURL replaces Books' API for every
// connection, and AccountsServer the accounts server (tests, a proxy).
type Options struct {
	BaseURL, AccountsServer string
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// New returns the Zoho Books connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{m: m, accounts: strings.TrimRight(o.AccountsServer, "/"), now: time.Now}
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
		c.fixed = true
	}
	if p, err := url.Parse(o.AccountsServer); err == nil && p.Hostname() != "" {
		m.Egress = append(m.Egress, p.Hostname())
	}
	if o.Now != nil {
		c.now = o.Now
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_organizations":   connector.ActionFunc(c.listOrganizations),
		"find_contact":         connector.ActionFunc(c.findContact),
		"create_contact":       connector.ActionFunc(c.createContact),
		"create_invoice":       connector.ActionFunc(c.createInvoice),
		"get_invoice":          connector.ActionFunc(c.getInvoice),
		"find_invoices":        connector.ActionFunc(c.findInvoices),
		"record_payment":       connector.ActionFunc(c.recordPayment),
		"create_sales_receipt": connector.ActionFunc(c.createSalesReceipt),
		"create_expense":       connector.ActionFunc(c.createExpense),
		"create_bill":          connector.ActionFunc(c.createBill),
		"list_items":           connector.ActionFunc(c.listItems),
		"list_accounts":        connector.ActionFunc(c.listAccounts),
		"list_taxes":           connector.ActionFunc(c.listTaxes),
		"changes_since":        connector.ActionFunc(c.changesSince),
	}}
}

type client struct {
	m        *connector.Manifest
	accounts string
	// fixed: every call goes to the manifest's base URL (Options.BaseURL).
	fixed bool
	now   func() time.Time
}

// --- errors ---

// Error is a Zoho Books refusal.
type Error struct {
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("zoho books: http %d: %s (code %d)", e.Status, e.Message, e.Code)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("zoho books %s: %w", op, err)
	}
	var b struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(he.Body, &b)
	ze := &Error{Status: he.Status, Code: b.Code, Message: b.Message}
	if ze.Message == "" {
		ze.Message = strings.TrimSpace(string(he.Body))
		if len(ze.Message) > 200 {
			ze.Message = ze.Message[:200]
		}
	}
	switch {
	case he.Status == http.StatusUnauthorized:
		return fmt.Errorf("zoho books %s: %w: %w: %w", op, ze, connector.ErrCredentialsRejected, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("zoho books %s: %w: %w", op, ze, he) // he carries the engine's kind and Retry-After
	case he.Status == http.StatusNotFound || ze.Code == 1002:
		return fmt.Errorf("zoho books %s: %w: %w", op, ze, connector.ErrNotFound)
	}
	return fmt.Errorf("zoho books %s: %w: %w", op, ze, effects.ErrFatal)
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("zoho books: "+format+": %w", append(a, effects.ErrFatal)...)
}

// --- the session: tokens and the organisation ---

type session struct {
	c     *client
	req   connector.Request
	creds map[string]string
}

func (c *client) open(req connector.Request) *session {
	return &session{c: c, req: req, creds: req.Credentials}
}

// apiBase is where the connection's calls go: its account's API domain,
// or the base URL where the deployment fixes one or the connection names
// none.
func (s *session) apiBase() (string, error) {
	d := strings.TrimRight(strings.TrimSpace(s.creds["api_domain"]), "/")
	if s.c.fixed || d == "" {
		return strings.TrimRight(s.c.m.BaseURL, "/"), nil
	}
	u, err := url.Parse(d)
	if err != nil || u.Scheme != "https" || !s.c.m.ConnectionHostAllowed(u.Hostname()) {
		return "", fatalf("the connection's API domain %q is not Zoho's: connect Zoho Books again", d)
	}
	return d + "/books/v3", nil
}

func (s *session) accountsServer() (string, error) {
	if s.c.accounts != "" {
		return s.c.accounts, nil
	}
	d := strings.TrimRight(strings.TrimSpace(s.creds["accounts_server"]), "/")
	u, err := url.Parse(d)
	if err != nil || u.Scheme != "https" || !s.c.m.ConnectionHostAllowed(u.Hostname()) {
		return "", fatalf("the connection's accounts server %q is not Zoho's: connect Zoho Books again", d)
	}
	return d, nil
}

func (c *client) fresh(creds map[string]string, stale string) bool {
	tok := creds["access_token"]
	if tok == "" || tok == stale {
		return false
	}
	exp, err := time.Parse(time.RFC3339, creds["access_expires_at"])
	return err == nil && c.now().Add(time.Minute).Before(exp)
}

// rotate stores what fn makes of the connection's credentials, once
// across workers; with credentials that are not a stored connection's, it
// is refused rather than spend an access token from Zoho's small budget
// unseen.
func (s *session) rotate(ctx context.Context, fn connector.RotateFunc) error {
	creds, err := s.req.RotateCredentials(ctx, fn)
	if errors.Is(err, connector.ErrNoRotate) {
		return fatalf("Zoho Books tokens are refreshed and kept by Taskiem: connect Zoho Books with the Connect button rather than pasting its details")
	}
	if err != nil {
		return err
	}
	s.creds = creds
	return nil
}

func (s *session) token(ctx context.Context, stale string) (string, error) {
	if s.c.fresh(s.creds, stale) {
		return s.creds["access_token"], nil
	}
	err := s.rotate(ctx, func(cur map[string]string) (map[string]string, error) {
		if s.c.fresh(cur, stale) {
			return cur, nil // another worker refreshed it
		}
		return s.refresh(ctx, cur)
	})
	return s.creds["access_token"], err
}

// refresh gets a new access token at the account's accounts server.
func (s *session) refresh(ctx context.Context, cur map[string]string) (map[string]string, error) {
	id, secret, rt := cur["client_id"], cur["client_secret"], cur["refresh_token"]
	if id == "" || secret == "" || rt == "" {
		return nil, fmt.Errorf("zoho books: the connection is missing its client or refresh token: connect Zoho Books again: %w: %w", connector.ErrCredentialsRejected, effects.ErrFatal)
	}
	server, err := s.accountsServer()
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {id}, "client_secret": {secret}}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("zoho books: %w: %w", err, effects.ErrFatal)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	resp, err := s.req.HTTP.Do(r)
	if err != nil {
		return nil, fmt.Errorf("zoho books: refreshing the token: %w: %w", connector.RedactURLError(err), effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("zoho books: reading the token answer: %w: %w", err, effects.ErrRetryable)
	}
	var t struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		APIDomain   string  `json:"api_domain"`
		Error       string  `json:"error"`
	}
	_ = json.Unmarshal(body, &t)
	switch {
	case resp.StatusCode == http.StatusOK && t.AccessToken != "":
	case t.Error == "invalid_code" || t.Error == "invalid_token":
		// Zoho answers a revoked or unknown refresh token with 200 and
		// {"error": "invalid_code"}.
		return nil, fmt.Errorf("zoho books: Zoho refused the refresh token (%s): connect Zoho Books again: %w: %w", t.Error, connector.ErrCredentialsRejected, effects.ErrFatal)
	case t.Error == "Access Denied" || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		// Too many access tokens asked for in ten minutes, or Zoho's side.
		return nil, fmt.Errorf("zoho books: refreshing the token: http %d %s: %w", resp.StatusCode, t.Error, effects.ErrRetryable)
	default:
		// invalid_client and the like: Taskiem's Zoho client, not the
		// business's connection.
		return nil, fatalf("Zoho refused the token refresh: http %d %s", resp.StatusCode, t.Error)
	}
	out := make(map[string]string, len(cur)+2)
	for k, v := range cur {
		out[k] = v
	}
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	out["access_token"] = t.AccessToken
	out["access_expires_at"] = s.c.now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second).Format(time.RFC3339)
	return out, nil
}

// do calls Books, refreshing the token once when it is refused. org says
// whether the call names the organisation.
func (s *session) do(ctx context.Context, op, method, path string, q url.Values, body, out any, org bool) error {
	base, err := s.apiBase()
	if err != nil {
		return err
	}
	if q == nil {
		q = url.Values{}
	}
	if org {
		id, err := s.organization(ctx)
		if err != nil {
			return err
		}
		q.Set("organization_id", id)
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	tok, err := s.token(ctx, "")
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		var raw json.RawMessage
		err = connector.DoJSON(ctx, s.req.HTTP, method, u, map[string]string{"Authorization": "Zoho-oauthtoken " + tok}, body, &raw)
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
		var env struct {
			Code    *int   `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Code != nil && *env.Code != 0 {
			return classify(op, &connector.HTTPError{Status: http.StatusBadRequest, Body: raw})
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("zoho books %s: decoding the answer: %w: %w", op, err, effects.ErrUnknownOutcome)
			}
		}
		return nil
	}
}

type organization struct {
	ID       zid    `json:"organization_id"`
	Name     string `json:"name"`
	Currency string `json:"currency_code"`
	Country  string `json:"country"`
	Default  bool   `json:"is_default_org"`
}

func (s *session) organizations(ctx context.Context) ([]organization, error) {
	var resp struct {
		Organizations []organization `json:"organizations"`
	}
	err := s.do(ctx, "list_organizations", http.MethodGet, "/organizations", nil, nil, &resp, false)
	return resp.Organizations, err
}

// organization is the one this connection keeps: the only one the user
// reaches, or else Zoho's default, chosen on the first call and stored.
func (s *session) organization(ctx context.Context) (string, error) {
	if id := s.creds["organization_id"]; id != "" {
		return id, nil
	}
	orgs, err := s.organizations(ctx)
	if err != nil {
		return "", err
	}
	var pick *organization
	for i := range orgs {
		if len(orgs) == 1 || orgs[i].Default {
			pick = &orgs[i]
			break
		}
	}
	if pick == nil {
		return "", fatalf("the Zoho user reaches %d organisations and none is the default: make one the default in Zoho Books, or connect as a user of one", len(orgs))
	}
	id, name := string(pick.ID), pick.Name
	err = s.rotate(ctx, func(cur map[string]string) (map[string]string, error) {
		if cur["organization_id"] != "" {
			return cur, nil
		}
		out := make(map[string]string, len(cur)+2)
		for k, v := range cur {
			out[k] = v
		}
		out["organization_id"], out["organization_name"] = id, name
		return out, nil
	})
	if err != nil {
		return "", err
	}
	return s.creds["organization_id"], nil
}

// --- values ---

// zid is a Zoho id, which the API gives as a string or, in some answers, a
// number.
type zid string

func (z *zid) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*z = zid(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*z = zid(n.String())
	return nil
}

func amount(kobo int64) json.Number { return money.Number(kobo, 2) }

func kobo(n json.Number) int64 {
	if n == "" {
		return 0
	}
	return money.Round(n, 2)
}

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
		if f, err := v.Float64(); err == nil {
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

func boolean(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func (c *client) today() string { return c.now().UTC().Format(time.DateOnly) }

// zohoTime is a time as Books' filters take it (2026-10-26T10:00:00+0000).
func zohoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05-0700") }

// parseZohoTime reads Books' times (2026-10-26T10:00:00+0100).
func parseZohoTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
