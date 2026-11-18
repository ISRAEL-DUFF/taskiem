package api_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/push"
)

// Groups on the server (people-chatting §9, §13, §14, §17 Q7, Q16, Q17;
// docs/spec/people-chatting-5b.md A and D with the founder's answers A2 to
// A8; "As built: stage 5b, groups (server)").

// adds makes a add b to their contacts (through b's link).
func adds(t *testing.T, a, b *client) {
	t.Helper()
	addByLink(t, a, linkOf(t, b))
}

// newGroup makes c a group and answers its id and what became of each
// person added.
func newGroup(t *testing.T, c *client, name string, ids ...uuid.UUID) (string, map[uuid.UUID]string) {
	t.Helper()
	body := map[string]any{"name": name}
	if len(ids) > 0 {
		body["person_ids"] = ids
	}
	out := c.must(201, "POST", "/v1/me/groups", body)
	return out["group"].(map[string]any)["id"].(string), addedOf(t, out)
}

func addedOf(t *testing.T, out map[string]any) map[uuid.UUID]string {
	t.Helper()
	got := map[uuid.UUID]string{}
	for _, a := range out["added"].([]any) {
		m := a.(map[string]any)
		got[uuid.MustParse(m["person_id"].(string))] = m["state"].(string)
	}
	return got
}

func gsend(t *testing.T, c *client, g, text string) map[string]any {
	t.Helper()
	return c.must(201, "POST", "/v1/me/groups/"+g+"/messages", map[string]any{"text": text})
}

func gmessages(t *testing.T, c *client, g string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range c.must(200, "GET", "/v1/me/groups/"+g+"/messages", nil)["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// gbodies are the words of c's messages in g that are not events.
func gbodies(t *testing.T, c *client, g string) []string {
	t.Helper()
	var out []string
	for _, m := range gmessages(t, c, g) {
		if m["kind"] != "event" {
			out = append(out, m["body"].(string))
		}
	}
	return out
}

func gmessage(t *testing.T, c *client, g, body string) map[string]any {
	t.Helper()
	for _, m := range gmessages(t, c, g) {
		if m["body"] == body {
			return m
		}
	}
	t.Fatalf("no message %q", body)
	return nil
}

func memberIDs(t *testing.T, c *client, g string) []string {
	t.Helper()
	var out []string
	for _, m := range c.must(200, "GET", "/v1/me/groups/"+g+"/members", nil)["members"].([]any) {
		out = append(out, m.(map[string]any)["person_id"].(string))
	}
	slices.Sort(out)
	return out
}

// Making a group and adding people (§9, A3, A8): the creator is an admin;
// a person is added at once only when the admin has them as a contact and
// their "Who can add me to groups" lets the admin (default: people they
// added), otherwise an invite waits in their Requests; across a block the
// admin is answered "invited" just the same and the person never sees it.
func TestGroupsMakeAndAdd(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031140001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140002", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140003", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031140004", "Dayo Bello")
	efe, efeID := w.many(t, "+2348031140005", "Efe Uche")
	femi, femiID := w.many(t, "+2348031140006", "Femi Ojo")
	gbenga, gbengaID := w.many(t, "+2348031140007", "Gbenga Ola")

	// Bola: each has the other (added). Chidi: Ada has Chidi, not back.
	// Dayo: "Everyone", Ada has not added Dayo. Efe: "Everyone", Ada added
	// Efe. Femi: "Nobody", each has the other. Gbenga: each has the
	// other, "Everyone", and Gbenga blocked Ada.
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	setPrivacy(t, dayo, map[string]any{"groups_add": "everyone"})
	setPrivacy(t, efe, map[string]any{"groups_add": "everyone"})
	adds(t, ada, efe)
	adds(t, ada, femi)
	adds(t, femi, ada)
	setPrivacy(t, femi, map[string]any{"groups_add": "nobody"})
	adds(t, ada, gbenga)
	adds(t, gbenga, ada)
	setPrivacy(t, gbenga, map[string]any{"groups_add": "everyone"})
	gchat := chatWith(t, gbenga, adaID)
	gbenga.must(200, "POST", "/v1/me/people/"+gchat+"/block", nil)
	if p := dayo.must(200, "GET", "/v1/me/privacy/people", nil); p["groups_add"] != "everyone" {
		t.Fatalf("the setting: %v", p)
	}
	if p := bola.must(200, "GET", "/v1/me/privacy/people", nil); p["groups_add"] != "contacts" {
		t.Fatalf("the default: %v", p)
	}

	out := ada.must(201, "POST", "/v1/me/groups", map[string]any{"name": "  Family  ", "description": "Sunday lunch",
		"person_ids": []uuid.UUID{bolaID, chidiID, dayoID, efeID, femiID, gbengaID, adaID, uuid.New()}})
	g := out["group"].(map[string]any)
	gid := g["id"].(string)
	if g["name"] != "Family" || g["description"] != "Sunday lunch" || g["role"] != "admin" || g["kind"] != "group" || g["can_post"] != true ||
		g["can_edit"] != true || g["admins_only_edit"] != true || g["admins_only_post"] != false || !strings.Contains(g["kept"].(string), "not end-to-end encrypted") {
		t.Fatalf("made: %v", g)
	}
	got := addedOf(t, out)
	want := map[uuid.UUID]string{bolaID: "added", chidiID: "invited", dayoID: "invited", efeID: "added", femiID: "invited", gbengaID: "invited"}
	for id, st := range want {
		if got[id] != st {
			t.Fatalf("%s: %q, want %q (%v)", id, got[id], st, got)
		}
	}
	if len(got) != 7 || got[adaID] != "" {
		t.Fatalf("the answer: %v", got)
	}
	if ids := memberIDs(t, ada, gid); len(ids) != 3 {
		t.Fatalf("members: %v", ids)
	}
	// The admin sees the invites waiting, Gbenga's among them (A8).
	m := ada.must(200, "GET", "/v1/me/groups/"+gid+"/members", nil)
	var invited []string
	for _, i := range m["invited"].([]any) {
		invited = append(invited, i.(map[string]any)["person_id"].(string))
	}
	slices.Sort(invited)
	wantInv := []string{chidiID.String(), dayoID.String(), femiID.String(), gbengaID.String()}
	slices.Sort(wantInv)
	if !slices.Equal(invited, wantInv) {
		t.Fatalf("invited: %v", invited)
	}
	// A member who is not an admin sees no invites.
	if mb := bola.must(200, "GET", "/v1/me/groups/"+gid+"/members", nil); mb["invited"] != nil {
		t.Fatalf("a member sees invites: %v", mb)
	}
	// Chidi's invite waits in Requests, with the group's name and who
	// invited; Gbenga, who blocked Ada, sees none, and nothing of the
	// group.
	inv := listOf(t, chidi, "/v1/me/groups/invites", "invites")
	if len(inv) != 1 || inv[0]["name"] != "Family" || inv[0]["by"] != "Ada Obi" || inv[0]["group_id"] != gid {
		t.Fatalf("Chidi's invites: %v", inv)
	}
	if inv := listOf(t, gbenga, "/v1/me/groups/invites", "invites"); len(inv) != 0 {
		t.Fatalf("Gbenga sees an invite across a block: %v", inv)
	}
	gbenga.must(404, "GET", "/v1/me/groups/"+gid, nil)
	gbenga.must(404, "POST", "/v1/me/groups/invites/"+gid+"/accept", nil)
	if c := gbenga.must(200, "GET", "/v1/me/conversations", nil); c["invites"] != float64(0) {
		t.Fatalf("Gbenga's count: %v", c)
	}
	if c := chidi.must(200, "GET", "/v1/me/conversations", nil); c["invites"] != float64(1) {
		t.Fatalf("Chidi's count: %v", c)
	}
	// Someone invited sees the name, never the messages.
	gsend(t, ada, gid, "Before Chidi")
	chidi.must(404, "GET", "/v1/me/groups/"+gid+"/messages", nil)
	chidi.must(404, "GET", "/v1/me/groups/"+gid, nil)

	// Accepting: Chidi joins from now (§17 Q16).
	acc := chidi.must(200, "POST", "/v1/me/groups/invites/"+gid+"/accept", nil)
	if acc["role"] != "member" || acc["members"] != float64(4) {
		t.Fatalf("accepted: %v", acc)
	}
	gsend(t, ada, gid, "After Chidi")
	if b := gbodies(t, chidi, gid); !slices.Equal(b, []string{"After Chidi"}) {
		t.Fatalf("Chidi sees: %v", b)
	}
	if b := gbodies(t, bola, gid); !slices.Equal(b, []string{"Before Chidi", "After Chidi"}) {
		t.Fatalf("Bola sees: %v", b)
	}
	// Declining is not told; a second add of Dayo stays an invite.
	dayo.must(204, "POST", "/v1/me/groups/invites/"+gid+"/decline", nil)
	dayo.must(404, "POST", "/v1/me/groups/invites/"+gid+"/decline", nil)
	re := ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{dayoID, bolaID}})
	if st := addedOf(t, re); st[dayoID] != "invited" || st[bolaID] != "member" {
		t.Fatalf("again: %v", st)
	}
	// Only an admin adds.
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{femiID}})
	// Femi turns "Who can add me" to My contacts: added now.
	setPrivacy(t, femi, map[string]any{"groups_add": "contacts"})
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/invites/"+femiID.String(), nil)
	if st := addedOf(t, ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{femiID}})); st[femiID] != "added" {
		t.Fatalf("Femi: %v", st)
	}
	// Bad settings are refused.
	code, _ := ada.do("PUT", "/v1/me/privacy/people", map[string]any{"groups_add": "anyone"})
	if code != 400 {
		t.Fatalf("a bad audience: %d", code)
	}
	ada.must(400, "POST", "/v1/me/groups", map[string]any{"name": " "})
	ada.must(400, "POST", "/v1/me/groups", map[string]any{"name": strings.Repeat("x", 81)})
	// The group is in the one Chats list as kind group, with its name.
	var row map[string]any
	for _, c := range listOf(t, bola, "/v1/me/conversations", "chats") {
		if c["id"] == gid {
			row = c
		}
	}
	if row == nil || row["kind"] != "group" || row["name"] != "Family" || row["group"].(map[string]any)["members"] != float64(5) {
		t.Fatalf("the one list: %v", row)
	}
	_ = efe
}

// Sealed under the group's key, one copy; history from joining (§17 Q16);
// leaving and removal keep the copy up to then and nothing after (A5);
// deleting one's copy; the last one out closes the group (§14). Checked
// through the database's own row-level security as well as the API.
func TestGroupsHistoryLeaveAndRemove(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140101", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140102", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140103", "Chidi Eze")
	outsider, outsiderID := w.many(t, "+2348031140104", "Out Sider")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Book club", bolaID)
	gsend(t, ada, gid, "The secret word is mango")
	gsend(t, bola, gid, "Hello all")
	// One copy, sealed: the words are nowhere in the clear.
	var plain, copies int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE position(convert_to('mango', 'UTF8') in sealed) > 0), count(*)
		FROM group_messages WHERE group_id = $1 AND kind = 'text'`, gid).Scan(&plain, &copies); err != nil || plain != 0 || copies != 2 {
		t.Fatalf("sealed: %d of %d %v", plain, copies, err)
	}
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{chidiID}})
	gsend(t, ada, gid, "Welcome Chidi")
	if b := gbodies(t, chidi, gid); !slices.Equal(b, []string{"Welcome Chidi"}) {
		t.Fatalf("Chidi from joining: %v", b)
	}
	// The database itself shows Chidi nothing before his join, and the
	// outsider nothing at all.
	seen := func(person uuid.UUID) (n int) {
		t.Helper()
		if err := db.InPersonTx(ctx, w.env.DB.App, person, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM group_messages WHERE group_id = $1 AND kind <> 'event'`, gid).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if seen(chidiID) != 1 || seen(outsiderID) != 0 || seen(bolaID) != 3 {
		t.Fatalf("by row-level security: chidi %d, outsider %d, bola %d", seen(chidiID), seen(outsiderID), seen(bolaID))
	}
	outsider.must(404, "GET", "/v1/me/groups/"+gid, nil)
	outsider.must(404, "GET", "/v1/me/groups/"+gid+"/messages", nil)
	outsider.must(404, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "hi"})
	// Even forging a membership-free insert is refused by the policy.
	if err := db.InPersonTx(ctx, w.env.DB.App, outsiderID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO group_messages (group_id, id, sender_id, kind) VALUES ($1, $2, $3, 'text')`, gid, uuid.New(), outsiderID)
		return err
	}); err == nil {
		t.Fatal("an outsider wrote to a group")
	}
	// A member cannot make themself an admin in the database.
	if err := db.InPersonTx(ctx, w.env.DB.App, bolaID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE group_members SET role = 'admin' WHERE group_id = $1 AND person_id = $2`, gid, bolaID)
		return err
	}); err == nil {
		t.Fatal("a member made themself an admin")
	}

	// Bola leaves: the copy up to now stays, nothing new, can't write (A5).
	left := bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	if left["closed"] != false || left["group"].(map[string]any)["left"] != true || left["group"].(map[string]any)["left_how"] != "left" {
		t.Fatalf("left: %v", left)
	}
	gsend(t, ada, gid, "After Bola left")
	if b := gbodies(t, bola, gid); !slices.Equal(b, []string{"The secret word is mango", "Hello all", "Welcome Chidi"}) {
		t.Fatalf("Bola's copy: %v", b)
	}
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "still here?"})
	bola.must(403, "GET", "/v1/me/groups/"+gid+"/members", nil)
	if seen(bolaID) != 3 {
		t.Fatalf("Bola sees %d by row-level security", seen(bolaID))
	}
	var lastEvent map[string]any
	for _, m := range gmessages(t, ada, gid) {
		if m["kind"] == "event" {
			lastEvent = m
		}
	}
	if lastEvent["body"] != "Bola Ade left" {
		t.Fatalf("the event: %v", lastEvent)
	}
	// Removed: the same for Chidi.
	bola.must(403, "DELETE", "/v1/me/groups/"+gid+"/members/"+chidiID.String(), nil)
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/members/"+chidiID.String(), nil)
	gsend(t, ada, gid, "After Chidi was removed")
	if b := gbodies(t, chidi, gid); !slices.Equal(b, []string{"Welcome Chidi", "After Bola left"}) {
		t.Fatalf("Chidi's copy: %v", b)
	}
	if g := chidi.must(200, "GET", "/v1/me/groups/"+gid, nil); g["left"] != true || g["left_how"] != "removed" || g["can_post"] != false {
		t.Fatalf("Chidi's group: %v", g)
	}
	// Nothing new of the group either: its name as it was, no photo.
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"name": "Book club (new)"})
	if code, _ := ada.putFile(t, "/v1/me/groups/"+gid+"/photo", "p.png", pngPhoto(t, 30)); code != 200 {
		t.Fatalf("photo: %d", code)
	}
	if g := chidi.must(200, "GET", "/v1/me/groups/"+gid, nil); g["name"] != "Book club" || g["photo"] != nil || g["members"] != float64(0) {
		t.Fatalf("Chidi sees the group as it was: %v", g)
	}
	if c, _, _ := rawGet(t, w.base+"/v1/me/groups/"+gid+"/photo", chidi.token); c != 404 {
		t.Fatalf("the photo after leaving: %d", c)
	}
	chidi.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "let me in"})
	// Bola deletes the copy: gone from the list for good.
	bola.must(204, "DELETE", "/v1/me/groups/"+gid, nil)
	bola.must(404, "GET", "/v1/me/groups/"+gid, nil)
	if l := listOf(t, bola, "/v1/me/groups", "groups"); len(l) != 0 {
		t.Fatalf("Bola's list: %v", l)
	}
	// Re-added, Bola sees from the new join only.
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})
	gsend(t, ada, gid, "Bola is back")
	if b := gbodies(t, bola, gid); !slices.Equal(b, []string{"Bola is back"}) {
		t.Fatalf("Bola back: %v", b)
	}
	// A member deleting their copy while in it: cleared, out of the list
	// until a new message.
	bola.must(204, "DELETE", "/v1/me/groups/"+gid, nil)
	if b := gbodies(t, bola, gid); len(b) != 0 {
		t.Fatalf("cleared: %v", b)
	}
	if l := listOf(t, bola, "/v1/me/groups", "groups"); len(l) != 0 {
		t.Fatalf("hidden: %v", l)
	}
	gsend(t, ada, gid, "New one")
	if l := listOf(t, bola, "/v1/me/groups", "groups"); len(l) != 1 || l[0]["last"] != "New one" {
		t.Fatalf("back in the list: %v", l)
	}
	// The last ones out: Bola leaves, Ada (the last admin and member)
	// leaves: the group is closed and deleted, Chidi's copy with it.
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	closed := ada.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	if closed["closed"] != true {
		t.Fatalf("closed: %v", closed)
	}
	var rows int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_groups WHERE id = $1) + (SELECT count(*) FROM group_messages WHERE group_id = $1)
		+ (SELECT count(*) FROM group_members WHERE group_id = $1) + (SELECT count(*) FROM group_keys WHERE group_id = $1)`, gid).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("closed group's rows: %d %v", rows, err)
	}
	chidi.must(404, "GET", "/v1/me/groups/"+gid, nil)
}

// Roles (§9, A2): admins only post when set; info edited by admins only by
// default, with a switch to let everyone; admins made and unmade; the
// last admin leaving makes the longest member an admin.
func TestGroupsRoles(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031140201", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140202", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140203", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Team", bolaID)
	time.Sleep(5 * time.Millisecond)
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{chidiID}})
	// Info: admins only by default (A2).
	bola.must(403, "PUT", "/v1/me/groups/"+gid, map[string]any{"name": "Bola's team"})
	bola.must(403, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	if g := ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"admins_only_edit": false}); g["admins_only_edit"] != false {
		t.Fatalf("switch: %v", g)
	}
	if g := bola.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"name": "Bola's team", "description": "Ours"}); g["name"] != "Bola's team" || g["can_edit"] != true {
		t.Fatalf("renamed by a member: %v", g)
	}
	if g := chidi.must(200, "GET", "/v1/me/groups/"+gid, nil); g["name"] != "Bola's team" || g["description"] != "Ours" {
		t.Fatalf("Chidi sees: %v", g)
	}
	// Only admins post.
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"admins_only_post": true})
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "may I?"})
	if g := bola.must(200, "GET", "/v1/me/groups/"+gid, nil); g["can_post"] != false {
		t.Fatalf("can_post: %v", g)
	}
	gsend(t, ada, gid, "Admins only")
	// Make Bola an admin: Bola posts.
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), map[string]any{"admin": true})
	gsend(t, bola, gid, "Now I can")
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), map[string]any{"admin": false})
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "not any more"})
	// The last admin can't unmake themself.
	ada.must(409, "PUT", "/v1/me/groups/"+gid+"/members/"+adaID.String(), map[string]any{"admin": false})
	// The last admin leaves: the member in it longest (Bola) is made one.
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	var roles []string
	for _, m := range bola.must(200, "GET", "/v1/me/groups/"+gid+"/members", nil)["members"].([]any) {
		mm := m.(map[string]any)
		roles = append(roles, mm["name"].(string)+":"+mm["role"].(string))
	}
	if !slices.Equal(roles, []string{"You:admin", "Chidi Eze:member"}) {
		t.Fatalf("roles: %v", roles)
	}
	var promoted bool
	for _, m := range gmessages(t, chidi, gid) {
		promoted = promoted || m["body"] == "Ada Obi made Bola Ade an admin"
	}
	if !promoted {
		t.Fatalf("no event: %v", gmessages(t, chidi, gid))
	}
	gsend(t, bola, gid, "My turn")
}

// The invite link (A4): off until turned on; a preview for anyone holding
// it (name, photo, members), signed in or not; joining at once by default;
// replaced (the old one 410) and turned off; "Approve new members" makes a
// join wait for an admin; someone removed waits too.
func TestGroupsLinks(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140301", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140302", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140303", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031140304", "Dayo Bello")
	gid, _ := newGroup(t, ada, "Street party")
	if l := ada.must(200, "GET", "/v1/me/groups/"+gid+"/link", nil); l["on"] != false {
		t.Fatalf("off at first: %v", l)
	}
	l := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)
	code := l["code"].(string)
	if l["on"] != true || !strings.HasSuffix(l["url"].(string), "/g/"+code) || l["qr"] == "" {
		t.Fatalf("the link: %v", l)
	}
	if code, _, _ := rawGet(t, w.base+"/v1/me/groups/"+gid+"/link/qr", ada.token); code != 200 {
		t.Fatalf("qr: %d", code)
	}
	// The preview, for anyone.
	anon := &client{t: t, base: w.base}
	p := anon.must(200, "GET", "/v1/group-links/"+code, nil)
	if p["name"] != "Street party" || p["members"] != float64(1) || p["photo"] != nil || p["approve"] != nil {
		t.Fatalf("preview: %v", p)
	}
	// A photo shows in the preview.
	if code, out := ada.putFile(t, "/v1/me/groups/"+gid+"/photo", "p.png", pngPhoto(t, 60)); code != 200 || out["photo"] == nil {
		t.Fatalf("the group's photo: %d %v", code, out)
	}
	if c, _, _ := rawGet(t, w.base+"/v1/me/groups/"+gid+"/photo", ada.token); c != 200 {
		t.Fatalf("the photo for a member: %d", c)
	}
	if c, _, _ := rawGet(t, w.base+"/v1/me/groups/"+gid+"/photo", bola.token); c != 404 {
		t.Fatalf("the photo for someone not in it: %d", c)
	}
	if p := anon.must(200, "GET", "/v1/group-links/"+code, nil); p["photo"] == nil {
		t.Fatalf("no photo: %v", p)
	}
	if c, _, _ := rawGet(t, w.base+"/v1/group-links/"+code+"/photo", ""); c != 200 {
		t.Fatalf("the photo: %d", c)
	}
	// Bola joins at once.
	j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil)
	if j["state"] != "joined" || j["group"].(map[string]any)["members"] != float64(2) {
		t.Fatalf("joined: %v", j)
	}
	if j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "member" {
		t.Fatalf("again: %v", j)
	}
	gsend(t, bola, gid, "Hi from the link")
	// Only admins see or change the link.
	bola.must(403, "GET", "/v1/me/groups/"+gid+"/link", nil)
	bola.must(403, "POST", "/v1/me/groups/"+gid+"/link", nil)
	// Replaced: the old link answers 410.
	nl := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)
	anon.must(410, "GET", "/v1/group-links/"+code, nil)
	chidi.must(410, "POST", "/v1/me/group-links/"+code+"/join", nil)
	chidi.must(404, "POST", "/v1/me/group-links/abcdefghjk/join", nil)
	code = nl["code"].(string)
	// Approve new members: the join waits for an admin.
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": true})
	if j := chidi.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" || j["group"] != nil {
		t.Fatalf("waiting: %v", j)
	}
	chidi.must(404, "GET", "/v1/me/groups/"+gid+"/messages", nil)
	reqs := listOf(t, ada, "/v1/me/groups/"+gid+"/joins", "requests")
	if len(reqs) != 1 || reqs[0]["person_id"] != chidiID.String() || reqs[0]["name"] != "Chidi Eze" {
		t.Fatalf("join requests: %v", reqs)
	}
	if g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil); g["join_requests"] != float64(1) {
		t.Fatalf("counted: %v", g)
	}
	ada.must(204, "POST", "/v1/me/groups/"+gid+"/joins/"+chidiID.String()+"/approve", nil)
	gsend(t, ada, gid, "Welcome Chidi")
	if b := gbodies(t, chidi, gid); !slices.Equal(b, []string{"Welcome Chidi"}) {
		t.Fatalf("Chidi in: %v", b)
	}
	// Dayo is turned away (not told).
	dayo.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil)
	ada.must(204, "POST", "/v1/me/groups/"+gid+"/joins/"+dayoID.String()+"/decline", nil)
	dayo.must(404, "GET", "/v1/me/groups/"+gid, nil)
	// Someone removed waits for an admin even with approval off.
	ada.must(200, "PUT", "/v1/me/groups/"+gid, map[string]any{"approve_joins": false})
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), nil)
	if j := bola.must(200, "POST", "/v1/me/group-links/"+code+"/join", nil); j["state"] != "waiting" {
		t.Fatalf("removed rejoining: %v", j)
	}
	// Turned off: 410.
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/link", nil)
	anon.must(410, "GET", "/v1/group-links/"+code, nil)
	if l := ada.must(200, "GET", "/v1/me/groups/"+gid+"/link", nil); l["on"] != false {
		t.Fatalf("off: %v", l)
	}
}

// Messages in a group (D, A7): replies, reactions counted per member,
// stars and the starred list, delete for me and for everyone, client_id,
// places and share cards, forwarding to and from groups, files.
func TestGroupsMessages(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031140401", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140402", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140403", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Neighbours", bolaID, chidiID)
	first := gsend(t, ada, gid, "Water is back")
	if first["mine"] != true || first["sender"] != "You" || first["status"] == nil {
		t.Fatalf("sent: %v", first)
	}
	again := ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "once", "client_id": "c-1"})
	if r := ada.must(200, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "once", "client_id": "c-1"}); r["id"] != again["id"] {
		t.Fatalf("client_id: %v", r)
	}
	reply := bola.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "Finally!", "reply_to": first["id"]})
	if reply["reply_to"] != first["id"] || reply["sender"] != "You" {
		t.Fatalf("reply: %v", reply)
	}
	if m := gmessage(t, ada, gid, "Finally!"); m["sender"] != "Bola Ade" || m["mine"] != false {
		t.Fatalf("Ada sees: %v", m)
	}
	// Reactions, one per member, counted.
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+first["id"].(string), map[string]any{"reaction": "👍"})
	chidi.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+first["id"].(string), map[string]any{"reaction": "👍"})
	chidi.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+first["id"].(string), map[string]any{"reaction": "🎉"})
	m := gmessage(t, ada, gid, "Water is back")
	rs := m["reactions"].([]any)
	if len(rs) != 2 || rs[0].(map[string]any)["count"] != float64(1) || m["my_reaction"] != nil {
		t.Fatalf("reactions: %v", m)
	}
	if who := listOf(t, ada, "/v1/me/groups/"+gid+"/messages/"+first["id"].(string)+"/reactions", "reactions"); len(who) != 2 || who[0]["name"] != "Bola Ade" {
		t.Fatalf("who reacted: %v", who)
	}
	// Stars are each one's own; the starred list across groups.
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+first["id"].(string), map[string]any{"starred": true})
	if m := gmessage(t, ada, gid, "Water is back"); m["starred"] != nil {
		t.Fatalf("Bola's star on Ada's: %v", m)
	}
	st := listOf(t, bola, "/v1/me/groups/starred", "starred")
	if len(st) != 1 || st[0]["message"].(map[string]any)["body"] != "Water is back" || st[0]["group"].(map[string]any)["name"] != "Neighbours" {
		t.Fatalf("starred: %v", st)
	}
	// Delete for me: Bola's copy only.
	bola.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+first["id"].(string)+"?for=me", nil)
	if b := gbodies(t, bola, gid); slices.Contains(b, "Water is back") {
		t.Fatalf("deleted for me: %v", b)
	}
	if b := gbodies(t, chidi, gid); !slices.Contains(b, "Water is back") {
		t.Fatalf("deleted for Chidi too: %v", b)
	}
	if st := listOf(t, bola, "/v1/me/groups/starred", "starred"); len(st) != 0 {
		t.Fatalf("starred after delete for me: %v", st)
	}
	// Delete for everyone: the sender's own only, within 24 hours.
	bola.must(403, "DELETE", "/v1/me/groups/"+gid+"/messages/"+again["id"].(string)+"?for=everyone", nil)
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+again["id"].(string)+"?for=everyone", nil)
	if m := gmessage(t, chidi, gid, "This message was deleted"); m["deleted"] != true {
		t.Fatalf("deleted for everyone: %v", m)
	}
	old := gsend(t, ada, gid, "Too late to delete")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE group_messages SET created_at = created_at - interval '25 hours' WHERE id = $1`, old["id"]); err != nil {
		t.Fatal(err)
	}
	// (everyone in the group from before then, so it is still seen)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE group_members SET visible = tstzmultirange(tstzrange(now() - interval '2 days', NULL)) WHERE group_id = $1`, gid); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "DELETE", "/v1/me/groups/"+gid+"/messages/"+old["id"].(string)+"?for=everyone", nil)
	// A place, and a share card that warns a member who has not added
	// the sender (Chidi added Ada: no warning; a stranger joining would).
	ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"location": map[string]any{"lat": 6.45, "lng": 3.39, "label": "Ada's gate"}})
	share := ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", splitCard("Generator diesel"))
	if m := gmessage(t, chidi, gid, ""); m == nil {
		t.Fatal("no place")
	}
	paid := bola.must(200, "POST", "/v1/me/groups/"+gid+"/messages/"+share["id"].(string)+"/paid", map[string]any{"paid": true})
	if c := paid["card"].(map[string]any); c["paid"] != true || c["paid_by_me"] != true {
		t.Fatalf("paid: %v", paid)
	}
	for _, m := range gmessages(t, ada, gid) {
		if m["kind"] == "card" {
			if c := m["card"].(map[string]any); c["paid"] != true || c["paid_by_me"] != false {
				t.Fatalf("Ada sees paid: %v", m)
			}
		}
	}
	// A photo, checked and sealed under the group's key.
	code, f := ada.postFile(t, "/v1/me/groups/"+gid+"/files", "p.png", pngPhoto(t, 80), map[string]string{"text": "The leak", "mentions": bolaID.String()})
	if code != 201 || f["attachment"] == nil || f["mentions"] == nil {
		t.Fatalf("photo: %d %v", code, f)
	}
	if c, _, _ := rawGet(t, w.base+"/v1/me/groups/"+gid+"/files/"+f["id"].(string), chidi.token); c != 200 {
		t.Fatalf("the file: %d", c)
	}
	if m := gmessage(t, bola, gid, "The leak"); m["mentions_me"] != true {
		t.Fatalf("mention: %v", m)
	}
	// Forwarding: from the group to a people chat and another group, and
	// from a people chat into a group; at most 5 at once; a share card
	// not at all.
	g2, _ := newGroup(t, ada, "Family", chidiID)
	// Bola added Ada: Ada's chat goes straight to Bola's chats.
	adaB := chatWith(t, ada, bolaID)
	send(t, ada, adaB, "Hi Bola")
	fw := ada.must(200, "POST", "/v1/me/forward", map[string]any{"group_id": gid, "message_id": reply["id"], "to_chats": []string{adaB}, "to_groups": []string{g2}})
	if len(fw["sent"].([]any)) != 2 || len(fw["failed"].([]any)) != 0 {
		t.Fatalf("forwarded: %v", fw)
	}
	if m := gmessage(t, chidi, g2, "Finally!"); m["forwarded"] != true {
		t.Fatalf("forwarded into a group: %v", m)
	}
	if m := messageOf(t, bola, chatOf(t, bola, adaID), "Finally!"); m["forwarded"] != true {
		t.Fatalf("forwarded into a chat: %v", m)
	}
	pm := send(t, ada, adaB, "From our chat")
	ada.must(200, "POST", "/v1/me/forward", map[string]any{"chat_id": adaB, "message_id": pm["id"], "to_groups": []string{gid}})
	if m := gmessage(t, chidi, gid, "From our chat"); m["forwarded"] != true {
		t.Fatalf("from a chat: %v", m)
	}
	ada.must(409, "POST", "/v1/me/forward", map[string]any{"group_id": gid, "message_id": share["id"], "to_groups": []string{g2}})
	many := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	ada.must(429, "POST", "/v1/me/forward", map[string]any{"group_id": gid, "message_id": reply["id"], "to_groups": many})
	// Per-minute limit (half in a first week: 10).
	for i := 0; ; i++ {
		code, _ := chidi.do("POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": fmt.Sprintf("spam %d", i)})
		if code == 429 {
			if i < 9 {
				t.Fatalf("limited after %d", i)
			}
			break
		}
		if i > 30 {
			t.Fatal("never limited")
		}
	}
}

// chatOf is c's people chat with the person.
func chatOf(t *testing.T, c *client, person uuid.UUID) string {
	t.Helper()
	for _, ch := range listOf(t, c, "/v1/me/people", "chats") {
		if ch["person_id"] == person.String() {
			return ch["id"].(string)
		}
	}
	for _, ch := range listOf(t, c, "/v1/me/people/requests", "requests") {
		if ch["person_id"] == person.String() {
			return ch["id"].(string)
		}
	}
	t.Fatal("no chat")
	return ""
}

// Read state (A6): "Read by" lists only members with read ticks on (theirs
// and the sender's), the sender's tick turns read when all of those have
// read it, and a member with ticks off who read shows only as having it.
func TestGroupsReadBy(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140501", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140502", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140503", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031140504", "Dayo Bello")
	for _, c := range []*client{bola, chidi, dayo} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	setPrivacy(t, chidi, map[string]any{"read_ticks": false})
	gid, _ := newGroup(t, ada, "Readers", bolaID, chidiID, dayoID)
	m := gsend(t, ada, gid, "Did you read this?")
	mid := m["id"].(string)
	if m["status"] != "sent" {
		t.Fatalf("at first: %v", m)
	}
	// Everyone has it: delivered.
	for _, c := range []*client{bola, chidi, dayo} {
		gmessages(t, c, gid)
	}
	if s := gmessage(t, ada, gid, "Did you read this?")["status"]; s != "delivered" {
		t.Fatalf("delivered: %v", s)
	}
	bola.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	chidi.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	info := ada.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+mid+"/info", nil)
	names := func(key string) []string {
		var out []string
		for _, r := range info[key].([]any) {
			out = append(out, r.(map[string]any)["name"].(string))
		}
		return out
	}
	if rb := names("read_by"); !slices.Equal(rb, []string{"Bola Ade"}) || info["read_by"].([]any)[0].(map[string]any)["at"] == nil {
		t.Fatalf("read by: %v", info)
	}
	if d := names("delivered_to"); len(d) != 2 || !slices.Contains(d, "Chidi Eze") || !slices.Contains(d, "Dayo Bello") {
		t.Fatalf("delivered to: %v", info)
	}
	// Not read yet: Dayo, who has ticks on, has not read.
	if s := gmessage(t, ada, gid, "Did you read this?")["status"]; s != "delivered" {
		t.Fatalf("before Dayo: %v", s)
	}
	dayo.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	// Read: every member with ticks on read it; Chidi (ticks off) is not
	// waited for.
	if s := gmessage(t, ada, gid, "Did you read this?")["status"]; s != "read" {
		t.Fatalf("read: %v", s)
	}
	info = ada.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+mid+"/info", nil)
	if rb := names("read_by"); len(rb) != 2 || slices.Contains(rb, "Chidi Eze") {
		t.Fatalf("read by after: %v", info)
	}
	// Only the sender sees who read it.
	bola.must(403, "GET", "/v1/me/groups/"+gid+"/messages/"+mid+"/info", nil)
	// The sender with ticks off sees no one's reads.
	setPrivacy(t, ada, map[string]any{"read_ticks": false})
	info = ada.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+mid+"/info", nil)
	if len(info["read_by"].([]any)) != 0 {
		t.Fatalf("ticks off both ways: %v", info)
	}
	if s := gmessage(t, ada, gid, "Did you read this?")["status"]; s != "delivered" {
		t.Fatalf("ticks off: %v", s)
	}
	// Unread counts follow each member's own read mark.
	gsend(t, ada, gid, "One more")
	if g := bola.must(200, "GET", "/v1/me/groups/"+gid, nil); g["unread"] != float64(1) {
		t.Fatalf("unread: %v", g)
	}
	if c := bola.must(200, "GET", "/v1/me/conversations", nil); c["unread"] != float64(1) {
		t.Fatalf("the one list's unread: %v", c)
	}
}

// Pushes (§13, A7): kind group to members not muting it, with the preview
// setting honoured; a mention pushes through a mute unless turned off for
// that group; none across a block; kind group_invite for an invite.
func TestGroupsPush(t *testing.T) {
	w := newJoinedWorld(t)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	svc := newPushService(t)
	ada, _ := w.many(t, "+2348031140601", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140602", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140603", "Chidi Eze")
	adds(t, ada, bola)
	adds(t, bola, ada)
	adds(t, ada, chidi)
	br := newPushBrowser(t)
	out := bola.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/bola", "keys": map[string]any{"p256dh": br.p256dh, "auth": br.au}})
	devID := out["devices"].([]any)[0].(map[string]any)["id"].(string)
	chidi.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/chidi", "keys": map[string]any{"p256dh": br.p256dh, "auth": br.au}})

	gid, added := newGroup(t, ada, "Lagos trip", bolaID, chidiID)
	if added[chidiID] != "invited" {
		t.Fatalf("Chidi: %v", added)
	}
	pushSettle(t, w.srv)
	if svc.count("/chidi") != 1 {
		t.Fatalf("invite pushes: %d", svc.count("/chidi"))
	}
	if p := br.decrypt(t, svc.bodies["/chidi"][0]); p.Kind != push.KindGroupInvite || p.Title != "Lagos trip" || p.Body != "Ada Obi invited you to the group" {
		t.Fatalf("invite push: %+v", p)
	}
	gsend(t, ada, gid, "Bus leaves at 6")
	pushSettle(t, w.srv)
	if svc.count("/bola") != 1 {
		t.Fatalf("pushes: %d", svc.count("/bola"))
	}
	p := br.decrypt(t, svc.bodies["/bola"][0])
	if p.Kind != push.KindGroup || p.Title != "Lagos trip" || p.Body != "Ada Obi: Bus leaves at 6" || p.Link != "/you/groups/"+gid {
		t.Fatalf("a group's push: %+v", p)
	}
	// Muted: none, but a mention comes through (A7).
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": true})
	gsend(t, ada, gid, "Muted, unmentioned")
	ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "@Bola bring water", "mentions": []uuid.UUID{bolaID}})
	pushSettle(t, w.srv)
	if svc.count("/bola") != 2 {
		t.Fatalf("muted pushes: %d", svc.count("/bola"))
	}
	if p := br.decrypt(t, svc.bodies["/bola"][1]); p.Body != "Ada Obi mentioned you: @Bola bring water" {
		t.Fatalf("a mention: %+v", p)
	}
	// Mentions in this muted group turned off: none.
	if g := bola.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"mentions": false}); g["mentions"] != false || g["muted"] != true {
		t.Fatalf("settings: %v", g)
	}
	ada.must(201, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": "@Bola again", "mentions": []uuid.UUID{bolaID}})
	pushSettle(t, w.srv)
	if svc.count("/bola") != 2 {
		t.Fatalf("mentions off: %d", svc.count("/bola"))
	}
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/me", map[string]any{"muted": false, "mentions": true})
	// The device's preview off: neither the group nor the words.
	bola.must(200, "PATCH", "/v1/me/push/"+devID, map[string]any{"preview": false})
	gsend(t, ada, gid, "Secret plans")
	pushSettle(t, w.srv)
	if p := br.decrypt(t, svc.bodies["/bola"][2]); p.Title != "Taskiem" || p.Body != "New message in a group" {
		t.Fatalf("preview off: %+v", p)
	}
	bola.must(200, "PATCH", "/v1/me/push/"+devID, map[string]any{"preview": true})
	// Groups turned off: none.
	if n := bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"groups": false}); n["groups"] != false {
		t.Fatalf("notify: %v", n)
	}
	gsend(t, ada, gid, "off")
	pushSettle(t, w.srv)
	bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"groups": true})
	// Bola blocks Ada (from the group): Ada's messages still show, no push.
	blk := bola.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+personID(t, ada).String()+"/block", nil)
	gsend(t, ada, gid, "blocked")
	pushSettle(t, w.srv)
	if svc.count("/bola") != 3 {
		t.Fatalf("pushes at the end: %d", svc.count("/bola"))
	}
	if b := gbodies(t, bola, gid); !slices.Contains(b, "blocked") {
		t.Fatalf("a blocked member's message is not seen: %v", b)
	}
	if blk["chat_id"] == nil {
		t.Fatalf("block: %v", blk)
	}
}

// Blocking someone you share a group with (A8): you still see their
// group messages; they can't message you one to one (one tick, nothing
// told), can't add you to groups (an invite you never see, answered as
// any invite), and don't see your last seen or photo; listed with your
// blocked people and unblocked there.
func TestGroupsBlocking(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140701", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140702", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031140703", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	adds(t, bola, chidi)
	adds(t, chidi, bola)
	setPrivacy(t, bola, map[string]any{"last_seen": "everyone", "photo": "everyone"})
	if st, _ := bola.putFile(t, "/v1/me/photo", "me.png", pngPhoto(t, 120)); st != 200 {
		t.Fatalf("photo: %d", st)
	}
	gid, _ := newGroup(t, ada, "Estate", bolaID, chidiID)
	// Before: Chidi sees Bola's photo in the members list.
	photoOf := func(c *client, of uuid.UUID) any {
		for _, m := range c.must(200, "GET", "/v1/me/groups/"+gid+"/members", nil)["members"].([]any) {
			mm := m.(map[string]any)
			if mm["person_id"] == of.String() {
				return mm["person"].(map[string]any)["photo"]
			}
		}
		t.Fatal("not a member")
		return nil
	}
	if photoOf(chidi, bolaID) == nil {
		t.Fatal("no photo before the block")
	}
	// Numbers stay hidden (§17 Q5) unless a member chose to show theirs.
	phoneOf := func(c *client, of uuid.UUID) any {
		for _, m := range c.must(200, "GET", "/v1/me/groups/"+gid+"/members", nil)["members"].([]any) {
			mm := m.(map[string]any)
			if mm["person_id"] == of.String() {
				return mm["phone"]
			}
		}
		return nil
	}
	if phoneOf(chidi, bolaID) != nil || phoneOf(bola, chidiID) != nil {
		t.Fatal("a number shown unasked")
	}
	setPrivacy(t, bola, map[string]any{"show_number": true})
	if phoneOf(chidi, bolaID) != "+2348031140702" {
		t.Fatalf("Bola's number: %v", phoneOf(chidi, bolaID))
	}
	blk := bola.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+chidiID.String()+"/block", nil)
	gsend(t, chidi, gid, "Chidi still speaks")
	if !slices.Contains(gbodies(t, bola, gid), "Chidi still speaks") {
		t.Fatal("Bola no longer sees Chidi's group messages")
	}
	if photoOf(chidi, bolaID) != nil || phoneOf(chidi, bolaID) != nil {
		t.Fatal("Chidi sees Bola's photo or number across the block")
	}
	// One to one: one tick, nothing told.
	cchat := chatWith(t, chidi, bolaID)
	if m := send(t, chidi, cchat, "Hi Bola"); m["status"] != "sent" {
		t.Fatalf("one to one: %v", m)
	}
	// Chidi's group: adding Bola is "invited", as any invite, and Bola
	// never sees it.
	g2, added := newGroup(t, chidi, "Chidi's group", bolaID)
	if added[bolaID] != "invited" {
		t.Fatalf("an add across a block: %v", added)
	}
	if inv := listOf(t, bola, "/v1/me/groups/invites", "invites"); len(inv) != 0 {
		t.Fatalf("Bola sees the invite: %v", inv)
	}
	bola.must(404, "GET", "/v1/me/groups/"+g2, nil)
	// Listed with Bola's blocked people; unblocked there, the add is an
	// ordinary one again (Bola added Chidi: added at once).
	bl := listOf(t, bola, "/v1/me/people/blocked", "people")
	if len(bl) != 1 || bl[0]["person_id"] != chidiID.String() || bl[0]["chat_id"] != blk["chat_id"] {
		t.Fatalf("blocked people: %v", bl)
	}
	bola.must(200, "DELETE", "/v1/me/people/"+blk["chat_id"].(string)+"/block", nil)
	if st := addedOf(t, chidi.must(200, "POST", "/v1/me/groups/"+g2+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})); st[bolaID] != "added" {
		t.Fatalf("after unblocking: %v", st)
	}
	// Bola removes Chidi from contacts: a new add is an invite Bola sees.
	g3, _ := newGroup(t, chidi, "Third")
	bola.must(204, "DELETE", "/v1/me/contacts/"+chidiID.String(), nil)
	if st := addedOf(t, chidi.must(200, "POST", "/v1/me/groups/"+g3+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})); st[bolaID] != "invited" {
		t.Fatalf("not a contact: %v", st)
	}
	if inv := listOf(t, bola, "/v1/me/groups/invites", "invites"); len(inv) != 1 || inv[0]["group_id"] != g3 {
		t.Fatalf("Bola's invites: %v", inv)
	}
	// An invite kept unseen across a block is seen once asked again after
	// the block is gone.
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/members/"+chidiID.String()+"/block", nil)
	g4, a4 := newGroup(t, chidi, "Fourth", bolaID)
	if a4[bolaID] != "invited" || len(listOf(t, bola, "/v1/me/groups/invites", "invites")) != 1 {
		t.Fatalf("across the block again: %v", a4)
	}
	bola.must(200, "DELETE", "/v1/me/people/"+blk["chat_id"].(string)+"/block", nil)
	chidi.must(200, "POST", "/v1/me/groups/"+g4+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})
	if inv := listOf(t, bola, "/v1/me/groups/invites", "invites"); len(inv) != 2 {
		t.Fatalf("the unseen invite now seen: %v", inv)
	}
}

// Deleting an account (§14, §17 Q17): the person leaves their groups, their
// messages stay shown as from "Deleted account", their marks go; the last
// member's deletion closes the group.
func TestGroupsAccountDeletion(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140801", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140802", "Bola Ade")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Club", bolaID)
	only, _ := newGroup(t, bola, "Bola alone")
	m := gsend(t, bola, gid, "Bola was here")
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string), map[string]any{"reaction": "❤️"})
	bola.must(200, "PUT", "/v1/me/groups/"+gid+"/messages/"+m["id"].(string), map[string]any{"starred": true, "reaction": "👍"})
	// Bola is the admin who stays: make Bola the only admin.
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+bolaID.String(), map[string]any{"admin": true})
	ada.must(200, "PUT", "/v1/me/groups/"+gid+"/members/"+personID(t, ada).String(), map[string]any{"admin": false})
	bola.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	bola.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348031140802")})
	got := gmessage(t, ada, gid, "Bola was here")
	if got["sender"] != "Deleted account" || len(got["reactions"].([]any)) != 1 {
		t.Fatalf("the message after: %v", got)
	}
	g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil)
	if g["members"] != float64(1) || g["role"] != "admin" {
		t.Fatalf("the group after: %v", g)
	}
	var lastEvent string
	for _, m := range gmessages(t, ada, gid) {
		if m["kind"] == "event" {
			lastEvent = m["body"].(string)
		}
	}
	if lastEvent != "Deleted account left" {
		t.Fatalf("the event: %q", lastEvent)
	}
	var rows int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM group_members WHERE person_id = $1) + (SELECT count(*) FROM group_marks WHERE person_id = $1)
		+ (SELECT count(*) FROM group_reactions WHERE person_id = $1) + (SELECT count(*) FROM people_groups WHERE id = $2)`, bolaID, only).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("Bola's rows: %d %v", rows, err)
	}
}

// Search's index covers group messages, keyed per group (A, D): a member
// finds only what they see, from joining, up to leaving; never a message
// deleted for them or for everyone; no words in the index.
func TestGroupsSearch(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031140901", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031140902", "Bola Ade")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Kitchen")
	gsend(t, ada, gid, "Jollof rice before Bola")
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{bolaID}})
	gsend(t, ada, gid, "Jollof rice after Bola joined")
	del := gsend(t, ada, gid, "Jollof deleted")
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+del["id"].(string)+"?for=everyone", nil)
	found := func(c *client, q string) []string {
		var out []string
		for _, r := range c.must(200, "GET", "/v1/me/groups/search?q="+q, nil)["results"].([]any) {
			out = append(out, r.(map[string]any)["message"].(map[string]any)["body"].(string))
		}
		return out
	}
	if f := found(ada, "jollof"); len(f) != 2 {
		t.Fatalf("Ada finds: %v", f)
	}
	if f := found(bola, "jol"); !slices.Equal(f, []string{"Jollof rice after Bola joined"}) {
		t.Fatalf("Bola finds: %v", f)
	}
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	gsend(t, ada, gid, "Jollof after Bola left")
	if f := found(bola, "jollof"); !slices.Equal(f, []string{"Jollof rice after Bola joined"}) {
		t.Fatalf("Bola after leaving: %v", f)
	}
	bola.must(400, "GET", "/v1/me/groups/search?q=a", nil)
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM group_search WHERE group_id = $1 AND position(convert_to('jol', 'UTF8') in token) > 0`, gid).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("words in the index: %d %v", plain, err)
	}
}

// The live connection carries group events to members only: a message,
// the group's change, the sender's ticks, typing; nothing to someone not
// in the group.
func TestGroupsLive(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031141001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031141002", "Bola Ade")
	outsider, _ := w.many(t, "+2348031141003", "Out Sider")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Live", bolaID)
	bEvents, _, closeB := liveStream(t, bola, "")
	defer closeB()
	aEvents, _, closeA := liveStream(t, ada, "")
	defer closeA()
	oEvents, _, closeO := liveStream(t, outsider, "")
	defer closeO()
	m := gsend(t, ada, gid, "Live words")
	got := await(bEvents, 5*time.Second,
		func(ev streamEvent) bool {
			if ev.Event != "group_message" {
				return false
			}
			mm, _ := ev.Data["message"].(map[string]any)
			return mm["body"] == "Live words" && ev.Data["group_id"] == gid
		},
		func(ev streamEvent) bool { return ev.Event == "group" && ev.Data["id"] == gid })
	if got[0] == nil || got[1] == nil {
		t.Fatalf("Bola's events: %v %v", got[0], got[1])
	}
	bola.must(204, "POST", "/v1/me/groups/"+gid+"/typing", nil)
	bola.must(204, "POST", "/v1/me/groups/"+gid+"/read", nil)
	got = await(aEvents, 6*time.Second,
		func(ev streamEvent) bool {
			if ev.Event != "group_typing" {
				return false
			}
			gs, _ := ev.Data["groups"].([]any)
			return len(gs) == 1 && gs[0].(map[string]any)["group_id"] == gid
		},
		func(ev streamEvent) bool {
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
	if got[0] == nil || got[1] == nil {
		t.Fatalf("Ada's events: typing %v, receipts %v", got[0], got[1])
	}
	for _, ev := range drain(oEvents, time.Second) {
		if strings.HasPrefix(ev.Event, "group") {
			t.Fatalf("the outsider got %v", ev)
		}
	}
}

// Many members sending at once, reading and being added: no deadlock, no
// failure, every message kept once, each sender's in their order (the
// group's row is every writer's first lock).
func TestGroupsAtOnce(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031141101", "Ada Obi")
	w.older(t, adaID)
	const n = 8
	members := make([]*client, n)
	ids := make([]uuid.UUID, n)
	for i := range members {
		members[i], ids[i] = w.many(t, fmt.Sprintf("+23480311412%02d", i), fmt.Sprintf("Member %d", i))
		w.older(t, ids[i])
		adds(t, ada, members[i])
		adds(t, members[i], ada)
	}
	gid, _ := newGroup(t, ada, "Busy", ids[:n-2]...)
	var wg sync.WaitGroup
	errs := make(chan string, 200)
	sendAll := func(c *client, who string) {
		defer wg.Done()
		for k := 0; k < 5; k++ {
			code, out, err := doWithin(c, 30*time.Second, "POST", "/v1/me/groups/"+gid+"/messages", map[string]any{"text": fmt.Sprintf("%s %d", who, k)})
			if err != nil || code != 201 {
				errs <- fmt.Sprintf("%s %d: %d %v %v", who, k, code, out, err)
			}
			if code, _, err := doWithin(c, 30*time.Second, "POST", "/v1/me/groups/"+gid+"/read", nil); err != nil || code != 204 {
				errs <- fmt.Sprintf("%s read: %d %v", who, code, err)
			}
		}
	}
	wg.Add(n - 1)
	go sendAll(ada, "ada")
	for i := 0; i < n-2; i++ {
		go sendAll(members[i], fmt.Sprintf("m%d", i))
	}
	// Meanwhile the admin adds the last two.
	wg.Add(1)
	go func() {
		defer wg.Done()
		code, out, err := doWithin(ada, 30*time.Second, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": ids[n-2:]})
		if err != nil || code != 200 {
			errs <- fmt.Sprintf("add: %d %v %v", code, out, err)
		}
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	var all []string
	for page := ""; ; {
		path := "/v1/me/groups/" + gid + "/messages"
		if page != "" {
			path += "?before=" + page
		}
		out := ada.must(200, "GET", path, nil)
		ms := out["messages"].([]any)
		var bodies []string
		for _, m := range ms {
			mm := m.(map[string]any)
			if mm["kind"] != "event" {
				bodies = append(bodies, mm["body"].(string))
			}
		}
		all = append(bodies, all...)
		if out["more"] != true {
			break
		}
		page = ms[0].(map[string]any)["id"].(string)
	}
	if len(all) != 5*(n-1) {
		t.Fatalf("kept %d: %v", len(all), all)
	}
	last := map[string]int{}
	for _, b := range all {
		var who string
		var k int
		if _, err := fmt.Sscanf(b, "%s %d", &who, &k); err != nil {
			t.Fatal(err)
		}
		if prev, ok := last[who]; ok && k != prev+1 {
			t.Fatalf("%s out of order: %v", who, all)
		}
		last[who] = k
	}
	if got := len(memberIDs(t, ada, gid)); got != n+1 {
		t.Fatalf("members: %d", got)
	}
}

// A group's size (§17 Q7): 256 at most, by add, invite and link.
func TestGroupsFull(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031141301", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031141302", "Bola Ade")
	chidi, _ := w.many(t, "+2348031141303", "Chidi Eze")
	adds(t, ada, bola)
	gid, added := newGroup(t, ada, "Big", bolaID)
	if added[bolaID] != "invited" {
		t.Fatalf("Bola: %v", added)
	}
	// Fill it to 256 in the database.
	if _, err := w.env.DB.Admin.Exec(ctx, `WITH u AS (INSERT INTO users (id, email, name) SELECT gen_random_uuid(), gen_random_uuid()::text || '@x.test', 'Filler' FROM generate_series(1, 255) RETURNING id)
		INSERT INTO group_members (group_id, person_id, how, visible) SELECT $1, id, 'added', tstzmultirange(tstzrange(now(), NULL)) FROM u`, gid); err != nil {
		t.Fatal(err)
	}
	bola.must(409, "POST", "/v1/me/groups/invites/"+gid+"/accept", nil)
	ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)
	code := ada.must(200, "GET", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
	if p := chidi.must(200, "GET", "/v1/group-links/"+code, nil); p["full"] != true || p["members"] != float64(256) {
		t.Fatalf("preview: %v", p)
	}
	chidi.must(409, "POST", "/v1/me/group-links/"+code+"/join", nil)
	_ = adaID
}

var _ = http.StatusOK

// Joins by a group's link are limited per group in an hour.
func TestGroupsLinkJoinsPerHour(t *testing.T) {
	defer api.SetGroupLinkJoinsPerHour(2)()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031141401", "Ada Obi")
	gid, _ := newGroup(t, ada, "Popular")
	code := ada.must(201, "POST", "/v1/me/groups/"+gid+"/link", nil)["code"].(string)
	for i := 0; i < 3; i++ {
		c, _ := w.many(t, fmt.Sprintf("+23480311415%02d", i), fmt.Sprintf("Joiner %d", i))
		want := 200
		if i == 2 {
			want = 429
		}
		c.must(want, "POST", "/v1/me/group-links/"+code+"/join", nil)
	}
}

// Groups in You's one Chats list: kind group, newest first with people
// chats, paged, archived apart, muted shown, unread counted.
func TestGroupsInTheOneList(t *testing.T) {
	defer api.SetPeopleListPage(2)()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031141601", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031141602", "Bola Ade")
	adds(t, ada, bola)
	adds(t, bola, ada)
	g1, _ := newGroup(t, ada, "One", bolaID)
	chat := chatWith(t, ada, bolaID)
	send(t, ada, chat, "A people chat")
	g2, _ := newGroup(t, ada, "Two", bolaID)
	g3, _ := newGroup(t, ada, "Three", bolaID)
	gsend(t, ada, g1, "One is newest")
	page := func(c *client, q string) ([]string, map[string]any) {
		out := c.must(200, "GET", "/v1/me/conversations"+q, nil)
		var ids []string
		for _, ch := range out["chats"].([]any) {
			ids = append(ids, ch.(map[string]any)["kind"].(string)+":"+ch.(map[string]any)["id"].(string))
		}
		return ids, out
	}
	ids, out := page(bola, "")
	if !slices.Equal(ids, []string{"group:" + g1, "group:" + g3}) || out["more"] != true || out["unread"] != float64(2) {
		t.Fatalf("first page: %v %v", ids, out)
	}
	ids, out = page(bola, "?before="+out["next"].(string))
	if !slices.Equal(ids, []string{"group:" + g2, "person:" + chatOf(t, bola, personID(t, ada))}) {
		t.Fatalf("second page: %v %v", ids, out)
	}
	// Archived apart; muted shown.
	bola.must(200, "PUT", "/v1/me/groups/"+g3+"/me", map[string]any{"archived": true, "muted": true})
	ids, _ = page(bola, "")
	if slices.Contains(ids, "group:"+g3) {
		t.Fatalf("an archived group in the list: %v", ids)
	}
	ids, out = page(bola, "?archived=1")
	if !slices.Equal(ids, []string{"group:" + g3}) || out["chats"].([]any)[0].(map[string]any)["muted"] != true {
		t.Fatalf("archived: %v %v", ids, out)
	}
	if l := bola.must(200, "GET", "/v1/me/groups?archived=1", nil); len(l["groups"].([]any)) != 1 {
		t.Fatalf("the groups list archived: %v", l)
	}
	// Reading clears the group's unread.
	bola.must(204, "POST", "/v1/me/groups/"+g1+"/read", nil)
	if _, out := page(bola, ""); out["unread"] != float64(1) {
		t.Fatalf("after reading: %v", out)
	}
}
