// Package mailchimp is the Mailchimp connector: audiences, members (only
// people who opted in), tags and campaigns on the Marketing API, connected
// through Taskiem's Mailchimp app or with an API key, and an audience's
// events through a webhook Taskiem creates (decision 0021). Built from
// Mailchimp's public developer documentation (docs/integrations/mailchimp.md).
//
// The server: every call goes to the account's server prefix
// (https://us6.api.mailchimp.com/3.0). Linked by OAuth, Mailchimp's metadata
// names it once, when the connection is made (connector/v1 rule 21); with
// an API key, it is the key's suffix (…-us6).
//
// Errors: Mailchimp answers problem details ({"type", "title", "status",
// "detail"}). 401 is a key or token Mailchimp refuses (the connection is
// expired); 429 is too many at once (retried); 404 is a member, audience or
// campaign that is not there; other refusals ("Member Exists", "Forgotten
// Email Not Subscribed", "Invalid Resource") are fatal; 5xx is an unknown
// outcome.
package mailchimp

import (
	"context"
	"crypto/md5" //nolint:gosec // Mailchimp names a member by the MD5 of its address, not for security
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector: BaseURL replaces every account's server
// and MetadataURL Mailchimp's metadata endpoint (tests, a proxy).
type Options struct{ BaseURL, MetadataURL string }

// DefaultMetadataURL is where Mailchimp names an OAuth token's account and
// server.
const DefaultMetadataURL = "https://login.mailchimp.com/oauth2/metadata"

// New returns the Mailchimp connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{m: m, metadata: DefaultMetadataURL}
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
		c.fixed = true
	}
	if o.MetadataURL != "" {
		c.metadata = o.MetadataURL
		if u, err := url.Parse(o.MetadataURL); err == nil && u.Hostname() != "" {
			m.Egress = append(m.Egress, u.Hostname())
		}
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_account":          connector.ActionFunc(c.getAccount),
		"list_audiences":       connector.ActionFunc(c.listAudiences),
		"add_or_update_member": connector.ActionFunc(c.addOrUpdateMember),
		"tag_member":           connector.ActionFunc(c.tagMember),
		"create_campaign":      connector.ActionFunc(c.createCampaign),
		"send_campaign":        connector.ActionFunc(c.sendCampaign),
	}, Registrars: map[string]connector.Registrar{"audience_events": &webhooks{c: c}}, Link: c.link}
}

type client struct {
	m        *connector.Manifest
	metadata string
	fixed    bool
}

// Error is a Mailchimp refusal.
type Error struct {
	Status int
	Title  string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mailchimp: http %d: %s: %s", e.Status, e.Title, e.Detail)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("mailchimp %s: %w", op, err)
	}
	var b struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(he.Body, &b)
	me := &Error{Status: he.Status, Title: b.Title, Detail: b.Detail}
	if me.Title == "" {
		me.Title = http.StatusText(he.Status)
	}
	switch {
	case he.Status == http.StatusUnauthorized:
		return fmt.Errorf("mailchimp %s: %w: connect Mailchimp again, or replace the API key: %w: %w", op, me, connector.ErrCredentialsRejected, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("mailchimp %s: %w: %w", op, me, he)
	case he.Status == http.StatusNotFound:
		return fmt.Errorf("mailchimp %s: %w: %w", op, me, connector.ErrNotFound)
	}
	return fmt.Errorf("mailchimp %s: %w: %w", op, me, effects.ErrFatal)
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("mailchimp: "+format+": %w", append(a, effects.ErrFatal)...)
}

var serverRe = regexp.MustCompile(`^[a-z]{2}[0-9]{1,3}$`)

// server is the account's server prefix: the connection's, or an API key's
// suffix.
func server(creds map[string]string) (string, error) {
	s := strings.TrimSpace(creds["server"])
	if s == "" {
		if _, suffix, ok := strings.Cut(creds["access_token"], "-"); ok {
			s = suffix
		}
	}
	if !serverRe.MatchString(s) {
		return "", fatalf("the connection names no Mailchimp server (us6): connect again, or paste the whole API key, with its -us6 ending")
	}
	return s, nil
}

// base is where the connection's calls go: its server, or the base URL
// where the deployment fixes one or the connection names none (a pasted
// key always ends in its server, and a linked connection keeps it).
func (c *client) base(creds map[string]string) (string, error) {
	if c.fixed || strings.TrimSpace(creds["server"]) == "" && !strings.Contains(creds["access_token"], "-") {
		return strings.TrimRight(c.m.BaseURL, "/"), nil
	}
	s, err := server(creds)
	if err != nil {
		return "", err
	}
	return "https://" + s + ".api.mailchimp.com/3.0", nil
}

func (c *client) call(ctx context.Context, creds map[string]string, hc *http.Client, op, method, path string, body, out any) error {
	tok := creds["access_token"]
	if tok == "" {
		return fatalf("the connection has no API key or token")
	}
	base, err := c.base(creds)
	if err != nil {
		return err
	}
	if err := connector.DoJSON(ctx, hc, method, base+path, map[string]string{"Authorization": "Bearer " + tok}, body, out); err != nil {
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
	l, _ := m[k].([]any)
	for _, v := range l {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// memberID is how Mailchimp names a member: the MD5 of the lowercased
// address.
func memberID(email string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email)))) //nolint:gosec // Mailchimp's naming
	return hex.EncodeToString(sum[:])
}

// --- connecting ---

// link reads the server prefix and account Mailchimp's metadata names for
// the token.
func (c *client) link(ctx context.Context, lc connector.LinkCall) (map[string]string, error) {
	var md struct {
		DC          string `json:"dc"`
		AccountName string `json:"accountname"`
	}
	if err := connector.DoJSON(ctx, lc.HTTP, http.MethodGet, c.metadata, map[string]string{"Authorization": "OAuth " + lc.Token}, nil, &md); err != nil {
		return nil, classify("connecting", err)
	}
	if !serverRe.MatchString(md.DC) {
		return nil, fmt.Errorf("mailchimp named no server for the account: %w", connector.ErrLinkRefused)
	}
	out := map[string]string{}
	for k, v := range lc.Credentials {
		out[k] = v
	}
	out["server"], out["account_name"] = md.DC, md.AccountName
	return out, nil
}

// --- actions ---

func (c *client) getAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	var a struct {
		AccountID   string `json:"account_id"`
		AccountName string `json:"account_name"`
		Email       string `json:"email"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "get_account", http.MethodGet, "/", nil, &a); err != nil {
		return connector.Response{}, err
	}
	s, _ := server(req.Credentials)
	return connector.Response{Output: map[string]any{"account_id": a.AccountID, "account_name": a.AccountName, "email": a.Email, "server": s}}, nil
}

func (c *client) listAudiences(ctx context.Context, req connector.Request) (connector.Response, error) {
	var resp struct {
		Lists []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Stats struct {
				Members int64 `json:"member_count"`
			} `json:"stats"`
		} `json:"lists"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "list_audiences", http.MethodGet, "/lists?count=1000&fields=lists.id,lists.name,lists.stats.member_count", nil, &resp); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(resp.Lists))
	for _, l := range resp.Lists {
		out = append(out, map[string]any{"id": l.ID, "name": l.Name, "members": l.Stats.Members})
	}
	return connector.Response{Output: map[string]any{"audiences": out}}, nil
}

// consented says whether the input carries the person's yes to marketing.
func consented(in map[string]any) bool {
	c, _ := in["consent"].(map[string]any)
	yes, _ := c["opted_in"].(bool)
	return yes
}

func (c *client) addOrUpdateMember(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	aud, email := str(in, "audience_id"), str(in, "email")
	if aud == "" || email == "" {
		return connector.Response{}, fatalf("add_or_update_member: audience_id and email are required")
	}
	if !consented(in) {
		return connector.Response{}, fatalf("add_or_update_member: someone goes on an audience only when they opted in (consent.opted_in: true)")
	}
	merge := map[string]any{}
	if mf, _ := in["merge_fields"].(map[string]any); mf != nil {
		for k, v := range mf {
			merge[strings.ToUpper(k)] = v
		}
	}
	for k, f := range map[string]string{"FNAME": "first_name", "LNAME": "last_name", "PHONE": "phone"} {
		if v := str(in, f); v != "" {
			merge[k] = v
		}
	}
	// status_if_new only: someone who unsubscribed stays unsubscribed.
	body := map[string]any{"email_address": email, "status_if_new": "subscribed"}
	if len(merge) > 0 {
		body["merge_fields"] = merge
	}
	if tags := list(in, "tags"); len(tags) > 0 {
		body["tags"] = tags
	}
	cons, _ := in["consent"].(map[string]any)
	if at := str(cons, "at"); at != "" {
		body["timestamp_opt"] = at
	}
	var m struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	path := "/lists/" + url.PathEscape(aud) + "/members/" + memberID(email) + "?skip_merge_validation=true"
	if err := c.call(ctx, req.Credentials, req.HTTP, "add_or_update_member", http.MethodPut, path, body, &m); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"member_id": m.ID, "status": m.Status}}, nil
}

func (c *client) tagMember(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	aud, email := str(in, "audience_id"), str(in, "email")
	add, remove := list(in, "add"), list(in, "remove")
	if aud == "" || email == "" || len(add)+len(remove) == 0 {
		return connector.Response{}, fatalf("tag_member: audience_id, email and tags to add or remove are required")
	}
	tags := make([]map[string]string, 0, len(add)+len(remove))
	for _, t := range add {
		tags = append(tags, map[string]string{"name": t, "status": "active"})
	}
	for _, t := range remove {
		tags = append(tags, map[string]string{"name": t, "status": "inactive"})
	}
	id := memberID(email)
	if err := c.call(ctx, req.Credentials, req.HTTP, "tag_member", http.MethodPost, "/lists/"+url.PathEscape(aud)+"/members/"+id+"/tags", map[string]any{"tags": tags}, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"member_id": id}}, nil
}

type campaign struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Settings struct {
		Title string `json:"title"`
	} `json:"settings"`
}

func (c *client) createCampaign(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	aud, title := str(in, "audience_id"), str(in, "title")
	for _, k := range []string{"audience_id", "title", "subject", "from_name", "reply_to", "html"} {
		if str(in, k) == "" {
			return connector.Response{}, fatalf("create_campaign: %s is required", k)
		}
	}
	// Made already, by an earlier try: the audience's campaign with this
	// title.
	var found struct {
		Campaigns []campaign `json:"campaigns"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "create_campaign", http.MethodGet, "/campaigns?count=1000&list_id="+url.QueryEscape(aud)+"&fields=campaigns.id,campaigns.status,campaigns.settings.title", nil, &found); err != nil {
		return connector.Response{}, err
	}
	for _, x := range found.Campaigns {
		if x.Settings.Title == title {
			return connector.Response{Output: map[string]any{"campaign_id": x.ID, "created": false, "status": x.Status}}, nil
		}
	}
	recipients := map[string]any{"list_id": aud}
	if tag, ok := in["tag_id"].(float64); ok && tag > 0 {
		recipients["segment_opts"] = map[string]any{"saved_segment_id": int64(tag)}
	}
	settings := map[string]any{"title": title, "subject_line": str(in, "subject"), "from_name": str(in, "from_name"), "reply_to": str(in, "reply_to")}
	if p := str(in, "preview_text"); p != "" {
		settings["preview_text"] = p
	}
	var made campaign
	if err := c.call(ctx, req.Credentials, req.HTTP, "create_campaign", http.MethodPost, "/campaigns", map[string]any{"type": "regular", "recipients": recipients, "settings": settings}, &made); err != nil {
		return connector.Response{}, err
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "create_campaign", http.MethodPut, "/campaigns/"+url.PathEscape(made.ID)+"/content", map[string]any{"html": str(in, "html")}, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"campaign_id": made.ID, "created": true, "status": made.Status}}, nil
}

func (c *client) sendCampaign(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "campaign_id")
	if id == "" {
		return connector.Response{}, fatalf("send_campaign: campaign_id is required")
	}
	var x campaign
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_campaign", http.MethodGet, "/campaigns/"+url.PathEscape(id)+"?fields=id,status", nil, &x); err != nil {
		return connector.Response{}, err
	}
	// Sent already, or on its way: by an earlier try, or by hand.
	if x.Status == "sent" || x.Status == "sending" {
		return connector.Response{Output: map[string]any{"campaign_id": id, "status": x.Status}}, nil
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_campaign", http.MethodPost, "/campaigns/"+url.PathEscape(id)+"/actions/send", nil, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"campaign_id": id, "status": "sending"}}, nil
}
