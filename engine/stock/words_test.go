package stock_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
)

func TestParseAdd(t *testing.T) {
	for _, c := range []struct {
		in         string
		qty        int64
		unit, name string
		cost       int64 // kobo; -1 none
	}{
		{"add 50 cartons of Indomie at 8,500", 50, "carton", "Indomie", 850000},
		{"Add 20 bags rice @ 45k", 20, "bag", "rice", 4500000},
		{"add 1,000 sachets of pure water", 1000, "sachet", "pure water", -1},
		{"add 6 Indomie Chicken", 6, "", "Indomie Chicken", -1},
		{"add 3 boxes of matches at ₦1,200 each", 3, "box", "matches", 120000},
		{"add 12 crates of eggs for 3500 per crate", 12, "crate", "eggs", 350000},
	} {
		a, ok := stock.ParseAdd(c.in)
		if !ok || a.Quantity != c.qty || a.Unit != c.unit || a.Name != c.name {
			t.Errorf("%q: %+v %v", c.in, a, ok)
			continue
		}
		if (c.cost < 0) != (a.CostKobo == nil) || a.CostKobo != nil && *a.CostKobo != c.cost {
			t.Errorf("%q: cost %v", c.in, a.CostKobo)
		}
	}
	for _, in := range []string{"add product Jollof rice 2000", "add rider Musa 0803", "add 0 bags of rice", "add to customers", "add 5"} {
		if a, ok := stock.ParseAdd(in); ok {
			t.Errorf("%q read as %+v", in, a)
		}
	}
}

func TestParseHowMany(t *testing.T) {
	for in, want := range map[string]string{
		"how many bags of rice do I have?": "rice",
		"How many Indomie left":            "Indomie",
		"how many cartons indomie remain":  "indomie",
		"how many garri dey":               "garri",
		"how much rice do we have left":    "rice",
	} {
		if got, ok := stock.ParseHowMany(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	if _, ok := stock.ParseHowMany("how are you"); ok {
		t.Error("not a question about stock")
	}
}

func TestParseAdjust(t *testing.T) {
	for in, want := range map[string]stock.Adjusted{
		"rice -2 spoilt":               {Name: "rice", Change: -2, Note: "spoilt"},
		"Indomie chicken +3 found":     {Name: "Indomie chicken", Change: 3, Note: "found"},
		"rice 4":                       {Name: "rice", Change: 4},
		"palm oil -1 tin leaked badly": {Name: "palm oil", Change: -1, Note: "leaked badly"},
	} {
		if got, ok := stock.ParseAdjust(in); !ok || got != want {
			t.Errorf("%q: %+v %v", in, got, ok)
		}
	}
	for _, in := range []string{"rice", "-2 spoilt", "rice -0"} {
		if got, ok := stock.ParseAdjust(in); ok {
			t.Errorf("%q read as %+v", in, got)
		}
	}
}

func TestUnits(t *testing.T) {
	for in, want := range map[string]string{"bags": "bag", "boxes": "box", "pieces": "piece", "glass": "glass", "batteries": "battery"} {
		if got := stock.Singular(in); got != want {
			t.Errorf("Singular(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"bag": "bags", "box": "boxes", "battery": "batteries", "glass": "glasses", "day": "days"} {
		if got := stock.Plural(in); got != want {
			t.Errorf("Plural(%q) = %q", in, got)
		}
	}
	it := stock.Item{Unit: "carton"}
	if it.Quantity(1) != "1 carton" || it.Quantity(48) != "48 cartons" || (stock.Item{}).Quantity(3) != "3" {
		t.Error(it.Quantity(48))
	}
	ten, five, two := int64(10), int64(5), int64(2)
	low := stock.Item{Stock: &two, Reorder: &five, Target: &ten}
	if !low.Low() || low.Suggest() != 8 {
		t.Errorf("suggest %d", low.Suggest())
	}
	low.Target = nil
	if low.Suggest() != 8 {
		t.Errorf("suggest without a target %d", low.Suggest())
	}
}
