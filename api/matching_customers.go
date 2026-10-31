package api

import (
	"errors"
	"fmt"
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
	// Open is the customer's statement: their open invoices and notebook
	// debts, oldest first (what their money pays first), and OwedKobo what
	// is due on them in all.
	Open       []customerLine              `json:"open"`
	OwedKobo   int64                       `json:"owed_kobo"`
	CreditKobo int64                       `json:"credit_kobo"`
	Accounts   []invoicing.CustomerAccount `json:"accounts"`
	Payers     []matching.Payer            `json:"payers"`
	Revealed   bool                        `json:"revealed"`
}

// customerLine is one open invoice or notebook debt on a customer's
// statement.
type customerLine struct {
	ID      uuid.UUID `json:"id"`
	Number  string    `json:"number"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	DueKobo int64     `json:"due_kobo"`
	DueOn   string    `json:"due_on,omitempty"`
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
		open, err := s.Invoices.OpenInvoicesOf(r.Context(), tx, tenant, x.CustomerID())
		if err != nil {
			return err
		}
		out.Open = make([]customerLine, 0, len(open))
		for _, o := range open {
			out.Open = append(out.Open, customerLine{ID: o.ID, Number: o.Number, Kind: o.Kind, Status: o.Status, DueKobo: o.DueKobo, DueOn: o.DueOn})
			out.OwedKobo += o.DueKobo
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

// customerAccountIn is what POST /v1/invoices/{id}/customer-accounts takes:
// the invoice's customer's account, issued or given (the customer is the
// invoice's, so the caller never needs their details).
type customerAccountIn struct {
	Connection    string `json:"connection"`
	Issue         bool   `json:"issue,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	Reference     string `json:"reference,omitempty"`
	BankName      string `json:"bank_name,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
	Account       string `json:"account,omitempty"`
	BVN           string `json:"bvn,omitempty"`
	NIN           string `json:"nin,omitempty"`
}

// invoicesSvc is the invoices service with the rails that issue accounts
// (AccountRails, else the invoices' own when they can).
func (s *Server) invoicesSvc() invoicing.Service {
	inv := *s.Invoices
	if inv.AccountRails == nil {
		inv.AccountRails = s.accountRails()
	}
	return invoicing.Service{Invoices: &inv}
}

// giveInvoiceCustomerAccount serves POST /v1/invoices/{id}/customer-accounts:
// the invoice's customer is given a permanent account number, issued on
// a connection that can (issue) or the one the business already has for
// them. The same customer and connection again answers the account
// already issued. The customer is read from the invoice, unmasked, so no
// details travel; the answer is masked as the invoice is.
func (s *Server) giveInvoiceCustomerAccount(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var req customerAccountIn
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.Get(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	a, err := s.invoicesSvc().GiveAccount(r.Context(), p.TenantID, invoicing.AccountRequest{Customer: x.Customer, Channel: req.Connection, Issue: req.Issue,
		AccountNumber: req.AccountNumber, Reference: req.Reference, BankName: req.BankName, AccountName: req.AccountName, Account: req.Account, BVN: req.BVN, NIN: req.NIN}, p.Actor())
	if err != nil {
		if errors.Is(err, invoicing.ErrNoRail) {
			err = fmt.Errorf("%w: %s", errConflict, plainInvoiceError(err))
		}
		s.fail(w, r, invoiceErr(err))
		return
	}
	masked := invoicing.Invoice{Customer: a.Customer}
	maskInvoice(&masked)
	a.Customer = masked.Customer
	writeJSON(w, http.StatusOK, a)
}

// listAccountIssuers serves GET /v1/customer-accounts/issuers: the
// business's active prod connections that can issue customers' account
// numbers, with what each needs.
func (s *Server) listAccountIssuers(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	list, err := s.invoicesSvc().Issuers(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issuers": list})
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
