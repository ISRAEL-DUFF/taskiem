package api

import "testing"

// Values typed in chat are kept as meant, as the web app keeps them (N5).
func TestPlainValue(t *testing.T) {
	for in, want := range map[string]any{"Ade": "Ade", "0803 123 4567": "+2348031234567", "+234 803 123 4567": "+2348031234567",
		"100000": float64(100000), "0123456789": "0123456789", "true": true, " Ade Stores ": "Ade Stores"} {
		if got := plainValue(in); got != want {
			t.Errorf("%q: %v (%T), want %v", in, got, got, want)
		}
	}
}
