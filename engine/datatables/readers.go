package datatables

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Readers (decision 0065, docs/data-tables.md#who-may-read-a-table): a
// table may name the membership roles (built-in, custom or business
// roles) whose holders may read and change its rows. Without readers
// every table.read holder reads them, as before; with them, only a
// member holding one of the roles, or the owner, does, on top of
// table.read (and table.edit to change them). The rows live in
// Xenibase, so the API and chat check MayRead before they reach a row;
// workflows reach them through the data_tables connection regardless.

// MaxReaders is the most roles a table's readers name.
const MaxReaders = 20

// Owner is the role that reads every table, whatever its readers.
const Owner = "owner"

// ErrRestricted: the table's rows are for its readers, and the person
// holds none of their roles (403 table_restricted).
var ErrRestricted = errors.New("restricted")

var readerRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// CheckReaders checks a readers list and returns it sorted without
// repeats. An empty list would mean nobody, which is refused: no readers
// at all (nil, every table.read holder) is set by clearing them.
func CheckReaders(roles []string) ([]string, error) {
	if len(roles) == 0 {
		return nil, fmt.Errorf("%w: readers names at least one role: an empty list would let nobody read the table (clear the readers to let every table.read holder read it)", ErrInvalid)
	}
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		if !readerRe.MatchString(r) {
			return nil, fmt.Errorf("%w: reader %q is not a role name (lower-case letters, digits and underscores, starting with a letter)", ErrInvalid, r)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(out) > MaxReaders {
		return nil, fmt.Errorf("%w: a table names at most %d readers", ErrInvalid, MaxReaders)
	}
	slices.Sort(out)
	return out, nil
}

// Restricted reports whether the table names readers.
func (t Table) Restricted() bool { return len(t.Readers) > 0 }

// MayRead reports whether a member holding roles may read (and, with
// table.edit, change) the table's rows: every member when it names no
// readers, else the owner and the holders of one of its roles.
// table.read and table.edit are checked separately.
func MayRead(t Table, roles []string) bool {
	if !t.Restricted() || slices.Contains(roles, Owner) {
		return true
	}
	for _, r := range roles {
		if slices.Contains(t.Readers, r) {
			return true
		}
	}
	return false
}

// CheckRead is MayRead as an error wrapping ErrRestricted.
func CheckRead(t Table, roles []string) error {
	if MayRead(t, roles) {
		return nil
	}
	return fmt.Errorf("%w: the rows of %s are for its readers only (roles %v, and the owner)", ErrRestricted, t.Name, t.Readers)
}

// SetReaders changes a table's readers: a checked list, or nil to clear
// them. A kept table's record does not change (its guard refuses, 42501),
// which is reported as ErrKept.
func SetReaders(ctx context.Context, tx pgx.Tx, id uuid.UUID, readers []string) error {
	var arg any
	if readers != nil {
		arg = readers
	}
	tag, err := tx.Exec(ctx, `UPDATE data_tables SET readers = $2, updated_at = now() WHERE id = $1`, id, arg)
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "42501" {
		return fmt.Errorf("%w: a kept table's readers do not change", ErrKept)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("table %s: %w", id, ErrNotFound)
	}
	return nil
}
