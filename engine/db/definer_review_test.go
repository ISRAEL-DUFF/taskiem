package db_test

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// What the definer review changed (migration 00266), as each caller meets it.
func TestDefinerReview(t *testing.T) {
	d := dbtest.New(t)
	partner := d.SeedTenant(t, nil)
	a := d.SeedTenant(t, &partner.ID)
	b := d.SeedTenant(t, &partner.ID)
	if _, err := d.Admin.Exec(ctx, `INSERT INTO tenant_usage (tenant_id, day, runs_started) VALUES ($1, current_date, 3), ($2, current_date, 4)`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	in := func(scope uuid.UUID, fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, d.App, []uuid.UUID{scope}, fn); err != nil {
			t.Fatal(err)
		}
	}

	// A sub-tenant gets the partner-wide totals, never its siblings' rows.
	in(a.ID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM taskiem_partner_usage($1)`, partner.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("a sub-tenant saw %d sub-tenants' usage: %v", n, err)
		}
		var today, month int64
		if err := tx.QueryRow(ctx, `SELECT runs_today, runs_month FROM taskiem_partner_run_totals($1)`, partner.ID).Scan(&today, &month); err != nil || today != 7 || month != 7 {
			t.Errorf("totals %d %d: %v", today, month, err)
		}
		return nil
	})
	in(partner.ID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM taskiem_partner_usage($1)`, partner.ID).Scan(&n); err != nil || n != 2 {
			t.Errorf("the partner saw %d sub-tenants' usage: %v", n, err)
		}
		return nil
	})
	other := d.SeedTenant(t, nil)
	in(other.ID, func(tx pgx.Tx) error {
		var today int64
		if err := tx.QueryRow(ctx, `SELECT runs_today FROM taskiem_partner_run_totals($1)`, partner.ID).Scan(&today); err != nil || today != 0 {
			t.Errorf("another tenant read the partner's totals: %d %v", today, err)
		}
		return nil
	})

	// A reset link with the wrong secret leaves the person's other links.
	user := uuid.Must(uuid.NewV7())
	if _, err := d.Admin.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'ada@example.com')`, user); err != nil {
		t.Fatal(err)
	}
	hash := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	if _, err := d.Admin.Exec(ctx, `INSERT INTO password_resets (selector, secret_hash, user_id, expires_at) VALUES
		($1, $2, $3, now() + interval '1 hour'), ($4, $5, $3, now() + interval '1 hour')`, []byte("selector-one-16b"), hash("one"), user, []byte("selector-two-16b"), hash("two")); err != nil {
		t.Fatal(err)
	}
	links := func() int {
		var n int
		_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, user).Scan(&n)
		return n
	}
	var got *uuid.UUID
	if err := d.App.QueryRow(ctx, `SELECT taskiem_auth_password_reset_use($1, $2)`, []byte("selector-one-16b"), hash("wrong")).Scan(&got); err != nil || got != nil || links() != 2 {
		t.Errorf("a wrong secret: %v %v, %d links left", got, err, links())
	}
	if err := d.App.QueryRow(ctx, `SELECT taskiem_auth_password_reset_use($1, $2)`, []byte("selector-one-16b"), hash("one")).Scan(&got); err != nil || got == nil || *got != user || links() != 0 {
		t.Errorf("the right secret: %v %v, %d links left", got, err, links())
	}

	// Who has an email, without the password hash.
	var id uuid.UUID
	var status string
	if err := d.App.QueryRow(ctx, `SELECT user_id, status FROM taskiem_auth_user_of('ADA@example.com')`).Scan(&id, &status); err != nil || id != user {
		t.Errorf("user_of: %v %s %v", id, status, err)
	}

	// A tenant's id finds that tenant, whatever another tenant is called.
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET name = 'mimic ' || $1::text WHERE id = $2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.App.QueryRow(ctx, `SELECT id FROM taskiem_ops_tenants($1, 1)`, a.ID.String()).Scan(&id); err != nil || id != a.ID {
		t.Errorf("looking up %s found %s: %v", a.ID, id, err)
	}

	// The console's agent list keeps the ID check, not the referee.
	tx, err := d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agents (id, user_id, workspace_id, code, name, phone, checks, registered_by)
		VALUES ($1, $2, $3, 'AG-ABCDE', 'Agent', '+2348000000000', '{"id_type": "nin", "id_checked": true, "referee_name": "Bola", "referee_phone": "+2348011111111"}', 'ops')`,
		uuid.New(), user, partner.ID); err != nil {
		t.Fatal(err)
	}
	var checks map[string]any
	if err := tx.QueryRow(ctx, `SELECT checks FROM taskiem_ops_agents()`).Scan(&checks); err != nil || checks["id_checked"] != true || checks["referee_name"] != nil || checks["referee_phone"] != nil {
		t.Errorf("checks %v: %v", checks, err)
	}
}
