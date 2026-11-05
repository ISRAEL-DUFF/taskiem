package moneylog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Counting money towards invoices and storefront orders: one path for
// both. Parts.Count counts a part of an entry towards a target and
// Parts.Undo undoes one; what they share is the entry's lock, the entry
// counted towards the same target once (unless a further part is meant,
// Counting.Add), a part no more than is left of the entry, the row; and
// the undoing, a row naming the part, once. What
// differs stays with the target's owner, as the callbacks of Counting and
// Undoing: invoicing (Invoices.RecordPayment, UndoPayment) and storefront
// (Orders.CountPayment, UndoPayment) find and lock the invoice or order,
// apply its own rules, move it, and give the row's own columns. The two
// keep their own tables (Parts).

// LockEntry takes the lock the entry is counted under (EntryLockSQL).
func LockEntry(ctx context.Context, tx pgx.Tx, entry uuid.UUID) error {
	_, err := tx.Exec(ctx, EntryLockSQL, entry)
	return err
}

// LockLeft takes the entry's lock and says what is left of it to count
// (EntryLeft), for counting it in parts.
func LockLeft(ctx context.Context, tx pgx.Tx, entry uuid.UUID) (left int64, ok bool, err error) {
	if err := LockEntry(ctx, tx, entry); err != nil {
		return 0, false, err
	}
	return EntryLeft(ctx, tx, entry)
}

// Parts is where one kind of target keeps the parts counted towards it:
// a payment row each, its undoing a row naming it (undoes), never changed.
type Parts struct {
	// Table holds the rows; Target is its column naming the invoice or
	// order.
	Table, Target string
	// Suggestion is the money_suggestions column naming the part a
	// suggestion led to.
	Suggestion string
}

// The parts of invoices and of storefront orders.
var (
	InvoiceParts = Parts{Table: "customer_invoice_payments", Target: "invoice_id", Suggestion: "payment_id"}
	OrderParts   = Parts{Table: "storefront_order_payments", Target: "order_id", Suggestion: "order_payment_id"}
)

// Cols are a row's own columns (the target's), beside those Count and
// Undo write.
type Cols map[string]any

// Counting is a part to count towards a target, and the target's own
// side of counting it.
type Counting struct {
	Tenant uuid.UUID
	// Entry is the money log entry the part counts; nil for money with
	// none (then no lock and no check of an entry).
	Entry      *uuid.UUID
	AmountKobo int64
	How, By    string
	Reasons    []string
	// Add is a further part of the entry towards a target it already
	// counts towards (a person splitting the payment again): it is added
	// to the first, a new row, rather than taken as counted already. Left
	// false by the paths that may hear of the same money twice (the
	// rails' payments, a customer's own account), so those count it once.
	Add bool
	// Open finds the target under the entry's lock, locking its row when
	// counting may move it, and returns its id.
	Open func() (uuid.UUID, error)
	// Refused words why the entry has no room for the part (EntryHas's
	// reason) as the target's error.
	Refused func(why string) error
	// Decide applies the target's own rules once the entry has room for
	// the part: it refuses, or moves the target, and returns the row's own
	// columns; write false writes no row (the target counts this money
	// already, or there is no entry to record).
	Decide func() (cols Cols, write bool, err error)
}

// Counted is what Count did.
type Counted struct {
	// Target is the invoice's or order's id (Open's).
	Target uuid.UUID
	// Already is true when the entry already counts towards the target:
	// nothing was done.
	Already bool
	// ID is the row written; uuid.Nil when none was.
	ID uuid.UUID
}

// Count counts a part towards a target, in the caller's transaction:
// under the entry's lock, the same entry again towards the same target is
// a no-op (Already) unless a further part is meant (Add), and a part more
// than is left of the entry is refused (Refused; the database refuses it
// too, 00210); then the target decides, and the row is written.
func (t Parts) Count(ctx context.Context, tx pgx.Tx, c Counting) (Counted, error) {
	var out Counted
	if c.Entry != nil {
		if err := LockEntry(ctx, tx, *c.Entry); err != nil {
			return out, err
		}
	}
	id, err := c.Open()
	if err != nil {
		return out, err
	}
	out.Target = id
	if c.Entry != nil {
		var here bool
		if !c.Add {
			//sqlcheck:built table and column names from InvoiceParts or OrderParts, package-level literals
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+t.Table+` p WHERE p.`+t.Target+` = $1 AND p.entry_id = $2 AND p.undoes IS NULL
				AND NOT EXISTS (SELECT 1 FROM `+t.Table+` u WHERE u.undoes = p.id))`, id, *c.Entry).Scan(&here); err != nil {
				return out, err
			}
		}
		if here {
			out.Already = true
			return out, nil
		}
		why, err := EntryHas(ctx, tx, c.Tenant, *c.Entry, c.AmountKobo)
		if err != nil {
			return out, err
		}
		if why != "" {
			return out, c.Refused(why)
		}
	}
	cols, write, err := c.Decide()
	if err != nil || !write {
		return out, err
	}
	reasons := c.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	raw, _ := json.Marshal(reasons)
	row := Cols{"id": uuid.New(), "tenant_id": c.Tenant, t.Target: id, "entry_id": c.Entry, "amount_kobo": c.AmountKobo, "how": c.How, "by": c.By, "reasons": raw}
	for k, v := range cols {
		row[k] = v
	}
	if err := t.insert(ctx, tx, row); err != nil {
		return out, err
	}
	out.ID = row["id"].(uuid.UUID)
	return out, nil
}

// Paid reports whether the target has a part not undone, of any money.
func (t Parts) Paid(ctx context.Context, tx pgx.Tx, target uuid.UUID) (bool, error) {
	var paid bool
	//sqlcheck:built table and column names from InvoiceParts or OrderParts, package-level literals
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+t.Table+` p WHERE p.`+t.Target+` = $1 AND p.undoes IS NULL
		AND NOT EXISTS (SELECT 1 FROM `+t.Table+` u WHERE u.undoes = p.id))`, target).Scan(&paid)
	return paid, err
}

// Part is a part counted towards a target.
type Part struct {
	ID, Target uuid.UUID
	// Entry is the money log entry it counts; nil for money with none.
	Entry      *uuid.UUID
	AmountKobo int64
}

// ErrNoPart is a part Undo cannot find: no such row, or one that undoes
// another.
var ErrNoPart = errors.New("moneylog: no such part")

// Undoing is a part to undo, and the target's own side of undoing it.
type Undoing struct {
	Tenant, Part uuid.UUID
	By           string
	// Open finds the part's target under the entry's lock, locking its
	// row when undoing may move it. Nil: nothing to find.
	Open func(p Part) error
	// Decide applies the target's own refusals and moves it back; it
	// returns the undo row's own columns. Nil: none.
	Decide func(p Part) (Cols, error)
}

// Undo undoes a part, in the caller's transaction: its row stays, and a
// row naming it (how manual) takes it out, once. undone is false when it
// was undone already, before or meanwhile. ErrNoPart when there is no
// such part.
func (t Parts) Undo(ctx context.Context, tx pgx.Tx, u Undoing) (p Part, undone bool, err error) {
	var undoes *uuid.UUID
	//sqlcheck:built table and column names from InvoiceParts or OrderParts, package-level literals
	err = tx.QueryRow(ctx, `SELECT id, `+t.Target+`, entry_id, amount_kobo, undoes FROM `+t.Table+` WHERE tenant_id = $1 AND id = $2`, u.Tenant, u.Part).
		Scan(&p.ID, &p.Target, &p.Entry, &p.AmountKobo, &undoes)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && undoes != nil {
		return Part{}, false, ErrNoPart
	}
	if err != nil {
		return Part{}, false, err
	}
	if p.Entry != nil {
		if err := LockEntry(ctx, tx, *p.Entry); err != nil {
			return p, false, err
		}
	}
	if u.Open != nil {
		if err := u.Open(p); err != nil {
			return p, false, err
		}
	}
	var already bool
	//sqlcheck:built the table name from InvoiceParts or OrderParts, package-level literals
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+t.Table+` WHERE undoes = $1)`, p.ID).Scan(&already); err != nil || already {
		return p, false, err
	}
	var cols Cols
	if u.Decide != nil {
		if cols, err = u.Decide(p); err != nil {
			return p, false, err
		}
	}
	row := Cols{"id": uuid.New(), "tenant_id": u.Tenant, t.Target: p.Target, "amount_kobo": p.AmountKobo, "how": "manual", "by": u.By, "undoes": p.ID}
	for k, v := range cols {
		row[k] = v
	}
	err = t.insert(ctx, tx, row)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return p, false, nil // undone meanwhile
	}
	return p, err == nil, err
}

// insert writes a row of the table, its columns in name order.
func (t Parts) insert(ctx context.Context, tx pgx.Tx, row Cols) error {
	names := make([]string, 0, len(row))
	for k := range row {
		names = append(names, k)
	}
	sort.Strings(names)
	marks, args := make([]string, len(names)), make([]any, len(names))
	for i, k := range names {
		marks[i], args[i] = fmt.Sprintf("$%d", i+1), row[k]
	}
	//sqlcheck:built the table from InvoiceParts or OrderParts; column names are literal keys in Count, Undo and their callers' Decide; $n from a count
	_, err := tx.Exec(ctx, `INSERT INTO `+t.Table+` (`+strings.Join(names, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`)`, args...)
	return err
}
