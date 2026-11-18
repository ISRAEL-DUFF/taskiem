package api_test

import (
	"testing"
	"time"
)

// An invite that goes is told on the invited person's live connections
// ("As built: stage 5b, groups (web)" and "(app)", "Review fixes"):
// taken back by an admin, declined on another device, accepted on another
// device; each a group_invite event with invite null, so it leaves their
// Requests at once.
func TestGroupInviteGoneLive(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031149001", "Ada Obi")
	w.older(t, adaID)
	chidi, chidiID := w.many(t, "+2348031149002", "Chidi Eze")
	adds(t, ada, chidi) // Chidi has not added Ada: an invite.
	g1, st1 := newGroup(t, ada, "Taken back", chidiID)
	g2, _ := newGroup(t, ada, "Declined", chidiID)
	g3, _ := newGroup(t, ada, "Accepted", chidiID)
	if st1[chidiID] != "invited" {
		t.Fatalf("Chidi: %v", st1)
	}
	events, _, done := liveStream(t, chidi, "")
	defer done()
	gone := func(gid string) func(streamEvent) bool {
		return func(ev streamEvent) bool {
			v, has := ev.Data["invite"]
			return ev.Event == "group_invite" && ev.Data["group_id"] == gid && has && v == nil
		}
	}
	// Each invite waiting is told first.
	waiting := func(gid string) func(streamEvent) bool {
		return func(ev streamEvent) bool {
			return ev.Event == "group_invite" && ev.Data["group_id"] == gid && ev.Data["invite"] != nil
		}
	}
	if got := await(events, 5*time.Second, waiting(g1), waiting(g2), waiting(g3)); got[0] == nil || got[1] == nil || got[2] == nil {
		t.Fatalf("the invites: %v", got)
	}
	ada.must(204, "DELETE", "/v1/me/groups/"+g1+"/invites/"+chidiID.String(), nil)
	chidi.must(204, "POST", "/v1/me/groups/invites/"+g2+"/decline", nil)
	chidi.must(200, "POST", "/v1/me/groups/invites/"+g3+"/accept", nil)
	if got := await(events, 6*time.Second, gone(g1), gone(g2), gone(g3)); got[0] == nil || got[1] == nil || got[2] == nil {
		t.Fatalf("the invites gone: taken back %v, declined %v, accepted %v", got[0], got[1], got[2])
	}
	if inv := listOf(t, chidi, "/v1/me/groups/invites", "invites"); len(inv) != 0 {
		t.Fatalf("invites left: %v", inv)
	}
}
