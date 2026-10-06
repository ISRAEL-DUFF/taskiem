// Package gmail is the Gmail connector (Gmail API v1): send and draft
// email built safely as RFC 5322, search and read messages, and change
// their labels. Connections are a service account with domain-wide
// delegation acting as a subject, or an OAuth client with a refresh token
// (connectors/internal/google). Built from Google's public documentation
// (docs/integrations/gmail.md).
package gmail

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/google"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// DefaultScopes are requested for service accounts unless the connection
// names others: gmail.modify covers every action here.
var DefaultScopes = []string{"https://www.googleapis.com/auth/gmail.modify"}

// maxRaw bounds a built message. Google documents 35 MB for media uploads;
// the connector sends the message inside a JSON request, whose limit is not
// documented, so it stays well below.
const maxRaw = 25 << 20

// Options configure the connector: BaseURL replaces
// https://gmail.googleapis.com/gmail/v1 and TokenURL Google's token
// endpoint (tests).
type Options struct {
	BaseURL  string
	TokenURL string
}

// New returns the Gmail connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{base: strings.TrimRight(m.BaseURL, "/"), tokens: google.NewTokens(o.TokenURL)}
	if o.BaseURL != "" {
		c.base = strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
		// The override narrows egress to its host; tokens still come from
		// the token endpoint.
		if h := hostOf(c.tokens.TokenURL); h != "" && !contains(m.Egress, h) {
			m.Egress = append(m.Egress, h)
		}
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_profile":   connector.ActionFunc(c.getProfile),
		"send_message":  connector.ActionFunc(c.sendMessage),
		"create_draft":  connector.ActionFunc(c.createDraft),
		"list_messages": connector.ActionFunc(c.listMessages),
		"get_message":   connector.ActionFunc(c.getMessage),
		"modify_labels": connector.ActionFunc(c.modifyLabels),
		"list_labels":   connector.ActionFunc(c.listLabels),
	}}
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Hostname()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type client struct {
	base   string
	tokens *google.Tokens
}

// do calls the API for the connection's mailbox; path starts after
// /users/{userId}.
func (c *client) do(ctx context.Context, req connector.Request, method, path string, q url.Values, body, out any) error {
	creds, err := google.FromConnection(req.Credentials, DefaultScopes)
	if err != nil {
		return err
	}
	user := strings.TrimSpace(req.Credentials["user_id"])
	if user == "" {
		user = "me"
	}
	u := c.base + "/users/" + url.PathEscape(user) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.tokens.Do(ctx, req.HTTP, creds, method, u, body, out)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func strs(m map[string]any, k string) []string {
	switch v := m[k].(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func fatal(action string, err error) error {
	return fmt.Errorf("gmail %s: %w: %w", action, err, effects.ErrFatal)
}

func (c *client) getProfile(ctx context.Context, req connector.Request) (connector.Response, error) {
	var p struct {
		EmailAddress  string `json:"emailAddress"`
		MessagesTotal int64  `json:"messagesTotal"`
		ThreadsTotal  int64  `json:"threadsTotal"`
		HistoryID     string `json:"historyId"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/profile", nil, nil, &p); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"email_address": p.EmailAddress, "messages_total": p.MessagesTotal,
		"threads_total": p.ThreadsTotal, "history_id": p.HistoryID}}, nil
}

// decodeBase64 accepts standard and URL-safe base64, padded or not.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.Join(strings.Fields(s), ""), "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// raw builds the message input into Gmail's base64url raw field.
func raw(action string, in map[string]any) (string, error) {
	m := message{To: strs(in, "to"), Cc: strs(in, "cc"), Bcc: strs(in, "bcc"), From: str(in, "from"), ReplyTo: str(in, "reply_to"),
		Subject: str(in, "subject"), Text: str(in, "text"), HTML: str(in, "html"), InReplyTo: str(in, "in_reply_to"), References: str(in, "references")}
	if list, ok := in["attachments"].([]any); ok {
		for i, x := range list {
			a, _ := x.(map[string]any)
			at := attachment{Filename: str(a, "filename"), ContentType: str(a, "content_type")}
			if at.Filename == "" {
				return "", fatal(action, fmt.Errorf("attachments[%d]: filename is required", i))
			}
			switch {
			case str(a, "content_base64") != "":
				b, err := decodeBase64(str(a, "content_base64"))
				if err != nil {
					return "", fatal(action, fmt.Errorf("attachments[%d]: content_base64 is not base64", i))
				}
				at.Data = b
			default:
				at.Data = []byte(str(a, "content"))
			}
			m.Attachments = append(m.Attachments, at)
		}
	}
	b, err := build(m)
	if err != nil {
		return "", fatal(action, err)
	}
	if len(b) > maxRaw {
		return "", fatal(action, fmt.Errorf("the message is %d bytes; the connector sends at most %d", len(b), maxRaw))
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

type msgRef struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	LabelIDs []string `json:"labelIds"`
}

func labels(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

func (c *client) sendMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	r, err := raw("send_message", req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"raw": r}
	if t := str(req.Input, "thread_id"); t != "" {
		body["threadId"] = t
	}
	var m msgRef
	if err := c.do(ctx, req, http.MethodPost, "/messages/send", nil, body, &m); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"id": m.ID, "thread_id": m.ThreadID, "label_ids": labels(m.LabelIDs)}}, nil
}

func (c *client) createDraft(ctx context.Context, req connector.Request) (connector.Response, error) {
	r, err := raw("create_draft", req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	msg := map[string]any{"raw": r}
	if t := str(req.Input, "thread_id"); t != "" {
		msg["threadId"] = t
	}
	var d struct {
		ID      string `json:"id"`
		Message msgRef `json:"message"`
	}
	if err := c.do(ctx, req, http.MethodPost, "/drafts", nil, map[string]any{"message": msg}, &d); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"draft_id": d.ID, "message_id": d.Message.ID, "thread_id": d.Message.ThreadID}}, nil
}

func intInput(in map[string]any, k string) (int64, bool) {
	switch n := in[k].(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func (c *client) listMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	q := url.Values{}
	if s := str(in, "q"); s != "" {
		q.Set("q", s)
	}
	for _, l := range strs(in, "label_ids") {
		q.Add("labelIds", l)
	}
	if n, ok := intInput(in, "max_results"); ok {
		q.Set("maxResults", strconv.FormatInt(n, 10))
	}
	if s := str(in, "page_token"); s != "" {
		q.Set("pageToken", s)
	}
	if b, ok := in["include_spam_trash"].(bool); ok {
		q.Set("includeSpamTrash", strconv.FormatBool(b))
	}
	var r struct {
		Messages           []msgRef `json:"messages"`
		NextPageToken      string   `json:"nextPageToken"`
		ResultSizeEstimate int64    `json:"resultSizeEstimate"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/messages", q, nil, &r); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(r.Messages))
	for i, m := range r.Messages {
		out[i] = map[string]any{"id": m.ID, "thread_id": m.ThreadID}
	}
	return connector.Response{Output: map[string]any{"messages": out, "next_page_token": r.NextPageToken, "result_size_estimate": r.ResultSizeEstimate}}, nil
}

// part is Gmail's MessagePart.
type part struct {
	PartID   string `json:"partId"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		AttachmentID string `json:"attachmentId"`
		Size         int64  `json:"size"`
		Data         string `json:"data"`
	} `json:"body"`
	Parts []part `json:"parts"`
}

func (p part) disposition() string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, "Content-Disposition") {
			return strings.ToLower(strings.TrimSpace(strings.SplitN(h.Value, ";", 2)[0]))
		}
	}
	return ""
}

// walk collects the first text/plain and text/html bodies that are not
// attachments, and every attachment.
func walk(p part, text, html *string, atts *[]any) {
	isAttachment := p.Filename != "" || p.Body.AttachmentID != "" || p.disposition() == "attachment"
	mt := strings.ToLower(p.MimeType)
	switch {
	case strings.HasPrefix(mt, "multipart/"):
		for _, c := range p.Parts {
			walk(c, text, html, atts)
		}
	case isAttachment:
		*atts = append(*atts, map[string]any{"filename": p.Filename, "mime_type": p.MimeType, "size": p.Body.Size, "attachment_id": p.Body.AttachmentID})
	case mt == "text/plain" && *text == "":
		if b, err := decodeBase64(p.Body.Data); err == nil {
			*text = string(b)
		}
	case mt == "text/html" && *html == "":
		if b, err := decodeBase64(p.Body.Data); err == nil {
			*html = string(b)
		}
	}
}

func (c *client) getMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id := strings.TrimSpace(str(in, "id"))
	if id == "" {
		return connector.Response{}, fatal("get_message", fmt.Errorf("id is required"))
	}
	format := str(in, "format")
	if format == "" {
		format = "full"
	}
	if format != "full" && format != "metadata" && format != "minimal" {
		return connector.Response{}, fatal("get_message", fmt.Errorf("format must be full, metadata or minimal"))
	}
	q := url.Values{"format": {format}}
	if format == "metadata" {
		for _, h := range strs(in, "metadata_headers") {
			q.Add("metadataHeaders", h)
		}
	}
	var m struct {
		msgRef
		Snippet      string `json:"snippet"`
		InternalDate string `json:"internalDate"`
		Payload      part   `json:"payload"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/messages/"+url.PathEscape(id), q, nil, &m); err != nil {
		return connector.Response{}, err
	}
	headers := map[string]any{}
	for _, h := range m.Payload.Headers {
		k := strings.ToLower(h.Name)
		if _, ok := headers[k]; !ok {
			headers[k] = h.Value
		}
	}
	hv := func(k string) string { s, _ := headers[k].(string); return s }
	out := map[string]any{"id": m.ID, "thread_id": m.ThreadID, "label_ids": labels(m.LabelIDs), "snippet": m.Snippet,
		"internal_date": m.InternalDate, "headers": headers, "from": hv("from"), "to": hv("to"), "subject": hv("subject"),
		"date": hv("date"), "message_id": hv("message-id"), "text": "", "html": "", "attachments": []any{}}
	if format == "full" {
		var text, html string
		atts := []any{}
		walk(m.Payload, &text, &html, &atts)
		out["text"], out["html"], out["attachments"] = text, html, atts
	}
	return connector.Response{Output: out}, nil
}

func (c *client) modifyLabels(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id := strings.TrimSpace(str(in, "id"))
	add, remove := strs(in, "add_label_ids"), strs(in, "remove_label_ids")
	if id == "" || len(add)+len(remove) == 0 {
		return connector.Response{}, fatal("modify_labels", fmt.Errorf("id and at least one label to add or remove are required"))
	}
	body := map[string]any{}
	if len(add) > 0 {
		body["addLabelIds"] = add
	}
	if len(remove) > 0 {
		body["removeLabelIds"] = remove
	}
	var m msgRef
	if err := c.do(ctx, req, http.MethodPost, "/messages/"+url.PathEscape(id)+"/modify", nil, body, &m); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"id": m.ID, "thread_id": m.ThreadID, "label_ids": labels(m.LabelIDs)}}, nil
}

func (c *client) listLabels(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"labels"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/labels", nil, nil, &r); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(r.Labels))
	for i, l := range r.Labels {
		out[i] = map[string]any{"id": l.ID, "name": l.Name, "type": l.Type}
	}
	return connector.Response{Output: map[string]any{"labels": out}}, nil
}
