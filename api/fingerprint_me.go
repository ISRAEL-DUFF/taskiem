package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The fingerprint as its own way to confirm a payment (docs/spec/
// fingerprint-key.md, migration 00740): a key pair made in the phone's
// secure hardware (Android Keystore, the iPhone's Secure Enclave), usable
// only after the phone's fingerprint or face check. The phone keeps the
// private key; its public key is kept here, for that person and that phone
// (one key a phone; turning it on again replaces it). Turning it on takes
// what setting the PIN takes: a passkey or an authenticator, the PIN, or a
// code to the person's phone. Paying with it is payments_fingerprint.go.
// The web has no fingerprint: it lists and removes the phones only.

// payKeyCodePurpose is the phone code's purpose that proves turning it on.
const payKeyCodePurpose = "pay_key"

// fingerprintDevice is one phone a person pays with by fingerprint.
type fingerprintDevice struct {
	ID         uuid.UUID  `json:"id"`
	DeviceID   uuid.UUID  `json:"device_id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// fingerprintPerson refuses what is not a person: a key, an end user, an
// agent, a run.
func fingerprintPerson(w http.ResponseWriter, p *Principal) bool {
	if p == nil || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil || p.Run != uuid.Nil {
		writeErr(w, http.StatusForbidden, "a fingerprint belongs to a person, not an API key")
		return false
	}
	return true
}

func (s *Server) fingerprintDevices(r *http.Request, user uuid.UUID) ([]fingerprintDevice, error) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, device_id, device_name, platform, created_at, last_used_at FROM taskiem_pay_keys($1)`, user)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (fingerprintDevice, error) {
		var d fingerprintDevice
		err := row.Scan(&d.ID, &d.DeviceID, &d.Name, &d.Platform, &d.CreatedAt, &d.LastUsedAt)
		return d, err
	})
	if out == nil {
		out = []fingerprintDevice{}
	}
	return out, err
}

// fingerprintProofs are the ways p may prove turning it on: passkey,
// totp, pin and code (a code to their phone), those they have.
func (s *Server) fingerprintProofs(r *http.Request, p *Principal) ([]string, string, error) {
	passkey, totp, err := s.pinFactors(r.Context(), p)
	if err != nil {
		return nil, "", err
	}
	info, err := s.waPinInfo(r.Context(), p.UserID)
	if err != nil {
		return nil, "", err
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_phone_of($1)`, p.UserID).Scan(&phone); err != nil {
		return nil, "", err
	}
	ways := []string{}
	if passkey {
		ways = append(ways, "passkey")
	}
	if totp {
		ways = append(ways, "totp")
	}
	if info.Set {
		ways = append(ways, "pin")
	}
	number := ""
	if phone != nil && (s.WhatsApp != nil || s.CodeSMS != nil) {
		ways, number = append(ways, "code"), *phone
	}
	return ways, number, nil
}

// getMeFingerprint is GET /v1/me/fingerprint: the person's phones that pay
// by fingerprint, its limits and what is left today, and the ways to prove
// turning it on.
func (s *Server) getMeFingerprint(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !fingerprintPerson(w, p) {
		return
	}
	devices, err := s.fingerprintDevices(r, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ways, _, err := s.fingerprintProofs(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var today int64
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_fingerprint_paid_today($1)`, p.UserID).Scan(&today); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "one_kobo": int64(fpPayOneKobo), "day_kobo": int64(fpPayDayKobo),
		"left_today_kobo": max(int64(fpPayDayKobo)-today, 0), "proofs": ways})
}

// fingerprintCode is POST /v1/me/fingerprint/code {via?}: a code to the
// person's phone, to prove turning the fingerprint on.
func (s *Server) fingerprintCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !fingerprintPerson(w, p) {
		return
	}
	var req struct {
		Via string `json:"via"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Via != "" && req.Via != "whatsapp" && req.Via != "sms" {
		s.fail(w, r, fmt.Errorf("%w: via is whatsapp or sms", errBadRequest))
		return
	}
	if !s.limiter("fingerprint-code:"+p.UserID.String(), time.Hour, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_phone_of($1)`, p.UserID).Scan(&phone); err != nil {
		s.fail(w, r, err)
		return
	}
	if phone == nil {
		s.fail(w, r, fmt.Errorf("%w: add your phone number first, or confirm with your passkey or PIN", errConflict))
		return
	}
	user := p.UserID
	sent := s.issuePhoneCode(w, r, *phone, payKeyCodePurpose, &user, req.Via)
	if sent == "" {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_via": sent, "expires_in": int(phoneCodeTTL.Seconds())})
}

// fingerprintProof checks what proves turning the fingerprint on: a fresh
// passkey or authenticator code, the PIN (its wrong tries counted with
// every other use of it), or the code sent to the person's phone. It
// answers how, or "" when it answered the request.
func (s *Server) fingerprintProof(w http.ResponseWriter, r *http.Request, proof factorProof, pin, code string) string {
	ctx := r.Context()
	p := principalFrom(ctx)
	if s.refuseUnproved(w, r) {
		return ""
	}
	ways, number, err := s.fingerprintProofs(r, p)
	if err != nil {
		s.fail(w, r, err)
		return ""
	}
	has := func(m string) bool {
		for _, w := range ways {
			if w == m {
				return true
			}
		}
		return false
	}
	refused := func(msg string) string {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": ways, "error": msg})
		return ""
	}
	switch {
	case proof.Passkey != nil && has("passkey"):
		if _, err := s.verifyPasskey(ctx, *proof.Passkey, "step_up", &p.UserID, stepUpScope("account.reauth", "fingerprint.add")); err != nil {
			if passkeyRefused(err) {
				return refused("That passkey response is not valid; try again.")
			}
			s.fail(w, r, err)
			return ""
		}
		return "passkey"
	case proof.TOTP != "" && has("totp"):
		ok, err := s.verifyTOTP(ctx, p.TenantID, p.UserID, proof.TOTP)
		if err != nil || !ok {
			s.totpRefused(w, r, err, map[string]any{"code": "proof_needed", "error": "that authenticator code is not right, or was already used", "reauth": ways})
			return ""
		}
		return "totp"
	case pin != "" && has("pin"):
		t, err := s.tryPin(ctx, p.UserID, pin)
		switch {
		case err != nil:
			s.fail(w, r, err)
			return ""
		case t.LockedUntil != nil:
			return refused("Your PIN is locked after too many wrong tries. Confirm another way.")
		case !t.Right:
			if err := s.auditEverywhere(ctx, p.UserID, "fingerprint.pin.fail", map[string]any{"failures": t.Failures, "ip": clientIP(r)}); err != nil {
				s.fail(w, r, err)
				return ""
			}
			if t.NowLocked {
				return refused("Too many wrong PINs: your PIN is locked for 15 minutes. Confirm another way.")
			}
			return refused(fmt.Sprintf("That PIN is not right. %d tries left.", waPinMaxFailures-t.Failures))
		}
		return "pin"
	case code != "" && has("code"):
		user := p.UserID
		if !s.checkPhoneCode(w, r, number, payKeyCodePurpose, &user, code, nil) {
			return ""
		}
		return "code"
	}
	return refused("To turn on paying by fingerprint, confirm it is you: your passkey, your PIN or a code to your phone.")
}

// parsePayKey reads a phone's public key: base64 DER SubjectPublicKeyInfo
// of an ECDSA P-256 key, as the phone's secure hardware makes it.
func parsePayKey(s string) ([]byte, *ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(der) < 64 || len(der) > 200 {
		return nil, nil, errors.New("public_key is a base64 P-256 public key")
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, nil, errors.New("public_key is a base64 P-256 public key")
	}
	ec, ok := k.(*ecdsa.PublicKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, nil, errors.New("public_key is an ECDSA P-256 key")
	}
	return der, ec, nil
}

// putMeFingerprint is POST /v1/me/fingerprint {public_key, device: {id,
// name, platform}, passkey? | totp? | pin? | code?}: this phone pays by
// fingerprint, replacing its earlier key. 403 proof_needed with the ways
// to prove when none is given.
func (s *Server) putMeFingerprint(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !fingerprintPerson(w, p) {
		return
	}
	var req struct {
		PublicKey string     `json:"public_key"`
		Device    *deviceReq `json:"device"`
		Pin       string     `json:"pin"`
		Code      string     `json:"code"`
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	der, _, err := parsePayKey(req.PublicKey)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %v", errBadRequest, err))
		return
	}
	d := req.Device
	if d == nil {
		s.fail(w, r, fmt.Errorf("%w: device is required", errBadRequest))
		return
	}
	d.Name = strings.TrimSpace(d.Name)
	switch {
	case d.ID == uuid.Nil:
		s.fail(w, r, fmt.Errorf("%w: device.id is required", errBadRequest))
		return
	case d.Platform != "android" && d.Platform != "ios":
		s.fail(w, r, fmt.Errorf("%w: device.platform is android or ios", errBadRequest))
		return
	case d.Name == "" || utf8.RuneCountInString(d.Name) > 80:
		s.fail(w, r, fmt.Errorf("%w: device.name is 1 to 80 characters", errBadRequest))
		return
	}
	via := s.fingerprintProof(w, r, req.factorProof, req.Pin, req.Code)
	if via == "" {
		return
	}
	var id uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_pay_key_put($1, $2, $3, $4, $5)`, p.UserID, d.ID, d.Name, d.Platform, der).Scan(&id); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "fingerprint.add", map[string]any{"key": id, "device": d.Name, "platform": d.Platform, "via": via, "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	devices, err := s.fingerprintDevices(r, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, x := range devices {
		if x.ID == id {
			writeJSON(w, http.StatusCreated, map[string]any{"device": x})
			return
		}
	}
	s.fail(w, r, pgx.ErrNoRows)
}

// removeMeFingerprint is DELETE /v1/me/fingerprint/{id}: that phone no
// longer pays by fingerprint. No proof: removing only takes a way away.
func (s *Server) removeMeFingerprint(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !fingerprintPerson(w, p) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var name *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_pay_key_remove($1, $2)`, p.UserID, id).Scan(&name); err != nil {
		s.fail(w, r, err)
		return
	}
	if name == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "fingerprint.remove", map[string]any{"key": id, "device": *name, "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
