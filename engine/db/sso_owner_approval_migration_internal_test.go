package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00325 (F39b) counts an existing single sign-on connection as an owner's
// only when the member who created it holds owner in that tenant: one an
// administrator, or a key, added stays unapproved, so it signs in no owner.
func TestSSOOwnerApprovalMigration(t *testing.T) {
	dsn := emptyDatabase(t)
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer func() { _ = sqlDB.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 324); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	const (
		acme  = "00000000-0000-0000-0000-0000000000a1"
		other = "00000000-0000-0000-0000-0000000000a2"
		owner = "00000000-0000-0000-0000-0000000000b1"
		admin = "00000000-0000-0000-0000-0000000000b2"
	)
	for _, q := range []string{
		`INSERT INTO tenants (id, name, plan_id) VALUES ('` + acme + `', 'Acme', gen_random_uuid()), ('` + other + `', 'Other', gen_random_uuid())`,
		`INSERT INTO users (id, email, name) VALUES ('` + owner + `', 'owner@acme.test', 'O'), ('` + admin + `', 'adm@acme.test', 'A')`,
		// The owner of Acme is only an administrator in Other.
		`INSERT INTO memberships (tenant_id, user_id, role) VALUES ('` + acme + `', '` + owner + `', 'owner'), ('` + acme + `', '` + admin + `', 'admin'),
			('` + other + `', '` + owner + `', 'admin')`,
		`INSERT INTO sso_connections (id, tenant_id, protocol, name, config, created_by) VALUES
			('00000000-0000-0000-0000-0000000000c1', '` + acme + `', 'saml', 'owner''s', '{}', '` + owner + `'),
			('00000000-0000-0000-0000-0000000000c2', '` + acme + `', 'saml', 'admin''s', '{}', '` + admin + `'),
			('00000000-0000-0000-0000-0000000000c3', '` + acme + `', 'saml', 'a key''s', '{}', 'key:00000000-0000-0000-0000-0000000000d1'),
			('00000000-0000-0000-0000-0000000000c4', '` + other + `', 'saml', 'owner as admin', '{}', '` + owner + `')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.UpTo(ctx, 325); err != nil {
		t.Fatal(err)
	}
	var approved []string
	if err := conn.QueryRow(ctx, `SELECT array_agg(name ORDER BY name) FROM sso_connections WHERE owner_approved_at IS NOT NULL`).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if len(approved) != 1 || approved[0] != "owner's" {
		t.Fatalf("approved by the migration: %v", approved)
	}
}
