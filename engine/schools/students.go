package schools

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Student statuses.
const (
	Active    = "active"
	Withdrawn = "withdrawn"
	Graduated = "graduated"
)

// Statuses are a student's possible statuses.
var Statuses = []string{Active, Withdrawn, Graduated}

// Student is a student of the school, their details opened.
type Student struct {
	ID              uuid.UUID  `json:"id"`
	AdmissionNumber string     `json:"admission_number"`
	Name            string     `json:"name"`
	DateOfBirth     string     `json:"date_of_birth,omitempty"`
	ClassID         uuid.UUID  `json:"class_id"`
	Class           string     `json:"class"`
	Status          string     `json:"status"`
	FamilyID        *uuid.UUID `json:"family_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// StudentDetail is a student with their family and class history.
type StudentDetail struct {
	Student
	Family      *Family      `json:"family"`
	Enrollments []Enrollment `json:"enrollments"`
}

type studentSealed struct {
	Name        string `json:"name"`
	DateOfBirth string `json:"date_of_birth,omitempty"`
}

// StudentIn is a student to add: their class, and their family (an
// existing one, or a new one with these guardians), or none yet.
type StudentIn struct {
	Name            string       `json:"name"`
	AdmissionNumber string       `json:"admission_number"`
	ClassID         uuid.UUID    `json:"class_id"`
	DateOfBirth     string       `json:"date_of_birth,omitempty"`
	FamilyID        *uuid.UUID   `json:"family_id,omitempty"`
	Guardians       []GuardianIn `json:"guardians,omitempty"`
}

// StudentChange changes a student: only what is given.
type StudentChange struct {
	Name            *string    `json:"name,omitempty"`
	AdmissionNumber *string    `json:"admission_number,omitempty"`
	ClassID         *uuid.UUID `json:"class_id,omitempty"`
	DateOfBirth     *string    `json:"date_of_birth,omitempty"`
	Status          *string    `json:"status,omitempty"`
	FamilyID        *uuid.UUID `json:"family_id,omitempty"`
}

var admissionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/_.-]{0,39}$`)

func checkName(n string) (string, error) {
	n = strings.Join(strings.Fields(n), " ")
	if n == "" || len(n) > 120 {
		return "", invalid("a student needs a name (up to 120 characters)")
	}
	return n, nil
}

func checkAdmission(a string) (string, error) {
	a = strings.TrimSpace(a)
	if !admissionRe.MatchString(a) {
		return "", invalid("an admission number has up to 40 letters, digits and '/', '_', '.' or '-', starting with a letter or digit")
	}
	return a, nil
}

func checkBirth(d string) (string, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return "", nil
	}
	t, ok := dateOnly(d)
	if !ok {
		if t2, err := time.Parse("02/01/2006", d); err == nil {
			t, ok = t2, true
		}
	}
	if !ok || t.Year() < 1900 || t.After(time.Now()) {
		return "", invalid("a date of birth is a past date, YYYY-MM-DD")
	}
	return t.Format(time.DateOnly), nil
}

const studentCols = `s.id, s.admission_number, s.details, s.class_id, c.name, c.arm, s.status, s.family_id, s.created_at, s.updated_at`
const studentFrom = ` FROM school_students s JOIN school_classes c ON c.id = s.class_id`

func (s *Schools) scanStudents(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Student, error) {
	type row struct {
		st  Student
		env map[string]any
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var name, arm string
		err := r.Scan(&x.st.ID, &x.st.AdmissionNumber, &x.env, &x.st.ClassID, &name, &arm, &x.st.Status, &x.st.FamilyID, &x.st.CreatedAt, &x.st.UpdatedAt)
		x.st.Class = Label(name, arm)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Student, 0, len(got))
	for _, x := range got {
		var d studentSealed
		erased, err := s.open(ctx, tx, tenant, x.env, &d)
		if err != nil {
			return nil, err
		}
		if erased {
			d = studentSealed{Name: Erased}
		}
		x.st.Name, x.st.DateOfBirth = d.Name, d.DateOfBirth
		out = append(out, x.st)
	}
	return out, nil
}

func (s *Schools) listStudents(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where string, args ...any) ([]Student, error) {
	//sqlcheck:built studentCols and studentFrom are consts; where is a literal, or one of two, at every caller
	rows, err := tx.Query(ctx, `SELECT `+studentCols+studentFrom+` WHERE s.tenant_id = $1 AND `+where+` ORDER BY upper(s.admission_number)`, append([]any{tenant}, args...)...)
	if err != nil {
		return nil, err
	}
	return s.scanStudents(ctx, tx, tenant, rows)
}

// Filter narrows the students listed.
type Filter struct {
	ClassID *uuid.UUID
	Status  string
	// Query matches the admission number, and the name when Names is
	// set (a caller that may see names).
	Query string
	Names bool
	Limit int
	// WithoutAccount keeps the students with no account number issued on
	// the school's issuing connection (for the template that issues
	// them); every student when no issuing connection is set.
	WithoutAccount bool
}

// MaxListed is the most students one list returns.
const MaxListed = 1000

// Students lists the school's students by admission number, and how many
// match in all.
func (s *Schools) Students(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter) ([]Student, int, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, 0, err
	}
	if f.Status != "" && !slices.Contains(Statuses, f.Status) {
		return nil, 0, invalid("status is active, withdrawn or graduated")
	}
	if f.Limit <= 0 || f.Limit > MaxListed {
		f.Limit = 200
	}
	list, err := s.listStudents(ctx, tx, tenant, `($2::uuid IS NULL OR s.class_id = $2) AND ($3 = '' OR s.status = $3)`, f.ClassID, f.Status)
	if err != nil {
		return nil, 0, err
	}
	issued := map[uuid.UUID]bool{}
	if f.WithoutAccount {
		st, err := GetIssuing(ctx, tx, tenant)
		if err != nil {
			return nil, 0, err
		}
		rows, err := tx.Query(ctx, `SELECT student_id FROM school_student_accounts WHERE tenant_id = $1 AND channel = $2 AND status = 'issued'`, tenant, st.Channel)
		if err != nil {
			return nil, 0, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, 0, err
		}
		for _, id := range ids {
			issued[id] = true
		}
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := make([]Student, 0, min(len(list), f.Limit))
	total := 0
	for _, st := range list {
		if q != "" && !strings.Contains(strings.ToLower(st.AdmissionNumber), q) && (!f.Names || !strings.Contains(strings.ToLower(st.Name), q)) {
			continue
		}
		if issued[st.ID] {
			continue
		}
		total++
		if len(out) < f.Limit {
			out = append(out, st)
		}
	}
	return out, total, nil
}

// Student reads one student by id or admission number, with their family
// and class history.
func (s *Schools) Student(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) (StudentDetail, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return StudentDetail{}, err
	}
	st, err := s.student(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return StudentDetail{}, err
	}
	d := StudentDetail{Student: st}
	if st.FamilyID != nil {
		f, err := s.Family(ctx, tx, tenant, *st.FamilyID)
		if err != nil {
			return StudentDetail{}, err
		}
		d.Family = &f
	}
	d.Enrollments, err = studentEnrollments(ctx, tx, tenant, st.ID)
	return d, err
}

func (s *Schools) student(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) (Student, error) {
	where, arg := `upper(s.admission_number) = upper($2)`, any(strings.TrimSpace(idOrNumber))
	if id, err := uuid.Parse(idOrNumber); err == nil {
		where, arg = `s.id = $2`, id
	}
	list, err := s.listStudents(ctx, tx, tenant, where, arg)
	if err != nil {
		return Student{}, err
	}
	if len(list) == 0 {
		return Student{}, ErrNotFound
	}
	return list[0], nil
}

// sealStudent seals a student's details under their own subject.
func (s *Schools) sealStudent(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, d studentSealed) (map[string]any, string, error) {
	env, err := s.Vault.SealTx(ctx, tx, tenant, "student", id.String())
	if err != nil {
		return nil, "", err
	}
	subject, _ := env["subject"].(string)
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "student", d)
	return sealed, subject, err
}

func (s *Schools) liveClass(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_classes WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL)`, tenant, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid("no such class: add it under Classes first")
	}
	return nil
}

func (s *Schools) liveFamily(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_families WHERE tenant_id = $1 AND id = $2)`, tenant, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid("no such family")
	}
	return nil
}

func admissionTaken(err error, n string) error {
	if code, constraint := pgCode(err); code == "23505" && constraint == "school_students_admission" {
		return refused("a student has the admission number %s already", n)
	}
	return err
}

// AddStudent adds one student, enrolled in the current term when one
// runs.
func (s *Schools) AddStudent(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in StudentIn, by string) (StudentDetail, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return StudentDetail{}, err
	}
	var err error
	if in.Name, err = checkName(in.Name); err != nil {
		return StudentDetail{}, err
	}
	if in.AdmissionNumber, err = checkAdmission(in.AdmissionNumber); err != nil {
		return StudentDetail{}, err
	}
	if in.DateOfBirth, err = checkBirth(in.DateOfBirth); err != nil {
		return StudentDetail{}, err
	}
	if err := s.liveClass(ctx, tx, tenant, in.ClassID); err != nil {
		return StudentDetail{}, err
	}
	if in.FamilyID != nil && len(in.Guardians) > 0 {
		return StudentDetail{}, invalid("give a family or new guardians, not both")
	}
	family := in.FamilyID
	if family != nil {
		if err := s.liveFamily(ctx, tx, tenant, *family); err != nil {
			return StudentDetail{}, err
		}
	} else if len(in.Guardians) > 0 {
		f, err := s.AddFamily(ctx, tx, tenant, in.Guardians, by)
		if err != nil {
			return StudentDetail{}, err
		}
		family = &f.ID
	}
	id := newID()
	sealed, subject, err := s.sealStudent(ctx, tx, tenant, id, studentSealed{Name: in.Name, DateOfBirth: in.DateOfBirth})
	if err != nil {
		return StudentDetail{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO school_students (id, tenant_id, admission_number, subject_id, details, class_id, family_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, tenant, in.AdmissionNumber, subject, sealed, in.ClassID, family, by); err != nil {
		return StudentDetail{}, admissionTaken(err, in.AdmissionNumber)
	}
	if _, err := enrollOne(ctx, tx, tenant, id, in.ClassID, by); err != nil {
		return StudentDetail{}, err
	}
	return s.Student(ctx, tx, tenant, id.String())
}

// Changed is what a change did, for the audit: the fields that changed,
// with their values before and after (no personal data: a name is
// "changed").
type Changed map[string]any

// ChangeStudent changes a student. Their class changing while a term
// runs moves their enrollment for it; becoming active again enrols them.
func (s *Schools) ChangeStudent(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, c StudentChange, by string) (StudentDetail, Changed, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return StudentDetail{}, nil, err
	}
	st, err := s.student(ctx, tx, tenant, id.String())
	if err != nil {
		return StudentDetail{}, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM school_students WHERE id = $1 FOR UPDATE`, id); err != nil {
		return StudentDetail{}, nil, err
	}
	changed := Changed{}
	details := studentSealed{Name: st.Name, DateOfBirth: st.DateOfBirth}
	reseal := false
	if c.Name != nil {
		n, err := checkName(*c.Name)
		if err != nil {
			return StudentDetail{}, nil, err
		}
		if n != st.Name {
			details.Name, reseal, changed["name"] = n, true, "changed"
		}
	}
	if c.DateOfBirth != nil {
		d, err := checkBirth(*c.DateOfBirth)
		if err != nil {
			return StudentDetail{}, nil, err
		}
		if d != st.DateOfBirth {
			details.DateOfBirth, reseal, changed["date_of_birth"] = d, true, "changed"
		}
	}
	admission, class, status, family := st.AdmissionNumber, st.ClassID, st.Status, st.FamilyID
	if c.AdmissionNumber != nil {
		a, err := checkAdmission(*c.AdmissionNumber)
		if err != nil {
			return StudentDetail{}, nil, err
		}
		if a != admission {
			changed["admission_number"] = map[string]any{"from": admission, "to": a}
			admission = a
		}
	}
	if c.ClassID != nil && *c.ClassID != class {
		if err := s.liveClass(ctx, tx, tenant, *c.ClassID); err != nil {
			return StudentDetail{}, nil, err
		}
		changed["class_id"] = map[string]any{"from": class, "to": *c.ClassID}
		class = *c.ClassID
	}
	if c.Status != nil && *c.Status != status {
		if !slices.Contains(Statuses, *c.Status) {
			return StudentDetail{}, nil, invalid("status is active, withdrawn or graduated")
		}
		changed["status"] = map[string]any{"from": status, "to": *c.Status}
		status = *c.Status
	}
	if c.FamilyID != nil && (family == nil || *family != *c.FamilyID) {
		if err := s.liveFamily(ctx, tx, tenant, *c.FamilyID); err != nil {
			return StudentDetail{}, nil, err
		}
		changed["family_id"] = map[string]any{"from": family, "to": *c.FamilyID}
		family = c.FamilyID
	}
	if len(changed) == 0 {
		d, err := s.Student(ctx, tx, tenant, id.String())
		return d, changed, err
	}
	var sealed any // NULL keeps the details as they are
	if reseal {
		env, _, err := s.sealStudent(ctx, tx, tenant, id, details)
		if err != nil {
			return StudentDetail{}, nil, err
		}
		sealed = env
	}
	if _, err := tx.Exec(ctx, `UPDATE school_students SET admission_number = $3, details = COALESCE($4, details), class_id = $5, status = $6, family_id = $7, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, admission, sealed, class, status, family); err != nil {
		return StudentDetail{}, nil, admissionTaken(err, admission)
	}
	if status == Active && (changed["class_id"] != nil || changed["status"] != nil) {
		moved, err := enrollOne(ctx, tx, tenant, id, class, by)
		if err != nil {
			return StudentDetail{}, nil, err
		}
		if moved {
			changed["enrollment"] = "current term"
		}
	}
	d, err := s.Student(ctx, tx, tenant, id.String())
	return d, changed, err
}
