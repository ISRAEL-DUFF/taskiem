package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Telegram as a chat channel (decision 0035, docs/telegram.md): the same
// conversation as WhatsApp (commands, inputs, builds, data tables,
// approvals, run outcomes and alerts) from the platform bot. A person links
// their Telegram account by opening a one-time t.me link made on their
// account page; the bot binds the account that opens it.

var errTelegramOff = fmt.Errorf("%w: Telegram is not set up on this Taskiem", errConflict)

type tgBindingInfo struct {
	Enabled     bool       `json:"enabled"`
	BotUsername string     `json:"bot_username,omitempty"`
	Linked      bool       `json:"linked"`
	Username    string     `json:"username,omitempty"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	// PendingExpiresAt: a link made and not opened yet, until then.
	PendingExpiresAt *time.Time `json:"pending_expires_at,omitempty"`
}

// getTelegram is the caller's Telegram link.
func (s *Server) getTelegram(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Telegram account belongs to a person, not an API key")
		return
	}
	out := tgBindingInfo{Enabled: s.Telegram != nil}
	if s.Telegram == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.BotUsername = s.Telegram.Config.BotUsername
	b, err := s.Telegram.Binding(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out.Linked, out.Username, out.VerifiedAt, out.PendingExpiresAt = b.TelegramID != 0, b.Username, b.VerifiedAt, b.PendingExpires
	writeJSON(w, http.StatusOK, out)
}

// startTelegramLink makes a one-time link that binds the Telegram account
// opening it. Like binding a WhatsApp number, it lets that account act for
// the person in every tenant they belong to, so it takes the same proof as
// adding a passkey.
func (s *Server) startTelegramLink(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Telegram account belongs to a person, not an API key")
		return
	}
	if s.Telegram == nil {
		s.fail(w, r, errTelegramOff)
		return
	}
	var req struct {
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.proveFactor(w, r, req.factorProof, "telegram.link") {
		return
	}
	if !s.limiter("tg-link:"+p.UserID.String(), 5*time.Minute, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many links asked for; wait a few minutes")
		return
	}
	code, err := s.Telegram.IssueLinkCode(r.Context(), p.UserID)
	if errors.Is(err, telegram.ErrTooMany) {
		writeErr(w, http.StatusTooManyRequests, "too many links this hour; try again later")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "telegram.link_made", p.UserID.String(), map[string]any{"channel": "web"})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": s.Telegram.LinkURL(code), "expires_at": time.Now().Add(10 * time.Minute).UTC()})
}

// unlinkTelegram removes the caller's Telegram link.
func (s *Server) unlinkTelegram(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Telegram account belongs to a person, not an API key")
		return
	}
	if s.Telegram == nil {
		s.fail(w, r, errTelegramOff)
		return
	}
	id, err := s.Telegram.Unbind(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id != 0 {
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "telegram.unlink", p.UserID.String(), map[string]any{"channel": "web"})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// TelegramHooks receives the platform bot's updates: POST
// /channels/telegram, with the secret set by setWebhook in
// X-Telegram-Bot-Api-Secret-Token. Served on the edge role, and on the
// api role when it runs every role.
func (s *Server) TelegramHooks() http.Handler {
	return http.HandlerFunc(s.tgReceive)
}

func (s *Server) tgReceive(w http.ResponseWriter, r *http.Request) {
	tg := s.Telegram
	if tg == nil || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	// Checked before anything is read: an update without the secret never
	// reaches the parser.
	if !telegram.VerifySecret(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), tg.Config.WebhookSecret) {
		writeErr(w, http.StatusUnauthorized, "bad secret token")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	u, err := telegram.Parse(body)
	if err != nil {
		// Malformed or unknown: acknowledged, or Telegram resends it forever.
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx := r.Context()
	// At most once: an update is claimed before it is acted on.
	fresh, err := tg.ClaimInbound(ctx, u.ID)
	if err != nil {
		s.Logger.Error("telegram: claiming an update", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "try again")
		return
	}
	if fresh {
		if err := s.tgHandle(ctx, u); err != nil {
			s.Logger.Error("telegram: handling an update", "update", u.ID, "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// tgHandle turns an update into an inbound message and hands it to the
// shared conversation.
func (s *Server) tgHandle(ctx context.Context, u telegram.Update) error {
	tg := s.Telegram
	// One-to-one chats only: in a group, anyone present would read the
	// replies, and a button's data would be visible to them all.
	if u.FromID <= 0 || u.ChatType != "private" || u.ChatID != u.FromID || (u.Kind != "message" && u.Kind != "callback") {
		return nil
	}
	if u.Kind == "callback" {
		// Acknowledged at once so the button stops spinning.
		if err := tg.Client.AnswerCallback(ctx, u.CallbackID, ""); err != nil {
			s.Logger.Warn("telegram: answering a button", "err", err)
		}
	}
	addr := telegram.Address(u.FromID)
	if err := tg.Touch(ctx, addr, true, false, nil); err != nil {
		return err
	}
	// Per account (spec 11.5): a compromised account cannot flood.
	if !s.limiter("tg:"+addr, 3*time.Second, 20).Allow() {
		if s.limiter("tg-warn:"+addr, time.Minute, 1).Allow() {
			return s.waSay(ctx, tg, addr, "", tr(s.chooseLang(ctx, uuid.Nil, addr, "").Tag, "wa.too_fast"))
		}
		return nil
	}
	in := whatsapp.Inbound{ID: strconv.FormatInt(u.ID, 10), Type: "text", Text: strings.TrimSpace(u.Text)}
	// A link being opened: t.me/<bot>?start=<code> arrives as "/start <code>".
	if code, ok := strings.CutPrefix(in.Text, "/start "); ok {
		return s.tgRedeem(ctx, u, strings.TrimSpace(code))
	}
	user, err := tg.UserOf(ctx, u.FromID)
	if err != nil {
		return err
	}
	if user == uuid.Nil {
		return s.tgUnbound(ctx, addr, in.Text)
	}
	switch {
	case u.Kind == "callback":
		payload, ok, err := tg.ResolveCallback(ctx, u.FromID, u.CallbackData)
		if err != nil {
			return err
		}
		if !ok {
			return s.waSay(ctx, tg, addr, "", tr(s.chooseLang(ctx, uuid.Nil, addr, "").Tag, "tg.button_expired"))
		}
		in.Type, in.Text, in.Reply = "interactive", "", payload
	case u.Voice != nil:
		in.Type, in.Text = "audio", ""
		in.Media = &whatsapp.Media{ID: u.Voice.FileID, MimeType: u.Voice.MimeType, Voice: u.Voice.Voice}
	case in.Text == "":
		return nil // a photo, a sticker, a location: nothing to act on
	default:
		// Telegram's commands are /help, /approvals...: read as the words.
		in.Text = tgCommandWords(in.Text, tg.Config.BotUsername)
	}
	return s.converse(ctx, &chat{ch: tg, addr: addr, in: in}, user)
}

// tgCommandWords reads "/help" (or "/help@bot", or "/start" with nothing
// after it) as the word the conversation understands.
func tgCommandWords(text, bot string) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}
	word, rest, _ := strings.Cut(text[1:], " ")
	word = strings.TrimSuffix(word, "@"+bot)
	if word == "start" {
		word = "help"
	}
	return strings.TrimSpace(word + " " + rest)
}

// tgRedeem binds the account that opened a one-time link.
func (s *Server) tgRedeem(ctx context.Context, u telegram.Update, code string) error {
	tg := s.Telegram
	addr := telegram.Address(u.FromID)
	l := s.chooseLang(ctx, uuid.Nil, addr, "").Tag
	where := s.accountWhere()
	user, outcome, err := tg.Redeem(ctx, code, u.FromID, u.Username)
	if err != nil {
		return err
	}
	switch outcome {
	case "ok":
		if err := s.tgAudit(ctx, user, "telegram.link", map[string]any{"channel": "telegram"}); err != nil {
			return err
		}
		return s.waSay(ctx, tg, addr, "", tr(l, "tg.linked"))
	case "account_taken":
		return s.waSay(ctx, tg, addr, "", tr(l, "tg.link_taken"))
	default:
		return s.waSay(ctx, tg, addr, "", tr(l, "tg.link_invalid", "where", where))
	}
}

// tgUnbound tells an account linked to no one how to link it.
func (s *Server) tgUnbound(ctx context.Context, addr, text string) error {
	if !s.limiter("tg-unbound:"+addr, 10*time.Minute, 2).Allow() {
		return nil
	}
	l := s.chooseLang(ctx, uuid.Nil, addr, text).Tag
	return s.waSay(ctx, s.Telegram, addr, "", tr(l, "tg.unbound", "where", s.accountWhere()))
}

func (s *Server) accountWhere() string {
	if s.PublicURL != "" {
		return s.PublicURL + "/account"
	}
	return "Account"
}

// tgAudit records a person's Telegram link in every tenant they belong to:
// the account now acts for them in each.
func (s *Server) tgAudit(ctx context.Context, user uuid.UUID, action string, detail map[string]any) error {
	tenants, err := s.tenantsOf(ctx, user)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			return waAudit(ctx, tx, "telegram", &Principal{TenantID: t.ID, UserID: user}, action, user.String(), detail)
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- alerts on Telegram ---

// TelegramAlerts delivers alerts to members' linked Telegram accounts (the
// telegram alert channel kind, alerts.Alerter.Telegram). Queued alerts are
// sent by the api role's notifier (RunWhatsApp).
type TelegramAlerts struct {
	Pool     *pgxpool.Pool
	Telegram *telegram.Platform
}

// Notify sends an alert (a test message) at once to those of members who
// have linked Telegram.
func (a *TelegramAlerts) Notify(ctx context.Context, tenant uuid.UUID, members []uuid.UUID, kind, title, body, link string, detail map[string]any) error {
	var name string
	var addrs map[uuid.UUID]string
	if err := db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
			return err
		}
		var err error
		addrs, err = chatAddrsTx(ctx, tx, "telegram", members)
		return err
	}); err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errors.New("none of the channel's members has linked Telegram")
	}
	m := whatsapp.AlertMessage(name, kind, title, body, link, detail)
	var errs []error
	for _, addr := range addrs {
		if _, err := a.Telegram.Send(ctx, addr, whatsapp.Message{Text: m.Text}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Queue queues a recorded alert for those of members who have linked
// Telegram, one message each, retried on its own by the notifier. It fails
// only when no member has linked Telegram.
func (a *TelegramAlerts) Queue(ctx context.Context, tenant, alert uuid.UUID, members []uuid.UUID) error {
	return db.InTenantTx(ctx, a.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		addrs, err := chatAddrsTx(ctx, tx, "telegram", members)
		if err != nil {
			return err
		}
		if len(addrs) == 0 {
			return errors.New("none of the channel's members has linked Telegram")
		}
		for u := range addrs {
			it := whatsapp.OutboxItem{Tenant: tenant, Channel: "telegram", Kind: whatsapp.OutboxAlert, User: u, Alert: &alert}
			if _, err := whatsapp.Enqueue(ctx, tx, it, outboxKey("telegram", "alert:"+alert.String()+":"+u.String())); err != nil {
				return err
			}
		}
		return nil
	})
}

// tgSendAlertItem sends a queued alert to its person on Telegram, rendered
// from the alert as recorded.
func (s *Server) tgSendAlertItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if s.Telegram == nil {
		return fmt.Errorf("%w: Telegram is off", whatsapp.ErrNothingToSend)
	}
	if it.Alert == nil {
		return fmt.Errorf("%w: no alert", whatsapp.ErrNothingToSend)
	}
	var name, kind, title, body string
	var link *string
	var detail map[string]any
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&name); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT kind, title, body, link, detail FROM alerts WHERE id = $1`, *it.Alert).Scan(&kind, &title, &body, &link, &detail)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the alert is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	_, addr, err := s.chatAddrOf(ctx, "telegram", it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := ""
	if link != nil {
		l = *link
	}
	m := whatsapp.AlertMessage(name, kind, title, body, l, detail)
	_, err = s.Telegram.Send(ctx, addr, whatsapp.Message{Text: m.Text})
	return err
}
