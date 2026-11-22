package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// getStockItems is GET /v1/stock/stock-items?show=all|sold|not_sold&retired=false:
// the Stock page's rows (docs/spec/what-a-business-buys.md §4.5), stock
// items each with the products it is sold as (none: a material, Not
// sold), by group then name; what they are worth at cost and how many
// are low, of those listed.
func (s *Server) getStockItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	show := stock.Show(q.Get("show"))
	switch show {
	case "":
		show = stock.ShowAll
	case stock.ShowAll, stock.ShowSold, stock.ShowNotSold:
	default:
		s.fail(w, r, fmt.Errorf("%w: show is all, sold or not_sold", errBadRequest))
		return
	}
	retired := false
	if v := q.Get("retired"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: retired is true or false", errBadRequest))
			return
		}
		retired = b
	}
	p := principalFrom(r.Context())
	sh, err := s.stockShopOf(r.Context(), p.TenantID)
	if errors.Is(err, errStockOff) {
		writeJSON(w, http.StatusOK, map[string]any{"keeps_stock": false, "items": []any{}, "value_kobo": 0, "low": 0})
		return
	}
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	rows, err := s.readAllRows(r.Context(), p.TenantID, sh.table, storefront.MaxItems+1)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	var items []stock.StockItemRow
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		items, err = stock.StockItemRowsTx(r.Context(), tx, p.TenantID, rows, sh.cols(), show, retired)
		return err
	}); err != nil {
		s.stockFail(w, r, err)
		return
	}
	var value int64
	low := 0
	for _, it := range items {
		value += it.ValueKobo
		if it.Low {
			low++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keeps_stock": true, "items": items, "value_kobo": value, "low": low})
}
