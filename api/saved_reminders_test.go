package api_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Reminders on saved things' dates (saved objects S2): set by the words,
// the date named by its label; told on its day from the morning, once,
// then moved to the next year; a date changed to a later one waits for
// it; a person's own birthday; listed and stopped.
func TestSavedReminders(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@reminders.test")
	const on = "+2348010000519"
	w.bind(t, owner, on)
	lagos, _ := time.LoadLocation("Africa/Lagos")
	clock := time.Date(2027, 3, 1, 10, 0, 0, 0, lagos)
	w.srv.BriefingClock = func() time.Time { return clock }

	car := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Car", "fields": []map[string]any{
		{"label": "Plate", "type": "text"}, {"label": "Insurance due", "type": "date"}, {"label": "Service due", "type": "date"},
	}})["kind"].(map[string]any)
	van := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "car", "title": "Delivery van", "kind": "custom", "kind_id": car["id"],
		"values": map[string]any{"plate": "LND 123 XY", "insurance_due": "2027-03-03", "service_due": "2027-05-10"}})["saved"].(map[string]any)
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "kind": "person", "details": map[string]any{"birthday": "1960-03-02"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "terms", "title": "Terms", "kind": "text", "details": map[string]any{"body": "No refunds"}})

	if r := w.say(t, on, "remind me when @car is due"); !strings.Contains(r, "Which of @car's dates? Insurance due, Service due") {
		t.Errorf("which: %q", r)
	}
	if r := w.say(t, on, "remind me when @car insurance is due"); !strings.Contains(r, "I'll remind you on 3 Mar 2027 that @car's Insurance due is due, and each year after") {
		t.Fatalf("set: %q", r)
	}
	if r := w.say(t, on, "remind me of @mum's birthday"); !strings.Contains(r, "on 2 Mar 2027 that @mum's Birthday") {
		t.Errorf("birthday: %q", r)
	}
	if r := w.say(t, on, "remind me when @terms is due"); !strings.Contains(r, "@terms holds no date") {
		t.Errorf("no dates: %q", r)
	}
	// A schedule is a workflow to build, not a saved thing's date.
	if r := w.say(t, on, "remind me every Friday to call @mum"); strings.Contains(r, "I'll remind you") {
		t.Errorf("a schedule taken: %q", r)
	}
	// The service, due in May, moved earlier to April: told on its new day.
	w.say(t, on, "remind me when @car service is due")
	if r := w.say(t, on, "my reminders"); !strings.Contains(r, "@mum: Birthday, 2 Mar 2027, each year") || !strings.Contains(r, "@car: Insurance due, 3 Mar 2027, each year") {
		t.Errorf("list: %q", r)
	}

	tick := func(at time.Time) []string {
		t.Helper()
		clock = at
		before := len(w.graph.Sent(on))
		if err := w.srv.SavedRemindersTick(ctx); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range w.lastTo(t, on, before) {
			out = append(out, m.Text)
		}
		return out
	}
	// Mum's birthday on the 2nd is Ada's own: to her own devices and her
	// own chat on Taskiem's number, never through the business (not in its
	// name, no notice of its).
	if got := tick(time.Date(2027, 3, 2, 8, 30, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Birthday") || strings.Contains(got[0], "Ada Foods") {
		t.Fatalf("one's own told: %v", got)
	}
	var notices int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE title LIKE '%Birthday%'`).Scan(&notices); err != nil || notices != 0 {
		t.Fatalf("one's own in the business's notices: %d %v", notices, err)
	}
	// The insurance renewed to April before its day: nothing on the 3rd.
	owner.must(200, "PUT", "/v1/saved/"+van["id"].(string), map[string]any{"values": map[string]any{"insurance_due": "2027-04-20", "service_due": "2027-04-12"}})
	if got := tick(time.Date(2027, 3, 3, 9, 0, 0, 0, lagos)); len(got) != 0 {
		t.Errorf("moved date told: %v", got)
	}
	if got := tick(time.Date(2027, 4, 12, 9, 0, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Service due is today (12 Apr 2027)") {
		t.Fatalf("a date moved earlier: %v", got)
	}
	if got := tick(time.Date(2027, 4, 20, 9, 0, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Delivery van: Insurance due is today (20 Apr 2027)") {
		t.Fatalf("the moved date: %v", got)
	}
	// Asked again the same day: not told again.
	w.say(t, on, "remind me when @car insurance is due")
	if got := tick(time.Date(2027, 4, 20, 10, 0, 0, 0, lagos)); len(got) != 0 {
		t.Errorf("told again after asking again: %v", got)
	}
	// Next year's birthday is waiting.
	var next time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT remind_on FROM saved_reminders WHERE field = 'birthday'`).Scan(&next); err != nil || next.Format(time.DateOnly) != "2028-03-02" {
		t.Errorf("next year: %v %v", next, err)
	}
	// A thing with no date, with words that are not about a date: a
	// workflow's words, not "holds no date".
	if r := w.say(t, on, "remind me to pay @terms on the 1st"); strings.Contains(r, "holds no date") {
		t.Errorf("not about a date taken: %q", r)
	}
	// A sensitive date can be reminded; its date is never said.
	sec := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Licence", "fields": []map[string]any{{"label": "Expires", "type": "date", "sensitive": true}}})["kind"].(map[string]any)
	lic := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "licence", "title": "Driving licence", "kind": "custom", "kind_id": sec["id"], "values": map[string]any{"expires": "2027-06-01"}})["saved"].(map[string]any)
	if r := w.say(t, on, "remind me when @licence expires"); !strings.Contains(r, "I'll remind you on 1 Jun 2027") {
		t.Fatalf("a sensitive date: %q", r)
	}
	if got := tick(time.Date(2027, 6, 1, 9, 0, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Driving licence: Expires is due.") || strings.Contains(got[0], "2027") {
		t.Fatalf("a sensitive date told: %v", got)
	}
	// Removed: its reminders go with it.
	owner.must(204, "DELETE", "/v1/saved/"+lic["id"].(string), nil)
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_reminders WHERE object_id = $1`, lic["id"]).Scan(&left); err != nil || left != 0 {
		t.Errorf("reminders of a removed thing: %d %v", left, err)
	}
	// One's own reminder is tied to no business.
	var tied int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_reminders WHERE person_id IS NOT NULL AND tell_tenant IS NOT NULL`).Scan(&tied); err != nil || tied != 0 {
		t.Errorf("one's own tied to a business: %d %v", tied, err)
	}
	if r := w.say(t, on, "stop reminding me about @car"); !strings.Contains(r, "No more reminders about @car") {
		t.Errorf("stop: %q", r)
	}
	if r := w.say(t, on, "my reminders"); strings.Contains(r, "• @car") {
		t.Errorf("after stopping: %q", r)
	}
}

// A business's reminder is for its members: one who left is told no
// more, and the reminder ends.
func TestSavedReminderEndsWhenMemberLeaves(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@left.test")
	ada := w.member(t, owner, "ada.staff@left.test", "operator")
	const on = "+2348010000529"
	w.bind(t, ada, on)
	lagos, _ := time.LoadLocation("Africa/Lagos")
	clock := time.Date(2027, 3, 1, 10, 0, 0, 0, lagos)
	w.srv.BriefingClock = func() time.Time { return clock }
	gen := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{{"label": "Service due", "type": "date"}}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "gen", "title": "Big gen", "kind": "custom", "kind_id": gen["id"], "values": map[string]any{"service_due": "2027-03-05"}})
	if r := w.say(t, on, "remind me when @gen service is due"); !strings.Contains(r, "I'll remind you on 5 Mar 2027") {
		t.Fatalf("set: %q", r)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM memberships WHERE user_id = (SELECT id FROM users WHERE email = 'ada.staff@left.test')`); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2027, 3, 5, 9, 0, 0, 0, lagos)
	before := len(w.graph.Sent(on))
	if err := w.srv.SavedRemindersTick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.lastTo(t, on, before); len(got) != 0 {
		t.Errorf("told after leaving: %+v", got)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_reminders`).Scan(&n); err != nil || n != 0 {
		t.Errorf("reminders left: %d %v", n, err)
	}
}

// Two things of one name: a reminder asks which only when both hold the
// date the words name.
func TestSavedReminderPick(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@reminderpick.test")
	const on = "+2348010000520"
	w.bind(t, owner, on)
	lagos, _ := time.LoadLocation("Africa/Lagos")
	w.srv.BriefingClock = func() time.Time { return time.Date(2027, 3, 1, 10, 0, 0, 0, lagos) }

	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "kind": "person", "details": map[string]any{"birthday": "1960-03-02"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum", "title": "Mum's shop", "kind": "person", "details": map[string]any{"phone": "0803 123 4567"}})
	if r := w.say(t, on, "remind me of @mum's birthday"); !strings.Contains(r, "on 2 Mar 2027 that @mum's Birthday") {
		t.Fatalf("only one holds it: %q", r)
	}
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "tolu", "title": "Tolu", "kind": "person", "details": map[string]any{"birthday": "1990-04-02"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "tolu", "title": "Tolu Bello", "kind": "person", "details": map[string]any{"birthday": "1988-05-09"}})
	if r := w.say(t, on, "remind me of @tolu's birthday"); !strings.Contains(r, "Which @tolu?") || !strings.Contains(r, "2. Ada Foods's: Tolu Bello") {
		t.Fatalf("which: %q", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "on 9 May 2027 that @tolu's Birthday") {
		t.Fatalf("the business's: %q", r)
	}
}
