package saved

import (
	"strings"
	"unicode"
)

// photoDigits are the letters a phone's text recognition reads in place
// of digits in a printed or handwritten number.
var photoDigits = map[rune]rune{'O': '0', 'o': '0', 'D': '0', 'Q': '0', 'I': '1', 'l': '1', 'i': '1', '|': '1', '!': '1',
	'Z': '2', 'z': '2', 'S': '5', 's': '5', 'B': '8', 'b': '6', 'G': '6', 'g': '9', 'q': '9', 'T': '7'}

// PhotoText is text read from a photo on the phone, made fit for the
// readers: in a word that is mostly digits (three or more, the rest
// letters read in place of digits, at most a third of it), each such
// letter becomes its digit ("O123456789" is 0123456789, "2O3456789l" is
// 2034567891). A word joined with '-' is judged a part at a time, and a
// part of letters alone is a word ("GTB-2034567891" keeps its bank, "ID-
// 12345" its ID). Words, names and amounts with their units ("45k",
// "500g", "5000s") are left as they are.
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

// photoWord is a word with its look-alike letters as digits, in each part
// of it (between '-') that is mostly digits; else the word itself.
// Punctuation at its ends is kept.
func photoWord(w string) string {
	core := strings.TrimRight(strings.TrimLeft(w, "([:"), ").,;:")
	if core == "" {
		return w
	}
	parts := strings.Split(core, "-")
	for i, p := range parts {
		parts[i] = photoPart(p)
	}
	if out := strings.Join(parts, "-"); out != core {
		return strings.Replace(w, core, out, 1)
	}
	return w
}

// photoNumberLen is the length from which a word is taken for a number
// whatever its last letter: an account number (10 digits) or a phone
// number (11).
const photoNumberLen = 10

// photoPart is one part of a word with its look-alike letters as digits,
// when it is mostly digits; else the part itself. A part of letters alone
// is never changed, nor one look-alike letter at the end of a shorter
// part straight after a digit: a unit or a plural ("500g", "5000s").
func photoPart(p string) string {
	rs := []rune(p)
	if len(rs) == 0 {
		return p
	}
	letters := true
	for _, r := range rs {
		if !unicode.IsLetter(r) {
			letters = false
			break
		}
	}
	if letters {
		return p
	}
	unit := ""
	if n := len(rs); n >= 2 && n < photoNumberLen && photoDigits[rs[n-1]] != 0 && rs[n-2] >= '0' && rs[n-2] <= '9' {
		unit, rs = string(rs[n-1]), rs[:n-1]
	}
	digits, alike := 0, 0
	for _, r := range rs {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case photoDigits[r] != 0:
			alike++
		default:
			return p
		}
	}
	if digits < 3 || alike == 0 || alike*3 > digits+alike {
		return p
	}
	for i, r := range rs {
		if d := photoDigits[r]; d != 0 {
			rs[i] = d
		}
	}
	return string(rs) + unit
}
