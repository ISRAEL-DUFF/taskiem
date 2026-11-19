package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00477 (the stage 5a review's fixes) goes up, down and up again: up, a
// copy someone wrote into has peer_wrote_at from its messages, a copy
// opened and never written in is empty, a share card may warn as a
// transfer and taskiem_customer_business_photos exists; down, all of it
// goes (a transfer warning becomes a warning).
func TestPeopleReviewMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 476); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	const ada, bola, chidi = "00000000-0000-0000-0000-0000000000e1", "00000000-0000-0000-0000-0000000000e2", "00000000-0000-0000-0000-0000000000e3"
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada'), ('` + bola + `', 'bola@x.test', 'Bola'), ('` + chidi + `', 'chidi@x.test', 'Chidi')`,
		// Ada wrote to Bola; Bola's copy has her message. Ada opened a chat
		// with Chidi and wrote nothing.
		`INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, waiting_at) VALUES
		   ('00000000-0000-0000-0000-0000000000f1', '` + ada + `', '` + bola + `', taskiem_people_pair('` + ada + `', '` + bola + `'), true, now()),
		   ('00000000-0000-0000-0000-0000000000f2', '` + bola + `', '` + ada + `', taskiem_people_pair('` + ada + `', '` + bola + `'), false, NULL)`,
		`INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me) VALUES
		   ('00000000-0000-0000-0000-0000000000f3', '` + ada + `', '` + chidi + `', taskiem_people_pair('` + ada + `', '` + chidi + `'), true)`,
		`INSERT INTO people_messages (person_id, id, chat_id, conversation_id, mine, kind, screen) VALUES
		   ('` + bola + `', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000f2', taskiem_people_pair('` + ada + `', '` + bola + `'), false, 'text', 'warn')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.UpTo(ctx, 477); err != nil {
		t.Fatal(err)
	}
	var wrote, emptyAda, emptyChidi bool
	if err := conn.QueryRow(ctx, `SELECT (SELECT peer_wrote_at IS NOT NULL FROM people_chats WHERE id = '00000000-0000-0000-0000-0000000000f2'),
		(SELECT empty FROM people_chats WHERE id = '00000000-0000-0000-0000-0000000000f1'), (SELECT empty FROM people_chats WHERE id = '00000000-0000-0000-0000-0000000000f3')`).
		Scan(&wrote, &emptyAda, &emptyChidi); err != nil || !wrote || emptyAda || !emptyChidi {
		t.Fatalf("after Up: wrote %v, Ada's empty %v, Chidi's empty %v, %v", wrote, emptyAda, emptyChidi, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE people_messages SET screen = 'transfer' WHERE id = '00000000-0000-0000-0000-0000000000a1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'taskiem_customer_business_photos'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("taskiem_customer_business_photos after Up: %d %v", n, err)
	}
	if _, err := p.DownTo(ctx, 476); err != nil {
		t.Fatal(err)
	}
	var screen string
	if err := conn.QueryRow(ctx, `SELECT screen FROM people_messages WHERE id = '00000000-0000-0000-0000-0000000000a1'`).Scan(&screen); err != nil || screen != "warn" {
		t.Fatalf("after Down: %q %v", screen, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'people_chats' AND column_name IN ('peer_wrote_at', 'empty')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("columns after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 477); err != nil {
		t.Fatal(err)
	}
}
