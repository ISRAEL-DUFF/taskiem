package publicweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A customer's page, GET /c/{token} on the form host (decision 0060;
// docs/spec/no-sheet-needed.md §6.4, R5): what a customer owes a business
// across their open invoices, oldest first, and one way to pay the total
// or a part of it. It is the invoice page's template and payment section,
// with a line per invoice; like it, it shows nothing personal. A payment
// is counted to the invoices oldest first by matching, never here.

// CustomerInvoice is one open invoice on a customer's page.
type CustomerInvoice struct {
	Number    string
	Status    string
	Lines     []invoicing.Line
	TotalKobo int64
	PaidKobo  int64
	DueOn     *time.Time
}

// CustomerPage is what a customer's page shows.
type CustomerPage struct {
	Tenant   uuid.UUID
	Business string
	Invoices []CustomerInvoice
}

// Customers reads a customer's open invoices by their page token's hash;
// ok is false for a token that is unknown, an inactive business's, or a
// customer who owes nothing.
type Customers func(ctx context.Context, tokenHash []byte) (p CustomerPage, ok bool, err error)

// DBCustomers reads customers' pages with taskiem_public_customer_link.
func DBCustomers(pool *pgxpool.Pool) Customers {
	return func(ctx context.Context, tokenHash []byte) (CustomerPage, bool, error) {
		var p CustomerPage
		rows, err := pool.Query(ctx, `SELECT tenant_id, number, status, lines, total_kobo, paid_kobo, due_on, taskiem_public_brand(tenant_id)->>'name'
			FROM taskiem_public_customer_link($1)`, tokenHash)
		if err != nil {
			return CustomerPage{}, false, err
		}
		defer rows.Close()
		for rows.Next() {
			var x CustomerInvoice
			var lines []byte
			var business *string
			if err := rows.Scan(&p.Tenant, &x.Number, &x.Status, &lines, &x.TotalKobo, &x.PaidKobo, &x.DueOn, &business); err != nil {
				return CustomerPage{}, false, err
			}
			if err := json.Unmarshal(lines, &x.Lines); err != nil {
				return CustomerPage{}, false, fmt.Errorf("invoice lines: %w", err)
			}
			if business != nil {
				p.Business = *business
			}
			p.Invoices = append(p.Invoices, x)
		}
		if err := rows.Err(); err != nil {
			return CustomerPage{}, false, err
		}
		return p, len(p.Invoices) > 0, nil
	}
}

// DBCustomerPay is InvoicePay for customers' pages, over the public
// role's functions for them.
type DBCustomerPay struct{ Pool *pgxpool.Pool }

// Options implements InvoicePay.
func (d DBCustomerPay) Options(ctx context.Context, tokenHash []byte) ([]PayOption, error) {
	rows, err := d.Pool.Query(ctx, `SELECT kind, COALESCE(status, ''), COALESCE(amount_kobo, 0), COALESCE(account_number, ''), COALESCE(bank_name, ''),
		COALESCE(account_name, ''), COALESCE(payment_url, ''), expires_at, COALESCE(on_late, ''), COALESCE(on_wrong_amount, ''), waiting,
		issued_last_hour, issued, part_payments FROM taskiem_public_customer_link_payment($1)`, tokenHash)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayOption, error) {
		var o PayOption
		err := r.Scan(&o.Kind, &o.Status, &o.AmountKobo, &o.AccountNumber, &o.BankName, &o.AccountName, &o.PaymentURL, &o.ExpiresAt, &o.OnLate,
			&o.OnWrongAmount, &o.Waiting, &o.LastHour, &o.Issued, &o.PartPayments)
		return o, err
	})
}

// OwnAccount implements InvoicePay.
func (d DBCustomerPay) OwnAccount(ctx context.Context, tokenHash []byte) (string, string, error) {
	var number, bank string
	err := d.Pool.QueryRow(ctx, `SELECT account_number, bank_name FROM taskiem_public_customer_link_own_account($1)`, tokenHash).Scan(&number, &bank)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return number, bank, err
}

// Request implements InvoicePay.
func (d DBCustomerPay) Request(ctx context.Context, tokenHash []byte, kind string, amountKobo int64) (bool, error) {
	var id *string
	if err := d.Pool.QueryRow(ctx, `SELECT taskiem_public_customer_link_request($1, $2, $3)::text`, tokenHash, kind, amountKobo).Scan(&id); err != nil {
		return false, err
	}
	return id != nil, nil
}

// serveCustomer serves /c/{token} and POST /c/{token}/pay. A token that
// is malformed or unknown, and a customer who owes nothing, all answer
// the same page.
func (s *Server) serveCustomer(w http.ResponseWriter, r *http.Request, token, sub string) {
	w.Header().Set("X-Robots-Tag", "noindex")
	if s.Customers == nil || !invoiceTokenRe.MatchString(token) || sub != "" && (sub != "pay" || s.CustomerPay == nil) {
		s.customerNotFound(w, r)
		return
	}
	hash := invoicing.CustomerTokenHash(token)
	if sub == "pay" {
		s.askToPay(w, r, s.CustomerPay, hash, "/c/"+token, "here")
		return
	}
	if !allowed(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	p, ok, err := s.Customers(r.Context(), hash)
	if err != nil {
		s.tryLater(w, r, "customer page", err)
		return
	}
	if !ok {
		s.customerNotFound(w, r)
		return
	}
	business := p.Business
	if business == "" {
		business = "the business"
	}
	d := invoiceData{pageData: pageData{Title: "What you owe " + business, Lang: "en", Look: s.lookFor(r.Context(), p.Tenant, business)},
		Heading: "What you owe " + business, Business: business, Status: "Awaiting payment", Payable: true}
	var due int64
	for _, x := range p.Invoices {
		left := max(x.TotalKobo-x.PaidKobo, 0)
		due += left
		what := x.Number
		if len(x.Lines) > 0 {
			what += ": " + x.Lines[0].Description
			if len(x.Lines) > 1 {
				what += fmt.Sprintf(" and %d more", len(x.Lines)-1)
			}
		}
		l := invoiceLine{Description: what, Amount: invoicing.Naira(left)}
		if x.DueOn != nil {
			l.Each = "due " + x.DueOn.Format("2 Jan 2006")
		}
		if x.PaidKobo > 0 {
			l.Note = invoicing.Naira(x.PaidKobo) + " of " + invoicing.Naira(x.TotalKobo) + " paid."
		}
		if x.Status == "overdue" {
			d.Status = "Overdue"
		}
		d.Lines = append(d.Lines, l)
	}
	d.Total, d.Due = invoicing.Naira(due), invoicing.Naira(due)
	d.Note = "Money paid here counts towards these invoices, the oldest first."
	if s.CustomerPay != nil {
		if d.Pay, d.Refresh, err = s.payWays(r.Context(), s.CustomerPay, hash, "/c/"+token+"/pay", "Pay what you owe", "your balance", business, due); err != nil {
			s.tryLater(w, r, "customer payment", err)
			return
		}
	}
	s.renderPrivate(w, r, http.StatusOK, "invoice.html", d)
}

func (s *Server) customerNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderPrivate(w, r, http.StatusNotFound, "formclosed.html", closedData{pageData: pageData{Title: "Nothing to pay", Lang: "en"},
		Business: "Taskiem", Message: "There is nothing to pay on this page."})
}
