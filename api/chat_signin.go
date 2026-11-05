package api

// Signing in on the web with the chat (docs/spec/chat-sign-in.md): a
// person whose WhatsApp or Telegram is bound to their account (a business
// that signed up in chat, or anyone who linked one) can sign in with a
// six-digit code sent to that chat from the login page, or with a
// one-time link "sign in" sends in chat. Both prove what signing up in
// chat proved: the person holds that chat. They meet the same rules as a
// password: an organisation's single sign-on, and an administrator's
// passkey where the deployment requires one. A person may turn it off
// for themselves (Account). Every chat sign-in is told to the person's
// chat and, when they have one, their email; "sign out everywhere" ends
// every web session of theirs.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	chatCodeTTL          = 10 * time.Minute
	chatCodeMaxSends     = 5 // an hour, per person
	chatCodeMaxFailures  = 5
	signinLinkTTL        = 10 * time.Minute
	signinLinkMaxPerHour = 3
)

// chatSigninSent is the start's only answer, known number or not.
const chatSigninSent = "If this number is on a Taskiem account, we have sent a code to it on WhatsApp or Telegram."

// chatCodeWrong is a wrong, expired or locked code's only answer.
const chatCodeWrong = "That code is not right, or it has expired. Ask for a new one."

// errChatSigninOff refuses a chat sign-in the person turned off, or whose
// account is not active.
var errChatSigninOff = errors.New("signing in with WhatsApp or Telegram is off for this account")

// chatSigninStart sends a code to the chat of the person a number signs
// in (POST /v1/auth/chat/start, {phone}). The answer is the same whether
// or not the number is on an account, and comes before anything is
// looked up: the look-up and the send happen after it.
func (s *Server) chatSigninStart(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.limiter("chat-signin:"+clientIP(r), time.Hour, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, err := whatsapp.Normalise(req.Phone, s.defaultCountry())
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: give the phone number of your WhatsApp or Telegram", errBadRequest))
		return
	}
	if !s.limiter("chat-signin-number:"+number, time.Hour, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	ip := clientIP(r)
	s.background(r.Context(), "chat sign-in code", func(ctx context.Context) {
		if err := s.chatSigninSend(ctx, number, ip); err != nil {
			s.Logger.Warn("chat sign-in: a code not sent", "err", err)
		}
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": chatSigninSent, "expires_in": int(chatCodeTTL.Seconds())})
}

// chatSigninSend issues and sends the code, for a number on an account
// that may sign in with its chat; nothing for any other.
func (s *Server) chatSigninSend(ctx context.Context, number, ip string) error {
	var user uuid.UUID
	var status string
	var on, wa bool
	var tg *int64
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, status, chat_signin, whatsapp, telegram_id FROM taskiem_auth_chat_user($1)`, number).Scan(&user, &status, &on, &wa, &tg)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "active" || !on {
		return nil
	}
	tenants, err := s.userTenants(ctx, user)
	if err != nil || len(tenants) == 0 {
		return err
	}
	if ok, err := s.passwordUsable(ctx, user); err != nil || !ok {
		return err // single sign-on everywhere: a code would be no use
	}
	if need, err := s.adminPasskeyRequired(ctx, user); err != nil || need {
		return err
	}
	channel := ""
	switch {
	case wa && s.WhatsApp != nil:
		channel = "whatsapp"
	case tg != nil && s.Telegram != nil:
		channel = "telegram"
	default:
		return nil
	}
	code := otpCode()
	var outcome string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_chat_code_issue($1, $2, $3, $4, $5::interval, $6)`,
		user, number, channel, code, chatCodeTTL.String(), chatCodeMaxSends).Scan(&outcome); err != nil {
		return err
	}
	if outcome != "ok" {
		return nil
	}
	// From Taskiem's own number: the code is for the person's account, not a
	// business. An authentication template, whatever the window; on
	// Telegram a message, which is kept in no history.
	switch channel {
	case "whatsapp":
		if _, err := s.WhatsApp.SendTemplate(ctx, tenants[0], number, whatsapp.TplOTP, map[string]string{"code": code}, nil); err != nil {
			return err
		}
	case "telegram":
		addr := fmt.Sprintf("tg:%d", *tg)
		l := s.chooseLang(ctx, tenants[0], addr, "").Tag
		if _, err := s.Telegram.Client.SendMessage(ctx, *tg, tr(l, "chat.signin.code", "code", code), nil); err != nil {
			return err
		}
	}
	return db.InTenantTx(ctx, s.Store.Pool, tenants, func(tx pgx.Tx) error {
		for _, t := range tenants {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.chat_signin_code', $3, jsonb_build_object('channel', $4::text, 'number', $5::text, 'ip', $6::text))`,
				t, user.String(), user.String(), channel, whatsapp.MaskNumber(number), ip); err != nil {
				return err
			}
		}
		return nil
	})
}

// chatSigninVerify signs in with the code (POST /v1/auth/chat/verify,
// {phone, code, tenant_id?, bearer?}).
func (s *Server) chatSigninVerify(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("chat-verify:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Phone    string    `json:"phone"`
		Code     string    `json:"code"`
		TenantID uuid.UUID `json:"tenant_id"`
		Bearer   bool      `json:"bearer"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, err := whatsapp.Normalise(req.Phone, s.defaultCountry())
	if err != nil {
		writeErr(w, http.StatusUnauthorized, chatCodeWrong)
		return
	}
	if !s.loginLimiter("chat-verify-number:" + number).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var outcome string
	var user *uuid.UUID
	var channel *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT outcome, user_id, channel FROM taskiem_auth_chat_code_check($1, $2, $3)`,
		number, strings.TrimSpace(req.Code), chatCodeMaxFailures).Scan(&outcome, &user, &channel); err != nil {
		s.fail(w, r, err)
		return
	}
	if outcome != "ok" || user == nil {
		writeErr(w, http.StatusUnauthorized, chatCodeWrong)
		return
	}
	s.chatSigninFinish(w, r, *user, *channel, "code", req.TenantID, req.Bearer)
}

// chatSigninLinkPeek says whether a sign-in link from chat is still good,
// without using it (POST /v1/auth/chat/link/peek, {token}): 204, or 404.
// It says nothing about whose it is.
func (s *Server) chatSigninLinkPeek(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("chat-link:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var ok bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_chat_link_peek($1)`, hashToken(strings.TrimSpace(req.Token))).Scan(&ok); err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "this link has been used or has expired; send *sign in* in chat for a new one")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// chatSigninLink signs in with a link from chat (POST /v1/auth/chat/link,
// {token, tenant_id?, bearer?}): only this POST, from the page's button,
// uses the token, so a chat app or browser fetching the link ahead of
// time signs no one in.
func (s *Server) chatSigninLink(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("chat-link:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Token    string    `json:"token"`
		TenantID uuid.UUID `json:"tenant_id"`
		Bearer   bool      `json:"bearer"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var user uuid.UUID
	var channel string
	err := s.Store.Pool.QueryRow(r.Context(), `SELECT user_id, channel FROM taskiem_auth_chat_link_use($1)`, hashToken(strings.TrimSpace(req.Token))).Scan(&user, &channel)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "this link has been used or has expired; send *sign in* in chat for a new one")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.chatSigninFinish(w, r, user, channel, "link", req.TenantID, req.Bearer)
}

// chatSigninFinish starts the session of a person whose chat proved them,
// under the rules a password meets, and tells them of it.
func (s *Server) chatSigninFinish(w http.ResponseWriter, r *http.Request, user uuid.UUID, channel, how string, want uuid.UUID, bearer bool) {
	ctx := r.Context()
	var status string
	var on bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT status, chat_signin FROM taskiem_auth_chat_state($1)`, user).Scan(&status, &on); err != nil {
		s.fail(w, r, err)
		return
	}
	if status != "active" || !on {
		writeErr(w, http.StatusUnauthorized, errChatSigninOff.Error())
		return
	}
	if need, err := s.adminPasskeyRequired(ctx, user); err != nil {
		s.fail(w, r, err)
		return
	} else if need {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "administrators sign in with their passkey", "passkey_required": true})
		return
	}
	sess, err := s.createSession(r, user, want, "chat")
	switch {
	case errors.Is(err, errSSORequired):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error(), "sso_required": true})
		return
	case errors.Is(err, errNoMembership), errors.Is(err, errNotMember):
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{sess.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.chat_signin', $3, jsonb_build_object('channel', $4::text, 'how', $5::text, 'ip', $6::text))`,
			sess.tenant, user.String(), user.String(), channel, how, clientIP(r))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	device := uaSummary(r.UserAgent())
	tenant := sess.tenant
	s.background(ctx, "chat sign-in told", func(ctx context.Context) { s.tellChatSignin(ctx, user, tenant, device) })
	out := map[string]any{"tenant_id": sess.tenant, "tenants": sess.tenants, "user_id": user}
	s.deliverSession(w, bearer, sess.token, out)
	writeJSON(w, http.StatusOK, out)
}

// tellChatSignin tells the person a chat sign-in happened: on their chat
// apps (outside WhatsApp's window, as the alert template) and to their
// email when they have a confirmed one.
func (s *Server) tellChatSignin(ctx context.Context, user, tenant uuid.UUID, device string) {
	type to struct{ channel, addr string }
	var sends []to
	var email string
	var verified bool
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
		s.Logger.Warn("chat sign-in: telling the person", "err", err)
		return
	}
	account := strings.TrimRight(s.PublicURL, "/") + "/account"
	for _, t := range sends {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		l := s.chooseLang(ctx, tenant, t.addr, "").Tag
		text := tr(l, "chat.signin.told", "device", device)
		tpl := whatsapp.TplAlert
		msg := whatsapp.Message{Tenant: tenant, Text: text, Template: &tpl, Vars: map[string]string{"tenant": "Taskiem", "title": text, "link": account}}
		if _, err := ch.Send(ctx, t.addr, msg); err != nil {
			s.Logger.Warn("chat sign-in: telling the chat", "channel", t.channel, "err", err)
		}
	}
	if verified && !emailPlaceholder(email) && s.mailOn() {
		body := fmt.Sprintf(`You signed in to Taskiem on the web with WhatsApp or Telegram, on %s, at %s.

If this was not you, send "sign out everywhere" to Taskiem in your chat, or sign out every session from your Account page:

%s
`, device, time.Now().UTC().Format("2 January 2006, 15:04 UTC"), account)
		if err := s.sendMail(ctx, email, "New sign-in to Taskiem", body); err != nil {
			s.Logger.Warn("chat sign-in: the email", "err", err)
		}
	}
}

// uaSummary names a browser and its system from a User-Agent, for telling
// a person where they signed in: "Chrome on Windows".
func uaSummary(ua string) string {
	browser := ""
	for _, b := range []struct{ token, name string }{{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	for _, o := range []struct{ token, name string }{{"Android", "Android"}, {"iPhone", "an iPhone"}, {"iPad", "an iPad"}, {"Windows", "Windows"}, {"Mac OS X", "a Mac"}, {"CrOS", "a Chromebook"}, {"Linux", "Linux"}} {
		if strings.Contains(ua, o.token) {
			system = o.name
			break
		}
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	return "a browser"
}

// userTenants is the businesses a person signs in to.
func (s *Server) userTenants(ctx context.Context, user uuid.UUID) ([]uuid.UUID, error) {
	var tenants []uuid.UUID
	err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&tenants)
	return tenants, err
}

// adminPasskeyRequired says the person is an administrator somewhere and
// has a passkey, so signs in with it (where the deployment requires
// administrators' passkeys), whichever business they ask for.
func (s *Server) adminPasskeyRequired(ctx context.Context, user uuid.UUID) (bool, error) {
	if !s.RequireAdminPasskeys {
		return false, nil
	}
	var has bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_has_passkey($1)`, user).Scan(&has); err != nil || !has {
		return false, err
	}
	tenants, err := s.userTenants(ctx, user)
	if err != nil {
		return false, err
	}
	for _, t := range tenants {
		p := Principal{TenantID: t, UserID: user, Permissions: map[string]bool{}}
		if err := s.loadRoles(ctx, &p); err != nil {
			return false, err
		}
		if isAdmin(p.Permissions) {
			return true, nil
		}
	}
	return false, nil
}

// setChatSignin turns chat sign-in on or off for the person
// (PUT /v1/me/chat-signin, {on}).
func (s *Server) setChatSignin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "chat sign-in belongs to a person, not an API key")
		return
	}
	var req struct {
		On *bool `json:"on"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.On == nil {
		s.fail(w, r, fmt.Errorf("%w: on is true or false", errBadRequest))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET chat_signin = $2 WHERE id = $1`, p.UserID, *req.On); err != nil {
			return err
		}
		return auditTx(r, tx, "auth.chat_signin_setting", p.UserID.String(), map[string]any{"on": *req.On})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat_signin": *req.On})
}

// --- in chat ---

// waSignInLink answers "sign in" in chat: a one-time link, good for ten
// minutes, that opens the web app signed in. The chat is already bound to
// the person, so the link goes only where they are. Who signs in another
// way is told that way: single sign-on, an administrator's passkey, or a
// password when they turned chat sign-in off.
func (s *Server) waSignInLink(ctx context.Context, c *chat) error {
	if s.PublicURL == "" {
		return c.say(ctx, s, c.t("wa.signin.unavailable"))
	}
	if ok, err := s.passwordUsable(ctx, c.p.UserID); err != nil || !ok {
		if err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.signin.sso", "link", strings.TrimRight(s.PublicURL, "/")+"/login"))
	}
	var status string
	var on bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT status, chat_signin FROM taskiem_auth_chat_state($1)`, c.p.UserID).Scan(&status, &on); err != nil {
		return err
	}
	if !on {
		return c.say(ctx, s, c.t("wa.signin.off"))
	}
	if need, err := s.adminPasskeyRequired(ctx, c.p.UserID); err != nil {
		return err
	} else if need {
		return c.say(ctx, s, c.t("wa.signin.passkey", "link", strings.TrimRight(s.PublicURL, "/")+"/login"))
	}
	if !s.limiter("wa-signin:"+c.p.UserID.String(), 10*time.Minute, 3).Allow() {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	token := newToken()
	var issued bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_chat_link_issue($1, $2, $3, $4::interval, $5)`,
		c.p.UserID, hashToken(token), c.ch.Name(), signinLinkTTL.String(), signinLinkMaxPerHour).Scan(&issued); err != nil {
		return err
	}
	if !issued {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "auth.signin_link", c.p.UserID.String(), nil)
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.signin.chat_link", "link", strings.TrimRight(s.PublicURL, "/")+"/signin#token="+token))
}

// waPasswordLink answers "password" in chat: a one-time link, good for an
// hour, to choose a password, naming the email to sign in with. Without
// an email there is nothing to sign in with, so it asks for one first.
func (s *Server) waPasswordLink(ctx context.Context, c *chat) error {
	if s.PublicURL == "" {
		return c.say(ctx, s, c.t("wa.signin.unavailable"))
	}
	if ok, err := s.passwordUsable(ctx, c.p.UserID); err != nil || !ok {
		if err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.signin.sso", "link", strings.TrimRight(s.PublicURL, "/")+"/login"))
	}
	var email string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, c.p.UserID).Scan(&email)
	}); err != nil {
		return err
	}
	if emailPlaceholder(email) {
		return c.say(ctx, s, c.t("door.signin_email"))
	}
	if !s.limiter("wa-password:"+c.p.UserID.String(), 10*time.Minute, 3).Allow() {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	token, selector, secretHash := newResetToken()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_password_reset_issue($1, $2, $3, $4::interval, $5)`, c.p.UserID, selector, secretHash, time.Hour.String(), c.ch.Name()); err != nil {
		return err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "auth.password_link", c.p.UserID.String(), nil)
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.signin.link", "domain", emailDomain(email), "link", strings.TrimRight(s.PublicURL, "/")+"/reset-password#token="+token))
}

// emailDomain is the part of an address chat may show: chat never
// carries an address, so the reply names where it is ("salon.test").
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return whatsapp.SafeText(email[at+1:])
}

// waSignOutEverywhere answers "sign out everywhere": every web session of
// the person's ends, in every business; chat is not a session and goes on.
func (s *Server) waSignOutEverywhere(ctx context.Context, c *chat) error {
	tenants, err := s.userTenants(ctx, c.p.UserID)
	if err != nil {
		return err
	}
	var n int64
	if err := db.InTenantTx(ctx, s.Store.Pool, tenants, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, c.p.UserID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return waAudit(ctx, tx, c.ch.Name(), c.p, "auth.signout_everywhere", c.p.UserID.String(), map[string]any{"sessions": n})
	}); err != nil {
		return err
	}
	if n == 0 {
		return c.say(ctx, s, c.t("chat.signout.none"))
	}
	return c.say(ctx, s, c.t("chat.signout.done", "count", fmt.Sprint(n)))
}
