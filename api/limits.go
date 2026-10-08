package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// getLimits shows the tenant its plan limits, what it uses of them, and
// limits it reached lately (any member). Tenants cannot change their
// limits: operators do, with `taskiem tenants limits`.
func (s *Server) getLimits(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ViewLimits(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	help := map[string]string{}
	for _, k := range runtime.LimitKeys {
		help[k.Key] = k.Help
	}
	// Deliveries refused while the tenant was suspended, by day (30 days).
	type refusal struct {
		Day  string `json:"day"`
		Kind string `json:"kind"`
		Hits int64  `json:"hits"`
	}
	var refused []refusal
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT day::text, kind, hits FROM ingest_refusals WHERE reason = 'suspended' AND day > (now() AT TIME ZONE 'UTC')::date - 30 ORDER BY day DESC, kind`)
		if err != nil {
			return err
		}
		refused, err = pgx.CollectRows(rows, pgx.RowToStructByPos[refusal])
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": v.Limits, "overrides": v.Overrides, "usage": v.Usage, "recent_hits": v.Hits, "help": help,
		"refused_while_suspended": nonNil(refused)})
}

// checkCount refuses creating one more workflow, secret or connection
// beyond the tenant's plan.
func (s *Server) checkCount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit string) error {
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return err
	}
	switch limit {
	case "max_workflows":
		err = runtime.CheckCount(ctx, tx, limit, `workflows WHERE tenant_id = $1`, lim.MaxWorkflows, tenant)
	case "max_secrets":
		err = runtime.CheckCount(ctx, tx, limit, `secrets WHERE tenant_id = $1 AND name IS NOT NULL`, lim.MaxSecrets, tenant)
	case "max_connections":
		err = runtime.CheckCount(ctx, tx, limit, `connections WHERE tenant_id = $1 AND status = 'active'`, lim.MaxConnections, tenant)
	}
	if _, ok := runtime.IsLimit(err); ok {
		s.Store.LimitHit(ctx, tenant, limit)
	}
	return err
}
