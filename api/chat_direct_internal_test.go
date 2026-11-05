package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/links"
)

// The "..." marker (chat_direct.go): three or more full stops or a phone's
// "…", then a space, a new line or nothing.
func TestCutDirect(t *testing.T) {
	for _, c := range []struct {
		in, rest string
		ok       bool
	}{
		{"...", "", true},
		{"  ...  ", "", true},
		{"…", "", true},
		{"...\n@Dangote hi", "@Dangote hi", true},
		{"... @Dangote hi", "@Dangote hi", true},
		{"….\n@Dangote hi", "", false},
		{"…\n@Dangote hi\nthere", "@Dangote hi\nthere", true},
		{"..... @BUA hi", "@BUA hi", true},
		{"...and five more", "", false},
		{"..", "", false},
		{"hi ...", "", false},
		{"@Dangote ...", "", false},
	} {
		rest, ok := cutDirect(c.in)
		if ok != c.ok || rest != c.rest {
			t.Errorf("%q: %q %v, want %q %v", c.in, rest, ok, c.rest, c.ok)
		}
	}
}

// What the businesses receive: mentions at the start left out, the rest
// kept as typed without the "@".
func TestDirectBody(t *testing.T) {
	act := []links.Link{{Name: "Dangote Nigeria Ltd", State: links.Active}, {Name: "BUA Foods", State: links.Active}}
	for in, want := range map[string]string{
		"@Dangote how much is cement?":         "how much is cement?",
		"@Dangote, @BUA: price this week?":     "price this week?",
		"@Dangote @bua foods price?":           "price?",
		"Hi @Dangote, is the depot open?":      "Hi Dangote, is the depot open?",
		"@Dangote\ntwo lines\nhere":            "two lines\nhere",
		"@Dangote ask @BUA Foods for the rest": "ask BUA Foods for the rest",
		"@Dangote":                             "",
	} {
		if got := directBody(in, parseMentions(in, act).Parts); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
