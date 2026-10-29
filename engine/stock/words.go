package stock

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stock's sentences in chat (docs/spec/stock.md §4), read by a parser and
// never by the model: "add 50 cartons of Indomie at 8,500", "how many bags
// of rice do I have?", "adjust rice -2 spoilt". The words here are
// English; other languages' come with their word lists.

// Units are the units read without "of" ("add 20 bags rice"); any word
// before "of" is a unit too.
var Units = []string{"bag", "carton", "crate", "piece", "pack", "packet", "tin", "bottle", "box", "sachet", "roll", "dozen", "kg", "kilo",
	"litre", "tuber", "basket", "paint", "mudu", "derica", "keg", "jerrycan", "set", "pair", "ream", "bundle", "tray", "can", "jar",
	"tube", "bucket", "sack", "unit", "plate", "wrap", "loaf", "bar", "cup", "gallon"}

// Singular is a unit word for one: bags, bag; boxes, box; pieces, piece.
func Singular(u string) string {
	l := strings.ToLower(u)
	switch {
	case len(l) > 3 && strings.HasSuffix(l, "ies"):
		return u[:len(u)-3] + "y"
	case strings.HasSuffix(l, "xes"), strings.HasSuffix(l, "ches"), strings.HasSuffix(l, "shes"), strings.HasSuffix(l, "sses"):
		return u[:len(u)-2]
	case len(l) > 2 && strings.HasSuffix(l, "s") && !strings.HasSuffix(l, "ss"):
		return u[:len(u)-1]
	}
	return u
}

func isUnit(w string) bool {
	s := strings.ToLower(Singular(w))
	for _, u := range Units {
		if s == u {
			return true
		}
	}
	return false
}

// Added is "add 50 cartons of Indomie at 8,500" read: how many, the unit
// (singular, "" when none was said), the item, and the price paid for one.
type Added struct {
	Quantity int64
	Unit     string
	Name     string
	CostKobo *int64
}

var (
	addRe   = regexp.MustCompile(`(?i)^\s*add\s+(\d[\d,]*)\s+(.+?)\s*$`)
	costRe  = regexp.MustCompile(`(?i)^(.+?)\s+(?:at|@|for)\s+(.+?)(?:\s+(?:each|per\s+\w+|a\s+\w+))?$`)
	countRe = regexp.MustCompile(`^\d[\d,]*$`)
)

// ParseAdd reads an add sentence; ok is false when it is not one.
func ParseAdd(text string) (Added, bool) {
	m := addRe.FindStringSubmatch(text)
	if m == nil {
		return Added{}, false
	}
	n, ok := wholeNumber(m[1])
	if !ok || n <= 0 || n > MaxDelta {
		return Added{}, false
	}
	a := Added{Quantity: n}
	rest := strings.TrimSpace(m[2])
	if c := costRe.FindStringSubmatch(rest); c != nil {
		if kobo, ok := storefront.ParsePrice(c[2]); ok {
			rest, a.CostKobo = strings.TrimSpace(c[1]), &kobo
		}
	}
	a.Unit, a.Name = unitAndName(rest)
	a.Name = strings.Trim(a.Name, " -:,.")
	if a.Name == "" || len([]rune(a.Name)) > 120 {
		return Added{}, false
	}
	return a, true
}

// unitAndName splits "cartons of Indomie" or "bags rice" into the unit
// (singular) and the item; "Indomie" alone has no unit.
func unitAndName(s string) (unit, name string) {
	f := strings.Fields(s)
	if len(f) >= 3 && strings.EqualFold(f[1], "of") && len([]rune(f[0])) <= 30 {
		return Singular(f[0]), strings.Join(f[2:], " ")
	}
	if len(f) >= 2 && isUnit(f[0]) {
		return Singular(f[0]), strings.Join(f[1:], " ")
	}
	return "", s
}

var howManyTail = regexp.MustCompile(`(?i)\s+(?:do\s+(?:i|we)\s+(?:have|get)(?:\s+left)?|have\s+(?:i|we)\s+got|are\s+(?:left|remaining)|is\s+left|left|remain(?:ing)?|in\s+stock|dey)$`)

// ParseHowMany reads "how many bags of rice do I have" (or "how many
// rice left") to the item named; ok is false when it is not one.
func ParseHowMany(text string) (string, bool) {
	s := strings.Trim(strings.TrimSpace(text), " ?!.")
	rest, ok := cutPrefixFold(s, "how many ")
	if !ok {
		if rest, ok = cutPrefixFold(s, "how much "); !ok {
			return "", false
		}
	}
	for {
		t := howManyTail.ReplaceAllString(rest, "")
		if t == rest {
			break
		}
		rest = t
	}
	_, name := unitAndName(strings.TrimSpace(rest))
	name = strings.TrimSpace(name)
	return name, name != ""
}

// Adjusted is "adjust rice -2 spoilt" read.
type Adjusted struct {
	Name   string
	Change int64
	Note   string
}

// ParseAdjust reads the words after "adjust": the item, a change (signed;
// unsigned is added) and why.
func ParseAdjust(args string) (Adjusted, bool) {
	f := strings.Fields(args)
	for i := 1; i < len(f); i++ {
		w := f[i]
		sign := int64(1)
		switch {
		case strings.HasPrefix(w, "-"), strings.HasPrefix(w, "−"):
			sign, w = -1, strings.TrimLeft(w, "-−")
		case strings.HasPrefix(w, "+"):
			w = strings.TrimPrefix(w, "+")
		}
		n, ok := wholeNumber(w)
		if !ok || n == 0 || n > MaxDelta {
			continue
		}
		a := Adjusted{Name: strings.Join(f[:i], " "), Change: sign * n, Note: strings.Join(f[i+1:], " ")}
		if i+1 < len(f) && isUnit(f[i+1]) {
			a.Note = strings.Join(f[i+2:], " ")
		}
		a.Note = strings.Trim(a.Note, " -:,.")
		if len([]rune(a.Note)) > 200 {
			return Adjusted{}, false
		}
		return a, true
	}
	return Adjusted{}, false
}

func wholeNumber(s string) (int64, bool) {
	if !countRe.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n, err == nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// Counted is one item a count names: "rice 12".
type Counted struct {
	Name  string
	Count int64
}

// ParseCount reads what follows "count": items and how many are on the
// shelf ("rice 12, indomie 40", "12 bags rice and 40 indomie"), then a
// reason after a colon ("count rice 12: rats"). ok is false when the words
// are not that.
func ParseCount(text string) (list []Counted, reason string, ok bool) {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, ":"); i >= 0 {
		text, reason = strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:])
	}
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	var split []string
	for _, p := range parts {
		split = append(split, strings.Split(p, " and ")...)
	}
	for _, p := range split {
		f := strings.Fields(p)
		if len(f) < 2 {
			return nil, "", false
		}
		var name string
		var n int64
		if v, isNum := wholeNumber(f[len(f)-1]); isNum {
			n, name = v, strings.Join(f[:len(f)-1], " ")
		} else if v, isNum := wholeNumber(f[0]); isNum {
			_, rest := unitAndName(strings.Join(f[1:], " "))
			n, name = v, rest
		} else {
			return nil, "", false
		}
		name = strings.Trim(name, " -.")
		if name == "" || n < 0 || n > MaxDelta {
			return nil, "", false
		}
		list = append(list, Counted{Name: name, Count: n})
	}
	return list, cut(reason, 200), len(list) > 0
}
