package schools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Invoicing a term (docs/spec/schools.md 5.3; docs/schools.md#invoicing-
// a-term): one invoice per student enrolled in the term, exactly once.
// Each is the family's (its first guardian's contact, sealed as every
// invoice's customer is) with the customer key student:<id>, so a
// student's own account later pays their own invoices (B1); it records
// the student, their class and the term (school_term_invoices); its lines
// are the class's structure, the student's optional items and their
// standing discounts; it is due on the term's fees_due_on and numbered in
// the business's invoice series.
//
// Exactly once is kept three ways: the invoice is created under the key
// term:<term>:student:<student> (a retry finds it, taking no second
// number), the link's primary key is the term and student, and the plan
// is made under the school's lock, leaving out every student invoiced
// already. Running it again after adding students invoices only them.

// PlanStudent is a student a plan names.
type PlanStudent struct {
	ID              uuid.UUID `json:"id"`
	AdmissionNumber string    `json:"admission_number"`
	Name            string    `json:"name"`
	Class           string    `json:"class"`
	TotalKobo       int64     `json:"total_kobo,omitempty"`
}

// PlanClass is one class in a plan.
type PlanClass struct {
	ClassID   uuid.UUID `json:"class_id"`
	Class     string    `json:"class"`
	Students  int       `json:"students"`
	TotalKobo int64     `json:"total_kobo"`
}

// TermPlan is what invoicing a term will do.
type TermPlan struct {
	Term Term `json:"term"`
	// Enrolled is how many students the term has.
	Enrolled int `json:"enrolled"`
	// ToInvoice students get an invoice now, for TotalKobo in all
	// (PassThroughKobo of it collected for others).
	ToInvoice       int         `json:"to_invoice"`
	TotalKobo       int64       `json:"total_kobo"`
	PassThroughKobo int64       `json:"pass_through_kobo"`
	Classes         []PlanClass `json:"classes"`
	// AlreadyInvoiced students have their invoice for the term.
	AlreadyInvoiced int `json:"already_invoiced"`
	// Left out, and why: no guardian to send it to, no fees for their
	// class, nothing to pay after discounts, a standing discount at or
	// above the threshold (waiting for a second person).
	NoContact     []PlanStudent `json:"no_contact"`
	NoFees        []PlanStudent `json:"no_fees"`
	NothingToPay  []PlanStudent `json:"nothing_to_pay"`
	NeedsApproval []PlanStudent `json:"needs_approval"`
	// Hash is the plan's: confirming with it invoices exactly this plan.
	Hash string `json:"hash"`

	todo []planned
}

type planned struct {
	student  Student
	class    uuid.UUID
	customer invoicing.Customer
	lines    []invoicing.Line
}

// TermInvoiced is what invoicing a term did.
type TermInvoiced struct {
	TermPlan
	// Created is how many invoices this call made (as drafts).
	Created int `json:"created"`
	// Send: the term's drafts are to be sent now (SendTerm).
	Send bool `json:"send"`
	// Sent is how many were sent, and Failed how many could not be, when
	// sent at once.
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}

// PlanTerm works out what invoicing the term will do.
func (s *Schools) PlanTerm(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID) (TermPlan, error) {
	t, err := s.Term(ctx, tx, tenant, termID)
	if err != nil {
		return TermPlan{}, err
	}
	if t.State != TermCurrent {
		return TermPlan{}, refused("%s %s is not the current term: start it first, and a past term is not invoiced", t.Name, t.Session)
	}
	p := TermPlan{Term: t, Classes: []PlanClass{}, NoContact: []PlanStudent{}, NoFees: []PlanStudent{}, NothingToPay: []PlanStudent{}, NeedsApproval: []PlanStudent{}}
	// The students enrolled, with the class they are in for the term and
	// whether they have their invoice.
	rows, err := tx.Query(ctx, `SELECT e.student_id, e.class_id, ti.invoice_id IS NOT NULL FROM school_enrollments e
		LEFT JOIN school_term_invoices ti ON ti.term_id = e.term_id AND ti.student_id = e.student_id
		WHERE e.tenant_id = $1 AND e.term_id = $2`, tenant, termID)
	if err != nil {
		return TermPlan{}, err
	}
	type enr struct {
		student, class uuid.UUID
		done           bool
	}
	enrs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (enr, error) {
		var e enr
		return e, r.Scan(&e.student, &e.class, &e.done)
	})
	if err != nil {
		return TermPlan{}, err
	}
	p.Enrolled = len(enrs)
	var ids []uuid.UUID
	classOf := map[uuid.UUID]uuid.UUID{}
	for _, e := range enrs {
		if e.done {
			p.AlreadyInvoiced++
			continue
		}
		ids = append(ids, e.student)
		classOf[e.student] = e.class
	}
	if len(ids) > 0 {
		if err := s.planTerm(ctx, tx, tenant, &p, ids, classOf); err != nil {
			return TermPlan{}, err
		}
	}
	p.Hash = p.hash()
	return p, nil
}

func (s *Schools) planTerm(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p *TermPlan, ids []uuid.UUID, classOf map[uuid.UUID]uuid.UUID) error {
	students, err := s.listStudents(ctx, tx, tenant, `s.id = ANY ($2::uuid[])`, ids)
	if err != nil {
		return err
	}
	cls, err := classes(ctx, tx, tenant)
	if err != nil {
		return err
	}
	label := map[uuid.UUID]string{}
	order := map[uuid.UUID]int{}
	for i, c := range cls {
		label[c.ID], order[c.ID] = c.Label, i
	}
	items, err := feeItems(ctx, tx, tenant, p.Term.ID, nil)
	if err != nil {
		return err
	}
	byClass := map[uuid.UUID][]FeeItem{}
	for _, it := range items {
		byClass[it.ClassID] = append(byClass[it.ClassID], it)
	}
	// Optional items and discounts, of every student at once.
	rows, err := tx.Query(ctx, `SELECT o.student_id, o.fee_item_id FROM school_student_options o JOIN school_fee_items f ON f.id = o.fee_item_id
		WHERE o.tenant_id = $1 AND f.term_id = $2`, tenant, p.Term.ID)
	if err != nil {
		return err
	}
	chosen := map[uuid.UUID][]uuid.UUID{}
	for rows.Next() {
		var st, it uuid.UUID
		if err := rows.Scan(&st, &it); err != nil {
			rows.Close()
			return err
		}
		chosen[st] = append(chosen[st], it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT id, student_id, kind, percent, amount_kobo, reason, created_by, created_at, ended_at FROM school_discounts
		WHERE tenant_id = $1 AND ended_at IS NULL ORDER BY created_at, id`, tenant)
	if err != nil {
		return err
	}
	ds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Discount, error) {
		var d Discount
		return d, r.Scan(&d.ID, &d.StudentID, &d.Kind, &d.Percent, &d.AmountKobo, &d.Reason, &d.CreatedBy, &d.CreatedAt, &d.EndedAt)
	})
	if err != nil {
		return err
	}
	discountsOf := map[uuid.UUID][]Discount{}
	for _, d := range ds {
		discountsOf[d.StudentID] = append(discountsOf[d.StudentID], d)
	}
	contacts, err := s.familyContacts(ctx, tx, tenant)
	if err != nil {
		return err
	}
	threshold, err := DiscountThreshold(ctx, tx, tenant)
	if err != nil {
		return err
	}
	perClass := map[uuid.UUID]*PlanClass{}
	for _, st := range students {
		class := classOf[st.ID]
		ps := PlanStudent{ID: st.ID, AdmissionNumber: st.AdmissionNumber, Name: st.Name, Class: label[class]}
		var g *Guardian
		if st.FamilyID != nil {
			g = contacts[*st.FamilyID]
		}
		lines := buildLines(byClass[class], chosen[st.ID], discountsOf[st.ID])
		var total int64
		over := false
		for _, l := range lines {
			total += l.AmountKobo()
			if l.Kind == invoicing.LineDiscount && threshold > 0 && -l.AmountKobo() >= threshold {
				over = true
			}
		}
		ps.TotalKobo = total
		switch {
		case g == nil:
			p.NoContact = append(p.NoContact, ps)
		case len(byClass[class]) == 0:
			p.NoFees = append(p.NoFees, ps)
		case over:
			p.NeedsApproval = append(p.NeedsApproval, ps)
		case total < 100:
			p.NothingToPay = append(p.NothingToPay, ps)
		default:
			p.ToInvoice++
			p.TotalKobo += total
			_, bodies := invoicing.PassThrough(lines)
			for _, b := range bodies {
				p.PassThroughKobo += b.Kobo
			}
			pc := perClass[class]
			if pc == nil {
				pc = &PlanClass{ClassID: class, Class: label[class]}
				perClass[class] = pc
			}
			pc.Students++
			pc.TotalKobo += total
			p.todo = append(p.todo, planned{student: st, class: class, lines: lines,
				customer: invoicing.Customer{Name: g.Name, Phone: g.Phone, Email: g.Email, Key: StudentKey(st.ID)}})
		}
	}
	for _, pc := range perClass {
		p.Classes = append(p.Classes, *pc)
	}
	sort.Slice(p.Classes, func(i, j int) bool { return order[p.Classes[i].ClassID] < order[p.Classes[j].ClassID] })
	sort.Slice(p.todo, func(i, j int) bool { return p.todo[i].student.AdmissionNumber < p.todo[j].student.AdmissionNumber })
	return nil
}

// StudentKey is a student's customer key on their invoices (B1's customer
// identity): their permanent account later pays these.
func StudentKey(id uuid.UUID) string { return "student:" + id.String() }

// TermInvoiceKey is the key a student's invoice for a term is made under.
func TermInvoiceKey(term, student uuid.UUID) string {
	return "term:" + term.String() + ":student:" + student.String()
}

// familyContacts is each family's first guardian (the earliest added),
// whom its invoices go to.
func (s *Schools) familyContacts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[uuid.UUID]*Guardian, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (family_id) id, family_id, details FROM school_guardians WHERE tenant_id = $1 ORDER BY family_id, created_at, id`, tenant)
	if err != nil {
		return nil, err
	}
	type gRow struct {
		id, family uuid.UUID
		env        map[string]any
	}
	gs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (gRow, error) {
		var g gRow
		return g, r.Scan(&g.id, &g.family, &g.env)
	})
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*Guardian{}
	for _, g := range gs {
		var d guardianSealed
		erased, err := s.open(ctx, tx, tenant, g.env, &d)
		if err != nil {
			return nil, err
		}
		if erased || d.Phone == "" {
			continue // an erased guardian is nobody to send to
		}
		out[g.family] = &Guardian{ID: g.id, Name: d.Name, Phone: d.Phone, Email: d.Email, Relationship: d.Relationship}
	}
	return out, nil
}

// hash is the plan's: the students it invoices and what for.
func (p TermPlan) hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "term %s\n", p.Term.ID)
	for _, x := range p.todo {
		raw, _ := json.Marshal(x.lines)
		fmt.Fprintf(h, "%s %s %s\n", x.student.ID, x.class, raw)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// TermNote is the note on a student's term invoice: no name (a note is not
// sealed), the admission number, class and term.
func TermNote(admission, class string, t Term) string {
	return fmt.Sprintf("School fees: admission no. %s, %s, %s %s.", admission, class, t.Name, t.Session)
}

// InvoiceTerm invoices the term as planned: hash, when given, must be the
// plan's still (else ErrState: the plan changed; check again). Each
// student without their invoice gets it, as a draft, once; send says the
// caller sends the term's drafts next (SendTerm), after this commits.
func (s *Schools) InvoiceTerm(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID, hash string, send bool, by string) (TermInvoiced, error) {
	if s.Invoices == nil {
		return TermInvoiced{}, refused("invoices are not available on this server")
	}
	if err := lock(ctx, tx, tenant); err != nil {
		return TermInvoiced{}, err
	}
	p, err := s.PlanTerm(ctx, tx, tenant, termID)
	if err != nil {
		return TermInvoiced{}, err
	}
	if hash != "" && hash != p.Hash {
		return TermInvoiced{}, refused("the plan changed since it was shown (students, fees or discounts): check it again")
	}
	out := TermInvoiced{TermPlan: p, Send: send}
	for _, x := range p.todo {
		inv, err := s.Invoices.CreateOnce(ctx, tx, tenant, TermInvoiceKey(termID, x.student.ID), invoicing.Draft{
			Customer: x.customer, Lines: x.lines, DueOn: p.Term.FeesDueOn, Note: TermNote(x.student.AdmissionNumber, labelOf(p, x.class), p.Term),
		}, by)
		if err != nil {
			return TermInvoiced{}, fmt.Errorf("invoicing %s: %w", x.student.AdmissionNumber, err)
		}
		tag, err := tx.Exec(ctx, `INSERT INTO school_term_invoices (tenant_id, term_id, student_id, class_id, invoice_id, created_by) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT DO NOTHING`, tenant, termID, x.student.ID, x.class, inv.ID, by)
		if err != nil {
			return TermInvoiced{}, err
		}
		if tag.RowsAffected() > 0 {
			out.Created++
		}
	}
	out.AlreadyInvoiced += out.Created
	return out, nil
}

func labelOf(p TermPlan, class uuid.UUID) string {
	for _, c := range p.Classes {
		if c.ClassID == class {
			return c.Class
		}
	}
	return ""
}

// TermDrafts are the term's invoices still drafts, oldest first.
func (s *Schools) TermDrafts(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT i.id FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND i.status = 'draft' ORDER BY i.number`, tenant, termID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// SendTerm sends each of the term's drafts with the existing Send (which
// counts the family's credit first), each under its own key so a retry
// sends no message twice. It reports how many were sent and how many
// could not be (their errors, by invoice).
func (s *Schools) SendTerm(ctx context.Context, tenant, termID uuid.UUID, by string) (int, map[string]string, error) {
	if s.Invoices == nil {
		return 0, nil, refused("invoices are not available on this server")
	}
	var drafts []uuid.UUID
	if err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		drafts, err = s.TermDrafts(ctx, tx, tenant, termID)
		return err
	}); err != nil {
		return 0, nil, err
	}
	sent, failed := 0, map[string]string{}
	for _, id := range drafts {
		if ctx.Err() != nil {
			return sent, failed, ctx.Err()
		}
		if _, err := s.Invoices.Send(ctx, tenant, id.String(), by, "term:"+termID.String()+":send:"+id.String()); err != nil {
			failed[id.String()] = err.Error()
			continue
		}
		sent++
	}
	return sent, failed, nil
}

// InvoiceTermOnce is invoice_term in a workflow: the plan and the
// confirmation in one, under the step's key, so a retry of the step
// answers what the first call did and invoices nobody twice.
func (s *Schools) InvoiceTermOnce(ctx context.Context, tenant, termID uuid.UUID, key string, send bool, by string) (TermInvoiced, error) {
	var out TermInvoiced
	found := false
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT result FROM school_term_invoicings WHERE tenant_id = $1 AND key = $2`, tenant, key).Scan(&raw)
		if err == nil {
			found = true
			return json.Unmarshal(raw, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out, err = s.InvoiceTerm(ctx, tx, tenant, termID, "", send, by); err != nil {
			return err
		}
		raw, _ = json.Marshal(out)
		_, err = tx.Exec(ctx, `INSERT INTO school_term_invoicings (tenant_id, key, term_id, result, created_by) VALUES ($1, $2, $3, $4, $5)`, tenant, key, termID, raw, by)
		return err
	})
	if err != nil || found || !send {
		return out, err
	}
	sent, failed, err := s.SendTerm(ctx, tenant, termID, by)
	out.Sent, out.Failed = sent, len(failed)
	if err != nil {
		return out, err
	}
	// The stored answer says what was sent, for a retry of the step.
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		raw, _ := json.Marshal(out)
		_, err := tx.Exec(ctx, `UPDATE school_term_invoicings SET result = $3 WHERE tenant_id = $1 AND key = $2`, tenant, key, raw)
		return err
	})
	return out, err
}

// --- the term's invoices ---

// TermInvoice is a student's invoice for a term.
type TermInvoice struct {
	InvoiceID       uuid.UUID  `json:"invoice_id"`
	Number          string     `json:"number"`
	Status          string     `json:"status"`
	TermID          uuid.UUID  `json:"term_id"`
	Term            string     `json:"term"`
	StudentID       uuid.UUID  `json:"student_id"`
	AdmissionNumber string     `json:"admission_number"`
	Name            string     `json:"name"`
	ClassID         uuid.UUID  `json:"class_id"`
	Class           string     `json:"class"`
	TotalKobo       int64      `json:"total_kobo"`
	PaidKobo        int64      `json:"paid_kobo"`
	DueKobo         int64      `json:"due_kobo"`
	SentAt          *time.Time `json:"sent_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const termInvoiceSelect = `SELECT ti.invoice_id, i.number, i.status, ti.term_id, t.name, ss.label, ti.student_id, s.admission_number, ti.class_id, c.name, c.arm,
	i.total_kobo, i.paid_kobo, i.sent_at, i.updated_at
	FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id JOIN school_students s ON s.id = ti.student_id
	JOIN school_classes c ON c.id = ti.class_id JOIN school_terms t ON t.id = ti.term_id JOIN school_sessions ss ON ss.id = t.session_id`

const termInvoiceOrder = ` ORDER BY c.class_order, lower(c.name), lower(c.arm), upper(s.admission_number)`

func (s *Schools) termInvoices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where string, args ...any) ([]TermInvoice, error) {
	return s.termInvoicesBy(ctx, tx, tenant, where, termInvoiceOrder, args...)
}

func (s *Schools) termInvoicesBy(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where, order string, args ...any) ([]TermInvoice, error) {
	rows, err := tx.Query(ctx, termInvoiceSelect+` WHERE ti.tenant_id = $1 AND `+where+order, append([]any{tenant}, args...)...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TermInvoice, error) {
		var x TermInvoice
		var name, arm, term, session string
		err := r.Scan(&x.InvoiceID, &x.Number, &x.Status, &x.TermID, &term, &session, &x.StudentID, &x.AdmissionNumber, &x.ClassID, &name, &arm,
			&x.TotalKobo, &x.PaidKobo, &x.SentAt, &x.UpdatedAt)
		x.Class = Label(name, arm)
		x.Term = term + " " + session
		x.DueKobo = max(x.TotalKobo-x.PaidKobo, 0)
		if x.Status == "void" {
			x.DueKobo = 0
		}
		return x, err
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return []TermInvoice{}, nil
	}
	ids := make([]uuid.UUID, 0, len(list))
	for _, x := range list {
		ids = append(ids, x.StudentID)
	}
	sts, err := s.listStudents(ctx, tx, tenant, `s.id = ANY ($2::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, st := range sts {
		names[st.ID] = st.Name
	}
	for i := range list {
		list[i].Name = names[list[i].StudentID]
	}
	return list, nil
}

// TermInvoices lists a term's invoices by class and admission number,
// optionally of one class and status.
func (s *Schools) TermInvoices(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID, class *uuid.UUID, status string) ([]TermInvoice, error) {
	if _, err := s.Term(ctx, tx, tenant, termID); err != nil {
		return nil, err
	}
	switch status {
	case "", "draft", "sent", "part_paid", "paid", "overdue", "void":
	default:
		return nil, invalid("status is draft, sent, part_paid, paid, overdue or void")
	}
	return s.termInvoices(ctx, tx, tenant, `ti.term_id = $2 AND ($3::uuid IS NULL OR ti.class_id = $3) AND ($4 = '' OR i.status = $4)`, termID, class, status)
}

// rebuildDrafts rebuilds the term invoices still drafts that where
// selects (on school_term_invoices ti, args from $2) from the fees,
// options and discounts as they are now. A draft that would come to
// nothing is left as it was. It reports how many were rebuilt.
func (s *Schools) rebuildDrafts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where string, args ...any) (int, error) {
	if s.Invoices == nil {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `SELECT ti.invoice_id, ti.term_id, ti.student_id, ti.class_id FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND i.status = 'draft' AND `+where, append([]any{tenant}, args...)...)
	if err != nil {
		return 0, err
	}
	type draft struct{ invoice, term, student, class uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (draft, error) {
		var d draft
		return d, r.Scan(&d.invoice, &d.term, &d.student, &d.class)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		items, err := feeItems(ctx, tx, tenant, d.term, &d.class)
		if err != nil {
			return n, err
		}
		chosen, err := studentOptions(ctx, tx, tenant, d.student, d.term)
		if err != nil {
			return n, err
		}
		ds, err := discounts(ctx, tx, tenant, d.student, false)
		if err != nil {
			return n, err
		}
		lines := buildLines(items, chosen, ds)
		var total int64
		for _, l := range lines {
			total += l.AmountKobo()
		}
		if total < 100 {
			continue
		}
		cur, err := s.Invoices.Get(ctx, tx, tenant, d.invoice.String())
		if err != nil {
			return n, err
		}
		if cur.Customer.Phone == "" && cur.Customer.Email == "" {
			continue // the family's details were erased
		}
		if _, err := s.Invoices.Update(ctx, tx, tenant, cur.ID.String(), invoicing.Draft{Customer: cur.Customer, Lines: lines, DueOn: cur.DueOn, Note: cur.Note,
			Connection: cur.Connection, FeePaidBy: cur.FeePaidBy, AllowPartPayments: &cur.AllowPartPayments}, "system:schools"); err != nil {
			return n, fmt.Errorf("rebuilding invoice %s: %w", cur.Number, err)
		}
		n++
	}
	return n, nil
}
