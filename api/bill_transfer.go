package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
)

// Paying a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md): the buyer asks for an account number
// for a bill. The request crosses the link to the seller's invoice (a
// request on it, channel bill), so the seller's provider keys never leave
// the seller; the seller's side issues the number for what is due on the
// invoice, and it comes back on the bill with the link mirror, as the
// bill's payments do. One number is open for a bill at a time; a new one
// once it has expired, or for what is left after a part payment.

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
	t := b.Transfer
	asked := b.TransferAskedAt != nil && (t == nil || b.TransferAskedAt.After(t.CreatedAt))
	switch {
	case asked && b.TransferProblem != "":
		return &billTransferView{State: "failed", Problem: b.TransferProblem}
	case asked:
		return &billTransferView{State: "asking"}
	case t == nil:
		return nil
	}
	v := &billTransferView{BankName: t.BankName, AccountNumber: t.AccountNumber, AccountName: t.AccountName, AmountKobo: t.AmountKobo, ExpiresAt: t.ExpiresAt}
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
	ctx := r.Context()
	p := principalFrom(ctx)
	var out billWithWays
	status := http.StatusOK
	err := s.tx(r, func(tx pgx.Tx) error {
		b, err := links.GetBillTx(ctx, tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM bills WHERE id = $1 FOR UPDATE`, b.ID); err != nil {
			return err
		}
		switch {
		case b.Status == "void" || b.DueKobo <= 0:
			return refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
		case b.Disputed:
			return refusePay(http.StatusConflict, "disputed", "This bill is queried: pay once the query is settled.")
		case !b.PayByTransfer:
			return refusePay(http.StatusConflict, "no_transfer", b.SupplierName+" can't give an account number for one payment: pay on their checkout or from Taskiem.")
		}
		now := time.Now()
		if t := transferOf(b, now); t != nil && !in.Longer {
			// One number at a time: the open one again, or the one coming.
			open := t.State == "open" && t.AmountKobo == b.DueKobo && t.ExpiresAt != nil && t.ExpiresAt.After(now.Add(time.Minute))
			coming := t.State == "asking" && b.TransferAskedAt != nil && b.TransferAskedAt.After(now.Add(-2*time.Minute))
			if open || coming {
				out, err = s.billView(ctx, tx, p, b)
				if coming {
					status = http.StatusAccepted
				}
				return err
			}
		}
		if !s.limiter("bill-transfer:"+b.ID.String(), time.Minute/6, 6).Allow() {
			return refusePay(http.StatusTooManyRequests, "too_many", "Too many numbers asked for this bill. Try again in a while, or pay on their checkout.")
		}
		var source uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE bills SET transfer_asked_at = now(), transfer_problem = NULL WHERE id = $1 RETURNING source_id`, b.ID).Scan(&source); err != nil {
			return err
		}
		b.TransferAskedAt, b.TransferProblem = &now, ""
		if out, err = s.billView(ctx, tx, p, b); err != nil {
			return err
		}
		out.Transfer = &billTransferView{State: "asking"}
		// From here the scope is the seller's: the request is put on their
		// invoice, for this buyer's bill.
		detail, _ := json.Marshal(map[string]any{"bill": b.Reference})
		seller, err := links.EnterTx(ctx, tx, b.LinkID, "link.bill.transfer", detail, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		if err != nil {
			return err
		}
		x, err := s.Invoices.Get(ctx, tx, seller, source.String())
		if err != nil {
			return err
		}
		if buyer, ok := links.BuyerOf(x.Customer.Key); !ok || buyer != p.TenantID || !x.Payable() {
			return refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
		}
		reply := map[string]any{"buyer": p.TenantID.String(), "bill": b.ID.String(), "link": b.LinkID.String()}
		if in.Longer {
			reply["minutes"] = invoicing.LongestMinutes
		}
		raw, _ := json.Marshal(reply)
		var id *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT taskiem_invoice_request_put($1, $2, 'transfer', 0, 'bill', $3)`, seller, source, raw).Scan(&id); err != nil {
			return err
		}
		if id == nil {
			return refusePay(http.StatusConflict, "nothing_due", "Nothing is left to pay on this bill.")
		}
		status = http.StatusAccepted
		return nil
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
	writeJSON(w, status, out)
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
		return nil
	}
	if issueErr == nil && a.Status != "failed" {
		if _, err := s.mirrorInvoice(ctx, q.Tenant, q.Invoice); err != nil {
			return err
		}
		var ref string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT number FROM customer_invoices WHERE id = $1`, q.Invoice).Scan(&ref)
		}); err != nil {
			return err
		}
		amount := a.AmountKobo
		s.billCards(ctx, link, q.Tenant, buyer, ref, "transfer", false, &amount, "")
		return nil
	}
	words := "Their bank did not give a number just now. Try again, or pay another way."
	switch {
	case errors.Is(issueErr, invoicing.ErrLimit):
		words = "Too many numbers asked for this bill. Try again in an hour, or pay on their checkout."
	case errors.Is(issueErr, invoicing.ErrNoRail):
		words = "They can't give an account number just now: pay on their checkout or from Taskiem."
	case errors.Is(issueErr, invoicing.ErrState):
		words = "Nothing is left to pay on this bill."
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		if _, err := links.EnterTx(ctx, tx, link, "link.bill.transfer_failed", nil, links.Actor{Type: "system", ID: "invoices"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE bills SET transfer_problem = $3 WHERE tenant_id = $1 AND id = $2`, buyer, bill, words)
		return err
	})
}

func str(v any) string { s, _ := v.(string); return s }
