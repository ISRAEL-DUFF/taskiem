package storefront

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Pay by bank transfer (docs/spec/iswallet-account-numbers.md N3). An
// order or booking waiting for its payment gets a one-time account number
// on the business's rail when the customer asks for one on its status
// page: the invoices' accounts (engine/invoicing), holding the order. A
// payment into it is counted towards the order exactly, by the matcher
// (the rail's event) or the invoices' checker (a rail that can be asked),
// along the path a card payment takes (CountPayment). A form whose
// connection is another rail than Paystack takes payment this way only.
// Such an order is refunded by hand: Taskiem cannot send money back on
// those rails, so the owner does from their bank and marks it refunded.

// IssueTransfer asks the business's rail for a one-time account number
// for an order: on connection (the form's, as connector/name), for
// amountKobo, valid for about minutes.
type IssueTransfer func(ctx context.Context, tenant, order uuid.UUID, connection string, amountKobo int64, description string, payer map[string]any, minutes int) error

// The least time an account number leaves a customer to pay, by kind: an
// order's hold is lengthened to it, a booking's slot held at least this.
const (
	transferOrderMinutes   = 60
	transferBookingMinutes = 30
)

// TransferRequested issues the account number a status page asked for:
// for an order still waiting for its payment, lengthening its hold so the
// customer has time to pay, on the form's connection.
func (o *Orders) TransferRequested(ctx context.Context, tenant, order uuid.UUID) error {
	if o.IssueTransfer == nil {
		return errors.New("storefront: account numbers are not set up on this server")
	}
	var ord Order
	var connection string
	var minutes int
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, order))
		if err != nil {
			return err
		}
		now := o.now()
		if ord.Status != "awaiting_payment" || ord.ExpiresAt != nil && !ord.ExpiresAt.After(now) {
			return fmt.Errorf("%w: %s is not waiting for its payment", ErrDecision, ord.Reference)
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(options->>'connection', '') FROM storefront_actions WHERE storefront_id = $1 AND kind = $2`,
			ord.storefront, ord.Kind).Scan(&connection); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		least := transferOrderMinutes
		if ord.Kind == "booking" {
			least = transferBookingMinutes
		}
		until := now.Add(time.Duration(least) * time.Minute)
		if ord.ExpiresAt != nil && ord.ExpiresAt.After(until) {
			until = *ord.ExpiresAt
		}
		minutes = int(math.Ceil(until.Sub(now).Minutes()))
		if ord.ExpiresAt == nil || until.After(*ord.ExpiresAt) {
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET expires_at = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, until); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = $2 WHERE reference = $1 AND expires_at IS NOT NULL`, ord.Reference, until); err != nil {
				return err
			}
		}
		return o.open(ctx, tx, tenant, &ord)
	})
	if err != nil {
		return err
	}
	what := "Order "
	if ord.Kind == "booking" {
		what = "Booking "
	}
	payer := map[string]any{"name": ord.Customer.Name, "email": o.payerEmail(ord.Customer, ord.Reference)}
	if ord.Customer.Phone != "" {
		payer["phone"] = ord.Customer.Phone
	}
	return o.IssueTransfer(ctx, tenant, ord.ID, checkoutChannel(connection), ord.Payable(), what+ord.Reference, payer, minutes)
}

// TransferReceived counts a payment the rail confirmed into an order's
// account number (the invoices' checker found it), as the matcher counts
// the rail's event: one money log entry and one match whichever is first.
// A payment the order can no longer take (expired, paid by other money)
// stays in the money log for a person.
func (o *Orders) TransferReceived(ctx context.Context, tenant, order uuid.UUID, paymentID, channel string, amount int64, account uuid.UUID) error {
	var c Counted
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var reference string
		if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, order).Scan(&reference); err != nil {
			return err
		}
		in := PaymentIn{AmountKobo: amount, How: "exact", By: "system:" + channel, Reasons: []string{"paid into the account number given for this order"}, Account: &account}
		if o.MoneyLog != nil {
			id, _, err := o.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: channel,
				Reference: paymentID, Event: "payment.received", AmountKobo: &amount, Source: "lookup"})
			if err != nil {
				return err
			}
			in.EntryID = &id
		}
		var err error
		c, err = o.CountPayment(ctx, tx, tenant, reference, in)
		return err
	})
	if errors.Is(err, ErrDecision) {
		o.log().Warn("storefront: a transfer for an order was not counted", "order", order, "err", err)
		return nil
	}
	if err != nil {
		return err
	}
	return o.Paid(ctx, tenant, c)
}

// statusLinkFor answers a payment link for an order paid by transfer: its
// status page, where the customer gets the account number.
func (o *Orders) statusLinkFor(ctx context.Context, tenant uuid.UUID, ord Order) (map[string]any, error) {
	var link string
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		link, err = o.linkOf(ctx, tx, tenant, ord)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"reference": ord.Reference, "url": link, "total": FormatNaira(ord.Payable())}, nil
}

// refundByHandNote is what a refund_due order paid by transfer says.
func refundByHandNote(ord Order) string {
	how := "by bank transfer"
	if ord.PaidBy != "transfer" {
		how = "with money matched to it, not through its card page"
	}
	return "Paid " + how + ": send " + FormatNaira(ord.Payable()) + " back to the customer from your bank, then mark it refunded."
}

// askRefundByHand leaves a refund_due order with the note saying how to
// refund it, and answers ErrRefundByHand with the same words.
func (o *Orders) askRefundByHand(ctx context.Context, tenant uuid.UUID, ord Order) error {
	note := refundByHandNote(ord)
	if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2 AND status = 'refund_due'`, tenant, ord.ID, note)
		return err
	}); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s %s", ErrRefundByHand, ord.Reference, strings.ToLower(note[:1])+note[1:])
}

// MarkRefunded records that the owner sent a refund_due order's money back
// themselves (an order paid by transfer, or any they chose to refund by
// hand), and tells the customer. A second call on a refunded order
// reports it.
func (o *Orders) MarkRefunded(ctx context.Context, tenant uuid.UUID, reference, actor, note string) (Order, error) {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 300 {
		return Order{}, fmt.Errorf("%w: a note is up to 300 characters", ErrInvalid)
	}
	var ord Order
	marked := false
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
		if err != nil {
			return err
		}
		switch ord.Status {
		case "refunded":
			return o.open(ctx, tx, tenant, &ord)
		case "refund_due":
		default:
			return fmt.Errorf("%w: %s is not waiting for a refund (%s)", ErrDecision, reference, strings.ReplaceAll(ord.Status, "_", " "))
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'refunded', refunded_at = now(), note = NULLIF($3, ''), updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, note); err != nil {
			return err
		}
		ord.Status, ord.Note, marked = "refunded", note, true
		if err := audit(ctx, tx, tenant, "storefront.refunded", reference, map[string]any{"by": actor, "by_hand": true, "total_kobo": ord.Payable()}); err != nil {
			return err
		}
		return o.open(ctx, tx, tenant, &ord)
	})
	if err != nil || !marked {
		return ord, err
	}
	err = o.tell(ctx, tenant, reference, "Your payment of "+FormatNaira(ord.Payable())+" for "+reference+" has been sent back to you. Contact us if it has not reached you.",
		"refund:"+reference)
	return ord, err
}
