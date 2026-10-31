package links

// Statements, credit terms and limits between linked businesses (L5;
// docs/spec/business-links.md §10; migration 00281). The seller's terms
// and limit for a link are its trade settings; the buyer sees them as a
// mirror (supplier_terms, written with its prices). An order that would
// take the buyer's open balance past the limit is flagged at the seller:
// not accepted by itself, and the seller may still accept it. The
// statement each side sees is computed from the invoices and the bills.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// openInvoiceStatuses are an invoice's statuses while something is owed.
var openInvoiceStatuses = []string{"sent", "part_paid", "overdue"}

// acceptedOrderStatuses are a storefront order's statuses once accepted.
var acceptedOrderStatuses = []string{"accepted", "paid", "dispatched", "on_the_way", "arrived", "completed"}

// OpenBalanceTx is what a linked buyer owes the seller in scope: its open
// invoices' dues, and its accepted orders not yet invoiced (raised on
// delivery, they are owed already).
func OpenBalanceTx(ctx context.Context, tx pgx.Tx, seller, buyer, link uuid.UUID) (int64, error) {
	var invoices, orders int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_kobo - paid_kobo), 0)::bigint FROM customer_invoices
		WHERE tenant_id = $1 AND customer_key = $2 AND status = ANY ($3)`, seller, KeyPrefix+buyer.String(), openInvoiceStatuses).Scan(&invoices); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(s.total_kobo), 0)::bigint FROM link_orders o JOIN storefront_orders s ON s.id = o.order_id
		WHERE o.tenant_id = $1 AND o.link_id = $2 AND o.invoice_id IS NULL AND s.status = ANY ($3)`, seller, link, acceptedOrderStatuses).Scan(&orders); err != nil {
		return 0, err
	}
	return invoices + orders, nil
}

// Credit is what a credit check found: the limit, the balance before the
// order, and whether the order takes the buyer past the limit.
type Credit struct {
	LimitKobo   int64
	BalanceKobo int64
	Over        bool
	// Changed: the order's flag was set or cleared by this check.
	Changed bool
}

// CreditCheckTx checks a new order from a linked buyer against the limit
// the seller in scope set for the link, and flags it (or clears its flag)
// accordingly. Only an order not yet accepted is checked.
func CreditCheckTx(ctx context.Context, tx pgx.Tx, seller uuid.UUID, ref string) (Order, Credit, error) {
	o, err := GetOrderTx(ctx, tx, ref, true)
	if err != nil {
		return o, Credit{}, err
	}
	var flagged *time.Time
	var limit *int64
	err = tx.QueryRow(ctx, `SELECT o.over_limit_at, (SELECT t.credit_limit_kobo FROM trade_settings t WHERE t.tenant_id = o.tenant_id AND t.link_id = o.link_id)
		FROM link_orders o WHERE o.order_id = $1`, o.ID).Scan(&flagged, &limit)
	if err != nil {
		return o, Credit{}, err
	}
	c := Credit{}
	if o.Status == OrderReceived && limit != nil {
		c.LimitKobo = *limit
		if c.BalanceKobo, err = OpenBalanceTx(ctx, tx, seller, o.Buyer, o.LinkID); err != nil {
			return o, c, err
		}
		c.Over = c.BalanceKobo+o.TotalKobo > c.LimitKobo
	}
	switch {
	case c.Over && flagged == nil:
		_, err = tx.Exec(ctx, `UPDATE link_orders SET over_limit_at = now(), updated_at = now() WHERE order_id = $1`, o.ID)
		c.Changed = true
	case !c.Over && flagged != nil:
		_, err = tx.Exec(ctx, `UPDATE link_orders SET over_limit_at = NULL, updated_at = now() WHERE order_id = $1`, o.ID)
		c.Changed = true
	}
	if err != nil {
		return o, c, err
	}
	if c.Over {
		// Checked again as the balance goes down.
		err = dueTx(ctx, tx, seller, "credit", o.ID, time.Hour)
	} else {
		err = Undue(ctx, tx, seller, "credit", o.ID)
	}
	if err != nil {
		return o, c, err
	}
	o, err = GetOrderTx(ctx, tx, ref, false)
	return o, c, err
}

// HeldNoteTx says on the order why it waits (or that it waits no
// longer: note ""), in the seller's scope; the order's mirror carries it
// to the buyer's purchase order. Only an order not yet answered: the
// seller's own words on a later step are left alone.
func HeldNoteTx(ctx context.Context, tx pgx.Tx, o Order, note string) error {
	_, err := tx.Exec(ctx, `UPDATE link_orders o SET seller_note = $2, updated_at = now() FROM storefront_orders s
		WHERE o.order_id = $1 AND s.id = o.order_id AND s.status = 'received'`, o.ID, note)
	return err
}

// --- the statement ---

// StatementDoc is an open document on a statement: an invoice we sent
// them, or a bill they sent us.
type StatementDoc struct {
	ID        uuid.UUID `json:"id"`
	Reference string    `json:"reference"`
	Status    string    `json:"status"`
	TotalKobo int64     `json:"total_kobo"`
	PaidKobo  int64     `json:"paid_kobo"`
	DueKobo   int64     `json:"due_kobo"`
	DueOn     string    `json:"due_on,omitempty"`
	Overdue   bool      `json:"overdue"`
	Disputed  bool      `json:"disputed"`
	IssuedAt  time.Time `json:"issued_at"`
}

// StatementSide is one direction of the trade: what is owed, how much of
// it is overdue, the oldest due date, the terms and limit, and the open
// documents, oldest due first.
type StatementSide struct {
	OwedKobo        int64          `json:"owed_kobo"`
	OverdueKobo     int64          `json:"overdue_kobo"`
	OldestDue       string         `json:"oldest_due,omitempty"`
	TermsDays       *int           `json:"terms_days,omitempty"`
	CreditLimitKobo *int64         `json:"credit_limit_kobo,omitempty"`
	AvailableKobo   *int64         `json:"available_kobo,omitempty"`
	Documents       []StatementDoc `json:"documents"`
}

// Statement is a link's statement as the business in scope sees it: what
// they owe us (our invoices to them) and what we owe them (their bills to
// us), each when there is any or terms are set.
type Statement struct {
	LinkID   uuid.UUID      `json:"link"`
	Business string         `json:"business"`
	TheyOwe  *StatementSide `json:"they_owe,omitempty"`
	WeOwe    *StatementSide `json:"we_owe,omitempty"`
}

// StatementTx is a link's statement for the business in scope.
func StatementTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (Statement, error) {
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return Statement{}, err
	}
	st := Statement{LinkID: link, Business: l.Name}
	today := time.Now().In(lagos).Format(time.DateOnly)

	// What they owe us: our open invoices to them, and our terms.
	rows, err := tx.Query(ctx, `SELECT id, number, status, total_kobo, paid_kobo, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''), disputed, COALESCE(sent_at, created_at)
		FROM customer_invoices WHERE tenant_id = $1 AND customer_key = $2 AND status = ANY ($3) ORDER BY due_on NULLS LAST, number`,
		tenant, KeyPrefix+l.Business.String(), openInvoiceStatuses)
	if err != nil {
		return st, err
	}
	theirs, err := statementDocs(rows, today)
	if err != nil {
		return st, err
	}
	var terms *int
	var limit *int64
	if err := tx.QueryRow(ctx, `SELECT terms_days, credit_limit_kobo FROM trade_settings WHERE tenant_id = $1 AND link_id = $2`, tenant, link).Scan(&terms, &limit); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	if len(theirs) > 0 || terms != nil || limit != nil {
		side := sideOf(theirs, terms, limit)
		// Accepted orders not yet invoiced count against the limit too.
		if limit != nil {
			bal, err := OpenBalanceTx(ctx, tx, tenant, l.Business, link)
			if err != nil {
				return st, err
			}
			avail := *limit - bal
			side.AvailableKobo = &avail
		}
		st.TheyOwe = &side
	}

	// What we owe them: their open bills to us, and the terms they give us.
	rows, err = tx.Query(ctx, `SELECT id, reference, status, amount_kobo, paid_kobo, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''), disputed, created_at
		FROM bills WHERE tenant_id = $1 AND link_id = $2 AND status NOT IN ('paid', 'void') AND NOT voided ORDER BY due_on NULLS LAST, reference`, tenant, link)
	if err != nil {
		return st, err
	}
	ours, err := statementDocs(rows, today)
	if err != nil {
		return st, err
	}
	terms, limit = nil, nil
	if err := tx.QueryRow(ctx, `SELECT terms_days, credit_limit_kobo FROM supplier_terms WHERE tenant_id = $1 AND link_id = $2`, tenant, link).Scan(&terms, &limit); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	if len(ours) > 0 || terms != nil || limit != nil {
		side := sideOf(ours, terms, limit)
		if limit != nil {
			avail := *limit - side.OwedKobo
			side.AvailableKobo = &avail
		}
		st.WeOwe = &side
	}
	return st, nil
}

var lagos = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

func statementDocs(rows pgx.Rows, today string) ([]StatementDoc, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StatementDoc, error) {
		var d StatementDoc
		err := r.Scan(&d.ID, &d.Reference, &d.Status, &d.TotalKobo, &d.PaidKobo, &d.DueOn, &d.Disputed, &d.IssuedAt)
		d.DueKobo = d.TotalKobo - d.PaidKobo
		d.Overdue = d.DueOn != "" && d.DueOn < today && d.DueKobo > 0
		return d, err
	})
	if out == nil {
		out = []StatementDoc{}
	}
	return out, err
}

func sideOf(docs []StatementDoc, terms *int, limit *int64) StatementSide {
	s := StatementSide{TermsDays: terms, CreditLimitKobo: limit, Documents: docs}
	for _, d := range docs {
		s.OwedKobo += d.DueKobo
		if d.Overdue {
			s.OverdueKobo += d.DueKobo
		}
		if d.DueOn != "" && (s.OldestDue == "" || d.DueOn < s.OldestDue) {
			s.OldestDue = d.DueOn
		}
	}
	return s
}

// TermsDueOn is the due date an invoice to a linked buyer takes from the
// seller's terms for the link, in scope: "" when none are set.
func TermsDueOn(ctx context.Context, tx pgx.Tx, seller, buyer uuid.UUID) (string, error) {
	l, err := LinkWith(ctx, tx, buyer)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var days *int
	err = tx.QueryRow(ctx, `SELECT terms_days FROM trade_settings WHERE tenant_id = $1 AND link_id = $2`, seller, l.ID).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && days == nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return time.Now().In(lagos).AddDate(0, 0, *days).Format(time.DateOnly), nil
}
