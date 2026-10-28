package schools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Fees (docs/spec/schools.md 5.1; docs/schools.md#fees): a class's fee
// structure for a term is its list of fee items, each required or
// optional (transport, lessons a family chooses) and, for money the
// school collects on someone else's behalf (WAEC, NECO), the body it is
// for. A student's optional items for a term and their standing
// discounts are recorded with them; invoicing the term builds each
// student's invoice from the three (invoiceterm.go).
//
// A change to a structure, a student's items or discounts after the term
// is invoiced is made to the drafts not yet sent, rebuilt from it; a sent
// invoice changes only by amendment (invoicing.Amend).

// Kinds of fee item.
const (
	Required = "required"
	Optional = "optional"
)

// MaxFeeItems is the most items one class's structure has.
const MaxFeeItems = 40

// FeeItem is one item of a class's fees for a term.
type FeeItem struct {
	ID             uuid.UUID `json:"id"`
	TermID         uuid.UUID `json:"term_id"`
	ClassID        uuid.UUID `json:"class_id"`
	Name           string    `json:"name"`
	AmountKobo     int64     `json:"amount_kobo"`
	Kind           string    `json:"kind"`
	PassThroughFor string    `json:"pass_through_for,omitempty"`
	Position       int       `json:"position"`
}

// FeeItemIn is a fee item to set.
type FeeItemIn struct {
	Name           string `json:"name"`
	AmountKobo     int64  `json:"amount_kobo"`
	Kind           string `json:"kind"`
	PassThroughFor string `json:"pass_through_for,omitempty"`
}

// ClassFees is one class's structure for a term.
type ClassFees struct {
	ClassID uuid.UUID `json:"class_id"`
	Class   string    `json:"class"`
	Items   []FeeItem `json:"items"`
	// RequiredKobo is what every student of the class pays before
	// optional items and discounts.
	RequiredKobo int64 `json:"required_kobo"`
	// PassThroughKobo is the part of RequiredKobo collected for others.
	PassThroughKobo int64 `json:"pass_through_kobo"`
}

func checkItems(in []FeeItemIn) ([]FeeItemIn, error) {
	if len(in) > MaxFeeItems {
		return nil, invalid("a class has at most %d fee items", MaxFeeItems)
	}
	seen := map[string]bool{}
	for i := range in {
		it := &in[i]
		it.Name = strings.Join(strings.Fields(it.Name), " ")
		it.PassThroughFor = strings.Join(strings.Fields(it.PassThroughFor), " ")
		if it.Kind == "" {
			it.Kind = Required
		}
		switch {
		case it.Name == "" || len(it.Name) > 80:
			return nil, invalid("fee item %d needs a name (up to 80 characters), such as Tuition", i+1)
		case seen[strings.ToLower(it.Name)]:
			return nil, invalid("the class has %s twice", it.Name)
		case it.AmountKobo < 100 || it.AmountKobo > invoicing.MaxTotalKobo:
			return nil, invalid("%s needs an amount of at least ₦1", it.Name)
		case it.Kind != Required && it.Kind != Optional:
			return nil, invalid("%s: kind is required or optional", it.Name)
		case len(it.PassThroughFor) > 60:
			return nil, invalid("%s: who it is collected for has up to 60 characters", it.Name)
		}
		seen[strings.ToLower(it.Name)] = true
	}
	return in, nil
}

const feeCols = `f.id, f.term_id, f.class_id, f.name, f.amount_kobo, f.kind, COALESCE(f.pass_through_for, ''), f.position`

func scanFee(r pgx.CollectableRow) (FeeItem, error) {
	var f FeeItem
	return f, r.Scan(&f.ID, &f.TermID, &f.ClassID, &f.Name, &f.AmountKobo, &f.Kind, &f.PassThroughFor, &f.Position)
}

func feeItems(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID, class *uuid.UUID) ([]FeeItem, error) {
	rows, err := tx.Query(ctx, `SELECT `+feeCols+` FROM school_fee_items f WHERE f.tenant_id = $1 AND f.term_id = $2 AND ($3::uuid IS NULL OR f.class_id = $3)
		ORDER BY f.class_id, f.position, lower(f.name)`, tenant, term, class)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanFee)
	if out == nil {
		out = []FeeItem{}
	}
	return out, err
}

func classFees(c Class, items []FeeItem) ClassFees {
	cf := ClassFees{ClassID: c.ID, Class: c.Label, Items: []FeeItem{}}
	for _, it := range items {
		if it.ClassID != c.ID {
			continue
		}
		cf.Items = append(cf.Items, it)
		if it.Kind == Required {
			cf.RequiredKobo += it.AmountKobo
			if it.PassThroughFor != "" {
				cf.PassThroughKobo += it.AmountKobo
			}
		}
	}
	return cf
}

// TermFees lists every class's structure for a term, classes in their
// order (a class with no items has an empty list).
func (s *Schools) TermFees(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID) ([]ClassFees, error) {
	if _, err := s.Term(ctx, tx, tenant, term); err != nil {
		return nil, err
	}
	cls, err := classes(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	items, err := feeItems(ctx, tx, tenant, term, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ClassFees, 0, len(cls))
	for _, c := range cls {
		out = append(out, classFees(c, items))
	}
	return out, nil
}

// Fees is one class's structure for a term.
func (s *Schools) Fees(ctx context.Context, tx pgx.Tx, tenant, term, class uuid.UUID) (ClassFees, error) {
	if _, err := s.Term(ctx, tx, tenant, term); err != nil {
		return ClassFees{}, err
	}
	c, err := s.Class(ctx, tx, tenant, class)
	if err != nil {
		return ClassFees{}, err
	}
	items, err := feeItems(ctx, tx, tenant, term, &class)
	if err != nil {
		return ClassFees{}, err
	}
	return classFees(c, items), nil
}

// FeesSet is what setting a structure did.
type FeesSet struct {
	Fees ClassFees `json:"fees"`
	// DraftsUpdated is how many of the term's draft invoices for the
	// class were rebuilt from it; sent ones are amended, never rebuilt.
	DraftsUpdated int `json:"drafts_updated"`
}

// SetFees sets a class's whole structure for a term: items are matched
// by name (whatever its case), so an item kept keeps the students who
// chose it; an item left out is removed. A past term's fees are
// read-only.
func (s *Schools) SetFees(ctx context.Context, tx pgx.Tx, tenant, term, class uuid.UUID, in []FeeItemIn, by string) (FeesSet, error) {
	t, err := s.Term(ctx, tx, tenant, term)
	if err != nil {
		return FeesSet{}, err
	}
	if t.State == TermPast {
		return FeesSet{}, refused("%s %s is past: its fees are read-only", t.Name, t.Session)
	}
	if _, err := s.Class(ctx, tx, tenant, class); err != nil {
		return FeesSet{}, err
	}
	in, err = checkItems(in)
	if err != nil {
		return FeesSet{}, err
	}
	cur, err := feeItems(ctx, tx, tenant, term, &class)
	if err != nil {
		return FeesSet{}, err
	}
	byName := map[string]FeeItem{}
	for _, f := range cur {
		byName[strings.ToLower(f.Name)] = f
	}
	keep := map[uuid.UUID]bool{}
	for i, it := range in {
		if old, ok := byName[strings.ToLower(it.Name)]; ok {
			keep[old.ID] = true
			if _, err := tx.Exec(ctx, `UPDATE school_fee_items SET name = $3, amount_kobo = $4, kind = $5, pass_through_for = $6, position = $7, updated_at = now()
				WHERE tenant_id = $1 AND id = $2`, tenant, old.ID, it.Name, it.AmountKobo, it.Kind, nullable(it.PassThroughFor), i); err != nil {
				return FeesSet{}, checked(err)
			}
			if it.Kind == Required {
				// No longer a choice: nobody "chose" it.
				if _, err := tx.Exec(ctx, `DELETE FROM school_student_options WHERE tenant_id = $1 AND fee_item_id = $2`, tenant, old.ID); err != nil {
					return FeesSet{}, err
				}
			}
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO school_fee_items (id, tenant_id, term_id, class_id, name, amount_kobo, kind, pass_through_for, position, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, newID(), tenant, term, class, it.Name, it.AmountKobo, it.Kind, nullable(it.PassThroughFor), i, by); err != nil {
			return FeesSet{}, checked(err)
		}
	}
	for _, f := range cur {
		if !keep[f.ID] {
			if _, err := tx.Exec(ctx, `DELETE FROM school_fee_items WHERE tenant_id = $1 AND id = $2`, tenant, f.ID); err != nil {
				return FeesSet{}, checked(err)
			}
		}
	}
	n, err := s.rebuildDrafts(ctx, tx, tenant, `ti.term_id = $2 AND ti.class_id = $3`, term, class)
	if err != nil {
		return FeesSet{}, err
	}
	fees, err := s.Fees(ctx, tx, tenant, term, class)
	return FeesSet{Fees: fees, DraftsUpdated: n}, err
}

func nullable(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// PreviousTerm is the term before this one, by when they start; ok false
// for the school's first.
func (s *Schools) PreviousTerm(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID) (Term, bool, error) {
	t, err := s.Term(ctx, tx, tenant, term)
	if err != nil {
		return Term{}, false, err
	}
	list, err := s.terms(ctx, tx, tenant, `t.starts_on < $2::date`, t.StartsOn)
	if err != nil || len(list) == 0 {
		return Term{}, false, err
	}
	return list[len(list)-1], true, nil
}

// CopyFees sets a class's structure for a term to another's: the same
// class in the term before (fromTerm nil), or another class or term.
func (s *Schools) CopyFees(ctx context.Context, tx pgx.Tx, tenant, term, class uuid.UUID, fromTerm, fromClass *uuid.UUID, by string) (FeesSet, error) {
	src := Term{}
	if fromTerm == nil {
		prev, ok, err := s.PreviousTerm(ctx, tx, tenant, term)
		if err != nil {
			return FeesSet{}, err
		}
		if !ok {
			return FeesSet{}, refused("there is no term before this one to copy from")
		}
		src = prev
	} else {
		var err error
		if src, err = s.Term(ctx, tx, tenant, *fromTerm); err != nil {
			return FeesSet{}, err
		}
	}
	srcClass := class
	if fromClass != nil {
		srcClass = *fromClass
	}
	if src.ID == term && srcClass == class {
		return FeesSet{}, invalid("copy from another class or term")
	}
	from, err := s.Fees(ctx, tx, tenant, src.ID, srcClass)
	if err != nil {
		return FeesSet{}, err
	}
	if len(from.Items) == 0 {
		return FeesSet{}, refused("%s has no fees in %s %s to copy", from.Class, src.Name, src.Session)
	}
	in := make([]FeeItemIn, 0, len(from.Items))
	for _, it := range from.Items {
		in = append(in, FeeItemIn{Name: it.Name, AmountKobo: it.AmountKobo, Kind: it.Kind, PassThroughFor: it.PassThroughFor})
	}
	return s.SetFees(ctx, tx, tenant, term, class, in, by)
}

// --- a student's optional items ---

// studentClassFor is the class a student is in for a term: their
// enrollment's, else their class now.
func studentClassFor(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID) (uuid.UUID, error) {
	var class uuid.UUID
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT e.class_id FROM school_enrollments e WHERE e.term_id = $3 AND e.student_id = s.id), s.class_id)
		FROM school_students s WHERE s.tenant_id = $1 AND s.id = $2`, tenant, student, term).Scan(&class)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return class, err
}

// StudentFees is a student's fees for a term: their class's items, which
// optional ones they take, and their standing discounts.
type StudentFees struct {
	StudentID uuid.UUID `json:"student_id"`
	TermID    uuid.UUID `json:"term_id"`
	ClassID   uuid.UUID `json:"class_id"`
	Items     []FeeItem `json:"items"`
	// Chosen are the optional items the student takes.
	Chosen    []uuid.UUID `json:"chosen"`
	Discounts []Discount  `json:"discounts"`
	// Lines are the invoice the student's fees make now, discounts
	// included; TotalKobo their sum.
	Lines     []invoicing.Line `json:"lines"`
	TotalKobo int64            `json:"total_kobo"`
	// Invoice is the term's invoice when the student has one.
	Invoice *TermInvoice `json:"invoice,omitempty"`
}

// StudentTermFees reads a student's fees for a term.
func (s *Schools) StudentTermFees(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID) (StudentFees, error) {
	if _, err := s.Term(ctx, tx, tenant, term); err != nil {
		return StudentFees{}, err
	}
	class, err := studentClassFor(ctx, tx, tenant, student, term)
	if err != nil {
		return StudentFees{}, err
	}
	items, err := feeItems(ctx, tx, tenant, term, &class)
	if err != nil {
		return StudentFees{}, err
	}
	chosen, err := studentOptions(ctx, tx, tenant, student, term)
	if err != nil {
		return StudentFees{}, err
	}
	ds, err := discounts(ctx, tx, tenant, student, false)
	if err != nil {
		return StudentFees{}, err
	}
	out := StudentFees{StudentID: student, TermID: term, ClassID: class, Items: items, Chosen: chosen, Discounts: ds}
	out.Lines = buildLines(items, chosen, ds)
	for _, l := range out.Lines {
		out.TotalKobo += l.AmountKobo()
	}
	list, err := s.termInvoices(ctx, tx, tenant, `ti.term_id = $2 AND ti.student_id = $3`, term, student)
	if err != nil {
		return StudentFees{}, err
	}
	if len(list) > 0 {
		out.Invoice = &list[0]
	}
	return out, nil
}

func studentOptions(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT o.fee_item_id FROM school_student_options o JOIN school_fee_items f ON f.id = o.fee_item_id
		WHERE o.tenant_id = $1 AND o.student_id = $2 AND f.term_id = $3 ORDER BY f.position`, tenant, student, term)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if out == nil {
		out = []uuid.UUID{}
	}
	return out, err
}

// SetOptions records the optional items a student takes in a term (all
// of them: an item left out is no longer taken), and rebuilds their draft
// invoice for it, if any. It reports what changed and how many drafts were
// rebuilt.
func (s *Schools) SetOptions(ctx context.Context, tx pgx.Tx, tenant, student, term uuid.UUID, items []uuid.UUID, by string) (StudentFees, int, error) {
	t, err := s.Term(ctx, tx, tenant, term)
	if err != nil {
		return StudentFees{}, 0, err
	}
	if t.State == TermPast {
		return StudentFees{}, 0, refused("%s %s is past: its fees are read-only", t.Name, t.Session)
	}
	class, err := studentClassFor(ctx, tx, tenant, student, term)
	if err != nil {
		return StudentFees{}, 0, err
	}
	offered, err := feeItems(ctx, tx, tenant, term, &class)
	if err != nil {
		return StudentFees{}, 0, err
	}
	for _, id := range items {
		ok := slices.ContainsFunc(offered, func(f FeeItem) bool { return f.ID == id && f.Kind == Optional })
		if !ok {
			return StudentFees{}, 0, invalid("a chosen item is one of the optional items of the student's class for the term")
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM school_student_options o USING school_fee_items f WHERE o.fee_item_id = f.id AND o.tenant_id = $1
		AND o.student_id = $2 AND f.term_id = $3 AND NOT (o.fee_item_id = ANY ($4::uuid[]))`, tenant, student, term, items); err != nil {
		return StudentFees{}, 0, err
	}
	if len(items) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO school_student_options (tenant_id, student_id, fee_item_id, created_by)
			SELECT $1, $2, x, $4 FROM unnest($3::uuid[]) x ON CONFLICT DO NOTHING`, tenant, student, items, by); err != nil {
			return StudentFees{}, 0, err
		}
	}
	n, err := s.rebuildDrafts(ctx, tx, tenant, `ti.term_id = $2 AND ti.student_id = $3`, term, student)
	if err != nil {
		return StudentFees{}, 0, err
	}
	out, err := s.StudentTermFees(ctx, tx, tenant, student, term)
	return out, n, err
}

// --- standing discounts ---

// Kinds of discount.
var DiscountKinds = []string{"sibling", "staff_child", "scholarship", "hardship", "other"}

var discountWords = map[string]string{"sibling": "Sibling", "staff_child": "Staff child", "scholarship": "Scholarship", "hardship": "Hardship", "other": "Other"}

// Discount is a student's standing discount: a percent of the school's
// own fees (never of money collected for others) or an amount, taken off
// every term invoice made while it stands.
type Discount struct {
	ID         uuid.UUID  `json:"id"`
	StudentID  uuid.UUID  `json:"student_id"`
	Kind       string     `json:"kind"`
	Percent    *int       `json:"percent,omitempty"`
	AmountKobo *int64     `json:"amount_kobo,omitempty"`
	Reason     string     `json:"reason"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}

// DiscountIn is a discount to give.
type DiscountIn struct {
	Kind       string `json:"kind"`
	Percent    *int   `json:"percent,omitempty"`
	AmountKobo *int64 `json:"amount_kobo,omitempty"`
	Reason     string `json:"reason"`
}

// Description is the discount's line on an invoice.
func (d Discount) Description() string {
	w := discountWords[d.Kind] + " discount"
	if d.Percent != nil {
		w += fmt.Sprintf(" (%d%%)", *d.Percent)
	}
	return w
}

// Of is what the discount takes off own, the school's own fees.
func (d Discount) Of(own int64) int64 {
	if d.Percent != nil {
		return (own*int64(*d.Percent) + 50) / 100
	}
	if d.AmountKobo != nil {
		return *d.AmountKobo
	}
	return 0
}

func discounts(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, ended bool) ([]Discount, error) {
	rows, err := tx.Query(ctx, `SELECT id, student_id, kind, percent, amount_kobo, reason, created_by, created_at, ended_at FROM school_discounts
		WHERE tenant_id = $1 AND student_id = $2 AND ($3 OR ended_at IS NULL) ORDER BY created_at, id`, tenant, student, ended)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Discount, error) {
		var d Discount
		return d, r.Scan(&d.ID, &d.StudentID, &d.Kind, &d.Percent, &d.AmountKobo, &d.Reason, &d.CreatedBy, &d.CreatedAt, &d.EndedAt)
	})
	if out == nil {
		out = []Discount{}
	}
	return out, err
}

// Discounts lists a student's discounts, the ended ones too.
func (s *Schools) Discounts(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) ([]Discount, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if _, err := s.student(ctx, tx, tenant, student.String()); err != nil {
		return nil, err
	}
	return discounts(ctx, tx, tenant, student, true)
}

// AddDiscount gives a student a standing discount, and rebuilds their
// draft invoices. One at or above the school's discount threshold is
// recorded as a request for a second person instead (ApprovalNeeded,
// cash.go): a percent is weighed against the student's fees in the
// current term.
func (s *Schools) AddDiscount(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, in DiscountIn, by string) (Discount, int, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Discount{}, 0, err
	}
	st, err := s.student(ctx, tx, tenant, student.String())
	if err != nil {
		return Discount{}, 0, err
	}
	in.Reason = strings.Join(strings.Fields(in.Reason), " ")
	switch {
	case !slices.Contains(DiscountKinds, in.Kind):
		return Discount{}, 0, invalid("kind is sibling, staff_child, scholarship, hardship or other")
	case (in.Percent == nil) == (in.AmountKobo == nil):
		return Discount{}, 0, invalid("a discount is a percent or an amount")
	case in.Percent != nil && (*in.Percent < 1 || *in.Percent > 100):
		return Discount{}, 0, invalid("a percent is 1 to 100")
	case in.AmountKobo != nil && (*in.AmountKobo < 100 || *in.AmountKobo > invoicing.MaxTotalKobo):
		return Discount{}, 0, invalid("an amount is at least ₦1")
	case in.Reason == "" || len(in.Reason) > 200:
		return Discount{}, 0, invalid("say why the discount is given (up to 200 characters)")
	}
	d := Discount{Kind: in.Kind, Percent: in.Percent, AmountKobo: in.AmountKobo}
	worth := d.Of(0)
	if in.Percent != nil {
		if cur, ok, err := currentTerm(ctx, tx, tenant); err != nil {
			return Discount{}, 0, err
		} else if ok {
			class, err := studentClassFor(ctx, tx, tenant, student, cur.ID)
			if err != nil {
				return Discount{}, 0, err
			}
			items, err := feeItems(ctx, tx, tenant, cur.ID, &class)
			if err != nil {
				return Discount{}, 0, err
			}
			chosen, err := studentOptions(ctx, tx, tenant, student, cur.ID)
			if err != nil {
				return Discount{}, 0, err
			}
			own, _ := invoicing.PassThrough(buildLines(items, chosen, nil))
			worth = d.Of(own)
		}
	}
	if err := s.CheckDiscount(ctx, tx, tenant, student, worth); errors.Is(err, ErrNeedsApproval) {
		return Discount{}, 0, s.requestDiscount(ctx, tx, tenant, st, in, worth, by)
	} else if err != nil {
		return Discount{}, 0, err
	}
	return s.giveDiscount(ctx, tx, tenant, student, in, worth, by)
}

// giveDiscount records a standing discount (checked already) with what
// it was weighed at, and rebuilds the student's drafts.
func (s *Schools) giveDiscount(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, in DiscountIn, worth int64, by string) (Discount, int, error) {
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_discounts (id, tenant_id, student_id, kind, percent, amount_kobo, reason, created_by, worth_kobo) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, tenant, student, in.Kind, in.Percent, in.AmountKobo, in.Reason, by, worth); err != nil {
		return Discount{}, 0, err
	}
	n, err := s.rebuildDrafts(ctx, tx, tenant, `ti.student_id = $2`, student)
	if err != nil {
		return Discount{}, 0, err
	}
	list, err := discounts(ctx, tx, tenant, student, false)
	if err != nil {
		return Discount{}, 0, err
	}
	for _, x := range list {
		if x.ID == id {
			return x, n, nil
		}
	}
	return Discount{}, n, ErrNotFound
}

// EndDiscount ends a standing discount: invoices already sent keep it;
// drafts are rebuilt without it.
func (s *Schools) EndDiscount(ctx context.Context, tx pgx.Tx, tenant, student, id uuid.UUID, by string) (Discount, int, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Discount{}, 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE school_discounts SET ended_at = now(), ended_by = $4 WHERE tenant_id = $1 AND student_id = $2 AND id = $3 AND ended_at IS NULL`,
		tenant, student, id, by)
	if err != nil {
		return Discount{}, 0, err
	}
	list, err := discounts(ctx, tx, tenant, student, true)
	if err != nil {
		return Discount{}, 0, err
	}
	i := slices.IndexFunc(list, func(d Discount) bool { return d.ID == id })
	if i < 0 {
		return Discount{}, 0, ErrNotFound
	}
	n := 0
	if tag.RowsAffected() > 0 {
		if n, err = s.rebuildDrafts(ctx, tx, tenant, `ti.student_id = $2`, student); err != nil {
			return Discount{}, 0, err
		}
	}
	return list[i], n, nil
}

// buildLines is a student's invoice for a term: the required items, the
// optional ones they take, then each standing discount as its own
// negative line with its reason, taken off the school's own fees only
// (never off money collected for others) and never below nothing.
func buildLines(items []FeeItem, chosen []uuid.UUID, ds []Discount) []invoicing.Line {
	lines := []invoicing.Line{}
	for _, it := range items {
		if it.Kind == Optional && !slices.Contains(chosen, it.ID) {
			continue
		}
		lines = append(lines, invoicing.Line{Description: it.Name, Quantity: 1, UnitKobo: it.AmountKobo, PassThroughFor: it.PassThroughFor})
	}
	own, _ := invoicing.PassThrough(lines)
	left := own
	for _, d := range ds {
		off := min(d.Of(own), left)
		if off <= 0 {
			continue
		}
		left -= off
		lines = append(lines, invoicing.Line{Description: d.Description(), Quantity: 1, UnitKobo: -off, Kind: invoicing.LineDiscount, Reason: d.Reason})
	}
	return lines
}

// --- the discount threshold ---

// ErrNeedsApproval is a discount at or above the school's discount
// threshold, or a cash posting at or above its cash threshold: it needs a
// second person (docs/spec/schools.md 8). The request is recorded and
// returned as an ApprovalNeeded (cash.go), which unwraps to this.
var ErrNeedsApproval = errors.New("schools: needs approval")

// DiscountThreshold is the school's discount threshold in kobo; 0 when
// none is set (no discount needs a second person).
func DiscountThreshold(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int64, error) {
	var k *int64
	err := tx.QueryRow(ctx, `SELECT discount_threshold_kobo FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&k)
	if errors.Is(err, pgx.ErrNoRows) || k == nil {
		return 0, nil
	}
	return *k, err
}

// SetDiscountThreshold sets it (the owner's setting); 0 turns it off. It
// reports the threshold before.
func SetDiscountThreshold(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kobo int64, by string) (int64, error) {
	if kobo < 0 || kobo > invoicing.MaxTotalKobo {
		return 0, invalid("the threshold is an amount in naira, 0 for none")
	}
	if err := need(ctx, tx, tenant); err != nil {
		return 0, err
	}
	was, err := DiscountThreshold(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}
	var v *int64
	if kobo > 0 {
		v = &kobo
	}
	_, err = tx.Exec(ctx, `UPDATE school_settings SET discount_threshold_kobo = $2, updated_by = $3, updated_at = now() WHERE tenant_id = $1`, tenant, v, by)
	return was, err
}

// CheckDiscount says whether a discount worth kobo for a student, with
// the discounts and reductions the student was given without a second
// person within the window (ReducedUnapproved), is at or above the
// school's threshold (ErrNeedsApproval): callers then record it as a
// request for a second person rather than give it. Standing discounts
// split under the threshold add up.
func (s *Schools) CheckDiscount(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, kobo int64) error {
	limit, err := DiscountThreshold(ctx, tx, tenant)
	if err != nil || limit <= 0 {
		return err
	}
	if kobo >= limit {
		return fmt.Errorf("%w: a discount of %s is at or above the school's discount threshold of %s and needs a second person's approval",
			ErrNeedsApproval, invoicing.Naira(kobo), invoicing.Naira(limit))
	}
	prior, err := ReducedUnapproved(ctx, tx, tenant, &student, nil)
	if err != nil {
		return err
	}
	if prior+kobo >= limit {
		return fmt.Errorf("%w: a discount of %s, with %s given the student already without a second person, reaches the school's discount threshold of %s and needs a second person's approval",
			ErrNeedsApproval, invoicing.Naira(kobo), invoicing.Naira(prior), invoicing.Naira(limit))
	}
	return nil
}
