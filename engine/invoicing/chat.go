package invoicing

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Invoices on WhatsApp and Telegram (decision 0045 point 7; spec 6.2):
// when Send reaches a customer who asked the business for updates on a
// chat app (decision 0039), the invoice is queued there too. The outbox
// row names the invoice and the contact only; the API's outbox renders
// the message, with its Pay by transfer and Open invoice buttons, at each
// attempt (api/invoice_chat.go). SMS and email go as before: outside
// WhatsApp's 24-hour window the chat message is dropped, there being no
// approved template with the buttons yet.

// OutboxInvoice is the chat outbox kind of an invoice to a contact.
const OutboxInvoice = "invoice"

// queueChat queues the invoice to its customer's chat app when they asked
// this business for updates there; the channel, or "" when none.
func (s *Invoices) queueChat(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Invoice, key string) (string, error) {
	if !s.Reach.Ready() || len(s.Chat) == 0 || x.Customer.Phone == "" {
		return "", nil
	}
	phone, err := s.Reach.Phone(x.Customer.Phone)
	if err != nil {
		return "", nil //nolint:nilerr // not a phone a chat app can be found by
	}
	t, ok, err := s.Reach.FindTx(ctx, tx, tenant, uuid.Nil, phone, s.Chat)
	if err != nil || !ok || t.Contact == uuid.Nil {
		return "", err
	}
	if s.Messages != nil {
		// The same limits per person as SMS, across businesses.
		if ok, err := s.Messages.TakeRecipient(ctx, tx, "chat", phone); err != nil || !ok {
			return "", err
		}
	}
	contact, invoice := t.Contact, x.ID
	queued, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: t.Channel, Kind: OutboxInvoice, Contact: &contact, Invoice: &invoice},
		key+":chat")
	if err != nil || !queued {
		return "", err
	}
	return t.Channel, nil
}

// PageLink is the invoice's page link ("" when pages are not served), the
// same link every time.
func (s *Invoices) PageLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Invoice) (string, error) {
	if s.PageBase == "" {
		return "", nil
	}
	t, err := s.token(ctx, tx, tenant, &x)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s.PageBase, "/") + "/i/" + t, nil
}

// Payable reports whether an invoice can still be paid: sent and not yet
// paid in full or void.
func (x Invoice) Payable() bool {
	switch x.Status {
	case "sent", "part_paid", "overdue":
		return true
	}
	return false
}
