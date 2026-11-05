package api

// Who is held to passkeys (docs/spec/optional-passkeys.md). No one is,
// unless:
//   - the deployment's switch is on (TASKIEM_REQUIRE_ADMIN_PASSKEYS, off
//     by default) and they are an administrator in the business;
//   - the business requires passkeys for administrators (Settings >
//     Security) and they are an administrator there;
//   - they chose "Only sign in with my passkey" (Account), in every
//     business.
//
// Someone held without a passkey can only add one (EnrolOnly); someone
// held with one signs in with it, not with a password or their chat.
// Approvals and key changes keep their own step-ups either way.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// errPasskeyRequired refuses a password or chat sign-in of someone held to
// passkeys, who has one, in the business asked for (or in every one).
var errPasskeyRequired = errors.New("sign in with your passkey")

// passkeyHold is whether and why a person is held to passkeys in a business.
type passkeyHold struct {
	held   bool
	has    bool   // they have a passkey
	reason string // personal, deployment or business
}

// passkeyHoldOf works out the rule for a person whose permissions in the
// business are known.
func (s *Server) passkeyHoldOf(ctx context.Context, user, tenant uuid.UUID, perms map[string]bool) (passkeyHold, error) {
	var only, business, has bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT passkey_only, require_admin_passkeys, has_passkey FROM taskiem_auth_passkey_rule($1, $2)`, user, tenant).
		Scan(&only, &business, &has); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return passkeyHold{}, nil
		}
		return passkeyHold{}, err
	}
	h := passkeyHold{has: has}
	admin := isAdmin(perms)
	switch {
	case only:
		h.held, h.reason = true, "personal"
	case s.RequireAdminPasskeys && admin:
		h.held, h.reason = true, "deployment"
	case business && admin:
		h.held, h.reason = true, "business"
	}
	return h, nil
}

// passkeyHoldIn is passkeyHoldOf for a person's roles in the business.
func (s *Server) passkeyHoldIn(ctx context.Context, user, tenant uuid.UUID) (passkeyHold, error) {
	p := Principal{TenantID: tenant, UserID: user, Permissions: map[string]bool{}}
	if err := s.loadRoles(ctx, &p); err != nil {
		return passkeyHold{}, err
	}
	return s.passkeyHoldOf(ctx, user, tenant, p.Permissions)
}

// passwordSigninTenant is the business a password or chat sign-in goes to:
// the one asked for, or else the first the person is not held to their
// passkey in; errPasskeyRequired when they are held there (or everywhere)
// and have one. Someone held without a passkey signs in, to add one.
func (s *Server) passwordSigninTenant(ctx context.Context, user, want uuid.UUID) (uuid.UUID, error) {
	tenants, err := s.userTenants(ctx, user)
	if err != nil || len(tenants) == 0 {
		return want, err // no membership: the session's own errors say so
	}
	if want != uuid.Nil {
		if !slices.Contains(tenants, want) {
			return want, nil
		}
		h, err := s.passkeyHoldIn(ctx, user, want)
		if err != nil {
			return want, err
		}
		if h.held && h.has {
			return want, errPasskeyRequired
		}
		return want, nil
	}
	for _, t := range tenants {
		h, err := s.passkeyHoldIn(ctx, user, t)
		if err != nil {
			return uuid.Nil, err
		}
		if !h.held || !h.has {
			return t, nil
		}
	}
	return uuid.Nil, errPasskeyRequired
}

// passkeyRequiredOut is the refusal of a password or chat sign-in.
func passkeyRequiredOut(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in with your passkey", "passkey_required": true})
}

// --- the personal setting ---

// setPasskeyOnly turns "Only sign in with my passkey" on or off
// (PUT /v1/me/passkey-only, {on, passkey}), confirmed with a passkey either
// way: an authenticator code or the password is not enough to turn it
// off, which is the point of it. Turning it on needs a passkey to sign in
// with.
func (s *Server) setPasskeyOnly(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "this setting belongs to a person, not an API key")
		return
	}
	var req struct {
		On      *bool           `json:"on"`
		Passkey *credentialJSON `json:"passkey"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.On == nil {
		s.fail(w, r, fmt.Errorf("%w: on is true or false", errBadRequest))
		return
	}
	ctx := r.Context()
	var has bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, p.UserID).Scan(&has); err != nil {
		s.fail(w, r, err)
		return
	}
	if !has {
		s.fail(w, r, fmt.Errorf("%w: add a passkey first", errConflict))
		return
	}
	target := "passkey_only.off"
	if *req.On {
		target = "passkey_only.on"
	}
	if req.Passkey == nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm it is you with your passkey", "reauth": []string{"passkey"}})
		return
	}
	if _, err := s.verifyPasskey(ctx, *req.Passkey, "step_up", &p.UserID, stepUpScope("account.reauth", target)); err != nil {
		if passkeyRefused(err) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "that passkey response is not valid; try again", "reauth": []string{"passkey"}})
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET passkey_only = $2 WHERE id = $1`, p.UserID, *req.On); err != nil {
			return err
		}
		return auditTx(r, tx, "auth.passkey_only", p.UserID.String(), map[string]any{"on": *req.On})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	key := "account.passkey_only.off"
	if *req.On {
		key = "account.passkey_only.on"
	}
	user, tenant := p.UserID, p.TenantID
	s.background(ctx, "passkey-only told", func(ctx context.Context) {
		s.tellPerson(ctx, user, tenant, func(l lang.Tag) string { return tr(l, key) }, "Your Taskiem sign-in changed", tr(lang.EN, key))
	})
	writeJSON(w, http.StatusOK, map[string]any{"passkey_only": *req.On})
}

// passkeysGone turns "Only sign in with my passkey" off for a person left
// with no passkey (one removed, or reset), so no one is locked out.
func passkeysGone(ctx context.Context, tx pgx.Tx, user uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE users SET passkey_only = false
		WHERE id = $1 AND passkey_only AND NOT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = $1)`, user)
	return err
}

// --- the business setting ---

// securityOut is Settings > Security: whether the business requires
// passkeys for administrators, how many of its administrators have none,
// and whether the person asking has one.
func (s *Server) getSecurity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var on bool
	var without int
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT require_admin_passkeys FROM tenants WHERE id = $1`, p.TenantID).Scan(&on); err != nil {
			return err
		}
		var err error
		without, err = s.adminsWithoutPasskey(ctx, tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	var mine bool
	if p.UserID != uuid.Nil {
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, p.UserID).Scan(&mine); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"require_admin_passkeys": on, "admins_without_passkey": without,
		"you_have_passkey": mine, "deployment_requires": s.RequireAdminPasskeys, "can_change": isOwner(p)})
}

// adminsWithoutPasskey counts the business's administrators who have no
// passkey.
func (s *Server) adminsWithoutPasskey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx, `SELECT m.user_id, m.role, r.permissions FROM memberships m
		LEFT JOIN roles r ON r.tenant_id = m.tenant_id AND r.name = m.role WHERE m.tenant_id = $1`, tenant)
	if err != nil {
		return 0, err
	}
	perms := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var user uuid.UUID
		var role string
		var custom []string
		if err := rows.Scan(&user, &role, &custom); err != nil {
			rows.Close()
			return 0, err
		}
		ps, builtin := rolePermissions[role]
		if !builtin {
			ps = custom
		}
		if perms[user] == nil {
			perms[user] = map[string]bool{}
		}
		for _, x := range ps {
			perms[user][x] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for user, ps := range perms {
		if !isAdmin(ps) {
			continue
		}
		var has bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, user).Scan(&has); err != nil {
			return 0, err
		}
		if !has {
			n++
		}
	}
	return n, nil
}

// isOwner is whether the person is an owner of the business.
func isOwner(p *Principal) bool { return p.UserID != uuid.Nil && slices.Contains(p.Roles, "owner") }

// putSecurity turns "Require passkeys for administrators" on or off
// (PUT /v1/settings/security, {require_admin_passkeys, passkey|totp|password}):
// owners only, confirmed as a factor change is; turning it on needs a
// passkey of the owner's own, or their next request would be held.
func (s *Server) putSecurity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !isOwner(p) {
		writeErr(w, http.StatusForbidden, "only an owner can change who must sign in with a passkey")
		return
	}
	var req struct {
		Require *bool `json:"require_admin_passkeys"`
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Require == nil {
		s.fail(w, r, fmt.Errorf("%w: require_admin_passkeys is true or false", errBadRequest))
		return
	}
	ctx := r.Context()
	if *req.Require {
		var has bool
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, p.UserID).Scan(&has); err != nil {
			s.fail(w, r, err)
			return
		}
		if !has {
			s.fail(w, r, fmt.Errorf("%w: add a passkey of your own first (Account), or you could not carry on", errConflict))
			return
		}
	}
	if !s.proveFactor(w, r, req.factorProof, "business.require_passkeys") {
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE tenants SET require_admin_passkeys = $2 WHERE id = $1`, p.TenantID, *req.Require); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.require_passkeys", p.TenantID.String(), map[string]any{"on": *req.Require})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"require_admin_passkeys": *req.Require})
}

// --- telling a person ---

// tellPerson tells a person something about their account: on their chat
// apps (outside WhatsApp's window, as the alert template) and, with a
// confirmed email, by email.
func (s *Server) tellPerson(ctx context.Context, user, tenant uuid.UUID, words func(lang.Tag) string, subject, emailBody string) {
	var email string
	var verified bool
	type to struct{ channel, addr string }
	var sends []to
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, user).Scan(&email, &verified); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, []uuid.UUID{user})
			if err != nil {
				return err
			}
			if a, ok := addrs[user]; ok {
				sends = append(sends, to{channel, a})
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("telling a person", "err", err)
		return
	}
	account := strings.TrimRight(s.PublicURL, "/") + "/account"
	for _, t := range sends {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		text := words(s.chooseLang(ctx, tenant, t.addr, "").Tag)
		tpl := whatsapp.TplAlert
		msg := whatsapp.Message{Tenant: tenant, Text: text, Template: &tpl, Vars: map[string]string{"tenant": "Taskiem", "title": text, "link": account}}
		if _, err := ch.Send(ctx, t.addr, msg); err != nil {
			s.Logger.Warn("telling a person: the chat", "channel", t.channel, "err", err)
		}
	}
	if verified && !emailPlaceholder(email) && s.mailOn() && emailBody != "" {
		body := emailBody + "\n\nIf this was not you, send \"sign out everywhere\" to Taskiem in your chat, or open your Account page:\n\n" + account + "\n"
		if err := s.sendMail(ctx, email, subject, body); err != nil {
			s.Logger.Warn("telling a person: the email", "err", err)
		}
	}
}
