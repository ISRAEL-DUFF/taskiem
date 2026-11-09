package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

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

// A person's profile (phase 3 plan, step 2): job title, start day, note,
// pay for the record and usual hours; checked, kept per business, and read
// with the person.
func TestStaffProfile(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	cashier := addMember(t, w.world, owner, "cash", "cashier")
	id := cashier.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)

	empty := owner.must(200, "GET", "/v1/staff/"+id, nil)
	if empty["roles"].([]any)[0] != "cashier" || empty["profile"].(map[string]any)["title"] != "" || len(empty["profile"].(map[string]any)["rota"].([]any)) != 0 {
		t.Fatalf("before: %v", empty)
	}
	good := map[string]any{"title": "Head cashier", "started_on": "2026-03-01", "note": "Keys to the till", "pay_kobo": 8000000, "pay_per": "month",
		"rota": []any{
			map[string]any{"day": "sat", "start": "10:00", "end": "18:00"},
			map[string]any{"day": "mon", "start": "08:00", "end": "16:00"},
			map[string]any{"day": "fri", "start": "20:00", "end": "04:00"},
		}}
	saved := owner.must(200, "PUT", "/v1/staff/"+id+"/profile", good)["profile"].(map[string]any)
	if days := saved["rota"].([]any); days[0].(map[string]any)["day"] != "mon" || days[2].(map[string]any)["day"] != "sat" {
		t.Fatalf("the rota in order: %v", saved)
	}
	got := owner.must(200, "GET", "/v1/staff/"+id, nil)["profile"].(map[string]any)
	if got["title"] != "Head cashier" || got["started_on"] != "2026-03-01" || got["pay_kobo"] != float64(8000000) || got["pay_per"] != "month" || len(got["rota"].([]any)) != 3 {
		t.Fatalf("after: %v", got)
	}
	for _, bad := range []map[string]any{
		{"title": strings.Repeat("x", 61), "rota": []any{}},
		{"started_on": "March", "rota": []any{}},
		{"pay_kobo": 100, "rota": []any{}},
		{"pay_kobo": 100, "pay_per": "year", "rota": []any{}},
		{"rota": []any{map[string]any{"day": "mon", "start": "8am", "end": "16:00"}}},
		{"rota": []any{map[string]any{"day": "mon", "start": "08:00", "end": "08:00"}}},
		{"rota": []any{map[string]any{"day": "mon", "start": "08:00", "end": "16:00"}, map[string]any{"day": "mon", "start": "17:00", "end": "18:00"}}},
		{"rota": []any{map[string]any{"day": "someday", "start": "08:00", "end": "16:00"}}},
	} {
		owner.must(400, "PUT", "/v1/staff/"+id+"/profile", bad)
	}
	// Pay cleared: neither amount nor what it is per.
	cleared := owner.must(200, "PUT", "/v1/staff/"+id+"/profile", map[string]any{"title": "Head cashier", "rota": []any{}})["profile"].(map[string]any)
	if cleared["pay_kobo"] != nil {
		t.Fatalf("pay kept: %v", cleared)
	}
	owner.must(404, "GET", "/v1/staff/"+uuid.NewString(), nil)
	owner.must(404, "PUT", "/v1/staff/"+uuid.NewString()+"/profile", good)
	cashier.must(403, "GET", "/v1/staff/"+id, nil)
	cashier.must(403, "PUT", "/v1/staff/"+id+"/profile", good)
}

// Clocking in and out (phase 3 plan, step 3): a cashier with usual hours
// who has not clocked in 15 minutes after the start is told to the owner
// once; "in", a shared location, "who is in", "out" with the hours; the
// briefing says who is in; a shift left open is closed by Taskiem.
func TestStaffClock(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const ownerNumber, cashNumber = "+2348031239501", "+2348031239502"
	w.bind(t, owner, ownerNumber)
	cashier := w.member(t, owner, "cash@acme.test", "cashier")
	w.bind(t, cashier, cashNumber)
	cashID := cashier.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = 'Bisi' WHERE id = $1`, cashID); err != nil {
		t.Fatal(err)
	}

	lagos, _ := time.LoadLocation("Africa/Lagos")
	base := time.Now().In(lagos)
	at := func(day, hour, minute int) time.Time {
		return time.Date(base.Year(), base.Month(), base.Day()+day, hour, minute, 0, 0, lagos)
	}
	today := rotaDayName(at(0, 12, 0))
	owner.must(200, "PUT", "/v1/staff/"+cashID+"/profile", map[string]any{"rota": []any{map[string]any{"day": today, "start": "08:00", "end": "16:00"}}})
	clock := func(day, h, m int) { w.srv.BriefingClock = func() time.Time { return at(day, h, m) } }

	// 08:10: not late yet; 08:20: late, told once.
	clock(0, 8, 10)
	before := len(w.graph.Sent(ownerNumber))
	if err := w.srv.StaffTick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.graph.Sent(ownerNumber)[before:]; len(got) != 0 {
		t.Fatalf("told too soon: %+v", got)
	}
	clock(0, 8, 20)
	for range 2 {
		if err := w.srv.StaffTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.graph.Sent(ownerNumber)[before:]; len(got) != 1 || !strings.Contains(got[0].Text, "Bisi has not clocked in") || !strings.Contains(got[0].Text, "Due at 08:00") {
		t.Fatalf("late: %+v", got)
	}
	b := owner.must(200, "GET", "/v1/chat-briefing", nil)
	if late := b["staff"].(map[string]any)["late"].([]any); len(late) != 1 || late[0].(map[string]any)["due"] != "08:00" {
		t.Fatalf("the briefing's late: %v", b["staff"])
	}

	// In, again, a location, who is in.
	clock(0, 8, 25)
	if got := w.say(t, cashNumber, "in"); !strings.Contains(got, "Clocked in at 08:25") {
		t.Fatalf("in: %q", got)
	}
	if got := w.say(t, cashNumber, "clock in"); !strings.Contains(got, "You clocked in at 08:25") {
		t.Fatalf("in again: %q", got)
	}
	got := w.send(t, cashNumber, map[string]any{"type": "location", "location": map[string]any{"latitude": 6.4541, "longitude": 3.3947}})
	if len(got) != 1 || !strings.Contains(got[0].Text, "Your location is with today's clock-in") {
		t.Fatalf("the location: %+v", got)
	}
	if got := w.say(t, ownerNumber, "who is in"); !strings.Contains(got, "In now: Bisi (since 08:25)") {
		t.Fatalf("who is in: %q", got)
	}
	if got := w.say(t, cashNumber, "who is in"); !strings.Contains(got, "Only those who run the business") {
		t.Fatalf("a cashier asks: %q", got)
	}
	day := owner.must(200, "GET", "/v1/staff/clock", nil)
	if shifts := day["shifts"].([]any); len(shifts) != 1 || shifts[0].(map[string]any)["in_where"] == nil || len(day["in"].([]any)) != 1 || len(day["late"].([]any)) != 0 {
		t.Fatalf("the day: %v", day)
	}
	if b := owner.must(200, "GET", "/v1/chat-briefing", nil); len(b["staff"].(map[string]any)["in"].([]any)) != 1 {
		t.Fatalf("the briefing's in: %v", b["staff"])
	}

	// Out, with the hours; out again says so.
	clock(0, 16, 30)
	if got := w.say(t, cashNumber, "out"); !strings.Contains(got, "Clocked out at 16:30: 8 hours 5 minutes today") {
		t.Fatalf("out: %q", got)
	}
	if got := w.say(t, cashNumber, "out"); !strings.Contains(got, "You are not clocked in") {
		t.Fatalf("out again: %q", got)
	}

	// Left open: in after the shift, closed by Taskiem after 16 hours.
	clock(0, 17, 0)
	w.say(t, cashNumber, "in")
	clock(1, 8, 59)
	if err := w.srv.StaffTick(ctx); err != nil {
		t.Fatal(err)
	}
	var how string
	var out time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(out_how, ''), COALESCE(out_at, now()) FROM staff_clock WHERE in_at = $1`, at(0, 17, 0)).Scan(&how, &out); err != nil || how != "" {
		t.Fatalf("closed too soon: %q %v", how, err)
	}
	clock(1, 9, 1)
	if err := w.srv.StaffTick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT out_how, out_at FROM staff_clock WHERE in_at = $1`, at(0, 17, 0)).Scan(&how, &out); err != nil || how != "auto" || !out.Equal(at(1, 9, 0)) {
		t.Fatalf("closed by Taskiem: %q %v %v", how, out, err)
	}
	owner.must(400, "GET", "/v1/staff/clock?day=today", nil)
	cashier.must(403, "GET", "/v1/staff/clock", nil)
}

// rotaDayName is a date's day as a rota names it.
func rotaDayName(t time.Time) string {
	return []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[t.Weekday()]
}
