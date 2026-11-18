package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00479 (You's live connection, typing, search, the trust line) goes up,
// down and up again: up, the copies' existing chats and messages have a
// changed_at, an update moves it, the new tables hold rows under row-level
// security, and taskiem_customer_business_trust exists for taskiem_app;
// down, all of it goes and the copies are as they were.
func TestPeopleLiveMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 478); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	const ada, bola = "00000000-0000-0000-0000-0000000000e1", "00000000-0000-0000-0000-0000000000e2"
	const chat, msg = "00000000-0000-0000-0000-0000000000f1", "00000000-0000-0000-0000-0000000000a1"
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada'), ('` + bola + `', 'bola@x.test', 'Bola')`,
		`INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me) VALUES
		   ('` + chat + `', '` + ada + `', '` + bola + `', taskiem_people_pair('` + ada + `', '` + bola + `'), true)`,
		`INSERT INTO people_messages (person_id, id, chat_id, conversation_id, mine, kind) VALUES
		   ('` + ada + `', '` + msg + `', '` + chat + `', taskiem_people_pair('` + ada + `', '` + bola + `'), true, 'text')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := p.UpTo(ctx, 479); err != nil {
			t.Fatal(err)
		}
		var before, after time.Time
		if err := conn.QueryRow(ctx, `SELECT changed_at FROM people_messages WHERE id = '`+msg+`'`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `UPDATE people_messages SET read_at = now() WHERE id = '`+msg+`' RETURNING changed_at`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if !after.After(before) {
			t.Fatalf("changed_at did not move: %v %v", before, after)
		}
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relname IN ('people_gone', 'person_live', 'people_search_ready', 'people_typing', 'people_search')
			AND relrowsecurity AND relforcerowsecurity`).Scan(&n); err != nil || n != 5 {
			t.Fatalf("tables with row-level security: %d %v", n, err)
		}
		for _, q := range []string{
			`INSERT INTO people_search (person_id, message_id, conversation_id, token) VALUES ('` + ada + `', '` + msg + `', taskiem_people_pair('` + ada + `', '` + bola + `'), '\x00112233445566778899aabbccddeeff')`,
			`INSERT INTO people_typing (person_id, chat_id, conversation_id, until) VALUES ('` + ada + `', '` + chat + `', taskiem_people_pair('` + ada + `', '` + bola + `'), now())`,
			`INSERT INTO people_gone (person_id, chat_id) VALUES ('` + ada + `', '` + chat + `')`,
		} {
			if _, err := conn.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		// A message deleted takes its index with it.
		if _, err := conn.Exec(ctx, `DELETE FROM people_messages WHERE id = '`+msg+`'`); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM people_search`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the index outlived its message: %d %v", n, err)
		}
		var granted bool
		if err := conn.QueryRow(ctx, `SELECT has_function_privilege('taskiem_app', 'taskiem_customer_business_trust(uuid[])', 'EXECUTE')`).Scan(&granted); err != nil || !granted {
			t.Fatalf("taskiem_customer_business_trust: %v %v", granted, err)
		}
		if _, err := p.DownTo(ctx, 478); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('people_chats', 'people_messages') AND column_name = 'changed_at'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("changed_at after Down: %d %v", n, err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_customer_business_trust', 'taskiem_people_changed')`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("functions after Down: %d %v", n, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO people_messages (person_id, id, chat_id, conversation_id, mine, kind) VALUES
		   ('`+ada+`', '`+msg+`', '`+chat+`', taskiem_people_pair('`+ada+`', '`+bola+`'), true, 'text')`); err != nil {
			t.Fatal(err)
		}
	}
}
