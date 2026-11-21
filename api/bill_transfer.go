package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Paying a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md): the buyer asks for an account number
// for a bill. The request crosses the link to the seller's invoice (a
// request on it, channel bill), so the seller's provider keys never leave
// the seller; the seller's side issues the number for what is due on the
// invoice, and it comes back on the bill with the link mirror, as the
// bill's payments do. One number is open for a bill at a time; a new one
// once it has expired, or for what is left after a part payment.

// askingFor is how long a bill waits for the seller's side to answer.
const askingFor = 3 * time.Minute

// billTransferView is a bill's account number as the buyer sees it.
type billTransferView struct {
	// State: asking, open, paid, part (less than the number's amount
	// arrived), expired, or failed.
	State         string     `json:"state"`
	BankName      string     `json:"bank_name,omitempty"`
	AccountNumber string     `json:"account_number,omitempty"`
	AccountName   string     `json:"account_name,omitempty"`
	AmountKobo    int64      `json:"amount_kobo,omitempty"`
	ReceivedKobo  int64      `json:"received_kobo,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	// Problem: why a number could not be had, in plain words.
	Problem string `json:"problem,omitempty"`
	// Next: the same seller's next bill still to pay, once this is paid.
	Next string `json:"next,omitempty"`
}

// billWithWays is a bill with how it can be paid and its account number.
type billWithWays struct {
	links.Bill
	Transfer  *billTransferView `json:"transfer"`
	WaysToPay []string          `json:"ways_to_pay"`
}

// transferOf is the bill's account number in its state at now; nil when
// none was asked for.
func transferOf(b links.Bill, now time.Time) *billTransferView {
	if b.Status == "void" {
		return nil
	}
	t := b.Transfer
	// Asked and not answered yet (the answer clears it); an answer that
	// never came is given up on after a few minutes.
	switch {
	case b.TransferProblem != "":
		return &billTransferView{State: "failed", Problem: b.TransferProblem}
	case b.TransferAskedAt != nil && b.TransferAskedAt.After(now.Add(-askingFor)):
		return &billTransferView{State: "asking"}
	case b.TransferAskedAt != nil && (t == nil || t.CreatedAt.Before(*b.TransferAskedAt)):
		return &billTransferView{State: "failed", Problem: "Their bank did not answer. Try again, or pay another way."}
	case t == nil:
		return nil
	}
	v := &billTransferView{BankName: t.BankName, AccountNumber: t.AccountNumber, AccountName: t.AccountName, AmountKobo: t.AmountKobo, ExpiresAt: t.ExpiresAt, PaidAt: t.PaidAt}
	status := t.Status
	if b.Status == "paid" {
		// Paid in full, by this number or otherwise: nothing more to send.
		status = "paid"
	}
	switch status {
	case "requested":
		v = &billTransferView{State: "asking"}
	case "paid":
		v.State = "paid"
		if b.DueKobo > 0 && b.DueKobo < t.AmountKobo {
			v.State, v.ReceivedKobo = "part", t.AmountKobo-b.DueKobo
		}
	case "open":
		v.State = "open"
		if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
			v.State = "expired"
		}
	default:
		v = &billTransferView{State: "failed", Problem: "Their bank did not give a number. Try again, or pay another way."}
	}
	return v
}

// billView is a bill as its API answers it: the account number, for those
// who may pay bills only, and the ways it can be paid.
func (s *Server) billView(ctx context.Context, tx pgx.Tx, p *Principal, b links.Bill) (billWithWays, error) {
	out := billWithWays{Bill: b, WaysToPay: []string{}}
	payable := b.DueKobo > 0 && !b.Disputed && b.Status != "void"
	if payable && b.PayByTransfer {
		out.WaysToPay = append(out.WaysToPay, "transfer")
	}
	if payable {
		st, err := billPaySettingsTx(ctx, tx)
		if err != nil {
			return out, err
		}
		if st.WorkflowID != nil {
			out.WaysToPay = append(out.WaysToPay, "taskiem")
		}
		if b.PayLink != "" {
			out.WaysToPay = append(out.WaysToPay, "checkout")
		}
	}
	if !p.Can(PermInvoiceManage) {
		return out, nil
	}
	out.Transfer = transferOf(b, time.Now())
	if out.Transfer != nil && out.Transfer.State == "paid" {
		// The same seller's next bill to pay: "Pay the next one".
		var next uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM bills WHERE supplier_tenant = $1 AND id <> $2 AND NOT voided AND NOT disputed AND paid_kobo < amount_kobo
			ORDER BY due_on NULLS LAST, created_at LIMIT 1`, b.Supplier, b.ID).Scan(&next)
		if err == nil {
			out.Transfer.Next = next.String()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	return out, nil
}

// askBillTransfer is POST /v1/bills/{id}/transfer {longer?}: the bill's
// open account number (200), or one asked for (202, state asking). longer
// asks for the longest the seller's provider gives.
func (s *Server) askBillTransfer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Longer bool `json:"longer"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var out billWithWays
	var asked bool
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, asked, err = s.askTransferTx(r.Context(), tx, principalFrom(r.Context()), chi.URLParam(r, "id"), in.Longer, uuid.Nil)
		return err
	})
	var oo *links.OtherOffError
	if errors.As(err, &oo) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "They have turned off invoices: pay on their checkout.", "code": "no_transfer"})
		return
	}
	if err != nil {
		s.payFail(w, r, billErr(err))
		return
	}
	status := http.StatusOK
	if asked {
		status = http.StatusAccepted
	}
	writeJSON(w, status, out)
}

// askTransferTx is a bill's open account number, or one asked for (asked
// true): one at a time; a new one once it has expired. tell, when set, is
// a person told on their chats when it arrives (they asked in a chat).
func (s *Server) askTransferTx(ctx context.Context, tx pgx.Tx, p *Principal, ref string, longer bool, tell uuid.UUID) (billWithWays, bool, error) {
	var out billWithWays
	b, err := links.GetBillTx(ctx, tx, ref)
	if err != nil {
		return out, false, err
	}
	// Locked, then read again: two asks at once put one request.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM bills WHERE id = $1 FOR UPDATE`, b.ID); err != nil {
		return out, false, err
	}
	if b, err = links.GetBillTx(ctx, tx, b.ID.String()); err != nil {
		return out, false, err
	}
	switch {
	case b.Status == "void" || b.DueKobo <= 0:
		return out, false, refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
	case b.Disputed:
		return out, false, refusePay(http.StatusConflict, "disputed", "This bill is queried: pay once the query is settled.")
	case b.PayingKobo > 0 || b.UncountedKobo > 0:
		return out, false, refusePay(http.StatusConflict, "paying", "A payment from Taskiem to "+b.SupplierName+" is under way: wait until they have counted it.")
	case !b.PayByTransfer:
		return out, false, refusePay(http.StatusConflict, "no_transfer", b.SupplierName+" can't give an account number for one payment: pay on their checkout or from Taskiem.")
	}
	now := time.Now()
	if t := transferOf(b, now); t != nil && !longer {
		// One number at a time: the open one again, or the one coming.
		open := t.State == "open" && t.AmountKobo == b.DueKobo && t.ExpiresAt != nil && t.ExpiresAt.After(now.Add(time.Minute))
		coming := t.State == "asking"
		if open || coming {
			out, err = s.billView(ctx, tx, p, b)
			return out, coming, err
		}
	}
	if !s.limiter("bill-transfer:"+b.ID.String(), time.Minute/6, 6).Allow() {
		return out, false, refusePay(http.StatusTooManyRequests, "too_many", "Too many numbers asked for this bill. Try again in a while, or pay on their checkout.")
	}
	var source uuid.UUID
	if err := tx.QueryRow(ctx, `UPDATE bills SET transfer_asked_at = now(), transfer_problem = NULL WHERE id = $1 RETURNING source_id`, b.ID).Scan(&source); err != nil {
		return out, false, err
	}
	b.TransferAskedAt, b.TransferProblem = &now, ""
	if out, err = s.billView(ctx, tx, p, b); err != nil {
		return out, false, err
	}
	out.Transfer = &billTransferView{State: "asking"}
	// From here the scope is the seller's: the request is put on their
	// invoice, for this buyer's bill.
	detail, _ := json.Marshal(map[string]any{"bill": b.Reference})
	seller, err := links.EnterTx(ctx, tx, b.LinkID, "link.bill.transfer", detail, links.Actor{Type: p.ActorType(), ID: p.Actor()})
	if err != nil {
		return out, false, err
	}
	x, err := s.Invoices.Get(ctx, tx, seller, source.String())
	if err != nil {
		return out, false, err
	}
	if buyer, ok := links.BuyerOf(x.Customer.Key); !ok || buyer != p.TenantID || !x.Payable() {
		return out, false, refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
	}
	reply := map[string]any{"buyer": p.TenantID.String(), "bill": b.ID.String(), "link": b.LinkID.String()}
	if longer {
		reply["minutes"] = invoicing.LongestMinutes
	}
	if tell != uuid.Nil {
		reply["tell"] = tell.String()
	}
	raw, _ := json.Marshal(reply)
	var id *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_invoice_request_put($1, $2, 'transfer', 0, 'bill', $3)`, seller, source, raw).Scan(&id); err != nil {
		return out, false, err
	}
	if id == nil {
		return out, false, refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
	}
	return out, true, nil
}

// getBillTransfer is GET /v1/bills/{id}/transfer: the bill with its
// account number.
func (s *Server) getBillTransfer(w http.ResponseWriter, r *http.Request) {
	var out billWithWays
	if err := s.tx(r, func(tx pgx.Tx) error {
		b, err := links.GetBillTx(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		out, err = s.billView(r.Context(), tx, principalFrom(r.Context()), b)
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// billTransferAnswered is told when the seller's side has issued (or
// could not issue) a number a bill asked for: the number reaches the bill
// with the mirror, and both chats get the card; a number that could not be
// had is said on the bill.
func (s *Server) billTransferAnswered(ctx context.Context, q invoicing.Request, a invoicing.Account, issueErr error) error {
	buyer, err1 := uuid.Parse(str(q.ReplyTo["buyer"]))
	bill, err2 := uuid.Parse(str(q.ReplyTo["bill"]))
	link, err3 := uuid.Parse(str(q.ReplyTo["link"]))
	if err1 != nil || err2 != nil || err3 != nil {
		return nil //nolint:nilerr // not a bill's request: nothing to answer
	}
	tell, _ := uuid.Parse(str(q.ReplyTo["tell"]))
	ok := issueErr == nil && a.Status != "failed" && a.Status != "unavailable"
	problem := ""
	if !ok {
		problem = "Their bank did not give a number just now. Try again, or pay another way."
		switch {
		case errors.Is(issueErr, invoicing.ErrLimit):
			problem = "Too many numbers asked for this bill. Try again in an hour, or pay on their checkout."
		case errors.Is(issueErr, invoicing.ErrNoRail):
			problem = "They can't give an account number just now: pay on their checkout or from Taskiem."
		case errors.Is(issueErr, invoicing.ErrState):
			problem = "Nothing is left to pay on this bill."
		}
	}
	var ref string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT number FROM customer_invoices WHERE id = $1`, q.Invoice).Scan(&ref)
	}); err != nil {
		return err
	}
	if ok {
		// The number reaches the bill with the mirror.
		if _, err := s.mirrorInvoice(ctx, q.Tenant, q.Invoice); err != nil {
			return err
		}
	}
	// The ask is answered: the bill stops waiting, with why when no number
	// came.
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		if _, err := links.EnterTx(ctx, tx, link, "link.bill.transfer_answered", nil, links.Actor{Type: "system", ID: "invoices"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE bills SET transfer_asked_at = NULL, transfer_problem = NULLIF($3, '') WHERE tenant_id = $1 AND id = $2`, buyer, bill, problem)
		return err
	}); err != nil {
		// The link ended, or the buyer turned Money off: nothing is said.
		return err
	}
	if !ok {
		if tell != uuid.Nil {
			s.tellAbout(ctx, buyer, []uuid.UUID{tell}, "bill:"+bill.String(), "Pay "+ref+" by transfer", problem, nil, nil)
		}
		return nil
	}
	amount := a.AmountKobo
	s.billCards(ctx, link, q.Tenant, buyer, ref, "transfer", false, &amount, "")
	if tell != uuid.Nil && a.AccountNumber != "" {
		s.tellAbout(ctx, buyer, []uuid.UUID{tell}, "bill:"+bill.String(), "Pay "+ref+" by transfer",
			transferWords(lang.EN, ref, a.AmountKobo, a.BankName, a.AccountNumber, a.AccountName, a.ExpiresAt), nil, nil)
	}
	return nil
}

func str(v any) string { s, _ := v.(string); return s }

// transferWords say an account number to pay into, in a chat's plain
// lines: how much exactly, where, and for how long.
func transferWords(l lang.Tag, ref string, amount int64, bank, number, name string, until *time.Time) string {
	to := number + ", " + bank
	if name != "" {
		to += ", " + name
	}
	good := ""
	if until != nil {
		if m := int(time.Until(*until).Round(time.Minute) / time.Minute); m > 0 {
			good = " " + tr(l, "bill.transfer.good_for", "minutes", strconv.Itoa(m))
		}
	}
	return tr(l, "bill.transfer.send", "amount", invoicing.Naira(amount), "to", whatsapp.SafeText(to), "reference", ref) + good + " " + tr(l, "bill.transfer.next")
}

// waPayBill answers "pay INV-0042" in a chat: the bill's account number in
// plain lines, at once when one is open, else within seconds of asking.
func (s *Server) waPayBill(ctx context.Context, c *chat, ref string) (bool, error) {
	if !c.p.Can(PermInvoiceManage) {
		return true, c.say(ctx, s, c.t("bill.denied", "org", c.tenant.Name))
	}
	var out billWithWays
	var asked bool
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		out, asked, err = s.askTransferTx(ctx, tx, c.p, ref, false, c.p.UserID)
		return err
	})
	var pr payRefused
	var oo *links.OtherOffError
	switch {
	case errors.Is(err, links.ErrNotFound):
		return true, c.say(ctx, s, c.t("bill.not_found", "reference", whatsapp.SafeText(ref)))
	case errors.Is(err, links.ErrAmbiguous):
		return true, c.say(ctx, s, whatsapp.SafeText(strings.TrimPrefix(err.Error(), links.ErrAmbiguous.Error()+": ")))
	case errors.As(err, &pr):
		return true, c.say(ctx, s, whatsapp.SafeText(pr.msg))
	case errors.As(err, &oo):
		return true, c.say(ctx, s, c.t("bill.transfer.off"))
	case err != nil:
		return true, err
	}
	if t := out.Transfer; !asked && t != nil && t.State == "open" {
		return true, c.say(ctx, s, transferWords(c.lang.Tag, out.Reference, t.AmountKobo, t.BankName, t.AccountNumber, t.AccountName, t.ExpiresAt))
	}
	return true, c.say(ctx, s, c.t("bill.transfer.asking", "reference", out.Reference))
}
