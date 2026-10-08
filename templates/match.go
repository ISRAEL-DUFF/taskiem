package templates

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Match is a template and how well it fits a goal.
type Match struct {
	Template *Template
	Score    int
	// Matched are the goal's words the template answered to.
	Matched []string
}

// MinScore is the score below which a template is not offered as a
// starting point: the builder then drafts freely.
const MinScore = 12

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "when": true, "then": true, "that": true, "this": true,
	"from": true, "into": true, "our": true, "their": true, "them": true, "they": true, "who": true, "what": true, "have": true, "has": true,
	"should": true, "want": true, "need": true, "please": true, "can": true, "you": true, "your": true, "are": true, "was": true, "will": true,
	"workflow": true, "automation": true, "automate": true, "set": true, "make": true, "also": true, "all": true, "any": true, "via": true,
	"using": true, "use": true, "every": true, "each": true, "get": true, "its": true, "just": true, "let": true, "know": true, "about": true, "some": true,
	"time": true, "day": true, "days": true, "same": true, "new": true, "send": true, "sent": true, "after": true, "before": true, "check": true,
	"read": true, "add": true, "give": true, "number": true, "name": true, "list": true, "start": true, "enter": true, "both": true, "only": true,
	"once": true, "more": true, "less": true, "look": true, "out": true, "than": true, "over": true, "under": true}

// stem is a deliberately small normaliser: plural and verb endings, so
// "reminders" meets "reminder" and "owes" meets "owe".
func stem(w string) string {
	for _, suf := range []string{"ing", "ies", "ed", "es", "s"} {
		if len(w) > len(suf)+2 && strings.HasSuffix(w, suf) {
			if suf == "ies" {
				return strings.TrimSuffix(w, suf) + "y"
			}
			if suf == "es" && !strings.HasSuffix(w, "sses") && !strings.HasSuffix(w, "xes") && !strings.HasSuffix(w, "ches") {
				return strings.TrimSuffix(w, "s")
			}
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// Words splits text into the normalised keywords retrieval compares.
func Words(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []string
	for _, w := range f {
		if len(w) >= 3 && !stopWords[w] {
			out = append(out, stem(w))
		}
	}
	return out
}

// index is each template's keyword weights and how many templates use
// each word, computed once per library.
type index struct {
	weights []map[string]int
	df      map[string]int
}

func (l *Library) index() *index {
	l.idxOnce.Do(func() {
		ix := &index{df: map[string]int{}}
		for _, t := range l.list {
			w := map[string]int{}
			add := func(text string, v int) {
				for _, x := range Words(text) {
					w[x] = max(w[x], v)
				}
			}
			add(t.Description, 1)
			add(t.Summary, 1)
			add(t.Title, 2)
			add(strings.Join(t.Tags, " "), 3)
			add(strings.Join(t.Keywords, " "), 3)
			for x := range w {
				ix.df[x]++
			}
			ix.weights = append(ix.weights, w)
		}
		l.idx = ix
	})
	return l.idx
}

// Match ranks templates against a goal by keyword overlap: a keyword or
// tag counts most, then the title, then the summary and description,
// each scaled by how rare the word is across the library (a word every
// template shares says little). Ties keep id order. Only templates
// scoring at least MinScore, from at least two distinct words one of
// which is a distinctive keyword, are returned, at most n.
func (l *Library) Match(goal string, n int) []Match {
	ix := l.index()
	goalWords := map[string]bool{}
	for _, w := range Words(goal) {
		goalWords[w] = true
	}
	total := float64(len(l.list))
	var out []Match
	for i, t := range l.list {
		score := 0.0
		var matched []string
		// At least one word must be a keyword or tag few templates share:
		// common words alone (pay, email, customer) never pick a template.
		distinctive := false
		for w := range goalWords {
			if v := ix.weights[i][w]; v > 0 {
				score += float64(v) * math.Log(1+total/float64(ix.df[w]))
				matched = append(matched, w)
				if v == 3 && ix.df[w] <= 3 {
					distinctive = true
				}
			}
		}
		s := int(math.Round(score))
		if s >= MinScore && len(matched) >= 2 && distinctive {
			sort.Strings(matched)
			out = append(out, Match{Template: t, Score: s, Matched: matched})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
