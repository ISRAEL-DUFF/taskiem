package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// WhatsApp as a client of the platform (spec 11): binding a number to an
// account by a code sent to it (11.2). The conversation itself is in
// whatsapp_chat.go, approvals by button and step-up hand-off in
// whatsapp_approvals.go.

const (
	waOTPTTL         = 10 * time.Minute
	waOTPMaxSends    = 3 // codes per person per hour
	waOTPMaxFailures = 5 // wrong codes before a code is spent
)

var errWhatsAppOff = fmt.Errorf("%w: WhatsApp is not set up on this Taskiem", errConflict)

type waBindingInfo struct {
	Enabled        bool       `json:"enabled"`
	PlatformNumber string     `json:"platform_number,omitempty"`
	Number         *string    `json:"number"`
	VerifiedAt     *time.Time `json:"verified_at"`
	Pending        *struct {
		Number    string    `json:"number"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"pending,omitempty"`
	// Pin is the WhatsApp approval PIN's state (spec 11.2).
	Pin waPinInfo `json:"pin"`
	// Flows: WhatsApp forms are set up (inputs and the PIN).
	Flows bool `json:"flows"`
}

// getWhatsApp is the caller's binding.
func (s *Server) getWhatsApp(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := waBindingInfo{Enabled: s.WhatsApp != nil}
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp number belongs to a person, not an API key")
		return
	}
	if s.WhatsApp != nil {
		// The number this organisation's messages come from.
		wa, err := s.WhatsApp.ForTenant(r.Context(), p.TenantID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.PlatformNumber, out.Flows = wa.Config.DisplayNumber, wa.Config.FlowKey != nil
		if out.Pin, err = s.waPinInfo(r.Context(), p.UserID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var pn *string
	var pe *time.Time
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT number, verified_at, pending_number, pending_expires_at FROM taskiem_wa_binding($1)`, p.UserID).
		Scan(&out.Number, &out.VerifiedAt, &pn, &pe); err != nil {
		s.fail(w, r, err)
		return
	}
	if pn != nil && pe != nil {
		out.Pending = &struct {
			Number    string    `json:"number"`
			ExpiresAt time.Time `json:"expires_at"`
		}{*pn, *pe}
	}
	writeJSON(w, http.StatusOK, out)
}

// startWhatsAppBinding sends a code to a number. Binding lets the number
// act for the person in every tenant they belong to, for as long as it is
// bound, so it takes the same proof as adding a passkey.
func (s *Server) startWhatsAppBinding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp number belongs to a person, not an API key")
		return
	}
	if s.WhatsApp == nil {
		s.fail(w, r, errWhatsAppOff)
		return
	}
	var req struct {
		Number string `json:"number"`
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, err := whatsapp.Normalise(req.Number, s.WhatsApp.Config.DefaultCountry)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	if !s.proveFactor(w, r, req.factorProof) {
		return
	}
	// Codes cost money and land on someone's phone: paced per person and
	// per number, on top of the hourly cap kept in the database.
	if !s.limiter("wa-otp:"+p.UserID.String(), 5*time.Minute, 3).Allow() || !s.limiter("wa-otp-number:"+number, 10*time.Minute, 3).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; wait a few minutes")
		return
	}
	code := otpCode()
	var outcome string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_wa_otp_issue($1, $2, $3, $4::interval, $5)`,
		p.UserID, number, code, waOTPTTL.String(), waOTPMaxSends).Scan(&outcome); err != nil {
		s.fail(w, r, err)
		return
	}
	switch outcome {
	case "number_taken":
		s.fail(w, r, fmt.Errorf("%w: that number is linked to another Taskiem account; unlink it there first", errConflict))
		return
	case "too_many":
		writeErr(w, http.StatusTooManyRequests, "too many codes this hour; try again later")
		return
	}
	// The code is an authentication template: it goes out whatever the
	// conversation window, from the tenant's own number when it has one,
	// and counts against the tenant's template allowance (never held back).
	wa, err := s.WhatsApp.ForTenant(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := wa.SendTemplate(r.Context(), p.TenantID, number, whatsapp.TplOTP, map[string]string{"code": code}, nil); err != nil {
		s.Logger.Warn("whatsapp: sending a code failed", "err", err)
		writeErr(w, http.StatusBadGateway, "the code could not be sent to that number; check it is on WhatsApp and try again")
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "whatsapp.code_sent", p.UserID.String(), map[string]any{"number": whatsapp.MaskNumber(number), "channel": "web"})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"number": number, "expires_at": time.Now().Add(waOTPTTL).UTC()})
}

// otpCode is six random digits.
func otpCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// verifyWhatsAppBinding binds the number when the code is right.
func (s *Server) verifyWhatsAppBinding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp number belongs to a person, not an API key")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, err := s.waVerifyCode(r.Context(), p.UserID, strings.TrimSpace(req.Code), "web", clientIP(r))
	switch {
	case errors.Is(err, errWaCodeWrong):
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
	case errors.Is(err, errWaCodeLocked):
		writeErr(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errWaCodeGone):
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"number": number})
	}
}

var (
	errWaCodeWrong  = errors.New("that code is not right")
	errWaCodeLocked = errors.New("too many wrong codes: ask for a new one")
	errWaCodeGone   = errors.New("no code is waiting, or it expired: ask for a new one")
)

// waVerifyCode checks a code (typed in the web app, or sent back from the
// number) and binds the number; audited in each of the person's tenants.
func (s *Server) waVerifyCode(ctx context.Context, user uuid.UUID, code, channel, ip string) (string, error) {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return "", errWaCodeWrong
	}
	var outcome string
	var number *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT outcome, number FROM taskiem_wa_otp_verify($1, $2, $3)`, user, code, waOTPMaxFailures).Scan(&outcome, &number); err != nil {
		return "", err
	}
	switch outcome {
	case "ok":
	case "wrong":
		return "", errWaCodeWrong
	case "locked":
		return "", errWaCodeLocked
	case "number_taken":
		return "", fmt.Errorf("%w: that number is linked to another Taskiem account", errConflict)
	default:
		return "", errWaCodeGone
	}
	err := s.auditEverywhere(ctx, user, "whatsapp.bind", map[string]any{"number": whatsapp.MaskNumber(*number), "channel": channel, "ip": ip})
	return *number, err
}

// unbindWhatsApp removes the caller's number.
func (s *Server) unbindWhatsApp(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a WhatsApp number belongs to a person, not an API key")
		return
	}
	var number *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_wa_unbind($1)`, p.UserID).Scan(&number); err != nil {
		s.fail(w, r, err)
		return
	}
	if number == nil {
		writeErr(w, http.StatusNotFound, "no number is linked")
		return
	}
	// The PIN goes with the number: a new number starts without one.
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_wa_pin_remove($1)`, p.UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(r.Context(), p.UserID, "whatsapp.unbind", map[string]any{"number": whatsapp.MaskNumber(*number), "channel": "web", "ip": clientIP(r)}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tenantRef is a tenant a person belongs to.
type tenantRef struct {
	ID   uuid.UUID
	Name string
}

// tenantsOf lists a person's active tenants, by name.
func (s *Server) tenantsOf(ctx context.Context, user uuid.UUID) ([]tenantRef, error) {
	var ids []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&ids); err != nil || len(ids) == 0 {
		return nil, err
	}
	var out []tenantRef
	err := db.InTenantTx(ctx, s.Store.Pool, ids, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name FROM tenants WHERE id = ANY ($1) ORDER BY lower(name), id`, ids)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (tenantRef, error) {
			var t tenantRef
			return t, r.Scan(&t.ID, &t.Name)
		})
		return err
	})
	return out, err
}

// auditEverywhere records a person-level change (their number) in each
// tenant they belong to: every tenant's audit shows who can act for whom.
func (s *Server) auditEverywhere(ctx context.Context, user uuid.UUID, action string, detail map[string]any) error {
	var ids []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&ids); err != nil || len(ids) == 0 {
		return err
	}
	raw, _ := json.Marshal(detail)
	return db.InTenantTx(ctx, s.Store.Pool, ids, func(tx pgx.Tx) error {
		for _, t := range ids {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, $3, $2, $4)`, t, user.String(), action, raw); err != nil {
				return err
			}
		}
		return nil
	})
}

// principalOf is a person acting in a tenant through WhatsApp: their roles
// and permissions there, exactly as for a web session.
func (s *Server) principalOf(ctx context.Context, tenant, user uuid.UUID) (*Principal, error) {
	p := &Principal{TenantID: tenant, UserID: user, Permissions: map[string]bool{}, AuthMethod: "whatsapp"}
	if err := s.loadRoles(ctx, p); err != nil {
		return nil, err
	}
	if len(p.Roles) == 0 || !s.userActive(ctx, tenant, user) {
		return nil, errNotMember
	}
	return p, nil
}

// waAudit appends an audit entry for an action taken over WhatsApp.
func waAudit(ctx context.Context, tx pgx.Tx, p *Principal, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["channel"] = "whatsapp"
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, $3, $4, $5)`, p.TenantID, p.UserID.String(), action, target, raw)
	return err
}
