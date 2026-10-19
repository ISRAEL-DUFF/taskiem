package telegram

import (
	"unicode"
	"unicode/utf16"
)

// Entity is a formatted range of a message, in UTF-16 code units as
// Telegram counts them (MessageEntity).
type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// Bold reads WhatsApp's *bold* markup, which the shared chat messages are
// written in, as Telegram formatting: the asterisks go and the words
// between them become a bold entity. Sending entities rather than a
// parse_mode leaves nothing in the text to escape: a value with < or & in
// it can never break a message. As on WhatsApp, a marker counts only at a
// word's edge (not inside 2*3*4), around text that neither starts nor ends
// with a space and holds no line break.
func Bold(text string) (string, []Entity) {
	rs := []rune(text)
	var out []rune
	var ents []Entity
	units := 0 // UTF-16 length of out
	edge := func(i int) bool {
		return i < 0 || i >= len(rs) || (!unicode.IsLetter(rs[i]) && !unicode.IsDigit(rs[i]) && rs[i] != '*')
	}
	for i := 0; i < len(rs); i++ {
		if rs[i] == '*' && edge(i-1) && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) && rs[i+1] != '*' {
			if j := closing(rs, i+1); j > 0 && edge(j+1) {
				inner := rs[i+1 : j]
				n := utf16Len(inner)
				ents = append(ents, Entity{Type: "bold", Offset: units, Length: n})
				out = append(out, inner...)
				units += n
				i = j
				continue
			}
		}
		out = append(out, rs[i])
		units += utf16.RuneLen(rs[i])
	}
	return string(out), ents
}

// closing is the index of the * closing a bold span opened before from,
// or -1: on the same line, after a character that is not a space.
func closing(rs []rune, from int) int {
	for j := from; j < len(rs); j++ {
		switch rs[j] {
		case '\n':
			return -1
		case '*':
			if unicode.IsSpace(rs[j-1]) {
				return -1
			}
			return j
		}
	}
	return -1
}

func utf16Len(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += utf16.RuneLen(r)
	}
	return n
}

// clipEntities keeps the entities inside the first n UTF-16 units.
func clipEntities(ents []Entity, n int) []Entity {
	var out []Entity
	for _, e := range ents {
		switch {
		case e.Offset >= n:
		case e.Offset+e.Length > n:
			e.Length = n - e.Offset
			out = append(out, e)
		default:
			out = append(out, e)
		}
	}
	return out
}
