package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// "sell <product> on a page" and "add a page for <product>" (forms that
// sell §7): the product's name and the packages typed after it, in every
// way they are written; every character after the name is a package or
// between two, else the bit not understood is named; another language's
// words reach it as "add a page for …".
func TestSellOnPageArgs(t *testing.T) {
	pk := func(qp ...int64) []storefront.Package {
		var out []storefront.Package
		for i := 0; i < len(qp); i += 2 {
			out = append(out, storefront.Package{Qty: int(qp[i]), PriceKobo: qp[i+1] * 100})
		}
		return out
	}
	catalogue := []string{"Ear cleaner", "2-in-1 Ear cleaner", "iPhone", "iPhone 13"}
	for _, c := range []struct {
		text, name string
		pkgs       []storefront.Package
		bad        string
	}{
		{"sell Ear cleaner on a page", "Ear cleaner", nil, ""},
		{"Sell ear cleaner on its own page.", "ear cleaner", nil, ""},
		{"sell Ear cleaner on a page of its own", "Ear cleaner", nil, ""},
		{"sell Ear cleaner on a page of its own: 1 for 25k", "Ear cleaner", pk(1, 25000), ""},
		{"sell Ear cleaner on a page 1 for 25000, 2 for 45,000, 3 for 65k", "Ear cleaner", pk(1, 25000, 2, 45000, 3, 65000), ""},
		{"sell Ear cleaner on a page: 1 for ₦25,000 and 2 for ₦45,000", "Ear cleaner", pk(1, 25000, 2, 45000), ""},
		// Thousands with a space, read whole.
		{"sell Ear cleaner on a page 1 for 25k, 2 for 45 000", "Ear cleaner", pk(1, 25000, 2, 45000), ""},
		{"sell Ear cleaner on a page 1 for 250 100 for 2m", "Ear cleaner", pk(1, 250, 100, 2000000), ""},
		// A quantity of any size is read (the packages' check refuses it),
		// never dropped.
		{"sell Ear cleaner on a page 1 for 25k, 100 for 2m", "Ear cleaner", pk(1, 25000, 100, 2000000), ""},
		// Words that are not packages are named, not read as some.
		{"sell Ear cleaner on a page 1 for 25k and free delivery for Lagos 2 at 3 for 7k", "Ear cleaner", nil, "free delivery for Lagos 2 at"},
		{"sell Ear cleaner on a page and more", "Ear cleaner", nil, "more"},
		{"sell Ear cleaner on a page 2 at 45k", "Ear cleaner", nil, "2 at 45k"},
		{"add a page for Ear cleaner", "Ear cleaner", nil, ""},
		{"make a page for 2-in-1 Ear cleaner 1 for 25k, 2 for 45k", "2-in-1 Ear cleaner", pk(1, 25000, 2, 45000), ""},
		// The longest catalogue name; "at" after a number is the name's.
		{"add a page for iPhone 13 1 for 500k", "iPhone 13", pk(1, 500000), ""},
		{"add a page for iPhone 13 at 500k", "iPhone 13", nil, "at 500k"},
		{"add a page for Rice cooker 1 for 30k", "Rice cooker", pk(1, 30000), ""},
		{"add a page for Rice cooker 2", "Rice cooker 2", nil, ""},
	} {
		name, pkgs, bad, ok := sellOnPageArgs(c.text, catalogue)
		if !ok || name != c.name || bad != c.bad || len(pkgs) != len(c.pkgs) {
			t.Errorf("%q: %q %v %q %v", c.text, name, pkgs, bad, ok)
			continue
		}
		for i := range pkgs {
			if pkgs[i] != c.pkgs[i] {
				t.Errorf("%q: package %d is %+v", c.text, i, pkgs[i])
			}
		}
	}
	// Not the command.
	for _, text := range []string{"sell rice 2", "sell Ear cleaner on a pager", "add front Snacks"} {
		if _, _, _, ok := sellOnPageArgs(text, catalogue); ok {
			t.Errorf("%q: read as a page", text)
		}
	}
	// Without the catalogue's names, an add-a-page splits at "N for".
	if name, pkgs, _, _ := sellOnPageArgs("add a page for iPhone 13 1 for 500k", nil); name != "iPhone 13" || len(pkgs) != 1 {
		t.Errorf("no catalogue: %q %v", name, pkgs)
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
