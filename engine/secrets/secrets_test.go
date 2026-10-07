package secrets_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

var ctx = context.Background()

func localVault(t *testing.T, d *dbtest.DB) *secrets.Vault {
	t.Helper()
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}
}

func TestLocalKMSRoundTripAndTamper(t *testing.T) {
	kms, _ := secrets.NewLocalKMS(map[string][]byte{"a": bytes.Repeat([]byte{1}, 32), "b": bytes.Repeat([]byte{2}, 32)})
	ct, err := kms.Encrypt(ctx, "a", []byte("kek"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := kms.Decrypt(ctx, "a", ct); err != nil || string(pt) != "kek" {
		t.Fatalf("round trip: %q %v", pt, err)
	}
	if _, err := kms.Decrypt(ctx, "b", ct); err == nil {
		t.Error("decrypted under the wrong root key")
	}
	if _, err := secrets.NewLocalKMS(map[string][]byte{"short": {1}}); err == nil {
		t.Error("accepted a short root key")
	}
}

func TestPutGetIsolationRotation(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)

	if _, err := v.Put(ctx, a.ID, "prod", "paystack_key", []byte("sk_live_A"), "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Put(ctx, b.ID, "prod", "paystack_key", []byte("sk_live_B"), "u2"); err != nil {
		t.Fatal(err)
	}
	for tn, want := range map[uuid.UUID]string{a.ID: "sk_live_A", b.ID: "sk_live_B"} {
		if got, err := v.Get(ctx, tn, "prod", "paystack_key"); err != nil || got != want {
			t.Errorf("get: %q %v", got, err)
		}
	}
	if _, err := v.Get(ctx, a.ID, "dev", "paystack_key"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("environments must be separate: %v", err)
	}

	// Nothing readable is stored.
	var raw []byte
	if err := d.Admin.QueryRow(ctx, `SELECT ciphertext FROM secrets WHERE tenant_id = $1`, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk_live")) {
		t.Error("plaintext stored")
	}

	// Moving A's ciphertext onto B's row (a database-level attack) does not decrypt.
	if _, err := d.Admin.Exec(ctx, `UPDATE secrets SET ciphertext = (SELECT ciphertext FROM secrets WHERE tenant_id = $1), wrapped_key = (SELECT wrapped_key FROM secrets WHERE tenant_id = $1) WHERE tenant_id = $2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(ctx, b.ID, "prod", "paystack_key"); err == nil {
		t.Error("swapped ciphertext decrypted")
	}

	// Rotation re-wraps data keys; values stay readable, also with a cold cache.
	ver, err := v.Rotate(ctx, a.ID, "u1")
	if err != nil || ver != 2 {
		t.Fatalf("rotate: %d %v", ver, err)
	}
	if r, err := v.RewrapAll(ctx, a.ID); err != nil || r.Secrets != 1 || !r.Done {
		t.Fatalf("rewrap: %+v %v", r, err)
	}
	cold := localVault(t, d)
	if got, err := cold.Get(ctx, a.ID, "prod", "paystack_key"); err != nil || got != "sk_live_A" {
		t.Errorf("after rotation: %q %v", got, err)
	}
	var kv int
	_ = d.Admin.QueryRow(ctx, `SELECT kek_version FROM secrets WHERE tenant_id = $1`, a.ID).Scan(&kv)
	if kv != 2 {
		t.Errorf("data key not re-wrapped: version %d", kv)
	}

	// Writes are audited.
	var n int
	_ = db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action IN ('secret.write', 'secret.rotate_key')`).Scan(&n)
	})
	if n != 2 {
		t.Errorf("audit entries %d, want 2", n)
	}
}

func TestConnections(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a := d.SeedTenant(t, nil)
	if _, err := v.CreateConnection(ctx, a.ID, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_live_1"}, "u1"); err != nil {
		t.Fatal(err)
	}
	creds, err := v.Credentials(ctx, a.ID, "prod", "paystack", "")
	if err != nil || creds["secret_key"] != "sk_live_1" {
		t.Fatalf("credentials: %v %v", creds, err)
	}
	if _, err := v.CreateConnection(ctx, a.ID, "prod", "paystack", "payroll", "api_key", map[string]string{"secret_key": "sk_live_2"}, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Credentials(ctx, a.ID, "prod", "paystack", ""); err == nil {
		t.Error("ambiguous connection should require a name")
	}
	if creds, err := v.Credentials(ctx, a.ID, "prod", "paystack", "payroll"); err != nil || creds["secret_key"] != "sk_live_2" {
		t.Errorf("named: %v %v", creds, err)
	}
	if _, err := v.Credentials(ctx, a.ID, "dev", "paystack", ""); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("dev has no connection: %v", err)
	}
}

// TestOpenBaoTransit runs against a real OpenBao when TASKIEM_TEST_OPENBAO_ADDR is
// set (dev server with a transit key named taskiem-root).
func TestOpenBaoTransit(t *testing.T) {
	addr := os.Getenv("TASKIEM_TEST_OPENBAO_ADDR")
	if addr == "" {
		t.Skip("TASKIEM_TEST_OPENBAO_ADDR not set")
	}
	kms := &secrets.OpenBaoTransit{Addr: addr, Token: envOr("TASKIEM_TEST_OPENBAO_TOKEN", "dev")}
	ct, err := kms.Encrypt(ctx, "taskiem-root", []byte("tenant kek"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := kms.Decrypt(ctx, "taskiem-root", ct); err != nil || string(pt) != "tenant kek" {
		t.Fatalf("transit round trip: %q %v", pt, err)
	}
	d := dbtest.New(t)
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "taskiem-root"}
	a := d.SeedTenant(t, nil)
	if _, err := v.Put(ctx, a.ID, "prod", "k", []byte("v"), "u"); err != nil {
		t.Fatal(err)
	}
	cold := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "taskiem-root"}
	if got, err := cold.Get(ctx, a.ID, "prod", "k"); err != nil || got != "v" {
		t.Errorf("get via openbao: %q %v", got, err)
	}
	var wrapped string
	_ = d.Admin.QueryRow(ctx, `SELECT wrapped_kek FROM tenant_keys WHERE tenant_id = $1`, a.ID).Scan(&wrapped)
	if len(wrapped) < 9 || wrapped[:6] != "vault:" {
		t.Errorf("tenant key not wrapped by transit: %q", wrapped)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
