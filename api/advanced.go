package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
)

// The Advanced switch (no-technical-steps N0-5): a business sees the
// technical parts of the web app only when it is on. Owners and admins
// (member.manage) set it in Settings; until they do, plans above Shop
// have it on and the rest off.

// advancedOf is whether tenant sees the technical parts.
func (s *Server) advancedOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (on bool, set bool, err error) {
	var v *bool
	if err := tx.QueryRow(ctx, `SELECT advanced FROM tenants WHERE id = $1`, tenant).Scan(&v); err != nil {
		return false, false, err
	}
	if v != nil {
		return *v, true, nil
	}
	if s.Billing == nil || !s.Billing.On() {
		return false, false, nil
	}
	p, _, err := s.Billing.PlanOf(ctx, tx, tenant)
	if err != nil {
		return false, false, err
	}
	return billing.TierRank(p.Tier) > billing.TierRank("shop"), false, nil
}

// getAdvanced answers GET /v1/settings/advanced.
func (s *Server) getAdvanced(w http.ResponseWriter, r *http.Request) {
	var on, set bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, set, err = s.advancedOf(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"advanced": on, "chosen": set})
}

// putAdvanced answers PUT /v1/settings/advanced with {"advanced": true}.
func (s *Server) putAdvanced(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Advanced bool `json:"advanced"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE tenants SET advanced = $2 WHERE id = $1`, p.TenantID, req.Advanced); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.advanced", p.TenantID.String(), map[string]any{"advanced": req.Advanced})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"advanced": req.Advanced, "chosen": true})
}
