package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The WhatsApp approval PIN (spec 11.2). A person with a bound number and
// a passkey or authenticator may set a six-digit PIN in the web app. In
// WhatsApp, a decision under a policy whose step-up is "whatsapp_pin" then
// asks for it in a WhatsApp Flow bound to that decision
// (whatsapp_flows.go). It is hashed with Argon2id like passwords, and five
// wrong PINs in 15 minutes lock it for 15 minutes, like authenticator
// codes. Policies asking for "totp" or "passkey" never take it.

type waPinInfo struct {
	Set         bool       `json:"set"`
	SetAt       *time.Time `json:"set_at,omitempty"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
}

func (s *Server) waPinInfo(ctx context.Context, user uuid.UUID) (waPinInfo, error) {
	var out waPinInfo
	var hash string
	err := s.Store.Pool.QueryRow(ctx, `SELECT pin_hash, set_at, locked_until FROM taskiem_wa_pin_state($1)`, user).Scan(&hash, &out.SetAt, &out.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Set = err == nil
	return out, err
}

// validPin: six digits.
func validPin(pin string) bool {
	return len(pin) == whatsapp.PinLength && strings.Trim(pin, "0123456789") == ""
}

// weakPin: one digit repeated, or a run up or down.
func weakPin(pin string) bool {
	same, up, down := true, true, true
	for i := 1; i < len(pin); i++ {
		d := int(pin[i]) - int(pin[i-1])
		same = same && d == 0
		up = up && (d == 1 || d == -9)
		down = down && (d == -1 || d == 9)
	}
	return same || up || down
}

// setWhatsAppPin sets or replaces the caller's PIN. It needs a bound
// number and a fresh passkey assertion or authenticator code: the PIN
// stands in for those factors in WhatsApp, so it is never easier to get
// than they are.
func (s *Server) setWhatsAppPin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp PIN belongs to a person, not an API key")
		return
	}
	if s.WhatsApp == nil {
		s.fail(w, r, errWhatsAppOff)
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
	var number *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT number FROM taskiem_wa_binding($1)`, p.UserID).Scan(&number); err != nil {
		s.fail(w, r, err)
		return
	}
	if number == nil {
		s.fail(w, r, fmt.Errorf("%w: link your WhatsApp number first", errConflict))
		return
	}
	f, _, err := s.factorsOf(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !f.Passkey && !f.TOTP {
		s.fail(w, r, fmt.Errorf("%w: add a passkey or an authenticator first; the PIN stands in for them on WhatsApp", errConflict))
		return
	}
	if !s.proveFactor(w, r, req.factorProof) {
		return
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_wa_pin_set($1, $2)`, p.UserID, hashPassword(req.Pin)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "whatsapp.pin.set", map[string]any{"channel": "web", "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	info, err := s.waPinInfo(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// removeWhatsAppPin removes the caller's PIN (no proof: it only takes a
// way to approve away).
func (s *Server) removeWhatsAppPin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.EndUser != nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp PIN belongs to a person, not an API key")
		return
	}
	var had bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_wa_pin_remove($1)`, p.UserID).Scan(&had); err != nil {
		s.fail(w, r, err)
		return
	}
	if !had {
		writeErr(w, http.StatusNotFound, "no PIN is set")
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "whatsapp.pin.remove", map[string]any{"channel": "web", "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
