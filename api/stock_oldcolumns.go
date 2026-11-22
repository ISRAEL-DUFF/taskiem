package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/stock"
)

// Workflows on the catalogue's old stock columns (what-a-business-buys
// §4.3, S1b): the steps of a business's workflows that read or set stock,
// unit, cost_price, reorder_level, target_level or supplier on its
// catalogue's table, with what to use instead. Nothing is in production,
// so it is a check, not a data migration: GET /v1/stock/old-columns runs
// it, and the storefront sweep runs it once per business after each start.
// Either way the owners are told once (stock.TellOldColumns).

// CheckOldColumns lists the business's workflow steps on the old columns
// and tells its owners of them, once.
func (s *Server) CheckOldColumns(ctx context.Context, tenant uuid.UUID) (uses []stock.OldColumnUse, told bool, err error) {
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if uses, err = stock.OldColumnsTx(ctx, tx, tenant); err != nil {
			return err
		}
		told, err = stock.TellOldColumns(ctx, tx, tenant, uses)
		return err
	})
	return uses, told, err
}

// checkOldColumnsOnce runs CheckOldColumns for a business the first time
// the sweep meets it since start.
func (s *Server) checkOldColumnsOnce(ctx context.Context, tenant uuid.UUID) {
	if _, done := s.oldColumnsChecked.LoadOrStore(tenant, true); done {
		return
	}
	if _, _, err := s.CheckOldColumns(ctx, tenant); err != nil {
		s.oldColumnsChecked.Delete(tenant)
		if ctx.Err() == nil {
			s.Logger.Warn("stock: checking workflows for the old columns", "tenant", tenant, "err", err)
		}
	}
}

func (s *Server) getStockOldColumns(w http.ResponseWriter, r *http.Request) {
	uses, _, err := s.CheckOldColumns(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uses": uses})
}
