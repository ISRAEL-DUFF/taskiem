package saved

import "strings"

// photoDigits are the letters a phone's text recognition reads in place
// of digits in a printed or handwritten number.
var photoDigits = map[rune]rune{'O': '0', 'o': '0', 'D': '0', 'Q': '0', 'I': '1', 'l': '1', 'i': '1', '|': '1', '!': '1',
	'Z': '2', 'z': '2', 'S': '5', 's': '5', 'B': '8', 'b': '6', 'G': '6', 'g': '9', 'q': '9', 'T': '7'}

// PhotoText is text read from a photo on the phone, made fit for the
// readers: in a word that is mostly digits (three or more, the rest
// letters read in place of digits, at most a third of it), each such
// letter becomes its digit ("O123456789" is 0123456789, "2O3456789l" is
// 2034567891). Words, names and amounts with their units ("45k") are left
// as they are.
func PhotoText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		words := strings.Fields(line)
		changed := false
		for j, w := range words {
			if c := photoWord(w); c != w {
				words[j], changed = c, true
			}
		}
		if changed {
			lines[i] = strings.Join(words, " ")
		}
	}
	return strings.Join(lines, "\n")
}

// photoWord is a word with its look-alike letters as digits, when it is
// mostly digits; else the word itself. Punctuation at its ends is kept.
func photoWord(w string) string {
	core := strings.TrimRight(strings.TrimLeft(w, "([:"), ").,;:")
	if core == "" {
		return w
	}
	digits, alike := 0, 0
	for _, r := range core {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case photoDigits[r] != 0:
			alike++
		case r == '-':
		default:
			return w
		}
	}
	if digits < 3 || alike == 0 || alike*3 > digits+alike {
		return w
	}
	out := strings.Map(func(r rune) rune {
		if d := photoDigits[r]; d != 0 {
			return d
		}
		return r
	}, core)
	return strings.Replace(w, core, out, 1)
}
