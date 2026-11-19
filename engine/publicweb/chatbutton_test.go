package publicweb

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/brand"
)

// "Chat with us" (docs/spec/taskiem-for-everyone.md §8.2) shows in every
// look when the page has a chat link, beside WhatsApp or alone: the
// owner's choice is made at publishing, by the links the page holds.
func TestStorefrontChatButton(t *testing.T) {
	const link = "https://taskiem.test/m/k7m2p9q4wx"
	for _, tmpl := range brand.Themes {
		for _, items := range []int{0, 4} {
			both := samplePage("classic", items)
			both.Model.ChatLink = link
			s, _ := storefrontServer(t, both, true)
			withBrand(s, brand.Brand{Theme: tmpl, Color: "#c2410c"})
			body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String()
			if !strings.Contains(body, `href="`+link+`"`) || !strings.Contains(body, "Chat with us") {
				t.Errorf("%s, %d items: no Chat with us", tmpl, items)
			}
			if !strings.Contains(body, "https://wa.me/2348031234567") {
				t.Errorf("%s, %d items: WhatsApp went with both", tmpl, items)
			}

			alone := samplePage("classic", items)
			alone.Model.ChatLink, alone.Model.WhatsLink = link, ""
			s, _ = storefrontServer(t, alone, true)
			withBrand(s, brand.Brand{Theme: tmpl, Color: "#c2410c"})
			body = get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String()
			if !strings.Contains(body, `href="`+link+`"`) || strings.Contains(body, "wa.me") {
				t.Errorf("%s, %d items: Chat with us alone", tmpl, items)
			}

			none := samplePage("classic", items)
			s, _ = storefrontServer(t, none, true)
			withBrand(s, brand.Brand{Theme: tmpl, Color: "#c2410c"})
			if body = get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String(); strings.Contains(body, "Chat with us") {
				t.Errorf("%s, %d items: Chat with us with no link", tmpl, items)
			}
		}
	}
}
