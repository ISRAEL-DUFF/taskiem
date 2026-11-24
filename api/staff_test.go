package api_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
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

	// From the Taskiem chat on the web: the number is invited on WhatsApp,
	// as from the Staff page (it was refused, the web being no channel an
	// invitation goes by).
	const kemi = "+2348031239405"
	said := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "add Kemi 0803 123 9405 as cashier"})["messages"].([]any)
	if last := said[len(said)-1].(map[string]any)["text"]; !strings.Contains(fmt.Sprint(last), "Invite Kemi ("+kemi+") to Acme as cashier?") {
		t.Fatalf("web confirm: %v", said)
	}
	said = owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "yes"})["messages"].([]any)
	if last := said[len(said)-1].(map[string]any)["text"]; !strings.Contains(fmt.Sprint(last), "Invitation sent to Kemi ("+kemi+")") {
		t.Fatalf("web sent: %v", said)
	}
	if tpl := w.graph.Last(t, kemi); tpl.Template != "taskiem_invite" || !strings.Contains(tpl.Text, "as cashier") {
		t.Fatalf("Kemi's invitation: %+v", tpl)
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

// TestStaffTasks: a task given from the web, told on WhatsApp, nudged,
// late to the giver, DONE; one for any cashier, taken, needing a photo;
// CAN'T; one said in chat and confirmed; a repeat making its task each
// day; the briefing's count; and removal giving tasks back.
func TestStaffTasks(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const ownerNumber, cashNumber = "+2348031239511", "+2348031239512"
	w.bind(t, owner, ownerNumber)
	cashier := w.member(t, owner, "cash@acme.test", "cashier")
	w.bind(t, cashier, cashNumber)
	id := func(c *client) string {
		return c.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	}
	ownerID, cashID := id(owner), id(cashier)
	for u, name := range map[string]string{ownerID: "Ada", cashID: "Bisi"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, u, name); err != nil {
			t.Fatal(err)
		}
	}
	var proofs [][]byte
	w.srv.ProofStore = func(_ context.Context, _ uuid.UUID, data []byte) (string, error) {
		proofs = append(proofs, data)
		return fmt.Sprintf("files/task-%d.jpg", len(proofs)), nil
	}
	lagos, _ := time.LoadLocation("Africa/Lagos")
	base := time.Now().In(lagos)
	at := func(day, hour, minute int) time.Time {
		return time.Date(base.Year(), base.Month(), base.Day()+day, hour, minute, 0, 0, lagos)
	}
	clock := func(day, h, m int) { w.srv.BriefingClock = func() time.Time { return at(day, h, m) } }
	tick := func() {
		t.Helper()
		if err := w.srv.StaffTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	since := func(number string) func() []string {
		n := len(w.graph.Sent(number))
		return func() []string {
			w.srv.WaitBackground()
			var out []string
			for _, s := range w.graph.Sent(number)[n:] {
				out = append(out, s.Text)
			}
			return out
		}
	}
	has := func(got []string, want string) bool {
		return slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) })
	}

	// Given on the web at 08:00, due 09:00: Bisi is told, with Done and Can't.
	clock(0, 8, 0)
	toBisi := since(cashNumber)
	task := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "count the cash float", "to_user": cashID, "due_at": at(0, 9, 0)})["task"].(map[string]any)
	if got := toBisi(); len(got) != 1 || !strings.Contains(got[0], "Ada asked you: count the cash float, by 09:00") {
		t.Fatalf("told: %q", got)
	}
	if got := w.say(t, cashNumber, "my tasks"); !strings.Contains(got, "1. count the cash float, by 09:00") {
		t.Fatalf("my tasks: %q", got)
	}
	// 08:35: a nudge, once. 09:05: Ada hears it is late, once.
	clock(0, 8, 35)
	toBisi = since(cashNumber)
	tick()
	tick()
	if got := toBisi(); len(got) != 1 || !strings.Contains(got[0], "Soon: count the cash float") {
		t.Fatalf("nudge: %q", got)
	}
	clock(0, 9, 5)
	toAda := since(ownerNumber)
	tick()
	tick()
	if got := toAda(); len(got) != 1 || !strings.Contains(got[0], "Count the cash float is 5 minutes late (Bisi)") {
		t.Fatalf("late: %q", got)
	}
	if b := owner.must(200, "GET", "/v1/chat-briefing", nil); b["tasks"].(map[string]any)["late"] != float64(1) {
		t.Fatalf("the briefing: %v", b["tasks"])
	}
	// Remind, from the button.
	toBisi = since(cashNumber)
	w.send(t, ownerNumber, whatsapptest.ButtonReply("tsk:remind:"+task["id"].(string), "Remind"))
	if got := toBisi(); len(got) != 1 || !strings.Contains(got[0], "Ada reminds you") {
		t.Fatalf("remind: %q", got)
	}
	// DONE: Ada hears.
	toAda = since(ownerNumber)
	if got := w.say(t, cashNumber, "done"); !strings.Contains(got, "Done: count the cash float") {
		t.Fatalf("done: %q", got)
	}
	if got := toAda(); !has(got, "Bisi did: count the cash float") || !has(got, "(5 minutes late)") {
		t.Fatalf("Ada told: %q", got)
	}
	if got := w.say(t, cashNumber, "done"); !strings.Contains(got, "no tasks open") {
		t.Fatalf("done again: %q", got)
	}

	// For any cashier, with a photo: taken, DONE asks for the photo, the photo closes it.
	toBisi = since(cashNumber)
	role := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "clean the counter", "to_role": "cashier", "photo": true})["task"].(map[string]any)
	if got := toBisi(); len(got) != 1 || !strings.Contains(got[0], "Ada asked any cashier: clean the counter") {
		t.Fatalf("role told: %q", got)
	}
	got := w.send(t, cashNumber, whatsapptest.ButtonReply("tsk:take:"+role["id"].(string), "I'll do it"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "It is yours: clean the counter") {
		t.Fatalf("take: %+v", got)
	}
	if got := w.say(t, cashNumber, "done"); !strings.Contains(got, "Send a photo of it to finish") {
		t.Fatalf("done without a photo: %q", got)
	}
	w.graph.AddMedia("media-counter", "image/jpeg", []byte("\xff\xd8\xff counter"), 0)
	got = w.send(t, cashNumber, whatsapptest.Image("media-counter", "image/jpeg", ""))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Done, with the photo: clean the counter") || len(proofs) != 1 {
		t.Fatalf("the photo: %+v", got)
	}
	cashier.must(409, "POST", "/v1/staff/tasks/"+task["id"].(string)+"/done", map[string]any{})
	cashier.must(409, "POST", "/v1/staff/tasks/"+role["id"].(string)+"/done", map[string]any{})

	// CAN'T, with why; Ada gives it to herself again.
	toAda = since(ownerNumber)
	fuel := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "buy diesel", "to_user": cashID})["task"].(map[string]any)
	w.srv.WaitBackground()
	if got := w.say(t, cashNumber, "can't no fuel station open"); !strings.Contains(got, "Sent back to Ada: buy diesel") {
		t.Fatalf("can't: %q", got)
	}
	if got := toAda(); !has(got, "Bisi can't: buy diesel") || !has(got, `"no fuel station open"`) {
		t.Fatalf("Ada told of can't: %q", got)
	}
	back := owner.must(200, "POST", "/v1/staff/tasks/"+fuel["id"].(string)+"/give", map[string]any{"to_user": ownerID})
	w.srv.WaitBackground()
	if back["state"] != "open" || back["to_user"] != ownerID {
		t.Fatalf("given again: %v", back)
	}

	// Said in chat, confirmed, told.
	if got := w.say(t, ownerNumber, "@Bisi clean the freezer by 5pm, with a photo"); !strings.Contains(got, "Make it a task for Bisi: clean the freezer, by 17:00, with a photo?") {
		t.Fatalf("said: %q", got)
	}
	toBisi = since(cashNumber)
	if got := w.say(t, ownerNumber, "yes"); !strings.Contains(got, "Task given to Bisi") {
		t.Fatalf("yes: %q", got)
	}
	if got := toBisi(); len(got) != 1 || !strings.Contains(got[0], "clean the freezer, by 17:00") || !strings.Contains(got[0], "send a photo") {
		t.Fatalf("Bisi told: %q", got)
	}
	// On the web, a task that needs a photo is not done without one.
	for _, x := range cashier.must(200, "GET", "/v1/staff/tasks", nil)["tasks"].([]any) {
		if x := x.(map[string]any); x["what"] == "clean the freezer" {
			cashier.must(400, "POST", "/v1/staff/tasks/"+x["id"].(string)+"/done", map[string]any{})
		}
	}
	if got := w.say(t, cashNumber, "@Ada do this by 3pm"); strings.Contains(got, "Make it a task") {
		t.Fatalf("a cashier gives a task: %q", got)
	}

	// Every day at 07:00, made at 09:05: none today; tomorrow's is made at midnight.
	owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "open the shop", "to_user": cashID, "repeat": map[string]any{"every": "day", "at": "07:00"}})
	all := owner.must(200, "GET", "/v1/staff/tasks?all=true", nil)
	if len(all["repeats"].([]any)) != 1 || slices.ContainsFunc(all["tasks"].([]any), func(x any) bool { return x.(map[string]any)["what"] == "open the shop" }) {
		t.Fatalf("the repeat: %v", all)
	}
	clock(1, 0, 5)
	toBisi = since(cashNumber)
	tick()
	tick()
	if got := toBisi(); !has(got, "open the shop, by 07:00") || len(got) != 1 {
		t.Fatalf("tomorrow's: %q", got)
	}

	// Bisi sees only her own; a cashier gives none and sees not everyone's.
	mine := cashier.must(200, "GET", "/v1/staff/tasks", nil)
	if mine["give"] != false || len(mine["tasks"].([]any)) == 0 {
		t.Fatalf("mine: %v", mine)
	}
	cashier.must(403, "GET", "/v1/staff/tasks?all=true", nil)
	cashier.must(403, "POST", "/v1/staff/tasks", map[string]any{"what": "x", "to_user": ownerID})
	owner.must(400, "POST", "/v1/staff/tasks", map[string]any{"what": "x", "to_role": "rider"})

	// Removed: Bisi's open tasks are Ada's again; the repeat stops.
	owner.must(204, "POST", "/v1/staff/"+cashID+"/remove", nil)
	var open, stopped int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM staff_tasks WHERE COALESCE(taken_by, to_user) = $1 AND state = 'open'),
		(SELECT count(*) FROM staff_task_repeats WHERE stopped_at IS NOT NULL)`, cashID).Scan(&open, &stopped); err != nil || open != 0 || stopped != 1 {
		t.Fatalf("after removal: %d %d %v", open, stopped, err)
	}
	var back2 int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM staff_tasks WHERE to_user = $1 AND state = 'open' AND what IN ('clean the freezer', 'open the shop')`, ownerID).Scan(&back2); err != nil || back2 != 2 {
		t.Fatalf("given back: %d %v", back2, err)
	}
}

// TestStaffTasksFixes: the review's fixes. A repeat said at the end of
// the words; one set after today's time starts tomorrow; "by 7am" said at
// 9am is tomorrow's; two people with one first name are asked about; a
// weekly repeat for a role, nudged to everyone in it; giving to someone
// else and calling off tell the one who had it; a task about an invoice
// closes with it; tasks given by someone removed are their remover's; a
// location shared without clocking in clocks no one in.
func TestStaffTasksFixes(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	const ownerOn, adeOn, okonOn, samOn = "+2348031239521", "+2348031239522", "+2348031239523", "+2348031239524"
	w.bind(t, owner, ownerOn)
	ade := w.member(t, owner, "ade@acme.test", "cashier")
	okon := w.member(t, owner, "okon@acme.test", "cashier")
	sam := w.member(t, owner, "sam@acme.test", "shift_manager")
	w.bind(t, ade, adeOn)
	w.bind(t, okon, okonOn)
	w.bind(t, sam, samOn)
	id := func(c *client) string {
		return c.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	}
	ownerID, adeID, okonID, samID := id(owner), id(ade), id(okon), id(sam)
	for u, name := range map[string]string{ownerID: "Ada", adeID: "Bisi Ade", okonID: "Bisi Okon", samID: "Sam"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, u, name); err != nil {
			t.Fatal(err)
		}
	}
	lagos, _ := time.LoadLocation("Africa/Lagos")
	base := time.Now().In(lagos)
	at := func(day, hour, minute int) time.Time {
		return time.Date(base.Year(), base.Month(), base.Day()+day, hour, minute, 0, 0, lagos)
	}
	clock := func(day, h, m int) { w.srv.BriefingClock = func() time.Time { return at(day, h, m) } }
	tick := func() {
		t.Helper()
		if err := w.srv.StaffTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	since := func(number string) func() []string {
		n := len(w.graph.Sent(number))
		return func() []string {
			w.srv.WaitBackground()
			var out []string
			for _, x := range w.graph.Sent(number)[n:] {
				out = append(out, x.Text)
			}
			return out
		}
	}
	has := func(got []string, want string) bool {
		return slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) })
	}
	tasksNamed := func(what string) int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM staff_tasks WHERE what = $1`, what).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// "every day at 7am" at the end, said at 9am: a repeat from tomorrow.
	clock(0, 9, 0)
	if got := w.say(t, ownerOn, "@Bisi Ade count the float every day at 7am"); !strings.Contains(got, "Make it a task for Bisi Ade: count the float, every day at 07:00?") {
		t.Fatalf("said: %q", got)
	}
	if got := w.say(t, ownerOn, "yes"); !strings.Contains(got, "Each time it falls due") {
		t.Fatalf("yes: %q", got)
	}
	clock(0, 9, 1)
	tick()
	if n := tasksNamed("count the float"); n != 0 {
		t.Fatalf("today's made late: %d", n)
	}
	clock(1, 0, 5)
	tick()
	if n := tasksNamed("count the float"); n != 1 {
		t.Fatalf("tomorrow's: %d", n)
	}

	// Two people called Bisi: which one. "by 7am" said at 9am: tomorrow's.
	clock(0, 9, 2)
	if got := w.say(t, ownerOn, "@Bisi clean up"); !strings.Contains(got, "Which one:") || !strings.Contains(got, "Bisi Ade") || !strings.Contains(got, "Bisi Okon") {
		t.Fatalf("which: %q", got)
	}
	if got := w.say(t, ownerOn, "@Bisi Okon open the gate by 7am"); !strings.Contains(got, "open the gate, by tomorrow 07:00") {
		t.Fatalf("by 7am: %q", got)
	}
	w.say(t, ownerOn, "no")

	// Every week on today's day at 10:00, for any cashier: today's made; at
	// 09:35 both cashiers are nudged.
	rep := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "clean the freezer", "to_role": "cashier",
		"repeat": map[string]any{"every": "week", "day": rotaDayName(at(0, 12, 0)), "at": "10:00"}})
	if rep["task"] == nil {
		t.Fatalf("this week's: %v", rep)
	}
	toAde, toOkon := since(adeOn), since(okonOn)
	clock(0, 9, 35)
	tick()
	if !has(toAde(), "Soon, for any cashier: clean the freezer") || !has(toOkon(), "Soon, for any cashier: clean the freezer") {
		t.Fatalf("the role nudged: %q %q", toAde(), toOkon())
	}

	// Given to someone else, then called off: the one who had it is told.
	diesel := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "buy diesel", "to_user": adeID})["task"].(map[string]any)
	toAde, toOkon = since(adeOn), since(okonOn)
	owner.must(200, "POST", "/v1/staff/tasks/"+diesel["id"].(string)+"/give", map[string]any{"to_user": okonID})
	if !has(toAde(), "Given to someone else: buy diesel") || !has(toOkon(), "asked you: buy diesel") {
		t.Fatalf("given: %q %q", toAde(), toOkon())
	}
	toOkon = since(okonOn)
	owner.must(200, "DELETE", "/v1/staff/tasks/"+diesel["id"].(string), nil)
	if !has(toOkon(), "Called off: buy diesel") {
		t.Fatalf("called off: %q", toOkon())
	}

	// About an invoice: done when it is paid, called off when it is void.
	inv := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola", "email": "bola@example.com"},
		"lines": []map[string]any{{"description": "Rice", "quantity": 1, "unit_kobo": 100000}}}, "Idempotency-Key", "inv-t")
	owner.must(200, "POST", "/v1/invoices/"+inv["id"].(string)+"/send", nil)
	chase := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "follow up the invoice", "to_user": adeID,
		"link": map[string]any{"kind": "invoice", "id": inv["id"], "name": inv["number"]}})["task"].(map[string]any)
	owner.must(200, "POST", "/v1/invoices/"+inv["id"].(string)+"/void", map[string]any{"reason": "Wrong price"})
	tick()
	var state string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT state FROM staff_tasks WHERE id = $1`, chase["id"]).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("closed with the invoice: %q %v", state, err)
	}

	// Sam gives a task and is removed: it is Ada's now, and only Ada hears it is late.
	clock(0, 9, 40)
	sam.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "sweep the yard", "to_user": adeID, "due_at": at(0, 9, 50)})
	owner.must(204, "POST", "/v1/staff/"+samID+"/remove", nil)
	toAda, toSam := since(ownerOn), since(samOn)
	clock(0, 9, 55)
	tick()
	if !has(toAda(), "Sweep the yard is 5 minutes late (Bisi Ade)") || len(toSam()) != 0 {
		t.Fatalf("late, after removal: Ada %q, Sam %q", toAda(), toSam())
	}

	// A location shared with no clock-in clocks no one in.
	w.send(t, ownerOn, map[string]any{"type": "location", "location": map[string]any{"latitude": 6.45, "longitude": 3.39}})
	var shifts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM staff_clock WHERE user_id = $1`, ownerID).Scan(&shifts); err != nil || shifts != 0 {
		t.Fatalf("clocked in by a location: %d %v", shifts, err)
	}
}

// A task for someone on no chat app reaches their Taskiem chat on the web,
// with Done and Can't that work there (it reached only the bell before).
func TestStaffTaskOnTheWeb(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	cashier := w.member(t, owner, "web@acme.test", "cashier")
	cashID := cashier.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	task := owner.must(201, "POST", "/v1/staff/tasks", map[string]any{"what": "restock the drinks fridge", "to_user": cashID})["task"].(map[string]any)
	w.srv.WaitBackground()

	msgs := cashier.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("nothing in the cashier's Taskiem chat")
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if !strings.Contains(fmt.Sprint(last["text"]), "asked you: restock the drinks fridge") {
		t.Fatalf("the task in the chat: %v", last)
	}
	var done string
	for _, b := range last["buttons"].([]any) {
		if m := b.(map[string]any); m["title"] == "Done" {
			done, _ = m["id"].(string)
		}
	}
	if done == "" {
		t.Fatalf("no Done to press: %v", last)
	}
	cashier.must(200, "POST", "/v1/taskiem-chat", map[string]any{"reply": done, "text": "Done"})
	state := ""
	for _, x := range cashier.must(200, "GET", "/v1/staff/tasks", nil)["tasks"].([]any) {
		if m := x.(map[string]any); m["id"] == task["id"] {
			state, _ = m["state"].(string)
		}
	}
	if state != "done" {
		t.Fatalf("the task after Done in the chat: %q", state)
	}
}
