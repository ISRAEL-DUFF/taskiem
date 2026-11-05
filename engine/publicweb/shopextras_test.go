package publicweb

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestAccentSplitsAHeadline(t *testing.T) {
	for in, want := range map[string][2]string{
		"Hot food, made fresh today.": {"Hot food,", "made fresh today."},
		"Book your glow.":             {"Book your", "glow."},
		"Welcome":                     {"Welcome", ""},
		"":                            {"", ""},
	} {
		if got := accent(in); got != want {
			t.Errorf("accent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSwatchAndOptkind(t *testing.T) {
	for in, want := range map[string]string{"Black": "sw-black", "moss (+₦500)": "sw-moss", "Sky": "sw-none", "": "sw-none"} {
		if got := swatch(in); got != want {
			t.Errorf("swatch(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Colour": "colour", "Color": "colour", "Shirt size": "size", "Sides": "other"} {
		if got := optkind(in); got != want {
			t.Errorf("optkind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchNarrowsTheCatalogue(t *testing.T) {
	mk := func(name, cat string) item { return item{Item: storefront.Item{Name: name, Category: cat}} }
	d := storefrontData{Sections: []section{
		{Title: "Rice", Items: []item{mk("Jollof rice", "Rice"), mk("Fried rice", "Rice")}},
		{Title: "Drinks", Items: []item{mk("Zobo", "Drinks")}},
	}}
	d.search("JOLLOF")
	if d.Found != 1 || len(d.Sections) != 1 || d.Sections[0].Items[0].Name != "Jollof rice" {
		t.Errorf("search: %+v", d)
	}
	none := storefrontData{Sections: []section{{Items: []item{mk("Zobo", "")}}}}
	none.search("pizza")
	if none.Found != 0 || len(none.Sections) != 0 || none.Search != "pizza" {
		t.Errorf("no match: %+v", none)
	}
	same := storefrontData{Sections: []section{{Items: []item{mk("Zobo", "")}}}}
	same.search("")
	if len(same.Sections) != 1 {
		t.Error("an empty search keeps the catalogue")
	}
}

func TestSearchTermIsShort(t *testing.T) {
	r := httptest.NewRequest("GET", "/?q=%20"+strings.Repeat("a", 100), nil)
	if got := searchTerm(r); len([]rune(got)) != 40 {
		t.Errorf("searchTerm length %d, want 40", len([]rune(got)))
	}
}

func TestItemDecorate(t *testing.T) {
	left := int64(3)
	x := item{Item: storefront.Item{Stock: &left, Gallery: []storefront.Image{{"320": "s", "800": "l"}, {}}}}
	x.decorate()
	if !x.Few || len(x.Pics) != 1 || x.Pics[0].Small != "s" || x.Pics[0].Large != "l" {
		t.Errorf("decorate: %+v", x)
	}
	plenty := int64(50)
	y := item{Item: storefront.Item{Stock: &plenty}}
	y.decorate()
	if y.Few {
		t.Error("plenty left is not few")
	}
}

func TestBagRemoveKeepsTheRest(t *testing.T) {
	f := formData{Action: "/a/order", Home: "/", Bag: []bagLine{
		{ID: "1", Qty: 2},
		{ID: "2", Qty: 1, Hidden: []bagField{{Name: "o.2.0", Value: "1"}}},
	}}
	if got, want := f.removeHref("1"), "/a/order?o.2.0=1&q.2=1&step=bag"; got != want {
		t.Errorf("removeHref = %q, want %q", got, want)
	}
	f.Bag = f.Bag[:1]
	if got := f.removeHref("1"); got != "/" {
		t.Errorf("removing the last line goes home, got %q", got)
	}
}
