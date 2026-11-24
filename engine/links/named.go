package links

// Finding a linked business by what a person typed (docs/spec/direct-messages.md
// §3): the first words of its name, in order, so "@Dangote" and "Dangote
// Nigeria" both name Dangote Nigeria Ltd. Case and punctuation are
// ignored, and so is a legal suffix (Ltd, Plc, …); one word with the
// spaces left out ("DangoteNigeria") also fits. A whole name typed beats a
// name it only begins. Several businesses fitting is for the caller to
// ask about: none is picked in silence.

import (
	"regexp"
	"strings"
	"unicode"
)

// legalSuffixes are the words at the end of a name that are left out when
// comparing.
var legalSuffixes = map[string]bool{"ltd": true, "limited": true, "plc": true, "inc": true, "co": true, "company": true}

// nameWords is a name's words as compared: letters and digits (the rest
// separates words), a legal suffix at the end left out unless it is the
// only word.
func nameWords(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for len(f) > 1 && legalSuffixes[strings.ToLower(f[len(f)-1])] {
		f = f[:len(f)-1]
	}
	return f
}

// fits says how typed words name a business's words: not at all, as the
// start of its name, or as the whole of it.
func fits(typed, name []string) (ok, whole bool) {
	if len(typed) == 0 || len(name) == 0 {
		return false, false
	}
	if len(typed) <= len(name) {
		ok = true
		for i, w := range typed {
			if !strings.EqualFold(w, name[i]) {
				ok = false
				break
			}
		}
		if ok {
			return true, len(typed) == len(name)
		}
	}
	// One word with the spaces left out: the name's first words joined.
	if len(typed) == 1 {
		for m := 2; m <= len(name); m++ {
			if strings.EqualFold(typed[0], strings.Join(name[:m], "")) {
				return true, m == len(name)
			}
		}
	}
	return false, false
}

// named is the active links the typed words name: those whose whole name
// they are, if any, else those whose name they begin.
func named(active []Link, typed []string) []Link {
	var whole, start []Link
	for _, l := range active {
		if l.State != Active {
			continue
		}
		if ok, w := fits(typed, nameWords(l.Name)); w {
			whole = append(whole, l)
		} else if ok {
			start = append(start, l)
		}
	}
	if len(whole) > 0 {
		return whole
	}
	return start
}

// Named is the active links a typed name names (§3.1): its words as the
// start or the whole of their names. More than one is for the caller to
// ask about.
func Named(active []Link, name string) []Link {
	return named(active, nameWords(name))
}

// mentionWordRe is one word of a mention: a letter or digit, then letters,
// digits and the marks a name may carry inside a word.
var mentionWordRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}'&.-]*`)

// maxMentionWords bounds the words a mention is read over.
const maxMentionWords = 8

// Mentioned reads a mention: text is what follows an "@". It returns the
// active links named by the longest run of its words that names any, and
// how many runes of text that run takes (0, and no links, when none
// does). Words are separated by single spaces or tabs; anything else
// (a comma, a question mark, a new line) ends the run.
func Mentioned(active []Link, text string) ([]Link, int) {
	r := []rune(text)
	var ends []int
	at := 0
	for len(ends) < maxMentionWords {
		w := mentionWordRe.FindString(string(r[at:]))
		if w == "" {
			break
		}
		at += len([]rune(w))
		ends = append(ends, at)
		if at+1 >= len(r) || (r[at] != ' ' && r[at] != '\t') || r[at+1] == ' ' || r[at+1] == '\t' {
			break
		}
		at++
	}
	for k := len(ends); k > 0; k-- {
		run := string(r[:ends[k-1]])
		// A word ending in a full stop ("Ltd.") is read without it.
		if hit := named(active, nameWords(strings.TrimRight(run, "."))); len(hit) > 0 {
			return hit, ends[k-1]
		}
	}
	return nil, 0
}
