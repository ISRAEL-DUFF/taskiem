// Package variants is a product's sizes and colours
// (docs/spec/chat-categories-and-variants.md, Part B): its choices (Size,
// Colour, Material), each with its values, and the variant rows a grid of
// them makes. Each variant is a catalogue row of its own, pointing at its
// product's row, so everything keyed by a row id (stock, sales, orders)
// works for a variant unchanged; this package only decides which rows a
// grid makes, renames, brings back or retires. A retired variant is
// hidden, never deleted, so its history keeps its id.
package variants

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalid wraps every refusal of choices or a grid.
var ErrInvalid = errors.New("variants: invalid")

// Limits from the plan.
const (
	MaxChoices  = 3
	MaxValues   = 20
	MaxVariants = 100
	// MaxWord is the longest a choice's name or a value may be.
	MaxWord = 30
)

// Choice is one way a product varies, and its values in order.
type Choice struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Check tidies choices (spaces trimmed, empty values dropped) and refuses
// what the plan does not allow: more than MaxChoices, a choice with no
// name or no values, two of one name, a value twice, more than MaxValues.
func Check(cs []Choice) ([]Choice, error) {
	if len(cs) > MaxChoices {
		return nil, fmt.Errorf("%w: up to %d choices (size, colour and one more)", ErrInvalid, MaxChoices)
	}
	out := make([]Choice, 0, len(cs))
	seen := map[string]bool{}
	for _, c := range cs {
		name := tidy(c.Name)
		if name == "" || len([]rune(name)) > MaxWord {
			return nil, fmt.Errorf("%w: a choice has a name of 1 to %d characters", ErrInvalid, MaxWord)
		}
		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("%w: %s is there twice", ErrInvalid, name)
		}
		seen[strings.ToLower(name)] = true
		var vals []string
		have := map[string]bool{}
		for _, v := range c.Values {
			v = tidy(v)
			if v == "" {
				continue
			}
			if len([]rune(v)) > MaxWord {
				return nil, fmt.Errorf("%w: %s: a value is up to %d characters", ErrInvalid, name, MaxWord)
			}
			if have[strings.ToLower(v)] {
				return nil, fmt.Errorf("%w: %s: %s is there twice", ErrInvalid, name, v)
			}
			have[strings.ToLower(v)] = true
			vals = append(vals, v)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("%w: %s has no values", ErrInvalid, name)
		}
		if len(vals) > MaxValues {
			return nil, fmt.Errorf("%w: %s has more than %d values", ErrInvalid, name, MaxValues)
		}
		out = append(out, Choice{Name: name, Values: vals})
	}
	return out, nil
}

func tidy(s string) string { return strings.Join(strings.Fields(s), " ") }

// Label is a variant's values in its choices' order: "42, Black".
func Label(v map[string]string, cs []Choice) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		if x := v[c.Name]; x != "" {
			parts = append(parts, x)
		}
	}
	return strings.Join(parts, ", ")
}

// Name is a variant row's name, written out in full so that every place
// that shows only text (an invoice, a diary line, WhatsApp, an old order
// line) reads right: "Air Max 90 – 42, Black".
func Name(product string, v map[string]string, cs []Choice) string {
	return product + " – " + Label(v, cs)
}

// Key is a variant's choices as one string, the same whatever their
// case: "colour=black|size=42". Two cells of one key are one variant.
func Key(v map[string]string) string {
	parts := make([]string, 0, len(v))
	for k, x := range v {
		parts = append(parts, strings.ToLower(tidy(k))+"="+strings.ToLower(tidy(x)))
	}
	slices.Sort(parts)
	return strings.Join(parts, "|")
}

// Fit is a variant made to its choices: each choice's value as the
// choices spell it; false when a choice has none, or a value it lacks,
// or the variant names a choice the product does not have.
func Fit(v map[string]string, cs []Choice) (map[string]string, bool) {
	if len(v) != len(cs) {
		return nil, false
	}
	out := make(map[string]string, len(cs))
	for _, c := range cs {
		var got string
		for k, x := range v {
			if strings.EqualFold(tidy(k), c.Name) {
				got = tidy(x)
			}
		}
		i := slices.IndexFunc(c.Values, func(s string) bool { return strings.EqualFold(s, got) })
		if got == "" || i < 0 {
			return nil, false
		}
		out[c.Name] = c.Values[i]
	}
	return out, true
}

// ParseChoices reads a product's choices column; nil when it holds none.
func ParseChoices(s string) []Choice {
	var cs []Choice
	if strings.TrimSpace(s) == "" || json.Unmarshal([]byte(s), &cs) != nil {
		return nil
	}
	return cs
}

// ParseVariant reads a variant's column; nil when it holds none.
func ParseVariant(s string) map[string]string {
	var v map[string]string
	if strings.TrimSpace(s) == "" || json.Unmarshal([]byte(s), &v) != nil || len(v) == 0 {
		return nil
	}
	return v
}

// JSON is choices or a variant as its column holds it.
func JSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Existing is a variant row a product has now.
type Existing struct {
	ID        string
	Variant   map[string]string
	Available bool
}

// Cell is one of a grid's: a variant sold (with its row, when it has one).
type Cell struct {
	// ID is the variant's row, when the grid shows one it has: changing
	// its values renames that row (a size "42" corrected to "43").
	ID      string            `json:"id,omitempty"`
	Variant map[string]string `json:"variant"`
	// Count is how many there are (a new variant's first goods in, an
	// existing one's count); nil leaves it.
	Count *int64 `json:"count,omitempty"`
	// PriceKobo is its own price; nil is the product's.
	PriceKobo *int64 `json:"price_kobo,omitempty"`
}

// Change is a variant row the grid keeps: its values and name as the grid
// has them, brought back when it had been retired.
type Change struct {
	ID      string
	Variant map[string]string
	Name    string
	Revive  bool
	Cell    Cell
}

// Plan is what a grid does to a product's rows.
type Plan struct {
	Create []Cell
	Keep   []Change
	Retire []string
}

// MakePlan works out a grid: cells of a row the product has keep (and,
// where their values changed, rename) it; a cell with no row whose values
// match a retired or kept row is that row; the rest are made; the rows a
// grid no longer has are retired. Every cell must name a value for each
// choice, once.
func MakePlan(product string, cs []Choice, cells []Cell, have []Existing) (Plan, error) {
	if len(cells) > MaxVariants {
		return Plan{}, fmt.Errorf("%w: up to %d sizes and colours in all", ErrInvalid, MaxVariants)
	}
	byID := map[string]Existing{}
	byKey := map[string]Existing{}
	for _, e := range have {
		byID[e.ID] = e
		if e.Variant != nil {
			byKey[Key(e.Variant)] = e
		}
	}
	var p Plan
	used := map[string]bool{}
	keys := map[string]bool{}
	for _, c := range cells {
		v, ok := Fit(c.Variant, cs)
		if !ok {
			return Plan{}, fmt.Errorf("%w: each one is a value of every choice", ErrInvalid)
		}
		k := Key(v)
		if keys[k] {
			return Plan{}, fmt.Errorf("%w: %s is there twice", ErrInvalid, Label(v, cs))
		}
		keys[k] = true
		c.Variant = v
		e, known := byID[c.ID]
		if c.ID != "" && !known {
			return Plan{}, fmt.Errorf("%w: %s is not one of this product's", ErrInvalid, c.ID)
		}
		if !known {
			e, known = byKey[k]
		}
		if known && !used[e.ID] {
			used[e.ID] = true
			p.Keep = append(p.Keep, Change{ID: e.ID, Variant: v, Name: Name(product, v, cs), Revive: !e.Available, Cell: c})
			continue
		}
		c.ID = ""
		p.Create = append(p.Create, c)
	}
	for _, e := range have {
		if !used[e.ID] && e.Available {
			p.Retire = append(p.Retire, e.ID)
		}
	}
	return p, nil
}
