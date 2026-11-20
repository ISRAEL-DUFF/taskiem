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

	if r := w.say(t, on, "remind me about @car"); !strings.Contains(r, "Which of @car's dates? Insurance due, Service due") {
		t.Errorf("which: %q", r)
	}
	if r := w.say(t, on, "remind me when @car insurance is due"); !strings.Contains(r, "I'll remind you on 3 Mar 2027 that @car's Insurance due is due, and each year after") {
		t.Fatalf("set: %q", r)
	}
	if r := w.say(t, on, "remind me of @mum's birthday"); !strings.Contains(r, "on 2 Mar 2027 that @mum's Birthday") {
		t.Errorf("birthday: %q", r)
	}
	if r := w.say(t, on, "remind me about @terms"); !strings.Contains(r, "@terms holds no date") {
		t.Errorf("no dates: %q", r)
	}
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
	// Mum's birthday on the 2nd: not before the morning, then once.
	if got := tick(time.Date(2027, 3, 2, 6, 0, 0, 0, lagos)); len(got) != 0 {
		t.Errorf("before the morning: %v", got)
	}
	if got := tick(time.Date(2027, 3, 2, 8, 30, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Mum: Birthday is today (2 Mar 2027)") {
		t.Fatalf("the birthday: %v", got)
	}
	if got := tick(time.Date(2027, 3, 2, 9, 0, 0, 0, lagos)); len(got) != 0 {
		t.Errorf("told twice: %v", got)
	}
	// The insurance renewed to April before its day: nothing on the 3rd.
	owner.must(200, "PUT", "/v1/saved/"+van["id"].(string), map[string]any{"values": map[string]any{"insurance_due": "2027-04-20"}})
	if got := tick(time.Date(2027, 3, 3, 9, 0, 0, 0, lagos)); len(got) != 0 {
		t.Errorf("moved date told: %v", got)
	}
	if got := tick(time.Date(2027, 4, 20, 9, 0, 0, 0, lagos)); len(got) != 1 || !strings.Contains(got[0], "Delivery van: Insurance due is today (20 Apr 2027)") {
		t.Fatalf("the moved date: %v", got)
	}
	// Next year's birthday is waiting.
	var next time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT remind_on FROM saved_reminders WHERE field = 'birthday'`).Scan(&next); err != nil || next.Format(time.DateOnly) != "2028-03-02" {
		t.Errorf("next year: %v %v", next, err)
	}
	if r := w.say(t, on, "stop reminding me about @car"); !strings.Contains(r, "No more reminders about @car") {
		t.Errorf("stop: %q", r)
	}
	if r := w.say(t, on, "my reminders"); strings.Contains(r, "• @car") {
		t.Errorf("after stopping: %q", r)
	}
}
