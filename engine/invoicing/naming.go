package invoicing

import (
	"strings"
	"unicode/utf8"
)

// DefaultNameLimit is the longest account name assumed when a rail
// documents none (docs/spec/schools.md 6.3: "often 31 characters"). None
// of the rails mapped so far documents its limit
// (docs/spec/schools-rail-facts.md, "Naming, side by side").
const DefaultNameLimit = 31

// AccountName is the name a payer should see when they transfer into a
// customer's permanent account: the business's name and the customer's,
// "GREENFIELD COURT ACADEMY / EBUBE EZE", in capitals, at most limit
// characters (runes). When it is too long the business's name shortens,
// word by word from its end ("GREENFIELD COURT / EBUBE EZE", then
// "GREENFIELD / EBUBE EZE"), then its one word is cut; the customer's name
// never shortens. Only when the customer's name alone (with the separator
// and one letter of the business's) does not fit is it cut, from its end,
// and flagged: the business should check what the rail shows. shortened
// says the business's name was shortened. A school's students' accounts
// are named the same way (engine/schools).
func AccountName(business, customer string, limit int) (name string, shortened, flagged bool) {
	if limit <= 0 {
		limit = DefaultNameLimit
	}
	words := strings.Fields(strings.ToUpper(business))
	who := strings.Join(strings.Fields(strings.ToUpper(customer)), " ")
	const sep = " / "
	if len(words) == 0 {
		if n := utf8.RuneCountInString(who); n > limit {
			return cutRunes(who, limit), false, true
		}
		return who, false, false
	}
	room := limit - utf8.RuneCountInString(sep+who)
	if room < 1 {
		// Not even one letter of the business's name fits: the customer's
		// name alone, cut to the limit.
		return cutRunes(who, limit), true, utf8.RuneCountInString(who) > limit
	}
	for k := len(words); k >= 1; k-- {
		s := strings.Join(words[:k], " ")
		if utf8.RuneCountInString(s) <= room {
			return s + sep + who, k < len(words), false
		}
	}
	return cutRunes(words[0], room) + sep + who, true, false
}

// cutRunes keeps the first n runes of s, without a space at its end.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return strings.TrimRight(string(r), " ")
}
