package storefront

import (
	"encoding/json"
	"math"
	"strings"
)

// A Food menu's options (docs/spec/fronts.md §6, F3): a dish asks a
// choice from each of its groups (a size, an extra), each choice with its
// price in naira over the dish's own. A basket line takes one choice per
// group, and the server prices it as the dish plus its choices from the
// copy, never from the form.

// ItemOption is one group of choices an item asks.
type ItemOption struct {
	Name    string         `json:"name"`
	Choices []OptionChoice `json:"choices"`
}

// OptionChoice is one choice, with what it adds to the price.
type OptionChoice struct {
	Name      string `json:"name"`
	PriceKobo int64  `json:"price_kobo"`
}

// LineOption is the choice a basket line took in one group.
type LineOption struct {
	Name      string `json:"name"`
	Choice    string `json:"choice"`
	PriceKobo int64  `json:"price_kobo"`
}

const (
	maxOptionGroups  = 5
	maxOptionChoices = 10
	maxOptionName    = 40
)

// ParseOptions reads an options cell: a list of groups, each a name and
// its choices, a choice a name and a price in naira (or a bare name, at no
// extra cost). What does not read is left out, so a typing slip in the
// table never hides the dish.
func ParseOptions(v any) []ItemOption {
	if s, ok := v.(string); ok {
		var x any
		if json.Unmarshal([]byte(s), &x) != nil {
			return nil
		}
		v = x
	}
	groups, _ := v.([]any)
	var out []ItemOption
	for _, g := range groups {
		m, _ := g.(map[string]any)
		name := optionName(m["name"])
		choices, _ := m["choices"].([]any)
		if name == "" || len(choices) == 0 {
			continue
		}
		o := ItemOption{Name: name}
		for _, c := range choices {
			var ch OptionChoice
			switch x := c.(type) {
			case string:
				ch.Name = optionName(x)
			case map[string]any:
				ch.Name = optionName(x["name"])
				if f, ok := num(x["price"]); ok && f >= 0 && f <= 10_000_000 {
					ch.PriceKobo = int64(math.Round(f * 100))
				}
			}
			if ch.Name != "" && len(o.Choices) < maxOptionChoices {
				o.Choices = append(o.Choices, ch)
			}
		}
		if len(o.Choices) > 0 && len(out) < maxOptionGroups {
			out = append(out, o)
		}
	}
	return out
}

func optionName(v any) string {
	s := str(v)
	if r := []rune(s); len(r) > maxOptionName {
		s = string(r[:maxOptionName])
	}
	if hasLink(s) {
		return ""
	}
	return s
}

// Choose takes a choice in each of an item's groups (by index; a group
// with none chosen takes its first) and is the line's options and what
// they add; ok is false for a choice the item does not have.
func (it Item) Choose(picked []int) ([]LineOption, int64, bool) {
	var out []LineOption
	var add int64
	for gi, g := range it.Options {
		i := 0
		if gi < len(picked) {
			i = picked[gi]
		}
		if i < 0 || i >= len(g.Choices) {
			return nil, 0, false
		}
		c := g.Choices[i]
		out = append(out, LineOption{Name: g.Name, Choice: c.Name, PriceKobo: c.PriceKobo})
		add += c.PriceKobo
	}
	return out, add, true
}

// rechoose prices a line's options again from the item as the table has
// it; ok is false when a group or a choice is gone.
func (it Item) rechoose(opts []LineOption) (int64, bool) {
	if len(opts) != len(it.Options) {
		return 0, false
	}
	var add int64
	for gi, o := range opts {
		g := it.Options[gi]
		found := false
		for _, c := range g.Choices {
			if g.Name == o.Name && c.Name == o.Choice {
				add += c.PriceKobo
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return add, true
}

// LineName is a line as the summary names it: "Jollof rice (Large,
// Plantain)".
func (l Line) LineName() string {
	if len(l.Options) == 0 {
		return l.Name
	}
	parts := make([]string, len(l.Options))
	for i, o := range l.Options {
		parts[i] = o.Choice
	}
	return l.Name + " (" + strings.Join(parts, ", ") + ")"
}
