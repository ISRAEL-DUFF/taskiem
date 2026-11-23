package api

import (
	"strings"
	"testing"
)

// A link code of one's own account that happens to begin "u_" is not a
// business's updates code: only "u_" and a whole updates code is one.
func TestReachStartCode(t *testing.T) {
	updates := strings.Repeat("A", 32)
	ownLink := "u_" + strings.Repeat("B", 30) // 32 characters, as IssueLinkCode makes
	cases := []struct {
		text string
		code string
		ok   bool
	}{
		{"/start u_" + updates, updates, true},
		{"/start u_" + updates + " ", updates, true},
		{"/start " + ownLink, "", false},
		{"/start u_short", "", false},
		{"/start " + updates, "", false},
		{"hello", "", false},
	}
	for _, c := range cases {
		code, ok := reachStartCode(c.text)
		if code != c.code || ok != c.ok {
			t.Errorf("reachStartCode(%q) = %q, %v; want %q, %v", c.text, code, ok, c.code, c.ok)
		}
	}
}
