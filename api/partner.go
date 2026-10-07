package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// The partner admin API (spec 13.4 step 5, 5.3; decision 0015): a partner's
// server, with an API key holding partner.read or partner.manage, creates
// and observes its sub-tenants, registers embed apps, and mints end-user
// tokens. It is the only path from a partner to a sub-tenant's data: every
// such access goes through taskiem_partner_enter, which checks the parent,
// writes the access to the partner's and the sub-tenant's audit chains, and
// narrows the transaction to that one sub-tenant.

// Partner permissions. Only owners hold them among the built-in roles; a
// partner's server gets them through an API key.
const (
	PermPartnerRead   = "partner.read"
	PermPartnerManage = "partner.manage"
)

func init() {
	allPermissions = append(allPermissions, PermPartnerRead, PermPartnerManage)
	rolePermissions["owner"] = allPermissions
	permissionInfo[PermPartnerRead] = "Partner API: see sub-tenants, their workflows, runs and usage, embed apps and webhook deliveries (audited in both tenants)"
	permissionInfo[PermPartnerManage] = "Partner API: create and suspend sub-tenants, manage embed apps, mint and revoke end-user tokens"
}

// partnerRoutes mounts /v1/partner, inside the authenticated group.
func (s *Server) partnerRoutes(r chi.Router) {
	r.Use(s.partnerKey)
	read, manage := s.need(PermPartnerRead), s.need(PermPartnerManage)
	r.With(read).Get("/", s.getPartner)
	r.With(read).Get("/sub-tenants", s.listSubTenants)
	r.With(manage).Post("/sub-tenants", s.createSubTenant)
	r.With(manage).Put("/sub-tenants/{sub}/limits", s.putSubTenantLimits)
	r.With(manage).Post("/sub-tenants/{sub}/suspend", s.setSubTenantStatus("suspended"))
	r.With(manage).Post("/sub-tenants/{sub}/resume", s.setSubTenantStatus("active"))
	r.With(read).Get("/sub-tenants/{sub}/workflows", s.listSubTenantWorkflows)
	r.With(read).Get("/sub-tenants/{sub}/runs", s.listSubTenantRuns)
	r.With(read).Get("/sub-tenants/{sub}/usage", s.subTenantUsage)
	r.With(read).Get("/embed-apps", s.listEmbedApps)
	r.With(manage).Post("/embed-apps", s.createEmbedApp)
	r.With(read).Get("/embed-apps/{app}", s.getEmbedApp)
	r.With(manage).Put("/embed-apps/{app}", s.updateEmbedApp)
	r.With(manage).Post("/embed-apps/{app}/webhook-secret", s.rotateWebhookSecret)
	r.With(manage).Post("/embed-apps/{app}/tokens", s.mintEndUserToken)
	r.With(manage).Post("/embed-apps/{app}/tokens/revoke", s.revokeEndUserTokens)
	r.With(manage).Post("/embed-apps/{app}/domains", s.addAppDomain)
	r.With(manage).Post("/embed-apps/{app}/domains/{domain}/verify", s.verifyAppDomain)
	r.With(manage).Delete("/embed-apps/{app}/domains/{domain}", s.removeAppDomain)
	r.With(read).Get("/connectors", s.listSharedConnectors)
	r.With(manage).Put("/connectors/{id}/share", s.shareConnector(true))
	r.With(manage).Delete("/connectors/{id}/share", s.shareConnector(false))
	r.With(read).Get("/sub-tenants/{sub}/connections", s.listSubTenantConnections)
	r.With(manage).Post("/sub-tenants/{sub}/connections", s.createSubTenantConnection)
	r.With(manage).Put("/sub-tenants/{sub}/connections/{conn}/credentials", s.replaceSubTenantConnection)
	r.With(manage).Delete("/sub-tenants/{sub}/connections/{conn}", s.deleteSubTenantConnection)
	r.With(read).Get("/webhook-deliveries", s.listWebhookDeliveries)
	r.With(manage).Post("/webhook-deliveries/{id}/retry", s.retryWebhookDelivery)
}

// partnerKey admits only API keys reaching the whole tenant, of a tenant
// that is a partner.
func (s *Server) partnerKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		if p.KeyID == uuid.Nil || p.Environment != "" {
			writeErr(w, http.StatusForbidden, "the partner API takes an API key reaching every environment")
			return
		}
		err := s.tx(r, func(tx pgx.Tx) error {
			_, err := tx.Exec(r.Context(), `SELECT taskiem_partner_self()`)
			return err
		})
		if err != nil {
			s.fail(w, r, partnerErr(err))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// partnerErr maps the partner functions' refusals to API errors.
func partnerErr(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "P0002":
		return pgx.ErrNoRows
	case "42501":
		return fmt.Errorf("%w: %s", errForbidden, pe.Message)
	case "53400":
		return &runtime.LimitError{Limit: "max_subtenants", Code: "limit_exceeded", Message: pe.Message}
	}
	return err
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// partnerTx runs fn inside one sub-tenant, entered through
// taskiem_partner_enter: the access is audited in both chains first, and
// fn sees the sub-tenant alone. fn must not call auditTx (the partner is no
// longer in scope); the entry is the audit.
func (s *Server) partnerTx(r *http.Request, sub uuid.UUID, action string, detail map[string]any, fn func(pgx.Tx) error) error {
	p := principalFrom(r.Context())
	if detail == nil {
		detail = map[string]any{}
	}
	detail["ip"] = clientIP(r)
	raw, _ := json.Marshal(detail)
	return partnerErr(db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT taskiem_partner_enter($1, $2, $3, $4, $5)`, sub, p.ActorType(), p.Actor(), action, raw); err != nil {
			return err
		}
		if fn == nil {
			return nil
		}
		return fn(tx)
	}))
}

func subParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "sub"))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

// getPartner shows the partner's caps, its sub-tenants' total usage, and
// the ceiling of its sub-tenants' limits (its own).
func (s *Server) getPartner(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := map[string]any{"tenant_id": p.TenantID}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var maxSubs int
		var day, month int64
		if err := tx.QueryRow(ctx, `SELECT max_subtenants, subtenant_runs_per_day, subtenant_runs_per_month FROM partners WHERE tenant_id = $1`, p.TenantID).
			Scan(&maxSubs, &day, &month); err != nil {
			return err
		}
		var subs int
		var today, thisMonth int64
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(runs_today), 0)::bigint, COALESCE(sum(runs_month), 0)::bigint FROM taskiem_partner_usage($1)`, p.TenantID).
			Scan(&subs, &today, &thisMonth); err != nil {
			return err
		}
		out["caps"] = map[string]any{"max_subtenants": maxSubs, "subtenant_runs_per_day": day, "subtenant_runs_per_month": month}
		out["usage"] = map[string]any{"subtenants": subs, "runs_today": today, "runs_this_month": thisMonth}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ceiling, err := s.Store.LimitsFor(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out["subtenant_limit_ceiling"] = ceiling
	writeJSON(w, http.StatusOK, out)
}

type subTenant struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Region    string    `json:"region"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) listSubTenants(w http.ResponseWriter, r *http.Request) {
	var out []subTenant
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, region, status, created_at FROM taskiem_partner_subtenants()`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[subTenant])
		return err
	})
	if err != nil {
		s.fail(w, r, partnerErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub_tenants": nonNil(out)})
}

var regionRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type subTenantReq struct {
	Name   string         `json:"name"`
	Region string         `json:"region,omitempty"`
	Limits map[string]any `json:"limits,omitempty"`
}

// subTenantLimits checks limits a partner sets for a sub-tenant: known
// keys, valid values, none above the partner's own (spec 13.1). A null
// returns the limit to the partner's value. worker_concurrency is always
// written (the partner's when not given) because workers read it from the
// sub-tenant's own row.
func (s *Server) subTenantLimits(ctx context.Context, partner uuid.UUID, in map[string]any) (map[string]any, error) {
	ceiling, err := s.Store.LimitsFor(ctx, partner)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	set := map[string]any{}
	for k, v := range in {
		var str string
		switch x := v.(type) {
		case nil:
		case float64:
			str = strconv.FormatFloat(x, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("%w: limit %s must be a number or null", errBadRequest, k)
		}
		pv, err := runtime.ParseLimit(k, str)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errBadRequest, err)
		}
		out[k] = pv
		if pv != nil {
			set[k] = pv
		}
	}
	proposed, err := ceiling.With(set)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadRequest, err)
	}
	if over := proposed.Exceeds(ceiling); len(over) > 0 {
		return nil, fmt.Errorf("%w: a sub-tenant's limits cannot exceed the partner's (%s)", errBadRequest, strings.Join(over, ", "))
	}
	if v, ok := out["worker_concurrency"]; (!ok || v == nil) && ceiling.WorkerConcurrency > 0 {
		out["worker_concurrency"] = ceiling.WorkerConcurrency
	}
	return out, nil
}

// createSubTenant creates a sub-tenant with a default workspace and the dev
// and prod environments. Its limits default to the partner's.
func (s *Server) createSubTenant(w http.ResponseWriter, r *http.Request) {
	var req subTenantReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 || (req.Region != "" && !regionRe.MatchString(req.Region)) {
		s.fail(w, r, fmt.Errorf("%w: a name (up to 200 characters) is required, and a region is like ng-lagos", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	limits, err := s.subTenantLimits(r.Context(), p.TenantID, req.Limits)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := uuid.Must(uuid.NewV7())
	detail, _ := json.Marshal(map[string]any{"name": req.Name, "limits": limits, "ip": clientIP(r)})
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if _, err := tx.Exec(ctx, `SELECT taskiem_partner_create_subtenant($1, $2, $3, $4, $5, $6)`, id, req.Name, req.Region, p.ActorType(), p.Actor(), detail); err != nil {
			return err
		}
		// Now inside the sub-tenant alone.
		if _, err := tx.Exec(ctx, `INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, uuid.Must(uuid.NewV7()), id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, id); err != nil {
			return err
		}
		if len(limits) > 0 {
			raw, _ := json.Marshal(limits)
			if _, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, $2, $3)`, id, raw, "partner:"+p.TenantID.String()+"/"+p.Actor()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, partnerErr(err))
		return
	}
	eff, err := s.Store.LimitsFor(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": req.Name, "status": "active", "limits": eff})
}

// putSubTenantLimits changes a sub-tenant's limits within the partner's.
func (s *Server) putSubTenantLimits(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req map[string]any
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	limits, err := s.subTenantLimits(r.Context(), p.TenantID, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, _ := json.Marshal(limits)
	err = s.partnerTx(r, sub, "partner.subtenant.limits.set", map[string]any{"limits": limits}, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `SELECT taskiem_set_tenant_limits($1, $2, $3)`, sub, raw, "partner:"+p.TenantID.String()+"/"+p.Actor())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Store.ForgetLimits(sub)
	eff, err := s.Store.LimitsFor(r.Context(), sub)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sub, "limits": eff})
}

// setSubTenantStatus suspends or resumes a sub-tenant. Suspension stops at
// once every end-user token and session of it (authentication requires an
// active tenant) and revokes its outstanding tokens. While suspended it
// does no new work: schedules are not claimed, webhook and connector
// deliveries get 423 (counted), queued runs wait and repair jobs wait
// (migration 00088). Resuming continues schedules from now, without
// catching up fires missed meanwhile (docs/embedding.md).
func (s *Server) setSubTenantStatus(status string) http.HandlerFunc {
	action := "partner.subtenant.suspend"
	if status == "active" {
		action = "partner.subtenant.resume"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		sub, err := subParam(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		err = s.partnerTx(r, sub, action, nil, func(tx pgx.Tx) error {
			ctx := r.Context()
			tag, err := tx.Exec(ctx, `UPDATE tenants SET status = $2 WHERE id = $1 AND status <> $2`, sub, status)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: the sub-tenant is already %s", errConflict, status)
			}
			if status == "suspended" {
				if _, err := tx.Exec(ctx, `UPDATE end_user_tokens SET revoked_at = now() WHERE revoked_at IS NULL`); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE revoked_at IS NULL`)
				return err
			}
			return nil
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": sub, "status": status})
	}
}

// listSubTenantWorkflows lists a sub-tenant's workflows (names and
// versions, not definitions).
func (s *Server) listSubTenantWorkflows(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []workflowSummary
	err = s.partnerTx(r, sub, "partner.subtenant.workflows.read", nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT w.id, w.name, w.active_version, COALESCE(max(v.version), 0), w.created_at, `+latestKey+`, w.git_path
			FROM workflows w LEFT JOIN workflow_versions v ON v.workflow_id = w.id GROUP BY w.id ORDER BY w.name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[workflowSummary])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": nonNil(out)})
}

// partnerRun is a sub-tenant's run as its partner sees it: the outcome
// only, redacted to its status and, for a failure, the error's kind. No
// inputs, outputs, step data or error messages.
type partnerRun struct {
	ID          uuid.UUID  `json:"id"`
	WorkflowID  uuid.UUID  `json:"workflow_id"`
	Workflow    string     `json:"workflow"`
	Version     int        `json:"version"`
	Environment string     `json:"environment"`
	Status      string     `json:"status"`
	StartedBy   *string    `json:"started_by"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at"`
	ErrorKind   *string    `json:"error_kind"`
}

func (s *Server) listSubTenantRuns(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	before := time.Now().Add(time.Hour)
	if b := q.Get("before"); b != "" {
		if before, err = time.Parse(time.RFC3339Nano, b); err != nil {
			s.fail(w, r, fmt.Errorf("%w: before must be an RFC 3339 time", errBadRequest))
			return
		}
	}
	var out []partnerRun
	err = s.partnerTx(r, sub, "partner.subtenant.runs.read", nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+runColumns+`,
			(SELECT e.payload->'error'->>'kind' FROM run_events e WHERE e.run_id = r.id AND e.run_started_at = r.started_at AND e.type = 'RunFailed' LIMIT 1)
			FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.started_at < $1 AND ($2 = '' OR r.status = $2) ORDER BY r.started_at DESC LIMIT $3`, before, q.Get("status"), limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[partnerRun])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNil(out)})
}

// subTenantUsage shows a sub-tenant's limits, usage and recent limit hits.
// The access is audited before the read.
func (s *Server) subTenantUsage(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.partnerTx(r, sub, "partner.subtenant.usage.read", nil, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Store.ViewLimits(r.Context(), sub)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub_tenant_id": sub, "limits": v.Limits, "overrides": v.Overrides, "usage": v.Usage, "recent_hits": v.Hits})
}
