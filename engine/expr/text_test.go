package expr

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestChunks(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want []string
	}{
		{"", 10, []string{}},
		{"Short one.", 50, []string{"Short one."}},
		{"We open at 8. We close at 6. Call us anytime.", 30, []string{"We open at 8. We close at 6.", "Call us anytime."}},
		{"line one\nline two is here", 12, []string{"line one", "line two is", "here"}},
		{"no sentence end here at all", 10, []string{"no", "sentence", "end here", "at all"}},
		{"abcdefghijklmnop", 5, []string{"abcde", "fghij", "klmno", "p"}},
		{"Ẹ kú àárọ̀. Ṣé ẹ wà dáadáa?", 12, []string{"Ẹ kú àárọ̀.", "Ṣé ẹ wà", "dáadáa?"}},
		{"Price: 1.5 kg costs N3,000. Done.", 20, []string{"Price: 1.5 kg costs", "N3,000. Done."}},
	} {
		got := Chunks(c.in, c.max)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Chunks(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		for _, p := range got {
			if utf8.RuneCountInString(p) > c.max {
				t.Errorf("%q is longer than %d", p, c.max)
			}
		}
	}
	long := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 300)
	parts := Chunks(long, 4096)
	if len(parts) != 4 || strings.Join(parts, " ") != strings.TrimSpace(long) {
		t.Fatalf("%d parts; joined again they must be the text", len(parts))
	}
}

func TestChunksInExpressions(t *testing.T) {
	got, err := e.Eval("=chunks('One. Two. Three.', 9)", nil)
	if err != nil || !reflect.DeepEqual(got, []any{"One. Two.", "Three."}) {
		t.Fatalf("%v %v", got, err)
	}
	got, err = e.Eval("=chunks('ab\U0001F600cd', 3, 'utf16')", nil)
	if err != nil || !reflect.DeepEqual(got, []any{"ab", "\U0001F600c", "d"}) {
		t.Fatalf("utf16: %q %v", got, err)
	}
	got, err = e.Eval("=chunks('ab\U0001F600cd', 3, 'characters')", nil)
	if err != nil || !reflect.DeepEqual(got, []any{"ab\U0001F600", "cd"}) {
		t.Fatalf("characters: %q %v", got, err)
	}
	for _, bad := range []string{"=chunks('x', 0)", "=chunks(1, 5)", "=chunks('x', 5, 'bytes')"} {
		if _, err := e.Eval(bad, nil); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

// An emoji counts two in UTF-16, so a reply that fits in characters can
// still be too long for Telegram; utf16 keeps every piece within max.
func TestChunksUTF16(t *testing.T) {
	smile := "\U0001F600"
	text := strings.Repeat(smile+" ", 3000)
	for _, p := range ChunksIn(text, 4096, UTF16) {
		if n := len(utf16.Encode([]rune(p))); n > 4096 {
			t.Fatalf("a piece is %d UTF-16 units", n)
		}
	}
	if got := ChunksIn(smile+smile, 1, UTF16); !reflect.DeepEqual(got, []string{smile, smile}) {
		t.Fatalf("a two-unit character with max 1: %q", got)
	}
}

func TestLanguageInExpressions(t *testing.T) {
	for src, want := range map[string]string{
		"=language('Abeg wetin una get for shop?')": "pcm",
		"=language('ok')":       "en",
		"=language('ok', 'yo')": "yo",
		"=language('Please, what is the price of rice?', 'yo')": "en",
		"=language('Sannu, don Allah ina son taimako', 'en')":   "ha",
	} {
		got, err := e.Eval(src, nil)
		if err != nil || got != want {
			t.Errorf("%s = %v (%v), want %s", src, got, err, want)
		}
	}
	if _, err := e.Eval("=language(5)", nil); err == nil {
		t.Error("a number is not a text")
	}
}
