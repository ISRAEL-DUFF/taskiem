package storefront

import "testing"

// The price check on a package (forms-that-sell §5.2): its lines are
// priced as the package from the published form, never per item from the
// table, so a bundle cheaper than its items stays its price; a package
// the form no longer offers, or an item gone, is gone; a new package
// price is a price change, shared over the lines.
func TestRepricePackages(t *testing.T) {
	price := int64(2500000)
	item := Item{ID: "7", Name: "Ear cleaner", PriceKobo: &price, Available: true}
	live := map[string]Item{"7": item}
	pf := PackagesField{Item: "7", Packages: []Package{{Qty: 1, PriceKobo: 2500000}, {Qty: 2, PriceKobo: 4500000}}}
	sub := Submission{Kind: "order", DeliveryKobo: 150000, TotalKobo: 4650000,
		Basket: []Line{{ID: "7", Name: "Ear cleaner", Quantity: 2, PriceKobo: 2250000, LineKobo: 4500000, PackageQty: 2, PackageKobo: 4500000}}}
	pc, changed := reprice(sub, live, ActionOptions{}, pf)
	if changed || pc.TotalKobo != 4650000 || pc.Basket[0].LineKobo != 4500000 {
		t.Fatalf("as published: %v %+v", changed, pc)
	}
	// The table's price per item moved: the package is still its price.
	dearer := int64(3000000)
	if _, changed := reprice(sub, map[string]Item{"7": {ID: "7", Name: "Ear cleaner", PriceKobo: &dearer, Available: true}}, ActionOptions{}, pf); changed {
		t.Fatal("a package repriced from the table")
	}
	// The owner published a new price for two.
	pf2 := PackagesField{Item: "7", Packages: []Package{{Qty: 2, PriceKobo: 4000000}}}
	if pc, changed := reprice(sub, live, ActionOptions{}, pf2); !changed || pc.TotalKobo != 4150000 || pc.Basket[0].PackageKobo != 4000000 {
		t.Fatalf("new package price: %v %+v", changed, pc)
	}
	// No longer offered, or the item gone: gone.
	if pc, changed := reprice(sub, live, ActionOptions{}, PackagesField{}); !changed || len(pc.Gone) != 1 || len(pc.Basket) != 0 {
		t.Fatalf("no such package: %+v", pc)
	}
	if pc, changed := reprice(sub, map[string]Item{}, ActionOptions{}, pf); !changed || len(pc.Gone) != 1 {
		t.Fatalf("item gone: %+v", pc)
	}
	// A size for each unit: lines per size, shared, adding up exactly.
	three := PackagesField{Item: "7", Packages: []Package{{Qty: 3, PriceKobo: 6500000}}}
	m := Item{ID: "7m", Name: "Shirt – M", PriceKobo: &price, Available: true}
	l := Item{ID: "7l", Name: "Shirt – L", PriceKobo: &price, Available: true}
	each := Submission{Kind: "order", TotalKobo: 6500000, Basket: []Line{
		{ID: "7m", Quantity: 2, LineKobo: 4333333, PackageQty: 3, PackageKobo: 6500000}, {ID: "7l", Quantity: 1, LineKobo: 2166667, PackageQty: 3, PackageKobo: 6500000}}}
	pc, changed = reprice(each, map[string]Item{"7m": m, "7l": l}, ActionOptions{}, three)
	if changed || pc.TotalKobo != 6500000 || pc.Basket[0].LineKobo+pc.Basket[1].LineKobo != 6500000 {
		t.Fatalf("each: %v %+v", changed, pc)
	}
}
