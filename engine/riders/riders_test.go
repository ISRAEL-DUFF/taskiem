package riders_test

import (
	"slices"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestParseWord(t *testing.T) {
	lagos, _ := time.LoadLocation("Africa/Lagos")
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, lagos)
	for _, c := range []struct {
		in    string
		state string
		until string // Lagos, "" for none
		ok    bool
	}{
		{"off", "off", "", true},
		{" OFF! ", "off", "", true},
		{"on", "on", "", true},
		{"free", "on", "", true},
		{"busy", "busy", "2026-10-28 23:59", true},
		{"busy till 3pm", "busy", "2026-10-28 15:00", true},
		{"Busy until 3:30 pm", "busy", "2026-10-28 15:30", true},
		{"busy till 15:45", "busy", "2026-10-28 15:45", true},
		{"busy till 9am", "busy", "2026-10-29 09:00", true},
		{"busy till 12am", "busy", "2026-10-29 00:00", true},
		{"busy till 13pm", "", "", false},
		{"busy till 25", "", "", false},
		{"busy till soon", "", "", false},
		{"offline", "", "", false},
		{"hello", "", "", false},
	} {
		got, ok := riders.ParseWord(c.in, now)
		if ok != c.ok || got.State != c.state {
			t.Errorf("%q: %+v %v", c.in, got, ok)
			continue
		}
		if c.until != "" && got.Until.In(lagos).Format("2006-01-02 15:04") != c.until {
			t.Errorf("%q: until %s, want %s", c.in, got.Until.In(lagos).Format("2006-01-02 15:04"), c.until)
		}
	}
}

func TestSplitZones(t *testing.T) {
	known := []storefront.Zone{{Name: "Lekki Phase 1"}, {Name: "Lekki"}, {Name: "Ajah"}}
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"Lekki Ajah", []string{"Lekki", "Ajah"}},
		{"lekki phase 1 ajah", []string{"lekki phase 1", "ajah"}},
		{"Yaba, Lekki Phase 1", []string{"Yaba", "Lekki Phase 1"}},
		{"Surulere Ikeja", []string{"Surulere", "Ikeja"}},
		{"", nil},
	} {
		if got := riders.SplitZones(c.in, known); !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanZones(t *testing.T) {
	known := []storefront.Zone{{Name: "Lekki Phase 1"}, {Name: "Yaba"}}
	got, err := riders.CleanZones([]string{"yaba", " LEKKI  phase 1 ", "Yaba"}, known)
	if err != nil || !slices.Equal(got, []string{"Yaba", "Lekki Phase 1"}) {
		t.Errorf("%q %v", got, err)
	}
	if _, err := riders.CleanZones([]string{"Ikeja"}, known); err == nil {
		t.Error("an area not the business's was taken")
	}
	if got, err := riders.CleanZones([]string{"Ikeja", "ikeja"}, nil); err != nil || !slices.Equal(got, []string{"Ikeja"}) {
		t.Errorf("free labels: %q %v", got, err)
	}
}

func TestInviteText(t *testing.T) {
	if got := riders.InviteText("Ada Stores", "https://forms.test/u/abc"); got != "You were added as a rider for Ada Stores. Tap to get their deliveries on WhatsApp: https://forms.test/u/abc" {
		t.Error(got)
	}
}
