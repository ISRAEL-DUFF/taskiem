package db_test

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Sign-in sessions, with their client IPs, were never deleted, and
// revoking a person's sessions scanned every session ever made (no index
// on user_id). A session is now deleted 30 days after it expires (a
// revoked one expires as it would have), by taskiem_purge_sessions from
// the scheduler, and revocation reads an index (F77).
func TestSessionsArePurgedAndIndexed(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	user := uuid.Must(uuid.NewV7())
	if _, err := d.Admin.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'ada@sessions.test')`, user); err != nil {
		t.Fatal(err)
	}
	add := func(name, expires, revoked string) []byte {
		h := sha256.Sum256([]byte(name))
		if _, err := d.Admin.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, created_at, expires_at, revoked_at, ip)
			VALUES ($1, $2, $3, now() - interval '60 days', now() + $4::interval, now() + NULLIF($5, '')::interval, '203.0.113.7')`,
			h[:], user, a.ID, expires, revoked); err != nil {
			t.Fatal(err)
		}
		return h[:]
	}
	live := add("live", "12 hours", "")
	add("expired long ago", "-31 days", "")
	add("revoked long ago", "-31 days", "-40 days")
	recent := add("expired yesterday", "-1 day", "")
	revokedRecent := add("revoked recently", "11 hours", "-1 hour")

	// The application role, with no tenant in scope, as the scheduler.
	var n int64
	if err := d.App.QueryRow(ctx, `SELECT taskiem_purge_sessions(100)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d sessions, want 2", n)
	}
	var kept [][]byte
	rows, err := d.Admin.Query(ctx, `SELECT token_hash FROM sessions WHERE user_id = $1 ORDER BY token_hash`, user)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, h)
	}
	rows.Close()
	if len(kept) != 3 {
		t.Errorf("kept %d sessions, want the live one, the one expired yesterday and the one revoked recently", len(kept))
	}
	for _, want := range [][]byte{live, recent, revokedRecent} {
		found := false
		for _, h := range kept {
			found = found || string(h) == string(want)
		}
		if !found {
			t.Errorf("a session that is live or recently ended was purged")
		}
	}
	if !leadingIndex(t, d, "sessions", "user_id", "") {
		t.Error("no index on sessions (user_id): revoking a person's sessions scans every session")
	}
	if !leadingIndex(t, d, "sessions", "expires_at", "") {
		t.Error("no index on sessions (expires_at): the purge scans every session")
	}
}
