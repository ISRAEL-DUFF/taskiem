package api

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
)

func i64(n int64) *int64 { return &n }

// Order words group a product's sizes, and only those: items that share
// the start of their names stay apart.
func TestLinesWordsGroupsByProduct(t *testing.T) {
	parents := map[string]string{"a42": "am", "a43": "am"}
	got := linesWords([]links.OrderLine{
		{ItemID: "a42", Item: "Air Max 90 – 42, Black", Quantity: 20, Unit: "pair"},
		{ItemID: "rice", Item: "Rice", Quantity: 2, Unit: "bag"},
		{ItemID: "a43", Item: "Air Max 90 – 43, Black", Quantity: 16, Unit: "pair"},
		{ItemID: "i1", Item: "Indomie – Chicken", Quantity: 5, Unit: "carton"},
		{ItemID: "i2", Item: "Indomie – Onion", Quantity: 3, Unit: "carton"},
	}, parents)
	want := "36 pairs Air Max 90 in 2 sizes and colours, 2 bags Rice, 5 cartons Indomie – Chicken, 3 cartons Indomie – Onion"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := linesWords([]links.OrderLine{{ItemID: "a42", Item: "Air Max 90 – 42, Black", Quantity: 1, Unit: "pair"}}, parents); got != "1 pair Air Max 90 – 42, Black" {
		t.Fatalf("one size alone, by its full name: %q", got)
	}
}

// Adding the sizes an order brings keeps the buyer's sizes as they are:
// a size with its own price keeps it; one already there is not added
// again (nothing to change at all when it has them all).
func TestSizesWithKeepsOwnPrices(t *testing.T) {
	choices := []variants.Choice{{Name: "Size", Values: []string{"42", "43"}}}
	product := stock.Item{ID: "p", Name: "Air Max 90", PriceKobo: i64(5000000), Choices: choices,
		OwnPrice: []string{variants.Key(map[string]string{"Size": "42"})}}
	items := []stock.Item{product,
		{ID: "s42", VariantOf: "p", Variant: map[string]string{"Size": "42"}, Available: true, PriceKobo: i64(5500000)},
		{ID: "s43", VariantOf: "p", Variant: map[string]string{"Size": "43"}, Available: true, PriceKobo: i64(5000000)},
	}
	theirs := []variants.Choice{{Name: "size", Values: []string{"42", "43", "44"}}}
	if _, _, err := sizesWith(product, items, theirs, []map[string]string{{"size": "42"}, {"Size": "43"}}); !errors.Is(err, errNoSizesChange) {
		t.Fatalf("all there: %v", err)
	}
	cs, cells, err := sizesWith(product, items, theirs, []map[string]string{{"size": "44"}, {"size": "42"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || len(cs[0].Values) != 3 || cs[0].Values[2] != "44" {
		t.Fatalf("44 added to the choice: %v", cs)
	}
	var added, own, plain int
	for _, c := range cells {
		switch {
		case c.ID == "" && c.Variant["Size"] == "44" && c.Count != nil && *c.Count == 0:
			added++
		case c.ID == "s42" && c.PriceKobo != nil && *c.PriceKobo == 5500000:
			own++
		case c.ID == "s43" && c.PriceKobo == nil && c.Count == nil:
			plain++
		}
	}
	if added != 1 || own != 1 || plain != 1 || len(cells) != 3 {
		t.Fatalf("44 new at 0, 42 keeps its own price, 43 as it is: %+v", cells)
	}
}

// The briefing and the handover say what is low as the cards do: a
// product sold in sizes once.
func TestLowByProduct(t *testing.T) {
	low := func(n int64) *int64 { return &n }
	items := []stock.Item{
		{ID: "rice", Name: "Rice", Stock: low(1), Reorder: low(5)},
		{ID: "am", Name: "Air Max 90"},
		{ID: "a42", Name: "Air Max 90 – 42", VariantOf: "am", Stock: low(0), Reorder: low(2)},
		{ID: "a43", Name: "Air Max 90 – 43", VariantOf: "am", Stock: low(1), Reorder: low(2)},
		{ID: "rn", Name: "Runner"},
		{ID: "r40", Name: "Runner – 40", VariantOf: "rn", Stock: low(0), Reorder: low(1)},
		{ID: "r41", Name: "Runner – 41", VariantOf: "rn", Stock: low(9), Reorder: low(1)},
	}
	got := lowByProduct(items)
	want := []string{"Rice", "Air Max 90 (2 sizes and colours)", "Runner – 40"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Reordering low sizes reaches the supplier's minimum, their sizes
// together, and is priced at that total.
func TestLowSizesReorder(t *testing.T) {
	product := links.PriceItem{ItemID: "their", Name: "Air Max 90", PriceKobo: 4000000, MinQuantity: 6, Available: true,
		Breaks:  []links.Break{{MinQuantity: 10, PriceKobo: 3800000}},
		Choices: []variants.Choice{{Name: "Size", Values: []string{"42", "43"}}},
		Sizes: []links.PriceSize{
			{ItemID: "t42", Name: "Air Max 90 – 42", Variant: map[string]string{"Size": "42"}, Available: true},
			{ItemID: "t43", Name: "Air Max 90 – 43", Variant: map[string]string{"Size": "43"}, Available: true},
		}}
	n := func(v int64) *int64 { return &v }
	low := []stock.Item{
		{ID: "o42", VariantOf: "mine", Variant: map[string]string{"Size": "42"}, Stock: n(0), Reorder: n(1), Target: n(2)},
		{ID: "o47", VariantOf: "mine", Variant: map[string]string{"Size": "47"}, Stock: n(0), Reorder: n(1), Target: n(2)},
	}
	r := sizesReorder(links.SupplierPrices{Business: "Zuri"}, product, low)
	if r == nil || r.SellerItemID != "their" || r.Quantity != 6 || r.Sizes["t42"] != 6 || len(r.Sizes) != 1 || r.PriceKobo != 4000000 {
		t.Fatalf("42 topped up to the minimum, 47 (not sold) left out: %+v", r)
	}

	// The low list's drafts: the same top-up, and the break at the total.
	lines := []any{
		map[string]any{"item": "t42", "quantity": int64(4), "price_kobo": int64(4000000)},
		map[string]any{"item": "t43", "quantity": int64(1), "price_kobo": int64(4000000)},
	}
	topUpSizes([]links.PriceItem{product}, lines)
	first := lines[0].(map[string]any)
	if first["quantity"] != int64(5) || first["topped_up"] != int64(1) || first["price_kobo"] != int64(4000000) {
		t.Fatalf("topped up to 6 together: %v", lines)
	}
	lines[1].(map[string]any)["quantity"] = int64(5)
	topUpSizes([]links.PriceItem{product}, lines)
	if lines[1].(map[string]any)["price_kobo"] != int64(3800000) {
		t.Fatalf("10 together: the 10-pair price on each: %v", lines)
	}
}
