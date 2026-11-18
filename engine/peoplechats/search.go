package peoplechats

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Search across a person's own people chats (people-chatting §6.2) runs on
// the server, but the words of each message are sealed, so the index
// (people_search, migration 00479) holds no words: only a keyed hash of
// each normalised word's beginnings (its first 2 to 8 letters), under a
// key derived from the person's own data key. A query's words are hashed
// the same way and matched; the messages found are then opened and
// checked against the words themselves, so a hash shared by chance never
// shows a message that does not match. Deleting the person's key with
// their account leaves the index unreadable as well as their messages.
//
// The honest limit: a keyed hash of a word is the same each time it is
// used, so someone holding the database and the person's key could test
// guesses; without the key, the index says only how many distinct word
// beginnings each message has.

const (
	// PrefixMin and PrefixMax are the word beginnings indexed: a query
	// word shorter than PrefixMin is not searched for; a longer one than
	// PrefixMax is found by its first PrefixMax letters, then checked.
	PrefixMin = 2
	PrefixMax = 8
	// TokenSize is the bytes kept of each hash.
	TokenSize = 16
	// MaxTokens is the most tokens one message is indexed under.
	MaxTokens = 600
)

// SearchKey is the key of a person's search index, derived from their own
// data key and bound to them.
func SearchKey(key []byte, owner uuid.UUID) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem-people-search:v1:" + owner.String()))
	return m.Sum(nil)
}

var fold = transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Normalise is a word as it is indexed and searched: lower case, without
// accents (Ọ̀ is o), letters and digits only.
func Normalise(word string) string {
	s, _, err := transform.String(fold, word)
	if err != nil {
		s = word
	}
	return strings.ToLower(s)
}

// span is one word of a text, where it is.
type span struct {
	word       string // normalised
	start, end int    // byte offsets in the text
}

// spans are the words of a text (runs of letters and digits), normalised,
// with where each is.
func spans(text string) []span {
	var out []span
	start := -1
	for i, r := range text {
		in := unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
		if in && start < 0 {
			start = i
		}
		if !in && start >= 0 {
			out = append(out, span{Normalise(text[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, span{Normalise(text[start:]), start, len(text)})
	}
	return out
}

// Words are a text's words, normalised.
func Words(text string) []string {
	var out []string
	for _, s := range spans(text) {
		if s.word != "" {
			out = append(out, s.word)
		}
	}
	return out
}

func token(skey []byte, prefix string) []byte {
	m := hmac.New(sha256.New, skey)
	m.Write([]byte("p:" + prefix))
	return m.Sum(nil)[:TokenSize]
}

func prefix(word string, n int) string {
	r := []rune(word)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// Tokens are what a message with these texts is indexed under: each
// word's beginnings of PrefixMin to PrefixMax letters, hashed, once each
// (at most MaxTokens).
func Tokens(skey []byte, texts ...string) [][]byte {
	seen := map[string]bool{}
	var out [][]byte
	for _, t := range texts {
		for _, w := range Words(t) {
			n := len([]rune(w))
			for l := PrefixMin; l <= PrefixMax && l <= n; l++ {
				p := prefix(w, l)
				if seen[p] {
					continue
				}
				seen[p] = true
				if len(out) >= MaxTokens {
					return out
				}
				out = append(out, token(skey, p))
			}
		}
	}
	return out
}

// Query is a search's words (normalised, those of PrefixMin letters or
// more, once each) and the tokens a message must be indexed under, all of
// them, to be a candidate.
func Query(skey []byte, q string) (words []string, tokens [][]byte) {
	seenW, seenT := map[string]bool{}, map[string]bool{}
	for _, w := range Words(q) {
		if len([]rune(w)) < PrefixMin || seenW[w] {
			continue
		}
		seenW[w] = true
		words = append(words, w)
		p := prefix(w, PrefixMax)
		if !seenT[p] {
			seenT[p] = true
			tokens = append(tokens, token(skey, p))
		}
	}
	return words, tokens
}

// Matches is whether every query word begins a word of one of the texts.
func Matches(words []string, texts ...string) bool {
	var have []string
	for _, t := range texts {
		have = append(have, Words(t)...)
	}
	for _, q := range words {
		found := false
		for _, w := range have {
			if strings.HasPrefix(w, q) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Snippet is a little of text around the first word a query word begins,
// at most max runes, with … where it was cut; "" when no word matches.
func Snippet(text string, words []string, max int) string {
	for _, s := range spans(text) {
		hit := false
		for _, q := range words {
			if strings.HasPrefix(s.word, q) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		r := []rune(text)
		at := len([]rune(text[:s.start]))
		from := at - max/3
		if from < 0 {
			from = 0
		}
		to := from + max
		if to > len(r) {
			to = len(r)
			if from = to - max; from < 0 {
				from = 0
			}
		}
		out := strings.TrimSpace(string(r[from:to]))
		if from > 0 {
			out = "…" + out
		}
		if to < len(r) {
			out += "…"
		}
		return out
	}
	return ""
}
