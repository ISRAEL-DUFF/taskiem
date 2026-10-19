package api_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
)

const tablesPlatformToken = "xb_platform"

// tablesWorld is a world with a fake Xenibase as the platform's.
type tablesWorld struct {
	*world
	pg  *xenibasetest.Fake
	svc *datatables.Service
}

func newTablesWorld(t *testing.T) *tablesWorld {
	t.Helper()
	w := newWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	svc := &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	w.srv.Tables = svc
	return &tablesWorld{world: w, pg: pg, svc: svc}
}

var debtorsTable = map[string]any{"name": "debtors", "title": "Debtors", "columns": []map[string]any{
	{"name": "name", "title": "Name", "type": "text", "required": true},
	{"name": "phone", "type": "phone"},
	{"name": "balance_kobo", "type": "integer"},
	{"name": "paid", "type": "boolean"},
}}

// upload posts a CSV as a multipart form.
func (c *client) uploadCSV(t *testing.T, path, filename, body string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = io.WriteString(fw, body)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (c *client) rawGet(t *testing.T, path string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

func (w *tablesWorld) auditActions(t *testing.T, tenant string) map[string]int {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT action, detail::text FROM audit_log WHERE tenant_id = (SELECT id FROM tenants WHERE name = $1) AND action LIKE 'table.%'`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var a, d string
		if err := rows.Scan(&a, &d); err != nil {
			t.Fatal(err)
		}
		out[a]++
		if strings.Contains(d, "2348012345678") || strings.Contains(d, "Ada") {
			t.Errorf("audit %s carries a row's values: %s", a, d)
		}
	}
	return out
}

// Tables end to end through the API: permissions, the project
// provisioned on the first table, create, list, get, patch, columns
// with typed confirmation, rows with type checks and the idempotency
// header, a CSV import and export round trip, delete with confirmation,
// and every change audited without a row's values.
func TestDataTables(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	approver := addMember(t, w.world, owner, "app", "approver")
	builder := addMember(t, w.world, owner, "bob", "builder")

	st := viewer.must(200, "GET", "/v1/tables/status", nil)
	if st["available"] != true || st["configured"] != true || st["status"] != "none" || st["own"] != false || st["tables"] != float64(0) {
		t.Fatalf("status before: %v", st)
	}
	if s, _ := approver.do("GET", "/v1/tables", nil); s != 403 {
		t.Errorf("approver listed tables: %d", s)
	}
	if s, _ := viewer.do("POST", "/v1/tables", debtorsTable); s != 403 {
		t.Errorf("viewer created a table: %d", s)
	}
	if s, _ := builder.do("PUT", "/v1/tables/project", map[string]any{"connection_id": uuid.New()}); s != 403 {
		t.Errorf("builder set the project: %d", s)
	}

	// Bad definitions never reach Xenibase.
	for _, bad := range []map[string]any{
		{"name": "Debtors", "columns": []map[string]any{{"name": "a", "type": "text"}}},
		{"name": "debtors", "columns": []map[string]any{{"name": "id", "type": "text"}}},
		{"name": "debtors", "columns": []map[string]any{{"name": "xb_a", "type": "text"}}},
		{"name": "debtors", "columns": []map[string]any{{"name": "a", "type": "uuid"}}},
		{"name": "debtors", "columns": []map[string]any{{"name": "a", "type": "text"}, {"name": "a", "type": "text"}}},
		{"name": "debtors", "columns": []map[string]any{}},
	} {
		if s, out := builder.do("POST", "/v1/tables", bad, invalidHeader, "1"); s != 400 {
			t.Errorf("%v: %d %v", bad, s, out)
		}
	}
	if len(w.pg.Requests) != 0 {
		t.Fatalf("Xenibase was called for bad tables: %v", w.pg.Requests)
	}

	// The first table provisions the project.
	tbl := builder.must(201, "POST", "/v1/tables", debtorsTable)
	id := tbl["id"].(string)
	cols := tbl["columns"].([]any)
	if tbl["name"] != "debtors" || tbl["postgres_table"] != "public.debtors" || tbl["connection"] != "data_tables" || len(cols) != 4 {
		t.Fatalf("table %v", tbl)
	}
	if c := cols[1].(map[string]any); c["name"] != "phone" || c["personal"] != true {
		t.Errorf("phone is not personal by default: %v", c)
	}
	if c := cols[0].(map[string]any); c["required"] != true || c["personal"] != false {
		t.Errorf("name column %v", c)
	}
	st = viewer.must(200, "GET", "/v1/tables/status", nil)
	if st["status"] != "ready" || st["own"] != false || st["project_ref"] == nil || st["api_url"] == nil || st["tables"] != float64(1) || st["token_expires_at"] == nil {
		t.Fatalf("status after: %v", st)
	}
	projects := w.pg.Projects()
	if len(projects) != 1 || w.pg.Table(projects[0], "debtors") == nil {
		t.Fatalf("projects %v", projects)
	}
	if s, out := builder.do("POST", "/v1/tables", debtorsTable); s != 409 {
		t.Errorf("second debtors: %d %v", s, out)
	}
	// The connection is Taskiem's, shown as such.
	var found bool
	for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		m := c.(map[string]any)
		if m["name"] == "data_tables" && m["connector"] == "xenibase" && m["provisioned_by"] == "taskiem:data_tables" && m["environment"] == "prod" {
			found = true
		}
	}
	if !found {
		t.Error("the data_tables connection is not listed as provisioned")
	}

	// List and get.
	list := viewer.must(200, "GET", "/v1/tables", nil)["tables"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["row_count"] != float64(0) {
		t.Fatalf("list %v", list)
	}
	got := viewer.must(200, "GET", "/v1/tables/"+id, nil)
	if got["name"] != "debtors" || got["row_count"] != float64(0) {
		t.Fatalf("get %v", got)
	}
	viewer.must(404, "GET", "/v1/tables/"+uuid.NewString(), nil)
	if p := builder.must(200, "PATCH", "/v1/tables/"+id, map[string]any{"title": "People who owe us"}); p["title"] != "People who owe us" || p["description"] != "" {
		t.Errorf("patch %v", p)
	}

	// Rows: checked, written under the request's key, replayed on a repeat.
	for _, bad := range []map[string]any{
		{"row": map[string]any{"phone": "2348012345678"}},
		{"row": map[string]any{"name": "Ada", "balance_kobo": 1.5}},
		{"row": map[string]any{"name": "Ada", "phone": "call me"}},
		{"row": map[string]any{"name": "Ada", "nope": 1}},
		{"row": map[string]any{"name": "Ada", "id": 3}},
	} {
		if s, out := builder.do("POST", "/v1/tables/"+id+"/rows", bad); s != 400 {
			t.Errorf("%v: %d %v", bad, s, out)
		}
	}
	ins := builder.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Ada", "phone": "+234 801 234 5678", "balance_kobo": "5000", "paid": "no"}}, "Idempotency-Key", "order-1")
	rows := ins["rows"].([]any)
	if ins["affected"] != float64(1) || ins["replayed"] != false || len(rows) != 1 {
		t.Fatalf("insert %v", ins)
	}
	row := rows[0].(map[string]any)
	if row["phone"] != "+2348012345678" || row["balance_kobo"] != float64(5000) || row["paid"] != false || row["id"] == nil {
		t.Fatalf("row %v", row)
	}
	again := builder.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Ada", "phone": "+234 801 234 5678", "balance_kobo": "5000", "paid": "no"}}, "Idempotency-Key", "order-1")
	if again["replayed"] != true || len(w.pg.Rows(projects[0], "debtors")) != 1 {
		t.Fatalf("repeat %v, rows %d", again, len(w.pg.Rows(projects[0], "debtors")))
	}
	if s, _ := builder.do("POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Bisi"}}, "Idempotency-Key", "bad key!"); s != 400 {
		t.Errorf("bad key accepted: %d", s)
	}
	builder.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"rows": []map[string]any{{"name": "Bisi", "balance_kobo": 0, "paid": true}, {"name": "Chidi", "balance_kobo": 12000}}})
	rowID := int(row["id"].(float64))
	up := builder.must(200, "PATCH", "/v1/tables/"+id+"/rows/"+strconv.Itoa(rowID), map[string]any{"set": map[string]any{"paid": true, "balance_kobo": 0}})
	if up["affected"] != float64(1) || up["row"].(map[string]any)["paid"] != true {
		t.Fatalf("update %v", up)
	}
	if s, _ := builder.do("PATCH", "/v1/tables/"+id+"/rows/"+strconv.Itoa(rowID), map[string]any{"set": map[string]any{"balance_kobo": "lots"}}); s != 400 {
		t.Errorf("bad patch value: %d", s)
	}
	if s, _ := viewer.do("PATCH", "/v1/tables/"+id+"/rows/"+strconv.Itoa(rowID), map[string]any{"set": map[string]any{"paid": false}}); s != 403 {
		t.Errorf("viewer changed a row: %d", s)
	}
	page := viewer.must(200, "GET", "/v1/tables/"+id+"/rows?limit=2", nil)
	if len(page["rows"].([]any)) != 2 || page["next_cursor"] == "" {
		t.Fatalf("page %v", page)
	}
	page = viewer.must(200, "GET", "/v1/tables/"+id+"/rows?q=chi", nil)
	if r := page["rows"].([]any); len(r) != 1 || r[0].(map[string]any)["name"] != "Chidi" {
		t.Fatalf("search %v", page)
	}
	q := viewer.must(200, "POST", "/v1/tables/"+id+"/rows/query", map[string]any{"filters": []map[string]any{{"column": "balance_kobo", "op": "gt", "value": 0}}, "order": []map[string]any{{"column": "name"}}, "count": "exact"})
	if r := q["rows"].([]any); len(r) != 1 || r[0].(map[string]any)["name"] != "Chidi" || q["count"] != float64(1) {
		t.Fatalf("query %v", q)
	}
	if s, _ := viewer.do("POST", "/v1/tables/"+id+"/rows/query", map[string]any{"filters": []map[string]any{{"column": "secret", "op": "eq", "value": 1}}}); s != 400 {
		t.Errorf("unknown filter column: %d", s)
	}
	del := builder.must(200, "DELETE", "/v1/tables/"+id+"/rows/"+strconv.Itoa(rowID), nil)
	if del["affected"] != float64(1) {
		t.Fatalf("delete %v", del)
	}
	builder.must(404, "DELETE", "/v1/tables/"+id+"/rows/"+strconv.Itoa(rowID), nil)

	// Columns: add (the limit holds), drop with the name typed.
	col := builder.must(201, "POST", "/v1/tables/"+id+"/columns", map[string]any{"name": "email", "type": "email"})
	if cs := col["columns"].([]any); len(cs) != 5 || cs[4].(map[string]any)["personal"] != true {
		t.Fatalf("add column %v", col)
	}
	if s, _ := builder.do("POST", "/v1/tables/"+id+"/columns", map[string]any{"name": "email", "type": "text"}); s != 409 {
		t.Errorf("duplicate column: %d", s)
	}
	if s, out := builder.do("DELETE", "/v1/tables/"+id+"/columns/email", map[string]any{"confirm": "debtors"}); s != 400 {
		t.Errorf("drop with the wrong name: %d %v", s, out)
	}
	builder.must(404, "DELETE", "/v1/tables/"+id+"/columns/nope", map[string]any{"confirm": "nope"})
	if d := builder.must(200, "DELETE", "/v1/tables/"+id+"/columns/email", map[string]any{"confirm": "email"}); len(d["columns"].([]any)) != 4 {
		t.Fatalf("drop column %v", d)
	}
	if tb := w.pg.Table(projects[0], "debtors"); len(tb.Columns) != 7 {
		t.Fatalf("Xenibase columns %v", tb.Columns)
	}

	// CSV: import new rows, export, import the export back (upsert on id).
	s, imp := builder.uploadCSV(t, "/v1/tables/"+id+"/import", "more.csv", "name,phone,balance_kobo,paid\nDayo,08012345679,300,false\nEfe,,0,true\n\n")
	if s != 200 || imp["rows"] != float64(2) || imp["imported"] != float64(2) || imp["mode"] != "insert_rows" || imp["batches"] != float64(1) {
		t.Fatalf("import %d %v", s, imp)
	}
	if s, out := builder.uploadCSV(t, "/v1/tables/"+id+"/import", "bad.csv", "name,colour\nX,red\n"); s != 400 || !strings.Contains(out["error"].(string), "colour") {
		t.Errorf("unknown column import: %d %v", s, out)
	}
	if s, out := builder.uploadCSV(t, "/v1/tables/"+id+"/import", "bad.csv", "name,balance_kobo\nX,lots\n"); s != 400 || !strings.Contains(out["error"].(string), "line 2") {
		t.Errorf("bad value import: %d %v", s, out)
	}
	status, hdr, body := viewer.rawGet(t, "/v1/tables/"+id+"/export")
	if status != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !strings.Contains(hdr.Get("Content-Disposition"), "debtors.csv") {
		t.Fatalf("export %d %v", status, hdr)
	}
	recs, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil || len(recs) != 5 || strings.Join(recs[0], ",") != "id,name,phone,balance_kobo,paid,created_at,updated_at" {
		t.Fatalf("export body %v %v", err, recs)
	}
	var dayo []string
	for _, r := range recs[1:] {
		if r[1] == "Dayo" {
			dayo = r
		}
	}
	if dayo == nil || dayo[2] != "08012345679" || dayo[3] != "300" || dayo[4] != "false" {
		t.Fatalf("Dayo exported as %v", dayo)
	}
	dayo[3] = "0"
	dayo[4] = "true"
	var back strings.Builder
	cw := csv.NewWriter(&back)
	_ = cw.Write(recs[0])
	_ = cw.Write(dayo)
	cw.Flush()
	s, imp = builder.uploadCSV(t, "/v1/tables/"+id+"/import", "debtors.csv", back.String())
	if s != 200 || imp["mode"] != "upsert_rows" || imp["rows"] != float64(1) {
		t.Fatalf("re-import %d %v", s, imp)
	}
	if n := len(w.pg.Rows(projects[0], "debtors")); n != 4 {
		t.Fatalf("rows after re-import: %d", n)
	}
	q = viewer.must(200, "POST", "/v1/tables/"+id+"/rows/query", map[string]any{"filters": []map[string]any{{"column": "name", "op": "eq", "value": "Dayo"}}})
	if r := q["rows"].([]any)[0].(map[string]any); r["paid"] != true || r["balance_kobo"] != float64(0) {
		t.Fatalf("Dayo after re-import %v", r)
	}

	// Every connection read is recorded like a step's.
	var reads int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM secret_reads WHERE kind = 'connection' AND name = 'data_tables' AND purpose = 'tables.api'`).Scan(&reads); err != nil || reads < 10 {
		t.Errorf("secret reads for the tables API: %d %v", reads, err)
	}

	// Delete: the name typed, the table dropped and forgotten.
	if s, out := builder.do("DELETE", "/v1/tables/"+id, map[string]any{"confirm": "Debtors"}); s != 400 {
		t.Errorf("delete with the wrong name: %d %v", s, out)
	}
	builder.must(204, "DELETE", "/v1/tables/"+id, map[string]any{"confirm": "debtors"})
	if w.pg.Table(projects[0], "debtors") != nil {
		t.Error("the Postgres table was not dropped")
	}
	viewer.must(404, "GET", "/v1/tables/"+id, nil)
	acts := w.auditActions(t, "Acme")
	for _, a := range []string{"table.create", "table.delete", "table.column.add", "table.column.drop", "table.import", "table.project.provision"} {
		if acts[a] == 0 {
			t.Errorf("no audit entry %s: %v", a, acts)
		}
	}
}

// The plan limit on tables, the quota at Xenibase and an unconfigured
// platform all answer with a code; an organisation may bring its own
// project instead, once, before any table.
func TestDataTablesLimitsAndOwnProject(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"max_data_tables": 1, "max_data_table_columns": 2}, "operator"); err != nil {
		t.Fatal(err)
	}
	if s, out := owner.do("POST", "/v1/tables", map[string]any{"name": "wide", "columns": []map[string]any{{"name": "a", "type": "text"}, {"name": "b", "type": "text"}, {"name": "c", "type": "text"}}}); s != 400 || !strings.Contains(out["error"].(string), "max_data_table_columns") {
		t.Errorf("three columns: %d %v", s, out)
	}
	first := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "one", "columns": []map[string]any{{"name": "a", "type": "text"}, {"name": "b", "type": "text"}}})
	if s, out := owner.do("POST", "/v1/tables", map[string]any{"name": "two", "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 429 || out["code"] != "limit_exceeded" || out["limit"] != "max_data_tables" {
		t.Errorf("second table: %d %v", s, out)
	}
	if s, out := owner.do("POST", "/v1/tables/"+first["id"].(string)+"/columns", map[string]any{"name": "c", "type": "text"}); s != 429 || out["limit"] != "max_data_table_columns" {
		t.Errorf("third column: %d %v", s, out)
	}
	// An own project after tables exist is refused.
	shop := "7d2c9a10-5b4e-4f00-9c11-0000000000b2"
	w.pg.AddProject(shop, "Shop")
	w.pg.AddSecretKey(shop, "xb_sec_shop")
	w.pg.AddToken(xenibasetest.Token{Secret: "xb_shop", Scopes: []string{"read", "write"}, ProjectIDs: []string{shop}})
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "xenibase@1", "name": "shop",
		"credentials": map[string]string{"token": "xb_shop", "project_id": shop, "secret_key": "xb_sec_shop", "api_url": w.pg.APIURL(shop)}})
	if s, out := owner.do("PUT", "/v1/tables/project", map[string]any{"connection_id": conn["id"]}); s != 409 {
		t.Errorf("own project with tables: %d %v", s, out)
	}

	// Another organisation brings its own project before any table.
	other := w.tenant(t, "Beta", "owner@beta.test")
	bare := other.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "xenibase@1", "name": "bare",
		"credentials": map[string]string{"token": "xb_shop", "project_id": shop}})
	if s, out := other.do("PUT", "/v1/tables/project", map[string]any{"connection_id": bare["id"]}); s != 400 || !strings.Contains(out["error"].(string), "secret key") {
		t.Errorf("own project without a key: %d %v", s, out)
	}
	if s, _ := other.do("PUT", "/v1/tables/project", map[string]any{"connection_id": uuid.New()}); s != 400 {
		t.Errorf("own project with an unknown connection: %d", s)
	}
	full := other.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "xenibase@1", "name": "shop",
		"credentials": map[string]string{"token": "xb_shop", "project_id": shop, "secret_key": "xb_sec_shop", "api_url": w.pg.APIURL(shop)}})
	st := other.must(200, "PUT", "/v1/tables/project", map[string]any{"connection_id": full["id"]})
	if st["own"] != true || st["status"] != "ready" || st["project_id"] != shop || st["available"] != true {
		t.Fatalf("own project %v", st)
	}
	before := len(w.pg.Projects())
	tbl := other.must(201, "POST", "/v1/tables", map[string]any{"name": "stock", "columns": []map[string]any{{"name": "item", "type": "text"}, {"name": "qty", "type": "integer"}}})
	if len(w.pg.Projects()) != before || w.pg.Table(shop, "stock") == nil {
		t.Fatalf("the table was not made in the own project: %v", w.pg.Projects())
	}
	other.must(201, "POST", "/v1/tables/"+tbl["id"].(string)+"/rows", map[string]any{"row": map[string]any{"item": "rice", "qty": 3}})
	if rows := w.pg.Rows(shop, "stock"); len(rows) != 1 {
		t.Fatalf("rows in the own project %v", rows)
	}
	// Taskiem's copy is the data_tables connection; the original stays.
	names := map[string]any{}
	for _, c := range other.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		m := c.(map[string]any)
		names[m["name"].(string)] = m["provisioned_by"]
	}
	if names["data_tables"] != "taskiem:data_tables" || names["shop"] != nil {
		t.Errorf("connections %v", names)
	}
	if acts := w.auditActions(t, "Beta"); acts["table.project.set"] != 1 {
		t.Errorf("audit %v", acts)
	}

	// The quota at Xenibase: a clear 409 naming it.
	w.pg.ProjectQuota(w.pg.OrgID, len(w.pg.Projects()))
	gamma := w.tenant(t, "Gamma", "owner@gamma.test")
	if s, out := gamma.do("POST", "/v1/tables", map[string]any{"name": "t", "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 409 || out["code"] != "xenibase_quota" || out["limit"] != "xenibase_projects" {
		t.Errorf("quota: %d %v", s, out)
	}
	if st := gamma.must(200, "GET", "/v1/tables/status", nil); st["status"] != "creating" || st["error"] == nil {
		t.Errorf("status after the quota refusal %v", st)
	}

	// Unconfigured: 503 with a code, and status says so; the own project
	// still works.
	w.svc.Platform = datatables.Platform{}
	if s, out := gamma.do("POST", "/v1/tables", map[string]any{"name": "t", "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 503 || out["code"] != "tables_unavailable" {
		t.Errorf("unconfigured: %d %v", s, out)
	}
	if st := gamma.must(200, "GET", "/v1/tables/status", nil); st["available"] != false || st["configured"] != false {
		t.Errorf("unconfigured status %v", st)
	}
	if st := other.must(200, "GET", "/v1/tables/status", nil); st["available"] != true || st["configured"] != false {
		t.Errorf("own project status when unconfigured %v", st)
	}
	other.must(201, "POST", "/v1/tables", map[string]any{"name": "staff", "columns": []map[string]any{{"name": "name", "type": "text"}}})
	w.srv.Tables = nil
	if s, out := owner.do("POST", "/v1/tables", map[string]any{"name": "t2", "columns": []map[string]any{{"name": "a", "type": "text"}}}); s != 503 || out["code"] != "tables_unavailable" {
		t.Errorf("no service: %d %v", s, out)
	}
}
