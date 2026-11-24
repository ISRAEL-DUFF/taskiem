package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration 00393 blanks the words chats' model calls left in
// ai_interactions (finding F50), keeping their tokens and outcome, and
// leaves builds' records as they were.
func TestAIChatTextMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 392); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	tenant := "00000000-0000-0000-0000-0000000000a1"
	exec(`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Acme', gen_random_uuid())`, tenant)
	exec(`INSERT INTO ai_builds (id, tenant_id, actor, goal, provider, model) VALUES ('00000000-0000-0000-0000-0000000000b1', $1, 'user:a', 'pay Chinedu', 'fake', 'm')`, tenant)
	row := func(kind, outcome, build, msg, resp, errText string) {
		t.Helper()
		exec(`INSERT INTO ai_interactions (id, tenant_id, build_id, round, kind, actor, provider, model, system_digest, messages, response, outcome, error, total_tokens)
			VALUES (gen_random_uuid(), $1, NULLIF($2, '')::uuid, 0, $3, 'user:a', 'fake', 'm', 'd', jsonb_build_array(jsonb_build_object('role', 'user', 'text', $4::text)),
			$5, $6, NULLIF($7, ''), 320)`, tenant, build, kind, msg, resp, outcome, errText)
	}
	row("intent", "valid", "", "Chinedu Okafor for Ikeja owe me 15k", `{"intent":"owes","argument":"Chinedu Okafor owes 15000"}`, "")
	row("table_query", "refused", "", "who owes in Ikeja", `{"table":"customers"}`, `the column "Chinedu" is not in the table`)
	row("table_query", "error", "", "who owes in Lekki", "", "the provider is overloaded")
	row("draft", "valid", "00000000-0000-0000-0000-0000000000b1", "pay Chinedu", "{}", "")
	if _, err := p.UpTo(ctx, 393); err != nil {
		t.Fatal(err)
	}
	var words, kept, tokens, errs int
	if err := conn.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind <> 'draft' AND (messages <> '[]'::jsonb OR response IS NOT NULL)),
		count(*) FILTER (WHERE kind = 'draft' AND messages::text LIKE '%Chinedu%' AND response = '{}'),
		sum(total_tokens), count(error)
		FROM ai_interactions`).Scan(&words, &kept, &tokens, &errs); err != nil {
		t.Fatal(err)
	}
	if words != 0 || kept != 1 || tokens != 4*320 || errs != 1 {
		t.Fatalf("after 00393: %d chat rows with words, %d build rows kept, %d tokens, %d errors", words, kept, tokens, errs)
	}
}
