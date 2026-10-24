package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/matching"
)

// listUnmatched serves GET /v1/money/unmatched: money received that
// matching left for a person, to match by hand, and the storefront orders
// it can be matched to (customers masked unless revealed, as suggestions).
func (s *Server) listUnmatched(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	reveal, ok := revealing(w, r)
	if !ok {
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var list []matching.Payment
	var orders []matching.OpenOrder
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = matching.Unmatched(r.Context(), tx, tenant, 100); err != nil {
			return err
		}
		if orders, err = s.matcher().OpenOrders(r.Context(), tx, tenant, 100); err != nil || !reveal {
			return err
		}
		return auditTx(r, tx, "pii.reveal", "money_unmatched", nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		for i := range orders {
			orders[i].Customer = maskName(orders[i].Customer)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": list, "orders": orders, "revealed": reveal})
}
