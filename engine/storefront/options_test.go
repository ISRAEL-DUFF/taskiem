package storefront

import (
	"reflect"
	"testing"
)

// A Food dish's options (fronts F3): read from the menu's options cell
// (prices in naira, a bare name free, what does not read left out), a
// choice per group priced over the dish, and the order priced again from
// the table: a choice's new price is taken, a choice gone is gone.
func TestFoodOptions(t *testing.T) {
	opts := ParseOptions(`[{"name": "Size", "choices": [{"name": "Regular", "price": 0}, {"name": "Large", "price": 500}]},
		{"name": "Extra", "choices": ["None", {"name": "Plantain", "price": 300}]}, {"name": "", "choices": ["x"]}, {"name": "Empty", "choices": []}]`)
	want := []ItemOption{{Name: "Size", Choices: []OptionChoice{{"Regular", 0}, {"Large", 50000}}},
		{Name: "Extra", Choices: []OptionChoice{{"None", 0}, {"Plantain", 30000}}}}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("parsed: %+v", opts)
	}
	if ParseOptions("not json") != nil || ParseOptions(nil) != nil {
		t.Fatal("a cell that does not read")
	}
	price := int64(150000)
	rice := Item{ID: "1", Name: "Jollof rice", PriceKobo: &price, Available: true, Options: opts}
	lo, add, ok := rice.Choose([]int{1, 1})
	if !ok || add != 80000 || lo[0].Choice != "Large" || lo[1].Choice != "Plantain" {
		t.Fatalf("chosen: %+v %d %v", lo, add, ok)
	}
	if _, _, ok := rice.Choose([]int{2}); ok {
		t.Fatal("a choice the dish does not have")
	}
	if first, add, _ := rice.Choose(nil); add != 0 || first[0].Choice != "Regular" {
		t.Fatalf("none chosen takes the first: %+v", first)
	}
	line := Line{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 230000, LineKobo: 460000, Options: lo}
	if line.LineName() != "Jollof rice (Large, Plantain)" {
		t.Fatalf("named: %s", line.LineName())
	}
	sub := Submission{Kind: "order", Basket: []Line{line}, TotalKobo: 460000}
	// Large goes up to ₦700 at the table.
	dearer := rice
	dearer.Options = ParseOptions(`[{"name": "Size", "choices": ["Regular", {"name": "Large", "price": 700}]}, {"name": "Extra", "choices": ["None", {"name": "Plantain", "price": 300}]}]`)
	pc, changed := reprice(sub, map[string]Item{"1": dearer}, ActionOptions{})
	if !changed || pc.TotalKobo != 2*(150000+70000+30000) {
		t.Fatalf("repriced: %+v %v", pc, changed)
	}
	// Plantain is off the menu.
	gone := rice
	gone.Options = ParseOptions(`[{"name": "Size", "choices": ["Regular", {"name": "Large", "price": 500}]}, {"name": "Extra", "choices": ["None"]}]`)
	if pc, changed := reprice(sub, map[string]Item{"1": gone}, ActionOptions{}); !changed || len(pc.Gone) != 1 || pc.Gone[0] != "Jollof rice (Large, Plantain)" {
		t.Fatalf("a choice gone: %+v", pc)
	}
	if _, changed := reprice(sub, map[string]Item{"1": rice}, ActionOptions{}); changed {
		t.Fatal("nothing changed")
	}
	// An order with a deposit keeps one at the new prices: the action's
	// percentage, else the share it had (30% of 460000 is 138000).
	dep := sub
	dep.DepositKobo = 138000
	if pc, _ := reprice(dep, map[string]Item{"1": dearer}, ActionOptions{DepositPercent: 30}); pc.DepositKobo != (500000*30+99)/100 {
		t.Fatalf("deposit at the action's percentage: %+v", pc)
	}
	if pc, _ := reprice(dep, map[string]Item{"1": dearer}, ActionOptions{}); pc.DepositKobo != 150000 {
		t.Fatalf("deposit at the order's share: %+v", pc)
	}
	if pc, _ := reprice(sub, map[string]Item{"1": dearer}, ActionOptions{}); pc.DepositKobo != 0 {
		t.Fatalf("no deposit stays none: %+v", pc)
	}
}

// An item in a basket twice (two sizes) takes stock once, for both lines.
func TestPerItemTotalsLines(t *testing.T) {
	got := perItem([]Line{{ID: "1", Name: "Rice", Quantity: 1}, {ID: "2", Name: "Zobo", Quantity: 2}, {ID: "1", Name: "Rice", Quantity: 3}, {ID: "3", Quantity: 0}})
	if len(got) != 2 || got[0].ID != "1" || got[0].Quantity != 4 || got[1].ID != "2" || got[1].Quantity != 2 {
		t.Fatalf("perItem: %+v", got)
	}
}
