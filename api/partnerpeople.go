package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
)

// People in sub-tenants (decision 0030; docs/embedding.md#13-people-in-a-
// sub-tenant). A partner invites a person into one of its sub-tenants with
// roles there, and creates the custom roles its workflows need. The
// invitation is an ordinary one (member_invitations, source "partner"):
// the person accepts it with their own credentials and becomes a member of
// that sub-tenant only. Someone without an account gets a single-use link
// by email to create a passkey and accept in one step; the link goes only
// to the address, never to the partner, which gets neither a credential
// nor a session. Every call enters the sub-tenant through
// taskiem_partner_enter, so it is audited in both chains.

// invitationLinkTTL is how long an emailed invitation link works.
const invitationLinkTTL = 7 * 24 * time.Hour

type partnerInviteReq struct {
	Email string   `json:"email"`
	Name  string   `json:"name,omitempty"`
	Roles []string `json:"roles"`
}

type partnerInvitation struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	InvitedBy string    `json:"invited_by"`
	InvitedAt time.Time `json:"invited_at"`
}

// partnerRolesValid checks the roles a partner gives in a sub-tenant: the
// built-in ones and the sub-tenant's custom roles. A role no one defined
// (a business role, which decides who approves) must be created first, as
// a custom role, so what it allows is written down. Owner is given only to
// a sub-tenant's first owner (decision 0030).
func partnerRolesValid(ctx context.Context, tx pgx.Tx, roles []string) error {
	for _, role := range roles {
		if _, builtin := rolePermissions[role]; builtin {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: %q is neither a built-in role nor one of the sub-tenant's roles (create it first: PUT /v1/partner/sub-tenants/{sub}/roles/{name})", errBadRequest, role)
		}
	}
	if !slices.Contains(roles, "owner") {
		return nil
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE role = 'owner')
		OR EXISTS (SELECT 1 FROM member_invitations WHERE 'owner' = ANY (roles))`).Scan(&owned); err != nil {
		return err
	}
	if owned {
		return fmt.Errorf("%w: the sub-tenant has an owner (or one is invited): only its owners make more", errConflict)
	}
	return nil
}

// inviteToSubTenant invites a person into a sub-tenant. The answer is the
// same whether or not the email has an account.
func (s *Server) inviteToSubTenant(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req partnerInviteReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	roles := slices.Compact(slices.Sorted(slices.Values(req.Roles)))
	if !strings.Contains(req.Email, "@") || len(req.Email) > 320 || len(req.Name) > 200 || len(roles) == 0 || len(roles) > 20 || slices.Contains(roles, "") {
		s.fail(w, r, fmt.Errorf("%w: an email and one to 20 roles are required", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	// The person, if they have an account; a new one is created (with no
	// way to sign in) in the same transaction as the invitation.
	user, existing := uuid.Must(uuid.NewV7()), true
	if err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, req.Email).Scan(&user); errors.Is(err, pgx.ErrNoRows) {
		existing = false
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	token, selector, secretHash := newResetToken()
	var linked bool
	var subName string
	err = s.partnerTx(r, sub, "partner.subtenant.invitation.create", map[string]any{"user": user, "roles": roles}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, sub).Scan(&subName); err != nil {
			return err
		}
		if err := partnerRolesValid(ctx, tx, roles); err != nil {
			return err
		}
		if !existing {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, user, req.Email, req.Name); err != nil {
				if isUnique(err) {
					return fmt.Errorf("%w: the same email was invited at the same moment; try again", errConflict)
				}
				return err
			}
		}
		var source string
		err := tx.QueryRow(ctx, `SELECT source FROM member_invitations WHERE user_id = $1 FOR UPDATE`, user).Scan(&source)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case source == "scim":
			return fmt.Errorf("%w: the sub-tenant's identity provider invited this person already", errConflict)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO member_invitations (tenant_id, user_id, roles, source, invited_by) VALUES ($1, $2, $3, 'partner', $4)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET roles = ARRAY(SELECT DISTINCT unnest(member_invitations.roles || EXCLUDED.roles) ORDER BY 1),
			source = 'partner', invited_by = EXCLUDED.invited_by`, sub, user, roles, partnerActor(p)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT taskiem_invitation_link_issue($1, $2, $3, $4::interval)`, user, selector, secretHash, invitationLinkTTL.String()).Scan(&linked); err != nil {
			return err
		}
		return subAudit(r, tx, sub, "member.invite", user.String(), map[string]any{"roles": roles, "via": "partner"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	delivery := "none"
	if s.mailOn() {
		delivery = "email"
		partnerName := s.partnerName(r)
		email := req.Email
		s.background(ctx, "invitation email", func(ctx context.Context) {
			s.sendInvitation(ctx, email, subName, partnerName, roles, linked, token)
		})
	} else {
		s.Logger.Warn("a partner invited someone, but this deployment cannot send email (TASKIEM_SMTP_URL, TASKIEM_ALERT_FROM, TASKIEM_PUBLIC_URL): nothing sent")
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user_id": user, "email": req.Email, "roles": roles, "status": "pending", "delivery": delivery})
}

// partnerName is the partner's own name, for the invitation email.
func (s *Server) partnerName(r *http.Request) string {
	var name string
	_ = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, principalFrom(r.Context()).TenantID).Scan(&name)
	})
	return name
}

// sendInvitation emails an invitation: with a link to create a passkey and
// accept, for someone with no account yet, or asking them to sign in and
// accept.
func (s *Server) sendInvitation(ctx context.Context, email, tenant, partner string, roles []string, linked bool, token string) {
	base := strings.TrimRight(s.PublicURL, "/")
	via := ""
	if partner != "" {
		via = " through " + partner
	}
	var how string
	if linked {
		// The token is in the fragment, which browsers send to no server.
		how = fmt.Sprintf(`To accept, create a passkey for your new Taskiem account here. The link works once, for 7 days:

%s/accept-invitation#token=%s

`, base, token)
	} else {
		how = fmt.Sprintf(`To accept, sign in to Taskiem as you usually do and open your invitations:

%s/login

`, base)
	}
	body := fmt.Sprintf(`You are invited to %s on Taskiem%s, as %s.

%sIf you did not expect this, ignore this email: nothing happens unless you accept.
`, tenant, via, strings.Join(roles, ", "), how)
	if err := s.sendMail(ctx, email, "You are invited to "+tenant+" on Taskiem", body); err != nil {
		s.Logger.Error("invitation email not sent", "err", err)
	}
}

// subAudit appends an entry to a sub-tenant's chain for the partner key, in
// a transaction partnerTx entered.
func subAudit(r *http.Request, tx pgx.Tx, sub uuid.UUID, action, target string, detail map[string]any) error {
	p := principalFrom(r.Context())
	if detail == nil {
		detail = map[string]any{}
	}
	detail["ip"] = clientIP(r)
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(r.Context(), `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, sub, p.ActorType(), partnerActor(p), action, target, raw)
	return err
}

// listSubTenantInvitations lists the partner's own pending invitations in
// a sub-tenant (not those its own admins made).
func (s *Server) listSubTenantInvitations(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []partnerInvitation
	err = s.partnerTx(r, sub, "partner.subtenant.invitations.read", nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT i.user_id, u.email, i.roles, i.invited_by, i.created_at
			FROM member_invitations i JOIN taskiem_tenant_invitees($1) u ON u.user_id = i.user_id WHERE i.source = 'partner' ORDER BY i.created_at`, sub)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[partnerInvitation])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": nonNil(out)})
}

// cancelSubTenantInvitation withdraws one of the partner's pending
// invitations; its link stops working with it.
func (s *Server) cancelSubTenantInvitation(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	err = s.partnerTx(r, sub, "partner.subtenant.invitation.cancel", map[string]any{"user": user}, func(tx pgx.Tx) error {
		var roles []string
		if err := tx.QueryRow(r.Context(), `DELETE FROM member_invitations WHERE user_id = $1 AND source = 'partner' RETURNING roles`, user).Scan(&roles); err != nil {
			return err
		}
		return subAudit(r, tx, sub, "member.invitation.cancel", user.String(), map[string]any{"roles": roles, "via": "partner"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSubTenantRoles lists a sub-tenant's roles, as GET /v1/roles does.
func (s *Server) listSubTenantRoles(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []roleInfo
	err = s.partnerTx(r, sub, "partner.subtenant.roles.read", nil, func(tx pgx.Tx) error {
		var err error
		out, err = rolesTx(r.Context(), tx)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

type partnerRoleReq struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// checkPartnerRole validates a custom role a partner defines in a
// sub-tenant: a valid name that is not built in, and at least one known
// permission, none of them the partner API's.
func checkPartnerRole(name string, perms []string) ([]string, error) {
	switch {
	case !roleNameRe.MatchString(name):
		return nil, fmt.Errorf("%w: a role name is 2 to 48 lowercase letters, digits or underscores", errBadRequest)
	case rolePermissions[name] != nil:
		return nil, fmt.Errorf("%w: %s is a built-in role", errBadRequest, name)
	case len(perms) == 0:
		return nil, fmt.Errorf("%w: role %s needs at least one permission", errBadRequest, name)
	}
	out := slices.Compact(slices.Sorted(slices.Values(perms)))
	for _, perm := range out {
		if _, ok := permissionInfo[perm]; !ok || perm == PermPartnerRead || perm == PermPartnerManage {
			return nil, fmt.Errorf("%w: role %s: %q is not a permission a sub-tenant's role can carry", errBadRequest, name, perm)
		}
	}
	return out, nil
}

// putPartnerRoleTx creates a custom role in the sub-tenant the transaction
// entered, or (with update) changes one the partner created that nobody
// holds yet. A role the sub-tenant's own people created, or a name members
// already hold as a business role, is theirs: it is left alone and
// reported as kept (created false), or refused when strict.
func putPartnerRoleTx(r *http.Request, tx pgx.Tx, sub uuid.UUID, name, description string, perms []string, strict, update bool) (created bool, err error) {
	ctx := r.Context()
	var createdBy string
	var before []string
	err = tx.QueryRow(ctx, `SELECT created_by, permissions FROM roles WHERE name = $1 FOR UPDATE`, name).Scan(&createdBy, &before)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE role = $1)`, name).Scan(&held); err != nil {
			return false, err
		}
		if held {
			if strict {
				return false, fmt.Errorf("%w: members of the sub-tenant hold %s already; its own admins define it", errConflict, name)
			}
			return false, nil
		}
		p := principalFrom(ctx)
		if _, err := tx.Exec(ctx, `INSERT INTO roles (tenant_id, name, description, permissions, created_by) VALUES ($1, $2, $3, $4, $5)`,
			sub, name, description, perms, partnerActor(p)); err != nil {
			return false, err
		}
		return true, subAudit(r, tx, sub, "role.put", name, map[string]any{"permissions": perms, "via": "partner"})
	case err != nil:
		return false, err
	}
	if !strings.HasPrefix(createdBy, "partner:") {
		if strict {
			return false, fmt.Errorf("%w: the sub-tenant's own people created %s; only they change it", errConflict, name)
		}
		return false, nil
	}
	if !update {
		return false, nil
	}
	// Once someone holds it, what it allows is the sub-tenant's to change:
	// the partner cannot widen its members' access behind them.
	var held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE role = $1)`, name).Scan(&held); err != nil {
		return false, err
	}
	if held && !slices.Equal(before, perms) {
		return false, fmt.Errorf("%w: members of the sub-tenant hold %s: only its own admins change what it allows", errConflict, name)
	}
	if _, err := tx.Exec(ctx, `UPDATE roles SET description = $2, permissions = $3, updated_at = now() WHERE name = $1`, name, description, perms); err != nil {
		return false, err
	}
	return false, subAudit(r, tx, sub, "role.put", name, map[string]any{"permissions": perms, "before": before, "via": "partner"})
}

// putSubTenantRole creates or changes a custom role in a sub-tenant.
func (s *Server) putSubTenantRole(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	var req partnerRoleReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	perms, err := checkPartnerRole(name, req.Permissions)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Sub-tenants have their partner's plan.
	if err := s.Billing.Require(r.Context(), principalFrom(r.Context()).TenantID, billing.FeatureCustomRoles); err != nil {
		s.billingFail(w, r, err)
		return
	}
	var created bool
	err = s.partnerTx(r, sub, "partner.subtenant.role.put", map[string]any{"role": name, "permissions": perms}, func(tx pgx.Tx) error {
		var err error
		created, err = putPartnerRoleTx(r, tx, sub, name, req.Description, perms, true, true)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, roleInfo{Name: name, Description: req.Description, Permissions: perms})
}
