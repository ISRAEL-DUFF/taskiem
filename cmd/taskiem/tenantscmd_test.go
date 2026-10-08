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

// Operators make a tenant a partner and set its caps; a sub-tenant cannot
// be one; every change is audited.
func TestTenantsPartnerCLI(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	sub := d.SeedTenant(t, &tn.ID)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("USER", "ops")
	var out bytes.Buffer
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--max-subtenants", "50"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--subtenant-runs-per-day", "1000"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "max_subtenants 50, subtenant_runs_per_day 1000, subtenant_runs_per_month 0") {
		t.Errorf("output:\n%s", out.String())
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--capabilities", "custom_domains, white_label"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "capabilities custom_domains,white_label") {
		t.Errorf("capabilities output:\n%s", out.String())
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--capabilities", "teleport"}, &out, &out); err == nil || !strings.Contains(err.Error(), "unknown capability") {
		t.Errorf("unknown capability: %v", err)
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--max-subtenants", "51"}, &out, &out); err != nil || !strings.HasSuffix(strings.TrimSpace(out.String()), "capabilities custom_domains,white_label") {
		t.Errorf("a change without --capabilities keeps them: %v\n%s", err, out.String())
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--capabilities", ""}, &out, &out); err != nil || !strings.HasSuffix(strings.TrimSpace(out.String()), "capabilities none") {
		t.Errorf("clearing capabilities: %v\n%s", err, out.String())
	}
	if err := run([]string{"tenants", "partner", sub.ID.String()}, &out, &out); err == nil || !strings.Contains(err.Error(), "sub-tenant cannot be a partner") {
		t.Errorf("sub-tenant as partner: %v", err)
	}
	if err := run([]string{"tenants", "partner", tn.ID.String(), "--disable"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var n, partners int
	ctx := context.Background()
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action IN ('partner.enable', 'partner.disable') AND actor_id = 'cli:ops'`, tn.ID).Scan(&n); err != nil || n != 6 {
		t.Errorf("audited %d changes (%v), want 6", n, err)
	}
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM partners`).Scan(&partners); err != nil || partners != 0 {
		t.Errorf("%d partners after --disable (%v)", partners, err)
	}
	if err := run([]string{"tenants", "partner", "nope"}, &out, &out); err == nil {
		t.Error("a bad tenant id was accepted")
	}
}
