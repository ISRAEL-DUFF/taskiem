package api

import (
	"context"
	"github.com/israel-duff/taskiem/engine/db"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Starting something (docs/spec/taskiem-for-everyone.md §18): a person
// starts a business, or a workflows workspace, a tenant with every area off
// that automates work for them or a team and is never listed for others to
// find. Both are tenants made the same way; tenants.kind says which, and
// follows the areas after (areas.go). Every door asks the same question:
// POST /v1/me/start here, chat sign-up (chat_signup.go), Slack (always
// workflows) and the web form (kinds: [] is workflows).

// startPerDay is how many businesses or workspaces one person may start in
// a day with POST /v1/me/start.
const startPerDay = 5

// insertTenantTx makes a tenant for an owner who exists (made earlier in
// this transaction, or before): the tenant of its kind, the owner's
// membership, the default workspace and the dev and prod environments. A
// tenant with no name (a workflows workspace) is named after its owner. It
// returns the name.
func insertTenantTx(ctx context.Context, tx pgx.Tx, tenant, owner uuid.UUID, name, kind string) (string, error) {
	if kind == "" {
		kind = areas.TenantBusiness
	}
	named := name
	if named == "" {
		named = workflowsName("")
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO tenants (id, name, plan_id, kind) VALUES ($1, $2, $3, $4)`, []any{tenant, named, uuid.Nil, kind}},
		{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{tenant, owner}},
		{`INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, []any{uuid.Must(uuid.NewV7()), tenant}},
		{`INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, []any{tenant}},
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st.q, st.args...); err != nil {
			return "", err
		}
	}
	if name != "" {
		return named, nil
	}
	// The owner is a member now, so their row is in scope.
	var person string
	if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, owner).Scan(&person); err != nil {
		return "", err
	}
	if n := workflowsName(person); n != named {
		if _, err := tx.Exec(ctx, `UPDATE tenants SET name = $2 WHERE id = $1`, tenant, n); err != nil {
			return "", err
		}
		named = n
	}
	return named, nil
}

// workflowsName is a workflows workspace's name until it is renamed:
// "{person}'s workflows", or "My workflows" for someone with no name.
func workflowsName(person string) string {
	person = strings.Join(strings.Fields(person), " ")
	if person == "" {
		return "My workflows"
	}
	const tail = "'s workflows"
	if r := []rune(person); len(r)+utf8.RuneCountInString(tail) > 80 {
		person = strings.TrimSpace(string(r[:80-utf8.RuneCountInString(tail)]))
	}
	return person + tail
}

// startAreasTx writes the areas a new tenant starts with, as the business's
// own choice (every part a row): Schools through its own switch, and a
// delivery business's set-up when kinds names delivery.
func startAreasTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, st areas.State, kinds []string, by string) error {
	if err := areas.SaveAll(ctx, tx, tenant, st, by); err != nil {
		return err
	}
	if st.On(areas.Schools) {
		if _, err := schools.SetEnabled(ctx, tx, tenant, true, by); err != nil {
			return err
		}
	}
	if slices.Contains(kinds, "delivery") {
		return startDeliveryBusinessTx(ctx, tx, tenant, "system:"+by)
	}
	return nil
}

// startSomething is POST /v1/me/start ({kind, name?, kinds?}): the
// signed-in person starts a business or a workflows workspace, as its
// owner, and this session moves into it, as switching does (switch.go).
// Only the person counts here, never the business the session is in, so a
// session in no business may start one too.
func (s *Server) startSomething(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	tok := sessionToken(r)
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.Agent != nil || p.EndUser != nil || p.AuthMethod == "" || tok == "" {
		writeErr(w, http.StatusForbidden, "only a signed-in person starts a business or workflows")
		return
	}
	if p.AuthMethod == "sso" {
		// As for switching: single sign-on vouches for a person in one
		// business only.
		writeJSON(w, http.StatusConflict, map[string]any{"error": "you signed in with your organisation's single sign-on: sign in another way to start something", "sign_in": true})
		return
	}
	var req struct {
		Kind  string    `json:"kind"`
		Name  string    `json:"name"`
		Kinds *[]string `json:"kinds"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var start areas.State
	var kinds []string
	name := ""
	switch req.Kind {
	case areas.TenantWorkflows:
		if req.Kinds != nil && len(*req.Kinds) > 0 {
			writeErr(w, http.StatusBadRequest, "workflows start with every part of the business off: start a business to choose what it does")
			return
		}
		if strings.TrimSpace(req.Name) != "" {
			n, ok := plainName(req.Name, 2, 80)
			if !ok {
				writeErr(w, http.StatusBadRequest, "the name needs 2 to 80 characters, and no links or email addresses")
				return
			}
			name = n
		}
		start, _ = areas.FromKinds(nil)
	case areas.TenantBusiness:
		n, ok := plainName(req.Name, 2, 80)
		if !ok {
			writeErr(w, http.StatusBadRequest, "the business name needs 2 to 80 characters, and no links or email addresses")
			return
		}
		name = n
		if req.Kinds != nil {
			if len(*req.Kinds) == 0 {
				writeErr(w, http.StatusBadRequest, "say what the business does (shop, service, delivery or school), or start workflows instead")
				return
			}
			var err error
			if start, err = areas.FromKinds(*req.Kinds); err != nil {
				writeErr(w, http.StatusBadRequest, "kinds are shop, service, delivery or school")
				return
			}
			kinds = *req.Kinds
		}
	default:
		writeErr(w, http.StatusBadRequest, "kind is business or workflows")
		return
	}
	ctx := r.Context()
	id := identityHash("start:" + p.UserID.String())
	var today, all int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT identity_day, all_hour FROM taskiem_chat_signup_counts($1)`, id).Scan(&today, &all); err != nil {
		s.fail(w, r, err)
		return
	}
	if today >= startPerDay {
		writeErr(w, http.StatusTooManyRequests, "you have started the most businesses and workspaces for one day: try again tomorrow")
		return
	}
	// A web start is recorded as a sign-up, whose "confirm your email"
	// holds then apply; a person with no email of their own (made with a
	// phone, or in chat) has none to confirm, so theirs is recorded as the
	// app's.
	source := "signup"
	if !p.viaCookie || s.placeholderEmailOf(ctx, p.UserID) {
		source = "app"
	}
	next := newToken()
	var made string
	nb := newBusiness{Name: name, Owner: p.UserID, Source: source, ChannelHash: id, ActorType: "user", Kind: req.Kind,
		Detail: map[string]any{"kind": req.Kind, "from": "start"}}
	nb.Then = func(tx pgx.Tx, tenant, owner uuid.UUID) error {
		if start != nil {
			if err := startAreasTx(ctx, tx, tenant, start, kinds, "start"); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&made); err != nil {
			return err
		}
		return nil
	}
	tenant, _, err := s.makeBusiness(ctx, nb)
	switch {
	case err != nil && tenant == uuid.Nil:
		s.fail(w, r, err)
		return
	case err != nil:
		// Made and moved into; only its trial did not start, which
		// starts on its first visit to billing.
		s.Logger.Warn("start: starting the trial", "err", err)
	}
	// This session moves into what was made, as switching moves it
	// (switch.go): from a business or from the person's own side.
	var moved bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_session_move($1, $2, $3, $4)`, hashToken(tok), hashToken(next), tenant, clientIP(r)).Scan(&moved); err != nil {
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
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.switch', $2, jsonb_build_object('from', $3::text, 'ip', $4::text))`,
			tenant, p.UserID.String(), from, clientIP(r))
		return err
	}); err != nil {
		s.Logger.Warn("start: audit", "err", err)
	}
	out := map[string]any{"tenant_id": tenant, "kind": req.Kind, "name": made, "user_id": p.UserID}
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, p.UserID).Scan(&tenants); err == nil {
		out["tenants"] = tenants
	}
	s.deliverSession(w, !p.viaCookie, next, out)
	writeJSON(w, http.StatusCreated, out)
}

// placeholderEmailOf reports whether the person's sign-in email is a
// placeholder (an account made with a phone, or in chat): they have no
// email of their own to confirm. A failed read counts as a placeholder,
// so no one is held on an email they may not have.
func (s *Server) placeholderEmailOf(ctx context.Context, user uuid.UUID) bool {
	var email string
	if err := db.InPersonTx(ctx, s.Store.Pool, user, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)
	}); err != nil {
		return true
	}
	return emailPlaceholder(email)
}
