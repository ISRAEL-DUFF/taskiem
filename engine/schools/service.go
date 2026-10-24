package schools

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service is the record's reads in their own transactions, for the
// schools@1 platform connector (connectors/schools).
type Service struct{ *Schools }

// FindClass is a class by id or as written (JSS 1 A, jss1a).
func (s Service) FindClass(ctx context.Context, tenant uuid.UUID, idOrLabel string) (Class, error) {
	var out Class
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		list, err := s.Classes(ctx, tx, tenant)
		if err != nil {
			return err
		}
		id, perr := uuid.Parse(idOrLabel)
		for _, c := range list {
			if (perr == nil && c.ID == id) || (perr != nil && labelKey(c.Label) == labelKey(idOrLabel)) {
				out = c
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

// ListStudents lists students, optionally of one class and status.
func (s Service) ListStudents(ctx context.Context, tenant uuid.UUID, class *uuid.UUID, status string, limit int) ([]Student, int, error) {
	var out []Student
	var total int
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, total, err = s.Students(ctx, tx, tenant, Filter{ClassID: class, Status: status, Limit: limit})
		return err
	})
	return out, total, err
}

// GetStudent reads one student by id or admission number.
func (s Service) GetStudent(ctx context.Context, tenant uuid.UUID, idOrNumber string) (StudentDetail, error) {
	var out StudentDetail
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.Student(ctx, tx, tenant, idOrNumber)
		return err
	})
	return out, err
}

// ListClasses lists the classes in their order.
func (s Service) ListClasses(ctx context.Context, tenant uuid.UUID) ([]Class, error) {
	var out []Class
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.Classes(ctx, tx, tenant)
		return err
	})
	return out, err
}

// ResolveTerm is a term by id, or the current one for "" (refused when
// none runs).
func (s Service) ResolveTerm(ctx context.Context, tenant uuid.UUID, id string) (Term, error) {
	var out Term
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if id == "" {
			t, ok, err := s.CurrentTerm(ctx, tx, tenant)
			if err == nil && !ok {
				err = refused("no term is current")
			}
			out = t
			return err
		}
		tid, err := uuid.Parse(id)
		if err != nil {
			return ErrNotFound
		}
		out, err = s.Term(ctx, tx, tenant, tid)
		return err
	})
	return out, err
}

// ListFees is a term's fee structures, optionally of one class.
func (s Service) ListFees(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID) ([]ClassFees, error) {
	out := []ClassFees{}
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		list, err := s.TermFees(ctx, tx, tenant, term)
		for _, f := range list {
			if class == nil || f.ClassID == *class {
				out = append(out, f)
			}
		}
		return err
	})
	return out, err
}

// ListTermInvoices is a term's invoices, optionally of one class and
// status.
func (s Service) ListTermInvoices(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID, status string) ([]TermInvoice, error) {
	var out []TermInvoice
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.TermInvoices(ctx, tx, tenant, term, class, status)
		return err
	})
	return out, err
}

// IssueAccount issues a student's account number on the school's issuing
// connection, once per student and connection (IssueOne).
func (s Service) IssueAccount(ctx context.Context, tenant uuid.UUID, student string, in IssueIn, by string) (StudentAccount, error) {
	return s.IssueOne(ctx, tenant, student, in, by)
}

// FamilyLinkFor mints a family page link for a family (by id) or a
// student's family (by id or admission number): the URL and when it
// expires (docs/schools.md#the-family-page).
func (s Service) FamilyLinkFor(ctx context.Context, tenant uuid.UUID, family, student string) (FamilyLinkMinted, error) {
	var out FamilyLinkMinted
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		fid, err := uuid.Parse(family)
		if family == "" || err != nil {
			st, err := s.Student(ctx, tx, tenant, student)
			if err != nil {
				return err
			}
			if st.FamilyID == nil {
				return refused("the student %s is in no family yet", st.AdmissionNumber)
			}
			fid = *st.FamilyID
		}
		out, err = s.MintFamilyLink(ctx, tx, tenant, fid, "workflow", "workflow:family_link")
		return err
	})
	return out, err
}

// Current is the current term; ok false when none runs.
func (s Service) Current(ctx context.Context, tenant uuid.UUID) (Term, bool, error) {
	var out Term
	var ok bool
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, ok, err = s.CurrentTerm(ctx, tx, tenant)
		return err
	})
	return out, ok, err
}
