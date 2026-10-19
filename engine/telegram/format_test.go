package telegram_test

import (
	"reflect"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
)

func TestBoldReadsWhatsAppMarkupAsEntities(t *testing.T) {
	for _, c := range []struct {
		in, text string
		ents     []telegram.Entity
	}{
		{"Send *help* to see what you can do.", "Send help to see what you can do.", []telegram.Entity{{Type: "bold", Offset: 5, Length: 4}}},
		{"*a* and *b c*", "a and b c", []telegram.Entity{{Type: "bold", Offset: 0, Length: 1}, {Type: "bold", Offset: 6, Length: 3}}},
		// Not markup: inside a word, around spaces, across lines, unclosed.
		{"2*3*4 = 24", "2*3*4 = 24", nil},
		{"a * b * c", "a * b * c", nil},
		{"*not\nbold*", "*not\nbold*", nil},
		{"price *", "price *", nil},
		{"**", "**", nil},
		// Offsets count UTF-16 units: an emoji before the span is two.
		{"🔒 *sealed*", "🔒 sealed", []telegram.Entity{{Type: "bold", Offset: 3, Length: 6}}},
		// Nothing else is markup: angle brackets and ampersands stay as typed.
		{"<b>&amp;</b> *x*", "<b>&amp;</b> x", []telegram.Entity{{Type: "bold", Offset: 13, Length: 1}}},
	} {
		text, ents := telegram.Bold(c.in)
		if text != c.text || !reflect.DeepEqual(ents, c.ents) {
			t.Errorf("Bold(%q) = %q %v, want %q %v", c.in, text, ents, c.text, c.ents)
		}
	}
}
