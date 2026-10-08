package lang

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// Intent is a command a person can give in a chat.
type Intent string

// The commands of the WhatsApp conversation (docs/whatsapp.md).
const (
	IntentNone      Intent = ""
	IntentHelp      Intent = "help"
	IntentStatus    Intent = "status"
	IntentApprovals Intent = "approvals"
	IntentCancel    Intent = "cancel"
	IntentYes       Intent = "yes"
	IntentNo        Intent = "no"
	IntentSwitch    Intent = "switch"
	IntentRun       Intent = "run"
	IntentBuild     Intent = "build"
	IntentLanguage  Intent = "language"
)

// Intents are every intent, in a fixed order.
var Intents = []Intent{IntentHelp, IntentStatus, IntentApprovals, IntentCancel, IntentYes, IntentNo, IntentSwitch, IntentRun, IntentBuild, IntentLanguage}

// Word lists live in intents/<tag>.json: whole messages that mean an
// intent (phrases), words that start one and are followed by what it is
// about (prefixes: "run payroll"), and words typical of the language that
// help recognise it (markers). Like the catalogues, every list but
// English's is a draft until a native speaker reviews it.

//go:embed intents/*.json
var intentFiles embed.FS

// Words is one language's word lists.
type Words struct {
	Language Tag                 `json:"language"`
	Reviewed bool                `json:"reviewed"`
	Status   string              `json:"status,omitempty"`
	Phrases  map[Intent][]string `json:"phrases"`
	Prefixes map[Intent][]string `json:"prefixes"`
	// Fillers are polite words a command may start with ("abeg", "biko"):
	// "abeg run payroll" is "run payroll".
	Fillers []string `json:"fillers"`
	Markers []string `json:"markers"`
}

// prefixIntents take what follows them; the others are whole messages.
var prefixIntents = []Intent{IntentSwitch, IntentRun, IntentBuild, IntentLanguage}

var (
	wordsOnce sync.Once
	words     map[Tag]*Words
	wordsErr  error
)

// LoadWords parses the embedded word lists.
func LoadWords() (map[Tag]*Words, error) {
	wordsOnce.Do(func() {
		words = map[Tag]*Words{}
		ents, err := intentFiles.ReadDir("intents")
		if err != nil {
			wordsErr = err
			return
		}
		for _, e := range ents {
			raw, err := intentFiles.ReadFile(path.Join("intents", e.Name()))
			if err != nil {
				wordsErr = err
				return
			}
			var w Words
			if err := json.Unmarshal(raw, &w); err != nil {
				wordsErr = fmt.Errorf("lang: intents/%s: %w", e.Name(), err)
				return
			}
			if string(w.Language)+".json" != e.Name() || !Valid(w.Language) {
				wordsErr = fmt.Errorf("lang: intents/%s declares language %q", e.Name(), w.Language)
				return
			}
			for i, ps := range w.Phrases {
				if !slices.Contains(Intents, i) {
					wordsErr = fmt.Errorf("lang: intents/%s: unknown intent %q", e.Name(), i)
					return
				}
				w.Phrases[i] = foldAll(ps)
			}
			for i, ps := range w.Prefixes {
				if !slices.Contains(prefixIntents, i) {
					wordsErr = fmt.Errorf("lang: intents/%s: %s takes phrases, not prefixes", e.Name(), i)
					return
				}
				w.Prefixes[i] = foldAll(ps)
			}
			w.Markers = foldAll(w.Markers)
			w.Fillers = foldAll(w.Fillers)
			words[w.Language] = &w
		}
		if words[EN] == nil {
			wordsErr = fmt.Errorf("lang: no English word lists")
		}
	})
	return words, wordsErr
}

func foldAll(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if f := Fold(p); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func mustWords() map[Tag]*Words {
	w, err := LoadWords()
	if err != nil {
		panic(err)
	}
	return w
}

// Match is what a message asks for.
type Match struct {
	Intent Intent
	// Arg is what follows a prefix, as typed ("Payroll run").
	Arg string
	// Language is the language whose list matched.
	Language Tag
}

// MatchIntent matches a message against the word lists of the given
// languages, in order (the person's language first, then English). Whole
// phrases win over prefixes, and longer prefixes over shorter ones.
func MatchIntent(text string, tags []Tag) (Match, bool) {
	ws := mustWords()
	f := Fold(text)
	if f == "" {
		return Match{}, false
	}
	if m, ok := matchOne(ws, f, text, tags); ok {
		return m, true
	}
	// Without a polite word in front ("abeg", "please", "biko").
	for _, t := range tags {
		if w := ws[t]; w != nil {
			for _, fl := range w.Fillers {
				if rest, ok := strings.CutPrefix(f, fl+" "); ok {
					if m, ok := matchOne(ws, rest, after(text, len(strings.Fields(fl))), tags); ok {
						return m, true
					}
				}
			}
		}
	}
	return Match{}, false
}

func matchOne(ws map[Tag]*Words, f, text string, tags []Tag) (Match, bool) {
	for _, t := range tags {
		w := ws[t]
		if w == nil {
			continue
		}
		for _, i := range Intents {
			if slices.Contains(w.Phrases[i], f) {
				return Match{Intent: i, Language: t}, true
			}
		}
	}
	type cand struct {
		intent Intent
		prefix string
		tag    Tag
	}
	var cands []cand
	for _, t := range tags {
		if w := ws[t]; w != nil {
			for _, i := range prefixIntents {
				for _, p := range w.Prefixes[i] {
					cands = append(cands, cand{i, p, t})
				}
			}
		}
	}
	// Longest first; ties keep the order of tags.
	sort.SliceStable(cands, func(a, b int) bool { return len(cands[a].prefix) > len(cands[b].prefix) })
	for _, c := range cands {
		if f == c.prefix || strings.HasPrefix(f, c.prefix+" ") {
			return Match{Intent: c.intent, Arg: after(text, len(strings.Fields(c.prefix))), Language: c.tag}, true
		}
	}
	return Match{}, false
}

// after drops the first n words of s.
func after(s string, n int) string {
	fs := strings.Fields(s)
	if n >= len(fs) {
		return ""
	}
	return strings.Trim(strings.Join(fs[n:], " "), " ?!.")
}

// Detect guesses which of the given languages a message is in, from the
// words typical of each and the letters only some of them use. It returns
// English unless one other language clearly leads: the person then gets
// that language until they choose one (docs/languages.md).
func Detect(text string, tags []Tag) (Tag, bool) {
	ws := mustWords()
	f := " " + Fold(text) + " "
	if strings.TrimSpace(f) == "" {
		return EN, false
	}
	score := map[Tag]int{}
	for _, t := range tags {
		w := ws[t]
		if t == EN || w == nil {
			continue
		}
		for _, m := range w.Markers {
			if strings.Contains(f, " "+m+" ") {
				score[t]++
			}
		}
		// A whole command in that language (and not in English).
		if m, ok := MatchIntent(text, []Tag{t}); ok {
			if _, en := MatchIntent(text, []Tag{EN}); !en && m.Arg == "" {
				score[t] += 2
			}
		}
	}
	nfc := norm.NFC.String(strings.ToLower(text))
	for t, letters := range letterHints {
		if slices.Contains(tags, t) && strings.ContainsAny(nfc, letters) {
			score[t]++
		}
	}
	best, top, second := EN, 0, 0
	for _, t := range tags {
		switch n := score[t]; {
		case n > top:
			best, second, top = t, top, n
		case n > second:
			second = n
		}
	}
	if top >= 2 && top > second {
		return best, true
	}
	return EN, false
}

// letterHints are letters (precomposed) that point to one language.
var letterHints = map[Tag]string{
	YO: "ẹṣ",
	IG: "ịụṅ",
	HA: "ɓɗƙƴ",
}
