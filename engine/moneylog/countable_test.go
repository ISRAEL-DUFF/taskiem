package moneylog

import "testing"

// What the payer sent counts: the amount, plus the fee when the amount is
// what landed.
func TestCountableKobo(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	cases := []struct {
		e    Entry
		want int64
	}{
		{Entry{}, 0},
		{Entry{AmountKobo: n(500000)}, 500000},
		// Gross: the fee was a separate cost.
		{Entry{AmountKobo: n(500000), FeeKobo: n(5000)}, 500000},
		// Net: what landed plus the fee.
		{Entry{AmountKobo: n(495000), FeeKobo: n(5000), Net: true}, 500000},
		// Net with no fee given, and no amount at all.
		{Entry{AmountKobo: n(495000), Net: true}, 495000},
		{Entry{FeeKobo: n(5000), Net: true}, 0},
	}
	for i, c := range cases {
		if got := CountableKobo(c.e); got != c.want {
			t.Errorf("%d: %d, want %d", i, got, c.want)
		}
	}
}
