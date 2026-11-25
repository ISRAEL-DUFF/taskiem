package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The person's PIN, set and changed in the app and on the web
// (docs/spec/transaction-pin.md). It is the one four-digit PIN that
// approves on WhatsApp and pays within its limits (payments_pin.go). Never
// typed in a message, and never reset by text. Setting or changing it is
// confirmed by one of:
//
//   - a one-time code sent to the person's verified phone
//     (POST /v1/me/pin/code, purpose "pin": good for the PIN only);
//   - a fresh passkey assertion (account.reauth / pin.set);
//   - an authenticator code, in a business's session.
//
// Whichever it is, the PIN pays at once: there is no 24-hour hold.
// Removing it takes no proof. A session made by a code on a new device,
// of someone who has a passkey or an authenticator, changes nothing until
// its second proof (refuseUnproved), as for every other factor.

// mePin is the person's PIN as their settings show it.
type mePin struct {
	Set         bool       `json:"set"`
	SetAt       *time.Time `json:"set_at,omitempty"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
	// Digits: how long a PIN is.
	Digits int `json:"digits"`
	// Ways: what may confirm setting or changing it now: "code" (to
	// CodeTo), "passkey", "totp".
	Ways []string `json:"ways"`
	// CodeTo: the phone a code goes to, masked.
	CodeTo string `json:"code_to,omitempty"`
	// Renew: the last PIN went when PINs became four digits; none since.
	Renew bool `json:"renew,omitempty"`
}

// pinPhone is the verified phone a PIN code goes to: the account's own
// number, "" when it has none.
func (s *Server) pinPhone(ctx context.Context, user uuid.UUID) string {
	n, _ := s.phoneOf(ctx, user).(string)
	return n
}

// pinFactors is whether p can prove themselves with a passkey or an
// authenticator. A personal session has no business, so no authenticator
// (they are per business): the passkey only.
func (s *Server) pinFactors(ctx context.Context, p *Principal) (passkey, totp bool, err error) {
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, p.UserID).Scan(&passkey); err != nil {
		return false, false, err
	}
	if p.Personal || p.TenantID == uuid.Nil {
		return passkey, false, nil
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		totp = s.totpEnrolled(ctx, tx, p.UserID)
		return nil
	})
	return passkey, totp, err
}

// pinWays is what may confirm a change to p's PIN now, and the phone a
// code goes to.
func (s *Server) pinWays(ctx context.Context, p *Principal) ([]string, string, error) {
	hasPasskey, hasTOTP, err := s.pinFactors(ctx, p)
	if err != nil {
		return nil, "", err
	}
	ways := []string{}
	number := s.pinPhone(ctx, p.UserID)
	if number != "" && (s.WhatsApp != nil || s.CodeSMS != nil) {
		ways = append(ways, "code")
	}
	if hasPasskey {
		ways = append(ways, "passkey")
	}
	if hasTOTP {
		ways = append(ways, "totp")
	}
	return ways, number, nil
}

// pinProof checks what proves setting or changing p's PIN: a code to
// their phone, a fresh passkey or an authenticator code they really have.
// A password, a junk code or a sign-in alone proves nothing. It answers
// how it was proved; false when it answered the request (refused).
func (s *Server) pinProof(w http.ResponseWriter, r *http.Request, proof factorProof, code, action string) (string, bool) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if s.refuseUnproved(w, r) {
		return "", false
	}
	ways, number, err := s.pinWays(ctx, p)
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	switch {
	case code != "" && number != "":
		if !s.checkPhoneCode(w, r, number, "pin", &p.UserID, code, nil) {
			return "", false
		}
		return "code", true
	case proof.Passkey != nil && slices.Contains(ways, "passkey"):
		if _, err := s.verifyPasskey(ctx, *proof.Passkey, "step_up", &p.UserID, stepUpScope("account.reauth", action)); err != nil {
			if passkeyRefused(err) {
				writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": ways, "error": "that passkey response is not valid; try again"})
				return "", false
			}
			s.fail(w, r, err)
			return "", false
		}
		return "passkey", true
	case proof.TOTP != "" && slices.Contains(ways, "totp"):
		ok, err := s.verifyTOTP(ctx, p.TenantID, p.UserID, proof.TOTP)
		if err != nil || !ok {
			s.totpRefused(w, r, err, map[string]any{"code": "proof_needed", "error": "that authenticator code is not right, or was already used", "reauth": ways})
			return "", false
		}
		return "totp", true
	}
	msg := "To set your PIN, type the code we send to your phone."
	if len(ways) == 0 {
		msg = "To set your PIN, add your phone number first: we send a code to it."
	} else if !slices.Contains(ways, "code") {
		msg = "To set your PIN, confirm with your passkey or your authenticator."
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": ways, "error": msg})
	return "", false
}

func (s *Server) mePinOf(r *http.Request, p *Principal) (mePin, error) {
	ctx := r.Context()
	info, err := s.waPinInfo(ctx, p.UserID)
	if err != nil {
		return mePin{}, err
	}
	out := mePin{Set: info.Set, SetAt: info.SetAt, LockedUntil: info.LockedUntil, Digits: whatsapp.PinLength}
	ways, number, err := s.pinWays(ctx, p)
	if err != nil {
		return mePin{}, err
	}
	out.Ways = ways
	if slices.Contains(ways, "code") {
		out.CodeTo = whatsapp.MaskNumber(number)
	}
	if !info.Set {
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_pin_renew($1)`, p.UserID).Scan(&out.Renew); err != nil {
			return mePin{}, err
		}
	}
	return out, nil
}

// mePinCode is POST /v1/me/pin/code ({via?}): a one-time code to the
// person's verified phone, good only for setting or changing their PIN
// (PUT /v1/me/pin {code}). 409 {no_phone} for an account with no phone;
// 429 as other codes are limited.
func (s *Server) mePinCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "a PIN belongs to a person, not an API key")
		return
	}
	var req struct {
		Via string `json:"via"`
	}
	if err := decodeOptional(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Via != "" && req.Via != "whatsapp" && req.Via != "sms" {
		writeErr(w, http.StatusBadRequest, "via is whatsapp or sms")
		return
	}
	if s.refuseUnproved(w, r) {
		return
	}
	number := s.pinPhone(r.Context(), p.UserID)
	if number == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"no_phone": true,
			"error": "Your account has no phone to send a code to. Add your phone number, or confirm with your passkey."})
		return
	}
	sent := s.issuePhoneCode(w, r, number, "pin", &p.UserID, req.Via)
	if sent == "" {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_via": sent, "expires_in": int(phoneCodeTTL.Seconds()), "code_to": whatsapp.MaskNumber(number)})
}

// getMePin is GET /v1/me/pin.
func (s *Server) getMePin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil {
		writeErr(w, http.StatusForbidden, "a PIN belongs to a person, not an API key")
		return
	}
	out, err := s.mePinOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putMePin is PUT /v1/me/pin {pin, code? | passkey? | totp?}: the
// person's PIN set or changed, confirmed by a code from POST
// /v1/me/pin/code, a passkey or an authenticator code; it pays at once.
// Otherwise 403 proof_needed with the ways to prove (reauth).
func (s *Server) putMePin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "a PIN belongs to a person, not an API key")
		return
	}
	var req struct {
		Pin  string `json:"pin"`
		Code string `json:"code"`
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !validPin(req.Pin) {
		s.fail(w, r, fmt.Errorf("%w: the PIN is %d digits", errBadRequest, whatsapp.PinLength))
		return
	}
	if weakPin(req.Pin) {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, pinWeakWords))
		return
	}
	via, ok := s.pinProof(w, r, req.factorProof, req.Code, "pin.set")
	if !ok {
		return
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_pin_set_hold($1, $2, '0'::interval)`, p.UserID, hashPassword(req.Pin)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "pin.set", map[string]any{"via": via, "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.mePinOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
