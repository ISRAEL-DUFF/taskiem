package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Keeping a chat binding its owner's (docs/spec/chat-way-in.md 6.3, 12).
// A chat-born account's number is its only credential until it adds an
// email, and operators reissue numbers that fall silent. So every binding
// silent for BindingDormantDays is dormant: it is sent nothing that
// carries a business's data (taskiem_wa_numbers and taskiem_tg_accounts
// leave it out) and its messages act on nothing until it is proven again,
// with the chat PIN, or by linking it again from the web. Dormant as long
// again, it is removed and the address is a stranger. The chat PIN is set
// from the chat by a person with no other credential, in the PIN form
// (encrypted to Taskiem), never as a message. And the terms: an account
// from before them is asked once, in chat as on the web.

// bindingGate records a bound person's message on their binding; a
// dormant one gets only the way to prove it (true: answered).
func (s *Server) bindingGate(ctx context.Context, c *chat, user uuid.UUID) (bool, error) {
	channel := c.ch.Name()
	if s.BindingDormantDays <= 0 || channel != "whatsapp" && channel != "telegram" {
		return false, nil
	}
	var state string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_binding_touch($1, $2, $3)`, channel, user, s.BindingDormantDays).Scan(&state); err != nil {
		return true, err
	}
	if state != "dormant" {
		return false, nil
	}
	if !s.limiter("dormant:"+c.addr, 10*time.Minute, 3).Allow() {
		return true, nil
	}
	days := strconv.Itoa(s.BindingDormantDays)
	if c.wa != nil && c.wa.Config.FlowKey != nil && len(c.tenants) > 0 {
		pin, err := s.waPinInfo(ctx, user)
		if err != nil {
			return true, err
		}
		if pin.Set && pin.LockedUntil == nil {
			c.tenant = c.tenants[0]
			sent, err := s.waSendChatPinFlow(ctx, c, user, "pin_wake", c.t("dormant.form_title"), c.t("dormant.prove", "days", days))
			if sent || err != nil {
				return true, err
			}
		}
	}
	return true, s.waSay(ctx, c.ch, c.addr, "", c.t("dormant.relink", "days", days, "where", s.accountWhere()))
}

// waChatPin answers "pin": the PIN form, for a person whose chat is their
// only credential; anyone with a passkey, an authenticator or a password
// sets it in the web app, behind those.
func (s *Server) waChatPin(ctx context.Context, c *chat) error {
	f, _, err := s.factorsOf(ctx, c.tenant.ID, c.p.UserID)
	if err != nil {
		return err
	}
	if f.Passkey || f.TOTP || f.Password {
		return c.say(ctx, s, c.t("chatpin.web", "where", s.accountWhere()))
	}
	if c.wa == nil || c.wa.Config.FlowKey == nil {
		return c.say(ctx, s, c.t("chatpin.not_here"))
	}
	sent, err := s.waSendChatPinFlow(ctx, c, c.p.UserID, "pin_set", c.t("chatpin.form_title"), c.t("chatpin.form"))
	if err != nil || sent {
		return err
	}
	return c.say(ctx, s, c.t("chatpin.not_here"))
}

// waSendChatPinFlow sends the PIN form to set the chat PIN (pin_set) or
// to wake a dormant binding with it (pin_wake), in c.tenant's records.
func (s *Server) waSendChatPinFlow(ctx context.Context, c *chat, user uuid.UUID, kind, title, body string) (bool, error) {
	tok, hash := whatsapp.NewFlowToken(c.tenant.ID)
	summary := c.t("chatpin.summary")
	meta, _ := json.Marshal(map[string]any{"title": title, "summary": summary})
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, user_id, data, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now() + $8::interval)`, hash, c.tenant.ID, kind, c.addr, c.wa.Config.PhoneNumberID, user,
			meta, waHandoffTTL.String())
		return err
	})
	if err != nil {
		return false, err
	}
	_, err = c.wa.Client.SendFlow(ctx, c.addr, whatsapp.FlowMessage{Flow: whatsapp.FlowPin, Token: tok, CTA: c.t("button.enter_pin"), Screen: whatsapp.ScreenPin,
		Data: whatsapp.PinForm(title, summary, ""), Body: body})
	if err != nil {
		s.Logger.Warn("whatsapp: sending a chat PIN form", "err", err)
		s.waDropFlow(ctx, c.tenant.ID, hex.EncodeToString(hash))
		return false, nil
	}
	return true, nil
}

// waFlowChatPin answers the PIN form sent by waSendChatPinFlow: on
// submission it sets the PIN, or checks it (wrong ones counted toward the
// lock) and wakes the binding.
func (s *Server) waFlowChatPin(ctx context.Context, row flowRow, req whatsapp.FlowRequest) any {
	title, _ := row.data["title"].(string)
	summary, _ := row.data["summary"].(string)
	screen := func(problem string) any {
		return flowScreen(whatsapp.ScreenPin, whatsapp.PinForm(title, summary, problem))
	}
	if req.Action != "data_exchange" || row.user == nil {
		return screen("")
	}
	user := *row.user
	finish := func(status, outcome string) any {
		if err := s.setFlow(ctx, row, status, map[string]any{"title": title, "summary": summary, "outcome": outcome}); err != nil {
			s.Logger.Error("whatsapp: closing a chat PIN form", "err", err)
		}
		if status == "done" {
			return flowDone(req.FlowToken)
		}
		return screen(outcome)
	}
	audit := func(action string, detail map[string]any) {
		p, err := s.principalOf(ctx, row.tenant, user)
		if err != nil {
			return
		}
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
			return waAudit(ctx, tx, "whatsapp", p, action, user.String(), detail)
		})
	}
	const wrong = "Something went wrong; try again."
	pin, _ := req.Data["pin"].(string)
	if row.kind == "pin_set" {
		switch {
		case !validPin(pin):
			return screen(fmt.Sprintf("The PIN is %d digits.", whatsapp.PinLength))
		case weakPin(pin):
			return screen("Choose a PIN that is not one digit repeated or a run like 123456.")
		}
		// Still a person with no other credential.
		if f, _, err := s.factorsOf(ctx, row.tenant, user); err != nil {
			s.Logger.Error("whatsapp: a chat PIN", "err", err)
			return screen(wrong)
		} else if f.Passkey || f.TOTP || f.Password {
			const m = "Set your PIN in the web app, on your Account page."
			return finish("refused", m)
		}
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_wa_pin_set($1, $2)`, user, hashPassword(pin)); err != nil {
			s.Logger.Error("whatsapp: setting a chat PIN", "err", err)
			return screen(wrong)
		}
		audit("whatsapp.pin.set", map[string]any{"via": "chat"})
		return finish("done", "Your chat PIN is set. Keep it to yourself: it proves this chat is yours.")
	}
	var hash string
	var locked *time.Time
	err := s.Store.Pool.QueryRow(ctx, `SELECT pin_hash, locked_until FROM taskiem_wa_pin_state($1)`, user).Scan(&hash, &locked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		const m = "You have no chat PIN. Link this number again from the web app."
		return finish("refused", m)
	case err != nil:
		s.Logger.Error("whatsapp: reading a PIN", "err", err)
		return screen(wrong)
	case locked != nil:
		m := "Your PIN is locked after too many wrong tries, until " + locked.UTC().Format("15:04") + " UTC."
		return finish("refused", m)
	}
	ok := validPin(pin) && checkPassword(hash, pin)
	var failures int
	var nowLocked bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT failures, locked FROM taskiem_wa_pin_result($1, $2, $3, $4::interval)`, user, ok, waPinMaxFailures, waPinWindow.String()).
		Scan(&failures, &nowLocked); err != nil {
		s.Logger.Error("whatsapp: recording a PIN check", "err", err)
		return screen(wrong)
	}
	if !ok {
		audit("whatsapp.pin.fail", map[string]any{"failures": failures, "via": "wake"})
		if nowLocked {
			return finish("refused", "Too many wrong PINs: your PIN is locked for 15 minutes.")
		}
		return screen(fmt.Sprintf("That PIN is not right. %d tries left.", waPinMaxFailures-failures))
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_binding_wake('whatsapp', $1)`, user); err != nil {
		s.Logger.Error("whatsapp: waking a binding", "err", err)
		return screen(wrong)
	}
	audit("binding.wake", map[string]any{"via": "chat_pin"})
	return finish("done", "This chat is yours again. Send *help* to carry on.")
}

// termsInChat asks an account from before the current terms to agree, once
// (as a message of its own, then the person's message goes on), and takes
// their agreement: the button, or "I agree". true: answered.
func (s *Server) termsInChat(ctx context.Context, c *chat, text string) (bool, error) {
	if !s.Terms.On() {
		return false, nil
	}
	if c.in.Reply == "terms:agree" || normalizeCommand(text) == "i agree" {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			return s.termsAcceptTx(ctx, tx, c.tenant.ID, c.p.UserID, c.ch.Name(), nil)
		})
		if err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("terms.thanks"))
	}
	key := c.p.UserID.String() + "|" + s.Terms.Version + "|" + s.Terms.PrivacyVersion
	if _, seen := s.termsAsked.Load(key); seen {
		return false, nil
	}
	var accepted, asked bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT accepted, asked FROM taskiem_terms_state($1, $2, $3)`, c.p.UserID, s.Terms.Version, s.Terms.PrivacyVersion).
		Scan(&accepted, &asked); err != nil {
		return true, err
	}
	if !accepted && !asked {
		var first bool
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_terms_asked($1, $2, $3, $4)`, c.p.UserID, s.Terms.Version, s.Terms.PrivacyVersion, c.ch.Name()).
			Scan(&first); err != nil {
			return true, err
		}
		if first {
			if err := s.waSay(ctx, c.ch, c.addr, "", c.t("terms.ask", "link", s.termsLink()), whatsapp.Button{ID: "terms:agree", Title: c.t("terms.button")}); err != nil {
				return true, err
			}
		}
	}
	s.termsAsked.Store(key, true)
	return false, nil
}

// termsLink is where the terms and privacy notice are read.
func (s *Server) termsLink() string {
	if s.Terms.URL != "" {
		return s.Terms.URL
	}
	if s.PublicURL != "" {
		return s.PublicURL + "/terms"
	}
	return "Taskiem's website"
}

// dormantSweep marks silent bindings dormant and removes those dormant a
// further span, at most once an hour per replica (WhatsAppTick).
func (s *Server) dormantSweep(ctx context.Context) error {
	if s.BindingDormantDays <= 0 || !s.limiter("dormant-sweep", time.Hour, 1).Allow() {
		return nil
	}
	var dormant, removed int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT dormant, removed FROM taskiem_bindings_dormant_sweep($1)`, s.BindingDormantDays).Scan(&dormant, &removed); err != nil {
		return err
	}
	if dormant > 0 || removed > 0 {
		s.Logger.Info("chat bindings: dormant sweep", "dormant", dormant, "removed", removed)
	}
	return nil
}
