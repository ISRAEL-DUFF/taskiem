package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/plain"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Matching payments in chat (decision 0045, T5; spec
// invoices-and-money-log 8.2; docs/whatsapp.md#matching-payments). When
// the matcher gives a payment suggestions and does not match it on its
// own, each member with invoice.manage and a linked WhatsApp number or
// Telegram account is asked, one message per payment through the outbox:
// the amount, when, how it came in and the best open suggestion with its
// reasons, with Yes, match and No. No turns the suggestion down and
// offers the next one. Names are never shown: the reasons hold none, and
// the customer is "the customer" (the web app shows them masked too).
//
// A button carries a payload signed with the chat token key, as invoice
// buttons do: Yes or No, the business, the suggestion and the member it
// was sent to. A press counts only when the signature holds, it comes
// from that member's own chat, the member belongs to the business and
// holds invoice.manage then, and the suggestion is still open; the
// decision is the web app's (matching.Matcher Confirm and Reject).

const matchButtonPrefix = "mt1."

// What a match button does.
const (
	matchYes byte = 'y'
	matchNo  byte = 'n'
	// matchSplit is Yes on a payment split across the customer's open
	// invoices, oldest first (matching.Matcher.ConfirmSplit).
	matchSplit byte = 's'
)

// matchButton is what a match button's payload says.
type matchButton struct {
	Action     byte
	Tenant     uuid.UUID
	Suggestion uuid.UUID
	User       uuid.UUID
}

func (b matchButton) body() []byte {
	out := make([]byte, 0, 49)
	out = append(out, b.Action)
	out = append(out, b.Tenant[:]...)
	out = append(out, b.Suggestion[:]...)
	return append(out, b.User[:]...)
}

func matchButtonMAC(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem/match-button/v1\x00"))
	m.Write(body)
	return m.Sum(nil)[:16]
}

func signMatchButton(key []byte, b matchButton) string {
	body := b.body()
	return matchButtonPrefix + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(matchButtonMAC(key, body))
}

var errBadMatchButton = errors.New("api: not a valid match button")

// parseMatchButton checks a payload's signature and reads it.
func parseMatchButton(key []byte, payload string) (matchButton, error) {
	var b matchButton
	rest, ok := strings.CutPrefix(payload, matchButtonPrefix)
	if !ok || len(key) < 32 || len(payload) > 200 {
		return b, errBadMatchButton
	}
	b64, m64, ok := strings.Cut(rest, ".")
	if !ok {
		return b, errBadMatchButton
	}
	body, err1 := base64.RawURLEncoding.DecodeString(b64)
	mac, err2 := base64.RawURLEncoding.DecodeString(m64)
	if err1 != nil || err2 != nil || len(body) != 49 || len(mac) != 16 || !hmac.Equal(mac, matchButtonMAC(key, body)) {
		return b, errBadMatchButton
	}
	if body[0] != matchYes && body[0] != matchNo && body[0] != matchSplit {
		return b, errBadMatchButton
	}
	b.Action = body[0]
	copy(b.Tenant[:], body[1:17])
	copy(b.Suggestion[:], body[17:33])
	copy(b.User[:], body[33:49])
	return b, nil
}

// MatchSuggested queues the question to the business's members who manage
// invoices and have linked a chat app: matching.Matcher's Suggested hook.
func (s *Server) MatchSuggested(ctx context.Context, tenant, entryID uuid.UUID, _ []matching.Suggestion) {
	if err := s.queueMatch(ctx, tenant, entryID); err != nil {
		s.Logger.Warn("matching: asking in chat", "tenant", tenant, "entry", entryID, "err", err)
	}
}

func (s *Server) queueMatch(ctx context.Context, tenant, entryID uuid.UUID) error {
	channels := s.chatChannelsOn()
	if len(channels) == 0 || s.Invoices == nil || len(s.chatSigner().Key) < 32 {
		return nil
	}
	var users []uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE tenant_id = $1`, tenant)
		if err != nil {
			return err
		}
		users, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		return err
	}
	var managers []uuid.UUID
	for _, u := range users {
		if p, _ := s.principalOf(ctx, tenant, u); p != nil && p.Can(PermInvoiceManage) {
			managers = append(managers, u)
		}
	}
	if len(managers) == 0 {
		return nil
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for _, channel := range channels {
			addrs, err := chatAddrsTx(ctx, tx, channel, managers)
			if err != nil {
				return err
			}
			for _, u := range managers {
				if _, ok := addrs[u]; !ok {
					continue
				}
				entry := entryID
				if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: channel, Kind: matching.OutboxMatch, User: u, Entry: &entry},
					outboxKey(channel, "match:"+entryID.String()+":"+u.String())); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// sendMatchItem sends a queued question (matching.OutboxMatch) with the
// payment's best open suggestion as it now stands; dropped when nothing is
// open any more, the member no longer manages invoices, or the chat cannot
// carry it (outside WhatsApp's window: there is no template for it, and
// the web app lists it).
func (s *Server) sendMatchItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if s.Invoices == nil || len(s.chatSigner().Key) < 32 {
		return fmt.Errorf("%w: matching in chat is off", whatsapp.ErrNothingToSend)
	}
	if it.Entry == nil || it.User == uuid.Nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	if p, _ := s.principalOf(ctx, it.Tenant, it.User); p == nil || !p.Can(PermInvoiceManage) {
		return fmt.Errorf("%w: the person no longer manages invoices", whatsapp.ErrNothingToSend)
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	var business string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business)
	}); err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	ask, ok, err := s.matchAsk(ctx, it.Tenant, it.User, l, *it.Entry)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the payment was matched or its suggestions turned down", whatsapp.ErrNothingToSend)
	}
	// Outside WhatsApp's window the question template carries the same
	// facts and the same signed buttons as quick replies, once approved.
	tpl := whatsapp.TplMatchQuestion
	ask.Vars["tenant"] = business
	_, err = ch.Send(ctx, addr, whatsapp.Message{Text: ch.Prefix(business) + ask.Text, Buttons: ask.Buttons, Tenant: it.Tenant,
		Template: &tpl, Vars: ask.Vars, Payloads: []string{ask.Buttons[0].ID, ask.Buttons[1].ID}})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsapp.ErrWindowClosed), whatsapp.TemplateUnusable(err):
		return fmt.Errorf("%w: outside WhatsApp's 24-hour window; the suggestion waits in the web app: %w", whatsapp.ErrNothingToSend, err)
	case errors.Is(err, telegram.ErrBlocked):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// matchQuestion is the question about a payment: its text and buttons
// inside the window, and the taskiem_match_question template's variables
// outside it (the facts in English, as the matcher gives them).
type matchQuestion struct {
	Text    string
	Buttons []whatsapp.Button
	Vars    map[string]string
}

// matchAsk is the question about a payment's best open suggestion, in l,
// with its buttons signed for user; false when none is open.
func (s *Server) matchAsk(ctx context.Context, tenant, user uuid.UUID, l lang.Tag, entryID uuid.UUID) (matchQuestion, bool, error) {
	var (
		id             uuid.UUID
		invoice, order *uuid.UUID
		raw            []byte
		amount         *int64
		when           time.Time
		channel, kind  string
		open           int
		number, ask    string
		due            int64
		found          bool
		reasons        []string
		split          matching.Proposal
	)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT s.id, s.invoice_id, s.order_id, s.reasons, e.amount_kobo, COALESCE(e.occurred_at, e.recorded_at), e.channel, e.kind,
				(SELECT count(*) FROM money_suggestions o WHERE o.tenant_id = s.tenant_id AND o.entry_id = s.entry_id AND o.status = 'open')
			FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id
			WHERE s.tenant_id = $1 AND s.entry_id = $2 AND s.status = 'open' ORDER BY s.score DESC, s.created_at, s.id LIMIT 1`, tenant, entryID).
			Scan(&id, &invoice, &order, &raw, &amount, &when, &channel, &kind, &open)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if order != nil {
			// A storefront order: its reference and what is paid online.
			ask = "wa.match.ask_order"
			if err := tx.QueryRow(ctx, `SELECT reference, COALESCE(deposit_kobo, total_kobo) FROM storefront_orders WHERE tenant_id = $1 AND id = $2`,
				tenant, *order).Scan(&number, &due); err != nil {
				return err
			}
		} else {
			ask = "wa.match.ask"
			x, err := s.Invoices.Get(ctx, tx, tenant, invoice.String())
			if err != nil {
				return err
			}
			number, due = x.Number, x.DueKobo
			// More than is due: offered split across the customer's open
			// invoices, oldest first.
			if split, err = s.matcher().Propose(ctx, tx, tenant, id, false); err != nil {
				return err
			}
			if split.Over {
				ask = "wa.match.ask_split"
			}
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return matchQuestion{}, false, err
	}
	_ = json.Unmarshal(raw, &reasons)
	var got int64
	if amount != nil {
		got = *amount
	}
	whenWords, channelWords, why := when.In(lagos()).Format("2 Jan 15:04"), matchChannelWords(l, channel, kind), whatsapp.SafeText(sentence(reasons))
	text := tr(l, ask, "amount", invoicing.Naira(got), "when", whenWords, "channel", channelWords,
		"number", number, "due", invoicing.Naira(due), "reasons", why, "parts", proposedParts(l, split))
	if open > 1 {
		text += "\n" + tr(l, "wa.match.more")
	}
	reasonsVar := strings.TrimRight(why, ".") + "."
	if split.Over {
		reasonsVar = "It is more than that, so Yes splits it across the customer's open invoices, oldest first: " + proposedParts(lang.EN, split) + ". " + reasonsVar
	}
	if order != nil {
		reasonsVar = "Storefront order " + number + ". " + reasonsVar
	}
	vars := map[string]string{"amount": invoicing.Naira(got), "when": whenWords, "channel": matchChannelWords(lang.EN, channel, kind),
		"number": number, "due": invoicing.Naira(due), "reasons": reasonsVar}
	key := s.chatSigner().Key
	yes := matchButton{Action: matchYes, Tenant: tenant, Suggestion: id, User: user}
	no := yes
	no.Action = matchNo
	if split.Over {
		yes.Action = matchSplit
	}
	buttons := []whatsapp.Button{{ID: signMatchButton(key, yes), Title: tr(l, "button.match_yes")}, {ID: signMatchButton(key, no), Title: tr(l, "button.match_no")}}
	return matchQuestion{Text: text, Buttons: buttons, Vars: vars}, true, nil
}

// proposedParts says a split: "INV-0101 ₦20,000, INV-0104 ₦30,000", and
// what would be left as the customer's credit.
func proposedParts(l lang.Tag, p matching.Proposal) string {
	list := make([]string, 0, len(p.Parts)+1)
	for _, x := range p.Parts {
		if x.AmountKobo > 0 {
			list = append(list, x.Number+" "+invoicing.Naira(x.AmountKobo))
		}
	}
	if p.CreditKobo > 0 {
		list = append(list, tr(l, "wa.match.split_credit", "credit", invoicing.Naira(p.CreditKobo)))
	}
	return whatsapp.SafeText(strings.Join(list, ", "))
}

// splitParts says a split as matched.
func splitParts(l lang.Tag, sp matching.Split) string {
	p := matching.Proposal{CreditKobo: sp.CreditKobo}
	for i, x := range sp.Parts {
		number := x.Target()
		if x.Invoice != nil {
			number = x.Invoice.Number
		}
		p.Parts = append(p.Parts, matching.ProposedPart{Part: matching.Part{AmountKobo: sp.PartKobo[i]}, Number: number})
	}
	return proposedParts(l, p)
}

// sentence joins reasons into one, its first letter a capital.
func sentence(reasons []string) string {
	out := strings.Join(reasons, ", ")
	if r, n := utf8.DecodeRuneInString(out); n > 0 {
		out = string(unicode.ToUpper(r)) + out[n:]
	}
	return out
}

// matchChannelWords says how money came in: "Paystack (main)", "a
// statement upload", "cash".
func matchChannelWords(l lang.Tag, channel, kind string) string {
	switch {
	case kind == "cash" || channel == "cash":
		return tr(l, "wa.match.channel.cash")
	case kind == "statement" || channel == "upload":
		return tr(l, "wa.match.channel.upload")
	}
	connector, connection, ok := strings.Cut(channel, "/")
	if !ok || connection == "" {
		return whatsapp.SafeText(plain.ServiceName(channel))
	}
	return whatsapp.SafeText(plain.ServiceName(connector) + " (" + connection + ")")
}

// matchPress handles a tapped Yes, match or No.
func (s *Server) matchPress(ctx context.Context, c *chat, payload string) error {
	b, err := parseMatchButton(s.chatSigner().Key, payload)
	if err != nil || s.Invoices == nil {
		return c.say(ctx, s, c.t("wa.button_invalid"))
	}
	// Decided in the business the button names, which the person must
	// belong to (on an own number, only that business).
	tenant, p := c.tenant, c.p
	if b.Tenant != c.tenant.ID {
		i := -1
		for j, t := range c.tenants {
			if t.ID == b.Tenant {
				i = j
			}
		}
		if i < 0 {
			return c.say(ctx, s, c.t("wa.button_invalid"))
		}
		tenant = c.tenants[i]
		if p, err = s.principalOf(ctx, tenant.ID, c.p.UserID); err != nil {
			return s.waSay(ctx, c.ch, c.addr, tenant.Name, c.t("wa.cannot_act", "org", tenant.Name))
		}
	}
	say := func(text string, buttons ...whatsapp.Button) error {
		return s.waSay(ctx, c.ch, c.addr, tenant.Name, text, buttons...)
	}
	if b.User != p.UserID {
		return say(c.t("wa.match.other_person"))
	}
	if !p.Can(PermInvoiceManage) {
		return say(c.t("wa.match.denied", "org", tenant.Name))
	}
	var entryID uuid.UUID
	var status, number string
	read := func() error {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT s.status, s.entry_id, COALESCE(i.number, o.reference) FROM money_suggestions s
				LEFT JOIN customer_invoices i ON i.id = s.invoice_id LEFT JOIN storefront_orders o ON o.id = s.order_id
				WHERE s.tenant_id = $1 AND s.id = $2`, tenant.ID, b.Suggestion).Scan(&status, &entryID, &number)
		})
	}
	err = read()
	if errors.Is(err, pgx.ErrNoRows) {
		return say(c.t("wa.button_invalid"))
	}
	if err != nil {
		return err
	}
	// decided answers a press on a suggestion already decided.
	decided := func() error {
		if err := read(); err != nil {
			return err
		}
		switch status {
		case "confirmed":
			return say(c.t("wa.match.already_matched", "number", number))
		case "rejected":
			return say(c.t("wa.match.already_rejected"))
		}
		// Closed: the payment was matched to another invoice or order.
		var other string
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT number FROM (
					SELECT i.number, p.created_at FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
					WHERE p.tenant_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL
					  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
					UNION ALL
					SELECT o.reference, p.created_at FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
					WHERE p.tenant_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL
					  AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)) m
				ORDER BY created_at DESC LIMIT 1`, tenant.ID, entryID).Scan(&other)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return say(c.t("wa.match.already_decided"))
		}
		if err != nil {
			return err
		}
		return say(c.t("wa.match.already_matched", "number", other))
	}
	if status != "open" {
		return decided()
	}
	m := s.matcher()
	if b.Action == matchSplit {
		sp, err := m.ConfirmSplit(ctx, tenant.ID, b.Suggestion, p.Actor())
		switch {
		case errors.Is(err, matching.ErrHeld):
			return say(c.t("wa.match.needs_approval"))
		case errors.Is(err, invoicing.ErrNotFound):
			return say(c.t("wa.button_invalid"))
		case errors.Is(err, invoicing.ErrState), errors.Is(err, invoicing.ErrInvalid):
			if rerr := read(); rerr != nil {
				return rerr
			}
			if status != "open" {
				return decided()
			}
			return say(c.t("wa.match.refused", "reason", whatsapp.SafeText(plainInvoiceError(err))))
		case err != nil:
			return err
		}
		return say(c.t("wa.match.matched_split", "parts", splitParts(c.lang.Tag, sp)))
	}
	if b.Action == matchYes {
		x, err := m.Confirm(ctx, tenant.ID, b.Suggestion, p.Actor(), false)
		switch {
		case errors.Is(err, matching.ErrHeld):
			return say(c.t("wa.match.needs_approval"))
		case errors.Is(err, invoicing.ErrNotFound):
			return say(c.t("wa.button_invalid"))
		case errors.Is(err, invoicing.ErrState):
			if rerr := read(); rerr != nil {
				return rerr
			}
			if status != "open" {
				return decided()
			}
			return say(c.t("wa.match.refused", "reason", whatsapp.SafeText(plainInvoiceError(err))))
		case err != nil:
			return err
		}
		if x.Order != nil {
			return say(c.t("wa.match.matched_order", "number", x.Order.Reference))
		}
		if x.Invoice.Status == "paid" || x.Invoice.DueKobo <= 0 {
			return say(c.t("wa.match.matched_paid", "number", x.Invoice.Number))
		}
		return say(c.t("wa.match.matched_part", "number", x.Invoice.Number, "due", invoicing.Naira(x.Invoice.DueKobo)))
	}
	err = m.Reject(ctx, tenant.ID, b.Suggestion, p.Actor())
	switch {
	case errors.Is(err, invoicing.ErrNotFound):
		return say(c.t("wa.button_invalid"))
	case errors.Is(err, invoicing.ErrState):
		return decided()
	case err != nil:
		return err
	}
	next, ok, err := s.matchAsk(ctx, tenant.ID, p.UserID, c.lang.Tag, entryID)
	if err != nil {
		return err
	}
	if ok {
		return say(c.t("wa.match.rejected_next")+"\n\n"+next.Text, next.Buttons...)
	}
	if s.PublicURL == "" {
		return say(c.t("wa.match.rejected_left"))
	}
	return say(c.t("wa.match.rejected_left_link", "link", s.PublicURL+"/invoices"))
}
