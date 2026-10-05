package db_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

var ctx = context.Background()

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func scoped(t *testing.T, d *dbtest.DB, tenants []uuid.UUID, fn func(pgx.Tx) error) error {
	t.Helper()
	return db.InTenantTx(ctx, d.App, tenants, fn)
}

func count(t *testing.T, d *dbtest.DB, tenants []uuid.UUID, q string, args ...any) int {
	t.Helper()
	var n int
	if err := scoped(t, d, tenants, func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEveryTenantTableIsProtected(t *testing.T) {
	d := dbtest.New(t)
	rows, err := d.Admin.Query(ctx, `
	  SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
	         EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid
	                 AND 'taskiem_app'::regrole = ANY (p.polroles))
	    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	   WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
	     AND c.relname NOT LIKE 'goose%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var rls, force, policy bool
		if err := rows.Scan(&name, &rls, &force, &policy); err != nil {
			t.Fatal(err)
		}
		seen++
		if !rls || !force || !policy {
			t.Errorf("%s: rls=%v force=%v app policy=%v", name, rls, force, policy)
		}
	}
	if seen < 13 {
		t.Errorf("only %d tables checked", seen)
	}
	// Partitions are reachable only through the parent's policies.
	var leaked int
	if err := d.Admin.QueryRow(ctx, `
	  SELECT count(*) FROM pg_class c WHERE c.relispartition
	     AND (has_table_privilege('taskiem_app', c.oid, 'SELECT') OR has_table_privilege('taskiem_dispatch', c.oid, 'SELECT'))`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Errorf("%d partitions are directly readable by application roles", leaked)
	}
}

func TestTenantIsolation(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	runA := d.StartRun(t, a)

	for _, tbl := range []string{"workflows", "workflow_versions", "runs", "run_events"} {
		if n := count(t, d, []uuid.UUID{b.ID}, "SELECT count(*) FROM "+tbl+" WHERE tenant_id = $1", a.ID); n != 0 {
			t.Errorf("tenant B sees %d of A's %s", n, tbl)
		}
	}
	if n := count(t, d, []uuid.UUID{b.ID}, "SELECT count(*) FROM tenants WHERE id = $1", a.ID); n != 0 {
		t.Errorf("tenant B sees tenant A")
	}
	if n := count(t, d, []uuid.UUID{a.ID}, "SELECT count(*) FROM run_events WHERE run_id = $1", runA); n != 1 {
		t.Errorf("tenant A sees %d of its own events, want 1", n)
	}

	// No scope at all sees nothing.
	var n int
	err := pgx.BeginFunc(ctx, d.App, func(tx pgx.Tx) error { return tx.QueryRow(ctx, "SELECT count(*) FROM runs").Scan(&n) })
	if err != nil || n != 0 {
		t.Errorf("unscoped: n=%d err=%v", n, err)
	}

	// Writing into another tenant is rejected by WITH CHECK.
	err = scoped(t, d, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'x', $1)`, uuid.New(), a.ID)
		return err
	})
	if sqlState(err) != "42501" {
		t.Errorf("cross-tenant insert: got %v, want RLS violation", err)
	}

	// Appending to another tenant's run fails as "not found".
	err = scoped(t, d, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'StepScheduled', 's', 1, '{}')`, runA)
		return err
	})
	if sqlState(err) != "P0002" {
		t.Errorf("cross-tenant append: got %v", err)
	}
}

func TestAppendOnlyTables(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	d.StartRun(t, a)
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'test', 'x.y', 't', '{}')`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"UPDATE run_events SET payload = '{}'",
		"DELETE FROM run_events",
		"UPDATE audit_log SET detail = '{}'",
		"DELETE FROM audit_log",
		"TRUNCATE run_events",
		"DELETE FROM workflow_versions",
	} {
		err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q); return err })
		if sqlState(err) != "42501" {
			t.Errorf("%s: got %v, want permission denied", q, err)
		}
	}
}

func TestWorkflowVersionsAreImmutable(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE workflow_versions SET definition = '{"changed":true}' WHERE workflow_id = $1`, a.WorkflowID)
		return err
	})
	if sqlState(err) != "23514" {
		t.Errorf("definition update: got %v", err)
	}
	err = scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE workflow_versions SET layout = '{"x":1}', state = 'deprecated' WHERE workflow_id = $1`, a.WorkflowID)
		return err
	})
	if err != nil {
		t.Errorf("layout/state update: %v", err)
	}
}

func TestConcurrentAppendsGetDistinctSeqs(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	run := d.StartRun(t, a)
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'StepScheduled', 's', 1, '{}')`, run)
					return err
				}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var n, distinct, maxSeq, last int
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*), count(DISTINCT seq), max(seq) FROM run_events WHERE run_id = $1`, run).Scan(&n, &distinct, &maxSeq); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT last_seq FROM runs WHERE id = $1`, run).Scan(&last)
	}); err != nil {
		t.Fatal(err)
	}
	want := workers*each + 1
	if n != want || distinct != want || maxSeq != want || last != want {
		t.Errorf("events=%d distinct=%d max=%d last_seq=%d, want %d", n, distinct, maxSeq, last, want)
	}
}

func addTasks(t *testing.T, d *dbtest.DB, tn dbtest.Tenant, run uuid.UUID, n int) {
	t.Helper()
	if err := scoped(t, d, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := tx.Exec(ctx, `INSERT INTO tasks (id, tenant_id, run_id, step_id, attempt, queue) VALUES ($1, $2, $3, $4, 1, 'connector')`,
				uuid.Must(uuid.NewV7()), tn.ID, run, "s"+uuid.NewString()[:6]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type claim struct {
	Task, Tenant, Run uuid.UUID
	Step              string
	Attempt           int
	Epoch             int64
}

func claimTasks(t *testing.T, d *dbtest.DB, worker string, n int, lease string) []claim {
	t.Helper()
	rows, err := d.App.Query(ctx, `SELECT * FROM taskiem_claim_tasks('connector', $1, $2, $3::interval)`, worker, n, lease)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[claim])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConcurrentClaimsAreDisjoint(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)
	addTasks(t, d, a, d.StartRun(t, a), 50)
	addTasks(t, d, b, d.StartRun(t, b), 50)

	var mu sync.Mutex
	seen := map[uuid.UUID]string{}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				got := claimTasks(t, d, worker, 7, "60 seconds")
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, c := range got {
					if prev, dup := seen[c.Task]; dup {
						t.Errorf("task %s claimed by %s and %s", c.Task, prev, worker)
					}
					seen[c.Task] = worker
				}
				mu.Unlock()
			}
		}(string(rune('a' + w)))
	}
	wg.Wait()
	if len(seen) != 100 {
		t.Errorf("claimed %d tasks, want 100", len(seen))
	}
}

func TestFencingRejectsStaleWorker(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	addTasks(t, d, a, d.StartRun(t, a), 1)

	first := claimTasks(t, d, "w1", 1, "1 millisecond")
	if len(first) != 1 || first[0].Epoch != 1 {
		t.Fatalf("first claim: %+v", first)
	}
	time.Sleep(20 * time.Millisecond)
	var recovered int
	if err := d.App.QueryRow(ctx, `SELECT taskiem_recover_expired_leases()`).Scan(&recovered); err != nil || recovered != 1 {
		t.Fatalf("recover: n=%d err=%v", recovered, err)
	}

	// After recovery, before anyone reclaims, the stale holder is already fenced out.
	fence := func(worker string, epoch int64, fn string) bool {
		var ok bool
		if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			if fn == "heartbeat" {
				return tx.QueryRow(ctx, `SELECT taskiem_task_heartbeat($1, $2, $3, '60 seconds')`, first[0].Task, worker, epoch).Scan(&ok)
			}
			return tx.QueryRow(ctx, `SELECT taskiem_task_finish($1, $2, $3)`, first[0].Task, worker, epoch).Scan(&ok)
		}); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if fence("w1", 1, "heartbeat") {
		t.Error("stale heartbeat accepted after recovery")
	}

	second := claimTasks(t, d, "w2", 1, "60 seconds")
	if len(second) != 1 || second[0].Epoch != 2 || second[0].Task != first[0].Task {
		t.Fatalf("second claim: %+v", second)
	}
	if fence("w1", 1, "finish") {
		t.Error("stale worker finished a task it no longer holds")
	}
	if fence("w2", 1, "finish") {
		t.Error("finish accepted with the wrong epoch")
	}
	if !fence("w2", 2, "heartbeat") || !fence("w2", 2, "finish") {
		t.Error("current holder fenced out")
	}
	if fence("w2", 2, "finish") {
		t.Error("task finished twice")
	}
}

func TestTimersFireOnceUnderLease(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	run := d.StartRun(t, a)
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO timers (id, tenant_id, run_id, step_id, kind, fire_at) VALUES ($1, $2, $3, 'w', 'wait', now() - interval '1 second')`,
			uuid.New(), a.ID, run)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimT := func(worker string) int {
		var n int
		if err := d.App.QueryRow(ctx, `SELECT count(*) FROM taskiem_claim_due_timers($1, 10)`, worker).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := claimT("s1"); n != 1 {
		t.Fatalf("first claim got %d", n)
	}
	if n := claimT("s2"); n != 0 {
		t.Errorf("timer claimed again while leased")
	}
}

func TestDispatchRoleCannotReadPayloads(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	d.StartRun(t, a)
	for _, q := range []string{
		"SELECT payload FROM run_events",
		"SELECT definition FROM workflow_versions",
		"SELECT detail FROM audit_log",
		"SELECT environment FROM runs",
		"SELECT name FROM users",
	} {
		_, err := d.Dispatch.Exec(ctx, q)
		if sqlState(err) != "42501" {
			t.Errorf("dispatch role ran %q: %v", q, err)
		}
	}
	// The application role is not a member of the dispatch role, so it can
	// reach dispatch privileges only through the SECURITY DEFINER functions.
	var member bool
	if err := d.Admin.QueryRow(ctx, `SELECT pg_has_role('taskiem_app', 'taskiem_dispatch', 'MEMBER')`).Scan(&member); err != nil || member {
		t.Errorf("taskiem_app is a member of taskiem_dispatch (err=%v)", err)
	}
}

func TestAuthTenantScope(t *testing.T) {
	d := dbtest.New(t)
	partner := d.SeedTenant(t, nil)
	sub := d.SeedTenant(t, &partner.ID)
	suspended := d.SeedTenant(t, &partner.ID)
	other := d.SeedTenant(t, nil)
	user := uuid.Must(uuid.NewV7())
	if err := scoped(t, d, []uuid.UUID{partner.ID, suspended.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'Ada@Example.com')`, user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`, partner.ID, user); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, suspended.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var found uuid.UUID
	if err := d.App.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user('ada@example.com')`).Scan(&found); err != nil || found != user {
		t.Fatalf("find user: %v %v", found, err)
	}
	scope := func(subs bool) []uuid.UUID {
		var s []uuid.UUID
		if err := d.App.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, $2)`, user, subs).Scan(&s); err != nil {
			t.Fatal(err)
		}
		sort.Slice(s, func(i, j int) bool { return s[i].String() < s[j].String() })
		return s
	}
	if s := scope(false); len(s) != 1 || s[0] != partner.ID {
		t.Errorf("own scope = %v", s)
	}
	s := scope(true)
	want := []uuid.UUID{partner.ID, sub.ID}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if len(s) != 2 || s[0] != want[0] || s[1] != want[1] {
		t.Errorf("partner scope = %v, want %v (no suspended %v, no other %v)", s, want, suspended.ID, other.ID)
	}
}

func TestAuditChain(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tn := a.ID
			if w%2 == 1 {
				tn = b.ID
			}
			for i := 0; i < 15; i++ {
				if err := scoped(t, d, []uuid.UUID{tn}, func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', 'u1', 'workflow.publish', 'wf_x', jsonb_build_object('i', $2::int, 'amount_kobo', 5000000))`, tn, i)
					return err
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	verify := func(tn uuid.UUID) *int64 {
		var bad *int64
		if err := scoped(t, d, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_audit_verify($1)`, tn).Scan(&bad)
		}); err != nil {
			t.Fatal(err)
		}
		return bad
	}
	if bad := verify(a.ID); bad != nil {
		t.Fatalf("intact chain A fails at %d", *bad)
	}
	if n := count(t, d, []uuid.UUID{a.ID}, `SELECT count(*) FROM audit_log WHERE tenant_id = $1`, a.ID); n != 60 {
		t.Fatalf("chain A has %d rows, want 60", n)
	}

	// A database-level attacker (superuser) edits one row: detected at that row.
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_log SET detail = '{"i":0,"amount_kobo":1}' WHERE tenant_id = $1 AND chain_seq = 17`, a.ID); err != nil {
		t.Fatal(err)
	}
	if bad := verify(a.ID); bad == nil || *bad != 17 {
		t.Errorf("tampered row: verify = %v, want 17", bad)
	}
	// Deleting the newest row of B is detected against the chain head.
	if _, err := d.Admin.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id = $1 AND chain_seq = 60`, b.ID); err != nil {
		t.Fatal(err)
	}
	if bad := verify(b.ID); bad == nil || *bad != 60 {
		t.Errorf("truncated chain: verify = %v, want 60", bad)
	}
}

func TestPartitionsCoverNewRuns(t *testing.T) {
	d := dbtest.New(t)
	var created int
	if err := d.App.QueryRow(ctx, `SELECT taskiem_ensure_run_event_partitions(now(), 3)`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	var parts int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent = 'run_events'::regclass`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts < 4 {
		t.Errorf("%d partitions after migration and ensure", parts)
	}
}
