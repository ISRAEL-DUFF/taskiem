package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/catalogue/cataloguetest"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func cli(args ...string) (string, error) {
	var out bytes.Buffer
	err := run(args, &out, &out)
	return out.String(), err
}

// TestConnectorSDKFlow is a publisher's day: scaffold a catalogue
// connector, build it, lint it, run its conformance cases in the sandbox,
// make a key, package and sign it, and verify the package.
func TestConnectorSDKFlow(t *testing.T) {
	// Inside this module, so the scaffold builds without fetching the SDK;
	// a leading underscore keeps it out of ./... patterns.
	dir, err := os.MkdirTemp(".", "_conntest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	conn := filepath.Join(dir, "ledger")
	if out, err := cli("connector", "init", "--publisher", "acme", "--name", "Acme ledger", conn); err != nil || !strings.Contains(out, "p_acme_ledger") || strings.Contains(out, "go get") {
		t.Fatalf("init: %v\n%s", err, out)
	}
	manifest, _ := os.ReadFile(filepath.Join(conn, "manifest.yaml"))
	if !strings.Contains(string(manifest), "id: p_acme_ledger") || !strings.Contains(string(manifest), "name: Acme ledger") {
		t.Fatalf("scaffolded manifest:\n%s", manifest)
	}
	if out, err := cli("connector", "init", conn); err == nil {
		t.Errorf("init into a non-empty directory: %s", out)
	}
	if out, err := cli("connector", "test", conn); err == nil || !strings.Contains(err.Error(), "connector build") {
		t.Errorf("test before build: %v %s", err, out)
	}
	if out, err := cli("connector", "build", conn); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	module := filepath.Join(conn, "connector.wasm")
	if out, err := cli("connector", "validate", filepath.Join(conn, "manifest.yaml"), module); err != nil || !strings.Contains(out, "p_acme_ledger 1.0.0 is valid") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if out, err := cli("connector", "validate", "--publisher", "globex", filepath.Join(conn, "manifest.yaml")); err == nil || !strings.Contains(out, "p_globex_<name>") {
		t.Errorf("validate in another namespace: %v\n%s", err, out)
	}
	if out, err := cli("connector", "test", "-v", conn); err != nil || !strings.Contains(out, "6 conformance case(s) passed") {
		t.Fatalf("test: %v\n%s", err, out)
	}
	// A broken expectation fails the suite with the reason.
	caseFile := filepath.Join(conn, "testdata", "conformance", "get_balance.json")
	orig, _ := os.ReadFile(caseFile)
	_ = os.WriteFile(caseFile, bytes.Replace(orig, []byte("125000"), []byte("1"), 1), 0o600)
	if out, err := cli("connector", "test", conn); err == nil || !strings.Contains(out, "does not match") {
		t.Errorf("failing case: %v\n%s", err, out)
	}
	_ = os.WriteFile(caseFile, orig, 0o600)

	keyFile := filepath.Join(dir, "publisher.key")
	out, err := cli("connector", "keygen", "-o", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pub := regexp.MustCompile(`public key: (\S+)`).FindStringSubmatch(out)[1]
	if out, err := cli("connector", "keygen", "-o", keyFile); err == nil {
		t.Errorf("keygen overwrote a key: %s", out)
	}
	if out, err := cli("connector", "package", "--key", keyFile, "--licence", "GPL-3.0-only", "--contact", "dev@acme.test", "--original", conn); err == nil {
		t.Errorf("a refused licence packaged: %s", out)
	}
	pkg := filepath.Join(dir, "ledger.tcpkg")
	if out, err := cli("connector", "package", "--key", keyFile, "--licence", "Apache-2.0", "--contact", "dev@acme.test", "--original", "-o", pkg, conn); err != nil || !strings.Contains(out, "packaged p_acme_ledger 1.0.0") {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if out, err := cli("connector", "verify", "--public-key", pub, pkg); err != nil || !strings.Contains(out, "signature verifies") || !strings.Contains(out, "6 conformance case(s)") {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	_, other := cataloguetest.Key()
	if out, err := cli("connector", "verify", "--public-key", other, pkg); err == nil {
		t.Errorf("verified with another key: %s", out)
	}
	raw, _ := os.ReadFile(pkg)
	_ = os.WriteFile(pkg, bytes.Replace(raw, []byte(`"licence": "Apache-2.0"`), []byte(`"licence": "MIT"`), 1), 0o600)
	if out, err := cli("connector", "verify", pkg); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("tampered package: %v %s", err, out)
	}
}

// TestCatalogueOperatorCLI: reviewers, publisher verification, the queue,
// a four-eyes review and a revocation that alerts an installing tenant.
func TestCatalogueOperatorCLI(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("USER", "ops")
	publisher := d.SeedTenant(t, nil)
	installer := d.SeedTenant(t, nil)
	key, _ := cataloguetest.Key()
	pkg := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	pubKey := key.Public()
	id := uuid.Must(uuid.NewV7())
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{publisher.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO connector_publishers (tenant_id, slug, name, public_key, key_id, requested_by) VALUES ($1, 'acme', 'Acme', $2, 'k', 'u')`,
			publisher.ID, pubKey)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	insert := func() error {
		return db.InTenantTx(ctx, d.App, []uuid.UUID{publisher.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO catalogue_versions (id, publisher_tenant, publisher, connector_id, version, manifest, module, module_digest, package_digest,
				key_id, signature, licence, conformance, attestation, checks, state, submitted_by)
				VALUES ($1, $2, 'acme', 'p_acme_ledger', '1.0.0', $3, $4, sha256($4), sha256($4), 'k', '\x01', 'MIT', '{}', '{}', '{"passed":true}', 'in_review', 'u')`,
				id, publisher.ID, pkg.Manifest, pkg.Module)
			return err
		})
	}
	if err := insert(); err == nil || !strings.Contains(err.Error(), "verified namespace") {
		t.Fatalf("submitted before verification: %v", err)
	}
	if out, err := cli("catalogue", "publishers", "verify", "acme"); err != nil || !strings.Contains(out, "acme is verified") {
		t.Fatalf("verify: %v %s", err, out)
	}
	if err := insert(); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("catalogue", "queue"); err != nil || !strings.Contains(out, id.String()) || !strings.Contains(out, "p_acme_ledger") {
		t.Fatalf("queue: %v\n%s", err, out)
	}
	if out, err := cli("catalogue", "show", id.String()); err != nil || !strings.Contains(out, "create_payment: reconcilable_write") || !strings.Contains(out, "review checklist") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if out, err := cli("catalogue", "review", id.String(), "--as", "rev@taskiem.test", "--approve", "--confirm", "all", "--note", "ok"); err == nil || !strings.Contains(err.Error(), "not a catalogue reviewer") {
		t.Errorf("unlisted reviewer: %v %s", err, out)
	}
	if out, err := cli("catalogue", "reviewers", "add", "rev@taskiem.test"); err != nil || !strings.Contains(out, "rev@taskiem.test") {
		t.Fatalf("reviewers: %v %s", err, out)
	}
	if out, err := cli("catalogue", "review", id.String(), "--as", "rev@taskiem.test", "--approve", "--confirm", "classes,hosts", "--note", "ok"); err == nil || !strings.Contains(err.Error(), "not confirmed: identity") {
		t.Errorf("partial checklist: %v %s", err, out)
	}
	if out, err := cli("catalogue", "review", id.String(), "--as", "rev@taskiem.test", "--approve", "--confirm", "all", "--note", "Checked against the provider's docs"); err != nil || !strings.Contains(out, "approved") {
		t.Fatalf("review: %v %s", err, out)
	}
	// The publisher publishes; a tenant installs; a reviewer revokes.
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{publisher.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE catalogue_versions SET state = 'published', published_at = now() WHERE id = $1`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{installer.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO alert_channels (id, tenant_id, kind, name, config, created_by) VALUES ($1, $2, 'email', 'ops', '{"to":["ops@installer.test"]}', 'u')`,
			uuid.Must(uuid.NewV7()), installer.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO catalogue_installs (tenant_id, connector_id, major, version, consent, installed_by) VALUES ($1, 'p_acme_ledger', 1, '1.0.0', '{}', 'u')`, installer.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("catalogue", "revoke", "p_acme_ledger", "1.0.0", "--as", "rev@taskiem.test"); err == nil {
		t.Errorf("revoke without a reason: %s", out)
	}
	if out, err := cli("catalogue", "revoke", "p_acme_ledger", "1.0.0", "--as", "rev@taskiem.test", "--reason", "exfiltrates credentials"); err != nil || !strings.Contains(out, "alerted 1 installing tenant") {
		t.Fatalf("revoke: %v %s", err, out)
	}
	var deliveries, audits int
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{installer.ID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_deliveries d JOIN alerts a ON a.id = d.alert_id WHERE a.kind = 'connector_revoked'`).Scan(&deliveries); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'catalogue.revoked'`).Scan(&audits)
	}); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || audits != 1 {
		t.Errorf("installer: %d deliveries, %d audit entries", deliveries, audits)
	}
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action IN ('catalogue.publisher.verify', 'catalogue.approve', 'catalogue.revoke')`, publisher.ID).Scan(&audits); err != nil || audits != 3 {
		t.Errorf("publisher's audit entries: %d %v", audits, err)
	}
}
