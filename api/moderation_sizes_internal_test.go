package api

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/moderation"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

// A product's sizes and colours are checked with it: a choice's value
// goes through the word list, and a size's own price through the price
// check, both under the product.
func TestModerationChecksSizes(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	it := storefront.Item{ID: "10", Name: "Trainers", PriceKobo: n(4500000),
		Choices: []variants.Choice{{Name: "Size", Values: []string{"42"}}, {Name: "Colour", Values: []string{"Black", "Ponzi edition"}}},
		Sizes: []storefront.Size{
			{ID: "11", Variant: map[string]string{"Size": "42", "Colour": "Black"}},
			{ID: "12", Variant: map[string]string{"Size": "42", "Colour": "Ponzi edition"}, PriceKobo: n(0)},
		}}
	m := &moderation.Checker{}
	res, err := m.Texts(context.Background(), &memCache{}, itemTexts(it))
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != moderation.Blocked || res.Findings[0].Field != "item 10" {
		t.Errorf("a colour's words were not checked: %+v", res)
	}
	var free bool
	for _, f := range itemPrices(m, it) {
		if f.Verdict == moderation.Flagged && f.Field == "item 10" && f.Text == "Trainers – 42, Ponzi edition ₦0.00" {
			free = true
		}
	}
	if !free {
		t.Errorf("a size's own price was not checked: %+v", itemPrices(m, it))
	}
}
