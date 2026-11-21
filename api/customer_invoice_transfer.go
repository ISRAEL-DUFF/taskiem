package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A customer pays an invoice by transfer from its card in their chat with
// the business, in the Taskiem app (the payments flow, part 1, settled 4):
// an account number for that invoice, issued by the business's provider,
// shown only to the customer the invoice is for; one at a time; asked for
// again and again, refused for a while, as checkout is.

// customerTransfer is an invoice's account number as its customer sees it.
type customerTransfer struct {
	Invoice       string     `json:"invoice"`
	Reference     string     `json:"reference"`
	DueKobo       int64      `json:"due_kobo"`
	State         string     `json:"state"` // open, paid, part, expired, none
	BankName      string     `json:"bank_name,omitempty"`
	AccountNumber string     `json:"account_number,omitempty"`
	AccountName   string     `json:"account_name,omitempty"`
	AmountKobo    int64      `json:"amount_kobo,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	// Longer: the business's provider gives longer than the default ("I
	// need longer" is offered).
	Longer bool `json:"longer,omitempty"`
}

// customerInvoiceTx is the person's own invoice from the business of chat
// c, read in the business's scope (entered): refused as not found when it
// is another customer's.
func (s *Server) customerInvoiceTx(ctx context.Context, tx pgx.Tx, c personChat, person uuid.UUID, id string) (invoicing.Invoice, error) {
	if s.Invoices == nil {
		return invoicing.Invoice{}, errDoorNone
	}
	if _, err := uuid.Parse(id); err != nil {
		return invoicing.Invoice{}, errDoorNone
	}
	var subject string
	if err := tx.QueryRow(ctx, `SELECT subject_id FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`,
		c.Business, person).Scan(&subject); err != nil {
		return invoicing.Invoice{}, err
	}
	x, err := s.Invoices.Get(ctx, tx, c.Business, id)
	if errors.Is(err, invoicing.ErrNotFound) || err == nil && (x.CustomerID() == "" || x.CustomerID() != subject || x.SentAt == nil) {
		return invoicing.Invoice{}, errDoorNone
	}
	return x, err
}

// customerTransferOf is the invoice's newest account number, in its state.
func customerTransferOf(ctx context.Context, tx pgx.Tx, x invoicing.Invoice) (customerTransfer, error) {
	out := customerTransfer{Invoice: x.ID.String(), Reference: x.Number, DueKobo: x.DueKobo, State: "none", PaidAt: x.PaidAt}
	if x.Status != "paid" && !x.Payable() {
		// Voided or cancelled: no number to pay into.
		return out, nil
	}
	all, err := invoicing.Accounts(ctx, tx, x.ID)
	if err != nil {
		return out, err
	}
	for _, a := range all {
		if a.Kind != "transfer" || a.AccountNumber == "" {
			continue
		}
		out.BankName, out.AccountNumber, out.AccountName, out.AmountKobo, out.ExpiresAt = a.BankName, a.AccountNumber, a.AccountName, a.AmountKobo, a.ExpiresAt
		switch {
		case x.Status == "paid" || a.Status == "paid" && x.DueKobo == 0:
			out.State = "paid"
		case a.Status == "paid":
			out.State = "part"
		case a.Open(time.Now()):
			out.State = "open"
		default:
			out.State = "expired"
		}
		return out, nil
	}
	if x.Status == "paid" {
		out.State = "paid"
	}
	return out, nil
}

// getCustomerTransfer is GET /v1/me/chats/{id}/invoices/{invoice}/transfer:
// the invoice's account number, if one was given.
func (s *Server) getCustomerTransfer(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out customerTransfer
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.pay", map[string]any{"invoice": chi.URLParam(r, "invoice")}, func(tx pgx.Tx, c personChat) error {
		x, err := s.customerInvoiceTx(r.Context(), tx, c, person, chi.URLParam(r, "invoice"))
		if err != nil {
			return err
		}
		out, err = customerTransferOf(r.Context(), tx, x)
		out.Longer = s.transferLonger(r.Context(), c.Business, x.Connection)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// transferLonger reports whether the business's provider gives a longer
// time than the default for an account number.
func (s *Server) transferLonger(ctx context.Context, tenant uuid.UUID, connection string) bool {
	if s.Invoices == nil || s.Invoices.Rails == nil {
		return false
	}
	rl, err := s.Invoices.Rails.Resolve(ctx, tenant, connection)
	return err == nil && rl.Transfer != nil && rl.Transfer.MaxMinutes > invoicing.AccountMinutes
}

// askCustomerTransfer is POST /v1/me/chats/{id}/invoices/{invoice}/transfer
// {longer?}: an account number for the invoice, for what is due: the open
// one again, or a new one once it has expired.
func (s *Server) askCustomerTransfer(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Longer bool `json:"longer"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.limiter("customer-transfer:"+person.String(), time.Minute/6, 6).Allow() {
		s.personFail(w, r, customerSendErr{http.StatusTooManyRequests, "You asked for numbers very fast. Wait a moment, or pay on the invoice's page."})
		return
	}
	ctx := r.Context()
	var business uuid.UUID
	var x invoicing.Invoice
	err = s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.pay", map[string]any{"invoice": chi.URLParam(r, "invoice")}, func(tx pgx.Tx, c personChat) error {
		var err error
		business = c.Business
		x, err = s.customerInvoiceTx(ctx, tx, c, person, chi.URLParam(r, "invoice"))
		if err != nil {
			return err
		}
		if !x.Payable() {
			return customerSendErr{http.StatusConflict, "Nothing is left to pay on this invoice."}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	minutes := 0
	if in.Longer {
		minutes = invoicing.LongestMinutes
	}
	_, err = s.Invoices.IssueFor(ctx, business, x.ID, "transfer", 0, minutes)
	switch {
	case errors.Is(err, invoicing.ErrLimit):
		s.personFail(w, r, customerSendErr{http.StatusTooManyRequests, "Too many numbers asked for this invoice. Try again in an hour, or pay on the invoice's page."})
		return
	case errors.Is(err, invoicing.ErrNoRail):
		s.personFail(w, r, customerSendErr{http.StatusConflict, "They can't give an account number just now: pay on the invoice's page."})
		return
	case errors.Is(err, invoicing.ErrState):
		s.personFail(w, r, customerSendErr{http.StatusConflict, "Nothing is left to pay on this invoice."})
		return
	case err != nil:
		s.Logger.Warn("customer: an invoice's account number", "tenant", business, "invoice", x.ID, "err", err)
		s.personFail(w, r, customerSendErr{http.StatusBadGateway, "Their bank did not give a number just now. Try again, or pay on the invoice's page."})
		return
	}
	var out customerTransfer
	err = s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.pay", map[string]any{"invoice": x.ID.String()}, func(tx pgx.Tx, c personChat) error {
		x, err := s.customerInvoiceTx(ctx, tx, c, person, x.ID.String())
		if err != nil {
			return err
		}
		out, err = customerTransferOf(ctx, tx, x)
		out.Longer = s.transferLonger(ctx, c.Business, x.Connection)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
