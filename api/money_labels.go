package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/spend"
)

// Spend labels (docs/spec/what-a-business-buys.md §5.4, U1): the
// business's own words for what its money out was for. Anyone who may see
// money or stock sees them, with suggestions for a picker; anyone who may
// enter money or draft an order makes one by typing it; those who manage
// money (invoice.manage) rename and merge them, from Money settings. The
// money log is never rewritten: reports find each entry's label by id.

const (
	permLabelRead = access.InvoiceRead + "|" + access.TableRead
	permLabelMake = access.InvoiceManage + "|" + access.TableEdit
)

func (s *Server) moneyLabelRoutes(r chi.Router) {
	r.With(s.need(permLabelRead)).Get("/money/labels", s.listLabels)
	r.With(s.need(permLabelMake), s.tenantWide).Post("/money/labels", s.makeLabel)
	r.With(s.need(PermInvoiceManage), s.tenantWide).Patch("/money/labels/{id}", s.renameLabel)
	r.With(s.need(PermInvoiceManage), s.tenantWide).Post("/money/labels/{id}/merge", s.mergeLabel)
}

func labelErr(err error) error {
	switch {
	case errors.Is(err, spend.ErrNoLabel):
		return pgx.ErrNoRows
	case errors.Is(err, spend.ErrLabel):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "spend: label: "))
	}
	return err
}

// listLabels serves GET /v1/money/labels (?item=, ?category=): the
// labels not merged, the most used first, and what to offer for an item
// bought (spend.Suggest).
func (s *Server) listLabels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	item, category := strings.TrimSpace(q.Get("item")), strings.TrimSpace(q.Get("category"))
	if len(item) > 200 || len(category) > 120 {
		s.fail(w, r, fmt.Errorf("%w: item is at most 200 characters, category 120", errBadRequest))
		return
	}
	var list []spend.Label
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = spend.ListTx(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": list, "suggestions": spend.Suggest(list, item, category, 4)})
}

type labelReq struct {
	Name string `json:"name"`
}

// makeLabel serves POST /v1/money/labels {name}: the label of that name,
// made when the business has none (201), else the one it has (200),
// without case; a merged label's name answers the label it was merged
// into.
func (s *Server) makeLabel(w http.ResponseWriter, r *http.Request) {
	var req labelReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var l spend.Label
	var made bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if l, made, err = spend.FindOrMakeTx(r.Context(), tx, p.TenantID, req.Name, p.Actor()); err != nil || !made {
			return err
		}
		return auditTx(r, tx, "money.label.made", l.Name, map[string]any{"label_id": l.ID})
	}); err != nil {
		s.fail(w, r, labelErr(err))
		return
	}
	status := http.StatusOK
	if made {
		status = http.StatusCreated
	}
	writeJSON(w, status, l)
}

func labelID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

// renameLabel serves PATCH /v1/money/labels/{id} {name}.
func (s *Server) renameLabel(w http.ResponseWriter, r *http.Request) {
	id, err := labelID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req labelReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var l spend.Label
	if err := s.tx(r, func(tx pgx.Tx) error {
		was, err := spend.LabelTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		if err != nil {
			return err
		}
		if l, err = spend.RenameTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id, req.Name); err != nil || l.Name == was.Name {
			return err
		}
		return auditTx(r, tx, "money.label.renamed", l.Name, map[string]any{"label_id": l.ID, "was": was.Name})
	}); err != nil {
		s.fail(w, r, labelErr(err))
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// mergeLabel serves POST /v1/money/labels/{id}/merge {into}: the label
// is merged into another, kept; its spending reads under that one.
func (s *Server) mergeLabel(w http.ResponseWriter, r *http.Request) {
	id, err := labelID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Into uuid.UUID `json:"into"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Into == uuid.Nil {
		s.fail(w, r, fmt.Errorf("%w: into is the label to keep", errBadRequest))
		return
	}
	var l spend.Label
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if l, err = spend.MergeTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id, req.Into); err != nil {
			return err
		}
		return auditTx(r, tx, "money.label.merged", l.Name, map[string]any{"label_id": l.ID, "into": l.MergedInto})
	}); err != nil {
		s.fail(w, r, labelErr(err))
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// expenseLabel finds the label an expense entered names, by id (the label
// kept, when it was merged) or by name (found or made): its id and name
// go on the entry. Cash takes no label.
func expenseLabel(r *http.Request, tx pgx.Tx, kind string, id *uuid.UUID, name string) (*spend.Label, error) {
	name = strings.TrimSpace(name)
	if id == nil && name == "" {
		return nil, nil
	}
	if kind != "expense" {
		return nil, fmt.Errorf("%w: only an expense takes a label", errBadRequest)
	}
	p := principalFrom(r.Context())
	if id != nil {
		l, err := spend.KeptTx(r.Context(), tx, p.TenantID, *id)
		if errors.Is(err, spend.ErrNoLabel) {
			return nil, fmt.Errorf("%w: no such label", errBadRequest)
		}
		return &l, err
	}
	l, made, err := spend.FindOrMakeTx(r.Context(), tx, p.TenantID, name, p.Actor())
	if err != nil {
		return nil, labelErr(err)
	}
	if made {
		if err := auditTx(r, tx, "money.label.made", l.Name, map[string]any{"label_id": l.ID}); err != nil {
			return nil, err
		}
	}
	return &l, nil
}
