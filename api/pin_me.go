package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The person's PIN, set and reset in the app and on the web (chat-pay-
// people, Settled row 3). It is the one PIN that approves on WhatsApp and
// pays within its limit (payments_pin.go). Never typed in a message, and
// never reset by text:
//
//   - Behind a passkey or an authenticator (a fresh proof), it is set or
//     changed and pays at once.
//   - Without them: a first PIN (at sign-up, once the phone is verified by
//     the code that signed in) or a forgotten one changed within minutes
//     of signing in again; either pays only 24 hours on, so a session
//     someone took cannot set a PIN and pay with it at once.
//
// A session made by a code on a new device, of someone who has a passkey
// or an authenticator, changes nothing until its second proof
// (refuseUnproved), as for every other factor.

// pinHoldWithout is how long a PIN set without a passkey or an
// authenticator waits before it pays.
const pinHoldWithout = 24 * time.Hour

// mePin is the person's PIN as their settings show it.
type mePin struct {
	Set         bool       `json:"set"`
	SetAt       *time.Time `json:"set_at,omitempty"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
	// PaysFrom: when it may pay (later than now for a new one set without a
	// passkey).
	PaysFrom *time.Time `json:"pays_from,omitempty"`
	// ChangeWithout: it may be set now without a passkey or an
	// authenticator (none set yet, or signed in moments ago); it then pays
	// 24 hours on.
	ChangeWithout bool `json:"change_without"`
	// ChangeWithoutUntil: when that ends, after a sign-in moments ago.
	ChangeWithoutUntil *time.Time `json:"change_without_until,omitempty"`
	// first: none set and none removed before.
	first bool
}

// pinWasRemoved: the person removed a PIN before, so the next one is not
// a first PIN.
func (s *Server) pinWasRemoved(ctx context.Context, user uuid.UUID) (bool, error) {
	var removed bool
	err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_pin_was_removed($1)`, user).Scan(&removed)
	return removed, err
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

// pinProof checks what proves a change to p's PIN (set, change, remove):
// a fresh passkey or authenticator code the person really has (hold 0,
// via "passkey" or "totp"); else, with neither, a first PIN or a change
// moments after signing in again (a 24-hour hold; via "first" or
// "signed_in_again"). A password, a junk code or a sign-in alone never
// lifts the hold. False when it answered the request (refused).
func (s *Server) pinProof(w http.ResponseWriter, r *http.Request, proof factorProof, action string) (time.Duration, string, bool) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if s.refuseUnproved(w, r) {
		return 0, "", false
	}
	hasPasskey, hasTOTP, err := s.pinFactors(ctx, p)
	if err != nil {
		s.fail(w, r, err)
		return 0, "", false
	}
	methods := []string{}
	if hasPasskey {
		methods = append(methods, "passkey")
	}
	if hasTOTP {
		methods = append(methods, "totp")
	}
	switch {
	case proof.Passkey != nil && hasPasskey:
		if _, err := s.verifyPasskey(ctx, *proof.Passkey, "step_up", &p.UserID, stepUpScope("account.reauth", action)); err != nil {
			if passkeyRefused(err) {
				writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": methods, "error": "that passkey response is not valid; try again"})
				return 0, "", false
			}
			s.fail(w, r, err)
			return 0, "", false
		}
		return 0, "passkey", true
	case proof.TOTP != "" && hasTOTP:
		ok, err := s.verifyTOTP(ctx, p.TenantID, p.UserID, proof.TOTP)
		if err != nil || !ok {
			s.totpRefused(w, r, err, map[string]any{"code": "proof_needed", "error": "that authenticator code is not right, or was already used", "reauth": methods})
			return 0, "", false
		}
		return 0, "totp", true
	}
	st, err := s.mePinOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return 0, "", false
	}
	if !st.ChangeWithout {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": methods,
			"error": "To change your PIN, confirm with your passkey; or sign out, sign in again and change it within 10 minutes (it can then pay from 24 hours on)."})
		return 0, "", false
	}
	if !st.first {
		return pinHoldWithout, "signed_in_again", true
	}
	return pinHoldWithout, "first", true
}

func (s *Server) mePinOf(r *http.Request, p *Principal) (mePin, error) {
	info, err := s.waPinInfo(r.Context(), p.UserID)
	if err != nil {
		return mePin{}, err
	}
	out := mePin{Set: info.Set, SetAt: info.SetAt, LockedUntil: info.LockedUntil}
	if info.Set {
		var from time.Time
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_pin_pays_from($1)`, p.UserID).Scan(&from); err == nil {
			out.PaysFrom = &from
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return mePin{}, err
		}
	}
	// A PIN removed before is not a first PIN when set again.
	first := !info.Set
	if first {
		removed, err := s.pinWasRemoved(r.Context(), p.UserID)
		if err != nil {
			return mePin{}, err
		}
		first = !removed
	}
	fresh := !p.SignedInAt.IsZero() && time.Since(p.SignedInAt) <= recentSignIn
	out.first = first
	out.ChangeWithout = first || fresh
	if !first && fresh {
		until := p.SignedInAt.Add(recentSignIn)
		out.ChangeWithoutUntil = &until
	}
	return out, nil
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

// putMePin is PUT /v1/me/pin {pin, passkey? | totp?}: the person's PIN set
// or changed. With a passkey or an authenticator code it pays at once;
// without (a first PIN, or one changed moments after signing in again) it
// pays 24 hours on. Otherwise 403 proof_needed with the ways to prove.
func (s *Server) putMePin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil {
		writeErr(w, http.StatusForbidden, "a PIN belongs to a person, not an API key")
		return
	}
	var req struct {
		Pin string `json:"pin"`
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
		s.fail(w, r, fmt.Errorf("%w: choose a PIN that is not one digit repeated or a run like 123456", errBadRequest))
		return
	}
	hold, via, ok := s.pinProof(w, r, req.factorProof, "pin.set")
	if !ok {
		return
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_pin_set_hold($1, $2, $3::interval)`, p.UserID, hashPassword(req.Pin), hold.String()); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "pin.set", map[string]any{"via": via, "hold_hours": int(hold.Hours()), "ip": clientIP(r)}); err != nil {
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
