package api_test

import (
	"testing"
	"time"
)

// The Overview and the daily close (B9): the term's money as it stands,
// the top debtors masked unless revealed (audited); a day closed once
// by whoever asks first, then read back, never recomputed; the day not
// over refused; nobody without school.manage closes.
func TestSchoolOverviewAndClose(t *testing.T) {
	w := newWorld(t)
	owner, _, tid, _ := clearanceSchool(t, w)
	viewer := addMember(t, w, owner, "vic", "viewer")
	builder := addMember(t, w, owner, "bob", "builder")

	o := viewer.must(200, "GET", "/v1/schools/overview", nil)
	ov := o["overview"].(map[string]any)
	if o["revealed"] != false || ov["term_id"] != tid || ov["term"] != "First term 2026/2027" || ov["enrolled"] != float64(3) || ov["invoiced"] != float64(3) {
		t.Fatalf("overview %v", ov)
	}
	// Three invoices of ₦100,000 due 2026-09-07: all expected by now;
	// ₦140,000 collected in cash by a person, this week; ₦160,000
	// outstanding and overdue.
	for k, want := range map[string]float64{"expected_kobo": 30_000_000, "expected_by_now_kobo": 30_000_000, "collected_kobo": 14_000_000, "outstanding_kobo": 16_000_000,
		"overdue_kobo": 16_000_000, "collected_percent": 46, "this_week_kobo": 14_000_000, "pass_through_kobo": 0, "debtors": 2, "debtors_kobo": 16_000_000} {
		if ov[k] != want {
			t.Errorf("%s = %v, want %v", k, ov[k], want)
		}
	}
	if m := ov["matched"].(map[string]any); m["person_kobo"] != float64(14_000_000) || m["auto_kobo"] != float64(0) || m["auto_percent"] != float64(0) {
		t.Fatalf("matched %v", m)
	}
	if hp := ov["how_paid"].([]any); len(hp) != 1 || hp[0].(map[string]any)["how"] != "cash" || hp[0].(map[string]any)["payments"] != float64(2) {
		t.Fatalf("how paid %v", hp)
	}
	top := ov["top_debtors"].([]any)
	if len(top) != 2 || top[0].(map[string]any)["admission_number"] != "GCA/003" || top[0].(map[string]any)["name"] != "T•••" || top[0].(map[string]any)["balance_kobo"] != float64(10_000_000) {
		t.Fatalf("top debtors %v", top)
	}
	if ag := ov["aging"].(map[string]any)["over_30"].(map[string]any); ag["students"] != float64(2) {
		t.Fatalf("aging %v", ov["aging"])
	}
	if cl := ov["clearance"].(map[string]any); cl["cleared"] != float64(1) || cl["partly_paid"] != float64(1) || cl["not_paid"] != float64(1) {
		t.Fatalf("clearance %v", cl)
	}
	if _, ok := ov["last_close"]; ok {
		t.Fatalf("a close before any was taken: %v", ov["last_close"])
	}
	viewer.must(200, "GET", "/v1/schools/overview?term="+tid, nil)
	viewer.must(400, "GET", "/v1/schools/overview?term=soon", nil)
	viewer.must(403, "GET", "/v1/schools/overview?reveal=true", nil)
	builder.must(403, "GET", "/v1/schools/overview", nil)
	if got := owner.must(200, "GET", "/v1/schools/overview?reveal=true", nil)["overview"].(map[string]any)["top_debtors"].([]any); got[0].(map[string]any)["name"] != "Tolu Ade" {
		t.Fatalf("revealed %v", got)
	}
	if n := schoolAudits(t, w, "Greenfield", "pii.reveal"); n != 1 {
		t.Fatalf("%d reveal audits", n)
	}

	// The close: yesterday, taken by the first to ask; the same close for
	// the next; listed; read back; today refused; viewers read, never
	// close.
	viewer.must(403, "POST", "/v1/schools/close", nil)
	owner.must(400, "POST", "/v1/schools/close", map[string]any{"day": "soon"})
	today := time.Now().In(time.FixedZone("WAT", 3600)).Format("2006-01-02")
	owner.must(409, "POST", "/v1/schools/close", map[string]any{"day": today})
	first := owner.must(200, "POST", "/v1/schools/close", nil)
	c := first["close"].(map[string]any)
	yesterday := time.Now().In(time.FixedZone("WAT", 3600)).AddDate(0, 0, -1).Format("2006-01-02")
	if first["taken_now"] != true || c["day"] != yesterday || c["reconciles"] != true || c["problems"] != float64(0) || c["term"] != "First term 2026/2027" {
		t.Fatalf("close %v", first)
	}
	if in := c["money_in"].(map[string]any); in["count"] != float64(0) {
		t.Fatalf("yesterday's money %v", in)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.close.run"); n != 1 {
		t.Fatalf("%d close audits", n)
	}
	again := owner.must(200, "POST", "/v1/schools/close", map[string]any{"day": yesterday})
	if again["taken_now"] != false || again["close"].(map[string]any)["id"] != c["id"] {
		t.Fatalf("second close %v", again)
	}
	if n := schoolAudits(t, w, "Greenfield", "school.close.run"); n != 1 {
		t.Fatalf("%d close audits after a repeat", n)
	}
	list := viewer.must(200, "GET", "/v1/schools/closes", nil)["closes"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != c["id"] || list[0].(map[string]any)["reconciles"] != true {
		t.Fatalf("closes %v", list)
	}
	if got := viewer.must(200, "GET", "/v1/schools/closes?from="+today, nil)["closes"].([]any); len(got) != 0 {
		t.Fatalf("from today %v", got)
	}
	viewer.must(400, "GET", "/v1/schools/closes?from=soon", nil)
	if got := viewer.must(200, "GET", "/v1/schools/closes/"+yesterday, nil)["close"].(map[string]any); got["id"] != c["id"] || got["ran_by"] != c["ran_by"] {
		t.Fatalf("read back %v", got)
	}
	viewer.must(404, "GET", "/v1/schools/closes/2020-01-01", nil)
	viewer.must(400, "GET", "/v1/schools/closes/soon", nil)
	// The overview now names the last close.
	if lc := viewer.must(200, "GET", "/v1/schools/overview", nil)["overview"].(map[string]any)["last_close"].(map[string]any); lc["id"] != c["id"] {
		t.Fatalf("last close %v", lc)
	}
}
