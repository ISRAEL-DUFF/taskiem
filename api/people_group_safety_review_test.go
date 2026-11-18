package api_test

import (
	"context"
	"fmt"
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

// reportedBy is the person's ReportedBy signal, as their own transaction
// reads it.
func reportedBy(t *testing.T, w *peopleWorld, id uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	tx, err := w.env.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.person_scope', $1, true)`, id.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT reported_by FROM taskiem_people_sender_signals()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A report of a member from a group weighs in their signals only from a
// reporter past their first week, in the group a day or more, about a
// member who wrote within what they see; and then half. Accounts made to
// join a group by its link and report a long-standing member weigh
// nothing; genuine reports from the group still count.
func TestGroupReportsWeighHalf(t *testing.T) {
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031159701", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031159702", "Bola Ade")
	w.older(t, adaID)
	w.older(t, bolaID)
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Street", bolaID)
	code := groupLink(t, ada, gid)
	join := func(number, name string, old bool) *client {
		t.Helper()
		c, id := w.many(t, number, name)
		if old {
			w.older(t, id)
		}
		c.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil)
		return c
	}
	report := func(c *client) map[string]any {
		t.Helper()
		return c.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "abuse", "consent": true, "person_id": bolaID})
	}
	// Five new accounts and two older ones join, Bola writes, they report.
	var attackers []*client
	for i := 0; i < 5; i++ {
		attackers = append(attackers, join(fmt.Sprintf("+23480311597%02d", 10+i), fmt.Sprintf("New %d", i), false))
	}
	attackers = append(attackers, join("+2348031159720", "Old 1", true), join("+2348031159721", "Old 2", true))
	gsend(t, bola, gid, "Morning all")
	for _, c := range attackers {
		report(c)
	}
	if n := reportedBy(t, w, bolaID); n != 0 {
		t.Fatalf("join and report weighed: %d", n)
	}
	if n := w.adminCount(t, `SELECT count(*)::int FROM people_reports WHERE reported_id = $1 AND group_member AND NOT peer_wrote`, bolaID); n != 7 {
		t.Fatalf("the reports kept, not earned: %d", n)
	}
	// Bola's requests are not slowed by them: a stranger's chat opens.
	_, dayoID := w.many(t, "+2348031159730", "Dayo Ola")
	w.older(t, dayoID)
	bola.must(201, "POST", "/v1/me/people", map[string]any{"person_id": dayoID})

	// Members of a day and more, past their first week: half each.
	genuine := []*client{join("+2348031159740", "Real 1", true), join("+2348031159741", "Real 2", true), join("+2348031159742", "Real 3", true)}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE group_members SET joined_at = joined_at - interval '2 days',
		visible = visible + tstzmultirange(tstzrange(joined_at - interval '2 days', joined_at)) WHERE group_id = $1 AND person_id <> ALL ($2)`,
		gid, []uuid.UUID{adaID, bolaID}); err != nil {
		t.Fatal(err)
	}
	gsend(t, bola, gid, "Still here")
	report(genuine[0])
	if n := reportedBy(t, w, bolaID); n != 0 {
		t.Fatalf("one reporter from a group is half a person: %d", n)
	}
	report(genuine[1])
	if n := reportedBy(t, w, bolaID); n != 1 {
		t.Fatalf("two reporters from a group are one: %d", n)
	}
	// A reporter twice counts once.
	report(genuine[1])
	report(genuine[2])
	if n := reportedBy(t, w, bolaID); n != 1 {
		t.Fatalf("three reporters from a group: %d", n)
	}
	// One who reports one to one too counts whole: Bola wrote to them.
	g0 := genuine[0]
	chat, _ := friends(t, g0, bola, bolaID)
	g0.must(201, "POST", "/v1/me/people/"+chat+"/report", map[string]any{"reason": "abuse", "consent": true})
	if n := reportedBy(t, w, bolaID); n != 2 {
		t.Fatalf("a whole reporter and two halves: %d", n)
	}
}

// A report of a member is one of a member after their account goes (never
// shown or counted as a report of the whole group); a member whose time
// in the group never met the reporter's is answered as an unknown id; a
// moderator's action names only a report of that group.
func TestGroupReportsMembersAndReach(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031159801", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031159802", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031159803", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031159804", "Dayo Ola")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID, dayoID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Club", bolaID, chidiID)
	gsend(t, chidi, gid, "Chidi was here")
	// Bola leaves; then Dayo comes in.
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	adds(t, ada, dayo)
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{dayoID}})
	unknown := bola.must(404, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "abuse", "consent": true, "person_id": uuid.New()})
	if e := bola.must(404, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "abuse", "consent": true, "person_id": dayoID}); e["error"] != unknown["error"] {
		t.Fatalf("someone Bola never met: %v", e)
	}
	member := bola.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "abuse", "consent": true, "person_id": chidiID})["id"].(string)
	whole := ada.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "spam", "consent": true})["id"].(string)

	// Chidi's account gone for good: the report is still a member's.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_reports SET reported_id = NULL WHERE id = $1`, member); err != nil {
		t.Fatal(err)
	}
	var reports []any
	for _, r := range mod.must(200, "GET", "/v1/ops/people/reports", nil)["reports"].([]any) {
		if r.(map[string]any)["group_id"] == gid {
			reports = append(reports, r)
		}
	}
	for _, r := range reports {
		m := r.(map[string]any)
		switch m["id"] {
		case member:
			if m["group_member"] != true || m["reported_id"] != nil {
				t.Fatalf("the member's report: %v", m)
			}
		case whole:
			if m["group_member"] != false || m["reports_against"] != float64(1) {
				t.Fatalf("the group's report, counted once: %v", m)
			}
		}
	}
	if len(reports) != 2 {
		t.Fatalf("the group's reports: %v", reports)
	}
	if r := mod.must(200, "GET", "/v1/ops/people/reports/"+member+"?why=Check", nil)["report"].(map[string]any); r["group_member"] != true {
		t.Fatalf("the report opened: %v", r)
	}
	page := mod.must(200, "GET", "/v1/ops/people/groups/"+gid+"?why=Check", nil)
	for _, r := range page["reports"].([]any) {
		if m := r.(map[string]any); (m["id"] == member) != (m["group_member"] == true) {
			t.Fatalf("the group's page: %v", m)
		}
	}

	// An action names a report of this group only.
	other, _ := newGroup(t, chidi, "Other")
	elsewhere := chidi.must(201, "POST", "/v1/me/groups/"+other+"/report", map[string]any{"reason": "spam", "consent": true})["id"].(string)
	mod.write(422, "ops.people.group.link_off", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "link_off", "reason": "Spam", "report_id": elsewhere})
	mod.write(201, "ops.people.group.link_off", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "link_off", "reason": "Spam", "report_id": whole})
}
