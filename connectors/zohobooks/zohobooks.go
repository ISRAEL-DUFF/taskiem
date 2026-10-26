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
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/connectors/internal/zoho"
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
	c := &client{m: m, now: time.Now}
	if o.Now != nil {
		c.now = o.Now
	}
	c.zoho = &zoho.Client{Manifest: m, Name: "Zoho Books", AccountsServer: o.AccountsServer, Now: c.now}
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
		c.fixed = true
	}
	if p, err := url.Parse(o.AccountsServer); err == nil && p.Hostname() != "" {
		m.Egress = append(m.Egress, p.Hostname())
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
	m    *connector.Manifest
	zoho *zoho.Client
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

// session is one action's use of the connection: Zoho's tokens, and the
// organisation.
type session struct {
	*zoho.Session
	c *client
}

func (c *client) open(req connector.Request) *session {
	return &session{Session: c.zoho.Open(req), c: c}
}

// apiBase is where the connection's calls go: its account's API domain,
// or the base URL where the deployment fixes one or the connection names
// none.
func (s *session) apiBase() (string, error) {
	if s.c.fixed || strings.TrimSpace(s.Creds["api_domain"]) == "" {
		return strings.TrimRight(s.c.m.BaseURL, "/"), nil
	}
	d, err := s.Server("api_domain")
	return d + "/books/v3", err
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
	var raw json.RawMessage
	if err := s.Do(ctx, method, u, body, &raw); err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			return classify(op, err)
		}
		return err
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
	if id := s.Creds["organization_id"]; id != "" {
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
	if err := s.Keep(ctx, map[string]string{"organization_id": string(pick.ID), "organization_name": pick.Name}); err != nil {
		return "", err
	}
	return s.Creds["organization_id"], nil
}

// --- values ---

// zid is a Zoho id.
type zid = zoho.ID

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

// zohoTime is a time as Books' filters take it.
func zohoTime(t time.Time) string { return zoho.Time(t) }

// parseZohoTime reads Books' times.
func parseZohoTime(s string) (time.Time, bool) { return zoho.ParseTime(s) }
