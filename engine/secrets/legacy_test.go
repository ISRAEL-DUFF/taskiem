package secrets_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// TestLegacyContextIsPinned (self-review K6): a secret still on the first
// encryption context is read, and upgraded, only where migration 00140
// recorded it. Renaming it, moving it to another environment, or a
// legacy row with no record is refused, by reads and by the re-wrap job,
// and the application role cannot rewrite the record.
func TestLegacyContextIsPinned(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a := d.SeedTenant(t, nil)
	if err := v.PutLegacy(ctx, a.ID, "prod", "legacy_key", []byte("sk_legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, secrets.RecordLegacyBindings); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(ctx, a.ID, "prod", "legacy_key"); err != nil || got != "sk_legacy" {
		t.Fatalf("legacy secret where it was recorded: %q %v", got, err)
	}

	// Renamed, or moved to another environment: refused.
	exec := func(q string) {
		t.Helper()
		if _, err := d.Admin.Exec(ctx, q, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE secrets SET name = 'stolen' WHERE tenant_id = $1 AND name = 'legacy_key'`)
	if _, err := v.Get(ctx, a.ID, "prod", "stolen"); !errors.Is(err, secrets.ErrLegacyContext) {
		t.Errorf("renamed legacy secret: %v", err)
	}
	// The re-wrap job will not bless the rename either.
	exec(`INSERT INTO key_rewrap_due (tenant_id, reason) VALUES ($1, 'migration') ON CONFLICT DO NOTHING`)
	if _, err := v.RewrapAll(ctx, a.ID); !errors.Is(err, secrets.ErrLegacyContext) {
		t.Errorf("re-wrap of a renamed legacy secret: %v", err)
	}
	var aad int
	_ = d.Admin.QueryRow(ctx, `SELECT aad_version FROM secrets WHERE tenant_id = $1`, a.ID).Scan(&aad)
	if aad != 1 {
		t.Errorf("renamed secret was upgraded to aad_version %d", aad)
	}
	exec(`UPDATE secrets SET name = 'legacy_key', environment = 'dev' WHERE tenant_id = $1`)
	if _, err := v.Get(ctx, a.ID, "dev", "legacy_key"); !errors.Is(err, secrets.ErrLegacyContext) {
		t.Errorf("legacy secret moved to another environment: %v", err)
	}
	exec(`UPDATE secrets SET environment = 'prod' WHERE tenant_id = $1`)

	// The application role can read the record but not rewrite it.
	for _, q := range []string{
		`UPDATE secret_legacy_bindings SET binding = 'stolen' WHERE tenant_id = $1`,
		`INSERT INTO secret_legacy_bindings (secret_id, tenant_id, environment, binding) VALUES (gen_random_uuid(), $1, 'prod', 'x')`,
	} {
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q, a.ID)
			return err
		})
		if err == nil {
			t.Errorf("application role ran %q", q)
		}
	}

	// Back where it was recorded: readable, and the job upgrades it and
	// drops the record.
	if r, err := v.RewrapAll(ctx, a.ID); err != nil || r.Secrets != 1 {
		t.Fatalf("re-wrap: %+v %v", r, err)
	}
	var left int
	_ = d.Admin.QueryRow(ctx, `SELECT aad_version, (SELECT count(*) FROM secret_legacy_bindings WHERE tenant_id = $1) FROM secrets WHERE tenant_id = $1`, a.ID).Scan(&aad, &left)
	if aad != 2 || left != 0 {
		t.Errorf("after re-wrap: aad_version %d, %d records left", aad, left)
	}
	if got, err := v.Get(ctx, a.ID, "prod", "legacy_key"); err != nil || got != "sk_legacy" {
		t.Errorf("after re-wrap: %q %v", got, err)
	}

	// A legacy row nobody recorded (written after the migration) is refused.
	if err := v.PutLegacy(ctx, a.ID, "prod", "unrecorded", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(ctx, a.ID, "prod", "unrecorded"); !errors.Is(err, secrets.ErrLegacyContext) {
		t.Errorf("unrecorded legacy secret: %v", err)
	}
}
