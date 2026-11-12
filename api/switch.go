package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// A person in several businesses switches between them (the phone app's
// switcher, docs/spec/mobile-app.md §3.1; WhatsApp's `switch`). A session
// belongs to one business, so switching ends this session and makes one in
// the other business that is the same in every other way: the same person,
// sign-in method, phone, and — what step-up reads — the same moment of
// signing in. Switching is never a fresh sign-in. The other business's own
// rules on signing in (single sign-on) still apply, and a session that
// signed in with one business's single sign-on does not switch at all.

// listBusinesses is GET /v1/me/businesses: the person's businesses by
// name, the one this session is in marked.
func (s *Server) listBusinesses(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "only a person has businesses")
		return
	}
	ts, err := s.tenantsOf(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		b := map[string]any{"id": t.ID, "name": t.Name}
		if t.ID == p.TenantID {
			b["current"] = true
		}
		out = append(out, b)
	}
	writeJSON(w, http.StatusOK, map[string]any{"businesses": out})
}

// switchBusiness is POST /v1/me/switch ({tenant_id}): this session ends and
// one in that business takes its place, answered as signing in is.
func (s *Server) switchBusiness(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	tok := sessionToken(r)
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.AuthMethod == "" || tok == "" {
		writeErr(w, http.StatusForbidden, "only a signed-in person switches business")
		return
	}
	// Single sign-on vouches for a person in one business, through its own
	// identity provider: carried into another, it would stand in for that
	// business's own rules (its passkey requirement skips SSO sessions).
	// A person who signed in that way signs in to the other business.
	if p.AuthMethod == "sso" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "you signed in with your organisation's single sign-on: sign in to that business instead", "sign_in": true})
		return
	}
	var req struct {
		TenantID uuid.UUID `json:"tenant_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, p.UserID).Scan(&tenants); err != nil {
		s.fail(w, r, err)
		return
	}
	member := false
	for _, t := range tenants {
		member = member || t == req.TenantID
	}
	if !member {
		writeErr(w, http.StatusNotFound, "you are not in that business")
		return
	}
	// A business that signs in with single sign-on is signed in to that way.
	var enforced bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_sso_enforced($1, $2)`, p.UserID, req.TenantID).Scan(&enforced); err != nil {
		s.fail(w, r, err)
		return
	}
	if enforced {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": errSSORequired.Error(), "sso_required": true})
		return
	}
	next := newToken()
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID, req.TenantID}, func(tx pgx.Tx) error {
		// The new session is this one's twin in the other business: its
		// moment of signing in, its method, its phone, its expiry (a phone's
		// renewed by use as before, never past 180 days from signing in).
		// Ended first, and only if still live: of two switches racing from
		// one session, one ends it and the other finds nothing to move.
		tag, err := tx.Exec(ctx, `WITH old AS (
				UPDATE sessions SET revoked_at = now()
				 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
				RETURNING user_id, created_at, expires_at, auth_method, device_id, device_name, device_platform, last_used_at)
			INSERT INTO sessions (token_hash, user_id, tenant_id, created_at, expires_at, ip, auth_method, device_id, device_name, device_platform, last_used_at)
			SELECT $2, user_id, $3, created_at, expires_at, $4, auth_method, device_id, device_name, device_platform, last_used_at FROM old`,
			hashToken(tok), hashToken(next), req.TenantID, clientIP(r))
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errNoSession
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.switch', $3, jsonb_build_object('from', $4::text, 'ip', $5::text))`,
			req.TenantID, p.UserID.String(), p.UserID.String(), p.TenantID.String(), clientIP(r))
		return err
	})
	if errors.Is(err, errNoSession) {
		writeErr(w, http.StatusUnauthorized, "this session has ended: sign in again")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"tenant_id": req.TenantID, "tenants": tenants, "user_id": p.UserID}
	s.deliverSession(w, !p.viaCookie, next, out)
	writeJSON(w, http.StatusOK, out)
}

var errNoSession = errors.New("no such session")
