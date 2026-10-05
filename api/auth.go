package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"github.com/israel-duff/taskiem/engine/db"
)

// Permissions (spec 13.3).
const (
	PermWorkflowRead     = "workflow.read"
	PermWorkflowEdit     = "workflow.edit"
	PermWorkflowPublish  = "workflow.publish"
	PermRunRead          = "run.read"
	PermRunStart         = "run.start"
	PermRunCancel        = "run.cancel"
	PermRunResolve       = "run.resolve"
	PermApprovalDecide   = "approval.decide"
	PermPIIReveal        = "pii.reveal"
	PermPIIErase         = "pii.erase"
	PermSecretManage     = "secret.manage"
	PermConnectionManage = "connection.manage"
	PermMemberManage     = "member.manage"
	PermAuditRead        = "audit.read"
)

var allPermissions = []string{PermWorkflowRead, PermWorkflowEdit, PermWorkflowPublish, PermRunRead, PermRunStart, PermRunCancel,
	PermRunResolve, PermApprovalDecide, PermPIIReveal, PermPIIErase, PermSecretManage, PermConnectionManage, PermMemberManage, PermAuditRead}

// rolePermissions are the built-in roles (spec 13.3). Any other membership
// role (e.g. "credit_officer") is a business role that only qualifies the
// member for approval steps naming it, on top of approval.decide.
var rolePermissions = map[string][]string{
	"owner":    allPermissions,
	"admin":    {PermWorkflowRead, PermWorkflowEdit, PermWorkflowPublish, PermRunRead, PermRunStart, PermRunCancel, PermRunResolve, PermSecretManage, PermConnectionManage, PermMemberManage, PermAuditRead},
	"builder":  {PermWorkflowRead, PermWorkflowEdit, PermRunRead, PermRunStart},
	"operator": {PermWorkflowRead, PermRunRead, PermRunStart, PermRunCancel, PermRunResolve},
	"approver": {PermWorkflowRead, PermRunRead, PermApprovalDecide},
	"auditor":  {PermWorkflowRead, PermRunRead, PermAuditRead},
	"viewer":   {PermWorkflowRead, PermRunRead},
}

// Principal is who is calling.
type Principal struct {
	TenantID    uuid.UUID
	UserID      uuid.UUID // zero for API keys
	KeyID       uuid.UUID // zero for users
	Roles       []string
	Permissions map[string]bool
	Environment string // API keys may be limited to one environment
	viaCookie   bool
}

func (p *Principal) Can(perm string) bool { return p.Permissions[perm] }

func (p *Principal) Actor() string {
	if p.KeyID != uuid.Nil {
		return "key:" + p.KeyID.String()
	}
	return p.UserID.String()
}

func (p *Principal) ActorType() string {
	if p.KeyID != uuid.Nil {
		return "api_key"
	}
	return "user"
}

type ctxKey struct{}

func principalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

const (
	sessionCookie = "taskiem_session"
	sessionTTL    = 12 * time.Hour
	apiKeyPrefix  = "tsk_key_"
	csrfHeader    = "X-Taskiem-Request"
)

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// --- passwords (argon2id, OWASP parameters) ---

const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func checkPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) //nolint:gosec // length of a stored 32-byte key
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked for unknown emails so timing does not reveal them.
var dummyHash = hashPassword(newToken())

// --- authentication middleware ---

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.resolve(r)
		if err != nil || p == nil {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// Cookie sessions must prove the request came from our own pages.
		if p.viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" {
			writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func (s *Server) resolve(r *http.Request) (*Principal, error) {
	ctx := r.Context()
	tok, viaCookie := "", false
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	} else if c, err := r.Cookie(sessionCookie); err == nil {
		tok, viaCookie = c.Value, true
	}
	if tok == "" {
		return nil, nil
	}
	if strings.HasPrefix(tok, apiKeyPrefix) {
		var p Principal
		var perms []string
		var env *string
		err := s.Store.Pool.QueryRow(ctx, `SELECT key_id, tenant_id, permissions, environment FROM taskiem_auth_api_key($1)`, hashToken(tok)).
			Scan(&p.KeyID, &p.TenantID, &perms, &env)
		if err != nil {
			return nil, err
		}
		p.Permissions = map[string]bool{}
		for _, x := range perms {
			p.Permissions[x] = true
		}
		if env != nil {
			p.Environment = *env
		}
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, p.KeyID)
			return err
		})
		return &p, nil
	}
	p := Principal{viaCookie: viaCookie, Permissions: map[string]bool{}}
	if err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, tenant_id FROM taskiem_auth_session($1)`, hashToken(tok)).Scan(&p.UserID, &p.TenantID); err != nil {
		return nil, err
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT role FROM memberships WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, p.UserID)
		if err != nil {
			return err
		}
		p.Roles, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, role := range p.Roles {
		for _, perm := range rolePermissions[role] {
			p.Permissions[perm] = true
		}
	}
	if len(p.Roles) == 0 {
		return nil, errors.New("no membership")
	}
	return &p, nil
}

func (s *Server) need(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !principalFrom(r.Context()).Can(perm) {
				writeErr(w, http.StatusForbidden, "requires "+perm)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- login, logout, signup, me ---

type loginReq struct {
	Email    string    `json:"email"`
	Password string    `json:"password"`
	TenantID uuid.UUID `json:"tenant_id,omitempty"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter(clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req loginReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var userID uuid.UUID
	var hash *string
	var status string
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, password_hash, status FROM taskiem_auth_find_user($1)`, req.Email).Scan(&userID, &hash, &status)
	stored := dummyHash
	if err == nil && hash != nil {
		stored = *hash
	}
	ok := checkPassword(stored, req.Password) && err == nil && hash != nil && status == "active"
	if !ok {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, userID).Scan(&tenants); err != nil || len(tenants) == 0 {
		writeErr(w, http.StatusUnauthorized, "no active membership")
		return
	}
	tenant := tenants[0]
	if req.TenantID != uuid.Nil {
		if !slices.Contains(tenants, req.TenantID) {
			writeErr(w, http.StatusUnauthorized, "not a member of that tenant")
			return
		}
		tenant = req.TenantID
	}
	tok := newToken()
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, ip) VALUES ($1, $2, $3, now() + $4::interval, $5)`,
			hashToken(tok), userID, tenant, sessionTTL.String(), clientIP(r)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.login', $3, jsonb_build_object('ip', $4::text))`, tenant, userID.String(), userID.String(), clientIP(r))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	//nolint:gosec // Secure is configuration: off only for plain-HTTP local development.
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "tenant_id": tenant, "tenants": tenants, "user_id": userID})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(sessionCookie); err == nil {
		tok = c.Value
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1`, hashToken(tok))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode}) //nolint:gosec // as in login
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	perms := make([]string, 0, len(p.Permissions))
	for k := range p.Permissions {
		perms = append(perms, k)
	}
	slices.Sort(perms)
	out := map[string]any{"tenant_id": p.TenantID, "roles": p.Roles, "permissions": perms}
	if p.UserID != uuid.Nil {
		var email, name string
		_ = s.tx(r, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT email, name FROM users WHERE id = $1`, p.UserID).Scan(&email, &name)
		})
		out["user"] = map[string]any{"id": p.UserID, "email": email, "name": name}
	}
	writeJSON(w, http.StatusOK, out)
}

type signupReq struct {
	Tenant   string `json:"tenant"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// signup creates a tenant with its owner, a default workspace, and the dev
// and prod environments.
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter(clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req signupReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	tenant, user, err := CreateTenant(r.Context(), s.Store.Pool, req.Tenant, req.Email, req.Name, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": tenant, "user_id": user})
}

// CreateTenant creates a tenant with its owner, a default workspace, and
// the dev and prod environments (signup, and `taskiem bootstrap`).
func CreateTenant(ctx context.Context, pool *pgxpool.Pool, name, email, userName, password string) (tenant, user uuid.UUID, err error) {
	if name == "" || !strings.Contains(email, "@") || len(password) < 12 {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: tenant, a valid email, and a password of at least 12 characters are required", errBadRequest)
	}
	tenant, user = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err = db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_auth_find_user($1))`, email).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%w: email already registered", errConflict)
		}
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO tenants (id, name, plan_id) VALUES ($1, $2, $3)`, []any{tenant, name, uuid.Nil}},
			{`INSERT INTO users (id, email, name, password_hash) VALUES ($1, $2, $3, $4)`, []any{user, email, userName, hashPassword(password)}},
			{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{tenant, user}},
			{`INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, []any{uuid.Must(uuid.NewV7()), tenant}},
			{`INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, []any{tenant}},
		}
		for _, st := range stmts {
			if _, err := tx.Exec(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'tenant.create', $3, '{}')`, tenant, user.String(), tenant.String())
		return err
	})
	return tenant, user, err
}

// --- members and API keys ---

type memberReq struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	type member struct {
		UserID uuid.UUID `json:"user_id"`
		Email  string    `json:"email"`
		Name   string    `json:"name"`
		Roles  []string  `json:"roles"`
	}
	var out []member
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT u.id, u.email, u.name, array_agg(m.role ORDER BY m.role) FROM memberships m JOIN users u ON u.id = m.user_id
			GROUP BY u.id, u.email, u.name ORDER BY u.email`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[member])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

// addMember creates (or finds) a user and grants roles. Only an owner may
// grant owner.
func (s *Server) addMember(w http.ResponseWriter, r *http.Request) {
	var req memberReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if slices.Contains(req.Roles, "owner") && !slices.Contains(p.Roles, "owner") {
		s.fail(w, r, fmt.Errorf("%w: only an owner can grant owner", errForbidden))
		return
	}
	if len(req.Roles) == 0 || !strings.Contains(req.Email, "@") {
		s.fail(w, r, fmt.Errorf("%w: email and at least one role are required", errBadRequest))
		return
	}
	var user uuid.UUID
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, req.Email).Scan(&user)
		if errors.Is(err, pgx.ErrNoRows) {
			if len(req.Password) < 12 {
				return fmt.Errorf("%w: new users need a password of at least 12 characters", errBadRequest)
			}
			user = uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash) VALUES ($1, $2, $3, $4)`, user, req.Email, req.Name, hashPassword(req.Password)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		for _, role := range req.Roles {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, p.TenantID, user, role); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "member.grant", user.String(), map[string]any{"roles": req.Roles})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user_id": user})
}

type keyReq struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	Environment string   `json:"environment,omitempty"`
	ExpiresDays int      `json:"expires_days,omitempty"`
}

// createKey issues a scoped API key and shows it once. A key cannot carry a
// permission its creator lacks.
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var req keyReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	for _, perm := range req.Permissions {
		if !p.Can(perm) {
			s.fail(w, r, fmt.Errorf("%w: cannot grant %s", errForbidden, perm))
			return
		}
	}
	if req.Name == "" || len(req.Permissions) == 0 {
		s.fail(w, r, fmt.Errorf("%w: name and permissions are required", errBadRequest))
		return
	}
	days := req.ExpiresDays
	if days <= 0 || days > 365 {
		days = 90
	}
	key := apiKeyPrefix + newToken()
	id := uuid.Must(uuid.NewV7())
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO api_keys (id, tenant_id, name, key_hash, prefix, permissions, environment, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, now() + make_interval(days => $9))`,
			id, p.TenantID, req.Name, hashToken(key), key[:len(apiKeyPrefix)+6], req.Permissions, req.Environment, p.Actor(), days); err != nil {
			return err
		}
		return auditTx(r, tx, "api_key.create", id.String(), map[string]any{"permissions": req.Permissions, "environment": req.Environment})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "key": key, "expires_days": days})
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	type key struct {
		ID          uuid.UUID  `json:"id"`
		Name        string     `json:"name"`
		Prefix      string     `json:"prefix"`
		Permissions []string   `json:"permissions"`
		ExpiresAt   time.Time  `json:"expires_at"`
		LastUsedAt  *time.Time `json:"last_used_at"`
		RevokedAt   *time.Time `json:"revoked_at"`
	}
	var out []key
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, prefix, permissions, expires_at, last_used_at, revoked_at FROM api_keys ORDER BY created_at DESC`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[key])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": out})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "api_key.revoke", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
