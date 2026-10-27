// Package brevo is the Brevo connector: transactional email and SMS from
// the business's own sender, contacts upserted by email, lists (only for
// people who opted in), campaigns, and Brevo's transactional and marketing
// events through webhooks Taskiem creates (decision 0021) with a bearer
// token of its own. Built from Brevo's public API reference
// (docs/integrations/brevo.md).
//
// Errors: Brevo answers {"code", "message"}. 401 (unauthorized) is a key
// Brevo refuses (the connection is expired); 429 is a rate limit
// (retried); 404 (document_not_found) is a contact, list or campaign that
// is not there; not_enough_credits and other refusals are fatal; 5xx is an
// unknown outcome. Sending takes no idempotency key: an unknown outcome is
// parked for a person.
package brevo

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector: BaseURL replaces
// https://api.brevo.com/v3 (tests, a proxy).
type Options struct{ BaseURL string }

// New returns the Brevo connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{m: m}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_account":              connector.ActionFunc(c.getAccount),
		"send_email":               connector.ActionFunc(c.sendEmail),
		"send_sms":                 connector.ActionFunc(c.sendSMS),
		"create_or_update_contact": connector.ActionFunc(c.createOrUpdateContact),
		"add_to_list":              connector.ActionFunc(c.addToList),
		"list_lists":               connector.ActionFunc(c.listLists),
		"send_campaign":            connector.ActionFunc(c.sendCampaign),
	}, Registrars: map[string]connector.Registrar{
		"transactional_events": &webhooks{c: c, kind: "transactional", events: m.Triggers["transactional_events"].Events},
		"marketing_events":     &webhooks{c: c, kind: "marketing", events: m.Triggers["marketing_events"].Events},
	}}
}

type client struct{ m *connector.Manifest }

// Error is a Brevo refusal.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("brevo: http %d: %s (%s)", e.Status, e.Message, e.Code)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("brevo %s: %w", op, err)
	}
	var b struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(he.Body, &b)
	be := &Error{Status: he.Status, Code: b.Code, Message: b.Message}
	if be.Message == "" {
		be.Message = http.StatusText(he.Status)
	}
	switch {
	case he.Status == http.StatusUnauthorized || b.Code == "unauthorized":
		return fmt.Errorf("brevo %s: %w: replace the API key on the connection: %w: %w", op, be, connector.ErrCredentialsRejected, effects.ErrFatal)
	case he.Status == http.StatusTooManyRequests || he.Status >= 500:
		return fmt.Errorf("brevo %s: %w: %w", op, be, he)
	case he.Status == http.StatusNotFound || b.Code == "document_not_found":
		return fmt.Errorf("brevo %s: %w: %w", op, be, connector.ErrNotFound)
	}
	return fmt.Errorf("brevo %s: %w: %w", op, be, effects.ErrFatal)
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("brevo: "+format+": %w", append(a, effects.ErrFatal)...)
}

func (c *client) call(ctx context.Context, creds map[string]string, hc *http.Client, op, method, path string, body, out any) error {
	key := creds["api_key"]
	if key == "" {
		return fatalf("the connection has no API key")
	}
	if err := connector.DoJSON(ctx, hc, method, strings.TrimRight(c.m.BaseURL, "/")+path, map[string]string{"api-key": key}, body, out); err != nil {
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

func ints(m map[string]any, k string) []int64 {
	var out []int64
	l, _ := m[k].([]any)
	for _, v := range l {
		switch n := v.(type) {
		case float64:
			out = append(out, int64(n))
		case int64:
			out = append(out, n)
		case int:
			out = append(out, int64(n))
		case json.Number:
			if i, err := n.Int64(); err == nil {
				out = append(out, i)
			}
		}
	}
	return out
}

func integer(m map[string]any, k string) (int64, bool) {
	l := ints(map[string]any{k: []any{m[k]}}, k)
	if len(l) == 0 {
		return 0, false
	}
	return l[0], true
}

// consented says whether the input carries the person's yes to marketing
// (consent.opted_in: true). Nobody is put on a marketing list, or sent a
// marketing text, without it.
func consented(in map[string]any) bool {
	c, _ := in["consent"].(map[string]any)
	yes, _ := c["opted_in"].(bool)
	return yes
}

// people is a list of addresses as Brevo takes them.
func people(addrs []string) []map[string]string {
	out := make([]map[string]string, 0, len(addrs))
	for _, a := range addrs {
		if p, err := mail.ParseAddress(a); err == nil {
			e := map[string]string{"email": p.Address}
			if p.Name != "" {
				e["name"] = p.Name
			}
			out = append(out, e)
		} else {
			out = append(out, map[string]string{"email": a})
		}
	}
	return out
}

// --- the account ---

type domain struct {
	Name          string `json:"domain_name"`
	Authenticated bool   `json:"authenticated"`
	Verified      bool   `json:"verified"`
}

func (c *client) domains(ctx context.Context, req connector.Request, op string) ([]domain, error) {
	var resp struct {
		Domains []domain `json:"domains"`
	}
	err := c.call(ctx, req.Credentials, req.HTTP, op, http.MethodGet, "/senders/domains", nil, &resp)
	return resp.Domains, err
}

func (c *client) getAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	var a struct {
		Email       string `json:"email"`
		CompanyName string `json:"companyName"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "get_account", http.MethodGet, "/account", nil, &a); err != nil {
		return connector.Response{}, err
	}
	ds, err := c.domains(ctx, req, "get_account")
	if err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]any{"domain": d.Name, "authenticated": d.Authenticated, "verified": d.Verified})
	}
	return connector.Response{Output: map[string]any{"email": a.Email, "company_name": a.CompanyName, "domains": out}}, nil
}

// --- sending ---

func (c *client) sendEmail(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	from, to := str(in, "sender_email"), list(in, "to")
	tmpl, hasTmpl := integer(in, "template_id")
	if from == "" || len(to) == 0 {
		return connector.Response{}, fatalf("send_email: sender_email and to are required")
	}
	if !hasTmpl && (str(in, "subject") == "" || str(in, "html") == "" && str(in, "text") == "") {
		return connector.Response{}, fatalf("send_email: give a subject and html or text, or a template_id")
	}
	// The domain is the business's: authenticated in Brevo, or nothing is
	// sent.
	_, d, _ := strings.Cut(strings.ToLower(from), "@")
	ds, err := c.domains(ctx, req, "send_email")
	if err != nil {
		return connector.Response{}, err
	}
	ok := false
	for _, x := range ds {
		ok = ok || strings.EqualFold(x.Name, d) && x.Authenticated
	}
	if !ok {
		return connector.Response{}, fatalf("send_email: %s is not a domain authenticated in Brevo: authenticate it there (Senders, domains and dedicated IPs), or send from one that is", d)
	}
	sender := map[string]string{"email": from}
	if n := str(in, "sender_name"); n != "" {
		sender["name"] = n
	}
	body := map[string]any{"sender": sender, "to": people(to)}
	if cc := list(in, "cc"); len(cc) > 0 {
		body["cc"] = people(cc)
	}
	if bcc := list(in, "bcc"); len(bcc) > 0 {
		body["bcc"] = people(bcc)
	}
	if r := str(in, "reply_to"); r != "" {
		body["replyTo"] = people([]string{r})[0]
	}
	if hasTmpl {
		body["templateId"] = tmpl
		if p, _ := in["params"].(map[string]any); len(p) > 0 {
			body["params"] = p
		}
	}
	for k, f := range map[string]string{"subject": "subject", "htmlContent": "html", "textContent": "text"} {
		if v := str(in, f); v != "" {
			body[k] = v
		}
	}
	if tags := list(in, "tags"); len(tags) > 0 {
		body["tags"] = tags
	}
	if atts, _ := in["attachments"].([]any); len(atts) > 0 {
		var out []map[string]string
		for i, v := range atts {
			a, _ := v.(map[string]any)
			att := map[string]string{"name": str(a, "name")}
			switch {
			case str(a, "content_base64") != "":
				att["content"] = str(a, "content_base64")
			case str(a, "url") != "":
				if u, err := url.Parse(str(a, "url")); err != nil || u.Scheme != "https" {
					return connector.Response{}, fatalf("send_email: attachment %d: url must be an https link", i+1)
				}
				att["url"] = str(a, "url")
			default:
				return connector.Response{}, fatalf("send_email: attachment %d has neither content_base64 nor url", i+1)
			}
			out = append(out, att)
		}
		body["attachment"] = out
	}
	var resp struct {
		MessageID string `json:"messageId"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_email", http.MethodPost, "/smtp/email", body, &resp); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": resp.MessageID}}, nil
}

func (c *client) sendSMS(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	sender, to, text := str(in, "sender"), str(in, "recipient"), str(in, "content")
	if sender == "" || to == "" || text == "" {
		return connector.Response{}, fatalf("send_sms: sender, recipient and content are required")
	}
	kind := str(in, "type")
	if kind == "" {
		kind = "transactional"
	}
	if kind == "marketing" && !consented(in) {
		return connector.Response{}, fatalf("send_sms: a marketing text goes only to someone who opted in (consent.opted_in: true)")
	}
	body := map[string]any{"sender": sender, "recipient": strings.TrimPrefix(to, "+"), "content": text, "type": kind}
	if t := str(in, "tag"); t != "" {
		body["tag"] = t
	}
	var resp struct {
		MessageID json.Number `json:"messageId"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_sms", http.MethodPost, "/transactionalSMS/send", body, &resp); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": resp.MessageID.String()}}, nil
}

// --- contacts and lists ---

func (c *client) createOrUpdateContact(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	email := str(in, "email")
	if email == "" {
		return connector.Response{}, fatalf("create_or_update_contact: email is required")
	}
	lists := ints(in, "list_ids")
	if len(lists) > 0 && !consented(in) {
		return connector.Response{}, fatalf("create_or_update_contact: a contact goes on a list only when they opted in (consent.opted_in: true); without it, leave list_ids out")
	}
	attrs := map[string]any{}
	if a, _ := in["attributes"].(map[string]any); a != nil {
		for k, v := range a {
			attrs[strings.ToUpper(k)] = v
		}
	}
	for k, f := range map[string]string{"FIRSTNAME": "first_name", "LASTNAME": "last_name", "SMS": "phone"} {
		if v := str(in, f); v != "" {
			attrs[k] = v
		}
	}
	body := map[string]any{"email": email, "updateEnabled": true}
	if len(attrs) > 0 {
		body["attributes"] = attrs
	}
	if len(lists) > 0 {
		body["listIds"] = lists
	}
	var resp struct {
		ID json.Number `json:"id"`
	}
	// 201 with the new contact's id; 204, no body, when it was updated.
	if err := c.call(ctx, req.Credentials, req.HTTP, "create_or_update_contact", http.MethodPost, "/contacts", body, &resp); err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"created": resp.ID != ""}
	if resp.ID != "" {
		out["contact_id"] = resp.ID.String()
	}
	return connector.Response{Output: out}, nil
}

func (c *client) addToList(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	id, ok := integer(in, "list_id")
	emails := list(in, "emails")
	if !ok || len(emails) == 0 {
		return connector.Response{}, fatalf("add_to_list: list_id and emails are required")
	}
	if !consented(in) {
		return connector.Response{}, fatalf("add_to_list: contacts go on a list only when they opted in (consent.opted_in: true)")
	}
	var resp struct {
		Contacts struct {
			Success []string `json:"success"`
			Failure []string `json:"failure"`
		} `json:"contacts"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "add_to_list", http.MethodPost, "/contacts/lists/"+strconv.FormatInt(id, 10)+"/contacts/add", map[string]any{"emails": emails}, &resp); err != nil {
		var be *Error
		// Already on the list: nothing to add.
		if errors.As(err, &be) && be.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(be.Message), "already in list") {
			return connector.Response{Output: map[string]any{"added": []string{}, "failed": []string{}}}, nil
		}
		return connector.Response{}, err
	}
	added, failed := resp.Contacts.Success, resp.Contacts.Failure
	if added == nil {
		added = []string{}
	}
	if failed == nil {
		failed = []string{}
	}
	return connector.Response{Output: map[string]any{"added": added, "failed": failed}}, nil
}

func (c *client) listLists(ctx context.Context, req connector.Request) (connector.Response, error) {
	var resp struct {
		Lists []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Subscribers int64  `json:"uniqueSubscribers"`
		} `json:"lists"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "list_lists", http.MethodGet, "/contacts/lists?limit=50&offset=0", nil, &resp); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(resp.Lists))
	for _, l := range resp.Lists {
		out = append(out, map[string]any{"id": l.ID, "name": l.Name, "subscribers": l.Subscribers})
	}
	return connector.Response{Output: map[string]any{"lists": out}}, nil
}

// --- campaigns ---

func (c *client) sendCampaign(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, ok := integer(req.Input, "campaign_id")
	if !ok {
		return connector.Response{}, fatalf("send_campaign: campaign_id is required")
	}
	path := "/emailCampaigns/" + strconv.FormatInt(id, 10)
	var camp struct {
		Status string `json:"status"`
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_campaign", http.MethodGet, path, nil, &camp); err != nil {
		return connector.Response{}, err
	}
	// Sent already, or on its way: by an earlier try, or by hand.
	switch camp.Status {
	case "sent", "queued", "in_process":
		return connector.Response{Output: map[string]any{"campaign_id": id, "status": camp.Status}}, nil
	}
	if err := c.call(ctx, req.Credentials, req.HTTP, "send_campaign", http.MethodPost, path+"/sendNow", nil, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"campaign_id": id, "status": "queued"}}, nil
}
