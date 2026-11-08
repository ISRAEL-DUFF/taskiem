package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
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
	day := func(n int) string {
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour).UTC().Format(time.RFC3339)
	}
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

// Erasing a customer by their number deletes what they said to the
// business's assistant, in whichever form the number was written, and
// leaves other customers' conversations (decision 0062 V5, P2).
func TestErasingACustomerDeletesTheirConversation(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "conversations", "keep_with_conversations": true,
		"columns": []map[string]any{{"name": "sender", "type": "text", "personal": true}, {"name": "text", "type": "text", "personal": true}}})
	project := w.pg.Projects()[0]
	now := time.Now().UTC().Format(time.RFC3339)
	tx := w.pg.Begin(project)
	for i, who := range []string{"2348031234567", "2348031234567", "+2348031234567", "2348099999999"} {
		tx.Insert("conversations", map[string]any{"id": int64(i + 1), "sender": who, "text": "hello", "created_at": now, "updated_at": now})
	}
	tx.Commit()
	secret := enrol(t, owner)
	out := owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": "08031234567", "totp": codeAfter(secret, 1)})
	if out["conversation_rows_deleted"] != float64(3) || out["conversation_rows_pending"] != nil || out["erased"] != false {
		t.Fatalf("erased: %v", out)
	}
	left := w.pg.Rows(project, "conversations")
	if len(left) != 1 || left[0]["sender"] != "2348099999999" {
		t.Fatalf("left %v", left)
	}
}

// Changing how long conversations are kept moves the runs already ended
// that are kept with conversations, and their files, and the plan caps
// what Settings shows.
func TestConversationDaysMoveEndedRuns(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string)
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Reply", "definition": map[string]any{"schema": "wd/v1", "id": "wf_reply", "version": 1, "name": "Reply",
		"trigger": map[string]any{"type": "manual"}, "settings": map[string]any{"retention": "conversations"},
		"steps": []any{map[string]any{"id": "a", "type": "transform", "config": map[string]any{"output": 1}}}}})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	keep := func() time.Duration {
		var until time.Time
		if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT retain_until FROM runs WHERE id = $1`, run).Scan(&until); err != nil {
			t.Fatal(err)
		}
		return time.Until(until)
	}
	if d := keep(); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("kept %v, want 30 days", d)
	}
	owner.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false, "conversation_days": 7})
	if d := keep(); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Fatalf("after 7 days was chosen: kept %v", d)
	}
	// A plan that keeps runs 3 days: Settings says 3, and so do the runs.
	if err := w.env.Store.SetLimits(context.Background(), uuid.MustParse(tenant), map[string]any{"max_retention_days": 3}, "operator"); err != nil {
		t.Fatal(err)
	}
	w.env.Store.ForgetLimits(uuid.MustParse(tenant))
	if v := owner.must(200, "GET", "/v1/languages", nil); v["conversation_days"] != float64(3) || v["max_conversation_days"] != float64(3) {
		t.Fatalf("on a 3-day plan: %v", v)
	}
	owner.must(400, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false, "conversation_days": 5})
}
