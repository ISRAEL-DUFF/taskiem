package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Operators change a tenant's limits from the CLI; every change is audited
// and "default" returns a limit to the platform's.
func TestTenantsLimitsCLI(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_DEFAULT_MAX_QUEUED_RUNS", "500")
	t.Setenv("USER", "ops")
	id := tn.ID.String()
	row := func(out, key string) string {
		m := regexp.MustCompile(`(?m)^` + key + `\s+(\S+)\s+(\S+)$`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no %s in\n%s", key, out)
		}
		return m[1] + " " + m[2]
	}

	var out bytes.Buffer
	if err := run([]string{"tenants", "limits", id, "--set", "runs_per_month=1000", "--set", "ingest_rate=2.5"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := row(out.String(), "runs_per_month"); got != "1000 tenant" {
		t.Errorf("runs_per_month: %s", got)
	}
	if got := row(out.String(), "ingest_rate"); got != "2.5 tenant" {
		t.Errorf("ingest_rate: %s", got)
	}
	if got := row(out.String(), "max_queued_runs"); got != "500 default" {
		t.Errorf("max_queued_runs from TASKIEM_DEFAULT_*: %s", got)
	}

	out.Reset()
	if err := run([]string{"tenants", "limits", id, "--set", "runs_per_month=default"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := row(out.String(), "runs_per_month"); got != "0 default" {
		t.Errorf("after reset: %s", got)
	}
	if got := row(out.String(), "ingest_rate"); got != "2.5 tenant" {
		t.Errorf("other limits kept: %s", got)
	}

	var n int
	if err := d.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'limits.set' AND actor_id = 'cli:ops'`, tn.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("audit entries: %d %v", n, err)
	}

	for _, bad := range [][]string{
		{"tenants", "limits", id, "--set", "nope=1"},
		{"tenants", "limits", id, "--set", "runs_per_day=-1"},
		{"tenants", "limits", "not-a-tenant"},
		{"tenants"},
	} {
		if err := run(bad, &out, &out); err == nil {
			t.Errorf("%s: accepted", strings.Join(bad, " "))
		}
	}
}
