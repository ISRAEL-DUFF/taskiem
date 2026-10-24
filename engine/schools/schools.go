// Package schools is the Schools area's record (decision 0046;
// docs/spec/schools.md 4; docs/schools.md): classes, sessions and terms,
// students, families and their guardians, enrollments, the import of a
// school's students from a file, and promotion at the end of a session.
//
// It is a platform record like invoices: people change it through the
// API, workflows read it through schools@1, and data tables get read-only
// kept copies (Mirror). The database keeps its rules (migration 00211):
// unique classes and admission numbers, one current term per school, past
// terms and their enrollments read-only, one enrollment per student and
// term, a guardian in one family per school, and one promotion per
// session.
//
// Students' and guardians' personal data (names, dates of birth, phones,
// emails, relationships) are sealed at rest: a student under a subject of
// their own, a guardian under their phone's subject, so erasing a person
// erases them. Callers that show them to people without pii.reveal mask
// them, as for invoices' customers.
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

var (
	// ErrNotFound is a record the caller's school does not have.
	ErrNotFound = errors.New("schools: not found")
	// ErrInvalid is a request that cannot be accepted; the message says
	// why.
	ErrInvalid = errors.New("schools: invalid")
	// ErrState refuses what the record's state does not allow (a past
	// term, a session promoted already, a guardian in another family).
	ErrState = errors.New("schools: not allowed")
	// ErrOff is the Schools area turned off for the business.
	ErrOff = errors.New("schools: the Schools area is off; an owner turns it on in Settings > Schools")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func refused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrState, fmt.Sprintf(format, a...))
}

// Plain is an error's message without the package's prefixes, for people.
func Plain(err error) string {
	msg := err.Error()
	for _, p := range []string{"schools: invalid: ", "schools: not allowed: ", "schools: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	return msg
}

// Schools runs businesses' school records.
type Schools struct {
	Pool  *pgxpool.Pool
	Vault *secrets.Vault
	Now   func() time.Time
}

func (s *Schools) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// In runs fn in a transaction scoped to tenant.
func (s *Schools) In(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}

// --- the setting ---

// Enabled reports whether the business has the Schools area on.
func Enabled(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `SELECT enabled FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetEnabled turns the area on or off. Off hides it; nothing is deleted.
// It reports whether the setting changed.
func SetEnabled(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, on bool, by string) (bool, error) {
	was, err := Enabled(ctx, tx, tenant)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO school_settings (tenant_id, enabled, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET enabled = $2, updated_by = $3, updated_at = now()`, tenant, on, by)
	return was != on, err
}

// need refuses when the area is off.
func need(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	on, err := Enabled(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if !on {
		return ErrOff
	}
	return nil
}

// lock serialises the school's writes that must see each other (starting
// a term, importing, promoting): the settings row, held until commit.
func lock(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var on bool
	err := tx.QueryRow(ctx, `SELECT enabled FROM school_settings WHERE tenant_id = $1 FOR UPDATE`, tenant).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !on {
		return ErrOff
	}
	return err
}

// --- personal data ---

func (s *Schools) open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env map[string]any, into any) (erased bool, err error) {
	if env == nil {
		return false, nil
	}
	v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	raw, _ := json.Marshal(v)
	return false, json.Unmarshal(raw, into)
}

// Erased is shown for a person whose data was erased.
const Erased = "(erased)"

// --- database errors ---

func pgCode(err error) (string, string) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe.ConstraintName
	}
	return "", ""
}

// checked turns the database's refusals into the package's errors: a
// broken rule (a past term changed) is ErrState with the database's own
// words.
func checked(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" && pe.ConstraintName == "" {
		return refused("%s", pe.Message)
	}
	return err
}

func dateOnly(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
	return t, err == nil
}

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
