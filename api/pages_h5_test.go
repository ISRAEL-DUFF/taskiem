package api_test

import (
	"context"
	"testing"
)

// Pages H5 (build-plan-gaps §7), g-pages-mine: a page of tickets filtered
// by Mine on a phone column shows a staff member their own: the number
// they sign in by, matched as a table may hold it (with or without its
// plus, or written at home), and nobody else's.
func TestPagesMineOnAPhoneColumn(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@h5mine.test")
	owner.must(201, "PUT", "/v1/roles/desk", map[string]any{"permissions": []string{"run.start"}})
	sam := addMember(t, w.world, owner, "sam", "desk")
	ada := addMember(t, w.world, owner, "ada", "desk") // no phone on her account
	if _, err := w.env.DB.Admin.Exec(context.Background(),
		`INSERT INTO user_phones (user_id, number) SELECT id, '+2348012345678' FROM users WHERE email = 'sam@acme.test'`); err != nil {
		t.Fatal(err)
	}

	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{
		{"title": "Printer jam", "status": "open", "phone": "2348012345678"},
		{"title": "Door stuck", "status": "open", "phone": "08012345678"},
		{"title": "No ink", "status": "open", "phone": "2348098765432"},
		{"title": "Screen dark", "status": "open"},
	}})

	// The editor's Mine on the phone column: Check takes it.
	doc := map[string]any{"page": "page/v1", "name": "my-tickets", "title": "My tickets", "roles": []string{"desk"},
		"filters": []map[string]any{{"id": "mine", "type": "mine", "column": "phone", "label": "Mine"}},
		"blocks": []map[string]any{
			{"id": "list", "type": "list", "title": "Tickets", "source": map[string]any{"table": "tickets", "order": []map[string]any{{"column": "title"}}, "uses": []string{"mine"}},
				"item": map[string]any{"title": "=row.title"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/my-tickets/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}

	mine := map[string]any{"mine": "on"}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", mine), "list")); len(got) != 2 || got[0] != "Door stuck" || got[1] != "Printer jam" {
		t.Errorf("Sam's own tickets: %v", got)
	}
	if got := titles(blockOf(t, viewOf(t, sam, "my-tickets", nil), "list")); len(got) != 4 {
		t.Errorf("without Mine, every ticket: %v", got)
	}
	if rows := blockOf(t, viewOf(t, ada, "my-tickets", mine), "list")["rows"].([]any); len(rows) != 0 {
		t.Errorf("Ada has no phone, so Mine shows her nothing: %v", rows)
	}
}
