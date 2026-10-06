// Package telegram is the Telegram Bot API connector: messages, documents
// and photos, edits, button answers, the bot's webhook, and an update
// trigger. Built from Telegram's public Bot API reference
// (docs/integrations/telegram.md).
//
// Every call is POST https://api.telegram.org/bot<token>/<method> with a
// JSON body; Telegram answers {"ok": bool, "result" | "description",
// "error_code", "parameters"}. The token is part of the URL, so errors are
// scrubbed of it before they leave the connector.
//
// Sends are not idempotent at Telegram (there is no key), so they are
// unsafe writes: a lost response parks the step for a person. A 429 is
// flood control refusing the request, which proves nothing was sent, so it
// is retryable even for sends (after parameters.retry_after seconds).
package telegram

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector; BaseURL replaces https://api.telegram.org
// (tests, a local Bot API server).
type Options struct {
	BaseURL string
}

// New returns the Telegram connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_me":                connector.ActionFunc(c.getMe),
		"get_chat":              connector.ActionFunc(c.getChat),
		"send_message":          connector.ActionFunc(c.sendMessage),
		"send_photo":            connector.ActionFunc(c.sendMedia("sendPhoto", "photo")),
		"send_document":         connector.ActionFunc(c.sendMedia("sendDocument", "document")),
		"edit_message_text":     connector.ActionFunc(c.editMessageText),
		"answer_callback_query": connector.ActionFunc(c.answerCallbackQuery),
		"set_webhook":           connector.ActionFunc(c.setWebhook),
		"delete_webhook":        connector.ActionFunc(c.deleteWebhook),
		"get_webhook_info":      connector.ActionFunc(c.getWebhookInfo),
	}}
}

type client struct{ base string }

// apiError is a refusal Telegram explained.
type apiError struct {
	method      string
	status      int
	description string
	retryAfter  int
	migrateTo   int64
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("telegram %s: %d %s", e.method, e.status, e.description)
	if e.retryAfter > 0 {
		s += fmt.Sprintf(" (retry after %ds)", e.retryAfter)
	}
	if e.migrateTo != 0 {
		s += fmt.Sprintf(" (the group is now supergroup %d; use that chat id)", e.migrateTo)
	}
	return s
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  *struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// scrubbed hides the bot token in an error's text and keeps its
// classification (errors.Is still sees the effects sentinels).
type scrubbed struct {
	msg string
	err error
}

func (s *scrubbed) Error() string { return s.msg }
func (s *scrubbed) Unwrap() error { return s.err }

func scrub(err error, token string) error {
	if err == nil || token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return &scrubbed{msg: strings.ReplaceAll(err.Error(), token, "<bot_token>"), err: err}
}

// call invokes one Bot API method and decodes "result" into out.
func (c *client) call(ctx context.Context, req connector.Request, method string, body map[string]any, out *any) error {
	tok := strings.TrimSpace(req.Credentials["bot_token"])
	if tok == "" {
		return fmt.Errorf("telegram: the connection has no bot_token: %w", effects.ErrFatal)
	}
	if body == nil {
		body = map[string]any{}
	}
	var env envelope
	err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base+"/bot"+tok+"/"+method, nil, body, &env)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e envelope
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{method: method, status: he.Status, description: e.Description}
		if ae.description == "" {
			ae.description = strings.TrimSpace(string(he.Body))
		}
		if p := e.Parameters; p != nil {
			ae.retryAfter, ae.migrateTo = p.RetryAfter, p.MigrateToChatID
		}
		if he.Status == http.StatusTooManyRequests {
			// Flood control refused the request: nothing was sent, so even
			// a send may go again once retry_after has passed.
			if ae.retryAfter > 0 {
				he.RetryAfter = time.Duration(ae.retryAfter) * time.Second
			}
			return scrub(fmt.Errorf("%w: %w: %w", ae, he, effects.ErrNotSent), tok)
		}
		// Otherwise keep the transport's classification: other 4xx fatal,
		// 5xx unknown outcome.
		return scrub(fmt.Errorf("%w: %w", ae, he), tok)
	}
	if err != nil {
		return scrub(err, tok)
	}
	if !env.OK {
		// A 2xx that says it failed is not documented; Telegram may have acted.
		return fmt.Errorf("telegram %s: ok false: %s: %w", method, env.Description, effects.ErrUnknownOutcome)
	}
	if out != nil {
		v, err := decode(env.Result)
		if err != nil {
			return fmt.Errorf("telegram %s: unreadable result: %w: %w", method, err, effects.ErrUnknownOutcome)
		}
		*out = v
	}
	return nil
}

func telegramError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// decode reads JSON keeping integers exact (chat ids exceed 32 bits).
func decode(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return normalise(v), nil
}

func normalise(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, c := range t {
			t[k] = normalise(c)
		}
	case []any:
		for i, c := range t {
			t[i] = normalise(c)
		}
	}
	return v
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// intOf reads an integer input (JSON numbers arrive as float64, json.Number
// or Go integers).
func intOf(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// idString renders a Telegram id for correlation and outputs.
func idString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case nil:
		return ""
	}
	if i, ok := intOf(v); ok {
		return strconv.FormatInt(i, 10)
	}
	return fmt.Sprint(v)
}

var numericID = regexp.MustCompile(`^-?[0-9]+$`)

// chatID accepts a numeric id (as a number or a string) or an @username.
func chatID(in map[string]any, required bool) (any, error) {
	v, ok := in["chat_id"]
	if !ok || v == nil || v == "" {
		if required {
			return nil, fmt.Errorf("telegram: chat_id is required: %w", effects.ErrFatal)
		}
		return nil, nil
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if numericID.MatchString(s) {
			n, err := strconv.ParseInt(s, 10, 64)
			if err == nil {
				return n, nil
			}
		}
		return s, nil
	}
	if n, ok := intOf(v); ok {
		return n, nil
	}
	return nil, fmt.Errorf("telegram: chat_id must be a number or @username, got %v: %w", v, effects.ErrFatal)
}

// copyOptional moves the optional inputs Telegram takes as they are.
func copyOptional(dst, in map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := in[k]; ok && v != nil && v != "" {
			dst[k] = v
		}
	}
}

func messageOut(v any) map[string]any {
	m, _ := v.(map[string]any)
	out := map[string]any{"message_id": int64(0), "chat_id": "", "date": int64(0), "message": m}
	if m == nil {
		out["message"] = map[string]any{}
		return out
	}
	if id, ok := intOf(m["message_id"]); ok {
		out["message_id"] = id
	}
	if d, ok := intOf(m["date"]); ok {
		out["date"] = d
	}
	if chat, ok := m["chat"].(map[string]any); ok {
		out["chat_id"] = idString(chat["id"])
	}
	return out
}

func (c *client) getMe(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r any
	if err := c.call(ctx, req, "getMe", nil, &r); err != nil {
		return connector.Response{}, err
	}
	u, _ := r.(map[string]any)
	if u == nil {
		u = map[string]any{}
	}
	isBot, _ := u["is_bot"].(bool)
	return connector.Response{Output: map[string]any{"id": idString(u["id"]), "is_bot": isBot, "first_name": str(u, "first_name"), "username": str(u, "username")}}, nil
}

func (c *client) getChat(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := chatID(req.Input, true)
	if err != nil {
		return connector.Response{}, err
	}
	var r any
	if err := c.call(ctx, req, "getChat", map[string]any{"chat_id": id}, &r); err != nil {
		return connector.Response{}, err
	}
	ch, _ := r.(map[string]any)
	if ch == nil {
		ch = map[string]any{}
	}
	return connector.Response{Output: map[string]any{"id": idString(ch["id"]), "type": str(ch, "type"), "title": str(ch, "title"),
		"username": str(ch, "username"), "first_name": str(ch, "first_name"), "last_name": str(ch, "last_name"), "chat": ch}}, nil
}

// replyTo turns reply_to_message_id into Telegram's reply_parameters.
func replyTo(body, in map[string]any) error {
	v, ok := in["reply_to_message_id"]
	if !ok || v == nil {
		return nil
	}
	id, ok := intOf(v)
	if !ok {
		return fmt.Errorf("telegram: reply_to_message_id must be a whole number: %w", effects.ErrFatal)
	}
	rp := map[string]any{"message_id": id}
	if b, ok := in["allow_sending_without_reply"].(bool); ok {
		rp["allow_sending_without_reply"] = b
	}
	body["reply_parameters"] = rp
	return nil
}

func linkPreview(body, in map[string]any) {
	if b, ok := in["disable_link_preview"].(bool); ok && b {
		body["link_preview_options"] = map[string]any{"is_disabled": true}
	}
}

func (c *client) sendMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id, err := chatID(in, true)
	if err != nil {
		return connector.Response{}, err
	}
	if str(in, "text") == "" {
		return connector.Response{}, fmt.Errorf("telegram: text is required: %w", effects.ErrFatal)
	}
	body := map[string]any{"chat_id": id, "text": str(in, "text")}
	copyOptional(body, in, "parse_mode", "reply_markup", "message_thread_id", "disable_notification", "protect_content")
	linkPreview(body, in)
	if err := replyTo(body, in); err != nil {
		return connector.Response{}, err
	}
	var r any
	if err := c.call(ctx, req, "sendMessage", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: messageOut(r)}, nil
}

// sendMedia sends a photo or document by URL or file_id (field names the
// input and Telegram's parameter).
func (c *client) sendMedia(method, field string) func(context.Context, connector.Request) (connector.Response, error) {
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		in := req.Input
		id, err := chatID(in, true)
		if err != nil {
			return connector.Response{}, err
		}
		if strings.TrimSpace(str(in, field)) == "" {
			return connector.Response{}, fmt.Errorf("telegram: %s (an HTTP URL or a file_id) is required: %w", field, effects.ErrFatal)
		}
		body := map[string]any{"chat_id": id, field: strings.TrimSpace(str(in, field))}
		copyOptional(body, in, "caption", "parse_mode", "reply_markup", "message_thread_id", "disable_notification", "protect_content")
		if err := replyTo(body, in); err != nil {
			return connector.Response{}, err
		}
		var r any
		if err := c.call(ctx, req, method, body, &r); err != nil {
			return connector.Response{}, err
		}
		return connector.Response{Output: messageOut(r)}, nil
	}
}

func (c *client) editMessageText(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	body := map[string]any{"text": str(in, "text")}
	if str(in, "text") == "" {
		return connector.Response{}, fmt.Errorf("telegram: text is required: %w", effects.ErrFatal)
	}
	var msgID int64
	if im := str(in, "inline_message_id"); im != "" {
		body["inline_message_id"] = im
	} else {
		id, err := chatID(in, false)
		if err != nil {
			return connector.Response{}, err
		}
		n, ok := intOf(in["message_id"])
		if id == nil || !ok {
			return connector.Response{}, fmt.Errorf("telegram: edit_message_text needs chat_id and message_id, or inline_message_id: %w", effects.ErrFatal)
		}
		body["chat_id"], body["message_id"], msgID = id, n, n
	}
	copyOptional(body, in, "parse_mode", "reply_markup")
	linkPreview(body, in)
	var r any
	err := c.call(ctx, req, "editMessageText", body, &r)
	if ae := telegramError(err); ae != nil && ae.status == http.StatusBadRequest && strings.Contains(strings.ToLower(ae.description), "message is not modified") {
		// Already as asked: an earlier attempt, or the same text.
		return connector.Response{Output: map[string]any{"modified": false, "message_id": msgID, "chat_id": idString(body["chat_id"]), "message": map[string]any{}}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"modified": true, "message_id": msgID, "chat_id": idString(body["chat_id"]), "message": map[string]any{}}
	if m, ok := r.(map[string]any); ok {
		mo := messageOut(m)
		out["message_id"], out["chat_id"], out["message"] = mo["message_id"], mo["chat_id"], m
	}
	return connector.Response{Output: out}, nil
}

func (c *client) answerCallbackQuery(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if str(in, "callback_query_id") == "" {
		return connector.Response{}, fmt.Errorf("telegram: callback_query_id is required: %w", effects.ErrFatal)
	}
	body := map[string]any{"callback_query_id": str(in, "callback_query_id")}
	copyOptional(body, in, "text", "show_alert", "url", "cache_time")
	if err := c.call(ctx, req, "answerCallbackQuery", body, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"answered": true}}, nil
}

// secretToken is Telegram's rule for secret_token.
var secretToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (c *client) setWebhook(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	u := strings.TrimSpace(str(in, "url"))
	if !strings.HasPrefix(u, "https://") {
		return connector.Response{}, fmt.Errorf("telegram: the webhook url must be https: %w", effects.ErrFatal)
	}
	secret := req.Credentials["webhook_secret"]
	if !secretToken.MatchString(secret) {
		return connector.Response{}, fmt.Errorf("telegram: the connection needs a webhook_secret of 1-256 characters from A-Z, a-z, 0-9, _ and -, so deliveries can be verified: %w", effects.ErrFatal)
	}
	body := map[string]any{"url": u, "secret_token": secret}
	copyOptional(body, in, "allowed_updates", "drop_pending_updates", "max_connections", "ip_address")
	if err := c.call(ctx, req, "setWebhook", body, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"set": true}}, nil
}

func (c *client) deleteWebhook(ctx context.Context, req connector.Request) (connector.Response, error) {
	body := map[string]any{}
	copyOptional(body, req.Input, "drop_pending_updates")
	if err := c.call(ctx, req, "deleteWebhook", body, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"deleted": true}}, nil
}

func (c *client) getWebhookInfo(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r any
	if err := c.call(ctx, req, "getWebhookInfo", nil, &r); err != nil {
		return connector.Response{}, err
	}
	w, _ := r.(map[string]any)
	if w == nil {
		w = map[string]any{}
	}
	num := func(k string) int64 { n, _ := intOf(w[k]); return n }
	custom, _ := w["has_custom_certificate"].(bool)
	allowed := []any{}
	if a, ok := w["allowed_updates"].([]any); ok {
		allowed = a
	}
	return connector.Response{Output: map[string]any{"url": str(w, "url"), "has_custom_certificate": custom,
		"pending_update_count": num("pending_update_count"), "ip_address": str(w, "ip_address"), "last_error_date": num("last_error_date"),
		"last_error_message": str(w, "last_error_message"), "max_connections": num("max_connections"), "allowed_updates": allowed}}, nil
}
