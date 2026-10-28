package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Payment at the door (docs/spec/dispatch.md D4): an order paid on
// delivery is paid when its rider arrives, into a one-time account number
// issued then (the storefront's own path, N3) and matched exactly to the
// order, so the money never passes through the rider; the rider's phone
// says Paid within seconds. Cash is the recorded exception: the order is
// paid in cash, completed, and the rider owes the business the amount
// until they remit it.

// door issues the order's account number at the door and sends it to the
// rider and the customer; seq tells a new number from the first.
func (d *Dispatch) door(ctx context.Context, tenant, delivery uuid.UUID, ord storefront.Order, seq string) error {
	issueErr := d.Orders.DoorTransfer(ctx, tenant, ord.ID)
	if issueErr != nil {
		d.log().Warn("dispatch: no account number at the door", "reference", ord.Reference, "err", issueErr)
	}
	var acct storefront.DoorAccount
	var open bool
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		x, err := d.GetTx(ctx, tx, tenant, delivery)
		if err != nil {
			return err
		}
		if acct, open, err = storefront.OpenDoorAccount(ctx, tx, tenant, ord.ID, d.now()); err != nil {
			return err
		}
		stage := StageDoor
		if !open {
			// No number now: cash, or ask again with New number.
			stage = StageUnpaid
		}
		return d.queue(ctx, tx, tenant, delivery, x.RiderID, stage, seq)
	})
	if err != nil || !open {
		return err
	}
	return d.tellCustomer(ctx, tenant, ord, "customer.delivery_pay_at_door", "door:"+acct.Number, "total", storefront.FormatNaira(acct.AmountKobo),
		"account", acct.Number, "bank", acct.Bank, "name", acct.Name, "until", acct.ExpiresAt.In(lagos).Format("15:04"))
}

var lagos = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

// cashTx records cash received at the door: a cash receipt in the money
// log, counted to the order (paid), and what the rider owes for it.
func (d *Dispatch) cashTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, ord storefront.Order) (storefront.Counted, error) {
	amount := ord.Payable()
	in := storefront.PaymentIn{AmountKobo: amount, How: "manual", By: "rider:" + x.RiderID.String(),
		Reasons: []string{"cash received at the door by the rider"}}
	if d.MoneyLog != nil {
		id, _, err := d.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "cash", Status: "settled", Channel: "cash",
			Reference: "delivery:" + x.ID.String(), AmountKobo: &amount, Source: "entered", Description: "Cash at the door for " + ord.Reference,
			EnteredBy: "rider:" + x.RiderID.String()})
		if err != nil {
			return storefront.Counted{}, err
		}
		in.EntryID = &id
	}
	c, err := d.Orders.CountPayment(ctx, tx, tenant, ord.Reference, in)
	if err != nil {
		return c, err
	}
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET paid_how = 'cash' WHERE id = $1`, x.ID); err != nil {
		return c, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rider_remittances (id, tenant_id, rider_id, delivery_id, amount_kobo) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (delivery_id) DO NOTHING`, uuid.Must(uuid.NewV7()), tenant, x.RiderID, x.ID, amount); err != nil {
		return c, err
	}
	return c, audit(ctx, tx, tenant, "dispatch.cash_received", ord.Reference, map[string]any{"rider": x.RiderID, "amount_kobo": amount})
}

// OrderPaid hears of an order a payment moved to paid (storefront's
// AfterPaid): paid at the door, its rider is told to hand it over and
// the customer thanked.
func (d *Dispatch) OrderPaid(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error {
	var x Delivery
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if x, err = d.HeldTx(ctx, tx, tenant, ord.ID); err != nil {
			return err
		}
		if x.Status != "arrived" && x.Status != "picked_up" || x.PaidHow == "cash" {
			return ErrNoDelivery
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET paid_how = 'transfer' WHERE id = $1 AND paid_how IS NULL`, x.ID); err != nil {
			return err
		}
		return d.queue(ctx, tx, tenant, x.ID, x.RiderID, StagePaid, "")
	})
	if errors.Is(err, ErrNoDelivery) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.tellCustomer(ctx, tenant, ord, "customer.delivery_paid", "paid")
}

// --- what riders owe ---

// Owed is what a rider owes in cash: the open remittances' total and how
// many.
type Owed struct {
	RiderID    uuid.UUID `json:"rider_id"`
	Rider      string    `json:"rider"`
	OwedKobo   int64     `json:"owed_kobo"`
	Deliveries int       `json:"deliveries"`
	Since      time.Time `json:"since"`
}

// OwedTx is what each rider owes now, most first.
func (d *Dispatch) OwedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Owed, error) {
	rows, err := tx.Query(ctx, `SELECT rider_id, sum(amount_kobo - paid_kobo), count(*), min(created_at) FROM rider_remittances
		WHERE tenant_id = $1 AND settled_at IS NULL GROUP BY rider_id ORDER BY 2 DESC`, tenant)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Owed, error) {
		var o Owed
		return o, r.Scan(&o.RiderID, &o.OwedKobo, &o.Deliveries, &o.Since)
	})
	if err != nil {
		return nil, err
	}
	for i := range list {
		r, err := d.Riders.GetTx(ctx, tx, tenant, list[i].RiderID)
		if err != nil {
			return nil, err
		}
		list[i].Rider = r.Name
	}
	if list == nil {
		list = []Owed{}
	}
	return list, nil
}

// Remit records cash a rider handed over, or a transfer from them (a money
// log entry), against what they owe, oldest first. What is beyond what
// they owe is refused.
func (d *Dispatch) RemitTx(ctx context.Context, tx pgx.Tx, tenant, rider uuid.UUID, amount int64, how string, entry *uuid.UUID, by string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("%w: an amount is needed", ErrInvalid)
	}
	if how != "cash" && how != "transfer" {
		return 0, fmt.Errorf("%w: a remittance is cash or transfer", ErrInvalid)
	}
	if entry == nil && d.MoneyLog != nil {
		// Notes handed in are cash in the money log, as a school's are.
		id, _, err := d.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "cash", Status: "settled", Channel: "cash",
			Reference: "remittance:" + uuid.Must(uuid.NewV7()).String(), AmountKobo: &amount, Source: "entered", Description: "Cash handed in by a rider",
			EnteredBy: by})
		if err != nil {
			return 0, err
		}
		entry = &id
	}
	rows, err := tx.Query(ctx, `SELECT id, amount_kobo - paid_kobo FROM rider_remittances WHERE tenant_id = $1 AND rider_id = $2 AND settled_at IS NULL
		ORDER BY created_at FOR UPDATE`, tenant, rider)
	if err != nil {
		return 0, err
	}
	type open struct {
		id   uuid.UUID
		left int64
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (open, error) {
		var o open
		return o, r.Scan(&o.id, &o.left)
	})
	if err != nil {
		return 0, err
	}
	var owed int64
	for _, o := range list {
		owed += o.left
	}
	if amount > owed {
		return 0, fmt.Errorf("%w: they owe %s, less than %s", ErrInvalid, storefront.FormatNaira(owed), storefront.FormatNaira(amount))
	}
	left := amount
	for _, o := range list {
		if left == 0 {
			break
		}
		part := min(left, o.left)
		if _, err := tx.Exec(ctx, `INSERT INTO rider_remittance_parts (id, tenant_id, remittance_id, entry_id, how, amount_kobo, by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.Must(uuid.NewV7()), tenant, o.id, entry, how, part, by); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE rider_remittances SET paid_kobo = paid_kobo + $2, settled_at = CASE WHEN paid_kobo + $2 = amount_kobo THEN now() END
			WHERE id = $1`, o.id, part); err != nil {
			return 0, err
		}
		left -= part
	}
	return owed - amount, audit(ctx, tx, tenant, "dispatch.remitted", rider.String(), map[string]any{"by": by, "how": how, "amount_kobo": amount})
}

// MatchRemittances counts transfers in the money log whose narration
// names an order a rider took cash for towards what that rider owes,
// oldest first (the suggestion matching serves the rest).
func (d *Dispatch) MatchRemittances(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (e.id) e.id, r.rider_id, COALESCE(e.amount_kobo, 0) FROM money_entries e
		JOIN rider_remittances r ON r.tenant_id = e.tenant_id AND r.settled_at IS NULL
		JOIN deliveries x ON x.id = r.delivery_id JOIN storefront_orders o ON o.id = x.order_id
		WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.kind <> 'cash' AND e.recorded_at > now() - interval '14 days'
		  AND upper(regexp_replace(COALESCE(e.description, '') || ' ' || COALESCE(e.note, ''), '[^A-Za-z0-9]', '', 'g')) LIKE '%' || upper(replace(o.reference, '-', '')) || '%'
		  AND NOT EXISTS (SELECT 1 FROM rider_remittance_parts p WHERE p.entry_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL)
		ORDER BY e.id, r.created_at`, tenant)
	if err != nil {
		return 0, err
	}
	type hit struct {
		entry, rider uuid.UUID
		amount       int64
	}
	hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hit, error) {
		var h hit
		return h, r.Scan(&h.entry, &h.rider, &h.amount)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, h := range hits {
		var owed int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo - paid_kobo), 0) FROM rider_remittances WHERE tenant_id = $1 AND rider_id = $2 AND settled_at IS NULL`,
			tenant, h.rider).Scan(&owed); err != nil {
			return n, err
		}
		amount := min(h.amount, owed)
		if amount <= 0 {
			continue
		}
		entry := h.entry
		if _, err := d.RemitTx(ctx, tx, tenant, h.rider, amount, "transfer", &entry, "system:matching"); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
