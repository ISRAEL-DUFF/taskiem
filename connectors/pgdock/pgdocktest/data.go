package pgdocktest

// The data API (Xenibase docs/backend-services.md, read 17 October 2026:
// Keys, Reading data, Writing data, Retrying writes safely:
// Idempotency-Key, Functions; docs/errors.md, the Data API table), served
// under /edge/{ref}/data/v1 of the same server with the project's secret
// key in the apikey header. Rows are typed JSON. Writes go through the
// same transaction path as Tx, so webhooks fire and the outbox keeps its
// order. The idempotency semantics are the documented ones: the first
// answer is kept per key and request for the fake's lifetime, a repeat
// gets it with Idempotent-Replayed: true, the same key with a different
// request gets 422 idempotency_key_reused, and a refused write records no
// key.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type dataErrBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id"`
}

// dataErr is the data API's error shape.
type dataErr struct {
	Error dataErrBody `json:"error"`
}

type idemEntry struct {
	hash   string
	status int
	header http.Header
	body   []byte
}

func refuseData(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	reply(w, status, dataErr{Error: dataErrBody{Code: code, Message: msg, Details: details, RequestID: "req_" + newID()[:8]}})
}

// serveData is the data API: the project by its reference, the key, the
// idempotency check, then the operation.
func (f *Fake) serveData(w http.ResponseWriter, r *http.Request) {
	seg := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // edge, ref, data, v1, …
	f.mu.Lock()
	defer f.mu.Unlock()
	var p *project
	for _, pr := range f.projects {
		if len(seg) > 1 && pr.Ref == seg[1] {
			p = pr
		}
	}
	if p == nil || !p.Services {
		refuseData(w, http.StatusNotFound, "unknown_host", "no project is served at this hostname", nil)
		return
	}
	key := r.Header.Get("apikey")
	switch {
	case key == "":
		refuseData(w, http.StatusUnauthorized, "key_required", "send the project's API key in the apikey header", nil)
		return
	case !slices.Contains(p.SecretKeys, key):
		refuseData(w, http.StatusUnauthorized, "invalid_key", "the API key is not one of this project's", nil)
		return
	}
	if len(seg) < 5 || seg[2] != "data" || seg[3] != "v1" {
		refuseData(w, http.StatusNotFound, "not_found", "no such route", nil)
		return
	}
	body, _ := io.ReadAll(r.Body)
	write := r.Method != http.MethodGet
	idem := r.Header.Get("Idempotency-Key")
	var idemKey, hash string
	if write && idem != "" {
		if len(idem) > 255 || strings.ContainsFunc(idem, func(c rune) bool { return c <= ' ' || c > '~' }) {
			refuseData(w, http.StatusBadRequest, "invalid_idempotency_key", "the key is 1 to 255 printable characters without spaces", nil)
			return
		}
		sum := sha256.Sum256([]byte(r.Method + "\x00" + r.URL.Path + "\x00" + r.URL.RawQuery + "\x00" + string(body)))
		hash = hex.EncodeToString(sum[:])
		idemKey = p.ID + "\x00" + key + "\x00" + idem
		if e, ok := f.idem[idemKey]; ok {
			if e.hash != hash {
				refuseData(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "the key was used in the last 24 hours for a different request", nil)
				return
			}
			for k, v := range e.header {
				w.Header()[k] = v
			}
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(e.status)
			_, _ = w.Write(e.body)
			return
		}
	}
	rec := httptest.NewRecorder()
	f.serveDataLocked(rec, r, p, seg[4:], body)
	if idemKey != "" && rec.Code >= 200 && rec.Code < 300 {
		f.idem[idemKey] = idemEntry{hash: hash, status: rec.Code, header: rec.Header().Clone(), body: rec.Body.Bytes()}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (f *Fake) serveDataLocked(w http.ResponseWriter, r *http.Request, p *project, rest []string, body []byte) {
	switch {
	case rest[0] == "rpc" && len(rest) == 2:
		f.serveRPC(w, r, p, rest[1], body)
	case rest[0] == "batch":
		refuseData(w, http.StatusNotFound, "not_found", "the fake has no batch endpoint", nil)
	case len(rest) == 1 || len(rest) == 2:
		t := p.Tables[qualify(rest[0])]
		if t == nil {
			refuseData(w, http.StatusNotFound, "unknown_table", "no table "+rest[0], nil)
			return
		}
		f.serveTable(w, r, p, rest[0], t, rest[1:], body)
	default:
		refuseData(w, http.StatusNotFound, "not_found", "no such route", nil)
	}
}

// --- conditions ---

// cond is a where condition: one comparison, or and, or, not.
type cond struct {
	column, op string
	value      any
	and, or    []cond
	not        *cond
}

var dataOps = []string{"eq", "neq", "lt", "lte", "gt", "gte", "in", "like", "ilike", "is", "contains", "contained_by", "search"}

// parseWhere reads column:op:value.
func parseWhere(s string) (cond, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return cond{}, fmt.Errorf("a condition is column:operator:value, not %q", s)
	}
	c := cond{column: parts[0], op: parts[1]}
	val := ""
	if len(parts) == 3 {
		val = parts[2]
	}
	switch c.op {
	case "in":
		c.value = splitList(val)
	case "is":
		switch val {
		case "null":
			c.value = nil
		case "true":
			c.value = true
		case "false":
			c.value = false
		default:
			return cond{}, fmt.Errorf("is takes null, true or false, not %q", val)
		}
	default:
		c.value = val
	}
	return c, nil
}

// splitList reads a,b,"c,d": comma-separated, double quotes around a
// value holding a comma or a quote, a backslash escaping a quote inside.
func splitList(s string) []any {
	var out []any
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote && ch == '\\' && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
		case ch == '"':
			inQuote = !inQuote
		case ch == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(out, cur.String())
}

// parseCond reads the query body's where: {column, op, value}, {and},
// {or}, {not}.
func parseCond(raw json.RawMessage) (cond, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return cond{}, fmt.Errorf("where must be an object")
	}
	var c cond
	switch {
	case m["and"] != nil || m["or"] != nil:
		key := "and"
		if m["or"] != nil {
			key = "or"
		}
		var list []json.RawMessage
		if err := json.Unmarshal(m[key], &list); err != nil {
			return cond{}, fmt.Errorf("%s must be a list", key)
		}
		for _, item := range list {
			sub, err := parseCond(item)
			if err != nil {
				return cond{}, err
			}
			if key == "and" {
				c.and = append(c.and, sub)
			} else {
				c.or = append(c.or, sub)
			}
		}
		if key == "and" && c.and == nil {
			c.and = []cond{}
		}
		if key == "or" && c.or == nil {
			c.or = []cond{}
		}
	case m["not"] != nil:
		sub, err := parseCond(m["not"])
		if err != nil {
			return cond{}, err
		}
		c.not = &sub
	default:
		_ = json.Unmarshal(m["column"], &c.column)
		_ = json.Unmarshal(m["op"], &c.op)
		if c.column == "" || c.op == "" {
			return cond{}, fmt.Errorf("a condition needs column and op")
		}
		if m["value"] != nil {
			dec := json.NewDecoder(strings.NewReader(string(m["value"])))
			dec.UseNumber()
			_ = dec.Decode(&c.value)
		}
		if c.op == "in" {
			if _, ok := c.value.([]any); !ok {
				return cond{}, fmt.Errorf("in takes a list")
			}
		}
	}
	return c, nil
}

// check refuses unknown columns and operators.
func (c cond) check(t *Table) (code, msg string) {
	for _, sub := range c.and {
		if code, msg = sub.check(t); code != "" {
			return code, msg
		}
	}
	for _, sub := range c.or {
		if code, msg = sub.check(t); code != "" {
			return code, msg
		}
	}
	if c.not != nil {
		return c.not.check(t)
	}
	if c.column == "" {
		return "", ""
	}
	if !slices.Contains(t.Columns, c.column) {
		return "unknown_column", "no column " + c.column
	}
	if !slices.Contains(dataOps, c.op) {
		return "unsupported_operator", "no operator " + c.op
	}
	return "", ""
}

func (c cond) match(row map[string]any) bool {
	switch {
	case c.and != nil:
		for _, sub := range c.and {
			if !sub.match(row) {
				return false
			}
		}
		return true
	case c.or != nil:
		for _, sub := range c.or {
			if sub.match(row) {
				return true
			}
		}
		return false
	case c.not != nil:
		return !c.not.match(row)
	}
	v, isNull := row[c.column], row[c.column] == nil
	s := fmt.Sprint(v)
	switch c.op {
	case "is":
		if c.value == nil {
			return isNull
		}
		return !isNull && s == fmt.Sprint(c.value)
	case "in":
		list, _ := c.value.([]any)
		for _, x := range list {
			if !isNull && s == fmt.Sprint(x) {
				return true
			}
		}
		return false
	case "like", "ilike":
		if isNull {
			return false
		}
		pat := likePattern(fmt.Sprint(c.value))
		if c.op == "ilike" {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		return err == nil && re.MatchString(s)
	case "search":
		return !isNull && strings.Contains(strings.ToLower(s), strings.ToLower(fmt.Sprint(c.value)))
	case "contains":
		if isNull {
			return false
		}
		if list, ok := v.([]any); ok {
			want, _ := c.value.([]any)
			for _, x := range want {
				if !slices.ContainsFunc(list, func(y any) bool { return fmt.Sprint(x) == fmt.Sprint(y) }) {
					return false
				}
			}
			return true
		}
		return strings.Contains(s, fmt.Sprint(c.value))
	case "contained_by":
		list, ok := v.([]any)
		want, _ := c.value.([]any)
		if !ok {
			return false
		}
		for _, x := range list {
			if !slices.ContainsFunc(want, func(y any) bool { return fmt.Sprint(x) == fmt.Sprint(y) }) {
				return false
			}
		}
		return true
	}
	return filter{Op: c.op, Value: c.value}.match(v)
}

// likePattern turns a LIKE pattern (% any run, _ one character) into a
// regular expression.
func likePattern(p string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range p {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

// --- reads ---

type ord struct {
	col  string
	desc bool
}

func parseOrder(s string, t *Table) ([]ord, string) {
	var out []ord
	if s != "" {
		for _, part := range strings.Split(s, ",") {
			col, dir, _ := strings.Cut(part, ":")
			if !slices.Contains(t.Columns, col) || dir != "" && dir != "asc" && dir != "desc" {
				return nil, part
			}
			out = append(out, ord{col: col, desc: dir == "desc"})
		}
	}
	for _, k := range t.Key {
		out = append(out, ord{col: k})
	}
	return out, ""
}

func sortRows(rows []map[string]any, order []ord) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, o := range order {
			a, b := fmt.Sprint(rows[i][o.col]), fmt.Sprint(rows[j][o.col])
			x, ex := strconv.ParseFloat(a, 64)
			y, ey := strconv.ParseFloat(b, 64)
			c := strings.Compare(a, b)
			if ex == nil && ey == nil {
				c = 0
				if x < y {
					c = -1
				} else if x > y {
					c = 1
				}
			}
			if c != 0 {
				return (c < 0) != o.desc
			}
		}
		return false
	})
}

func (t *Table) keyOf(row map[string]any) string {
	vals := make([]any, len(t.Key))
	for i, k := range t.Key {
		vals[i] = fmt.Sprint(row[k])
	}
	raw, _ := json.Marshal(vals)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// pick keeps the selected columns of a row.
func pick(row map[string]any, sel []string) map[string]any {
	if sel == nil {
		return clone(row)
	}
	out := map[string]any{}
	for _, c := range sel {
		out[c] = row[c]
	}
	return out
}

func parseSelect(s string, t *Table) ([]string, bool) {
	if s == "" || s == "*" {
		return nil, true
	}
	var out []string
	for _, c := range strings.Split(s, ",") {
		if !slices.Contains(t.Columns, c) {
			return nil, false
		}
		out = append(out, c)
	}
	return out, true
}

// read is a page of the rows where matches.
type read struct {
	where  *cond
	order  string
	sel    string
	limit  int
	offset int
	cursor string
	count  string
}

func (f *Fake) serveRead(w http.ResponseWriter, t *Table, rd read) {
	sel, ok := parseSelect(rd.sel, t)
	if !ok {
		refuseData(w, http.StatusBadRequest, "invalid_select", "select names columns", nil)
		return
	}
	if rd.where != nil {
		if code, msg := rd.where.check(t); code != "" {
			refuseData(w, http.StatusBadRequest, code, msg, nil)
			return
		}
	}
	order, bad := parseOrder(rd.order, t)
	if bad != "" {
		refuseData(w, http.StatusBadRequest, "invalid_order", "bad order "+bad, nil)
		return
	}
	if rd.limit < 1 || rd.limit > 1000 {
		refuseData(w, http.StatusBadRequest, "invalid_limit", "limit is 1 to 1000", nil)
		return
	}
	if rd.count != "" && rd.count != "exact" && rd.count != "estimated" {
		refuseData(w, http.StatusBadRequest, "invalid_count", "count is exact or estimated", nil)
		return
	}
	var rows []map[string]any
	for _, row := range t.rows {
		if rd.where == nil || rd.where.match(row) {
			rows = append(rows, row)
		}
	}
	total := len(rows)
	sortRows(rows, order)
	if rd.cursor != "" {
		found := false
		for i, row := range rows {
			if t.keyOf(row) == rd.cursor {
				rows, found = rows[i+1:], true
				break
			}
		}
		if !found {
			refuseData(w, http.StatusBadRequest, "invalid_cursor", "the cursor is not one of this query's", nil)
			return
		}
	}
	if rd.offset > 0 {
		if rd.offset > len(rows) {
			rd.offset = len(rows)
		}
		rows = rows[rd.offset:]
	}
	out := map[string]any{}
	if len(rows) > rd.limit {
		out["next_cursor"] = t.keyOf(rows[rd.limit-1])
		rows = rows[:rd.limit]
	}
	data := []any{}
	for _, row := range rows {
		data = append(data, pick(row, sel))
	}
	out["data"] = data
	if rd.count != "" {
		out["count"] = total
	}
	reply(w, http.StatusOK, out)
}

// --- the table routes ---

func (f *Fake) serveTable(w http.ResponseWriter, r *http.Request, p *project, name string, t *Table, rest []string, body []byte) {
	q := r.URL.Query()
	whereFromQuery := func() (*cond, bool) {
		var all []cond
		for _, s := range q["where"] {
			c, err := parseWhere(s)
			if err != nil {
				refuseData(w, http.StatusBadRequest, "invalid_filter", err.Error(), nil)
				return nil, false
			}
			all = append(all, c)
		}
		for _, group := range q["or"] {
			var either []cond
			for _, s := range strings.Split(group, ",") {
				c, err := parseWhere(s)
				if err != nil {
					refuseData(w, http.StatusBadRequest, "invalid_filter", err.Error(), nil)
					return nil, false
				}
				either = append(either, c)
			}
			all = append(all, cond{or: either})
		}
		if len(all) == 0 {
			return nil, true
		}
		return &cond{and: all}, true
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		where, ok := whereFromQuery()
		if !ok {
			return
		}
		limit := 100
		if l := q.Get("limit"); l != "" {
			limit, _ = strconv.Atoi(l)
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		f.serveRead(w, t, read{where: where, order: q.Get("order"), sel: q.Get("select"), limit: limit, offset: offset, cursor: q.Get("cursor"), count: q.Get("count")})
	case len(rest) == 1 && rest[0] == "query" && r.Method == http.MethodPost:
		var req struct {
			Select string          `json:"select"`
			Where  json.RawMessage `json:"where"`
			Order  string          `json:"order"`
			Limit  int             `json:"limit"`
			Offset int             `json:"offset"`
			Cursor string          `json:"cursor"`
			Count  string          `json:"count"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			refuseData(w, http.StatusBadRequest, "invalid_body", err.Error(), nil)
			return
		}
		rd := read{order: req.Order, sel: req.Select, limit: req.Limit, offset: req.Offset, cursor: req.Cursor, count: req.Count}
		if rd.limit == 0 {
			rd.limit = 100
		}
		if len(req.Where) > 0 && string(req.Where) != "null" {
			c, err := parseCond(req.Where)
			if err != nil {
				refuseData(w, http.StatusBadRequest, "invalid_filter", err.Error(), nil)
				return
			}
			rd.where = &c
		}
		f.serveRead(w, t, rd)
	case len(rest) == 1 && r.Method == http.MethodGet:
		row := t.byKey(rest[0])
		if row == nil {
			refuseData(w, http.StatusNotFound, "not_found", "no row "+rest[0], nil)
			return
		}
		sel, ok := parseSelect(q.Get("select"), t)
		if !ok {
			refuseData(w, http.StatusBadRequest, "invalid_select", "select names columns", nil)
			return
		}
		reply(w, http.StatusOK, map[string]any{"data": pick(row, sel)})
	case len(rest) == 0 && r.Method == http.MethodPost:
		f.serveInsert(w, q, p, name, t, body)
	case r.Method == http.MethodPatch || r.Method == http.MethodDelete:
		var where *cond
		if len(rest) == 1 {
			if len(t.Key) != 1 {
				refuseData(w, http.StatusBadRequest, "no_single_key", "the table has no single-column primary key", nil)
				return
			}
			where = &cond{column: t.Key[0], op: "eq", value: rest[0]}
		} else {
			var ok bool
			if where, ok = whereFromQuery(); !ok {
				return
			}
			if where == nil {
				refuseData(w, http.StatusBadRequest, "filter_required", "an update or delete needs a filter or a key", nil)
				return
			}
		}
		f.serveChange(w, r, q, p, name, t, where, body)
	default:
		refuseData(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" is not allowed here", nil)
	}
}

func (t *Table) byKey(key string) map[string]any {
	if len(t.Key) != 1 {
		return nil
	}
	for _, row := range t.rows {
		if fmt.Sprint(row[t.Key[0]]) == key {
			return row
		}
	}
	return nil
}

// writeParams reads return, select and max_affected.
type writeParams struct {
	minimal     bool
	sel         []string
	maxAffected int
}

func parseWriteParams(w http.ResponseWriter, q url.Values, t *Table) (writeParams, bool) {
	wp := writeParams{maxAffected: 1000}
	switch q.Get("return") {
	case "", "representation":
	case "minimal":
		wp.minimal = true
	default:
		refuseData(w, http.StatusBadRequest, "invalid_return", "return is representation or minimal", nil)
		return wp, false
	}
	var ok bool
	if wp.sel, ok = parseSelect(q.Get("select"), t); !ok {
		refuseData(w, http.StatusBadRequest, "invalid_select", "select names columns", nil)
		return wp, false
	}
	if m := q.Get("max_affected"); m != "" {
		n, err := strconv.Atoi(m)
		if err != nil || n < 1 || n > 100000 {
			refuseData(w, http.StatusBadRequest, "invalid_max_affected", "max_affected is 1 to 100000", nil)
			return wp, false
		}
		wp.maxAffected = n
	}
	return wp, true
}

func decodeRows(body []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}, nil
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("every row must be an object")
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, fmt.Errorf("the body is an object or a list of objects")
}

func (t *Table) checkColumns(w http.ResponseWriter, row map[string]any) bool {
	for c := range row {
		if !slices.Contains(t.Columns, c) {
			refuseData(w, http.StatusBadRequest, "unknown_column", "no column "+c, nil)
			return false
		}
	}
	return true
}

func (t *Table) pk(row map[string]any) map[string]any {
	k := map[string]any{}
	for _, c := range t.Key {
		k[c] = row[c]
	}
	return k
}

func (f *Fake) answer(w http.ResponseWriter, status int, t *Table, wp writeParams, rows []map[string]any, affected int) {
	if wp.minimal {
		reply(w, status, map[string]any{"affected": affected})
		return
	}
	data := []any{}
	for _, row := range rows {
		data = append(data, pick(row, wp.sel))
	}
	reply(w, status, map[string]any{"affected": affected, "data": data})
}

// serveInsert is POST /data/v1/{table}: insert, or upsert with on_conflict.
func (f *Fake) serveInsert(w http.ResponseWriter, q url.Values, p *project, name string, t *Table, body []byte) {
	wp, ok := parseWriteParams(w, q, t)
	if !ok {
		return
	}
	rows, err := decodeRows(body)
	if err != nil || len(rows) == 0 || len(rows) > 1000 {
		refuseData(w, http.StatusBadRequest, "invalid_body", "one row or a list of 1 to 1,000", nil)
		return
	}
	var conflictCols []string
	if oc := q.Get("on_conflict"); oc != "" {
		conflictCols = strings.Split(oc, ",")
		for _, c := range conflictCols {
			if !slices.Contains(t.Columns, c) {
				refuseData(w, http.StatusBadRequest, "unknown_column", "no column "+c, nil)
				return
			}
		}
	}
	resolution := q.Get("resolution")
	if resolution != "" && resolution != "merge" && resolution != "ignore" {
		refuseData(w, http.StatusBadRequest, "invalid_resolution", "resolution is merge or ignore", nil)
		return
	}
	tx := f.Begin(p.ID)
	var written []map[string]any
	seq := t.seq
	for _, row := range rows {
		if !t.checkColumns(w, row) {
			return
		}
		if len(conflictCols) > 0 {
			match := map[string]any{}
			for _, c := range conflictCols {
				match[c] = row[c]
			}
			if i := t.find(match); i >= 0 {
				if resolution == "ignore" {
					continue
				}
				tx.Update(name, t.pk(t.rows[i]), row)
				merged := clone(t.rows[i])
				for k, v := range row {
					merged[k] = v
				}
				written = append(written, merged)
				continue
			}
		}
		row = clone(row)
		if len(t.Key) == 1 {
			if _, has := row[t.Key[0]]; !has {
				seq = nextSeq(t, written, seq)
				row[t.Key[0]] = seq
			} else if t.find(t.pk(row)) >= 0 || slices.ContainsFunc(written, func(r map[string]any) bool { return fmt.Sprint(r[t.Key[0]]) == fmt.Sprint(row[t.Key[0]]) }) {
				refuseData(w, http.StatusConflict, "unique_violation", "duplicate key value violates unique constraint", map[string]any{"pg_code": "23505", "table": name})
				return
			}
		}
		tx.Insert(name, row)
		written = append(written, row)
	}
	t.seq = seq
	tx.commitLocked()
	f.answer(w, http.StatusCreated, t, wp, written, len(written))
}

// nextSeq is the next identity value: above every key present, in the
// table and in the rows this insert already holds.
func nextSeq(t *Table, pending []map[string]any, seq int64) int64 {
	for _, rows := range [][]map[string]any{t.rows, pending} {
		for _, row := range rows {
			if n, err := strconv.ParseInt(fmt.Sprint(row[t.Key[0]]), 10, 64); err == nil && n > seq {
				seq = n
			}
		}
	}
	return seq + 1
}

// serveChange is PATCH or DELETE of the rows where matches.
func (f *Fake) serveChange(w http.ResponseWriter, r *http.Request, q url.Values, p *project, name string, t *Table, where *cond, body []byte) {
	wp, ok := parseWriteParams(w, q, t)
	if !ok {
		return
	}
	if code, msg := where.check(t); code != "" {
		refuseData(w, http.StatusBadRequest, code, msg, nil)
		return
	}
	var set map[string]any
	if r.Method == http.MethodPatch {
		rows, err := decodeRows(body)
		if err != nil || len(rows) != 1 || len(rows[0]) == 0 {
			refuseData(w, http.StatusBadRequest, "invalid_body", "the body is an object of the columns to set", nil)
			return
		}
		set = rows[0]
		if !t.checkColumns(w, set) {
			return
		}
	}
	var matched []map[string]any
	for _, row := range t.rows {
		if where.match(row) {
			matched = append(matched, row)
		}
	}
	if len(matched) > wp.maxAffected {
		refuseData(w, http.StatusBadRequest, "too_many_rows", fmt.Sprintf("%d rows would change, over max_affected %d; nothing changed", len(matched), wp.maxAffected), nil)
		return
	}
	tx := f.Begin(p.ID)
	var out []map[string]any
	for _, row := range matched {
		if r.Method == http.MethodPatch {
			tx.Update(name, t.pk(row), set)
			merged := clone(row)
			for k, v := range set {
				merged[k] = v
			}
			out = append(out, merged)
		} else {
			tx.Delete(name, t.pk(row))
			out = append(out, clone(row))
		}
	}
	tx.commitLocked()
	f.answer(w, http.StatusOK, t, wp, out, len(matched))
}

// serveRPC is POST /data/v1/rpc/{function} with named arguments; GET is
// refused as the fake's functions are volatile.
func (f *Fake) serveRPC(w http.ResponseWriter, r *http.Request, p *project, name string, body []byte) {
	fn := p.Functions[name]
	if fn == nil {
		refuseData(w, http.StatusNotFound, "unknown_function", "no function "+name, nil)
		return
	}
	if r.Method != http.MethodPost {
		refuseData(w, http.StatusMethodNotAllowed, "volatile_function", "a function that writes needs POST", nil)
		return
	}
	args := map[string]any{}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &args); err != nil {
			refuseData(w, http.StatusBadRequest, "invalid_body", "arguments are a JSON object", nil)
			return
		}
	}
	// The function runs without the lock: it may read the fake's tables.
	f.mu.Unlock()
	out, err := fn(args)
	f.mu.Lock()
	if err != nil {
		refuseData(w, http.StatusBadRequest, "database_error", err.Error(), map[string]any{"pg_code": "P0001"})
		return
	}
	reply(w, http.StatusOK, map[string]any{"data": out})
}
