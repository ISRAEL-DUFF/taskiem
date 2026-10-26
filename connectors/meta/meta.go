// Package meta is the Messenger and Instagram connectors: a Facebook
// Page's messages, and an Instagram professional account's direct
// messages through the Page it is linked to, on Taskiem's Meta app
// (Facebook Login; decision 0051) and the Graph API. Built from Meta's
// public developer documentation (docs/integrations/messenger-and-instagram.md).
//
// Connecting: the login grants a short-lived user token; Link (rule 21)
// trades it for a long-lived one, takes the one Page's own token (which
// does not expire) and subscribes the Page to the app. The user token is
// not kept.
//
// Sending: Meta allows a reply within 24 hours of the person's last
// message, and after that only a person replying by hand within 7 days
// (HUMAN_AGENT). The window is read from the conversation before sending;
// outside it nothing is sent. The Send API takes no idempotency key, so a
// message is an unsafe write.
//
// Errors: Graph answers {"error": {"message", "type", "code",
// "error_subcode"}}. Code 190 is a token Meta no longer accepts (the
// connection is expired); 4, 17, 32 and 613 are rate limits (retried); 10,
// 200 and 100 are refusals (fatal); 551 is a person who cannot be reached;
// 5xx is an unknown outcome.
package meta

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

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed messenger.yaml
var messengerManifest []byte

//go:embed instagram.yaml
var instagramManifest []byte

// Options configure the connectors: BaseURL replaces
// https://graph.facebook.com/v26.0 (tests, another Graph version).
type Options struct {
	BaseURL string
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// platform is Messenger's or Instagram's side of the same API.
type platform struct {
	name   string // the conversations API's platform: messenger, instagram
	fields string // the subscribed_apps fields
}

var (
	messenger = platform{name: "messenger", fields: "messages,messaging_postbacks,messaging_referrals,message_reads,message_deliveries,message_echoes,message_reactions"}
	instagram = platform{name: "instagram", fields: "messages,messaging_postbacks,messaging_referrals,message_reactions"}
)

// Messenger returns the Messenger connector.
func Messenger(o Options) *connector.Connector { return build(messengerManifest, messenger, o) }

// Instagram returns the Instagram connector.
func Instagram(o Options) *connector.Connector { return build(instagramManifest, instagram, o) }

func build(src []byte, p platform, o Options) *connector.Connector {
	m := connector.MustParse(src)
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
	}
	c := &client{m: m, p: p, now: time.Now}
	if o.Now != nil {
		c.now = o.Now
	}
	actions := map[string]connector.Action{
		"send_message":     connector.ActionFunc(c.sendMessage),
		"send_typing":      connector.ActionFunc(c.sendTyping),
		"mark_seen":        connector.ActionFunc(c.markSeen),
		"get_profile":      connector.ActionFunc(c.getProfile),
		"get_conversation": connector.ActionFunc(c.getConversation),
	}
	if p == instagram {
		actions["get_account"] = connector.ActionFunc(c.getAccount)
	} else {
		actions["get_page"] = connector.ActionFunc(c.getPage)
	}
	return &connector.Connector{Manifest: m, Actions: actions, Link: c.link}
}

type client struct {
	m   *connector.Manifest
	p   platform
	now func() time.Time
}

func (c *client) base() string { return strings.TrimRight(c.m.BaseURL, "/") }

// --- errors ---

// Error is a Graph API refusal.
type Error struct {
	Status  int
	Code    int
	Subcode int
	Type    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("meta: http %d: %s (code %d)", e.Status, e.Message, e.Code)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("meta %s: %w", op, err)
	}
	var b struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    int    `json:"code"`
			Subcode int    `json:"error_subcode"`
		} `json:"error"`
	}
	_ = json.Unmarshal(he.Body, &b)
	ge := &Error{Status: he.Status, Code: b.Error.Code, Subcode: b.Error.Subcode, Type: b.Error.Type, Message: b.Error.Message}
	if ge.Message == "" {
		ge.Message = http.StatusText(he.Status)
	}
	switch {
	case ge.Code == 190:
		return fmt.Errorf("meta %s: %w: connect again: %w: %w", op, ge, connector.ErrCredentialsRejected, effects.ErrFatal)
	case ge.Code == 4 || ge.Code == 17 || ge.Code == 32 || ge.Code == 613 || he.Status == http.StatusTooManyRequests:
		return fmt.Errorf("meta %s: %w: %w", op, ge, effects.ErrRetryable)
	case he.Status >= 500:
		return fmt.Errorf("meta %s: %w: %w", op, ge, he) // he carries the engine's kind
	case he.Status == http.StatusNotFound:
		return fmt.Errorf("meta %s: %w: %w", op, ge, connector.ErrNotFound)
	}
	return fmt.Errorf("meta %s: %w: %w", op, ge, effects.ErrFatal)
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("meta: "+format+": %w", append(a, effects.ErrFatal)...)
}

// --- calls ---

// call sends a Graph request with the Page token.
func (c *client) call(ctx context.Context, req connector.Request, op, method, path string, q url.Values, body, out any) error {
	tok := req.Credentials["page_token"]
	if tok == "" {
		return fatalf("the connection has no Page token: connect again")
	}
	if q == nil {
		q = url.Values{}
	}
	u := c.base() + path + "?" + q.Encode()
	if err := connector.DoJSON(ctx, req.HTTP, method, u, map[string]string{"Authorization": "Bearer " + tok}, body, out); err != nil {
		return classify(op, err)
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func (c *client) page(req connector.Request) (string, error) {
	id := req.Credentials["page_id"]
	if id == "" {
		return "", fatalf("the connection has no Page: connect again")
	}
	return id, nil
}

// --- connecting ---

type grantedPage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token"`
	Instagram   *struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"instagram_business_account"`
}

// link turns the login's user token into the connection: the one Page
// granted (for Instagram, the one with an Instagram account), its own
// token, subscribed to Taskiem's app.
func (c *client) link(ctx context.Context, lc connector.LinkCall) (map[string]string, error) {
	// A long-lived user token, so the Page token from it does not expire.
	var long struct {
		AccessToken string `json:"access_token"`
	}
	q := url.Values{"grant_type": {"fb_exchange_token"}, "client_id": {lc.ClientID}, "client_secret": {lc.ClientSecret}, "fb_exchange_token": {lc.Token}}
	if err := connector.DoJSON(ctx, lc.HTTP, http.MethodGet, c.base()+"/oauth/access_token?"+q.Encode(), nil, nil, &long); err != nil {
		return nil, classify("connecting", err)
	}
	var pages struct {
		Data []grantedPage `json:"data"`
	}
	q = url.Values{"fields": {"id,name,access_token,instagram_business_account{id,username}"}}
	if err := connector.DoJSON(ctx, lc.HTTP, http.MethodGet, c.base()+"/me/accounts?"+q.Encode(), map[string]string{"Authorization": "Bearer " + long.AccessToken}, nil, &pages); err != nil {
		return nil, classify("connecting", err)
	}
	var usable []grantedPage
	for _, p := range pages.Data {
		if p.AccessToken != "" && (c.p != instagram || p.Instagram != nil && p.Instagram.ID != "") {
			usable = append(usable, p)
		}
	}
	what := "Facebook Page"
	if c.p == instagram {
		what = "Instagram account linked to a Facebook Page"
	}
	switch {
	case len(usable) == 0:
		return nil, fmt.Errorf("the login granted no %s you manage; connect again and choose one: %w", what, connector.ErrLinkRefused)
	case len(usable) > 1:
		return nil, fmt.Errorf("the login granted %d; a connection is one %s: connect again and choose exactly one: %w", len(usable), what, connector.ErrLinkRefused)
	}
	p := usable[0]
	var sub struct {
		Success bool `json:"success"`
	}
	q = url.Values{"subscribed_fields": {c.p.fields}}
	if err := connector.DoJSON(ctx, lc.HTTP, http.MethodPost, c.base()+"/"+url.PathEscape(p.ID)+"/subscribed_apps?"+q.Encode(), map[string]string{"Authorization": "Bearer " + p.AccessToken}, nil, &sub); err != nil {
		return nil, classify("subscribing the Page", err)
	}
	if !sub.Success {
		return nil, fatalf("Meta did not subscribe the Page %s to Taskiem's app", p.Name)
	}
	out := map[string]string{}
	for k, v := range lc.Credentials {
		out[k] = v
	}
	out["page_id"], out["page_name"], out["page_token"] = p.ID, p.Name, p.AccessToken
	if p.Instagram != nil {
		out["instagram_id"], out["instagram_username"] = p.Instagram.ID, p.Instagram.Username
	}
	return out, nil
}

// --- the account ---

func (c *client) getPage(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := c.page(req)
	if err != nil {
		return connector.Response{}, err
	}
	var p struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, req, "get_page", http.MethodGet, "/"+url.PathEscape(id), url.Values{"fields": {"id,name"}}, nil, &p); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"page_id": p.ID, "name": p.Name}}, nil
}

func (c *client) getAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := c.page(req)
	if err != nil {
		return connector.Response{}, err
	}
	var p grantedPage
	if err := c.call(ctx, req, "get_account", http.MethodGet, "/"+url.PathEscape(id), url.Values{"fields": {"id,instagram_business_account{id,username}"}}, nil, &p); err != nil {
		return connector.Response{}, err
	}
	if p.Instagram == nil {
		return connector.Response{}, fatalf("the Page is no longer linked to an Instagram account: link it again in Meta Business Suite, then connect again")
	}
	return connector.Response{Output: map[string]any{"instagram_id": p.Instagram.ID, "username": p.Instagram.Username, "page_id": p.ID}}, nil
}

// --- the window ---

// window is the conversation with a person: its id, and when they last
// wrote (zero when they never have, as far as the last 20 messages go).
type window struct {
	conversation string
	lastFrom     time.Time
	messages     []map[string]any
}

func (c *client) conversation(ctx context.Context, req connector.Request, op, person string, limit int) (window, error) {
	page, err := c.page(req)
	if err != nil {
		return window{}, err
	}
	var resp struct {
		Data []struct {
			ID       string `json:"id"`
			Messages struct {
				Data []struct {
					ID      string `json:"id"`
					Created string `json:"created_time"`
					Message string `json:"message"`
					From    struct {
						ID string `json:"id"`
					} `json:"from"`
				} `json:"data"`
			} `json:"messages"`
		} `json:"data"`
	}
	q := url.Values{"platform": {c.p.name}, "user_id": {person}, "fields": {fmt.Sprintf("messages.limit(%d){id,created_time,from,message}", limit)}}
	if err := c.call(ctx, req, op, http.MethodGet, "/"+url.PathEscape(page)+"/conversations", q, nil, &resp); err != nil {
		return window{}, err
	}
	var w window
	if len(resp.Data) == 0 {
		return w, nil
	}
	conv := resp.Data[0]
	w.conversation = conv.ID
	for _, m := range conv.Messages.Data {
		at, ok := parseTime(m.Created)
		mine := m.From.ID != person
		if !mine && ok && at.After(w.lastFrom) {
			w.lastFrom = at
		}
		row := map[string]any{"message_id": m.ID, "from_person": !mine}
		if ok {
			row["created_at"] = at.UTC().Format(time.RFC3339)
		}
		if m.Message != "" {
			row["text"] = m.Message
		}
		w.messages = append(w.messages, row)
	}
	return w, nil
}

// parseTime reads Graph's times (2026-10-26T10:00:00+0000).
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

const (
	standardWindow = 24 * time.Hour
	humanAgent     = 7 * 24 * time.Hour
)

// --- sending ---

func (c *client) sendMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	to, text, link := str(in, "recipient_id"), str(in, "text"), str(in, "attachment_url")
	if to == "" || (text == "" && link == "") {
		return connector.Response{}, fatalf("send_message: give recipient_id, and text or attachment_url")
	}
	message := map[string]any{}
	if link != "" {
		u, err := url.Parse(link)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return connector.Response{}, fatalf("send_message: attachment_url must be an https link")
		}
		kind := str(in, "attachment_type")
		if kind == "" {
			kind = "image"
		}
		message["attachment"] = map[string]any{"type": kind, "payload": map[string]any{"url": link, "is_reusable": false}}
		if text != "" {
			return connector.Response{}, fatalf("send_message: Meta sends text and an attachment as two messages: send them in two steps")
		}
	} else {
		message["text"] = text
	}
	if qrs, _ := in["quick_replies"].([]any); len(qrs) > 0 {
		var out []map[string]any
		for _, v := range qrs {
			q, _ := v.(map[string]any)
			title := str(q, "title")
			if title == "" {
				continue
			}
			payload := str(q, "payload")
			if payload == "" {
				payload = title
			}
			out = append(out, map[string]any{"content_type": "text", "title": title, "payload": payload})
		}
		message["quick_replies"] = out
	}
	// The window, before anything is sent.
	w, err := c.conversation(ctx, req, "send_message", to, 20)
	if err != nil {
		return connector.Response{}, err
	}
	if w.lastFrom.IsZero() {
		return connector.Response{}, fatalf("send_message: this person has not written to the account (Meta allows messages only to people who wrote first)")
	}
	now := c.now()
	body := map[string]any{"recipient": map[string]any{"id": to}, "message": message}
	ends := w.lastFrom.Add(standardWindow)
	switch tag := str(in, "tag"); {
	case now.Before(ends):
		body["messaging_type"] = "RESPONSE"
	case tag == "HUMAN_AGENT" && now.Before(w.lastFrom.Add(humanAgent)):
		body["messaging_type"], body["tag"] = "MESSAGE_TAG", tag
		ends = w.lastFrom.Add(humanAgent)
	case tag == "HUMAN_AGENT":
		return connector.Response{}, fatalf("send_message: the person last wrote %s; a person may reply by hand within 7 days, which ended %s", w.lastFrom.UTC().Format(time.RFC3339), w.lastFrom.Add(humanAgent).UTC().Format(time.RFC3339))
	default:
		return connector.Response{}, fatalf("send_message: the person last wrote %s; Meta allows replies within 24 hours, which ended %s (a person replying by hand may use the HUMAN_AGENT tag within 7 days)", w.lastFrom.UTC().Format(time.RFC3339), ends.UTC().Format(time.RFC3339))
	}
	if r := str(in, "reply_to"); r != "" {
		body["reply_to"] = map[string]any{"mid": r}
	}
	page, _ := c.page(req)
	var resp struct {
		RecipientID string `json:"recipient_id"`
		MessageID   string `json:"message_id"`
	}
	if err := c.call(ctx, req, "send_message", http.MethodPost, "/"+url.PathEscape(page)+"/messages", nil, body, &resp); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": resp.MessageID, "recipient_id": resp.RecipientID, "window_ends_at": ends.UTC().Format(time.RFC3339)}}, nil
}

func (c *client) senderAction(ctx context.Context, req connector.Request, op, action string) (connector.Response, error) {
	to := str(req.Input, "recipient_id")
	if to == "" {
		return connector.Response{}, fatalf("%s: recipient_id is required", op)
	}
	page, err := c.page(req)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"recipient": map[string]any{"id": to}, "sender_action": action}
	if err := c.call(ctx, req, op, http.MethodPost, "/"+url.PathEscape(page)+"/messages", nil, body, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"recipient_id": to}}, nil
}

func (c *client) sendTyping(ctx context.Context, req connector.Request) (connector.Response, error) {
	action := "typing_on"
	if on, ok := req.Input["on"].(bool); ok && !on {
		action = "typing_off"
	}
	return c.senderAction(ctx, req, "send_typing", action)
}

func (c *client) markSeen(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.senderAction(ctx, req, "mark_seen", "mark_seen")
}

// --- people and conversations ---

func (c *client) getProfile(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "person_id")
	if id == "" {
		return connector.Response{}, fatalf("get_profile: person_id is required")
	}
	fields := "first_name,last_name,profile_pic"
	if c.p == instagram {
		fields = "name,username,profile_pic"
	}
	var p map[string]any
	if err := c.call(ctx, req, "get_profile", http.MethodGet, "/"+url.PathEscape(id), url.Values{"fields": {fields}}, nil, &p); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"person_id": id}
	for _, k := range []string{"first_name", "last_name", "name", "username", "profile_pic"} {
		if v, ok := p[k].(string); ok && v != "" {
			out[k] = v
		}
	}
	if _, ok := out["name"]; !ok {
		out["name"] = strings.TrimSpace(str(p, "first_name") + " " + str(p, "last_name"))
	}
	return connector.Response{Output: out}, nil
}

func (c *client) getConversation(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "person_id")
	if id == "" {
		return connector.Response{}, fatalf("get_conversation: person_id is required")
	}
	limit := 10
	if n, ok := req.Input["limit"].(float64); ok && n >= 1 && n <= 20 {
		limit = int(n)
	}
	w, err := c.conversation(ctx, req, "get_conversation", id, limit)
	if err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"conversation_id": w.conversation, "messages": w.messages}
	if out["messages"] == nil {
		out["messages"] = []map[string]any{}
	}
	if !w.lastFrom.IsZero() {
		out["last_from_person_at"] = w.lastFrom.UTC().Format(time.RFC3339)
		out["window_ends_at"] = w.lastFrom.Add(standardWindow).UTC().Format(time.RFC3339)
	}
	return connector.Response{Output: out}, nil
}
