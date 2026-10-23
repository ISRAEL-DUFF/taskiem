package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Matching money to invoices and storefront orders (decision 0045, T5;
// docs/matching.md): the suggestions waiting for a person, confirming or
// rejecting one, and matching a payment by hand. Undoing a match to an
// invoice is undoing the invoice's payment
// (/v1/invoices/{id}/payments/{payment}/undo); undoing one to an order is
// /v1/money/order-payments/{id}/undo.

// matchingRoutes mounts /v1/money inside the authenticated group.
func (s *Server) matchingRoutes(r chi.Router) {
	read, manage := s.need(PermInvoiceRead), s.need(PermInvoiceManage)
	r.With(read).Get("/money/suggestions", s.listSuggestions)
	r.With(read).Get("/money/unmatched", s.listUnmatched)
	r.With(manage, s.tenantWide).Post("/money/suggestions/{id}/confirm", s.confirmSuggestion)
	r.With(manage, s.tenantWide).Post("/money/suggestions/{id}/reject", s.rejectSuggestion)
	r.With(manage, s.tenantWide).Post("/money/entries/{id}/match", s.matchEntry)
	r.With(manage, s.tenantWide).Post("/money/order-payments/{id}/undo", s.undoOrderPayment)
}

// matcher is Server.Matcher, or one made from Invoices and Orders, asking
// in chat about what it suggests (whatsapp_matching.go) unless it already
// has a Suggested hook.
func (s *Server) matcher() *matching.Matcher {
	m := matching.Matcher{Invoices: s.Invoices, PII: s.Store.PII}
	if s.Matcher != nil {
		m = *s.Matcher
	}
	if m.Orders == nil {
		m.Orders = s.Orders
	}
	if m.Suggested == nil {
		m.Suggested = s.MatchSuggested
	}
	return &m
}

// revealing reads ?reveal=true: customers' names are masked unless the
// caller holds pii.reveal and asks, which is audited (target names what).
func revealing(w http.ResponseWriter, r *http.Request) (bool, bool) {
	reveal := r.URL.Query().Get("reveal") == "true"
	if reveal && !principalFrom(r.Context()).Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
		return false, false
	}
	return reveal, true
}

// maskName leaves a name's first letter, as invoices and orders do.
func maskName(name string) string {
	if r := []rune(name); len(r) > 0 {
		return string(r[0]) + "•••"
	}
	return ""
}

func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	reveal, ok := revealing(w, r)
	if !ok {
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var list []matching.Waiting
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = matching.Open(r.Context(), tx, tenant, 100); err != nil {
			return err
		}
		// Who each is from: the invoice's or order's customer.
		for i := range list {
			switch {
			case list[i].InvoiceID != nil:
				x, err := s.Invoices.Get(r.Context(), tx, tenant, list[i].InvoiceID.String())
				if err != nil {
					return err
				}
				list[i].Customer = x.Customer.Name
			case list[i].OrderID != nil && s.Orders != nil:
				o, err := s.Orders.GetOrder(r.Context(), tx, tenant, list[i].Number)
				if err != nil && !errors.Is(err, storefront.ErrNoOrder) {
					return err
				}
				list[i].Customer = o.Customer.Name
			}
		}
		if !reveal {
			return nil
		}
		return auditTx(r, tx, "pii.reveal", "money_suggestions", nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		for i := range list {
			list[i].Customer = maskName(list[i].Customer)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": list, "revealed": reveal})
}

func (s *Server) confirmSuggestion(w http.ResponseWriter, r *http.Request) {
	s.decideSuggestion(w, r, func(ctx context.Context, tenant, id uuid.UUID, by string) (*matching.Result, error) {
		x, err := s.matcher().Confirm(ctx, tenant, id, by, false)
		return &x, err
	})
}

func (s *Server) rejectSuggestion(w http.ResponseWriter, r *http.Request) {
	s.decideSuggestion(w, r, func(ctx context.Context, tenant, id uuid.UUID, by string) (*matching.Result, error) {
		return nil, s.matcher().Reject(ctx, tenant, id, by)
	})
}

func (s *Server) decideSuggestion(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, tenant, id uuid.UUID, by string) (*matching.Result, error)) {
	if !s.invoicesOn(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	x, err := do(r.Context(), p.TenantID, id, p.Actor())
	if err != nil {
		s.fail(w, r, invoiceErr(err))
		return
	}
	if x == nil {
		writeJSON(w, http.StatusOK, map[string]any{"rejected": true, "at": time.Now().UTC()})
		return
	}
	s.writeMatched(w, r, *x)
}

// writeMatched answers a match: the invoice (as GET /v1/invoices/{id}
// does), or the order matched, customers masked.
func (s *Server) writeMatched(w http.ResponseWriter, r *http.Request, x matching.Result) {
	switch {
	case x.Invoice != nil:
		maskInvoice(x.Invoice)
		writeJSON(w, http.StatusOK, x.Invoice)
	case x.Order != nil:
		s.writeMatchedOrder(w, r, *x.Order)
	default:
		s.fail(w, r, errors.New("api: a match with no invoice or order"))
	}
}

// writeMatchedOrder answers with an order and its payments (MatchedOrder).
func (s *Server) writeMatchedOrder(w http.ResponseWriter, r *http.Request, o storefront.Order) {
	var pays []storefront.OrderPayment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		pays, err = storefront.PaymentsOf(r.Context(), tx, principalFrom(r.Context()).TenantID, o.ID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": matching.OrderTarget(o.Reference), "reference": o.Reference, "status": o.Status, "paid_at": o.PaidAt,
		"due_kobo": o.Payable(), "total_kobo": o.TotalKobo, "customer": maskName(o.Customer.Name), "payments": pays})
}

func (s *Server) matchEntry(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Invoice string `json:"invoice"`
		Order   string `json:"order"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	target := req.Invoice
	switch {
	case (req.Invoice == "") == (req.Order == ""):
		s.fail(w, r, fmt.Errorf("%w: give invoice (the invoice's id or number) or order (the order's reference)", errBadRequest))
		return
	case req.Order != "":
		target = matching.OrderTarget(req.Order)
	}
	p := principalFrom(r.Context())
	x, err := s.matcher().Match(r.Context(), p.TenantID, id, target, p.Actor())
	if err != nil {
		s.fail(w, r, invoiceErr(err))
		return
	}
	s.writeMatched(w, r, x)
}

// undoOrderPayment serves POST /v1/money/order-payments/{id}/undo: a
// payment counted towards an order is taken out; the order goes back to
// unpaid, or the undo is refused (409) when the order went on.
func (s *Server) undoOrderPayment(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	o, err := s.matcher().UndoOrderPayment(r.Context(), p.TenantID, id, p.Actor())
	if err != nil {
		s.fail(w, r, invoiceErr(err))
		return
	}
	s.writeMatchedOrder(w, r, o)
}
