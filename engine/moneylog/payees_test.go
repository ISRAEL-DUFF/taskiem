package moneylog_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

func TestSubjectAccount(t *testing.T) {
	for _, c := range []struct {
		subject       any
		account, bank string
	}{
		{map[string]any{"account_number": "0123 456 789", "bank_code": "058"}, "0123 456 789", "058"},
		{map[string]any{"nuban": "0987654321"}, "0987654321", ""},
		{map[string]any{"account": float64(9876543210)}, "9876543210", ""},
		{map[string]any{"account": "main wallet", "nuban": "1234567890"}, "1234567890", ""},
		{map[string]any{"account_number": "12"}, "", ""},
		{map[string]any{"amount_kobo": float64(500)}, "", ""},
		{"pay", "", ""},
		{nil, "", ""},
	} {
		a, b := moneylog.SubjectAccount(c.subject)
		if a != c.account || b != c.bank {
			t.Errorf("%v: %q %q, want %q %q", c.subject, a, b, c.account, c.bank)
		}
	}
	if d := moneylog.AccountDigits("0123-456-789"); d != "0123456789" {
		t.Errorf("digits %q", d)
	}
	if d := moneylog.AccountDigits("12345"); d != "" {
		t.Errorf("five digits are not an account: %q", d)
	}
}
