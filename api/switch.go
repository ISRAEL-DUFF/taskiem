package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// A person in several businesses switches between them (the phone app's
// switcher, docs/spec/mobile-app.md §3.1; WhatsApp's `switch`). A session
// belongs to one business, so switching ends this session and makes one in
// the other business that is the same in every other way: the same person,
// sign-in method, phone, and — what step-up reads — the same moment of
// signing in. Switching is never a fresh sign-in. The other business's own
// rules on signing in still apply before the session moves: its single
// sign-on, its passkeys, and a second proof for a session made by a code to
// the person's phone on a new device (personal.go). A session that signed
// in with one business's single sign-on does not switch to another. The
// person's own side (no business) is switched to and from like a business
// (docs/spec/taskiem-for-everyone.md §5.4).

// listBusinesses is GET /v1/me/businesses: the person's businesses by
// name, the one this session is in marked, each with what locks it for
// this session (personal.go): sso_required, passkey_required or
// second_proof_required.
func (s *Server) listBusinesses(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "only a person has businesses")
		return
	}
	proof, err := s.proofStateOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.businessesWithLocks(r, p, proof)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"businesses": out, "personal": p.Personal})
}

// switchBusiness is POST /v1/me/switch ({tenant_id}): this session ends and
// one in that business takes its place, answered as signing in is.
// {tenant_id: null} goes to the person's own side
// (docs/spec/taskiem-for-everyone.md §5.4).
func (s *Server) switchBusiness(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	tok := sessionToken(r)
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.AuthMethod == "" || tok == "" || p.Agent != nil {
		writeErr(w, http.StatusForbidden, "only a signed-in person switches business")
		return
	}
	var req struct {
		TenantID json.RawMessage `json:"tenant_id"`
		// Personal asks for the person's own side, as tenant_id null does,
		// for clients that leave out what is null.
		Personal bool `json:"personal"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var target *uuid.UUID
	raw := strings.TrimSpace(string(req.TenantID))
	if req.Personal && raw == "" {
		raw = "null"
	}
	switch raw {
	case "":
		s.fail(w, r, fmt.Errorf("%w: tenant_id is a business's id, or null for your own side", errBadRequest))
		return
	case "null":
	default:
		var id uuid.UUID
		if err := json.Unmarshal(req.TenantID, &id); err != nil {
			s.fail(w, r, fmt.Errorf("%w: tenant_id is a business's id, or null for your own side", errBadRequest))
			return
		}
		target = &id
	}
	tenants, err := s.userTenants(ctx, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if target == nil {
		s.switchToPersonal(w, r, p, tok, tenants)
		return
	}
	// Single sign-on vouches for a person in one business, through its own
	// identity provider: carried into another, it would stand in for that
	// business's own rules (its passkey requirement skips SSO sessions).
	// A person who signed in that way signs in to the other business.
	if p.AuthMethod == "sso" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "you signed in with your organisation's single sign-on: sign in to that business instead", "sign_in": true})
		return
	}
	if !slices.Contains(tenants, *target) {
		writeErr(w, http.StatusNotFound, "you are not in that business")
		return
	}
	proof, err := s.proofStateOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lock, err := s.businessLock(ctx, p, *target, proof)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch lock {
	case "sso_required":
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": errSSORequired.Error(), "sso_required": true})
		return
	case "passkey_required":
		passkeyRequiredOut(w)
		return
	case "second_proof_required":
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "this is a new device: prove it is you another way first (a passkey, a link to your email, or your chat PIN)",
			"second_proof_required": true, "ways": proof.ways})
		return
	}
	next := newToken()
	var moved bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_session_move($1, $2, $3, $4)`, hashToken(tok), hashToken(next), *target, clientIP(r)).Scan(&moved); err != nil {
		s.fail(w, r, err)
		return
	}
	if !moved {
		writeErr(w, http.StatusUnauthorized, "this session has ended: sign in again")
		return
	}
	from := "personal"
	if !p.Personal {
		from = p.TenantID.String()
	}
	detail := map[string]any{"from": from, "ip": clientIP(r)}
	if proof.unprovable {
		// Signed in by a code to the phone on a new device, with no second
		// proof to give: let in, so no one is locked out of their own
		// business, and recorded where its owners can see it.
		detail["second_proof"] = "none_possible"
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{*target}, func(tx pgx.Tx) error {
		raw, _ := json.Marshal(detail)
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.switch', $2, $3)`, *target, p.UserID.String(), raw)
		return err
	}); err != nil {
		s.Logger.Warn("switch: audit", "err", err)
	}
	out := map[string]any{"tenant_id": *target, "tenants": tenants, "user_id": p.UserID, "personal": false}
	s.deliverSession(w, !p.viaCookie, next, out)
	writeJSON(w, http.StatusOK, out)
}

// switchToPersonal moves a business session to the person's own side.
func (s *Server) switchToPersonal(w http.ResponseWriter, r *http.Request, p *Principal, tok string, tenants []uuid.UUID) {
	ctx := r.Context()
	if p.Personal {
		writeErr(w, http.StatusConflict, "you are on your own side already")
		return
	}
	next := newToken()
	var moved bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_session_move($1, $2, NULL, $3)`, hashToken(tok), hashToken(next), clientIP(r)).Scan(&moved); err != nil {
		s.fail(w, r, err)
		return
	}
	if !moved {
		writeErr(w, http.StatusUnauthorized, "this session has ended: sign in again")
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.switch', $2, jsonb_build_object('to', 'personal', 'ip', $3::text))`,
			p.TenantID, p.UserID.String(), clientIP(r))
		return err
	}); err != nil {
		s.Logger.Warn("switch: audit", "err", err)
	}
	out := map[string]any{"tenant_id": nil, "tenants": nonNil(tenants), "user_id": p.UserID, "personal": true}
	s.deliverSession(w, !p.viaCookie, next, out)
	writeJSON(w, http.StatusOK, out)
}
