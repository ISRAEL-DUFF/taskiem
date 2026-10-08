package googlesheets

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/connectors/internal/google"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const sid = "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"

var creds = map[string]string{"client_id": "cid.apps.googleusercontent.com", "client_secret": "shh", "refresh_token": "1//rt"}

func tokenServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"ya29.test-token","expires_in":3599,"token_type":"Bearer"}`)
	}))
	t.Cleanup(ts.Close)
	return ts, &n
}

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (map[string]any, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	ts, _ := tokenServer(t)
	c := New(Options{BaseURL: srv.URL, TokenURL: ts.URL})
	r, err := c.Actions[action].Execute(context.Background(), connector.Request{Input: input, Credentials: creds, HTTP: srv.Client(), Attempt: 1})
	out, _ := r.Output.(map[string]any)
	return out, err
}

func TestRegistersHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("googlesheets@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "sheets.googleapis.com" || h[1] != google.TokenHost {
		t.Errorf("hosts %v", h)
	}
	if h := New(Options{BaseURL: "https://sheets.proxy.example/v4"}).Manifest.Hosts(); len(h) != 2 || h[1] != google.TokenHost {
		t.Errorf("override hosts %v", h)
	}
}

func TestGetValuesAsRowsAndObjects(t *testing.T) {
	out, err := call(t, "get_values", map[string]any{"spreadsheet_id": sid, "range": "Class Data!A1:E", "value_render_option": "UNFORMATTED_VALUE", "major_dimension": "ROWS", "header_row": true}, "get")
	if err != nil {
		t.Fatal(err)
	}
	if out["range"] != "'Class Data'!A1:E31" || len(out["values"].([]any)) != 4 {
		t.Errorf("rows %v", out)
	}
	// Blank headers take their column letter; repeats are numbered.
	if h := out["headers"].([]any); !reflect.DeepEqual(h, []any{"Student Name", "Gender", "C", "Gender_2", "Major"}) {
		t.Errorf("headers %v", h)
	}
	objs := out["objects"].([]any)
	if len(objs) != 3 {
		t.Fatalf("objects %v", objs)
	}
	if o := objs[0].(map[string]any); o["Student Name"] != "Alexandra" || o["C"] != "4. Senior" || o["Gender_2"] != "x" || o["Major"] != "English" {
		t.Errorf("first %v", o)
	}
	// Google omits trailing empty cells: they are null.
	if o := objs[1].(map[string]any); o["C"] != float64(1) || o["Major"] != nil {
		t.Errorf("short row %v", o)
	}
	if o := objs[2].(map[string]any); o["F"] != "extra" {
		t.Errorf("cells past the header %v", o)
	}

	out, err = call(t, "get_values", map[string]any{"spreadsheet_id": sid, "range": "Sheet2", "header_row": true}, "get_empty")
	if err != nil || len(out["values"].([]any)) != 0 || len(out["objects"].([]any)) != 0 {
		t.Errorf("empty %v %v", out, err)
	}
	// Without header_row only rows.
	out, err = call(t, "get_values", map[string]any{"spreadsheet_id": sid, "range": "Sheet2"}, "get_empty")
	if _, ok := out["objects"]; err != nil || ok {
		t.Errorf("no header %v %v", out, err)
	}
	if _, err := call(t, "get_values", map[string]any{"spreadsheet_id": sid, "range": "A1", "value_render_option": "PRETTY"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad enum: %v", err)
	}
	if _, err := call(t, "get_values", map[string]any{"range": "A1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no id: %v", err)
	}
}

func TestBatchGet(t *testing.T) {
	out, err := call(t, "batch_get_values", map[string]any{"spreadsheet_id": sid, "ranges": []any{"Sheet1!A1:B2", "Sheet2!A1:A2"}, "header_row": true}, "batch_get")
	if err != nil {
		t.Fatal(err)
	}
	vr := out["value_ranges"].([]any)
	if len(vr) != 2 || vr[1].(map[string]any)["objects"].([]any)[0].(map[string]any)["name"] != "Ada" {
		t.Errorf("%v", vr)
	}
	if _, err := call(t, "batch_get_values", map[string]any{"spreadsheet_id": sid, "ranges": []any{}}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no ranges: %v", err)
	}
}

func TestAppendRows(t *testing.T) {
	// RAW by default, so "=HYPERLINK(...)" from a payload stays text.
	out, err := call(t, "append_rows", map[string]any{"spreadsheet_id": sid, "range": "Sheet1!A:C",
		"values": []any{[]any{"2026-10-06", `=HYPERLINK("x")`, int64(100), true, nil}}}, "append")
	if err != nil {
		t.Fatal(err)
	}
	if out["table_range"] != "Sheet1!A1:C10" || out["updated_range"] != "Sheet1!A11:E11" || out["updated_rows"] != int64(1) {
		t.Errorf("%v", out)
	}
	in := map[string]any{"spreadsheet_id": sid, "range": "Sheet1!A:C", "values": []any{[]any{"x"}}}
	// Unknown outcome: the rows may be there; a repeat would add them again.
	if _, err := call(t, "append_rows", in, "append_500"); effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("500: %v", err)
	}
	// Quota: refused before writing.
	if _, err := call(t, "append_rows", in, "append_429"); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("429: %v", err)
	}
	for name, bad := range map[string]any{"not rows": []any{"x"}, "object cell": []any{[]any{map[string]any{"a": 1}}}, "none": []any{}, "string": "x"} {
		if _, err := call(t, "append_rows", map[string]any{"spreadsheet_id": sid, "range": "A1", "values": bad}); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := call(t, "append_rows", map[string]any{"spreadsheet_id": sid, "range": "A1", "values": []any{[]any{"x"}}, "insert_data_option": "APPEND"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad insert option: %v", err)
	}
}

func TestUpdateAndClear(t *testing.T) {
	out, err := call(t, "update_values", map[string]any{"spreadsheet_id": sid, "range": "Sheet1!B2:C2", "values": []any{[]any{"paid", "=B1*2"}}, "value_input_option": "USER_ENTERED", "request_key": "k"}, "update")
	if err != nil || out["updated_cells"] != int64(2) {
		t.Errorf("update %v %v", out, err)
	}
	out, err = call(t, "clear_values", map[string]any{"spreadsheet_id": sid, "range": "Sheet1!A2:Z", "request_key": "k"}, "clear")
	if err != nil || out["cleared_range"] != "Sheet1!A2:Z1000" {
		t.Errorf("clear %v %v", out, err)
	}
}

func TestCreateAddAndGetSpreadsheet(t *testing.T) {
	out, err := call(t, "create_spreadsheet", map[string]any{"title": "Payroll 2026-10", "time_zone": "Africa/Lagos", "sheet_titles": []any{"Staff", "Payments"}}, "create")
	if err != nil {
		t.Fatal(err)
	}
	if out["spreadsheet_id"] != "1NewSheetId" || len(out["sheets"].([]any)) != 2 || out["sheets"].([]any)[1].(map[string]any)["sheet_id"] != int64(1290) {
		t.Errorf("create %v", out)
	}
	out, err = call(t, "add_sheet", map[string]any{"spreadsheet_id": sid, "title": "October", "index": int64(0), "row_count": int64(100), "column_count": int64(8)}, "add_sheet")
	if err != nil || out["sheet_id"] != int64(123456) || out["title"] != "October" {
		t.Errorf("add %v %v", out, err)
	}
	_, err = call(t, "add_sheet", map[string]any{"spreadsheet_id": sid, "title": "October"}, "add_sheet_exists")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("exists: %v", err)
	}
	out, err = call(t, "get_spreadsheet", map[string]any{"spreadsheet_id": sid}, "spreadsheet")
	if err != nil || out["title"] != "Example Spreadsheet" || out["sheets"].([]any)[0].(map[string]any)["title"] != "Class Data" {
		t.Errorf("get %v %v", out, err)
	}
	_, err = call(t, "get_spreadsheet", map[string]any{"spreadsheet_id": "missing"}, "not_found")
	if effects.Classify(err) != effects.KindFatal || !google.IsNotFound(err) {
		t.Errorf("404: %v", err)
	}
	_, err = call(t, "get_spreadsheet", map[string]any{"spreadsheet_id": sid}, "forbidden")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Errorf("403: %v", err)
	}
}

func TestTokenIsCachedAcrossCalls(t *testing.T) {
	srv := fixture.Serve(t, fixture.Load(t, "spreadsheet"), fixture.Load(t, "spreadsheet"))
	ts, mints := tokenServer(t)
	c := New(Options{BaseURL: srv.URL, TokenURL: ts.URL})
	for range 2 {
		if _, err := c.Actions["get_spreadsheet"].Execute(context.Background(), connector.Request{Input: map[string]any{"spreadsheet_id": sid}, Credentials: creds, HTTP: srv.Client()}); err != nil {
			t.Fatal(err)
		}
	}
	if mints.Load() != 1 {
		t.Errorf("%d mints", mints.Load())
	}
}

func TestColumnLetters(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := column(i); got != want {
			t.Errorf("column(%d) = %s, want %s", i, got, want)
		}
	}
}
