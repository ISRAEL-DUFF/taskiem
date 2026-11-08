package publicweb

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

// A product sold in sizes and colours on the page (phase 4): its page asks
// the size and the colour, a value with none left struck out; the pick
// becomes a bag line of the size's own row, at its own price; the order
// form lists the product as Choose, never each size; a size sold out, or
// a pick that names no size, is refused in words. In every theme.
func TestSizesOnThePage(t *testing.T) {
	order := uuid.New()
	n := func(v int64) *int64 { return &v }
	trainer := storefront.Item{ID: "10", Name: "Air Max 90", PriceKobo: kobo(4500000), Available: true, Stock: n(5), Variants: 3,
		Choices: []variants.Choice{{Name: "Size", Values: []string{"42", "43"}}, {Name: "Colour", Values: []string{"Black", "White"}}},
		Sizes: []storefront.Size{
			{ID: "11", Variant: map[string]string{"Size": "42", "Colour": "Black"}, Stock: n(3)},
			{ID: "12", Variant: map[string]string{"Size": "43", "Colour": "Black"}, Stock: n(2), PriceKobo: kobo(4800000)},
			{ID: "13", Variant: map[string]string{"Size": "42", "Colour": "White"}, Stock: n(0)},
		}}
	for _, theme := range brand.Themes {
		p := orderPage()
		home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Zuri"}
		shoes := storefront.PublicFront{Slug: "shoes", Kind: "shop", Name: "Shoes",
			Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "shoes", "columns": {"name": "name"}}`)}},
			Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
		p.Model.Fronts = []storefront.PublicFront{home, shoes}
		p.Lists = map[string][]storefront.Item{"shoes": {trainer}}
		e := formServer(t, p)
		withBrand(e.s, brand.Brand{Theme: theme})
		e.h = e.s.Handler()

		// Its page: the size and the colour to pick; White has none left.
		item := get(e.h, "GET", sfHost, "/shoes/i/10")
		if item.Code != http.StatusOK {
			t.Fatalf("%s: the product's page: %d", theme, item.Code)
		}
		page := item.Body.String()
		for _, want := range []string{`name="z.10.0" value="42" required`, `name="z.10.1" value="Black" required`, `name="z.10.1" value="White" required disabled`,
			"(none left)", `name="q.10"`, `data-sizes="11||3|42|Black`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the product's page is missing %q", theme, want)
			}
		}
		// The pick is a bag line of the size's own row, at its own price.
		bag := get(e.h, "GET", sfHost, "/shoes/a/order?step=bag&q.10=2&z.10.0=43&z.10.1=black").Body.String()
		for _, want := range []string{`name="q.12" value="2"`, "Air Max 90 – 43, Black", "₦48,000 each", "Total: ₦96,000"} {
			if !strings.Contains(bag, want) {
				t.Errorf("%s: the bag is missing %q", theme, want)
			}
		}
		if strings.Contains(bag, `name="q.10"`) {
			t.Errorf("%s: the product itself is in the bag", theme)
		}
		// The one-page form: the product to choose, not its sizes.
		form, tok := e.form(t, "/shoes/a/order")
		if !strings.Contains(form, `href="/shoes/i/10"`) || strings.Contains(form, `name="q.11"`) || strings.Contains(form, `name="q.10"`) {
			t.Errorf("%s: the form lists sizes, or not the product to choose", theme)
		}
		// Refused in words: a size with none left; a pick naming none.
		for pick, want := range map[string]string{"42|White": "Air Max 90 – 42, White is sold out", "44|Black": "Please choose a size and colour for Air Max 90"} {
			parts := strings.Split(pick, "|")
			if body := get(e.h, "GET", sfHost, "/shoes/a/order?step=bag&q.10=1&z.10.0="+parts[0]+"&z.10.1="+parts[1]).Body.String(); !strings.Contains(body, want) {
				t.Errorf("%s: %s: not refused with %q", theme, pick, want)
			}
		}
		// An order of it: one line, the size's row, priced from the copy.
		*e.now = e.now.Add(20 * time.Second)
		w := e.post("/shoes/a/order", url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"},
			"q.10": {"1"}, "z.10.0": {"42"}, "z.10.1": {"Black"}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: the order: %d %s", theme, w.Code, w.Body.String())
		}
		ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
		sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(sub.Basket) != 1 || sub.Basket[0].ID != "11" || sub.Basket[0].Name != "Air Max 90 – 42, Black" || sub.Basket[0].PriceKobo != 4500000 {
			t.Errorf("%s: the order's lines: %+v", theme, sub.Basket)
		}
	}
}
