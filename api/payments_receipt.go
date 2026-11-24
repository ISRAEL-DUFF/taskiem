package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/pdf"
)

// A receipt to share (docs/spec/chat-pay-people.md, "After": "a receipt to
// share back in that chat: the person who sent the account details sees it
// was paid"; Settled 6: Send them a receipt, for a payee not on Taskiem).
// A paid payment's receipt is a card in the payer's own chat and a PDF.
// It says who paid whom, how much, what for, when, and the account's
// bank and last four digits; never who approved, the balance or the fee.
// The PDF is also at a link of its own, signed for that payment alone, for
// the payer to send to the person paid.

// receiptLinkDays is how long a receipt's own link works after it was
// paid: long enough to send it and for it to be opened, not for ever.
const receiptLinkDays = 90

// receiptView is a payment's receipt.
type receiptView struct {
	Business  string    `json:"business"`
	Payee     string    `json:"payee"`
	Bank      string    `json:"bank_name"`
	Account   string    `json:"account"`
	Amount    int64     `json:"amount_kobo"`
	For       string    `json:"for"`
	PaidAt    time.Time `json:"paid_at"`
	Reference string    `json:"reference"`
	// Route: "Taskiem wallet" when it went wallet to wallet.
	Route string `json:"route"`
}

// receiptOf is payment x's receipt, paid by business.
func receiptOf(business string, x payment) receiptView {
	at := x.CreatedAt
	if x.FinishedAt != nil {
		at = *x.FinishedAt
	}
	what := paymentPurposeWords(x.Purpose)
	if x.Purpose == "other" && x.PurposeNote != "" {
		what = x.PurposeNote
	}
	route := "Bank transfer"
	if x.Route == "wallet" {
		route = "Taskiem wallet"
	}
	return receiptView{Business: business, Payee: payeeWords(x), Bank: x.BankName, Account: x.Account, Amount: x.AmountKobo, For: what,
		PaidAt: at, Reference: strings.ToUpper(strings.ReplaceAll(x.ID.String(), "-", "")[:12]), Route: route}
}

// receiptToken is the link's token for a payment's receipt: the business
// and the payment, signed.
func (s *Server) receiptToken(tenant, id uuid.UUID) string {
	key := s.chatSigner().Key
	if len(key) < 32 {
		return ""
	}
	body := append(tenant[:], id[:]...)
	m := hmac.New(sha256.New, key)
	m.Write([]byte("pay-receipt:"))
	m.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// receiptURL is the link to a payment's receipt PDF; "" without a public
// address or a key to sign it with.
func (s *Server) receiptURL(tenant, id uuid.UUID) string {
	tok := s.receiptToken(tenant, id)
	if tok == "" || s.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(s.PublicURL, "/") + "/v1/pay-receipts/" + tok + "/pdf"
}

// receiptFromToken reads a receipt link's token: the business and the
// payment, when its signature holds.
func (s *Server) receiptFromToken(tok string) (tenant, id uuid.UUID, ok bool) {
	body, mac, found := strings.Cut(tok, ".")
	if !found {
		return uuid.Nil, uuid.Nil, false
	}
	b, err1 := base64.RawURLEncoding.Strict().DecodeString(body)
	if err1 != nil || len(b) != 32 {
		return uuid.Nil, uuid.Nil, false
	}
	copy(tenant[:], b[:16])
	copy(id[:], b[16:])
	want := s.receiptToken(tenant, id)
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(body+"."+mac)) != 1 {
		return uuid.Nil, uuid.Nil, false
	}
	return tenant, id, true
}

// paidReceiptTx reads payment id's receipt in tenant's scope; only a paid
// payment has one.
func paidReceiptTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, owner bool) (receiptView, payment, error) {
	x, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1 AND (NOT personal OR $2)`, id, owner))
	if err != nil {
		return receiptView{}, x, err
	}
	if x.Status != "paid" {
		return receiptView{}, x, refusePay(http.StatusConflict, "not_paid", "A receipt is for a payment the bank received: this one was "+paymentStatusWords(x.Status)+".")
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT t.name FROM tenants t JOIN payments p ON p.tenant_id = t.id WHERE p.id = $1`, id).Scan(&business); err != nil {
		return receiptView{}, x, err
	}
	return receiptOf(business, x), x, nil
}

// getPaymentReceipt is GET /v1/payments/{id}/receipt: the receipt, and
// the link to send it by.
func (s *Server) getPaymentReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	var rc receiptView
	var x payment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		rc, x, err = paidReceiptTx(r.Context(), tx, id, isOwnerOf(p))
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	// An owner's own payment has no link to send: it is theirs alone, and
	// the link would answer nothing (the public receipt never reads one).
	url := ""
	if !x.Personal {
		url = s.receiptURL(p.TenantID, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipt": rc, "url": url})
}

// getPaymentReceiptPDF is GET /v1/payments/{id}/receipt/pdf.
func (s *Server) getPaymentReceiptPDF(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var rc receiptView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		rc, _, err = paidReceiptTx(r.Context(), tx, id, isOwnerOf(principalFrom(r.Context())))
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	s.writeReceiptPDF(w, r, rc)
}

// publicPayReceipt is GET /v1/pay-receipts/{token}/pdf: a receipt sent to
// the person paid, by its signed link, with no session.
func (s *Server) publicPayReceipt(w http.ResponseWriter, r *http.Request) {
	tenant, id, ok := s.receiptFromToken(chi.URLParam(r, "token"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !s.limiter("pay-receipt:"+clientIP(r), time.Minute, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down")
		return
	}
	var rc receiptView
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		rc, _, err = paidReceiptTx(r.Context(), tx, id, false)
		return err
	})
	var pr payRefused
	// The link works while the payment stands paid (a payment the bank
	// sent back has no receipt), for receiptLinkDays after it was paid.
	if err == nil && time.Since(rc.PaidAt) > receiptLinkDays*24*time.Hour {
		err = pgx.ErrNoRows
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &pr) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeReceiptPDF(w, r, rc)
}

func (s *Server) writeReceiptPDF(w http.ResponseWriter, r *http.Request, rc receiptView) {
	out, err := renderPayReceipt(rc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="receipt-`+rc.Reference+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) //nolint:gosec // a PDF made here from the payment's own record, sent as application/pdf with nosniff
}

// renderPayReceipt draws a payment's receipt on an A5 page.
func renderPayReceipt(rc receiptView) ([]byte, error) {
	d, err := pdf.New("Receipt " + rc.Reference)
	if err != nil {
		return nil, err
	}
	const left, right = 36.0, pdf.A5Width - 36.0
	width := right - left
	y := 54.0
	for _, l := range d.Wrap(rc.Business, 16, true, width-120) {
		d.Text(left, y, 16, l, true)
		y += 20
	}
	d.Text(left, y, 10, "Payment receipt", false)
	d.TextRight(right, 54, 11, rc.Reference, true)
	d.TextRight(right, 70, 9, rc.PaidAt.In(lagosTZ).Format("2 Jan 2006, 15:04"), false)
	y += 14
	d.Line(left, y, right, y, 0.8)
	y += 26
	d.Text(left, y, 9, "Paid to", false)
	y += 16
	for _, l := range d.Wrap(rc.Payee, 13, true, width) {
		d.Text(left, y, 13, l, true)
		y += 16
	}
	d.Text(left, y, 10, rc.Bank+" · account ending "+strings.TrimPrefix(rc.Account, "••"), false)
	y += 30
	d.Text(left, y, 9, "Amount", false)
	d.TextRight(right, y, 18, invoicing.Naira(rc.Amount), true)
	y += 26
	for _, row := range [][2]string{{"What for", rc.For}, {"How", rc.Route}, {"Status", "Received by the bank"}} {
		d.Text(left, y, 10, row[0], false)
		d.TextRight(right, y, 10, row[1], true)
		y += 18
	}
	y += 10
	d.Line(left, y, right, y, 0.4)
	y += 18
	d.Gray(0.35)
	d.Text(left, y, 8, fmt.Sprintf("Sent with Taskiem by %s.", rc.Business), false)
	d.Gray(0)
	return d.Bytes()
}

// sharePaymentReceipt is POST /v1/payments/{id}/share {where}: the
// receipt sent back into a chat: a card in the person's own Taskiem chat
// ("taskiem"), or a message from them, with the receipt's link, in their
// conversation with a linked business (the person who sent the account
// details sees it was paid).
func (s *Server) sharePaymentReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Where string `json:"where"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if p.UserID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	where, err := s.diaryWhere(ctx, p.TenantID, p.UserID, in.Where)
	if err != nil || where == nil {
		if err == nil {
			err = refusePay(http.StatusBadRequest, "where", "Say which chat to send it to.")
		}
		s.payFail(w, r, err)
		return
	}
	var rc receiptView
	var x payment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		rc, x, err = paidReceiptTx(ctx, tx, id, isOwnerOf(p))
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	text := fmt.Sprintf("Paid %s %s for %s on %s · %s %s.", rc.Payee, invoicing.Naira(rc.Amount), rc.For, rc.PaidAt.In(lagosTZ).Format("2 Jan 2006"), rc.Bank, rc.Account)
	if *where == "taskiem" {
		if err := s.tx(r, func(tx pgx.Tx) error {
			env, err := s.sealMemberText(ctx, tx, p.TenantID, p.UserID, text)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at, payment_id)
				VALUES ($1, $2, $3, 'web', false, 'receipt', $4, '{}', now() + $5::interval, $6)`,
				uuid.Must(uuid.NewV7()), p.TenantID, p.UserID, env, s.chatHistoryTTL().String(), x.ID); err != nil {
				return err
			}
			return auditTx(r, tx, "payment.receipt_share", id.String(), map[string]any{"where": "taskiem"})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"shared": "taskiem"})
		return
	}
	if !p.Can(PermChatTrade) {
		s.fail(w, r, errForbidden)
		return
	}
	link := uuid.MustParse(*where)
	if u := s.receiptURL(p.TenantID, id); u != "" {
		text += " Receipt: " + u
	}
	m, err := s.sendLink(ctx, p, link, text, links.SendOpts{})
	if err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "payment.receipt_share", id.String(), map[string]any{"where": "link", "link": link})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"shared": "link", "message": m})
}
