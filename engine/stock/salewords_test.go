package stock_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
)

func TestParseSale(t *testing.T) {
	type item struct {
		q          int64
		unit, name string
	}
	for _, c := range []struct {
		in     string
		items  []item
		amount int64 // kobo; -1 none
		how    string
	}{
		{"3 bags of rice 45,000 cash", []item{{3, "bag", "rice"}}, 4500000, "cash"},
		{"2 cartons indomie, 1 bag rice 60,000 transfer", []item{{2, "carton", "indomie"}, {1, "bag", "rice"}}, 6000000, "transfer"},
		{"indomie 500", []item{{1, "", "indomie"}}, 50000, ""},
		{"2 indomie and 3 bread for 2k on pos", []item{{2, "", "indomie"}, {3, "", "bread"}}, 200000, "pos"},
		{"5 sachets of pure water", []item{{5, "sachet", "pure water"}}, -1, ""},
		{"2x malt 1200 by transfer", []item{{2, "", "malt"}}, 120000, "transfer"},
		{"1 crate eggs N4500 owo", []item{{1, "crate", "eggs"}}, 450000, "cash"},
	} {
		got, ok := stock.ParseSale(c.in)
		if !ok || len(got.Items) != len(c.items) || got.PaidHow != c.how {
			t.Errorf("%q: %+v %v", c.in, got, ok)
			continue
		}
		for i, it := range c.items {
			if g := got.Items[i]; g.Quantity != it.q || g.Unit != it.unit || g.Name != it.name {
				t.Errorf("%q item %d: %+v", c.in, i, g)
			}
		}
		if (c.amount < 0) != (got.AmountKobo == nil) || got.AmountKobo != nil && *got.AmountKobo != c.amount {
			t.Errorf("%q: amount %v", c.in, got.AmountKobo)
		}
	}
	for _, in := range []string{"", "cash", "45,000 cash"} {
		if got, ok := stock.ParseSale(in); ok {
			t.Errorf("%q read as %+v", in, got)
		}
	}
}
