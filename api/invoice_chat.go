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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Invoices on WhatsApp and Telegram (decision 0045 point 7; spec
// invoices-and-money-log 6.2; docs/invoices.md). An invoice sent to a
// customer who asked the business for updates on a chat app (decision
// 0039) also goes there, with Pay by transfer and Open invoice. Those
// buttons, and New account number on the answer, are the only things a
// person outside the business can do in the chat besides STOP and START.
//
// A button carries a payload signed with the chat token key: what it does,
// the business, the invoice and the contact it was sent to. A press counts
// only when the signature holds, the address pressing it is that contact's
// link on that channel (not stopped), the invoice is the business's and
// still for that contact's phone, and the invoice is open. Pay by transfer
// and New account number leave a request for the invoice queue
// (taskiem_invoice_request_put); the queue issues the account and
// InvoiceAnswered replies in the chat.

const invoiceButtonPrefix = "iv1."

// What an invoice button does.
const (
	invoicePay  byte = 'p' // Pay by transfer
	invoiceNew  byte = 'n' // New account number
	invoiceOpen byte = 'o' // Open invoice
)

// invoiceButton is what an invoice button's payload says.
type invoiceButton struct {
	Action  byte
	Tenant  uuid.UUID
	Invoice uuid.UUID
	Contact uuid.UUID
}

var errBadInvoiceButton = errors.New("api: not a valid invoice button")

func (b invoiceButton) body() []byte {
	out := make([]byte, 0, 49)
	out = append(out, b.Action)
	out = append(out, b.Tenant[:]...)
	out = append(out, b.Invoice[:]...)
	return append(out, b.Contact[:]...)
}

func invoiceButtonMAC(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem/invoice-button/v1\x00"))
	m.Write(body)
	return m.Sum(nil)[:16]
}

// signInvoiceButton is a button's payload (93 characters: within
// WhatsApp's 256 and Telegram's stored 1024).
func signInvoiceButton(key []byte, b invoiceButton) string {
	body := b.body()
	return invoiceButtonPrefix + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(invoiceButtonMAC(key, body))
}

// parseInvoiceButton checks a payload's signature and reads it.
func parseInvoiceButton(key []byte, payload string) (invoiceButton, error) {
	var b invoiceButton
	rest, ok := strings.CutPrefix(payload, invoiceButtonPrefix)
	if !ok || len(key) < 32 || len(payload) > 200 {
		return b, errBadInvoiceButton
	}
	b64, m64, ok := strings.Cut(rest, ".")
	if !ok {
		return b, errBadInvoiceButton
	}
	body, err1 := base64.RawURLEncoding.DecodeString(b64)
	mac, err2 := base64.RawURLEncoding.DecodeString(m64)
	if err1 != nil || err2 != nil || len(body) != 49 || len(mac) != 16 || !hmac.Equal(mac, invoiceButtonMAC(key, body)) {
		return b, errBadInvoiceButton
	}
	switch body[0] {
	case invoicePay, invoiceNew, invoiceOpen:
	default:
		return b, errBadInvoiceButton
	}
	b.Action = body[0]
	copy(b.Tenant[:], body[1:17])
	copy(b.Invoice[:], body[17:33])
	copy(b.Contact[:], body[33:49])
	return b, nil
}

// invoiceButtons are the buttons for actions, in l, each signed for b's
// invoice and contact.
func invoiceButtons(l lang.Tag, key []byte, b invoiceButton, actions ...byte) []whatsapp.Button {
	titles := map[byte]string{invoicePay: "button.pay_transfer", invoiceNew: "button.new_account", invoiceOpen: "button.open_invoice"}
	out := make([]whatsapp.Button, 0, len(actions))
	for _, a := range actions {
		b.Action = a
		out = append(out, whatsapp.Button{ID: signInvoiceButton(key, b), Title: tr(l, titles[a])})
	}
	return out
}

// lagos is the time zone "valid until" is told in.
func lagos() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}

// errInvoiceMoot: the invoice can no longer be paid; nothing to send.
var errInvoiceMoot = errors.New("the invoice is paid, void or gone")

// sendInvoiceItem sends a queued invoice to a contact's chat app
// (invoicing.OutboxInvoice): with its buttons inside WhatsApp's 24-hour
// window, outside it as the invoice template (Pay by transfer as a quick
// reply carrying the same signed button, Open invoice opening its page)
// once Meta approved it in the number's account. The SMS and email went
// when it was sent, so when the chat cannot carry it (the template not
// approved; the person stopped or blocked the bot) it is dropped.
func (s *Server) sendInvoiceItem(ctx context.Context, it whatsapp.OutboxItem) error {
	key := s.chatSigner().Key
	if s.Invoices == nil || !s.Reach.Ready() || len(key) < 32 {
		return fmt.Errorf("%w: invoices in chat are off", whatsapp.ErrNothingToSend)
	}
	if it.Contact == nil || it.Invoice == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	ch, err := s.chatChannelFor(ctx, it.Channel, it.Tenant)
	if err != nil {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	var business, addr, link string
	var x invoicing.Invoice
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		if x, err = s.Invoices.Get(ctx, tx, it.Tenant, it.Invoice.String()); err != nil {
			return err
		}
		if !x.Payable() {
			return errInvoiceMoot
		}
		if link, err = s.Invoices.PageLink(ctx, tx, it.Tenant, x); err != nil {
			return err
		}
		addr, err = s.Reach.AddressTx(ctx, tx, it.Tenant, reach.Target{Channel: it.Channel, Contact: *it.Contact})
		return err
	})
	switch {
	case errors.Is(err, reach.ErrGone), errors.Is(err, invoicing.ErrNotFound), errors.Is(err, errInvoiceMoot):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	case err != nil:
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	b := invoiceButton{Tenant: it.Tenant, Invoice: x.ID, Contact: *it.Contact}
	buttons := invoiceButtons(l, key, b, invoicePay, invoiceOpen)
	tpl := whatsapp.TplInvoice
	m := whatsapp.Message{Tenant: it.Tenant, Buttons: buttons, Template: &tpl,
		Vars:     map[string]string{"tenant": business, "number": x.Number, "amount": invoicing.Naira(x.DueKobo), "link": link},
		Payloads: []string{buttons[0].ID},
		Text:     ch.Prefix(business) + tr(l, "invoice.chat.sent", "number", x.Number, "business", business, "amount", invoicing.Naira(x.DueKobo))}
	_, err = ch.Send(ctx, addr, m)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsapp.ErrWindowClosed), whatsapp.TemplateUnusable(err):
		return fmt.Errorf("%w: outside WhatsApp's 24-hour window, the invoice went by SMS or email only: %w", whatsapp.ErrNothingToSend, err)
	case errors.Is(err, telegram.ErrBlocked):
		if berr := s.Reach.Blocked(ctx, it.Channel, addr); berr != nil {
			return berr
		}
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// invoicePress handles a tapped invoice button from any chat address,
// member or not; false when the payload is not an invoice button's.
func (s *Server) invoicePress(ctx context.Context, ch chatChannel, channel, addr, payload string) (bool, error) {
	if !strings.HasPrefix(payload, invoiceButtonPrefix) {
		return false, nil
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	invalid := func() (bool, error) { return true, s.waSay(ctx, ch, addr, "", tr(l, "invoice.chat.button_invalid")) }
	if s.Invoices == nil || !s.Reach.Ready() {
		return invalid()
	}
	if !s.limiter("invoice-press:"+addr, time.Minute, 6).Allow() {
		return true, nil
	}
	b, err := parseInvoiceButton(s.chatSigner().Key, payload)
	// A button for one business pressed on another's own number does nothing.
	if err != nil || (ch.OwnTenant() != uuid.Nil && ch.OwnTenant() != b.Tenant) {
		return invalid()
	}
	var business, link, own, ownBank string
	var x invoicing.Invoice
	var ok bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{b.Tenant}, func(tx pgx.Tx) error {
		var err error
		if ok, x, err = s.invoiceForContact(ctx, tx, b, channel, addr); err != nil || !ok {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, b.Tenant).Scan(&business); err != nil {
			return err
		}
		switch {
		case !x.Payable():
		case b.Action == invoiceOpen:
			link, err = s.Invoices.PageLink(ctx, tx, b.Tenant, x)
		default:
			// A customer with a number of their own pays into it (W3).
			if x.CustomerID() != "" {
				err = tx.QueryRow(ctx, `SELECT account_number, COALESCE(bank_name, '') FROM customer_accounts WHERE customer_id = $1 ORDER BY created_at DESC, id LIMIT 1`,
					x.CustomerID()).Scan(&own, &ownBank)
				if err == nil {
					return nil
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			// The reply goes to the contact's channel: no address is kept.
			reply, _ := json.Marshal(map[string]any{"contact": b.Contact.String()})
			var id *uuid.UUID
			err = tx.QueryRow(ctx, `SELECT taskiem_invoice_request_put($1, $2, 'transfer', 0, $3, $4)`, b.Tenant, b.Invoice, channel, reply).Scan(&id)
		}
		return err
	})
	if err != nil {
		return true, err
	}
	if !ok {
		return invalid()
	}
	l = s.chooseLang(ctx, b.Tenant, addr, "").Tag
	switch {
	case x.Status == "paid":
		return true, s.waSay(ctx, ch, addr, business, tr(l, "invoice.chat.paid", "number", x.Number))
	case !x.Payable():
		return true, s.waSay(ctx, ch, addr, business, tr(l, "invoice.chat.closed", "number", x.Number, "business", business))
	case b.Action == invoiceOpen && link != "":
		return true, s.waSay(ctx, ch, addr, business, tr(l, "invoice.chat.link", "number", x.Number, "link", link))
	case b.Action == invoiceOpen:
		return invalid()
	case own != "":
		return true, s.waSay(ctx, ch, addr, business, tr(l, "invoice.chat.own_account", "number", x.Number, "amount", invoicing.Naira(x.TotalKobo-x.PaidKobo),
			"account", own, "bank", ownBank))
	}
	// The account number follows within seconds (InvoiceAnswered).
	return true, nil
}

// invoiceForContact reads b's invoice when addr is b's contact on channel
// and the invoice was sent to that contact's phone; false otherwise.
func (s *Server) invoiceForContact(ctx context.Context, tx pgx.Tx, b invoiceButton, channel, addr string) (bool, invoicing.Invoice, error) {
	h, found, err := s.Reach.ContactAtTx(ctx, tx, b.Tenant, b.Contact, channel, addr)
	if err != nil || !found {
		return false, invoicing.Invoice{}, err
	}
	x, err := s.Invoices.Get(ctx, tx, b.Tenant, b.Invoice.String())
	if errors.Is(err, invoicing.ErrNotFound) {
		return false, x, nil
	}
	if err != nil {
		return false, x, err
	}
	phone, err := s.Reach.Phone(x.Customer.Phone)
	if err != nil || x.SentAt == nil || !hmac.Equal(h, s.Reach.PhoneHash(phone)) {
		return false, x, nil //nolint:nilerr // the invoice is not this contact's
	}
	return true, x, nil
}

// InvoiceAnswered replies in the chat to a request from an invoice's
// buttons once the queue has issued the account number, or failed
// (invoicing.Invoices.Answered).
func (s *Server) InvoiceAnswered(ctx context.Context, q invoicing.Request, a invoicing.Account, issueErr error) {
	if err := s.invoiceAnswer(ctx, q, a, issueErr); err != nil {
		s.Logger.Warn("invoices: answering in chat", "tenant", q.Tenant, "invoice", q.Invoice, "channel", q.Channel, "err", err)
	}
}

func (s *Server) invoiceAnswer(ctx context.Context, q invoicing.Request, a invoicing.Account, issueErr error) error {
	key := s.chatSigner().Key
	if s.Invoices == nil || !s.Reach.Ready() || (q.Channel != reach.WhatsApp && q.Channel != reach.Telegram) {
		return nil
	}
	cs, _ := q.ReplyTo["contact"].(string)
	contact, err := uuid.Parse(cs)
	if err != nil {
		return nil //nolint:nilerr // nobody to answer
	}
	ch, err := s.chatChannelFor(ctx, q.Channel, q.Tenant)
	if err != nil {
		return err
	}
	var business, link, addr string
	var x invoicing.Invoice
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, q.Tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		if x, err = s.Invoices.Get(ctx, tx, q.Tenant, q.Invoice.String()); err != nil {
			return err
		}
		if link, err = s.Invoices.PageLink(ctx, tx, q.Tenant, x); err != nil {
			return err
		}
		addr, err = s.Reach.AddressTx(ctx, tx, q.Tenant, reach.Target{Channel: q.Channel, Contact: contact})
		return err
	})
	if errors.Is(err, reach.ErrGone) {
		return nil
	}
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, q.Tenant, addr, "").Tag
	text, actions := invoiceAnswerText(l, business, x, a, issueErr, link, time.Now())
	var buttons []whatsapp.Button
	if len(key) >= 32 {
		buttons = invoiceButtons(l, key, invoiceButton{Tenant: q.Tenant, Invoice: q.Invoice, Contact: contact}, actions...)
	}
	_, err = ch.Send(ctx, addr, whatsapp.Message{Text: ch.Prefix(business) + text, Buttons: buttons, Tenant: q.Tenant})
	if errors.Is(err, telegram.ErrBlocked) {
		return s.Reach.Blocked(ctx, q.Channel, addr)
	}
	if err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}

// invoiceAnswerText is the reply to Pay by transfer or New account
// number, in l, and the buttons that go with it: the account to pay into
// and until when (Lagos time); that it is on its way; or why not, with the
// invoice's page.
func invoiceAnswerText(l lang.Tag, business string, x invoicing.Invoice, a invoicing.Account, issueErr error, link string, now time.Time) (string, []byte) {
	withLink := func(t string) string {
		if link == "" {
			return t
		}
		return t + "\n" + link
	}
	switch {
	case errors.Is(issueErr, invoicing.ErrState) || issueErr == nil && !x.Payable():
		if x.Status == "paid" {
			return tr(l, "invoice.chat.paid", "number", x.Number), nil
		}
		return tr(l, "invoice.chat.closed", "number", x.Number, "business", business), nil
	case errors.Is(issueErr, invoicing.ErrLimit):
		return withLink(tr(l, "invoice.chat.limit", "number", x.Number)), []byte{invoiceOpen}
	case issueErr != nil:
		return withLink(tr(l, "invoice.chat.failed", "number", x.Number)), []byte{invoiceOpen}
	case a.Status != "open" || a.AccountNumber == "":
		return tr(l, "invoice.chat.wait", "number", x.Number), []byte{invoiceNew}
	}
	kv := []string{"number", x.Number, "amount", invoicing.Naira(a.AmountKobo), "account", a.AccountNumber, "bank", a.BankName, "name", a.AccountName}
	if a.ExpiresAt == nil {
		return tr(l, "invoice.chat.account_open", kv...), []byte{invoiceOpen}
	}
	if !a.Open(now) {
		return tr(l, "invoice.chat.wait", "number", x.Number), []byte{invoiceNew}
	}
	kv = append(kv, "until", a.ExpiresAt.In(lagos()).Format("15:04"))
	return tr(l, "invoice.chat.account", kv...), []byte{invoiceNew, invoiceOpen}
}
