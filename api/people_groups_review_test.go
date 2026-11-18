package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// The review of stage 5b's groups on the server ("As built: stage 5b,
// groups (server)", "Review fixes").

// asApp runs q as the API's role in person's own scope.
func asApp(t *testing.T, w *peopleWorld, person uuid.UUID, q string, args ...any) (int64, error) {
	t.Helper()
	var n int64
	err := db.InPersonTx(context.Background(), w.env.DB.App, person, func(tx pgx.Tx) error {
		tag, err := tx.Exec(context.Background(), q, args...)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// countAdmin counts with the database's own view (tests only).
func countAdmin(t *testing.T, w *peopleWorld, q string, args ...any) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 1. A request to join goes when the person joins another way or leaves;
// an admin's approve of an older request than their latest joining or
// leaving lets no one in; the person takes their own request back.
func TestGroupsReviewJoinRequestsDoNotOutliveJoining(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348039990051", "Ada Obi")
	chidi, chidiID := w.many(t, "+2348039990053", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348039990054", "Dayo Ola")
	gid, _ := newGroup(t, ada, "Club")
	code := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	if j := chidi.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("waiting: %v", j)
	}
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": false})
	if j := chidi.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "joined" {
		t.Fatalf("joined: %v", j)
	}
	if l := listOf(t, ada, "/v1/me/groups/"+gid+"/joins", "requests"); len(l) != 0 {
		t.Fatalf("a request outlived the join: %v", l)
	}
	chidi.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	ada.must(404, "POST", "/v1/me/groups/"+gid+"/joins/"+chidiID.String()+"/approve", nil)
	if g := chidi.must(200, "GET", "/v1/me/groups/"+gid, nil); g["left"] != true {
		t.Fatalf("an approve brought back someone who left: %v", g)
	}

	// An older request than the person's leaving (kept from before this
	// fix) is stale: it goes, and no one is let in.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO group_join_requests (group_id, person_id, created_at) VALUES ($1, $2, now() - interval '1 hour')`,
		gid, chidiID); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "POST", "/v1/me/groups/"+gid+"/joins/"+chidiID.String()+"/approve", nil)
	if g := chidi.must(200, "GET", "/v1/me/groups/"+gid, nil); g["left"] != true {
		t.Fatalf("a stale request let them back: %v", g)
	}
	if n := countAdmin(t, w, `SELECT count(*) FROM group_join_requests WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("the stale request stayed: %d", n)
	}

	// Waiting, then taken back by the person; again is fine; an unknown
	// link is 404.
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	if j := dayo.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("Dayo waiting: %v", j)
	}
	if l := listOf(t, ada, "/v1/me/groups/"+gid+"/joins", "requests"); len(l) != 1 || l[0]["person_id"] != dayoID.String() {
		t.Fatalf("Dayo's request: %v", l)
	}
	dayo.must(204, "DELETE", "/v1/me/group-links/"+code+"/join", nil)
	dayo.must(204, "DELETE", "/v1/me/group-links/"+code+"/join", nil)
	dayo.must(404, "DELETE", "/v1/me/group-links/nosuchcode1/join", nil)
	if l := listOf(t, ada, "/v1/me/groups/"+gid+"/joins", "requests"); len(l) != 0 {
		t.Fatalf("taken back: %v", l)
	}
	ada.must(404, "POST", "/v1/me/groups/"+gid+"/joins/"+dayoID.String()+"/approve", nil)
	// Waiting, then added by an admin another way: the request goes.
	adds(t, ada, dayo)
	adds(t, dayo, ada)
	dayo.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil)
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{dayoID}})
	if l := listOf(t, ada, "/v1/me/groups/"+gid+"/joins", "requests"); len(l) != 0 {
		t.Fatalf("a request outlived the add: %v", l)
	}
}

// 2. Someone removed stays removed after deleting their copy: their link
// join waits for an admin; an admin letting them in clears it. How they
// left stays as it was, even by SQL.
func TestGroupsReviewRemovedStaysRemoved(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348039990001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348039990002", "Bola Ade")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Club", bolaID)
	code := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), nil)
	// How they left stays, for them and for an admin.
	for _, by := range []uuid.UUID{bolaID, adaID} {
		if _, err := asApp(t, w, by, `UPDATE group_members SET left_how = 'left', removed_by = NULL WHERE group_id = $1 AND person_id = $2`, gid, bolaID); err == nil {
			t.Fatalf("%v rewrote a removal", by)
		}
	}
	if j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("removed: %v", j)
	}
	bola.must(204, "DELETE", "/v1/me/group-links/"+code+"/join", nil)
	bola.must(204, "DELETE", "/v1/me/groups/"+gid, nil)
	if j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("removed, after deleting their copy: %v", j)
	}
	// The admin lets them in: no longer kept as removed.
	ada.must(204, "POST", "/v1/me/groups/"+gid+"/joins/"+bolaID.String()+"/approve", nil)
	if n := countAdmin(t, w, `SELECT count(*) FROM group_removed WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("still kept as removed: %d", n)
	}
	// Leaving by themself and coming back by the link is at once.
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	bola.must(204, "DELETE", "/v1/me/groups/"+gid, nil)
	if j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "joined" {
		t.Fatalf("left by themself: %v", j)
	}
	// Removed again, then added by an admin: cleared too.
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), nil)
	if a := addedOf(t, ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})); a[bolaID] != "added" {
		t.Fatalf("added: %v", a)
	}
	if n := countAdmin(t, w, `SELECT count(*) FROM group_removed WHERE group_id = $1`, gid); n != 0 {
		t.Fatalf("still kept as removed after the add: %d", n)
	}
}

// 3. The public preview says nothing of approval: someone an admin blocked
// sees what anyone does; members see approve_joins on the group.
func TestGroupsReviewPreviewTellsNoBlock(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348039990011", "Ada Obi")
	chidi, chidiID := w.many(t, "+2348039990013", "Chidi Eze")
	gid, _ := newGroup(t, ada, "Club")
	code := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
	anon := &client{t: t, base: w.base}
	before := anon.must(200, "GET", "/v1/group-links/"+code, nil)
	chat := chatWith(t, ada, chidiID)
	ada.must(200, "POST", "/v1/me/people/"+chat+"/block", nil)
	p := chidi.must(200, "GET", "/v1/group-links/"+code, nil)
	if _, ok := p["approve"]; ok || p["name"] != before["name"] || p["members"] != before["members"] || p["full"] != before["full"] {
		t.Fatalf("the preview to someone blocked: %v (anyone: %v)", p, before)
	}
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	if q := anon.must(200, "GET", "/v1/group-links/"+code, nil); q["approve"] != nil {
		t.Fatalf("the preview tells approval: %v", q)
	}
	if g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil); g["approve_joins"] != true {
		t.Fatalf("a member's view: %v", g)
	}
	_ = adaID
}

// 4. A join while the link is being turned off or replaced, with the
// group's row held: the join waits for it and finds the link gone.
func TestGroupsReviewJoinRacesLinkOff(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348039990041", "Ada Obi")
	chidi, _ := w.many(t, "+2348039990043", "Chidi Eze")
	gid, _ := newGroup(t, ada, "Club")
	for _, how := range []string{"off", "replace"} {
		code := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
		locked := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- db.InPersonTx(ctx, w.env.DB.App, adaID, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SELECT 1 FROM people_groups WHERE id = $1 FOR UPDATE`, uuid.MustParse(gid)); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE group_links SET replaced_at = now() WHERE group_id = $1 AND replaced_at IS NULL`, uuid.MustParse(gid)); err != nil {
					return err
				}
				if how == "replace" {
					if _, err := tx.Exec(ctx, `INSERT INTO group_links (id, group_id, code_hash, nonce, created_by) VALUES ($1, $2, $3, $4, $5)`,
						uuid.New(), uuid.MustParse(gid), make([]byte, 32), make([]byte, 16), adaID); err != nil {
						return err
					}
				}
				close(locked)
				time.Sleep(700 * time.Millisecond)
				return nil
			})
		}()
		<-locked
		st, body := chidi.do("POST", "/v1/me/group-links/"+code+"/join", nil)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if st != 410 || body["gone"] != true {
			t.Fatalf("%s: a join while the link went: %d %v", how, st, body)
		}
		if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM group_links WHERE group_id = $1`, uuid.MustParse(gid)); err != nil {
			t.Fatal(err)
		}
	}
	if n := countAdmin(t, w, `SELECT count(*) FROM group_members WHERE group_id = $1`, gid); n != 1 {
		t.Fatalf("members: %d", n)
	}
}

// 5. Someone who left sees nothing new: reactions and read or delivered
// marks after they left, changes on the live connection, and they can't
// block, by the group, someone who came in after they left.
func TestGroupsReviewLeftSeesNothingNew(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348039990021", "Ada Obi")
	bola, bolaID := w.many(t, "+2348039990022", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348039990023", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348039990024", "Dayo Ola")
	for _, c := range []*client{bola, chidi, dayo} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Club", bolaID, chidiID)
	m := gsend(t, bola, gid, "hello")
	mine := gsend(t, chidi, gid, "from Chidi")
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string), map[string]any{"reaction": "❤️"})
	chidi.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	events, _, closeC := liveStream(t, chidi, "")
	defer closeC()
	drain(events, 1500*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	// After Chidi left: Bola reacts, Ada reads Chidi's message.
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string), map[string]any{"reaction": "👍"})
	ada.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	bola.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	r := chidi.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string)+"/reactions", nil)["reactions"].([]any)
	if len(r) != 1 || r[0].(map[string]any)["reaction"] != "❤️" {
		t.Fatalf("Chidi sees reactions made after leaving: %v", r)
	}
	for _, x := range gmessages(t, chidi, gid) {
		if x["id"] == m["id"] {
			if rs, _ := x["reactions"].([]any); len(rs) != 1 {
				t.Fatalf("the counts after leaving: %v", rs)
			}
		}
		if x["id"] == mine["id"] && x["status"] == "read" {
			t.Fatalf("Chidi's own message turned read after he left: %v", x)
		}
	}
	info := chidi.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+mine["id"].(string)+"/info", nil)
	if rb, _ := info["read_by"].([]any); len(rb) != 0 {
		t.Fatalf("read after Chidi left, shown to him: %v", info)
	}
	for _, ev := range drain(events, 2500*time.Millisecond) {
		if ev.Event == "group_message" || ev.Event == "group_receipts" {
			t.Fatalf("Chidi's connection after leaving: %v", ev)
		}
	}
	// Ada, in the group, sees both reactions and read marks as ever.
	if r := ada.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string)+"/reactions", nil)["reactions"].([]any); len(r) != 2 {
		t.Fatalf("Ada's reactions: %v", r)
	}
	// Dayo joins after Chidi left: Chidi can't block him by the group (as
	// for any unknown id); Bola, in with Dayo, can.
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{dayoID}})
	unknown := chidi.must(404, "POST", "/v1/me/groups/"+gid+"/members/"+uuid.New().String()+"/block", nil)
	later := chidi.must(404, "POST", "/v1/me/groups/"+gid+"/members/"+dayoID.String()+"/block", nil)
	if unknown["error"] != later["error"] {
		t.Fatalf("told apart: %v %v", unknown, later)
	}
	chidi.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+bolaID.String()+"/block", nil)
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+dayoID.String()+"/block", nil)
	_ = dayo
}

// 6. Deleting an account takes every invite to the person, the unseen
// ones too; the definer that does it is for the deletion alone.
func TestGroupsReviewDeletionTakesHiddenInvites(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348039990031", "Ada Obi")
	gb, gbID := w.many(t, "+2348039990032", "Gbenga Ola")
	chat := chatWith(t, gb, adaID)
	gb.must(200, "POST", "/v1/me/people/"+chat+"/block", nil)
	_, added := newGroup(t, ada, "Club", gbID)
	if added[gbID] != "invited" {
		t.Fatalf("added: %v", added)
	}
	if _, err := asApp(t, w, gbID, `SELECT taskiem_group_forget()`); err == nil {
		t.Fatal("a live account forgot its invites")
	}
	if n := countAdmin(t, w, `SELECT count(*) FROM group_invites WHERE person_id = $1`, gbID); n != 1 {
		t.Fatalf("the unseen invite: %d", n)
	}
	gb.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	gb.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348039990032")})
	if n := countAdmin(t, w, `SELECT count(*) FROM group_invites WHERE person_id = $1`, gbID); n != 0 {
		t.Fatalf("invites of the deleted account: %d", n)
	}
}

// 7. Row-level security holds to the API's rules, by direct SQL as the
// API's role in the person's own scope.
func TestGroupsReviewRowLevelSecurityHoldsTheRules(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348039990061", "Ada Obi")
	bola, bolaID := w.many(t, "+2348039990062", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348039990063", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348039990064", "Dayo Ola")
	for _, c := range []*client{bola, chidi, dayo} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Club")
	g := uuid.MustParse(gid)
	gsend(t, ada, gid, "before Bola")
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID, chidiID}})
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), map[string]any{"admin": true})
	seen := func(person uuid.UUID) (n int) {
		t.Helper()
		if err := db.InPersonTx(ctx, w.env.DB.App, person, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM group_messages WHERE group_id = $1 AND kind <> 'event'`, g).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	refused := func(what string, person uuid.UUID, q string, args ...any) {
		t.Helper()
		if _, err := asApp(t, w, person, q, args...); err == nil || !strings.Contains(err.Error(), "42501") && !strings.Contains(err.Error(), "row-level security") {
			t.Fatalf("%s: %v", what, err)
		}
	}
	none := func(what string, person uuid.UUID, q string, args ...any) {
		t.Helper()
		n, err := asApp(t, w, person, q, args...)
		if err == nil && n != 0 {
			t.Fatalf("%s: %d rows", what, n)
		}
	}

	// Memberships.
	refused("an admin widens what they see", bolaID, `UPDATE group_members SET visible = tstzmultirange(tstzrange(NULL, NULL)) WHERE group_id = $1 AND person_id = $2`, g, bolaID)
	refused("an admin moves their own join", bolaID, `UPDATE group_members SET joined_at = joined_at - interval '1 day' WHERE group_id = $1 AND person_id = $2`, g, bolaID)
	refused("an admin widens another's", bolaID, `UPDATE group_members SET visible = tstzmultirange(tstzrange(NULL, NULL)) WHERE group_id = $1 AND person_id = $2`, g, chidiID)
	refused("an admin mutes another", bolaID, `UPDATE group_members SET muted = true WHERE group_id = $1 AND person_id = $2`, g, chidiID)
	refused("an admin removes as 'left'", bolaID, `UPDATE group_members SET left_at = now(), left_how = 'left' WHERE group_id = $1 AND person_id = $2`, g, chidiID)
	refused("an admin adds with history", bolaID, `INSERT INTO group_members (group_id, person_id, how, added_by, visible)
		VALUES ($1, $2, 'added', $3, tstzmultirange(tstzrange(NULL, NULL)))`, g, dayoID, bolaID)
	refused("an admin adds an admin", bolaID, `INSERT INTO group_members (group_id, person_id, role, how, added_by, visible)
		VALUES ($1, $2, 'admin', 'added', $3, tstzmultirange(tstzrange(now(), NULL)))`, g, dayoID, bolaID)
	refused("a member leaves seeing on", chidiID, `UPDATE group_members SET left_at = clock_timestamp(), left_how = 'left',
		left_sealed = (SELECT sealed FROM people_groups WHERE id = $1) WHERE group_id = $1 AND person_id = $2`, g, chidiID)
	if seen(bolaID) != 0 {
		t.Fatalf("Bola sees %d from before he joined", seen(bolaID))
	}
	gsend(t, ada, gid, "after Bola joined")
	// What the API does by SQL still works: leaving, seeing nothing from then.
	if _, err := asApp(t, w, chidiID, `WITH t AS (SELECT clock_timestamp() AS at) UPDATE group_members SET left_at = t.at, left_how = 'left',
		left_sealed = (SELECT sealed FROM people_groups WHERE id = $1), visible = visible - tstzmultirange(tstzrange(t.at, NULL)) FROM t
		WHERE group_id = $1 AND person_id = $2`, g, chidiID); err != nil {
		t.Fatalf("leaving by the API's own statement: %v", err)
	}
	gsend(t, ada, gid, "after Chidi left")
	if seen(chidiID) != 1 {
		t.Fatalf("Chidi sees %d after leaving", seen(chidiID))
	}
	// Their own settings still change once left.
	if g := chidi.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"archived": true}); g["archived"] != true {
		t.Fatalf("archived after leaving: %v", g)
	}
	refused("someone who left moves their leaving", chidiID, `UPDATE group_members SET left_at = left_at + interval '1 hour' WHERE group_id = $1 AND person_id = $2`, g, chidiID)
	refused("an admin widens someone who left", adaID, `UPDATE group_members SET visible = tstzmultirange(tstzrange(NULL, NULL)) WHERE group_id = $1 AND person_id = $2`, g, chidiID)

	// The group's row.
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), map[string]any{"admin": false})
	refused("a member changes the settings", bolaID, `UPDATE people_groups SET admins_only_post = true WHERE id = $1`, g)
	refused("a member changes the info", bolaID, `UPDATE people_groups SET sealed = '\x00' WHERE id = $1`, g)
	refused("a member turns the clock back", bolaID, `UPDATE people_groups SET last_message_at = last_message_at - interval '1 day' WHERE id = $1`, g)
	if _, err := asApp(t, w, bolaID, `UPDATE people_groups SET last_message_at = clock_timestamp() WHERE id = $1`, g); err != nil {
		t.Fatalf("a member's message moves the group: %v", err)
	}
	none("a member deletes the group", bolaID, `DELETE FROM people_groups WHERE id = $1`, g)
	if n := countAdmin(t, w, `SELECT count(*) FROM people_groups WHERE id = $1`, g); n != 1 {
		t.Fatal("the group went")
	}
	refused("a member sets the photo", bolaID, `INSERT INTO group_photos (group_id, type, sealed) VALUES ($1, 'image/png', '\x00')`, g)
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"admins_only_edit": false})
	if _, err := asApp(t, w, bolaID, `INSERT INTO group_photos (group_id, type, sealed) VALUES ($1, 'image/png', '\x00')`, g); err != nil {
		t.Fatalf("an editor sets the photo: %v", err)
	}

	// A message's file and tokens are its sender's.
	adaMsg := uuid.MustParse(gsend(t, ada, gid, "Ada's words")["id"].(string))
	refused("a file on another's message", bolaID, `INSERT INTO group_files (group_id, message_id, type, size, sealed) VALUES ($1, $2, 'image/png', 1, '\x00')`, g, adaMsg)
	refused("tokens on another's message", bolaID, `INSERT INTO group_search (group_id, message_id, token) VALUES ($1, $2, '\x00112233445566778899aabbccddeeff')`, g, adaMsg)
	none("another's tokens taken away", bolaID, `DELETE FROM group_search WHERE group_id = $1 AND message_id = $2`, g, adaMsg)
	if n := countAdmin(t, w, `SELECT count(*) FROM group_search WHERE group_id = $1 AND message_id = $2`, g, adaMsg); n == 0 {
		t.Fatal("Ada's tokens went")
	}
	_ = dayo
}

// 8. A group message's pushes, queued after the send's commit in one
// statement: to the right members (mute, mentions, the groups switch, a
// block either way), once per message; forwarding to groups pushes each.
func TestGroupsReviewPushInOneStatement(t *testing.T) {
	w := newJoinedWorld(t)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	svc := newPushService(t)
	br := newPushBrowser(t)
	ada, adaID := w.many(t, "+2348039990071", "Ada Obi")
	names := []string{"Bola Ade", "Chidi Eze", "Dayo Ola", "Efe Uche", "Femi Ojo", "Gbenga Ola"}
	var cs []*client
	var ids []uuid.UUID
	for i, n := range names {
		c, id := w.many(t, fmt.Sprintf("+23480399900%d%d", 8, i), n)
		adds(t, ada, c)
		adds(t, c, ada)
		c.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/" + id.String(), "keys": map[string]any{"p256dh": br.p256dh, "auth": br.au}})
		cs = append(cs, c)
		ids = append(ids, id)
	}
	bola, chidi, dayo, efe, femi, gbenga := cs[0], cs[1], cs[2], cs[3], cs[4], cs[5]
	gid, _ := newGroup(t, ada, "Trip", ids...)
	other, _ := newGroup(t, ada, "Other", ids[0])
	chidi.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": true})
	dayo.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": true})
	efe.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": true, "mentions": false})
	femi.must(200, "PUT", "/v1/me/notifications", map[string]any{"groups": false})
	gbenga.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+adaID.String()+"/block", nil)
	pushSettle(t, w.srv)
	before := map[uuid.UUID]int{}
	for _, id := range ids {
		before[id] = svc.count("/" + id.String())
	}
	msg := ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "@Dayo @Efe bus at 6", "mentions": []uuid.UUID{ids[2], ids[3]}})
	pushSettle(t, w.srv)
	want := map[uuid.UUID]int{ids[0]: 1, ids[1]: 0, ids[2]: 1, ids[3]: 0, ids[4]: 0, ids[5]: 0}
	for i, id := range ids {
		if got := svc.count("/"+id.String()) - before[id]; got != want[id] {
			t.Fatalf("%s: %d pushes, want %d", names[i], got, want[id])
		}
	}
	if p := br.decrypt(t, svc.bodies["/"+ids[2].String()][len(svc.bodies["/"+ids[2].String()])-1]); p.Body != "Ada Obi mentioned you: @Dayo @Efe bus at 6" || p.Title != "Trip" {
		t.Fatalf("Dayo's push: %+v", p)
	}
	if p := br.decrypt(t, svc.bodies["/"+ids[0].String()][len(svc.bodies["/"+ids[0].String()])-1]); p.Body != "Ada Obi: @Dayo @Efe bus at 6" {
		t.Fatalf("Bola's push: %+v", p)
	}
	// Once per message: asked again, nothing more is queued.
	var n int
	if err := db.InPersonTx(context.Background(), w.env.DB.App, adaID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT taskiem_group_push($1, $2, '{webpush}', 'x', 'y', 'z')`, gid, msg["id"]).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("pushed again: %d %v", n, err)
	}
	// Not someone else's message.
	more := gsend(t, ada, gid, "more")["id"]
	if err := db.InPersonTx(context.Background(), w.env.DB.App, ids[0], func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT taskiem_group_push($1, $2, '{webpush}', 'x', 'y', 'z')`, gid, more).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("Bola pushed Ada's message: %d %v", n, err)
	}
	pushSettle(t, w.srv)
	// Forwarded to two groups: Bola is pushed in each.
	b0 := svc.count("/" + ids[0].String())
	fw := ada.must(200, "POST", "/v1/me/forward", map[string]any{"group_id": gid, "message_id": msg["id"], "to_groups": []string{gid, other}})
	if s, _ := fw["sent"].([]any); len(s) != 2 {
		t.Fatalf("forward: %v", fw)
	}
	pushSettle(t, w.srv)
	if got := svc.count("/"+ids[0].String()) - b0; got != 2 {
		t.Fatalf("forwarded pushes to Bola: %d", got)
	}
	_ = bola
}

// 9. Another member's mute, archive or mentions wakes no one's ticks; a
// read does.
func TestGroupsReviewMuteWakesNoTicks(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348039990091", "Ada Obi")
	bola, bolaID := w.many(t, "+2348039990092", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348039990093", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Club", bolaID, chidiID)
	m := gsend(t, ada, gid, "hello")
	events, _, closeA := liveStream(t, ada, "")
	defer closeA()
	drain(events, 2*time.Second)
	runs := w.srv.GroupTickRuns()
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": true})
	chidi.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"archived": true, "mentions": false})
	for _, ev := range drain(events, 2500*time.Millisecond) {
		if ev.Event == "group_receipts" || ev.Event == "group" {
			t.Fatalf("Ada woken by another's settings: %v", ev)
		}
	}
	if got := w.srv.GroupTickRuns(); got != runs {
		t.Fatalf("ticks recomputed %d times for others' settings", got-runs)
	}
	bola.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	chidi.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	got := await(events, 5*time.Second, func(ev streamEvent) bool {
		if ev.Event != "group_receipts" {
			return false
		}
		for _, x := range ev.Data["messages"].([]any) {
			if x.(map[string]any)["id"] == m["id"] && x.(map[string]any)["status"] == "read" {
				return true
			}
		}
		return false
	})
	if got[0] == nil || w.srv.GroupTickRuns() == runs {
		t.Fatalf("reads did not move the ticks: %v", got[0])
	}
}

// 10. Adding many at once: each one's answer, in two queries' checks.
func TestGroupsReviewAddManyAtOnce(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348039990101", "Ada Obi")
	var ids []uuid.UUID
	var cs []*client
	for i := 0; i < 8; i++ {
		c, id := w.many(t, fmt.Sprintf("+23480399902%d0", i), fmt.Sprintf("Person %c", 'A'+i))
		cs = append(cs, c)
		ids = append(ids, id)
	}
	// 0 to 3 and Ada have each other; 4 is Ada's contact but hasn't added
	// her ("Who can add me": My contacts); 5 is not Ada's contact; 6 is in
	// the group; 7 left it.
	for _, i := range []int{0, 1, 2, 3, 6, 7} {
		adds(t, ada, cs[i])
		adds(t, cs[i], ada)
	}
	adds(t, ada, cs[4])
	adds(t, cs[5], ada)
	gid, _ := newGroup(t, ada, "Many", ids[6], ids[7])
	cs[7].must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	a := addedOf(t, ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": append(ids, uuid.New())}))
	want := []string{"added", "added", "added", "added", "invited", "invited", "member", "added"}
	for i, id := range ids {
		if a[id] != want[i] {
			t.Fatalf("person %d: %q, want %q (%v)", i, a[id], want[i], a)
		}
	}
	if len(a) != 9 {
		t.Fatalf("answers: %v", a)
	}
	if g := cs[7].must(200, "GET", "/v1/me/groups/"+gid, nil); g["left"] != false {
		t.Fatalf("back in: %v", g)
	}
}
