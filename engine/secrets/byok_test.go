package secrets_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/byok/byoktest"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// clock is a settable clock shared by vaults in a test.
type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *clock) add(d time.Duration) { c.t.Add(int64(d)) }

func newClock() *clock {
	c := &clock{}
	c.t.Store(time.Now().UnixNano())
	return c
}

func (c *clock) vault(v *secrets.Vault) *secrets.Vault {
	v.Now = c.now
	return v
}

func byokVault(t *testing.T, d *dbtest.DB, f *byok.Factory, c *clock) *secrets.Vault {
	t.Helper()
	v := localVault(t, d)
	v.BYOK, v.BYOKCacheTTL, v.DestroyAfter = f, time.Minute, time.Millisecond
	return c.vault(v)
}

func auditActions(t *testing.T, d *dbtest.DB, tenant uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action FROM audit_log WHERE tenant_id = $1`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				return err
			}
			out[a]++
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sealSubject(t *testing.T, d *dbtest.DB, v *secrets.Vault, tenant uuid.UUID, value string) map[string]any {
	t.Helper()
	var env map[string]any
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		env, err = v.SealTx(ctx, tx, tenant, "bvn", value)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func openSubject(d *dbtest.DB, v *secrets.Vault, tenant uuid.UUID, env map[string]any) (any, error) {
	var out any
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = v.OpenTx(ctx, tx, tenant, env)
		return err
	})
	return out, err
}

// TestBYOKLifecycle walks a tenant from the platform key to a customer key
// and back: onboarding, background re-wrapping (S23), the second
// encryption context (S33), revocation and recovery, rotation, credential
// replacement and offboarding.
func TestBYOKLifecycle(t *testing.T) {
	d := dbtest.New(t)
	fake := byoktest.NewTransit(t)
	c := newClock()
	v := byokVault(t, d, byoktest.Factory(fake.Server), c)
	a := d.SeedTenant(t, nil)
	b := d.SeedTenant(t, nil)

	// Before: a secret from before migration 00100, one from after, a
	// connection, and personal data sealed under version 1.
	if err := v.PutLegacy(ctx, a.ID, "prod", "legacy_key", []byte("sk_legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Put(ctx, a.ID, "prod", "paystack_key", []byte("sk_live_A"), "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.CreateConnection(ctx, a.ID, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_conn"}, "u1"); err != nil {
		t.Fatal(err)
	}
	legacySubject, err := v.LegacySubjectID(ctx, a.ID, "bvn", "22212345678")
	if err != nil {
		t.Fatal(err)
	}
	envelope := sealSubject(t, d, v, a.ID, "22212345678")
	if envelope["subject"] != legacySubject {
		t.Fatalf("subject id changed with the pseudonym key: %v vs %v", envelope["subject"], legacySubject)
	}

	// A failed round trip stores nothing.
	cfg := fake.Config("customer")
	if _, _, err := v.EnableBYOK(ctx, a.ID, cfg, map[string]string{"token": "wrong"}, "owner"); !errors.Is(err, secrets.ErrVerify) {
		t.Fatalf("bad token onboarded: %v", err)
	}
	if st, _ := v.KeyStatus(ctx, a.ID); st.BYOK != nil || st.Mode != "platform" {
		t.Fatalf("status after failed onboarding: %+v", st)
	}

	key, ver, err := v.EnableBYOK(ctx, a.ID, cfg, map[string]string{"token": fake.Token}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if ver != 2 || key.Status != "active" || key.Provider != byok.VaultTransit || !strings.Contains(key.Description, "transit key transit/customer") {
		t.Fatalf("onboarded: %d %+v", ver, key)
	}
	var wrapped, creds string
	_ = d.Admin.QueryRow(ctx, `SELECT wrapped_kek FROM tenant_keys WHERE tenant_id = $1 AND version = 2`, a.ID).Scan(&wrapped)
	_ = d.Admin.QueryRow(ctx, `SELECT credentials FROM tenant_byok_keys WHERE tenant_id = $1`, a.ID).Scan(&creds)
	if !strings.HasPrefix(wrapped, "vault:v1:") {
		t.Errorf("version 2 not wrapped by the customer key: %q", wrapped)
	}
	if strings.Contains(creds, fake.Token) || creds == "" {
		t.Errorf("credentials stored readable: %q", creds)
	}

	// Re-wrapping moves everything onto version 2, upgrades the legacy
	// secret's context, seals the pseudonym key, and retires version 1.
	st, _ := v.KeyStatus(ctx, a.ID)
	if st.Rewrap == nil || st.Rewrap.Secrets != 3 || st.Rewrap.Subjects != 1 || st.CurrentVersion != 2 || st.Mode != "customer" {
		t.Fatalf("status before rewrap: %+v %+v", st, st.Rewrap)
	}
	r, err := v.RewrapAll(ctx, a.ID)
	if err != nil || r.Secrets != 3 || r.SubjectKeys != 1 || !r.Done || len(r.Retired) != 1 {
		t.Fatalf("rewrap: %+v %v", r, err)
	}
	c.add(time.Second)
	if r, err = v.RewrapAll(ctx, a.ID); err != nil || len(r.Destroyed) != 1 || r.Destroyed[0] != 1 {
		t.Fatalf("destroy: %+v %v", r, err)
	}
	var v1 string
	_ = d.Admin.QueryRow(ctx, `SELECT wrapped_kek FROM tenant_keys WHERE tenant_id = $1 AND version = 1`, a.ID).Scan(&v1)
	if v1 != "" {
		t.Errorf("version 1 not destroyed")
	}
	cold := byokVault(t, d, byoktest.Factory(fake.Server), c)
	for name, want := range map[string]string{"legacy_key": "sk_legacy", "paystack_key": "sk_live_A"} {
		if got, err := cold.Get(ctx, a.ID, "prod", name); err != nil || got != want {
			t.Errorf("%s after rewrap: %q %v", name, got, err)
		}
	}
	if cr, err := cold.Credentials(ctx, a.ID, "prod", "paystack", ""); err != nil || cr["secret_key"] != "sk_conn" {
		t.Errorf("connection after rewrap: %v %v", cr, err)
	}
	// Personal data stays readable, and its subject id stays the same
	// although version 1 is gone (S23).
	if pt, err := openSubject(d, cold, a.ID, envelope); err != nil || pt != "22212345678" {
		t.Errorf("envelope after rewrap: %v %v", pt, err)
	}
	if again := sealSubject(t, d, cold, a.ID, "22212345678"); again["subject"] != legacySubject {
		t.Errorf("subject id changed after rotation: %v", again["subject"])
	}
	var nOld int
	_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM secrets WHERE tenant_id = $1 AND (kek_version <> 2 OR aad_version <> 2)`, a.ID).Scan(&nOld)
	if nOld != 0 {
		t.Errorf("%d secrets left behind", nOld)
	}

	// S33: a row renamed or moved to another environment no longer decrypts.
	if _, err := d.Admin.Exec(ctx, `UPDATE secrets SET name = 'stolen' WHERE tenant_id = $1 AND name = 'paystack_key'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cold.Get(ctx, a.ID, "prod", "stolen"); err == nil {
		t.Error("renamed secret decrypted")
	}
	_, _ = d.Admin.Exec(ctx, `UPDATE secrets SET name = 'paystack_key' WHERE tenant_id = $1 AND name = 'stolen'`, a.ID)
	if _, err := d.Admin.Exec(ctx, `UPDATE secrets SET environment = 'dev' WHERE tenant_id = $1 AND name = 'legacy_key'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cold.Get(ctx, a.ID, "dev", "legacy_key"); err == nil {
		t.Error("secret moved to another environment decrypted")
	}
	_, _ = d.Admin.Exec(ctx, `UPDATE secrets SET environment = 'prod' WHERE tenant_id = $1 AND name = 'legacy_key'`, a.ID)

	// Credentials sealed for one tenant cannot be moved to another.
	if _, _, err := v.EnableBYOK(ctx, b.ID, cfg, map[string]string{"token": fake.Token}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenant_byok_keys SET credentials = (SELECT credentials FROM tenant_byok_keys WHERE tenant_id = $1) WHERE tenant_id = $2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	other := byokVault(t, d, byoktest.Factory(fake.Server), c)
	if _, err := other.Rotate(ctx, b.ID, "owner"); err == nil || !strings.Contains(err.Error(), "bound to another key") {
		t.Errorf("moved credentials used: %v", err)
	}

	// Revocation: a process with the tenant key cached keeps it for at most
	// the cache TTL; a fresh one fails at once. Both fail closed.
	if got, err := v.Get(ctx, a.ID, "prod", "paystack_key"); err != nil || got != "sk_live_A" {
		t.Fatalf("warm: %q %v", got, err)
	}
	fake.Revoke(true)
	if _, err := v.Get(ctx, a.ID, "prod", "paystack_key"); err != nil {
		t.Errorf("cached key refused within the TTL: %v", err)
	}
	fresh := byokVault(t, d, byoktest.Factory(fake.Server), c)
	if _, err := fresh.Get(ctx, a.ID, "prod", "paystack_key"); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Errorf("fresh process: %v", err)
	}
	c.add(2 * time.Minute)
	if _, err := v.Get(ctx, a.ID, "prod", "paystack_key"); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Errorf("cached key outlived the TTL: %v", err)
	}
	if _, err := openSubject(d, v, a.ID, envelope); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Errorf("personal data opened with the key revoked: %v", err)
	}
	if _, err := v.Put(ctx, a.ID, "prod", "new", []byte("x"), "u"); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Errorf("write with the key revoked: %v", err)
	}
	// Health: unavailable after two failed checks, audited once.
	h, err := v.CheckKey(ctx, a.ID, "system")
	if err != nil || h.OK || h.Status != "active" {
		t.Fatalf("first failed check: %+v %v", h, err)
	}
	h, _ = v.CheckKey(ctx, a.ID, "system")
	if h.OK || h.Status != "unavailable" || !h.Changed || !strings.Contains(h.Error, "permission denied") {
		t.Fatalf("second failed check: %+v", h)
	}
	h, _ = v.CheckKey(ctx, a.ID, "system")
	if h.Changed {
		t.Errorf("third check changed the status again: %+v", h)
	}
	st, _ = v.KeyStatus(ctx, a.ID)
	if st.BYOK.Status != "unavailable" || st.BYOK.FailingSince == nil || st.BYOK.LastError == "" {
		t.Errorf("status: %+v", st.BYOK)
	}
	if _, err := v.Rotate(ctx, a.ID, "owner"); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Errorf("rotation with the key revoked: %v", err)
	}

	// Recovery.
	fake.Revoke(false)
	c.add(time.Minute)
	h, _ = v.CheckKey(ctx, a.ID, "owner")
	if !h.OK || !h.Recovered || h.Status != "active" {
		t.Fatalf("recovery: %+v", h)
	}
	if got, err := v.Get(ctx, a.ID, "prod", "paystack_key"); err != nil || got != "sk_live_A" {
		t.Errorf("after recovery: %q %v", got, err)
	}

	// Rotation under the customer key; AppRole credentials replace the token.
	if ver, err := v.Rotate(ctx, a.ID, "owner"); err != nil || ver != 3 {
		t.Fatalf("rotate: %d %v", ver, err)
	}
	if _, err := v.ReplaceBYOKCredentials(ctx, a.ID, map[string]string{"role_id": fake.RoleID, "secret_id": "nope"}, "owner"); !errors.Is(err, secrets.ErrVerify) {
		t.Errorf("bad replacement accepted: %v", err)
	}
	if k, err := v.ReplaceBYOKCredentials(ctx, a.ID, map[string]string{"role_id": fake.RoleID, "secret_id": fake.SecretID}, "owner"); err != nil || k.CredentialsDigest == key.CredentialsDigest {
		t.Fatalf("replace credentials: %+v %v", k, err)
	}
	fake.Revoke(false)
	if _, err := byokVault(t, d, byoktest.Factory(fake.Server), c).Get(ctx, a.ID, "prod", "paystack_key"); err != nil {
		t.Errorf("with AppRole credentials: %v", err)
	}

	// Offboarding: back to the platform key once everything is re-wrapped;
	// the customer key's credentials are then forgotten.
	if ver, err := v.DisableBYOK(ctx, a.ID, "owner"); err != nil || ver != 4 {
		t.Fatalf("disable: %d %v", ver, err)
	}
	if _, err := v.RewrapAll(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	c.add(time.Second)
	if _, err := v.RewrapAll(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = v.KeyStatus(ctx, a.ID)
	if st.Mode != "platform" || st.BYOK != nil || len(st.RetiredBYOK) != 1 || st.Rewrap != nil || st.CurrentVersion != 4 {
		t.Fatalf("after offboarding: %+v", st)
	}
	_ = d.Admin.QueryRow(ctx, `SELECT credentials FROM tenant_byok_keys WHERE tenant_id = $1`, a.ID).Scan(&creds)
	if creds != "" {
		t.Error("retired customer key's credentials kept")
	}
	fake.Down(true) // the customer's KMS is no longer needed
	if got, err := byokVault(t, d, byoktest.Factory(fake.Server), c).Get(ctx, a.ID, "prod", "paystack_key"); err != nil || got != "sk_live_A" {
		t.Errorf("after offboarding: %q %v", got, err)
	}
	fake.Down(false)

	acts := auditActions(t, d, a.ID)
	for _, want := range []string{"key.byok_enabled", "key.rewrap", "key.rewrap_completed", "key.versions_destroyed", "key.byok_unavailable",
		"key.byok_recovered", "key.byok_checked", "secret.rotate_key", "key.byok_credentials_replaced", "key.byok_disabled", "key.byok_forgotten"} {
		if acts[want] == 0 {
			t.Errorf("no %s audit entry (have %v)", want, acts)
		}
	}
	if acts["key.byok_unavailable"] != 1 || acts["key.byok_recovered"] != 1 {
		t.Errorf("transitions audited more than once: %v", acts)
	}
}

// TestKeyJob checks the scheduler pass: it re-wraps due tenants, marks a
// revoked key unavailable, and resumes parked steps only once it works.
func TestKeyJob(t *testing.T) {
	d := dbtest.New(t)
	fake := byoktest.NewTransit(t)
	c := newClock()
	v := byokVault(t, d, byoktest.Factory(fake.Server), c)
	v.Pool = d.AppPool(t, 4)
	a := d.SeedTenant(t, nil)
	if _, err := v.Put(ctx, a.ID, "prod", "k", []byte("v"), "u1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.EnableBYOK(ctx, a.ID, fake.Config("customer"), map[string]string{"token": fake.Token}, "owner"); err != nil {
		t.Fatal(err)
	}
	var resumed []uuid.UUID
	var unavailable int
	job := &secrets.KeyJob{Vault: v, Resume: func(_ context.Context, tn uuid.UUID) (int, error) { resumed = append(resumed, tn); return 0, nil },
		Unavailable: func(uuid.UUID, secrets.Health) { unavailable++ }}
	if err := job.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var kv int
	_ = d.Admin.QueryRow(ctx, `SELECT kek_version FROM secrets WHERE tenant_id = $1`, a.ID).Scan(&kv)
	if kv != 2 || len(resumed) != 1 {
		t.Fatalf("job did not rewrap or resume: version %d, resumed %v", kv, resumed)
	}
	fake.Disable("customer", true)
	_ = job.Tick(ctx)
	_ = job.Tick(ctx)
	if unavailable != 2 || len(resumed) != 1 {
		t.Fatalf("revoked: unavailable %d, resumed %v", unavailable, resumed)
	}
	st, _ := v.KeyStatus(ctx, a.ID)
	if st.BYOK.Status != "unavailable" {
		t.Errorf("status %q", st.BYOK.Status)
	}
	fake.Disable("customer", false)
	if err := job.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 2 {
		t.Errorf("not resumed on recovery: %v", resumed)
	}
}
