package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A code asked for in WhatsApp or Telegram (g-scan-wa) is the next
// message only when it can be one: words, not a command, not a button,
// and not a bare number while a menu waits for a number; a barcode's
// digits are a code when nothing waits.
func TestCodeWords(t *testing.T) {
	idle := func(text string) *chat {
		return &chat{state: stateIdle, in: whatsapp.Inbound{Type: "text", Text: text}}
	}
	menu := func(text string) *chat {
		return &chat{state: stateCollecting, data: chatData{Menu: &chatMenuState{}}, in: whatsapp.Inbound{Type: "text", Text: text}}
	}
	for _, c := range []struct {
		c    *chat
		want bool
	}{
		{idle("6151100093217"), true},
		{idle("ABC-123 shelf 4"), true},
		{idle("help"), false},
		{idle("/visit"), false},
		{idle("menu"), false},
		{idle("  "), false},
		{idle("bad\x07code"), false},
		{menu("2"), false},
		{menu("6151100093217"), false},
		{menu("ABC-123"), true},
		{&chat{state: stateConfirming, in: whatsapp.Inbound{Type: "text", Text: "ABC-123"}}, false},
		{&chat{state: stateIdle, in: whatsapp.Inbound{Type: "text", Text: "ABC-123", Reply: "yes"}}, false},
	} {
		if got := codeWords(c.c); got != c.want {
			t.Errorf("%q (%s): %v, want %v", c.c.in.Text, c.c.state, got, c.want)
		}
	}
}
