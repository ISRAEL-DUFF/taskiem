package storefront

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The One product kind of front (docs/spec/forms-that-sell.md §5.4): the
// whole page of a pay-on-delivery seller made from one catalogue row. Its
// settings are the front's (storefront_fronts.options): the promise line
// at the top, up to six feature chips each with an icon from a fixed set,
// and whether the stock line ("Only 14 left", from the item's stock and
// never a made-up number) shows. The page is drawn in phase L2; here is
// what the settings are and how they are checked.

// OneProduct is the One product kind's name.
const OneProduct = "one-product"

// Limits on a One product front's settings.
const (
	MaxPromise  = 80
	MaxChips    = 6
	MaxChipText = 40
)

// ChipIcons are the icons a feature chip may have: a fixed set, so a page
// never carries a picture the owner uploaded as a badge (a fake seal, a
// bank's logo).
var ChipIcons = []string{"truck", "cash", "shield", "star", "clock", "check", "gift", "leaf", "phone", "sparkle", "heart", "return"}

// Chip is one feature chip: an icon and a short line.
type Chip struct {
	Icon string `json:"icon"`
	Text string `json:"text"`
}

// OneProductSettings are a One product front's settings.
type OneProductSettings struct {
	// Promise is the line at the top, in the storefront's accent ("Pay on
	// delivery nationwide"); empty shows none.
	Promise string `json:"promise,omitempty"`
	Chips   []Chip `json:"chips,omitempty"`
	// StockLine shows how many are left, where stock is kept.
	StockLine bool `json:"stock_line"`
}

// DefaultPromise is the promise line a new One product front has.
const DefaultPromise = "Pay on delivery nationwide"

// CleanOneProduct checks a One product front's settings: a promise of one
// line up to 80 characters with no links, up to six chips each an icon of
// the set and a line up to 40 characters with no links.
func CleanOneProduct(s OneProductSettings) (OneProductSettings, error) {
	s.Promise = strings.TrimSpace(s.Promise)
	if len([]rune(s.Promise)) > MaxPromise || hasLink(s.Promise) || strings.ContainsAny(s.Promise, "\n\r") {
		return s, fmt.Errorf("%w: the promise line is up to %d characters on one line, with no links", ErrInvalid, MaxPromise)
	}
	if len(s.Chips) > MaxChips {
		return s, fmt.Errorf("%w: up to %d feature chips", ErrInvalid, MaxChips)
	}
	out := make([]Chip, 0, len(s.Chips))
	for _, c := range s.Chips {
		c.Text = strings.TrimSpace(c.Text)
		if !slices.Contains(ChipIcons, c.Icon) {
			return s, fmt.Errorf("%w: a chip's icon is one of %s", ErrInvalid, strings.Join(ChipIcons, ", "))
		}
		if c.Text == "" || len([]rune(c.Text)) > MaxChipText || hasLink(c.Text) || strings.ContainsAny(c.Text, "\n\r") {
			return s, fmt.Errorf("%w: a chip's words are 1 to %d characters on one line, with no links", ErrInvalid, MaxChipText)
		}
		out = append(out, c)
	}
	s.Chips = out
	return s, nil
}

// CheckFrontSettings checks a front's settings for its kind: a One
// product front's are OneProductSettings, read strictly; other kinds
// take none.
func CheckFrontSettings(kind string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if kind != OneProduct {
		return nil, fmt.Errorf("%w: only a One product front has settings of its own", ErrInvalid)
	}
	var s OneProductSettings
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: a One product front's settings are its promise, its chips and its stock line", ErrInvalid)
	}
	s, err := CleanOneProduct(s)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
