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
	// Steps are the template, filled with its examples, read as plain
	// numbered steps.
	Steps []wdtext.Line `json:"steps"`
	// Available: every connector it uses is available to the tenant.
	Available  bool            `json:"available"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

func viewTemplate(t *templates.Template, reg connector.Lookup, full bool) templateView {
	v := templateView{ID: t.ID, Title: t.Title, Summary: t.Summary, Description: t.Description, Category: t.Category,
		Tags: nonNil(t.Tags), Connectors: nonNil(t.Connectors), Variables: nonNil(t.Variables), Params: nonNil(t.Params),
		Steps: []wdtext.Line{}, Available: true}
	for _, c := range t.Connectors {
		if _, ok := reg.Get(c); !ok {
			v.Available = false
		}
	}
	if doc, err := t.Instantiate(t.Examples(), ""); err == nil {
		if def, err := wd.Load(doc); err == nil {
			v.Steps = wdtext.Describe(def, reg)
		}
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
	list := lib.List()
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		list = nil
		for _, m := range lib.Match(q, 0) {
			list = append(list, m.Template)
		}
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
		"problems": nonNil(s.checkFor(r, doc)), "variables": nonNil(t.Variables)})
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
