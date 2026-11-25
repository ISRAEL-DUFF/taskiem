package api

// Second proofs (docs/spec/taskiem-for-everyone.md §5.4). A code to a phone
// proves only that someone holds the number today, and numbers are given
// to new people. So an account in a business that signed in by phone code
// on a new device proves itself another way before entering a business:
// a passkey (signing in with one, passkeys.go, or an assertion here), a
// one-time link to the account's real email, or the person's password.
// The PIN is for paying and approving only, never a second proof. The
// proof is the device's, kept in user_devices, and forgotten after 90 days
// unused.

import (
	"context"
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
	if err := db.InPersonTx(ctx, s.Store.Pool, p.UserID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	// A real email, confirmed or not: the link goes to it, and only its
	// reader can use it (§5.4). A placeholder receives no mail.
	if emailPlaceholder(email) {
		s.fail(w, r, fmt.Errorf("%w: your account has no email", errConflict))
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
	_ = s.auditEverywhere(ctx, p.UserID, "auth.second_proof_link", map[string]any{"sent_to": maskEmail(email), "ip": clientIP(r), "device": device})
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

// deviceProved records a second proof for the device and answers 200
// {device_proved: true}.
func (s *Server) deviceProved(w http.ResponseWriter, r *http.Request, user uuid.UUID, key []byte, how string) {
	ctx := r.Context()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_device_prove($1, $2, $3)`, user, key, how); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.auditEverywhere(ctx, user, "auth.second_proof", map[string]any{"how": how, "ip": clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"device_proved": true})
}

// secondProofPassword is POST /v1/auth/second-proof/password ({password}):
// the person's password, where it is a way (secondProofsOf: set, usable in
// a business of theirs, on a confirmed email where email can be sent),
// proves this device. Limited as signing in is.
func (s *Server) secondProofPassword(w http.ResponseWriter, r *http.Request) {
	p, key, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	if !s.loginLimiter("second-proof-password:"+p.UserID.String()).Allow() || !s.loginLimiter("login:"+limitIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	ways, err := s.secondProofsOf(ctx, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ways.Password {
		s.fail(w, r, fmt.Errorf("%w: your password cannot prove it is you here: use another way", errConflict))
		return
	}
	var hash *string
	if err := db.InPersonTx(ctx, s.Store.Pool, p.UserID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, p.UserID).Scan(&hash)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if hash == nil || req.Password == "" || !checkPassword(*hash, req.Password) {
		_ = s.auditEverywhere(ctx, p.UserID, "auth.second_proof_fail", map[string]any{"how": "password", "ip": clientIP(r)})
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "That password is not right.", "password_wrong": true})
		return
	}
	s.deviceProved(w, r, p.UserID, key, "password")
}

// secondProofPasskeyOptions is POST /v1/auth/second-proof/passkey/options:
// a challenge for one of the signed-in person's own passkeys, as signing
// in with one asks ({publicKey}).
func (s *Server) secondProofPasskeyOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	p, _, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	if !s.limiter("challenge:"+limitIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	ctx := r.Context()
	var ids [][]byte
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_passkey_ids($1)`, p.UserID).Scan(&ids); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(ids) == 0 {
		s.fail(w, r, fmt.Errorf("%w: you have no passkey: use another way", errConflict))
		return
	}
	ch, err := s.issueChallenge(ctx, "step_up", &p.UserID, secondProofScope)
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

// secondProofScope binds a second-proof passkey challenge to its use.
const secondProofScope = "second_proof"

// secondProofPasskey is POST /v1/auth/second-proof/passkey ({credential}):
// an assertion by one of the person's own passkeys, for a challenge from
// the options above, proves this device. The session is not changed.
func (s *Server) secondProofPasskey(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) || !jsonOnly(w, r) {
		return
	}
	p, key, ok := s.proofDevice(w, r)
	if !ok {
		return
	}
	if !s.loginLimiter("login:" + limitIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Credential credentialJSON `json:"credential"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.verifyPasskey(r.Context(), req.Credential, "step_up", &p.UserID, secondProofScope); err != nil {
		s.passkeyFail(w, r, err)
		return
	}
	s.deviceProved(w, r, p.UserID, key, "passkey")
}

// firstWayDevice is the device of this session when it was let in without
// a second proof because the person had no way to give one (a session made
// by a code, proofState.unprovable); nil otherwise. Adding their first way
// (an email, a password, a passkey) proves that device too
// (proveFirstWay): it was in already, and the new way would otherwise shut
// it out of the person's other businesses and settings until proved.
func (s *Server) firstWayDevice(r *http.Request) []byte {
	p := principalFrom(r.Context())
	if p == nil || !codeSession(p) {
		return nil
	}
	st, err := s.proofStateOf(r, p)
	if err != nil || !st.unprovable {
		return nil
	}
	return s.sessionDeviceKey(r, p)
}

// proveFirstWay records the proof of firstWayDevice's device once the way
// is added; nothing for a nil device. Audited as a second proof.
func (s *Server) proveFirstWay(ctx context.Context, r *http.Request, user uuid.UUID, key []byte, how string) {
	if key == nil {
		return
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_device_prove($1, $2, $3)`, user, key, how); err != nil {
		s.Logger.Warn("first way: the device not proved", "err", err)
		return
	}
	_ = s.auditEverywhere(ctx, user, "auth.second_proof", map[string]any{"how": how, "first_way": true, "ip": clientIP(r)})
}
