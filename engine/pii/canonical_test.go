package pii_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/pii"
)

// The same person's value in another format keys the same subject.
func TestCanonical(t *testing.T) {
	for _, c := range []struct {
		cat  string
		in   any
		want any
	}{
		{"phone", "+2348031234567", "+2348031234567"},
		{"phone", "08031234567", "+2348031234567"},
		{"phone", "0803 123 4567", "+2348031234567"},
		{"phone", " +234 (0)803 123 4567", "+2348031234567"},
		{"phone", "(0803) 123-4567", "+2348031234567"},
		{"phone", "2348031234567", "+2348031234567"},
		{"phone", "+44 20 7946 0958", "+442079460958"},
		{"phone", "not a phone", "not a phone"},
		{"email", " Ada@X.com ", "ada@x.com"},
		{"bvn", "222 1234 5678", "22212345678"},
		{"account_number", "0123-456-789", "0123456789"},
		{"bvn", "BVN 2221", "BVN 2221"},
		{"name", " Ada Obi ", " Ada Obi "},
		{"other", "Hello", "Hello"},
		{"phone", float64(8031234567), float64(8031234567)},
	} {
		if got := pii.Canonical(c.cat, c.in); got != c.want {
			t.Errorf("Canonical(%s, %v) = %v, want %v", c.cat, c.in, got, c.want)
		}
	}
}
