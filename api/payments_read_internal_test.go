package api

import "testing"

// The amount a message says to pay: naira or k; the account number,
// however spaced, and phone numbers set aside; one amount, or the one
// after pay, send or transfer; none when it is not clear which.
func TestReadPayAmount(t *testing.T) {
	const n = "0123456789"
	for _, c := range []struct {
		text string
		want int64
	}{
		{"send 45k to 0123456789 gtb", 4500000},
		{"0123 456 789 Access ₦45,000", 4500000},
		{"Tel 0803 123 4567 naira, acct 0123456789", 0},
		{"2 bags 5k each, so pay 10k to 0123456789", 1000000},
		{"5k or 10k, 0123456789", 0},
		{"0123456789 GTBank Chidi", 0},
	} {
		if got := readPayAmount(c.text, n); got != c.want {
			t.Errorf("%q: %d, want %d", c.text, got, c.want)
		}
	}
}
