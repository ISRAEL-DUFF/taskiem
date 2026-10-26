// Package payrolla is the Payrolla connector (Connect API 2026-11-01): a
// company's payroll runs, each employee's pay status, the team, approval
// and payslip links, and the company's events, delivered to webhooks
// Taskiem subscribes itself (decision 0021). Payrolla is a product
// (decision 0044): nothing here moves money, approves a payroll or changes
// an employee. Built from Payrolla's reference
// (docs/integrations/payrolla-reference).
package payrolla

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

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector. LiveURL replaces Payrolla's production
// address (TASKIEM_PAYROLLA_URL), TestURL its sandbox's
// (TASKIEM_PAYROLLA_TEST_URL). BaseURL is where connections made before
// modes go (TASKIEM_PAYROLLA_URL, tests, the conformance kit's replay
// server).
type Options struct {
	LiveURL, TestURL string
	BaseURL          string
}

// New returns the Payrolla connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	m.SetModeURL("live", o.LiveURL)
	m.SetModeURL("test", o.TestURL)
	c := &client{m: m}
	regs := map[string]connector.Registrar{}
	for name, t := range m.Triggers {
		regs[name] = registrar{c: c, events: t.Events}
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_company":         connector.ActionFunc(c.getCompany),
		"list_runs":           connector.ActionFunc(c.listRuns),
		"get_run":             connector.ActionFunc(c.getRun),
		"get_run_lines":       connector.ActionFunc(c.getRunLines),
		"get_approval_link":   connector.ActionFunc(c.getApprovalLink),
		"list_employees":      connector.ActionFunc(c.listEmployees),
		"get_employee":        connector.ActionFunc(c.getEmployee),
		"create_payslip_link": connector.ActionFunc(c.createPayslipLink),
		"list_events":         connector.ActionFunc(c.listEvents),
	}, Registrars: regs}
}

// client reads the base URL from the manifest on each call, so an
// override (the conformance kit's replay server) applies at once.
type client struct{ m *connector.Manifest }

// Error is a refusal Payrolla explained: its error envelope's code
// (API_KEY_INVALID, SCOPE_REQUIRED, PAYSLIP_LINKS_DISABLED, …) and message.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("payrolla %d %s: %s", e.Status, e.Code, e.Message)
	if hint := hints[e.Code]; hint != "" {
		msg += " (" + hint + ")"
	}
	return msg
}

// hints say what the business can do about a refusal (Payrolla's guide,
// "Errors and limits").
var hints = map[string]string{
	"API_KEY_INVALID":           "the key was revoked or is wrong: create a new one in Payrolla → Settings → Connected apps and reconnect",
	"API_KEY_REQUIRED":          "the connection has no key: reconnect Payrolla",
	"SCOPE_REQUIRED":            "the key lacks this permission: the owner issues a new key that has it",
	"PAYSLIP_LINKS_DISABLED":    "the owner turns on payslip links in Payrolla → Settings → Connected apps",
	"PAYSLIP_NOT_FINAL":         "the run is not locked yet; ask again once it is",
	"NO_PHONE_FOR_VERIFICATION": "the employee has no phone in Payrolla to confirm the link with",
	"WEBHOOK_DISABLED":          "the owner turns the subscription back on in Payrolla → Settings → Connected apps",
}

// do sends one request and decodes the envelope's data into out. Failures
// keep the transport's classification (429 and 503 retryable, other 5xx
// unknown outcome, other 4xx fatal), 404 is connector.ErrNotFound, a
// revoked key is connector.ErrCredentialsRejected, and Payrolla's
// explanation is an *Error.
func (c *client) do(ctx context.Context, creds map[string]string, hc *http.Client, method, path string, body, out any) error {
	key := strings.TrimSpace(creds["api_key"])
	if key == "" {
		return fmt.Errorf("payrolla: the connection has no API key: %w", effects.ErrFatal)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	base, err := c.m.BaseFor(creds)
	if err != nil {
		return err
	}
	err = connector.DoJSON(ctx, hc, method, base+path, map[string]string{"Authorization": "Bearer " + key}, body, &env)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(he.Body, &e)
		pe := &Error{Status: he.Status, Code: e.Error.Code, Message: e.Error.Message}
		if pe.Message == "" {
			pe.Message = http.StatusText(he.Status)
		}
		switch {
		case he.Status == http.StatusNotFound:
			return fmt.Errorf("%w: %w", pe, connector.ErrNotFound)
		case he.Status == http.StatusUnauthorized && pe.Code == "API_KEY_INVALID":
			// Revoked or unknown: the engine marks the connection expired
			// rather than trying again.
			return fmt.Errorf("%w: %w: %w", pe, connector.ErrCredentialsRejected, he)
		}
		return fmt.Errorf("%w: %w", pe, he)
	}
	if err != nil {
		return err
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("payrolla: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

// get reads path into a generic value, the action's output.
func (c *client) get(ctx context.Context, req connector.Request, path string, q url.Values) (connector.Response, error) {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out map[string]any
	if err := c.do(ctx, req.Credentials, req.HTTP, http.MethodGet, path, nil, &out); err != nil {
		return connector.Response{}, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return connector.Response{Output: out}, nil
}

func str(in map[string]any, k string) string {
	switch v := in[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int, int64:
		return fmt.Sprint(v)
	}
	return ""
}

// id is a required id input, escaped for the path.
func id(in map[string]any, k string) (string, error) {
	v := str(in, k)
	if v == "" {
		return "", fmt.Errorf("payrolla: %s is required: %w", k, effects.ErrFatal)
	}
	return url.PathEscape(v), nil
}

// query is the inputs named, as Payrolla's query parameters (empty ones
// left out).
func query(in map[string]any, keys ...string) url.Values {
	q := url.Values{}
	for _, k := range keys {
		if v := str(in, k); v != "" {
			q.Set(k, v)
		}
	}
	return q
}

func (c *client) getCompany(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.get(ctx, req, "/connect/company", nil)
}

func (c *client) listRuns(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.get(ctx, req, "/connect/runs", query(req.Input, "status", "updated_since", "cursor", "limit"))
}

func (c *client) getRun(ctx context.Context, req connector.Request) (connector.Response, error) {
	run, err := id(req.Input, "run_id")
	if err != nil {
		return connector.Response{}, err
	}
	return c.get(ctx, req, "/connect/runs/"+run, nil)
}

func (c *client) getRunLines(ctx context.Context, req connector.Request) (connector.Response, error) {
	run, err := id(req.Input, "run_id")
	if err != nil {
		return connector.Response{}, err
	}
	return c.get(ctx, req, "/connect/runs/"+run+"/lines", nil)
}

func (c *client) getApprovalLink(ctx context.Context, req connector.Request) (connector.Response, error) {
	run, err := id(req.Input, "run_id")
	if err != nil {
		return connector.Response{}, err
	}
	return c.get(ctx, req, "/connect/runs/"+run+"/approval-link", nil)
}

func (c *client) listEmployees(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.get(ctx, req, "/connect/employees", query(req.Input, "status", "updated_since", "cursor", "limit"))
}

func (c *client) getEmployee(ctx context.Context, req connector.Request) (connector.Response, error) {
	emp, err := id(req.Input, "employee_id")
	if err != nil {
		return connector.Response{}, err
	}
	return c.get(ctx, req, "/connect/employees/"+emp, nil)
}

// createPayslipLink asks for a fresh payslip link. Payrolla issues another
// link each time and cancels none; it takes no idempotency key, so the
// action is an unsafe write.
func (c *client) createPayslipLink(ctx context.Context, req connector.Request) (connector.Response, error) {
	slip, err := id(req.Input, "payslip_id")
	if err != nil {
		return connector.Response{}, err
	}
	var out map[string]any
	if err := c.do(ctx, req.Credentials, req.HTTP, http.MethodPost, "/connect/payslips/"+slip+"/link", map[string]any{}, &out); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) listEvents(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.get(ctx, req, "/connect/events", query(req.Input, "after", "types", "limit"))
}
