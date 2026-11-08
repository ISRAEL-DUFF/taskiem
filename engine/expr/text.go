package expr

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// chunks(text, max) splits a text into pieces of at most max characters,
// for channels that refuse a long message (WhatsApp and Telegram take
// 4,096 characters, an SMS 160). It breaks after a sentence (. ! ? or a
// line break) where it can, else between words, and inside a word only
// when one word is longer than max. Pieces are trimmed; an empty text
// gives an empty list. A workflow sends each piece as its own step
// (foreach), so each send keeps its own key and retry (decision 0062 C20).
var chunksFunction = cel.Function("chunks", cel.Overload("chunks_string_int", []*cel.Type{cel.StringType, cel.IntType}, cel.ListType(cel.StringType),
	cel.BinaryBinding(func(t, m ref.Val) ref.Val {
		s, ok1 := t.(types.String)
		n, ok2 := m.(types.Int)
		if !ok1 || !ok2 {
			return types.NewErr("chunks: takes a text and a number")
		}
		if n < 1 || n > 100_000 {
			return types.NewErr("chunks: the size must be between 1 and 100000")
		}
		if len(s) > 1<<20 {
			return types.NewErr("chunks: the text is longer than 1 MB")
		}
		out := Chunks(string(s), int(n))
		vals := make([]ref.Val, len(out))
		for i, c := range out {
			vals[i] = types.String(c)
		}
		return types.NewRefValList(types.DefaultTypeAdapter, vals)
	})))

// Chunks is chunks() in Go: pieces of at most max characters (runes).
func Chunks(text string, max int) []string {
	text = strings.TrimSpace(text)
	var out []string
	for text != "" {
		if utf8.RuneCountInString(text) <= max {
			out = append(out, text)
			break
		}
		// The byte offset just after max runes.
		end := 0
		for i := 0; i < max; i++ {
			_, size := utf8.DecodeRuneInString(text[end:])
			end += size
		}
		cut := -1
		// The last sentence end in reach, then the last space.
		for i := end - 1; i > 0; i-- {
			c := text[i]
			if c == '\n' || ((c == '.' || c == '!' || c == '?') && i+1 < len(text) && unicode.IsSpace(rune(text[i+1]))) {
				cut = i + 1
				break
			}
		}
		if cut <= 0 {
			if i := strings.LastIndexFunc(text[:end], unicode.IsSpace); i > 0 {
				cut = i
			} else {
				cut = end
			}
		}
		piece := strings.TrimSpace(text[:cut])
		if piece != "" {
			out = append(out, piece)
		}
		text = strings.TrimSpace(text[cut:])
	}
	if out == nil {
		out = []string{}
	}
	return out
}
