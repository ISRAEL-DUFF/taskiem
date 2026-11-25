package storefront_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Forms that sell L0 (docs/spec/forms-that-sell.md §5.2): a form with
// packages validates, or is refused for the right reason; its label is
// drawn from the quantity and price when none is given; a basket may show
// only some rows; one item named is the product the form sells.
func TestPackagesField(t *testing.T) {
	ok := `{"type":"object","properties":{"note":{"type":"string"},"package":{"type":"object","x-form":{"widget":"packages","list":"catalogue","item":"row-7",` +
		`"packages":[{"qty":1,"price_kobo":2500000,"label":"1 item + free delivery"},{"qty":2,"price_kobo":4500000},{"qty":3,"price_kobo":6500000}]}}}}`
	if err := storefront.CheckForm([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	if !storefront.HasWidget([]byte(ok), "packages") || !slices.Equal(storefront.FormFields([]byte(ok)), []string{"note"}) {
		t.Fatal("the packages field is a widget, not a question")
	}
	p, found := storefront.FormPackages([]byte(ok))
	if !found || p.Name != "package" || p.Item != "row-7" || p.List != "catalogue" || len(p.Packages) != 3 || p.Each() {
		t.Fatalf("read: %+v", p)
	}
	if got := p.Packages[0].Title(); got != "1 item + free delivery" {
		t.Errorf("the owner's label: %q", got)
	}
	if got := p.Packages[1].Title(); got != "2 items · ₦45,000" {
		t.Errorf("the drawn label: %q", got)
	}
	if got := storefront.PackageLabel(1, 2500000); got != "1 item · ₦25,000" {
		t.Errorf("one item: %q", got)
	}
	if x, ok := p.Find(3); !ok || x.PriceKobo != 6500000 || x.UnitKobo() != 2166667 {
		t.Errorf("find 3: %+v %v", x, ok)
	}
	if _, ok := p.Find(4); ok {
		t.Error("a package not offered was found")
	}
	if storefront.OneItem([]byte(ok)) != "row-7" {
		t.Error("the one item")
	}

	// Dearer per item than the catalogue: a warning, not a refusal.
	if w := storefront.PackageWarning("Ear cleaner", storefront.Package{Qty: 2, PriceKobo: 5200000}, 2500000); !strings.Contains(w, "₦26,000 an item") ||
		!strings.Contains(w, "₦25,000") {
		t.Errorf("warning: %q", w)
	}
	if w := storefront.PackageWarning("Ear cleaner", storefront.Package{Qty: 2, PriceKobo: 4500000}, 2500000); w != "" {
		t.Errorf("a cheaper package warned: %q", w)
	}
	// Far below the catalogue (under a quarter an item): most likely a
	// price typed wrong ("2 for 45"), warned; a quarter itself is not.
	if w := storefront.PackageWarning("Ear cleaner", storefront.Package{Qty: 2, PriceKobo: 4500}, 2500000); !strings.Contains(w, "₦22.50 an item") ||
		!strings.Contains(w, "under a quarter") {
		t.Errorf("a price far below: %q", w)
	}
	if w := storefront.PackageWarning("Ear cleaner", storefront.Package{Qty: 2, PriceKobo: 1250000}, 2500000); w != "" {
		t.Errorf("a quarter warned: %q", w)
	}
	// FrontSlug's address beside those taken, as CreateFront gives it and
	// a confirmation shows it: reserved or empty is "page", taken "-2".
	for _, c := range [][3]string{{"Smart Ear Cleaner", "", "smart-ear-cleaner"}, {"Smart Ear Cleaner", "smart-ear-cleaner", "smart-ear-cleaner-2"},
		{"Admin", "", "page"}, {"!!!", "page", "page-2"}, {"API", "page page-2", "page-3"}} {
		if got := storefront.NextFrontSlug(c[0], strings.Fields(c[1])); got != c[2] {
			t.Errorf("NextFrontSlug(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	// A reason to buy from the item's words, none when they hold a link.
	if r := storefront.ReasonFrom("See inside\nyour ear."); r != "See inside your ear." {
		t.Errorf("reason: %q", r)
	}
	if r := storefront.ReasonFrom("Buy now at https://cheap.example/x"); r != "" {
		t.Errorf("reason with a link: %q", r)
	}

	pkg := func(xform string) string {
		return `{"type":"object","properties":{"package":{"type":"object","x-form":{"widget":"packages",` + xform + `}}}}`
	}
	seven := make([]string, 7)
	for i := range seven {
		seven[i] = `{"qty":` + string(rune('1'+i)) + `,"price_kobo":100000}`
	}
	for name, c := range map[string]struct{ form, says string }{
		"seven packages":   {pkg(`"list":"catalogue","item":"r","packages":[` + strings.Join(seven, ",") + `]`), "one to 6 packages"},
		"none":             {pkg(`"list":"catalogue","item":"r","packages":[]`), "one to 6 packages"},
		"no packages":      {pkg(`"list":"catalogue","item":"r"`), "packages are a list"},
		"no item":          {pkg(`"list":"catalogue","packages":[{"qty":1,"price_kobo":100}]`), "the one product"},
		"no list":          {pkg(`"item":"r","packages":[{"qty":1,"price_kobo":100}]`), "the list"},
		"none of an item":  {pkg(`"list":"c","item":"r","packages":[{"qty":0,"price_kobo":100}]`), "1 to 20 items"},
		"too many":         {pkg(`"list":"c","item":"r","packages":[{"qty":21,"price_kobo":100}]`), "1 to 20 items"},
		"twice":            {pkg(`"list":"c","item":"r","packages":[{"qty":2,"price_kobo":100},{"qty":2,"price_kobo":200}]`), "offered once"},
		"free":             {pkg(`"list":"c","item":"r","packages":[{"qty":1,"price_kobo":0}]`), "from ₦1"},
		"a link":           {pkg(`"list":"c","item":"r","packages":[{"qty":1,"price_kobo":100,"label":"see www.scam.ng"}]`), "no links"},
		"a long label":     {pkg(`"list":"c","item":"r","packages":[{"qty":1,"price_kobo":100,"label":"` + strings.Repeat("a", 61) + `"}]`), "up to 60"},
		"an unknown field": {pkg(`"list":"c","item":"r","packages":[{"qty":1,"price_kobo":100,"total":5}]`), "packages are a list"},
		"choose":           {pkg(`"list":"c","item":"r","choose":"some","packages":[{"qty":1,"price_kobo":100}]`), "once for all"},
		"not an object":    {`{"type":"object","properties":{"package":{"type":"array","x-form":{"widget":"packages","list":"c","item":"r","packages":[{"qty":1,"price_kobo":100}]}}}}`, "is an object"},
		"two packages": {`{"type":"object","properties":{"a":{"type":"object","x-form":{"widget":"packages","list":"c","item":"r","packages":[{"qty":1,"price_kobo":100}]}},` +
			`"b":{"type":"object","x-form":{"widget":"packages","list":"c","item":"r","packages":[{"qty":1,"price_kobo":100}]}}}}`, "one packages field"},
		"and a basket": {`{"type":"object","properties":{"a":{"type":"object","x-form":{"widget":"packages","list":"c","item":"r","packages":[{"qty":1,"price_kobo":100}]}},` +
			`"b":{"type":"array","x-form":{"widget":"basket","list":"c"}}}}`, "not both"},
		"empty items":     {`{"type":"object","properties":{"b":{"type":"array","x-form":{"widget":"basket","list":"c","items":[]}}}}`, "one or more"},
		"an item twice":   {`{"type":"object","properties":{"b":{"type":"array","x-form":{"widget":"basket","list":"c","items":["a","a"]}}}}`, "once"},
		"items not names": {`{"type":"object","properties":{"b":{"type":"array","x-form":{"widget":"basket","list":"c","items":[1]}}}}`, "one or more"},
	} {
		err := storefront.CheckForm([]byte(c.form))
		if !errors.Is(err, storefront.ErrInvalid) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A basket of some rows only; of one row, it sells that one.
	some := `{"type":"object","properties":{"b":{"type":"array","x-form":{"widget":"basket","list":"c","items":["a","b"]}}}}`
	if err := storefront.CheckForm([]byte(some)); err != nil || !slices.Equal(storefront.BasketItems([]byte(some)), []string{"a", "b"}) || storefront.OneItem([]byte(some)) != "" {
		t.Fatalf("a basket of some: %v %v", err, storefront.BasketItems([]byte(some)))
	}
	if one, err := storefront.BasketForm("catalogue", []string{"a"}, nil); err != nil || storefront.OneItem(one) != "a" {
		t.Fatalf("a basket of one: %s %v", one, err)
	}
	if storefront.BasketItems(storefront.BuiltinForm("order")) != nil {
		t.Fatal("the built-in basket shows every row")
	}
	made, err := storefront.PackagesForm("catalogue", "row-7", []storefront.Package{{Qty: 1, PriceKobo: 2500000}, {Qty: 2, PriceKobo: 4500000}}, "each",
		map[string]json.RawMessage{"when": json.RawMessage(`{"type":"string"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := storefront.FormPackages(made); !ok || !p.Each() || len(p.Packages) != 2 || !slices.Equal(storefront.FormFields(made), []string{"when"}) {
		t.Fatalf("made: %s", made)
	}
	if _, err := storefront.PackagesForm("catalogue", "row-7", nil, "", nil); !errors.Is(err, storefront.ErrInvalid) {
		t.Fatalf("made with none: %v", err)
	}
}

// The One product kind (§5.4): its settings are a promise line, up to six
// chips from the fixed icons and the stock line, checked strictly; it is
// checked with the other kinds and offered second, after Shop (L2 draws it).
func TestOneProductSettings(t *testing.T) {
	good := `{"promise":"Pay on delivery nationwide","chips":[{"icon":"truck","text":"Free delivery in Lagos"},{"icon":"shield","text":"30-day warranty"}],"stock_line":true}`
	out, err := storefront.CheckFrontSettings(storefront.OneProduct, json.RawMessage(good))
	if err != nil {
		t.Fatal(err)
	}
	var s storefront.OneProductSettings
	if err := json.Unmarshal(out, &s); err != nil || len(s.Chips) != 2 || !s.StockLine || s.Promise != "Pay on delivery nationwide" {
		t.Fatalf("settings: %s %v", out, err)
	}
	chips := make([]string, 7)
	for i := range chips {
		chips[i] = `{"icon":"star","text":"Good"}`
	}
	for name, raw := range map[string]string{
		"seven chips":     `{"chips":[` + strings.Join(chips, ",") + `]}`,
		"an icon not set": `{"chips":[{"icon":"bank","text":"CBN approved"}]}`,
		"no words":        `{"chips":[{"icon":"star","text":" "}]}`,
		"long words":      `{"chips":[{"icon":"star","text":"` + strings.Repeat("a", 41) + `"}]}`,
		"a link":          `{"promise":"Order at www.example.com"}`,
		"a long promise":  `{"promise":"` + strings.Repeat("a", 81) + `"}`,
		"an unknown":      `{"headline":"Buy now"}`,
	} {
		if _, err := storefront.CheckFrontSettings(storefront.OneProduct, json.RawMessage(raw)); !errors.Is(err, storefront.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := storefront.CheckFrontSettings("shop", json.RawMessage(good)); !errors.Is(err, storefront.ErrInvalid) {
		t.Errorf("a shop with settings: %v", err)
	}
	var all, offered []string
	for _, k := range storefront.AllFrontKinds() {
		all = append(all, k.Name)
	}
	for _, k := range storefront.FrontKinds() {
		offered = append(offered, k.Name)
	}
	if !slices.Contains(all, storefront.OneProduct) || !slices.Contains(offered, storefront.OneProduct) || offered[1] != storefront.OneProduct {
		t.Fatalf("kinds: all %v, offered %v", all, offered)
	}
	k, _ := storefront.FrontKindOf("shop")
	if k.Name != "shop" {
		t.Fatal("shop is offered")
	}
	for _, k := range storefront.AllFrontKinds() {
		if k.Name == storefront.OneProduct && (k.Table != "products" || len(k.Actions) != 1 || !k.Actions[0].Options.PayOnDelivery || k.Actions[0].Options.Notice == "") {
			t.Fatalf("one product: %+v", k)
		}
	}
	if storefront.NoticeFor(storefront.ActionOptions{}, true) != storefront.DefaultNotice || storefront.NoticeFor(storefront.ActionOptions{}, false) != "" ||
		storefront.NoticeFor(storefront.ActionOptions{Notice: "-"}, true) != "" || storefront.NoticeFor(storefront.ActionOptions{Notice: "Ready?"}, false) != "Ready?" {
		t.Fatal("the notice")
	}
	if storefront.CheckNotice("see www.x.ng") == nil || storefront.CheckNotice(strings.Repeat("a", 161)) == nil || storefront.CheckNotice("Ready in 2 to 4 days.") != nil {
		t.Fatal("checking the notice")
	}
}
