package assistant

import (
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The words a customer controls the assistant with, in the five
// languages (decision 0062 P3, D13). A message is one of them when, folded
// (lower case, no tone marks) and without punctuation, it is exactly one
// of the phrases: "stop" is, "please don't stop the delivery" is not.
// Drafts until native speakers review them, like the messages
// (docs/languages.md).
var (
	stopWords = []string{
		"stop", "unsubscribe", "stop messages", "stop all", "opt out", "optout", "stop promotions",
		"stop am", "make una stop", // Pidgin
		// Yoruba's and Hausa's everyday words for stop also mean wait or
		// stand ("duro", "tsaya"), so only phrases that cannot be a
		// customer asking to wait, until native speakers review them.
		"e ma fi ranse mo",      // Yoruba: don't send any more
		"kada ku kara turo min", // Hausa: don't send me any more
		"kwusi", "kwusi ya",     // Igbo
	}
	startWords = []string{
		"start", "unstop", "resume", "start again", "opt in",
		"start am",       // Pidgin
		"bere", "e bere", // Yoruba
		"fara", "ci gaba", // Hausa
		"malite", "bido", // Igbo
	}
	personWords = []string{
		"agent", "person", "human", "a person", "a human", "real person", "someone", "staff",
		"customer care", "customer service", "support", "speak to a person", "talk to a person",
		"speak to someone", "talk to someone", "i want to talk to a person", "i want to speak to a person",
		"i want to talk to someone", "speak to an agent", "talk to an agent", "human agent",
		"i wan talk to person", "abeg person", "make person answer me", "i wan see person", // Pidgin
		"eniyan", "mo fe ba eniyan soro", "e je ki n ba eniyan soro", // Yoruba
		"mutum", "ina son magana da mutum", "a bani mutum", // Hausa
		"mmadu", "achoro m ikwu okwu na mmadu", "mmadu ka m choro", // Igbo
	}
)

// Word is what a message asks of the assistant, if it is one of its words.
type Word string

// Words.
const (
	WordNone   Word = ""
	WordStop   Word = "stop"
	WordStart  Word = "start"
	WordPerson Word = "person"
)

// WordOf reads a message as one of the words, or none.
func WordOf(text string) Word {
	f := foldWords(text)
	if f == "" {
		return WordNone
	}
	for _, l := range []struct {
		w    Word
		list []string
	}{{WordStop, stopWords}, {WordStart, startWords}, {WordPerson, personWords}} {
		for _, p := range l.list {
			if f == p {
				return l.w
			}
		}
	}
	return WordNone
}

// foldWords is text folded for matching: lower case, tone marks and
// punctuation gone, spaces collapsed, a leading slash (Telegram's /stop)
// dropped.
func foldWords(s string) string {
	s = lang.Fold(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "/")
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
