package moneydigest

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/schools"
)

// The schools section (docs/spec/schools.md 13.3, B9; docs/schools.md
// #summaries): when the Schools area is on and a term is current, the
// daily summary and the monthly email gain a line: what was collected
// for fees in the period, what is outstanding on the term's invoices,
// and the share of the term's students cleared (B8's rule). It is read
// when the summary is rendered, like the rest of the figures.

// SchoolSummary is the schools section's figures.
type SchoolSummary struct {
	Term string `json:"term"`
	// CollectedKobo is what was counted towards term invoices in the
	// period (whatever the term).
	CollectedKobo int64 `json:"collected_kobo"`
	// OutstandingKobo is what is left on the current term's sent invoices
	// now; Students and Cleared the term's students and those cleared.
	OutstandingKobo int64 `json:"outstanding_kobo"`
	Students        int   `json:"students"`
	Cleared         int   `json:"cleared"`
	ClearedPercent  int   `json:"cleared_percent"`
}

// SchoolSummarise reads the schools section for [from, to) with rec (the
// school's record, which opens the students' sealed names for the
// clearance rule): nil when rec is nil, the area is off or no term is
// current.
func SchoolSummarise(ctx context.Context, tx pgx.Tx, rec *schools.Schools, tenant uuid.UUID, from, to time.Time) (*SchoolSummary, error) {
	if rec == nil {
		return nil, nil
	}
	on, err := schools.Enabled(ctx, tx, tenant)
	if err != nil || !on {
		return nil, err
	}
	term, ok, err := rec.CurrentTerm(ctx, tx, tenant)
	if err != nil || !ok {
		return nil, err
	}
	out := &SchoolSummary{Term: term.Name + " " + term.Session}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(p.amount_kobo), 0) FROM customer_invoice_payments p JOIN school_term_invoices ti ON ti.invoice_id = p.invoice_id
		WHERE ti.tenant_id = $1 AND p.undoes IS NULL AND p.created_at >= $2 AND p.created_at < $3
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)`, tenant, from, to).Scan(&out.CollectedKobo); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(GREATEST(i.total_kobo - i.paid_kobo, 0)), 0) FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND i.sent_at IS NOT NULL AND i.status <> 'void'`, tenant, term.ID).Scan(&out.OutstandingKobo); err != nil {
		return nil, err
	}
	cl, err := rec.TermClearance(ctx, tx, tenant, term.ID, nil)
	if err != nil {
		return nil, err
	}
	out.Students, out.Cleared = cl.Totals.Students, cl.Totals.Cleared
	if out.Students > 0 {
		out.ClearedPercent = out.Cleared * 100 / out.Students
	}
	return out, nil
}

// SchoolLine is the schools section in l, one line; "" for none.
func SchoolLine(l lang.Tag, s *SchoolSummary) string {
	if s == nil {
		return ""
	}
	return lang.Default().Text(l, "money.daily.school", "collected", invoicing.Naira(s.CollectedKobo), "outstanding", invoicing.Naira(s.OutstandingKobo),
		"cleared", strconv.Itoa(s.ClearedPercent), "term", s.Term)
}

// schoolEmail is the schools section of the monthly email.
func schoolEmail(s *SchoolSummary) string {
	if s == nil {
		return ""
	}
	n := invoicing.Naira
	return fmt.Sprintf("\nSchool fees (%s): %s collected this month; %s outstanding; %d of %d students cleared (%d%%).\n", s.Term, n(s.CollectedKobo), n(s.OutstandingKobo), s.Cleared, s.Students, s.ClearedPercent)
}
