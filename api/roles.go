package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Custom roles (spec 13.3): a tenant names a set of permissions, and
// members hold it like a built-in role. No one can create, widen or grant
// a role carrying a permission they do not hold themselves, so roles never
// escalate privilege.

// permissionInfo describes each permission for the role editor.
var permissionInfo = map[string]string{
	PermWorkflowRead:     "See workflows and their versions",
	PermWorkflowEdit:     "Create and edit workflow drafts",
	PermWorkflowPublish:  "Publish workflow versions",
	PermRunRead:          "See runs and their history",
	PermRunStart:         "Start runs",
	PermRunCancel:        "Cancel runs",
	PermRunResolve:       "Resolve steps that need reconciliation",
	PermApprovalDecide:   "Decide approvals for roles held",
	PermPIIReveal:        "Reveal sealed personal data (audited)",
	PermPIIErase:         "Erase a person's data",
	PermSecretManage:     "Manage secrets, variables and egress",
	PermConnectionManage: "Manage connections and acknowledge contract drift",
	PermMemberManage:     "Manage members and API keys",
	PermAuditRead:        "Read the audit log and reports",
	PermGitManage:        "Connect environments to Git",
	PermPolicyManage:     "Manage approval policies",
	PermConnectorManage:  "Upload the tenant's own connectors",
	PermRoleManage:       "Manage custom roles",
	PermAlertManage:      "Manage alert channels and rules",
	PermSCIM:             "Provision members over SCIM (for an identity provider's key)",
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,47}$`)

type roleInfo struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Permissions []string   `json:"permissions"`
	BuiltIn     bool       `json:"built_in"`
	Members     int        `json:"members"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

func (s *Server) listPermissions(w http.ResponseWriter, _ *http.Request) {
	type perm struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	out := make([]perm, 0, len(allPermissions))
	for _, p := range allPermissions {
		out = append(out, perm{p, permissionInfo[p]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": out})
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	var custom []roleInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT role, count(*) FROM memberships GROUP BY role`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var role string
			var n int
			if err := rows.Scan(&role, &n); err != nil {
				rows.Close()
				return err
			}
			counts[role] = n
		}
		rows.Close()
		rows, err = tx.Query(r.Context(), `SELECT name, description, permissions, updated_at FROM roles ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ri roleInfo
			var at time.Time
			if err := rows.Scan(&ri.Name, &ri.Description, &ri.Permissions, &at); err != nil {
				return err
			}
			ri.UpdatedAt = &at
			custom = append(custom, ri)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []roleInfo
	names := make([]string, 0, len(rolePermissions))
	for n := range rolePermissions {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(rolePermissions[names[i]]) > len(rolePermissions[names[j]]) })
	for _, n := range names {
		out = append(out, roleInfo{Name: n, Permissions: slices.Sorted(slices.Values(rolePermissions[n])), BuiltIn: true, Members: counts[n]})
	}
	for _, c := range custom {
		c.Members = counts[c.Name]
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

// putRole creates or replaces a custom role.
func (s *Server) putRole(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	switch {
	case !roleNameRe.MatchString(name):
		s.fail(w, r, fmt.Errorf("%w: a role name is 2 to 48 lowercase letters, digits or underscores", errBadRequest))
		return
	case rolePermissions[name] != nil:
		s.fail(w, r, fmt.Errorf("%w: %s is a built-in role", errBadRequest, name))
		return
	case len(req.Permissions) == 0:
		s.fail(w, r, fmt.Errorf("%w: a role needs at least one permission (a role with none is a business role: grant it directly)", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	perms := slices.Compact(slices.Sorted(slices.Values(req.Permissions)))
	for _, perm := range perms {
		if _, ok := permissionInfo[perm]; !ok {
			s.fail(w, r, fmt.Errorf("%w: unknown permission %q", errBadRequest, perm))
			return
		}
		if !p.Can(perm) {
			s.fail(w, r, fmt.Errorf("%w: you cannot give a role %s, which you do not hold", errForbidden, perm))
			return
		}
	}
	created := false
	err := s.tx(r, func(tx pgx.Tx) error {
		var before []string
		err := tx.QueryRow(r.Context(), `SELECT permissions FROM roles WHERE name = $1 FOR UPDATE`, name).Scan(&before)
		if errors.Is(err, pgx.ErrNoRows) {
			created = true
			// Members may already hold the name as a business role: giving
			// it permissions grants those to them, so it takes the standing
			// to grant the role.
			var held bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM memberships WHERE role = $1)`, name).Scan(&held); err != nil {
				return err
			}
			if held {
				if !p.Can(PermMemberManage) {
					return fmt.Errorf("%w: members hold %s already; turning it into a custom role needs %s", errForbidden, name, PermMemberManage)
				}
				if err := canGrant(r.Context(), tx, p, []string{name}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO roles (tenant_id, name, description, permissions, created_by) VALUES ($1, $2, $3, $4, $5)`,
				p.TenantID, name, req.Description, perms, p.Actor()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if _, err := tx.Exec(r.Context(), `UPDATE roles SET description = $2, permissions = $3, updated_at = now() WHERE name = $1`, name, req.Description, perms); err != nil {
			return err
		}
		return auditTx(r, tx, "role.put", name, map[string]any{"permissions": perms, "before": before})
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

// deleteRole removes a custom role no one holds.
func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := s.tx(r, func(tx pgx.Tx) error {
		var held int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM memberships WHERE role = $1`, name).Scan(&held); err != nil {
			return err
		}
		if held > 0 {
			return fmt.Errorf("%w: %d member(s) hold %s; take it from them first", errConflict, held, name)
		}
		tag, err := tx.Exec(r.Context(), `DELETE FROM roles WHERE name = $1`, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "role.delete", name, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// canGrant refuses roles carrying permissions the granter does not hold,
// and business roles to a granter who cannot decide approvals: a business
// role (one with no roles row), or a custom role an approval names, decides
// who approves, so granting it takes approval.decide.
func canGrant(ctx context.Context, tx pgx.Tx, p *Principal, roles []string) error {
	for _, role := range roles {
		perms, builtin := rolePermissions[role]
		if !builtin {
			err := tx.QueryRow(ctx, `SELECT permissions FROM roles WHERE name = $1`, role).Scan(&perms)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			routed := errors.Is(err, pgx.ErrNoRows)
			if !routed {
				if routed, err = approvalRole(ctx, tx, role); err != nil {
					return err
				}
			}
			if routed && !p.Can(PermApprovalDecide) {
				return fmt.Errorf("%w: %s decides who approves: granting it needs %s", errForbidden, role, PermApprovalDecide)
			}
		}
		for _, perm := range perms {
			if !p.Can(perm) {
				return fmt.Errorf("%w: you cannot grant %s: it carries %s, which you do not hold", errForbidden, role, perm)
			}
		}
	}
	return nil
}

// approvalRole reports whether an approval names role: an open or past
// approval, an approval policy, or a workflow definition.
func approvalRole(ctx context.Context, tx pgx.Tx, role string) (bool, error) {
	var named bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approvals WHERE role = $1)
		OR EXISTS (SELECT 1 FROM approval_policies WHERE state IN ('active', 'pending') AND jsonb_path_exists(document, '$.**.role ? (@ == $r)', jsonb_build_object('r', $1::text)))
		OR EXISTS (SELECT 1 FROM workflow_versions WHERE state IN ('draft', 'published', 'deprecated') AND jsonb_path_exists(definition, '$.**.role ? (@ == $r)', jsonb_build_object('r', $1::text)))`,
		role).Scan(&named)
	return named, err
}

// revokeRole takes one role from a member. The last owner keeps theirs.
func (s *Server) revokeRole(w http.ResponseWriter, r *http.Request) {
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: bad user id", errBadRequest))
		return
	}
	role := chi.URLParam(r, "role")
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := canGrant(r.Context(), tx, p, []string{role}); err != nil {
			return err // taking a role away needs the same standing as giving it
		}
		if role == "owner" {
			// Lock the owners' rows, so two owners cannot revoke each other at once.
			rows, err := tx.Query(r.Context(), `SELECT user_id FROM memberships WHERE role = 'owner' FOR UPDATE`)
			if err != nil {
				return err
			}
			held, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			owners := 0
			for _, o := range held {
				if o != user {
					owners++
				}
			}
			if owners == 0 {
				return fmt.Errorf("%w: a tenant keeps at least one owner", errConflict)
			}
		}
		tag, err := tx.Exec(r.Context(), `DELETE FROM memberships WHERE user_id = $1 AND role = $2`, user, role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		// Delegations of the role end with it.
		if err := endDelegations(r.Context(), tx, user, role); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM memberships WHERE user_id = $1)`, user); err != nil {
			return err
		}
		return auditTx(r, tx, "member.revoke", user.String(), map[string]any{"role": role})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// endDelegations takes role out of the user's delegations, revoking those
// left with no role. Approvals also check that a delegator still holds the
// role, so a membership removed any other way (SCIM) ends them too.
func endDelegations(ctx context.Context, tx pgx.Tx, user uuid.UUID, role string) error {
	if _, err := tx.Exec(ctx, `UPDATE delegations SET revoked_at = now() WHERE from_user = $1 AND roles = ARRAY[$2]::text[] AND revoked_at IS NULL`, user, role); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE delegations SET roles = array_remove(roles, $2) WHERE from_user = $1 AND $2 = ANY (roles) AND cardinality(roles) > 1 AND revoked_at IS NULL`, user, role)
	return err
}
