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
