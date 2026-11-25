package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// One product fronts (docs/spec/forms-that-sell.md §5.4, L2): a page of
// its own for one catalogue row, with its promise, photos, chips, price
// bar and order form selling the item in packages, paid at the door. The
// web app's Fronts tab adds one (POST /v1/storefront/fronts with kind
// one-product and the item), and chat says "sell <product> on a page"
// (chat_oneproduct.go); both make it here.

// makeOneProduct makes a One product front from a catalogue row, off, with
// its order form, audited; then, in the same transaction, done. It needs
// the storefront and its products, and the Orders area on, since its form
// takes orders.
func (s *Server) makeOneProduct(ctx context.Context, p *Principal, in storefront.NewOneProduct,
	audit func(tx pgx.Tx, action, target string, detail map[string]any) error, done func(tx pgx.Tx, made storefront.OneProductFront) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		for _, a := range []areas.Area{areas.Storefront, areas.Orders} {
			on, err := areas.Enabled(ctx, tx, p.TenantID, a)
			if err != nil {
				return err
			}
			if !on {
				return areaOff{a}
			}
		}
		made, err := storefront.CreateOneProduct(ctx, tx, p.TenantID, in)
		if err != nil {
			return storefrontErr(err)
		}
		if err := audit(tx, "storefront.front_create", made.Front.Slug, map[string]any{"kind": made.Front.Kind, "name": made.Front.Name, "item": made.Item.ID}); err != nil {
			return err
		}
		if err := audit(tx, "storefront.action_add", made.Action.ID.String(), map[string]any{"front": made.Front.ID, "kind": made.Action.Kind, "slug": made.Action.Slug}); err != nil {
			return err
		}
		return done(tx, made)
	})
}

// addOneProduct answers POST /v1/storefront/fronts for a One product
// front: the front made from the item, off, with its order form off; Set
// up and the switches turn them on as for any front.
func (s *Server) addOneProduct(w http.ResponseWriter, r *http.Request, in storefront.NewOneProduct) {
	p := principalFrom(r.Context())
	if in.Item == "" {
		s.fail(w, r, fmt.Errorf("%w: choose the product the page sells", errBadRequest))
		return
	}
	var v frontView
	err := s.makeOneProduct(r.Context(), p, in, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}, func(tx pgx.Tx, made storefront.OneProductFront) error {
		list, err := frontViews(r.Context(), tx, p.TenantID)
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.ID == made.Front.ID {
				v = x
			}
		}
		v.Warnings = made.Warnings
		return nil
	})
	var off areaOff
	if errors.As(err, &off) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": areaOffSentence(off.area), "code": "area_off", "area_off": string(off.area)})
		return
	}
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
