package discover

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestMissingWords(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{MissingPhoto}, "Customers can't find you in Discover until you add a photo."},
		{[]string{MissingArea, MissingHours}, "Customers can't find you in Discover until you add an area and opening hours."},
		{[]string{MissingCategory, MissingArea, MissingPhoto, MissingHours},
			"Customers can't find you in Discover until you add a category, an area, a photo and opening hours."},
	} {
		if got := MissingWords(c.in); got != c.want {
			t.Errorf("%v: %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHoursLine(t *testing.T) {
	week := func(spans ...storefront.Span) storefront.Hours {
		var h storefront.Hours
		for d := range 5 { // Monday to Friday
			h.Days[d] = spans
		}
		return h
	}
	h := week(storefront.Span{Open: "09:00", Close: "18:00"})
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.ParseInLocation("2006-01-02 15:04", s, storefront.Lagos)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		h    storefront.Hours
		now  string
		open bool
		line string
	}{
		{h, "2026-11-17 10:00", true, "Open now, closes at 18:00"}, // a Tuesday
		{h, "2026-11-17 07:00", false, "Closed now. Opens at 09:00"},
		{h, "2026-11-17 19:00", false, "Closed now. Opens tomorrow at 09:00"},
		{h, "2026-11-20 19:00", false, "Closed now. Opens on Monday at 09:00"}, // a Friday
		{storefront.Hours{}, "2026-11-17 10:00", false, ""},
	} {
		open, line := HoursLine(c.h, at(c.now))
		if open != c.open || line != c.line {
			t.Errorf("%s: %v %q, want %v %q", c.now, open, line, c.open, c.line)
		}
	}
}

func TestCheckHours(t *testing.T) {
	ok := storefront.Hours{}
	ok.Days[0] = []storefront.Span{{Open: "08:00", Close: "18:30"}, {Open: "22:00", Close: "02:00"}}
	if err := checkHours(ok); err != nil {
		t.Errorf("good hours refused: %v", err)
	}
	for _, sp := range []storefront.Span{{Open: "8:00", Close: "18:00"}, {Open: "24:00", Close: "18:00"}, {Open: "09:60", Close: "18:00"}, {Open: "09:00", Close: "09:00"}} {
		bad := storefront.Hours{}
		bad.Days[2] = []storefront.Span{sp}
		if err := checkHours(bad); err == nil {
			t.Errorf("%v taken", sp)
		}
	}
}
