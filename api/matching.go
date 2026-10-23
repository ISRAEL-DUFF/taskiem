package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
)

// Matching money to invoices (decision 0045, T5; docs/matching.md): the
// suggestions waiting for a person, confirming or rejecting one, and
// matching a payment by hand. Undoing a match is undoing the invoice's
// payment (/v1/invoices/{id}/payments/{payment}/undo).

// matchingRoutes mounts /v1/money inside the authenticated group.
func (s *Server) matchingRoutes(r chi.Router) {
	read, manage := s.need(PermInvoiceRead), s.need(PermInvoiceManage)
	r.With(read).Get("/money/suggestions", s.listSuggestions)
	r.With(read).Get("/money/unmatched", s.listUnmatched)
	r.With(manage, s.tenantWide).Post("/money/suggestions/{id}/confirm", s.confirmSuggestion)
	r.With(manage, s.tenantWide).Post("/money/suggestions/{id}/reject", s.rejectSuggestion)
	r.With(manage, s.tenantWide).Post("/money/entries/{id}/match", s.matchEntry)
}

// matcher is Server.Matcher, or one made from Invoices, asking in chat
// about what it suggests (whatsapp_matching.go) unless it already has a
// Suggested hook.
func (s *Server) matcher() *matching.Matcher {
	m := matching.Matcher{Invoices: s.Invoices, PII: s.Store.PII}
	if s.Matcher != nil {
		m = *s.Matcher
	}
	if m.Suggested == nil {
		m.Suggested = s.MatchSuggested
	}
	return &m
}

func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var list []matching.Waiting
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = matching.Open(r.Context(), tx, principalFrom(r.Context()).TenantID, 100)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": list})
}

func (s *Server) confirmSuggestion(w http.ResponseWriter, r *http.Request) {
	s.decideSuggestion(w, r, func(ctx context.Context, tenant, id uuid.UUID, by string) (*invoicing.Invoice, error) {
		x, err := s.matcher().Confirm(ctx, tenant, id, by, false)
		return &x, err
	})
}

func (s *Server) rejectSuggestion(w http.ResponseWriter, r *http.Request) {
	s.decideSuggestion(w, r, func(ctx context.Context, tenant, id uuid.UUID, by string) (*invoicing.Invoice, error) {
		return nil, s.matcher().Reject(ctx, tenant, id, by)
	})
}

func (s *Server) decideSuggestion(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, tenant, id uuid.UUID, by string) (*invoicing.Invoice, error)) {
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
	maskInvoice(x)
	writeJSON(w, http.StatusOK, x)
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
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Invoice == "" {
		s.fail(w, r, fmt.Errorf("%w: invoice is the invoice's id or number", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	x, err := s.matcher().Match(r.Context(), p.TenantID, id, req.Invoice, p.Actor())
	if err != nil {
		s.fail(w, r, invoiceErr(err))
		return
	}
	maskInvoice(&x)
	writeJSON(w, http.StatusOK, x)
}
