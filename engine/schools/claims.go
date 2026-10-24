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
	"github.com/israel-duff/taskiem/engine/matching"
)

// I have paid claims and payment plan requests (docs/spec/schools.md 10
// and 11.3, B6; docs/schools.md#the-family-page). A parent who paid some
// other way says so on the family page: the amount, the date, how and
// any reference. It is recorded as a claim, never as a payment: it
// reaches no invoice and no receipt; it is shown in To match beside the
// unmatched money and on the student's page, and is resolved when a
// payment for the student is counted later, or dismissed by the bursar
// with a reason. A payment plan request is the parent's instalments and
// words; the bursar approves it (with the instalments as decided) or
// declines it with a note, and the family is told either way. Its
// effects on reminders and clearance come with chasing (B7, B8).

// Claim is an I have paid claim as the pages show it.
type Claim struct {
	ID              uuid.UUID  `json:"id"`
	FamilyID        uuid.UUID  `json:"family_id"`
	StudentID       uuid.UUID  `json:"student_id"`
	AdmissionNumber string     `json:"admission_number"`
	Name            string     `json:"name"`
	Class           string     `json:"class"`
	AmountKobo      int64      `json:"amount_kobo"`
	PaidOn          string     `json:"paid_on"`
	How             string     `json:"how"`
	Reference       string     `json:"reference,omitempty"`
	Note            string     `json:"note,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy      string     `json:"resolved_by,omitempty"`
	Reason          string     `json:"resolution_reason,omitempty"`
}

// ClaimIn is what a parent says on the family page.
type ClaimIn struct {
	StudentID  uuid.UUID `json:"student_id"`
	AmountKobo int64     `json:"amount_kobo"`
	PaidOn     string    `json:"paid_on"`
	How        string    `json:"how"`
	Reference  string    `json:"reference,omitempty"`
	Note       string    `json:"note,omitempty"`
}

type claimSealed struct {
	Reference string `json:"reference,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ClaimHows are the ways a parent says they paid.
var ClaimHows = []string{"transfer", "cash", "pos", "other"}

// ClaimHowWords are the ways, for people.
var ClaimHowWords = map[string]string{"transfer": "Bank transfer", "cash": "Cash", "pos": "POS", "other": "Another way"}

func (in *ClaimIn) check(now time.Time) error {
	in.How = strings.ToLower(strings.TrimSpace(in.How))
	in.Reference = strings.Join(strings.Fields(in.Reference), " ")
	in.Note = strings.TrimSpace(in.Note)
	if in.AmountKobo <= 0 || in.AmountKobo > invoicing.MaxTotalKobo {
		return invalid("the amount is in naira, more than nothing")
	}
	d, ok := dateOnly(in.PaidOn)
	if !ok || d.After(now.Add(24*time.Hour)) || d.Before(now.AddDate(-1, 0, 0)) {
		return invalid("the date paid is a date in the last year, YYYY-MM-DD")
	}
	in.PaidOn = d.Format(time.DateOnly)
	if _, ok := ClaimHowWords[in.How]; !ok {
		return invalid("how is transfer, cash, pos or other")
	}
	if len(in.Reference) > 80 {
		return invalid("a reference has up to 80 characters")
	}
	if len(in.Note) > 500 {
		return invalid("a note has up to 500 characters")
	}
	return nil
}

// familySubject is the subject a family's claims and requests are sealed
// under: its first guardian's, else the student's own.
func (s *Schools) familySubject(ctx context.Context, tx pgx.Tx, tenant, family, student uuid.UUID) (string, error) {
	var subject string
	err := tx.QueryRow(ctx, `SELECT subject_id FROM school_guardians WHERE tenant_id = $1 AND family_id = $2 ORDER BY created_at, id LIMIT 1`, tenant, family).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT subject_id FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, student).Scan(&subject)
	}
	return subject, err
}

// studentOfFamily checks a student is the family's.
func studentOfFamily(ctx context.Context, tx pgx.Tx, tenant, family, student uuid.UUID) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_students WHERE tenant_id = $1 AND id = $2 AND family_id = $3)`, tenant, student, family).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// RecordClaim records a family's claim for one of its students.
func (s *Schools) RecordClaim(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, in ClaimIn, by string) (Claim, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Claim{}, err
	}
	if err := in.check(s.now()); err != nil {
		return Claim{}, err
	}
	if err := studentOfFamily(ctx, tx, tenant, family, in.StudentID); err != nil {
		return Claim{}, err
	}
	subject, err := s.familySubject(ctx, tx, tenant, family, in.StudentID)
	if err != nil {
		return Claim{}, err
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", claimSealed{Reference: in.Reference, Note: in.Note})
	if err != nil {
		return Claim{}, err
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_payment_claims (id, tenant_id, family_id, student_id, amount_kobo, paid_on, how, details) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenant, family, in.StudentID, in.AmountKobo, in.PaidOn, in.How, sealed); err != nil {
		return Claim{}, err
	}
	_ = by
	return s.Claim(ctx, tx, tenant, id)
}

const claimCols = `c.id, c.family_id, c.student_id, s.admission_number, s.details, cl.name || CASE WHEN cl.arm = '' THEN '' ELSE ' ' || cl.arm END,
	c.amount_kobo, c.paid_on::text, c.how, c.details, c.status, c.created_at, c.resolved_at, COALESCE(c.resolved_by, ''), COALESCE(c.resolution_reason, '')
	FROM school_payment_claims c JOIN school_students s ON s.id = c.student_id JOIN school_classes cl ON cl.id = s.class_id`

func (s *Schools) scanClaims(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Claim, error) {
	type row struct {
		c                Claim
		student, details map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.c.ID, &x.c.FamilyID, &x.c.StudentID, &x.c.AdmissionNumber, &x.student, &x.c.Class, &x.c.AmountKobo, &x.c.PaidOn, &x.c.How, &x.details,
			&x.c.Status, &x.c.CreatedAt, &x.c.ResolvedAt, &x.c.ResolvedBy, &x.c.Reason)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Claim, 0, len(list))
	for _, x := range list {
		c := x.c
		var sd studentSealed
		erased, err := s.open(ctx, tx, tenant, x.student, &sd)
		if err != nil {
			return nil, err
		}
		c.Name = sd.Name
		if erased {
			c.Name = Erased
		}
		var d claimSealed
		if _, err := s.open(ctx, tx, tenant, x.details, &d); err != nil {
			return nil, err
		}
		c.Reference, c.Note = d.Reference, d.Note
		out = append(out, c)
	}
	return out, nil
}

// Claim reads one claim.
func (s *Schools) Claim(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Claim, error) {
	rows, err := tx.Query(ctx, `SELECT `+claimCols+` WHERE c.tenant_id = $1 AND c.id = $2`, tenant, id)
	if err != nil {
		return Claim{}, err
	}
	list, err := s.scanClaims(ctx, tx, tenant, rows)
	if err != nil {
		return Claim{}, err
	}
	if len(list) == 0 {
		return Claim{}, ErrNotFound
	}
	return list[0], nil
}

// ClaimFilter narrows the claims listed.
type ClaimFilter struct {
	Student *uuid.UUID
	Family  *uuid.UUID
	// Status: open, matched, dismissed, or "" for every status.
	Status string
	Limit  int
}

// Claims lists claims, newest first.
func (s *Schools) Claims(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ClaimFilter) ([]Claim, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := tx.Query(ctx, `SELECT `+claimCols+` WHERE c.tenant_id = $1 AND ($2::uuid IS NULL OR c.student_id = $2) AND ($3::uuid IS NULL OR c.family_id = $3)
		AND ($4 = '' OR c.status = $4) ORDER BY c.created_at DESC LIMIT $5`, tenant, f.Student, f.Family, f.Status, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanClaims(ctx, tx, tenant, rows)
	if list == nil {
		list = []Claim{}
	}
	return list, err
}

// ResolveClaim dismisses an open claim with a reason (the bursar looked
// and found no such payment, or the money was found another way).
func (s *Schools) ResolveClaim(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, reason, by string) (Claim, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Claim{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 300 {
		return Claim{}, invalid("dismissing a claim needs a reason (up to 300 characters)")
	}
	tag, err := tx.Exec(ctx, `UPDATE school_payment_claims SET status = 'dismissed', resolved_at = now(), resolved_by = $3, resolution_reason = $4
		WHERE tenant_id = $1 AND id = $2 AND status = 'open'`, tenant, id, by, reason)
	if err != nil {
		return Claim{}, err
	}
	if tag.RowsAffected() == 0 {
		c, err := s.Claim(ctx, tx, tenant, id)
		if err != nil {
			return Claim{}, err
		}
		return Claim{}, refused("the claim was resolved already (%s)", c.Status)
	}
	return s.Claim(ctx, tx, tenant, id)
}

// claimsCounted is told of a payment part counted towards a student's
// invoice: the student's open claims of that amount are matched by it,
// else the oldest open claim is; a claim never counts as a payment.
func (s *Schools) claimsCounted(ctx context.Context, tx pgx.Tx, tenant, student, part uuid.UUID, amountKobo int64, by string) error {
	tag, err := tx.Exec(ctx, `UPDATE school_payment_claims SET status = 'matched', resolved_at = now(), resolved_by = $3, resolved_part = $4, resolution_reason = 'a payment of this amount was counted'
		WHERE tenant_id = $1 AND student_id = $2 AND status = 'open' AND amount_kobo = $5`, tenant, student, by, part, amountKobo)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE school_payment_claims SET status = 'matched', resolved_at = now(), resolved_by = $3, resolved_part = $4, resolution_reason = 'a payment for the student was counted'
		WHERE id = (SELECT id FROM school_payment_claims WHERE tenant_id = $1 AND student_id = $2 AND status = 'open' ORDER BY created_at LIMIT 1)`, tenant, student, by, part)
	return err
}

// OpenClaims is the matcher's ClaimSource: the school's open claims,
// oldest first, beside the unmatched money in To match.
func (s *Schools) OpenClaims(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]matching.Claim, error) {
	out := []matching.Claim{}
	on, err := Enabled(ctx, tx, tenant)
	if err != nil || !on {
		return out, err
	}
	list, err := s.Claims(ctx, tx, tenant, ClaimFilter{Status: "open", Limit: limit})
	if err != nil {
		return nil, err
	}
	for i := len(list) - 1; i >= 0; i-- {
		c := list[i]
		out = append(out, matching.Claim{ID: c.ID, FamilyID: c.FamilyID, StudentID: c.StudentID, Student: c.AdmissionNumber, Name: c.Name, Class: c.Class,
			AmountKobo: c.AmountKobo, PaidOn: c.PaidOn, How: c.How, Reference: c.Reference, Note: c.Note, CreatedAt: c.CreatedAt})
	}
	return out, nil
}

// --- payment plan requests ---

// Instalment is one instalment of a plan: an amount by a date.
type Instalment struct {
	AmountKobo int64  `json:"amount_kobo"`
	DueOn      string `json:"due_on"`
}

// PlanRequest is a payment plan request as the pages show it.
type PlanRequest struct {
	ID                 uuid.UUID    `json:"id"`
	FamilyID           uuid.UUID    `json:"family_id"`
	StudentID          uuid.UUID    `json:"student_id"`
	AdmissionNumber    string       `json:"admission_number"`
	Name               string       `json:"name"`
	Class              string       `json:"class"`
	Instalments        []Instalment `json:"instalments"`
	Message            string       `json:"message,omitempty"`
	Status             string       `json:"status"`
	CreatedAt          time.Time    `json:"created_at"`
	DecidedAt          *time.Time   `json:"decided_at,omitempty"`
	DecidedBy          string       `json:"decided_by,omitempty"`
	DecidedUser        *uuid.UUID   `json:"decided_user,omitempty"`
	DecisionNote       string       `json:"decision_note,omitempty"`
	DecidedInstalments []Instalment `json:"decided_instalments,omitempty"`
	// ToldAt is when the family was told of the decision (B7).
	ToldAt *time.Time `json:"told_at,omitempty"`
}

// TotalKobo is what the instalments come to.
func TotalKobo(list []Instalment) int64 {
	var n int64
	for _, i := range list {
		n += i.AmountKobo
	}
	return n
}

// PlanIn is a parent's plan request.
type PlanIn struct {
	StudentID   uuid.UUID    `json:"student_id"`
	Instalments []Instalment `json:"instalments"`
	Message     string       `json:"message,omitempty"`
}

// CheckInstalments checks 1 to 6 instalments, each an amount by a date
// not past, dates in order.
func CheckInstalments(list []Instalment, now time.Time) ([]Instalment, error) {
	if len(list) == 0 || len(list) > 6 {
		return nil, invalid("a plan has 1 to 6 instalments")
	}
	out := make([]Instalment, 0, len(list))
	last := ""
	for i, in := range list {
		if in.AmountKobo <= 0 || in.AmountKobo > invoicing.MaxTotalKobo {
			return nil, invalid("instalment %d needs an amount in naira", i+1)
		}
		d, ok := dateOnly(in.DueOn)
		if !ok || d.Before(now.Add(-24*time.Hour)) || d.After(now.AddDate(2, 0, 0)) {
			return nil, invalid("instalment %d needs a date from today on, YYYY-MM-DD", i+1)
		}
		due := d.Format(time.DateOnly)
		if due < last {
			return nil, invalid("instalments are in date order")
		}
		last = due
		out = append(out, Instalment{AmountKobo: in.AmountKobo, DueOn: due})
	}
	return out, nil
}

// RequestPlan records a family's plan request for one of its students;
// one at a time per student while the last is undecided.
func (s *Schools) RequestPlan(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, in PlanIn) (PlanRequest, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return PlanRequest{}, err
	}
	list, err := CheckInstalments(in.Instalments, s.now())
	if err != nil {
		return PlanRequest{}, err
	}
	in.Message = strings.TrimSpace(in.Message)
	if len(in.Message) > 500 {
		return PlanRequest{}, invalid("a message has up to 500 characters")
	}
	if err := studentOfFamily(ctx, tx, tenant, family, in.StudentID); err != nil {
		return PlanRequest{}, err
	}
	subject, err := s.familySubject(ctx, tx, tenant, family, in.StudentID)
	if err != nil {
		return PlanRequest{}, err
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", in.Message)
	if err != nil {
		return PlanRequest{}, err
	}
	raw, _ := json.Marshal(list)
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_plan_requests (id, tenant_id, family_id, student_id, instalments, message) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, tenant, family, in.StudentID, raw, sealed); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return PlanRequest{}, refused("the student's last plan request is not decided yet")
		}
		return PlanRequest{}, err
	}
	return s.Plan(ctx, tx, tenant, id)
}

const planCols = `p.id, p.family_id, p.student_id, s.admission_number, s.details, cl.name || CASE WHEN cl.arm = '' THEN '' ELSE ' ' || cl.arm END,
	p.instalments, p.message, p.status, p.created_at, p.decided_at, COALESCE(p.decided_by, ''), p.decided_user, COALESCE(p.decision_note, ''), p.decided_instalments, p.told_at
	FROM school_plan_requests p JOIN school_students s ON s.id = p.student_id JOIN school_classes cl ON cl.id = s.class_id`

func (s *Schools) scanPlans(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]PlanRequest, error) {
	type row struct {
		p                PlanRequest
		student, message map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.p.ID, &x.p.FamilyID, &x.p.StudentID, &x.p.AdmissionNumber, &x.student, &x.p.Class, &x.p.Instalments, &x.message, &x.p.Status, &x.p.CreatedAt,
			&x.p.DecidedAt, &x.p.DecidedBy, &x.p.DecidedUser, &x.p.DecisionNote, &x.p.DecidedInstalments, &x.p.ToldAt)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]PlanRequest, 0, len(list))
	for _, x := range list {
		p := x.p
		var sd studentSealed
		erased, err := s.open(ctx, tx, tenant, x.student, &sd)
		if err != nil {
			return nil, err
		}
		p.Name = sd.Name
		if erased {
			p.Name = Erased
		}
		var msg string
		if _, err := s.open(ctx, tx, tenant, x.message, &msg); err != nil {
			return nil, err
		}
		p.Message = msg
		out = append(out, p)
	}
	return out, nil
}

// Plan reads one plan request.
func (s *Schools) Plan(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (PlanRequest, error) {
	rows, err := tx.Query(ctx, `SELECT `+planCols+` WHERE p.tenant_id = $1 AND p.id = $2`, tenant, id)
	if err != nil {
		return PlanRequest{}, err
	}
	list, err := s.scanPlans(ctx, tx, tenant, rows)
	if err != nil {
		return PlanRequest{}, err
	}
	if len(list) == 0 {
		return PlanRequest{}, ErrNotFound
	}
	return list[0], nil
}

// PlanFilter narrows the plan requests listed.
type PlanFilter struct {
	Student *uuid.UUID
	Family  *uuid.UUID
	// Status: requested, approved, declined, or "" for every status.
	Status string
	Limit  int
}

// Plans lists plan requests, undecided first, then newest first.
func (s *Schools) Plans(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PlanFilter) ([]PlanRequest, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := tx.Query(ctx, `SELECT `+planCols+` WHERE p.tenant_id = $1 AND ($2::uuid IS NULL OR p.student_id = $2) AND ($3::uuid IS NULL OR p.family_id = $3)
		AND ($4 = '' OR p.status = $4) ORDER BY p.status = 'requested' DESC, p.created_at DESC LIMIT $5`, tenant, f.Student, f.Family, f.Status, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanPlans(ctx, tx, tenant, rows)
	if list == nil {
		list = []PlanRequest{}
	}
	return list, err
}

// PlanDecision is what the bursar decides.
type PlanDecision struct {
	Approve bool `json:"approve"`
	// Instalments, when approving, are the instalments as decided (the
	// parent's when empty).
	Instalments []Instalment `json:"instalments,omitempty"`
	Note        string       `json:"note,omitempty"`
}

// DecidePlan approves or declines a requested plan, once, by a person
// with school.approve. Declining needs a note; the note goes to the
// family either way (PlanDecidedAfter).
func (s *Schools) DecidePlan(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, d PlanDecision, by string, user *uuid.UUID) (PlanRequest, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return PlanRequest{}, err
	}
	d.Note = strings.TrimSpace(d.Note)
	if len(d.Note) > 300 {
		return PlanRequest{}, invalid("a note has up to 300 characters")
	}
	if !d.Approve && d.Note == "" {
		return PlanRequest{}, invalid("declining a plan needs a note for the family")
	}
	p, err := s.Plan(ctx, tx, tenant, id)
	if err != nil {
		return PlanRequest{}, err
	}
	if p.Status != "requested" {
		return PlanRequest{}, refused("the plan request was %s already", p.Status)
	}
	status := "declined"
	var decided []byte
	if d.Approve {
		status = "approved"
		list := d.Instalments
		if len(list) == 0 {
			list = p.Instalments
		}
		if list, err = CheckInstalments(list, s.now()); err != nil {
			return PlanRequest{}, err
		}
		decided, _ = json.Marshal(list)
	}
	tag, err := tx.Exec(ctx, `UPDATE school_plan_requests SET status = $3, decided_at = now(), decided_by = $4, decided_user = $5, decision_note = NULLIF($6, ''), decided_instalments = $7
		WHERE tenant_id = $1 AND id = $2 AND status = 'requested'`, tenant, id, status, by, user, d.Note, decided)
	if err != nil {
		return PlanRequest{}, err
	}
	if tag.RowsAffected() == 0 {
		return PlanRequest{}, refused("the plan request was decided already")
	}
	return s.Plan(ctx, tx, tenant, id)
}

// PlanDecisionShort is the SMS: the decision and the page, where the
// instalments and the note are.
func PlanDecisionShort(p PlanRequest, link string) string {
	s := "Payment plan for " + p.AdmissionNumber + ": "
	if p.Status == "approved" {
		s += fmt.Sprintf("approved, %d instalment(s).", len(p.DecidedInstalments))
	} else {
		s += "not approved."
	}
	if link != "" {
		s += " Details on your fees page: " + link
	}
	return s
}

// PlanDecisionText is the message a family gets about a decided plan.
func PlanDecisionText(business string, p PlanRequest, link string) string {
	var b strings.Builder
	if p.Status == "approved" {
		fmt.Fprintf(&b, "%s approved a payment plan for %s:", business, p.AdmissionNumber)
		for i, in := range p.DecidedInstalments {
			if d, err := time.Parse(time.DateOnly, in.DueOn); err == nil {
				fmt.Fprintf(&b, " %d) %s by %s", i+1, invoicing.Naira(in.AmountKobo), d.Format("2 Jan 2006"))
			}
			if i < len(p.DecidedInstalments)-1 {
				b.WriteString(";")
			}
		}
		b.WriteString(".")
	} else {
		fmt.Fprintf(&b, "%s could not approve a payment plan for %s.", business, p.AdmissionNumber)
	}
	if p.DecisionNote != "" {
		b.WriteString(" " + strings.TrimRight(p.DecisionNote, ".") + ".")
	}
	if link != "" {
		b.WriteString(" Your fees page: " + link)
	}
	return b.String()
}
