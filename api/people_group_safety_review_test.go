package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A lawful export naming one other person (with_person_id) gives only the
// groups both were in, and only messages of the times both were in each,
// for sent and seen alike; the export and its record are one transaction.
func TestLawfulGroupExportWithPerson(t *testing.T) {
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031159601", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031159602", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031159603", "Chidi Eze")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	second := newModerator(t, w, "second@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	adds(t, chidi, ada)
	shared, _ := newGroup(t, ada, "With Chidi", bolaID, chidiID)
	other, _ := newGroup(t, ada, "Without Chidi", bolaID)
	gsend(t, bola, shared, "Bola to both")
	gsend(t, chidi, shared, "Chidi to both")
	gsend(t, bola, other, "Bola's secret")
	gsend(t, ada, other, "Ada's secret")
	chidi.must(200, "POST", "/v1/me/groups/"+shared+"/leave", nil)
	gsend(t, bola, shared, "Bola after Chidi left")

	export := func(groups string, with *uuid.UUID) map[string]any {
		t.Helper()
		body := map[string]any{"authority": "Police", "reference": "REF-" + groups, "legal_basis": "X", "person_id": bolaID,
			"from": time.Now().Add(-time.Hour), "to": time.Now().Add(time.Hour), "files": false, "received_at": time.Now(), "groups": groups}
		if with != nil {
			body["with_person_id"] = with
		}
		id := mod.write(201, "ops.lawful.record", bolaID.String(), "/v1/ops/lawful-requests", body)["request"].(map[string]any)["id"].(string)
		second.write(200, "ops.lawful.approve", id, "/v1/ops/lawful-requests/"+id+"/approve", nil)
		return mod.write(200, "ops.lawful.export", id, "/v1/ops/lawful-requests/"+id+"/export", nil)
	}
	got := func(out map[string]any) string {
		var b []string
		for _, g := range out["groups"].([]any) {
			for _, m := range g.(map[string]any)["messages"].([]any) {
				b = append(b, g.(map[string]any)["name"].(string)+": "+m.(map[string]any)["body"].(string))
			}
		}
		return strings.Join(b, " | ")
	}
	if b := got(export("seen", &chidiID)); b != "With Chidi: Bola to both | With Chidi: Chidi to both" {
		t.Fatalf("seen with Chidi: %s", b)
	}
	if b := got(export("sent", &chidiID)); b != "With Chidi: Bola to both" {
		t.Fatalf("sent with Chidi: %s", b)
	}
	// Without anyone named, every group of Bola's, as before.
	if b := got(export("sent", nil)); b != "With Chidi: Bola to both | With Chidi: Bola after Chidi left | Without Chidi: Bola's secret" {
		t.Fatalf("sent: %s", b)
	}
	// Recorded with what was given, and the request answered with it.
	a := platformAudits(t, w, "ops.lawful.export")
	if len(a) != 3 || a[0].Detail["messages"] != float64(2) || a[2].Detail["messages"] != float64(3) {
		t.Fatalf("the records: %v", a)
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM lawful_requests WHERE state = 'answered' AND messages_given = 3 AND scope_groups = 'sent' AND scope_with IS NULL`); n != 1 {
		t.Fatalf("answered: %d", n)
	}
}

// The review's fixes to safety in groups ("As built: stage 5b, group
// safety (server)", Review fixes).

// A closed group refuses every write that changes it or reaches its
// members (409, in the closed words): reactions, a share marked paid, who
// is an admin, removing people, its rules and info, its photo, adding
// people, invites, its link, join requests; typing answers 204 and writes
// nothing. Each member still leaves, sets their own settings, stars,
// deletes for themselves and their own message for everyone, reads,
// reports, blocks a member (their own block: nothing in the group), and
// its admins appeal.
func TestGroupClosedRefusesChanges(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031159501", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031159502", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031159503", "Chidi Eze")
	_, dayoID := w.many(t, "+2348031159504", "Dayo Ola")
	eve, eveID := w.many(t, "+2348031159505", "Eve Uba")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID, dayoID, eveID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	adds(t, chidi, ada)
	gid, added := newGroup(t, ada, "Loud", bolaID, chidiID, dayoID)
	if added[dayoID] != "invited" {
		t.Fatalf("Dayo invited: %v", added)
	}
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	code := groupLink(t, ada, gid)
	if j := eve.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("Eve waits: %v", j)
	}
	share := ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", splitCard("Diesel"))["id"].(string)
	mine := gsend(t, bola, gid, "Bola's own")["id"].(string)
	other := gsend(t, chidi, gid, "Chidi's")["id"].(string)
	mod.write(201, "ops.people.group.close", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "close", "reason": "Scams"})
	events := func() int {
		return w.adminCount(t, `SELECT count(*)::int FROM group_messages WHERE group_id = $1 AND kind = 'event'`, gid)
	}
	before := events()

	// Refused, with the closed words.
	g := "/v1/me/groups/" + gid
	for _, c := range []struct {
		who          *client
		method, path string
		body         any
	}{
		{bola, "PUT", g + "/messages/" + other, map[string]any{"reaction": "👍"}},
		{bola, "POST", g + "/messages/" + share + "/paid", map[string]any{"paid": true}},
		{ada, "PUT", g + "/members/" + chidiID.String(), map[string]any{"admin": true}},
		{ada, "DELETE", g + "/members/" + bolaID.String(), nil},
		{ada, "PUT", g, map[string]any{"admins_only_post": true}},
		{ada, "PUT", g, map[string]any{"name": "Renamed"}},
		{ada, "DELETE", g + "/photo", nil},
		{ada, "POST", g + "/members", map[string]any{"person_ids": []uuid.UUID{eveID}}},
		{ada, "DELETE", g + "/invites/" + dayoID.String(), nil},
		{ada, "POST", g + "/link", nil},
		{ada, "DELETE", g + "/link", nil},
		{ada, "POST", g + "/joins/" + eveID.String() + "/approve", nil},
		{ada, "POST", g + "/joins/" + eveID.String() + "/decline", nil},
	} {
		if e := c.who.must(409, c.method, c.path, c.body); !strings.Contains(e["error"].(string), "closed this group") {
			t.Fatalf("%s %s: %v", c.method, c.path, e)
		}
	}
	bola.must(403, "POST", g+"/messages", map[string]any{"text": "x"})
	bola.must(204, "POST", g+"/typing", nil)
	if n := w.adminCount(t, `SELECT count(*)::int FROM group_typing WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("typing written: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM group_messages WHERE id = $1 AND paid_at IS NULL`, share); n != 1 {
		t.Fatal("the share marked paid")
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM group_reactions WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("a reaction kept: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM group_members WHERE group_id = $1 AND left_at IS NULL AND (role = 'admin') = (person_id = $2)`, gid, adaID); n != 3 {
		t.Fatalf("members or admins changed: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM group_invites WHERE group_id = $1`, gid) + w.adminCount(t, `SELECT count(*)::int FROM group_join_requests WHERE group_id = $1`, gid); n != 2 {
		t.Fatalf("the invite or the join request went: %d", n)
	}

	// Still each member's own.
	bola.must(200, "PUT", g+"/me", map[string]any{"muted": true, "mentions": false})
	bola.must(200, "PUT", g+"/messages/"+other, map[string]any{"starred": true})
	bola.must(204, "DELETE", g+"/messages/"+other+"?for=me", nil)
	bola.must(204, "DELETE", g+"/messages/"+mine+"?for=everyone", nil)
	bola.must(204, "POST", g+"/read", nil)
	bola.must(201, "POST", g+"/report", map[string]any{"reason": "scam", "consent": true, "person_id": chidiID})
	if b := bola.must(200, "POST", g+"/members/"+chidiID.String()+"/block", nil); b["blocked"] != true {
		t.Fatalf("the block: %v", b)
	}
	ada.must(201, "POST", g+"/appeal", map[string]any{"text": "We sell real things"})
	chidi.must(200, "POST", g+"/leave", nil)
	var blocked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*)::int FROM people_blocks WHERE person_id = $1 AND blocked_id = $2`, bolaID, chidiID).Scan(&blocked); err != nil || blocked != 1 {
		t.Fatalf("Bola's block: %d %v", blocked, err)
	}
	// Leaving keeps its event as ever; nothing else wrote one.
	if after := events(); after != before+1 {
		t.Fatalf("events: %d before, %d after (only the leave's)", before, after)
	}
}
