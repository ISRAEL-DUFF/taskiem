package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/matching"
)

// listUnmatched serves GET /v1/money/unmatched: money received that
// matching left for a person, to match by hand.
func (s *Server) listUnmatched(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var list []matching.Payment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = matching.Unmatched(r.Context(), tx, principalFrom(r.Context()).TenantID, 100)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": list})
}
