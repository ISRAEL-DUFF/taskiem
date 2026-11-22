package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/stock"
)

// Counted by (docs/spec/what-a-business-buys.md §4.1, §4.5): what a
// product of the catalogue is counted by, a stock item, and how many of it
// one sale uses (a pack of 6 uses 6 of a single item), or none. Selling
// the product lowers the stock item by the quantity times its uses, and
// how many are left of it is how many whole sales the stock item covers.
// The screens are S2's; this is their door.

// countedProduct is the product a counted-by request names: a row of the
// catalogue, not a material, nor a product sold in sizes and colours
// (counted through its sizes).
func (s *Server) countedProduct(w http.ResponseWriter, r *http.Request) (stockShop, stock.Item, bool) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return stockShop{}, stock.Item{}, false
	}
	p := principalFrom(r.Context())
	sh, err := s.stockShopOf(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return sh, stock.Item{}, false
	}
	id := chi.URLParam(r, "id")
	it, err := s.stockItem(r.Context(), p.TenantID, sh, id)
	if err != nil {
		s.stockFail(w, r, err)
		return sh, it, false
	}
	switch {
	case it.Material || it.ID != id:
		s.fail(w, r, fmt.Errorf("%w: name a product of your catalogue by its id: a stock item is not counted by another", errBadRequest))
		return sh, it, false
	case it.HasSizes():
		s.fail(w, r, fmt.Errorf("%w: %s is sold in sizes and colours: each is counted by its own stock item", errBadRequest, it.Name))
		return sh, it, false
	}
	return sh, it, true
}

// putCountedBy is PUT /v1/stock/products/{id}/counted-by: the product is
// counted by the stock item named, uses of it a sale (1 when not said).
func (s *Server) putCountedBy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StockItemID uuid.UUID `json:"stock_item_id"`
		Uses        *int64    `json:"uses"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	uses := int64(1)
	if in.Uses != nil {
		uses = *in.Uses
	}
	if uses < 1 || uses > productstock.MaxUses {
		s.fail(w, r, fmt.Errorf("%w: uses is how many of the stock item one sale takes: a whole number from 1 to 1,000,000", errBadRequest))
		return
	}
	if in.StockItemID == uuid.Nil {
		s.fail(w, r, fmt.Errorf("%w: stock_item_id names the stock item it is counted by", errBadRequest))
		return
	}
	sh, it, ok := s.countedProduct(w, r)
	if !ok {
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		x, err := stock.StockItemTx(r.Context(), tx, p.TenantID, in.StockItemID)
		if err != nil {
			return err
		}
		if x.RetiredAt != nil {
			return fmt.Errorf("%w: %s is a size or colour taken off sale", stock.ErrInvalid, x.Name)
		}
		if err := productstock.LinkTx(r.Context(), tx, p.TenantID, it.ID, x.ID, uses); err != nil {
			return err
		}
		return auditTx(r, tx, "stock.counted_by", it.ID, map[string]any{"product": it.Name, "stock_item_id": x.ID, "stock_item": x.Name, "uses": uses})
	})
	switch {
	case errors.Is(err, stock.ErrNoItem):
		writeErr(w, http.StatusNotFound, "no such stock item")
		return
	case err != nil:
		s.stockFail(w, r, err)
		return
	}
	s.countedOut(w, r, sh, it.ID)
}

// deleteCountedBy is DELETE /v1/stock/products/{id}/counted-by: the
// product is counted by none. Its stock item stays, with its count and
// history (a material, when it counts nothing else).
func (s *Server) deleteCountedBy(w http.ResponseWriter, r *http.Request) {
	sh, it, ok := s.countedProduct(w, r)
	if !ok {
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := productstock.UnlinkTx(r.Context(), tx, p.TenantID, it.ID); err != nil {
			return err
		}
		return auditTx(r, tx, "stock.counted_by", it.ID, map[string]any{"product": it.Name, "stock_item_id": nil})
	}); err != nil {
		s.stockFail(w, r, err)
		return
	}
	s.countedOut(w, r, sh, it.ID)
}

// countedOut answers the product as it is now.
func (s *Server) countedOut(w http.ResponseWriter, r *http.Request, sh stockShop, id string) {
	it, err := s.stockItem(r.Context(), principalFrom(r.Context()).TenantID, sh, id)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it})
}
