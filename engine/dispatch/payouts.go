package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Riders' fees and payouts (docs/spec/dispatch.md D5): a delivery's fee
// accrues to its rider when it is delivered; a payout pays each rider
// their fees since the last, less the cash they owe, through the
// pay-riders workflow: the bank confirms each account's name, the owner
// approves the batch, and each rider is paid once (pay_bank, keyed by the
// payout) however often anything is retried.

// PayLine is what a rider is due now.
type PayLine struct {
	RiderID    uuid.UUID `json:"rider_id"`
	Rider      string    `json:"rider"`
	Deliveries int       `json:"deliveries"`
	FeesKobo   int64     `json:"fees_kobo"`
	OwedKobo   int64     `json:"owed_kobo"`
	// NetKobo is the fees less the cash owed (never below nothing).
	NetKobo int64 `json:"net_kobo"`
	// Bank is the account it goes to, as it may be shown; "" until set.
	Bank string `json:"bank"`
}

// PayLinesTx is what each rider is due now: their delivered deliveries
// not yet paid, their fees and the cash they owe.
func (d *Dispatch) PayLinesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]PayLine, error) {
	rows, err := tx.Query(ctx, `SELECT r.id, COALESCE(r.bank_shown, ''),
		  (SELECT count(*) FROM deliveries x WHERE x.rider_id = r.id AND x.status = 'delivered' AND x.payout_id IS NULL),
		  (SELECT COALESCE(sum(x.fee_kobo), 0) FROM deliveries x WHERE x.rider_id = r.id AND x.status = 'delivered' AND x.payout_id IS NULL),
		  (SELECT COALESCE(sum(m.amount_kobo - m.paid_kobo), 0) FROM rider_remittances m WHERE m.rider_id = r.id AND m.settled_at IS NULL)
		FROM riders r WHERE r.tenant_id = $1`, tenant)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayLine, error) {
		var l PayLine
		return l, r.Scan(&l.RiderID, &l.Bank, &l.Deliveries, &l.FeesKobo, &l.OwedKobo)
	})
	if err != nil {
		return nil, err
	}
	out := []PayLine{}
	for _, l := range list {
		if l.Deliveries == 0 && l.OwedKobo == 0 {
			continue
		}
		r, err := d.Riders.GetTx(ctx, tx, tenant, l.RiderID)
		if err != nil {
			return nil, err
		}
		l.Rider, l.NetKobo = r.Name, max(0, l.FeesKobo-l.OwedKobo)
		out = append(out, l)
	}
	return out, nil
}

// Bank is a rider's bank account, sealed under their personal-data
// subject; AccountName is the bank's, kept after the first payout.
type Bank struct {
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name,omitempty"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name,omitempty"`
}

// Shown is a bank account as it may be shown: the bank, the last four
// digits and the name once the bank gave it.
func (b Bank) Shown() string {
	n := b.AccountNumber
	if len(n) > 4 {
		n = n[len(n)-4:]
	}
	s := strings.TrimSpace(b.BankName)
	if s == "" {
		s = "Bank " + b.BankCode
	}
	s += ", ending " + n
	if b.AccountName != "" {
		s += ", " + b.AccountName
	}
	return s
}

// SetBankTx keeps the account a rider's payouts go to; the bank confirms
// its name when they are first paid.
func (d *Dispatch) SetBankTx(ctx context.Context, tx pgx.Tx, tenant, rider uuid.UUID, b Bank, actor string) (string, error) {
	b.BankCode, b.AccountNumber, b.BankName = strings.TrimSpace(b.BankCode), strings.TrimSpace(b.AccountNumber), strings.TrimSpace(b.BankName)
	switch {
	case len(b.BankCode) < 3 || len(b.BankCode) > 6 || strings.Trim(b.BankCode, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") != "":
		return "", fmt.Errorf("%w: choose the rider's bank", ErrInvalid)
	case len(b.AccountNumber) != 10 || strings.Trim(b.AccountNumber, "0123456789") != "":
		return "", fmt.Errorf("%w: an account number is 10 digits", ErrInvalid)
	case len(b.BankName) > 60:
		return "", fmt.Errorf("%w: a bank's name is up to 60 characters", ErrInvalid)
	}
	b.AccountName = ""
	sealed, err := d.sealBank(ctx, tx, tenant, rider, b)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE riders SET bank = $3, bank_shown = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, rider, sealed, b.Shown()); err != nil {
		return "", err
	}
	return b.Shown(), audit(ctx, tx, tenant, "dispatch.rider_bank_set", rider.String(), map[string]any{"by": actor})
}

func (d *Dispatch) sealBank(ctx context.Context, tx pgx.Tx, tenant, rider uuid.UUID, b Bank) (map[string]any, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT phone FROM riders WHERE tenant_id = $1 AND id = $2`, tenant, rider).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoDelivery
	} else if err != nil {
		return nil, err
	}
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	subject, _ := env["subject"].(string)
	return d.Vault.SealUnderTx(ctx, tx, tenant, subject, "bank_account", b)
}

func (d *Dispatch) bankOf(ctx context.Context, tx pgx.Tx, tenant, rider uuid.UUID) (Bank, bool, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT bank FROM riders WHERE tenant_id = $1 AND id = $2`, tenant, rider).Scan(&raw); err != nil || raw == nil {
		return Bank{}, false, err
	}
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	v, err := d.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return Bank{}, false, nil
	}
	if err != nil {
		return Bank{}, false, err
	}
	j, _ := json.Marshal(v)
	var b Bank
	return b, json.Unmarshal(j, &b) == nil && b.AccountNumber != "", nil
}

// Payout is one rider's part of a batch.
type Payout struct {
	ID            uuid.UUID `json:"payout_id"`
	RiderID       uuid.UUID `json:"rider_id"`
	Rider         string    `json:"rider"`
	Deliveries    int       `json:"deliveries"`
	FeesKobo      int64     `json:"fees_kobo"`
	OwedKobo      int64     `json:"owed_kobo"`
	AmountKobo    int64     `json:"amount_kobo"`
	Status        string    `json:"status"`
	BankCode      string    `json:"bank_code"`
	AccountNumber string    `json:"account_number"`
	AccountName   string    `json:"account_name"`
}

// Batch is a payout run: the riders to pay, and those not paid for want
// of a bank account.
type Batch struct {
	ID        uuid.UUID `json:"batch_id"`
	Payouts   []Payout  `json:"payouts"`
	NotPaid   []string  `json:"not_paid"`
	TotalKobo int64     `json:"total_kobo"`
}

// PrepareBatch makes a batch with this id, once: a payout for each rider
// due fees with a bank account (their deliveries bound to it, so they
// are paid once); those whose cash owed covers their fees are settled at
// once with nothing to pay. Again, the same batch is read back.
func (d *Dispatch) PrepareBatch(ctx context.Context, tenant, batch uuid.UUID) (Batch, error) {
	b := Batch{ID: batch, Payouts: []Payout{}, NotPaid: []string{}}
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		// One batch is made at a time.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('rider_payouts:' || $1::text, 0))`, tenant); err != nil {
			return err
		}
		var made bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rider_payouts WHERE tenant_id = $1 AND batch_id = $2)`, tenant, batch).Scan(&made); err != nil {
			return err
		}
		if !made {
			lines, err := d.PayLinesTx(ctx, tx, tenant)
			if err != nil {
				return err
			}
			for _, l := range lines {
				if l.Deliveries == 0 {
					continue
				}
				_, hasBank, err := d.bankOf(ctx, tx, tenant, l.RiderID)
				if err != nil {
					return err
				}
				if !hasBank && l.NetKobo > 0 {
					b.NotPaid = append(b.NotPaid, l.Rider)
					continue
				}
				id := uuid.Must(uuid.NewV7())
				status := "pending"
				if l.NetKobo == 0 {
					status = "paid"
				}
				if _, err := tx.Exec(ctx, `INSERT INTO rider_payouts (id, tenant_id, batch_id, rider_id, deliveries, fees_kobo, owed_kobo, net_kobo, status)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, tenant, batch, l.RiderID, l.Deliveries, l.FeesKobo, min(l.OwedKobo, l.FeesKobo), l.NetKobo, status); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE deliveries SET payout_id = $3 WHERE tenant_id = $1 AND rider_id = $2 AND status = 'delivered' AND payout_id IS NULL`,
					tenant, l.RiderID, id); err != nil {
					return err
				}
				if status == "paid" {
					if err := d.settlePayoutTx(ctx, tx, tenant, id, l.RiderID, min(l.OwedKobo, l.FeesKobo)); err != nil {
						return err
					}
				}
			}
			if err := audit(ctx, tx, tenant, "dispatch.payouts_prepared", batch.String(), map[string]any{"not_paid": len(b.NotPaid)}); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id, rider_id, deliveries, fees_kobo, owed_kobo, net_kobo, status FROM rider_payouts
			WHERE tenant_id = $1 AND batch_id = $2 AND status = 'pending' ORDER BY created_at, id`, tenant, batch)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payout, error) {
			var p Payout
			return p, r.Scan(&p.ID, &p.RiderID, &p.Deliveries, &p.FeesKobo, &p.OwedKobo, &p.AmountKobo, &p.Status)
		})
		if err != nil {
			return err
		}
		for _, p := range list {
			r, err := d.Riders.GetTx(ctx, tx, tenant, p.RiderID)
			if err != nil {
				return err
			}
			bank, ok, err := d.bankOf(ctx, tx, tenant, p.RiderID)
			if err != nil {
				return err
			}
			if !ok {
				b.NotPaid = append(b.NotPaid, r.Name)
				continue
			}
			p.Rider, p.BankCode, p.AccountNumber, p.AccountName = r.Name, bank.BankCode, bank.AccountNumber, bank.AccountName
			b.Payouts = append(b.Payouts, p)
			b.TotalKobo += p.AmountKobo
		}
		return nil
	})
	return b, err
}

// Result is how one payout went: paid, failed, or pending (the bank has
// not confirmed it yet); AccountName is the name the bank gave.
type Result struct {
	PayoutID    uuid.UUID `json:"payout_id"`
	Status      string    `json:"status"`
	AccountName string    `json:"account_name,omitempty"`
}

// FinishBatch records how a batch went: a paid rider's deliveries stay
// paid and the cash they owed is netted from it; one failed, or a batch
// not approved (cancel), leaves their deliveries to be paid next time.
// The bank's name for an account is kept.
func (d *Dispatch) FinishBatch(ctx context.Context, tenant, batch uuid.UUID, results []Result, cancel bool) (paid, failed int, err error) {
	err = d.tx(ctx, tenant, func(tx pgx.Tx) error {
		by := map[uuid.UUID]Result{}
		for _, r := range results {
			by[r.PayoutID] = r
		}
		rows, err := tx.Query(ctx, `SELECT id, rider_id, owed_kobo FROM rider_payouts WHERE tenant_id = $1 AND batch_id = $2 AND status = 'pending' FOR UPDATE`, tenant, batch)
		if err != nil {
			return err
		}
		type open struct {
			id, rider uuid.UUID
			owed      int64
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (open, error) {
			var o open
			return o, r.Scan(&o.id, &o.rider, &o.owed)
		})
		if err != nil {
			return err
		}
		for _, o := range list {
			r := by[o.id]
			status := r.Status
			if cancel {
				status = "cancelled"
			}
			switch status {
			case "paid":
				if _, err := tx.Exec(ctx, `UPDATE rider_payouts SET status = 'paid', settled_at = now() WHERE id = $1`, o.id); err != nil {
					return err
				}
				if err := d.settlePayoutTx(ctx, tx, tenant, o.id, o.rider, o.owed); err != nil {
					return err
				}
				if r.AccountName != "" {
					if bank, ok, err := d.bankOf(ctx, tx, tenant, o.rider); err != nil {
						return err
					} else if ok && bank.AccountName == "" {
						bank.AccountName = r.AccountName
						sealed, err := d.sealBank(ctx, tx, tenant, o.rider, bank)
						if err != nil {
							return err
						}
						if _, err := tx.Exec(ctx, `UPDATE riders SET bank = $2, bank_shown = $3 WHERE id = $1`, o.rider, sealed, bank.Shown()); err != nil {
							return err
						}
					}
				}
				paid++
			case "failed", "cancelled":
				if _, err := tx.Exec(ctx, `UPDATE rider_payouts SET status = $2, settled_at = now() WHERE id = $1`, o.id, status); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE deliveries SET payout_id = NULL WHERE payout_id = $1`, o.id); err != nil {
					return err
				}
				failed++
			}
		}
		return audit(ctx, tx, tenant, "dispatch.payouts_finished", batch.String(), map[string]any{"paid": paid, "failed": failed, "cancelled": cancel})
	})
	return paid, failed, err
}

// settlePayoutTx nets the cash a rider owed from their paid payout,
// oldest first. Cash they handed in since the payout was made is not
// taken twice: what they owe now caps it, and what the rider is due back
// is kept in the audit log.
func (d *Dispatch) settlePayoutTx(ctx context.Context, tx pgx.Tx, tenant, payout, rider uuid.UUID, owed int64) error {
	if owed <= 0 {
		return nil
	}
	var now int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo - paid_kobo), 0) FROM rider_remittances WHERE tenant_id = $1 AND rider_id = $2 AND settled_at IS NULL`,
		tenant, rider).Scan(&now); err != nil {
		return err
	}
	if now < owed {
		if err := audit(ctx, tx, tenant, "dispatch.payout_over_netted", payout.String(), map[string]any{"rider": rider.String(), "due_back_kobo": owed - now}); err != nil {
			return err
		}
		if owed = now; owed == 0 {
			return nil
		}
	}
	_, err := d.remitTx(ctx, tx, tenant, rider, owed, "netted", nil, &payout, "system:payout")
	return err
}

// Payouts lists the batches' payouts, newest first.
func (d *Dispatch) PayoutsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Payout, error) {
	rows, err := tx.Query(ctx, `SELECT id, rider_id, deliveries, fees_kobo, owed_kobo, net_kobo, status FROM rider_payouts
		WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payout, error) {
		var p Payout
		return p, r.Scan(&p.ID, &p.RiderID, &p.Deliveries, &p.FeesKobo, &p.OwedKobo, &p.AmountKobo, &p.Status)
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
		list = []Payout{}
	}
	return list, nil
}

// --- the day, per rider ---

// RiderDay is one rider's day: deliveries done, paid at the door by
// transfer and in cash, cash remitted, what they owe now, and their
// rating that day.
type RiderDay struct {
	RiderID      uuid.UUID `json:"rider_id"`
	Rider        string    `json:"rider"`
	Delivered    int       `json:"delivered"`
	DoorKobo     int64     `json:"door_transfer_kobo"`
	CashKobo     int64     `json:"cash_kobo"`
	RemittedKobo int64     `json:"remitted_kobo"`
	OwedKobo     int64     `json:"owed_kobo"`
	FeesKobo     int64     `json:"fees_kobo"`
	Rating       *float64  `json:"rating"`
	RatingsCount int       `json:"ratings"`
}

// RiderDaysTx is each rider's [from, to), those with nothing left out.
func (d *Dispatch) RiderDaysTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) ([]RiderDay, error) {
	rows, err := tx.Query(ctx, `SELECT r.id,
		  (SELECT count(*) FROM deliveries x WHERE x.rider_id = r.id AND x.delivered_at >= $2 AND x.delivered_at < $3),
		  (SELECT COALESCE(sum(COALESCE(o.deposit_kobo, o.total_kobo)), 0) FROM deliveries x JOIN storefront_orders o ON o.id = x.order_id
		    WHERE x.rider_id = r.id AND x.paid_how = 'transfer' AND x.delivered_at >= $2 AND x.delivered_at < $3),
		  (SELECT COALESCE(sum(m.amount_kobo), 0) FROM rider_remittances m WHERE m.rider_id = r.id AND m.created_at >= $2 AND m.created_at < $3),
		  (SELECT COALESCE(sum(p.amount_kobo), 0) FROM rider_remittance_parts p JOIN rider_remittances m ON m.id = p.remittance_id
		    WHERE m.rider_id = r.id AND p.created_at >= $2 AND p.created_at < $3),
		  (SELECT COALESCE(sum(m.amount_kobo - m.paid_kobo), 0) FROM rider_remittances m WHERE m.rider_id = r.id AND m.settled_at IS NULL),
		  (SELECT COALESCE(sum(x.fee_kobo), 0) FROM deliveries x WHERE x.rider_id = r.id AND x.delivered_at >= $2 AND x.delivered_at < $3),
		  (SELECT avg(x.rating)::float8 FROM deliveries x WHERE x.rider_id = r.id AND x.rated_at >= $2 AND x.rated_at < $3),
		  (SELECT count(*) FROM deliveries x WHERE x.rider_id = r.id AND x.rated_at >= $2 AND x.rated_at < $3)
		FROM riders r WHERE r.tenant_id = $1`, tenant, from, to)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RiderDay, error) {
		var x RiderDay
		return x, r.Scan(&x.RiderID, &x.Delivered, &x.DoorKobo, &x.CashKobo, &x.RemittedKobo, &x.OwedKobo, &x.FeesKobo, &x.Rating, &x.RatingsCount)
	})
	if err != nil {
		return nil, err
	}
	out := []RiderDay{}
	for _, x := range list {
		if x.Delivered == 0 && x.CashKobo == 0 && x.RemittedKobo == 0 && x.OwedKobo == 0 && x.RatingsCount == 0 {
			continue
		}
		r, err := d.Riders.GetTx(ctx, tx, tenant, x.RiderID)
		if err != nil {
			return nil, err
		}
		x.Rider = r.Name
		out = append(out, x)
	}
	return out, nil
}

// RiderLine is a rider's day in one line, for the daily close: "Musa: 4
// delivered, ₦8,500 at the door, ₦3,000 cash, owes ₦3,000".
func RiderLine(x RiderDay) string {
	parts := []string{fmt.Sprintf("%d delivered", x.Delivered)}
	if x.DoorKobo > 0 {
		parts = append(parts, storefront.FormatNaira(x.DoorKobo)+" paid at the door")
	}
	if x.CashKobo > 0 {
		parts = append(parts, storefront.FormatNaira(x.CashKobo)+" cash")
	}
	if x.RemittedKobo > 0 {
		parts = append(parts, storefront.FormatNaira(x.RemittedKobo)+" remitted")
	}
	if x.OwedKobo > 0 {
		parts = append(parts, "owes "+storefront.FormatNaira(x.OwedKobo))
	}
	if x.Rating != nil {
		parts = append(parts, fmt.Sprintf("rated %.1f", *x.Rating))
	}
	return x.Rider + ": " + strings.Join(parts, ", ")
}
