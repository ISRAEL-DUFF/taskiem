package publicweb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Paying an invoice from its page (decision 0045, T4): Pay by bank
// transfer asks for a one-time account number, Pay by card for a payment
// link, and the page shows what was issued with how long it is valid. The
// public role only queues the request (taskiem_public_invoice_request);
// the api role asks the rail. The page refreshes itself while it waits
// and while an account is open, with no script.

// PayOption is the state of one way of paying an invoice.
type PayOption struct {
	Kind          string // transfer or link
	Status        string // "" (none yet), requested, open, failed, paid, returned
	AmountKobo    int64
	AccountNumber string
	BankName      string
	AccountName   string
	PaymentURL    string
	ExpiresAt     *time.Time
	OnLate        string
	OnWrongAmount string
	Waiting       bool
	LastHour      int
	Issued        int
	PartPayments  bool
}

// InvoicePay reads an invoice's ways of paying and queues requests for
// new ones.
type InvoicePay interface {
	Options(ctx context.Context, tokenHash []byte) ([]PayOption, error)
	// Request queues an account (transfer) or a link; ok is false when
	// the invoice refuses it (not open, an amount it does not take, or
	// past the limits).
	Request(ctx context.Context, tokenHash []byte, kind string, amountKobo int64) (ok bool, err error)
	// OwnAccount is the customer's own permanent account number, when the
	// business registered one for them (W3): number "" when none.
	OwnAccount(ctx context.Context, tokenHash []byte) (number, bank string, err error)
}

// DBInvoicePay is InvoicePay over the public role's functions.
type DBInvoicePay struct{ Pool *pgxpool.Pool }

// Options implements InvoicePay.
func (d DBInvoicePay) Options(ctx context.Context, tokenHash []byte) ([]PayOption, error) {
	rows, err := d.Pool.Query(ctx, `SELECT kind, COALESCE(status, ''), COALESCE(amount_kobo, 0), COALESCE(account_number, ''), COALESCE(bank_name, ''),
		COALESCE(account_name, ''), COALESCE(payment_url, ''), expires_at, COALESCE(on_late, ''), COALESCE(on_wrong_amount, ''), waiting,
		issued_last_hour, issued, part_payments FROM taskiem_public_invoice_payment($1)`, tokenHash)
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
func (d DBInvoicePay) OwnAccount(ctx context.Context, tokenHash []byte) (string, string, error) {
	var number, bank string
	err := d.Pool.QueryRow(ctx, `SELECT account_number, bank_name FROM taskiem_public_invoice_own_account($1)`, tokenHash).Scan(&number, &bank)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return number, bank, err
}

// Request implements InvoicePay.
func (d DBInvoicePay) Request(ctx context.Context, tokenHash []byte, kind string, amountKobo int64) (bool, error) {
	var id *string
	if err := d.Pool.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, $2, $3)::text`, tokenHash, kind, amountKobo).Scan(&id); err != nil {
		return false, err
	}
	return id != nil, nil
}

// lagos is where times on invoice pages are given.
var lagos = func() *time.Location {
	if l, err := time.LoadLocation("Africa/Lagos"); err == nil {
		return l
	}
	return time.FixedZone("WAT", 3600)
}()

// payView is what the page shows of the ways to pay.
type payView struct {
	// Own: the customer's own account number, always open.
	Own *accountView
	// Transfer: the account to pay into, while it is open.
	Account *accountView
	// Waiting: an account or link is being made; the page refreshes.
	Waiting string
	// Note: why there is no account (expired, failed, limits).
	Note string
	// The buttons: Pay by bank transfer (or Get a new account number),
	// Pay by card, and whether an amount may be typed.
	TransferLabel string
	CardLabel     string
	CardURL       string
	PartPayments  bool
	MinPart, Due  string
	Action        string
}

type accountView struct {
	Number, Bank, Name, Amount, Until, Left, Late, Wrong string
}

// payOptions fills the page's payment section and says how often it
// should refresh itself (0: never).
func (s *Server) payOptions(ctx context.Context, token string, p InvoicePage) (*payView, int, error) {
	opts, err := s.InvoicePay.Options(ctx, invoicing.TokenHash(token))
	if err != nil {
		return nil, 0, err
	}
	now := s.now()
	due := p.TotalKobo - p.PaidKobo
	v := &payView{TransferLabel: "Pay by bank transfer", CardLabel: "Pay by card", Action: "/i/" + token + "/pay", Due: invoicing.Naira(due)}
	refresh := 0
	for _, o := range opts {
		v.PartPayments = o.PartPayments
		limited := o.LastHour >= invoicing.MaxPerHour || o.Issued >= invoicing.MaxPerInvoice
		switch o.Kind {
		case "transfer":
			switch {
			case o.Waiting || o.Status == "requested" && o.AccountNumber == "":
				v.Waiting, refresh = "Getting your account number…", 2
			case o.Status == "open" && o.ExpiresAt != nil && o.ExpiresAt.After(now):
				left := o.ExpiresAt.Sub(now).Round(time.Minute)
				v.Account = &accountView{Number: o.AccountNumber, Bank: o.BankName, Name: o.AccountName, Amount: invoicing.Naira(o.AmountKobo),
					Until: "Valid until " + o.ExpiresAt.In(lagos).Format("15:04") + " (Lagos time)", Left: minutesLeft(left),
					Late: lateWords[o.OnLate], Wrong: wrongWords[o.OnWrongAmount]}
				v.TransferLabel, refresh = "Get a new account number", 30
			case o.Status == "open" || o.Status == "requested":
				v.Note, v.TransferLabel = "The last account number has expired. If you already paid into it, your payment will still show here.", "Get a new account number"
			case o.Status == "failed":
				v.Note = "We could not get an account number just now. Please try again in a few minutes."
			}
			if limited {
				v.TransferLabel = ""
				if v.Account == nil && v.Waiting == "" {
					v.Note = "Too many account numbers were asked for this invoice. Please try again in an hour, or contact " + p.Business + "."
				}
			}
		case "link":
			switch {
			case o.Waiting || o.Status == "requested" && o.PaymentURL == "":
				if v.Waiting == "" {
					v.Waiting, refresh = "Getting your payment page…", 2
				}
			case o.Status == "open" && o.PaymentURL != "":
				v.CardURL, v.CardLabel = o.PaymentURL, "Continue to card payment"
			case o.Status == "failed" && v.Note == "":
				v.Note = "Card payment is not available for this invoice just now."
			}
			if limited && v.CardURL == "" {
				v.CardLabel = ""
			}
		}
	}
	if v.PartPayments {
		v.MinPart = invoicing.Naira(min(invoicing.MinPartKobo, due))
	}
	// A customer with a number of their own pays into it; a one-time number
	// stays on offer.
	if num, bank, err := s.InvoicePay.OwnAccount(ctx, invoicing.TokenHash(token)); err != nil {
		return nil, 0, err
	} else if num != "" {
		v.Own = &accountView{Number: num, Bank: bank, Amount: v.Due}
		if v.TransferLabel == "Pay by bank transfer" {
			v.TransferLabel = "Get a one-time account number instead"
		}
	}
	return v, refresh, nil
}

var lateWords = map[string]string{
	"returned": "A transfer made after this time is sent back to you.",
	"credited": "A transfer made after this time is still counted.",
}

var wrongWords = map[string]string{
	"returned":        "Send exactly this amount: a different amount is sent back to you.",
	"excess_returned": "Send exactly this amount: less is sent back to you, and anything over it is returned.",
	"credited":        "Send exactly this amount.",
}

func minutesLeft(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m <= 1:
		return "Less than a minute left"
	case m < 120:
		return strconv.Itoa(m) + " minutes left"
	}
	return fmt.Sprintf("About %d hours left", m/60)
}

// servePay serves POST /i/{token}/pay: kind (transfer or link) and, for
// an invoice that takes part payments, amount in naira. It queues the
// request and sends the customer back to the page, which waits for it.
func (s *Server) servePay(w http.ResponseWriter, r *http.Request, token string) {
	if !allowed(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "This could not be read. Please try again.", http.StatusBadRequest)
		return
	}
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		s.invoiceMessage(w, r, http.StatusTooManyRequests, "Too many requests from this connection. Please wait a few minutes and try again.")
		return
	}
	kind := r.PostForm.Get("kind")
	if kind != "transfer" && kind != "link" {
		s.invoiceMessage(w, r, http.StatusBadRequest, "This could not be sent. Please try again.")
		return
	}
	var amount int64
	if a := strings.TrimSpace(r.PostForm.Get("amount")); a != "" {
		k, ok := parseNaira(a)
		if !ok {
			s.invoiceMessage(w, r, http.StatusUnprocessableEntity, "Type the amount in naira, such as 5000 or 5,000.50.")
			return
		}
		amount = k
	}
	ok, err := s.InvoicePay.Request(r.Context(), invoicing.TokenHash(token), kind, amount)
	if err != nil {
		s.tryLater(w, r, "invoice payment", err)
		return
	}
	if !ok && amount > 0 {
		s.invoiceMessage(w, r, http.StatusUnprocessableEntity, "That amount cannot be paid on this invoice. Go back and check it.")
		return
	}
	// Refused otherwise (paid, limits): the page says why.
	http.Redirect(w, r, "/i/"+token, http.StatusSeeOther) //nolint:gosec // G710: a relative path; serveInvoice checked token against invoiceTokenRe
}

func (s *Server) invoiceMessage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.renderPrivate(w, r, status, "formclosed.html", closedData{pageData: pageData{Title: "Invoice", Lang: "en"}, Business: "Taskiem", Message: msg})
}

// parseNaira reads "5000", "5,000" or "5,000.50" as kobo.
func parseNaira(s string) (int64, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "₦"), "N")
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(whole) > 12 || len(frac) > 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	k := n * 100
	if frac != "" {
		if len(frac) == 1 {
			frac += "0"
		}
		f, err := strconv.ParseInt(frac, 10, 64)
		if err != nil || f < 0 {
			return 0, false
		}
		k += f
	}
	return k, k > 0
}
