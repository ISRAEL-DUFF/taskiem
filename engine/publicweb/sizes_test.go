package publicweb

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
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
			"(none left)", `name="q.10"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the product's page is missing %q", theme, want)
			}
		}
		// The sizes for the script: JSON, each size's id, price, count and
		// values.
		if got := sizesAttr(t, page); len(got) != 3 || got[0][0] != "11" || got[0][1] != nil || got[0][2] != float64(3) || got[1][1] != float64(4800000) {
			t.Errorf("%s: the sizes for the script: %v", theme, got)
		}
		// The list sends it to its page to be picked, never to the form
		// as it is.
		list := get(e.h, "GET", sfHost, "/shoes").Body.String()
		if !strings.Contains(list, `href="/shoes/i/10"`) || strings.Contains(list, "item=10") {
			t.Errorf("%s: the list does not send the product to its page", theme)
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

// sizesAttr reads an item page's data-sizes, as its script does.
func sizesAttr(t *testing.T, page string) [][]any {
	t.Helper()
	m := regexp.MustCompile(`data-sizes="([^"]*)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no data-sizes")
	}
	var out [][]any
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &out); err != nil {
		t.Fatalf("data-sizes %q: %v", m[1], err)
	}
	return out
}

// A product sold in sizes and colours in the home page's catalogue (not
// a front's list): its page, a value of any character, the bag, and what
// an order refuses in words, never leaving a line out unsaid.
func TestSizesInTheCatalogue(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	tee := func(avail bool, stocks ...int64) storefront.Item {
		it := storefront.Item{ID: "50", Name: "Tee", PriceKobo: kobo(1000000), Available: avail, Variants: 2,
			Choices: []variants.Choice{{Name: "Colour", Values: []string{`Navy | "Gold"`, "Red"}}},
			Sizes: []storefront.Size{
				{ID: "51", Variant: map[string]string{"Colour": `Navy | "Gold"`}, Stock: n(stocks[0])},
				{ID: "52", Variant: map[string]string{"Colour": "Red"}, Stock: n(stocks[1])},
			}}
		var all int64
		for _, x := range stocks {
			all += x
		}
		it.Stock = &all
		return it
	}
	page := func(it storefront.Item) *formEnv {
		p := orderPage()
		p.Items = append(p.Items, it)
		return formServer(t, p)
	}
	base := url.Values{"website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "fulfilment": {"pickup"}}
	send := func(e *formEnv, more url.Values) *httptest.ResponseRecorder {
		t.Helper()
		_, tok := e.form(t, "/a/order")
		*e.now = e.now.Add(20 * time.Second)
		v := url.Values{"token": {tok}}
		for k, x := range base {
			v[k] = x
		}
		for k, x := range more {
			v[k] = x
		}
		return e.post("/a/order", v)
	}

	e := page(tee(true, 3, 0))
	item := get(e.h, "GET", sfHost, "/i/50").Body.String()
	if got := sizesAttr(t, item); len(got) != 2 || got[0][3].([]any)[0] != `Navy | "Gold"` {
		t.Errorf("a value with | and quotes: %v", got)
	}
	if !strings.Contains(get(e.h, "GET", sfHost, "/").Body.String(), `href="/i/50"`) {
		t.Error("the home page does not send the product to its page")
	}
	// More than are left, without a script: the bag takes what is left,
	// and says so.
	bag := get(e.h, "GET", sfHost, "/a/order?step=bag&q.51=5").Body.String()
	if !strings.Contains(bag, `name="q.51" value="3"`) || !strings.Contains(bag, "Only 3 of Tee – Navy | &#34;Gold&#34; left") {
		t.Errorf("five of three left: %s", bag)
	}
	// A pick sold out beside another item: refused with its words, the
	// order not sent without it.
	for name, more := range map[string]url.Values{
		"with another item": {"q.2": {"1"}, "q.50": {"1"}, "z.50.0": {"Red"}},
		"alone":             {"q.50": {"1"}, "z.50.0": {"Red"}},
	} {
		w := send(e, more)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Tee – Red is sold out. Please choose another.") {
			t.Errorf("%s: a sold-out pick: %d", name, w.Code)
		}
	}
	if w := send(e, url.Values{"q.2": {"1"}, "q.50": {"1"}, "z.50.0": {"Blue"}}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Please choose a colour for Tee.") {
		t.Errorf("a pick naming no size: %d", w.Code)
	}
	if w := send(e, url.Values{"q.51": {"5"}}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Only 3 of Tee") {
		t.Errorf("five of three left, sent: %d", w.Code)
	}
	if w := send(e, url.Values{"q.50": {"2"}, "z.50.0": {`navy | "gold"`}}); w.Code != http.StatusSeeOther {
		t.Errorf("a good pick: %d %s", w.Code, w.Body.String())
	}

	// Every size sold out: the page says so and offers nothing; a pick
	// is refused.
	e = page(tee(true, 0, 0))
	if body := get(e.h, "GET", sfHost, "/i/50").Body.String(); strings.Contains(body, `name="q.50"`) || !strings.Contains(body, "Sold out.") {
		t.Error("every size sold out: the page still offers it")
	}
	if w := send(e, url.Values{"q.50": {"1"}, "z.50.0": {"Red"}}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "is sold out") {
		t.Errorf("every size sold out: %d", w.Code)
	}

	// A hidden product: neither its pick nor a size's own line is taken.
	e = page(tee(false, 3, 3))
	if body := get(e.h, "GET", sfHost, "/i/50").Body.String(); strings.Contains(body, `name="q.50"`) {
		t.Error("a hidden product's page offers it")
	}
	if w := send(e, url.Values{"q.2": {"1"}, "q.50": {"1"}, "z.50.0": {"Red"}}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Tee is not available right now.") {
		t.Errorf("a hidden product's pick: %d", w.Code)
	}
	if w := send(e, url.Values{"q.52": {"1"}}); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "not available right now") {
		t.Errorf("a hidden product's size sent by its id: %d", w.Code)
	}
}
