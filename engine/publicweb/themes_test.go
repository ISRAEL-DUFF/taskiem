package publicweb

import (
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/brand"
)

// TestEveryThemeHasEveryPage: each theme draws every page itself, so a
// page one theme lacks would fail only when a business in that theme
// asked for it. Every theme defines the pages the handlers render and the
// partials the shared templates and the pages' contract use.
func TestEveryThemeHasEveryPage(t *testing.T) {
	names := func(theme string) []string {
		var out []string
		for _, tp := range themed[theme].Templates() {
			if n := tp.Name(); n != "" {
				out = append(out, n)
			}
		}
		slices.Sort(out)
		return out
	}
	for _, theme := range brand.Themes {
		got := names(theme)
		for _, page := range []string{"storefront.html", "form.html", "booking.html", "hostedform.html", "formdone.html", "formclosed.html", "formapp.html",
			"status.html", "invoice.html", "report.html", "reach.html", "notfound.html", "setup.html", "unavailable.html", "embedsetup.html",
			"fhead", "fbody", "ffoot", "head", "foot", "hfield", "meta", "og", "guard", "safety"} {
			if !slices.Contains(got, page) {
				t.Errorf("%s has no %s", theme, page)
			}
		}
	}
}

// TestThemeFromBrand: a business's pages are drawn in its theme and
// colour; an unknown theme, or no brand at all, is the default look.
func TestThemeFromBrand(t *testing.T) {
	for _, c := range []struct {
		b         brand.Brand
		css, link string
	}{
		{brand.Brand{Theme: "editorial", Color: "#1f3a5f"}, "theme-editorial.", "/_brand/1/1f3a5f.css"},
		{brand.Brand{Theme: "bold"}, "theme-bold.", "/_brand/1/0b6e4f.css"},
		{brand.Brand{Theme: "neon", Color: "red;}"}, "theme-clean.", "/_brand/1/0b6e4f.css"},
	} {
		s, _ := storefrontServer(t, samplePage("classic", 2), true)
		withBrand(s, c.b)
		body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String()
		if !strings.Contains(body, c.css) || !strings.Contains(body, `href="`+c.link+`"`) {
			t.Errorf("%+v: want %s and %s", c.b, c.css, c.link)
		}
	}
	// A logo and a cover photo show; without a logo, Clean shows the initials.
	s, _ := storefrontServer(t, samplePage("classic", 1), true)
	withBrand(s, brand.Brand{Logo: "m1", LogoURLs: map[string]string{"320": "https://img.test/l320.jpg", "800": "https://img.test/l800.jpg"},
		Cover: "m2", CoverURLs: map[string]string{"800": "https://img.test/c800.jpg", "2000": "https://img.test/c2000.jpg"}})
	body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String()
	for _, want := range []string{`src="https://img.test/l320.jpg"`, `src="https://img.test/c800.jpg"`, `property="og:image" content="https://img.test/l800.jpg"`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %s", want)
		}
	}
	// (Editorial sets the name as a masthead instead.)
	s, _ = storefrontServer(t, samplePage("classic", 1), true)
	withBrand(s, brand.Brand{Theme: "clean"})
	if body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String(); !strings.Contains(body, `aria-hidden="true">AK</span>`) {
		t.Error("no initials for a business without a logo")
	}
}
