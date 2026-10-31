package publicweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// An invoice's page, GET /i/{token} on the form host (decision 0045, T3;
// docs/invoices.md): what a business sent its customer, read-only. It
// shows the business, the invoice's lines and totals, its status and its
// note, and nothing personal: the customer's name, phone and email are
// sealed and never reach the public role. The ways to pay it (T4) are in
// invoicepay.go.

// InvoicePage is what an invoice's page shows.
type InvoicePage struct {
	Tenant    uuid.UUID
	Business  string
	Number    string
	Status    string // sent, part_paid, paid, overdue or void
	Lines     []invoicing.Line
	TotalKobo int64
	PaidKobo  int64
	DueOn     *time.Time
	Note      string
	// LinkCode is the business's link code, printed for a customer that
	// is a business on Taskiem too (business links); empty when it has none.
	LinkCode string
}

// Invoices reads a sent invoice by its page token's hash; ok is false for
// a token that is unknown, a draft's, or an inactive business's.
type Invoices func(ctx context.Context, tokenHash []byte) (p InvoicePage, ok bool, err error)

// DBInvoices reads invoices with taskiem_public_customer_invoice, and the
// business's name with taskiem_public_brand.
func DBInvoices(pool *pgxpool.Pool) Invoices {
	return func(ctx context.Context, tokenHash []byte) (InvoicePage, bool, error) {
		var p InvoicePage
		var lines []byte
		var business, note, code *string
		err := pool.QueryRow(ctx, `SELECT i.tenant_id, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.due_on, i.note,
			taskiem_public_brand(i.tenant_id)->>'name', taskiem_public_link_code(i.tenant_id)
			FROM taskiem_public_customer_invoice($1) i`, tokenHash).
			Scan(&p.Tenant, &p.Number, &p.Status, &lines, &p.TotalKobo, &p.PaidKobo, &p.DueOn, &note, &business, &code)
		if errors.Is(err, pgx.ErrNoRows) {
			return InvoicePage{}, false, nil
		}
		if err != nil {
			return InvoicePage{}, false, err
		}
		if err := json.Unmarshal(lines, &p.Lines); err != nil {
			return InvoicePage{}, false, fmt.Errorf("invoice lines: %w", err)
		}
		if business != nil {
			p.Business = *business
		}
		if note != nil {
			p.Note = *note
		}
		if code != nil {
			p.LinkCode = *code
		}
		return p, true, nil
	}
}

// invoiceTokenRe is a page token's form: 24 random bytes, base64url.
var invoiceTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

// invoiceWords are the statuses a customer sees; a draft is never shown.
var invoiceWords = map[string]string{
	"sent": "Awaiting payment", "part_paid": "Part-paid", "paid": "Paid", "overdue": "Overdue", "void": "Void",
}

// invoiceData is the invoice template's input.
type invoiceData struct {
	pageData
	Business string
	Home     string // none: an invoice has no page behind it
	Number   string
	Status   string
	Lines    []invoiceLine
	Total    string
	Paid     string // empty when nothing was paid
	Due      string // empty when nothing is due
	DueOn    string // "Due 31 Jan 2099", while something is due
	Note     string
	Message  string // paid or void
	// Payable: something is due; Pay is how to pay it, nil when the
	// server offers no payments.
	Payable bool
	Pay     *payView
	Refresh int
	// LinkURL opens a link request to the business, for a customer that is
	// a business on Taskiem too (the web app's /l/<code>).
	LinkURL string
}

type invoiceLine struct {
	Description, Each, Amount string
	// Note: "Collected on behalf of WAEC" for a pass-through line, the
	// reason for a discount or an amendment.
	Note string
}

// invoiceLineView is how the page shows a line: a pass-through line as
// collected on its body's behalf (decision 0046 point 11), a discount or
// an amendment with its reason and no unit price.
func invoiceLineView(l invoicing.Line) invoiceLine {
	v := invoiceLine{Description: l.Description, Amount: invoicing.Naira(l.AmountKobo())}
	if l.Kind == "" {
		v.Each = fmt.Sprintf("%d × %s", l.Quantity, invoicing.Naira(l.UnitKobo))
	}
	var notes []string
	if l.PassThroughFor != "" {
		notes = append(notes, "Collected on behalf of "+l.PassThroughFor+".")
	}
	if l.Amendment != "" && l.Kind == "" {
		notes = append(notes, "Added after the invoice was sent.")
	}
	if l.Reason != "" {
		notes = append(notes, l.Reason)
	}
	v.Note = strings.Join(notes, " ")
	return v
}

// serveInvoice serves /i/{token}. A token that is malformed, unknown or a
// draft's all answer the same page, so the page tells nobody whether a
// token exists.
func (s *Server) serveInvoice(w http.ResponseWriter, r *http.Request, token, sub string) {
	w.Header().Set("X-Robots-Tag", "noindex")
	if s.Invoices == nil || !invoiceTokenRe.MatchString(token) || sub != "" && (sub != "pay" || s.InvoicePay == nil) {
		s.invoiceNotFound(w, r)
		return
	}
	if sub == "pay" {
		s.servePay(w, r, token)
		return
	}
	if !allowed(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	p, ok, err := s.Invoices(r.Context(), invoicing.TokenHash(token))
	if err != nil {
		s.tryLater(w, r, "invoice", err)
		return
	}
	words, shown := invoiceWords[p.Status]
	if !ok || !shown {
		s.invoiceNotFound(w, r)
		return
	}
	business := p.Business
	if business == "" {
		business = "the business"
	}
	d := invoiceData{pageData: pageData{Title: "Invoice " + p.Number, Lang: "en", Look: s.lookFor(r.Context(), p.Tenant, business)},
		Business: business, Number: p.Number, Status: words, Total: invoicing.Naira(p.TotalKobo), Note: p.Note}
	for _, l := range p.Lines {
		d.Lines = append(d.Lines, invoiceLineView(l))
	}
	if p.LinkCode != "" && s.AppURL != "" {
		d.LinkURL = strings.TrimRight(s.AppURL, "/") + "/l/" + p.LinkCode
	}
	if p.PaidKobo > 0 {
		d.Paid = invoicing.Naira(p.PaidKobo)
	}
	switch p.Status {
	case "void":
		d.Message = "This invoice was cancelled."
	case "paid":
		d.Message = "Paid in full. Thank you."
	default:
		if due := p.TotalKobo - p.PaidKobo; due > 0 {
			d.Due = invoicing.Naira(due)
		}
		if p.DueOn != nil {
			d.DueOn = "Due " + p.DueOn.Format("2 Jan 2006")
		}
		d.Payable = true
		if s.InvoicePay != nil && p.TotalKobo > p.PaidKobo {
			if d.Pay, d.Refresh, err = s.payOptions(r.Context(), token, p); err != nil {
				s.tryLater(w, r, "invoice payment", err)
				return
			}
		}
	}
	s.renderPrivate(w, r, http.StatusOK, "invoice.html", d)
}

func (s *Server) invoiceNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderPrivate(w, r, http.StatusNotFound, "formclosed.html", closedData{pageData: pageData{Title: "Invoice not found", Lang: "en"},
		Business: "Taskiem", Message: "This invoice was not found."})
}
