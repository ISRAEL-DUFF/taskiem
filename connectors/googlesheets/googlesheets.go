// Package googlesheets is the Google Sheets connector (Sheets API v4):
// read, append, write and clear ranges, and create spreadsheets and sheets.
// Connections are a service account (shared on each spreadsheet, or with
// domain-wide delegation and a subject) or an OAuth client with a refresh
// token (connectors/internal/google). Built from Google's public
// documentation (docs/integrations/googlesheets.md).
package googlesheets

import (
	"context"
	_ "embed"
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
// names others.
var DefaultScopes = []string{"https://www.googleapis.com/auth/spreadsheets"}

// Options configure the connector: BaseURL replaces
// https://sheets.googleapis.com/v4 and TokenURL Google's token endpoint
// (tests).
type Options struct {
	BaseURL  string
	TokenURL string
}

// New returns the Google Sheets connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{base: strings.TrimRight(m.BaseURL, "/"), tokens: google.NewTokens(o.TokenURL)}
	if o.BaseURL != "" {
		c.base = strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
		// The override narrows egress to its host; tokens still come from
		// the token endpoint.
		if u, err := url.Parse(c.tokens.TokenURL); err == nil && u.Hostname() != "" && !contains(m.Egress, u.Hostname()) {
			m.Egress = append(m.Egress, u.Hostname())
		}
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_values":         connector.ActionFunc(c.getValues),
		"batch_get_values":   connector.ActionFunc(c.batchGetValues),
		"append_rows":        connector.ActionFunc(c.appendRows),
		"update_values":      connector.ActionFunc(c.updateValues),
		"clear_values":       connector.ActionFunc(c.clearValues),
		"create_spreadsheet": connector.ActionFunc(c.createSpreadsheet),
		"add_sheet":          connector.ActionFunc(c.addSheet),
		"get_spreadsheet":    connector.ActionFunc(c.getSpreadsheet),
	}}
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

func (c *client) do(ctx context.Context, req connector.Request, method, path string, q url.Values, body, out any) error {
	creds, err := google.FromConnection(req.Credentials, DefaultScopes)
	if err != nil {
		return err
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.tokens.Do(ctx, req.HTTP, creds, method, u, body, out)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func fatal(action string, format string, args ...any) error {
	return fmt.Errorf("googlesheets %s: %s: %w", action, fmt.Sprintf(format, args...), effects.ErrFatal)
}

// need returns the named string inputs, refusing empty ones.
func need(action string, in map[string]any, keys ...string) ([]string, error) {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = strings.TrimSpace(str(in, k))
		if out[i] == "" {
			return nil, fatal(action, "%s is required", k)
		}
	}
	return out, nil
}

// enum checks an optional enum input and returns it (or def).
func enum(action string, in map[string]any, k, def string, allowed ...string) (string, error) {
	v := str(in, k)
	if v == "" {
		return def, nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fatal(action, "%s must be one of %s", k, strings.Join(allowed, ", "))
}

func sheetPath(id string) string { return "/spreadsheets/" + url.PathEscape(id) }

func valuesPath(id, rng string) string {
	return sheetPath(id) + "/values/" + url.PathEscape(rng)
}

// readQuery reads the shared read options.
func readQuery(action string, in map[string]any) (url.Values, error) {
	q := url.Values{}
	for _, o := range []struct {
		in, param string
		allowed   []string
	}{
		{"value_render_option", "valueRenderOption", []string{"FORMATTED_VALUE", "UNFORMATTED_VALUE", "FORMULA"}},
		{"date_time_render_option", "dateTimeRenderOption", []string{"SERIAL_NUMBER", "FORMATTED_STRING"}},
		{"major_dimension", "majorDimension", []string{"ROWS", "COLUMNS"}},
	} {
		v, err := enum(action, in, o.in, "", o.allowed...)
		if err != nil {
			return nil, err
		}
		if v != "" {
			q.Set(o.param, v)
		}
	}
	return q, nil
}

// valueRange is Google's ValueRange.
type valueRange struct {
	Range          string  `json:"range"`
	MajorDimension string  `json:"majorDimension"`
	Values         [][]any `json:"values"`
}

// output renders a range; with header the first row names the fields of
// one object per following row.
func (v valueRange) output(header bool) map[string]any {
	rows := make([]any, len(v.Values))
	for i, r := range v.Values {
		if r == nil {
			r = []any{}
		}
		rows[i] = r
	}
	out := map[string]any{"range": v.Range, "major_dimension": v.MajorDimension, "values": rows}
	if !header || v.MajorDimension == "COLUMNS" {
		return out
	}
	headers := []any{}
	objects := []any{}
	if len(v.Values) > 0 {
		names := headerNames(v.Values[0])
		for _, n := range names {
			headers = append(headers, n)
		}
		for _, r := range v.Values[1:] {
			obj := make(map[string]any, len(names))
			for i, n := range names {
				if i < len(r) {
					obj[n] = r[i]
				} else {
					obj[n] = nil
				}
			}
			// Cells beyond the header get their column letter.
			for i := len(names); i < len(r); i++ {
				obj[column(i)] = r[i]
			}
			objects = append(objects, obj)
		}
	}
	out["headers"], out["objects"] = headers, objects
	return out
}

// headerNames turns a header row into unique field names: blank headers
// become their column letter, repeats get _2, _3.
func headerNames(row []any) []string {
	names := make([]string, len(row))
	seen := map[string]int{}
	for i, c := range row {
		n := strings.TrimSpace(fmt.Sprint(c))
		if c == nil || n == "" {
			n = column(i)
		}
		seen[n]++
		if seen[n] > 1 {
			n = n + "_" + strconv.Itoa(seen[n])
		}
		names[i] = n
	}
	return names
}

// column is the A1 letter of a zero-based column index.
func column(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func (c *client) getValues(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("get_values", in, "spreadsheet_id", "range")
	if err != nil {
		return connector.Response{}, err
	}
	q, err := readQuery("get_values", in)
	if err != nil {
		return connector.Response{}, err
	}
	var v valueRange
	if err := c.do(ctx, req, http.MethodGet, valuesPath(ids[0], ids[1]), q, nil, &v); err != nil {
		return connector.Response{}, err
	}
	h, _ := in["header_row"].(bool)
	return connector.Response{Output: v.output(h)}, nil
}

func strList(v any) []string {
	var out []string
	switch l := v.(type) {
	case []string:
		out = l
	case []any:
		for _, x := range l {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (c *client) batchGetValues(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("batch_get_values", in, "spreadsheet_id")
	if err != nil {
		return connector.Response{}, err
	}
	ranges := strList(in["ranges"])
	if len(ranges) == 0 {
		return connector.Response{}, fatal("batch_get_values", "ranges is required")
	}
	q, err := readQuery("batch_get_values", in)
	if err != nil {
		return connector.Response{}, err
	}
	for _, r := range ranges {
		q.Add("ranges", r)
	}
	var r struct {
		ValueRanges []valueRange `json:"valueRanges"`
	}
	if err := c.do(ctx, req, http.MethodGet, sheetPath(ids[0])+"/values:batchGet", q, nil, &r); err != nil {
		return connector.Response{}, err
	}
	h, _ := in["header_row"].(bool)
	out := make([]any, len(r.ValueRanges))
	for i, v := range r.ValueRanges {
		out[i] = v.output(h)
	}
	return connector.Response{Output: map[string]any{"value_ranges": out}}, nil
}

// rows checks that values is an array of arrays of cells.
func rows(action string, v any) ([]any, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fatal(action, "values must be an array of rows")
	}
	for i, r := range list {
		cells, ok := r.([]any)
		if !ok {
			return nil, fatal(action, "values[%d] must be an array of cells", i)
		}
		for j, c := range cells {
			switch c.(type) {
			case nil, string, bool, int, int64, float64:
			default:
				return nil, fatal(action, "values[%d][%d] must be a string, number, boolean or null", i, j)
			}
		}
	}
	return list, nil
}

// updates is Google's UpdateValuesResponse.
type updates struct {
	UpdatedRange   string `json:"updatedRange"`
	UpdatedRows    int64  `json:"updatedRows"`
	UpdatedColumns int64  `json:"updatedColumns"`
	UpdatedCells   int64  `json:"updatedCells"`
}

func (u updates) output() map[string]any {
	return map[string]any{"updated_range": u.UpdatedRange, "updated_rows": u.UpdatedRows, "updated_columns": u.UpdatedColumns, "updated_cells": u.UpdatedCells}
}

func (c *client) appendRows(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("append_rows", in, "spreadsheet_id", "range")
	if err != nil {
		return connector.Response{}, err
	}
	vals, err := rows("append_rows", in["values"])
	if err != nil {
		return connector.Response{}, err
	}
	if len(vals) == 0 {
		return connector.Response{}, fatal("append_rows", "values has no rows")
	}
	vio, err := enum("append_rows", in, "value_input_option", "RAW", "RAW", "USER_ENTERED")
	if err != nil {
		return connector.Response{}, err
	}
	ido, err := enum("append_rows", in, "insert_data_option", "INSERT_ROWS", "INSERT_ROWS", "OVERWRITE")
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{"valueInputOption": {vio}, "insertDataOption": {ido}}
	var r struct {
		TableRange string  `json:"tableRange"`
		Updates    updates `json:"updates"`
	}
	if err := c.do(ctx, req, http.MethodPost, valuesPath(ids[0], ids[1])+":append", q, map[string]any{"majorDimension": "ROWS", "values": vals}, &r); err != nil {
		return connector.Response{}, err
	}
	out := r.Updates.output()
	out["table_range"] = r.TableRange
	return connector.Response{Output: out}, nil
}

func (c *client) updateValues(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("update_values", in, "spreadsheet_id", "range")
	if err != nil {
		return connector.Response{}, err
	}
	vals, err := rows("update_values", in["values"])
	if err != nil {
		return connector.Response{}, err
	}
	vio, err := enum("update_values", in, "value_input_option", "RAW", "RAW", "USER_ENTERED")
	if err != nil {
		return connector.Response{}, err
	}
	var u updates
	body := map[string]any{"range": ids[1], "majorDimension": "ROWS", "values": vals}
	if err := c.do(ctx, req, http.MethodPut, valuesPath(ids[0], ids[1]), url.Values{"valueInputOption": {vio}}, body, &u); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: u.output()}, nil
}

func (c *client) clearValues(ctx context.Context, req connector.Request) (connector.Response, error) {
	ids, err := need("clear_values", req.Input, "spreadsheet_id", "range")
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		ClearedRange string `json:"clearedRange"`
	}
	// The documented request body is empty.
	if err := c.do(ctx, req, http.MethodPost, valuesPath(ids[0], ids[1])+":clear", nil, nil, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"cleared_range": r.ClearedRange}}, nil
}

// spreadsheet is the part of Google's Spreadsheet the connector returns.
type spreadsheet struct {
	SpreadsheetID  string `json:"spreadsheetId"`
	SpreadsheetURL string `json:"spreadsheetUrl"`
	Properties     struct {
		Title string `json:"title"`
	} `json:"properties"`
	Sheets []struct {
		Properties sheetProps `json:"properties"`
	} `json:"sheets"`
}

type sheetProps struct {
	SheetID int64  `json:"sheetId"`
	Title   string `json:"title"`
	Index   int64  `json:"index"`
}

func (p sheetProps) output() map[string]any {
	return map[string]any{"sheet_id": p.SheetID, "title": p.Title, "index": p.Index}
}

func (s spreadsheet) output() map[string]any {
	sheets := make([]any, len(s.Sheets))
	for i, sh := range s.Sheets {
		sheets[i] = sh.Properties.output()
	}
	return map[string]any{"spreadsheet_id": s.SpreadsheetID, "spreadsheet_url": s.SpreadsheetURL, "title": s.Properties.Title, "sheets": sheets}
}

func (c *client) createSpreadsheet(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("create_spreadsheet", in, "title")
	if err != nil {
		return connector.Response{}, err
	}
	props := map[string]any{"title": ids[0]}
	if s := str(in, "locale"); s != "" {
		props["locale"] = s
	}
	if s := str(in, "time_zone"); s != "" {
		props["timeZone"] = s
	}
	body := map[string]any{"properties": props}
	if titles := strList(in["sheet_titles"]); len(titles) > 0 {
		sheets := make([]any, len(titles))
		for i, t := range titles {
			sheets[i] = map[string]any{"properties": map[string]any{"title": t}}
		}
		body["sheets"] = sheets
	}
	var s spreadsheet
	if err := c.do(ctx, req, http.MethodPost, "/spreadsheets", nil, body, &s); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: s.output()}, nil
}

func intIn(in map[string]any, k string) (int64, bool) {
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

func (c *client) addSheet(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ids, err := need("add_sheet", in, "spreadsheet_id", "title")
	if err != nil {
		return connector.Response{}, err
	}
	props := map[string]any{"title": ids[1]}
	if n, ok := intIn(in, "index"); ok {
		props["index"] = n
	}
	grid := map[string]any{}
	if n, ok := intIn(in, "row_count"); ok {
		grid["rowCount"] = n
	}
	if n, ok := intIn(in, "column_count"); ok {
		grid["columnCount"] = n
	}
	if len(grid) > 0 {
		props["gridProperties"] = grid
	}
	body := map[string]any{"requests": []any{map[string]any{"addSheet": map[string]any{"properties": props}}}}
	var r struct {
		Replies []struct {
			AddSheet struct {
				Properties sheetProps `json:"properties"`
			} `json:"addSheet"`
		} `json:"replies"`
	}
	if err := c.do(ctx, req, http.MethodPost, sheetPath(ids[0])+":batchUpdate", nil, body, &r); err != nil {
		return connector.Response{}, err
	}
	if len(r.Replies) == 0 {
		// Google acted (2xx) but did not say what it made.
		return connector.Response{}, fmt.Errorf("googlesheets add_sheet: no reply for the new sheet: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: r.Replies[0].AddSheet.Properties.output()}, nil
}

func (c *client) getSpreadsheet(ctx context.Context, req connector.Request) (connector.Response, error) {
	ids, err := need("get_spreadsheet", req.Input, "spreadsheet_id")
	if err != nil {
		return connector.Response{}, err
	}
	q := url.Values{"fields": {"spreadsheetId,spreadsheetUrl,properties.title,sheets.properties(sheetId,title,index)"}}
	var s spreadsheet
	if err := c.do(ctx, req, http.MethodGet, sheetPath(ids[0]), q, nil, &s); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: s.output()}, nil
}
