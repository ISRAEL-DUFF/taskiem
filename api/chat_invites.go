package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Bringing people in from chat (docs/spec/chat-way-in.md 10.2, decision
// 0058). An owner sends `invite +234… as viewer` (WhatsApp, Telegram) or
// `invite @ada as viewer` (Slack) and confirms with Yes. The person
// invited gets the taskiem_invite template on WhatsApp (the owner's
// invitation is the opt-in; Stop ends it), a t.me link the owner forwards
// on Telegram (a bot cannot write first), or a DM in Slack. Joining is a
// way in like any other: the terms and Yes before anything is made, then
// the person (made if new), their chat bound, and a membership with the
// role. On Telegram the link alone is not enough, since anyone it reaches
// holds it: the person shares their own contact, and its number must be
// the one invited. In Slack, a member of an installed workspace who is
// in no business there may ask to join; an owner approves with a role.

const (
	chatInviteTTL = 7 * 24 * time.Hour
	invPrefix     = "inv:"
	// suInvite is a Telegram join waiting for the person's own contact.
	suInvite = "invite"
)

var (
	inviteRe       = regexp.MustCompile(`(?i)^invite\s+(.+?)\s+as\s+(?:an?\s+)?([a-z][a-z0-9_]*)$`)
	slackMentionRe = regexp.MustCompile(`^<@([A-Z0-9]{1,24})(?:\|[^>]*)?>$`)
)

// chatInvite is an invitation the owner is confirming (chatData).
type chatInvite struct {
	Channel string   `json:"channel"`
	Address string   `json:"address"`
	Shown   string   `json:"shown"`
	Roles   []string `json:"roles"`
}

// inviteRow is an invitation as the person invited, or an owner deciding
// a request, finds it.
type inviteRow struct {
	ID       uuid.UUID
	Tenant   uuid.UUID
	Business string
	Channel  string
	Address  string
	Roles    []string
	Inviter  string
	State    string
}

func (s *Server) inviteByID(ctx context.Context, id uuid.UUID, token []byte) (*inviteRow, error) {
	var r inviteRow
	err := s.Store.Pool.QueryRow(ctx, `SELECT id, tenant_id, business, channel, address, roles, inviter, state FROM taskiem_chat_invite($1, $2)`, id, token).
		Scan(&r.ID, &r.Tenant, &r.Business, &r.Channel, &r.Address, &r.Roles, &r.Inviter, &r.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

// --- the owner's side ---

// waInvite starts an invitation: `invite <number or @mention> as <role>`.
func (s *Server) waInvite(ctx context.Context, c *chat, text string) error {
	if !c.p.Can(PermMemberManage) {
		return c.say(ctx, s, c.t("invite.denied", "org", c.tenant.Name))
	}
	m := inviteRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return c.say(ctx, s, c.t("invite.how"))
	}
	who, role := strings.TrimSpace(m[1]), strings.ToLower(m[2])
	inv := &chatInvite{Channel: c.ch.Name(), Roles: []string{role}}
	if inv.Channel == "slack" {
		mm := slackMentionRe.FindStringSubmatch(who)
		team, _, ok := parseSlackAddr(c.addr)
		if mm == nil || !ok {
			return c.say(ctx, s, c.t("invite.how"))
		}
		inv.Address, inv.Shown = slackAddr(team, mm[1]), "@"+mm[1]
		if out, err := s.slackCall(ctx, c.tenant.ID, team, "get_user", map[string]any{"user": mm[1]}); err == nil {
			if n := strings.TrimSpace(asString(out["real_name"])); n != "" {
				inv.Shown = n
			}
		}
	} else {
		number, err := whatsapp.Normalise(who, s.defaultCountry())
		if err != nil {
			return c.say(ctx, s, c.t("invite.how"))
		}
		inv.Address, inv.Shown = number, number
	}
	if role == "owner" && !slices.Contains(c.p.Roles, "owner") {
		return c.say(ctx, s, c.t("invite.owner_only"))
	}
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if _, builtin := access.Roles[role]; !builtin {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, role).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return errInviteRole
			}
		}
		return canGrant(ctx, tx, c.p, inv.Roles)
	})
	switch {
	case errors.Is(err, errInviteRole):
		return c.say(ctx, s, c.t("invite.role_unknown", "role", role, "roles", "viewer, operator, approver, builder, auditor, admin"))
	case errors.Is(err, errForbidden):
		return c.say(ctx, s, c.t("invite.cannot_grant", "role", role))
	case err != nil:
		return err
	}
	data := c.data
	data.Invite = inv
	if err := s.saveSession(ctx, c, stateConfirming, data, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("invite.confirm", "who", inv.Shown, "org", c.tenant.Name, "role", role),
		whatsapp.Button{ID: "yes", Title: c.t("button.yes")}, whatsapp.Button{ID: "no", Title: c.t("button.no")})
}

var errInviteRole = errors.New("invite: no such role")

// waInviteReply takes the owner's Yes or No, and on Yes sends the
// invitation.
func (s *Server) waInviteReply(ctx context.Context, c *chat, cmd string) error {
	inv := c.data.Invite
	switch cmd {
	case "yes", "y", "confirm":
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("invite.cancelled"))
	default:
		return c.say(ctx, s, c.t("invite.confirm", "who", inv.Shown, "org", c.tenant.Name, "role", strings.Join(inv.Roles, ", ")),
			whatsapp.Button{ID: "yes", Title: c.t("button.yes")}, whatsapp.Button{ID: "no", Title: c.t("button.no")})
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	id := uuid.Must(uuid.NewV7())
	var token string
	var hash []byte
	if inv.Channel == "telegram" {
		if s.Telegram == nil || s.Telegram.Config.BotUsername == "" {
			return c.say(ctx, s, c.t("invite.send_failed"))
		}
		token, hash = newChatLinkToken()
	}
	var inviter string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, c.p.UserID).Scan(&inviter); err != nil {
			return err
		}
		if inviter = clip(strings.TrimSpace(inviter), 80); inviter == "" {
			inviter = c.tenant.Name
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_invites (id, tenant_id, channel, address, roles, invited_by, inviter, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::interval)`, id, c.tenant.ID, inv.Channel, inv.Address, inv.Roles, c.p.UserID, inviter, hash, chatInviteTTL.String()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "member.invite", inviteShown(inv.Channel, inv.Address), map[string]any{"roles": inv.Roles, "via": "chat"})
	}); err != nil {
		return err
	}
	role := strings.Join(inv.Roles, ", ")
	switch inv.Channel {
	case "whatsapp":
		if s.WhatsApp == nil {
			return c.say(ctx, s, c.t("invite.send_failed"))
		}
		if _, err := s.WhatsApp.SendTemplate(ctx, c.tenant.ID, inv.Address, whatsapp.TplInvite, map[string]string{"tenant": c.tenant.Name, "inviter": inviter, "role": role},
			[]string{invPrefix + "join:" + id.String(), invPrefix + "no:" + id.String()}); err != nil {
			s.Logger.Warn("invite: sending the template", "err", err)
			return c.say(ctx, s, c.t("invite.send_failed"))
		}
		return c.say(ctx, s, c.t("invite.sent", "who", inv.Shown))
	case "telegram":
		link := "https://t.me/" + s.Telegram.Config.BotUsername + "?start=inv_" + token
		return c.say(ctx, s, c.t("invite.sent_telegram", "who", inv.Shown, "link", link))
	case "slack":
		l := s.chooseLang(ctx, uuid.Nil, inv.Address, "").Tag
		if _, err := (&slackChannel{s: s, tenant: c.tenant.ID}).Send(ctx, inv.Address, inviteOffer(l, &inviteRow{ID: id, Business: c.tenant.Name, Inviter: inviter, Roles: inv.Roles})); err != nil {
			s.Logger.Warn("invite: the Slack DM", "err", err)
			return c.say(ctx, s, c.t("invite.send_failed"))
		}
		return c.say(ctx, s, c.t("invite.sent", "who", inv.Shown))
	}
	return nil
}

// inviteShown is an invitation's address as the audit log keeps it.
func inviteShown(channel, address string) string {
	if channel == "slack" {
		return address
	}
	return whatsapp.MaskNumber(address)
}

// inviteOffer is the invitation as its person reads it, with Join and No.
func inviteOffer(l lang.Tag, inv *inviteRow) whatsapp.Message {
	return whatsapp.Message{Text: tr(l, "invite.offer", "inviter", inv.Inviter, "org", inv.Business, "role", strings.Join(inv.Roles, ", ")),
		Buttons: []whatsapp.Button{{ID: invPrefix + "join:" + inv.ID.String(), Title: tr(l, "invite.button_join")}, {ID: invPrefix + "no:" + inv.ID.String(), Title: tr(l, "button.no")}}}
}

// --- the person invited ---

// inviteChat is a chat answering an invitation.
type inviteChat struct {
	ch   chatChannel
	addr string
	// user is the person when the chat is bound already.
	user uuid.UUID
	lang lang.Tag
	// tg is the Telegram account writing, when the update gives it.
	tg *telegram.Update
}

func (ic *inviteChat) say(ctx context.Context, s *Server, text string, buttons ...whatsapp.Button) error {
	return s.waSay(ctx, ic.ch, ic.addr, "", text, buttons...)
}

func (ic *inviteChat) t(id string, kv ...string) string { return tr(ic.lang, id, kv...) }

// chatInviteMessage answers an invitation's buttons and words: Join, No,
// the terms' Yes; to a stranger with an invitation waiting, it is
// offered. false leaves the message to what comes next.
func (s *Server) chatInviteMessage(ctx context.Context, ic *inviteChat, text, reply string) (bool, error) {
	word := normalizeCommand(text)
	if rest, ok := strings.CutPrefix(reply, invPrefix); ok {
		action, idText, _ := strings.Cut(rest, ":")
		switch action {
		case "ok", "deny":
			return true, s.inviteDecide(ctx, ic, action, idText)
		case "ask":
			return false, nil // Slack's Ask to join (slackStranger)
		}
		id, err := uuid.Parse(strings.SplitN(idText, ":", 2)[0])
		if err != nil {
			return true, ic.say(ctx, s, ic.t("invite.gone"))
		}
		inv, err := s.inviteByID(ctx, id, nil)
		if err != nil {
			return true, err
		}
		if inv == nil || inv.State != "sent" || inv.Channel != ic.ch.Name() || (inv.Channel != "telegram" && inv.Address != ic.addr) {
			return true, ic.say(ctx, s, ic.t("invite.gone"))
		}
		return true, s.inviteAnswer(ctx, ic, inv, action)
	}
	if ic.ch.Name() == "telegram" {
		return false, nil // Telegram's invitations come by their link
	}
	waiting, err := s.invitesFor(ctx, ic.ch.Name(), ic.addr)
	if err != nil || len(waiting) == 0 {
		return false, err
	}
	switch {
	case word == "join" || word == "stop" && ic.user == uuid.Nil:
		inv := waiting[0]
		action := "join"
		if word == "stop" {
			action = "no"
		}
		return true, s.inviteAnswer(ctx, ic, &inv, action)
	case ic.user == uuid.Nil:
		// A stranger with an invitation waiting: it, rather than sign-up.
		_, err := ic.ch.Send(ctx, ic.addr, inviteOffer(ic.lang, &waiting[0]))
		return true, err
	}
	return false, nil
}

func (s *Server) invitesFor(ctx context.Context, channel, addr string) ([]inviteRow, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT id, tenant_id, business, roles, inviter FROM taskiem_chat_invites_for($1, $2)`, channel, addr)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (inviteRow, error) {
		x := inviteRow{Channel: channel, Address: addr, State: "sent"}
		return x, r.Scan(&x.ID, &x.Tenant, &x.Business, &x.Roles, &x.Inviter)
	})
}

// inviteAnswer takes Join, the terms' Yes, or No.
func (s *Server) inviteAnswer(ctx context.Context, ic *inviteChat, inv *inviteRow, action string) error {
	switch action {
	case "no":
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{inv.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE chat_invites SET state = 'declined', answered_at = now() WHERE id = $1 AND state = 'sent'`, inv.ID)
			return err
		}); err != nil {
			return err
		}
		return ic.say(ctx, s, ic.t("invite.declined"))
	case "join", "yes":
	default:
		return nil
	}
	// The terms first, for someone with no account: nothing is made
	// before Yes.
	var agreed *time.Time
	if action == "yes" {
		now := time.Now().UTC()
		agreed = &now
	} else if ic.user == uuid.Nil && s.Terms.On() {
		return ic.say(ctx, s, ic.t("invite.terms", "org", inv.Business, "link", s.termsLink()),
			whatsapp.Button{ID: invPrefix + "yes:" + inv.ID.String(), Title: ic.t("button.yes")}, whatsapp.Button{ID: invPrefix + "no:" + inv.ID.String(), Title: ic.t("button.no")})
	}
	if ic.ch.Name() == "telegram" {
		// The link reached whoever it was forwarded to: their own
		// contact must be the number invited.
		id, ok := telegram.ParseAddress(ic.addr)
		if !ok || s.Telegram == nil {
			return nil
		}
		raw, _ := json.Marshal(inviteWait{Invite: inv.ID, Agreed: agreed})
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_signup_put($1, 'telegram', $2, $3, $4::interval)`, signupKey(ic.addr), suInvite, raw, chatSignupTTL.String()); err != nil {
			return err
		}
		return s.Telegram.Client.AskContact(ctx, id, ic.t("invite.ask_contact"), ic.t("agent.share_button"))
	}
	return s.inviteJoin(ctx, ic, inv, agreed)
}

// inviteWait is a Telegram join waiting for the contact.
type inviteWait struct {
	Invite uuid.UUID  `json:"invite"`
	Agreed *time.Time `json:"agreed,omitempty"`
}

// inviteContact takes the contact a Telegram join asked for: the person's
// own, and the number invited. false when no join waits for it.
func (s *Server) inviteContact(ctx context.Context, ic *inviteChat, c *telegram.Contact) (bool, error) {
	var step string
	var raw []byte
	err := s.Store.Pool.QueryRow(ctx, `SELECT step, data FROM taskiem_chat_signup_get($1)`, signupKey(ic.addr)).Scan(&step, &raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && step != suInvite) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var w inviteWait
	_ = json.Unmarshal(raw, &w)
	inv, err := s.inviteByID(ctx, w.Invite, nil)
	if err != nil {
		return true, err
	}
	if inv == nil || inv.State != "sent" {
		return true, ic.say(ctx, s, ic.t("invite.gone"))
	}
	number, nerr := whatsapp.Normalise(c.Phone, s.defaultCountry())
	if !c.Own || nerr != nil || number != inv.Address {
		return true, ic.say(ctx, s, ic.t("invite.contact_wrong"))
	}
	if err := s.signupDrop(ctx, ic.addr); err != nil {
		return true, err
	}
	return true, s.inviteJoin(ctx, ic, inv, w.Agreed)
}

// inviteJoin makes the person a member: their account if they have none
// (a sign-in name by their chat, as a chat sign-up makes), their chat
// bound, the roles, the terms when they just agreed.
func (s *Server) inviteJoin(ctx context.Context, ic *inviteChat, inv *inviteRow, agreed *time.Time) error {
	channel := ic.ch.Name()
	user, isNew := ic.user, ic.user == uuid.Nil
	var email, name string
	var tgID int64
	var tgName string
	team, slackUser, _ := parseSlackAddr(ic.addr)
	switch channel {
	case "whatsapp":
		email = ownerPlaceholderEmail(ic.addr)
	case "telegram":
		tgID, _ = telegram.ParseAddress(ic.addr)
		email = telegramPlaceholderEmail(tgID)
		if ic.tg != nil {
			tgName = ic.tg.Username
		}
	case "slack":
		email = slackPlaceholderEmail(team, slackUser)
		if out, err := s.slackCall(ctx, inv.Tenant, team, "get_user", map[string]any{"user": slackUser}); err == nil {
			if n, ok := plainName(strings.TrimSpace(asString(out["real_name"])), 1, 80); ok {
				name = n
			}
			if e, err := s.slackOwnerEmail(ctx, &slackSignup{team: team, user: slackUser, email: strings.ToLower(strings.TrimSpace(asString(out["email"])))}); err == nil {
				email = e
			}
		}
	}
	if isNew {
		// Someone a field agent or an earlier invitation made by this
		// chat is the same person.
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, email).Scan(&user)
		switch {
		case err == nil:
			isNew = false
		case errors.Is(err, pgx.ErrNoRows):
			user = uuid.Must(uuid.NewV7())
		default:
			return err
		}
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{inv.Tenant}, func(tx pgx.Tx) error {
		if isNew {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, user, email, name); err != nil {
				return err
			}
		}
		if ic.user == uuid.Nil {
			outcome := "ok"
			var err error
			switch channel {
			case "whatsapp":
				err = tx.QueryRow(ctx, `SELECT taskiem_chat_bind_whatsapp($1, $2)`, user, ic.addr).Scan(&outcome)
			case "telegram":
				err = tx.QueryRow(ctx, `SELECT taskiem_chat_bind_telegram($1, $2, $3)`, user, tgID, tgName).Scan(&outcome)
			}
			if err != nil {
				return err
			}
			if outcome != "ok" && outcome != "has" {
				return errSignupBind{outcome}
			}
		}
		if channel == "slack" {
			if _, err := tx.Exec(ctx, `INSERT INTO slack_bindings (tenant_id, user_id, team_id, slack_user_id, verified_at) VALUES ($1, $2, $3, $4, now())
				ON CONFLICT (tenant_id, user_id) DO UPDATE SET team_id = EXCLUDED.team_id, slack_user_id = EXCLUDED.slack_user_id, nonce_hash = NULL, verified_at = now()`,
				inv.Tenant, user, team, slackUser); err != nil {
				return err
			}
		}
		for _, role := range inv.Roles {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, inv.Tenant, user, role); err != nil {
				return err
			}
		}
		if agreed != nil {
			if err := s.termsAcceptTx(ctx, tx, inv.Tenant, user, channel, agreed); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE chat_invites SET state = 'joined', joined_user = $2, answered_at = now() WHERE id = $1 AND state = 'sent'`, inv.ID, user)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errInviteGone
		}
		raw, _ := json.Marshal(map[string]any{"channel": channel, "roles": inv.Roles, "invite": inv.ID})
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'member.join', $2, $3)`, inv.Tenant, user.String(), raw)
		return err
	})
	if be := (errSignupBind{}); errors.As(err, &be) {
		return ic.say(ctx, s, ic.t("invite.chat_taken"))
	}
	if errors.Is(err, errInviteGone) {
		return ic.say(ctx, s, ic.t("invite.gone"))
	}
	if err != nil {
		return err
	}
	toucher := ic.ch
	if channel == "slack" {
		toucher = &slackDMChannel{s: s}
	}
	if err := toucher.Touch(ctx, ic.addr, false, true, &inv.Tenant); err != nil {
		return err
	}
	return ic.say(ctx, s, ic.t("invite.joined", "org", inv.Business))
}

var errInviteGone = errors.New("invite: answered or expired")

// inviteStart answers a Telegram invitation's link: t.me/<bot>?start=inv_<token>.
func (s *Server) inviteStart(ctx context.Context, ic *inviteChat, token string) error {
	inv, err := s.inviteByID(ctx, uuid.Nil, chatLinkHash(token))
	if err != nil {
		return err
	}
	if inv == nil || inv.State != "sent" || inv.Channel != "telegram" {
		return ic.say(ctx, s, ic.t("invite.gone"))
	}
	_, err = ic.ch.Send(ctx, ic.addr, inviteOffer(ic.lang, inv))
	return err
}

// --- Slack: asking to join ---

// slackAskToJoin offers a member of a workspace that is some business's,
// and in none there, to ask to join; Ask to join tells the owners.
func (s *Server) slackAskToJoin(ctx context.Context, ic *inviteChat, reply string) error {
	team, _, _ := parseSlackAddr(ic.addr)
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, name, connection_id FROM taskiem_slack_team_businesses($1)`, team)
	if err != nil {
		return err
	}
	type biz struct {
		id   uuid.UUID
		name string
	}
	var list []biz
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (biz, error) {
		var b biz
		var conn uuid.UUID
		return b, r.Scan(&b.id, &b.name, &conn)
	})
	if err != nil {
		return err
	}
	for _, b := range all {
		if !slices.ContainsFunc(list, func(x biz) bool { return x.id == b.id }) && len(list) < 3 {
			list = append(list, b)
		}
	}
	if len(list) == 0 {
		return nil
	}
	ic.ch = &slackChannel{s: s, tenant: list[0].id}
	if t, ok := strings.CutPrefix(reply, invPrefix+"ask:"); ok {
		i := slices.IndexFunc(list, func(b biz) bool { return b.id.String() == t })
		if i < 0 {
			return nil
		}
		ic.ch = &slackChannel{s: s, tenant: list[i].id}
		return s.inviteAsk(ctx, ic, list[i].id, list[i].name)
	}
	if !s.limiter("sl-ask:"+ic.addr, 10*time.Minute, 2).Allow() {
		return nil
	}
	var names []string
	var buttons []whatsapp.Button
	for _, b := range list {
		names = append(names, b.name)
		buttons = append(buttons, whatsapp.Button{ID: invPrefix + "ask:" + b.id.String(), Title: ic.t("invite.button_ask", "org", b.name)})
	}
	return ic.say(ctx, s, ic.t("invite.ask_offer", "businesses", strings.Join(names, ", ")), buttons...)
}

// inviteAsk records a request to join and tells the business's owners,
// on the chat they linked, with Approve as a role or Decline.
func (s *Server) inviteAsk(ctx context.Context, ic *inviteChat, tenant uuid.UUID, business string) error {
	team, slackUser, _ := parseSlackAddr(ic.addr)
	if !s.limiter("sl-asked:"+ic.addr+":"+tenant.String(), time.Hour, 1).Allow() {
		return ic.say(ctx, s, ic.t("invite.asked", "org", business))
	}
	asker := "@" + slackUser
	if out, err := s.slackCall(ctx, tenant, team, "get_user", map[string]any{"user": slackUser}); err == nil {
		if n, ok := plainName(strings.TrimSpace(asString(out["real_name"])), 1, 80); ok {
			asker = n
		}
	}
	id := uuid.Must(uuid.NewV7())
	type to struct {
		channel, addr string
	}
	var owners []to
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_invites WHERE channel = 'slack' AND address = $1 AND state = 'asked' AND expires_at > now())`, ic.addr).Scan(&pending); err != nil || pending {
			if pending {
				return errInviteAsked
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_invites (id, tenant_id, channel, address, state, inviter, expires_at) VALUES ($1, $2, 'slack', $3, 'asked', $4, now() + $5::interval)`,
			id, tenant, ic.addr, asker, chatInviteTTL.String()); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]any{"channel": "slack"})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'slack', 'member.ask', $2, $3)`, tenant, ic.addr, raw); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM memberships WHERE role = 'owner'`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, channel := range []string{"slack", "whatsapp", "telegram"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, ids)
			if err != nil {
				return err
			}
			for _, u := range ids {
				if a, ok := addrs[u]; ok && !slices.ContainsFunc(owners, func(o to) bool { return o.addr == a }) {
					owners = append(owners, to{channel, a})
				}
			}
		}
		return nil
	})
	if errors.Is(err, errInviteAsked) {
		return ic.say(ctx, s, ic.t("invite.asked", "org", business))
	}
	if err != nil {
		return err
	}
	for _, o := range owners {
		ch, err := s.chatChannelFor(ctx, o.channel, tenant)
		if err != nil {
			continue
		}
		l := s.chooseLang(ctx, tenant, o.addr, "").Tag
		if err := s.waSay(ctx, ch, o.addr, "", tr(l, "invite.ask_owner", "who", asker, "org", business),
			whatsapp.Button{ID: invPrefix + "ok:" + id.String() + ":viewer", Title: tr(l, "invite.button_viewer")},
			whatsapp.Button{ID: invPrefix + "ok:" + id.String() + ":operator", Title: tr(l, "invite.button_operator")},
			whatsapp.Button{ID: invPrefix + "deny:" + id.String(), Title: tr(l, "invite.button_decline")}); err != nil {
			s.Logger.Warn("invite: telling an owner of a request", "err", err)
		}
	}
	return ic.say(ctx, s, ic.t("invite.asked", "org", business))
}

var errInviteAsked = errors.New("invite: asked already")

// inviteDecide is an owner's Approve (with a role) or Decline of a
// request to join: Approve sends the person the invitation.
func (s *Server) inviteDecide(ctx context.Context, ic *inviteChat, action, rest string) error {
	idText, role, _ := strings.Cut(rest, ":")
	id, err := uuid.Parse(idText)
	if err != nil || ic.user == uuid.Nil {
		return ic.say(ctx, s, ic.t("invite.gone"))
	}
	inv, err := s.inviteByID(ctx, id, nil)
	if err != nil {
		return err
	}
	if inv == nil || inv.State != "asked" {
		return ic.say(ctx, s, ic.t("invite.gone"))
	}
	// A request keeps the asker's name where an invitation keeps the
	// inviter's.
	asked := inv.Inviter
	p, err := s.principalOf(ctx, inv.Tenant, ic.user)
	if err != nil || !p.Can(PermMemberManage) {
		return ic.say(ctx, s, ic.t("invite.denied", "org", inv.Business))
	}
	var inviter string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{inv.Tenant}, func(tx pgx.Tx) error {
		if action == "deny" {
			_, err := tx.Exec(ctx, `UPDATE chat_invites SET state = 'declined', answered_at = now() WHERE id = $1 AND state = 'asked'`, id)
			return err
		}
		if err := canGrant(ctx, tx, p, []string{role}); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, ic.user).Scan(&inviter); err != nil {
			return err
		}
		if inviter = clip(strings.TrimSpace(inviter), 80); inviter == "" {
			inviter = inv.Business
		}
		if _, err := tx.Exec(ctx, `UPDATE chat_invites SET state = 'sent', roles = $2, invited_by = $3, inviter = $4, expires_at = now() + $5::interval WHERE id = $1 AND state = 'asked'`,
			id, []string{role}, ic.user, inviter, chatInviteTTL.String()); err != nil {
			return err
		}
		return waAudit(ctx, tx, ic.ch.Name(), p, "member.invite", inv.Address, map[string]any{"roles": []string{role}, "via": "ask_to_join"})
	})
	if errors.Is(err, errForbidden) {
		return ic.say(ctx, s, ic.t("invite.cannot_grant", "role", role))
	}
	if err != nil {
		return err
	}
	asker := &slackChannel{s: s, tenant: inv.Tenant}
	l := s.chooseLang(ctx, uuid.Nil, inv.Address, "").Tag
	if action == "deny" {
		if err := s.waSay(ctx, asker, inv.Address, "", tr(l, "invite.ask_declined", "org", inv.Business)); err != nil {
			s.Logger.Warn("invite: telling a declined request", "err", err)
		}
		return ic.say(ctx, s, ic.t("invite.decided_no", "who", asked))
	}
	inv.Roles, inv.Inviter = []string{role}, inviter
	if _, err := asker.Send(ctx, inv.Address, inviteOffer(l, inv)); err != nil {
		s.Logger.Warn("invite: sending an approved request", "err", err)
	}
	return ic.say(ctx, s, ic.t("invite.decided_yes", "who", asked, "role", role))
}
