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
	if why := BelowFloor(lines, map[string]int64{"r": 3700000}, DealSettings{}); len(why) != 0 {
		t.Fatalf("at or above the floor: %v", why)
	}
	why := BelowFloor(lines, map[string]int64{"r": 3900000}, DealSettings{})
	if len(why) != 1 || !strings.Contains(why[0], "Rice, 25kg at ₦38,000 is under its lowest price, ₦39,000") {
		t.Fatalf("under the floor: %v", why)
	}
	// ₦960,000 against ₦1,020,000 is about 5.9% off.
	five, ten := 5, 10
	if why := BelowFloor(lines, nil, DealSettings{MaxDiscountPct: &five}); len(why) != 1 || !strings.Contains(why[0], "more than 5% off") {
		t.Fatalf("past the largest discount: %v", why)
	}
	if why := BelowFloor(lines, nil, DealSettings{MaxDiscountPct: &ten}); len(why) != 0 {
		t.Fatalf("within the largest discount: %v", why)
	}
}
