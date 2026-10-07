package templates

import (
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
const MinScore = 6

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "when": true, "then": true, "that": true, "this": true,
	"from": true, "into": true, "our": true, "their": true, "them": true, "they": true, "who": true, "what": true, "have": true, "has": true,
	"should": true, "want": true, "need": true, "please": true, "can": true, "you": true, "your": true, "are": true, "was": true, "will": true,
	"workflow": true, "automation": true, "automate": true, "set": true, "make": true, "also": true, "all": true, "any": true, "via": true,
	"using": true, "use": true, "get": true, "its": true, "just": true, "let": true, "know": true, "about": true, "some": true}

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

// Match ranks templates against a goal by keyword overlap: a keyword or
// tag counts most, then the title, then the summary and description.
// Ties keep id order. Only templates scoring at least MinScore, from at
// least two distinct words, are returned, at most n.
func (l *Library) Match(goal string, n int) []Match {
	goalWords := map[string]bool{}
	for _, w := range Words(goal) {
		goalWords[w] = true
	}
	var out []Match
	for _, t := range l.list {
		weights := map[string]int{}
		add := func(text string, w int) {
			for _, x := range Words(text) {
				weights[x] = max(weights[x], w)
			}
		}
		add(t.Description, 1)
		add(t.Summary, 1)
		add(t.Title, 2)
		add(strings.Join(t.Tags, " "), 3)
		add(strings.Join(t.Keywords, " "), 3)
		score := 0
		var matched []string
		for w := range goalWords {
			if v := weights[w]; v > 0 {
				score += v
				matched = append(matched, w)
			}
		}
		if score >= MinScore && len(matched) >= 2 {
			sort.Strings(matched)
			out = append(out, Match{Template: t, Score: score, Matched: matched})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
