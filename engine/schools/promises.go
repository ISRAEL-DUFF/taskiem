package schools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/notices"
)

// Promises to pay (docs/spec/schools.md 11.2; docs/schools.md#promises):
// the bursar records a date and a note ("salary comes on the 25th").
// Reminders skip the family until the date; the check that runs after it
// finds the promise kept when the balance moved down, missed when it did
// not, each exactly once (the row leaves open once), and the student is
// back on Debtors flagged. A promise can be withdrawn; none is deleted.

// Promise states.
const (
	PromiseOpen      = "open"
	PromiseKept      = "kept"
	PromiseMissed    = "missed"
	PromiseWithdrawn = "withdrawn"
)

// Promise is a promise to pay as the pages show it.
type Promise struct {
	ID              uuid.UUID  `json:"id"`
	StudentID       uuid.UUID  `json:"student_id"`
	AdmissionNumber string     `json:"admission_number"`
	FamilyID        *uuid.UUID `json:"family_id,omitempty"`
	PromisedOn      string     `json:"promised_on"`
	Note            string     `json:"note,omitempty"`
	BalanceKobo     int64      `json:"balance_kobo"`
	Status          string     `json:"status"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy      string     `json:"resolved_by,omitempty"`
}

// PromiseIn is a promise to record.
type PromiseIn struct {
	Date string `json:"date"`
	Note string `json:"note,omitempty"`
}

// RecordPromise records a student's promise to pay by a date (today to a
// year on): one open promise per student. The note is sealed under the
// family's subject, as the family's own words are.
func (s *Schools) RecordPromise(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, in PromiseIn, by string) (Promise, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Promise{}, err
	}
	d, ok := dateOnly(in.Date)
	today := s.today()
	if !ok || d.Before(today) || d.After(today.AddDate(1, 0, 0)) {
		return Promise{}, invalid("the date promised is from today to a year on, YYYY-MM-DD")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 300 {
		return Promise{}, invalid("a note has up to 300 characters")
	}
	st, err := s.student(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Promise{}, err
	}
	balance, err := studentBalance(ctx, tx, tenant, st.ID)
	if err != nil {
		return Promise{}, err
	}
	if balance <= 0 {
		return Promise{}, refused("%s has nothing due", st.AdmissionNumber)
	}
	subject := ""
	if st.FamilyID != nil {
		if subject, err = s.familySubject(ctx, tx, tenant, *st.FamilyID, st.ID); err != nil {
			return Promise{}, err
		}
	} else if err := tx.QueryRow(ctx, `SELECT subject_id FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, st.ID).Scan(&subject); err != nil {
		return Promise{}, err
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", in.Note)
	if err != nil {
		return Promise{}, err
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_promises (id, tenant_id, student_id, family_id, promised_on, note, balance_kobo, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenant, st.ID, st.FamilyID, d.Format(time.DateOnly), sealed, balance, by); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return Promise{}, refused("%s has a promise open already: withdraw it first", st.AdmissionNumber)
		}
		return Promise{}, err
	}
	return s.Promise(ctx, tx, tenant, id)
}

const promiseCols = `p.id, p.student_id, s.admission_number, p.family_id, p.promised_on::text, p.note, p.balance_kobo, p.status, p.created_by, p.created_at, p.resolved_at, COALESCE(p.resolved_by, '')
	FROM school_promises p JOIN school_students s ON s.id = p.student_id`

func (s *Schools) scanPromises(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Promise, error) {
	type row struct {
		p    Promise
		note map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.p.ID, &x.p.StudentID, &x.p.AdmissionNumber, &x.p.FamilyID, &x.p.PromisedOn, &x.note, &x.p.BalanceKobo, &x.p.Status, &x.p.CreatedBy, &x.p.CreatedAt, &x.p.ResolvedAt, &x.p.ResolvedBy)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Promise, 0, len(list))
	for _, x := range list {
		p := x.p
		var note string
		if _, err := s.open(ctx, tx, tenant, x.note, &note); err != nil {
			return nil, err
		}
		p.Note = note
		out = append(out, p)
	}
	return out, nil
}

// Promise reads one promise.
func (s *Schools) Promise(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Promise, error) {
	rows, err := tx.Query(ctx, `SELECT `+promiseCols+` WHERE p.tenant_id = $1 AND p.id = $2`, tenant, id)
	if err != nil {
		return Promise{}, err
	}
	list, err := s.scanPromises(ctx, tx, tenant, rows)
	if err != nil {
		return Promise{}, err
	}
	if len(list) == 0 {
		return Promise{}, ErrNotFound
	}
	return list[0], nil
}

// PromiseFilter narrows the promises listed.
type PromiseFilter struct {
	Student *uuid.UUID
	// Status: open, kept, missed, withdrawn, or "" for every status.
	Status string
	// Latest keeps only each student's latest promise.
	Latest bool
	Limit  int
}

// Promises lists promises, newest first.
func (s *Schools) Promises(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PromiseFilter) ([]Promise, error) {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	q := `SELECT ` + promiseCols + ` WHERE p.tenant_id = $1 AND ($2::uuid IS NULL OR p.student_id = $2) AND ($3 = '' OR p.status = $3) ORDER BY p.created_at DESC LIMIT $4`
	if f.Latest {
		q = `SELECT * FROM (SELECT DISTINCT ON (p.student_id) ` + promiseCols + ` WHERE p.tenant_id = $1 AND ($2::uuid IS NULL OR p.student_id = $2) AND ($3 = '' OR p.status = $3)
			ORDER BY p.student_id, p.created_at DESC) x LIMIT $4`
	}
	rows, err := tx.Query(ctx, q, tenant, f.Student, f.Status, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanPromises(ctx, tx, tenant, rows)
	if list == nil {
		list = []Promise{}
	}
	return list, err
}

// WithdrawPromise withdraws an open promise.
func (s *Schools) WithdrawPromise(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, by string) (Promise, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Promise{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE school_promises SET status = 'withdrawn', resolved_at = now(), resolved_by = $3 WHERE tenant_id = $1 AND id = $2 AND status = 'open'`, tenant, id, by)
	if err != nil {
		return Promise{}, err
	}
	if tag.RowsAffected() == 0 {
		p, err := s.Promise(ctx, tx, tenant, id)
		if err != nil {
			return Promise{}, err
		}
		return Promise{}, refused("the promise is %s already", p.Status)
	}
	return s.Promise(ctx, tx, tenant, id)
}

// checkPromises decides each open promise past its date, once: kept when
// the balance moved down since it was made (or is nothing), missed
// otherwise. The bursars are told of the missed ones.
func (s *Schools) checkPromises(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (kept, missed int, err error) {
	rows, err := tx.Query(ctx, `SELECT id, student_id, balance_kobo FROM school_promises WHERE tenant_id = $1 AND status = 'open' AND promised_on < $2::date ORDER BY promised_on, created_at FOR UPDATE`, tenant, s.today().Format(time.DateOnly))
	if err != nil {
		return 0, 0, err
	}
	type due struct {
		id, student uuid.UUID
		balance     int64
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.student, &d.balance)
	})
	if err != nil {
		return 0, 0, err
	}
	var lines []string
	for _, d := range list {
		now, err := studentBalance(ctx, tx, tenant, d.student)
		if err != nil {
			return kept, missed, err
		}
		status := PromiseMissed
		if now <= 0 || now < d.balance {
			status = PromiseKept
		}
		tag, err := tx.Exec(ctx, `UPDATE school_promises SET status = $3, resolved_at = now(), resolved_by = 'system:chasing' WHERE tenant_id = $1 AND id = $2 AND status = 'open'`, tenant, d.id, status)
		if err != nil {
			return kept, missed, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if status == PromiseKept {
			kept++
			continue
		}
		missed++
		var number string
		if err := tx.QueryRow(ctx, `SELECT admission_number FROM school_students WHERE id = $1`, d.student).Scan(&number); err != nil {
			return kept, missed, err
		}
		lines = append(lines, fmt.Sprintf("%s: %s still due", number, invoicing.Naira(now)))
	}
	if missed > 0 {
		n := notices.Notice{Kind: "school_promise_missed", Level: notices.Warning, Title: "A promise to pay was missed", Many: "{n} promises to pay were missed",
			Body: strings.Join(lines, "\n"), Link: "/schools/debtors?has=missed", Group: "school_promise_missed"}
		if missed > 1 {
			n.Title = fmt.Sprintf("%d promises to pay were missed", missed)
		}
		if err := tellBursars(ctx, tx, tenant, n); err != nil {
			return kept, missed, err
		}
	}
	return kept, missed, nil
}
