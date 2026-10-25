package schools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/notices"
)

// Payment plans, the effect half (docs/spec/schools.md 11.3 and 12.1,
// B7; docs/schools.md#payment-plans). B6 records a family's request and
// the bursar's decision; an approved plan's instalments (as decided) are
// the schedule. The plan is in good standing while every instalment past
// its date plus the school's grace is covered by what was paid for the
// student since the plan was approved; otherwise it is in arrears, from
// the first instalment not covered. Debtors shows the standing, reminders
// skip families in good standing and remind those in arrears by the days
// past the instalment missed, and the check marks each missed instalment
// once (school_plan_arrears) and tells the bursars. PlanStanding is what
// clearance (B8) reads.

// Plan standings.
const (
	PlanGood    = "good"
	PlanArrears = "arrears"
	PlanSettled = "settled"
)

// MissedInstalment is an instalment past due plus the grace and not
// covered.
type MissedInstalment struct {
	Instalment int    `json:"instalment"`
	DueOn      string `json:"due_on"`
	AmountKobo int64  `json:"amount_kobo"`
	// ShortKobo is what is still missing of the instalments due so far.
	ShortKobo int64 `json:"short_kobo"`
	// DaysPast counts days past the instalment's date (the grace not
	// taken off).
	DaysPast int `json:"days_past"`
}

// PlanStanding is where a student's approved plan stands.
type PlanStanding struct {
	PlanID      uuid.UUID    `json:"plan_id"`
	StudentID   uuid.UUID    `json:"student_id"`
	State       string       `json:"state"`
	ApprovedAt  time.Time    `json:"approved_at"`
	Instalments []Instalment `json:"instalments"`
	GraceDays   int          `json:"grace_days"`
	// PaidSinceKobo is what was counted towards the student's invoices
	// since the plan was approved; DueSoFarKobo what the instalments past
	// their date plus the grace come to.
	PaidSinceKobo int64              `json:"paid_since_kobo"`
	DueSoFarKobo  int64              `json:"due_so_far_kobo"`
	Missed        []MissedInstalment `json:"missed"`
	// NextDueOn is the next instalment not yet past, and its amount.
	NextDueOn      string `json:"next_due_on,omitempty"`
	NextAmountKobo int64  `json:"next_amount_kobo,omitempty"`
}

// planRow is an approved plan as the standing reads it.
type planRow struct {
	id, student uuid.UUID
	decidedAt   time.Time
	instalments []Instalment
}

// PlanStanding is the standing of a student's latest approved plan: for a
// term, the latest approved since the term started; for uuid.Nil, the
// latest approved. nil when the student has none.
func (s *Schools) PlanStanding(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID) (*PlanStanding, error) {
	list, err := s.planStandings(ctx, tx, tenant, term, student)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// planStandings is the standing of each student's latest approved plan
// (one student's when student is given).
func (s *Schools) planStandings(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID, student ...uuid.UUID) ([]PlanStanding, error) {
	since := "1970-01-01"
	if term != uuid.Nil {
		t, err := s.Term(ctx, tx, tenant, term)
		if err != nil {
			return nil, err
		}
		since = t.StartsOn
	}
	var only *uuid.UUID
	if len(student) > 0 {
		only = &student[0]
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (student_id) id, student_id, decided_at, decided_instalments FROM school_plan_requests
		WHERE tenant_id = $1 AND status = 'approved' AND (decided_at AT TIME ZONE 'Africa/Lagos')::date >= $2::date AND ($3::uuid IS NULL OR student_id = $3)
		ORDER BY student_id, decided_at DESC`, tenant, since, only)
	if err != nil {
		return nil, err
	}
	plans, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (planRow, error) {
		var p planRow
		var raw []byte
		if err := r.Scan(&p.id, &p.student, &p.decidedAt, &raw); err != nil {
			return p, err
		}
		return p, json.Unmarshal(raw, &p.instalments)
	})
	if err != nil {
		return nil, err
	}
	grace, err := PlanGraceDays(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	today := s.today()
	out := make([]PlanStanding, 0, len(plans))
	for _, p := range plans {
		st, err := s.standing(ctx, tx, tenant, p, grace, today)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// standing works a plan's standing out.
func (s *Schools) standing(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p planRow, grace int, today time.Time) (PlanStanding, error) {
	st := PlanStanding{PlanID: p.id, StudentID: p.student, State: PlanGood, ApprovedAt: p.decidedAt, Instalments: p.instalments, GraceDays: grace, Missed: []MissedInstalment{}}
	if st.Instalments == nil {
		st.Instalments = []Instalment{}
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(p.amount_kobo), 0) FROM customer_invoice_payments p JOIN school_term_invoices ti ON ti.invoice_id = p.invoice_id
		WHERE ti.tenant_id = $1 AND ti.student_id = $2 AND p.created_at >= $3 AND p.undoes IS NULL
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)`, tenant, p.student, p.decidedAt).Scan(&st.PaidSinceKobo); err != nil {
		return st, err
	}
	balance, err := studentBalance(ctx, tx, tenant, p.student)
	if err != nil {
		return st, err
	}
	var cum int64
	for i, in := range p.instalments {
		cum += in.AmountKobo
		past := daysPast(today, in.DueOn)
		if past > grace {
			st.DueSoFarKobo = cum
			if st.PaidSinceKobo < cum {
				st.Missed = append(st.Missed, MissedInstalment{Instalment: i + 1, DueOn: in.DueOn, AmountKobo: in.AmountKobo, ShortKobo: cum - st.PaidSinceKobo, DaysPast: past})
			}
			continue
		}
		if st.NextDueOn == "" {
			st.NextDueOn, st.NextAmountKobo = in.DueOn, in.AmountKobo
		}
	}
	switch {
	case balance <= 0 || st.PaidSinceKobo >= TotalKobo(p.instalments):
		st.State = PlanSettled
		st.Missed = []MissedInstalment{}
	case len(st.Missed) > 0:
		st.State = PlanArrears
	}
	return st, nil
}

// PlanArrear is an instalment the check found missed.
type PlanArrear struct {
	PlanID     uuid.UUID `json:"plan_id"`
	Instalment int       `json:"instalment"`
	StudentID  uuid.UUID `json:"student_id"`
	DueOn      string    `json:"due_on"`
	AmountKobo int64     `json:"amount_kobo"`
	ShortKobo  int64     `json:"short_kobo"`
	MarkedAt   time.Time `json:"marked_at"`
}

// Arrears lists the instalments marked missed for a student, newest first.
func (s *Schools) Arrears(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) ([]PlanArrear, error) {
	rows, err := tx.Query(ctx, `SELECT plan_id, instalment, student_id, due_on::text, amount_kobo, short_kobo, marked_at FROM school_plan_arrears
		WHERE tenant_id = $1 AND student_id = $2 ORDER BY marked_at DESC, instalment DESC`, tenant, student)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PlanArrear, error) {
		var a PlanArrear
		return a, r.Scan(&a.PlanID, &a.Instalment, &a.StudentID, &a.DueOn, &a.AmountKobo, &a.ShortKobo, &a.MarkedAt)
	})
	if list == nil {
		list = []PlanArrear{}
	}
	return list, err
}

// checkPlans marks each instalment newly found missed, once, and tells
// the bursars.
func (s *Schools) checkPlans(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	standings, err := s.planStandings(ctx, tx, tenant, uuid.Nil)
	if err != nil {
		return 0, err
	}
	n := 0
	var lines []string
	for _, st := range standings {
		if st.State != PlanArrears {
			continue
		}
		for _, m := range st.Missed {
			tag, err := tx.Exec(ctx, `INSERT INTO school_plan_arrears (plan_id, instalment, tenant_id, student_id, due_on, amount_kobo, short_kobo) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT DO NOTHING`, st.PlanID, m.Instalment, tenant, st.StudentID, m.DueOn, m.AmountKobo, m.ShortKobo)
			if err != nil {
				return n, err
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			n++
			var number string
			if err := tx.QueryRow(ctx, `SELECT admission_number FROM school_students WHERE id = $1`, st.StudentID).Scan(&number); err != nil {
				return n, err
			}
			lines = append(lines, fmt.Sprintf("%s: instalment %d of %s due %s, %s short", number, m.Instalment, invoicing.Naira(m.AmountKobo), dateWords(m.DueOn), invoicing.Naira(m.ShortKobo)))
		}
	}
	if n > 0 {
		no := notices.Notice{Kind: "school_plan_arrears", Level: notices.Warning, Title: "A payment plan instalment was missed", Many: "{n} payment plan instalments were missed",
			Body: strings.Join(lines, "\n"), Link: "/schools/debtors?has=arrears", Group: "school_plan_arrears"}
		if n > 1 {
			no.Title = fmt.Sprintf("%d payment plan instalments were missed", n)
		}
		if err := tellBursars(ctx, tx, tenant, no); err != nil {
			return n, err
		}
	}
	return n, nil
}

// --- telling the family of a decision ---

// PlanDecisionFilter narrows PlanDecisions.
type PlanDecisionFilter struct {
	// Status: approved, declined, or "" for both.
	Status string
	// Untold keeps the decisions the family was not told of yet.
	Untold bool
	// SinceDays looks back this many days (7 by default, up to 90).
	SinceDays int
	Limit     int
}

// PlanDecisions lists decided plan requests, oldest first, for the
// template that tells families.
func (s *Schools) PlanDecisions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PlanDecisionFilter) ([]PlanRequest, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if f.SinceDays <= 0 || f.SinceDays > 90 {
		f.SinceDays = 7
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	switch f.Status {
	case "", "approved", "declined":
	default:
		return nil, invalid("status is approved or declined")
	}
	rows, err := tx.Query(ctx, `SELECT `+planCols+` WHERE p.tenant_id = $1 AND p.status <> 'requested' AND ($2 = '' OR p.status = $2)
		AND (NOT $3 OR p.told_at IS NULL) AND p.decided_at > now() - make_interval(days => $4) ORDER BY p.decided_at LIMIT $5`, tenant, f.Status, f.Untold, f.SinceDays, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanPlans(ctx, tx, tenant, rows)
	if list == nil {
		list = []PlanRequest{}
	}
	return list, err
}

// PlanTold is what telling a family of a decision did.
type PlanTold struct {
	Plan PlanRequest `json:"plan"`
	Sent FamilySent  `json:"sent"`
	// Told: the family was told before this call (nothing was sent).
	Told bool `json:"told"`
}

// TellPlanDecision tells a family of a decided plan, once: the message is
// keyed by the plan and its decision, so the platform's own telling at
// decision time and a template's are one message, and the plan is marked
// told either way.
func (s *Schools) TellPlanDecision(ctx context.Context, tenant, id uuid.UUID) (PlanTold, error) {
	var out PlanTold
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		p, err := s.Plan(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if p.Status == "requested" {
			return refused("the plan request is not decided yet")
		}
		out.Plan = p
		var told *time.Time
		if err := tx.QueryRow(ctx, `SELECT told_at FROM school_plan_requests WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&told); err != nil {
			return err
		}
		if told != nil {
			out.Told = true
			return nil
		}
		if out.Sent, err = s.tellPlan(ctx, tx, tenant, p); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE school_plan_requests SET told_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id)
		return err
	})
	return out, err
}

// tellPlan sends the family the decision with a fresh link.
func (s *Schools) tellPlan(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p PlanRequest) (FamilySent, error) {
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return FamilySent{}, err
	}
	return s.SendFamily(ctx, tx, tenant, p.FamilyID, FamilyMessage{Purpose: "decision", Key: "plan:" + p.ID.String() + ":" + p.Status,
		Text:  func(link string) string { return PlanDecisionText(business, p, link) },
		Short: func(link string) string { return PlanDecisionShort(p, link) }}, p.DecidedBy)
}

// PlanDecidedAfter tells the family of a decision, with a fresh link, in
// a transaction of its own after the decision committed; a family a
// guardian of which was reached is marked told, so the template that
// tells families sends nothing more.
func (s *Schools) PlanDecidedAfter(ctx context.Context, tenant uuid.UUID, p PlanRequest) (FamilySent, error) {
	var out FamilySent
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if out, err = s.tellPlan(ctx, tx, tenant, p); err != nil {
			return err
		}
		if out.Nobody {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE school_plan_requests SET told_at = now() WHERE tenant_id = $1 AND id = $2 AND told_at IS NULL`, tenant, p.ID)
		return err
	})
	if errors.Is(err, ErrOff) {
		return out, nil
	}
	return out, err
}
