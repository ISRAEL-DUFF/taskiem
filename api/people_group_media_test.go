package api_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// A group's media and files (5b A, the group info's "Media and files";
// "As built: stage 5b leftovers"): the photos, voice notes and files,
// newest first, paged, only within the caller's own time(s) in the group,
// never one deleted for them or for everyone; no texts or cards.
func TestGroupsMedia(t *testing.T) {
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031141501", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031141502", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031141503", "Chidi Eze")
	outsider, _ := w.many(t, "+2348031141504", "Out Sider")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Street", bolaID)
	photo := func(c *client, text string) string {
		t.Helper()
		code, f := c.postFile(t, "/v1/me/groups/"+gid+"/files", "p.png", pngPhoto(t, 40), map[string]string{"text": text})
		if code != 201 {
			t.Fatalf("photo %q: %d %v", text, code, f)
		}
		return f["id"].(string)
	}
	media := func(c *client, before string) ([]string, bool) {
		t.Helper()
		path := "/v1/me/groups/" + gid + "/media"
		if before != "" {
			path += "?before=" + before
		}
		out := c.must(200, "GET", path, nil)
		var got []string
		for _, m := range out["messages"].([]any) {
			mm := m.(map[string]any)
			if mm["attachment"] == nil {
				t.Fatalf("not media: %v", mm)
			}
			got = append(got, mm["body"].(string))
		}
		return got, out["more"].(bool)
	}
	photo(ada, "one")
	gsend(t, ada, gid, "words only")
	gone := photo(bola, "two")
	hidden := photo(ada, "three")
	if got, more := media(bola, ""); !slices.Equal(got, []string{"three", "two", "one"}) || more {
		t.Fatalf("Bola's media: %v %v", got, more)
	}
	// Deleted for everyone, and for me alone.
	bola.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+gone+"?for=everyone", nil)
	bola.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+hidden+"?for=me", nil)
	if got, _ := media(bola, ""); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("after deleting: %v", got)
	}
	if got, _ := media(ada, ""); !slices.Equal(got, []string{"three", "one"}) {
		t.Fatalf("Ada's after Bola's deletes: %v", got)
	}
	// Chidi sees only from when he joined; Bola, once he left, up to then.
	ada.must(200, "POST", "/v1/me/groups/"+gid+"/members", map[string]any{"person_ids": []uuid.UUID{chidiID}})
	photo(ada, "four")
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	photo(ada, "five")
	if got, _ := media(chidi, ""); !slices.Equal(got, []string{"five", "four"}) {
		t.Fatalf("Chidi's: %v", got)
	}
	if got, _ := media(bola, ""); !slices.Equal(got, []string{"four", "one"}) {
		t.Fatalf("Bola's after leaving: %v", got)
	}
	outsider.must(404, "GET", "/v1/me/groups/"+gid+"/media", nil)
	// Paged, newest first: 50, then the rest before the last. (The rows
	// are copies made in the database: sending 50 would meet the limit a
	// minute; a copy's sealed words don't open under its new id.)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO group_messages (group_id, id, sender_id, kind, sealed, attachment)
		SELECT group_id, gen_random_uuid(), sender_id, kind, sealed, attachment FROM group_messages, generate_series(1, 50)
		WHERE group_id = $1 AND kind = 'media' ORDER BY created_at DESC LIMIT 50`, gid); err != nil {
		t.Fatal(err)
	}
	first, more := media(ada, "")
	if len(first) != 50 || !more {
		t.Fatalf("first page: %d %v", len(first), more)
	}
	out := ada.must(200, "GET", "/v1/me/groups/"+gid+"/media", nil)["messages"].([]any)
	last := out[49].(map[string]any)["id"].(string)
	if got, more := media(ada, last); !slices.Equal(got, []string{"five", "four", "three", "one"}) || more {
		t.Fatalf("second page: %v %v", got, more)
	}
}
