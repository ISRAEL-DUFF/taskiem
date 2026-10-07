// Package pgdock is the first-party PGDock connector (docs/integrations/pgdock.md).
// It is built only from PGDock's published contracts (decision 0018): its
// webhooks and CLI docs and its OpenAPI file. Its row-changed trigger is
// registered remotely (decision 0021): Taskiem creates the PGDock webhook
// behind it when a workflow is deployed and deletes it when the workflow
// leaves the environment.
package pgdock

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector. BaseURL is the platform's PGDock (the
// default for connections that name no server; TASKIEM_PGDOCK_URL).
type Options struct {
	BaseURL string
}

// New returns the PGDock connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	base := strings.TrimRight(o.BaseURL, "/")
	if base != "" {
		m.BaseURL = base
		if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
			m.Egress = append([]string{u.Hostname()}, m.Egress...)
		}
	}
	c := &client{base: base}
	return &connector.Connector{Manifest: m,
		Actions: map[string]connector.Action{
			"test_connection": connector.ActionFunc(c.testConnection),
			"query_rows":      connector.ActionFunc(c.queryRows),
			"get_row":         connector.ActionFunc(c.getRow),
		},
		Registrars: map[string]connector.Registrar{"row_changed": registrar{c}},
		Enrichers:  map[string]connector.Enricher{"row_changed": c.enrich},
	}
}

type client struct{ base string }

// server is the connection's PGDock: its own server URL, or the platform's.
func (c *client) server(creds map[string]string) (string, error) {
	if s := strings.TrimRight(strings.TrimSpace(creds["base_url"]), "/"); s != "" {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" {
			return "", fmt.Errorf("pgdock: the connection's server URL must be https://host[/path]: %w", effects.ErrFatal)
		}
		return s, nil
	}
	if c.base == "" {
		return "", fmt.Errorf("pgdock: no PGDock server: set the connection's server URL (this platform has no default PGDock): %w", effects.ErrFatal)
	}
	return c.base, nil
}

// do calls PGDock's API with the connection's token and maps its errors.
func (c *client) do(ctx context.Context, creds map[string]string, hc *http.Client, method, path string, body, out any) error {
	tok := creds["token"]
	if tok == "" {
		return fmt.Errorf("pgdock: the connection has no API token: %w", effects.ErrFatal)
	}
	base, err := c.server(creds)
	if err != nil {
		return err
	}
	err = connector.DoJSON(ctx, hc, method, base+path, map[string]string{"Authorization": "Bearer " + tok}, body, out)
	return mapError(err)
}

func project(creds map[string]string) (string, error) {
	p := strings.TrimSpace(creds["project_id"])
	if p == "" {
		return "", fmt.Errorf("pgdock: the connection has no project: %w", effects.ErrFatal)
	}
	return url.PathEscape(p), nil
}

// --- test_connection ---

type tokenGrant struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	OrgID      string    `json:"org_id"`
	Scopes     []string  `json:"scopes"`
	ProjectIDs []string  `json:"project_ids"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// expiringSoon is when test_connection warns that the token expires.
const expiringSoon = 14 * 24 * time.Hour

// testConnection names the token's organisation and projects (GET /me,
// /orgs/{org}, /projects) and checks the connection's project is one of them.
func (c *client) testConnection(ctx context.Context, req connector.Request) (connector.Response, error) {
	var me struct {
		Token *tokenGrant `json:"token"`
	}
	if err := c.do(ctx, req.Credentials, req.HTTP, http.MethodGet, "/api/v1/me", nil, &me); err != nil {
		return connector.Response{}, err
	}
	if me.Token == nil {
		return connector.Response{}, fmt.Errorf("pgdock: the credential is not an API token: %w", effects.ErrFatal)
	}
	t := me.Token
	if want := strings.TrimSpace(req.Credentials["org_id"]); want != "" && !strings.EqualFold(want, t.OrgID) {
		return connector.Response{}, fmt.Errorf("pgdock: the token acts in organisation %s, not %s: %w", t.OrgID, want, effects.ErrFatal)
	}
	var org named
	if err := c.do(ctx, req.Credentials, req.HTTP, http.MethodGet, "/api/v1/orgs/"+url.PathEscape(t.OrgID), nil, &org); err != nil {
		return connector.Response{}, err
	}
	var list struct {
		Items []named `json:"items"`
	}
	if err := c.do(ctx, req.Credentials, req.HTTP, http.MethodGet, "/api/v1/projects?limit=500&org="+url.QueryEscape(t.OrgID), nil, &list); err != nil {
		return connector.Response{}, err
	}
	projects := []any{}
	var mine *named
	pid := strings.TrimSpace(req.Credentials["project_id"])
	for i, p := range list.Items {
		if t.ProjectIDs != nil && !slices.Contains(t.ProjectIDs, p.ID) {
			continue
		}
		projects = append(projects, map[string]any{"id": p.ID, "name": p.Name})
		if p.ID == pid {
			mine = &list.Items[i]
		}
	}
	if mine == nil {
		return connector.Response{}, fmt.Errorf("pgdock: project %s is not one this token reaches in %s: %w", pid, org.Name, effects.ErrFatal)
	}
	warnings := []any{}
	if t.ProjectIDs == nil {
		warnings = append(warnings, "the token reaches every project you can see in "+org.Name+"; restrict it to "+mine.Name)
	}
	if !slices.Contains(t.Scopes, "write") {
		warnings = append(warnings, "without the write scope Taskiem cannot create the webhooks row-changed triggers need")
	}
	if slices.Contains(t.Scopes, "admin") {
		warnings = append(warnings, "the token has the admin scope, which Taskiem does not need")
	}
	if !t.ExpiresAt.IsZero() && time.Until(t.ExpiresAt) < expiringSoon {
		warnings = append(warnings, "the token expires on "+t.ExpiresAt.UTC().Format("2 January 2006"))
	}
	scopes := make([]any, len(t.Scopes))
	for i, s := range t.Scopes {
		scopes[i] = s
	}
	out := map[string]any{
		"organisation": map[string]any{"id": t.OrgID, "name": org.Name},
		"project":      map[string]any{"id": mine.ID, "name": mine.Name},
		"projects":     projects, "scopes": scopes, "restricted": t.ProjectIDs != nil, "warnings": warnings,
	}
	if !t.ExpiresAt.IsZero() {
		out["expires_at"] = t.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return connector.Response{Output: out}, nil
}

// --- query_rows and get_row ---

type tablePage struct {
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columns"`
	Rows       [][]*string `json:"rows"`
	Next       string      `json:"next"`
	Order      string      `json:"order"`
	KeyColumns []string    `json:"key_columns"`
}

var ops = []string{"eq", "neq", "lt", "lte", "gt", "gte", "contains", "is_null", "not_null", "in"}

// rowsQuery builds the rows endpoint's query string. Filters and order are
// PGDock's structured JSON forms, which it compiles to a parameterised
// WHERE clause: values never become SQL text. The raw "where" parameter is
// never used.
func rowsQuery(filters, order []any, limit int, after string) (url.Values, error) {
	q := url.Values{}
	for i, f := range filters {
		m, ok := f.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("filters[%d] is not an object", i)
		}
		col, _ := m["column"].(string)
		op, _ := m["op"].(string)
		if col == "" || !slices.Contains(ops, op) {
			return nil, fmt.Errorf("filters[%d] needs a column and an op (%s)", i, strings.Join(ops, ", "))
		}
		item := map[string]any{"column": col, "op": op}
		switch op {
		case "is_null", "not_null":
		case "in":
			vals, ok := m["values"].([]any)
			if !ok {
				return nil, fmt.Errorf("filters[%d]: in takes values, a list", i)
			}
			item["values"] = vals
		default:
			v, ok := m["value"]
			if !ok {
				return nil, fmt.Errorf("filters[%d]: %s takes a value", i, op)
			}
			item["value"] = v
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("filters[%d]: %w", i, err)
		}
		q.Add("filter", string(raw))
	}
	for i, o := range order {
		m, ok := o.(map[string]any)
		col, _ := m["column"].(string)
		if !ok || col == "" {
			return nil, fmt.Errorf("order[%d] needs a column", i)
		}
		desc, _ := m["desc"].(bool)
		raw, _ := json.Marshal(map[string]any{"column": col, "desc": desc})
		q.Add("order", string(raw))
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("limit must be 1 to 1000, not %d", limit)
	}
	q.Set("limit", strconv.Itoa(limit))
	if after != "" {
		q.Set("after", after)
	}
	return q, nil
}

func tablePath(creds map[string]string, schema, table string) (string, error) {
	p, err := project(creds)
	if err != nil {
		return "", err
	}
	if schema == "" {
		schema = "public"
	}
	if table == "" {
		return "", fmt.Errorf("pgdock: a table is required: %w", effects.ErrFatal)
	}
	return "/api/v1/projects/" + p + "/tables/" + url.PathEscape(schema) + "/" + url.PathEscape(table) + "/rows", nil
}

func (c *client) rows(ctx context.Context, creds map[string]string, hc *http.Client, schema, table string, q url.Values) (tablePage, error) {
	var page tablePage
	path, err := tablePath(creds, schema, table)
	if err != nil {
		return page, err
	}
	err = c.do(ctx, creds, hc, http.MethodGet, path+"?"+q.Encode(), nil, &page)
	return page, err
}

// objects turns PGDock's rows (arrays of text, null for NULL) into objects
// keyed by column.
func (p tablePage) objects() []any {
	out := make([]any, 0, len(p.Rows))
	for _, r := range p.Rows {
		o := map[string]any{}
		for i, col := range p.Columns {
			if i < len(r) && r[i] != nil {
				o[col.Name] = *r[i]
			} else {
				o[col.Name] = nil
			}
		}
		out = append(out, o)
	}
	return out
}

func intOr(v any, d int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return d
}

func (c *client) queryRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	filters, _ := in["filters"].([]any)
	order, _ := in["order"].([]any)
	after, _ := in["after"].(string)
	q, err := rowsQuery(filters, order, intOr(in["limit"], 50), after)
	if err != nil {
		return connector.Response{}, fmt.Errorf("pgdock: %w: %w", err, effects.ErrFatal)
	}
	schema, _ := in["schema"].(string)
	table, _ := in["table"].(string)
	page, err := c.rows(ctx, req.Credentials, req.HTTP, schema, table, q)
	if err != nil {
		return connector.Response{}, err
	}
	cols := make([]any, len(page.Columns))
	for i, col := range page.Columns {
		cols[i] = map[string]any{"name": col.Name, "type": col.Type}
	}
	keys := make([]any, len(page.KeyColumns))
	for i, k := range page.KeyColumns {
		keys[i] = k
	}
	return connector.Response{Output: map[string]any{"rows": page.objects(), "columns": cols, "next": page.Next, "order": page.Order, "key_columns": keys}}, nil
}

// errAmbiguousKey: a "primary key" that matches several rows.
var errAmbiguousKey = errors.New("the key matches more than one row")

// row reads one row by its key columns.
func (c *client) row(ctx context.Context, creds map[string]string, hc *http.Client, schema, table string, key map[string]any) (map[string]any, bool, error) {
	if len(key) == 0 {
		return nil, false, fmt.Errorf("pgdock: a key is required: %w", effects.ErrFatal)
	}
	cols := make([]string, 0, len(key))
	for k := range key {
		cols = append(cols, k)
	}
	slices.Sort(cols)
	filters := make([]any, 0, len(cols))
	for _, k := range cols {
		filters = append(filters, map[string]any{"column": k, "op": "eq", "value": key[k]})
	}
	q, err := rowsQuery(filters, nil, 2, "")
	if err != nil {
		return nil, false, fmt.Errorf("pgdock: %w: %w", err, effects.ErrFatal)
	}
	page, err := c.rows(ctx, creds, hc, schema, table, q)
	if err != nil {
		return nil, false, err
	}
	rows := page.objects()
	switch len(rows) {
	case 0:
		return nil, false, nil
	case 1:
		return rows[0].(map[string]any), true, nil
	}
	return nil, false, fmt.Errorf("pgdock: %w: %w", errAmbiguousKey, effects.ErrFatal)
}

func (c *client) getRow(ctx context.Context, req connector.Request) (connector.Response, error) {
	schema, _ := req.Input["schema"].(string)
	table, _ := req.Input["table"].(string)
	key, _ := req.Input["key"].(map[string]any)
	row, found, err := c.row(ctx, req.Credentials, req.HTTP, schema, table, key)
	if err != nil {
		return connector.Response{}, err
	}
	out := map[string]any{"found": found}
	if found {
		out["row"] = row
	}
	return connector.Response{Output: out}, nil
}

// splitTable reads "schema.table" (or "table", in public).
func splitTable(s string) (schema, table string) {
	if sch, t, ok := strings.Cut(s, "."); ok {
		return sch, t
	}
	return "public", s
}
