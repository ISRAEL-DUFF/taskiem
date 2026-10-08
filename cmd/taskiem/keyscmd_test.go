package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Operators see a tenant's keys, rotate with --wait and check them; each
// action is audited in the tenant's log as the operator.
func TestTenantsKeysCLI(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	key := bytes.Repeat([]byte{9}, 32)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_KMS", "local")
	t.Setenv("TASKIEM_LOCAL_KMS_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("USER", "ops")
	ctx := context.Background()
	kms, _ := secrets.NewLocalKMS(map[string][]byte{"taskiem": key})
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "taskiem"}
	if _, err := v.Put(ctx, tn.ID, "prod", "api_token", []byte("tok"), "u"); err != nil {
		t.Fatal(err)
	}
	id := tn.ID.String()
	var out bytes.Buffer
	if err := run([]string{"tenants", "keys", id}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "mode: platform; current version 1") || !strings.Contains(out.String(), "1        platform    current  1") {
		t.Fatalf("status:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"tenants", "keys", id, "rotate", "--wait"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tenant key version 2 is current") || !strings.Contains(out.String(), "re-wrapped 1 data keys") ||
		!strings.Contains(out.String(), "retired versions [1]") || strings.Contains(out.String(), "re-wrapping (") {
		t.Fatalf("rotate --wait:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"tenants", "keys", id, "check"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the tenant key works; resumed 0 parked steps") {
		t.Fatalf("check:\n%s", out.String())
	}
	if got, err := v.Get(ctx, tn.ID, "prod", "api_token"); err != nil || got != "tok" {
		t.Errorf("after rotation: %q %v", got, err)
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'secret.rotate_key' AND actor_id = 'cli:ops' AND actor_type = 'platform_admin'`, tn.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("audited %d rotations (%v)", n, err)
	}
	for _, bad := range [][]string{{"tenants", "keys"}, {"tenants", "keys", "nope"}, {"tenants", "keys", id, "check", "--wait"}, {"tenants", "keys", id, "explode"}} {
		if err := run(bad, &out, &out); err == nil {
			t.Errorf("%s: accepted", strings.Join(bad, " "))
		}
	}
}
