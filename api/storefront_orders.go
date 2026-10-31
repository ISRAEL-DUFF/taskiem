package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// The storefront's forms and the orders they bring (Phase 5, S3;
// docs/storefronts.md#orders).

func (s *Server) listStorefrontActions(w http.ResponseWriter, r *http.Request) {
	var list []storefront.Action
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = storefront.Actions(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return storefrontErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": list})
}

// putStorefrontAction saves a form's settings. On a published storefront
// the page is published again, so its buttons match the forms.
func (s *Server) putStorefrontAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool                     `json:"enabled"`
		Label   string                   `json:"label"`
		Payment string                   `json:"payment"`
		Options storefront.ActionOptions `json:"options"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var a storefront.Action
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if req.Enabled && req.Payment == "at_order" {
			// Payment when ordering: a verified business on a plan with it.
			v, err := verificationOf(r.Context(), tx, p.TenantID)
			if err != nil {
				return err
			}
			if err := s.payOnOrderAllowed(r.Context(), p.TenantID, v); err != nil {
				return storefrontErr(err)
			}
		}
		a, err = storefront.SaveAction(r.Context(), tx, p.TenantID, storefront.Action{Kind: chi.URLParam(r, "kind"), Enabled: req.Enabled, Label: req.Label,
			Payment: req.Payment, Options: req.Options})
		if errors.Is(err, storefront.ErrNoWorkflow) {
			return fmt.Errorf("%w: %s", errConflict, plainError(err))
		}
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
		return auditTx(r, tx, "storefront.action_save", a.Kind, map[string]any{"enabled": a.Enabled, "payment": a.Payment})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) listStorefrontOrders(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	var list []storefront.Order
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.Orders.ListOrders(r.Context(), tx, principalFrom(r.Context()).TenantID, r.URL.Query().Get("status"), r.URL.Query().Get("kind"), limit)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		maskCustomer(&list[i].Customer)
	}
	if list == nil {
		list = []storefront.Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": list})
}

// getStorefrontOrder is one order. The customer's details are masked
// unless the caller holds pii.reveal and asks with ?reveal=true, which is
// audited, as for runs (spec 9.4).
func (s *Server) getStorefrontOrder(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	reveal := r.URL.Query().Get("reveal") == "true"
	if reveal && !principalFrom(r.Context()).Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
		return
	}
	ref := chi.URLParam(r, "reference")
	var o storefront.Order
	var pays []storefront.OrderPayment
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		o, err = s.Orders.GetOrder(r.Context(), tx, principalFrom(r.Context()).TenantID, ref)
		if errors.Is(err, storefront.ErrNoOrder) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		// The money matched to it (docs/matching.md), undone with
		// /v1/money/order-payments/{id}/undo.
		if pays, err = storefront.PaymentsOf(r.Context(), tx, principalFrom(r.Context()).TenantID, o.ID); err != nil || !reveal {
			return err
		}
		return auditTx(r, tx, "pii.reveal", "storefront_order:"+ref, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		maskCustomer(&o.Customer)
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o, "payments": pays, "revealed": reveal})
}

// maskCustomer leaves enough to recognise a customer: a name's first
// letter, a phone's last four digits, an email's first letter and domain.
func maskCustomer(c *storefront.Customer) {
	if r := []rune(c.Name); len(r) > 0 {
		c.Name = string(r[0]) + "•••"
	}
	if len(c.Phone) > 4 {
		c.Phone = "•••" + c.Phone[len(c.Phone)-4:]
	}
	if at := strings.IndexByte(c.Email, '@'); at > 0 {
		c.Email = c.Email[:1] + "•••" + c.Email[at:]
	}
	for _, f := range []*string{&c.Address, &c.Message, &c.BestTime} {
		if *f != "" {
			*f = "•••"
		}
	}
}

// RunStorefrontOrders takes submissions from the inbox every second, asks
// the payment provider about waiting payments every 30 seconds, and every
// 15 applies customers' and owners' changes, sweeps bookings and moves
// riders' deliveries on (offers that ran out, quiet riders).
func (s *Server) RunStorefrontOrders(ctx context.Context) error {
	s.chatHooks()
	if s.Orders == nil {
		<-ctx.Done()
		return nil
	}
	tick, payments, bookings := time.NewTicker(time.Second), time.NewTicker(30*time.Second), time.NewTicker(15*time.Second)
	defer tick.Stop()
	defer payments.Stop()
	defer bookings.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := s.Orders.Drain(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("storefront: inbox", "err", err)
			}
		case <-payments.C:
			if err := s.Orders.CheckPayments(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("storefront: payments", "err", err)
			}
		case <-bookings.C:
			if err := s.Orders.CheckBookings(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("storefront: bookings", "err", err)
			}
			// Offers that ran out, quiet riders, remittances (dispatch).
			if s.Dispatch.Ready() {
				if err := s.Dispatch.Sweep(ctx); err != nil && ctx.Err() == nil {
					s.Logger.Error("dispatch: sweep", "err", err)
				}
			}
		}
	}
}
