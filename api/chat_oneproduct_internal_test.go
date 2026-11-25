package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// "sell <product> on a page" and "add a page for <product>" (forms that
// sell §7): the product's name and the packages typed after it, in every
// way they are written; another language's words reach it as "add a page
// for …".
func TestSellOnPageArgs(t *testing.T) {
	for _, c := range []struct {
		text, name string
		pkgs       []storefront.Package
		ok         bool
	}{
		{"sell Ear cleaner on a page", "Ear cleaner", nil, true},
		{"Sell ear cleaner on its own page.", "ear cleaner", nil, true},
		{"sell Ear cleaner on a page 1 for 25000, 2 for 45,000, 3 for 65k", "Ear cleaner", []storefront.Package{{Qty: 1, PriceKobo: 2500000}, {Qty: 2, PriceKobo: 4500000}, {Qty: 3, PriceKobo: 6500000}}, true},
		{"sell Ear cleaner on a page: 1 for ₦25,000 and 2 for ₦45,000", "Ear cleaner", []storefront.Package{{Qty: 1, PriceKobo: 2500000}, {Qty: 2, PriceKobo: 4500000}}, true},
		{"add a page for Ear cleaner", "Ear cleaner", nil, true},
		{"make a page for 2-in-1 Ear cleaner 1 for 25k, 2 for 45k", "2-in-1 Ear cleaner", []storefront.Package{{Qty: 1, PriceKobo: 2500000}, {Qty: 2, PriceKobo: 4500000}}, true},
		{"sell Ear cleaner on a page and more", "Ear cleaner", nil, false},
		{"sell rice 2", "", nil, false},
	} {
		name, pkgs, matched, ok := sellOnPageArgs(c.text)
		if c.name == "" {
			if matched {
				t.Errorf("%q: read as a page", c.text)
			}
			continue
		}
		if !matched || ok != c.ok || name != c.name || len(pkgs) != len(c.pkgs) {
			t.Errorf("%q: %q %v %v %v", c.text, name, pkgs, matched, ok)
			continue
		}
		for i := range pkgs {
			if pkgs[i] != c.pkgs[i] {
				t.Errorf("%q: package %d is %+v", c.text, i, pkgs[i])
			}
		}
	}
	for _, i := range []lang.Intent{lang.IntentSellOnPage} {
		if got := shopCommandOf(i, "Ear cleaner 1 for 25k"); got != "add a page for Ear cleaner 1 for 25k" {
			t.Errorf("%s: %q", i, got)
		}
		if got := shopCommandOf(i, " "); got != "" {
			t.Errorf("%s with nothing: %q", i, got)
		}
	}
}
