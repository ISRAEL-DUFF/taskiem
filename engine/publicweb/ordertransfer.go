package publicweb

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Paying an order by bank transfer from its status page (docs/spec/
// iswallet-account-numbers.md N3), as an invoice's page does: Pay by bank
// transfer (POST /o/{reference}/pay?k=) asks for a one-time account
// number, which the api role issues on the business's rail, and the page
// shows it with how long it is valid. The public role only queues the
// request (taskiem_public_order_request). The page refreshes itself while
// it waits and while the account is open, with no script.

// OrderPayment is what an order's status page may show of paying by
// transfer: the latest account number, the limits, whether a card page is
// coming (the form takes card payments through Paystack), and until when
// the order is held.
type OrderPayment struct {
	Status        string // "" (none yet), requested, open, failed, paid, returned
	AmountKobo    int64
	AccountNumber string
	BankName      string
	AccountName   string
	ExpiresAt     *time.Time
	OnLate        string
	OnWrongAmount string
	Waiting       bool
	LastHour      int
	Issued        int
	Card          bool
	HeldUntil     *time.Time
}

// OrderPay reads an order's transfer and queues requests for one.
type OrderPay interface {
	// Payment is ok false when the reference and key name no order.
	Payment(ctx context.Context, reference string, statusHash []byte) (OrderPayment, bool, error)
	// Request queues an account number; false when the order refuses one
	// (not waiting for its payment, or past the limits).
	Request(ctx context.Context, reference string, statusHash []byte) (bool, error)
}

// DBOrderPay is OrderPay over the public role's functions.
type DBOrderPay struct{ Pool *pgxpool.Pool }

// Payment implements OrderPay.
func (d DBOrderPay) Payment(ctx context.Context, reference string, statusHash []byte) (OrderPayment, bool, error) {
	var p OrderPayment
	err := d.Pool.QueryRow(ctx, `SELECT COALESCE(status, ''), COALESCE(amount_kobo, 0), COALESCE(account_number, ''), COALESCE(bank_name, ''),
		COALESCE(account_name, ''), expires_at, COALESCE(on_late, ''), COALESCE(on_wrong_amount, ''), waiting, issued_last_hour, issued, card, held_until
		FROM taskiem_public_order_payment($1, $2)`, reference, statusHash).Scan(&p.Status, &p.AmountKobo, &p.AccountNumber, &p.BankName, &p.AccountName,
		&p.ExpiresAt, &p.OnLate, &p.OnWrongAmount, &p.Waiting, &p.LastHour, &p.Issued, &p.Card, &p.HeldUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrderPayment{}, false, nil
	}
	return p, err == nil, err
}

// Request implements OrderPay.
func (d DBOrderPay) Request(ctx context.Context, reference string, statusHash []byte) (bool, error) {
	var id *string
	if err := d.Pool.QueryRow(ctx, `SELECT taskiem_public_order_request($1, $2)::text`, reference, statusHash).Scan(&id); err != nil {
		return false, err
	}
	return id != nil, nil
}

// transferView is the status page's Pay by bank transfer.
type transferView struct {
	Account *accountView
	Waiting string
	Note    string
	// Label is the button's ("" when none may be asked for); Action the
	// form's target.
	Label, Action string
	Primary       bool
}

// transferOnly reports whether a waiting order is paid by transfer and
// not (or not yet) by card: its form takes no card payments, or the
// customer already asked for an account number.
func (d *statusData) transferOnly() bool {
	return d.pay != nil && (!d.pay.Card || d.pay.Status != "" || d.pay.Waiting)
}

// heldLine says until when a waiting order (or booking's time) is held.
func (d *statusData) heldLine(booking bool) string {
	what := "Your order is held"
	if booking {
		what = "Your time is held"
	}
	if d.pay != nil && d.pay.HeldUntil != nil {
		return what + " until " + d.pay.HeldUntil.In(lagos).Format("15:04") + " (Lagos time)."
	}
	return what + " for 30 minutes."
}

// payByTransfer adds Pay by bank transfer to an order waiting for its
// payment: the account number while one is open, the button to get one,
// and how often the page refreshes. It leads when there is no card page.
func (s *Server) payByTransfer(d *statusData, ref, key, prefix string) {
	p := d.pay
	if p == nil || d.PayURL == "" && !d.transferOnly() {
		return
	}
	now := s.now()
	t := &transferView{Label: "Pay by bank transfer", Action: statusLink(prefix, ref, key, "pay"), Primary: d.PayURL == ""}
	if d.PayURL != "" {
		t.Label = "Pay by bank transfer instead"
	}
	switch {
	case p.Waiting || p.Status == "requested" && p.AccountNumber == "":
		t.Waiting, d.RefreshAfter, t.Label = "Getting your account number…", 2, ""
	case p.Status == "open" && p.ExpiresAt != nil && p.ExpiresAt.After(now):
		left := p.ExpiresAt.Sub(now).Round(time.Minute)
		t.Account = &accountView{Number: p.AccountNumber, Bank: p.BankName, Name: p.AccountName, Amount: invoicing.Naira(p.AmountKobo),
			Until: "Valid until " + p.ExpiresAt.In(lagos).Format("15:04") + " (Lagos time)", Left: minutesLeft(left),
			Late: lateWords[p.OnLate], Wrong: wrongWords[p.OnWrongAmount]}
		t.Label, t.Primary, d.RefreshAfter = "Get a new account number", false, 30
	case p.Status == "open" || p.Status == "requested":
		t.Note, t.Label = "The last account number has expired. If you already paid into it, this page will show your payment.", "Get a new account number"
	case p.Status == "failed":
		t.Note = "We could not get an account number just now. Please try again in a few minutes."
	}
	if p.LastHour >= invoicing.MaxPerHour || p.Issued >= invoicing.MaxPerInvoice {
		t.Label = ""
		if t.Account == nil && t.Waiting == "" {
			t.Note = "Too many account numbers were asked for this order. Please try again in an hour, or contact " + d.Business + "."
		}
	}
	d.Transfer = t
}

// serveOrderPay serves POST /o/{reference}/pay?k=: it queues the request
// for an account number and sends the customer back to the status page,
// which waits for it.
func (s *Server) serveOrderPay(w http.ResponseWriter, r *http.Request, site Site, ref string, embed bool) {
	if !allowed(w, r, http.MethodPost) {
		return
	}
	key := r.URL.Query().Get("k")
	if _, ok := s.statusOf(w, r, site, ref, key); !ok {
		return
	}
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		s.page(w, r, http.StatusTooManyRequests, "unavailable", pageData{Title: "Too many requests"})
		return
	}
	if s.OrderPay != nil {
		if _, err := s.OrderPay.Request(r.Context(), ref, storefront.StatusHash(key)); err != nil {
			s.tryLater(w, r, "order payment", err)
			return
		}
	}
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	// Refused (paid, expired, limits): the page says why.
	http.Redirect(w, r, statusLink(prefix, ref, key, ""), http.StatusSeeOther) //nolint:gosec // G710: a relative path built from a reference statusOf found
}
