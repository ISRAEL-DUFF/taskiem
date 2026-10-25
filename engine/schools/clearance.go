package schools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Clearance and the gate (decision 0046, points 4 and 10; docs/spec/
// schools.md 12, B8; docs/schools.md#clearance).
//
// A student is cleared for a term when the term's invoices are paid in
// full, or an approved payment plan is in good standing (or settled), or
// an override is in force. A term with some money counted but a balance
// left is partly paid, never cleared (decided 2026-10-23); otherwise not
// paid. A student with no invoice for the term is cleared only when
// their class's fee structure for the term has no required items (there
// is nothing to pay), and not paid otherwise (fees exist that were never
// invoiced). Clearance is computed every time it is read and stored
// nowhere: no route writes it.
//
// Overrides (school_clearance_overrides) are the one record: a person
// with school.approve clears a student with a reason and an end date,
// audited, withdrawable; past its end an override no longer counts.
//
// The gate reads the same rule and answers cleared or see the bursar,
// with the student's name and class and nothing else (gate.go).

// Clearance statuses.
const (
	Cleared    = "cleared"
	PartlyPaid = "partly_paid"
	NotPaid    = "not_paid"
)

// Why a student stands where they do.
const (
	// ReasonPaid: the term's invoices are paid in full.
	ReasonPaid = "paid"
	// ReasonPlan: an approved payment plan is in good standing or settled.
	ReasonPlan = "plan"
	// ReasonOverride: an override is in force.
	ReasonOverride = "override"
	// ReasonNoFees: no invoice, and the class's fee structure for the term
	// has nothing required: nothing to pay.
	ReasonNoFees = "no_fees"
	// ReasonBalance: a balance is left (partly paid or not paid).
	ReasonBalance = "balance"
	// ReasonNoInvoice: fees exist for the class but the student has no
	// invoice for the term yet.
	ReasonNoInvoice = "no_invoice"
)

// ReasonWords say each reason in plain words.
var ReasonWords = map[string]string{
	ReasonPaid:      "paid in full",
	ReasonPlan:      "payment plan in good standing",
	ReasonOverride:  "cleared by override",
	ReasonNoFees:    "nothing to pay",
	ReasonBalance:   "a balance is left",
	ReasonNoInvoice: "not invoiced yet",
}

// ClearanceOverride is an override as the pages show it.
type ClearanceOverride struct {
	ID              uuid.UUID  `json:"id"`
	StudentID       uuid.UUID  `json:"student_id"`
	AdmissionNumber string     `json:"admission_number"`
	TermID          uuid.UUID  `json:"term_id"`
	Term            string     `json:"term"`
	Reason          string     `json:"reason"`
	StartsOn        string     `json:"starts_on"`
	EndsOn          string     `json:"ends_on"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	WithdrawnAt     *time.Time `json:"withdrawn_at,omitempty"`
	WithdrawnBy     string     `json:"withdrawn_by,omitempty"`
	// InForce: not withdrawn, and today is within its dates.
	InForce bool `json:"in_force"`
}

// Clearance is where a student stands for a term.
type Clearance struct {
	StudentID uuid.UUID `json:"student_id"`
	TermID    uuid.UUID `json:"term_id"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	// Invoiced: the student has an invoice for the term. The amounts are
	// the term's invoices' (callers that may show no amounts drop them).
	Invoiced    bool  `json:"invoiced"`
	TotalKobo   int64 `json:"total_kobo"`
	PaidKobo    int64 `json:"paid_kobo"`
	BalanceKobo int64 `json:"balance_kobo"`
	// Plan is the standing of the student's plan for the term, when one
	// is; Override the override in force, when one is.
	Plan     *PlanStanding      `json:"plan,omitempty"`
	Override *ClearanceOverride `json:"override,omitempty"`
}

// ClearanceRow is one student on a clearance list.
type ClearanceRow struct {
	Clearance
	AdmissionNumber string    `json:"admission_number"`
	Name            string    `json:"name"`
	StudentStatus   string    `json:"student_status"`
	ClassID         uuid.UUID `json:"class_id"`
	Class           string    `json:"class"`
}

// ClearanceTotals count a list's students by status.
type ClearanceTotals struct {
	Students   int `json:"students"`
	Cleared    int `json:"cleared"`
	PartlyPaid int `json:"partly_paid"`
	NotPaid    int `json:"not_paid"`
}

// ClearanceList is a term's students, by class and admission number,
// each with their clearance.
type ClearanceList struct {
	TermID uuid.UUID       `json:"term_id"`
	Term   string          `json:"term"`
	Rows   []ClearanceRow  `json:"rows"`
	Totals ClearanceTotals `json:"totals"`
	Today  string          `json:"today"`
}

// termSums are a student's term invoices summed.
type termSums struct {
	total, paid int64
	invoiced    bool
}

// termSumsFor sums each student's non-void invoices for a term (drafts
// included: fees owed that were not sent yet are still owed).
func termSumsFor(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID, student *uuid.UUID) (map[uuid.UUID]termSums, error) {
	rows, err := tx.Query(ctx, `SELECT ti.student_id, i.total_kobo, i.paid_kobo FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND ($3::uuid IS NULL OR ti.student_id = $3) AND i.status <> 'void'`, tenant, term, student)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]termSums{}
	for rows.Next() {
		var id uuid.UUID
		var total, paid int64
		if err := rows.Scan(&id, &total, &paid); err != nil {
			return nil, err
		}
		s := out[id]
		s.total += total
		s.paid += min(paid, total)
		s.invoiced = true
		out[id] = s
	}
	return out, rows.Err()
}

// requiredByClass is what each class's required items for a term come
// to, by class.
func requiredByClass(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID) (map[uuid.UUID]int64, error) {
	rows, err := tx.Query(ctx, `SELECT class_id, COALESCE(sum(amount_kobo), 0) FROM school_fee_items WHERE tenant_id = $1 AND term_id = $2 AND kind = 'required' GROUP BY class_id`, tenant, term)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// decide applies the rule to what was read for one student.
func decide(c *Clearance, sums termSums, required int64, plan *PlanStanding, override *ClearanceOverride) {
	c.Invoiced = sums.invoiced
	c.TotalKobo, c.PaidKobo = sums.total, sums.paid
	c.BalanceKobo = max(sums.total-sums.paid, 0)
	c.Plan, c.Override = plan, override
	switch {
	case !sums.invoiced && required <= 0 && override == nil:
		c.Status, c.Reason = Cleared, ReasonNoFees
	case sums.invoiced && c.BalanceKobo <= 0:
		c.Status, c.Reason = Cleared, ReasonPaid
	case override != nil && override.InForce:
		c.Status, c.Reason = Cleared, ReasonOverride
	case plan != nil && (plan.State == PlanGood || plan.State == PlanSettled):
		c.Status, c.Reason = Cleared, ReasonPlan
	case !sums.invoiced:
		c.Status, c.Reason = NotPaid, ReasonNoInvoice
	case sums.paid > 0:
		c.Status, c.Reason = PartlyPaid, ReasonBalance
	default:
		c.Status, c.Reason = NotPaid, ReasonBalance
	}
}

// Clearance is where a student stands for a term, computed now.
func (s *Schools) Clearance(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID) (Clearance, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Clearance{}, err
	}
	if _, err := s.Term(ctx, tx, tenant, term); err != nil {
		return Clearance{}, err
	}
	class, err := studentClassFor(ctx, tx, tenant, student, term)
	if err != nil {
		return Clearance{}, err
	}
	sums, err := termSumsFor(ctx, tx, tenant, term, &student)
	if err != nil {
		return Clearance{}, err
	}
	required, err := requiredByClass(ctx, tx, tenant, term)
	if err != nil {
		return Clearance{}, err
	}
	plan, err := s.PlanStanding(ctx, tx, tenant, student, term)
	if err != nil {
		return Clearance{}, err
	}
	overrides, err := s.Overrides(ctx, tx, tenant, OverrideFilter{Student: &student, Term: &term, InForce: true})
	if err != nil {
		return Clearance{}, err
	}
	var override *ClearanceOverride
	if len(overrides) > 0 {
		override = &overrides[0]
	}
	c := Clearance{StudentID: student, TermID: term}
	decide(&c, sums[student], required[class], plan, override)
	return c, nil
}

// TermClearance lists every student enrolled in a term (of one class
// when given), by class and admission number, with their clearance.
func (s *Schools) TermClearance(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID, class *uuid.UUID) (ClearanceList, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return ClearanceList{}, err
	}
	t, err := s.Term(ctx, tx, tenant, term)
	if err != nil {
		return ClearanceList{}, err
	}
	out := ClearanceList{TermID: t.ID, Term: t.Name + " " + t.Session, Rows: []ClearanceRow{}, Today: s.today().Format(time.DateOnly)}
	rows, err := tx.Query(ctx, `SELECT e.student_id, s.admission_number, s.details, s.status, e.class_id, c.name, c.arm
		FROM school_enrollments e JOIN school_students s ON s.id = e.student_id JOIN school_classes c ON c.id = e.class_id
		WHERE e.tenant_id = $1 AND e.term_id = $2 AND ($3::uuid IS NULL OR e.class_id = $3)
		ORDER BY c.class_order, lower(c.name), lower(c.arm), upper(s.admission_number)`, tenant, term, class)
	if err != nil {
		return ClearanceList{}, err
	}
	type row struct {
		r       ClearanceRow
		details map[string]any
		arm     string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.r.StudentID, &x.r.AdmissionNumber, &x.details, &x.r.StudentStatus, &x.r.ClassID, &x.r.Class, &x.arm)
		return x, err
	})
	if err != nil {
		return ClearanceList{}, err
	}
	if len(list) == 0 {
		return out, nil
	}
	sums, err := termSumsFor(ctx, tx, tenant, term, nil)
	if err != nil {
		return ClearanceList{}, err
	}
	required, err := requiredByClass(ctx, tx, tenant, term)
	if err != nil {
		return ClearanceList{}, err
	}
	standings, err := s.planStandings(ctx, tx, tenant, term)
	if err != nil {
		return ClearanceList{}, err
	}
	plans := map[uuid.UUID]*PlanStanding{}
	for i := range standings {
		plans[standings[i].StudentID] = &standings[i]
	}
	overrides, err := s.Overrides(ctx, tx, tenant, OverrideFilter{Term: &term, InForce: true})
	if err != nil {
		return ClearanceList{}, err
	}
	inForce := map[uuid.UUID]*ClearanceOverride{}
	for i := range overrides {
		inForce[overrides[i].StudentID] = &overrides[i]
	}
	for _, x := range list {
		r := x.r
		r.Class = Label(r.Class, x.arm)
		r.TermID = term
		var sd studentSealed
		erased, err := s.open(ctx, tx, tenant, x.details, &sd)
		if err != nil {
			return ClearanceList{}, err
		}
		r.Name = sd.Name
		if erased {
			r.Name = Erased
		}
		decide(&r.Clearance, sums[r.StudentID], required[r.ClassID], plans[r.StudentID], inForce[r.StudentID])
		out.Rows = append(out.Rows, r)
		out.Totals.Students++
		switch r.Status {
		case Cleared:
			out.Totals.Cleared++
		case PartlyPaid:
			out.Totals.PartlyPaid++
		default:
			out.Totals.NotPaid++
		}
	}
	return out, nil
}

// --- overrides ---

// OverrideIn is an override to record.
type OverrideIn struct {
	// TermID is the term; the current one when nil.
	TermID *uuid.UUID `json:"term_id,omitempty"`
	Reason string     `json:"reason"`
	// EndsOn is the last day it counts; the term's end (or today, for a
	// term that has ended) when empty.
	EndsOn string `json:"ends_on,omitempty"`
}

// AddOverride clears a student for a term by hand, with a reason, until
// a date: one in force per student and term.
func (s *Schools) AddOverride(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, in OverrideIn, by string) (ClearanceOverride, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return ClearanceOverride{}, err
	}
	in.Reason = strings.Join(strings.Fields(in.Reason), " ")
	if in.Reason == "" || len(in.Reason) > 300 {
		return ClearanceOverride{}, invalid("an override needs a reason (up to 300 characters)")
	}
	st, err := s.student(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return ClearanceOverride{}, err
	}
	var t Term
	if in.TermID != nil {
		if t, err = s.Term(ctx, tx, tenant, *in.TermID); err != nil {
			return ClearanceOverride{}, err
		}
	} else {
		var ok bool
		if t, ok, err = s.CurrentTerm(ctx, tx, tenant); err != nil {
			return ClearanceOverride{}, err
		} else if !ok {
			return ClearanceOverride{}, refused("no term is current: name the term")
		}
	}
	today := s.today()
	ends := today
	if end, ok := dateOnly(t.EndsOn); ok && end.After(today) {
		ends = end
	}
	if strings.TrimSpace(in.EndsOn) != "" {
		d, ok := dateOnly(in.EndsOn)
		if !ok || d.Before(today) || d.After(today.AddDate(1, 0, 0)) {
			return ClearanceOverride{}, invalid("the override ends on a date from today to a year on, YYYY-MM-DD")
		}
		ends = d
	}
	subject := ""
	if st.FamilyID != nil {
		if subject, err = s.familySubject(ctx, tx, tenant, *st.FamilyID, st.ID); err != nil {
			return ClearanceOverride{}, err
		}
	} else if err := tx.QueryRow(ctx, `SELECT subject_id FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, st.ID).Scan(&subject); err != nil {
		return ClearanceOverride{}, err
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", in.Reason)
	if err != nil {
		return ClearanceOverride{}, err
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_clearance_overrides (id, tenant_id, student_id, term_id, reason, starts_on, ends_on, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenant, st.ID, t.ID, sealed, today.Format(time.DateOnly), ends.Format(time.DateOnly), by); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return ClearanceOverride{}, refused("%s has an override for %s %s already: withdraw it first", st.AdmissionNumber, t.Name, t.Session)
		}
		return ClearanceOverride{}, err
	}
	return s.Override(ctx, tx, tenant, id)
}

const overrideCols = `o.id, o.student_id, s.admission_number, o.term_id, t.name || ' ' || ss.label, o.reason, o.starts_on::text, o.ends_on::text, o.created_by, o.created_at, o.withdrawn_at, COALESCE(o.withdrawn_by, ''),
	(o.withdrawn_at IS NULL AND $2::date BETWEEN o.starts_on AND o.ends_on)
	FROM school_clearance_overrides o JOIN school_students s ON s.id = o.student_id JOIN school_terms t ON t.id = o.term_id JOIN school_sessions ss ON ss.id = t.session_id`

func (s *Schools) scanOverrides(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]ClearanceOverride, error) {
	type row struct {
		o      ClearanceOverride
		reason map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.o.ID, &x.o.StudentID, &x.o.AdmissionNumber, &x.o.TermID, &x.o.Term, &x.reason, &x.o.StartsOn, &x.o.EndsOn, &x.o.CreatedBy, &x.o.CreatedAt, &x.o.WithdrawnAt, &x.o.WithdrawnBy, &x.o.InForce)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]ClearanceOverride, 0, len(list))
	for _, x := range list {
		o := x.o
		var reason string
		if _, err := s.open(ctx, tx, tenant, x.reason, &reason); err != nil {
			return nil, err
		}
		o.Reason = reason
		out = append(out, o)
	}
	return out, nil
}

// Override reads one override.
func (s *Schools) Override(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (ClearanceOverride, error) {
	rows, err := tx.Query(ctx, `SELECT `+overrideCols+` WHERE o.tenant_id = $1 AND o.id = $3`, tenant, s.today().Format(time.DateOnly), id)
	if err != nil {
		return ClearanceOverride{}, err
	}
	list, err := s.scanOverrides(ctx, tx, tenant, rows)
	if err != nil {
		return ClearanceOverride{}, err
	}
	if len(list) == 0 {
		return ClearanceOverride{}, ErrNotFound
	}
	return list[0], nil
}

// OverrideFilter narrows the overrides listed.
type OverrideFilter struct {
	Student *uuid.UUID
	Term    *uuid.UUID
	// InForce keeps only those counting today.
	InForce bool
	Limit   int
}

// Overrides lists overrides, newest first.
func (s *Schools) Overrides(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f OverrideFilter) ([]ClearanceOverride, error) {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	rows, err := tx.Query(ctx, `SELECT `+overrideCols+` WHERE o.tenant_id = $1 AND ($3::uuid IS NULL OR o.student_id = $3) AND ($4::uuid IS NULL OR o.term_id = $4)
		AND (NOT $5 OR (o.withdrawn_at IS NULL AND $2::date BETWEEN o.starts_on AND o.ends_on)) ORDER BY o.created_at DESC LIMIT $6`,
		tenant, s.today().Format(time.DateOnly), f.Student, f.Term, f.InForce, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanOverrides(ctx, tx, tenant, rows)
	if list == nil {
		list = []ClearanceOverride{}
	}
	return list, err
}

// WithdrawOverride withdraws an override not withdrawn yet.
func (s *Schools) WithdrawOverride(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, by string) (ClearanceOverride, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return ClearanceOverride{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE school_clearance_overrides SET withdrawn_at = now(), withdrawn_by = $3 WHERE tenant_id = $1 AND id = $2 AND withdrawn_at IS NULL`, tenant, id, by)
	if err != nil {
		return ClearanceOverride{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Override(ctx, tx, tenant, id); err != nil {
			return ClearanceOverride{}, err
		}
		return ClearanceOverride{}, refused("the override is withdrawn already")
	}
	return s.Override(ctx, tx, tenant, id)
}

// --- the gate ---

// Gate answers.
const (
	GateCleared   = "cleared"
	GateSeeBursar = "see_bursar"
)

// GateHit is a student the gate's search found: id, name and class,
// nothing else.
type GateHit struct {
	StudentID uuid.UUID `json:"student_id"`
	Name      string    `json:"name"`
	Class     string    `json:"class"`
}

// GateAnswer is what the gate shows for one student: cleared or see the
// bursar, with the name and class. No amount, ever.
type GateAnswer struct {
	StudentID uuid.UUID `json:"student_id"`
	Name      string    `json:"name"`
	Class     string    `json:"class"`
	Status    string    `json:"status"`
}

// MaxGateHits is the most students one gate search answers.
const MaxGateHits = 10

// MinGateQuery is the fewest letters a gate search takes.
const MinGateQuery = 3

// GateSearch finds active students whose name holds q (three or more
// letters), at most MaxGateHits, those whose name starts with it first.
func (s *Schools) GateSearch(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, q string) ([]GateHit, error) {
	q = strings.ToLower(strings.Join(strings.Fields(q), " "))
	if len([]rune(q)) < MinGateQuery {
		return nil, invalid("type at least %d letters of the name", MinGateQuery)
	}
	list, _, err := s.Students(ctx, tx, tenant, Filter{Status: Active, Query: q, Names: true, Limit: MaxListed})
	if err != nil {
		return nil, err
	}
	out := make([]GateHit, 0, min(len(list), MaxGateHits))
	for _, st := range list {
		if !strings.Contains(strings.ToLower(st.Name), q) {
			continue // the admission number matched, not the name
		}
		out = append(out, GateHit{StudentID: st.ID, Name: st.Name, Class: st.Class})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.HasPrefix(strings.ToLower(out[i].Name), q), strings.HasPrefix(strings.ToLower(out[j].Name), q)
		return a && !b
	})
	if len(out) > MaxGateHits {
		out = out[:MaxGateHits]
	}
	return out, nil
}

// GateCheck answers the gate for one student in the current term:
// cleared, or see the bursar (also when no term is current, since nothing
// says the fees are settled).
func (s *Schools) GateCheck(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) (GateAnswer, error) {
	st, err := s.student(ctx, tx, tenant, student.String())
	if err != nil {
		return GateAnswer{}, err
	}
	out := GateAnswer{StudentID: st.ID, Name: st.Name, Class: st.Class, Status: GateSeeBursar}
	t, ok, err := s.CurrentTerm(ctx, tx, tenant)
	if err != nil || !ok {
		return out, err
	}
	c, err := s.Clearance(ctx, tx, tenant, st.ID, t.ID)
	if errors.Is(err, ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return GateAnswer{}, err
	}
	if c.Status == Cleared {
		out.Status = GateCleared
	}
	return out, nil
}

// --- the connector's reads ---

// ClearanceOf is a student's clearance (by id or admission number) for a
// term, in its own transaction.
func (s Service) ClearanceOf(ctx context.Context, tenant uuid.UUID, student string, term uuid.UUID) (ClearanceRow, error) {
	var out ClearanceRow
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		st, err := s.student(ctx, tx, tenant, student)
		if err != nil {
			return err
		}
		c, err := s.Clearance(ctx, tx, tenant, st.ID, term)
		if err != nil {
			return err
		}
		out = ClearanceRow{Clearance: c, AdmissionNumber: st.AdmissionNumber, Name: st.Name, StudentStatus: st.Status, ClassID: st.ClassID, Class: st.Class}
		return nil
	})
	return out, err
}

// ListClearance is TermClearance in its own transaction.
func (s Service) ListClearance(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID) (ClearanceList, error) {
	var out ClearanceList
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.TermClearance(ctx, tx, tenant, term, class)
		return err
	})
	return out, err
}

// ClassSummary is one class's clearance list in words, for a teacher:
// the counts and who is not cleared, with no amounts.
type ClassSummary struct {
	ClassID    uuid.UUID `json:"class_id"`
	Class      string    `json:"class"`
	Students   int       `json:"students"`
	Cleared    int       `json:"cleared"`
	PartlyPaid int       `json:"partly_paid"`
	NotPaid    int       `json:"not_paid"`
	// NotCleared names each student not cleared, with their admission
	// number and status.
	NotCleared []string `json:"not_cleared"`
	// Text is the message to send, at most MaxSummaryText characters.
	Text string `json:"text"`
}

// MaxSummaryText caps a class summary's text (a notify@1 message takes
// 1,500 characters).
const MaxSummaryText = 1400

// Summarise groups a clearance list by class into teachers' summaries.
func Summarise(l ClearanceList) []ClassSummary {
	var out []ClassSummary
	index := map[uuid.UUID]int{}
	for _, r := range l.Rows {
		i, ok := index[r.ClassID]
		if !ok {
			out = append(out, ClassSummary{ClassID: r.ClassID, Class: r.Class, NotCleared: []string{}})
			i = len(out) - 1
			index[r.ClassID] = i
		}
		c := &out[i]
		c.Students++
		switch r.Status {
		case Cleared:
			c.Cleared++
		case PartlyPaid:
			c.PartlyPaid++
			c.NotCleared = append(c.NotCleared, fmt.Sprintf("%s (%s): partly paid", r.Name, r.AdmissionNumber))
		default:
			c.NotPaid++
			c.NotCleared = append(c.NotCleared, fmt.Sprintf("%s (%s): not paid", r.Name, r.AdmissionNumber))
		}
	}
	for i := range out {
		out[i].Text = summaryText(out[i], l.Term)
	}
	if out == nil {
		out = []ClassSummary{}
	}
	return out
}

func summaryText(c ClassSummary, term string) string {
	head := fmt.Sprintf("Clearance list, %s, %s: %d of %d cleared.", c.Class, term, c.Cleared, c.Students)
	if len(c.NotCleared) == 0 {
		return head + " Everyone is cleared."
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(" See the bursar:")
	shown := 0
	for _, line := range c.NotCleared {
		next := "\n- " + line
		if b.Len()+len(next)+40 > MaxSummaryText {
			break
		}
		b.WriteString(next)
		shown++
	}
	if left := len(c.NotCleared) - shown; left > 0 {
		fmt.Fprintf(&b, "\n- and %d more", left)
	}
	return b.String()
}
