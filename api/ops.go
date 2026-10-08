package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/oidc"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// The operator console's identity (decision 0027, docs/operator-console.md):
// Taskiem's operators sign in to /v1/ops with a passkey (or single sign-on
// from a configured issuer), get a session of their own (a cookie scoped
// to /v1/ops, an hour by default), and prove their passkey again for
// every write. Operator sessions are never accepted on tenant routes, and
// tenant sessions and keys never on operator routes.

// OpsSettings turns the operator console on.
type OpsSettings struct {
	// SessionTTL is how long an operator session lasts (TASKIEM_OPS_SESSION_TTL,
	// default one hour, at most eight).
	SessionTTL time.Duration
	// OIDC is single sign-on for operators from one issuer
	// (TASKIEM_OPS_OIDC_*); nil is passkeys only.
	OIDC *OpsOIDC
}

// OpsOIDC is the one identity provider operators may sign in with. Only
// existing operators sign in; the first sign-in pins the subject.
type OpsOIDC struct {
	Name         string // shown on the sign-in button
	Issuer       string
	ClientID     string
	ClientSecret string
}

const (
	opsCookie       = "taskiem_ops_session"
	opsCookiePath   = "/v1/ops"
	opsSSOCookie    = "taskiem_ops_sso"
	opsTokenPrefix  = "tsk_ops_"
	opsDefaultTTL   = time.Hour
	opsMaxTTL       = 8 * time.Hour
	opsSSORequestTT = 10 * time.Minute
)

func (o *OpsSettings) ttl() time.Duration {
	if o == nil || o.SessionTTL <= 0 {
		return opsDefaultTTL
	}
	return min(o.SessionTTL, opsMaxTTL)
}

// Operator is a signed-in operator.
type Operator struct {
	ID         uuid.UUID `json:"id"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	AuthMethod string    `json:"auth_method"`
	ExpiresAt  time.Time `json:"expires_at"`
	token      string
}

// Actor is how the operator is recorded in audit chains and status
// updates.
func (o *Operator) Actor() string { return "operator:" + o.Email }

type operatorCtxKey struct{}

func operatorFrom(ctx context.Context) *Operator {
	o, _ := ctx.Value(operatorCtxKey{}).(*Operator)
	return o
}

func (s *Server) opsRoutes(r chi.Router) {
	r.Use(s.opsOn)
	r.Get("/auth/config", s.opsAuthConfig)
	r.Post("/auth/passkey/options", s.opsLoginOptions)
	r.Post("/auth/passkey", s.opsPasskeyLogin)
	r.Post("/auth/enrol/options", s.opsEnrolOptions)
	r.Post("/auth/enrol", s.opsEnrol)
	r.Get("/auth/sso/start", s.opsSSOStart)
	r.Get("/auth/sso/callback", s.opsSSOCallback)
	r.Group(func(r chi.Router) {
		r.Use(s.operatorAuth)
		r.Get("/me", s.opsMe)
		r.Post("/auth/logout", s.opsLogout)
		r.Post("/step-up/options", s.opsStepUpOptions)
		r.Get("/catalogue/queue", s.opsQueue)
		r.Get("/catalogue/submissions/{id}", s.opsSubmission)
		r.Post("/catalogue/submissions/{id}/review", s.opsReview)
		r.Post("/catalogue/revoke", s.opsRevoke)
		r.Get("/catalogue/publishers", s.opsPublishers)
		r.Post("/catalogue/publishers/{slug}/{action}", s.opsPublisherAction)
		r.Get("/catalogue/reviewers", s.opsReviewers)
		r.Get("/status/incidents", s.opsIncidents)
		r.Post("/status/incidents", s.opsOpenIncident)
		r.Post("/status/incidents/{id}/updates", s.opsUpdateIncident)
		r.Get("/tenants", s.opsTenants)
		r.Get("/tenants/{id}", s.opsTenant)
		r.Get("/audit", s.opsAudit)
		r.Get("/audit/verify", s.opsAuditVerify)
		r.Get("/audit/export", s.opsAuditExport)
	})
}

// opsOn answers 404 for every operator route when the console is off.
func (s *Server) opsOn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Ops == nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// operatorAuth admits an operator's session cookie and nothing else: an
// Authorization header (a tenant's session or API key, a status token) is
// refused outright, so no tenant credential ever reaches these routes.
func (s *Server) operatorAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			writeErr(w, http.StatusUnauthorized, "the operator console takes an operator session only, never a tenant's credentials")
			return
		}
		c, err := r.Cookie(opsCookie)
		if err != nil || !strings.HasPrefix(c.Value, opsTokenPrefix) {
			writeErr(w, http.StatusUnauthorized, "operator sign-in required")
			return
		}
		o := Operator{token: c.Value}
		err = s.Store.Pool.QueryRow(r.Context(), `SELECT operator_id, email, name, auth_method, expires_at FROM taskiem_ops_session($1)`, ops.HashToken(c.Value)).
			Scan(&o.ID, &o.Email, &o.Name, &o.AuthMethod, &o.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			s.clearOpsCookie(w)
			writeErr(w, http.StatusUnauthorized, "operator sign-in required")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" {
			writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), operatorCtxKey{}, &o)))
	})
}

func (s *Server) opsAuthConfig(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{"passkeys": s.WebAuthn.RPID != "" && len(s.WebAuthn.Origins) > 0, "sso": s.Ops.OIDC != nil,
		"session_minutes": int(s.Ops.ttl().Minutes())}
	if s.Ops.OIDC != nil {
		out["sso_name"] = s.Ops.OIDC.Name
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) opsMe(w http.ResponseWriter, r *http.Request) {
	o := operatorFrom(r.Context())
	var reviewer bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM taskiem_catalogue_reviewers() WHERE email = $1)`, o.Email).Scan(&reviewer); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operator": o, "reviewer": reviewer})
}

// --- sessions ---

// startOpsSession signs an operator in: a new token, kept hashed, in an
// HttpOnly cookie sent only to /v1/ops (never to tenant routes), and an
// entry in the platform audit chain.
func (s *Server) startOpsSession(w http.ResponseWriter, r *http.Request, operator uuid.UUID, method string) error {
	tok := opsTokenPrefix + newToken()
	ttl := s.Ops.ttl()
	ctx := r.Context()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_ops_session_start($1, $2, $3, make_interval(secs => $4), $5)`,
		ops.HashToken(tok), operator, method, ttl.Seconds(), clientIP(r)); err != nil {
		return err
	}
	var email string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT email FROM taskiem_ops_session($1)`, ops.HashToken(tok)).Scan(&email); err != nil {
		return err
	}
	if err := ops.Audit(ctx, s.Store.Pool, "operator:"+email, "operator.sign_in", email, map[string]any{"method": method, "ip": clientIP(r)}); err != nil {
		return err
	}
	//nolint:gosec // Secure is configuration: off only for plain-HTTP local development.
	http.SetCookie(w, &http.Cookie{Name: opsCookie, Value: tok, Path: opsCookiePath, HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
	return nil
}

func (s *Server) clearOpsCookie(w http.ResponseWriter) {
	//nolint:gosec // as in startOpsSession
	http.SetCookie(w, &http.Cookie{Name: opsCookie, Value: "", Path: opsCookiePath, MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (s *Server) opsLogout(w http.ResponseWriter, r *http.Request) {
	o := operatorFrom(r.Context())
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_ops_session_end($1)`, ops.HashToken(o.token)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := ops.Audit(r.Context(), s.Store.Pool, o.Actor(), "operator.sign_out", o.Email, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	s.clearOpsCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// --- passkeys ---

// opsChallenge issues a single-use operator challenge. A step-up challenge
// carries its scope's hash in its last 16 bytes, as tenants' do (S35).
func (s *Server) opsChallenge(ctx context.Context, purpose string, operator *uuid.UUID, scope string) ([]byte, error) {
	ch := webauthn.NewChallenge()
	var sc *string
	if scope != "" {
		sum := sha256.Sum256([]byte(scope))
		copy(ch[16:], sum[:16])
		sc = &scope
	}
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_ops_challenge_issue($1, $2, $3, make_interval(secs => $4), $5)`, ch, purpose, operator, challengeTTL.Seconds(), sc)
	return ch, err
}

func (s *Server) opsTakeChallenge(ctx context.Context, clientData []byte, purpose string, operator *uuid.UUID, scope string) ([]byte, error) {
	ch, err := challengeOf(clientData)
	if err != nil {
		return nil, err
	}
	var sc *string
	if scope != "" {
		if sum := sha256.Sum256([]byte(scope)); !bytes.Equal(ch[16:], sum[:16]) {
			return nil, errPasskey
		}
		sc = &scope
	}
	var owner *uuid.UUID
	err = s.Store.Pool.QueryRow(ctx, `SELECT operator_id FROM taskiem_ops_challenge_take($1, $2, $3)`, ch, purpose, sc).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errPasskey
	}
	if err != nil {
		return nil, err
	}
	if operator != nil && (owner == nil || *owner != *operator) {
		return nil, errPasskey
	}
	return ch, nil
}

// opsVerifyPasskey checks an operator's assertion; with operator set the
// credential must be theirs, with scope set the challenge must be for it.
func (s *Server) opsVerifyPasskey(ctx context.Context, c credentialJSON, purpose string, operator *uuid.UUID, scope string) (uuid.UUID, error) {
	id, clientData, err := c.decode()
	if err != nil {
		return uuid.Nil, err
	}
	authData, err1 := b64.DecodeString(c.Response.AuthenticatorData)
	sig, err2 := b64.DecodeString(c.Response.Signature)
	if err1 != nil || err2 != nil {
		return uuid.Nil, fmt.Errorf("%w: bad assertion", errBadRequest)
	}
	ch, err := s.opsTakeChallenge(ctx, clientData, purpose, operator, scope)
	if err != nil {
		return uuid.Nil, err
	}
	var owner uuid.UUID
	var cred webauthn.Credential
	var count int64
	var status string
	err = s.Store.Pool.QueryRow(ctx, `SELECT operator_id, public_key, alg, sign_count, status FROM taskiem_ops_credential_find($1)`, id).
		Scan(&owner, &cred.PublicKey, &cred.Alg, &count, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errPasskey
	}
	if err != nil {
		return uuid.Nil, err
	}
	if status != "active" || (operator != nil && owner != *operator) {
		return uuid.Nil, errPasskey
	}
	cred.ID, cred.SignCount = id, uint32(count) //nolint:gosec // stored from a uint32
	n, err := s.WebAuthn.VerifyAssertion(ch, cred, clientData, authData, sig)
	if err != nil {
		return uuid.Nil, err
	}
	var ok bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_ops_credential_used($1, $2, $3)`, id, count, int64(n)).Scan(&ok); err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, webauthn.ErrCloned
	}
	return owner, nil
}

func (s *Server) opsLimit(w http.ResponseWriter, r *http.Request) bool {
	if !s.loginLimiter("ops-login:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many sign-in attempts")
		return false
	}
	return true
}

func (s *Server) opsLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	if !s.limiter("ops-challenge:"+clientIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	ch, err := s.opsChallenge(r.Context(), "login", nil, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge": b64.EncodeToString(ch), "rpId": s.WebAuthn.RPID, "userVerification": "required", "timeout": 60000}})
}

func (s *Server) opsPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) || !s.opsLimit(w, r) {
		return
	}
	var req struct {
		Credential credentialJSON `json:"credential"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	operator, err := s.opsVerifyPasskey(r.Context(), req.Credential, "login", nil, "")
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	if err := s.startOpsSession(w, r, operator, "passkey"); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operator_id": operator})
}

// --- enrolment (a one-time link from taskiem operators add|enrol) ---

type enrolReq struct {
	Token      string          `json:"token"`
	Name       string          `json:"name,omitempty"`
	Credential *credentialJSON `json:"credential,omitempty"`
}

func (s *Server) enrolment(r *http.Request, token string) (id uuid.UUID, email, name string, err error) {
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT operator_id, email, name FROM taskiem_ops_enrol_check($1)`, ops.HashToken(token)).Scan(&id, &email, &name)
	return
}

func (s *Server) opsEnrolOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) || !s.opsLimit(w, r) {
		return
	}
	var req enrolReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	id, email, name, err := s.enrolment(r, req.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "this enrolment link is not valid: it was used, it expired, or the operator is disabled; ask for a new one (taskiem operators enrol)")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_credential_ids($1)`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	existing, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ch, err := s.opsChallenge(r.Context(), "enrol", &id, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	exclude := make([]any, len(existing))
	for i, c := range existing {
		exclude[i] = map[string]any{"type": "public-key", "id": b64.EncodeToString(c)}
	}
	params := make([]any, len(webauthn.Algorithms))
	for i, alg := range webauthn.Algorithms {
		params[i] = map[string]any{"type": "public-key", "alg": alg}
	}
	if name == "" {
		name = email
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "publicKey": map[string]any{
		"challenge":              b64.EncodeToString(ch),
		"rp":                     map[string]any{"id": s.WebAuthn.RPID, "name": s.WebAuthn.RPName},
		"user":                   map[string]any{"id": b64.EncodeToString(id[:]), "name": email, "displayName": name + " (Taskiem operator)"},
		"pubKeyCredParams":       params,
		"authenticatorSelection": map[string]any{"residentKey": "required", "userVerification": "required"},
		"attestation":            "none",
		"excludeCredentials":     exclude,
		"timeout":                60000,
	}})
}

func (s *Server) opsEnrol(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) || !s.opsLimit(w, r) {
		return
	}
	var req enrolReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Credential == nil {
		s.fail(w, r, fmt.Errorf("%w: credential is required", errBadRequest))
		return
	}
	id, email, _, err := s.enrolment(r, req.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "this enrolment link is not valid any more; ask for a new one")
		return
	}
	if err != nil {
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
	ch, err := s.opsTakeChallenge(r.Context(), clientData, "enrol", &id, "")
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
	var ok bool
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_ops_enrol_complete($1, $2, $3, $4, $5, $6, $7, $8, $9)`, ops.HashToken(req.Token), id,
		cred.ID, cred.PublicKey, cred.Alg, int64(cred.SignCount), cred.AAGUID, cred.BackupEligible, name).Scan(&ok)
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		s.fail(w, r, fmt.Errorf("%w: this passkey is already registered", errConflict))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "this enrolment link is not valid any more; ask for a new one")
		return
	}
	if err := ops.Audit(r.Context(), s.Store.Pool, "operator:"+email, "operator.passkey.add", email,
		map[string]any{"name": name, "credential": b64.EncodeToString(cred.ID), "synced": cred.BackupEligible, "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.startOpsSession(w, r, id, "passkey"); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operator_id": id, "passkey": name})
}

// --- step-up: a passkey for every write ---

// opsStepUpOps are the operator writes, each confirmed by a passkey
// assertion bound to it and its target:
//
//	ops.catalogue.review     <submission id>/<approve|reject>
//	ops.catalogue.revoke     <connector>@<version>
//	ops.publisher.verify, ops.publisher.suspend, ops.publisher.reinstate
//	                         <slug>
//	ops.status.open          incident | maintenance
//	ops.status.update        <incident id>/<status>
var opsStepUpOps = map[string]bool{
	"ops.catalogue.review": true, "ops.catalogue.revoke": true,
	"ops.publisher.verify": true, "ops.publisher.suspend": true, "ops.publisher.reinstate": true,
	"ops.status.open": true, "ops.status.update": true,
}

func (s *Server) opsStepUpOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	o := operatorFrom(r.Context())
	var req struct {
		Operation string `json:"operation"`
		Target    string `json:"target"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !opsStepUpOps[req.Operation] || !validTarget(req.Target) {
		s.fail(w, r, fmt.Errorf("%w: name the operation and its target this passkey confirms (operation, target)", errBadRequest))
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_credential_ids($1)`, o.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(ids) == 0 {
		s.fail(w, r, fmt.Errorf("%w: you have no passkey: every operator write needs one (taskiem operators enrol)", errForbidden))
		return
	}
	ch, err := s.opsChallenge(r.Context(), "step_up", &o.ID, stepUpScope(req.Operation, req.Target))
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

// opsStepUp is the passkey assertion every operator write carries.
type opsStepUp struct {
	Passkey *credentialJSON `json:"passkey,omitempty"`
}

// checkOpsStepUp verifies the write's step-up for op and target; on
// failure it answers (403 with "step_up") and returns false.
func (s *Server) checkOpsStepUp(w http.ResponseWriter, r *http.Request, proof *opsStepUp, op, target string) bool {
	o := operatorFrom(r.Context())
	if proof == nil || proof.Passkey == nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm this with your passkey (step_up)", "step_up": "passkey", "operation": op, "target": target})
		return false
	}
	if _, err := s.opsVerifyPasskey(r.Context(), *proof.Passkey, "step_up", &o.ID, stepUpScope(op, target)); err != nil {
		if passkeyRefused(err) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "that passkey response is not valid for this; try again", "step_up": "passkey"})
			return false
		}
		s.fail(w, r, err)
		return false
	}
	return true
}

// --- single sign-on ---

func (s *Server) opsRedirect() string { return s.PublicURL + "/v1/ops/auth/sso/callback" }

func (s *Server) opsProvider() *oidc.Provider {
	c := s.Ops.OIDC
	g := s.Egress
	if g == nil {
		g = &egress.Guard{}
	}
	host := c.Issuer
	if u, err := url.Parse(c.Issuer); err == nil {
		host = u.Hostname()
	}
	return &oidc.Provider{Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		HTTP: g.Client(egress.Policy{Tenant: "platform", Hosts: []string{host}, Purpose: "ops-sso"}, 15*time.Second)}
}

func (s *Server) opsSSOStart(w http.ResponseWriter, r *http.Request) {
	if s.Ops.OIDC == nil {
		writeErr(w, http.StatusNotFound, "single sign-on is not configured for operators")
		return
	}
	if !s.ssoLimit(w, r) {
		return
	}
	state, nonce, verifier, bind := randomState(), oidc.NewNonce(), oidc.NewVerifier(), newToken()
	target, err := s.opsProvider().AuthURL(r.Context(), s.opsRedirect(), b64.EncodeToString(state), nonce, verifier, []string{"openid", "email", "profile"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_ops_sso_begin($1, $2, $3, $4, make_interval(secs => $5))`, state, nonce, verifier,
		hashToken(bind), opsSSORequestTT.Seconds()); err != nil {
		s.fail(w, r, err)
		return
	}
	//nolint:gosec // Secure is configuration, as for the session cookie
	http.SetCookie(w, &http.Cookie{Name: opsSSOCookie, Value: bind, Path: "/v1/ops/auth/sso/", MaxAge: int(opsSSORequestTT.Seconds()), HttpOnly: true,
		Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // the configured operator identity provider
}

// opsSSOFail sends the operator back to the console's sign-in page with a
// reason; the detail goes to the log only.
func (s *Server) opsSSOFail(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Warn("operator single sign-on failed", "err", err, "ip", clientIP(r))
	http.Redirect(w, r, "/ops/login?sso_error="+url.QueryEscape("Single sign-on did not complete. Start again, or sign in with your passkey."), http.StatusFound)
}

func (s *Server) opsSSOCallback(w http.ResponseWriter, r *http.Request) {
	if s.Ops.OIDC == nil {
		writeErr(w, http.StatusNotFound, "single sign-on is not configured for operators")
		return
	}
	if !s.ssoLimit(w, r) {
		return
	}
	q := r.URL.Query()
	raw, err := b64.DecodeString(q.Get("state"))
	if err != nil || len(raw) != 32 {
		s.opsSSOFail(w, r, errSSOFailed)
		return
	}
	var nonce, verifier string
	var binding []byte
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT nonce, verifier, binding FROM taskiem_ops_sso_take($1)`, raw).Scan(&nonce, &verifier, &binding)
	if err != nil {
		s.opsSSOFail(w, r, errSSOFailed)
		return
	}
	//nolint:gosec // as at the start
	http.SetCookie(w, &http.Cookie{Name: opsSSOCookie, Value: "", Path: "/v1/ops/auth/sso/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode})
	if c, err := r.Cookie(opsSSOCookie); err != nil || subtle.ConstantTimeCompare(hashToken(c.Value), binding) != 1 {
		s.opsSSOFail(w, r, errSSOBrowser)
		return
	}
	if e := q.Get("error"); e != "" {
		s.opsSSOFail(w, r, fmt.Errorf("%w (%s)", errSSOFailed, e))
		return
	}
	p := s.opsProvider()
	tok, err := p.Exchange(r.Context(), q.Get("code"), s.opsRedirect(), verifier)
	if err != nil {
		s.opsSSOFail(w, r, err)
		return
	}
	claims, err := p.Verify(r.Context(), tok, nonce, "", time.Now())
	if err != nil {
		s.opsSSOFail(w, r, err)
		return
	}
	if !claims.EmailVerified || claims.Subject == "" {
		s.opsSSOFail(w, r, errors.New("the identity provider has not verified this email address"))
		return
	}
	var id *uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_ops_sso_operator($1, $2, $3)`, s.Ops.OIDC.Issuer, claims.Subject, claims.Email).Scan(&id); err != nil {
		s.opsSSOFail(w, r, err)
		return
	}
	if id == nil {
		_ = ops.Audit(r.Context(), s.Store.Pool, "sso:"+claims.Email, "operator.sign_in.refused", strings.ToLower(claims.Email),
			map[string]any{"issuer": s.Ops.OIDC.Issuer, "ip": clientIP(r)})
		s.opsSSOFail(w, r, fmt.Errorf("%s is not an active operator, or signed in before as another subject", claims.Email))
		return
	}
	if err := s.startOpsSession(w, r, *id, "sso"); err != nil {
		s.opsSSOFail(w, r, err)
		return
	}
	http.Redirect(w, r, "/ops", http.StatusFound)
}
