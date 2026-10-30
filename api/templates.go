package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
	"github.com/israel-duff/taskiem/templates"
)

// The SME template library (spec 11.1, docs/templates.md). Templates are
// files in the binary; any member may browse them, and a person with
// workflow.edit instantiates one into a draft workflow of their tenant.
// Instantiation checks every parameter against the template and the
// result against the publishing checks; it never publishes.

// templateView is a template as the gallery shows it.
type templateView struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Tags        []string          `json:"tags"`
	Connectors  []string          `json:"connectors"`
	Variables   []templates.Need  `json:"variables"`
	Params      []templates.Param `json:"params"`
	// Tables are the data tables the workflow works on, made for the
	// business when the template is used if they do not exist yet.
	Tables []templates.Table `json:"tables"`
	// Steps are the template, filled with its examples, read as plain
	// numbered steps.
	Steps []wdtext.Line `json:"steps"`
	// Available: every connector it uses is available to the tenant.
	Available bool `json:"available"`
	// NeedsDeveloper: it starts from a signed web address another system
	// calls (a webhook), which takes someone technical to set up.
	NeedsDeveloper bool `json:"needs_developer"`
	// SupersededBy names the template that replaces this one, which is
	// no longer offered.
	SupersededBy string `json:"superseded_by,omitempty"`
	// Product is the product the template works around (payrolla): the
	// Start page offers it to businesses that connected the product.
	Product string `json:"product,omitempty"`
	// Services are the services the template works with that a business
	// connects (K9): each a list of the connectors that serve it, any one
	// of which will do (the books: QuickBooks or Zoho Books). The Start
	// page offers the template, first, once each is connected.
	Services [][]string `json:"services,omitempty"`
	// Callable is, for a template another workflow can use as a step
	// (decision 0047), its interface: run inputs, outputs and effect.
	Callable   *callableView   `json:"callable,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

// callableView is a callable template's interface: the run inputs a
// template step gives (a JSON Schema), the parameters it fills (those only
// the template's trigger uses are left out), its outputs, and its effect,
// computed from its steps.
type callableView struct {
	Inputs  json.RawMessage    `json:"inputs"`
	Params  []string           `json:"params"`
	Outputs []templates.Output `json:"outputs"`
	Effect  templates.Effect   `json:"effect"`
}

func viewTemplate(t *templates.Template, reg connector.Lookup, full bool) templateView {
	v := templateView{ID: t.ID, Title: t.Title, Summary: t.Summary, Description: t.Description, Category: t.Category,
		Tags: nonNil(t.Tags), Connectors: nonNil(t.Connectors), Variables: nonNil(t.Variables), Params: nonNil(t.Params),
		Tables: nonNil(t.Tables), Steps: []wdtext.Line{}, Available: true, SupersededBy: t.SupersededBy, Product: t.Product}
	for _, c := range t.Connectors {
		if uses, ok := strings.CutPrefix(c, templates.CapabilityPrefix); ok {
			by := providers(reg, uses)
			if len(by) == 0 {
				v.Available = false
			} else if strings.HasPrefix(uses, "books_") {
				v.Services = addService(v.Services, by)
			}
			continue
		}
		got, ok := reg.Get(c)
		if !ok {
			v.Available = false
			continue
		}
		if isService(got.Manifest) {
			v.Services = addService(v.Services, []string{got.Manifest.ID})
		}
	}
	if doc, err := t.Instantiate(t.Examples(), ""); err == nil {
		if def, err := wd.Load(doc); err == nil {
			v.Steps = wdtext.Describe(def, reg)
			if t.Callable && def.RawInputs != nil {
				c := &callableView{Inputs: def.RawInputs.Schema, Params: []string{}, Outputs: t.Outputs, Effect: templates.EffectOf(def, reg)}
				for _, p := range t.StepParams() {
					c.Params = append(c.Params, p.Name)
				}
				v.Callable = c
			}
		}
	}
	var head struct {
		Trigger struct {
			Type string `json:"type"`
		} `json:"trigger"`
	}
	if json.Unmarshal(t.Definition, &head) == nil {
		v.NeedsDeveloper = head.Trigger.Type == "webhook"
	}
	if full {
		v.Definition = t.Definition
	}
	return v
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lib := templates.Default()
	list := lib.Offered()
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		list = nil
		for _, m := range lib.Match(q, 0) {
			list = append(list, m.Template)
		}
	}
	// The sheet-based stock and sales templates give way, for a business
	// keeping stock in a table or recording sales, to stock's own (K5).
	list = templates.For(list, s.templateHolds(r.Context(), principalFrom(r.Context()).TenantID))
	if r.URL.Query().Get("callable") == "true" {
		var kept []*templates.Template
		for _, t := range list {
			if t.Callable {
				kept = append(kept, t)
			}
		}
		list = kept
	}
	if c := r.URL.Query().Get("category"); c != "" {
		var kept []*templates.Template
		for _, t := range list {
			if t.Category == c {
				kept = append(kept, t)
			}
		}
		list = kept
	}
	out := make([]templateView, 0, len(list))
	for _, t := range list {
		out = append(out, viewTemplate(t, reg, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Default().Get(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewTemplate(t, reg, true))
}

// paramsRefused answers a set of parameter values that does not fit.
func paramsRefused(w http.ResponseWriter, errs templates.ErrParams) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_params", "error": errs.Error(), "params": errs})
}

type instantiateReq struct {
	Params map[string]any `json:"params"`
	Name   string         `json:"name,omitempty"`
}

// instantiateTemplate creates a draft workflow from a template.
func (s *Server) instantiateTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Default().Get(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req instantiateReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var wf uuid.UUID
	var doc []byte
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		wf, doc, err = s.instantiateTx(r.Context(), tx, p, t, req.Params, req.Name, func(action, target string, detail map[string]any) error {
			return auditTx(r, tx, action, target, detail)
		})
		return err
	})
	var pe templates.ErrParams
	if errors.As(err, &pe) {
		paramsRefused(w, pe)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": wf, "version": 1, "state": "draft", "template": t.ID,
		"problems": nonNil(s.checkFor(r, doc)), "variables": nonNil(t.Variables), "tables": s.templateTables(r, p, t, req.Params)})
}

// templateTables makes the data tables a template declares, after its
// draft is saved: one that exists already is left as it is, and one that
// cannot be made is reported, never a reason to lose the draft (making a
// table may provision the data project, which no transaction can hold).
func (s *Server) templateTables(r *http.Request, p *Principal, t *templates.Template, params map[string]any) []aiTableResult {
	tables := t.TablesFor(params)
	out := make([]aiTableResult, 0, len(tables))
	for _, tb := range tables {
		res := aiTableResult{Name: tb.Name, Status: "created"}
		req := tableReq{Name: tb.Name, Title: tb.Title, Description: tb.Description}
		for _, c := range tb.Columns {
			req.Columns = append(req.Columns, columnReq{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required, Personal: c.Personal})
		}
		switch _, err := s.newDataTable(r, p, req); {
		case err == nil:
		case errors.Is(err, errConflict) && strings.Contains(err.Error(), "already exists"):
			res.Status = "exists"
			res.Missing = s.tableMissing(r.Context(), p.TenantID, tb.Name, req.Columns)
		case !p.Can(PermTableEdit):
			res.Status, res.Error = "skipped", "making tables needs table.edit: ask an owner or admin to create it on the Tables page"
		default:
			res.Status, res.Error = "failed", errorText(s, r, err)
		}
		out = append(out, res)
	}
	return out
}

// instantiateTx fills a template and saves it as a new draft workflow by
// p, recording the template on the version.
func (s *Server) instantiateTx(ctx context.Context, tx pgx.Tx, p *Principal, t *templates.Template, params map[string]any, name string,
	audit func(action, target string, detail map[string]any) error) (uuid.UUID, []byte, error) {
	if params == nil {
		params = map[string]any{}
	}
	raw, err := t.Instantiate(params, name)
	if err != nil {
		return uuid.Nil, nil, err
	}
	doc, err := canonical(raw)
	if err != nil {
		return uuid.Nil, nil, err
	}
	var d struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(doc, &d)
	wf, err := s.createWorkflowTx(ctx, tx, p, d.Name)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if err := insertVersionTx(ctx, tx, p.TenantID, wf, 1, doc, nil, p.Actor(), ""); err != nil {
		return uuid.Nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET template_id = $2 WHERE workflow_id = $1 AND version = 1`, wf, t.ID); err != nil {
		return uuid.Nil, nil, err
	}
	if err := audit("workflow.create", wf.String(), map[string]any{"name": d.Name, "template": t.ID, "state": "draft"}); err != nil {
		return uuid.Nil, nil, err
	}
	return wf, doc, nil
}

// embedTemplates lists the templates an embed app allows its end users.
func (s *Server) embedTemplates(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []templateView{}
	for _, id := range p.EndUser.AllowedTemplates {
		t, ok := templates.Default().Get(id)
		if !ok {
			continue
		}
		v := viewTemplate(t, reg, false)
		if doc, err := t.Instantiate(t.Examples(), ""); err == nil {
			if bad, err := checkUses(p.EndUser, doc); err != nil || len(bad) > 0 {
				v.Available = false
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// embedInstantiate creates an end user's draft from one of the app's
// templates, refused when the result uses a connector or step type the
// app does not allow.
func (s *Server) embedInstantiate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id := chi.URLParam(r, "id")
	t, ok := templates.Default().Get(id)
	if !ok || !slices.Contains(p.EndUser.AllowedTemplates, id) {
		writeErr(w, http.StatusForbidden, "the app does not allow this template")
		return
	}
	var req instantiateReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	preview, err := t.Instantiate(req.Params, req.Name)
	var pe templates.ErrParams
	if errors.As(err, &pe) {
		paramsRefused(w, pe)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	bad, err := checkUses(p.EndUser, preview)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	if len(bad) > 0 {
		notAllowed(w, bad)
		return
	}
	var wf uuid.UUID
	var doc []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		wf, doc, err = s.instantiateTx(r.Context(), tx, p, t, req.Params, req.Name, func(action, target string, detail map[string]any) error {
			return auditTx(r, tx, action, target, detail)
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": wf, "version": 1, "state": "draft", "template": t.ID, "problems": nonNil(s.checkFor(r, doc))})
}

// checkTemplateIDs refuses ids that are not in the library (embed apps'
// allowed_templates).
func checkTemplateIDs(ids []string) error {
	lib := templates.Default()
	for _, id := range ids {
		if !lib.Has(id) {
			return fmt.Errorf("%w: unknown template %q (GET /v1/templates lists them)", errBadRequest, id)
		}
	}
	return nil
}

// providers are the connectors the tenant can use that declare the
// capability: rails, and for the books capabilities the books (QuickBooks
// and Zoho Books are products: rule 16), sorted.
func providers(reg connector.Lookup, uses string) []string {
	var out []string
	for _, c := range reg.List() {
		_, can := c.Manifest.Capabilities[uses]
		_, sends := c.Manifest.Events[uses] // capability:payout.failed, for a capability_event trigger
		kind := c.Manifest.KindOf()
		if (can || sends) && (kind == "rail" || (kind == "product" && strings.HasPrefix(uses, "books"))) && !slices.Contains(out, c.Manifest.ID) {
			out = append(out, c.Manifest.ID)
		}
	}
	slices.Sort(out)
	return out
}

// isService reports whether a business connects the connector as a
// service of its own, as the Start page counts them: an app (a product,
// such as QuickBooks), or one connected through Taskiem's platform app
// (Slack, Meta, Zoho, Mailchimp: rule 20).
func isService(m *connector.Manifest) bool {
	if m.KindOf() == "product" {
		return true
	}
	if o := m.Auth.OAuth2; o != nil && o.Client != "" {
		return true
	}
	for _, h := range m.Auth.HostModes {
		if h.Client != "" {
			return true
		}
	}
	return false
}

// addService adds a service once.
func addService(list [][]string, by []string) [][]string {
	for _, s := range list {
		if slices.Equal(s, by) {
			return list
		}
	}
	return append(list, by)
}

// templateHolds is which of the templates' replacement conditions hold
// for a business.
func (s *Server) templateHolds(ctx context.Context, tenant uuid.UUID) map[string]bool {
	holds := map[string]bool{}
	// No Google Sheets connection in any environment: the sheet templates'
	// twins are offered instead (decision 0060). Worked out before stock,
	// which a deployment may not have.
	var sheets bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections WHERE tenant_id = $1 AND connector = 'googlesheets' AND status = 'active')`, tenant).Scan(&sheets)
	}); err != nil {
		s.Logger.Warn("templates: reading the Google Sheets connections", "tenant", tenant, "err", err)
		return holds
	}
	holds[templates.WhenNoSheets] = !sheets
	if !s.Stock.Ready() {
		return holds
	}
	holds[templates.WhenNoSheetsStock] = !sheets
	if _, err := s.stockShopOf(ctx, tenant); err == nil {
		holds[templates.WhenKeepsStock] = true
	}
	var sales bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_sales WHERE tenant_id = $1)`, tenant).Scan(&sales)
	}); err == nil && sales {
		holds[templates.WhenRecordsSales] = true
	}
	return holds
}

// tableMissing is the columns of want that the business's table of that
// name lacks (decision 0060: a table a template needs that exists already
// is left as it is, and what it lacks is said). Nothing is said when the
// tables cannot be read.
func (s *Server) tableMissing(ctx context.Context, tenant uuid.UUID, name string, want []columnReq) []string {
	var missing []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tables, err := datatables.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		for _, t := range tables {
			if t.Name != name {
				continue
			}
			have := map[string]bool{}
			for _, c := range t.Columns {
				have[c.Name] = true
			}
			for _, c := range want {
				if !have[c.Name] {
					missing = append(missing, c.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("templates: reading a table's columns", "tenant", tenant, "table", name, "err", err)
		return nil
	}
	return missing
}
