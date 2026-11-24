package links

import "testing"

func TestValidReactionIsExactlyOneEmoji(t *testing.T) {
	for _, ok := range []string{"👍", "❤️", "😂", "🎉", "🔥", "👩🏽\u200d💻", "👨\u200d👩\u200d👧\u200d👦", "🇳🇬", "1️⃣", "✅", "⭐", "🫡"} {
		if !ValidReaction(ok) {
			t.Errorf("%q should be a reaction", ok)
		}
	}
	for _, no := range []string{"", "a", "A1", " ", "👍 ", "👍👍", "❤️❤️", "<b>", "👍<", "\u200d", "👍\u200d", "🇳", "🇳🇬🇳🇬", "1", "12", "x👍", "javascript:alert(1)", "👍\n", "🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉🎉"} {
		if ValidReaction(no) {
			t.Errorf("%q should not be a reaction", no)
		}
	}
}
