package pii

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Masker swaps personal data for placeholders before a value leaves for
// an AI model, and swaps them back in the model's answer (decision 0050):
// the model sees "[[phone_1]]" where the run has a phone number, and the
// step's output carries the number again, sealed in history like any
// output that repeats personal data.
//
// It masks what the run already knows is personal (the taint: values
// opened from sealed envelopes) wherever it appears, whole or inside a
// longer text, and what the detectors recognise (structured fields by
// name and shape, and emails, phone numbers, BVNs, NINs and card numbers
// in free text). The same value always gets the same placeholder, so the
// model can tell two mentions of one person from two people.
type Masker struct {
	taint    Taint
	byKey    map[string]string // scalarKey -> placeholder
	values   map[string]any    // placeholder -> original value
	cats     map[string]string // placeholder -> category
	counts   map[string]int    // category -> placeholders so far
	restored Taint             // what Unmask put back, to be sealed
}

// NewMasker returns a masker for a run's known personal values. A known
// text that contains another known value (a sealed prompt, a greeting
// with a name in it) is not itself a value to hide: the values inside it
// are masked instead.
func NewMasker(taint Taint) *Masker {
	atoms := Taint{}
	for k, cat := range taint {
		if !strings.HasPrefix(k, "s:") {
			atoms[k] = cat
			continue
		}
		v, composite := k[2:], false
		for k2 := range taint {
			if v2 := k2[2:]; k2 != k && len(v2) >= MinRedact && len(v2) < len(v) && strings.Contains(v, v2) {
				composite = true
				break
			}
		}
		if !composite {
			atoms[k] = cat
		}
	}
	return &Masker{taint: atoms, byKey: map[string]string{}, values: map[string]any{}, cats: map[string]string{}, counts: map[string]int{}, restored: Taint{}}
}

// Restored is every value Unmask put back, by category: the personal
// values themselves, and each text a placeholder was put back into. The
// caller adds them to the run's taint so the output carrying them is
// sealed whole.
func (m *Masker) Restored() Taint { return m.restored }

// Masked reports how many personal values were swapped for placeholders.
func (m *Masker) Masked() int { return len(m.values) }

func (m *Masker) placeholder(v any, cat string) string {
	k, ok := scalarKey(v)
	if !ok {
		k = "s:" + strconv.Quote(strings.TrimSpace(toString(v)))
	}
	if p, ok := m.byKey[k]; ok {
		return p
	}
	if cat == "" {
		cat = "personal"
	}
	cat = strings.ReplaceAll(cat, " ", "_")
	m.counts[cat]++
	p := "[[" + cat + "_" + strconv.Itoa(m.counts[cat]) + "]]"
	m.byKey[k], m.values[p], m.cats[p] = p, v, cat
	return p
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	}
	return ""
}

// Mask returns a copy of v with its personal values swapped for
// placeholders.
func (m *Masker) Mask(v any) any {
	v = deepCopy(v)
	// Structured detection first: a field named phone holding a number.
	if r := walkDetect(v, "", "", nil, func(_, cat string, x any) any { return m.placeholder(x, cat) }); r != nil {
		v = r
	}
	return m.maskKnown(v)
}

func (m *Masker) maskKnown(v any) any {
	switch t := v.(type) {
	case map[string]any:
		// Keys too: an input built as {name: balance} carries the name in
		// its key, and the key reaches the model like any value.
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[m.maskString(k)] = m.maskKnown(x)
		}
		return out
	case []any:
		for i, x := range t {
			t[i] = m.maskKnown(x)
		}
		return t
	case string:
		return m.maskString(t)
	}
	if k, ok := scalarKey(v); ok {
		if cat, hit := m.taint[k]; hit {
			return m.placeholder(v, cat)
		}
	}
	return v
}

func (m *Masker) maskString(s string) string {
	if cat, ok := m.taint["s:"+s]; ok && s != "" && !isPlaceholder(s) {
		return m.placeholder(s, cat)
	}
	return m.MaskText(s)
}

var placeholderRe = regexp.MustCompile(`\[\[[a-z0-9_]+_[0-9]+\]\]`)

func isPlaceholder(s string) bool {
	return placeholderRe.MatchString(s) && placeholderRe.FindString(s) == s
}

// MaskText swaps the personal values in free text for placeholders: the
// run's known values (at least MinRedact long, whole matches only, longest
// first) and what the text patterns recognise.
func (m *Masker) MaskText(s string) string {
	if s == "" {
		return s
	}
	type hit struct{ val, cat string }
	var hits []hit
	for k, cat := range m.taint {
		v := k[2:]
		if len(v) >= MinRedact && strings.Contains(s, v) {
			hits = append(hits, hit{v, cat})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].val) != len(hits[j].val) {
			return len(hits[i].val) > len(hits[j].val)
		}
		return hits[i].val < hits[j].val
	})
	for _, h := range hits {
		s = strings.ReplaceAll(s, h.val, m.placeholder(h.val, h.cat))
	}
	s = textEmail.ReplaceAllStringFunc(s, func(x string) string { return m.placeholder(x, "email") })
	s = textPhone.ReplaceAllStringFunc(s, func(x string) string { return m.placeholder(x, "phone") })
	return textDigit.ReplaceAllStringFunc(s, func(x string) string {
		d := strings.NewReplacer(" ", "", "-", "").Replace(x)
		switch {
		case len(d) == 11 && strings.HasPrefix(d, "22"):
			return m.placeholder(x, "bvn")
		case len(d) == 11:
			return m.placeholder(x, "nin")
		case len(d) >= 13 && isCard(d):
			return m.placeholder(x, "card")
		}
		return x
	})
}

// Unmask returns a copy of v with every placeholder this masker made
// swapped back for its value: a string that is exactly a placeholder
// becomes the value itself (a number stays a number), and placeholders
// inside longer text become the value's text. Placeholders the masker did
// not make are left as they are.
func (m *Masker) Unmask(v any) any {
	if len(m.values) == 0 {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[m.unmaskText(k)] = m.Unmask(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = m.Unmask(x)
		}
		return out
	case string:
		if orig, ok := m.values[t]; ok {
			m.restored.Add(orig, m.cats[t])
			return orig
		}
		return m.unmaskText(t)
	}
	return v
}

// unmaskText puts the values back into a text (or an object key).
func (m *Masker) unmaskText(t string) string {
	cat := ""
	out := placeholderRe.ReplaceAllStringFunc(t, func(p string) string {
		if orig, ok := m.values[p]; ok {
			if cat == "" {
				cat = m.cats[p]
			}
			return toString(orig)
		}
		return p
	})
	if out != t {
		m.restored.Add(out, cat)
	}
	return out
}
