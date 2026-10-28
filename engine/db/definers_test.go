package db_test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// SECURITY DEFINER functions run as taskiem_dispatch, past row-level
// security (spec 5.3). The ones the application role may call are pinned
// in definers.tsv, each with an owner and a reason, so a new one is seen
// and reviewed instead of becoming reachable with a single GRANT.
func TestDefinersAllowList(t *testing.T) {
	d := dbtest.New(t)
	listed := map[string]bool{}
	for _, line := range lines(t, "definers.tsv") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || strings.TrimSpace(f[1]) == "" || strings.TrimSpace(f[2]) == "" {
			t.Errorf("definers.tsv: want function, owner and reason separated by tabs: %q", line)
			continue
		}
		if listed[f[0]] {
			t.Errorf("definers.tsv lists %s twice", f[0])
		}
		listed[f[0]] = true
	}
	granted := query(t, d, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prosecdef AND p.prorettype <> 'trigger'::regtype
		  AND has_function_privilege('taskiem_app', p.oid, 'EXECUTE')`)
	for _, f := range granted {
		if !listed[f] {
			t.Errorf("taskiem_app may execute the definer %s, which definers.tsv does not list: review it, then add it with an owner and a reason", f)
		}
		delete(listed, f)
	}
	for f := range listed {
		t.Errorf("definers.tsv lists %s, which taskiem_app may not execute (or which no longer exists): remove it", f)
	}

	// Every definer pins search_path, so an object a caller creates cannot
	// stand in for one the function means.
	for _, f := range query(t, d, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prosecdef
		  AND NOT EXISTS (SELECT 1 FROM unnest(COALESCE(p.proconfig, '{}')) c WHERE c LIKE 'search_path=%')`) {
		t.Errorf("the definer %s does not set search_path", f)
	}
	// Every definer runs as taskiem_dispatch, whose grants and policies say
	// what it may touch; one owned by the migrating user would ignore them
	// on a superuser, or meet forced row-level security with no policy on
	// a plain schema owner. Partitions are made and dropped only by the
	// table's owner, and two read what dispatch may not.
	ownedByTable := map[string]bool{
		"taskiem_ensure_run_event_partitions(timestamp with time zone,integer)": true,
		"taskiem_drop_empty_run_event_partitions()":                             true,
		// They read usage figures and remote ids, which the dispatch role
		// is kept from (TestDispatchRoleCannotReadPayloads).
		"taskiem_partner_usage_snapshots(uuid,date,date)":          true,
		"taskiem_remote_tenants(timestamp with time zone,integer)": true,
	}
	for _, f := range query(t, d, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prosecdef AND pg_get_userbyid(p.proowner) <> 'taskiem_dispatch'`) {
		if !ownedByTable[f] {
			t.Errorf("the definer %s is not owned by taskiem_dispatch: ALTER FUNCTION ... OWNER TO taskiem_dispatch, granting it what the function reads", f)
		}
	}
	// None is left executable by every role (a function's default).
	for _, f := range query(t, d, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prosecdef AND p.prorettype <> 'trigger'::regtype
		  AND has_function_privilege('public', p.oid, 'EXECUTE')`) {
		t.Errorf("PUBLIC may execute the definer %s: REVOKE EXECUTE ... FROM PUBLIC", f)
	}
}

// The dispatch role sees every tenant's rows (USING (true) policies), so
// the columns it may read are the boundary: they are pinned in
// dispatch_columns.txt, and a new grant is added there in the same change.
func TestDispatchColumnsAllowList(t *testing.T) {
	d := dbtest.New(t)
	listed := map[string]bool{}
	for _, c := range lines(t, "dispatch_columns.txt") {
		listed[c] = true
	}
	readable := query(t, d, `SELECT c.relname || '.' || a.attname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v') AND NOT c.relispartition
		  AND has_column_privilege('taskiem_dispatch', c.oid, a.attnum, 'SELECT')`)
	for _, c := range readable {
		if !listed[c] {
			t.Errorf("taskiem_dispatch may read %s, which dispatch_columns.txt does not list: a definer could return it to any tenant; add it only if that is intended", c)
		}
		delete(listed, c)
	}
	for c := range listed {
		t.Errorf("dispatch_columns.txt lists %s, which taskiem_dispatch may not read: remove it", c)
	}
}

// lines returns a list file's entries: not blank, not a # comment.
func lines(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := sc.Text(); strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func query(t *testing.T, d *dbtest.DB, q string) []string {
	t.Helper()
	rows, err := d.Admin.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
