package storefront

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/variants"
)

// A product with sizes and colours: its stock is theirs together (on sale
// only), and the public page lists the product, not them.
func TestVariantsRollUp(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	items := []Item{
		{ID: "1", Name: "Air Max 90", Available: true, Stock: n(0)},
		{ID: "2", Name: "Air Max 90 – 42, Black", Available: true, Stock: n(3), VariantOf: "1"},
		{ID: "3", Name: "Air Max 90 – 43, Black", Available: true, Stock: n(2), VariantOf: "1"},
		{ID: "4", Name: "Air Max 90 – 40, Black", Available: false, Stock: n(9), VariantOf: "1"}, // retired
		{ID: "5", Name: "Slides", Available: true, Stock: n(4)},
	}
	rollUpVariants(items)
	if items[0].Variants != 2 || items[0].Stock == nil || *items[0].Stock != 5 {
		t.Fatalf("the product: %+v", items[0])
	}
	if items[4].Variants != 0 || *items[4].Stock != 4 {
		t.Fatalf("a product without: %+v", items[4])
	}
	pub := PublicItems(items)
	if len(pub) != 2 || pub[0].ID != "1" || pub[1].ID != "5" {
		t.Fatalf("public: %+v", pub)
	}
}

// A product is sold out only when every size on sale keeps a count and has
// none left; one size that keeps no count makes its stock unknown.
func TestVariantsRollUpUncounted(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	items := []Item{
		{ID: "1", Name: "Tee", Available: true},
		{ID: "2", Name: "Tee – S", Available: true, Stock: n(0), VariantOf: "1"},
		{ID: "3", Name: "Tee – M", Available: true, VariantOf: "1"}, // keeps no count
		{ID: "4", Name: "Cap", Available: true},
		{ID: "5", Name: "Cap – S", Available: true, Stock: n(0), VariantOf: "4"},
		{ID: "6", Name: "Cap – M", Available: true, Stock: n(0), VariantOf: "4"},
		{ID: "7", Name: "Cap – L", Available: false, VariantOf: "4"}, // off sale
	}
	rollUpVariants(items)
	if items[0].Stock != nil || items[0].SoldOut() || items[0].Variants != 2 {
		t.Fatalf("a product with an uncounted size: %+v", items[0])
	}
	if items[3].Stock == nil || *items[3].Stock != 0 || !items[3].SoldOut() {
		t.Fatalf("a product with every size at none left: %+v", items[3])
	}
}

// A product set up in sizes and colours with none of them on sale is no
// plain item to put in a bag: it is not on sale either. A product with no
// choices stays as it is.
func TestVariantsRollUpNoneOnSale(t *testing.T) {
	cs := []variants.Choice{{Name: "Colour", Values: []string{"Red"}}}
	items := []Item{
		{ID: "1", Name: "Apron", Available: true, Choices: cs},
		{ID: "2", Name: "Apron – Red", Available: false, VariantOf: "1", Variant: map[string]string{"Colour": "Red"}},
		{ID: "3", Name: "Slides", Available: true},
	}
	rollUpVariants(items)
	if items[0].Available || items[0].Choices != nil {
		t.Fatalf("the product with none on sale: %+v", items[0])
	}
	if !items[2].Available {
		t.Fatalf("a plain item: %+v", items[2])
	}
}
