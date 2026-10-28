package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Money a person entered, and the second person (docs/spec/schools.md 8,
// B5; docs/schools.md#cash-and-transfers). A school's cash threshold
// makes cash posted at the desk wait for a second person (engine/schools);
// money a person entered on Money (cash, or a statement line uploaded) is
// the same money, so counting it towards one of the school's invoices
// waits the same way: at or above the threshold the match is recorded as
// a request (school_pending_postings, kind RequestKind) and nothing is
// counted until someone other than the requester approves it
// (ApproveHeld). Money a rail reported (its events, the look-back, a
// step's answer) is the rail's word and is never held.

// RequestKind is the kind of the request a held match records.
const RequestKind = "cash_match"

// ErrHeld is what a held match unwraps to.
var ErrHeld = errors.New("matching: needs approval")

// Held is what a match of money a person entered answers when it waits
// for a second person: the request recorded, nothing counted.
type Held struct {
	RequestID  uuid.UUID
	AmountKobo int64
}

func (e *Held) Error() string {
	return fmt.Sprintf("%v: matching %s of money a person entered waits for a second person's approval", ErrHeld, invoicing.Naira(e.AmountKobo))
}

func (e *Held) Unwrap() error { return ErrHeld }

// personEntered reports whether an entry's source is a person's word:
// entered by hand, or a line of a statement they uploaded.
func personEntered(source string) bool { return source == "entered" || source == "upload" }

// CashLimit is the school's cash threshold for money from source: nil
// when the business runs no school with one, or the money is a rail's.
func CashLimit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, source string) (*int64, error) {
	if !personEntered(source) {
		return nil, nil
	}
	var limit *int64
	err := tx.QueryRow(ctx, `SELECT cash_threshold_kobo FROM school_settings WHERE tenant_id = $1 AND enabled`, tenant).Scan(&limit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return limit, err
}

// EnteredUnapproved is what money a person entered (cash, a statement
// line, cash posted at a school's desk) counts towards the invoices
// without a second person having approved it: what a school's cash
// threshold weighs a further such payment with, per invoice, so that
// splitting the money does not pass under it.
func EnteredUnapproved(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, invoices []uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(p.amount_kobo), 0)::bigint FROM customer_invoice_payments p JOIN money_entries e ON e.id = p.entry_id
		WHERE p.tenant_id = $1 AND p.invoice_id = ANY ($2) AND p.undoes IS NULL
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
		  AND e.source IN ('entered', 'upload')
		  AND NOT EXISTS (SELECT 1 FROM school_pending_postings r WHERE r.tenant_id = p.tenant_id AND r.status = 'posted' AND r.decided_by IS NOT NULL
		                  AND COALESCE(r.result->>'entry_id', r.details->>'entry_id') = e.id::text)`, tenant, invoices).Scan(&n)
	return n, err
}

// heldRequest is what a held match asks for, as its request's details
// keep it.
type heldRequest struct {
	Entry      uuid.UUID  `json:"entry_id"`
	Suggestion *uuid.UUID `json:"suggestion,omitempty"`
	Parts      []Part     `json:"parts"`
	CreditRest bool       `json:"credit_rest,omitempty"`
	How        string     `json:"how"`
	Reasons    []string   `json:"reasons,omitempty"`
}

// holdMatch decides, in count's transaction, whether a match waits for a
// second person, and records the request when it does. parts are the
// match's, their amounts known. A match automatic matching asked for is
// refused instead (a person decides); a payment whose match waits
// already is refused.
func (m *Matcher) holdMatch(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c counting, source string, amount int64, parts []Part) (*Held, error) {
	limit, err := CashLimit(ctx, tx, tenant, source)
	if err != nil || limit == nil {
		return nil, err
	}
	// Held when the money is at or above the threshold, or when a part
	// with what money a person entered already counts towards its
	// invoice without a second person is: two entries under it, matched
	// to one invoice, are not let through.
	var invoice *uuid.UUID
	var towards int64
	over := amount >= *limit
	for _, p := range parts {
		if p.Invoice == "" {
			continue
		}
		towards += p.AmountKobo
		x, err := m.Invoices.Get(ctx, tx, tenant, p.Invoice)
		if err != nil {
			return nil, err
		}
		if invoice == nil {
			id := x.ID
			invoice = &id
		}
		if !over {
			prior, err := EnteredUnapproved(ctx, tx, tenant, []uuid.UUID{x.ID})
			if err != nil {
				return nil, err
			}
			over = prior+p.AmountKobo >= *limit
		}
	}
	if invoice == nil || !over {
		return nil, nil
	}
	if c.how == "suggested_auto" {
		return nil, fmt.Errorf("%w: money a person entered at or above the school's cash threshold is matched by a person", invoicing.ErrState)
	}
	var waiting bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_pending_postings WHERE tenant_id = $1 AND kind = $2 AND status = 'pending' AND details->>'entry_id' = $3)`,
		tenant, RequestKind, c.entry.String()).Scan(&waiting); err != nil {
		return nil, err
	}
	if waiting {
		return nil, fmt.Errorf("%w: a match of this payment already waits for a second person's approval", invoicing.ErrState)
	}
	var student *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT student_id FROM school_term_invoices WHERE invoice_id = $1`, *invoice).Scan(&student); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	raw, _ := json.Marshal(heldRequest{Entry: c.entry, Suggestion: c.suggestion, Parts: c.parts, CreditRest: c.creditRest, How: c.how, Reasons: c.reasons})
	var user *uuid.UUID
	if id, err := uuid.Parse(c.by); err == nil {
		user = &id
	}
	h := &Held{RequestID: uuid.New(), AmountKobo: towards}
	_, err = tx.Exec(ctx, `INSERT INTO school_pending_postings (id, tenant_id, kind, status, student_id, invoice_id, amount_kobo, details, requested_by, requested_user)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6, $7, $8, $9)`, h.RequestID, tenant, RequestKind, student, invoice, towards, raw, c.by, user)
	return h, err
}

// ApproveHeld makes a held match a second person approved (details are
// its request's), in tx, as its requester (by) asked for it. Call after
// once tx commits.
func (m *Matcher) ApproveHeld(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, details map[string]any, by string) (Split, func(context.Context) error, error) {
	raw, _ := json.Marshal(details)
	var r heldRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return Split{}, nil, err
	}
	s, done, _, err := m.countTx(ctx, tx, tenant, counting{entry: r.Entry, suggestion: r.Suggestion, parts: r.Parts, how: r.How, by: by, reasons: r.Reasons,
		creditRest: r.CreditRest, approved: true})
	if err != nil {
		return Split{}, nil, err
	}
	return s, func(ctx context.Context) error { return m.paid(ctx, tenant, done) }, nil
}
