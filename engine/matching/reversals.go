package matching

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/notices"
)

// Money that went back to the payer (docs/spec/schools.md 18, decided
// 2026-10-23; docs/matching.md#money-that-went-back), for every business.
// A payment the rail returned or reversed (an entry money in with status
// reversed, naming the first: payment.returned, a POS sale reversed), or a
// refund that settled (refund.processed, naming the payment it refunds,
// by its reference or a credit's credit_id), is recorded once against the
// credit it takes back (money_reversals). What the credit counts for is
// then less what went back (taskiem_money_entry_reversed, 00213): its
// matches to invoices are undone, each invoice reopened; what is left of
// it, if any, is matched again by the permanent-account rule when it was
// paid into a customer's account, else waits in To match with the reason.
// The owners are told, and the people who match money. Parts counted
// towards storefront orders are left: an order has its own refunds.

// Why reversed money is held.
const (
	HeldReversed       = "the money went back to the payer"
	HeldPartlyReversed = "part of this payment went back to the payer; match what is left"
)

// AlertReversed is the kind of the alert a business gets when money it had
// counted towards invoices went back to the payer; once per reversal.
const AlertReversed = "money_reversed"

// Reversals looks at every tenant's money that went back and has not been
// looked at.
func (m *Matcher) Reversals(ctx context.Context) error {
	rows, err := m.Invoices.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_unreversed_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := m.TenantReversals(ctx, t); err != nil && ctx.Err() == nil {
			m.log().Warn("matching: reversals", "tenant", t, "err", err)
		}
	}
	return nil
}

// reversal is an entry saying money went back.
type reversal struct {
	id         uuid.UUID
	corrects   *uuid.UUID
	kind       string
	channel    string
	reference  string
	amountKobo *int64
}

// TenantReversals looks at up to 100 of a tenant's reversals not yet
// looked at, oldest first.
func (m *Matcher) TenantReversals(ctx context.Context, tenant uuid.UUID) error {
	var list []reversal
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.id, e.corrects, e.kind, e.channel, e.reference, e.amount_kobo FROM money_entries e
			WHERE e.tenant_id = $1 AND (e.direction = 'in' AND e.status = 'reversed' OR e.kind = 'refund' AND e.status = 'settled')
			  AND NOT EXISTS (SELECT 1 FROM money_reversals r WHERE r.by_entry = e.id)
			ORDER BY e.recorded_at, e.id LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (reversal, error) {
			var x reversal
			return x, r.Scan(&x.id, &x.corrects, &x.kind, &x.channel, &x.reference, &x.amountKobo)
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, r := range list {
		if err := m.reverse(ctx, tenant, r); err != nil {
			return err
		}
	}
	return nil
}

// original is the money-in entry a reversal takes back, or nil.
func original(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r reversal) (*uuid.UUID, error) {
	var id uuid.UUID
	var err error
	if r.corrects != nil {
		err = tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND id = $2 AND direction = 'in' AND status = 'settled'`, tenant, *r.corrects).Scan(&id)
	} else {
		// A refund (or a reversal naming no entry) names the payment by its
		// reference, or, for a credit to a permanent account, its credit_id.
		err = tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND direction = 'in' AND status = 'settled' AND id <> $4
			AND (credit_id = $3 OR credit_id IS NULL AND reference = $3) ORDER BY recorded_at, id LIMIT 1`, tenant, r.channel, r.reference, r.id).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// undoneInvoice is an invoice whose part a reversal undid.
type undoneInvoice struct {
	number string
	part   uuid.UUID
}

// reverse records one reversal and undoes what its credit counted towards.
func (m *Matcher) reverse(ctx context.Context, tenant uuid.UUID, r reversal) error {
	var again *entry
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		again = nil
		orig, err := original(ctx, tx, tenant, r)
		if err != nil {
			return err
		}
		if orig != nil {
			if err := moneylog.LockEntry(ctx, tx, *orig); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO money_reversals (by_entry, tenant_id, entry_id, amount_kobo) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			r.id, tenant, orig, r.amountKobo)
		if err != nil || tag.RowsAffected() == 0 || orig == nil {
			return err // looked at meanwhile, or no credit of ours to take back
		}
		// Its parts towards invoices, newest first, each undone.
		rows, err := tx.Query(ctx, `SELECT p.id, p.invoice_id, i.number FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
			WHERE p.tenant_id = $1 AND p.entry_id = $2 AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
			ORDER BY p.created_at DESC, p.id`, tenant, *orig)
		if err != nil {
			return err
		}
		type part struct {
			id, invoice uuid.UUID
			number      string
		}
		parts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (part, error) {
			var p part
			return p, row.Scan(&p.id, &p.invoice, &p.number)
		})
		if err != nil {
			return err
		}
		var undone []undoneInvoice
		for _, p := range parts {
			if _, err := m.Invoices.UndoPayment(ctx, tx, tenant, p.invoice.String(), p.id, "system:reversal"); err != nil {
				return fmt.Errorf("undoing %s: %w", p.number, err)
			}
			undone = append(undone, undoneInvoice{number: p.number, part: p.id})
		}
		if _, err := tx.Exec(ctx, `UPDATE money_reversals SET undone = $2 WHERE by_entry = $1`, r.id, len(undone)); err != nil {
			return err
		}
		if m.Reversed != nil {
			ids := make([]uuid.UUID, len(undone))
			for i, u := range undone {
				ids[i] = u.part
			}
			if err := m.Reversed(ctx, tx, tenant, *orig, ids); err != nil {
				return err
			}
		}
		// What the credit is now.
		var considered bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_match_state WHERE entry_id = $1)`, *orig).Scan(&considered); err != nil {
			return err
		}
		left, _, err := moneylog.EntryLeft(ctx, tx, *orig)
		if err != nil {
			return err
		}
		if considered {
			// Its suggestions were for what it was: none is offered again.
			if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = now() WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`, tenant, *orig); err != nil {
				return err
			}
		}
		switch {
		case !considered:
			// Not looked at yet: matching takes it for what is left.
		case left <= 0:
			if err := setState(ctx, tx, tenant, *orig, "matched", HeldReversed); err != nil {
				return err
			}
		default:
			var how *string
			if err := tx.QueryRow(ctx, `SELECT how FROM money_entry_customers WHERE entry_id = $1`, *orig).Scan(&how); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if how != nil && *how == "account" {
				// Paid into a customer's account: what is left is matched
				// again, oldest open invoice first, once this commits.
				e := entry{ID: *orig, AmountKobo: left}
				if err := tx.QueryRow(ctx, `SELECT channel, reference, COALESCE(account_number, '') FROM money_entries WHERE id = $1`, *orig).
					Scan(&e.Channel, &e.Reference, &e.AccountNumber); err != nil {
					return err
				}
				again = &e
			} else if err := setState(ctx, tx, tenant, *orig, "unmatched", HeldPartlyReversed); err != nil {
				return err
			}
		}
		if len(undone) == 0 {
			return nil
		}
		return m.tellReversed(ctx, tx, tenant, r, undone)
	})
	if err != nil || again == nil {
		return err
	}
	_, err = m.permanent(ctx, tenant, *again)
	return err
}

func setState(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, state, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, held_reason) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET state = $3, held_reason = $4, considered_at = now()`, entry, tenant, state, reason)
	return err
}

// tellReversed tells the owners (alerts.TellOwners: their notifications,
// chat, and the business's alert channels) and the people who match money
// (their notifications) that invoices are open again.
func (m *Matcher) tellReversed(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r reversal, undone []undoneInvoice) error {
	numbers := make([]string, len(undone))
	for i, u := range undone {
		numbers[i] = u.number
	}
	what := "Money went back to the payer"
	if r.kind == "refund" {
		what = "A payment was refunded"
	}
	if r.amountKobo != nil {
		what += " (" + invoicing.Naira(*r.amountKobo) + ")"
	}
	open := strings.Join(numbers, ", ")
	title := what + ": " + open + " is open again"
	if len(numbers) > 1 {
		title = what + ": " + open + " are open again"
	}
	body := "The payment that counted towards " + open + " went back to the payer, so it no longer counts: the invoice's balance is due again. " +
		"Any receipt for it no longer stands. Check the invoice, and ask the family or customer to pay again if they still owe it."
	link := "/invoices/" + numbers[0]
	al := alerts.Alert{Kind: AlertReversed, Dedup: r.id.String(), Title: title, Body: body, Link: link,
		Detail: map[string]any{"invoices": numbers, "entry": r.id.String()}}
	if _, err := alerts.TellOwners(ctx, tx, tenant, al); err != nil {
		return err
	}
	people, err := matchers(ctx, tx, tenant)
	if err != nil {
		return err
	}
	return notices.AddTx(ctx, tx, tenant, people, notices.Notice{Kind: notices.Alert, Level: notices.Warning, Title: title, Body: body, Link: link,
		Group: "reversed:" + r.id.String()})
}

// matchers are the tenant's active members, not owners (told already), who
// match money: a built-in role holding invoice.manage, or a custom role
// (the Bursar) whose permissions say so.
func matchers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
	builtin, manage := access.BuiltinNames(), access.Holding(access.InvoiceManage)
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND u.status = 'active' AND ms.role <> 'owner'
		  AND NOT EXISTS (SELECT 1 FROM memberships o WHERE o.tenant_id = ms.tenant_id AND o.user_id = ms.user_id AND o.role = 'owner')
		  AND CASE WHEN ms.role = ANY ($2) THEN ms.role = ANY ($3)
		      ELSE EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = ms.tenant_id AND r.name = ms.role AND $4 = ANY (r.permissions)) END
		ORDER BY ms.user_id`, tenant, builtin, manage, access.InvoiceManage)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
