package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// One person, many doors (docs/spec/chat-way-in.md 10, decision 0058).
// Someone who uses Taskiem on the web links a chat from the chat itself:
// "I already use Taskiem" asks for the email they sign in with and emails
// that account a one-time link (24 bytes, an hour, once), with the same
// answer whether or not the email has an account. Opening it signed in
// to that account binds the chat to it. A Telegram sign-up whose shared
// number is a chat-born WhatsApp person's joins that person; otherwise the
// account's email gets the link. A chat-born owner adds a real email
// (Account, or "email" in chat) by opening its confirmation link, which
// replaces the placeholder sign-in name. And "plan" in chat says the
// trial's days and the price, with Pay.

const chatLinkTTL = time.Hour

// suEmail is the sign-up step that waits for the email of an account to
// link the chat to.
const suEmail = "email"

func newChatLinkToken() (token string, hash []byte) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, chatLinkHash(token)
}

func chatLinkHash(token string) []byte {
	h := sha256.Sum256([]byte("chat-link:" + token))
	return h[:]
}

// chatLinkAsk answers "I already use Taskiem": the email they sign in
// with, when this deployment can send one; else the web's way.
func (s *Server) chatLinkAsk(ctx context.Context, sc *signupChat) error {
	if !s.mailOn() {
		if sc.ch.Name() == "telegram" {
			return s.suSay(ctx, sc, sc.t("tg.unbound", "where", s.accountWhere()))
		}
		return s.suSay(ctx, sc, sc.t("wa.unbound", "where", s.accountWhere()))
	}
	if err := s.signupSave(ctx, sc, &signupState{Step: suEmail}); err != nil {
		return err
	}
	return s.suSay(ctx, sc, sc.t("door.ask_email"))
}

// chatLinkEmail takes the email: a link goes to it when it is an
// account's, and the answer is the same either way.
func (s *Server) chatLinkEmail(ctx context.Context, sc *signupChat, text string) error {
	email, _, ok := signupEmail(text)
	if !ok {
		return s.suSay(ctx, sc, sc.t("door.email_bad"))
	}
	email = strings.ToLower(email)
	if err := s.signupDrop(ctx, sc.addr); err != nil {
		return err
	}
	if !s.limiter("chat-link:"+sc.addr, time.Hour, 3).Allow() || !s.limiter("chat-link-email:"+email, time.Hour, 3).Allow() {
		return s.suSay(ctx, sc, sc.t("door.too_many"))
	}
	var user uuid.UUID
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, email).Scan(&user)
	switch {
	case err == nil:
		channel, address, username := chatLinkAddress(sc)
		if err := s.chatLinkIssue(ctx, user, email, channel, address, username); err != nil {
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	return s.suSay(ctx, sc, sc.t("door.email_sent", "email", email))
}

// chatLinkAddress is what a link binds: the WhatsApp number, the Telegram
// account (and its username), or the Slack user.
func chatLinkAddress(sc *signupChat) (channel, address, username string) {
	channel, address = sc.ch.Name(), sc.addr
	if channel == "telegram" {
		if sc.tg != nil {
			return channel, strconv.FormatInt(sc.tg.FromID, 10), sc.tg.Username
		}
		if id, ok := telegram.ParseAddress(sc.addr); ok {
			return channel, strconv.FormatInt(id, 10), ""
		}
	}
	return channel, address, ""
}

// chatLinkIssue keeps a link for an account and emails it.
func (s *Server) chatLinkIssue(ctx context.Context, user uuid.UUID, email, channel, address, username string) error {
	token, hash := newChatLinkToken()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_link_put($1, $2, $3, $4, $5, $6::interval)`, hash, user, channel, address, username, chatLinkTTL.String()); err != nil {
		return err
	}
	what := map[string]string{"whatsapp": "WhatsApp (" + whatsapp.MaskNumber(address) + ")", "telegram": "Telegram", "slack": "Slack"}[channel]
	if channel == "telegram" && username != "" {
		what = "Telegram (@" + username + ")"
	}
	link := strings.TrimRight(s.PublicURL, "/") + "/link-chat#token=" + token
	body := fmt.Sprintf(`Someone asked, from %s, to link that chat to your Taskiem account.

If it was you, open this link while signed in to Taskiem. It works once, for an hour:

%s

If it was not you, ignore this email: nothing changes.
`, what, link)
	s.background(ctx, "chat link email", func(ctx context.Context) {
		if err := s.sendMail(ctx, email, "Link your chat to Taskiem", body); err != nil {
			s.Logger.Error("chat link email not sent", "err", err)
		}
	})
	return nil
}

// linkChat binds the chat a link names to the signed-in person: POST
// /v1/me/chat-link.
func (s *Server) linkChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "a chat belongs to a person, not an API key")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.limiter("chat-link-open:"+p.UserID.String(), time.Minute, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	ctx := r.Context()
	var channel, address string
	var username *string
	err := s.Store.Pool.QueryRow(ctx, `SELECT channel, address, username FROM taskiem_chat_link_take($1, $2)`, chatLinkHash(req.Token), p.UserID).Scan(&channel, &address, &username)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, fmt.Errorf("%w: this link is unknown, already used, expired, or for another account; ask again from the chat", errBadRequest))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tell := address
	var tenant uuid.UUID
	switch channel {
	case "slack":
		team, user, ok := parseSlackAddr(address)
		if !ok {
			s.fail(w, r, fmt.Errorf("%w: this link is not valid", errBadRequest))
			return
		}
		tenant = p.TenantID
		err = s.tx(r, func(tx pgx.Tx) error {
			var reaches bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connection_app_accounts WHERE connector = 'slack' AND account_key = $1)`, team).Scan(&reaches); err != nil {
				return err
			}
			if !reaches {
				return fmt.Errorf("%w: this business has not connected that Slack workspace; sign in to the business that has, and open the link again", errConflict)
			}
			_, err := tx.Exec(ctx, `INSERT INTO slack_bindings (tenant_id, user_id, team_id, slack_user_id, verified_at) VALUES ($1, $2, $3, $4, now())
				ON CONFLICT (tenant_id, user_id) DO UPDATE SET team_id = EXCLUDED.team_id, slack_user_id = EXCLUDED.slack_user_id, nonce_hash = NULL,
				requested_at = now(), verified_at = now()`, p.TenantID, p.UserID, team, user)
			return err
		})
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			err = fmt.Errorf("%w: that Slack account is linked to someone else here", errConflict)
		}
	default:
		var outcome string
		if err = s.Store.Pool.QueryRow(ctx, `SELECT taskiem_chat_link_bind($1, $2, $3, $4)`, p.UserID, channel, address, deref(username)).Scan(&outcome); err == nil {
			switch outcome {
			case "taken":
				err = fmt.Errorf("%w: that chat is linked to another Taskiem account", errConflict)
			case "has":
				err = fmt.Errorf("%w: your account has another %s linked; unlink it on your profile first, then open the link again", errConflict, chatChannelWord(channel))
			}
		}
		if channel == "telegram" {
			id, _ := strconv.ParseInt(address, 10, 64)
			tell = telegram.Address(id)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auditEverywhere(ctx, p.UserID, "auth.link", map[string]any{"channel": channel, "via": "email_link", "ip": clientIP(r)}); err != nil {
		s.Logger.Warn("chat link: audit", "err", err)
	}
	if ch := s.chatLinkedChannel(ctx, channel, tenant); ch != nil {
		s.background(ctx, "chat linked", func(ctx context.Context) {
			if err := s.waSay(ctx, ch, tell, "", tr(s.chooseLang(ctx, uuid.Nil, tell, "").Tag, "door.linked")); err != nil {
				s.Logger.Warn("chat link: telling the chat", "err", err)
			}
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": channel, "linked": true})
}

func chatChannelWord(channel string) string {
	return map[string]string{"whatsapp": "WhatsApp number", "telegram": "Telegram account", "slack": "Slack account"}[channel]
}

// chatLinkedChannel is where a person who linked a chat is told: the
// shared number they wrote to, the bot, or the business's Slack.
func (s *Server) chatLinkedChannel(ctx context.Context, channel string, tenant uuid.UUID) chatChannel {
	if channel == "whatsapp" {
		if s.WhatsApp == nil {
			return nil
		}
		return s.WhatsApp
	}
	ch, err := s.chatChannelFor(ctx, channel, tenant)
	if err != nil {
		return nil
	}
	return ch
}

// chatNumberDoor answers a Telegram sign-up whose shared number is a
// WhatsApp-bound person's: one person, many doors. A chat-born account
// (the number was its only proof, and Telegram's own-contact check is the
// same proof) gets this Telegram account too; any other is asked to
// prove its email, by a link sent there. false: no such person.
func (s *Server) chatNumberDoor(ctx context.Context, sc *signupChat, st *signupState, number string) (bool, error) {
	var owner uuid.UUID
	var chatBorn bool
	var email string
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id, chat_born, email FROM taskiem_chat_number_owner($1)`, number).Scan(&owner, &chatBorn, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if err := s.signupDrop(ctx, sc.addr); err != nil {
		return true, err
	}
	say := func(text string) error { return s.Telegram.Client.SendClearing(ctx, sc.tg.FromID, text) }
	if !chatBorn {
		signupCount(sc.ch.Name(), "exists")
		if s.mailOn() && !strings.HasSuffix(email, ".taskiem.invalid") {
			if err := s.chatLinkIssue(ctx, owner, email, "telegram", strconv.FormatInt(sc.tg.FromID, 10), sc.tg.Username); err != nil {
				return true, err
			}
			return true, say(sc.t("door.tg_number_email"))
		}
		return true, say(sc.t("signup.number_taken", "where", s.accountWhere()))
	}
	var outcome string
	err = pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_chat_bind_telegram($1, $2, $3)`, owner, sc.tg.FromID, sc.tg.Username).Scan(&outcome); err != nil || outcome != "ok" {
			return err
		}
		return s.termsAcceptTx(ctx, tx, uuid.Nil, owner, "telegram", st.Agreed)
	})
	if err != nil {
		return true, err
	}
	if outcome != "ok" {
		signupCount(sc.ch.Name(), "exists")
		return true, say(sc.t("signup.number_taken", "where", s.accountWhere()))
	}
	if err := s.auditEverywhere(ctx, owner, "telegram.bind", map[string]any{"channel": "telegram", "via": "shared_number", "number": whatsapp.MaskNumber(number)}); err != nil {
		s.Logger.Warn("chat sign-up: audit", "err", err)
	}
	signupCount(sc.ch.Name(), "linked")
	return true, say(sc.t("door.tg_joined"))
}

// --- an email of one's own ---

// emailPlaceholder reports whether a sign-in name receives no mail.
func emailPlaceholder(email string) bool { return strings.HasSuffix(email, ".taskiem.invalid") }

// issueEmailChange keeps a link that confirms email and makes it the
// person's, and returns the address the browser opens.
func (s *Server) issueEmailChange(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, email string) (string, error) {
	token, err := issueVerificationFor(ctx, tx, tenant, user, email, true)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s.PublicURL, "/") + "/confirm-email#" + url.Values{"t": {tenant.String()}, "token": {token}}.Encode(), nil
}

func (s *Server) sendEmailChange(ctx context.Context, email, link string) {
	body := fmt.Sprintf(`You asked to use %s for your Taskiem account.

Open this link to confirm it. It works once, for 24 hours:

%s

Until then, your account keeps the sign-in it has. If you did not ask for this, ignore this email.
`, email, link)
	if err := s.sendMail(ctx, email, "Confirm your email for Taskiem", body); err != nil {
		s.Logger.Error("email change not sent", "err", err)
	}
}

// emailAddable says why a person cannot add an email ("" when they can):
// one already confirmed is changed on the web, not replaced from a link.
func emailAddable(current string, verified bool) bool {
	return emailPlaceholder(current) || !verified
}

// addEmail sends a confirmation link to an email the signed-in person
// wants for their account: POST /v1/me/email.
func (s *Server) addEmail(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "emails belong to people, not API keys")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.mailOn() {
		writeErr(w, http.StatusServiceUnavailable, "this deployment cannot send email; ask your operator")
		return
	}
	email, _, ok := signupEmail(req.Email)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: that is not an email address Taskiem can use", errBadRequest))
		return
	}
	if !s.limiter("email-add:"+p.UserID.String(), 20*time.Minute, 3).Allow() {
		writeErr(w, http.StatusTooManyRequests, "a link was sent a moment ago: check your inbox, or try again in a while")
		return
	}
	var link string
	err := s.tx(r, func(tx pgx.Tx) error {
		var current string
		var verified bool
		if err := tx.QueryRow(r.Context(), `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).Scan(&current, &verified); err != nil {
			return err
		}
		if !emailAddable(current, verified) {
			return fmt.Errorf("%w: your account's email is confirmed already", errConflict)
		}
		var err error
		link, err = s.issueEmailChange(r.Context(), tx, p.TenantID, p.UserID, email)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.background(r.Context(), "email change", func(ctx context.Context) { s.sendEmailChange(ctx, email, link) })
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "A link is on its way to " + email + ". Open it to confirm; it works once, for 24 hours."})
}

// confirmEmailChange confirms an added email from its link, signed in or
// not (a chat-born owner has no other way to sign in): POST
// /v1/auth/email/confirm. The link alone is the proof: it reached the
// mailbox, and the person asked for it from their account.
func (s *Server) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tenant string `json:"tenant"`
		Token  string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	tenant, err := uuid.Parse(req.Tenant)
	selector, secretHash, ok := parseResetToken(req.Token)
	if err != nil || !ok {
		writeErr(w, http.StatusBadRequest, verifyLinkBadMsg)
		return
	}
	if !s.limiter("email-confirm:"+clientIP(r), time.Minute, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	ctx := r.Context()
	var email string
	var wrong bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var user uuid.UUID
		var hash []byte
		var usable bool
		err := tx.QueryRow(ctx, `SELECT user_id, secret_hash, email, change AND used_at IS NULL AND expires_at > now() AND failures < 5
			FROM email_verifications WHERE selector = $1 FOR UPDATE`, selector).Scan(&user, &hash, &email, &usable)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !usable) {
			return fmt.Errorf("%w: %s", errBadRequest, verifyLinkBadMsg)
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(hash, secretHash) != 1 {
			// Counted, and committed: five lock the link.
			wrong = true
			_, err := tx.Exec(ctx, `UPDATE email_verifications SET failures = failures + 1 WHERE selector = $1`, selector)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE email_verifications SET used_at = now() WHERE selector = $1`, selector); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET email = $2, email_verified_at = now() WHERE id = $1 AND (email LIKE '%.taskiem.invalid' OR email_verified_at IS NULL)`, user, email)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: your account's email is confirmed already", errConflict)
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'user.email.change', $2, '{}')`, tenant, user.String())
		return err
	})
	var pgErr interface{ SQLState() string }
	switch {
	case err == nil && wrong:
		writeErr(w, http.StatusBadRequest, verifyLinkBadMsg)
	case errors.As(err, &pgErr) && pgErr.SQLState() == "23505":
		s.fail(w, r, fmt.Errorf("%w: %s belongs to another Taskiem account; use another email", errConflict, email))
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"email": email, "email_verified": true})
	}
}

// waEmail answers "email" in chat: the account's email, or a link to
// confirm a new one.
func (s *Server) waEmail(ctx context.Context, c *chat, arg string) error {
	var current string
	var verified bool
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, c.p.UserID).Scan(&current, &verified)
	}); err != nil {
		return err
	}
	if !emailAddable(current, verified) {
		return c.say(ctx, s, c.t("door.email_is", "email", current))
	}
	if strings.TrimSpace(arg) == "" {
		return c.say(ctx, s, c.t("door.email_ask"))
	}
	if !s.mailOn() {
		return c.say(ctx, s, c.t("door.email_off"))
	}
	email, _, ok := signupEmail(arg)
	if !ok {
		return c.say(ctx, s, c.t("door.email_bad"))
	}
	if !s.limiter("email-add:"+c.p.UserID.String(), 20*time.Minute, 3).Allow() {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	var link string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		link, err = s.issueEmailChange(ctx, tx, c.tenant.ID, c.p.UserID, email)
		return err
	}); err != nil {
		return err
	}
	s.background(ctx, "email change", func(ctx context.Context) { s.sendEmailChange(ctx, email, link) })
	return c.say(ctx, s, c.t("door.email_confirm", "email", email))
}

// --- the plan, and paying, from chat ---

// waPlan answers "plan": the business's plan, the trial's days left or
// when it renews, the price, and Pay for someone who may.
func (s *Server) waPlan(ctx context.Context, c *chat) error {
	if !s.Billing.On() {
		return c.say(ctx, s, c.t("door.plan_off"))
	}
	ov, err := s.Billing.Overview(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	sub, plan := ov.Subscription, ov.Plan
	price := billing.Naira(plan.MonthlyKobo)
	var text string
	switch {
	case sub == nil:
		return c.say(ctx, s, c.t("door.plan_partner", "plan", plan.Name))
	case sub.Status == billing.StatusTrial && sub.TrialEnd != nil:
		days := int(math.Ceil(time.Until(*sub.TrialEnd).Hours() / 24))
		text = c.t("door.plan_trial", "plan", plan.Name, "days", strconv.Itoa(max(days, 0)), "price", price)
	case sub.Status == billing.StatusPastDue || sub.Status == billing.StatusDegraded:
		text = c.t("door.plan_due", "plan", plan.Name)
	default:
		text = c.t("door.plan_active", "plan", plan.Name, "price", price, "date", sub.PeriodEnd.Format("2 January 2006"))
	}
	if sub.Status != billing.StatusActive && c.p.Can(PermBillingManage) {
		return c.say(ctx, s, text, whatsapp.Button{ID: "plan:pay", Title: c.t("door.button_pay")})
	}
	return c.say(ctx, s, text)
}

// waPay answers Pay (or "pay you@example.com"): a checkout link, the
// email asked for first when the account has none, since the payment
// provider sends receipts there.
func (s *Server) waPay(ctx context.Context, c *chat, email string) error {
	if !c.p.Can(PermBillingManage) {
		return c.say(ctx, s, c.t("door.pay_denied", "org", c.tenant.Name))
	}
	if !s.Billing.On() {
		return c.say(ctx, s, c.t("door.plan_off"))
	}
	if email != "" {
		e, _, ok := signupEmail(email)
		if !ok {
			return c.say(ctx, s, c.t("door.email_bad"))
		}
		email = e
	} else {
		var current, billingEmail string
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT u.email, COALESCE((SELECT s.billing_email FROM subscriptions s WHERE s.tenant_id = $2), '')
				FROM users u WHERE u.id = $1`, c.p.UserID, c.tenant.ID).Scan(&current, &billingEmail)
		}); err != nil {
			return err
		}
		if emailPlaceholder(current) && billingEmail == "" {
			return c.say(ctx, s, c.t("door.pay_email"))
		}
	}
	if !s.limiter("chat-pay:"+c.tenant.ID.String(), 10*time.Minute, 5).Allow() {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	out, err := s.Billing.Checkout(ctx, c.tenant.ID, billing.CheckoutRequest{Email: email}, c.p.ActorType()+":"+c.p.Actor())
	switch {
	case errors.Is(err, billing.ErrInvalid), errors.Is(err, billing.ErrConflict):
		return c.say(ctx, s, c.t("door.pay_failed", "reason", strings.TrimSpace(err.Error())))
	case err != nil:
		return err
	case out.Paid || out.CheckoutURL == "":
		return c.say(ctx, s, c.t("door.pay_nothing"))
	}
	return s.chatLinkSay(ctx, c, c.t("door.pay_link", "amount", billing.Naira(out.Invoice.TotalKobo)), c.t("door.button_pay"), out.CheckoutURL)
}

// chatLinkSay says text with a web address: on its own line where the
// chat's text links, as a button on Slack, whose plain text does not.
func (s *Server) chatLinkSay(ctx context.Context, c *chat, text, title, link string) error {
	if c.ch.Name() != "slack" {
		return c.say(ctx, s, text+"\n"+link)
	}
	_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + text, Link: &whatsapp.LinkButton{Title: title, URL: link}})
	return err
}
