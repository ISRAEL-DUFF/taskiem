package api

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestParseWhen(t *testing.T) {
	// Tuesday 20 October 2026, 09:15 in Lagos.
	now := time.Date(2026, 10, 20, 9, 15, 0, 0, storefront.Lagos)
	at := func(m time.Month, d, hh, mm int) time.Time {
		return time.Date(2026, m, d, hh, mm, 0, 0, storefront.Lagos)
	}
	for in, want := range map[string]time.Time{
		"sat 11:00":         at(10, 24, 11, 0),
		"Saturday 2pm":      at(10, 24, 14, 0),
		"tue 9am":           at(10, 20, 9, 0),
		"tuesday at 2:30pm": at(10, 20, 14, 30),
		"tomorrow 10:30":    at(10, 21, 10, 30),
		"today 17:00":       at(10, 20, 17, 0),
		"24 oct 12 pm":      at(10, 24, 12, 0),
		"oct 24 12am":       at(10, 24, 0, 0),
		"2026-11-02 14:00":  at(11, 2, 14, 0),
		"25/12 9":           at(12, 25, 9, 0),
		"5th jan 08:00":     time.Date(2027, 1, 5, 8, 0, 0, 0, storefront.Lagos),
	} {
		got, ok := parseWhen(in, now, true)
		if !ok || !got.Equal(want) {
			t.Errorf("%q: %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"sat", "someday 11:00", "monkey 10:00", "31 feb 10:00", "sat 25:00", "sat 10:61", ""} {
		if got, ok := parseWhen(in, now, true); ok {
			t.Errorf("%q read as %v", in, got)
		}
	}
	if got, ok := parseWhen("25 Dec", now, false); !ok || !got.Equal(at(12, 25, 0, 0)) {
		t.Errorf("a day: %v %v", got, ok)
	}
	if _, ok := parseWhen("25 Dec 10:00", now, false); ok {
		t.Error("a day with a time was read as a day")
	}
}
