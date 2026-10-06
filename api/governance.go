package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/totp"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Governance (spec 9.1): approval policies, delegation, step-up, and
// four-eyes on change.

// --- settings ---

type governance struct {
	FourEyesPublish  bool `json:"four_eyes_publish"`
	FourEyesPolicies bool `json:"four_eyes_policies"`
	// DefaultRetention keeps run payloads this long after a run ends when
	// its workflow sets no settings.retention (spec 9.4); empty is 90 days.
	DefaultRetention string     `json:"default_retention"`
	UpdatedBy        string     `json:"updated_by,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
}

func governanceTx(ctx context.Context, tx pgx.Tx) (governance, error) {
	var g governance
	err := tx.QueryRow(ctx, `SELECT four_eyes_publish, four_eyes_policies, COALESCE(default_retention, ''), updated_by, updated_at FROM governance_settings`).
		Scan(&g.FourEyesPublish, &g.FourEyesPolicies, &g.DefaultRetention, &g.UpdatedBy, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return governance{}, nil
	}
	return g, err
}

func (s *Server) getGovernance(w http.ResponseWriter, r *http.Request) {
	var g governance
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		g, err = governanceTx(r.Context(), tx)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// putGovernance changes the four-eyes settings. Only an owner may, and the
// change is audited with its before and after.
func (s *Server) putGovernance(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner changes governance settings")
		return
	}
	var req governance
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.DefaultRetention != "" {
		if d, err := wd.ParseDuration(req.DefaultRetention); err != nil || d < 24*time.Hour {
			s.fail(w, r, fmt.Errorf("%w: default_retention is a duration of at least 1d, such as 30d", errBadRequest))
			return
		}
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		before, err := governanceTx(r.Context(), tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO governance_settings (tenant_id, four_eyes_publish, four_eyes_policies, default_retention, updated_by) VALUES ($1, $2, $3, NULLIF($4, ''), $5)
			ON CONFLICT (tenant_id) DO UPDATE SET four_eyes_publish = EXCLUDED.four_eyes_publish, four_eyes_policies = EXCLUDED.four_eyes_policies,
			  default_retention = EXCLUDED.default_retention, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			p.TenantID, req.FourEyesPublish, req.FourEyesPolicies, req.DefaultRetention, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "governance.change", "", map[string]any{
			"before": map[string]any{"four_eyes_publish": before.FourEyesPublish, "four_eyes_policies": before.FourEyesPolicies, "default_retention": before.DefaultRetention},
			"after":  map[string]any{"four_eyes_publish": req.FourEyesPublish, "four_eyes_policies": req.FourEyesPolicies, "default_retention": req.DefaultRetention},
		})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// --- approval policies ---

type policyVersion struct {
	Name      string          `json:"name"`
	Version   int             `json:"version"`
	Document  json.RawMessage `json:"document"`
	State     string          `json:"state"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	DecidedBy *string         `json:"decided_by"`
	DecidedAt *time.Time      `json:"decided_at"`
}

const policyColumns = `name, version, document, state, created_by, created_at, decided_by, decided_at`

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	var out []policyVersion
	err := s.tx(r, func(tx pgx.Tx) error {
		// The active version of each policy, and versions waiting for approval.
		rows, err := tx.Query(r.Context(), `SELECT `+policyColumns+` FROM approval_policies WHERE state IN ('active', 'pending') ORDER BY name, version`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[policyVersion])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": nonNil(out)})
}

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	var out []policyVersion
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+policyColumns+` FROM approval_policies WHERE name = $1 ORDER BY version DESC`, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[policyVersion])
		if err == nil && len(out) == 0 {
			return pgx.ErrNoRows
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

// putPolicy saves a new version of a policy. With four-eyes on policy
// edits it waits for a second person; otherwise it is active at once. Runs
// already started keep the version they started with.
func (s *Server) putPolicy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !policy.ValidName(name) {
		s.fail(w, r, fmt.Errorf("%w: policy names are lower-case letters, digits and _", errBadRequest))
		return
	}
	var req struct {
		Document json.RawMessage `json:"document"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := policy.Parse(req.Document); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	doc, err := canonical(req.Document)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var v int
	var state string
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy:' || $1))`, name); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM approval_policies WHERE name = $1`, name).Scan(&v); err != nil {
			return err
		}
		state = "active"
		if g.FourEyesPolicies {
			state = "pending"
		} else if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO approval_policies (tenant_id, name, version, document, state, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			p.TenantID, name, v, doc, state, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "policy.write", fmt.Sprintf("%s/%d", name, v), map[string]any{"state": state})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": name, "version": v, "state": state})
}

// decidePolicy approves or rejects a pending policy version. The person who
// wrote it cannot approve it.
func (s *Server) decidePolicy(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		v, err := strconv.Atoi(chi.URLParam(r, "v"))
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		p := principalFrom(r.Context())
		err = s.tx(r, func(tx pgx.Tx) error {
			ctx := r.Context()
			var state, author string
			if err := tx.QueryRow(ctx, `SELECT state, created_by FROM approval_policies WHERE name = $1 AND version = $2 FOR UPDATE`, name, v).Scan(&state, &author); err != nil {
				return err
			}
			if state != "pending" {
				return fmt.Errorf("%w: version %d is %s", errConflict, v, state)
			}
			if author == p.Actor() {
				return fmt.Errorf("%w: a policy change needs someone other than its author to approve it", errForbidden)
			}
			next := "rejected"
			if approve {
				next = "active"
				if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, name); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = $3, decided_by = $4, decided_at = now() WHERE name = $1 AND version = $2`, name, v, next, p.Actor()); err != nil {
				return err
			}
			return auditTx(r, tx, "policy."+map[bool]string{true: "approve", false: "reject"}[approve], fmt.Sprintf("%s/%d", name, v), nil)
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "version": v, "state": map[bool]string{true: "active", false: "rejected"}[approve]})
	}
}

// missingPolicies lists the approval policies a definition names that have
// no active version: publishing such a workflow would route nothing.
func missingPolicies(ctx context.Context, tx pgx.Tx, doc []byte) ([]problem, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return nil, nil //nolint:nilerr // an invalid definition is reported by the definition checks
	}
	var out []problem
	var walk func([]*wd.Step) error
	walk = func(steps []*wd.Step) error {
		for _, st := range steps {
			if st.Approval != nil && st.Approval.Policy != "" {
				var ok bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_policies WHERE name = $1 AND state = 'active')`, st.Approval.Policy).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					out = append(out, problem{Path: "/steps/" + st.ID + "/config/policy", Message: fmt.Sprintf("approval policy %q has no active version", st.Approval.Policy)})
				}
			}
			for _, sub := range st.Children() {
				if err := walk(sub); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return out, walk(def.Steps)
}

// --- delegation ---

type delegation struct {
	ID        uuid.UUID  `json:"id"`
	FromUser  uuid.UUID  `json:"from_user"`
	FromEmail string     `json:"from_email"`
	ToUser    uuid.UUID  `json:"to_user"`
	ToEmail   string     `json:"to_email"`
	Roles     []string   `json:"roles"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    time.Time  `json:"ends_at"`
	Reason    string     `json:"reason"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// listDelegations shows delegations from or to the caller; member admins
// see all of them.
func (s *Server) listDelegations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out []delegation
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT d.id, d.from_user, fu.email, d.to_user, tu.email, d.roles, d.starts_at, d.ends_at, d.reason, d.created_at, d.revoked_at
			FROM delegations d JOIN users fu ON fu.id = d.from_user JOIN users tu ON tu.id = d.to_user
			WHERE $1 OR d.from_user = $2 OR d.to_user = $2 ORDER BY d.created_at DESC LIMIT 200`, p.Can(PermMemberManage), p.UserID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[delegation])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegations": nonNil(out)})
}

// maxDelegation bounds how long a delegation can last (spec 9.1:
// delegation is explicit and time-boxed).
const maxDelegation = 90 * 24 * time.Hour

// createDelegation lets a member hand some of their approval roles to
// another member for a period, for leave or cover.
func (s *Server) createDelegation(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "only people delegate")
		return
	}
	var req struct {
		To       string     `json:"to"` // email
		Roles    []string   `json:"roles"`
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   time.Time  `json:"ends_at"`
		Reason   string     `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	start := time.Now()
	if req.StartsAt != nil {
		start = *req.StartsAt
	}
	switch {
	case len(req.Roles) == 0 || req.Reason == "":
		s.fail(w, r, fmt.Errorf("%w: roles and a reason are required", errBadRequest))
		return
	case !req.EndsAt.After(start) || req.EndsAt.Sub(start) > maxDelegation:
		s.fail(w, r, fmt.Errorf("%w: a delegation ends after it starts and lasts at most 90 days", errBadRequest))
		return
	}
	for _, role := range req.Roles {
		if !slices.Contains(p.Roles, role) {
			s.fail(w, r, fmt.Errorf("%w: you can only delegate roles you hold (%s)", errForbidden, role))
			return
		}
		if role == "owner" || role == "admin" {
			s.fail(w, r, fmt.Errorf("%w: delegation is for approval roles, not %s", errBadRequest, role))
			return
		}
	}
	id := uuid.Must(uuid.NewV7())
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var to uuid.UUID
		err := tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN memberships m ON m.user_id = u.id WHERE lower(u.email) = lower($1) LIMIT 1`, req.To).Scan(&to)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s is not a member", errBadRequest, req.To)
		}
		if err != nil {
			return err
		}
		if to == p.UserID {
			return fmt.Errorf("%w: delegate to someone else", errBadRequest)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delegations (id, tenant_id, from_user, to_user, roles, starts_at, ends_at, reason, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, p.TenantID, p.UserID, to, req.Roles, start, req.EndsAt, req.Reason, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "delegation.create", id.String(), map[string]any{"to": to, "roles": req.Roles, "starts_at": start, "ends_at": req.EndsAt, "reason": req.Reason})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) revokeDelegation(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE delegations SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL AND ($2 OR from_user = $3)`,
			id, p.Can(PermMemberManage), p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "delegation.revoke", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- TOTP step-up ---

// The TOTP secret is an encrypted secret of the tenant in a reserved
// environment, so it is protected like every other secret.
const identityEnv = "_identity"

func totpName(user uuid.UUID, pending bool) string {
	if pending {
		return "totp_pending_" + user.String()
	}
	return "totp_" + user.String()
}

// beginTOTP issues a new secret to enrol an authenticator app; it is used
// only once confirmed with a code from the app.
func (s *Server) beginTOTP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "only people enrol")
		return
	}
	secret, err := totp.NewSecret()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Vault.Put(r.Context(), p.TenantID, identityEnv, totpName(p.UserID, true), []byte(secret), p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	var email string
	_ = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
	})
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "uri": totp.URI(secret, "Taskiem", email)})
}

func (s *Server) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	secret, err := s.Vault.Get(ctx, p.TenantID, identityEnv, totpName(p.UserID, true))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: start enrolment first", errConflict))
		return
	}
	step, ok := totp.Verify(secret, req.Code, time.Now(), 0)
	if !ok {
		writeErr(w, http.StatusUnprocessableEntity, "that code is not right; check the time on your device")
		return
	}
	if _, err := s.Vault.Put(ctx, p.TenantID, identityEnv, totpName(p.UserID, false), []byte(secret), p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Vault.Delete(ctx, p.TenantID, identityEnv, totpName(p.UserID, true), p.Actor())
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO member_mfa (tenant_id, user_id, totp_confirmed_at, totp_last_step) VALUES ($1, $2, now(), $3)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET totp_confirmed_at = now(), totp_last_step = EXCLUDED.totp_last_step`, p.TenantID, p.UserID, step); err != nil {
			return err
		}
		return auditTx(r, tx, "mfa.totp.enrol", p.UserID.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// verifyTOTP checks a member's code, once: the time step is recorded so the
// same code cannot be replayed.
func (s *Server) verifyTOTP(ctx context.Context, tenant, user uuid.UUID, code string) (bool, error) {
	secret, err := s.Vault.Get(ctx, tenant, identityEnv, totpName(user, false))
	if errors.Is(err, secrets.ErrNotFound) {
		return false, nil // not enrolled
	}
	if err != nil {
		return false, err
	}
	ok := false
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var last int64
		err := tx.QueryRow(ctx, `SELECT totp_last_step FROM member_mfa WHERE user_id = $1 AND totp_confirmed_at IS NOT NULL FOR UPDATE`, user).Scan(&last)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		step, good := totp.Verify(secret, code, time.Now(), last)
		if !good {
			return nil
		}
		ok = true
		_, err = tx.Exec(ctx, `UPDATE member_mfa SET totp_last_step = $2 WHERE user_id = $1`, user, step)
		return err
	})
	return ok, err
}

func (s *Server) removeTOTP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ok, err := s.verifyTOTP(r.Context(), p.TenantID, p.UserID, req.Code)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "a current code is needed to remove the authenticator")
		return
	}
	_ = s.Vault.Delete(r.Context(), p.TenantID, identityEnv, totpName(p.UserID, false), p.Actor())
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE member_mfa SET totp_confirmed_at = NULL WHERE user_id = $1`, p.UserID); err != nil {
			return err
		}
		return auditTx(r, tx, "mfa.totp.remove", p.UserID.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) totpEnrolled(ctx context.Context, tx pgx.Tx, user uuid.UUID) bool {
	var ok bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM member_mfa WHERE user_id = $1 AND totp_confirmed_at IS NOT NULL)`, user).Scan(&ok)
	return ok
}

// --- four-eyes publishing ---

type publishRequest struct {
	WorkflowID  uuid.UUID `json:"workflow_id"`
	Workflow    string    `json:"workflow"`
	Version     int       `json:"version"`
	RequestedBy uuid.UUID `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
	Status      string    `json:"status"`
}

func (s *Server) listPublishRequests(w http.ResponseWriter, r *http.Request) {
	var out []publishRequest
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT q.workflow_id, w.name, q.version, q.requested_by, q.requested_at, q.status
			FROM publish_requests q JOIN workflows w ON w.id = q.workflow_id WHERE q.status = 'pending' ORDER BY q.requested_at`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[publishRequest])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(out)})
}

// decidePublish approves or rejects a pending publish. The person who asked
// to publish, or who wrote the version, cannot approve it.
func (s *Server) decidePublish(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wf, v, err := versionParams(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p := principalFrom(r.Context())
		if p.UserID == uuid.Nil {
			writeErr(w, http.StatusForbidden, "publishing is approved by people, not API keys")
			return
		}
		var req struct {
			Comment string `json:"comment"`
		}
		_ = decodeBody(r, &req)
		var probs []problem
		published := false
		err = s.tx(r, func(tx pgx.Tx) error {
			ctx := r.Context()
			var requester uuid.UUID
			var status string
			var author *string
			if err := tx.QueryRow(ctx, `SELECT q.requested_by, q.status, v.created_by FROM publish_requests q
				JOIN workflow_versions v ON v.workflow_id = q.workflow_id AND v.version = q.version
				WHERE q.workflow_id = $1 AND q.version = $2 FOR UPDATE OF q`, wf, v).Scan(&requester, &status, &author); err != nil {
				return err
			}
			if status != "pending" {
				return fmt.Errorf("%w: the request is %s", errConflict, status)
			}
			if requester == p.UserID || (author != nil && *author == p.Actor()) {
				return fmt.Errorf("%w: publishing needs a second person: not whoever asked, nor the version's author", errForbidden)
			}
			next := "rejected"
			if approve {
				next = "approved"
				by := p.UserID
				var digest string
				var err error
				if probs, digest, published, err = s.publishTx(ctx, tx, p.TenantID, wf, v, &by); err != nil {
					return err
				}
				if err := auditTx(r, tx, "workflow.publish", fmt.Sprintf("%s/%d", wf, v), map[string]any{"digest": digest, "requested_by": requester}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE publish_requests SET status = $3, decided_by = $4, decided_at = now(), comment = NULLIF($5, '') WHERE workflow_id = $1 AND version = $2`,
				wf, v, next, p.UserID, req.Comment); err != nil {
				return err
			}
			return auditTx(r, tx, "publish_request."+map[bool]string{true: "approve", false: "reject"}[approve], fmt.Sprintf("%s/%d", wf, v), map[string]any{"comment": req.Comment})
		})
		if errors.Is(err, errInvalid) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "definition is not valid", "problems": probs})
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := map[string]any{"id": wf, "version": v, "state": map[bool]string{true: "published", false: "draft"}[approve]}
		if published {
			if g := s.proposeToGit(r, wf, v); g != nil {
				out["git"] = g
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
