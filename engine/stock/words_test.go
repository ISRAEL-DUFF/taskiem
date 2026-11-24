package stock_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
)

// g-uses-ask: a line into a stock item counted in another unit asks how
// many of it one is; the same unit, a count of pieces or a product's own
// uses ask nothing. lib/treat.test.ts and treat_line_test.dart hold the
// same cases.
func TestAskUses(t *testing.T) {
	for _, c := range []struct {
		line, stock string
		uses        int64
		ask         bool
	}{
		{"bag", "g", 1, true},
		{"", "g", 1, true},
		{"bags", "kg", 0, true},
		{"yard", "yards", 1, false},
		{"Bags", "bag", 1, false},
		{"bag", "", 1, false},
		{"bag", "pcs", 1, false},
		{"carton", "piece", 1, false},
		{"bag", "g", 1000, false},
	} {
		if got := stock.AskUses(c.line, c.stock, c.uses); got != c.ask {
			t.Errorf("%q into %q (uses %d): asks %v", c.line, c.stock, c.uses, got)
		}
	}
}

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
	for in, want := range map[string]string{"bag": "bags", "box": "boxes", "battery": "batteries", "glass": "glasses", "day": "days", "g": "g", "kg": "kg", "ml": "ml", "L": "L"} {
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

func TestParseCount(t *testing.T) {
	for _, c := range []struct {
		in     string
		want   []stock.Counted
		reason string
	}{
		{"rice 12, indomie 40", []stock.Counted{{"rice", 12}, {"indomie", 40}}, ""},
		{"12 bags of rice and 40 indomie: rats in the store", []stock.Counted{{"rice", 12}, {"indomie", 40}}, "rats in the store"},
		{"Indomie Chicken 0", []stock.Counted{{"Indomie Chicken", 0}}, ""},
	} {
		got, reason, ok := stock.ParseCount(c.in)
		if !ok || reason != c.reason || len(got) != len(c.want) {
			t.Errorf("%q: %+v %q %v", c.in, got, reason, ok)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: %+v", c.in, got)
			}
		}
	}
	for _, bad := range []string{"", "rice", "rice twelve", "rice -2"} {
		if got, _, ok := stock.ParseCount(bad); ok {
			t.Errorf("%q: %+v", bad, got)
		}
	}
}

// Counting in by words (what-a-business-buys §4.5): a quantity first,
// after "add" or "stock in", is counting something in; "add product",
// "add item … <price>", "add rider" and the rest are other commands'.
func TestParseCountIn(t *testing.T) {
	for _, c := range []struct {
		in         string
		ok         bool
		qty        int64
		unit, name string
	}{
		{"add 20 yards of ankara fabric", true, 20, "yard", "ankara fabric"},
		{"stock in 20 yards of ankara fabric", true, 20, "yard", "ankara fabric"},
		{"Stock in 5 cartons of Indomie at 8,500", true, 5, "carton", "Indomie"},
		{"add 3 rolls thread", true, 3, "roll", "thread"},
		{"add product Jollof rice 2500", false, 0, "", ""},
		{"add item Chin chin 500", false, 0, "", ""},
		{"add dish Jollof rice 2500", false, 0, "", ""},
		{"add rider Musa 0803 123 4567 Ikeja", false, 0, "", ""},
		{"add supplier Dangote Depot 0803 123 4567", false, 0, "", ""},
		{"add service Haircut 30 2000", false, 0, "", ""},
		{"stock rice", false, 0, "", ""},
		{"stock in", false, 0, "", ""},
		{"received 20 yards of ankara fabric", false, 0, "", ""},
		// Bought in a unit is stock counted in, not an expense; bought
		// with no unit is the money words'.
		{"we bought 10 bags of rice at 1,500", true, 10, "bag", "rice"},
		{"bought 5 cartons of Indomie", true, 5, "carton", "Indomie"},
		{"I bought 2 crates of eggs from Mama Tunde at 3,200", true, 2, "crate", "eggs"},
		{"We bought 4 bags rice from Dangote", true, 4, "bag", "rice"},
		{"bought snacks for 8,000", false, 0, "", ""},
		{"bought 5 snacks", false, 0, "", ""},
		{"bought 15,000 worth of fuel", false, 0, "", ""},
		{"bought", false, 0, "", ""},
		{"buy 10 bags of rice", false, 0, "", ""},
	} {
		a, ok := stock.ParseCountIn(c.in)
		if ok != c.ok || ok && (a.Quantity != c.qty || a.Unit != c.unit || a.Name != c.name) {
			t.Errorf("%q: %+v %v", c.in, a, ok)
		}
	}
	// The cost of one survives who it was bought from.
	if a, ok := stock.ParseCountIn("I bought 2 crates of eggs from Mama Tunde at 3,200"); !ok || a.CostKobo == nil || *a.CostKobo != 320000 {
		t.Errorf("cost: %+v %v", a, ok)
	}
}
