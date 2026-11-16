package api

// Second proofs (docs/spec/taskiem-for-everyone.md §5.4). A code to a phone
// proves only that someone holds the number today, and numbers are given
// to new people. So an account in a business that signed in by phone code
// on a new device proves itself another way before entering a business:
// a passkey (signing in with one, passkeys.go), a one-time link to the
// account's confirmed email, or the chat PIN. The proof is the device's,
// kept in user_devices, and forgotten after 90 days unused.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

const (
	proofLinkTTL     = 15 * time.Minute
	proofLinkPerHour = 3
)

// proofDevice is the device of the request's session, or 400 for a client
// that is not one (an API or CLI token): it cannot be proved.
func (s *Server) proofDevice(w http.ResponseWriter, r *http.Request) (*Principal, []byte, bool) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.Agent != nil || p.AuthMethod == "" {
		writeErr(w, http.StatusForbidden, "a second proof is a person's")
		return nil, nil, false
	}
	key := s.sessionDeviceKey(r, p)
	if key == nil {
		writeErr(w, http.StatusBadRequest, "this client is not a phone app or a browser, so it cannot be proved: sign in on one")
		return nil, nil, false
	}
	return p, key, true
}

// secondProofEmailStart is POST /v1/auth/second-proof/email/start: a
// one-time link to the account's confirmed email, for this device.
func (s *Server) secondProofEmailStart(w http.ResponseWriter, r *http.Request) {
	p, key, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !s.mailOn() {
		writeErr(w, http.StatusServiceUnavailable, "this server cannot send email")
		return
	}
	var email string
	var verified bool
	if err := db.InPersonTx(ctx, s.Store.Pool, p.UserID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).Scan(&email, &verified)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if emailPlaceholder(email) || !verified {
		s.fail(w, r, fmt.Errorf("%w: your account has no confirmed email", errConflict))
		return
	}
	token := newToken()
	var issued bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_proof_link_issue($1, $2, $3, $4::interval, $5)`,
		p.UserID, hashToken(token), key, proofLinkTTL.String(), proofLinkPerHour).Scan(&issued); err != nil {
		s.fail(w, r, err)
		return
	}
	if !issued {
		writeErr(w, http.StatusTooManyRequests, "We have sent you several links. Wait an hour, then ask again.")
		return
	}
	link := strings.TrimRight(s.PublicURL, "/") + "/second-proof#token=" + token
	device := uaSummary(r.UserAgent())
	body := fmt.Sprintf("Someone signing in to your Taskiem account on %s asked to prove it is you.\n\n"+
		"If that is you, open this link on that device within 15 minutes:\n\n%s\n\n"+
		"If it is not you, do nothing: without this link they cannot reach your businesses. Consider changing your phone number in Settings.", device, link)
	if err := s.sendMail(ctx, email, "Confirm it is you signing in to Taskiem", body); err != nil {
		s.Logger.Warn("second proof: the email not sent", "err", err)
		writeErr(w, http.StatusBadGateway, "The email could not be sent just now. Try again in a moment.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_to": maskEmail(email), "expires_in": int(proofLinkTTL.Seconds())})
}

// maskEmail shows enough of an address to recognise it: "a•••@salon.test".
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 1 {
		return "•••"
	}
	return email[:1] + "•••" + email[at:]
}

// secondProofEmailVerify is POST /v1/auth/second-proof/email/verify
// ({token}): opened on the device that asked, it proves that device.
func (s *Server) secondProofEmailVerify(w http.ResponseWriter, r *http.Request) {
	p, key, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	if !s.loginLimiter("second-proof:" + p.UserID.String()).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many tries; wait a little")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var used bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_proof_link_use($1, $2, $3)`, hashToken(strings.TrimSpace(req.Token)), p.UserID, key).Scan(&used); err != nil {
		s.fail(w, r, err)
		return
	}
	if !used {
		writeErr(w, http.StatusBadRequest, "This link has been used, has expired, or was asked for on another device. Open it on the device you are signing in on, or ask for a new one.")
		return
	}
	_ = s.auditEverywhere(r.Context(), p.UserID, "auth.second_proof", map[string]any{"how": "email", "ip": clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"device_proved": true})
}

// secondProofPin is POST /v1/auth/second-proof/pin ({pin}): the chat PIN,
// checked and counted as on WhatsApp (whatsapp_flows.go), proves this
// device.
func (s *Server) secondProofPin(w http.ResponseWriter, r *http.Request) {
	p, key, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Pin string `json:"pin"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var hash string
	var locked *time.Time
	err := s.Store.Pool.QueryRow(ctx, `SELECT pin_hash, locked_until FROM taskiem_wa_pin_state($1)`, p.UserID).Scan(&hash, &locked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		s.fail(w, r, fmt.Errorf("%w: you have no chat PIN", errConflict))
		return
	case err != nil:
		s.fail(w, r, err)
		return
	case locked != nil:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Your PIN is locked after too many wrong tries, until " + locked.UTC().Format("15:04") + " UTC.",
			"locked": true})
		return
	}
	right := validPin(req.Pin) && checkPassword(hash, req.Pin)
	var failures int
	var nowLocked bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT failures, locked FROM taskiem_wa_pin_result($1, $2, $3, $4::interval)`, p.UserID, right, waPinMaxFailures, waPinWindow.String()).
		Scan(&failures, &nowLocked); err != nil {
		s.fail(w, r, err)
		return
	}
	if !right {
		_ = s.auditEverywhere(ctx, p.UserID, "whatsapp.pin.fail", map[string]any{"failures": failures, "via": "second_proof"})
		if nowLocked {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many wrong PINs: your PIN is locked for 15 minutes.", "locked": true})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "That PIN is not right.", "tries_left": waPinMaxFailures - failures})
		return
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_device_prove($1, $2, 'pin')`, p.UserID, key); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.auditEverywhere(ctx, p.UserID, "auth.second_proof", map[string]any{"how": "pin", "ip": clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"device_proved": true})
}
