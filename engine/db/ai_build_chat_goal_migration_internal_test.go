package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration 00398 blanks the goal a chat build kept in the clear and the
// words of its model calls (finding F50b), keeping their tokens and
// outcome; builds from the web and template picks keep theirs.
func TestAIBuildChatGoalMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 397); err != nil {
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
	build := func(id, channel, goal string, template any) {
		t.Helper()
		exec(`INSERT INTO ai_builds (id, tenant_id, actor, goal, provider, model, channel, template_id) VALUES ($1, $2, 'user:a', $3, 'fake', 'm', $4, $5)`,
			id, tenant, goal, channel, template)
		exec(`INSERT INTO ai_interactions (id, tenant_id, build_id, round, kind, actor, provider, model, system_digest, messages, response, outcome, error, total_tokens)
			VALUES (gen_random_uuid(), $1, $2, 1, 'draft', 'user:a', 'fake', 'm', 'd', jsonb_build_array(jsonb_build_object('role', 'user', 'text', $3::text)),
			'{"summary":"pay Chinedu"}', 'valid', NULL, 320)`, tenant, id, goal)
	}
	build("00000000-0000-0000-0000-0000000000b1", "whatsapp", "pay Chinedu Okafor every Friday", nil)
	build("00000000-0000-0000-0000-0000000000b2", "web", "pay Chinedu Okafor every Friday", nil)
	build("00000000-0000-0000-0000-0000000000b3", "whatsapp", "template debtor-reminder-sms", "debtor-reminder-sms")
	if _, err := p.UpTo(ctx, 398); err != nil {
		t.Fatal(err)
	}
	var chatWords, webKept, tplKept, tokens int
	if err := conn.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE b.channel = 'whatsapp' AND b.template_id IS NULL AND (b.goal <> '' OR i.messages <> '[]'::jsonb OR i.response IS NOT NULL)),
		count(*) FILTER (WHERE b.channel = 'web' AND b.goal LIKE '%Chinedu%' AND i.messages::text LIKE '%Chinedu%' AND i.response IS NOT NULL),
		count(*) FILTER (WHERE b.template_id IS NOT NULL AND b.goal <> '' AND i.messages <> '[]'::jsonb),
		sum(i.total_tokens)
		FROM ai_builds b JOIN ai_interactions i ON i.build_id = b.id`).Scan(&chatWords, &webKept, &tplKept, &tokens); err != nil {
		t.Fatal(err)
	}
	if chatWords != 0 || webKept != 1 || tplKept != 1 || tokens != 3*320 {
		t.Fatalf("after 00398: %d chat rows with words, %d web rows kept, %d template rows kept, %d tokens", chatWords, webKept, tplKept, tokens)
	}
}
