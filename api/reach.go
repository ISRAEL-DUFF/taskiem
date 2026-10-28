package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Updates for people who are not on Taskiem (no-technical-steps N11,
// decision 0039): a business's staff and customers send a link's one-time
// code to the platform bot or number, and are then reached there by the
// business's workflows (notify@1 message). They never use Taskiem from
// the chat: STOP and START are all they can say, and anything else gets
// one fixed answer.

// reachCodeRe finds a WhatsApp link's code in the message it filled in.
// A code may end in "-", after which there is no word boundary, so the
// end is the end of the text or a character a code cannot hold.
var reachCodeRe = regexp.MustCompile(`\bU-([A-Za-z0-9_-]{32})(?:[^A-Za-z0-9_-]|$)`)

// reachStart links the chat address that sent a code.
func (s *Server) reachStart(ctx context.Context, ch chatChannel, channel, addr, code string) error {
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	if !s.Reach.Ready() || !reach.CodeRe.MatchString(code) {
		return s.waSay(ctx, ch, addr, "", tr(l, "reach.code_invalid"))
	}
	t, ok, err := s.Reach.Take(ctx, code, channel, addr)
	if err != nil {
		return err
	}
	// A code for one business sent to another's own number links nothing.
	if !ok || (ch.OwnTenant() != uuid.Nil && ch.OwnTenant() != t.Tenant) {
		return s.waSay(ctx, ch, addr, "", tr(l, "reach.code_invalid"))
	}
	var p reach.Payload
	if len(t.Payload) > 0 && s.ReachKeys != nil {
		if err := s.ReachKeys.Open(reach.PayloadAAD(t.Hash), t.Payload, &p); err != nil {
			s.Logger.Warn("reach: opening a code's phones", "err", err)
		}
	}
	// WhatsApp proves the number a message comes from.
	proven := p.Proven
	if channel == reach.WhatsApp {
		proven = addr
	}
	rider := false
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		if t.Reference != "" {
			if err := s.Reach.LinkRequestTx(ctx, tx, t.Tenant, t.Reference, channel, addr, p.Phones); err != nil {
				return err
			}
		}
		if proven != "" {
			if _, err := s.Reach.LinkTx(ctx, tx, t.Tenant, proven, channel, addr); err != nil && !errors.Is(err, sender.ErrInvalid) {
				return err
			}
			var err error
			if rider, err = s.isRiderTx(ctx, tx, t.Tenant, proven); err != nil {
				return err
			}
		}
		return reachAudit(ctx, tx, t.Tenant, "reach.linked", map[string]any{"channel": channel, "proven": proven != "", "submission": t.Reference})
	}); err != nil {
		return err
	}
	if proven != "" || channel != reach.Telegram {
		if err := s.Reach.Done(ctx, t.Hash); err != nil {
			return err
		}
		return s.waSay(ctx, ch, addr, "", tr(l, linkedKey(rider), "business", t.Business))
	}
	// A Telegram account proves its phone with Telegram's own button.
	id, _ := telegram.ParseAddress(addr)
	key := "reach.share_number"
	if t.Reference != "" && len(p.Phones) > 0 {
		key = "reach.share_number_request"
	}
	return s.Telegram.Client.AskContact(ctx, id, tr(l, key, "business", t.Business), tr(l, "agent.share_button"))
}

// reachContact takes the phone a Telegram account shared for the code it
// sent; false when it sent none (an owner agreeing to a field agent).
func (s *Server) reachContact(ctx context.Context, u telegram.Update) (bool, error) {
	if !s.Reach.Ready() || u.Contact == nil {
		return false, nil
	}
	tg := s.Telegram
	addr := telegram.Address(u.FromID)
	t, ok, err := s.Reach.Pending(ctx, reach.Telegram, addr)
	if err != nil || !ok {
		return false, err
	}
	l := s.chooseLang(ctx, uuid.Nil, addr, "").Tag
	if !u.Contact.Own {
		return true, tg.Client.AskContact(ctx, u.FromID, tr(l, "reach.share_own"), tr(l, "agent.share_button"))
	}
	phone, err := s.Reach.Phone(u.Contact.Phone)
	if err != nil {
		if err := s.Reach.Done(ctx, t.Hash); err != nil {
			return true, err
		}
		return true, tg.Client.SendClearing(ctx, u.FromID, tr(l, "reach.number_unreachable", "business", t.Business))
	}
	rider := false
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		if _, err := s.Reach.LinkTx(ctx, tx, t.Tenant, phone, reach.Telegram, addr); err != nil {
			return err
		}
		var err error
		if rider, err = s.isRiderTx(ctx, tx, t.Tenant, phone); err != nil {
			return err
		}
		return reachAudit(ctx, tx, t.Tenant, "reach.linked", map[string]any{"channel": reach.Telegram, "proven": true, "submission": t.Reference})
	}); err != nil {
		return true, err
	}
	if err := s.Reach.Done(ctx, t.Hash); err != nil {
		return true, err
	}
	return true, tg.Client.SendClearing(ctx, u.FromID, tr(l, linkedKey(rider), "business", t.Business))
}

// isRiderTx reports whether a person who just tapped in is one of the
// business's riders (dispatch D1), who are told what the link is for.
func (s *Server) isRiderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phone string) (bool, error) {
	if !s.Riders.Ready() {
		return false, nil
	}
	return s.Riders.IsRiderTx(ctx, tx, tenant, phone)
}

// linkedKey is what a person who tapped in is told: a rider that their
// deliveries come here, anyone else that updates do.
func linkedKey(rider bool) string {
	if rider {
		return "rider.linked"
	}
	return "reach.linked"
}

// reachStopStart handles STOP and START from an address linked to no
// member; false for anything else.
func (s *Server) reachStopStart(ctx context.Context, ch chatChannel, channel, addr, text string) (bool, error) {
	if !s.Reach.Ready() {
		return false, nil
	}
	word := strings.Trim(strings.ToLower(strings.TrimSpace(text)), "/ .!")
	if i := strings.IndexByte(word, '@'); i > 0 {
		word = word[:i] // /stop@bot
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	switch word {
	case "stop", "unsubscribe":
		name, err := s.Reach.Stop(ctx, channel, addr)
		if err != nil {
			return true, err
		}
		if name == "" {
			return true, s.waSay(ctx, ch, addr, "", tr(l, "reach.stopped_none"))
		}
		return true, s.waSay(ctx, ch, addr, "", tr(l, "reach.stopped", "business", name))
	case "start":
		name, err := s.Reach.Resume(ctx, channel, addr)
		if err != nil || name == "" {
			return name != "", err
		}
		return true, s.waSay(ctx, ch, addr, "", tr(l, "reach.resumed", "business", name))
	}
	return false, nil
}

// reachAbout answers anything else from an address that businesses
// reach: who they are and how to stop. false when none reaches it.
func (s *Server) reachAbout(ctx context.Context, ch chatChannel, channel, addr string) (bool, error) {
	if !s.Reach.Ready() {
		return false, nil
	}
	names, err := s.Reach.Businesses(ctx, channel, addr)
	if err != nil || len(names) == 0 {
		return false, err
	}
	if !s.limiter("reach-about:"+addr, 10*time.Minute, 2).Allow() {
		return true, nil
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	return true, s.waSay(ctx, ch, addr, "", tr(l, "reach.about", "businesses", strings.Join(names, ", ")))
}

// reachAudit records a person outside the business linking a chat app.
func reachAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'reach', $2, 'contact', $3)`, tenant, action, raw)
	return err
}

// --- sending ---

// sendReachItem sends a queued message to someone outside the business
// (notify@1 message) on the chat app they linked. When they can no longer
// be reached there (they stopped, blocked the bot, or WhatsApp's update
// template is not approved yet), it goes by SMS instead.
func (s *Server) sendReachItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if !s.Reach.Ready() {
		return fmt.Errorf("%w: updates are off", whatsapp.ErrNothingToSend)
	}
	business, in, err := s.stepInput(ctx, it)
	if err != nil {
		return err
	}
	title, _ := in["title"].(string)
	message, _ := in["message"].(string)
	about, _ := in["about"].(string)
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%w: no message", whatsapp.ErrNothingToSend)
	}
	t := reach.Target{Channel: it.Channel}
	switch {
	case it.Contact != nil:
		t.Contact = *it.Contact
	case it.Reference != nil:
		t.Reference = *it.Reference
	default:
		return fmt.Errorf("%w: to no one", whatsapp.ErrNothingToSend)
	}
	ch, err := s.chatChannelFor(ctx, it.Channel, it.Tenant)
	if err != nil {
		return s.reachFallback(ctx, it, business, in, err)
	}
	var addr string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		addr, err = s.Reach.AddressTx(ctx, tx, it.Tenant, t)
		return err
	}); err != nil {
		if errors.Is(err, reach.ErrGone) {
			return s.reachFallback(ctx, it, business, in, err)
		}
		return err
	}
	body := message
	if title != "" {
		body = "*" + title + "*\n\n" + message
	}
	if about == "" {
		about = title
	}
	if about == "" {
		about = "your request"
	}
	head := message
	if title != "" {
		head = title + ": " + message
	}
	if r := []rune(head); len(r) > 500 {
		head = string(r[:500]) + "…"
	}
	tpl := whatsapp.TplUpdate
	m := whatsapp.Message{Text: ch.Prefix(business) + body, Tenant: it.Tenant, Template: &tpl,
		Vars: map[string]string{"tenant": business, "about": about, "message": head}}
	_, err = ch.Send(ctx, addr, m)
	var waErr *whatsapp.GraphError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, telegram.ErrBlocked):
		// The person blocked the bot or deleted their account.
		if berr := s.Reach.Blocked(ctx, it.Channel, addr); berr != nil {
			return berr
		}
		return s.reachFallback(ctx, it, business, in, err)
	case errors.As(err, &waErr) && waErr.Code >= 132000 && waErr.Code <= 132016:
		// The update template is missing, not approved or paused.
		return s.reachFallback(ctx, it, business, in, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// reachFallback sends a message that cannot go on its chat app by SMS to
// the step's phone, once; the chat message is then dropped.
func (s *Server) reachFallback(ctx context.Context, it whatsapp.OutboxItem, business string, in map[string]any, why error) error {
	phone, _ := in["phone"].(string)
	title, _ := in["title"].(string)
	message, _ := in["message"].(string)
	if s.Messages == nil || phone == "" {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, why)
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		_, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: it.Tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: phone,
			SenderName: business, Text: notify.SMSText(business, title, message), Key: "message-fallback:" + it.ID.String()})
		return err
	})
	if err != nil && !errors.Is(err, sender.ErrAllowance) && !errors.Is(err, sender.ErrInvalid) {
		return err
	}
	return fmt.Errorf("%w: sent by SMS instead: %w", whatsapp.ErrNothingToSend, why)
}

// stepInput is a queued item's step input, opened from the run's history
// (the outbox holds no text), and the business's name.
func (s *Server) stepInput(ctx context.Context, it whatsapp.OutboxItem) (string, map[string]any, error) {
	if it.Run == nil || it.Step == nil {
		return "", nil, fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business string
	var in map[string]any
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		in, err = s.openStepInput(ctx, tx, it.Tenant, *it.Run, *it.Step)
		return err
	})
	return business, in, err
}

// --- the business's view (Settings > People you message) ---

type reachView struct {
	// Telegram and WhatsApp say which apps people can get updates on.
	Telegram bool            `json:"telegram"`
	WhatsApp bool            `json:"whatsapp"`
	PageURL  string          `json:"page_url,omitempty"`
	Contacts []reach.Contact `json:"contacts"`
}

// getReach is who the business reaches on chat apps, and its standing
// page's link.
func (s *Server) getReach(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := reachView{Telegram: s.Telegram != nil, WhatsApp: s.WhatsApp != nil && s.WhatsApp.Config.DisplayNumber != "", Contacts: []reach.Contact{}}
	if !s.Reach.Ready() || s.ReachHost == "" {
		out.Telegram, out.WhatsApp = false, false
		writeJSON(w, http.StatusOK, out)
		return
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		id, err := reach.PageTx(r.Context(), tx, p.TenantID, false)
		if err != nil {
			return err
		}
		out.PageURL = "https://" + s.ReachHost + "/u/" + id
		out.Contacts, err = s.Reach.ListTx(r.Context(), tx, p.TenantID, 500)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// newReachPage replaces the standing page's link; the old one stops.
func (s *Server) newReachPage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !s.Reach.Ready() || s.ReachHost == "" {
		s.fail(w, r, fmt.Errorf("%w: updates on WhatsApp and Telegram are not set up on this Taskiem", errConflict))
		return
	}
	var url string
	err := s.tx(r, func(tx pgx.Tx) error {
		id, err := reach.PageTx(r.Context(), tx, p.TenantID, true)
		if err != nil {
			return err
		}
		url = "https://" + s.ReachHost + "/u/" + id
		return auditTx(r, tx, "reach.page_replaced", p.TenantID.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page_url": url})
}

// removeReachContact stops reaching a person: their contact and links go.
func (s *Server) removeReachContact(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || !s.Reach.Ready() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var ok bool
	err = s.tx(r, func(tx pgx.Tx) error {
		if ok, err = s.Reach.RemoveTx(r.Context(), tx, id); err != nil || !ok {
			return err
		}
		return auditTx(r, tx, "reach.contact_removed", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
