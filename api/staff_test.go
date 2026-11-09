package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Staff by phone (docs/spec/chat-five-more-flows.md, phase 3 plan, step 1):
// added from chat with a name ("add Tunde 0803… as shift manager") or from
// the web's Staff page, they join from their WhatsApp with no email; the
// business is told; an invitation is renewed rather than doubled, sent
// again not too soon, and cancelled; someone removed no longer reaches it.
func TestStaffByPhone(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ownerNumber, tunde, bisi = "+2348031239401", "+2348031239402", "+2348031239403"
	w.bind(t, owner, ownerNumber)

	// From chat, with a name and a role of two words.
	if got := w.say(t, ownerNumber, "add Tunde 0803 123 9402 as shift manager"); !strings.Contains(got, "Invite Tunde ("+tunde+") to Acme as shift manager?") {
		t.Fatalf("confirm: %q", got)
	}
	if got := w.say(t, ownerNumber, "yes"); !strings.Contains(got, "Invitation sent to Tunde ("+tunde+")") {
		t.Fatalf("sent: %q", got)
	}
	tpl := w.graph.Last(t, tunde)
	if tpl.Template != "taskiem_invite" || !strings.Contains(tpl.Text, "as shift manager") {
		t.Fatalf("the invitation: %+v", tpl)
	}
	before := len(w.graph.Sent(ownerNumber))
	terms := w.press(t, tunde, tpl.Payloads[0])
	if got := w.press(t, tunde, terms[0].Buttons[0].ID); len(got) != 1 || !strings.Contains(got[0].Text, "You are in Acme") {
		t.Fatalf("joined: %+v", got)
	}
	user, _ := w.wa.UserOf(ctx, tunde)
	if user == uuid.Nil || memberRoles(t, w.world, user, "Acme") != "shift_manager" {
		t.Fatalf("membership: %v %q", user, memberRoles(t, w.world, user, "Acme"))
	}
	var name, email string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT name, email FROM users WHERE id = $1`, user).Scan(&name, &email); err != nil || name != "Tunde" || !strings.HasSuffix(email, ".taskiem.invalid") {
		t.Fatalf("the person: %q %q %v", name, email, err)
	}
	w.srv.WaitBackground()
	if told := w.lastTo(t, ownerNumber, before); len(told) == 0 || !strings.Contains(told[len(told)-1].Text, "Tunde joined Acme") {
		t.Fatalf("the owner told: %+v", told)
	}

	// The Staff page: Tunde with his chat, no email; the owner as "you".
	list := owner.must(200, "GET", "/v1/staff", nil)
	var found map[string]any
	for _, x := range list["people"].([]any) {
		if m := x.(map[string]any); m["user_id"] == user.String() {
			found = m
		}
	}
	if found == nil || found["name"] != "Tunde" || found["email"] != nil || found["phone"] != tunde || found["chat"] != true {
		t.Fatalf("Tunde on the page: %v", list)
	}

	// From the web: Bisi, renewed not doubled, not sent again too soon,
	// then cancelled.
	owner.must(400, "POST", "/v1/staff/invites", map[string]any{"name": "Bisi", "phone": "not a number", "roles": []string{"cashier"}})
	owner.must(400, "POST", "/v1/staff/invites", map[string]any{"name": "Bisi", "phone": "08031239403", "roles": []string{}})
	owner.must(400, "POST", "/v1/staff/invites", map[string]any{"name": "Bisi", "phone": "08031239403", "roles": []string{"wizard"}})
	owner.must(400, "POST", "/v1/staff/invites", map[string]any{"name": "", "phone": "08031239403", "roles": []string{"cashier"}})
	x := owner.must(201, "POST", "/v1/staff/invites", map[string]any{"name": "Bisi", "phone": "0803 123 9403", "roles": []string{"cashier"}})
	if x["sent"] != true {
		t.Fatalf("not sent: %v", x)
	}
	if tpl := w.graph.Last(t, bisi); tpl.Template != "taskiem_invite" || !strings.Contains(tpl.Text, "as cashier") {
		t.Fatalf("Bisi's invitation: %+v", tpl)
	}
	again := owner.must(201, "POST", "/v1/staff/invites", map[string]any{"name": "Bisi Okafor", "phone": "08031239403", "roles": []string{"cashier"}})
	if again["id"] != x["id"] {
		t.Fatalf("a second invitation: %v %v", x, again)
	}
	invites := owner.must(200, "GET", "/v1/staff", nil)["invites"].([]any)
	if len(invites) != 1 || invites[0].(map[string]any)["name"] != "Bisi Okafor" {
		t.Fatalf("the invitations: %v", invites)
	}
	id := x["id"].(string)
	owner.must(409, "POST", "/v1/staff/invites/"+id+"/send", nil)
	stale := w.graph.Last(t, bisi).Payloads[0]
	owner.must(204, "DELETE", "/v1/staff/invites/"+id, nil)
	owner.must(404, "DELETE", "/v1/staff/invites/"+id, nil)
	if got := w.press(t, bisi, stale); len(got) != 1 || !strings.Contains(got[0].Text, "ended or was answered") {
		t.Fatalf("Join after cancelling: %+v", got)
	}

	// Removed: no role left, the business out of reach; not oneself.
	ownerID := owner.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	owner.must(409, "POST", "/v1/staff/"+ownerID+"/remove", nil)
	owner.must(204, "POST", "/v1/staff/"+user.String()+"/remove", nil)
	owner.must(404, "POST", "/v1/staff/"+user.String()+"/remove", nil)
	if roles := memberRoles(t, w.world, user, "Acme"); roles != "" {
		t.Fatalf("still in Acme: %q", roles)
	}
	// Only those who manage people.
	viewer := addMember(t, w.world, owner, "view", "viewer")
	viewer.must(403, "GET", "/v1/staff", nil)
	viewer.must(403, "POST", "/v1/staff/invites", map[string]any{"name": "X", "phone": "08031239404", "roles": []string{"viewer"}})
}
