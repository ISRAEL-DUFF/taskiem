package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
)

// Rows go through the connector's actions on the data_tables connection,
// as a workflow step's would: the API checks values against the table's
// columns first, sends an Idempotency-Key with every write (the
// request's own, or a fresh one), and never puts a row in a log or an
// audit entry.

const (
	// importMaxRows is the most rows one CSV import takes.
	importMaxRows = 5000
	// importBatch is how many rows go to Xenibase in one write.
	importBatch = 500
	// exportMaxRows is the most rows one export writes.
	exportMaxRows = 100000
	// exportPage is the page size an export reads with.
	exportPage = 1000
	// maxImportBytes bounds the CSV file.
	maxImportBytes = 16 << 20
)

// keyCharset is what the data API takes as an Idempotency-Key, as the
// connector's manifest limits it.
var keyCharset = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// idempotencyKey is the request's Idempotency-Key, or a fresh one.
func idempotencyKey(r *http.Request) (string, error) {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" {
		return "api-" + uuid.Must(uuid.NewV7()).String(), nil
	}
	if !keyCharset.MatchString(k) {
		return "", fmt.Errorf("%w: Idempotency-Key is 1 to 255 letters, digits, dashes and underscores", errBadRequest)
	}
	return k, nil
}

// tableRows is the table in the path with the connector ready to act on
// its rows.
type tableRows struct {
	table datatables.Table
	conn  *connector.Connector
	creds map[string]string
	hc    *http.Client
}

func (s *Server) tableRows(r *http.Request) (tableRows, error) {
	t, err := s.loadTable(r)
	if err != nil {
		return tableRows{}, err
	}
	return s.tableRowsFor(r.Context(), principalFrom(r.Context()).TenantID, t, tablesPurpose)
}

// tableRowsFor is a table with the connector ready to act on its rows,
// the connection read recorded under purpose (the API's, or the chat's).
func (s *Server) tableRowsFor(ctx context.Context, tenant uuid.UUID, t datatables.Table, purpose string) (tableRows, error) {
	conn, creds, hc, err := s.pgdockConnection(ctx, tenant, datatables.ConnectionName, purpose)
	if err != nil {
		return tableRows{}, err
	}
	return tableRows{table: t, conn: conn, creds: creds, hc: hc}, nil
}

// call runs one connector action on the table's connection.
func (tr tableRows) call(ctx context.Context, s *Server, action string, in map[string]any, key string) (map[string]any, error) {
	req := connector.Request{Input: in, Credentials: tr.creds, HTTP: tr.hc, Logger: s.Logger, Attempt: 1}
	if key != "" {
		req.IdempotencyKey, req.KeyFirstSent = key, time.Now()
	}
	resp, err := tr.conn.Actions[action].Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// known reports whether a column may be filtered or ordered on.
func (tr tableRows) known(col string) bool { return knownColumn(tr.table.Columns, col) }

func knownColumn(cols []datatables.Column, col string) bool {
	if col == "id" || col == "created_at" || col == "updated_at" {
		return true
	}
	for _, c := range cols {
		if c.Name == col {
			return true
		}
	}
	return false
}

// searchFilter is the read_rows filter for a search q: column:text on
// one of the table's columns, or text alone matched on the table's first
// text column; false when there is no column to search.
func searchFilter(cols []datatables.Column, q string) (map[string]any, bool) {
	col, text, ok := strings.Cut(q, ":")
	if !ok || !knownColumn(cols, col) {
		col, text = "", q
		for _, c := range cols {
			if c.Type == "text" {
				col = c.Name
				break
			}
		}
	}
	if col == "" {
		return nil, false
	}
	return map[string]any{"column": col, "op": "ilike", "value": "%" + strings.NewReplacer("%", `\%`, "_", `\_`).Replace(strings.TrimSpace(text)) + "%"}, true
}

// pageLimit reads limit (1 to 1,000; default 50).
func pageLimit(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 1000 {
		return 0, fmt.Errorf("%w: limit is 1 to 1000", errBadRequest)
	}
	return n, nil
}

// listTableRows is a page of rows, newest first: ?limit, ?cursor and ?q,
// a search as column:text (the column must be text, phone or email) or
// text alone, matched on the table's first text column.
func (s *Server) listTableRows(w http.ResponseWriter, r *http.Request) {
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	limit, err := pageLimit(r.URL.Query().Get("limit"), 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in := map[string]any{"table": tr.table.Name, "limit": limit, "order": []any{map[string]any{"column": "id", "desc": true}}}
	if cur := r.URL.Query().Get("cursor"); cur != "" {
		in["cursor"] = cur
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		f, ok := searchFilter(tr.table.Columns, q)
		if !ok {
			writeErr(w, http.StatusBadRequest, "q searches a text column: give it as column:text")
			return
		}
		in["filters"] = []any{f}
	}
	out, err := tr.call(r.Context(), s, "read_rows", in, "")
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rowsOf(out), "next_cursor": out["next_cursor"]})
}

func rowsOf(out map[string]any) []any {
	rows, _ := out["rows"].([]any)
	if rows == nil {
		rows = []any{}
	}
	return rows
}

type rowsQuery struct {
	Filters []json.RawMessage `json:"filters"`
	Order   []json.RawMessage `json:"order"`
	Limit   int               `json:"limit"`
	Cursor  string            `json:"cursor"`
	Count   string            `json:"count"`
}

// queryTableRows is a page of rows by the connector's structured
// filters and order (read_rows), so a client can do what a workflow
// step can.
func (s *Server) queryTableRows(w http.ResponseWriter, r *http.Request) {
	var req rowsQuery
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	in := map[string]any{"table": tr.table.Name}
	in["limit"] = 50
	if req.Limit != 0 {
		if req.Limit < 1 || req.Limit > 1000 {
			writeErr(w, http.StatusBadRequest, "limit is 1 to 1000")
			return
		}
		in["limit"] = req.Limit
	}
	named := func(list []json.RawMessage, what string) ([]any, error) {
		out := make([]any, 0, len(list))
		for i, raw := range list {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("%w: %s[%d] is not an object", errBadRequest, what, i)
			}
			col, _ := m["column"].(string)
			if !tr.known(col) {
				return nil, fmt.Errorf("%w: %s[%d]: the table has no column %q", errBadRequest, what, i, col)
			}
			out = append(out, m)
		}
		return out, nil
	}
	if len(req.Filters) > 0 {
		f, err := named(req.Filters, "filters")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in["filters"] = f
	}
	if len(req.Order) > 0 {
		o, err := named(req.Order, "order")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in["order"] = o
	} else {
		in["order"] = []any{map[string]any{"column": "id", "desc": true}}
	}
	if req.Cursor != "" {
		in["cursor"] = req.Cursor
	}
	if req.Count != "" {
		if req.Count != "exact" && req.Count != "estimated" {
			writeErr(w, http.StatusBadRequest, "count is exact or estimated")
			return
		}
		in["count"] = req.Count
	}
	out, err := tr.call(r.Context(), s, "read_rows", in, "")
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	ans := map[string]any{"rows": rowsOf(out), "next_cursor": out["next_cursor"]}
	if req.Count != "" {
		ans["count"] = out["count"]
	}
	writeJSON(w, http.StatusOK, ans)
}

type rowsReq struct {
	Rows []map[string]any `json:"rows"`
	Row  map[string]any   `json:"row"`
}

// insertTableRows inserts one row or up to 1,000, checked against the
// columns, under the request's Idempotency-Key.
func (s *Server) insertTableRows(w http.ResponseWriter, r *http.Request) {
	var req rowsReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := req.Rows
	if req.Row != nil {
		if rows != nil {
			writeErr(w, http.StatusBadRequest, "give rows or row, not both")
			return
		}
		rows = []map[string]any{req.Row}
	}
	if len(rows) == 0 || len(rows) > 1000 {
		writeErr(w, http.StatusBadRequest, "rows is a list of 1 to 1,000 objects, or row one object")
		return
	}
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	checked := make([]any, 0, len(rows))
	for i, row := range rows {
		x, err := datatables.CheckRow(tr.table.Columns, row, true, false)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("rows[%d]: %s", i, strings.TrimPrefix(err.Error(), "invalid: ")))
			return
		}
		checked = append(checked, x)
	}
	out, err := tr.call(r.Context(), s, "insert_rows", map[string]any{"table": tr.table.Name, "rows": checked}, key)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"affected": out["affected"], "rows": rowsOf(out), "replayed": out["replayed"] == true})
}

// rowKey reads the row id in the path.
func rowKey(r *http.Request) (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, "row"), 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("row: %w", datatables.ErrNotFound)
	}
	return n, nil
}

// updateTableRow sets columns on one row by its id: {"set": {...}}.
func (s *Server) updateTableRow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Set map[string]any `json:"set"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := rowKey(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	set, err := datatables.CheckRow(tr.table.Columns, req.Set, false, false)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "update_rows", map[string]any{"table": tr.table.Name, "key": id, "set": set, "max_affected": 1}, key)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	rows := rowsOf(out)
	if n, _ := out["affected"].(int); n == 0 && len(rows) == 0 {
		writeErr(w, http.StatusNotFound, "no row "+strconv.FormatInt(id, 10))
		return
	}
	var row any
	if len(rows) > 0 {
		row = rows[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": out["affected"], "row": row, "replayed": out["replayed"] == true})
}

func (s *Server) deleteTableRow(w http.ResponseWriter, r *http.Request) {
	key, err := idempotencyKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := rowKey(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "delete_rows", map[string]any{"table": tr.table.Name, "key": id, "max_affected": 1}, key)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if n, _ := out["affected"].(int); n == 0 {
		writeErr(w, http.StatusNotFound, "no row "+strconv.FormatInt(id, 10))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": out["affected"], "replayed": out["replayed"] == true})
}

// --- CSV ---

// importTableRows takes a CSV file (multipart field file) whose header
// names columns, up to 5,000 rows, in batches of 500 upserted on id when
// the file has an id column and inserted otherwise, each batch under its
// own key from the import id and the batch's index, so a retried import
// writes nothing twice.
func (s *Server) importTableRows(w http.ResponseWriter, r *http.Request) {
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		writeErr(w, http.StatusBadRequest, "send the CSV as a multipart form with a file field (at most 16 MB)")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the form has no file field")
		return
	}
	defer func() { _ = f.Close() }()
	rows, hasID, err := readCSV(f, tr.table.Columns)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	importID := uuid.Must(uuid.NewV7()).String()
	action := "insert_rows"
	imported, replayed, batches := 0, 0, 0
	for start := 0; start < len(rows); start += importBatch {
		end := min(start+importBatch, len(rows))
		in := map[string]any{"table": tr.table.Name, "rows": rows[start:end], "return": "minimal"}
		if hasID {
			action = "upsert_rows"
			in["on_conflict"] = []any{"id"}
		}
		out, err := tr.call(r.Context(), s, action, in, "imp-"+importID+"-"+strconv.Itoa(batches))
		if err != nil {
			s.failTables(w, r, fmt.Errorf("batch %d (rows %d to %d): %w", batches+1, start+1, end, err))
			return
		}
		batches++
		if n, ok := out["affected"].(int); ok {
			imported += n
		} else {
			imported += end - start
		}
		if out["replayed"] == true {
			replayed++
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "table.import", tr.table.Name, map[string]any{"table_id": tr.table.ID, "import_id": importID, "rows": len(rows), "imported": imported, "batches": batches, "upsert": hasID})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"import_id": importID, "rows": len(rows), "imported": imported, "batches": batches, "replayed_batches": replayed, "mode": action})
}

// readCSV parses a CSV whose header row names the table's columns (id
// allowed; created_at and updated_at ignored) into checked rows.
func readCSV(rd io.Reader, cols []datatables.Column) (rows []any, hasID bool, err error) {
	cr := csv.NewReader(rd)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, false, fmt.Errorf("%w: the file has no header row", errBadRequest)
	}
	names := make([]string, len(header))
	for i, h := range header {
		names[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))
	}
	known := map[string]bool{"id": true, "created_at": true, "updated_at": true}
	for _, c := range cols {
		known[c.Name] = true
	}
	for _, n := range names {
		if !known[n] {
			return nil, false, fmt.Errorf("%w: the table has no column %q (the header row names columns)", errBadRequest, n)
		}
		hasID = hasID || n == "id"
	}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("%w: line %d: %w", errBadRequest, line, err)
		}
		if len(rows) >= importMaxRows {
			return nil, false, fmt.Errorf("%w: at most %d rows per import; split the file", errBadRequest, importMaxRows)
		}
		raw := map[string]any{}
		empty := true
		for i, v := range rec {
			if i >= len(names) {
				return nil, false, fmt.Errorf("%w: line %d has more cells than the header", errBadRequest, line)
			}
			if names[i] == "created_at" || names[i] == "updated_at" {
				continue
			}
			if strings.TrimSpace(v) != "" {
				empty = false
			}
			raw[names[i]] = v
		}
		if empty {
			continue
		}
		row, err := datatables.CheckRow(cols, raw, !hasID, hasID)
		if err != nil {
			return nil, false, fmt.Errorf("%w: line %d: %s", errBadRequest, line, strings.TrimPrefix(err.Error(), "invalid: "))
		}
		if hasID {
			if _, ok := row["id"]; !ok {
				return nil, false, fmt.Errorf("%w: line %d: id is empty; leave the id column out to insert new rows", errBadRequest, line)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, false, fmt.Errorf("%w: the file has no rows", errBadRequest)
	}
	return rows, hasID, nil
}

// exportTableRows writes the table as CSV: id, the columns, created_at
// and updated_at, paging through read_rows up to 100,000 rows.
func (s *Server) exportTableRows(w http.ResponseWriter, r *http.Request) {
	tr, err := s.tableRows(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	header := []string{"id"}
	for _, c := range tr.table.Columns {
		header = append(header, c.Name)
	}
	header = append(header, "created_at", "updated_at")
	w.Header().Set("Content-Type", mime.FormatMediaType("text/csv", map[string]string{"charset": "utf-8"}))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": tr.table.Name + ".csv"}))
	cw := csv.NewWriter(w)
	cursor := ""
	written := 0
	first := true
	for written < exportMaxRows {
		in := map[string]any{"table": tr.table.Name, "limit": min(exportPage, exportMaxRows-written), "order": []any{map[string]any{"column": "id"}}}
		if cursor != "" {
			in["cursor"] = cursor
		}
		out, err := tr.call(r.Context(), s, "read_rows", in, "")
		if err != nil {
			if first {
				s.failTables(w, r, err)
				return
			}
			s.Logger.Warn("data tables: export stopped early", "table", tr.table.Name, "written", written, "err", err)
			break
		}
		if first {
			_ = cw.Write(header)
			first = false
		}
		rows := rowsOf(out)
		for _, row := range rows {
			m, _ := row.(map[string]any)
			rec := make([]string, len(header))
			for i, h := range header {
				rec[i] = cell(m[h])
			}
			_ = cw.Write(rec)
			written++
		}
		cursor, _ = out["next_cursor"].(string)
		if cursor == "" || len(rows) == 0 {
			break
		}
	}
	cw.Flush()
}

// cell renders a value as a CSV cell.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case json.Number:
		return x.String()
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
