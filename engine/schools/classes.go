package schools

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Class is a class of a school: JSS 1 A is the class JSS 1, arm A.
// Order places it among the others: promotion moves a student to the
// class of the next order up, of the same arm where there is one.
type Class struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Arm       string    `json:"arm"`
	Order     int       `json:"order"`
	Label     string    `json:"label"`
	Students  int       `json:"students"` // active students in it now
	CreatedAt time.Time `json:"created_at"`
}

// ClassIn is a class to add or change.
type ClassIn struct {
	Name  string `json:"name"`
	Arm   string `json:"arm"`
	Order int    `json:"order"`
}

// Label is how a class is written: its name, then its arm.
func Label(name, arm string) string {
	if arm == "" {
		return name
	}
	return name + " " + arm
}

// labelKey is a class's label as a file may write it: case, spaces and
// dashes ignored ("JSS 1 A", "jss1a", "JSS1-A").
func labelKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *ClassIn) check() error {
	c.Name = strings.Join(strings.Fields(c.Name), " ")
	c.Arm = strings.Join(strings.Fields(c.Arm), " ")
	switch {
	case c.Name == "" || len(c.Name) > 60:
		return invalid("a class needs a name (up to 60 characters), such as JSS 1 or Primary 4")
	case len(c.Arm) > 20:
		return invalid("an arm has up to 20 characters, such as A or Gold")
	case c.Order < 0 || c.Order > 1000:
		return invalid("a class's order is 0 to 1000: the order classes go up in")
	}
	return nil
}

const classCols = `c.id, c.name, c.arm, c.class_order, c.created_at,
	(SELECT count(*) FROM school_students x WHERE x.class_id = c.id AND x.status = 'active')`

func scanClass(r pgx.Row) (Class, error) {
	var c Class
	err := r.Scan(&c.ID, &c.Name, &c.Arm, &c.Order, &c.CreatedAt, &c.Students)
	c.Label = Label(c.Name, c.Arm)
	return c, err
}

// Classes lists the school's classes in their order.
func (s *Schools) Classes(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Class, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	return classes(ctx, tx, tenant)
}

func classes(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Class, error) {
	rows, err := tx.Query(ctx, `SELECT `+classCols+` FROM school_classes c WHERE c.tenant_id = $1 AND c.deleted_at IS NULL
		ORDER BY c.class_order, lower(c.name), lower(c.arm)`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Class, error) { return scanClass(r) })
	if out == nil {
		out = []Class{}
	}
	return out, err
}

// Class reads one class.
func (s *Schools) Class(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Class, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Class{}, err
	}
	c, err := scanClass(tx.QueryRow(ctx, `SELECT `+classCols+` FROM school_classes c WHERE c.tenant_id = $1 AND c.id = $2 AND c.deleted_at IS NULL`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Class{}, ErrNotFound
	}
	return c, err
}

func classTaken(err error, in ClassIn) error {
	if code, constraint := pgCode(err); code == "23505" && constraint == "school_classes_name" {
		return refused("the school has a class %s already", Label(in.Name, in.Arm))
	}
	return err
}

// AddClass adds a class.
func (s *Schools) AddClass(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in ClassIn, by string) (Class, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Class{}, err
	}
	if err := in.check(); err != nil {
		return Class{}, err
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_classes (id, tenant_id, name, arm, class_order, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, tenant, in.Name, in.Arm, in.Order, by); err != nil {
		return Class{}, classTaken(err, in)
	}
	return s.Class(ctx, tx, tenant, id)
}

// ChangeClass renames a class or moves it in the order. Its students and
// enrollments stay its own.
func (s *Schools) ChangeClass(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, in ClassIn) (Class, error) {
	if err := in.check(); err != nil {
		return Class{}, err
	}
	if _, err := s.Class(ctx, tx, tenant, id); err != nil {
		return Class{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE school_classes SET name = $3, arm = $4, class_order = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, in.Name, in.Arm, in.Order); err != nil {
		return Class{}, classTaken(err, in)
	}
	return s.Class(ctx, tx, tenant, id)
}

// RemoveClass removes a class no student was ever in (a mistake); a class
// with history stays.
func (s *Schools) RemoveClass(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Class, error) {
	c, err := s.Class(ctx, tx, tenant, id)
	if err != nil {
		return Class{}, err
	}
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_students WHERE class_id = $1) OR EXISTS (SELECT 1 FROM school_enrollments WHERE class_id = $1)`, id).Scan(&used); err != nil {
		return Class{}, err
	}
	if used {
		return Class{}, refused("%s has had students: a class with history is kept", c.Label)
	}
	_, err = tx.Exec(ctx, `UPDATE school_classes SET deleted_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id)
	return c, err
}
