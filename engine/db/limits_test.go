package db_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func claimCapped(t *testing.T, d *dbtest.DB, worker string, n, tenantCap int) map[uuid.UUID]int {
	t.Helper()
	rows, err := d.App.Query(ctx, `SELECT task_id, tenant_id, run_id, step_id, attempt, lease_epoch FROM taskiem_claim_tasks('connector', $1, $2, '60 seconds', $3)`, worker, n, tenantCap)
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
		_, err := tx.Exec(ctx, `SELECT taskiem_ops_set_tenant_limits($1, '{"worker_concurrency": 5}', 'test')`, b.ID)
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

// The application role reads a tenant's limits but cannot write the table.
// The operators' function (the CLI) works only inside the tenant's scope;
// the partner API's sets only a sub-tenant's, so a tenant's own transaction
// cannot raise its own limits through it (F78).
func TestTenantsCannotChangeTheirLimits(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_ops_set_tenant_limits($1, '{"runs_per_month": 100}', 'operator')`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT taskiem_set_tenant_limits($1, '{"runs_per_month": null, "max_running_runs": 100000}', 'me')`,
		`SELECT taskiem_limits_apply($1, '{"runs_per_month": null}', 'me')`,
	} {
		err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q, a.ID)
			return err
		})
		if sqlState(err) != "42501" {
			t.Errorf("%s on a top-level tenant's own scope: %v, want permission denied", q, err)
		}
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
		_, err := tx.Exec(ctx, `SELECT taskiem_ops_set_tenant_limits($1, '{"runs_per_month": 5}', 'a')`, b.ID)
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

// A tenant whose only tasks are not due yet (a retry's backoff) is passed
// over by the claim's tenant walk: its worker pool, cap and leased tasks
// are not looked up, so claim cost does not grow with such tenants, and
// the tenant with work due is still served.
func TestClaimWalksOnlyTenantsWithTasksDue(t *testing.T) {
	d := dbtest.New(t)
	for i := 0; i < 20; i++ {
		tn := d.SeedTenant(t, nil)
		run := d.StartRun(t, tn)
		if _, err := d.Admin.Exec(ctx, `INSERT INTO tasks (id, tenant_id, run_id, step_id, attempt, queue, available_at) VALUES ($1, $2, $3, 's', 1, 'connector', now() + interval '1 hour')`,
			uuid.Must(uuid.NewV7()), tn.ID, run); err != nil {
			t.Fatal(err)
		}
	}
	due := d.SeedTenant(t, nil)
	addTasks(t, d, due, d.StartRun(t, due), 2)
	var claimed, mine, lookups int
	if err := pgx.BeginFunc(ctx, d.Admin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL track_functions = 'all'`); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE tenant_id = $1) FROM taskiem_claim_tasks('connector', 'w1', 10, '60 seconds', 0, 'shared')`, due.ID).Scan(&claimed, &mine); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(calls), 0) FROM pg_stat_xact_user_functions WHERE funcname = 'taskiem_tenant_worker_pool'`).Scan(&lookups)
	}); err != nil {
		t.Fatal(err)
	}
	if claimed != 2 || mine != 2 {
		t.Errorf("claimed %d tasks (%d of the tenant with work due), want its 2 and no future task", claimed, mine)
	}
	if lookups != 1 {
		t.Errorf("the claim looked up %d tenants' worker pools, want 1 (only the tenant with work due)", lookups)
	}
}

// A partner sets a sub-tenant's limits (the partner API, in the
// sub-tenant's scope) no higher than its own explicit ones, read as
// runtime.Limits.Exceeds reads them (F78).
func TestSubTenantLimitsStayWithinThePartners(t *testing.T) {
	d := dbtest.New(t)
	p := d.SeedTenant(t, nil)
	sub := d.SeedTenant(t, &p.ID)
	if err := scoped(t, d, []uuid.UUID{p.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_ops_set_tenant_limits($1, '{"runs_per_month": 1000, "max_workflows": 0, "container_minutes_monthly": 0}', 'operator')`, p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	set := func(values string) error {
		return scoped(t, d, []uuid.UUID{sub.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_set_tenant_limits($1, $2, 'partner')`, sub.ID, values)
			return err
		})
	}
	for _, ok := range []string{
		`{"runs_per_month": 500}`,
		`{"runs_per_month": 1000}`,
		`{"runs_per_month": null}`,     // the partner's applies
		`{"max_workflows": 50000}`,     // the partner's 0 is no limit
		`{"max_running_runs": 100000}`, // the partner set none (Go checks the platform's)
		`{"container_minutes_monthly": 0}`,
	} {
		if err := set(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, over := range []string{
		`{"runs_per_month": 1001}`,
		`{"runs_per_month": 0}`, // no limit, under the partner's 1000
		`{"container_minutes_monthly": 10}`,
	} {
		if err := set(over); sqlState(err) != "42501" {
			t.Errorf("%s: %v, want refused", over, err)
		}
	}
}

// Only the operators' CLI sets limits through the operators' function: the
// API never names it, and only cmd/taskiem calls Store.SetLimits (F78).
func TestOperatorLimitsCallers(t *testing.T) {
	root := filepath.Join("..", "..")
	want := map[string]*regexp.Regexp{
		"taskiem_ops_set_tenant_limits": regexp.MustCompile(`^engine/runtime/limits\.go$`),
		".SetLimits(":                   regexp.MustCompile(`^cmd/taskiem/`),
	}
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && (e.Name() == "node_modules" || e.Name() == "web" || strings.HasPrefix(e.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for name, allowed := range want {
				if strings.Contains(line, name) && !allowed.MatchString(rel) {
					t.Errorf("%s names %s: operators' limits are set only by the CLI", rel, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
