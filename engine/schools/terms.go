package schools

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Session is a school year (2026/2027) and its terms.
type Session struct {
	ID         uuid.UUID  `json:"id"`
	Label      string     `json:"label"`
	Terms      []Term     `json:"terms"`
	PromotedAt *time.Time `json:"promoted_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Term states: planned (not started), current (the one term running),
// past (started before the current one; read-only).
const (
	TermPlanned = "planned"
	TermCurrent = "current"
	TermPast    = "past"
)

// Term is one part of a session.
type Term struct {
	ID        uuid.UUID  `json:"id"`
	SessionID uuid.UUID  `json:"session_id"`
	Session   string     `json:"session"`
	Name      string     `json:"name"`
	StartsOn  string     `json:"starts_on"`
	EndsOn    string     `json:"ends_on"`
	FeesDueOn string     `json:"fees_due_on"`
	State     string     `json:"state"`
	StartedAt *time.Time `json:"started_at"`
	Enrolled  int        `json:"enrolled"`
}

// TermIn is a term to add or change.
type TermIn struct {
	Name      string `json:"name"`
	StartsOn  string `json:"starts_on"`
	EndsOn    string `json:"ends_on"`
	FeesDueOn string `json:"fees_due_on"`
}

var sessionRe = regexp.MustCompile(`^([0-9]{4})/([0-9]{4})$`)

func (t *TermIn) check() error {
	t.Name = strings.Join(strings.Fields(t.Name), " ")
	if t.Name == "" || len(t.Name) > 40 {
		return invalid("a term needs a name (up to 40 characters), such as First term")
	}
	start, ok1 := dateOnly(t.StartsOn)
	end, ok2 := dateOnly(t.EndsOn)
	due, ok3 := dateOnly(t.FeesDueOn)
	if !ok1 || !ok2 || !ok3 {
		return invalid("starts_on, ends_on and fees_due_on are dates, YYYY-MM-DD")
	}
	if !start.Before(end) {
		return invalid("a term ends after it starts")
	}
	if due.After(end) {
		return invalid("fees are due by the end of the term")
	}
	t.StartsOn, t.EndsOn, t.FeesDueOn = start.Format(time.DateOnly), end.Format(time.DateOnly), due.Format(time.DateOnly)
	return nil
}

const termCols = `t.id, t.session_id, ss.label, t.name, to_char(t.starts_on, 'YYYY-MM-DD'), to_char(t.ends_on, 'YYYY-MM-DD'), to_char(t.fees_due_on, 'YYYY-MM-DD'),
	t.started_at, t.current, (SELECT count(*) FROM school_enrollments e WHERE e.term_id = t.id)`

func scanTerm(r pgx.Row) (Term, error) {
	var t Term
	var current bool
	err := r.Scan(&t.ID, &t.SessionID, &t.Session, &t.Name, &t.StartsOn, &t.EndsOn, &t.FeesDueOn, &t.StartedAt, &current, &t.Enrolled)
	switch {
	case current:
		t.State = TermCurrent
	case t.StartedAt != nil:
		t.State = TermPast
	default:
		t.State = TermPlanned
	}
	return t, err
}

// Sessions lists the school's sessions, oldest first, each with its terms
// in date order.
func (s *Schools) Sessions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Session, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT ss.id, ss.label, ss.created_at, p.created_at FROM school_sessions ss
		LEFT JOIN school_promotions p ON p.session_id = ss.id WHERE ss.tenant_id = $1 ORDER BY ss.label`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Session, error) {
		var x Session
		err := r.Scan(&x.ID, &x.Label, &x.CreatedAt, &x.PromotedAt)
		x.Terms = []Term{}
		return x, err
	})
	if err != nil {
		return nil, err
	}
	terms, err := s.terms(ctx, tx, tenant, `TRUE`)
	if err != nil {
		return nil, err
	}
	for _, t := range terms {
		for i := range out {
			if out[i].ID == t.SessionID {
				out[i].Terms = append(out[i].Terms, t)
			}
		}
	}
	if out == nil {
		out = []Session{}
	}
	return out, nil
}

func (s *Schools) terms(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where string, args ...any) ([]Term, error) {
	rows, err := tx.Query(ctx, `SELECT `+termCols+` FROM school_terms t JOIN school_sessions ss ON ss.id = t.session_id
		WHERE t.tenant_id = $1 AND `+where+` ORDER BY t.starts_on, t.name`, append([]any{tenant}, args...)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Term, error) { return scanTerm(r) })
}

// AddSession adds a school year, written 2026/2027.
func (s *Schools) AddSession(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, label, by string) (Session, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Session{}, err
	}
	label = strings.TrimSpace(label)
	m := sessionRe.FindStringSubmatch(label)
	if m == nil {
		return Session{}, invalid("a session is written as two years, such as 2026/2027")
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	if b != a+1 {
		return Session{}, invalid("a session's second year follows its first, such as 2026/2027")
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_sessions (id, tenant_id, label, created_by) VALUES ($1, $2, $3, $4)`, id, tenant, label, by); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return Session{}, refused("the school has the session %s already", label)
		}
		return Session{}, err
	}
	return Session{ID: id, Label: label, Terms: []Term{}, CreatedAt: s.now()}, nil
}

// Term reads one term.
func (s *Schools) Term(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Term, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Term{}, err
	}
	return term(ctx, tx, tenant, id)
}

func term(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Term, error) {
	t, err := scanTerm(tx.QueryRow(ctx, `SELECT `+termCols+` FROM school_terms t JOIN school_sessions ss ON ss.id = t.session_id WHERE t.tenant_id = $1 AND t.id = $2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Term{}, ErrNotFound
	}
	return t, err
}

// CurrentTerm is the school's current term; ok false when no term runs.
func (s *Schools) CurrentTerm(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Term, bool, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Term{}, false, err
	}
	return currentTerm(ctx, tx, tenant)
}

func currentTerm(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Term, bool, error) {
	t, err := scanTerm(tx.QueryRow(ctx, `SELECT `+termCols+` FROM school_terms t JOIN school_sessions ss ON ss.id = t.session_id WHERE t.tenant_id = $1 AND t.current`, tenant))
	if errors.Is(err, pgx.ErrNoRows) {
		return Term{}, false, nil
	}
	return t, err == nil, err
}

func termTaken(err error, name string) error {
	if code, constraint := pgCode(err); code == "23505" && constraint == "school_terms_name" {
		return refused("the session has a term named %s already", name)
	}
	return checked(err)
}

// AddTerm adds a term to a session.
func (s *Schools) AddTerm(ctx context.Context, tx pgx.Tx, tenant, session uuid.UUID, in TermIn, by string) (Term, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Term{}, err
	}
	if err := in.check(); err != nil {
		return Term{}, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_sessions WHERE tenant_id = $1 AND id = $2)`, tenant, session).Scan(&ok); err != nil {
		return Term{}, err
	}
	if !ok {
		return Term{}, ErrNotFound
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_terms (id, tenant_id, session_id, name, starts_on, ends_on, fees_due_on, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenant, session, in.Name, in.StartsOn, in.EndsOn, in.FeesDueOn, by); err != nil {
		return Term{}, termTaken(err, in.Name)
	}
	return term(ctx, tx, tenant, id)
}

// ChangeTerm changes a term's name or dates. A past term is read-only.
func (s *Schools) ChangeTerm(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, in TermIn) (Term, Term, error) {
	before, err := s.Term(ctx, tx, tenant, id)
	if err != nil {
		return Term{}, Term{}, err
	}
	if err := in.check(); err != nil {
		return Term{}, Term{}, err
	}
	if before.State == TermPast {
		return Term{}, Term{}, refused("%s %s is past and read-only", before.Name, before.Session)
	}
	if _, err := tx.Exec(ctx, `UPDATE school_terms SET name = $3, starts_on = $4, ends_on = $5, fees_due_on = $6, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, in.Name, in.StartsOn, in.EndsOn, in.FeesDueOn); err != nil {
		return Term{}, Term{}, termTaken(err, in.Name)
	}
	after, err := term(ctx, tx, tenant, id)
	return before, after, err
}

// RemoveTerm removes a term that has not started.
func (s *Schools) RemoveTerm(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Term, error) {
	t, err := s.Term(ctx, tx, tenant, id)
	if err != nil {
		return Term{}, err
	}
	if t.State != TermPlanned {
		return Term{}, refused("%s %s has started: a started term is kept", t.Name, t.Session)
	}
	_, err = tx.Exec(ctx, `DELETE FROM school_terms WHERE tenant_id = $1 AND id = $2`, tenant, id)
	return t, checked(err)
}

// Started is what starting a term did.
type Started struct {
	Term Term `json:"term"`
	// Previous is the term that was current before, now past.
	Previous *Term `json:"previous,omitempty"`
	// Enrolled is how many students this call enrolled: every active
	// student in their class the first time, then only those enrolled in
	// no class for the term yet.
	Enrolled int `json:"enrolled"`
}

// StartTerm makes a term the current one, the one before it past, and
// enrols every active student in their class for it. Each student is
// enrolled in a term once: starting it again enrols only the students
// added since without one (normally none, as a student added while a
// term runs is enrolled at once). A past term, or one that starts before
// the current one, is not started.
func (s *Schools) StartTerm(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, by string) (Started, error) {
	if err := lock(ctx, tx, tenant); err != nil {
		return Started{}, err
	}
	t, err := term(ctx, tx, tenant, id)
	if err != nil {
		return Started{}, err
	}
	out := Started{}
	switch t.State {
	case TermPast:
		return Started{}, refused("%s %s is past: past terms are read-only", t.Name, t.Session)
	case TermPlanned:
		cur, ok, err := currentTerm(ctx, tx, tenant)
		if err != nil {
			return Started{}, err
		}
		if ok {
			if t.StartsOn < cur.StartsOn {
				return Started{}, refused("%s %s starts before the current term, %s %s", t.Name, t.Session, cur.Name, cur.Session)
			}
			if _, err := tx.Exec(ctx, `UPDATE school_terms SET current = false, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, cur.ID); err != nil {
				return Started{}, checked(err)
			}
			cur.State = TermPast
			out.Previous = &cur
		}
		if _, err := tx.Exec(ctx, `UPDATE school_terms SET current = true, started_at = now(), started_by = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, id, by); err != nil {
			return Started{}, checked(err)
		}
	}
	n, err := enrollAll(ctx, tx, tenant, id, by)
	if err != nil {
		return Started{}, err
	}
	out.Enrolled = n
	out.Term, err = term(ctx, tx, tenant, id)
	return out, err
}

// enrollAll enrols each active student not yet enrolled in the term, in
// their class.
func enrollAll(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID, by string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT s.id, s.class_id FROM school_students s WHERE s.tenant_id = $1 AND s.status = 'active'
		AND NOT EXISTS (SELECT 1 FROM school_enrollments e WHERE e.term_id = $2 AND e.student_id = s.id) ORDER BY s.id`, tenant, termID)
	if err != nil {
		return 0, err
	}
	var students, classes, ids []uuid.UUID
	for rows.Next() {
		var st, cl uuid.UUID
		if err := rows.Scan(&st, &cl); err != nil {
			rows.Close()
			return 0, err
		}
		students, classes, ids = append(students, st), append(classes, cl), append(ids, newID())
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(students) == 0 {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO school_enrollments (id, tenant_id, term_id, student_id, class_id, created_by)
		SELECT x.id, $1, $2, x.student, x.class, $3 FROM unnest($4::uuid[], $5::uuid[], $6::uuid[]) AS x (id, student, class)
		ON CONFLICT (term_id, student_id) DO NOTHING`, tenant, termID, by, ids, students, classes)
	if err != nil {
		return 0, checked(err)
	}
	return int(tag.RowsAffected()), nil
}

// enrollOne enrols one student in the current term, when a term runs
// and the student is active and not yet enrolled; their enrollment moves
// with them when their class changes during the term.
func enrollOne(ctx context.Context, tx pgx.Tx, tenant, student, class uuid.UUID, by string) (bool, error) {
	cur, ok, err := currentTerm(ctx, tx, tenant)
	if err != nil || !ok {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO school_enrollments (id, tenant_id, term_id, student_id, class_id, created_by) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (term_id, student_id) DO UPDATE SET class_id = EXCLUDED.class_id, updated_at = now() WHERE school_enrollments.class_id <> EXCLUDED.class_id`,
		newID(), tenant, cur.ID, student, class, by)
	if err != nil {
		return false, checked(err)
	}
	return tag.RowsAffected() > 0, nil
}

// Enrollment is a student in a class for a term.
type Enrollment struct {
	ID              uuid.UUID `json:"id"`
	TermID          uuid.UUID `json:"term_id"`
	Term            string    `json:"term"`
	Session         string    `json:"session"`
	TermState       string    `json:"term_state"`
	StudentID       uuid.UUID `json:"student_id"`
	AdmissionNumber string    `json:"admission_number"`
	ClassID         uuid.UUID `json:"class_id"`
	Class           string    `json:"class"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const enrollmentCols = `e.id, e.term_id, t.name, ss.label, t.started_at IS NOT NULL AND NOT t.current, e.student_id, s.admission_number, e.class_id, c.name, c.arm, e.created_at, e.updated_at`

func scanEnrollment(r pgx.Row) (Enrollment, error) {
	var e Enrollment
	var past bool
	var name, arm string
	err := r.Scan(&e.ID, &e.TermID, &e.Term, &e.Session, &past, &e.StudentID, &e.AdmissionNumber, &e.ClassID, &name, &arm, &e.CreatedAt, &e.UpdatedAt)
	e.Class = Label(name, arm)
	e.TermState = TermCurrent
	if past {
		e.TermState = TermPast
	}
	return e, err
}

const enrollmentFrom = ` FROM school_enrollments e JOIN school_terms t ON t.id = e.term_id JOIN school_sessions ss ON ss.id = t.session_id
	JOIN school_students s ON s.id = e.student_id JOIN school_classes c ON c.id = e.class_id`

// Enrollments lists a term's enrollments by class and admission number.
func (s *Schools) Enrollments(ctx context.Context, tx pgx.Tx, tenant, termID uuid.UUID) ([]Enrollment, error) {
	if _, err := s.Term(ctx, tx, tenant, termID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+enrollmentCols+enrollmentFrom+` WHERE e.tenant_id = $1 AND e.term_id = $2
		ORDER BY c.class_order, lower(c.name), lower(c.arm), upper(s.admission_number)`, tenant, termID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Enrollment, error) { return scanEnrollment(r) })
	if out == nil {
		out = []Enrollment{}
	}
	return out, err
}

func studentEnrollments(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) ([]Enrollment, error) {
	rows, err := tx.Query(ctx, `SELECT `+enrollmentCols+enrollmentFrom+` WHERE e.tenant_id = $1 AND e.student_id = $2 ORDER BY t.starts_on DESC`, tenant, student)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Enrollment, error) { return scanEnrollment(r) })
	if out == nil {
		out = []Enrollment{}
	}
	return out, err
}
