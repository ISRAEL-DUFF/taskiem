package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Operators route a tenant or a plan to a dedicated pool; assigning to a
// pool with no live worker needs --force; changes are audited.
func TestPoolsCLI(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_KMS", "local")
	t.Setenv("TASKIEM_LOCAL_KMS_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	t.Setenv("USER", "ops")
	ctx := context.Background()
	id := tn.ID.String()
	var out bytes.Buffer
	if err := run([]string{"pools", "assign", "acme", "--tenant", id}, &out, &out); err == nil || !strings.Contains(err.Error(), "no live worker") {
		t.Fatalf("assign to an empty pool: %v", err)
	}
	if _, err := d.App.Exec(ctx, `SELECT taskiem_worker_seen('host/acme/connector/1', 'connector', 'acme')`); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pools", "assign", "acme", "--tenant", id}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "work now goes to pool acme") {
		t.Fatalf("assign:\n%s", out.String())
	}
	out.Reset()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO plans (id, name, tier, monthly_kobo, annual_kobo, updated_by) VALUES ('enterprise', 'Enterprise', 'enterprise', 0, 0, 'test')`); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pools", "assign", "dedicated", "--plan", "enterprise", "--force"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"pools"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acme  connector  1", "tenant " + id + "  acme       cli:ops", "plan enterprise", "dedicated"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"pools", "unassign", "--tenant", id}, &out, &out); err != nil {
		t.Fatal(err)
	}
	// Back to its plan's pool? It has no subscription: the shared pool.
	if !strings.Contains(out.String(), "work now goes to pool shared") {
		t.Errorf("unassign:\n%s", out.String())
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action LIKE 'worker_pool.%' AND actor_type = 'platform_admin' AND actor_id = 'cli:ops'`, tn.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("audited %d (%v)", n, err)
	}
	for _, bad := range [][]string{{"pools", "assign"}, {"pools", "assign", "Bad Name", "--tenant", id}, {"pools", "assign", "x", "--tenant", id, "--plan", "p"},
		{"pools", "unassign"}, {"pools", "unassign", "--tenant", id, "--force"}, {"pools", "assign", "x", "--tenant", "nope"}, {"pools", "explode"}, {"pools", "list", "extra"}} {
		if err := run(bad, &out, &out); err == nil {
			t.Errorf("%s: accepted", strings.Join(bad, " "))
		}
	}
}

func TestCloudConfig(t *testing.T) {
	t.Setenv("TASKIEM_WORKER_POOL", "Bad Pool")
	if _, err := cloudConfigFromEnv(); err == nil {
		t.Error("bad pool name accepted")
	}
	t.Setenv("TASKIEM_WORKER_POOL", "acme")
	t.Setenv("TASKIEM_DATABASE_READ_MAX_LAG", "-1s")
	if _, err := cloudConfigFromEnv(); err == nil {
		t.Error("negative lag accepted")
	}
	t.Setenv("TASKIEM_DATABASE_READ_MAX_LAG", "3s")
	t.Setenv("TASKIEM_DATABASE_RETRY_WINDOW", "45s")
	t.Setenv("TASKIEM_DATABASE_READ_URL", "postgres://reader@replica/taskiem")
	c, err := cloudConfigFromEnv()
	if err != nil || c.WorkerPool != "acme" || c.ReadMaxLag != 3*time.Second || c.RetryWindow != 45*time.Second || c.ReadDSN == "" {
		t.Errorf("%+v %v", c, err)
	}
	if w := dsnWarnings("TASKIEM_DATABASE_URL", "postgres://u@db1:5432,db2:5432/taskiem"); len(w) != 1 {
		t.Errorf("several hosts without target_session_attrs: %v", w)
	}
	for _, ok := range []string{"postgres://u@db1:5432,db2:5432/taskiem?target_session_attrs=read-write", "postgres://u@db1/taskiem"} {
		if w := dsnWarnings("TASKIEM_DATABASE_URL", ok); len(w) != 0 {
			t.Errorf("%s: %v", ok, w)
		}
	}
}
