package api

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Invitations (docs/governance.md#members-and-invitations). A tenant
// invites someone who already has an account; they accept or decline, and
// the tenant's admins see and cancel what is pending. A person with an
// account but no membership anywhere signs in to an invitee session: it
// reaches their invitations and nothing else, and becomes an ordinary
// session in the tenant whose invitation they accept.

type pendingInvitation struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Roles     []string  `json:"roles"`
	Source    string    `json:"source"`
	InvitedBy string    `json:"invited_by"`
	InvitedAt time.Time `json:"invited_at"`
}

// listInvitations lists the tenant's pending invitations (member.manage).
func (s *Server) listInvitations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out []pendingInvitation
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT i.user_id, u.email, u.name, i.roles, i.source, i.invited_by, i.created_at
			FROM member_invitations i JOIN taskiem_tenant_invitees($1) u ON u.user_id = i.user_id ORDER BY i.created_at`, p.TenantID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[pendingInvitation])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": nonNil(out)})
}

// cancelInvitation withdraws a pending invitation (member.manage).
func (s *Server) cancelInvitation(w http.ResponseWriter, r *http.Request) {
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var roles []string
		if err := tx.QueryRow(r.Context(), `DELETE FROM member_invitations WHERE user_id = $1 RETURNING roles`, user).Scan(&roles); err != nil {
			return err
		}
		return auditTx(r, tx, "member.invitation.cancel", user.String(), map[string]any{"roles": roles})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// declineInvitation is the invitee turning an invitation down.
func (s *Server) declineInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	if err != nil || p.UserID == uuid.Nil || !s.invitedBy(r, p.UserID, tenant) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var roles []string
		if err := tx.QueryRow(ctx, `DELETE FROM member_invitations WHERE user_id = $1 RETURNING roles`, p.UserID).Scan(&roles); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'member.invitation.decline', $2, jsonb_build_object('roles', $3::jsonb, 'ip', $4::text))`,
			tenant, p.UserID.String(), toJSONB(roles), clientIP(r))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// invitedBy reports whether an active tenant invites the person.
func (s *Server) invitedBy(r *http.Request, user, tenant uuid.UUID) bool {
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_invited_tenants($1)`, user).Scan(&tenants); err != nil {
		return false
	}
	return slices.Contains(tenants, tenant)
}

// --- invitee sessions ---

// inviteeRoutes are all an invitee session may reach.
var inviteeRoutes = regexp.MustCompile(`^(GET /v1/me|GET /v1/me/invitations|POST /v1/me/invitations/[^/]+/(accept|decline)|POST /v1/auth/logout)$`)

func inviteeAllowed(method, path string) bool {
	return inviteeRoutes.MatchString(method + " " + strings.TrimSuffix(path, "/"))
}

// startInviteeSession signs in a person who belongs to no tenant but is
// invited by one, to answer their invitations. It reports false when they
// are invited by none (the caller refuses the sign-in as before).
func (s *Server) startInviteeSession(w http.ResponseWriter, r *http.Request, user uuid.UUID, method string, bearer bool) (bool, error) {
	tok := newToken()
	var ok bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_invitee_start($1, $2, $3, $4::interval, $5)`,
		hashToken(tok), user, method, sessionTTL.String(), clientIP(r)).Scan(&ok); err != nil || !ok {
		return false, err
	}
	out := map[string]any{"user_id": user, "tenants": []uuid.UUID{}, "invitations_only": true}
	s.deliverSession(w, bearer, tok, out)
	writeJSON(w, http.StatusOK, out)
	return true, nil
}

// requestToken is the session or key token the request carries.
func requestToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// resolveInvitee reads an invitee session; nil when tok is not one.
func (s *Server) resolveInvitee(r *http.Request, tok string, viaCookie bool) (*Principal, error) {
	p := Principal{viaCookie: viaCookie, Permissions: map[string]bool{}, Invitee: true}
	err := s.Store.Pool.QueryRow(r.Context(), `SELECT user_id, email, name, auth_method FROM taskiem_auth_invitee_session($1)`, hashToken(tok)).
		Scan(&p.UserID, &p.inviteeEmail, &p.inviteeName, &p.AuthMethod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// upgradeInvitee turns an invitee session into an ordinary session in the
// tenant whose invitation was just accepted. It reports whether it did: a
// tenant that requires single sign-on needs the person to sign in again.
func (s *Server) upgradeInvitee(w http.ResponseWriter, r *http.Request, p *Principal, tenant uuid.UUID) (map[string]any, error) {
	sess, err := s.createSession(r, p.UserID, tenant, p.AuthMethod)
	if errors.Is(err, errSSORequired) {
		return map[string]any{"sign_in": "sso_required"}, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_auth_invitee_end($1)`, hashToken(requestToken(r))); err != nil {
		return nil, err
	}
	// The new session goes the way the invitee session came: a cookie
	// stays a cookie, a bearer token is answered with a token.
	out := map[string]any{"tenants": sess.tenants}
	s.deliverSession(w, !p.viaCookie, sess.token, out)
	return out, nil
}
