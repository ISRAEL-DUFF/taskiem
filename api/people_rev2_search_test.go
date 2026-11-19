package api_test

import (
	"context"
	"testing"
	"time"
)

// C2: the search index's backfill of older messages keeps how far it
// got and goes on from there; a delete for everyone made while it runs
// leaves no tokens of the message, and a delete for me made while it runs
// is not an error.
func TestPeopleSearchBackfill(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031133001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031133002", "Bola Ade")
	adaChat, _ := friends(t, ada, bola, bolaID)
	alpha := send(t, ada, adaChat, "alpha one")
	bravo := send(t, ada, adaChat, "bravo two")
	charlie := send(t, ada, adaChat, "charlie three")
	tokens := func(id any) int {
		t.Helper()
		return w.adminCount(t, `SELECT count(*) FROM people_search WHERE person_id = $1 AND message_id = $2`, adaID, id)
	}
	// As if kept before the index: none of Ada's messages indexed.
	unindex := func() {
		t.Helper()
		for _, q := range []string{`DELETE FROM people_search WHERE person_id = $1`, `DELETE FROM people_search_ready WHERE person_id = $1`} {
			if _, err := w.env.DB.Admin.Exec(ctx, q, adaID); err != nil {
				t.Fatal(err)
			}
		}
	}
	found := func(q string) int {
		t.Helper()
		return len(ada.must(200, "GET", "/v1/me/people/search?q="+q, nil)["results"].([]any))
	}

	// Cut short after bravo: the next search goes on from there.
	unindex()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_search_ready (person_id, done_at, through_at, through_id)
		SELECT person_id, NULL, created_at, id FROM people_messages WHERE person_id = $1 AND id = $2`, adaID, bravo["id"]); err != nil {
		t.Fatal(err)
	}
	if found("charlie") != 1 || tokens(alpha["id"]) != 0 || tokens(bravo["id"]) != 0 || tokens(charlie["id"]) == 0 {
		t.Fatal("the backfill did not go on from where it got to")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_search_ready WHERE person_id = $1 AND done_at IS NOT NULL`, adaID); n != 1 {
		t.Fatal("the backfill is not marked done")
	}

	// whileBackfilling runs a search (and so the backfill) while another
	// transaction holds message id, then runs change in that transaction
	// once the backfill waits for it; answers the search's status.
	whileBackfilling := func(id any, change string) int {
		t.Helper()
		unindex()
		tx, err := w.env.DB.Admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT 1 FROM people_messages WHERE person_id = $1 AND id = $2 FOR UPDATE`, adaID, id); err != nil {
			t.Fatal(err)
		}
		code := make(chan int, 1)
		go func() {
			c, _ := ada.do("GET", "/v1/me/people/search?q=alpha", nil)
			code <- c
		}()
		deadline := time.Now().Add(10 * time.Second)
		for w.adminCount(t, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%FOR SHARE%'`) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the backfill did not hold the messages it indexes")
			}
			time.Sleep(50 * time.Millisecond)
		}
		if _, err := tx.Exec(ctx, change, adaID, id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return <-code
	}
	// Deleted for everyone (as forgetMessageTx empties a copy) meanwhile.
	if c := whileBackfilling(charlie["id"], `UPDATE people_messages SET deleted_at = now(), sealed = NULL WHERE person_id = $1 AND id = $2`); c != 200 {
		t.Fatalf("search during a delete for everyone: %d", c)
	}
	if n := tokens(charlie["id"]); n != 0 {
		t.Fatalf("a message deleted for everyone during the backfill kept %d tokens", n)
	}
	// Deleted for me meanwhile.
	if c := whileBackfilling(bravo["id"], `DELETE FROM people_messages WHERE person_id = $1 AND id = $2`); c != 200 {
		t.Fatalf("search during a delete for me: %d", c)
	}
	if found("alpha") != 1 {
		t.Fatal("the rest was not indexed")
	}
}
