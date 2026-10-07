// Package whatsapp is Taskiem's own WhatsApp channel (spec 11): the
// operator's platform number, through which people bound to it approve,
// trigger and check runs, and receive notifications. It is separate from
// the whatsapp@1 connector, which tenants use inside workflows with their
// own credentials.
//
// This package holds what does not depend on the API: the Cloud API client
// (sends through the egress guard), webhook signature checks and parsing,
// the template library, signed decision tokens, number formatting and the
// masking of what messages show. Built from Meta's public Cloud API
// documentation only (docs/integrations/whatsapp.md, docs/whatsapp.md).
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
)

// DefaultGraphURL is the Graph API version the channel speaks.
const DefaultGraphURL = "https://graph.facebook.com/v25.0"

// ErrWindowClosed: the person has not written in 24 hours, so only a
// template may be sent (Meta error 131047).
var ErrWindowClosed = errors.New("whatsapp: outside the 24-hour customer service window")

// GraphError is an error the Cloud API returned.
type GraphError struct {
	Status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
	Trace   string `json:"fbtrace_id"`
}

func (e *GraphError) Error() string {
	s := fmt.Sprintf("whatsapp %d (code %d): %s", e.Status, e.Code, e.Message)
	if e.Trace != "" {
		s += " (fbtrace_id " + e.Trace + ")"
	}
	return s
}

func (e *GraphError) Unwrap() error {
	if e.Code == 131047 {
		return ErrWindowClosed
	}
	return nil
}

// Client sends messages from the platform number.
type Client struct {
	// BaseURL is DefaultGraphURL unless overridden (tests).
	BaseURL       string
	PhoneNumberID string
	Token         string
	// Egress guards the calls; nil uses a default guard. HTTP, when set,
	// replaces the guarded client.
	Egress *egress.Guard
	HTTP   *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultGraphURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) client() (*http.Client, error) {
	if c.HTTP != nil {
		return c.HTTP, nil
	}
	u, err := url.Parse(c.base())
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("whatsapp: bad Graph API URL %q", c.BaseURL)
	}
	g := c.Egress
	if g == nil {
		g = &egress.Guard{}
	}
	return g.Client(egress.Policy{Tenant: "platform", Hosts: []string{u.Hostname()}, Purpose: "whatsapp_platform"}, 15*time.Second), nil
}

// Button is a reply button: its id comes back when tapped.
type Button struct {
	ID    string // at most 256 characters
	Title string // at most 20 characters
}

// SendText sends a free-form message (inside the window only).
func (c *Client) SendText(ctx context.Context, to, body string) (string, error) {
	return c.send(ctx, to, "text", map[string]any{"body": clip(body, 4096), "preview_url": false})
}

// SendButtons sends text with up to three reply buttons (inside the window
// only).
func (c *Client) SendButtons(ctx context.Context, to, body string, buttons []Button) (string, error) {
	if len(buttons) == 0 || len(buttons) > 3 {
		return "", errors.New("whatsapp: one to three buttons")
	}
	bs := make([]any, len(buttons))
	for i, b := range buttons {
		bs[i] = map[string]any{"type": "reply", "reply": map[string]any{"id": b.ID, "title": clip(b.Title, 20)}}
	}
	return c.send(ctx, to, "interactive", map[string]any{"type": "button", "body": map[string]any{"text": clip(body, 1024)}, "action": map[string]any{"buttons": bs}})
}

// SendTemplate sends an approved template with its body variables and,
// for quick-reply buttons, their payloads in order.
func (c *Client) SendTemplate(ctx context.Context, to string, t Template, lang string, vars map[string]string, payloads []string) (string, error) {
	tpl, err := t.Payload(lang, vars, payloads)
	if err != nil {
		return "", err
	}
	return c.send(ctx, to, "template", tpl)
}

func (c *Client) send(ctx context.Context, to, kind string, content any) (string, error) {
	if c.PhoneNumberID == "" || c.Token == "" {
		return "", errors.New("whatsapp: the platform number is not configured")
	}
	body, _ := json.Marshal(map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": to, "type": kind, kind: content})
	hc, err := c.client()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/"+url.PathEscape(c.PhoneNumberID)+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is harmless, but keep errors short
		}
		return "", fmt.Errorf("whatsapp: sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error *GraphError `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			e.Error.Status = resp.StatusCode
			return "", e.Error
		}
		return "", fmt.Errorf("whatsapp: Graph API answered %s", resp.Status)
	}
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Messages) == 0 {
		return "", errors.New("whatsapp: accepted without a message id")
	}
	return out.Messages[0].ID, nil
}

// clip shortens s to at most n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
