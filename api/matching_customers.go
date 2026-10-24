package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
)

// Customers in matching (decision 0046 points 5 to 7; docs/matching.md):
// a customer's permanent accounts, their credit and the payers remembered
// for them, seen from one of their invoices; registering an account; and
// forgetting a payer.

// invoiceCustomer is what GET /v1/invoices/{id}/customer answers.
type invoiceCustomer struct {
	CreditKobo int64                       `json:"credit_kobo"`
	Accounts   []invoicing.CustomerAccount `json:"accounts"`
	Payers     []matching.Payer            `json:"payers"`
	Revealed   bool                        `json:"revealed"`
}

// maskPayer leaves a payer's first letter, or an account's last four
// digits.
func maskPayer(p *matching.Payer) {
	if p.Kind == "account" && len(p.Payer) > 4 {
		p.Payer = "•••" + p.Payer[len(p.Payer)-4:]
		return
	}
	p.Payer = maskName(p.Payer)
}

// getInvoiceCustomer serves GET /v1/invoices/{id}/customer: the invoice's
// customer's credit, permanent accounts and remembered payers. Names are
// masked unless the caller holds pii.reveal and asks (audited).
func (s *Server) getInvoiceCustomer(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	reveal, ok := revealing(w, r)
	if !ok {
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var out invoiceCustomer
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, err := s.Invoices.Get(r.Context(), tx, tenant, chi.URLParam(r, "id"))
		if err != nil {
			return invoiceErr(err)
		}
		if out.CreditKobo, err = invoicing.CreditOf(r.Context(), tx, tenant, x.CustomerID()); err != nil {
			return err
		}
		if out.Accounts, err = s.Invoices.AccountsOf(r.Context(), tx, tenant, x.CustomerID()); err != nil {
			return err
		}
		if out.Payers, err = matching.PayersOf(r.Context(), tx, s.Store.PII, tenant, x.CustomerID()); err != nil {
			return err
		}
		if !reveal {
			return nil
		}
		return auditTx(r, tx, "pii.reveal", "customer_invoice:"+x.Number+":customer", nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		for i := range out.Accounts {
			x := invoicing.Invoice{Customer: out.Accounts[i].Customer}
			maskInvoice(&x)
			out.Accounts[i].Customer = x.Customer
		}
		for i := range out.Payers {
			maskPayer(&out.Payers[i])
		}
	}
	out.Revealed = reveal
	writeJSON(w, http.StatusOK, out)
}

// registerCustomerAccount serves POST /v1/customer-accounts: a customer's
// permanent account (a virtual_account issued for them) linked to them, so
// money paid into it is matched to their invoices.
func (s *Server) registerCustomerAccount(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var req invoicing.AccountIn
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var a invoicing.CustomerAccount
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		a, err = s.Invoices.RegisterAccount(r.Context(), tx, p.TenantID, req, p.Actor())
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	x := invoicing.Invoice{Customer: a.Customer}
	maskInvoice(&x)
	a.Customer = x.Customer
	writeJSON(w, http.StatusOK, a)
}

// forgetPayer serves DELETE /v1/money/payers/{id}: a remembered payer is
// forgotten, and their next payment is suggested as anyone's.
func (s *Server) forgetPayer(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := matching.Forget(r.Context(), tx, principalFrom(r.Context()).TenantID, id); err != nil {
			return invoiceErr(err)
		}
		return auditTx(r, tx, "money.payer_forgotten", "money_payer:"+id.String(), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forgotten": true, "at": time.Now().UTC()})
}
