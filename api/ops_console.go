package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/status"
)

// The operator console's work (docs/operator-console.md): the catalogue
// review queue, publishers and reviewers; status page incidents and
// maintenance; read-only views of tenants; the platform audit chain.
// Every write needs a passkey step-up and is recorded in the platform
// chain, and in the publisher's chain where the CLI records it there.

// opsTx runs fn in a transaction scoped to the platform chain; also, when
// fn calls widen, to one tenant's (to audit there in the same commit).
func (s *Server) opsTx(r *http.Request, fn func(tx pgx.Tx, widen func(uuid.UUID) error) error) error {
	ctx := r.Context()
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		return fn(tx, func(tenant uuid.UUID) error {
			_, err := tx.Exec(ctx, "SELECT set_config('app.tenant_scope', $1, true)", db.Scope(ops.PlatformChain, tenant))
			return err
		})
	})
}

// opsDBFail maps the definer functions' refusals to answers.
func (s *Server) opsDBFail(w http.ResponseWriter, r *http.Request, err error) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "42501":
			writeErr(w, http.StatusForbidden, pe.Message)
			return
		case "22023":
			writeErr(w, http.StatusUnprocessableEntity, pe.Message)
			return
		case "P0002":
			writeErr(w, http.StatusNotFound, pe.Message)
			return
		case "55000":
			writeErr(w, http.StatusConflict, pe.Message)
			return
		}
	}
	s.fail(w, r, err)
}

// tenantAudit appends to a tenant's chain inside an opsTx widened to it.
func tenantAudit(r *http.Request, tx pgx.Tx, tenant uuid.UUID, actor, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(r.Context(), `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, tenant, actor, action, target, raw)
	return err
}

// --- catalogue ---

var allCatalogueStates = []string{"checks_failed", "in_review", "approved", "rejected", "published", "withdrawn", "revoked"}

func (s *Server) opsQueue(w http.ResponseWriter, r *http.Request) {
	states := []string{"in_review"}
	if st := r.URL.Query().Get("state"); st == "all" {
		states = allCatalogueStates
	} else if st != "" {
		states = strings.Split(st, ",")
	}
	type item struct {
		ID          uuid.UUID `json:"id"`
		Publisher   string    `json:"publisher"`
		Connector   string    `json:"connector"`
		Version     string    `json:"version"`
		State       string    `json:"state"`
		Licence     string    `json:"licence"`
		SubmittedBy string    `json:"submitted_by"`
		SubmittedAt time.Time `json:"submitted_at"`
		Passed      bool      `json:"checks_passed"`
		ReviewedBy  *string   `json:"reviewed_by"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, publisher, connector_id, version, state, licence, submitted_by, submitted_at, passed, reviewed_by
		FROM taskiem_catalogue_queue($1)`, states)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[item])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": nonNil(out)})
}

func (s *Server) opsSubmission(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such submission")
		return
	}
	var v struct {
		ID           uuid.UUID       `json:"id"`
		Tenant       uuid.UUID       `json:"publisher_tenant"`
		Publisher    string          `json:"publisher"`
		Connector    string          `json:"connector"`
		Version      string          `json:"version"`
		State        string          `json:"state"`
		Manifest     string          `json:"manifest"`
		ModuleSHA    string          `json:"module_sha256"`
		PackageSHA   string          `json:"package_digest"`
		KeyID        string          `json:"key_id"`
		Licence      string          `json:"licence"`
		Source       *string         `json:"source_url"`
		Attestation  json.RawMessage `json:"attestation"`
		Checks       json.RawMessage `json:"checks"`
		SubmittedBy  string          `json:"submitted_by"`
		SubmittedAt  time.Time       `json:"submitted_at"`
		ReviewedBy   *string         `json:"reviewed_by"`
		ReviewedAt   *time.Time      `json:"reviewed_at"`
		ReviewNote   *string         `json:"review_note"`
		PublishedAt  *time.Time      `json:"published_at"`
		RevokedBy    *string         `json:"revoked_by"`
		RevokedAt    *time.Time      `json:"revoked_at"`
		RevokeReason *string         `json:"revoke_reason"`
	}
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT * FROM taskiem_catalogue_submission($1)`, id).Scan(&v.ID, &v.Tenant, &v.Publisher, &v.Connector, &v.Version,
		&v.State, &v.Manifest, &v.ModuleSHA, &v.PackageSHA, &v.KeyID, &v.Licence, &v.Source, &v.Attestation, &v.Checks, &v.SubmittedBy, &v.SubmittedAt,
		&v.ReviewedBy, &v.ReviewedAt, &v.ReviewNote, &v.PublishedAt, &v.RevokedBy, &v.RevokedAt, &v.RevokeReason)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no such submission")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type event struct {
		Event string    `json:"event"`
		Actor string    `json:"actor"`
		Note  *string   `json:"note"`
		At    time.Time `json:"at"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT event, actor, note, at FROM taskiem_catalogue_history($1)`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	history, err := pgx.CollectRows(rows, pgx.RowToStructByPos[event])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"submission": v, "history": nonNil(history), "checklist": catalogue.Checklist}
	m, findings := catalogue.Lint([]byte(v.Manifest), catalogue.LintOptions{Publisher: v.Publisher})
	out["lint"] = nonNil(findings)
	if m != nil {
		out["summary"] = catalogue.Summarise(m)
	}
	writeJSON(w, http.StatusOK, out)
}

type opsReviewReq struct {
	Decision  string          `json:"decision"` // approve | reject
	Note      string          `json:"note"`
	Checklist map[string]bool `json:"checklist,omitempty"`
	StepUp    *opsStepUp      `json:"step_up,omitempty"`
}

// opsReview decides a submission as the signed-in operator: the reviewer
// is who signed in, not an email typed in. The database still enforces
// four eyes (on the reviewer list, outside the publisher), the note and,
// to approve, every checklist item.
func (s *Server) opsReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such submission")
		return
	}
	var req opsReviewReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Decision != "approve" && req.Decision != "reject" {
		s.fail(w, r, fmt.Errorf("%w: decision is approve or reject", errBadRequest))
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "a review needs a note: the publisher is told it")
		return
	}
	approve := req.Decision == "approve"
	checklist := map[string]bool{}
	var missing []string
	for _, k := range catalogue.ChecklistKeys() {
		if req.Checklist[k] {
			checklist[k] = true
		} else {
			missing = append(missing, k)
		}
	}
	if approve && len(missing) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, "to approve, confirm every checklist item; not confirmed: "+strings.Join(missing, ", "))
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.catalogue.review", id.String()+"/"+req.Decision) {
		return
	}
	o := operatorFrom(r.Context())
	raw, _ := json.Marshal(checklist)
	action := map[bool]string{true: "catalogue.approve", false: "catalogue.reject"}[approve]
	var state string
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		var tenant uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_catalogue_review($1, $2, $3, $4, $5)`, id, o.Email, approve, req.Note, raw).Scan(&tenant); err != nil {
			return err
		}
		if err := widen(tenant); err != nil {
			return err
		}
		detail := map[string]any{"note": req.Note, "checklist": checklist, "via": "console"}
		if err := tenantAudit(r, tx, tenant, "reviewer:"+o.Email, action, id.String(), detail); err != nil {
			return err
		}
		detail["publisher_tenant"] = tenant
		state = map[bool]string{true: "approved", false: "rejected"}[approve]
		return ops.AuditTx(r.Context(), tx, o.Actor(), action, id.String(), detail)
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	s.Logger.Info("ops: catalogue review", "submission", id, "state", state, "by", o.Actor())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": state})
}

type opsRevokeReq struct {
	Connector string     `json:"connector"`
	Version   string     `json:"version"`
	Reason    string     `json:"reason"`
	StepUp    *opsStepUp `json:"step_up,omitempty"`
}

// opsRevoke is the kill switch for a published version, as
// `taskiem catalogue revoke`.
func (s *Server) opsRevoke(w http.ResponseWriter, r *http.Request) {
	var req opsRevokeReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !catalogueIDRe(req.Connector) || !versionRe(req.Version) || strings.TrimSpace(req.Reason) == "" {
		s.fail(w, r, fmt.Errorf("%w: connector, version and a reason are required", errBadRequest))
		return
	}
	ref := req.Connector + "@" + req.Version
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.catalogue.revoke", ref) {
		return
	}
	o := operatorFrom(r.Context())
	by := "reviewer:" + o.Email
	err := s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		var tenant uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_catalogue_revoke($1, $2, $3, $4)`, req.Connector, req.Version, o.Email, req.Reason).Scan(&tenant); err != nil {
			return err
		}
		if err := widen(tenant); err != nil {
			return err
		}
		if err := tenantAudit(r, tx, tenant, by, "catalogue.revoke", ref, map[string]any{"reason": req.Reason, "via": "console"}); err != nil {
			return err
		}
		return ops.AuditTx(r.Context(), tx, o.Actor(), "catalogue.revoke", ref, map[string]any{"reason": req.Reason, "publisher_tenant": tenant})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	n, err := catalogue.NotifyRevoked(r.Context(), s.Store.Pool, req.Connector, req.Version, req.Reason, by, s.PublicURL)
	if err != nil {
		s.Logger.Error("ops: revocation alerts", "connector", ref, "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": ref, "alerted_tenants": n})
}

func (s *Server) opsPublishers(w http.ResponseWriter, r *http.Request) {
	type publisher struct {
		Tenant      uuid.UUID  `json:"tenant_id"`
		Slug        string     `json:"slug"`
		Name        string     `json:"name"`
		KeyID       string     `json:"key_id"`
		Status      string     `json:"status"`
		RequestedAt time.Time  `json:"requested_at"`
		VerifiedBy  *string    `json:"verified_by"`
		VerifiedAt  *time.Time `json:"verified_at"`
		Note        *string    `json:"status_note"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_catalogue_publishers()`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[publisher])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publishers": nonNil(out)})
}

// opsPublisherAction verifies, suspends or reinstates a publisher, as
// `taskiem catalogue publishers verify|suspend|reinstate`.
func (s *Server) opsPublisherAction(w http.ResponseWriter, r *http.Request) {
	slug, action := chi.URLParam(r, "slug"), chi.URLParam(r, "action")
	status := map[string]string{"verify": "verified", "suspend": "suspended", "reinstate": "verified"}[action]
	if status == "" {
		writeErr(w, http.StatusNotFound, "the action is verify, suspend or reinstate")
		return
	}
	var req struct {
		Note   string     `json:"note"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if action == "suspend" && strings.TrimSpace(req.Note) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "a suspension needs a note: the publisher is told why")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.publisher."+action, slug) {
		return
	}
	o := operatorFrom(r.Context())
	err := s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		var tenant uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_catalogue_set_publisher($1, $2, $3, $4)`, slug, status, o.Actor(), req.Note).Scan(&tenant); err != nil {
			return err
		}
		if err := widen(tenant); err != nil {
			return err
		}
		if err := tenantAudit(r, tx, tenant, o.Actor(), "catalogue.publisher."+action, slug, map[string]any{"note": req.Note, "via": "console"}); err != nil {
			return err
		}
		return ops.AuditTx(r.Context(), tx, o.Actor(), "catalogue.publisher."+action, slug, map[string]any{"note": req.Note, "publisher_tenant": tenant})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "status": status})
}

func (s *Server) opsReviewers(w http.ResponseWriter, r *http.Request) {
	type reviewer struct {
		Email   string    `json:"email"`
		AddedBy string    `json:"added_by"`
		AddedAt time.Time `json:"added_at"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT email, added_by, added_at FROM taskiem_catalogue_reviewers()`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reviewer])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviewers": nonNil(out)})
}

// --- status page ---

func (s *Server) opsIncidents(w http.ResponseWriter, r *http.Request) {
	list, err := status.Incidents(r.Context(), s.Store.Pool, time.Now().AddDate(0, 0, -90), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": nonNil(list), "components": status.Components})
}

func (s *Server) invalidateStatus() {
	if s.Status != nil && s.Status.Page != nil {
		s.Status.Page.Invalidate()
	}
}

func (s *Server) opsOpenIncident(w http.ResponseWriter, r *http.Request) {
	var req struct {
		status.Declaration
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "incident"
	}
	if kind != "incident" && kind != "maintenance" {
		writeErr(w, http.StatusUnprocessableEntity, "kind is incident or maintenance")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.status.open", kind) {
		return
	}
	o := operatorFrom(r.Context())
	id, err := status.Open(r.Context(), s.Store.Pool, req.Declaration, o.Actor())
	if err != nil {
		s.statusFail(w, r, err)
		return
	}
	if err := ops.Audit(r.Context(), s.Store.Pool, o.Actor(), "status.open", id.String(),
		map[string]any{"kind": kind, "title": req.Title, "components": req.Components, "status": req.Status, "impact": req.Impact}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidateStatus()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) opsUpdateIncident(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such incident")
		return
	}
	var req struct {
		status.Change
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.status.update", id.String()+"/"+req.Status) {
		return
	}
	o := operatorFrom(r.Context())
	if err := status.Post(r.Context(), s.Store.Pool, id, req.Change, o.Actor()); err != nil {
		s.statusFail(w, r, err)
		return
	}
	if err := ops.Audit(r.Context(), s.Store.Pool, o.Actor(), "status.update", id.String(), map[string]any{"status": req.Status, "impact": req.Impact}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidateStatus()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// --- tenants (read-only) ---

type opsTenantRow struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Parent       *uuid.UUID `json:"parent_id"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	Plan         *string    `json:"plan"`
	Subscription *string    `json:"subscription_status"`
	Pool         string     `json:"pool"`
}

func (s *Server) opsTenants(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_tenants($1, $2)`, strings.TrimSpace(r.URL.Query().Get("q")), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[opsTenantRow])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": nonNil(out)})
}

// opsTenant is one tenant's plan, subscription state, limits, usage and
// pool: what `taskiem tenants limits` and `taskiem pools` show, and no
// more. Each view is recorded in the platform chain.
func (s *Server) opsTenant(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such tenant")
		return
	}
	ctx := r.Context()
	rows, err := s.Store.Pool.Query(ctx, `SELECT * FROM taskiem_ops_tenants($1, 1)`, id.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[opsTenantRow])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(list) == 0 || list[0].ID != id {
		writeErr(w, http.StatusNotFound, "no such tenant")
		return
	}
	type subscription struct {
		Plan              string     `json:"plan"`
		Interval          string     `json:"interval"`
		Status            string     `json:"status"`
		PeriodStart       time.Time  `json:"period_start"`
		PeriodEnd         time.Time  `json:"period_end"`
		TrialEnd          *time.Time `json:"trial_end"`
		CompUntil         *time.Time `json:"comp_until"`
		PastDueSince      *time.Time `json:"past_due_since"`
		CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
		PendingPlan       *string    `json:"pending_plan"`
	}
	var sub *subscription
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{id}, func(tx pgx.Tx) error {
		var x subscription
		err := tx.QueryRow(ctx, `SELECT plan_id, billing_interval, status, period_start, period_end, trial_end, comp_until, past_due_since,
			cancel_at_period_end, pending_plan_id FROM subscriptions WHERE tenant_id = $1`, id).
			Scan(&x.Plan, &x.Interval, &x.Status, &x.PeriodStart, &x.PeriodEnd, &x.TrialEnd, &x.CompUntil, &x.PastDueSince, &x.CancelAtPeriodEnd, &x.PendingPlan)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		sub = &x
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limits, err := s.Store.ViewLimits(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	o := operatorFrom(ctx)
	if err := ops.Audit(ctx, s.Store.Pool, o.Actor(), "tenant.view", id.String(), nil); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant": list[0], "subscription": sub, "limits": limits})
}

// --- the platform audit chain ---

func (s *Server) opsAudit(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Seq       int64           `json:"seq"`
		ActorType string          `json:"actor_type"`
		ActorID   string          `json:"actor_id"`
		Action    string          `json:"action"`
		Target    string          `json:"target"`
		Detail    json.RawMessage `json:"detail"`
		At        time.Time       `json:"at"`
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	var out []entry
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT chain_seq, actor_type, actor_id, action, target, detail, at FROM audit_log
			WHERE tenant_id = $1 AND chain_seq < $2 ORDER BY chain_seq DESC LIMIT $3`, ops.PlatformChain, before, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chain": ops.PlatformChain, "entries": nonNil(out)})
}

func (s *Server) opsAuditVerify(w http.ResponseWriter, r *http.Request) {
	var broken *int64
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT taskiem_audit_verify($1)`, ops.PlatformChain).Scan(&broken)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intact": broken == nil, "first_broken_seq": broken})
}

// opsAuditExport streams the platform chain as JSON lines for the offline
// verifier (`taskiem audit verify FILE`).
func (s *Server) opsAuditExport(w http.ResponseWriter, r *http.Request) {
	o := operatorFrom(r.Context())
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-platform.jsonl"`)
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		if err := ops.AuditTx(r.Context(), tx, o.Actor(), "audit.export", ops.PlatformChain.String(), nil); err != nil {
			return err
		}
		return audit.Export(r.Context(), tx, ops.PlatformChain.String(), w)
	})
	if err != nil {
		s.Logger.Error("platform audit export failed", "err", err)
	}
}
