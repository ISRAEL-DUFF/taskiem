package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
)

// Password reset and change (docs/governance.md#passwords).
//
// A reset link goes by email only to someone who signs in with a password:
// people who sign in only with passkeys or single sign-on are not given a
// password this way. It works once, within thirty minutes, and not after
// five wrong secrets. Setting the new password ends every session of the
// person and signs nobody in, so the passkey rule for administrators and
// SSO enforcement apply as ever at the next sign-in.

const (
	resetTTL        = 30 * time.Minute
	minPasswordLen  = 12
	maxBackground   = 64 // reset emails being prepared at once
	forgotAnswer    = "If that email belongs to an account that signs in with a password, a link to reset it is on its way. It works once, for 30 minutes."
	resetLinkBadMsg = "this reset link is not valid: it may have expired or been used. Ask for a new one."
)

var errResetLink = errors.New("reset link not valid")

// newResetToken makes a link's token: a selector, to find the row, and a
// 256-bit secret, stored only as its SHA-256.
func newResetToken() (token string, selector, secretHash []byte) {
	selector, secret := make([]byte, 16), make([]byte, 32)
	_, _ = rand.Read(selector)
	_, _ = rand.Read(secret)
	h := sha256.Sum256(secret)
	return b64.EncodeToString(selector) + "." + b64.EncodeToString(secret), selector, h[:]
}

func parseResetToken(token string) (selector, secretHash []byte, ok bool) {
	a, b, found := strings.Cut(token, ".")
	if !found {
		return nil, nil, false
	}
	selector, err1 := b64.DecodeString(a)
	secret, err2 := b64.DecodeString(b)
	if err1 != nil || err2 != nil || len(selector) != 16 || len(secret) != 32 {
		return nil, nil, false
	}
	h := sha256.Sum256(secret)
	return selector, h[:], true
}

// mailOn reports whether this deployment can send people email: the mail
// server, a sender, and a public URL for links.
func (s *Server) mailOn() bool {
	return s.Alerts != nil && s.Alerts.Mailer != nil && s.Alerts.From != "" && s.PublicURL != ""
}

func (s *Server) sendMail(ctx context.Context, to, subject, body string) error {
	msg := alerts.BuildEmail(s.Alerts.From, []string{to}, subject, body, uuid.NewString())
	return s.Alerts.Mailer.Send(ctx, s.Alerts.From, []string{to}, msg)
}

// background runs fn after the request has been answered, so how long it
// takes tells the caller nothing. At most maxBackground run at once; more
// are dropped.
func (s *Server) background(ctx context.Context, what string, fn func(context.Context)) {
	if s.bgActive.Add(1) > maxBackground {
		s.bgActive.Add(-1)
		s.Logger.Warn("too much background work: dropped", "what", what)
		return
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer s.bgActive.Add(-1)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		fn(ctx)
	}()
}

// passwordUsable reports whether a password would sign user in anywhere:
// they belong to an active tenant that does not hold them to single sign-on
// (owners are exempt from enforcement, as at sign-in).
func (s *Server) passwordUsable(ctx context.Context, user uuid.UUID) (bool, error) {
	var ok bool
	err := s.Store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM unnest(taskiem_auth_tenant_scope($1, false)) t WHERE NOT taskiem_auth_sso_enforced($1, t))`, user).Scan(&ok)
	return ok, err
}

// forgotPassword sends a reset link. The answer is the same whoever the
// email belongs to, or nobody, and the work happens after answering.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("forgot:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	email := strings.TrimSpace(req.Email)
	if !strings.Contains(email, "@") || len(email) > 320 {
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	// Per address: five links, then one every ten minutes. Over the limit
	// the answer is the same, so it tells nothing either.
	if s.limiter("forgot-email:"+strings.ToLower(email), 10*time.Minute, 5).Allow() {
		ip := clientIP(r)
		s.background(r.Context(), "password reset", func(ctx context.Context) { s.sendPasswordReset(ctx, email, ip) })
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": forgotAnswer})
}

func (s *Server) sendPasswordReset(ctx context.Context, email, ip string) {
	if !s.mailOn() {
		s.Logger.Warn("a password reset was asked for, but this deployment cannot send email (TASKIEM_SMTP_URL, TASKIEM_ALERT_FROM, TASKIEM_PUBLIC_URL): nothing sent")
		return
	}
	var user uuid.UUID
	var hash *string
	var status string
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, password_hash, status FROM taskiem_auth_find_user($1)`, email).Scan(&user, &hash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		s.Logger.Error("password reset", "err", err)
		return
	}
	if hash == nil || status != "active" {
		return // passkeys or SSO only, or disabled: no password to reset
	}
	if ok, err := s.passwordUsable(ctx, user); err != nil || !ok {
		if err != nil {
			s.Logger.Error("password reset", "err", err)
		}
		return
	}
	token, selector, secretHash := newResetToken()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_password_reset_issue($1, $2, $3, $4::interval, $5)`, user, selector, secretHash, resetTTL.String(), ip); err != nil {
		s.Logger.Error("password reset", "err", err)
		return
	}
	// The token is in the fragment, which browsers send to no server: it
	// stays out of access logs and Referer headers.
	link := strings.TrimRight(s.PublicURL, "/") + "/reset-password#token=" + token
	body := fmt.Sprintf(`Someone, hopefully you, asked to reset the password of your Taskiem account (%s).

Choose a new password here. The link works once, for 30 minutes:

%s

If you did not ask, ignore this email: your password stays as it is.
The request came from the address %s.
`, email, link, ip)
	if err := s.sendMail(ctx, email, "Reset your Taskiem password", body); err != nil {
		s.Logger.Error("password reset email not sent", "err", err)
	}
}

// resetPassword sets a new password with a link's token. It does not sign
// in.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	ctx := r.Context()
	if !s.loginLimiter("reset:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Password) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "a password needs at least 12 characters")
		return
	}
	selector, secretHash, ok := parseResetToken(req.Token)
	if !ok {
		writeErr(w, http.StatusBadRequest, resetLinkBadMsg)
		return
	}
	// Per link too; the database locks a link after five wrong secrets.
	if !s.limiter("reset-token:"+hex.EncodeToString(selector), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var user *uuid.UUID
	var outcome string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, outcome FROM taskiem_auth_password_reset_check($1, $2)`, selector, secretHash).Scan(&user, &outcome); err != nil {
		s.fail(w, r, err)
		return
	}
	if outcome != "ok" || user == nil {
		writeErr(w, http.StatusBadRequest, resetLinkBadMsg)
		return
	}
	if ok, err := s.passwordUsable(ctx, *user); err != nil {
		s.fail(w, r, err)
		return
	} else if !ok {
		writeErr(w, http.StatusBadRequest, resetLinkBadMsg)
		return
	}
	newHash := hashPassword(req.Password)
	email, err := s.setPassword(ctx, *user, newHash, nil, func(tx pgx.Tx) error {
		var used *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_password_reset_use($1, $2)`, selector, secretHash).Scan(&used); err != nil {
			return err
		}
		if used == nil || *used != *user {
			return errResetLink
		}
		return nil
	}, "auth.password.reset", clientIP(r))
	switch {
	case errors.Is(err, errResetLink):
		writeErr(w, http.StatusBadRequest, resetLinkBadMsg)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.notifyPasswordChanged(ctx, email, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"status": "Your password is changed and you are signed out everywhere. Sign in with the new password."})
}

// setPassword stores a new password hash for user and ends their sessions
// but keep, auditing action in every tenant they belong to. check runs
// first in the same transaction. It answers the person's email.
func (s *Server) setPassword(ctx context.Context, user uuid.UUID, hash string, keep []byte, check func(pgx.Tx) error, action, ip string) (string, error) {
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&tenants); err != nil {
		return "", err
	}
	if len(tenants) == 0 {
		return "", errResetLink
	}
	var email string
	err := db.InTenantTx(ctx, s.Store.Pool, tenants, func(tx pgx.Tx) error {
		if err := check(tx); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2 RETURNING email, status`, hash, user).Scan(&email, &status); err != nil {
			return err
		}
		if status != "active" {
			return errResetLink
		}
		var revoked int64
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_revoke_sessions($1, $2)`, user, keep).Scan(&revoked); err != nil {
			return err
		}
		for _, t := range tenants {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, $3, $2, jsonb_build_object('ip', $4::text, 'sessions_ended', $5::bigint))`,
				t, user.String(), action, ip, revoked); err != nil {
				return err
			}
		}
		return nil
	})
	return email, err
}

// notifyPasswordChanged tells the person, so a change they did not make
// does not go unnoticed.
func (s *Server) notifyPasswordChanged(ctx context.Context, email, ip string) {
	if !s.mailOn() {
		return
	}
	s.background(ctx, "password changed email", func(ctx context.Context) {
		body := fmt.Sprintf(`The password of your Taskiem account (%s) was just changed, from the address %s, and every session was signed out.

If this was not you, ask for a new password at once with "Forgot password?" on the sign-in page (%s/login), and tell your Taskiem administrator.
`, email, ip, strings.TrimRight(s.PublicURL, "/"))
		if err := s.sendMail(ctx, email, "Your Taskiem password was changed", body); err != nil {
			s.Logger.Error("password change email not sent", "err", err)
		}
	})
}

// changePassword sets the signed-in person's password. With a password,
// they give it; without one (passkeys only), they prove themselves with a
// passkey or authenticator code. Someone with neither signs in by single
// sign-on only and gets no password this way. Their other sessions end.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "passwords belong to people, not API keys")
		return
	}
	var req struct {
		CurrentPassword string          `json:"current_password,omitempty"`
		NewPassword     string          `json:"new_password"`
		TOTP            string          `json:"totp,omitempty"`
		Passkey         *credentialJSON `json:"passkey,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "a password needs at least 12 characters")
		return
	}
	f, hash, err := s.factorsOf(ctx, p.TenantID, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch {
	case f.Password:
		if !s.loginLimiter("reauth:" + p.UserID.String()).Allow() {
			writeErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		if !checkPassword(hash, req.CurrentPassword) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "your current password is not right", "reauth": []string{"password"}})
			return
		}
	case f.Passkey || f.TOTP:
		if !s.proveFactor(w, r, factorProof{TOTP: req.TOTP, Passkey: req.Passkey}, "password.change") {
			return
		}
	default:
		writeErr(w, http.StatusForbidden, "you sign in with single sign-on: there is no Taskiem password to set")
		return
	}
	newHash := hashPassword(req.NewPassword)
	email, err := s.setPassword(ctx, p.UserID, newHash, hashToken(sessionToken(r)), func(pgx.Tx) error { return nil }, "auth.password.change", clientIP(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.notifyPasswordChanged(ctx, email, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// sessionToken is the session the request came with.
func sessionToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}
