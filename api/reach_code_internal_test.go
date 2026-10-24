package api

import "testing"

// A link's code is found whatever character it ends in: a code ending in
// "-" (one in 64) has no word boundary after it.
func TestReachCodeRe(t *testing.T) {
	for _, tc := range []struct{ text, code string }{
		{"Get updates from Ada Stores U-abcdefghijklmnopqrstuvwxyz012345", "abcdefghijklmnopqrstuvwxyz012345"},
		{"Get updates from Ada Stores U-abcdefghijklmnopqrstuvwxyz01234-", "abcdefghijklmnopqrstuvwxyz01234-"},
		{"Get updates from Ada Stores U-_bcdefghijklmnopqrstuvwxyz0123-_", "_bcdefghijklmnopqrstuvwxyz0123-_"},
		{"U-abcdefghijklmnopqrstuvwxyz01234- please", "abcdefghijklmnopqrstuvwxyz01234-"},
		{"U-abcdefghijklmnopqrstuvwxyz0123456 (one too many)", ""},
		{"XU-abcdefghijklmnopqrstuvwxyz012345", ""},
	} {
		got := ""
		if m := reachCodeRe.FindStringSubmatch(tc.text); m != nil {
			got = m[1]
		}
		if got != tc.code {
			t.Errorf("%q: got %q, want %q", tc.text, got, tc.code)
		}
	}
}
