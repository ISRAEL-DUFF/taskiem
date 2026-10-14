package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// Invitation links (decision 0030): someone a partner invited who has no
// account yet follows the link from their email, creates a passkey, and is
// a member of the inviting sub-tenant, signed in, in one step. The link
// works once, for seven days, only while the invitation stands and nobody
// can sign in as the account; anyone with an account signs in and accepts
// as usual (GET /v1/me/invitations).

const invitationLinkBadMsg = "this invitation link is not valid: it may have expired, been used or withdrawn. If you have set up your account, sign in to accept your invitations."

var errInvitationLink = errors.New("invitation link not valid")

// checkInvitationLink reads a link's token, within the per-address and
// per-link rates. It answers the request itself when the token is no good.
func (s *Server) checkInvitationLink(w http.ResponseWriter, r *http.Request, token string) (user, tenant uuid.UUID, email string, ok bool) {
	if !s.loginLimiter("invitation:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	selector, secretHash, valid := parseResetToken(token)
	if !valid {
		writeErr(w, http.StatusBadRequest, invitationLinkBadMsg)
		return
	}
	if !s.limiter("invitation-token:"+hex.EncodeToString(selector), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var u, t *uuid.UUID
	var e *string
	var outcome string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT user_id, tenant_id, email, outcome FROM taskiem_auth_invitation_link_check($1, $2)`, selector, secretHash).
		Scan(&u, &t, &e, &outcome); err != nil {
		s.fail(w, r, err)
		return
	}
	if outcome != "ok" || u == nil || t == nil || e == nil {
		writeErr(w, http.StatusBadRequest, invitationLinkBadMsg)
		return
	}
	return *u, *t, *e, true
}

// invitationLinkOptions shows what a link invites to and issues the
// challenge for the passkey that accepts it: {"token"}.
func (s *Server) invitationLinkOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	user, tenant, email, ok := s.checkInvitationLink(w, r, req.Token)
	if !ok {
		return
	}
	var tenantName string
	var roles []string
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&tenantName); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT roles FROM member_invitations WHERE user_id = $1`, user).Scan(&roles)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ch, err := s.issueChallenge(r.Context(), "register", &user, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	params := make([]any, len(webauthn.Algorithms))
	for i, alg := range webauthn.Algorithms {
		params[i] = map[string]any{"type": "public-key", "alg": alg}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenant, "tenant": tenantName, "roles": nonNil(roles), "email": email,
		"publicKey": map[string]any{
			"challenge":              b64.EncodeToString(ch),
			"rp":                     map[string]any{"id": s.WebAuthn.RPID, "name": s.WebAuthn.RPName},
			"user":                   map[string]any{"id": b64.EncodeToString(user[:]), "name": email, "displayName": email},
			"pubKeyCredParams":       params,
			"authenticatorSelection": map[string]any{"residentKey": "required", "userVerification": "required"},
			"attestation":            "none",
			"excludeCredentials":     []any{},
			"timeout":                60000,
		}})
}

// acceptInvitationLink registers the passkey, accepts the invitation and
// signs the person in: {"token", "credential", "name"?, "passkey_name"?,
// "bearer"?}. All of it happens in one transaction, or none of it.
func (s *Server) acceptInvitationLink(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) {
		return
	}
	var req struct {
		Token       string         `json:"token"`
		Credential  credentialJSON `json:"credential"`
		Name        string         `json:"name,omitempty"`
		PasskeyName string         `json:"passkey_name,omitempty"`
		Bearer      bool           `json:"bearer,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	user, tenant, _, ok := s.checkInvitationLink(w, r, req.Token)
	if !ok {
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
	ch, err := s.takeChallenge(r.Context(), clientData, "register", &user, "")
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	cred, err := s.WebAuthn.VerifyRegistration(ch, clientData, att)
	if err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	keyName := strings.TrimSpace(req.PasskeyName)
	if keyName == "" {
		keyName = "Passkey"
	}
	keyName = firstRunes(keyName, 64)
	name := firstRunes(strings.TrimSpace(req.Name), 200)
	selector, secretHash, _ := parseResetToken(req.Token)
	ctx := r.Context()
	var roles []string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var used, usedTenant uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id, tenant_id FROM taskiem_auth_invitation_link_use($1, $2)`, selector, secretHash).Scan(&used, &usedTenant); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errInvitationLink
			}
			return err
		}
		if used != user || usedTenant != tenant {
			return errInvitationLink
		}
		if err := tx.QueryRow(ctx, `DELETE FROM member_invitations WHERE user_id = $1 RETURNING roles`, user).Scan(&roles); err != nil {
			return err
		}
		for _, role := range roles {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tenant, user, role); err != nil {
				return err
			}
		}
		// A member now: the passkey and the name are theirs to set.
		if name != "" {
			if _, err := tx.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, user, name); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO webauthn_credentials (id, user_id, public_key, alg, sign_count, aaguid, backup_eligible, name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, cred.ID, user, cred.PublicKey, cred.Alg, int64(cred.SignCount), cred.AAGUID, cred.BackupEligible, keyName); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: this passkey is already registered", errConflict)
			}
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'passkey.add', $2, jsonb_build_object('name', $3::text, 'credential', $4::text, 'synced', $5::boolean, 'ip', $6::text))`,
			tenant, user.String(), keyName, b64.EncodeToString(cred.ID), cred.BackupEligible, clientIP(r)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'member.invitation.accept', $2, jsonb_build_object('roles', $3::jsonb, 'via', 'link', 'ip', $4::text))`,
			tenant, user.String(), toJSONB(roles), clientIP(r))
		return err
	})
	if errors.Is(err, errInvitationLink) {
		writeErr(w, http.StatusBadRequest, invitationLinkBadMsg)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"tenant_id": tenant, "user_id": user, "roles": nonNil(roles)}
	sess, err := s.createSession(r, user, tenant, "passkey")
	switch {
	case errors.Is(err, errSSORequired):
		out["sign_in"] = "sso_required"
	case err != nil:
		s.fail(w, r, err)
		return
	default:
		out["tenants"] = sess.tenants
		s.deliverSession(w, req.Bearer, sess.token, out)
	}
	writeJSON(w, http.StatusOK, out)
}

// firstRunes keeps at most n characters of s.
func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
