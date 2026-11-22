package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	fresh := !p.SignedInAt.IsZero() && time.Since(p.SignedInAt) <= recentSignIn
	out.ChangeWithout = !info.Set || fresh
	if info.Set && fresh {
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
	hold, via := pinHoldWithout, ""
	if req.Passkey != nil || req.TOTP != "" {
		if !s.proveFactor(w, r, req.factorProof, "pin.set") {
			return
		}
		hold, via = 0, "passkey"
		if req.Passkey == nil {
			via = "totp"
		}
	} else {
		if s.refuseUnproved(w, r) {
			return
		}
		st, err := s.mePinOf(r, p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !st.ChangeWithout {
			f, _, err := s.factorsOf(r.Context(), p.TenantID, p.UserID)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			methods := []string{}
			if f.Passkey {
				methods = append(methods, "passkey")
			}
			if f.TOTP {
				methods = append(methods, "totp")
			}
			writeJSON(w, http.StatusForbidden, map[string]any{"code": "proof_needed", "reauth": methods,
				"error": "To change your PIN, confirm with your passkey; or sign out, sign in again and change it within 10 minutes (it can then pay from 24 hours on)."})
			return
		}
		via = "first"
		if st.Set {
			via = "signed_in_again"
		}
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
