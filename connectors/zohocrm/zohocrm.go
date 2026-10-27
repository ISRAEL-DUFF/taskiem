// Package zohocrm is the Zoho CRM connector: records of any module (leads,
// contacts, deals and the business's own), upserted so a retry updates
// rather than duplicates, notes, and CRM's notification watches as a
// remotely registered trigger, renewed before they lapse. Through Taskiem's
// Zoho client in the account's own data centre (decision 0051). Built from
// Zoho's public developer documentation (docs/integrations/zoho-crm.md).
//
// Errors: CRM answers {"code", "details", "message", "status": "error"},
// for a whole request or each record of a write. 401 (INVALID_TOKEN) is a
// refused token: refreshed once, then the connection is expired; 429 is a
// rate limit (retried); INVALID_URL_PATTERN and 404 are a module or
// record that is not there; other refusals are fatal; 5xx is an unknown
// outcome.
package zohocrm

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
// CRM API and AccountsServer its accounts server (tests, a proxy).
type Options struct {
	BaseURL, AccountsServer string
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// New returns the Zoho CRM connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{m: m, now: time.Now}
	if o.Now != nil {
		c.now = o.Now
	}
	c.zoho = &zoho.Client{Manifest: m, Name: "Zoho CRM", AccountsServer: o.AccountsServer, Now: c.now}
	if o.BaseURL != "" {
		m.OverrideBaseURL(o.BaseURL)
		c.fixed = true
	}
	if p, err := url.Parse(o.AccountsServer); err == nil && p.Hostname() != "" {
		m.Egress = append(m.Egress, p.Hostname())
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_modules":   connector.ActionFunc(c.listModules),
		"upsert_record":  connector.ActionFunc(c.upsertRecord),
		"update_record":  connector.ActionFunc(c.updateRecord),
		"get_record":     connector.ActionFunc(c.getRecord),
		"search_records": connector.ActionFunc(c.searchRecords),
		"add_note":       connector.ActionFunc(c.addNote),
	}, Registrars: map[string]connector.Registrar{"record_changes": &watches{c: c}}}
}

type client struct {
	m     *connector.Manifest
	zoho  *zoho.Client
	fixed bool
	now   func() time.Time
}

// --- errors ---

// Error is a CRM refusal.
type Error struct {
	Status  int
	Code    string
	Message string
	Details json.RawMessage
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("zoho crm: http %d: %s (%s)", e.Status, e.Message, e.Code)
	if len(e.Details) > 2 && string(e.Details) != "{}" {
		msg += " " + string(e.Details)
	}
	return msg
}

type fault struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Status  string          `json:"status"`
	Details json.RawMessage `json:"details"`
}

func (f fault) err(op string, status int) error {
	ce := &Error{Status: status, Code: f.Code, Message: f.Message, Details: f.Details}
	switch {
	case status == http.StatusUnauthorized || f.Code == "INVALID_TOKEN" || f.Code == "AUTHENTICATION_FAILURE":
		return fmt.Errorf("zoho crm %s: %w: %w: %w", op, ce, connector.ErrCredentialsRejected, effects.ErrFatal)
	case status == http.StatusTooManyRequests || f.Code == "TOO_MANY_REQUESTS":
		return fmt.Errorf("zoho crm %s: %w: %w", op, ce, effects.ErrRetryable)
	case status >= 500:
		return fmt.Errorf("zoho crm %s: %w: %w", op, ce, effects.ErrUnknownOutcome)
	case status == http.StatusNotFound || f.Code == "INVALID_URL_PATTERN" || f.Code == "INVALID_MODULE":
		return fmt.Errorf("zoho crm %s: %w: %w", op, ce, connector.ErrNotFound)
	}
	return fmt.Errorf("zoho crm %s: %w: %w", op, ce, effects.ErrFatal)
}

func classify(op string, err error) error {
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return fmt.Errorf("zoho crm %s: %w", op, err)
	}
	var f fault
	_ = json.Unmarshal(he.Body, &f)
	// A write refused for its records answers them in data[].
	if f.Code == "" {
		var w struct {
			Data []fault `json:"data"`
		}
		if json.Unmarshal(he.Body, &w) == nil && len(w.Data) > 0 {
			f = w.Data[0]
		}
	}
	if f.Message == "" {
		f.Message = http.StatusText(he.Status)
	}
	if he.Status == http.StatusTooManyRequests || he.Status >= 500 {
		return fmt.Errorf("%w: %w", f.err(op, he.Status), he) // he carries Retry-After
	}
	return f.err(op, he.Status)
}

// --- calls ---

func (c *client) base(s *zoho.Session) (string, error) {
	if c.fixed || strings.TrimSpace(s.Creds["api_domain"]) == "" {
		return strings.TrimRight(c.m.BaseURL, "/"), nil
	}
	d, err := s.Server("api_domain")
	return d + "/crm/v8", err
}

// do calls CRM; a 204 (nothing found) leaves out as it is.
func (c *client) do(ctx context.Context, s *zoho.Session, op, method, path string, q url.Values, body, out any) error {
	base, err := c.base(s)
	if err != nil {
		return err
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if err := s.Do(ctx, method, u, body, out); err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			return classify(op, err)
		}
		return err
	}
	return nil
}

// written is one record's answer to a write.
type written struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Action  string `json:"action"`
	// DuplicateField names the field an upsert matched by.
	DuplicateField string `json:"duplicate_field"`
	Details        struct {
		ID zoho.ID `json:"id"`
	} `json:"details"`
	Raw json.RawMessage `json:"-"`
}

// write sends one record and answers its result, a refusal of it as an
// error.
func (c *client) write(ctx context.Context, s *zoho.Session, op, method, path string, body map[string]any) (written, error) {
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := c.do(ctx, s, op, method, path, nil, body, &resp); err != nil {
		return written{}, err
	}
	if len(resp.Data) == 0 {
		return written{}, fmt.Errorf("zoho crm %s: the answer has no record: %w", op, effects.ErrUnknownOutcome)
	}
	var w written
	if err := json.Unmarshal(resp.Data[0], &w); err != nil {
		return written{}, fmt.Errorf("zoho crm %s: decoding the answer: %w: %w", op, err, effects.ErrUnknownOutcome)
	}
	if w.Status == "error" {
		var f fault
		_ = json.Unmarshal(resp.Data[0], &f)
		return w, f.err(op, http.StatusBadRequest)
	}
	return w, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// module is a module's API name as a path segment.
func module(c *client, in map[string]any) (string, error) {
	m := str(in, "module")
	if m == "" || strings.ContainsAny(m, "/?#") {
		return "", c.zoho.Fatalf("give module, the module's API name (Leads, Contacts, Deals…)")
	}
	return url.PathEscape(m), nil
}

func triggers(in map[string]any) []string {
	if run, ok := in["run_workflows"].(bool); ok && !run {
		return []string{}
	}
	return []string{"workflow"}
}

// --- actions ---

func (c *client) listModules(ctx context.Context, req connector.Request) (connector.Response, error) {
	var resp struct {
		Modules []struct {
			APIName      string `json:"api_name"`
			Singular     string `json:"singular_label"`
			Plural       string `json:"plural_label"`
			APISupported bool   `json:"api_supported"`
		} `json:"modules"`
	}
	if err := c.do(ctx, c.zoho.Open(req), "list_modules", http.MethodGet, "/settings/modules", nil, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	out := []any{}
	for _, m := range resp.Modules {
		if m.APISupported {
			out = append(out, map[string]any{"api_name": m.APIName, "singular": m.Singular, "plural": m.Plural})
		}
	}
	return connector.Response{Output: map[string]any{"modules": out}}, nil
}

func (c *client) upsertRecord(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	mod, err := module(c, in)
	if err != nil {
		return connector.Response{}, err
	}
	rec, _ := in["record"].(map[string]any)
	if len(rec) == 0 {
		return connector.Response{}, c.zoho.Fatalf("upsert_record: record names no fields")
	}
	body := map[string]any{"data": []any{rec}, "trigger": triggers(in)}
	if dup, _ := in["duplicate_check_fields"].([]any); len(dup) > 0 {
		body["duplicate_check_fields"] = dup
	}
	w, err := c.write(ctx, c.zoho.Open(req), "upsert_record", http.MethodPost, "/"+mod+"/upsert", body)
	if err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"id": string(w.Details.ID), "action": w.Action}
	if w.DuplicateField != "" {
		out["duplicate_field"] = w.DuplicateField
	}
	return connector.Response{Output: out}, nil
}

func (c *client) updateRecord(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	mod, err := module(c, in)
	if err != nil {
		return connector.Response{}, err
	}
	id := str(in, "id")
	rec, _ := in["record"].(map[string]any)
	if id == "" || len(rec) == 0 {
		return connector.Response{}, c.zoho.Fatalf("update_record: id and record are required")
	}
	w, err := c.write(ctx, c.zoho.Open(req), "update_record", http.MethodPut, "/"+mod+"/"+url.PathEscape(id), map[string]any{"data": []any{rec}, "trigger": triggers(in)})
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"id": string(w.Details.ID)}}, nil
}

func (c *client) getRecord(ctx context.Context, req connector.Request) (connector.Response, error) {
	mod, err := module(c, req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	id := str(req.Input, "id")
	if id == "" {
		return connector.Response{}, c.zoho.Fatalf("get_record: id is required")
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.do(ctx, c.zoho.Open(req), "get_record", http.MethodGet, "/"+mod+"/"+url.PathEscape(id), nil, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	if len(resp.Data) == 0 {
		return connector.Response{}, fmt.Errorf("zoho crm get_record: no record %s: %w", id, connector.ErrNotFound)
	}
	return connector.Response{Output: map[string]any{"id": id, "record": resp.Data[0]}}, nil
}

func (c *client) searchRecords(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	mod, err := module(c, in)
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{}
	for _, k := range []string{"email", "phone", "word", "criteria"} {
		if v := str(in, k); v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return connector.Response{}, c.zoho.Fatalf("search_records: give an email, a phone, a word or criteria")
	}
	n := 50
	if f, ok := in["limit"].(float64); ok && f >= 1 && f <= 200 {
		n = int(f)
	}
	q.Set("per_page", strconv.Itoa(n))
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.do(ctx, c.zoho.Open(req), "search_records", http.MethodGet, "/"+mod+"/search", q, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(resp.Data))
	for _, r := range resp.Data {
		id, _ := r["id"].(string)
		out = append(out, map[string]any{"id": id, "record": r})
	}
	return connector.Response{Output: map[string]any{"records": out}}, nil
}

func (c *client) addNote(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	mod, err := module(c, in)
	if err != nil {
		return connector.Response{}, err
	}
	id, title, text := str(in, "id"), str(in, "title"), str(in, "content")
	if id == "" || text == "" {
		return connector.Response{}, c.zoho.Fatalf("add_note: id and content are required")
	}
	s := c.zoho.Open(req)
	path := "/" + mod + "/" + url.PathEscape(id) + "/Notes"
	// On the record already, from an earlier try.
	var notes struct {
		Data []struct {
			ID      zoho.ID `json:"id"`
			Title   string  `json:"Note_Title"`
			Content string  `json:"Note_Content"`
		} `json:"data"`
	}
	if err := c.do(ctx, s, "add_note", http.MethodGet, path, url.Values{"fields": {"Note_Title,Note_Content"}, "per_page": {"200"}}, nil, &notes); err != nil {
		return connector.Response{}, err
	}
	for _, n := range notes.Data {
		if strings.TrimSpace(n.Title) == title && strings.TrimSpace(n.Content) == text {
			return connector.Response{Output: map[string]any{"note_id": string(n.ID), "created": false}}, nil
		}
	}
	note := map[string]any{"Note_Content": text}
	if title != "" {
		note["Note_Title"] = title
	}
	w, err := c.write(ctx, s, "add_note", http.MethodPost, path, map[string]any{"data": []any{note}})
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"note_id": string(w.Details.ID), "created": true}}, nil
}
