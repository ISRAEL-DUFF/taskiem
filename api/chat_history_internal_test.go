package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// What is kept of a message's words (chat_history.go): links without
// their queries or fragments and with long opaque parts cut; nothing of
// a message that is only a code.
func TestHistoryText(t *testing.T) {
	for in, want := range map[string]string{
		"Open https://app.taskiem.test/approve?t=tk1.abc.def to decide.":                                "Open https://app.taskiem.test/approve to decide.",
		"Reset: https://app.taskiem.test/reset-password#token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc": "Reset: https://app.taskiem.test/reset-password",
		"Your link: https://t.taskiem.test/r/Zm9vYmFyYmF6cXV4cXV1eDEyMzQ1Njc4OTA, valid a day.":         "Your link: https://t.taskiem.test/r/…, valid a day.",
		"(see https://taskiem.ng/help/invoices).":                                                       "(see https://taskiem.ng/help/invoices).",
		"123456":                     "",
		" 4821 ":                     "",
		"080-1234":                   "",
		"20":                         "20",
		"4111 1111 1111 1111":        "", // a card number
		"4111-1111-1111-1111":        "",
		"card 4111111111111111 pls":  "", // a card number in words
		"5399 8300 1234 5678":        "", // a number of a card's shape, mostly digits
		"0123456789012345":           "", // sixteen digits, no longer kept
		"pay 2 bags of rice, 50 kg":  "pay 2 bags of rice, 50 kg",
		"INV-0042 ₦25,000 by 15 Nov": "INV-0042 ₦25,000 by 15 Nov",
		"Order P-7QX4KD: 20 bags at 45,000 each, 900,000 in all, due 2026-11-30": "Order P-7QX4KD: 20 bags at 45,000 each, 900,000 in all, due 2026-11-30",
		"status":                     "status",
		"   ":                        "",
		"Sold 3 cartons for ₦30,000": "Sold 3 cartons for ₦30,000",
	} {
		if got := historyText(in); got != want {
			t.Errorf("historyText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := historyText(strings.Repeat("a", 5000)); len([]rune(got)) != maxHistoryText {
		t.Errorf("clipped to %d", len(got))
	}
	u := uuid.New()
	if got, ok := webUser(webAddr(u)); !ok || got != u {
		t.Errorf("web address: %v %v", got, ok)
	}
	if _, ok := webUser("+2348010000000"); ok {
		t.Error("a phone is not a web address")
	}
}
