package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Result is what a payment was matched to: an invoice or an order.
type Result struct {
	Invoice *invoicing.Invoice
	Order   *storefront.Order
}

// Target names it as money_matches does: invoice:INV-0104, order:AK-7QX4KD.
func (r Result) Target() string {
	switch {
	case r.Invoice != nil:
		return TargetInvoice + ":" + r.Invoice.Number
	case r.Order != nil:
		return TargetOrder + ":" + r.Order.Reference
	}
	return ""
}

// orderErr says a storefront refusal as matching's (invoicing's) errors,
// which the API and chat already word.
func orderErr(err error) error {
	trim := func(e error) string {
		msg := err.Error()
		for _, p := range []string{e.Error() + ": ", e.Error()} {
			msg = strings.Replace(msg, p, "", 1)
		}
		return msg
	}
	switch {
	case errors.Is(err, storefront.ErrDecision):
		return fmt.Errorf("%w: %s", invoicing.ErrState, trim(storefront.ErrDecision))
	case errors.Is(err, storefront.ErrInvalid):
		return fmt.Errorf("%w: %s", invoicing.ErrInvalid, trim(storefront.ErrInvalid))
	case errors.Is(err, storefront.ErrNoOrder):
		return fmt.Errorf("%w: no such order", invoicing.ErrNotFound)
	}
	return err
}

func (m *Matcher) orders() (*storefront.Orders, error) {
	if m.Orders == nil {
		return nil, fmt.Errorf("%w: storefront orders are not available on this server", invoicing.ErrState)
	}
	return m.Orders, nil
}

// notOnOrder refuses an entry already counted towards an order before it
// is counted towards an invoice (invoicing checks invoices under the same
// lock).
func notOnOrder(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID) error {
	if err := storefront.EntryLock(ctx, tx, entry); err != nil {
		return err
	}
	other, err := storefront.CountedTowards(ctx, tx, tenant, entry)
	if err != nil {
		return err
	}
	if strings.HasPrefix(other, TargetOrder+" ") {
		return fmt.Errorf("%w: this payment is already counted towards %s; undo that first", invoicing.ErrState, other)
	}
	return nil
}

// exactOrder counts a payment through an order's checkout page towards
// the order, along the path its own payment check takes, once.
func (m *Matcher) exactOrder(ctx context.Context, tenant uuid.UUID, e entry, order string) error {
	if e.AmountKobo <= 0 {
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}
	var c storefront.Counted
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		c, err = m.Orders.CountPayment(ctx, tx, tenant, order, storefront.PaymentIn{EntryID: &e.ID, AmountKobo: e.AmountKobo, How: "exact",
			By: "system:" + e.Channel, Reasons: []string{"paid through the order's checkout page"}})
		if err != nil {
			return err
		}
		return matched(ctx, tx, tenant, e.ID, m.now())
	})
	if errors.Is(err, storefront.ErrDecision) || errors.Is(err, storefront.ErrInvalid) {
		// Paid by other money, cancelled or expired, or too little: a
		// person decides.
		m.log().Info("matching: exact order match refused", "tenant", tenant, "entry", e.ID, "err", err)
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}
	if err != nil {
		return err
	}
	return m.Orders.Paid(ctx, tenant, c)
}

func matched(ctx context.Context, tx pgx.Tx, tenant, entryID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = $3 WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`,
		tenant, entryID, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'matched', $3)
		ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = $3`, entryID, tenant, now)
	return err
}

// Confirm makes a suggestion a match: the payment counts towards its
// invoice or order, the entry's other suggestions close, and a run waiting
// on the invoice is told when it becomes paid (an order's paid flow runs).
// auto records it as matched without asking.
func (m *Matcher) Confirm(ctx context.Context, tenant, suggestion uuid.UUID, by string, auto bool) (Result, error) {
	var res Result
	var became bool
	var counted storefront.Counted
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var invoice, order *uuid.UUID
		var entryID uuid.UUID
		var status, channel, reference string
		var amount *int64
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT s.invoice_id, s.order_id, s.entry_id, s.status, s.reasons, e.amount_kobo, e.channel, e.reference
			FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id WHERE s.tenant_id = $1 AND s.id = $2 FOR UPDATE OF s`, tenant, suggestion).
			Scan(&invoice, &order, &entryID, &status, &raw, &amount, &channel, &reference)
		if errors.Is(err, pgx.ErrNoRows) {
			return invoicing.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
		}
		var reasons []string
		_ = json.Unmarshal(raw, &reasons)
		how := "suggested"
		if auto {
			how = "suggested_auto"
		}
		if amount == nil || *amount <= 0 {
			return fmt.Errorf("%w: the payment has no amount", invoicing.ErrState)
		}
		now := m.now()
		if order != nil {
			orders, err := m.orders()
			if err != nil {
				return err
			}
			var ref string
			if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, *order).Scan(&ref); err != nil {
				return err
			}
			if counted, err = orders.CountPayment(ctx, tx, tenant, ref, storefront.PaymentIn{EntryID: &entryID, AmountKobo: *amount, How: how, By: by, Reasons: reasons}); err != nil {
				return orderErr(err)
			}
			res.Order = &counted.Order
			if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'confirmed', decided_by = $3, decided_at = $4,
				order_payment_id = (SELECT p.id FROM storefront_order_payments p WHERE p.entry_id = $5 AND p.order_id = $6 AND p.undoes IS NULL ORDER BY p.created_at DESC LIMIT 1)
				WHERE tenant_id = $1 AND id = $2`, tenant, suggestion, by, now, entryID, *order); err != nil {
				return err
			}
			return matched(ctx, tx, tenant, entryID, now)
		}
		if err := notOnOrder(ctx, tx, tenant, entryID); err != nil {
			return err
		}
		var x invoicing.Invoice
		x, became, err = m.Invoices.RecordPayment(ctx, tx, tenant, invoice.String(), invoicing.PaymentIn{AmountKobo: *amount, PaymentID: reference,
			EntryID: &entryID, Connection: channel, How: how, By: by, Reasons: reasons})
		if err != nil {
			return err
		}
		res.Invoice = &x
		var pay uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM customer_invoice_payments WHERE tenant_id = $1 AND entry_id = $2 AND invoice_id = $3 AND undoes IS NULL
			ORDER BY created_at DESC LIMIT 1`, tenant, entryID, *invoice).Scan(&pay); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'confirmed', decided_by = $3, decided_at = $4, payment_id = $5 WHERE tenant_id = $1 AND id = $2`,
			tenant, suggestion, by, now, pay); err != nil {
			return err
		}
		return matched(ctx, tx, tenant, entryID, now)
	})
	if err != nil {
		return res, err
	}
	return res, m.paid(ctx, tenant, res, became, counted)
}

// paid runs what follows a match once it is committed.
func (m *Matcher) paid(ctx context.Context, tenant uuid.UUID, res Result, became bool, c storefront.Counted) error {
	switch {
	case res.Order != nil:
		return m.Orders.Paid(ctx, tenant, c)
	case res.Invoice != nil && became:
		return m.Invoices.Paid(ctx, tenant, *res.Invoice)
	}
	return nil
}

// Reject turns a suggestion down: it is not offered again.
func (m *Matcher) Reject(ctx context.Context, tenant, suggestion uuid.UUID, by string) error {
	return db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `SELECT entry_id, status FROM money_suggestions WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, suggestion).Scan(&entryID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return invoicing.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
		}
		now := m.now()
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'rejected', decided_by = $3, decided_at = $4 WHERE tenant_id = $1 AND id = $2`,
			tenant, suggestion, by, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE money_match_state SET state = 'unmatched', considered_at = $3 WHERE tenant_id = $1 AND entry_id = $2
			AND NOT EXISTS (SELECT 1 FROM money_suggestions s WHERE s.entry_id = $2 AND s.status = 'open')`, tenant, entryID, now)
		return err
	})
}

// Open lists the suggestions waiting for a person, newest payment first,
// with the payment's details and what is due on the invoice or order.
// Customer is left for the caller to fill (and mask).
func Open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Waiting, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT s.id, s.entry_id, s.invoice_id, s.order_id, COALESCE(i.number, o.reference),
			CASE WHEN i.id IS NOT NULL THEN i.total_kobo - i.paid_kobo ELSE COALESCE(o.deposit_kobo, o.total_kobo) END,
			s.score, s.reasons, e.amount_kobo, e.channel, e.reference, COALESCE(e.occurred_at, e.recorded_at), e.kind
		FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id
			LEFT JOIN customer_invoices i ON i.id = s.invoice_id LEFT JOIN storefront_orders o ON o.id = s.order_id
		WHERE s.tenant_id = $1 AND s.status = 'open' ORDER BY e.recorded_at DESC, s.score DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Waiting, error) {
		var w Waiting
		var raw []byte
		err := r.Scan(&w.ID, &w.EntryID, &w.InvoiceID, &w.OrderID, &w.Number, &w.DueKobo, &w.Score, &raw, &w.AmountKobo, &w.Channel, &w.Reference, &w.When, &w.Kind)
		_ = json.Unmarshal(raw, &w.Reasons)
		w.Status, w.TargetKind = "open", TargetInvoice
		if w.OrderID != nil {
			w.TargetKind = TargetOrder
		}
		w.Target = w.TargetKind + ":" + w.Number
		return w, err
	})
	if out == nil {
		out = []Waiting{}
	}
	return out, err
}

// Waiting is an open suggestion with the payment it is for.
type Waiting struct {
	Suggestion
	// DueKobo is what is due on the invoice, or paid online for the order.
	DueKobo int64 `json:"due_kobo"`
	// Customer is the invoice's or order's customer's name (masked by the
	// API unless revealed).
	Customer   string    `json:"customer,omitempty"`
	AmountKobo *int64    `json:"amount_kobo"`
	Channel    string    `json:"channel"`
	Reference  string    `json:"reference"`
	When       time.Time `json:"received_at"`
	Kind       string    `json:"kind"`
}

// OrderTarget is how Match names an order: "order:AK-7QX4KD".
func OrderTarget(reference string) string { return TargetOrder + ":" + reference }

// Match counts a money-in entry towards an invoice (its id or number) or
// an order ("order:<reference>") a person chose (how manual), closing the
// entry's open suggestions.
func (m *Matcher) Match(ctx context.Context, tenant, entryID uuid.UUID, target, by string) (Result, error) {
	var res Result
	var became bool
	var counted storefront.Counted
	order, isOrder := strings.CutPrefix(target, TargetOrder+":")
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var direction, status, channel, reference string
		var amount *int64
		err := tx.QueryRow(ctx, `SELECT direction, status, amount_kobo, channel, reference FROM money_entries WHERE tenant_id = $1 AND id = $2`, tenant, entryID).
			Scan(&direction, &status, &amount, &channel, &reference)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no such payment", invoicing.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if direction != "in" || status != "settled" || amount == nil || *amount <= 0 {
			return fmt.Errorf("%w: only money received and settled can be matched to an invoice or order", invoicing.ErrState)
		}
		reasons := []string{"matched by hand"}
		if isOrder {
			orders, err := m.orders()
			if err != nil {
				return err
			}
			if counted, err = orders.CountPayment(ctx, tx, tenant, order, storefront.PaymentIn{EntryID: &entryID, AmountKobo: *amount, How: "manual", By: by, Reasons: reasons}); err != nil {
				return orderErr(err)
			}
			res.Order = &counted.Order
		} else {
			if err := notOnOrder(ctx, tx, tenant, entryID); err != nil {
				return err
			}
			var x invoicing.Invoice
			if x, became, err = m.Invoices.RecordPayment(ctx, tx, tenant, target, invoicing.PaymentIn{AmountKobo: *amount, PaymentID: reference, EntryID: &entryID,
				Connection: channel, How: "manual", By: by, Reasons: reasons}); err != nil {
				return err
			}
			res.Invoice = &x
		}
		return matched(ctx, tx, tenant, entryID, m.now())
	})
	if err != nil {
		return res, err
	}
	return res, m.paid(ctx, tenant, res, became, counted)
}

// UndoOrderPayment undoes a payment counted towards an order (a wrong
// match): the order goes back to unpaid when that payment paid it, and is
// refused when the order went on to be fulfilled (storefront.UndoPayment).
// The money goes back to be matched: the suggestion that led to the match
// is not offered again, the payment's other suggestions are.
func (m *Matcher) UndoOrderPayment(ctx context.Context, tenant, payment uuid.UUID, by string) (storefront.Order, error) {
	var ord storefront.Order
	orders, err := m.orders()
	if err != nil {
		return ord, err
	}
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entry uuid.UUID
		var err error
		if ord, entry, err = orders.UndoPayment(ctx, tx, tenant, payment, by); err != nil {
			return orderErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'rejected', decided_at = now() WHERE tenant_id = $1 AND order_payment_id = $2`, tenant, payment); err != nil {
			return err
		}
		// Back to be matched, unless the entry counts towards something
		// again (an undo of an undone payment changes nothing).
		other, err := storefront.CountedTowards(ctx, tx, tenant, entry)
		if err != nil || other != "" {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'open', decided_at = NULL WHERE tenant_id = $1 AND entry_id = $2 AND status = 'closed'`, tenant, entry); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ($1, $2,
			CASE WHEN EXISTS (SELECT 1 FROM money_suggestions WHERE entry_id = $1 AND status = 'open') THEN 'suggested' ELSE 'unmatched' END)
			ON CONFLICT (entry_id) DO UPDATE SET state = EXCLUDED.state, considered_at = now()`, entry, tenant)
		return err
	})
	return ord, err
}
