package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Data tables (decision 0033, docs/data-tables.md): a table's definition
// lives here, its rows in the organisation's Xenibase project, reached
// through the xenibase@1 connection named data_tables like any workflow
// step would. Reading rows needs table.read, changing anything
// table.edit. Personal columns come back to table.read holders as they
// are (the page is the record) and never reach audit details or logs.

// tablesPurpose is the recorded purpose of the connection reads the
// tables API makes, so every one is in secret_reads like a step's.
const tablesPurpose = "tables.api"

// tablesRoutes mounts the data tables API.
func (s *Server) tablesRoutes(r chi.Router) {
	r.With(s.need(PermTableRead)).Get("/tables/status", s.tablesStatus)
	r.With(s.need(PermTableEdit), s.need(PermConnectionManage)).Put("/tables/project", s.putTablesProject)
	r.With(s.need(PermTableRead)).Get("/tables", s.listTables)
	r.With(s.need(PermTableEdit)).Post("/tables", s.createTable)
	r.With(s.need(PermTableRead)).Get("/tables/{id}", s.getTable)
	r.With(s.need(PermTableEdit)).Patch("/tables/{id}", s.patchTable)
	r.With(s.need(PermTableEdit)).Delete("/tables/{id}", s.deleteTable)
	r.With(s.need(PermTableEdit)).Post("/tables/{id}/columns", s.addTableColumn)
	r.With(s.need(PermTableEdit)).Delete("/tables/{id}/columns/{name}", s.dropTableColumn)
	r.With(s.need(PermTableRead)).Get("/tables/{id}/rows", s.listTableRows)
	r.With(s.need(PermTableRead)).Post("/tables/{id}/rows/query", s.queryTableRows)
	r.With(s.need(PermTableEdit)).Post("/tables/{id}/rows", s.insertTableRows)
	r.With(s.need(PermTableEdit)).Patch("/tables/{id}/rows/{row}", s.updateTableRow)
	r.With(s.need(PermTableEdit)).Delete("/tables/{id}/rows/{row}", s.deleteTableRow)
	r.With(s.need(PermTableEdit)).Post("/tables/{id}/import", s.importTableRows)
	r.With(s.need(PermTableRead)).Get("/tables/{id}/export", s.exportTableRows)
}

// tables is the service, or a 503 when the deployment has none.
func (s *Server) tables() (*datatables.Service, error) {
	if s.Tables == nil {
		return nil, datatables.ErrUnavailable
	}
	return s.Tables, nil
}

// failTables answers an error from the tables service or Xenibase.
func (s *Server) failTables(w http.ResponseWriter, r *http.Request, err error) {
	code := func(status int, code, msg string) {
		writeJSON(w, status, map[string]any{"error": msg, "code": code})
	}
	var xe *datatables.Error
	var pe *xenibase.Error
	switch {
	case errors.Is(err, datatables.ErrKept):
		code(http.StatusConflict, "table_kept", strings.TrimPrefix(err.Error(), datatables.ErrKept.Error()+": "))
	case errors.Is(err, datatables.ErrUnavailable):
		code(http.StatusServiceUnavailable, "tables_unavailable", "data tables are not available on this platform: no Xenibase is configured and the organisation brought no project")
	case errors.Is(err, datatables.ErrProvisioning):
		w.Header().Set("Retry-After", "30")
		code(http.StatusConflict, "tables_provisioning", err.Error())
	case errors.Is(err, datatables.ErrProjectFailed):
		code(http.StatusServiceUnavailable, "tables_project_failed", pii.Redact(err.Error()))
	case errors.Is(err, datatables.ErrInvalid):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, datatables.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, datatables.ErrConflict):
		writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "conflict: "))
	case errors.As(err, &xe):
		switch {
		case xe.IsQuota():
			writeJSON(w, http.StatusConflict, map[string]any{"error": "the platform's Xenibase organisation cannot take another project (" + xe.Message + "); the operator must raise its quota",
				"code": "xenibase_quota", "limit": xe.QuotaLimit()})
		case xe.Temporary():
			ra := xe.RetryAfter
			if ra <= 0 {
				ra = 30 * time.Second
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(ra.Round(time.Second).Seconds())))
			code(http.StatusServiceUnavailable, "xenibase_unavailable", "Xenibase cannot do this right now ("+xe.Code+"): "+xe.Message)
		case xe.Code == "ddl_failed", xe.Code == "stale_plan", xe.Status == http.StatusBadRequest, xe.Status == http.StatusUnprocessableEntity:
			code(http.StatusBadRequest, "xenibase_refused", "Xenibase refused the change ("+xe.Code+"): "+xe.Message)
		case xe.Status == http.StatusConflict:
			code(http.StatusConflict, "xenibase_conflict", "Xenibase: "+xe.Message)
		default:
			s.Logger.Warn("data tables: Xenibase refused a call", "path", r.URL.Path, "call", xe.Call, "status", xe.Status, "code", xe.Code)
			to := ""
			if xe.Call != "" {
				to = " to " + xe.Call
			}
			code(http.StatusBadGateway, "xenibase_error", "Xenibase answered "+strconv.Itoa(xe.Status)+" "+xe.Code+to+": "+xe.Message)
		}
	case errors.As(err, &pe):
		msg := "Xenibase: " + pe.Message
		if pe.Code != "" {
			msg = "Xenibase (" + pe.Code + "): " + pe.Message
		}
		switch pe.Class {
		case xenibase.ClassValidation:
			code(http.StatusBadRequest, "xenibase_refused", msg)
		case xenibase.ClassNotFound:
			code(http.StatusNotFound, "xenibase_not_found", msg)
		case xenibase.ClassConflict:
			code(http.StatusConflict, "xenibase_conflict", msg)
		case xenibase.ClassQuota:
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": msg, "code": "quota_exceeded", "limit": "xenibase_" + pe.Code})
		case xenibase.ClassRateLimited, xenibase.ClassUnavailable, xenibase.ClassTimeout:
			ra := pe.RetryAfter
			if ra <= 0 {
				ra = 5 * time.Second
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(ra.Round(time.Second).Seconds())))
			code(http.StatusServiceUnavailable, "xenibase_unavailable", msg)
		default:
			s.Logger.Warn("data tables: the data API refused a call", "path", r.URL.Path, "status", pe.Status, "code", pe.Code, "class", pe.Class)
			code(http.StatusBadGateway, "xenibase_error", msg)
		}
	case errors.Is(err, egress.ErrDenied):
		code(http.StatusBadGateway, "xenibase_error", "Xenibase could not be reached: "+pii.Redact(err.Error()))
	default:
		s.fail(w, r, err)
	}
}

// --- status and the project ---

func (s *Server) tablesStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var n int
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		n, err = datatables.CountTables(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	svc := s.Tables
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "configured": false, "status": "none", "own": false, "connection": datatables.ConnectionName, "tables": n})
		return
	}
	st, err := svc.Status(r.Context(), p.TenantID)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusView(st, n))
}

func statusView(st datatables.Status, tables int) map[string]any {
	out := map[string]any{"available": st.Available, "configured": st.Configured, "status": st.Status, "own": st.Own, "connection": st.Connection, "tables": tables}
	for k, v := range map[string]string{"project_id": st.ProjectID, "project_ref": st.Ref, "api_url": st.APIURL, "project_name": st.ProjectName, "error": st.Error} {
		if v != "" {
			out[k] = v
		}
	}
	if st.TokenExpiresAt != nil {
		out["token_expires_at"] = st.TokenExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

// putTablesProject makes an existing xenibase connection of the
// organisation's own the data tables project (decision 0033, point 4).
func (s *Server) putTablesProject(w http.ResponseWriter, r *http.Request) {
	svc, err := s.tables()
	if err != nil && s.Tables == nil {
		s.failTables(w, r, err)
		return
	}
	var req struct {
		ConnectionID uuid.UUID `json:"connection_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var name string
	if err := s.tx(r, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT name FROM connections WHERE id = $1 AND environment = $2 AND connector = $3 AND status = 'active' AND provisioned_by IS NULL`,
			req.ConnectionID, datatables.Environment, datatables.Connector).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: connection_id must be an active xenibase@1 connection of your own in %s", errBadRequest, datatables.Environment)
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	conn, creds, hc, err := s.xenibaseConnection(r.Context(), p.TenantID, name, "tables.project.set")
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if strings.TrimSpace(creds["secret_key"]) == "" || strings.TrimSpace(creds["api_url"]) == "" {
		writeErr(w, http.StatusBadRequest, "the connection needs the project's secret key (xb_sec_…) and its data API URL: data tables write rows through the data API")
		return
	}
	resp, err := conn.Actions["test_connection"].Execute(r.Context(), connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the connection failed its test: "+pii.Redact(err.Error()))
		return
	}
	out, _ := resp.Output.(map[string]any)
	proj, _ := out["project"].(map[string]any)
	projectName, _ := proj["name"].(string)
	apiURL, _ := out["api_url"].(string)
	if apiURL != "" && !strings.EqualFold(strings.TrimRight(apiURL, "/"), strings.TrimRight(strings.TrimSpace(creds["api_url"]), "/")) {
		writeErr(w, http.StatusBadRequest, "the connection's data API URL is not the project's ("+apiURL+")")
		return
	}
	var expires *time.Time
	if s, _ := out["expires_at"].(string); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			expires = &t
		}
	}
	ref := ""
	if u := strings.TrimPrefix(strings.TrimPrefix(apiURL, "https://"), "http://"); u != "" {
		ref, _, _ = strings.Cut(u, ".")
	}
	var status datatables.Status
	err = s.tx(r, func(tx pgx.Tx) error {
		proj, renewed, err := svc.SetOwnProject(r.Context(), tx, p.TenantID, creds, projectName, ref, strings.TrimSpace(creds["api_url"]), expires)
		if err != nil {
			return err
		}
		status = datatables.Status{Project: proj, Available: true, Configured: svc.Available(), Connection: datatables.ConnectionName}
		return auditTx(r, tx, "table.project.set", proj.ProjectID, map[string]any{"connection_id": req.ConnectionID, "connection": name, "project_name": projectName,
			"renewed_credentials": renewed})
	})
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusView(status, 0))
}

// xenibaseConnection reads a xenibase connection's credentials for a purpose
// and returns the connector with a guarded client for it.
func (s *Server) xenibaseConnection(ctx context.Context, tenant uuid.UUID, name, purpose string) (*connector.Connector, map[string]string, *http.Client, error) {
	if s.Registry == nil || s.Vault == nil {
		return nil, nil, nil, errors.New("no connector registry or vault")
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, nil, nil, err
	}
	conn, ok := reg.Get(datatables.Connector + "@1")
	if !ok {
		return nil, nil, nil, errors.New("the Xenibase connector is not installed")
	}
	actor := "system"
	if p := principalFrom(ctx); p != nil {
		actor = p.Actor()
	}
	creds, err := s.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose, Actor: actor}), tenant, datatables.Environment, conn.Manifest.ID, name)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, nil, nil, fmt.Errorf("the %s connection is missing: %w", name, datatables.ErrNotFound)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID}, 30*time.Second)
	return conn, creds, hc, nil
}

// --- tables ---

// tableView is a table as the API answers it.
func tableView(t datatables.Table) map[string]any {
	cols := make([]map[string]any, 0, len(t.Columns))
	for _, c := range t.Columns {
		cols = append(cols, map[string]any{"name": c.Name, "title": c.Title, "type": c.Type, "required": c.Required, "personal": c.Personal})
	}
	var kept any // null for an ordinary table
	if t.Kept() {
		kept = t.KeptBy
	}
	return map[string]any{"id": t.ID, "name": t.Name, "title": t.Title, "description": t.Description, "columns": cols,
		"postgres_table": "public." + t.Name, "connection": datatables.ConnectionName, "created_by": t.CreatedBy, "kept_by": kept,
		"created_at": t.CreatedAt.UTC().Format(time.RFC3339), "updated_at": t.UpdatedAt.UTC().Format(time.RFC3339)}
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	var tables []datatables.Table
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		tables, err = datatables.ListTables(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	views := make([]map[string]any, len(tables))
	for i, t := range tables {
		views[i] = tableView(t)
	}
	if r.URL.Query().Get("counts") != "no" {
		for i, n := range s.rowCounts(r.Context(), principalFrom(r.Context()).TenantID, tablesPurpose, tables) {
			if n != nil {
				views[i]["row_count"] = *n
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": views})
}

// rowCounts are the tables' estimated row counts, read in parallel with
// a short budget, in the tables' order; a table whose count cannot be had
// (or every one, when the connection cannot be read) has nil.
func (s *Server) rowCounts(ctx context.Context, tenant uuid.UUID, purpose string, tables []datatables.Table) []*int64 {
	counts := make([]*int64, len(tables))
	if len(tables) == 0 {
		return counts
	}
	conn, creds, hc, err := s.xenibaseConnection(ctx, tenant, datatables.ConnectionName, purpose)
	if err != nil {
		return counts
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, t := range tables {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			if n, ok := s.rowCount(ctx, conn, creds, hc, name); ok {
				counts[i] = &n
			}
		}(i, t.Name)
	}
	wg.Wait()
	return counts
}

// rowCount is a table's estimated row count through read_rows.
func (s *Server) rowCount(ctx context.Context, conn *connector.Connector, creds map[string]string, hc *http.Client, table string) (int64, bool) {
	resp, err := conn.Actions["read_rows"].Execute(ctx, connector.Request{Input: map[string]any{"table": table, "limit": 1, "select": "id", "count": "estimated"},
		Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		return 0, false
	}
	out, _ := resp.Output.(map[string]any)
	switch n := out["count"].(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

type tableReq struct {
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Columns     []columnReq `json:"columns"`
}

// columnReq is a column as the API takes it: personal left out means
// the type's default (true for phone and email).
type columnReq struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Personal *bool  `json:"personal"`
}

// columns are the request's columns as the tables service takes them.
func (t tableReq) columns() []datatables.Column {
	out := make([]datatables.Column, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.column()
	}
	return out
}

func (c columnReq) column() datatables.Column {
	out := datatables.Column{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required, Personal: datatables.PersonalByDefault(c.Type)}
	if c.Personal != nil {
		out.Personal = *c.Personal
	}
	return out
}

// createTable records the table, provisions the organisation's project
// on the first one, and creates the Postgres table through Xenibase's
// schema API.
func (s *Server) createTable(w http.ResponseWriter, r *http.Request) {
	var req tableReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.newDataTable(r, principalFrom(r.Context()), req)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, tableView(t))
}

// newDataTable makes a table for p: the Tables page, and an AI draft's
// tables when it is saved (ai.go). Its errors are answered by failTables.
func (s *Server) newDataTable(r *http.Request, p *Principal, req tableReq) (datatables.Table, error) {
	svc, err := s.tables()
	if err != nil {
		return datatables.Table{}, err
	}
	lim, err := s.Store.LimitsFor(r.Context(), p.TenantID)
	if err != nil {
		return datatables.Table{}, err
	}
	if err := datatables.CheckName("table", req.Name); err != nil {
		return datatables.Table{}, err
	}
	cols := req.columns()
	if err := datatables.CheckColumns(cols, lim.MaxDataTableColumns); err != nil {
		return datatables.Table{}, err
	}
	if len(req.Title) > 120 || len(req.Description) > 2000 {
		return datatables.Table{}, fmt.Errorf("%w: the title is at most 120 characters and the description 2,000", errBadRequest)
	}
	// The limit and the name are checked before anything is made.
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkCount(r.Context(), tx, p.TenantID, "max_data_tables"); err != nil {
			return err
		}
		var taken bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM data_tables WHERE name = $1)`, req.Name).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("%w: a table named %q already exists", errConflict, req.Name)
		}
		// A name Taskiem keeps for its own table is not made here, even
		// before Taskiem has made it (datatables.KeptNames).
		return datatables.CheckTableName(req.Name)
	}); err != nil {
		return datatables.Table{}, err
	}
	proj, err := svc.EnsureProject(r.Context(), p.TenantID)
	if err != nil {
		return datatables.Table{}, err
	}
	if !proj.Ready() {
		return datatables.Table{}, datatables.ErrProvisioning
	}
	t := datatables.Table{Name: req.Name, Title: req.Title, Description: req.Description, Columns: cols, CreatedBy: p.Actor()}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkCount(r.Context(), tx, p.TenantID, "max_data_tables"); err != nil {
			return err
		}
		if err := datatables.InsertTable(r.Context(), tx, p.TenantID, &t); err != nil {
			return err
		}
		return auditTx(r, tx, "table.create", t.Name, map[string]any{"table_id": t.ID, "columns": columnNames(t.Columns)})
	})
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			err = fmt.Errorf("%w: a table named %q already exists", errConflict, req.Name)
		}
		return datatables.Table{}, err
	}
	if err := svc.Apply(r.Context(), p.TenantID, p.Actor(), datatables.CreateTableChange(t.Name, t.Columns)); err != nil {
		// The record goes with the table that was not made.
		_ = s.tx(r, func(tx pgx.Tx) error { return datatables.DeleteTable(r.Context(), tx, t.ID) })
		return datatables.Table{}, err
	}
	return t, nil
}

func columnNames(cols []datatables.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name + ":" + c.Type
	}
	return out
}

// loadTable reads the table in the path.
func (s *Server) loadTable(r *http.Request) (datatables.Table, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return datatables.Table{}, fmt.Errorf("table: %w", datatables.ErrNotFound)
	}
	var t datatables.Table
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		t, err = datatables.GetTable(r.Context(), tx, id)
		return err
	})
	return t, err
}

func (s *Server) getTable(w http.ResponseWriter, r *http.Request) {
	t, err := s.loadTable(r)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	view := tableView(t)
	p := principalFrom(r.Context())
	if conn, creds, hc, err := s.xenibaseConnection(r.Context(), p.TenantID, datatables.ConnectionName, tablesPurpose); err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		if n, ok := s.rowCount(ctx, conn, creds, hc, t.Name); ok {
			view["row_count"] = n
		}
		cancel()
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) patchTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Title != nil && len(*req.Title) > 120 || req.Description != nil && len(*req.Description) > 2000 {
		writeErr(w, http.StatusBadRequest, "the title is at most 120 characters and the description 2,000")
		return
	}
	t, err := s.loadTable(r)
	if err == nil {
		err = datatables.Writable(t)
	}
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := datatables.UpdateTable(r.Context(), tx, t.ID, req.Title, req.Description); err != nil {
			return err
		}
		t, err = datatables.GetTable(r.Context(), tx, t.ID)
		return err
	})
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tableView(t))
}

type confirmReq struct {
	Confirm string `json:"confirm"`
}

// deleteTable drops the Postgres table (every row with it) after the
// table's name is typed, then forgets it.
func (s *Server) deleteTable(w http.ResponseWriter, r *http.Request) {
	svc, err := s.tables()
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	var req confirmReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.loadTable(r)
	if err == nil {
		err = datatables.Writable(t)
	}
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if req.Confirm != t.Name {
		writeErr(w, http.StatusBadRequest, "type the table's name ("+t.Name+") as confirm to delete it and every row")
		return
	}
	p := principalFrom(r.Context())
	if err := svc.Apply(r.Context(), p.TenantID, p.Actor(), datatables.DropTableChange(t.Name, req.Confirm)); err != nil && !alreadyGone(err) {
		s.failTables(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := datatables.DeleteTable(r.Context(), tx, t.ID); err != nil {
			return err
		}
		return auditTx(r, tx, "table.delete", t.Name, map[string]any{"table_id": t.ID, "columns": len(t.Columns)})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// alreadyGone reports that Xenibase refused a drop because the object
// does not exist: the record is stale and may go.
func alreadyGone(err error) bool {
	xe, ok := datatables.AsError(err)
	return ok && xe.Code == "ddl_failed" && strings.Contains(xe.Message, "does not exist")
}

// --- columns ---

func (s *Server) addTableColumn(w http.ResponseWriter, r *http.Request) {
	svc, err := s.tables()
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	var req columnReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	c := req.column()
	t, err := s.loadTable(r)
	if err == nil {
		err = datatables.Writable(t)
	}
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	lim, err := s.Store.LimitsFor(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := datatables.CheckColumn(&c); err != nil {
		s.failTables(w, r, err)
		return
	}
	for _, x := range t.Columns {
		if x.Name == c.Name {
			writeErr(w, http.StatusConflict, "the table already has a column "+c.Name)
			return
		}
	}
	if lim.MaxDataTableColumns > 0 && len(t.Columns) >= lim.MaxDataTableColumns {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": fmt.Sprintf("this plan allows at most %d columns per table", lim.MaxDataTableColumns), "code": "limit_exceeded", "limit": "max_data_table_columns"})
		return
	}
	if err := svc.Apply(r.Context(), p.TenantID, p.Actor(), datatables.AddColumnChange(t.Name, c)); err != nil {
		s.failTables(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := datatables.InsertColumn(r.Context(), tx, p.TenantID, t.ID, c); err != nil {
			return err
		}
		if err := auditTx(r, tx, "table.column.add", t.Name+"."+c.Name, map[string]any{"table_id": t.ID, "type": c.Type, "required": c.Required, "personal": c.Personal}); err != nil {
			return err
		}
		t, err = datatables.GetTable(r.Context(), tx, t.ID)
		return err
	})
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, tableView(t))
}

func (s *Server) dropTableColumn(w http.ResponseWriter, r *http.Request) {
	svc, err := s.tables()
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	var req confirmReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.loadTable(r)
	if err == nil {
		err = datatables.Writable(t)
	}
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	found := false
	for _, c := range t.Columns {
		found = found || c.Name == name
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if req.Confirm != name {
		writeErr(w, http.StatusBadRequest, "type the column's name ("+name+") as confirm to drop it and its data")
		return
	}
	p := principalFrom(r.Context())
	if err := svc.Apply(r.Context(), p.TenantID, p.Actor(), datatables.DropColumnChange(t.Name, name, req.Confirm)); err != nil && !alreadyGone(err) {
		s.failTables(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := datatables.DeleteColumn(r.Context(), tx, t.ID, name); err != nil {
			return err
		}
		if err := auditTx(r, tx, "table.column.drop", t.Name+"."+name, map[string]any{"table_id": t.ID}); err != nil {
			return err
		}
		t, err = datatables.GetTable(r.Context(), tx, t.ID)
		return err
	})
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tableView(t))
}
