package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Quotes and Custom fronts (docs/spec/fronts.md §6, F7).

// quoteStorefrontOrder answers POST /v1/storefront/orders/{reference}/quote:
// the owner prices a quote request; the customer is sent it by the
// request's workflow and accepts or declines it on their page.
func (s *Server) quoteStorefrontOrder(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		PriceKobo int64 `json:"price_kobo"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	o, err := s.Orders.Quote(r.Context(), p.TenantID, chi.URLParam(r, "reference"), req.PriceKobo, p.Actor())
	switch {
	case errors.Is(err, storefront.ErrNoOrder):
		err = pgx.ErrNoRows
	case errors.Is(err, storefront.ErrDecision):
		err = fmt.Errorf("%w: %s", errConflict, plainError(err))
	case errors.Is(err, storefront.ErrInvalid):
		err = fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	maskCustomer(&o.Customer)
	writeJSON(w, http.StatusOK, map[string]any{"order": o})
}

// addFrontAction answers POST /v1/storefront/fronts/{id}/actions: a form
// added to a front, off (a blank form, a question, a call-back or a quote
// request). Set it up and turn it on as any form is.
func (s *Server) addFrontAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	front, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var a storefront.Action
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if a, err = storefront.AddFrontAction(r.Context(), tx, principalFrom(r.Context()).TenantID, front, req.Kind, req.Label); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.action_add", a.ID.String(), map[string]any{"front": front, "kind": a.Kind, "slug": a.Slug})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// putActionForm answers PUT /v1/storefront/actions/{id}/form: a plain
// form's own questions, as a JSON Schema object of questions ({"form":
// null} puts back its kind's). A published page is published again, with
// its checks: a question a live workflow reads cannot be dropped.
func (s *Server) putActionForm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Form json.RawMessage `json:"form"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	form := []byte(req.Form)
	if string(form) == "null" {
		form = nil
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := storefront.SetActionForm(r.Context(), tx, id, form); err != nil {
			return storefrontErr(err)
		}
		if err := auditTx(r, tx, "storefront.action_form", id.String(), map[string]any{"questions": storefront.FormFields(form)}); err != nil {
			return err
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
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "questions": storefront.FormFields(form)})
}
