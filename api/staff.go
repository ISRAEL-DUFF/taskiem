package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Staff (docs/spec/chat-five-more-flows.md, phase 3 plan, step 1): the
// people of a business, added by phone from the web as from chat
// ("invite +234… as cashier", "add Tunde 0803 123 4567 as cashier"). An
// invitation goes to their WhatsApp as the taskiem_invite template; Join
// makes their account (no email needed), binds their chat and grants the
// role (chat_invites.go). Removing someone takes every role they hold in
// the business; their history stays.

// staffInviteResend is how soon an invitation may be sent again.
const staffInviteResend = 5 * time.Minute

// staffInviteTx records an invitation by phone: one still waiting for the
// same number is renewed (its roles, name and seven days), not doubled.
// It answers the invitation and the inviter's name, for the message.
func (s *Server) staffInviteTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, business string, p *Principal, number, name string, roles []string, via string) (uuid.UUID, string, error) {
	var inviter string
	if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&inviter); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", err
	}
	if inviter = clip(strings.TrimSpace(inviter), 80); inviter == "" {
		inviter = business
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM chat_invites WHERE tenant_id = $1 AND channel = 'whatsapp' AND address = $2 AND state = 'sent' AND expires_at > now()
		ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, tenant, number).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		id = uuid.Must(uuid.NewV7())
		var by *uuid.UUID
		if p.UserID != uuid.Nil {
			by = &p.UserID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_invites (id, tenant_id, channel, address, roles, invited_by, inviter, name, expires_at)
			VALUES ($1, $2, 'whatsapp', $3, $4, $5, $6, $7, now() + $8::interval)`, id, tenant, number, roles, by, inviter, name, chatInviteTTL.String()); err != nil {
			return uuid.Nil, "", err
		}
	case err != nil:
		return uuid.Nil, "", err
	default:
		if _, err := tx.Exec(ctx, `UPDATE chat_invites SET roles = $2, name = CASE WHEN $3 = '' THEN name ELSE $3 END, inviter = $4,
			expires_at = now() + $5::interval WHERE id = $1`, id, roles, name, inviter, chatInviteTTL.String()); err != nil {
			return uuid.Nil, "", err
		}
	}
	shown := number
	if name != "" {
		shown = name + " (" + number + ")"
	}
	return id, inviter, waAudit(ctx, tx, via, p, "member.invite", shown, map[string]any{"roles": roles, "via": via})
}

// deliverStaffInvite sends the invitation to the person's WhatsApp, and
// records when; false when it could not go (it stays, to send again).
func (s *Server) deliverStaffInvite(ctx context.Context, tenant uuid.UUID, business, inviter, number string, roles []string, id uuid.UUID) bool {
	if s.WhatsApp == nil {
		return false
	}
	role := strings.ReplaceAll(strings.Join(roles, ", "), "_", " ")
	if _, err := s.WhatsApp.SendTemplate(ctx, tenant, number, whatsapp.TplInvite, map[string]string{"tenant": business, "inviter": inviter, "role": role},
		[]string{invPrefix + "join:" + id.String(), invPrefix + "no:" + id.String()}); err != nil {
		s.Logger.Warn("staff: sending the invitation", "err", err)
		return false
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE chat_invites SET sent_at = now() WHERE id = $1`, id)
		return err
	}); err != nil {
		s.Logger.Warn("staff: recording the invitation sent", "err", err)
	}
	return true
}

// staffMember is a person of the business, as the Staff page lists them.
type staffMember struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	// Email is theirs, or none for someone who joined by phone.
	Email string   `json:"email,omitempty"`
	Phone string   `json:"phone,omitempty"`
	Roles []string `json:"roles"`
	// Chat: their WhatsApp or Telegram is linked.
	Chat bool `json:"chat"`
	You  bool `json:"you,omitempty"`
}

// staffInvite is an invitation by phone not yet answered.
type staffInvite struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name,omitempty"`
	Phone     string     `json:"phone"`
	Roles     []string   `json:"roles"`
	Inviter   string     `json:"inviter"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// listStaff is GET /v1/staff: the people of the business and the
// invitations by phone waiting for an answer. Numbers are masked unless
// the person may reveal personal data.
func (s *Server) listStaff(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	reveal := p.Can(PermPIIReveal)
	show := func(n string) string {
		if reveal {
			return n
		}
		return maskPhone(n)
	}
	people := []staffMember{}
	invites := []staffInvite{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if people, err = staffPeopleTx(r.Context(), tx, p, uuid.Nil, show); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT id, name, address, roles, inviter, sent_at, expires_at, created_at FROM chat_invites
			WHERE tenant_id = $1 AND channel = 'whatsapp' AND state = 'sent' AND expires_at > now() ORDER BY created_at DESC`, p.TenantID)
		if err != nil {
			return err
		}
		if invites, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (staffInvite, error) {
			var x staffInvite
			err := row.Scan(&x.ID, &x.Name, &x.Phone, &x.Roles, &x.Inviter, &x.SentAt, &x.ExpiresAt, &x.CreatedAt)
			x.Phone = show(x.Phone)
			return x, err
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": people, "invites": invites})
}

// staffPeopleTx is the people of the business (one, when user is set),
// with their chat numbers shown as show says.
func staffPeopleTx(ctx context.Context, tx pgx.Tx, p *Principal, user uuid.UUID, show func(string) string) ([]staffMember, error) {
	rows, err := tx.Query(ctx, `SELECT u.id, u.name, u.email, array_agg(m.role ORDER BY m.role) FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.tenant_id = $1 AND ($2::uuid IS NULL OR u.id = $2) GROUP BY u.id, u.name, u.email ORDER BY lower(u.name), u.email`, p.TenantID, nullUUID(user))
	if err != nil {
		return nil, err
	}
	people, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (staffMember, error) {
		var m staffMember
		err := row.Scan(&m.UserID, &m.Name, &m.Email, &m.Roles)
		if emailPlaceholder(m.Email) {
			m.Email = ""
		}
		m.You = m.UserID == p.UserID
		return m, err
	})
	if err != nil || len(people) == 0 {
		return []staffMember{}, err
	}
	// Their chat numbers (the bindings answer within the business).
	ids := make([]uuid.UUID, len(people))
	for i, m := range people {
		ids[i] = m.UserID
	}
	rows, err = tx.Query(ctx, `SELECT user_id, number FROM taskiem_wa_numbers($1)`, ids)
	if err != nil {
		return nil, err
	}
	type bound struct {
		User   uuid.UUID
		Number string
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[bound])
	for _, b := range got {
		for i := range people {
			if people[i].UserID == b.User {
				people[i].Phone, people[i].Chat = show(b.Number), true
			}
		}
	}
	return people, err
}

// nullUUID is nil for the zero id, for "any" in a query.
func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// staffInviteBody is POST /v1/staff/invites.
type staffInviteBody struct {
	Name  string   `json:"name"`
	Phone string   `json:"phone"`
	Roles []string `json:"roles"`
}

// inviteStaff is POST /v1/staff/invites: add someone by phone. Their
// WhatsApp gets the invitation; they join with Join.
func (s *Server) inviteStaff(w http.ResponseWriter, r *http.Request) {
	var in staffInviteBody
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	name, ok := plainName(strings.TrimSpace(in.Name), 1, 80)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: give their name (up to 80 letters)", errBadRequest))
		return
	}
	number, err := whatsapp.Normalise(in.Phone, s.defaultCountry())
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %s is not a mobile number Taskiem can reach (like 0803 123 4567)", errBadRequest, strings.TrimSpace(in.Phone)))
		return
	}
	roles := []string{}
	for _, x := range in.Roles {
		if x = staffRole(x); x != "" && !slices.Contains(roles, x) {
			roles = append(roles, x)
		}
	}
	if len(roles) == 0 {
		s.fail(w, r, fmt.Errorf("%w: choose what they do (a role)", errBadRequest))
		return
	}
	if slices.Contains(roles, "owner") && !slices.Contains(p.Roles, "owner") {
		s.fail(w, r, fmt.Errorf("%w: only an owner can make someone an owner", errForbidden))
		return
	}
	var business, inviter string
	var id uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		for _, role := range roles {
			if _, builtin := access.Roles[role]; builtin {
				continue
			}
			var exists bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, role).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: there is no role called %s", errBadRequest, role)
			}
		}
		if err := canGrant(r.Context(), tx, p, roles); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&business); err != nil {
			return err
		}
		var err error
		id, inviter, err = s.staffInviteTx(r.Context(), tx, p.TenantID, business, p, number, name, roles, "web")
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	sent := s.deliverStaffInvite(r.Context(), p.TenantID, business, inviter, number, roles, id)
	s.diaryChange(r.Context(), p, "Invited "+name+" as "+strings.ReplaceAll(strings.Join(roles, ", "), "_", " "))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "sent": sent})
}

// resendStaffInvite is POST /v1/staff/invites/{id}/send: the invitation
// again, and seven more days; not more than once in five minutes.
func (s *Server) resendStaffInvite(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var business, inviter, number string
	var roles []string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var sent *time.Time
		if err := tx.QueryRow(r.Context(), `SELECT i.address, i.roles, i.inviter, i.sent_at, t.name FROM chat_invites i JOIN tenants t ON t.id = i.tenant_id
			WHERE i.tenant_id = $1 AND i.id = $2 AND i.channel = 'whatsapp' AND i.state = 'sent' FOR UPDATE OF i`, p.TenantID, id).
			Scan(&number, &roles, &inviter, &sent, &business); err != nil {
			return err
		}
		if sent != nil && time.Since(*sent) < staffInviteResend {
			return fmt.Errorf("%w: it was sent a few minutes ago; wait a little before sending it again", errConflict)
		}
		if _, err := tx.Exec(r.Context(), `UPDATE chat_invites SET expires_at = now() + $2::interval WHERE id = $1`, id, chatInviteTTL.String()); err != nil {
			return err
		}
		return auditTx(r, tx, "member.invite.resend", id.String(), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "sent": s.deliverStaffInvite(r.Context(), p.TenantID, business, inviter, number, roles, id)})
}

// cancelStaffInvite is DELETE /v1/staff/invites/{id}: Join no longer works.
func (s *Server) cancelStaffInvite(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE chat_invites SET state = 'cancelled', answered_at = now() WHERE tenant_id = $1 AND id = $2 AND state IN ('sent', 'asked')`, p.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "member.invite.cancel", id.String(), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeStaff is POST /v1/staff/{user}/remove: every role the person holds
// in the business goes, so their chat and the web no longer reach it. Not
// oneself, an owner only by an owner, never the last owner, and only by
// someone who could take each of those roles away one by one (mayTakeRoles).
func (s *Server) removeStaff(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if user == p.UserID {
		s.fail(w, r, fmt.Errorf("%w: you cannot remove yourself", errConflict))
		return
	}
	var name string
	var returned []staffTask
	if err := s.tx(r, func(tx pgx.Tx) error {
		var roles []string
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(array_agg(role), '{}') FROM memberships WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, user).Scan(&roles); err != nil {
			return err
		}
		if len(roles) == 0 {
			return pgx.ErrNoRows
		}
		if slices.Contains(roles, "owner") && !slices.Contains(p.Roles, "owner") {
			return fmt.Errorf("%w: only an owner can remove an owner", errForbidden)
		}
		// Removing someone takes every role they hold, so it needs the
		// standing to take each (as DELETE /v1/members/{user}/roles/{role}),
		// and never leaves the business without an owner.
		slices.Sort(roles)
		if err := mayTakeRoles(r.Context(), tx, p, user, roles); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT CASE WHEN name = '' THEN email ELSE name END FROM users WHERE id = $1`, user).Scan(&name); err != nil {
			return err
		}
		if emailPlaceholder(name) {
			name = "Someone"
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM memberships WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, user); err != nil {
			return err
		}
		// Their open tasks go back to whoever gave them (staff_tasks.go).
		var err error
		if returned, err = returnTasksTx(r.Context(), tx, p.TenantID, user, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM member_invitations WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, user); err != nil {
			return err
		}
		return auditTx(r, tx, "member.remove", user.String(), map[string]any{"roles": roles})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.diaryChange(r.Context(), p, "Removed "+name+" from the business")
	s.background(r.Context(), "tasks", func(ctx context.Context) {
		for _, t := range returned {
			if t.GivenBy == p.UserID {
				continue
			}
			s.tellAbout(ctx, p.TenantID, []uuid.UUID{t.GivenBy}, "task:"+t.ID.String(), "Back to you: "+t.What,
				name+" left the business, so the task is yours again. Give it to someone else in Tasks.", nil, nil)
		}
	})
	w.WriteHeader(http.StatusNoContent)
}
