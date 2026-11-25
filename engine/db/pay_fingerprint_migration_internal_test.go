package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00740 (paying by fingerprint) goes up, down and up again; it adds its
// phone code's purpose to the list and takes only that one away.
func TestPayFingerprintMigrationUpDownUp(t *testing.T) {
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
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	state := func() (bool, string) {
		t.Helper()
		var has bool
		var def string
		if err := conn.QueryRow(ctx, `SELECT to_regclass('pay_keys') IS NOT NULL,
			(SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'phone_codes'::regclass AND conname = 'phone_codes_purpose_check')`).Scan(&has, &def); err != nil {
			t.Fatal(err)
		}
		return has, def
	}
	if _, err := p.UpTo(ctx, 739); err != nil {
		t.Fatal(err)
	}
	_, before := state()
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 740); err != nil {
			t.Fatal(err)
		}
		has, def := state()
		if !has || !strings.Contains(def, "'pay_key'") || !strings.Contains(def, "'change_old'") {
			t.Fatalf("round %d, up: %v %s", round, has, def)
		}
		if _, err := p.DownTo(ctx, 739); err != nil {
			t.Fatal(err)
		}
		if has, def := state(); has || def != before {
			t.Fatalf("round %d, down: %v %s, want %s", round, has, def, before)
		}
	}
}
