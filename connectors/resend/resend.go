// Package resend is the Resend connector: transactional email from the
// business's own verified domain, sent once by Resend's Idempotency-Key,
// and Resend's email events through a webhook Taskiem creates (decision
// 0021), signed by Svix. Built from Resend's public API reference
// (docs/integrations/resend.md).
//
// Errors: Resend answers {"statusCode", "name", "message"}. 401 and 403
// for the key are a key Resend refuses (the connection is expired); a 403
// for the from domain is fatal; 409 concurrent_idempotent_requests is
// retried, and invalid_idempotent_request (the same key with another body)
// is fatal; 429 is a rate limit (retried) unless it is a daily or monthly
// quota (fatal: nothing will send until it resets); 5xx is an unknown
// outcome, retried under the same key.
package resend

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector: BaseURL replaces https://api.resend.com
// (tests, a proxy).
type Options struct{ BaseURL string }

// New returns the Resend connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{m: m}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_domains": connector.ActionFunc(c.listDomains),
		"send_email":   connector.ActionFunc(c.sendEmail),
		"get_email":    connector.ActionFunc(c.getEmail),
	}, Registrars: map[string]connector.Registrar{"email_events": &webhooks{c: c, events: m.Triggers["email_events"].Events}}}
}

type client struct{ m *connector.Manifest }

// Error is a Resend refusal.
type Error struct {
	Status  int
	Name    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("resend: http %d: %s (%s)", e.Status, e.Message, e.Name)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("resend %s: %w", op, err)
	}
	var b struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(he.Body, &b)
	re := &Error{Status: he.Status, Name: b.Name, Message: b.Message}
	if re.Message == "" {
		re.Message = http.StatusText(he.Status)
	}
	key := strings.Contains(strings.ToLower(re.Message), "api key")
	switch {
	case he.Status == http.StatusUnauthorized, b.Name == "invalid_api_key", b.Name == "suspended_api_key", (he.Status == http.StatusForbidden || he.Status == http.StatusBadRequest) && key:
		return fmt.Errorf("resend %s: %w: replace the API key on the connection: %w: %w", op, re, connector.ErrCredentialsRejected, effects.ErrFatal)
	case b.Name == "concurrent_idempotent_requests":
		return fmt.Errorf("resend %s: %w: %w", op, re, effects.ErrRetryable)
	case b.Name == "daily_quota_exceeded" || b.Name == "monthly_quota_exceeded":
		return fmt.Errorf("resend %s: %w: %w", op, re, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("resend %s: %w: %w", op, re, he)
	case he.Status == http.StatusNotFound:
		return fmt.Errorf("resend %s: %w: %w", op, re, connector.ErrNotFound)
	}
	return fmt.Errorf("resend %s: %w: %w", op, re, effects.ErrFatal)
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("resend: "+format+": %w", append(a, effects.ErrFatal)...)
}

func (c *client) call(ctx context.Context, creds map[string]string, hc *http.Client, op, method, path string, headers map[string]string, body, out any) error {
	key := creds["api_key"]
	if key == "" {
		return fatalf("the connection has no API key")
	}
	h := map[string]string{"Authorization": "Bearer " + key}
	for k, v := range headers {
		h[k] = v
	}
	if err := connector.DoJSON(ctx, hc, method, strings.TrimRight(c.m.BaseURL, "/")+path, h, body, out); err != nil {
		return classify(op, err)
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func list(m map[string]any, k string) []string {
	var out []string
	switch v := m[k].(type) {
	case string:
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// --- domains ---

type domain struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Region string `json:"region"`
}

func (c *client) domains(ctx context.Context, req connector.Request, op string) ([]domain, error) {
	var resp struct {
		Data []domain `json:"data"`
	}
	err := c.call(ctx, req.Credentials, req.HTTP, op, http.MethodGet, "/domains", nil, nil, &resp)
	return resp.Data, err
}

func (c *client) listDomains(ctx context.Context, req connector.Request) (connector.Response, error) {
	ds, err := c.domains(ctx, req, "list_domains")
	if err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]any{"id": d.ID, "name": d.Name, "status": d.Status, "verified": d.Status == "verified", "region": d.Region})
	}
	return connector.Response{Output: map[string]any{"domains": out}}, nil
}

// domainOf is an address's domain, lower case.
func domainOf(addr string) (string, bool) {
	a, err := mail.ParseAddress(addr)
	if err != nil {
		return "", false
	}
	_, d, ok := strings.Cut(a.Address, "@")
	return strings.ToLower(d), ok && d != ""
}

// --- sending ---

func (c *client) sendEmail(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	from, subject, to := str(in, "from"), str(in, "subject"), list(in, "to")
	if from == "" || subject == "" || len(to) == 0 {
		return connector.Response{}, fatalf("send_email: from, to and subject are required")
	}
	if str(in, "html") == "" && str(in, "text") == "" {
		return connector.Response{}, fatalf("send_email: give html or text")
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fatalf("send_email: no idempotency key: the engine always sends one")
	}
	// The domain is the business's: verified in Resend, or nothing is sent.
	d, ok := domainOf(from)
	if !ok {
		return connector.Response{}, fatalf("send_email: %q is not an email address", from)
	}
	ds, err := c.domains(ctx, req, "send_email")
	if err != nil {
		return connector.Response{}, err
	}
	verified := false
	for _, x := range ds {
		verified = verified || strings.EqualFold(x.Name, d) && x.Status == "verified"
	}
	if !verified {
		return connector.Response{}, fatalf("send_email: %s is not a domain verified in Resend: verify it there (Domains), or send from one that is", d)
	}
	body := map[string]any{"from": from, "to": to, "subject": subject}
	for _, k := range []string{"html", "text", "scheduled_at"} {
		if v := str(in, k); v != "" {
			body[k] = v
		}
	}
	for _, k := range []string{"cc", "bcc", "reply_to"} {
		if v := list(in, k); len(v) > 0 {
			body[k] = v
		}
	}
	if tags, _ := in["tags"].([]any); len(tags) > 0 {
		body["tags"] = tags
	}
	if atts, _ := in["attachments"].([]any); len(atts) > 0 {
		var out []map[string]any
		for i, v := range atts {
			a, _ := v.(map[string]any)
			att := map[string]any{"filename": str(a, "filename")}
			switch {
			case str(a, "content_base64") != "":
				att["content"] = str(a, "content_base64")
			case str(a, "path") != "":
				if u, err := url.Parse(str(a, "path")); err != nil || u.Scheme != "https" {
					return connector.Response{}, fatalf("send_email: attachment %d: path must be an https link", i+1)
				}
				att["path"] = str(a, "path")
			default:
				return connector.Response{}, fatalf("send_email: attachment %d has neither content_base64 nor path", i+1)
			}
			if ct := str(a, "content_type"); ct != "" {
				att["content_type"] = ct
			}
			out = append(out, att)
		}
		body["attachments"] = out
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_email", http.MethodPost, "/emails", map[string]string{"Idempotency-Key": req.IdempotencyKey}, body, &resp); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"email_id": resp.ID}}, nil
}

func (c *client) getEmail(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "email_id")
	if id == "" {
		return connector.Response{}, fatalf("get_email: email_id is required")
	}
	var e struct {
		ID          string   `json:"id"`
		From        string   `json:"from"`
		To          []string `json:"to"`
		Subject     string   `json:"subject"`
		CreatedAt   string   `json:"created_at"`
		LastEvent   string   `json:"last_event"`
		ScheduledAt *string  `json:"scheduled_at"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "get_email", http.MethodGet, "/emails/"+url.PathEscape(id), nil, nil, &e); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"email_id": e.ID, "from": e.From, "to": e.To, "subject": e.Subject, "created_at": e.CreatedAt, "last_event": e.LastEvent}
	if e.ScheduledAt != nil {
		out["scheduled_at"] = *e.ScheduledAt
	}
	return connector.Response{Output: out}, nil
}
