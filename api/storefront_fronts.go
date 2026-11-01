package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Fronts in the web app (docs/spec/fronts.md F3, §7.1): the storefront's
// pages of things and actions. A front is made from a kind (§6): its table
// (a known table, made for the business), a list of it, and its actions,
// off. Turning a front on, or changing one on a live page, publishes the
// page again with its checks.

// frontView is a front as the Fronts tab shows it.
type frontView struct {
	storefront.Front
	Blocks  []storefront.Block  `json:"blocks"`
	Actions []storefront.Action `json:"actions"`
	// Table is the front's list's table and Items how many it shows.
	Table *uuid.UUID `json:"table_id"`
	Items int        `json:"items"`
}

type kindView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Line  string `json:"line"`
}

func kindViews() []kindView {
	var out []kindView
	for _, k := range storefront.FrontKinds() {
		out = append(out, kindView{k.Name, k.Title, k.Line})
	}
	return out
}

// frontViews reads the storefront's fronts with their blocks, actions and
// item counts.
func frontViews(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]frontView, error) {
	fronts, err := storefront.Fronts(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	actions, err := storefront.Actions(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]frontView, 0, len(fronts))
	for _, f := range fronts {
		v := frontView{Front: f, Actions: []storefront.Action{}}
		if v.Blocks, err = storefront.Blocks(ctx, tx, f.ID); err != nil {
			return nil, err
		}
		for _, a := range actions {
			// The built-in forms not saved yet belong to the home page.
			if (a.FrontID != nil && *a.FrontID == f.ID) || (a.FrontID == nil && f.Slug == "") {
				v.Actions = append(v.Actions, a)
			}
		}
		for _, b := range v.Blocks {
			var lc storefront.ListConfig
			if b.Kind != "list" || json.Unmarshal(b.Config, &lc) != nil || lc.Table == nil {
				continue
			}
			v.Table = lc.Table
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM storefront_catalogue c JOIN storefronts s ON s.id = c.storefront_id
				WHERE s.tenant_id = $1 AND c.table_name = $2`, tenant, lc.TableName).Scan(&v.Items); err != nil {
				return nil, err
			}
			break
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) listFronts(w http.ResponseWriter, r *http.Request) {
	var list []frontView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = frontViews(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return storefrontErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": kindViews(), "fronts": list})
}

// addFront answers POST /v1/storefront/fronts {"name", "kind"}: the kind's
// table made for the business (named for the kind, or the next free name),
// then the front, off, with its list and actions.
func (s *Server) addFront(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	k, ok := storefront.FrontKindOf(req.Kind)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: no kind of front %q", errBadRequest, req.Kind))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := storefront.Get(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		if errors.Is(err, storefront.ErrNotFound) {
			err = fmt.Errorf("%w: make your page first", errConflict)
		}
		s.fail(w, r, err)
		return
	}
	var v frontView
	err := s.makeFront(r.Context(), p, req.Name, k, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}, func(tx pgx.Tx, f storefront.Front) error {
		list, err := frontViews(r.Context(), tx, p.TenantID)
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.ID == f.ID {
				v = x
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// makeFront makes a front of a kind for the web app and chat alike: the
// kind's table made for the business (named for the kind, or the next free
// name; it needs table.edit), then the front, off, with its list and its
// actions, audited; then, in the same transaction, done.
func (s *Server) makeFront(ctx context.Context, p *Principal, name string, k storefront.FrontKind,
	audit func(tx pgx.Tx, action, target string, detail map[string]any) error, done func(tx pgx.Tx, f storefront.Front) error) error {
	in := storefront.NewFront{Name: name, Kind: k.Name}
	if k.Table != "" {
		if !p.Can(PermTableEdit) {
			return fmt.Errorf("%w: a %s front makes a table, which needs %s", errForbidden, k.Title, PermTableEdit)
		}
		kt, ok := templates.KnownTable(k.Table)
		if !ok {
			return fmt.Errorf("the kind %s names no known table %s", k.Name, k.Table)
		}
		t := datatables.Table{Name: kt.Name, Title: kt.Title, Description: kt.Description}
		for _, col := range kt.Columns {
			dc := datatables.Column{Name: col.Name, Title: col.Title, Type: col.Type, Required: col.Required}
			if col.Personal != nil {
				dc.Personal = *col.Personal
			}
			t.Columns = append(t.Columns, dc)
		}
		names := []string{kt.Name}
		for n := 2; n <= 9; n++ {
			names = append(names, fmt.Sprintf("%s_%d", kt.Name, n))
		}
		if err := s.makeOwnTable(ctx, p.TenantID, p.Actor(), audit, &t, names...); err != nil {
			return storefrontErr(fmt.Errorf("%w: the table could not be made: %s", errConflict, plainError(err)))
		}
		in.Table, in.TableName = &t.ID, t.Name
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		f, err := storefront.CreateFront(ctx, tx, p.TenantID, in)
		if err != nil {
			return storefrontErr(err)
		}
		if err := audit(tx, "storefront.front_create", f.Slug, map[string]any{"kind": f.Kind, "name": f.Name}); err != nil {
			return err
		}
		return done(tx, f)
	})
}

// patchFront changes a front's name, line, address, place or switch; on a
// live page the page is published again, with its checks, so a front is
// turned on only when the page may show it.
func (s *Server) patchFront(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req storefront.FrontPatch
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var v frontView
	err = s.tx(r, func(tx pgx.Tx) error {
		f, err := storefront.UpdateFront(r.Context(), tx, p.TenantID, id, req)
		if err != nil {
			return storefrontErr(err)
		}
		sf, err := storefront.Get(r.Context(), tx, p.TenantID)
		if err != nil {
			return storefrontErr(err)
		}
		if sf.PublishedVersion > 0 {
			if _, err := s.publishGated(r.Context(), tx, p.TenantID, p.Actor()); err != nil {
				return storefrontErr(err)
			}
		}
		list, err := frontViews(r.Context(), tx, p.TenantID)
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.ID == f.ID {
				v = x
			}
		}
		return auditTx(r, tx, "storefront.front_save", f.Slug, map[string]any{"enabled": f.Enabled})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteFront(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := storefront.DeleteFront(r.Context(), tx, principalFrom(r.Context()).TenantID, id); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.front_delete", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// actionTemplates are the workflow templates that start on each kind of
// action (§8).
var actionTemplates = map[string]string{"order": "storefront-order", "enquiry": "storefront-enquiry", "callback": "storefront-callback", "booking": "storefront-booking", "hire": "storefront-hire",
	"space": "storefront-space"}

// setupAction answers POST /v1/storefront/actions/{id}/setup: the action's
// default workflow from its kind's template, its trigger naming the action
// by id, published (or asked for, under four-eyes). An action with a
// workflow already is answered as it is.
func (s *Server) setupAction(w http.ResponseWriter, r *http.Request) {
	// The route's segment is the actions route's, a kind or an id; only an
	// id is set up here.
	id, err := uuid.Parse(chi.URLParam(r, "kind"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	var wf uuid.UUID
	held := false
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		wf, held, err = s.setupActionTx(r.Context(), tx, p, id, func(action, target string, detail map[string]any) error {
			return auditTx(r, tx, action, target, detail)
		})
		return err
	})
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	if held {
		writeJSON(w, http.StatusOK, map[string]any{"workflow_id": wf, "state": "published"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"workflow_id": wf, "state": "draft", "version": 1})
}

// setupActionTx makes an action's default workflow from its kind's
// template, its trigger naming the action by id, as version 1 to publish;
// held is set, and nothing made, when a workflow is deployed for it
// already.
func (s *Server) setupActionTx(ctx context.Context, tx pgx.Tx, p *Principal, id uuid.UUID, audit func(action, target string, detail map[string]any) error) (uuid.UUID, bool, error) {
	var kind, label, front string
	if err := tx.QueryRow(ctx, `SELECT a.kind, a.label, f.name FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id WHERE a.id = $1`, id).
		Scan(&kind, &label, &front); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, storefront.ErrNotFound
		}
		return uuid.Nil, false, err
	}
	var wf uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workflow_id FROM triggers WHERE type = 'storefront' AND storefront_action_id = $1 AND environment = 'prod'`, id).
		Scan(&wf); err == nil {
		return wf, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	tpl, ok := templates.Default().Get(actionTemplates[kind])
	if !ok {
		return uuid.Nil, false, fmt.Errorf("%w: no workflow template for a %s", errConflict, kind)
	}
	named, err := templateForAction(tpl, id)
	if err != nil {
		return uuid.Nil, false, err
	}
	wf, _, err = s.instantiateTx(ctx, tx, p, named, map[string]any{}, front+": "+label, audit)
	return wf, false, err
}

// templateForAction is a template whose storefront trigger names the
// action by id.
func templateForAction(tpl *templates.Template, id uuid.UUID) (*templates.Template, error) {
	named := *tpl
	var def map[string]any
	if err := json.Unmarshal(tpl.Definition, &def); err != nil {
		return nil, err
	}
	trig, _ := def["trigger"].(map[string]any)
	cfg, _ := trig["config"].(map[string]any)
	if cfg == nil {
		return nil, fmt.Errorf("the template %s has no storefront trigger", tpl.ID)
	}
	cfg["action_id"] = id.String()
	var err error
	if named.Definition, err = json.Marshal(def); err != nil {
		return nil, err
	}
	return &named, nil
}
