package api

import (
	"testing"
	"time"
)

func TestNextOccurrence(t *testing.T) {
	d := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	for _, c := range []struct {
		v, after string
		yearly   bool
		want     string
		ok       bool
	}{
		{"2027-03-03", "2027-03-01", true, "2027-03-03", true},
		{"2027-03-03", "2027-03-03", true, "2028-03-03", true},
		{"1960-03-02", "2027-03-01", true, "2027-03-02", true},
		{"2027-03-03", "2027-03-03", false, "", false},
		{"2027-03-03", "2026-12-31", false, "2027-03-03", true},
		// 29 Feb: 1 Mar in a year without one, 29 Feb again in a leap year.
		{"2024-02-29", "2027-02-27", true, "2027-03-01", true},
		{"2024-02-29", "2027-03-01", true, "2028-02-29", true},
	} {
		got, ok := nextOccurrence(d(c.v), c.yearly, d(c.after))
		if ok != c.ok || (ok && got.Format(time.DateOnly) != c.want) {
			t.Errorf("nextOccurrence(%s, %v, %s) = %s %v, want %s %v", c.v, c.yearly, c.after, got.Format(time.DateOnly), ok, c.want, c.ok)
		}
	}
}
