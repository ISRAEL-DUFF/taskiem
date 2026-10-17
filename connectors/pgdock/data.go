package pgdock

// The project's data API (Xenibase docs/backend-services.md, read
// 17 October 2026: Keys, Reading data, Writing data, Retrying writes
// safely: Idempotency-Key, Functions; docs/errors.md, the Data API table).
// It is served at the project's own hostname (the connection's api_url,
// which the project's services answer names as url) and authenticated
// with the project's secret key in the apikey header, so every call runs
// as the service role. Management API tokens are not accepted there.
//
// Every write (insert, upsert, update, delete, function call) carries the
// engine's derived key as Idempotency-Key: a repeat within 24 hours gets
// the first answer with Idempotent-Replayed: true and writes nothing, and
// the same key with a different request is refused (422
// idempotency_key_reused). Values go in JSON bodies or in bound where
// parameters, never SQL text.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// maxDataBody is the data API's result ceiling (10 MB, 413
// result_too_large above it).
const maxDataBody = 10 << 20

// keyWindow is how long Xenibase keeps an idempotency key.
const keyWindow = 24 * time.Hour

// dataAPI reads the connection's data API URL and secret key. The URL is
// https://<ref>.<api-domain>; plain http is accepted only for a loopback
// host (a local xenibase-edge in tests), which the egress guard refuses in
// production anyway.
func dataAPI(creds map[string]string) (base, key string, err error) {
	key = strings.TrimSpace(creds["secret_key"])
	if key == "" {
		return "", "", fmt.Errorf("pgdock: the connection has no project secret key (xb_sec_…), which writes, functions and read_rows need: %w", effects.ErrFatal)
	}
	base = strings.TrimRight(strings.TrimSpace(creds["api_url"]), "/")
	if base == "" {
		return "", "", fmt.Errorf("pgdock: the connection has no data API URL (https://<ref>.<api-domain>, on the project's API page): %w", effects.ErrFatal)
	}
	u, perr := url.Parse(base)
	if perr != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("pgdock: the connection's data API URL must be https://host: %w", effects.ErrFatal)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback(u.Hostname())) {
		return "", "", fmt.Errorf("pgdock: the connection's data API URL must be https://host: %w", effects.ErrFatal)
	}
	return base, key, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// doData sends one data API request and decodes its answer into out. It
// reports whether Xenibase replayed an earlier attempt's answer
// (Idempotent-Replayed: true). Errors are classified by mapDataError;
// transport errors by connector.ClassifyTransport.
func (c *client) doData(ctx context.Context, req connector.Request, method, path string, q url.Values, body, out any) (replayed bool, err error) {
	base, key, err := dataAPI(req.Credentials)
	if err != nil {
		return false, err
	}
	write := method != http.MethodGet
	if write && req.IdempotencyKey != "" && req.Attempt > 1 && !req.KeyFirstSent.IsZero() && time.Since(req.KeyFirstSent) > keyWindow {
		// Xenibase forgets the key after a day; sending it again could
		// run the write twice. The earlier attempt may have committed.
		return false, fmt.Errorf("pgdock: the idempotency key was first sent over 24 hours ago, as long as Xenibase keeps it; an earlier attempt may have written, so check the table before running this step again: %w", effects.ErrFatal)
	}
	target := base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return false, fmt.Errorf("pgdock: encode request: %w: %w", err, effects.ErrFatal)
		}
		rd = bytes.NewReader(raw)
	}
	hr, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return false, fmt.Errorf("%w: %w", connector.RedactURLError(err), effects.ErrFatal)
	}
	hr.Header.Set("apikey", key)
	hr.Header.Set("Accept", "application/json")
	if body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if write && req.IdempotencyKey != "" {
		hr.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}
	hc := req.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hr)
	if err != nil {
		return false, connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDataBody+1))
	if err != nil {
		return false, fmt.Errorf("pgdock: read response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var ra time.Duration
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			ra = time.Duration(s) * time.Second
		}
		return false, mapDataError(resp.StatusCode, raw, ra)
	}
	if len(raw) > maxDataBody {
		return false, fmt.Errorf("pgdock: the answer is over 10 MB; ask for fewer rows or columns: %w", effects.ErrFatal)
	}
	replayed = strings.EqualFold(resp.Header.Get("Idempotent-Replayed"), "true")
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			// Xenibase acted but the answer cannot be read.
			return replayed, fmt.Errorf("pgdock: decode response: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return replayed, nil
}

// --- filters ---

// dataOps are the data API's where operators (Reading data).
var dataOps = []string{"eq", "neq", "lt", "lte", "gt", "gte", "in", "like", "ilike", "is", "contains", "contained_by", "search"}

// dataFilter is one structured filter: the query_rows form (column, op,
// value or values, with is_null and not_null) plus the data API's own
// operators.
type dataFilter struct {
	column, op string
	value      any
	values     []any
}

// parseDataFilters checks filters as the actions take them. A column is a
// plain name or a JSON path (meta->tag, meta->>tag), never anything that
// could be read as an operator or a condition.
func parseDataFilters(filters []any) ([]dataFilter, error) {
	out := make([]dataFilter, 0, len(filters))
	for i, f := range filters {
		m, ok := f.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("filters[%d] is not an object", i)
		}
		col, _ := m["column"].(string)
		op, _ := m["op"].(string)
		if !columnRe.MatchString(col) {
			return nil, fmt.Errorf("filters[%d] needs a column (a name, or a JSON path like meta->tag)", i)
		}
		if !slices.Contains(dataOps, op) && op != "is_null" && op != "not_null" {
			return nil, fmt.Errorf("filters[%d] needs an op (%s, is_null, not_null)", i, strings.Join(dataOps, ", "))
		}
		df := dataFilter{column: col, op: op}
		switch op {
		case "is_null", "not_null":
		case "in":
			vals, ok := m["values"].([]any)
			if !ok || len(vals) == 0 {
				return nil, fmt.Errorf("filters[%d]: in takes values, a non-empty list", i)
			}
			df.values = vals
		case "is":
			v, ok := m["value"]
			if _, isBool := v.(bool); !ok || v != nil && !isBool {
				return nil, fmt.Errorf("filters[%d]: is takes null, true or false", i)
			}
			df.value = v
		default:
			v, ok := m["value"]
			if !ok {
				return nil, fmt.Errorf("filters[%d]: %s takes a value", i, op)
			}
			df.value = v
		}
		out = append(out, df)
	}
	return out, nil
}

// valueText is a value as the where parameter's grammar takes it. The
// whole parameter is percent-encoded when the URL is built, so colons and
// commas in a value reach Xenibase as they are.
func valueText(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case json.Number:
		return x.String(), nil
	}
	raw, err := json.Marshal(v) // arrays and objects, for contains and contained_by
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// whereParams renders filters as the data API's where parameters
// (column:op:value, each a separate where=, all of which must hold), for
// an update or delete by filter. An in list is comma-joined, with double
// quotes around a value holding a comma or a quote (a,b,"c,d"). not_null
// has no where form (the grammar has no not), so it is refused: a read
// through the query body supports it.
func whereParams(filters []dataFilter) ([]string, error) {
	out := make([]string, 0, len(filters))
	for i, f := range filters {
		var text string
		switch f.op {
		case "not_null":
			return nil, fmt.Errorf("filters[%d]: not_null is not supported for writes (the data API's where has no not); filter on the rows another way, or update by key", i)
		case "is_null":
			text = "is:null"
		case "in":
			parts := make([]string, 0, len(f.values))
			for _, v := range f.values {
				s, err := valueText(v)
				if err != nil {
					return nil, fmt.Errorf("filters[%d]: %w", i, err)
				}
				if strings.ContainsAny(s, `,"`) {
					s = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
				}
				parts = append(parts, s)
			}
			text = "in:" + strings.Join(parts, ",")
		default:
			s, err := valueText(f.value)
			if err != nil {
				return nil, fmt.Errorf("filters[%d]: %w", i, err)
			}
			text = f.op + ":" + s
		}
		out = append(out, f.column+":"+text)
	}
	return out, nil
}

// queryWhere renders filters as the query body's where (all must hold):
// one condition, or {and: [...]}. is_null is {op: is, value: null};
// not_null is {not: {op: is, value: null}}; in sends its values as a list.
func queryWhere(filters []dataFilter) any {
	conds := make([]any, 0, len(filters))
	for _, f := range filters {
		var c map[string]any
		switch f.op {
		case "is_null":
			c = map[string]any{"column": f.column, "op": "is", "value": nil}
		case "not_null":
			c = map[string]any{"not": map[string]any{"column": f.column, "op": "is", "value": nil}}
		case "in":
			c = map[string]any{"column": f.column, "op": "in", "value": f.values}
		default:
			c = map[string]any{"column": f.column, "op": f.op, "value": f.value}
		}
		conds = append(conds, c)
	}
	switch len(conds) {
	case 0:
		return nil
	case 1:
		return conds[0]
	}
	return map[string]any{"and": conds}
}

// orderText renders order as the data API takes it: column:asc,column:desc.
func orderText(order []any) (string, error) {
	parts := make([]string, 0, len(order))
	for i, o := range order {
		m, ok := o.(map[string]any)
		col, _ := m["column"].(string)
		if !ok || !columnRe.MatchString(col) {
			return "", fmt.Errorf("order[%d] needs a column", i)
		}
		dir := "asc"
		if desc, _ := m["desc"].(bool); desc {
			dir = "desc"
		}
		parts = append(parts, col+":"+dir)
	}
	return strings.Join(parts, ","), nil
}

// --- inputs ---

func fatal(format string, args ...any) error {
	return fmt.Errorf("pgdock: "+format+": %w", append(args, effects.ErrFatal)...)
}

// dataTable reads the action's table (table, or schema.table) for the path.
func dataTable(v any) (string, error) {
	t, _ := v.(string)
	if !identRe.MatchString(t) {
		return "", fatal("a table is required, as table or schema.table")
	}
	return url.PathEscape(t), nil
}

// rowsInput reads rows (1 to 1,000 objects) or row (one object).
func rowsInput(in map[string]any) ([]any, error) {
	rows, hasRows := in["rows"]
	row, hasRow := in["row"]
	switch {
	case hasRows && hasRow:
		return nil, fatal("give rows or row, not both")
	case hasRow:
		m, ok := row.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, fatal("row must be an object with at least one column")
		}
		return []any{m}, nil
	case hasRows:
		list, ok := rows.([]any)
		if !ok || len(list) == 0 || len(list) > 1000 {
			return nil, fatal("rows must be a list of 1 to 1,000 objects")
		}
		for i, r := range list {
			if m, ok := r.(map[string]any); !ok || len(m) == 0 {
				return nil, fatal("rows[%d] must be an object with at least one column", i)
			}
		}
		return list, nil
	}
	return nil, fatal("rows (a list) or row (an object) is required")
}

// returnQuery reads select and return into the query.
func returnQuery(in map[string]any, q url.Values) error {
	if sel, ok := in["select"]; ok {
		s, _ := sel.(string)
		if s == "" || strings.ContainsAny(s, "()") {
			return fatal("select names columns (a,b,c); relations are not returned from a write")
		}
		q.Set("select", s)
	}
	if r, ok := in["return"]; ok {
		s, _ := r.(string)
		if s != "representation" && s != "minimal" {
			return fatal("return is representation or minimal")
		}
		q.Set("return", s)
	}
	return nil
}

// rowTarget reads key or filters (exactly one) into the path and query of
// an update or delete, so a call that would change every row is refused
// before anything is sent, as Xenibase refuses it (filter_required).
func rowTarget(in map[string]any, table string, q url.Values) (string, error) {
	key, hasKey := in["key"]
	filters, hasFilters := in["filters"]
	if hasKey && key == nil {
		hasKey = false
	}
	if hasFilters && filters == nil {
		hasFilters = false
	}
	switch {
	case hasKey && hasFilters:
		return "", fatal("give key (one row by its primary key) or filters, not both")
	case hasKey:
		switch key.(type) {
		case string, float64, int, int64, json.Number, bool:
		default:
			return "", fatal("key is one row's primary key value (a string or a number)")
		}
		text, err := valueText(key)
		if err != nil || text == "" {
			return "", fatal("key is one row's primary key value (a string or a number)")
		}
		return "/data/v1/" + table + "/" + url.PathEscape(text), nil
	case hasFilters:
		list, ok := filters.([]any)
		if !ok || len(list) == 0 {
			return "", fatal("filters must be a non-empty list; to change every row, filter on a column every row matches")
		}
		fs, err := parseDataFilters(list)
		if err != nil {
			return "", fatal("%v", err)
		}
		where, err := whereParams(fs)
		if err != nil {
			return "", fatal("%v", err)
		}
		q["where"] = where
		return "/data/v1/" + table, nil
	}
	return "", fatal("key (one row by its primary key) or filters (a non-empty list) is required: a write to every row is refused")
}

func maxAffected(in map[string]any, q url.Values) error {
	n := intOr(in["max_affected"], 1000)
	if n < 1 || n > 100000 {
		return fatal("max_affected is 1 to 100,000")
	}
	q.Set("max_affected", strconv.Itoa(n))
	return nil
}

// --- actions ---

type writeAnswer struct {
	Affected int   `json:"affected"`
	Data     []any `json:"data"`
}

func (a writeAnswer) output(replayed bool, withRows bool) map[string]any {
	out := map[string]any{"affected": a.Affected, "replayed": replayed}
	if withRows {
		rows := a.Data
		if rows == nil {
			rows = []any{}
		}
		out["rows"] = rows
	}
	return out
}

// readRows is POST /data/v1/{table}/query: the structured body carries
// select, where, order, limit, cursor and count.
func (c *client) readRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	table, err := dataTable(in["table"])
	if err != nil {
		return connector.Response{}, err
	}
	limit := intOr(in["limit"], 100)
	if limit < 1 || limit > 1000 {
		return connector.Response{}, fatal("limit must be 1 to 1000, not %d", limit)
	}
	body := map[string]any{"limit": limit}
	if sel, _ := in["select"].(string); sel != "" {
		body["select"] = sel
	}
	if list, _ := in["filters"].([]any); len(list) > 0 {
		fs, err := parseDataFilters(list)
		if err != nil {
			return connector.Response{}, fatal("%v", err)
		}
		body["where"] = queryWhere(fs)
	}
	if order, _ := in["order"].([]any); len(order) > 0 {
		o, err := orderText(order)
		if err != nil {
			return connector.Response{}, fatal("%v", err)
		}
		body["order"] = o
	}
	if cur, _ := in["cursor"].(string); cur != "" {
		body["cursor"] = cur
	}
	count, _ := in["count"].(string)
	if count != "" {
		if count != "exact" && count != "estimated" {
			return connector.Response{}, fatal("count is exact or estimated")
		}
		body["count"] = count
	}
	var page struct {
		Data       []any  `json:"data"`
		NextCursor string `json:"next_cursor"`
		Count      *int64 `json:"count"`
	}
	if _, err := c.doData(ctx, req, http.MethodPost, "/data/v1/"+table+"/query", nil, body, &page); err != nil {
		return connector.Response{}, err
	}
	rows := page.Data
	if rows == nil {
		rows = []any{}
	}
	out := map[string]any{"rows": rows, "next_cursor": page.NextCursor}
	if count != "" {
		out["count"] = nil
		if page.Count != nil {
			out["count"] = *page.Count
		}
	}
	return connector.Response{Output: out}, nil
}

// insertRows is POST /data/v1/{table} with the rows as a list.
func (c *client) insertRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.post(ctx, req, false)
}

// upsertRows is the same POST with on_conflict and resolution.
func (c *client) upsertRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	return c.post(ctx, req, true)
}

func (c *client) post(ctx context.Context, req connector.Request, upsert bool) (connector.Response, error) {
	in := req.Input
	table, err := dataTable(in["table"])
	if err != nil {
		return connector.Response{}, err
	}
	rows, err := rowsInput(in)
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{}
	if err := returnQuery(in, q); err != nil {
		return connector.Response{}, err
	}
	if upsert {
		cols := strs(in["on_conflict"])
		if len(cols) == 0 {
			return connector.Response{}, fatal("on_conflict names the columns of a unique constraint")
		}
		for _, col := range cols {
			if !identRe.MatchString(col) || strings.Contains(col, ".") {
				return connector.Response{}, fatal("on_conflict: %q is not a column name", col)
			}
		}
		q.Set("on_conflict", strings.Join(cols, ","))
		if r, ok := in["resolution"]; ok {
			s, _ := r.(string)
			if s != "merge" && s != "ignore" {
				return connector.Response{}, fatal("resolution is merge or ignore")
			}
			q.Set("resolution", s)
		}
	}
	var ans writeAnswer
	replayed, err := c.doData(ctx, req, http.MethodPost, "/data/v1/"+table, q, rows, &ans)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: ans.output(replayed, true)}, nil
}

// updateRows is PATCH /data/v1/{table}/{key} or /data/v1/{table}?where=.
func (c *client) updateRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	table, err := dataTable(in["table"])
	if err != nil {
		return connector.Response{}, err
	}
	set, _ := in["set"].(map[string]any)
	if len(set) == 0 {
		return connector.Response{}, fatal("set is an object of the columns to change")
	}
	q := url.Values{}
	path, err := rowTarget(in, table, q)
	if err != nil {
		return connector.Response{}, err
	}
	if err := maxAffected(in, q); err != nil {
		return connector.Response{}, err
	}
	if err := returnQuery(in, q); err != nil {
		return connector.Response{}, err
	}
	var ans writeAnswer
	replayed, err := c.doData(ctx, req, http.MethodPatch, path, q, set, &ans)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: ans.output(replayed, true)}, nil
}

// deleteRows is DELETE /data/v1/{table}/{key} or /data/v1/{table}?where=,
// with return=minimal: deleted rows are not brought back into the run.
func (c *client) deleteRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	table, err := dataTable(in["table"])
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{}
	path, err := rowTarget(in, table, q)
	if err != nil {
		return connector.Response{}, err
	}
	if err := maxAffected(in, q); err != nil {
		return connector.Response{}, err
	}
	q.Set("return", "minimal")
	var ans writeAnswer
	replayed, err := c.doData(ctx, req, http.MethodDelete, path, q, nil, &ans)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: ans.output(replayed, false)}, nil
}

// callFunction is POST /data/v1/rpc/{function} with named arguments.
func (c *client) callFunction(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	fn, _ := in["function"].(string)
	if !identRe.MatchString(fn) {
		return connector.Response{}, fatal("a function is required, as function or schema.function")
	}
	args, _ := in["args"].(map[string]any)
	if args == nil {
		if in["args"] != nil {
			return connector.Response{}, fatal("args is an object of named arguments")
		}
		args = map[string]any{}
	}
	var ans struct {
		Data any `json:"data"`
	}
	replayed, err := c.doData(ctx, req, http.MethodPost, "/data/v1/rpc/"+url.PathEscape(fn), nil, args, &ans)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"data": ans.Data, "replayed": replayed}}, nil
}
