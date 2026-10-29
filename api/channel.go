package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// chatChannel is a chat app people use Taskiem from (spec 11): WhatsApp,
// on the platform number or a tenant's own, Telegram, on the platform bot
// (decision 0035), or Slack, for approvals, on a business's own workspace
// (decision 0051, slack.go). The conversation (commands, inputs, builds, data
// tables, approvals, run outcomes) is the same on each; only how a message
// travels differs. An address is a WhatsApp number (+E.164) or a Telegram
// account (tg:<id>), and the two never collide.
type chatChannel interface {
	// Name is "whatsapp", "telegram" or "slack".
	Name() string
	// OwnTenant is the tenant a channel speaks for alone (a tenant's own
	// WhatsApp number), uuid.Nil for a shared one.
	OwnTenant() uuid.UUID
	// Send delivers a message. Telegram has no 24-hour window and sends
	// the text and buttons; WhatsApp falls back to the template outside it.
	Send(ctx context.Context, addr string, m whatsapp.Message) (string, error)
	// Contact is the address's conversation: the tenant it speaks to now.
	Contact(ctx context.Context, addr string) (whatsapp.Contact, error)
	// Touch records an inbound message and, with setTenant, the tenant the
	// address now speaks to.
	Touch(ctx context.Context, addr string, inbound, setTenant bool, tenant *uuid.UUID) error
	// Prefix is how a tenant's messages start on a shared channel.
	Prefix(tenant string) string
}

var _ chatChannel = (*whatsapp.Platform)(nil)

// chatChannelsOn are the chat channels this deployment runs.
func (s *Server) chatChannelsOn() []string {
	var on []string
	if s.WhatsApp != nil {
		on = append(on, "whatsapp")
	}
	if s.Telegram != nil {
		on = append(on, "telegram")
	}
	if s.Slack != nil {
		on = append(on, "slack")
	}
	return on
}

// chatSigner signs and checks decision and hand-off tokens on every
// channel: WhatsApp's key when it is on (TASKIEM_WHATSAPP_TOKEN_KEY),
// else TASKIEM_CHAT_TOKEN_KEY.
func (s *Server) chatSigner() whatsapp.Signer {
	if s.WhatsApp != nil {
		return s.WhatsApp.Signer
	}
	return s.ChatSigner
}

// chatChannelFor is the channel a tenant speaks on: its own WhatsApp
// number when it has one, else the platform's.
func (s *Server) chatChannelFor(ctx context.Context, channel string, tenant uuid.UUID) (chatChannel, error) {
	switch {
	case channel == "telegram" && s.Telegram != nil:
		return s.Telegram, nil
	case channel == "whatsapp" && s.WhatsApp != nil:
		return s.WhatsApp.ForTenant(ctx, tenant)
	case channel == "slack" && s.Slack != nil:
		// The business's own workspace, through its Slack connection.
		return &slackChannel{s: s, tenant: tenant}, nil
	}
	return nil, fmt.Errorf("%w: the %s channel is off", whatsapp.ErrNothingToSend, channel)
}

// chatAddrsTx are the addresses on a channel of those of users who have
// linked it and are members of a tenant in tx's scope.
func chatAddrsTx(ctx context.Context, tx pgx.Tx, channel string, users []uuid.UUID) (map[uuid.UUID]string, error) {
	if channel == "slack" {
		return slackAddrsTx(ctx, tx, users)
	}
	q := `SELECT user_id, number FROM taskiem_wa_numbers($1)`
	if channel == "telegram" {
		q = `SELECT user_id, 'tg:' || telegram_id FROM taskiem_tg_accounts($1)`
	}
	//sqlcheck:built q is one of two literals above
	rows, err := tx.Query(ctx, q, users)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var u uuid.UUID
		var a string
		if err := rows.Scan(&u, &a); err != nil {
			rows.Close()
			return nil, err
		}
		out[u] = a
	}
	rows.Close()
	return out, rows.Err()
}

// chatAddrOf is the channel a member of tenant is reached on and their
// address there, or whatsapp.ErrNothingToSend when they have not linked it.
func (s *Server) chatAddrOf(ctx context.Context, channel string, tenant, user uuid.UUID) (chatChannel, string, error) {
	ch, err := s.chatChannelFor(ctx, channel, tenant)
	if err != nil {
		return nil, "", err
	}
	var addrs map[uuid.UUID]string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		addrs, err = chatAddrsTx(ctx, tx, channel, []uuid.UUID{user})
		return err
	}); err != nil {
		return nil, "", err
	}
	addr, ok := addrs[user]
	if !ok {
		return nil, "", fmt.Errorf("%w: the person has not linked %s", whatsapp.ErrNothingToSend, channel)
	}
	return ch, addr, nil
}

// sendOwnTemplate sends a member text, outside WhatsApp's window as its
// own template tpl with vars (the tenant's name among them), and, when the
// number's account does not hold the template approved or it cannot go
// as one (whatsapp.TemplateUnusable), as the alert template with
// alertVars instead, as before the template existed: the message is then
// sent, and why it went that way is recorded on it (whatsapp.ErrFellBack).
// The daily money summary and a Tell me message with a template of its
// own go this way.
func sendOwnTemplate(ctx context.Context, ch chatChannel, addr, text string, tenant uuid.UUID, tpl whatsapp.Template, vars, alertVars map[string]string) error {
	_, err := ch.Send(ctx, addr, whatsapp.Message{Text: text, Template: &tpl, Vars: vars, Tenant: tenant})
	if !whatsapp.TemplateUnusable(err) {
		return err
	}
	why := err
	alert := whatsapp.TplAlert
	if _, err := ch.Send(ctx, addr, whatsapp.Message{Text: text, Template: &alert, Vars: alertVars, Tenant: tenant}); err != nil {
		return err
	}
	return fmt.Errorf("%w: as %s, since %s could not go: %w", whatsapp.ErrFellBack, alert.Name, tpl.Name, why)
}

// outboxKey keeps one channel's queued messages apart from another's.
// WhatsApp's keys are as they were before Telegram.
func outboxKey(channel, key string) string {
	if channel == "" || channel == "whatsapp" {
		return key
	}
	return channel + ":" + key
}

// sendAlertItem sends a queued alert on its channel.
func (s *Server) sendAlertItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Channel == "telegram" {
		return s.tgSendAlertItem(ctx, it)
	}
	if s.WhatsApp == nil {
		return fmt.Errorf("%w: WhatsApp is off", whatsapp.ErrNothingToSend)
	}
	return s.WhatsApp.SendAlertItem(ctx, it)
}
