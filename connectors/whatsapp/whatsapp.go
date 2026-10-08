// Package whatsapp is the WhatsApp Cloud API connector (Meta Graph API):
// text, template, media and interactive messages, read receipts, and a
// messages trigger for inbound messages and delivery statuses. Built from
// Meta's public Cloud API documentation (docs/integrations/whatsapp.md).
//
// Sends: POST /{phone-number-id}/messages with a bearer token. The response
// only says Meta accepted the message; delivery arrives as status events.
// Meta has no idempotency key for sends, so they are unsafe writes: a lost
// response parks the step for a person rather than risking a second
// message.
//
// Errors: Meta asks clients to act on error.code, not on HTTP status. Rate
// and throughput limits (4, 80007, 130429, 131056) and documented temporary
// conditions (2, 131016, 131057, 133004, 2494100) are refusals: nothing was
// sent, so they are retryable even for sends. 1 and 131000 ("unknown
// error") may follow an accepted message: unknown outcome. Everything else
// Meta explains (invalid parameters, permissions, expired tokens, policy)
// is fatal.
package whatsapp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector; BaseURL replaces
// https://graph.facebook.com/v25.0 (tests, another Graph API version).
type Options struct {
	BaseURL string
}

// New returns the WhatsApp connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_phone_number": connector.ActionFunc(c.getPhoneNumber),
		"send_text":        connector.ActionFunc(c.sendText),
		"send_template":    connector.ActionFunc(c.sendTemplate),
		"send_media":       connector.ActionFunc(c.sendMedia),
		"send_interactive": connector.ActionFunc(c.sendInteractive),
		"mark_as_read":     connector.ActionFunc(c.markAsRead),
	}}
}

type client struct{ base string }

// metaError is a Graph API error response.
type metaError struct {
	status  int
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
	Subcode int    `json:"error_subcode"`
	Data    struct {
		Details string `json:"details"`
	} `json:"error_data"`
	Trace string `json:"fbtrace_id"`
}

func (e *metaError) Error() string {
	s := fmt.Sprintf("whatsapp %d (code %d", e.status, e.Code)
	if e.Subcode != 0 {
		s += fmt.Sprintf(", subcode %d", e.Subcode)
	}
	s += "): " + e.Message
	if e.Data.Details != "" && !strings.Contains(e.Message, e.Data.Details) {
		s += ": " + e.Data.Details
	}
	if e.Trace != "" {
		s += " (fbtrace_id " + e.Trace + ")"
	}
	return s
}

// Codes Meta documents as limits or temporary conditions: the request was
// refused, so sending again later is safe.
var refused = map[int]bool{
	4:       true, // app API call rate limit
	80007:   true, // WhatsApp Business Account rate limit
	130429:  true, // Cloud API throughput reached
	131056:  true, // too many messages to the same recipient
	2:       true, // temporary: downtime or overloaded
	131016:  true, // service temporarily unavailable
	131057:  true, // account in maintenance mode (throughput upgrade)
	133004:  true, // server temporarily unavailable
	2494100: true, // phone number in maintenance mode
}

// Codes after which Meta may still have accepted the message.
var unknown = map[int]bool{
	1:      true, // invalid request or possible server error
	131000: true, // something went wrong
}

// classify turns a non-2xx response into an error the engine can act on.
func classify(he *connector.HTTPError) error {
	var body struct {
		Error *metaError `json:"error"`
	}
	_ = json.Unmarshal(he.Body, &body)
	me := body.Error
	if me == nil || me.Code == 0 && me.Message == "" {
		// Not a Graph error: keep the transport's classification.
		return he
	}
	me.status = he.Status
	switch {
	case refused[me.Code]:
		return fmt.Errorf("%w: %w: %w", me, effects.ErrRetryable, effects.ErrNotSent)
	case unknown[me.Code]:
		return fmt.Errorf("%w: %w", me, effects.ErrUnknownOutcome)
	case he.Status >= 500:
		// An undocumented code with a server error: Meta may have acted.
		return fmt.Errorf("%w: %w", me, effects.ErrUnknownOutcome)
	}
	return fmt.Errorf("%w: %w", me, effects.ErrFatal)
}

// do sends one Graph API request.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	tok := strings.TrimSpace(req.Credentials["access_token"])
	if tok == "" {
		return fmt.Errorf("whatsapp: the connection has no access_token: %w", effects.ErrFatal)
	}
	err := connector.DoJSON(ctx, req.HTTP, method, c.base+path, map[string]string{"Authorization": "Bearer " + tok}, body, out)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		return classify(he)
	}
	return err
}

func phoneNumberID(req connector.Request) (string, error) {
	id := strings.TrimSpace(req.Credentials["phone_number_id"])
	if id == "" {
		return "", fmt.Errorf("whatsapp: the connection has no phone_number_id: %w", effects.ErrFatal)
	}
	return url.PathEscape(id), nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func (c *client) getPhoneNumber(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := phoneNumberID(req)
	if err != nil {
		return connector.Response{}, err
	}
	var p struct {
		ID                     string `json:"id"`
		DisplayPhoneNumber     string `json:"display_phone_number"`
		VerifiedName           string `json:"verified_name"`
		QualityRating          string `json:"quality_rating"`
		CodeVerificationStatus string `json:"code_verification_status"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/"+id, nil, &p); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"id": p.ID, "display_phone_number": p.DisplayPhoneNumber, "verified_name": p.VerifiedName,
		"quality_rating": p.QualityRating, "code_verification_status": p.CodeVerificationStatus}}, nil
}

// send posts one message of type kind with content, adding the fields every
// message shares.
func (c *client) send(ctx context.Context, req connector.Request, kind string, content any) (connector.Response, error) {
	id, err := phoneNumberID(req)
	if err != nil {
		return connector.Response{}, err
	}
	to := strings.TrimSpace(str(req.Input, "to"))
	if to == "" {
		return connector.Response{}, fmt.Errorf("whatsapp: to (the recipient's number) is required: %w", effects.ErrFatal)
	}
	body := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": to, "type": kind, kind: content}
	if r := str(req.Input, "reply_to"); r != "" {
		body["context"] = map[string]any{"message_id": r}
	}
	if d := str(req.Input, "biz_opaque_callback_data"); d != "" {
		body["biz_opaque_callback_data"] = d
	}
	var resp struct {
		Contacts []struct {
			Input string `json:"input"`
			WaID  string `json:"wa_id"`
		} `json:"contacts"`
		Messages []struct {
			ID            string `json:"id"`
			MessageStatus string `json:"message_status"`
		} `json:"messages"`
	}
	if err := c.do(ctx, req, http.MethodPost, "/"+id+"/messages", body, &resp); err != nil {
		return connector.Response{}, err
	}
	if len(resp.Messages) == 0 || resp.Messages[0].ID == "" {
		// Accepted without a message id: undocumented; Meta may have sent it.
		return connector.Response{}, fmt.Errorf("whatsapp: Meta accepted the request without a message id: %w", effects.ErrUnknownOutcome)
	}
	out := map[string]any{"message_id": resp.Messages[0].ID, "message_status": resp.Messages[0].MessageStatus, "wa_id": "", "input": ""}
	if len(resp.Contacts) > 0 {
		out["wa_id"], out["input"] = resp.Contacts[0].WaID, resp.Contacts[0].Input
	}
	return connector.Response{Output: out}, nil
}

func (c *client) sendText(ctx context.Context, req connector.Request) (connector.Response, error) {
	text := map[string]any{"body": str(req.Input, "body")}
	if text["body"] == "" {
		return connector.Response{}, fmt.Errorf("whatsapp: body is required: %w", effects.ErrFatal)
	}
	if p, ok := req.Input["preview_url"].(bool); ok {
		text["preview_url"] = p
	}
	return c.send(ctx, req, "text", text)
}

func (c *client) sendTemplate(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if str(in, "name") == "" || str(in, "language") == "" {
		return connector.Response{}, fmt.Errorf("whatsapp: a template needs name and language: %w", effects.ErrFatal)
	}
	tpl := map[string]any{"name": str(in, "name"), "language": map[string]any{"code": str(in, "language")}}
	if comps, ok := in["components"].([]any); ok && len(comps) > 0 {
		tpl["components"] = comps
	} else if in["components"] != nil && !ok {
		return connector.Response{}, fmt.Errorf("whatsapp: components must be a list: %w", effects.ErrFatal)
	}
	return c.send(ctx, req, "template", tpl)
}

func (c *client) sendMedia(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	kind := str(in, "media_type")
	switch kind {
	case "image", "document", "audio", "video", "sticker":
	default:
		return connector.Response{}, fmt.Errorf("whatsapp: media_type must be image, document, audio, video or sticker, got %q: %w", kind, effects.ErrFatal)
	}
	link, mediaID := strings.TrimSpace(str(in, "link")), strings.TrimSpace(str(in, "media_id"))
	if (link == "") == (mediaID == "") {
		return connector.Response{}, fmt.Errorf("whatsapp: give exactly one of link and media_id: %w", effects.ErrFatal)
	}
	media := map[string]any{}
	if link != "" {
		media["link"] = link
	} else {
		media["id"] = mediaID
	}
	if cp := str(in, "caption"); cp != "" {
		if kind == "audio" || kind == "sticker" {
			return connector.Response{}, fmt.Errorf("whatsapp: %s messages take no caption: %w", kind, effects.ErrFatal)
		}
		media["caption"] = cp
	}
	if fn := str(in, "filename"); fn != "" {
		if kind != "document" {
			return connector.Response{}, fmt.Errorf("whatsapp: only documents take a filename: %w", effects.ErrFatal)
		}
		media["filename"] = fn
	}
	return c.send(ctx, req, kind, media)
}

func (c *client) sendInteractive(ctx context.Context, req connector.Request) (connector.Response, error) {
	ia, ok := req.Input["interactive"].(map[string]any)
	if !ok || str(ia, "type") == "" || ia["action"] == nil {
		return connector.Response{}, fmt.Errorf("whatsapp: interactive needs at least type and action: %w", effects.ErrFatal)
	}
	return c.send(ctx, req, "interactive", ia)
}

func (c *client) markAsRead(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := phoneNumberID(req)
	if err != nil {
		return connector.Response{}, err
	}
	msg := strings.TrimSpace(str(req.Input, "message_id"))
	if msg == "" {
		return connector.Response{}, fmt.Errorf("whatsapp: message_id (a wamid) is required: %w", effects.ErrFatal)
	}
	var r struct {
		Success bool `json:"success"`
	}
	body := map[string]any{"messaging_product": "whatsapp", "status": "read", "message_id": msg}
	if err := c.do(ctx, req, http.MethodPost, "/"+id+"/messages", body, &r); err != nil {
		return connector.Response{}, err
	}
	if !r.Success {
		return connector.Response{}, fmt.Errorf("whatsapp: mark as read returned success false: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"success": true}}, nil
}
