package dbtest

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// Two packages migrating their own databases at once can both create one
// server-wide role (CI's race job saw it in migration 00176): that loss,
// as goose reports it, is retried, and nothing else is.
func TestRoleRaceIsRetried(t *testing.T) {
	lost := &goose.PartialError{Err: &pgconn.PgError{Code: "23505", ConstraintName: "pg_authid_rolname_index"}}
	if !roleRace(lost) {
		t.Error("the role race, as goose reports it, is not retried")
	}
	for _, err := range []error{
		&goose.PartialError{Err: &pgconn.PgError{Code: "23505", ConstraintName: "tenants_pkey"}},
		&goose.PartialError{Err: &pgconn.PgError{Code: "42601"}},
		errors.New("connection refused"),
	} {
		if roleRace(err) {
			t.Errorf("retried: %v", err)
		}
	}
}
