package linkify

import (
	"encoding/json"
	"os"
	"testing"
)

// The cases the web and the app share (web/src/lib/linkify.cases.json).
func TestSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/lib/linkify.cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Text  string `json:"text"`
			Links []struct {
				Text, URL, Shown string
			} `json:"links"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Cases {
		got := Find(c.Text)
		if len(got) != len(c.Links) {
			t.Errorf("%q: found %d links %+v, want %d", c.Text, len(got), got, len(c.Links))
			continue
		}
		for i, w := range c.Links {
			g := got[i]
			if g.Text != w.Text || g.URL != w.URL || c.Text[g.Start:g.End] != w.Text {
				t.Errorf("%q: link %d = %+v, want %+v", c.Text, i, g, w)
			}
			if w.Shown != "" && Shown(g.Text) != w.Shown {
				t.Errorf("%q: shown %q, want %q", c.Text, Shown(g.Text), w.Shown)
			}
		}
	}
	if First("no links") != "" || First("a example.com b https://x.org") != "https://example.com" {
		t.Error("First")
	}
}
