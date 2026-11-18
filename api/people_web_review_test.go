package api_test

import (
	"context"
	"testing"
)

// The server's part of the stage 5a web review ("Web review fixes" in
// docs/spec/people-chatting.md): a suspended account seen by id and in
// the contacts, and Requests answered a page at a time.

// A suspended account is shown by its name and as suspended, not as a
// deleted one, by id and in the contacts; nothing else of it shows.
func TestPersonSuspendedByID(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031131401", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031131402", "Bola Ade")
	w.older(t, adaID)
	friends(t, ada, bola, bolaID)
	if p := bola.must(200, "GET", "/v1/people/"+adaID.String(), nil); p["name"] != "Ada Obi" || p["suspended"] != nil || p["deleted"] != false {
		t.Fatalf("active: %v", p)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	p := bola.must(200, "GET", "/v1/people/"+adaID.String(), nil)
	if p["name"] != "Ada Obi" || p["suspended"] != true || p["deleted"] != false || p["photo"] != nil || p["last_seen_at"] != nil || p["online"] != nil || p["number"] != nil || p["read_ticks"] != false {
		t.Fatalf("suspended, by id: %v", p)
	}
	cs := listOf(t, bola, "/v1/me/contacts", "contacts")
	if len(cs) != 1 || cs[0]["name"] != "Ada Obi" || cs[0]["suspended"] != true || cs[0]["deleted"] != false {
		t.Fatalf("suspended, in the contacts: %v", cs)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if p := bola.must(200, "GET", "/v1/people/"+adaID.String(), nil); p["name"] != "Ada Obi" || p["suspended"] != nil {
		t.Fatalf("active again: %v", p)
	}
}

// Requests come 200 a page, newest first, with more and next as the
// lists do.
func TestPeopleRequestsPage(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031131501", "Ada Obi")
	if _, err := w.env.DB.Admin.Exec(ctx, `WITH u AS (
		  INSERT INTO users (id, email, name) SELECT gen_random_uuid(), 'asker' || i || '-' || $2 || '@x.test', 'Asker ' || i FROM generate_series(1, 205) i RETURNING id, name)
		INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, request_at, last_message_at)
		SELECT gen_random_uuid(), $1, u.id, taskiem_people_pair($1, u.id), false, now(), date_trunc('second', now()) - (substring(u.name from 7)::int / 3) * interval '1 second' FROM u`, adaID, adaID.String()); err != nil {
		t.Fatal(err)
	}
	first := ada.must(200, "GET", "/v1/me/people/requests", nil)
	if len(first["requests"].([]any)) != 200 || first["more"] != true || first["next"] == nil {
		t.Fatalf("the first page: %d %v %v", len(first["requests"].([]any)), first["more"], first["next"])
	}
	second := ada.must(200, "GET", "/v1/me/people/requests?before="+first["next"].(string), nil)
	if len(second["requests"].([]any)) != 5 || second["more"] != false || second["next"] != nil {
		t.Fatalf("the second page: %v", second)
	}
	seen := map[string]bool{}
	for _, c := range append(first["requests"].([]any), second["requests"].([]any)...) {
		id := c.(map[string]any)["id"].(string)
		if seen[id] {
			t.Fatalf("twice: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 205 {
		t.Fatalf("%d requests", len(seen))
	}
	if l := ada.must(200, "GET", "/v1/me/conversations", nil); l["requests"] != float64(205) {
		t.Fatalf("counted: %v", l["requests"])
	}
	ada.must(400, "GET", "/v1/me/people/requests?before=nonsense", nil)
}
