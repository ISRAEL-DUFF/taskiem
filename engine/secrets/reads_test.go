package secrets_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

func reads(t *testing.T, d *dbtest.DB, tenant uuid.UUID, f secrets.ReadFilter) []secrets.Read {
	t.Helper()
	var out []secrets.Read
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = secrets.ListReads(ctx, tx, f)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadsRecordedIsolatedAndAppendOnly(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	run := uuid.Must(uuid.NewV7())
	if _, err := v.Put(ctx, a.ID, "prod", "api_key", []byte("sk_live_A"), "u1"); err != nil {
		t.Fatal(err)
	}
	conn, err := v.CreateConnection(ctx, a.ID, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_conn"}, "u1")
	if err != nil {
		t.Fatal(err)
	}
	// Writing and checking existence decrypt nothing.
	if ok, err := v.Exists(ctx, a.ID, "prod", "api_key"); err != nil || !ok {
		t.Fatalf("exists: %v %v", ok, err)
	}
	if n := len(reads(t, d, a.ID, secrets.ReadFilter{})); n != 0 {
		t.Fatalf("%d reads before any use", n)
	}

	use := secrets.WithUse(ctx, secrets.Use{Purpose: "step.http", RunID: run, StepID: "post", Attempt: 2})
	if _, err := v.Get(use, a.ID, "prod", "api_key"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Credentials(secrets.WithUse(ctx, secrets.Use{Purpose: "step.connector", RunID: run, StepID: "pay", Attempt: 1}), a.ID, "prod", "paystack", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(ctx, a.ID, "prod", "api_key"); err != nil { // no Use: still recorded
		t.Fatal(err)
	}
	if _, err := v.Get(use, a.ID, "prod", "missing"); err == nil {
		t.Fatal("missing secret read")
	}

	all := reads(t, d, a.ID, secrets.ReadFilter{})
	if len(all) != 3 {
		t.Fatalf("%d reads, want 3: %+v", len(all), all)
	}
	byRun := reads(t, d, a.ID, secrets.ReadFilter{Run: run})
	if len(byRun) != 2 {
		t.Fatalf("%d reads for the run", len(byRun))
	}
	for _, r := range byRun {
		switch {
		case r.Kind == "secret" && r.Name == "api_key" && r.Purpose == "step.http" && *r.StepID == "post" && *r.Attempt == 2:
		case r.Kind == "connection" && r.Name == "main" && *r.ConnectionID == conn && *r.Connector == "paystack" && *r.StepID == "pay" && *r.Attempt == 1:
		default:
			t.Errorf("unexpected read %+v", r)
		}
	}
	if r := reads(t, d, a.ID, secrets.ReadFilter{Name: "api_key"}); len(r) != 2 || r[0].Purpose != "unspecified" || r[0].Actor != "system" || r[0].RunID != nil {
		t.Errorf("by name: %+v", r)
	}
	if r := reads(t, d, a.ID, secrets.ReadFilter{Connection: conn}); len(r) != 1 {
		t.Errorf("by connection: %+v", r)
	}
	if r := reads(t, d, a.ID, secrets.ReadFilter{Since: time.Now().Add(time.Hour)}); len(r) != 0 {
		t.Errorf("since the future: %+v", r)
	}
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), "sk_live_A") || strings.Contains(string(raw), "sk_conn") {
		t.Error("a value was recorded")
	}

	// Tenant B sees none of A's reads, and cannot record into A.
	if r := reads(t, d, b.ID, secrets.ReadFilter{}); len(r) != 0 {
		t.Errorf("tenant b sees %d reads", len(r))
	}
	err = db.InTenantTx(ctx, d.App, []uuid.UUID{b.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO secret_reads (id, tenant_id, kind, environment, name, purpose, actor) VALUES ($1, $2, 'secret', 'prod', 'x', 'p', 'system')`,
			uuid.New(), a.ID)
		return err
	})
	if err == nil {
		t.Error("tenant b recorded a read for tenant a")
	}

	// The application can neither change nor remove a read.
	for _, q := range []string{`UPDATE secret_reads SET purpose = 'nothing'`, `DELETE FROM secret_reads`, `UPDATE secret_read_digests SET reads = 0`, `DELETE FROM secret_read_digests`} {
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: %v", q, err)
		}
	}
	// Not even the schema owner changes one.
	if _, err := d.Admin.Exec(ctx, `UPDATE secret_reads SET purpose = 'nothing'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("owner update: %v", err)
	}
	// Retention never removes recent reads.
	var purged int
	if err := d.App.QueryRow(ctx, `SELECT taskiem_secret_reads_purge('1 second', 1000)`).Scan(&purged); err != nil || purged != 0 {
		t.Errorf("purge: %d %v", purged, err)
	}
}

func TestReadDigestsInAuditChain(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	for _, tn := range []uuid.UUID{a.ID, b.ID} {
		if _, err := v.Put(ctx, tn, "prod", "api_key", []byte("sk"), "u1"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := v.Get(secrets.WithUse(ctx, secrets.Use{Purpose: "step.http"}), a.ID, "prod", "api_key"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.Get(ctx, b.ID, "prod", "api_key"); err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)

	// The current hour is not closed: nothing is digested yet.
	x := &secrets.ReadDigester{Pool: d.App}
	if n, err := x.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("open hour digested: %d %v", n, err)
	}
	x.Now = func() time.Time { return hour.Add(time.Hour + 20*time.Minute) }
	if n, err := x.Tick(ctx); err != nil || n != 2 {
		t.Fatalf("digests: %d %v", n, err)
	}
	if n, err := x.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("digested twice: %d %v", n, err)
	}

	verify := func() []secrets.DigestCheck {
		t.Helper()
		var out []secrets.DigestCheck
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			var err error
			out, err = secrets.VerifyDigests(ctx, tx, a.ID, hour.Add(-time.Hour), hour.Add(2*time.Hour), 0)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The digest is in the chain, which still verifies, and matches the rows.
	var detail []byte
	var broken *int64
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT detail FROM audit_log WHERE action = 'secret.read.digest'`).Scan(&detail); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT taskiem_audit_verify($1)`, a.ID).Scan(&broken)
	})
	if err != nil || broken != nil {
		t.Fatalf("chain: broken at %v, %v", broken, err)
	}
	var dg secrets.Digest
	if err := json.Unmarshal(detail, &dg); err != nil || dg.Reads != 3 || len(dg.Secrets) != 1 || dg.Secrets[0].Name != "api_key" || dg.Secrets[0].Reads != 3 || !dg.Hour.Equal(hour) {
		t.Fatalf("digest %s: %v", detail, err)
	}
	if c := verify(); len(c) != 1 || c[0].Status != "ok" || c[0].Recorded != 3 || c[0].Found != 3 {
		t.Fatalf("verify: %+v", c)
	}

	// A read removed behind the application's back no longer matches.
	if _, err := d.Admin.Exec(ctx, `DELETE FROM secret_reads WHERE id = (SELECT id FROM secret_reads WHERE tenant_id = $1 LIMIT 1)`, a.ID); err != nil {
		t.Fatal(err)
	}
	if c := verify(); len(c) != 1 || c[0].Status != "mismatch" || c[0].Found != 2 {
		t.Fatalf("after tampering: %+v", c)
	}
}
