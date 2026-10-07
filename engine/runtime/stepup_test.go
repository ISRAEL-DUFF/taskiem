package runtime_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/runtime"
)

func TestStepUpSatisfies(t *testing.T) {
	for _, c := range []struct {
		required, given string
		ok              bool
	}{
		{"", "", true},
		{"", "whatsapp_pin", true},
		{"whatsapp_pin", "whatsapp_pin", true},
		{"whatsapp_pin", "totp", true},
		{"whatsapp_pin", "passkey", true},
		{"whatsapp_pin", "", false},
		{"totp", "whatsapp_pin", false}, // a PIN never stands in for a stronger factor
		{"totp", "totp", true},
		{"totp", "passkey", true},
		{"passkey", "whatsapp_pin", false},
		{"passkey", "totp", false},
		{"passkey", "passkey", true},
		{"something_new", "passkey", false},
	} {
		if got := runtime.StepUpSatisfies(c.required, c.given); got != c.ok {
			t.Errorf("%q by %q: %v", c.required, c.given, got)
		}
	}
}
