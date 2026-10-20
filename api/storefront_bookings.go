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

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Storefront bookings (docs/storefronts.md#bookings): the services table
// and availability, the services copy, the bookings list, and the owner's
// changes from the web app (the same as from chat).

// tableColumns reads a table's column types for checking a mapping;
// personal columns read as "personal", which no mapping accepts.
func tableColumns(t datatables.Table) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, c := range t.Columns {
			if c.Name == name {
				if c.Personal {
					return "personal", true
				}
				return c.Type, true
			}
		}
		return "", false
	}
}

// putStorefrontBookings sets the services table, its mapping and the
// availability, refreshes the services copy, and republishes a published
// storefront so its page lists them.
func (s *Server) putStorefrontBookings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServicesTable   *uuid.UUID              `json:"services_table"`
		ServicesColumns storefront.Columns      `json:"services_columns"`
		Availability    storefront.Availability `json:"availability"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var sf storefront.Storefront
	err := s.tx(r, func(tx pgx.Tx) error {
		if req.ServicesTable != nil {
			t, err := datatables.GetTable(r.Context(), tx, *req.ServicesTable)
			if err != nil {
				return fmt.Errorf("%w: the services table: %w", errBadRequest, err)
			}
			if err := req.ServicesColumns.CheckServices(tableColumns(t)); err != nil {
				return storefrontErr(err)
			}
		} else {
			req.ServicesColumns = storefront.Columns{}
		}
		var err error
		if sf, err = storefront.SaveBookings(r.Context(), tx, p.TenantID, req.ServicesTable, req.ServicesColumns, req.Availability); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.bookings_save", sf.Slug, map[string]any{"auto_confirm": req.Availability.AutoConfirm})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.SyncStorefront(r.Context(), p.TenantID); err != nil {
		s.fail(w, r, storefrontErr(fmt.Errorf("%w: the services could not be read: %w", errConflict, err)))
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(r.Context(), tx, p.TenantID); err != nil || sf.PublishedVersion == 0 {
			return storefrontErr(err)
		}
		sf, err = storefront.Publish(r.Context(), tx, p.TenantID, s.previewDomain(), p.Actor())
		return storefrontErr(err)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontOut(sf))
}

func (s *Server) getStorefrontServices(w http.ResponseWriter, r *http.Request) {
	var sf storefront.Storefront
	var items []storefront.Item
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		tenant := principalFrom(r.Context()).TenantID
		if sf, err = storefront.Get(r.Context(), tx, tenant); err != nil {
			return storefrontErr(err)
		}
		items, err = storefront.Services(r.Context(), tx, tenant)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []storefront.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "availability": sf.Availability.Effective(sf.Draft.Hours), "synced_at": sf.CatalogueSynced,
		"error": sf.CatalogueError})
}

// listStorefrontBookings lists the bookings holding their time between
// from and to (default: today and the next 30 days), customers masked.
func (s *Server) listStorefrontBookings(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	now := time.Now().In(storefront.Lagos)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
	to := from.AddDate(0, 0, 31)
	for _, f := range []struct {
		name string
		v    *time.Time
	}{{"from", &from}, {"to", &to}} {
		if q := r.URL.Query().Get(f.name); q != "" {
			t, err := time.ParseInLocation(time.DateOnly, q, storefront.Lagos)
			if err != nil {
				writeErr(w, http.StatusBadRequest, f.name+" is a date like 2026-10-24")
				return
			}
			*f.v = t
		}
	}
	if !to.After(from) || to.Sub(from) > 92*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "to must be after from, at most 92 days on")
		return
	}
	var list []storefront.Order
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.Orders.Bookings(r.Context(), tx, principalFrom(r.Context()).TenantID, from, to)
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
	writeJSON(w, http.StatusOK, map[string]any{"bookings": list})
}

// cancelStorefrontBooking and moveStorefrontBooking are the owner's
// changes; the run is moved on and the customer told by the order sweeper.
func (s *Server) cancelStorefrontBooking(w http.ResponseWriter, r *http.Request) {
	s.changeBooking(w, r, func(ctx context.Context, tenant uuid.UUID, ref, actor string) (storefront.Order, error) {
		return s.Orders.OwnerCancel(ctx, tenant, ref, actor)
	})
}

func (s *Server) moveStorefrontBooking(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StartsAt time.Time `json:"starts_at"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	s.changeBooking(w, r, func(ctx context.Context, tenant uuid.UUID, ref, actor string) (storefront.Order, error) {
		return s.Orders.OwnerMove(ctx, tenant, ref, req.StartsAt, actor)
	})
}

func (s *Server) changeBooking(w http.ResponseWriter, r *http.Request, fn func(context.Context, uuid.UUID, string, string) (storefront.Order, error)) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	o, err := fn(r.Context(), p.TenantID, chi.URLParam(r, "reference"), p.Actor())
	switch {
	case errors.Is(err, storefront.ErrNoOrder):
		err = pgx.ErrNoRows
	case errors.Is(err, storefront.ErrSlotTaken), errors.Is(err, storefront.ErrDecision):
		err = fmt.Errorf("%w: %s", errConflict, plainError(err))
	default:
		err = storefrontErr(err)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	maskCustomer(&o.Customer)
	writeJSON(w, http.StatusOK, map[string]any{"booking": o})
}

// syncServices refreshes the services copy from the services table; a
// failure keeps the old copy and is recorded beside the catalogue's.
func (s *Server) syncServices(ctx context.Context, tenant uuid.UUID, sf storefront.Storefront) error {
	if sf.ServicesTable == nil {
		return nil
	}
	var table datatables.Table
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		table, err = datatables.GetTable(ctx, tx, *sf.ServicesTable)
		return err
	}); err != nil {
		return err
	}
	rows, rerr := s.readAllRows(ctx, tenant, table, storefront.MaxItems+1)
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if rerr != nil {
			_ = storefront.CatalogueFailed(ctx, tx, tenant, fmt.Errorf("the services: %w", rerr))
			return rerr
		}
		items, err := storefront.Items(ctx, tx, tenant, rows, sf.ServicesColumns)
		if err != nil {
			_ = storefront.CatalogueFailed(ctx, tx, tenant, fmt.Errorf("the services: %w", err))
			return err
		}
		return storefront.SyncServices(ctx, tx, tenant, items)
	})
}

// refundStorefrontOrder refunds a paid order or booking that was declined
// or cancelled, in full through the business's Paystack connection: the
// owner's confirmation, as from chat.
func (s *Server) refundStorefrontOrder(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	o, err := s.Orders.Refund(r.Context(), p.TenantID, chi.URLParam(r, "reference"), p.Actor())
	switch {
	case errors.Is(err, storefront.ErrNoOrder):
		err = pgx.ErrNoRows
	case errors.Is(err, storefront.ErrDecision), errors.Is(err, storefront.ErrRefund):
		err = fmt.Errorf("%w: %s", errConflict, plainError(err))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	maskCustomer(&o.Customer)
	writeJSON(w, http.StatusOK, map[string]any{"order": o})
}
