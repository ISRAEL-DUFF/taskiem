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
		// An order form that sells (forms-that-sell §5.1): one product in
		// packages, or a basket of some items, with its notice.
		Sells  *storefront.SellSpec `json:"sells"`
		Notice string               `json:"notice"`
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
	var warnings []string
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if req.Kind == "order" {
			if req.Sells == nil {
				return fmt.Errorf("%w: say what the order form sells: a product in packages, or items", errBadRequest)
			}
			a, warnings, err = storefront.AddOrderAction(r.Context(), tx, principalFrom(r.Context()).TenantID, front, storefront.NewOrderForm{Label: req.Label,
				Sells: *req.Sells, Notice: req.Notice})
		} else {
			a, err = storefront.AddFrontAction(r.Context(), tx, principalFrom(r.Context()).TenantID, front, req.Kind, req.Label)
		}
		if err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.action_add", a.ID.String(), map[string]any{"front": front, "kind": a.Kind, "slug": a.Slug})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Kind == "order" {
		writeJSON(w, http.StatusCreated, map[string]any{"action": a, "warnings": nonNilStrings(warnings)})
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// sellableItems answers GET /v1/storefront/fronts/{id}/sellable: the
// products an order form on the front can sell, as the page has them (its
// copy): id, name, price, photo and stock left, for choosing one to sell
// in packages (forms-that-sell §5.1).
func (s *Server) sellableItems(w http.ResponseWriter, r *http.Request) {
	front, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	type sellable struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		PriceKobo *int64 `json:"price_kobo"`
		Photo     string `json:"photo,omitempty"`
		Stock     *int64 `json:"stock"`
		Sizes     int    `json:"sizes"`
	}
	out := []sellable{}
	err = s.tx(r, func(tx pgx.Tx) error {
		items, _, err := storefront.FrontItems(r.Context(), tx, principalFrom(r.Context()).TenantID, front)
		if err != nil {
			return storefrontErr(err)
		}
		for _, it := range storefront.PublicItems(items) {
			if !it.Available || it.PriceKobo == nil {
				continue
			}
			out = append(out, sellable{ID: it.ID, Name: it.Name, PriceKobo: it.PriceKobo, Photo: it.Photo["320"], Stock: it.Stock, Sizes: len(it.Sizes)})
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// putActionSells answers PUT /v1/storefront/actions/{id}/sells: what an
// order form sells (its packages, or its basket's items) and its notice
// (forms-that-sell §5.2), keeping its own questions. A published page is
// published again, and the answer carries what the packages say against
// the list's prices.
func (s *Server) putActionSells(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sells  storefront.SellSpec `json:"sells"`
		Notice *string             `json:"notice"`
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
	p := principalFrom(r.Context())
	var warnings []string
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if warnings, err = storefront.SetSells(r.Context(), tx, p.TenantID, id, req.Sells, req.Notice); err != nil {
			return storefrontErr(err)
		}
		if err := auditTx(r, tx, "storefront.action_sells", id.String(), map[string]any{"sells": req.Sells}); err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "warnings": nonNilStrings(warnings)})
}

// getStorefrontEmbed answers GET /v1/storefront/embed?action=<id>: the
// code that shows one order form on the business's own website
// (forms-that-sell §5.5), as a frame that grows with the form (with the
// few lines that hear its height) or a plain frame of a fixed height.
func (s *Server) getStorefrontEmbed(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.URL.Query().Get("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "name the order form: ?action=<id>")
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	err = s.tx(r, func(tx pgx.Tx) error {
		var front, slug, label string
		var enabled bool
		if err := tx.QueryRow(r.Context(), `SELECT f.slug, a.slug, a.label, a.enabled FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id
			WHERE a.id = $1 AND a.tenant_id = $2`, id, p.TenantID).Scan(&front, &slug, &label, &enabled); err != nil {
			return err
		}
		sf, err := storefront.Get(r.Context(), tx, p.TenantID)
		if err != nil {
			return storefrontErr(err)
		}
		base := sf.URL(s.previewDomain())
		if sf.PublishedVersion == 0 || base == "" {
			return fmt.Errorf("%w: publish your page first: the form is shown from it", errConflict)
		}
		frame := actionURL(base, front, slug, true)
		out = map[string]any{"action_id": id, "label": label, "on": enabled, "url": actionURL(base, front, slug, false), "embed_url": frame,
			"frame": EmbedFrame(frame, label, "taskiem-"+slug), "script": EmbedScript(frame, "taskiem-"+slug), "plain": EmbedPlain(frame, label)}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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
