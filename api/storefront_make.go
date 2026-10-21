package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// makeStorefrontTable answers POST /v1/storefront/catalogue/make with
// {"kind": "products"} or {"kind": "services"}: the table and its mapping
// as chat makes them, in one step, so nobody builds a table and maps its
// columns by hand (no-technical-steps N7). A storefront that already has
// one is answered as it is.
func (s *Server) makeStorefrontTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if !p.Can(PermTableEdit) {
		s.fail(w, r, fmt.Errorf("%w: making the table needs %s", errForbidden, PermTableEdit))
		return
	}
	var t datatables.Table
	var names []string
	switch req.Kind {
	case "products":
		t, names = datatables.Table{Name: "products", Title: "Products", Description: "The storefront's catalogue", Columns: productColumns}, []string{"products", "shop_products", "products_2", "products_3"}
	case "services":
		t, names = datatables.Table{Name: "services", Title: "Services", Description: "The storefront's services to book", Columns: serviceColumns}, []string{"services", "shop_services", "services_2", "services_3"}
	default:
		s.fail(w, r, fmt.Errorf("%w: kind must be products or services", errBadRequest))
		return
	}
	var sf storefront.Storefront
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		sf, err = storefront.Get(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		if errors.Is(err, storefront.ErrNotFound) {
			s.fail(w, r, fmt.Errorf("%w: make your page first", errConflict))
			return
		}
		s.fail(w, r, err)
		return
	}
	existing := sf.CatalogueTable
	if req.Kind == "services" {
		existing = sf.ServicesTable
	}
	if existing != nil {
		writeJSON(w, http.StatusOK, map[string]any{"kind": req.Kind, "table_id": *existing, "made": false})
		return
	}
	if err := s.makeOwnTable(r.Context(), p.TenantID, p.Actor(), func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}, &t, names...); err != nil {
		s.fail(w, r, storefrontErr(fmt.Errorf("%w: the table could not be made: %s", errConflict, plainError(err))))
		return
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		if req.Kind == "products" {
			m := productMapping
			if err := m.Check(tableColumns(t)); err != nil {
				return err
			}
			saved, err := storefront.Save(r.Context(), tx, p.TenantID, sf.Slug, sf.Draft, &t.ID, m, p.Actor())
			if err != nil {
				return err
			}
			return auditTx(r, tx, "storefront.save", saved.Slug, map[string]any{"catalogue_table": t.ID})
		}
		m := serviceMapping
		if err := m.CheckServices(tableColumns(t)); err != nil {
			return err
		}
		saved, err := storefront.SaveBookings(r.Context(), tx, p.TenantID, &t.ID, m, sf.Availability)
		if err != nil {
			return err
		}
		return auditTx(r, tx, "storefront.bookings_save", saved.Slug, map[string]any{"services_table": t.ID})
	})
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"kind": req.Kind, "table_id": t.ID, "made": true})
}
