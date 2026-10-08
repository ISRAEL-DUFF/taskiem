// Package slack is the Slack connector: the Web API with a bot token
// (messages, reactions, channels, users, file uploads) and the Events API
// and interactivity as triggers. Built from Slack's public developer
// documentation (docs/integrations/slack.md).
//
// Errors: Slack answers most refusals with HTTP 200 and {"ok": false,
// "error": "<code>"}. Rate limits (HTTP 429, or ratelimited) prove nothing
// was done and are retried, honouring Retry-After; refusals of the request
// itself (invalid_auth, channel_not_found, invalid_blocks, ...) are fatal;
// internal_error and fatal_error, which Slack says may have partly
// succeeded, are unknown outcomes.
package slack

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// maxUpload bounds upload_file's content.
const maxUpload = 50 << 20

// Options configure the connector; BaseURL replaces https://slack.com/api
// (tests, a proxy).
type Options struct {
	BaseURL string
}

// New returns the Slack connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	if o.BaseURL != "" {
		c.base = strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"auth_test":            connector.ActionFunc(c.authTest),
		"post_message":         connector.ActionFunc(c.postMessage),
		"update_message":       connector.ActionFunc(c.updateMessage),
		"post_ephemeral":       connector.ActionFunc(c.postEphemeral),
		"add_reaction":         connector.ActionFunc(c.addReaction),
		"list_conversations":   connector.ActionFunc(c.listConversations),
		"get_conversation":     connector.ActionFunc(c.getConversation),
		"lookup_user_by_email": connector.ActionFunc(c.lookupUserByEmail),
		"upload_file":          connector.ActionFunc(c.uploadFile),
	}}
}

type client struct{ base string }

// APIError is a Web API refusal: {"ok": false, "error": Code}, or an HTTP
// error status.
type APIError struct {
	Method     string
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
	}
	return fmt.Sprintf("slack %s: http %d", e.Method, e.Status)
}

// Code returns the Slack error code in err, if any.
func Code(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// Error codes by class. Slack's lists are per method and "other errors can
// be returned"; codes not named here are classified by their shape, and
// anything else is an unknown outcome (the safe default).
var (
	// Refused before anything was done: safe to send again, even a post.
	rateLimited = map[string]bool{"ratelimited": true, "rate_limited": true}
	// Temporary; Slack does not say whether anything was done.
	temporary = map[string]bool{"service_unavailable": true, "team_added_to_org": true, "org_login_required": true, "request_timeout": true}
	// Slack says "it's possible some aspect of the operation succeeded".
	maybeDone = map[string]bool{"internal_error": true, "fatal_error": true}
	// The request itself was refused.
	refused = map[string]bool{
		"access_denied": true, "accesslimited": true, "account_inactive": true, "already_reacted": true,
		"app_access_restricted": true, "as_user_not_supported": true, "bad_timestamp": true, "block_mismatch": true,
		"blocked_file_type": true, "cant_broadcast_message": true, "cant_update_message": true, "channel_not_found": true,
		"deprecated_endpoint": true, "edit_window_closed": true, "ekm_access_denied": true, "enterprise_is_restricted": true,
		"external_channel_migrating": true, "file_deleted": true, "file_is_deleted": true, "file_share_limit_reached": true,
		"is_archived": true, "is_inactive": true, "markdown_text_conflict": true, "message_limit_exceeded": true,
		"messages_tab_disabled": true, "metadata_must_be_sent_from_app": true, "method_deprecated": true,
		"msg_blocks_too_long": true, "no_dual_broadcast_content_update": true, "no_permission": true, "no_text": true,
		"not_authed": true, "not_allowed_token_type": true, "not_in_channel": true, "send_on_behalf_not_allowed": true,
		"team_access_not_granted": true, "team_not_found": true, "token_expired": true, "token_revoked": true,
		"two_factor_setup_required": true, "users_not_found": true, "user_not_found": true, "user_not_in_channel": true,
		"cannot_reply_to_message": true, "attachment_payload_limit_exceeded": true, "channels_limit_exceeded": true,
		"alt_txt_too_large": true, "max_file_sharing_exceeded": true,
	}
	refusedPrefixes = []string{"invalid_", "missing_", "not_", "restricted_action", "too_many_", "cant_", "cannot_", "file_upload"}
	refusedSuffixes = []string{"_not_found", "_too_long", "_too_large", "_disabled", "_restricted", "_not_allowed", "_conflict"}
)

// classify wraps a Slack error code with the engine's error class.
func classify(ae *APIError) error {
	c := ae.Code
	switch {
	case rateLimited[c]:
		return fmt.Errorf("%w: %w: %w", ae, effects.ErrRetryable, effects.ErrNotSent)
	case temporary[c]:
		return fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
	case maybeDone[c]:
		return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
	case refused[c]:
		return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	}
	for _, p := range refusedPrefixes {
		if strings.HasPrefix(c, p) {
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		}
	}
	for _, s := range refusedSuffixes {
		if strings.HasSuffix(c, s) {
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		}
	}
	return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
}

// call invokes a Web API method. args is sent as JSON when it is a map (the
// write methods, which take Block Kit arrays) and form-encoded when it is
// url.Values (the read and file methods, as every method accepts). The
// response is decoded into out after its ok flag is checked.
func (c *client) call(ctx context.Context, req connector.Request, method string, args any, out any) error {
	tok := strings.TrimSpace(req.Credentials["bot_token"])
	if tok == "" {
		return fmt.Errorf("slack: the connection has no bot_token: %w", effects.ErrFatal)
	}
	var body []byte
	var ctype string
	switch a := args.(type) {
	case url.Values:
		body, ctype = []byte(a.Encode()), "application/x-www-form-urlencoded"
	case nil:
		body, ctype = nil, ""
	default:
		var err error
		if body, err = json.Marshal(a); err != nil {
			return fmt.Errorf("slack %s: encoding: %w: %w", method, err, effects.ErrFatal)
		}
		ctype = "application/json; charset=utf-8"
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack %s: %w: %w", method, err, effects.ErrFatal)
	}
	if ctype != "" {
		hr.Header.Set("Content-Type", ctype)
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("Authorization", "Bearer "+tok)
	resp, err := req.HTTP.Do(hr)
	if err != nil {
		return connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("slack %s: reading the response: %w: %w", method, err, effects.ErrUnknownOutcome)
	}
	retryAfter := time.Duration(0)
	if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s >= 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	jerr := json.Unmarshal(raw, &env)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Method: method, Status: resp.StatusCode, Code: env.Error, RetryAfter: retryAfter}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			ae.Code = "ratelimited"
			return classify(ae)
		case ae.Code != "":
			return classify(ae)
		case resp.StatusCode == http.StatusServiceUnavailable:
			return fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
		case resp.StatusCode >= 500:
			return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
		}
		return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	}
	if jerr != nil {
		return fmt.Errorf("slack %s: unreadable response: %w: %w", method, jerr, effects.ErrUnknownOutcome)
	}
	if !env.OK {
		return classify(&APIError{Method: method, Status: resp.StatusCode, Code: env.Error, RetryAfter: retryAfter})
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("slack %s: unreadable response: %w: %w", method, err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// copyArgs copies the named inputs that are present into a JSON body.
func copyArgs(in map[string]any, body map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := in[k]; ok && v != nil {
			body[k] = v
		}
	}
}

func required(action string, in map[string]any, keys ...string) error {
	for _, k := range keys {
		if strings.TrimSpace(str(in, k)) == "" {
			return fmt.Errorf("slack %s: %s is required: %w", action, k, effects.ErrFatal)
		}
	}
	return nil
}

func (c *client) authTest(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		URL    string `json:"url"`
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	if err := c.call(ctx, req, "auth.test", url.Values{}, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"url": r.URL, "team": r.Team, "team_id": r.TeamID, "user": r.User, "user_id": r.UserID, "bot_id": r.BotID}}, nil
}

func hasContent(in map[string]any) bool {
	if strings.TrimSpace(str(in, "text")) != "" {
		return true
	}
	b, ok := in["blocks"].([]any)
	return ok && len(b) > 0
}

func (c *client) postMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("post_message", in, "channel"); err != nil {
		return connector.Response{}, err
	}
	if !hasContent(in) {
		return connector.Response{}, fmt.Errorf("slack post_message: text or blocks is required: %w", effects.ErrFatal)
	}
	body := map[string]any{}
	copyArgs(in, body, "channel", "text", "blocks", "thread_ts", "reply_broadcast", "unfurl_links", "unfurl_media", "mrkdwn", "metadata", "username", "icon_emoji", "icon_url")
	var r struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := c.call(ctx, req, "chat.postMessage", body, &r); err != nil {
		return connector.Response{}, err
	}
	parent := str(in, "thread_ts")
	if parent == "" {
		parent = r.TS
	}
	return connector.Response{Output: map[string]any{"channel": r.Channel, "ts": r.TS,
		"message_key": r.Channel + ":" + r.TS, "thread_key": r.Channel + ":" + parent}}, nil
}

func (c *client) updateMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("update_message", in, "channel", "ts"); err != nil {
		return connector.Response{}, err
	}
	_, hasBlocks := in["blocks"]
	if strings.TrimSpace(str(in, "text")) == "" && !hasBlocks {
		return connector.Response{}, fmt.Errorf("slack update_message: text or blocks is required: %w", effects.ErrFatal)
	}
	body := map[string]any{}
	copyArgs(in, body, "channel", "ts", "text", "blocks", "metadata")
	var r struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Text    string `json:"text"`
	}
	if err := c.call(ctx, req, "chat.update", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"channel": r.Channel, "ts": r.TS, "text": r.Text}}, nil
}

func (c *client) postEphemeral(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("post_ephemeral", in, "channel", "user"); err != nil {
		return connector.Response{}, err
	}
	if !hasContent(in) {
		return connector.Response{}, fmt.Errorf("slack post_ephemeral: text or blocks is required: %w", effects.ErrFatal)
	}
	body := map[string]any{}
	copyArgs(in, body, "channel", "user", "text", "blocks", "thread_ts")
	var r struct {
		MessageTS string `json:"message_ts"`
	}
	if err := c.call(ctx, req, "chat.postEphemeral", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_ts": r.MessageTS}}, nil
}

func (c *client) addReaction(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("add_reaction", in, "channel", "timestamp", "name"); err != nil {
		return connector.Response{}, err
	}
	name := strings.Trim(strings.TrimSpace(str(in, "name")), ":")
	err := c.call(ctx, req, "reactions.add", map[string]any{"channel": str(in, "channel"), "timestamp": str(in, "timestamp"), "name": name}, nil)
	if Code(err) == "already_reacted" {
		// Done before (perhaps by an attempt whose response was lost).
		return connector.Response{Output: map[string]any{"already_reacted": true}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"already_reacted": false}}, nil
}

func formBool(v url.Values, in map[string]any, k string) {
	if b, ok := in[k].(bool); ok {
		v.Set(k, strconv.FormatBool(b))
	}
}

func formInt(v url.Values, in map[string]any, k string) {
	switch n := in[k].(type) {
	case int:
		v.Set(k, strconv.Itoa(n))
	case int64:
		v.Set(k, strconv.FormatInt(n, 10))
	case float64:
		v.Set(k, strconv.FormatInt(int64(n), 10))
	}
}

func formStr(v url.Values, in map[string]any, keys ...string) {
	for _, k := range keys {
		if s := str(in, k); s != "" {
			v.Set(k, s)
		}
	}
}

func (c *client) listConversations(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	v := url.Values{}
	formStr(v, in, "types", "cursor", "team_id")
	formBool(v, in, "exclude_archived")
	formInt(v, in, "limit")
	var r struct {
		Channels []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			IsPrivate  bool   `json:"is_private"`
			IsArchived bool   `json:"is_archived"`
			IsMember   bool   `json:"is_member"`
			NumMembers int64  `json:"num_members"`
		} `json:"channels"`
		Meta struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := c.call(ctx, req, "conversations.list", v, &r); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(r.Channels))
	for i, ch := range r.Channels {
		out[i] = map[string]any{"id": ch.ID, "name": ch.Name, "is_private": ch.IsPrivate, "is_archived": ch.IsArchived, "is_member": ch.IsMember, "num_members": ch.NumMembers}
	}
	return connector.Response{Output: map[string]any{"channels": out, "next_cursor": r.Meta.NextCursor}}, nil
}

func (c *client) getConversation(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("get_conversation", in, "channel"); err != nil {
		return connector.Response{}, err
	}
	v := url.Values{}
	formStr(v, in, "channel")
	formBool(v, in, "include_num_members")
	var r struct {
		Channel map[string]any `json:"channel"`
	}
	if err := c.call(ctx, req, "conversations.info", v, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"channel": r.Channel}}, nil
}

func (c *client) lookupUserByEmail(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("lookup_user_by_email", in, "email"); err != nil {
		return connector.Response{}, err
	}
	var r struct {
		User struct {
			ID       string `json:"id"`
			TeamID   string `json:"team_id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			TZ       string `json:"tz"`
			IsBot    bool   `json:"is_bot"`
			Profile  struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	err := c.call(ctx, req, "users.lookupByEmail", url.Values{"email": {strings.TrimSpace(str(in, "email"))}}, &r)
	if Code(err) == "users_not_found" {
		return connector.Response{Output: map[string]any{"found": false}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	u := r.User
	realName := u.RealName
	if realName == "" {
		realName = u.Profile.RealName
	}
	return connector.Response{Output: map[string]any{"found": true, "id": u.ID, "team_id": u.TeamID, "name": u.Name,
		"real_name": realName, "display_name": u.Profile.DisplayName, "tz": u.TZ, "is_bot": u.IsBot}}, nil
}

// notSent marks a failure in a step that shares nothing (upload_file before
// files.completeUploadExternal): it may be repeated even for an unsafe
// write. Fatal errors stay fatal.
func notSent(err error) error {
	if err == nil || errors.Is(err, effects.ErrFatal) {
		return err
	}
	return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
}

func (c *client) uploadFile(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := required("upload_file", in, "filename"); err != nil {
		return connector.Response{}, err
	}
	var data []byte
	switch {
	case str(in, "content_base64") != "":
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(str(in, "content_base64")))
		if err != nil {
			return connector.Response{}, fmt.Errorf("slack upload_file: content_base64 is not base64: %w", effects.ErrFatal)
		}
		data = b
	case str(in, "content") != "":
		data = []byte(str(in, "content"))
	default:
		return connector.Response{}, fmt.Errorf("slack upload_file: content or content_base64 is required: %w", effects.ErrFatal)
	}
	if len(data) > maxUpload {
		return connector.Response{}, fmt.Errorf("slack upload_file: %d bytes is over the connector's %d-byte limit: %w", len(data), maxUpload, effects.ErrFatal)
	}

	// 1. Ask for an upload URL.
	v := url.Values{"filename": {str(in, "filename")}, "length": {strconv.Itoa(len(data))}}
	formStr(v, in, "snippet_type")
	if a := str(in, "alt_text"); a != "" {
		v.Set("alt_txt", a)
	}
	var u struct {
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	if err := c.call(ctx, req, "files.getUploadURLExternal", v, &u); err != nil {
		return connector.Response{}, notSent(err)
	}
	if u.UploadURL == "" || u.FileID == "" {
		return connector.Response{}, fmt.Errorf("slack files.getUploadURLExternal: no upload_url or file_id: %w: %w", effects.ErrRetryable, effects.ErrNotSent)
	}
	pu, err := url.Parse(u.UploadURL)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") {
		return connector.Response{}, fmt.Errorf("slack files.getUploadURLExternal: unusable upload_url: %w", effects.ErrFatal)
	}

	// 2. Send the bytes. Nothing is shared until step 3.
	if err := c.sendBytes(ctx, req, u.UploadURL, data); err != nil {
		return connector.Response{}, notSent(err)
	}

	// 3. Complete, which shares the file.
	title := str(in, "title")
	if title == "" {
		title = str(in, "filename")
	}
	files, _ := json.Marshal([]map[string]string{{"id": u.FileID, "title": title}})
	cv := url.Values{"files": {string(files)}}
	formStr(cv, in, "channel_id", "thread_ts", "initial_comment")
	var done struct {
		Files []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"files"`
	}
	if err := c.call(ctx, req, "files.completeUploadExternal", cv, &done); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"file_id": u.FileID, "title": title}
	if len(done.Files) > 0 {
		out["file_id"], out["title"] = done.Files[0].ID, done.Files[0].Title
	}
	return connector.Response{Output: out}, nil
}

// sendBytes posts the raw file to Slack's upload URL; Slack answers 200
// when it has the file.
func (c *client) sendBytes(ctx context.Context, req connector.Request, uploadURL string, data []byte) error {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("slack upload: %w: %w", err, effects.ErrFatal)
	}
	hr.Header.Set("Content-Type", "application/octet-stream")
	resp, err := req.HTTP.Do(hr)
	if err != nil {
		return connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		kind := effects.ErrRetryable
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			kind = effects.ErrFatal
		}
		return fmt.Errorf("slack upload: http %d: %w", resp.StatusCode, kind)
	}
	return nil
}
