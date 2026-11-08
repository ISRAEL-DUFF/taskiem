package expr

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/israel-duff/taskiem/engine/lang"
)

// chunks(text, max) splits a text into pieces of at most max characters,
// for channels that refuse a long message (WhatsApp and Telegram take
// 4,096 characters, an SMS 160). It breaks after a sentence (. ! ? or a
// line break) where it can, else between words, and inside a word only
// when one word is longer than max. Pieces are trimmed; an empty text
// gives an empty list. A workflow sends each piece as its own step
// (foreach), so each send keeps its own key and retry (decision 0062 C20).
//
// chunks(text, max, "utf16") counts as Telegram and WhatsApp do: in UTF-16
// code units, where an emoji or another character outside the Basic
// Multilingual Plane counts two. chunks(text, max, "characters") is the
// two-argument form.
var chunksFunction = cel.Function("chunks",
	cel.Overload("chunks_string_int", []*cel.Type{cel.StringType, cel.IntType}, cel.ListType(cel.StringType),
		cel.BinaryBinding(func(t, m ref.Val) ref.Val { return chunksVal(t, m, types.String(Characters)) })),
	cel.Overload("chunks_string_int_string", []*cel.Type{cel.StringType, cel.IntType, cel.StringType}, cel.ListType(cel.StringType),
		cel.FunctionBinding(func(args ...ref.Val) ref.Val { return chunksVal(args[0], args[1], args[2]) })))

// The units chunks() counts in.
const (
	Characters = "characters"
	UTF16      = "utf16"
)

func chunksVal(t, m, u ref.Val) ref.Val {
	s, ok1 := t.(types.String)
	n, ok2 := m.(types.Int)
	unit, ok3 := u.(types.String)
	if !ok1 || !ok2 || !ok3 {
		return types.NewErr("chunks: takes a text, a number and optionally \"characters\" or \"utf16\"")
	}
	if unit != Characters && unit != UTF16 {
		return types.NewErr("chunks: the unit must be \"characters\" or \"utf16\"")
	}
	if n < 1 || n > 100_000 {
		return types.NewErr("chunks: the size must be between 1 and 100000")
	}
	if len(s) > 1<<20 {
		return types.NewErr("chunks: the text is longer than 1 MB")
	}
	out := ChunksIn(string(s), int(n), string(unit))
	vals := make([]ref.Val, len(out))
	for i, c := range out {
		vals[i] = types.String(c)
	}
	return types.NewRefValList(types.DefaultTypeAdapter, vals)
}

// Chunks is chunks() in Go: pieces of at most max characters (runes).
func Chunks(text string, max int) []string { return ChunksIn(text, max, Characters) }

// width is how much one character counts in the unit.
func width(r rune, unit string) int {
	if unit == UTF16 && r > 0xFFFF {
		return 2
	}
	return 1
}

// ChunksIn is chunks() with a unit: Characters (runes) or UTF16 (code
// units). A piece never splits a character.
func ChunksIn(text string, max int, unit string) []string {
	text = strings.TrimSpace(text)
	var out []string
	for text != "" {
		// The byte offset just after max units, at a character boundary;
		// at least one character, so a two-unit character with max 1
		// still moves on.
		end, used := 0, 0
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			w := width(r, unit)
			if used+w > max && end > 0 {
				break
			}
			used += w
			end += size
		}
		if end == len(text) {
			out = append(out, text)
			break
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

// language(text) guesses which of Taskiem's languages a customer wrote in
// (decision 0062 V4, A9): en, pcm, yo, ha or ig, from the words typical of
// each and the letters only some use (lang.Detect, as Taskiem's own chat
// does). It says en unless another language clearly leads.
// language(text, otherwise) gives otherwise instead when it cannot tell:
// the language the conversation was in, so one short "ok" does not switch
// a Yoruba conversation to English.
var languageFunction = cel.Function("language",
	cel.Overload("language_string", []*cel.Type{cel.StringType}, cel.StringType,
		cel.UnaryBinding(func(t ref.Val) ref.Val {
			s, ok := t.(types.String)
			if !ok {
				return types.NewErr("language: takes a text")
			}
			tag, _ := DetectLanguage(string(s))
			return types.String(tag)
		})),
	cel.Overload("language_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.StringType,
		cel.BinaryBinding(func(t, o ref.Val) ref.Val {
			s, ok1 := t.(types.String)
			other, ok2 := o.(types.String)
			if !ok1 || !ok2 {
				return types.NewErr("language: takes a text and a language")
			}
			if tag, sure := DetectLanguage(string(s)); sure {
				return types.String(tag)
			}
			return other
		})))

// DetectLanguage is language() in Go: the language and whether it is
// more than a default (English is sure with three of its common words
// and nothing of the others).
func DetectLanguage(text string) (string, bool) {
	if len(text) > 1<<16 {
		text = text[:1<<16]
	}
	all := lang.All()
	tags := make([]lang.Tag, len(all))
	for i, l := range all {
		tags[i] = l.Tag
	}
	t, sure := lang.DetectAny(text, tags)
	return string(t), sure
}
