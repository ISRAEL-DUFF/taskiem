package xenibasetest

// Provisioning (decision 0033, data tables), built from Xenibase's
// api/openapi.yaml read 17 October 2026: POST /projects
// (CreateProjectRequest, 202 ProjectCredentials), GET /operations/{id}
// (Operation: queued, running, succeeded, failed), POST
// /projects/{id}/services (202 BackendServicesEnabled, the keys shown
// once), POST /projects/{id}/services/keys (201 CreatedApiKey), POST
// /tokens (201 CreatedToken, never exceeding the caller), GET
// /projects/{id}/schema (DbSchema) and POST /projects/{id}/schema/preview
// and /apply (SchemaPlan, SchemaApplied, 409 stale_plan, 422
// ddl_failed); docs/cli.md (scopes read < write < admin; project
// creation needs an unrestricted write token; enabling services, minting
// keys and drops by token need admin; drops also need a typed confirm);
// docs/errors.md (quota_exceeded 409 with quota, insufficient_scope 403,
// confirm_required 400, conflict 409); docs/integrations/taskiem-reply.md
// P3-G2. An operation is queued on its first poll and succeeded on the
// second, so a client that polls is exercised.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Operation is a Xenibase operation the fake queued.
type Operation struct {
	ID, Kind, ProjectID string
	Status              string // queued, running, succeeded, failed
	Error               string
	polls               int
}

// apiKey is a backend-services API key.
type apiKey struct {
	ID, Kind, Name, Value string
	CreatedAt             time.Time
}

// ProjectQuota caps how many projects an organisation may have: creating
// one past it gets 409 quota_exceeded, as Xenibase answers (P3-G2).
func (f *Fake) ProjectQuota(org string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.quotas == nil {
		f.quotas = map[string]int{}
	}
	f.quotas[org] = n
}

// FailOperation makes the next operation of this kind fail instead of
// succeeding (a create that Xenibase could not finish).
func (f *Fake) FailOperation(kind, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opFailures == nil {
		f.opFailures = map[string]string{}
	}
	f.opFailures[kind] = reason
}

// Operations returns copies of every operation, oldest first.
func (f *Fake) Operations() []Operation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Operation, 0, len(f.ops))
	for _, o := range f.ops {
		out = append(out, *o)
	}
	return out
}

// Projects returns the ids of every project, in the order they were made.
func (f *Fake) Projects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.projectOrder)
}

// Tokens returns every token the fake accepts (names repeat after a
// renewal; the secret tells them apart).
func (f *Fake) Tokens() []Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Token, 0, len(f.tokens))
	for _, t := range f.tokens {
		out = append(out, t)
	}
	return out
}

// Table returns a copy of a table's definition (nil when there is none).
func (f *Fake) Table(projectID, name string) *Table {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		return nil
	}
	t := p.Tables[qualify(name)]
	if t == nil {
		return nil
	}
	c := Table{Columns: slices.Clone(t.Columns), Key: slices.Clone(t.Key), Types: map[string]string{}}
	for k, v := range t.Types {
		c.Types[k] = v
	}
	return &c
}

func (f *Fake) newOperation(kind, projectID string) *Operation {
	op := &Operation{ID: newID(), Kind: kind, ProjectID: projectID, Status: "queued"}
	if reason, ok := f.opFailures[kind]; ok {
		op.Error = reason
		delete(f.opFailures, kind)
	}
	f.ops = append(f.ops, op)
	return op
}

func (op *Operation) view(now time.Time) map[string]any {
	var finished, errText any
	if op.Status == "succeeded" || op.Status == "failed" {
		finished = now.UTC().Format(time.RFC3339)
	}
	if op.Error != "" {
		errText = op.Error
	}
	return map[string]any{"id": op.ID, "kind": op.Kind, "project_id": op.ProjectID, "params": map[string]any{}, "status": op.Status,
		"attempts": 1, "run_after": now.UTC().Format(time.RFC3339), "log": []any{}, "error": errText, "created_by": nil,
		"created_at": now.UTC().Format(time.RFC3339), "finished_at": finished}
}

func (p *project) view(f *Fake) map[string]any {
	status := "active"
	switch {
	case p.failed:
		status = "error"
	case p.creating:
		status = "provisioning"
	}
	return map[string]any{"id": p.ID, "org_id": p.OrgID, "name": p.Name, "slug": strings.ToLower(strings.ReplaceAll(p.Name, " ", "-")),
		"db_name": p.dbName(), "owner_role": "p_owner", "tier": "shared", "status": status, "region": p.Region, "settings": map[string]any{},
		"created_at": f.Now().UTC().Format(time.RFC3339),
		"connection": map[string]any{"host": "db.xenibase.test", "port": 5432, "database": p.dbName(), "user": "p_owner"}}
}

func (k apiKey) view() map[string]any {
	out := map[string]any{"id": k.ID, "kind": k.Kind, "name": k.Name, "prefix": k.Value[:min(len(k.Value), 10)],
		"last_used_at": nil, "revoked_at": nil, "created_at": k.CreatedAt.UTC().Format(time.RFC3339)}
	if k.Kind == "publishable" {
		out["key"] = k.Value // a secret key's value is shown only when made
	}
	return out
}

func newKeyValue(kind string) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	if kind == "secret" {
		return "xb_sec_" + hex.EncodeToString(raw)
	}
	return "xb_pub_" + hex.EncodeToString(raw)
}

// servicesView is GET /projects/{id}/services for a project with its
// services on: the keys' metadata, with only the publishable value.
func (f *Fake) servicesView(p *project) map[string]any {
	keys := []any{}
	for _, k := range p.Keys {
		keys = append(keys, k.view())
	}
	return map[string]any{"enabled": true, "ref": p.Ref, "url": f.URL + "/edge/" + p.Ref, "enabled_at": "2026-10-01T00:00:00Z",
		"keys": keys, "cors_origins": []any{}, "exposed_schemas": []any{"public"}, "public_tables": []any{}, "settings": map[string]any{}, "feed_configured": true,
		"roles": map[string]any{"anon": "p_anon", "user": "p_user", "service": "p_service"}}
}

// --- the organisation-level routes ---

// serveCreateProject is POST /api/v1/projects: an unrestricted token with
// the write scope, in the organisation, within its project quota.
func (f *Fake) serveCreateProject(w http.ResponseWriter, r *http.Request, tok Token) {
	var req struct {
		Name   string `json:"name"`
		OrgID  string `json:"org_id"`
		Tier   string `json:"tier"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 64 {
		refuse(w, http.StatusBadRequest, "bad_request", "name is required (1 to 64 characters)")
		return
	}
	if !slices.Contains(tok.Scopes, "write") || tok.ProjectIDs != nil {
		refuse(w, http.StatusForbidden, "insufficient_scope", "creating a project needs the write scope and a token not restricted to projects")
		return
	}
	if req.OrgID != "" && req.OrgID != tok.OrgID {
		refuse(w, http.StatusNotFound, "not_found", "no such organisation")
		return
	}
	if req.Tier != "" && req.Tier != "shared" && req.Tier != "dedicated" {
		refuse(w, http.StatusBadRequest, "bad_request", "tier is shared or dedicated")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if max, ok := f.quotas[tok.OrgID]; ok {
		n := 0
		for _, p := range f.projects {
			if p.OrgID == tok.OrgID {
				n++
			}
		}
		if n >= max {
			reply(w, http.StatusConflict, map[string]any{"code": "quota_exceeded", "message": fmt.Sprintf("the organisation has %d projects, its plan's limit", n),
				"quota": map[string]any{"limit": "projects", "used": n, "max": max}})
			return
		}
	}
	id := newID()
	p := &project{ID: id, Name: req.Name, OrgID: tok.OrgID, Tables: map[string]*Table{}, Ref: ref(id), Functions: map[string]Function{}, Region: req.Region, creating: true}
	f.projects[id] = p
	f.projectOrder = append(f.projectOrder, id)
	op := f.newOperation("create", id)
	reply(w, http.StatusAccepted, map[string]any{"project": p.view(f), "operation": op.view(f.Now()), "password": "pw-" + id[:8],
		"connection": map[string]any{"host": "db.xenibase.test", "port": 5432, "database": p.dbName(), "user": "p_owner"}})
}

// serveOperation is GET /api/v1/operations/{id}: queued on the first
// poll, finished on the second.
func (f *Fake) serveOperation(w http.ResponseWriter, tok Token, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var op *Operation
	for _, o := range f.ops {
		if o.ID == id {
			op = o
		}
	}
	if op == nil || (op.ProjectID != "" && (f.projects[op.ProjectID] == nil || f.projects[op.ProjectID].OrgID != tok.OrgID)) {
		refuse(w, http.StatusNotFound, "not_found", "no such operation")
		return
	}
	f.advance(op)
	reply(w, http.StatusOK, op.view(f.Now()))
}

// advance counts a poll of an operation: queued on the first, finished
// on the second. Polling a project advances its pending create
// operation the same way (Xenibase's runs on its own; the fake's moves
// when it is looked at).
func (f *Fake) advance(op *Operation) {
	op.polls++
	if op.polls >= 2 && op.Status == "queued" {
		op.Status = "succeeded"
		if op.Error != "" {
			op.Status = "failed"
		}
		if p := f.projects[op.ProjectID]; p != nil && op.Kind == "create" {
			p.creating = false
			if op.Status == "failed" {
				p.failed = true
			}
		}
	}
}

// serveCreateToken is POST /api/v1/tokens: a token made by a token never
// exceeds the caller's scopes, projects or expiry, and lasts at most a
// year.
func (f *Fake) serveCreateToken(w http.ResponseWriter, r *http.Request, tok Token) {
	var req struct {
		Name          string   `json:"name"`
		OrgID         string   `json:"org_id"`
		Scopes        []string `json:"scopes"`
		ProjectIDs    []string `json:"project_ids"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || len(req.Name) > 100 || req.OrgID == "" || len(req.Scopes) == 0 {
		refuse(w, http.StatusBadRequest, "invalid", "name, org_id and scopes are required")
		return
	}
	if req.OrgID != tok.OrgID {
		refuse(w, http.StatusNotFound, "not_found", "no such organisation")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 365 {
		refuse(w, http.StatusBadRequest, "invalid", "expires_in_days is 1 to 365")
		return
	}
	for _, s := range req.Scopes {
		if !slices.Contains([]string{"read", "write", "admin"}, s) {
			refuse(w, http.StatusBadRequest, "invalid", "unknown scope "+s)
			return
		}
		if !slices.Contains(tok.Scopes, s) {
			refuse(w, http.StatusForbidden, "insufficient_scope", "the new token would exceed the caller's scopes")
			return
		}
	}
	if slices.Contains(req.Scopes, "admin") && !slices.Contains(req.Scopes, "write") {
		refuse(w, http.StatusBadRequest, "invalid", "admin requires write")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if tok.ProjectIDs != nil {
		if req.ProjectIDs == nil {
			refuse(w, http.StatusForbidden, "insufficient_scope", "the caller is restricted to projects; the new token must be too")
			return
		}
		for _, id := range req.ProjectIDs {
			if !slices.Contains(tok.ProjectIDs, id) {
				refuse(w, http.StatusForbidden, "insufficient_scope", "the new token would reach projects the caller cannot")
				return
			}
		}
	}
	for _, id := range req.ProjectIDs {
		if p := f.projects[id]; p == nil || p.OrgID != tok.OrgID {
			refuse(w, http.StatusNotFound, "not_found", "no such project "+id)
			return
		}
	}
	days := req.ExpiresInDays
	if days == 0 {
		days = 90
	}
	expires := f.Now().Add(time.Duration(days) * 24 * time.Hour)
	if expires.After(tok.ExpiresAt) {
		expires = tok.ExpiresAt
	}
	raw := make([]byte, 20)
	_, _ = rand.Read(raw)
	nt := Token{Secret: "xb_" + hex.EncodeToString(raw), ID: newID(), Name: req.Name, OrgID: tok.OrgID, Scopes: slices.Clone(req.Scopes), ProjectIDs: req.ProjectIDs, ExpiresAt: expires}
	f.tokens[nt.Secret] = nt
	view := map[string]any{"id": nt.ID, "name": nt.Name, "org_id": nt.OrgID, "prefix": nt.Secret[:8], "scopes": nt.Scopes, "project_ids": nt.ProjectIDs,
		"expires_at": nt.ExpiresAt.UTC().Format(time.RFC3339), "last_used_at": nil, "last_used_ip": nil, "revoked_at": nil,
		"created_at": f.Now().UTC().Format(time.RFC3339), "created_via": "api", "status": "active"}
	reply(w, http.StatusCreated, map[string]any{"secret": nt.Secret, "token": view})
}

// --- the project-level routes ---

// serveEnableServices is POST /projects/{id}/services (admin scope): the
// first publishable and secret keys are returned once.
func (f *Fake) serveEnableServices(w http.ResponseWriter, p *project, tok Token) {
	if !slices.Contains(tok.Scopes, "admin") {
		refuse(w, http.StatusForbidden, "insufficient_scope", "enabling backend services needs the admin scope")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.creating || p.failed {
		refuse(w, http.StatusConflict, "conflict", "the project is busy with another operation")
		return
	}
	if p.Services {
		refuse(w, http.StatusConflict, "conflict", "backend services are already on")
		return
	}
	p.Services = true
	made := []any{}
	for _, kind := range []string{"publishable", "secret"} {
		k := apiKey{ID: newID(), Kind: kind, Name: "default", Value: newKeyValue(kind), CreatedAt: f.Now()}
		p.Keys = append(p.Keys, k)
		if kind == "secret" {
			p.SecretKeys = append(p.SecretKeys, k.Value)
		}
		made = append(made, map[string]any{"key": k.view(), "value": k.Value})
	}
	op := f.newOperation("enable_services", p.ID)
	reply(w, http.StatusAccepted, map[string]any{"services": f.servicesView(p), "operation": op.view(f.Now()), "keys": made})
}

// serveCreateKey is POST /projects/{id}/services/keys (admin scope).
func (f *Fake) serveCreateKey(w http.ResponseWriter, r *http.Request, p *project, tok Token) {
	if !slices.Contains(tok.Scopes, "admin") {
		refuse(w, http.StatusForbidden, "insufficient_scope", "making an API key needs the admin scope")
		return
	}
	var req struct {
		Kind, Name string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Kind != "publishable" && req.Kind != "secret") || req.Name == "" || len(req.Name) > 64 {
		refuse(w, http.StatusBadRequest, "bad_request", "kind (publishable or secret) and name are required")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !p.Services {
		refuse(w, http.StatusConflict, "conflict", "backend services are off")
		return
	}
	k := apiKey{ID: newID(), Kind: req.Kind, Name: req.Name, Value: newKeyValue(req.Kind), CreatedAt: f.Now()}
	p.Keys = append(p.Keys, k)
	if k.Kind == "secret" {
		p.SecretKeys = append(p.SecretKeys, k.Value)
	}
	reply(w, http.StatusCreated, map[string]any{"key": k.view(), "value": k.Value})
}

// serveSchema is GET /projects/{id}/schema: the public schema's tables.
func (f *Fake) serveSchema(w http.ResponseWriter, p *project) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(p.Tables))
	for n := range p.Tables {
		names = append(names, n)
	}
	slices.Sort(names)
	tables := []any{}
	for _, n := range names {
		t := p.Tables[n]
		cols := []any{}
		for _, c := range t.Columns {
			typ := t.Types[c]
			if typ == "" {
				typ = "text"
			}
			cols = append(cols, map[string]any{"name": c, "type": typ, "nullable": !slices.Contains(t.Key, c), "default": nil})
		}
		tables = append(tables, map[string]any{"name": strings.TrimPrefix(n, "public."), "kind": "table", "row_estimate": len(t.rows), "size_bytes": 8192,
			"comment": nil, "primary_key": t.Key, "columns": cols, "indexes": []any{}})
	}
	reply(w, http.StatusOK, map[string]any{"schemas": []any{map[string]any{"name": "public", "tables": tables, "enums": []any{}}}})
}

// schemaChange is the part of SchemaChange the fake understands.
type schemaChange struct {
	Kind       string      `json:"kind"`
	Schema     string      `json:"schema"`
	Table      string      `json:"table"`
	Column     *schemaCol  `json:"column"`
	Columns    []schemaCol `json:"columns"`
	ColumnName string      `json:"column_name"`
	NewName    string      `json:"new_name"`
}

type schemaCol struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   *bool  `json:"nullable"`
	Default    string `json:"default"`
	PrimaryKey bool   `json:"primary_key"`
	Unique     bool   `json:"unique"`
	Comment    string `json:"comment"`
}

var sqlIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// planHash is the preview's hash: deterministic over the change, so
// apply with a preview's hash passes and any other change is stale.
func planHash(raw json.RawMessage) string {
	var v any
	_ = json.Unmarshal(raw, &v)
	canon, _ := json.Marshal(v) // map keys are sorted
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// plan turns a change into its statements, risks and confirm name, or a
// refusal (code, message).
func (f *Fake) plan(p *project, c schemaChange) (map[string]any, string, string) {
	if c.Schema == "" {
		c.Schema = "public"
	}
	if c.Schema != "public" {
		return nil, "bad_request", "the fake serves only the public schema"
	}
	q := func(s string) string { return `"` + s + `"` }
	statements := []any{}
	risks := []any{}
	down := []any{}
	confirm := ""
	slug := c.Kind
	switch c.Kind {
	case "create_table":
		if !sqlIdent.MatchString(c.Table) || len(c.Columns) == 0 {
			return nil, "bad_request", "create_table needs a table and columns"
		}
		defs := make([]string, 0, len(c.Columns))
		for _, col := range c.Columns {
			if !sqlIdent.MatchString(col.Name) || col.Type == "" {
				return nil, "bad_request", "every column needs a name and a type"
			}
			d := q(col.Name) + " " + col.Type
			if col.Nullable != nil && !*col.Nullable {
				d += " NOT NULL"
			}
			if col.Default != "" {
				d += " DEFAULT " + col.Default
			}
			if col.PrimaryKey {
				d += " PRIMARY KEY"
			}
			if col.Unique {
				d += " UNIQUE"
			}
			defs = append(defs, d)
		}
		statements = append(statements, map[string]any{"sql": "CREATE TABLE " + q(c.Table) + " (" + strings.Join(defs, ", ") + ")", "transactional": true})
		down = append(down, "DROP TABLE "+q(c.Table))
		slug += "_" + c.Table
	case "add_column":
		if !sqlIdent.MatchString(c.Table) || c.Column == nil || !sqlIdent.MatchString(c.Column.Name) || c.Column.Type == "" {
			return nil, "bad_request", "add_column needs a table and a column with a name and a type"
		}
		d := q(c.Column.Name) + " " + c.Column.Type
		if c.Column.Nullable != nil && !*c.Column.Nullable {
			d += " NOT NULL"
			if c.Column.Default == "" {
				risks = append(risks, map[string]any{"level": "warning", "message": "a NOT NULL column without a default fails on a table with rows"})
			}
		}
		if c.Column.Default != "" {
			d += " DEFAULT " + c.Column.Default
		}
		statements = append(statements, map[string]any{"sql": "ALTER TABLE " + q(c.Table) + " ADD COLUMN " + d, "transactional": true})
		down = append(down, "ALTER TABLE "+q(c.Table)+" DROP COLUMN "+q(c.Column.Name))
		slug += "_" + c.Table + "_" + c.Column.Name
	case "drop_column":
		if !sqlIdent.MatchString(c.Table) || !sqlIdent.MatchString(c.ColumnName) {
			return nil, "bad_request", "drop_column needs a table and a column_name"
		}
		statements = append(statements, map[string]any{"sql": "ALTER TABLE " + q(c.Table) + " DROP COLUMN " + q(c.ColumnName), "transactional": true})
		risks = append(risks, map[string]any{"level": "danger", "message": "drops the column and its data"})
		confirm = c.ColumnName
		slug += "_" + c.Table + "_" + c.ColumnName
	case "drop_table":
		if !sqlIdent.MatchString(c.Table) {
			return nil, "bad_request", "drop_table needs a table"
		}
		statements = append(statements, map[string]any{"sql": "DROP TABLE " + q(c.Table), "transactional": true})
		risks = append(risks, map[string]any{"level": "danger", "message": "drops the table and every row"})
		confirm = c.Table
		slug += "_" + c.Table
	case "rename_column":
		if !sqlIdent.MatchString(c.Table) || !sqlIdent.MatchString(c.ColumnName) || !sqlIdent.MatchString(c.NewName) {
			return nil, "bad_request", "rename_column needs a table, a column_name and a new_name"
		}
		statements = append(statements, map[string]any{"sql": "ALTER TABLE " + q(c.Table) + " RENAME COLUMN " + q(c.ColumnName) + " TO " + q(c.NewName), "transactional": true})
		down = append(down, "ALTER TABLE "+q(c.Table)+" RENAME COLUMN "+q(c.NewName)+" TO "+q(c.ColumnName))
		slug += "_" + c.Table + "_" + c.NewName
	default:
		return nil, "bad_request", "the fake knows create_table, add_column, drop_column, drop_table and rename_column, not " + c.Kind
	}
	out := map[string]any{"statements": statements, "risks": risks, "down": down, "down_todo": []any{}, "slug": slug}
	if confirm != "" {
		out["confirm"] = confirm
	}
	return out, "", ""
}

// serveSchemaPreview is POST /projects/{id}/schema/preview.
func (f *Fake) serveSchemaPreview(w http.ResponseWriter, p *project, body []byte) {
	var req struct {
		Change json.RawMessage `json:"change"`
	}
	var c schemaChange
	if err := json.Unmarshal(body, &req); err != nil || len(req.Change) == 0 || json.Unmarshal(req.Change, &c) != nil {
		refuse(w, http.StatusBadRequest, "bad_request", "change is required")
		return
	}
	f.mu.Lock()
	out, code, msg := f.plan(p, c)
	f.mu.Unlock()
	if code != "" {
		refuse(w, http.StatusBadRequest, code, msg)
		return
	}
	out["hash"] = planHash(req.Change)
	reply(w, http.StatusOK, out)
}

// serveSchemaApply is POST /projects/{id}/schema/apply: the hash must be
// the preview's (409 stale_plan), a drop needs confirm (400
// confirm_required) and, by token, the admin scope (403
// insufficient_scope); DDL Postgres would refuse is 422 ddl_failed.
func (f *Fake) serveSchemaApply(w http.ResponseWriter, p *project, tok Token, body []byte) {
	var req struct {
		Change  json.RawMessage `json:"change"`
		Hash    string          `json:"hash"`
		Confirm string          `json:"confirm"`
	}
	var c schemaChange
	if err := json.Unmarshal(body, &req); err != nil || len(req.Change) == 0 || json.Unmarshal(req.Change, &c) != nil || req.Hash == "" {
		refuse(w, http.StatusBadRequest, "bad_request", "change and hash are required")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	plan, code, msg := f.plan(p, c)
	if code != "" {
		refuse(w, http.StatusBadRequest, code, msg)
		return
	}
	if planHash(req.Change) != req.Hash {
		refuse(w, http.StatusConflict, "stale_plan", "the change would now produce other SQL; preview it again")
		return
	}
	if confirm, _ := plan["confirm"].(string); confirm != "" {
		if !slices.Contains(tok.Scopes, "admin") {
			refuse(w, http.StatusForbidden, "insufficient_scope", "a drop by token needs the admin scope")
			return
		}
		if req.Confirm != confirm {
			refuse(w, http.StatusBadRequest, "confirm_required", "type the name of what is dropped: "+confirm)
			return
		}
	}
	key := qualify(c.Table)
	t := p.Tables[key]
	ddl := func(msg string) {
		reply(w, http.StatusUnprocessableEntity, map[string]any{"code": "ddl_failed", "message": msg, "statement": plan["statements"].([]any)[0].(map[string]any)["sql"]})
	}
	switch c.Kind {
	case "create_table":
		if t != nil {
			ddl("relation \"" + c.Table + "\" already exists")
			return
		}
		nt := &Table{Types: map[string]string{}}
		for _, col := range c.Columns {
			if slices.Contains(nt.Columns, col.Name) {
				ddl("column \"" + col.Name + "\" specified more than once")
				return
			}
			nt.Columns = append(nt.Columns, col.Name)
			nt.Types[col.Name] = col.Type
			if col.PrimaryKey {
				nt.Key = append(nt.Key, col.Name)
			}
		}
		p.Tables[key] = nt
	case "add_column":
		if t == nil {
			ddl("relation \"" + c.Table + "\" does not exist")
			return
		}
		if slices.Contains(t.Columns, c.Column.Name) {
			ddl("column \"" + c.Column.Name + "\" of relation \"" + c.Table + "\" already exists")
			return
		}
		if c.Column.Nullable != nil && !*c.Column.Nullable && c.Column.Default == "" && len(t.rows) > 0 {
			ddl("column \"" + c.Column.Name + "\" of relation \"" + c.Table + "\" contains null values")
			return
		}
		t.Columns = append(t.Columns, c.Column.Name)
		if t.Types == nil {
			t.Types = map[string]string{}
		}
		t.Types[c.Column.Name] = c.Column.Type
		for _, row := range t.rows {
			row[c.Column.Name] = nil
		}
	case "drop_column":
		if t == nil {
			ddl("relation \"" + c.Table + "\" does not exist")
			return
		}
		if !slices.Contains(t.Columns, c.ColumnName) {
			ddl("column \"" + c.ColumnName + "\" of relation \"" + c.Table + "\" does not exist")
			return
		}
		if slices.Contains(t.Key, c.ColumnName) {
			ddl("cannot drop column \"" + c.ColumnName + "\": it is part of the primary key")
			return
		}
		t.Columns = slices.DeleteFunc(t.Columns, func(s string) bool { return s == c.ColumnName })
		delete(t.Types, c.ColumnName)
		for _, row := range t.rows {
			delete(row, c.ColumnName)
		}
	case "drop_table":
		if t == nil {
			ddl("table \"" + c.Table + "\" does not exist")
			return
		}
		delete(p.Tables, key)
	case "rename_column":
		if t == nil || !slices.Contains(t.Columns, c.ColumnName) {
			ddl("column \"" + c.ColumnName + "\" does not exist")
			return
		}
		if slices.Contains(t.Columns, c.NewName) {
			ddl("column \"" + c.NewName + "\" already exists")
			return
		}
		for i, col := range t.Columns {
			if col == c.ColumnName {
				t.Columns[i] = c.NewName
			}
		}
		if typ, ok := t.Types[c.ColumnName]; ok {
			delete(t.Types, c.ColumnName)
			t.Types[c.NewName] = typ
		}
		for i, k := range t.Key {
			if k == c.ColumnName {
				t.Key[i] = c.NewName
			}
		}
		for _, row := range t.rows {
			if v, ok := row[c.ColumnName]; ok {
				delete(row, c.ColumnName)
				row[c.NewName] = v
			}
		}
	}
	plan["hash"] = req.Hash
	reply(w, http.StatusOK, map[string]any{"plan": plan, "duration_ms": 3})
}
