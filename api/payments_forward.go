package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Forward or reply "pay" (docs/spec/chat-pay-people.md, "WhatsApp,
// Telegram and Slack"; build-plan-gaps H7, g-pay-fwd): a message with
// account details forwarded to Taskiem on WhatsApp, Telegram or Slack, or
// a reply of "pay" quoting one, is read for an account as a typed one is.
// Taskiem checks the name with the bank and answers "Pay Chidi Okeke
// (GTBank ••6789) how much, and for what?", then asks as Pay an account
// number does; the answer prepares a payment, confirmed only with a
// passkey (by the link) or the PIN in WhatsApp's form. Nothing pays on
// words. A message with account details is kept, sealed, for a week under
// its id, for a reply of "pay" on a channel that sends only the id
// (WhatsApp, Slack).

// payReplyWords is a reply asking to pay what it quotes.
var payReplyWords = regexp.MustCompile(`(?i)^\s*(?:pay|pay (?:this|it|them|him|her|this account|this one)|pay now please|please pay)\s*[.!]*\s*$`)

const payQuoteTTL = 7 * 24 * time.Hour

// waPayForward answers a forwarded message with account details, or a
// reply of "pay" quoting one; false for anything else.
func (s *Server) waPayForward(ctx context.Context, c *chat) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil || c.in.Reply != "" || c.ch == nil {
		return false, nil
	}
	channel := c.ch.Name()
	if channel != "whatsapp" && channel != "telegram" && channel != "slack" {
		return false, nil
	}
	text := strings.TrimSpace(c.in.Text)
	reading := saved.Read(text)
	has := reading.Kind == saved.KindBankAccount && reading.Details["account_number"] != ""
	if has {
		s.keepPayQuote(ctx, c, channel, text)
	}
	if !c.p.Can(PermMoneyPay) {
		return false, nil
	}
	switch {
	case c.in.Forwarded && has:
		return true, s.payAskFor(ctx, c, reading)
	case c.in.Quoted != "" && payReplyWords.MatchString(text):
		quoted := strings.TrimSpace(c.in.QuotedText)
		if quoted == "" {
			quoted = s.payQuote(ctx, c, channel)
		}
		r := saved.Read(quoted)
		if quoted == "" || r.Kind != saved.KindBankAccount || r.Details["account_number"] == "" {
			return true, c.say(ctx, s, c.t("wa.pay.fwd.none"))
		}
		return true, s.payAskFor(ctx, c, r)
	}
	return false, nil
}

// keepPayQuote keeps a message with account details under its id, sealed
// under its person, for a week: a reply of "pay" quoting it finds it.
func (s *Server) keepPayQuote(ctx context.Context, c *chat, channel, text string) {
	ref := c.in.Ref
	if ref == "" {
		ref = c.in.ID
	}
	if ref == "" || channel == "telegram" { // Telegram sends the words with the reply
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_pay_quotes WHERE tenant_id = $1 AND user_id = $2 AND expires_at <= now()`, c.tenant.ID, c.p.UserID); err != nil {
			return err
		}
		env, err := s.sealMemberText(ctx, tx, c.tenant.ID, c.p.UserID, clipRunes(text, 2000))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat_pay_quotes (tenant_id, user_id, channel, message_id, body, expires_at) VALUES ($1, $2, $3, $4, $5, now() + $6::interval)
			ON CONFLICT (tenant_id, user_id, channel, message_id) DO UPDATE SET body = $5, expires_at = now() + $6::interval`,
			c.tenant.ID, c.p.UserID, channel, clipRunes(ref, 200), env, payQuoteTTL.String())
		return err
	}); err != nil {
		s.Logger.Warn("payments: keeping a message to pay from", "tenant", c.tenant.ID, "err", err)
	}
}

// payQuote is the kept message a reply quotes, "" when none.
func (s *Server) payQuote(ctx context.Context, c *chat, channel string) string {
	var out string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var env map[string]any
		err := tx.QueryRow(ctx, `SELECT body FROM chat_pay_quotes WHERE tenant_id = $1 AND user_id = $2 AND channel = $3 AND message_id = $4 AND expires_at > now()`,
			c.tenant.ID, c.p.UserID, channel, c.in.Quoted).Scan(&env)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		v, err := openText(ctx, s.Store.PII, tx, c.tenant.ID, env)
		if errors.Is(err, pii.ErrErased) {
			return nil
		}
		out, _ = v.(string)
		return err
	}); err != nil {
		s.Logger.Warn("payments: reading a quoted message", "tenant", c.tenant.ID, "err", err)
	}
	return out
}

// payAskFor checks the account's name with the bank and asks how much and
// what for, as Pay an account number asks (a saved account's @name for
// staff, who pay saved accounts only).
func (s *Server) payAskFor(ctx context.Context, c *chat, reading saved.Reading) error {
	number := reading.Details["account_number"]
	owner := slices.Contains(c.p.Roles, "owner")
	tenant := c.tenant.ID
	// A saved account with this number: paid by its @name (the only way
	// staff pay).
	payee := ""
	o := savedOwner{tenant: tenant, by: c.p.UserID}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, who := ownerWhere(o)
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND kind = $2 AND details->>'last4' = $3`, who, saved.KindBankAccount, saved.Last4(number))
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		for _, x := range list {
			n, err := s.openSaved(ctx, tx, o, x.ID, x.Sealed)
			if err != nil {
				return err
			}
			if n == number {
				payee = x.Name
				return nil
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if payee != "" {
		a, _ := chatActionByID("pay.person")
		return s.payStepsWith(ctx, c, a, map[string]string{"who": payee}, c.t("wa.pay.fwd.saved", "name", "@"+payee))
	}
	if !owner {
		return c.say(ctx, s, c.t("wa.pay.fwd.staff"))
	}
	a, _ := chatActionByID("pay.account")
	bank := strings.TrimSpace(reading.Details["bank_name"])
	if bank == "" {
		// Which bank: asked as Pay an account number asks it, the banks it
		// can be with first.
		return s.stepsStart(ctx, c, a, map[string]string{"number": number})
	}
	rl, err := s.payRail(ctx, tenant)
	if err != nil {
		return err
	}
	if rl == nil {
		return c.say(ctx, s, c.t("wa.pay.not", "reason", "Connect a payments account first: people are paid from it."))
	}
	acct := saved.BankAccount{BankName: bank, AccountNumber: number}
	code, err := s.bankCodeFor(ctx, tenant, *rl, acct, false)
	var pr payRefused
	if errors.As(err, &pr) {
		return s.stepsStart(ctx, c, a, map[string]string{"number": number})
	}
	if err != nil {
		return err
	}
	acct.BankCode, acct.CodeFrom = code, rl.Channel()
	res, _, err := s.checkWithRail(ctx, tenant, *rl, acct)
	if w := refusedWords(err); w != "" {
		return c.say(ctx, s, c.t("wa.pay.not", "reason", w))
	}
	if err != nil {
		return err
	}
	name := ""
	if res != nil {
		name = res.AccountName
	}
	if name == "" {
		name = saved.Masked(number)
	}
	return s.payStepsWith(ctx, c, a, map[string]string{"number": number, "bank": bank},
		c.t("wa.pay.fwd.ask", "name", whatsapp.SafeText(name), "bank", whatsapp.SafeText(bank), "account", "••"+saved.Last4(number)))
}

// payStepsWith starts an action's questions with values given, the first
// one asked in the words given ("… how much, and for what?").
func (s *Server) payStepsWith(ctx context.Context, c *chat, a chatAction, given map[string]string, ask string) error {
	st := &chatSteps{Action: a.ID, Values: given}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Steps: st}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, ask)
}
