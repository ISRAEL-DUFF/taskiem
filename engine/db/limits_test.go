package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func claimCapped(t *testing.T, d *dbtest.DB, worker string, n, tenantCap int) map[uuid.UUID]int {
	t.Helper()
	rows, err := d.App.Query(ctx, `SELECT * FROM taskiem_claim_tasks('connector', $1, $2, '60 seconds', $3)`, worker, n, tenantCap)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[claim])
	if err != nil {
		t.Fatal(err)
	}
	per := map[uuid.UUID]int{}
	for _, c := range got {
		per[c.Tenant]++
	}
	return per
}

// A tenant with a deep queue does not take a batch from a tenant whose one
// task arrived later: claims go round-robin across tenants.
func TestClaimsAreFairAcrossTenants(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	addTasks(t, d, a, d.StartRun(t, a), 40) // older
	addTasks(t, d, b, d.StartRun(t, b), 1)
	per := claimCapped(t, d, "w1", 4, 0)
	if per[b.ID] != 1 || per[a.ID] != 3 {
		t.Fatalf("first claim of 4: A %d, B %d; want 3 and 1", per[a.ID], per[b.ID])
	}
}

// No tenant holds more than its worker_concurrency of a queue's tasks at
// once (the platform default, or its own limit), so the rest of the
// workers stay free for other tenants.
func TestClaimsRespectTenantCap(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	addTasks(t, d, a, d.StartRun(t, a), 20)
	addTasks(t, d, b, d.StartRun(t, b), 20)
	if err := scoped(t, d, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, '{"worker_concurrency": 5}', 'test')`, b.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	per := claimCapped(t, d, "w1", 10, 3) // platform cap 3; B has its own 5
	if per[a.ID] != 3 || per[b.ID] != 5 {
		t.Fatalf("A %d (cap 3), B %d (cap 5)", per[a.ID], per[b.ID])
	}
	per = claimCapped(t, d, "w2", 10, 3)
	if len(per) != 0 {
		t.Fatalf("claimed beyond the caps: %v", per)
	}
	// One of A's tasks finishes: A may have one more.
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = (SELECT id FROM tasks WHERE lease_owner IS NOT NULL LIMIT 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if per = claimCapped(t, d, "w2", 10, 3); per[a.ID] != 1 || per[b.ID] != 0 {
		t.Fatalf("after one finished: %v", per)
	}
}

// The application role reads a tenant's limits but cannot write the table;
// the operators' function (the CLI) works only inside the tenant's scope.
func TestTenantsCannotChangeTheirLimits(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, '{"runs_per_month": 100}', 'operator')`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE tenant_limits SET runs_per_month = 1000000`,
		`INSERT INTO tenant_limits (tenant_id, updated_by) VALUES ($1, 'me')`,
		`DELETE FROM tenant_limits`,
	} {
		err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			args := []any{}
			if q[0] == 'I' {
				args = append(args, a.ID)
			}
			_, err := tx.Exec(ctx, q, args...)
			return err
		})
		if sqlState(err) != "42501" {
			t.Errorf("%s: %v, want permission denied", q, err)
		}
	}
	err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, '{"runs_per_month": 5}', 'a')`, b.ID)
		return err
	})
	if sqlState(err) != "42501" {
		t.Errorf("setting another tenant's limits: %v", err)
	}
	if n := count(t, d, []uuid.UUID{a.ID}, `SELECT count(*) FROM tenant_limits WHERE runs_per_month = 100`); n != 1 {
		t.Errorf("tenant cannot read its limits: %d", n)
	}
	if n := count(t, d, []uuid.UUID{b.ID}, `SELECT count(*) FROM tenant_limits`); n != 0 {
		t.Errorf("tenant B sees %d limit rows", n)
	}
}
