package schools

import (
	"strings"
	"unicode/utf8"
)

// DefaultNameLimit is the longest account name assumed when a rail
// documents none (docs/spec/schools.md 6.3: "often 31 characters"). None
// of the rails mapped so far documents its limit
// (docs/spec/schools-rail-facts.md, "Naming, side by side").
const DefaultNameLimit = 31

// AccountName is the name a parent should see when they transfer into a
// student's account: the school's name and the child's, "GREENFIELD COURT
// ACADEMY / EBUBE EZE", in capitals, at most limit characters (runes).
// When it is too long the school's name shortens, word by word from its
// end ("GREENFIELD COURT / EBUBE EZE", then "GREENFIELD / EBUBE EZE"),
// then its one word is cut; the child's name never shortens. Only when
// the child's name alone (with the separator and one letter of the
// school's) does not fit is it cut, from its end, and flagged: the school
// should check what the rail shows. shortened says the school's name was
// shortened.
func AccountName(school, child string, limit int) (name string, shortened, flagged bool) {
	if limit <= 0 {
		limit = DefaultNameLimit
	}
	words := strings.Fields(strings.ToUpper(school))
	kid := strings.Join(strings.Fields(strings.ToUpper(child)), " ")
	const sep = " / "
	if len(words) == 0 {
		if n := utf8.RuneCountInString(kid); n > limit {
			return cut(kid, limit), false, true
		}
		return kid, false, false
	}
	room := limit - utf8.RuneCountInString(sep+kid)
	if room < 1 {
		// Not even one letter of the school's name fits: the child's name
		// alone, cut to the limit.
		return cut(kid, limit), true, utf8.RuneCountInString(kid) > limit
	}
	for k := len(words); k >= 1; k-- {
		s := strings.Join(words[:k], " ")
		if utf8.RuneCountInString(s) <= room {
			return s + sep + kid, k < len(words), false
		}
	}
	return cut(words[0], room) + sep + kid, true, false
}

// cut keeps the first n runes of s, without a space at its end.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return strings.TrimRight(string(r), " ")
}
