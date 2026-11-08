package links

import (
	"strings"
	"testing"
)

func TestSpreadTotal(t *testing.T) {
	// ₦820,000 for 20 bags at ₦41,000, offered as ₦780,000.
	got, err := SpreadTotal([]int64{4100000}, []int64{20}, 78000000)
	if err != nil || got[0] != 3900000 {
		t.Fatalf("one line: %v %v", got, err)
	}
	// Two lines keep their proportions, within a naira a line.
	got, err = SpreadTotal([]int64{7800000, 4100000}, []int64{2, 10}, 50000000)
	if err != nil {
		t.Fatal(err)
	}
	total := got[0]*2 + got[1]*10
	if total > 50000000 || 50000000-total > 2*100 || got[0] <= got[1] {
		t.Fatalf("two lines: %v (%d)", got, total)
	}
	if _, err := SpreadTotal([]int64{1}, []int64{1}, 0); err == nil {
		t.Fatal("a total of nothing")
	}
}

func TestPriceAtAndBreaks(t *testing.T) {
	it := PriceItem{Name: "Rice", PriceKobo: 4100000, MinQuantity: 5, Breaks: []Break{{10, 3950000}, {50, 3800000}}}
	for q, want := range map[int64]int64{5: 4100000, 9: 4100000, 10: 3950000, 49: 3950000, 50: 3800000, 200: 3800000} {
		if p := PriceAt(it, q); p != want {
			t.Errorf("%d bags: %d, want %d", q, p, want)
		}
	}
	if err := checkBreaks(it); err != nil {
		t.Fatal(err)
	}
	// A link's own price stands whatever the quantity.
	own := it
	own.Overridden, own.PriceKobo = true, 4000000
	if PriceAt(own, 100) != 4000000 {
		t.Error("an override with breaks")
	}
	for _, bad := range [][]Break{{{5, 3900000}}, {{10, 4200000}}, {{10, 3900000}, {8, 3800000}}, {{10, 3900000}, {20, 3950000}}} {
		if checkBreaks(PriceItem{Name: "Rice", PriceKobo: 4100000, MinQuantity: 5, Breaks: bad}) == nil {
			t.Errorf("allowed: %v", bad)
		}
	}
}

func TestBelowFloor(t *testing.T) {
	lines := []OrderLine{
		{Item: "Rice, 25kg", ItemID: "r", Quantity: 20, PriceKobo: 3800000, ListKobo: 4100000},
		{Item: "Beans", ItemID: "b", Quantity: 10, PriceKobo: 2000000, ListKobo: 2000000},
	}
	if why := BelowFloor(lines, map[string]int64{"r": 3700000}, nil, DealSettings{}); len(why) != 0 {
		t.Fatalf("at or above the floor: %v", why)
	}
	why := BelowFloor(lines, map[string]int64{"r": 3900000}, nil, DealSettings{})
	if len(why) != 1 || !strings.Contains(why[0], "Rice, 25kg at ₦38,000 is under its lowest price, ₦39,000") {
		t.Fatalf("under the floor: %v", why)
	}
	// ₦960,000 against ₦1,020,000 is about 5.9% off.
	five, ten := 5, 10
	if why := BelowFloor(lines, nil, nil, DealSettings{MaxDiscountPct: &five}); len(why) != 1 || !strings.Contains(why[0], "more than 5% off") {
		t.Fatalf("past the largest discount: %v", why)
	}
	if why := BelowFloor(lines, nil, nil, DealSettings{MaxDiscountPct: &ten}); len(why) != 0 {
		t.Fatalf("within the largest discount: %v", why)
	}
	// A size: its own floor, else its product's.
	sized := []OrderLine{{Item: "Air Max 90 – 42, Black", ItemID: "s42", Quantity: 6, PriceKobo: 3000000}}
	if why := BelowFloor(sized, map[string]int64{"am90": 3500000}, map[string]string{"s42": "am90"}, DealSettings{}); len(why) != 1 {
		t.Fatalf("under the product's floor: %v", why)
	}
	if why := BelowFloor(sized, map[string]int64{"am90": 3500000, "s42": 2500000}, map[string]string{"s42": "am90"}, DealSettings{}); len(why) != 0 {
		t.Fatalf("the size's own floor stands: %v", why)
	}
}

// A product listed with its sizes: each size an item of its own, at the
// product's price unless it has its own (then without the product's
// breaks); a link's own price for the product is every size's.
func TestFlatSizes(t *testing.T) {
	product := PriceItem{ItemID: "am90", Name: "Air Max 90", Unit: "pair", PriceKobo: 4000000, MinQuantity: 1, Available: true,
		Breaks: []Break{{MinQuantity: 50, PriceKobo: 3600000}},
		Sizes: []PriceSize{{ItemID: "s42", Name: "Air Max 90 – 42", Variant: map[string]string{"Size": "42"}, Available: true},
			{ItemID: "s46", Name: "Air Max 90 – 46", Variant: map[string]string{"Size": "46"}, PriceKobo: 4500000, Available: true},
			{ItemID: "s47", Name: "Air Max 90 – 47", Variant: map[string]string{"Size": "47"}}}}
	flat := Flat([]PriceItem{product, {ItemID: "rice", Name: "Rice", PriceKobo: 100}})
	if _, ok := flat["am90"]; ok || flat["rice"].Name != "Rice" {
		t.Fatalf("the product itself is not orderable; a plain item is: %v", flat)
	}
	s42, s46 := flat["s42"], flat["s46"]
	if s42.PriceKobo != 4000000 || PriceAt(s42, 50) != 3600000 || ProductOf(s42) != "am90" || s42.Unit != "pair" {
		t.Fatalf("a size at the product's price and breaks: %+v", s42)
	}
	if s46.PriceKobo != 4500000 || PriceAt(s46, 50) != 4500000 {
		t.Fatalf("a size's own price, no breaks: %+v", s46)
	}
	if flat["s47"].Available {
		t.Fatal("a size off sale")
	}
	product.Overridden, product.PriceKobo = true, 3900000
	if f := Flat([]PriceItem{product}); f["s46"].PriceKobo != 3900000 || !f["s46"].Overridden {
		t.Fatalf("a link's own price is each size's: %+v", f["s46"])
	}
}
