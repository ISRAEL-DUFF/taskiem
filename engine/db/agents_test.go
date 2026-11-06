package db_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// The agent-session definers act only on a tenant in the transaction's
// scope (F83): a transaction of business A cannot open a support session
// in business B (which would let the agent act there), list B's sessions
// or end them. Each still works for A.
func TestAgentSessionDefinersCheckScope(t *testing.T) {
	d := dbtest.New(t)
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	agentUser, owner, agent := uuid.New(), uuid.New(), uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'agent@x.test'), ($2, 'owner@x.test')`, agentUser, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO agents (id, user_id, workspace_id, code, name, phone, registered_by)
		VALUES ($1, $2, $3, 'AG-ABCDE', 'Tunde', '+234', 'ops')`, agent, agentUser, a.ID); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	open := `SELECT taskiem_agent_session_open($1, $2, 'support', $3, $4, NULL, NULL)`

	// Into B from A's transaction: refused.
	err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		var s uuid.UUID
		return tx.QueryRow(ctx, open, agent, b.ID, owner, expires).Scan(&s)
	})
	if sqlState(err) != "42501" {
		t.Fatalf("opening a session in B from A's scope: %v, want 42501", err)
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM agent_sessions WHERE tenant_id = $1`, b.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("B has %d agent sessions (%v)", n, err)
	}

	// B's own session, opened in B's scope.
	var inB uuid.UUID
	if err := scoped(t, d, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, open, agent, b.ID, owner, expires).Scan(&inB)
	}); err != nil {
		t.Fatal(err)
	}
	list := `SELECT count(*) FROM taskiem_tenant_agent_sessions($1, 100)`
	if n := count(t, d, []uuid.UUID{a.ID}, list, b.ID); n != 0 {
		t.Errorf("A's transaction lists %d of B's agent sessions", n)
	}
	if err := d.App.QueryRow(ctx, list, b.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("a query with no scope lists %d of B's agent sessions (%v)", n, err)
	}
	if n := count(t, d, []uuid.UUID{b.ID}, list, b.ID); n != 1 {
		t.Errorf("B lists %d of its agent sessions, want 1", n)
	}
	var ended bool
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_agent_session_end($1, $2, 'x')`, inB, b.ID).Scan(&ended)
	}); err != nil || ended {
		t.Errorf("A's transaction ended B's session: %v %v", ended, err)
	}
	if err := scoped(t, d, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_agent_session_end($1, $2, 'owner_ended')`, inB, b.ID).Scan(&ended)
	}); err != nil || !ended {
		t.Errorf("B could not end its own session: %v %v", ended, err)
	}
}
