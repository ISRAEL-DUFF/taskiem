package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// Passkeys (spec 13.2): sign-in and step-up with WebAuthn. Admins are held
// to passkeys when RequireAdminPasskeys is set: a password session of a
// member with administrative permissions can only enrol a passkey, and
// once they have one, their password no longer signs them in.

// adminPerms are the permissions that make a member an administrator for
// the passkey rule.
var adminPerms = []string{PermMemberManage, PermRoleManage, PermSecretManage, PermPolicyManage, PermGitManage, PermConnectorManage, PermPIIReveal, PermPIIErase}

func isAdmin(perms map[string]bool) bool {
	for _, p := range adminPerms {
		if perms[p] {
			return true
		}
	}
	return false
}

// enrolOnly are the requests a password session held to the passkey rule
// may make.
var enrolOnly = map[string]bool{
	"GET /v1/me": true, "GET /v1/me/passkeys": true, "POST /v1/me/passkeys/options": true, "POST /v1/me/passkeys": true, "POST /v1/auth/logout": true,
}

const challengeTTL = 5 * time.Minute

var b64 = base64.RawURLEncoding

// credentialJSON is PublicKeyCredential.toJSON() from the browser.
type credentialJSON struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string   `json:"clientDataJSON"`
		AttestationObject string   `json:"attestationObject"`
		AuthenticatorData string   `json:"authenticatorData"`
		Signature         string   `json:"signature"`
		UserHandle        string   `json:"userHandle"`
		Transports        []string `json:"transports"`
	} `json:"response"`
}

func (c credentialJSON) decode() (id, clientData []byte, err error) {
	if c.Type != "public-key" {
		return nil, nil, fmt.Errorf("%w: not a public-key credential", errBadRequest)
	}
	if id, err = b64.DecodeString(c.RawID); err != nil || len(id) == 0 {
		return nil, nil, fmt.Errorf("%w: bad credential id", errBadRequest)
	}
	if clientData, err = b64.DecodeString(c.Response.ClientDataJSON); err != nil {
		return nil, nil, fmt.Errorf("%w: bad clientDataJSON", errBadRequest)
	}
	return id, clientData, nil
}

// challengeOf reads the challenge the browser signed, to look it up.
func challengeOf(clientData []byte) ([]byte, error) {
	var cd struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(clientData, &cd); err != nil {
		return nil, fmt.Errorf("%w: bad clientDataJSON", errBadRequest)
	}
	ch, err := b64.DecodeString(cd.Challenge)
	if err != nil || len(ch) != 32 {
		return nil, fmt.Errorf("%w: bad challenge", errBadRequest)
	}
	return ch, nil
}

func (s *Server) passkeysOn(w http.ResponseWriter) bool {
	if s.WebAuthn.RPID == "" || len(s.WebAuthn.Origins) == 0 {
		writeErr(w, http.StatusNotImplemented, "passkeys are not configured on this deployment (TASKIEM_PUBLIC_URL)")
		return false
	}
	return true
}

func (s *Server) issueChallenge(ctx context.Context, purpose string, user *uuid.UUID) ([]byte, error) {
	ch := webauthn.NewChallenge()
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_challenge_issue($1, $2, $3, $4::interval)`, ch, purpose, user, challengeTTL.String())
	return ch, err
}

// takeChallenge consumes the challenge a response was made for. It works
// once, within five minutes, for the purpose and person it was issued to.
func (s *Server) takeChallenge(ctx context.Context, clientData []byte, purpose string, user *uuid.UUID) ([]byte, error) {
	ch, err := challengeOf(clientData)
	if err != nil {
		return nil, err
	}
	var owner *uuid.UUID
	err = s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_challenge_take($1, $2)`, ch, purpose).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errPasskey
	}
	if err != nil {
		return nil, err
	}
	if user != nil && (owner == nil || *owner != *user) {
		return nil, errPasskey
	}
	return ch, nil
}

var errPasskey = errors.New("that passkey response is not valid here; try again")

// --- sign-in ---

func (s *Server) passkeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	// Issuing a challenge is cheap but stores a row: its own, looser limit.
	if !s.limiter("challenge:"+clientIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	ch, err := s.issueChallenge(r.Context(), "login", nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Discoverable credentials: the browser offers the person's passkeys
	// for this site, so no email is asked for or revealed.
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge": b64.EncodeToString(ch), "rpId": s.WebAuthn.RPID, "userVerification": "required", "timeout": 60000}})
}

func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	if !s.loginLimiter(clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Credential credentialJSON `json:"credential"`
		TenantID   uuid.UUID      `json:"tenant_id,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	user, err := s.verifyPasskey(r.Context(), req.Credential, "login", nil)
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	s.startSession(w, r, user, req.TenantID, "passkey")
}

// verifyPasskey checks an assertion against the stored credential and
// records its use. With user set, the credential must be theirs.
func (s *Server) verifyPasskey(ctx context.Context, c credentialJSON, purpose string, user *uuid.UUID) (uuid.UUID, error) {
	id, clientData, err := c.decode()
	if err != nil {
		return uuid.Nil, err
	}
	authData, err1 := b64.DecodeString(c.Response.AuthenticatorData)
	sig, err2 := b64.DecodeString(c.Response.Signature)
	if err1 != nil || err2 != nil {
		return uuid.Nil, fmt.Errorf("%w: bad assertion", errBadRequest)
	}
	ch, err := s.takeChallenge(ctx, clientData, purpose, user)
	if err != nil {
		return uuid.Nil, err
	}
	var owner uuid.UUID
	var cred webauthn.Credential
	var count int64
	var status string
	err = s.Store.Pool.QueryRow(ctx, `SELECT user_id, public_key, alg, sign_count, status FROM taskiem_auth_find_passkey($1)`, id).
		Scan(&owner, &cred.PublicKey, &cred.Alg, &count, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errPasskey
	}
	if err != nil {
		return uuid.Nil, err
	}
	if status != "active" || (user != nil && owner != *user) {
		return uuid.Nil, errPasskey
	}
	cred.ID, cred.SignCount = id, uint32(count) //nolint:gosec // stored from a uint32
	n, err := s.WebAuthn.VerifyAssertion(ch, cred, clientData, authData, sig)
	if err != nil {
		return uuid.Nil, err
	}
	var ok bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_passkey_used($1, $2, $3)`, id, count, int64(n)).Scan(&ok); err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, webauthn.ErrCloned // another sign-in used this counter first
	}
	return owner, nil
}

func (s *Server) passkeyFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, webauthn.ErrCloned):
		s.Logger.Warn("passkey counter did not advance", "ip", clientIP(r))
		writeErr(w, http.StatusUnauthorized, "this passkey may have been copied; sign in another way and remove it")
	case errors.Is(err, errPasskey), errors.Is(err, webauthn.ErrInvalid):
		writeErr(w, http.StatusUnauthorized, errPasskey.Error())
	default:
		s.fail(w, r, err)
	}
}

// --- registration ---

func (s *Server) passkeyRegisterOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "passkeys belong to people, not API keys")
		return
	}
	var email, name string
	var existing [][]byte
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT email, name FROM users WHERE id = $1`, p.UserID).Scan(&email, &name); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT id FROM webauthn_credentials WHERE user_id = $1`, p.UserID)
		if err != nil {
			return err
		}
		existing, err = pgx.CollectRows(rows, pgx.RowTo[[]byte])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ch, err := s.issueChallenge(r.Context(), "register", &p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	exclude := make([]any, len(existing))
	for i, id := range existing {
		exclude[i] = map[string]any{"type": "public-key", "id": b64.EncodeToString(id)}
	}
	params := make([]any, len(webauthn.Algorithms))
	for i, alg := range webauthn.Algorithms {
		params[i] = map[string]any{"type": "public-key", "alg": alg}
	}
	if name == "" {
		name = email
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge":              b64.EncodeToString(ch),
		"rp":                     map[string]any{"id": s.WebAuthn.RPID, "name": s.WebAuthn.RPName},
		"user":                   map[string]any{"id": b64.EncodeToString(p.UserID[:]), "name": email, "displayName": name},
		"pubKeyCredParams":       params,
		"authenticatorSelection": map[string]any{"residentKey": "required", "userVerification": "required"},
		"attestation":            "none",
		"excludeCredentials":     exclude,
		"timeout":                60000,
	}})
}

func (s *Server) passkeyRegister(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	p := principalFrom(r.Context())
	var req struct {
		Name       string         `json:"name"`
		Credential credentialJSON `json:"credential"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	_, clientData, err := req.Credential.decode()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	att, err := b64.DecodeString(req.Credential.Response.AttestationObject)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: bad attestationObject", errBadRequest))
		return
	}
	ch, err := s.takeChallenge(r.Context(), clientData, "register", &p.UserID)
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	cred, err := s.WebAuthn.VerifyRegistration(ch, clientData, att)
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO webauthn_credentials (id, user_id, public_key, alg, sign_count, aaguid, backup_eligible, name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, cred.ID, p.UserID, cred.PublicKey, cred.Alg, int64(cred.SignCount), cred.AAGUID, cred.BackupEligible, name); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
				return fmt.Errorf("%w: this passkey is already registered", errConflict)
			}
			return err
		}
		return auditTx(r, tx, "passkey.add", p.UserID.String(), map[string]any{"name": name, "credential": b64.EncodeToString(cred.ID), "synced": cred.BackupEligible})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": b64.EncodeToString(cred.ID), "name": name, "synced": cred.BackupEligible})
}

type passkeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Synced     bool       `json:"synced"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out []passkeyInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, backup_eligible, created_at, last_used_at FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`, p.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id []byte
			var k passkeyInfo
			if err := rows.Scan(&id, &k.Name, &k.Synced, &k.CreatedAt, &k.LastUsedAt); err != nil {
				return err
			}
			k.ID = b64.EncodeToString(id)
			out = append(out, k)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": nonNil(out)})
}

func (s *Server) removePasskey(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := b64.DecodeString(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`, id, p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "passkey.remove", p.UserID.String(), map[string]any{"credential": chi.URLParam(r, "id")})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetPasskeys removes every passkey of a member who lost theirs. The
// caller needs the standing to grant all of the member's roles.
func (s *Server) resetPasskeys(w http.ResponseWriter, r *http.Request) {
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT role FROM memberships WHERE user_id = $1`, user)
		if err != nil {
			return err
		}
		roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if len(roles) == 0 {
			return pgx.ErrNoRows
		}
		if err := canGrant(r.Context(), tx, p, roles); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `DELETE FROM webauthn_credentials WHERE user_id = $1`, user)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, user); err != nil {
			return err
		}
		return auditTx(r, tx, "passkey.reset", user.String(), map[string]any{"removed": tag.RowsAffected()})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- step-up ---

func (s *Server) stepUpOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	p := principalFrom(r.Context())
	var ids [][]byte
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id FROM webauthn_credentials WHERE user_id = $1`, p.UserID)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[[]byte])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(ids) == 0 {
		s.fail(w, r, fmt.Errorf("%w: you have no passkey; add one under Account", errBadRequest))
		return
	}
	ch, err := s.issueChallenge(r.Context(), "step_up", &p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allow := make([]any, len(ids))
	for i, id := range ids {
		allow[i] = map[string]any{"type": "public-key", "id": b64.EncodeToString(id)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge": b64.EncodeToString(ch), "rpId": s.WebAuthn.RPID, "allowCredentials": allow, "userVerification": "required", "timeout": 60000}})
}

// --- sessions shared by password, passkey and SSO sign-in ---

// startSession signs user in to tenant (or their first), by method.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user, want uuid.UUID, method string) {
	ctx := r.Context()
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&tenants); err != nil || len(tenants) == 0 {
		writeErr(w, http.StatusUnauthorized, "no active membership")
		return
	}
	tenant := tenants[0]
	if want != uuid.Nil {
		if !slices.Contains(tenants, want) {
			writeErr(w, http.StatusUnauthorized, "not a member of that tenant")
			return
		}
		tenant = want
	}
	tok := newToken()
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, ip, auth_method) VALUES ($1, $2, $3, now() + $4::interval, $5, $6)`,
			hashToken(tok), user, tenant, sessionTTL.String(), clientIP(r), method); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.login', $3, jsonb_build_object('ip', $4::text, 'method', $5::text))`,
			tenant, user.String(), user.String(), clientIP(r), method)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	//nolint:gosec // Secure is configuration: off only for plain-HTTP local development.
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "tenant_id": tenant, "tenants": tenants, "user_id": user})
}

// ResetPasskeys removes every passkey of the person with email and ends
// their sessions, for an operator recovering the last owner of a tenant
// (taskiem passkeys reset). It is audited in each of their tenants.
func ResetPasskeys(ctx context.Context, pool *pgxpool.Pool, email, operator string) (int64, error) {
	var user uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, email).Scan(&user); err != nil {
		return 0, fmt.Errorf("no user %s: %w", email, err)
	}
	var tenants []uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&tenants); err != nil || len(tenants) == 0 {
		return 0, fmt.Errorf("%s belongs to no active tenant", email)
	}
	var removed int64
	err := db.InTenantTx(ctx, pool, tenants, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM webauthn_credentials WHERE user_id = $1`, user)
		if err != nil {
			return err
		}
		removed = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, user); err != nil {
			return err
		}
		for _, t := range tenants {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, 'passkey.reset', $3, jsonb_build_object('removed', $4::bigint, 'via', 'cli'))`,
				t, operator, user.String(), removed); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}
