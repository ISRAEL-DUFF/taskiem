package moneylog

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// What a payment to a person was for (docs/spec/chat-pay-people.md, Data
// model: "each finished payment is recorded with its what for"; Settled 4:
// "the fee is its own line in the money log, as bank charges"). The
// payout's entry carries the payment's purpose as its category, so the
// reports split by it (a delivery, wages, a refund, stock bought, a bill,
// or the payer's own word for other); its fee is an entry of its own, kind
// fee, category bank charges.

// CategoryBankCharges is the category of a payout's fee as its own line.
const CategoryBankCharges = "bank charges"

// purposeCategory is a payment's purpose as the money log's category.
var purposeCategory = map[string]string{"bill": "bill", "wages": "wages", "delivery": "delivery", "refund": "refund", "stock": "stock bought"}

// PurposeCategory is the category of a payment for purpose ("other" with
// a word is that word).
func PurposeCategory(purpose, note string) string {
	if c, ok := purposeCategory[purpose]; ok {
		return c
	}
	if n := strings.ToLower(strings.TrimSpace(note)); n != "" {
		return clip(n, 60)
	}
	return "other"
}

// PayoutPurposeTx is the category of the payment to a person whose payout
// is reference on channel; ok false when the payout is not one.
func PayoutPurposeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, reference string) (string, bool, error) {
	if reference == "" {
		return "", false, nil
	}
	var purpose, note string
	err := tx.QueryRow(ctx, `SELECT purpose, purpose_note FROM payments WHERE tenant_id = $1 AND provider_channel = $2 AND payout_ref = $3
		ORDER BY created_at DESC LIMIT 1`, tenant, channel, reference).Scan(&purpose, &note)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return PurposeCategory(purpose, note), true, nil
}

// FeeEntry is a payout's fee as its own line: out, settled, on the
// payout's channel and reference, category bank charges. Its key is the
// payout's own with event payout.fee, so it is written once.
func FeeEntry(channel, reference string, fee int64, description string) Entry {
	return Entry{Direction: "out", Kind: "fee", Status: "settled", Channel: channel, Reference: reference, Event: "payout.fee",
		Source: "step", Currency: "NGN", AmountKobo: &fee, Category: CategoryBankCharges, Description: clip(description, 300)}
}

// reversedPaymentTx marks the payment to a person whose payout e says the
// bank sent back after it was paid as reversed (paying people H7, Settled
// 6), and queues the word to the person paid, when they were told it was
// paid, and to the person who prepared it from the chat: kinds payee_paid
// and payment, as api/payments_after.go and payments_chat_result.go send
// them.
//
// The fee stays the business's (Settled 4: the business bears it): its
// line stands, unless the rail says the fee came back with the money
// (feeBack, the reversal's fee_kobo); then a line of that much, reversed,
// names the fee's line.
func (l *Log) reversedPaymentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, e Entry, feeBack *int64) error {
	var id uuid.UUID
	var told *string
	var from, by string
	err := tx.QueryRow(ctx, `UPDATE payments SET status = 'reversed', reason = COALESCE(NULLIF($4, ''), reason)
		WHERE tenant_id = $1 AND provider_channel = $2 AND payout_ref = $3 AND status = 'paid'
		RETURNING id, payee_told, started_from, prepared_by`, tenant, e.Channel, e.Reference, e.Note).Scan(&id, &told, &from, &by)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if feeBack != nil && *feeBack > 0 {
		var fee uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND reference = $3 AND kind = 'fee' AND status = 'settled'
			ORDER BY recorded_at LIMIT 1`, tenant, e.Channel, e.Reference).Scan(&fee)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			back := FeeEntry(e.Channel, e.Reference, *feeBack, "Fee returned: "+e.Note)
			back.Status, back.Event, back.Source, back.Corrects = "reversed", "payout.fee.reversed", e.Source, &fee
			if _, _, err := l.RecordTx(ctx, tx, tenant, back); err != nil {
				return err
			}
		}
	}
	if told != nil && *told == "paid" {
		if _, err := tx.Exec(ctx, `UPDATE payments SET payee_told = 'reversed' WHERE id = $1`, id); err != nil {
			return err
		}
		it := whatsapp.OutboxItem{Tenant: tenant, Kind: "payee_paid", Payment: &id}
		var staff *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT staff_id FROM payments WHERE id = $1`, id).Scan(&staff); err != nil {
			return err
		}
		if staff != nil {
			it.User = *staff
		}
		if _, err := whatsapp.Enqueue(ctx, tx, it, "payee:"+id.String()+":reversed"); err != nil {
			return err
		}
	}
	if user, err := uuid.Parse(by); err == nil && from == "chat" {
		if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Kind: "payment", User: user, Payment: &id}, "payment:"+id.String()+":reversed"); err != nil {
			return err
		}
	}
	return nil
}
