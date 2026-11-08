package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A table that keeps its rows for a time (decision 0062 V5): set when it
// is made or later, shown on the table, and its older rows deleted by the
// sweep, exactly those, oldest first; a conversations table follows how
// long the business keeps conversations, within the plan.
func TestRowExpiry(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	notes := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "notes", "keep_days": 10,
		"columns": []map[string]any{{"name": "text", "type": "text"}}})
	if notes["keep_days"] != float64(10) || notes["keep_with_conversations"] != false {
		t.Fatalf("made: %v", notes)
	}
	conv := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "conversations", "keep_with_conversations": true,
		"columns": []map[string]any{{"name": "sender", "type": "text", "personal": true}, {"name": "text", "type": "text", "personal": true}}})
	plain := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "customers", "columns": []map[string]any{{"name": "name", "type": "text"}}})
	if plain["keep_days"] != nil {
		t.Fatalf("an ordinary table keeps rows until deleted: %v", plain)
	}
	owner.must(400, "PATCH", "/v1/tables/"+notes["id"].(string), map[string]any{"keep_days": 0})

	project := w.pg.Projects()[0]
	day := func(n int) string { return time.Now().Add(-time.Duration(n) * 24 * time.Hour).UTC().Format(time.RFC3339) }
	tx := w.pg.Begin(project)
	for i, age := range []int{40, 20, 12, 5, 1} {
		tx.Insert("notes", map[string]any{"id": int64(i + 1), "text": fmt.Sprint("note ", age), "created_at": day(age), "updated_at": day(age)})
		tx.Insert("conversations", map[string]any{"id": int64(i + 1), "sender": "234803", "text": fmt.Sprint("said ", age), "created_at": day(age), "updated_at": day(age)})
		tx.Insert("customers", map[string]any{"id": int64(i + 1), "name": fmt.Sprint("c", age), "created_at": day(age), "updated_at": day(age)})
	}
	tx.Commit()

	n, err := w.srv.ExpireRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// notes: 40, 20 and 12 days old go (kept 10); conversations: 40 goes
	// (kept 30 by default); customers keep everything.
	if n != 4 || len(w.pg.Rows(project, "notes")) != 2 || len(w.pg.Rows(project, "conversations")) != 4 || len(w.pg.Rows(project, "customers")) != 5 {
		t.Fatalf("deleted %d: notes %d, conversations %d, customers %d", n, len(w.pg.Rows(project, "notes")), len(w.pg.Rows(project, "conversations")), len(w.pg.Rows(project, "customers")))
	}
	// The owner keeps conversations for 7 days: two more go.
	v := owner.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false, "conversation_days": 7})
	if v["conversation_days"] != float64(7) {
		t.Fatalf("set: %v", v)
	}
	owner.must(400, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false, "conversation_days": 0})
	if n, err := w.srv.ExpireRows(context.Background()); err != nil || n != 2 || len(w.pg.Rows(project, "conversations")) != 2 {
		t.Fatalf("after 7 days: %d %v, %d left", n, err, len(w.pg.Rows(project, "conversations")))
	}
	// Changed to keep until deleted: nothing more goes.
	got := owner.must(200, "PATCH", "/v1/tables/"+conv["id"].(string), map[string]any{"keep_with_conversations": false})
	if got["keep_with_conversations"] != false || got["keep_days"] != nil {
		t.Fatalf("patched: %v", got)
	}
	tx = w.pg.Begin(project)
	tx.Insert("conversations", map[string]any{"id": int64(9), "sender": "234803", "text": "old", "created_at": day(90), "updated_at": day(90)})
	tx.Commit()
	if n, _ := w.srv.ExpireRows(context.Background()); n != 0 {
		t.Fatalf("a table kept until deleted lost %d rows", n)
	}
}
