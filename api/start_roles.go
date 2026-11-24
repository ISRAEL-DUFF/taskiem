package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Who may start a workflow by hand (decision 0073). A definition's roles
// name the members who may start it, beside run.start; owners always may.
// The rule holds on every way a person starts a run: the web and the
// app's Run and the API (startRun), and run in the chat apps and the web's
// Taskiem conversation (waTrigger, waConfirm). An API key acts as its
// owner, with their roles (KeyOwnerRoles). Runs a workflow starts by itself (a schedule,
// a webhook, an event, a form, another workflow) are not started by hand
// and are not held to it; neither is a failed run resumed by a repair
// someone accepted, which goes on as the run that was allowed. With no
// roles, everyone with run.start may start it, as before.

// mayStart says whether p may start a workflow whose roles are roles.
func mayStart(p *Principal, roles []string) bool {
	if len(roles) == 0 {
		return true
	}
	if p == nil {
		return false
	}
	held := p.Roles
	if p.KeyID != uuid.Nil {
		held = p.KeyOwnerRoles // a key acts as its owner
	}
	return slices.Contains(held, "owner") || slices.ContainsFunc(roles, func(r string) bool { return slices.Contains(held, r) })
}

// startRolesOf reads a definition's roles and name without loading all
// of it.
func startRolesOf(doc []byte) (roles []string, name string) {
	var head struct {
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	_ = json.Unmarshal(doc, &head)
	return head.Roles, head.Name
}

// startRolesWords names the roles for a person: "staff or manager".
func startRolesWords(roles []string) string {
	switch len(roles) {
	case 0:
		return ""
	case 1:
		return roles[0]
	}
	return strings.Join(roles[:len(roles)-1], ", ") + " or " + roles[len(roles)-1]
}

// errNotYours refuses a start the workflow's roles do not allow.
func errNotYours(name string, roles []string) error {
	return fmt.Errorf("%w: only %s can start %s", errForbidden, startRolesWords(roles), name)
}

// auditStartRefused records a start refused by the workflow's roles.
func auditStartRefused(ctx context.Context, tx pgx.Tx, channel string, p *Principal, wf string, roles []string) error {
	return waAudit(ctx, tx, channel, p, "run.start.refused", wf, map[string]any{"why": "roles", "roles": roles, "held": p.Roles})
}
