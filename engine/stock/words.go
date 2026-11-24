package stock

import (
	"math/big"
	"regexp"
	"slices"
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

// countUnits count things one by one: a stock item counted in one of them
// (or in none) takes one of it for each one bought.
var countUnits = map[string]bool{"": true, "piece": true, "pc": true, "unit": true, "item": true, "each": true, "one": true, "no": true}

// measureUnits measure rather than count: a line bought with no unit of
// its own, into a stock item counted in one of them, asks how much one is.
var measureUnits = map[string]bool{"g": true, "gram": true, "kg": true, "kilo": true, "kilogram": true, "mg": true, "ml": true, "cl": true, "l": true,
	"litre": true, "yard": true, "metre": true, "meter": true, "m": true, "cm": true, "mm": true, "inch": true, "foot": true, "feet": true,
	"ft": true, "lb": true, "oz": true}

// unitKey is a unit as AskUses compares it: lower case, for one.
func unitKey(u string) string { return strings.ToLower(Singular(strings.TrimSpace(u))) }

// AskUses reports whether a line bought in lineUnit ("bag", or "" when
// none was said), going into a stock item counted in stockUnit, asks how
// many of the stock item one of it is (what a business buys §6;
// build-plan-gaps H2, g-uses-ask): the stock item is counted in another
// unit (a bag into grams) and nothing says it yet (uses, a product's own,
// is 1). A line with no unit asks only of a stock item measured (grams,
// yards), not of one counted in bags or cartons. The web (lib/treat.ts
// askUses) and the app (treat_line.dart askUses) ask by the same rule.
func AskUses(lineUnit, stockUnit string, uses int64) bool {
	if uses > 1 {
		return false
	}
	s, l := unitKey(stockUnit), unitKey(lineUnit)
	if countUnits[s] || l == s {
		return false
	}
	return l != "" || measureUnits[s]
}

// convert is each measure's size in its kind's smallest unit (mg, ml,
// mm): "50 kg" into a stock item counted in g is 50,000.
var convert = map[string]struct {
	kind string
	size int64
}{
	"mg": {"mass", 1}, "g": {"mass", 1000}, "gram": {"mass", 1000}, "kg": {"mass", 1_000_000}, "kilo": {"mass", 1_000_000},
	"kilogram": {"mass", 1_000_000},
	"ml": {"volume", 1}, "cl": {"volume", 10}, "l": {"volume", 1000}, "litre": {"volume", 1000},
	"mm": {"length", 1}, "cm": {"length", 10}, "m": {"length", 1000}, "metre": {"length", 1000}, "meter": {"length", 1000},
}

var usesRe = regexp.MustCompile(`^\s*(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d+))?\s*([A-Za-z]+)?\s*\.?\s*$`)

// ParseUses reads how many of a stock item counted in stockUnit one of a
// line is, from an answer (g-uses-ask): a whole number ("50000",
// "50,000"), or a number followed by the stock unit itself or one that
// converts to it ("50 kg" into g is 50,000; "1.5 l" into ml is 1,500). A
// decimal is taken only when it makes whole stock units, and commas only
// as thousands ("2,5" is not a number). Anything else ("1 bag = 50,000g",
// "a lot") is not an answer: ok is false. The result is 1 to MaxDelta.
func ParseUses(text, stockUnit string) (int64, bool) {
	m := usesRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, ok := new(big.Rat).SetString(strings.ReplaceAll(m[1], ",", "") + "." + m[2] + "0")
	if !ok {
		return 0, false
	}
	if u := unitKey(m[3]); u != "" && u != unitKey(stockUnit) {
		from, okFrom := convert[u]
		to, okTo := convert[unitKey(stockUnit)]
		if !okFrom || !okTo || from.kind != to.kind {
			return 0, false
		}
		n.Mul(n, big.NewRat(from.size, to.size))
	}
	if !n.IsInt() || !n.Num().IsInt64() {
		return 0, false
	}
	v := n.Num().Int64()
	if v < 1 || v > MaxDelta {
		return 0, false
	}
	return v, true
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

// ParseCountIn reads words that count something in: "add 20 yards of
// ankara fabric", "add 50 cartons of Indomie at 8,500" (a cost, what one
// was bought for) and the same after "stock in". A quantity comes first, so
// "add product …", "add item …" and "add rider …" are never this. Goods
// bought in a unit are counted in too: "we bought 10 bags of rice at
// 1,500", "bought 5 cartons of Indomie" (ParseBought); "bought snacks for
// 8,000" is an expense, which the money words read. ok is false when the
// words are not one.
func ParseCountIn(text string) (Added, bool) {
	t := strings.TrimSpace(text)
	if m := boughtRe.FindStringSubmatch(t); m != nil {
		return ParseBought(m[1])
	}
	if rest, ok := cutPrefixFold(t, "stock in "); ok {
		t = "add " + rest
	}
	return ParseAdd(t)
}

var (
	boughtRe = regexp.MustCompile(`(?i)^\s*(?:(?:i|we)\s+)?bought\s+(.+?)\s*$`)
	// fromRe is who it was bought from, which counting in does not keep:
	// "rice from Dangote at 1,500".
	fromRe = regexp.MustCompile(`(?i)\s+from\s+.+?(\s+(?:at|@|for)\s+|$)`)
)

// notUnits are words before "of" that say money, not a unit: "15,000
// worth of fuel" is spending.
var notUnits = []string{"worth", "naira", "kobo"}

// ParseBought reads what follows a word for buying ("bought", "buy" and
// the other languages' words) when it is a quantity in a unit: "10 bags
// of rice at 1,500", "5 cartons of Indomie". That is stock, not an
// expense (what a business buys §5.4); ok is false for anything else
// ("snacks for 8,000", "5 snacks"), which the money words read.
func ParseBought(rest string) (Added, bool) {
	rest = fromRe.ReplaceAllString(strings.TrimSpace(rest), "$1")
	a, ok := ParseAdd("add " + rest)
	if !ok || a.Unit == "" || slices.Contains(notUnits, strings.ToLower(a.Unit)) {
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
