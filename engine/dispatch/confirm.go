package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Riders are paid per delivery (docs/spec/chat-riders.md, phase 4): a
// delivery's fee is due when the rider taps Delivered and payable when
// the business confirms it. The confirm makes a payout of that one
// delivery, paid through the pay-riders path that exists (the batch is
// the one payout; PrepareBatch reads it back for the run) or recorded as
// paid in cash by hand, with no transfer. Cash the rider took at the
// door nets the fee as a batch nets it.

// How the fee is paid.
const (
	FeeByBank = "bank"
	FeeInCash = "cash"
)

// Confirmed is what a confirm did.
type Confirmed struct {
	Delivery Delivery `json:"delivery"`
	// Batch is the payout batch to pay through the pay-riders workflow,
	// uuid.Nil when nothing is left to pay (paid in cash, or netted
	// against the cash the rider owed).
	Batch uuid.UUID `json:"-"`
	// NetKobo is what reaches the rider's bank, after the netting.
	NetKobo int64 `json:"net_kobo"`
}

// ConfirmTx confirms a delivered delivery: the business's word that it
// is done and the fee is to be paid. how is FeeByBank (a payout of this
// delivery, pending the run that pays it; paid at once when the cash the
// rider owes covers the fee) or FeeInCash (the whole fee handed over in
// cash: paid, with a cash payment out in the money log, and what the
// rider owes from the door untouched). A delivery not yet
// delivered, already confirmed, or already in a payout is refused; so is
// paying by bank a rider with no bank account.
func (d *Dispatch) ConfirmTx(ctx context.Context, tx pgx.Tx, tenant, delivery uuid.UUID, how, actor string) (Confirmed, error) {
	var out Confirmed
	if how == "" {
		how = FeeByBank
	}
	if how != FeeByBank && how != FeeInCash {
		return out, fmt.Errorf("%w: the fee is paid by bank or in cash", ErrInvalid)
	}
	x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
		WHERE d.tenant_id = $1 AND d.id = $2 FOR UPDATE OF d`, tenant, delivery))
	if err != nil {
		return out, err
	}
	switch {
	case x.Status != "delivered":
		return out, fmt.Errorf("%w: %s is not delivered yet", ErrMove, x.Reference)
	case x.ConfirmedAt != nil:
		return out, fmt.Errorf("%w: %s is confirmed already", ErrMove, x.Reference)
	case x.FeePaid != "":
		return out, fmt.Errorf("%w: %s's fee is in a payout already", ErrMove, x.Reference)
	}
	r, err := d.Riders.GetTx(ctx, tx, tenant, x.RiderID)
	if err != nil {
		return out, err
	}
	x.Rider = r.Name
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET confirmed_at = now(), confirmed_by = $3 WHERE tenant_id = $1 AND id = $2`, tenant, delivery, actor); err != nil {
		return out, err
	}
	// Paid by bank, the cash the rider owes, up to the fee, comes off it.
	// Handed over in cash, the whole fee was paid and what they owe stands.
	var owed int64
	if how == FeeByBank {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo - paid_kobo), 0) FROM rider_remittances WHERE tenant_id = $1 AND rider_id = $2 AND settled_at IS NULL`,
			tenant, x.RiderID).Scan(&owed); err != nil {
			return out, err
		}
		owed = min(owed, x.FeeKobo)
	}
	net := x.FeeKobo - owed
	payout, batch := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	status := "paid"
	if how == FeeByBank && net > 0 {
		if _, hasBank, err := d.bankOf(ctx, tx, tenant, x.RiderID); err != nil {
			return out, err
		} else if !hasBank {
			return out, fmt.Errorf("%w: %s has no bank account yet: add it under Riders, or mark the fee paid in cash", ErrMove, r.Name)
		}
		status = "pending"
		out.Batch = batch
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rider_payouts (id, tenant_id, batch_id, rider_id, deliveries, fees_kobo, owed_kobo, net_kobo, status, how, delivery_id, settled_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $7, $8, $9, $10, CASE WHEN $8 = 'paid' THEN now() END)`,
		payout, tenant, batch, x.RiderID, x.FeeKobo, owed, net, status, how, delivery); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET payout_id = $2 WHERE id = $1`, delivery, payout); err != nil {
		return out, err
	}
	if err := audit(ctx, tx, tenant, "dispatch.confirmed", x.Reference, map[string]any{"by": actor, "rider": x.RiderID, "how": how, "fee_kobo": x.FeeKobo, "owed_kobo": owed, "net_kobo": net}); err != nil {
		return out, err
	}
	out.NetKobo = net
	if status == "pending" {
		// The run pays it; the paid card comes with FinishBatch.
		if err := d.mirrorStep(ctx, tx, tenant, x, "confirmed", true); err != nil {
			return out, err
		}
	} else {
		if err := d.settlePayoutTx(ctx, tx, tenant, payout, x.RiderID, owed); err != nil {
			return out, err
		}
		if how == FeeInCash && net > 0 && d.MoneyLog != nil {
			// The notes handed over are a payment out, as any payout is.
			if _, _, err := d.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "out", Kind: "payout", Status: "settled", Channel: "cash",
				Reference: "rider-fee:" + payout.String(), AmountKobo: &net, Source: "entered", Description: "Rider's fee for " + x.Reference + " paid in cash to " + r.Name,
				EnteredBy: actor}); err != nil {
				return out, err
			}
		}
		if err := d.feePaidTx(ctx, tx, tenant, x, how); err != nil {
			return out, err
		}
	}
	out.Delivery, err = d.GetTx(ctx, tx, tenant, delivery)
	return out, err
}

// feePaidTx tells both sides a delivery's fee is paid: the business's
// card, and the rider's card and money log through the link.
func (d *Dispatch) feePaidTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, how string) error {
	x.paidHow = how
	return d.mirrorStep(ctx, tx, tenant, x, "paid", true)
}

// payoutPaidTx is FinishBatch's word that a payout went through: for a
// payout of one delivery (a confirm), the fee's paid card on both sides.
func (d *Dispatch) payoutPaidTx(ctx context.Context, tx pgx.Tx, tenant, payout uuid.UUID) error {
	var delivery *uuid.UUID
	var how string
	if err := tx.QueryRow(ctx, `SELECT delivery_id, how FROM rider_payouts WHERE tenant_id = $1 AND id = $2`, tenant, payout).Scan(&delivery, &how); err != nil {
		return err
	}
	if delivery == nil {
		return nil
	}
	x, err := d.GetTx(ctx, tx, tenant, *delivery)
	if errors.Is(err, ErrNoDelivery) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.feePaidTx(ctx, tx, tenant, x, how)
}

// riderIncomeTx records the fee as income in the rider's own business, in
// the rider's scope (after the door opened), against their delivery order.
func (d *Dispatch) riderIncomeTx(ctx context.Context, tx pgx.Tx, rider uuid.UUID, x Delivery, business string, at time.Time) error {
	if d.MoneyLog == nil || x.FeeKobo <= 0 {
		return nil
	}
	fee := x.FeeKobo
	channel := "link"
	if x.paidHow == FeeInCash {
		channel = "cash"
	}
	_, _, err := d.MoneyLog.RecordTx(ctx, tx, rider, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: channel, OccurredAt: &at,
		Reference: "delivery-fee:" + x.ID.String(), AmountKobo: &fee, Source: "event", Description: "Delivery fee for " + x.Reference + " from " + business})
	return err
}
