// Package lang localises what Taskiem itself says to people over WhatsApp,
// USSD and SMS (spec 11.6, decision 0029): message catalogues keyed by id,
// English as the source, and the words people use for each command, per
// language.
//
// English is always on. Nigerian Pidgin (pcm), Yoruba (yo), Hausa (ha) and
// Igbo (ig) ship as drafts marked unreviewed: an operator turns a language
// on (TASKIEM_LANGUAGES, or per tenant), and a native speaker reviews each
// entry before it is called reviewed (docs/languages.md). A message missing
// from a catalogue falls back to English and is reported.
//
// The package holds text only: no database, no network, no model. The API
// decides which language a number gets; engine/ai/intent asks the model
// when the word lists do not match.
package lang

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Tag is a language code: ISO 639-1 where one exists, ISO 639-3 for
// Nigerian Pidgin.
type Tag string

// The languages Taskiem has catalogues for.
const (
	EN  Tag = "en"
	PCM Tag = "pcm"
	YO  Tag = "yo"
	HA  Tag = "ha"
	IG  Tag = "ig"
)

// Info describes a language.
type Info struct {
	Tag     Tag
	English string // the language's name in English
	Native  string // its name in itself
	// Speech is the code a speech-to-text provider takes as a hint (ISO
	// 639-1); "" lets the provider detect it (Pidgin has no code).
	Speech string
	// Names are the words people may use to ask for it ("language
	// yoruba", "language yo").
	Names []string
}

var all = []Info{
	{EN, "English", "English", "en", []string{"en", "english", "oyinbo", "turanci", "bekee"}},
	{PCM, "Nigerian Pidgin", "Naija", "", []string{"pcm", "pidgin", "naija", "nigerian pidgin", "broken", "broken english"}},
	{YO, "Yoruba", "Yorùbá", "yo", []string{"yo", "yoruba", "yorùbá", "ede yoruba"}},
	{HA, "Hausa", "Hausa", "ha", []string{"ha", "hausa", "harshen hausa"}},
	{IG, "Igbo", "Igbo", "ig", []string{"ig", "igbo", "asusu igbo"}},
}

// All lists the languages with catalogues, English first.
func All() []Info { return slices.Clone(all) }

// Lookup finds a language by its tag.
func Lookup(t Tag) (Info, bool) {
	for _, i := range all {
		if i.Tag == t {
			return i, true
		}
	}
	return Info{}, false
}

// Find finds a language by what a person typed: a code, its English or
// its own name, with or without accents.
func Find(name string) (Info, bool) {
	n := Fold(name)
	if n == "" {
		return Info{}, false
	}
	for _, i := range all {
		for _, x := range i.Names {
			if Fold(x) == n {
				return i, true
			}
		}
		if Fold(i.English) == n || Fold(i.Native) == n {
			return i, true
		}
	}
	return Info{}, false
}

// Valid reports whether t has a catalogue.
func Valid(t Tag) bool {
	_, ok := Lookup(t)
	return ok
}

// ParseList reads a comma-separated list of tags (TASKIEM_LANGUAGES):
// "pcm,yo". English is always on and need not be listed; "" means English
// only.
func ParseList(s string) ([]Tag, error) {
	out := []Tag{EN}
	for f := range strings.SplitSeq(s, ",") {
		t := Tag(strings.ToLower(strings.TrimSpace(f)))
		if t == "" {
			continue
		}
		if !Valid(t) {
			return nil, fmt.Errorf("unknown language %q (en, pcm, yo, ha, ig)", t)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Fold lowercases s, drops accents and tone marks, and collapses spaces
// and punctuation at the ends: how messages are compared with the word
// lists. "Ẹ jọ̀wọ́!" folds to "e jowo".
func Fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if a, ok := plain[r]; ok {
			b.WriteString(a)
			continue
		}
		b.WriteRune(r)
	}
	return strings.Trim(strings.Join(strings.Fields(b.String()), " "), " ?!.,;:")
}

// plain spells the letters that do not decompose in ASCII.
var plain = map[rune]string{
	'ɓ': "b", 'ɗ': "d", 'ƙ': "k", 'ƴ': "y", 'ŋ': "n", 'Ɓ': "b", 'Ɗ': "d", 'Ƙ': "k", 'Ƴ': "y",
	'’': "'", '‘': "'", 'ʼ': "'", '“': "\"", '”': "\"", '…': "...", '–': "-", '—': "-", '₦': "N", '•': "-",
}

// ASCII is s as USSD screens and SMS carry it (the GSM 7-bit alphabet's
// ASCII part): accents and tone marks dropped, the hooked letters spelled
// plainly, anything else non-ASCII removed. Case is kept.
func ASCII(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r < 0x80:
			b.WriteRune(r)
		default:
			if a, ok := plain[r]; ok {
				if unicode.IsUpper(r) {
					a = strings.ToUpper(a)
				}
				b.WriteString(a)
			}
		}
	}
	return b.String()
}
