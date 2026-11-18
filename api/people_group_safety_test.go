package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Safety in groups (people-chatting §8.2 to §8.5; 5b B with the founder's
// B1; "As built: stage 5b, group safety (server)").

// groupLink turns c's group's link on and answers its code.
func groupLink(t *testing.T, c *client, g string) string {
	t.Helper()
	return c.must(201, "POST", "/v1/me/groups/"+g+"/link", nil)["code"].(string)
}

// hasBody is whether c sees a message with these words in g.
func hasBody(t *testing.T, c *client, g, body string) bool {
	t.Helper()
	for _, m := range gmessages(t, c, g) {
		if m["body"] == body {
			return true
		}
	}
	return false
}

// Limits (§8.2): groups made an hour and a day, people invited who aren't
// the admin's contacts in a day, and link joins an hour, all lower in an
// account's first week, with plain words; the counts are the database's
// (a ledger), not one server's memory.
func TestGroupLimits(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150001", "Ada Obi")
	older, olderID := w.many(t, "+2348031150002", "Bola Ade")
	w.older(t, olderID)

	// A first-week account: two groups an hour, three a day.
	newGroup(t, ada, "One")
	newGroup(t, ada, "Two")
	if e := ada.must(429, "POST", "/v1/me/groups", map[string]any{"name": "Three"}); !strings.Contains(e["error"].(string), "last hour") {
		t.Fatalf("the hour's limit: %v", e)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_ledger SET at = at - interval '2 hours' WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	newGroup(t, ada, "Three")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_ledger SET at = at - interval '2 hours' WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if e := ada.must(429, "POST", "/v1/me/groups", map[string]any{"name": "Four"}); !strings.Contains(e["error"].(string), "today") {
		t.Fatalf("the day's limit: %v", e)
	}
	// An account past its first month makes five in an hour.
	for i := 0; i < 5; i++ {
		newGroup(t, older, fmt.Sprintf("Older %d", i))
	}
	older.must(429, "POST", "/v1/me/groups", map[string]any{"name": "Sixth"})

	// People who aren't the admin's contacts are invited, ten a day in the
	// first week; contacts don't count, nor someone already invited.
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM group_ledger WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	gid, _ := newGroup(t, ada, "Neighbours")
	var strangers []uuid.UUID
	for i := 0; i < 11; i++ {
		_, id := w.many(t, fmt.Sprintf("+23480311501%02d", i), fmt.Sprintf("Stranger %d", i))
		strangers = append(strangers, id)
	}
	friend, friendID := w.many(t, "+2348031150200", "Friend")
	adds(t, ada, friend)
	adds(t, friend, ada)
	if e := ada.must(429, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": strangers}); !strings.Contains(e["error"].(string), "contacts") {
		t.Fatalf("eleven strangers: %v", e)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM group_invites WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("%d invited past the limit", n)
	}
	got := addedOf(t, ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": append(strangers[:10:10], friendID)}))
	if got[friendID] != "added" || got[strangers[0]] != "invited" || got[strangers[9]] != "invited" {
		t.Fatalf("ten strangers and a friend: %v", got)
	}
	// Asked again: already invited, nothing more counts.
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": strangers[:3]})
	ada.must(429, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": strangers[10:]})

	// Link joins: three an hour in the first week.
	chidi, _ := w.many(t, "+2348031150300", "Chidi Eze")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_ledger SET at = at - interval '2 days' WHERE person_id = $1`, olderID); err != nil {
		t.Fatal(err)
	}
	var codes []string
	for i := 0; i < 4; i++ {
		g, _ := newGroup(t, older, fmt.Sprintf("Club %d", i))
		codes = append(codes, groupLink(t, older, g))
	}
	for i, code := range codes {
		want := 200
		if i == 3 {
			want = 429
		}
		out := chidi.must(want, "POST", "/v1/me/group-links/"+code+"/join", nil)
		if i == 3 && !strings.Contains(out["error"].(string), "joined many groups") {
			t.Fatalf("the fourth join: %v", out)
		}
	}
	if n := w.adminCount(t, `SELECT count(*) FROM group_members WHERE person_id = (SELECT person_id FROM group_ledger WHERE kind = 'link_join' LIMIT 1) AND how = 'link'`); n != 3 {
		t.Fatalf("%d link joins kept", n)
	}

	// The sweep prunes the ledger past two days; an account's deletion
	// takes its own.
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM group_ledger WHERE at < now() - interval '2 days'`); n != 0 {
		t.Fatalf("%d old ledger rows", n)
	}
	w.deleteAccount(t, ada, "+2348031150001")
	if n := w.adminCount(t, `SELECT count(*) FROM group_ledger WHERE person_id = $1`, adaID); n != 0 {
		t.Fatal("the ledger outlived the account")
	}
}

// Reporting a group or a member (§8.3): with consent, the last 20 messages
// the reporter could see (never from before they joined), with their
// photos, copied and sealed under the report's own key; moderators list
// them without words, open them only saying why (recorded), and the report
// outlives the reporter and the group.
func TestGroupReports(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150401", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031150402", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031150403", "Chidi Eze")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	adds(t, chidi, ada)
	gid, _ := newGroup(t, ada, "Market day", bolaID)
	gsend(t, ada, gid, "Before Chidi came: a secret")
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{chidiID}})
	if code, f := bola.postFile(t, "/v1/me/groups/"+gid+"/files", "coins.png", pngPhoto(t, 90), map[string]string{"text": "Buy my coins"}); code != 201 {
		t.Fatalf("a photo: %d %v", code, f)
	}
	for i := 0; i < 12; i++ {
		gsend(t, bola, gid, fmt.Sprintf("Bola %d", i))
		gsend(t, ada, gid, fmt.Sprintf("Ada %d", i))
	}

	// Consent first; a member who isn't in the group is not found.
	chidi.must(400, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "scam", "person_id": bolaID})
	chidi.must(404, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "scam", "consent": true, "person_id": uuid.New()})
	rep := chidi.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "scam", "note": "Coins", "consent": true, "person_id": bolaID})
	repID := rep["id"].(string)
	if !strings.Contains(rep["said"].(string), "block") {
		t.Fatalf("said: %v", rep)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1 AND group_id = $2 AND reported_id = $3 AND messages = '[]' AND sealed IS NOT NULL
		AND group_sealed IS NOT NULL AND peer_wrote AND position(convert_to('Bola', 'UTF8') in sealed) = 0
		AND position(convert_to('Market', 'UTF8') in group_sealed) = 0`, repID, gid, bolaID); n != 1 {
		t.Fatal("the report is not kept sealed, with its group")
	}

	// The list: the group, no words.
	list := mod.must(200, "GET", "/v1/ops/people/reports", nil)["reports"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["group_id"] != gid || list[0].(map[string]any)["reported_name"] != "Bola Ade" {
		t.Fatalf("the list: %v", list)
	}
	if bytes.Contains(asJSON(t, list), []byte("Market")) || bytes.Contains(asJSON(t, list), []byte("coins")) {
		t.Fatal("the list carries words")
	}
	// Opened saying why: 20 messages, the newest first, none from before
	// Chidi joined, who sent each; the group's name; recorded.
	mod.must(400, "GET", "/v1/ops/people/reports/"+repID, nil)
	opened := mod.must(200, "GET", "/v1/ops/people/reports/"+repID+"?why=Group+report", nil)
	msgs := opened["messages"].([]any)
	if len(msgs) != 20 {
		t.Fatalf("%d messages", len(msgs))
	}
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["body"] == "Before Chidi came: a secret" || mm["kind"] == "event" {
			t.Fatalf("outside the reporter's own time: %v", mm)
		}
		if strings.HasPrefix(mm["body"].(string), "Bola") && (mm["sender"] != "Bola Ade" || mm["sender_id"] != bolaID.String()) {
			t.Fatalf("who sent it: %v", mm)
		}
	}
	if g := opened["group"].(map[string]any); g["name"] != "Market day" || g["members"] != float64(3) {
		t.Fatalf("the group: %v", g)
	}
	if a := platformAudits(t, w, "ops.people.report.open"); len(a) != 1 || a[0].Detail["why"] != "Group report" || a[0].Detail["group"] != gid {
		t.Fatalf("the opening's record: %v", a)
	}

	// A report of the group with its photo: the photo is copied and opened
	// only saying why (recorded).
	all := chidi.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "spam", "consent": true})["id"].(string)
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1 AND reported_id IS NULL`, all); n != 1 {
		t.Fatal("a report of the group names a person")
	}
	// Only Bola's latest 20 and Ada's are in it; for the photo, a report
	// of Bola after Ada stops.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_messages SET created_at = created_at - interval '1 hour' WHERE group_id = $1 AND sender_id = $2`, gid, adaID); err != nil {
		t.Fatal(err)
	}
	withPhoto := chidi.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "scam", "consent": true, "person_id": bolaID})["id"].(string)
	one := mod.must(200, "GET", "/v1/ops/people/reports/"+withPhoto+"?why=The+photo+report", nil)
	files := one["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files: %v", one)
	}
	code, got := mod.raw("/v1/ops/people/reports/" + withPhoto + "/files/" + files[0].(map[string]any)["message_id"].(string) + "?why=Look")
	if code != 200 || len(got) < 100 {
		t.Fatalf("the photo: %d", code)
	}
	if a := platformAudits(t, w, "ops.people.report.file"); len(a) != 1 {
		t.Fatalf("the photo's record: %v", a)
	}

	// The reporter deletes their account, the group is closed and goes:
	// the reports stay, detached, with their evidence.
	w.deleteAccount(t, chidi, "+2348031150403")
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	if n := w.adminCount(t, `SELECT count(*) FROM people_groups WHERE id = $1`, gid); n != 0 {
		t.Fatal("the group did not close")
	}
	again := mod.must(200, "GET", "/v1/ops/people/reports/"+repID+"?why=After", nil)
	if len(again["messages"].([]any)) != 20 || again["report"].(map[string]any)["reporter_id"] != nil || again["group"].(map[string]any)["name"] != "Market day" {
		t.Fatalf("after: %v", again["report"])
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE group_id = $1 AND person_id IS NULL AND detached_at IS NOT NULL`, gid); n != 3 {
		t.Fatalf("%d reports detached", n)
	}
}

// Moderators and a group (§8.3): the group's page opened only saying why
// (recorded), its link turned off and the group closed with a passkey, its
// members told in plain words, its admins' appeal (once, decided by a
// different moderator), reopening, and a closure carried out by the safety
// job (all of it deleted, §14) unless an appeal is open.
func TestGroupModeration(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150501", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031150502", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031150503", "Chidi Eze")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	other := newModerator(t, w, "other@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Loud group", bolaID)
	code := groupLink(t, ada, gid)
	gsend(t, bola, gid, "Hello all")
	rep := bola.must(201, "POST", "/v1/me/groups/"+gid+"/report", map[string]any{"reason": "abuse", "consent": true})["id"].(string)

	// The page: why first, recorded; members and the report, no words.
	mod.must(400, "GET", "/v1/ops/people/groups/"+gid, nil)
	page := mod.must(200, "GET", "/v1/ops/people/groups/"+gid+"?why=Report+of+the+group", nil)
	if g := page["group"].(map[string]any); g["members"] != float64(2) || g["admins"] != float64(1) || g["link_on"] != true || g["reports"] != float64(1) {
		t.Fatalf("the group: %v", g)
	}
	if len(page["members"].([]any)) != 2 || len(page["reports"].([]any)) != 1 || bytes.Contains(asJSON(t, page), []byte("Loud")) {
		t.Fatalf("the page: %v", page)
	}
	if a := platformAudits(t, w, "ops.people.group.open"); len(a) != 1 || a[0].Detail["why"] != "Report of the group" || a[0].Target != gid {
		t.Fatalf("its record: %v", a)
	}

	// The link turned off (a passkey): joining is gone, no new link, the
	// members told.
	if e := mod.must(403, "POST", "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "link_off", "reason": "Spam joins"}); e["step_up"] != "passkey" {
		t.Fatalf("no step-up: %v", e)
	}
	off := mod.write(201, "ops.people.group.link_off", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "link_off", "reason": "Spam joins",
		"report_id": rep})
	mod.write(409, "ops.people.group.link_off", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "link_off", "reason": "Again"})
	chidi.must(410, "POST", "/v1/me/group-links/"+code+"/join", nil)
	if e := ada.must(403, "POST", "/v1/me/groups/"+gid+"/link", nil); !strings.Contains(e["error"].(string), "turned off") {
		t.Fatalf("a new link: %v", e)
	}
	if !hasBody(t, bola, gid, "Taskiem's moderators turned off this group's invite link.") {
		t.Fatal("the members were not told")
	}
	if g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil); g["link_stopped"] != true || g["closed"] != nil {
		t.Fatalf("the group's view: %v", g)
	}
	if a := platformAudits(t, w, "ops.people.group.link_off"); len(a) != 1 || a[0].Detail["reason"] != "Spam joins" {
		t.Fatalf("its record: %v", a)
	}
	// Lifted: admins may make a link again.
	mod.write(200, "ops.people.lift", off["id"].(string), "/v1/ops/people/actions/"+off["id"].(string)+"/lift", map[string]any{"reason": "Calm now"})
	groupLink(t, ada, gid)
	if !hasBody(t, bola, gid, "Taskiem's moderators let this group's admins make an invite link again.") {
		t.Fatal("not told of the lift")
	}

	// Closed: no one sends, adds, edits or joins; the members told when it
	// is deleted; its admins see the closure to appeal.
	closed := mod.write(201, "ops.people.group.close", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "close", "reason": "Scams"})
	closeID := closed["id"].(string)
	if e := bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "Still here?"}); !strings.Contains(e["error"].(string), "closed this group") {
		t.Fatalf("a message: %v", e)
	}
	ada.must(403, "PUT", "/v1/me/groups/"+gid, map[string]any{"name": "Renamed"})
	ada.must(403, "POST", "/v1/me/groups/"+gid+"/link", nil)
	msgs := gmessages(t, bola, gid)
	last := msgs[len(msgs)-1]
	if last["sender"] != "Taskiem" || !strings.Contains(last["body"].(string), "closed this group") || !strings.Contains(last["body"].(string), "will be deleted on") {
		t.Fatalf("the members' notice: %v", last)
	}
	bv := bola.must(200, "GET", "/v1/me/groups/"+gid, nil)
	if bv["can_post"] != false || bv["closed"].(map[string]any)["words"] == "" || bv["closed"].(map[string]any)["action_id"] != nil {
		t.Fatalf("a member's view: %v", bv)
	}
	av := ada.must(200, "GET", "/v1/me/groups/"+gid, nil)["closed"].(map[string]any)
	if av["action_id"] != closeID || av["can_appeal"] != true || av["reason"] != "Scams" {
		t.Fatalf("the admin's view: %v", av)
	}
	// Members are acted on as people are.
	mod.write(201, "ops.people.warn", bolaID.String(), "/v1/ops/people/persons/"+bolaID.String()+"/actions", map[string]any{"action": "warn", "reason": "In the group"})

	// The appeal: an admin's, once.
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/appeal", map[string]any{"text": "Not me"})
	ap := ada.must(201, "POST", "/v1/me/groups/"+gid+"/appeal", map[string]any{"text": "We sell real things"})["appeal"].(map[string]any)
	ada.must(409, "POST", "/v1/me/groups/"+gid+"/appeal", map[string]any{"text": "Again"})
	if a := platformAudits(t, w, "people.group.appeal"); len(a) != 1 || a[0].Target != closeID {
		t.Fatalf("the appeal's record: %v", a)
	}
	appeals := mod.must(200, "GET", "/v1/ops/people/appeals", nil)["appeals"].([]any)
	if len(appeals) != 1 || appeals[0].(map[string]any)["group_id"] != gid || appeals[0].(map[string]any)["action"] != "group_close" {
		t.Fatalf("appeals: %v", appeals)
	}
	// Held by the open appeal past its day.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, closeID); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_groups WHERE id = $1`, gid); n != 1 {
		t.Fatal("deleted with an appeal open")
	}
	// Overturned by a different moderator: reopened, told.
	apID := ap["id"].(string)
	mod.write(403, "ops.people.appeal.overturn", apID, "/v1/ops/people/appeals/"+apID+"/overturn", map[string]any{"note": "Fine"})
	dec := other.write(200, "ops.people.appeal.overturn", apID, "/v1/ops/people/appeals/"+apID+"/overturn", map[string]any{"note": "Real sellers"})
	if dec["group_id"] != gid || dec["person_id"] != adaID.String() {
		t.Fatalf("decided: %v", dec)
	}
	gsend(t, bola, gid, "Back again")
	if !hasBody(t, bola, gid, "Taskiem's moderators reopened this group. You can send messages again.") {
		t.Fatal("not told it reopened")
	}
	if g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil); g["closed"] != nil || g["can_post"] != true {
		t.Fatalf("reopened: %v", g)
	}

	// Closed again, no appeal: the safety job deletes it all when its day
	// comes (reports keep their own copies), recorded.
	again := mod.write(201, "ops.people.group.close", gid, "/v1/ops/people/groups/"+gid+"/actions", map[string]any{"action": "close", "reason": "Scams again"})["id"].(string)
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_groups WHERE id = $1`, gid); n != 1 {
		t.Fatal("deleted before its day")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, again); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "POST", "/v1/me/groups/"+gid+"/appeal", map[string]any{"text": "Too late"})
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT (SELECT count(*) FROM people_groups WHERE id = $1) + (SELECT count(*) FROM group_messages WHERE group_id = $1)
		+ (SELECT count(*) FROM group_members WHERE group_id = $1) + (SELECT count(*) FROM group_keys WHERE group_id = $1)`, gid); n != 0 {
		t.Fatal("the closed group outlived its day")
	}
	if a := platformAudits(t, w, "people.group.close"); len(a) != 1 || a[0].Target != gid || a[0].Detail["outcome"] != "deleted" {
		t.Fatalf("the closure's record: %v", a)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1`, rep); n != 1 {
		t.Fatal("the report went with the group")
	}
	// Its page still answers what was decided.
	if p := mod.must(200, "GET", "/v1/ops/people/groups/"+gid+"?why=After", nil); p["gone"] != true || len(p["actions"].([]any)) != 3 {
		t.Fatalf("after: %v", p)
	}
	ada.must(404, "GET", "/v1/me/groups/"+gid, nil)
}

// Scam checks in groups (B1): a message from a member some members haven't
// added, in a group younger than a week or from someone who joined by its
// link in the last day, is checked; only the verdict is kept. Held: no one
// gets it (one copy), refused to the sender, kept sealed for moderators,
// who release it to the group (once) or confirm it. Warned: delivered,
// marked only for those who haven't added the sender.
func TestGroupScamChecks(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150601", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031150602", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031150603", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031150604", "Dayo Bello")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID, dayoID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	adds(t, chidi, ada)
	gid, _ := newGroup(t, ada, "New group", bolaID, chidiID)

	// Warned: Chidi hasn't added Bola, Ada has.
	warn := "Pay into my account 0123456789 at GTBank"
	gsend(t, bola, gid, warn)
	if m := gmessage(t, chidi, gid, warn); m["warn"] != true || m["warn_transfer"] != true {
		t.Fatalf("Chidi's: %v", m)
	}
	if m := gmessage(t, ada, gid, warn); m["warn"] == true {
		t.Fatalf("Ada's: %v", m)
	}
	if m := gmessage(t, bola, gid, warn); m["warn"] == true {
		t.Fatalf("Bola's own: %v", m)
	}
	// A share card naming an account, from someone not added: warned.
	bola.must(201, "POST", "/v1/me/groups/"+gid+"/messages", splitCard("The cake"))
	for _, m := range gmessages(t, chidi, gid) {
		if m["kind"] == "card" && (m["warn"] != true || m["warn_transfer"] != true) {
			t.Fatalf("a share card: %v", m)
		}
	}

	// Held: refused, no one gets it, kept sealed with the group.
	scam := "Congratulations you have won! Send me the code you received to claim your prize"
	if e := bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": scam, "mentions": []uuid.UUID{chidiID}}); !strings.Contains(e["error"].(string), "scam") {
		t.Fatalf("held: %v", e)
	}
	for _, c := range []*client{ada, bola, chidi} {
		if hasBody(t, c, gid, scam) {
			t.Fatal("a held message arrived")
		}
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_held WHERE group_id = $1 AND sender_id = $2 AND recipient_id IS NULL AND chat_id IS NULL AND $3 = ANY (mentions)
		AND position(convert_to('Congratulations', 'UTF8') in sealed) = 0`, gid, bolaID, chidiID); n != 1 {
		t.Fatal("not kept sealed with its group")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_screen_verdicts WHERE group_id = $1 AND to_person IS NULL`, gid); n != 3 {
		t.Fatalf("%d verdicts kept for the group", n)
	}
	held := mod.must(200, "GET", "/v1/ops/people/held", nil)["held"].([]any)
	if len(held) != 1 || held[0].(map[string]any)["group_id"] != gid || bytes.Contains(asJSON(t, held), []byte("Congratulations")) {
		t.Fatalf("held: %v", held)
	}
	hid := held[0].(map[string]any)["id"].(string)
	if one := mod.must(200, "GET", "/v1/ops/people/held/"+hid+"?why=Group+hold", nil); one["message"].(map[string]any)["body"] != scam {
		t.Fatalf("opened: %v", one)
	}
	// Released: in the group, as Bola wrote it, once.
	rel := mod.write(200, "ops.people.held.release", hid, "/v1/ops/people/held/"+hid+"/release", map[string]any{"note": "A real raffle"})
	m := gmessage(t, chidi, gid, scam)
	if m["id"] != rel["message_id"] || m["mentions_me"] != true {
		t.Fatalf("released: %v %v", rel, m)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_held SET state = 'held', decided_by = NULL, decided_at = NULL, released_message = NULL WHERE id = $1`, hid); err != nil {
		t.Fatal(err)
	}
	mod.write(200, "ops.people.held.release", hid, "/v1/ops/people/held/"+hid+"/release", nil)
	n := 0
	for _, x := range gmessages(t, ada, gid) {
		if x["body"] == scam {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("released %d times", n)
	}
	// Confirmed: never delivered.
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "Tell me your OTP now"})
	var second string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM people_held WHERE state = 'held'`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	mod.write(200, "ops.people.held.confirm", second, "/v1/ops/people/held/"+second+"/confirm", nil)
	if hasBody(t, chidi, gid, "Tell me your OTP now") {
		t.Fatal("a confirmed hold arrived")
	}

	// Everyone in it has added the sender: no checks.
	pair, _ := newGroup(t, ada, "Pair", bolaID)
	gsend(t, bola, pair, scam)

	// A group older than a week, a member there since: no checks.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_groups SET created_at = now() - interval '8 days' WHERE id = $1`, gid); err != nil {
		t.Fatal(err)
	}
	gsend(t, bola, gid, "Send me the code you received please")
	// Someone who joined by its link in the last day: checked.
	code := groupLink(t, ada, gid)
	dayo.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil)
	dayo.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": scam})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_members SET joined_at = now() - interval '2 days' WHERE group_id = $1 AND person_id = $2`, gid, dayoID); err != nil {
		t.Fatal(err)
	}
	gsend(t, dayo, gid, scam)

	// The sender's account goes: their undecided holds go with it.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_members SET joined_at = now() WHERE group_id = $1 AND person_id = $2`, gid, dayoID); err != nil {
		t.Fatal(err)
	}
	dayo.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": scam})
	w.deleteAccount(t, dayo, "+2348031150604")
	if n := w.adminCount(t, `SELECT count(*) FROM people_held WHERE state = 'held'`); n != 0 {
		t.Fatalf("%d holds outlived their sender", n)
	}
}

// Lawful requests (§8.5): a person's group messages within the scope and
// dates asked: their own (sent) or every one they could see (seen, only
// within their own time in each group), with one other person named only
// theirs and the person's; none unless asked; logged.
func TestLawfulGroupExport(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150701", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031150702", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031150703", "Chidi Eze")
	for _, id := range []uuid.UUID{adaID, bolaID, chidiID} {
		w.older(t, id)
	}
	mod := newModerator(t, w, "mod@taskiem.test")
	second := newModerator(t, w, "second@taskiem.test")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	adds(t, chidi, ada)
	gid, _ := newGroup(t, ada, "Family", chidiID)
	gsend(t, chidi, gid, "Before Bola")
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})
	gsend(t, bola, gid, "Bola's own")
	gsend(t, chidi, gid, "Chidi after")
	gsend(t, ada, gid, "Ada after")
	if code, _ := bola.postFile(t, "/v1/me/groups/"+gid+"/files", "p.png", pngPhoto(t, 50), map[string]string{"text": "Bola's photo"}); code != 201 {
		t.Fatal("a photo")
	}

	export := func(groups string, with *uuid.UUID, files bool) map[string]any {
		t.Helper()
		body := map[string]any{"authority": "Police", "reference": "REF-" + groups, "legal_basis": "Cybercrimes Act s.38", "person_id": bolaID,
			"from": time.Now().Add(-time.Hour), "to": time.Now().Add(time.Hour), "files": files, "received_at": time.Now(), "groups": groups}
		if with != nil {
			body["with_person_id"] = with
		}
		lr := mod.write(201, "ops.lawful.record", bolaID.String(), "/v1/ops/lawful-requests", body)["request"].(map[string]any)
		id := lr["id"].(string)
		if lr["groups"] != groups && (groups != "" || lr["groups"] != "none") {
			t.Fatalf("recorded: %v", lr)
		}
		second.write(200, "ops.lawful.approve", id, "/v1/ops/lawful-requests/"+id+"/approve", nil)
		return mod.write(200, "ops.lawful.export", id, "/v1/ops/lawful-requests/"+id+"/export", nil)
	}
	bodies := func(out map[string]any) []string {
		var b []string
		for _, g := range out["groups"].([]any) {
			if g.(map[string]any)["name"] != "Family" {
				t.Fatalf("the group's name: %v", g)
			}
			for _, m := range g.(map[string]any)["messages"].([]any) {
				b = append(b, m.(map[string]any)["body"].(string))
			}
		}
		return b
	}
	mod.write(422, "ops.lawful.record", bolaID.String(), "/v1/ops/lawful-requests", map[string]any{"authority": "Police", "reference": "X", "legal_basis": "Y",
		"person_id": bolaID, "from": time.Now().Add(-time.Hour), "to": time.Now(), "received_at": time.Now(), "groups": "all"})
	if out := export("", nil, false); len(out["groups"].([]any)) != 0 {
		t.Fatalf("not asked: %v", out["groups"])
	}
	if b := bodies(export("sent", nil, false)); strings.Join(b, "|") != "Bola's own|Bola's photo" {
		t.Fatalf("sent: %v", b)
	}
	seen := export("seen", nil, true)
	if b := bodies(seen); strings.Join(b, "|") != "Bola's own|Chidi after|Ada after|Bola's photo" {
		t.Fatalf("seen: %v", b)
	}
	if seen["files_given"] != float64(1) || seen["messages_given"] != float64(4) {
		t.Fatalf("counts: %v", seen)
	}
	if b := bodies(export("seen", &chidiID, false)); strings.Join(b, "|") != "Bola's own|Chidi after|Bola's photo" {
		t.Fatalf("seen with Chidi: %v", b)
	}
	a := platformAudits(t, w, "ops.lawful.export")
	if len(a) != 4 || a[2].Detail["groups"] != "seen" || a[2].Detail["messages"] != float64(4) {
		t.Fatalf("the exports' records: %v", a)
	}
	_ = ctx
}

// Storage watching includes groups' one copy of messages and files.
func TestGroupStorageWatched(t *testing.T) {
	w := safetyWorld(t)
	ada, adaID := w.many(t, "+2348031150801", "Ada Obi")
	w.older(t, adaID)
	mod := newModerator(t, w, "mod@taskiem.test")
	gid, _ := newGroup(t, ada, "Photos")
	if code, _ := ada.postFile(t, "/v1/me/groups/"+gid+"/files", "p.png", pngPhoto(t, 40), nil); code != 201 {
		t.Fatal("a photo")
	}
	rows, err := telemetry.PeopleStorage(context.Background(), w.env.DB.Admin)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Table] = r.Bytes > 0
	}
	if !seen["group_messages"] || !seen["group_files"] {
		t.Fatalf("storage: %v", rows)
	}
	tables := mod.must(200, "GET", "/v1/ops/people/storage", nil)["tables"].([]any)
	if !bytes.Contains(asJSON(t, tables), []byte("group_files")) {
		t.Fatalf("the console's storage: %v", tables)
	}
}
