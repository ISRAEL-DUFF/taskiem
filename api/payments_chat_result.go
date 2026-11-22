package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Results back in the same chat (docs/spec/chat-pay-people.md, "WhatsApp,
// Telegram and Slack"): a payment prepared from the chat that ends paid,
// failed or refused is told to the person who prepared it, on the first
// chat app they linked (WhatsApp, then Telegram, then Slack): "Paid Chidi
// Okeke ₦45,000. Received by the bank." A batch prepared from the chat is
// told once, when the last of its rows ends. The outbox row names the
// payment or the batch only; the words are made from it when sent.

// Outbox kinds of a payment's end.
const (
	outboxPayment      = "payment"
	outboxPaymentBatch = "payment_batch"
)

// payEnded are the statuses a payment's end is told for.
var payEnded = []string{"paid", "failed", "rejected"}

// chatPayResultTx queues, in the transaction that recorded payment id's
// end, the message telling its preparer: for one started from the chat,
// once; for a row of a batch, once for the batch, when none of its rows
// is still to end.
func chatPayResultTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	var from, by, status string
	var batch *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT started_from, prepared_by, status, batch_id FROM payments WHERE id = $1`, id).Scan(&from, &by, &status, &batch); err != nil {
		return err
	}
	// Prepared by a person (not a workflow), from the chat, and ended.
	user, perr := uuid.Parse(by)
	if from != "chat" || perr != nil || !slices.Contains(payEnded, status) {
		return nil //nolint:nilerr // prepared by no person: nobody to tell
	}
	it := whatsapp.OutboxItem{Tenant: tenant, Kind: outboxPayment, User: user, Payment: &id}
	key := "payment:" + id.String()
	if batch != nil {
		// The batch's rows end in their own transactions: the batch is
		// held while one looks at the others, so the last to end sees
		// every other's end.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM payment_batches WHERE id = $1 FOR UPDATE`, *batch); err != nil {
			return err
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments WHERE batch_id = $1 AND status IN ('prepared', 'sending', 'waiting_ok', 'unknown'))`, *batch).Scan(&open); err != nil {
			return err
		}
		if open {
			return nil
		}
		it.Kind, it.Payment, it.Batch, key = outboxPaymentBatch, nil, batch, "payment_batch:"+batch.String()
	}
	ch, err := firstChatTx(ctx, tx, user)
	if err != nil || ch == "" {
		return err
	}
	it.Channel = ch
	_, err = whatsapp.Enqueue(ctx, tx, it, key)
	return err
}

// chatBatchClosedTx tells a chat batch's result when its last open rows
// were let go (cancelled) rather than ended by the bank: the rows that
// went are told, as when the last of them ends. Nothing when no row went.
func chatBatchClosedTx(ctx context.Context, tx pgx.Tx, tenant, batch uuid.UUID) error {
	var row uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM payments WHERE batch_id = $1 AND status = ANY ($2) ORDER BY finished_at DESC NULLS LAST LIMIT 1`, batch, payEnded).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return chatPayResultTx(ctx, tx, tenant, row)
}

// firstChatTx is the first chat app user linked, in the order Tell me
// messages pick (connectors/notify: WhatsApp, then Telegram), then Slack;
// "" when none.
func firstChatTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) (string, error) {
	for _, q := range []struct{ channel, sql string }{
		{"whatsapp", `SELECT EXISTS (SELECT 1 FROM taskiem_wa_numbers($1))`},
		{"telegram", `SELECT EXISTS (SELECT 1 FROM taskiem_tg_accounts($1))`},
		{"slack", `SELECT EXISTS (SELECT 1 FROM slack_bindings WHERE verified_at IS NOT NULL AND user_id = ANY ($1))`},
	} {
		var linked bool
		if err := tx.QueryRow(ctx, q.sql, []uuid.UUID{user}).Scan(&linked); err != nil {
			return "", err
		}
		if linked {
			return q.channel, nil
		}
	}
	return "", nil
}

// payeeWords is who a payment went to: the bank's name for the account
// when it is known, else the payee's.
func payeeWords(x payment) string {
	if x.AccountName != nil && strings.TrimSpace(*x.AccountName) != "" {
		return *x.AccountName
	}
	return x.Payee
}

// payEndWords say how one payment ended.
func payEndWords(l lang.Tag, x payment) string {
	reason := ""
	if x.Reason != nil {
		reason = strings.TrimSpace(*x.Reason)
	}
	if reason == "" {
		reason = tr(l, "wa.pay.result.no_reason")
	}
	to, amount := payeeWords(x), invoicing.Naira(x.AmountKobo)
	switch x.Status {
	case "paid":
		return tr(l, "wa.pay.result.paid", "to", to, "amount", amount)
	case "rejected":
		return tr(l, "wa.pay.result.rejected", "to", to, "amount", amount)
	}
	return tr(l, "wa.pay.result.failed", "to", to, "amount", amount, "reason", reason)
}

// batchEndWords say how a batch's rows ended: how many were paid and how
// much, then who was not and why.
func batchEndWords(l lang.Tag, b batchView) (string, string) {
	var count, paid int
	var paidKobo int64
	var not []string
	for _, x := range b.Payments {
		switch x.Status {
		case "paid":
			count++
			paid++
			paidKobo += x.AmountKobo
		case "failed", "rejected":
			count++
			why := tr(l, "wa.pay.result.no_reason")
			if x.Status == "rejected" {
				why = tr(l, "wa.pay.result.declined")
			}
			if x.Reason != nil && strings.TrimSpace(*x.Reason) != "" {
				why = strings.TrimSpace(*x.Reason)
			}
			not = append(not, payeeWords(x)+", "+why)
		case "cancelled":
			// Let go before it was sent (the rest of the list let go).
			count++
			not = append(not, payeeWords(x)+", "+tr(l, "wa.pay.result.let_go"))
		}
	}
	head := tr(l, "wa.pay.result.batch", "paid", strconv.Itoa(paid), "count", strconv.Itoa(count), "amount", invoicing.Naira(paidKobo))
	switch {
	case paid == count && count == 1:
		head = tr(l, "wa.pay.result.batch_one", "amount", invoicing.Naira(paidKobo))
	case paid == count:
		head = tr(l, "wa.pay.result.batch_all", "count", strconv.Itoa(count), "amount", invoicing.Naira(paidKobo))
	}
	if len(not) == 0 {
		return head, ""
	}
	return head, tr(l, "wa.pay.result.batch_not", "count", strconv.Itoa(len(not)), "who", strings.Join(not, "; "))
}

// sendPayResultItem sends a queued payment's (or batch's) end to its
// preparer, made from the payments as they stand.
func (s *Server) sendPayResultItem(ctx context.Context, it whatsapp.OutboxItem) error {
	var business string
	var one payment
	var batch batchView
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		switch {
		case it.Kind == outboxPaymentBatch && it.Batch != nil:
			var err error
			batch, err = batchTx(ctx, tx, *it.Batch, true)
			return err
		case it.Payment != nil:
			var err error
			one, err = scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1`, *it.Payment))
			return err
		}
		return fmt.Errorf("%w: no payment", whatsapp.ErrNothingToSend)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the payment is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	var title, body, link string
	base := strings.TrimRight(s.PublicURL, "/")
	if it.Kind == outboxPaymentBatch {
		title, body = batchEndWords(l, batch)
		if base != "" {
			link = base + "/payments/batches/" + batch.ID.String()
		}
	} else {
		title = payEndWords(l, one)
		if base != "" {
			link = base + "/payments/" + one.ID.String()
		}
	}
	// Outside WhatsApp's window the alert template carries the first line
	// and the link.
	m := whatsapp.AlertMessage(business, "payment", title, body, link, nil)
	m.Text = strings.TrimRight(m.Text, "\n")
	m.Tenant = it.Tenant
	if ch.OwnTenant() != uuid.Nil {
		m.Text = strings.TrimPrefix(m.Text, "["+business+"] ")
	}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}
