// Package zohomail is the Zoho Mail connector: one mailbox, in the
// account's own data centre, through Taskiem's Zoho client (decision
// 0051), with new mail polled. Built from Zoho's public developer
// documentation (docs/integrations/zoho-mail.md).
//
// Connecting: Link (connector/v1 rule 21) finds the mailbox before the
// connection is saved: the user's own Zoho account, at the Mail server of
// their data centre (mail.zoho.eu for accounts.zoho.eu).
//
// Errors: Zoho Mail answers {"status": {"code", "description"}, "data":
// {"errorCode", "moreInfo"}}. 401 is a refused token (refreshed once, then
// the connection is expired); 429 is a rate limit (retried); 404 is a
// message or folder that is not there; other refusals are fatal; 5xx is an
// unknown outcome. Sending has no idempotency key: an unknown outcome is
// parked for a person.
package zohomail

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/zoho"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector: BaseURL replaces every connection's
// Mail server and AccountsServer its accounts server (tests, a proxy).
type Options struct {
	BaseURL, AccountsServer string
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// New returns the Zoho Mail connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{m: m, now: time.Now}
	if o.Now != nil {
		c.now = o.Now
	}
	c.zoho = &zoho.Client{Manifest: m, Name: "Zoho Mail", AccountsServer: o.AccountsServer, Now: c.now}
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
		c.fixed = true
	}
	if p, err := url.Parse(o.AccountsServer); err == nil && p.Hostname() != "" {
		m.Egress = append(m.Egress, p.Hostname())
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_account":     connector.ActionFunc(c.getAccount),
		"send_message":    connector.ActionFunc(c.sendMessage),
		"reply":           connector.ActionFunc(c.reply),
		"list_messages":   connector.ActionFunc(c.listMessages),
		"search_messages": connector.ActionFunc(c.searchMessages),
		"get_message":     connector.ActionFunc(c.getMessage),
		"update_message":  connector.ActionFunc(c.updateMessage),
		"list_folders":    connector.ActionFunc(c.listFolders),
		"list_labels":     connector.ActionFunc(c.listLabels),
		"new_messages":    connector.ActionFunc(c.newMessages),
	}, Link: c.link}
}

type client struct {
	m     *connector.Manifest
	zoho  *zoho.Client
	fixed bool
	now   func() time.Time
}

// --- errors ---

// Error is a Zoho Mail refusal.
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	msg := e.Description
	if e.Code != "" {
		msg += " (" + e.Code + ")"
	}
	return fmt.Sprintf("zoho mail: http %d: %s", e.Status, msg)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("zoho mail %s: %w", op, err)
	}
	var b struct {
		Status struct {
			Description string `json:"description"`
		} `json:"status"`
		Data struct {
			ErrorCode string `json:"errorCode"`
			MoreInfo  string `json:"moreInfo"`
		} `json:"data"`
	}
	_ = json.Unmarshal(he.Body, &b)
	ze := &Error{Status: he.Status, Code: b.Data.ErrorCode, Description: strings.TrimSpace(b.Status.Description + " " + b.Data.MoreInfo)}
	if ze.Description == "" {
		ze.Description = http.StatusText(he.Status)
	}
	switch {
	case he.Status == http.StatusUnauthorized:
		return fmt.Errorf("zoho mail %s: %w: %w: %w", op, ze, connector.ErrCredentialsRejected, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("zoho mail %s: %w: %w", op, ze, he)
	case he.Status == http.StatusNotFound:
		return fmt.Errorf("zoho mail %s: %w: %w", op, ze, connector.ErrNotFound)
	}
	return fmt.Errorf("zoho mail %s: %w: %w", op, ze, effects.ErrFatal)
}

// --- calls ---

// base is where the connection's calls go: its Mail server, or the base URL
// where the deployment fixes one or the connection names none.
func (c *client) base(s *zoho.Session) (string, error) {
	if c.fixed || strings.TrimSpace(s.Creds["mail_server"]) == "" {
		return strings.TrimRight(c.m.BaseURL, "/"), nil
	}
	return s.Server("mail_server")
}

// do calls /api/accounts/{account}/path with the session's token and
// decodes the answer's data.
func (c *client) do(ctx context.Context, s *zoho.Session, op, method, path string, q url.Values, body, data any) error {
	acct := s.Creds["account_id"]
	if acct == "" {
		return c.zoho.Fatalf("the connection has no mailbox: connect Zoho Mail again")
	}
	base, err := c.base(s)
	if err != nil {
		return err
	}
	u := base + "/api/accounts/" + url.PathEscape(acct) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := s.Do(ctx, method, u, body, &resp); err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			return classify(op, err)
		}
		return err
	}
	if data != nil && len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, data); err != nil {
			return fmt.Errorf("zoho mail %s: decoding the answer: %w: %w", op, err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

// --- connecting ---

type account struct {
	ID          zoho.ID `json:"accountId"`
	Primary     string  `json:"primaryEmailAddress"`
	Mailbox     string  `json:"mailboxAddress"`
	DisplayName string  `json:"accountDisplayName"`
	Type        string  `json:"type"`
	Enabled     *bool   `json:"enabled"`
}

// mailServer is where an account's data centre serves Mail: the accounts
// server's host with accounts. as mail. (accounts.zoho.eu: mail.zoho.eu).
func mailServer(accounts string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(accounts))
	if err != nil || u.Scheme != "https" || !strings.HasPrefix(u.Hostname(), "accounts.") {
		return "", false
	}
	return "https://mail." + strings.TrimPrefix(u.Hostname(), "accounts."), true
}

// link finds the mailbox: the user's own Zoho account (not an IMAP
// account it reads), at their data centre's Mail server.
func (c *client) link(ctx context.Context, lc connector.LinkCall) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range lc.Credentials {
		out[k] = v
	}
	if !c.fixed {
		server, ok := mailServer(out["accounts_server"])
		if !ok {
			return nil, fmt.Errorf("the account is in no Zoho data centre Taskiem knows: %w", connector.ErrLinkRefused)
		}
		out["mail_server"] = server
	} else {
		out["mail_server"] = strings.TrimRight(c.m.BaseURL, "/")
	}
	if u, err := url.Parse(out["mail_server"]); err != nil || !c.fixed && !c.m.ConnectionHostAllowed(u.Hostname()) {
		return nil, fmt.Errorf("the account's Mail server is not one Taskiem uses: %w", connector.ErrLinkRefused)
	}
	// The granted access token lasts an hour: kept, so the first call does
	// not spend another.
	out["access_token"] = lc.Token
	out["access_expires_at"] = c.now().UTC().Add(55 * time.Minute).Format(time.RFC3339)
	var resp struct {
		Data []account `json:"data"`
	}
	if err := connector.DoJSON(ctx, lc.HTTP, http.MethodGet, out["mail_server"]+"/api/accounts", map[string]string{"Authorization": "Zoho-oauthtoken " + lc.Token}, nil, &resp); err != nil {
		return nil, classify("connecting", err)
	}
	for _, a := range resp.Data {
		if a.Type != "ZOHO_ACCOUNT" || a.Enabled != nil && !*a.Enabled {
			continue
		}
		email := a.Primary
		if email == "" {
			email = a.Mailbox
		}
		out["account_id"], out["email_address"] = string(a.ID), email
		return out, nil
	}
	return nil, fmt.Errorf("this Zoho user has no Zoho Mail mailbox of their own; connect as a user with one: %w", connector.ErrLinkRefused)
}

// --- the mailbox ---

func (c *client) getAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	s := c.zoho.Open(req)
	var a account
	if err := c.do(ctx, s, "get_account", http.MethodGet, "", nil, nil, &a); err != nil {
		return connector.Response{}, err
	}
	email := a.Primary
	if email == "" {
		email = a.Mailbox
	}
	return connector.Response{Output: map[string]any{"account_id": string(a.ID), "email_address": email, "display_name": a.DisplayName}}, nil
}

// --- input helpers ---

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func list(m map[string]any, k string) []string {
	var out []string
	switch v := m[k].(type) {
	case string:
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
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

func limit(m map[string]any, def int) int {
	if n, ok := m["limit"].(float64); ok && n >= 1 && n <= 200 {
		return int(n)
	}
	return def
}

// --- sending ---

// content is a message's body and its format.
func content(in map[string]any) (string, string) {
	if h := str(in, "html"); h != "" {
		return h, "html"
	}
	return str(in, "text"), "plaintext"
}

func (c *client) sendMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	to := list(in, "to")
	if len(to) == 0 || str(in, "subject") == "" {
		return connector.Response{}, c.zoho.Fatalf("send_message: to and subject are required")
	}
	s := c.zoho.Open(req)
	from := str(in, "from")
	if from == "" {
		from = s.Creds["email_address"]
	}
	body, format := content(in)
	rec := map[string]any{"fromAddress": from, "toAddress": strings.Join(to, ","), "subject": str(in, "subject"), "content": body, "mailFormat": format}
	if cc := list(in, "cc"); len(cc) > 0 {
		rec["ccAddress"] = strings.Join(cc, ",")
	}
	if bcc := list(in, "bcc"); len(bcc) > 0 {
		rec["bccAddress"] = strings.Join(bcc, ",")
	}
	var out struct {
		MessageID zoho.ID `json:"messageId"`
	}
	if err := c.do(ctx, s, "send_message", http.MethodPost, "/messages", nil, rec, &out); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": string(out.MessageID)}}, nil
}

func (c *client) reply(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id, to := str(in, "message_id"), list(in, "to")
	if id == "" || len(to) == 0 {
		return connector.Response{}, c.zoho.Fatalf("reply: message_id and to are required")
	}
	s := c.zoho.Open(req)
	body, format := content(in)
	action := "reply"
	if all, _ := in["all"].(bool); all {
		action = "replyall"
	}
	rec := map[string]any{"fromAddress": s.Creds["email_address"], "toAddress": strings.Join(to, ","), "content": body, "mailFormat": format, "action": action}
	if subj := str(in, "subject"); subj != "" {
		rec["subject"] = subj
	}
	if cc := list(in, "cc"); len(cc) > 0 {
		rec["ccAddress"] = strings.Join(cc, ",")
	}
	var out struct {
		MessageID zoho.ID `json:"messageId"`
	}
	if err := c.do(ctx, s, "reply", http.MethodPost, "/messages/"+url.PathEscape(id), nil, rec, &out); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": string(out.MessageID)}}, nil
}

// --- reading ---

type message struct {
	ID         zoho.ID `json:"messageId"`
	Folder     zoho.ID `json:"folderId"`
	Thread     zoho.ID `json:"threadId"`
	Subject    string  `json:"subject"`
	From       string  `json:"fromAddress"`
	To         string  `json:"toAddress"`
	Cc         string  `json:"ccAddress"`
	Summary    string  `json:"summary"`
	Received   zoho.ID `json:"receivedTime"`
	Status     zoho.ID `json:"status"`
	Attachment zoho.ID `json:"hasAttachment"`
}

// received is a message's received time (milliseconds since the epoch).
func (m message) received() (time.Time, bool) {
	ms, err := strconv.ParseInt(string(m.Received), 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

func (m message) out() map[string]any {
	o := map[string]any{"message_id": string(m.ID), "folder_id": string(m.Folder), "thread_id": string(m.Thread), "subject": m.Subject,
		"from": address(m.From), "to": address(m.To), "summary": m.Summary,
		"unread": string(m.Status) == "0", "has_attachment": string(m.Attachment) == "1" || string(m.Attachment) == "true"}
	if at, ok := m.received(); ok {
		o["received_at"] = at.Format(time.RFC3339)
	}
	return o
}

// address undoes the HTML escaping Zoho Mail lists addresses with
// ("Ada &lt;ada@example.com&gt;").
func address(s string) string { return html.UnescapeString(s) }

func (c *client) messages(ctx context.Context, s *zoho.Session, op, path string, q url.Values) ([]map[string]any, []message, error) {
	var rows []message
	if err := c.do(ctx, s, op, http.MethodGet, path, q, nil, &rows); err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, m.out())
	}
	return out, rows, nil
}

func (c *client) listMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	q := url.Values{"limit": {strconv.Itoa(limit(in, 50))}, "sortorder": {"false"}}
	if f := str(in, "folder_id"); f != "" {
		q.Set("folderId", f)
	}
	if st := str(in, "status"); st != "" {
		q.Set("status", st)
	}
	if l := str(in, "label_id"); l != "" {
		q.Set("labelid", l)
	}
	out, _, err := c.messages(ctx, c.zoho.Open(req), "list_messages", "/messages/view", q)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"messages": out}}, nil
}

func (c *client) searchMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	key := str(req.Input, "search_key")
	if key == "" {
		return connector.Response{}, c.zoho.Fatalf("search_messages: search_key is required")
	}
	q := url.Values{"searchKey": {key}, "limit": {strconv.Itoa(limit(req.Input, 50))}}
	out, _, err := c.messages(ctx, c.zoho.Open(req), "search_messages", "/messages/search", q)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"messages": out}}, nil
}

var tags = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)

// text is an HTML body as plain text, roughly: for a workflow to read.
func text(h string) string {
	t := tags.ReplaceAllString(strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "</div>", "\n").Replace(h), "")
	lines := strings.Split(html.UnescapeString(t), "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func (c *client) getMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	folder, id := str(req.Input, "folder_id"), str(req.Input, "message_id")
	if folder == "" || id == "" {
		return connector.Response{}, c.zoho.Fatalf("get_message: folder_id and message_id are required")
	}
	s := c.zoho.Open(req)
	path := "/folders/" + url.PathEscape(folder) + "/messages/" + url.PathEscape(id)
	var meta message
	if err := c.do(ctx, s, "get_message", http.MethodGet, path+"/details", nil, nil, &meta); err != nil {
		return connector.Response{}, err
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := c.do(ctx, s, "get_message", http.MethodGet, path+"/content", nil, nil, &body); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"message_id": id, "subject": meta.Subject, "from": address(meta.From), "to": address(meta.To), "html": body.Content, "text": text(body.Content)}
	if meta.Cc != "" {
		out["cc"] = address(meta.Cc)
	}
	if at, ok := meta.received(); ok {
		out["received_at"] = at.Format(time.RFC3339)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) newMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	since, err := time.Parse(time.RFC3339, str(req.Input, "since"))
	if err != nil {
		return connector.Response{}, c.zoho.Fatalf("new_messages: since must be a date and time (RFC 3339)")
	}
	// The inbox, newest first: those received since.
	_, rows, err := c.messages(ctx, c.zoho.Open(req), "new_messages", "/messages/view", url.Values{"limit": {"200"}, "sortorder": {"false"}})
	if err != nil {
		return connector.Response{}, err
	}
	out := []map[string]any{}
	for _, m := range rows {
		if at, ok := m.received(); ok && !at.Before(since) {
			out = append(out, m.out())
		}
	}
	return connector.Response{Output: map[string]any{"messages": out}}, nil
}

// --- changing ---

var modes = map[string]string{"mark_read": "markAsRead", "mark_unread": "markAsUnread", "move": "moveMessage", "add_label": "applyLabel", "remove_label": "removeLabel"}

func (c *client) updateMessage(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids := list(in, "message_ids")
	mode, ok := modes[str(in, "change")]
	if len(ids) == 0 || !ok {
		return connector.Response{}, c.zoho.Fatalf("update_message: message_ids and a change (mark_read, mark_unread, move, add_label, remove_label) are required")
	}
	rec := map[string]any{"mode": mode, "messageId": ids}
	switch mode {
	case "moveMessage":
		f := str(in, "folder_id")
		if f == "" {
			return connector.Response{}, c.zoho.Fatalf("update_message: move needs folder_id")
		}
		rec["destfolderId"] = f
	case "applyLabel", "removeLabel":
		l := str(in, "label_id")
		if l == "" {
			return connector.Response{}, c.zoho.Fatalf("update_message: a label change needs label_id")
		}
		rec["labelId"] = []string{l}
	}
	if err := c.do(ctx, c.zoho.Open(req), "update_message", http.MethodPut, "/updatemessage", nil, rec, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"updated": len(ids)}}, nil
}

// --- folders and labels ---

func (c *client) listFolders(ctx context.Context, req connector.Request) (connector.Response, error) {
	var rows []struct {
		ID   zoho.ID `json:"folderId"`
		Name string  `json:"folderName"`
		Path string  `json:"path"`
		Type string  `json:"folderType"`
	}
	if err := c.do(ctx, c.zoho.Open(req), "list_folders", http.MethodGet, "/folders", nil, nil, &rows); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(rows))
	for _, f := range rows {
		out = append(out, map[string]any{"folder_id": string(f.ID), "name": f.Name, "path": f.Path, "type": f.Type})
	}
	return connector.Response{Output: map[string]any{"folders": out}}, nil
}

func (c *client) listLabels(ctx context.Context, req connector.Request) (connector.Response, error) {
	var rows []struct {
		ID   zoho.ID `json:"labelId"`
		Name string  `json:"displayName"`
	}
	if err := c.do(ctx, c.zoho.Open(req), "list_labels", http.MethodGet, "/labels", nil, nil, &rows); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(rows))
	for _, l := range rows {
		out = append(out, map[string]any{"label_id": string(l.ID), "name": l.Name})
	}
	return connector.Response{Output: map[string]any{"labels": out}}, nil
}
