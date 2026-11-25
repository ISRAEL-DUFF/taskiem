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
)

// oneProductPage is a storefront with a One product front, ear-cleaner,
// selling item 2 of the catalogue in three packages, delivered to two
// areas and paid at the door.
func oneProductPage(stock *int64) StorefrontPage {
	p := samplePage("classic", 4)
	p.Items[1].Stock = stock
	p.Items[1].Name = "Smart Ear Cleaner"
	p.Items[1].Gallery = []storefront.Image{{"320": "https://ref.xb.test/g1/320.jpg", "800": "https://ref.xb.test/g1/800.jpg"}}
	form, err := storefront.PackagesForm("products", "2", []storefront.Package{{Qty: 1, PriceKobo: 2500000, Label: "1 item + free delivery"},
		{Qty: 2, PriceKobo: 4500000}, {Qty: 3, PriceKobo: 6500000}}, "", nil)
	if err != nil {
		panic(err)
	}
	id := uuid.New()
	list, _ := json.Marshal(storefront.ListConfig{TableName: "products"})
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Home"}, {Slug: "ear-cleaner", Kind: storefront.OneProduct, Name: "Smart Ear Cleaner Kit",
		Tagline: "See inside your ear and clean it safely.",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: list}},
		OneProduct: &storefront.OneProductSettings{Promise: "Pay on delivery nationwide + free delivery today", StockLine: true,
			Chips: []storefront.Chip{{Icon: "truck", Text: "Delivered in 2 to 4 days"}, {Icon: "shield", Text: "30-day warranty"}}},
		Actions: []storefront.PublicAction{{ID: &id, Slug: "order-now", Kind: "order", Label: "Order now", Payment: "none", Delivery: true, PayOnDelivery: true,
			Form: form, Notice: "Do not order if you are not ready to receive it in 2 to 4 days.",
			Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}, {Name: "Yaba", FeeKobo: 0}}}}}}
	p.Lists = map[string][]storefront.Item{"products": p.Items}
	return p
}

// The One product page (forms that sell §5.4, L2): every part drawn in
// every look: the promise line, the stock line from the item's stock, the
// headline and reason, the photos, the chips, the form with its packages
// posting to the action's address, the notice, Order via WhatsApp with
// the product named, and the price bar.
func TestOneProductPage(t *testing.T) {
	left := int64(14)
	for _, theme := range brand.Themes {
		e := formServer(t, oneProductPage(&left))
		withBrand(e.s, brand.Brand{Theme: theme, Color: "#c2410c"})
		e.h = e.s.Handler()
		body, _ := e.form(t, "/ear-cleaner")
		for _, want := range []string{
			`<p class="op-promise">Pay on delivery nationwide &#43; free delivery today</p>`, "Only 14 left", "Pay on delivery",
			`<h1 class="op-h">Smart Ear Cleaner Kit</h1>`, "See inside your ear and clean it safely.",
			"https://ref.xb.test/storage/v1/public/taskiem-public/images/h001/800.jpg", "https://ref.xb.test/g1/800.jpg", "photo 2 of 2",
			`class="op-chips"`, "Delivered in 2 to 4 days", "30-day warranty", `viewBox="0 0 24 24"`,
			`action="/ear-cleaner/a/order-now"`, `class="packages"`, `name="pkg" value="2"`, "Lekki, ₦1,500 delivery",
			"ready to receive it in 2 to 4 days", `class="sell-notice"`,
			`href="https://wa.me/2348031234567?text=Hello&#43;Ada%27s&#43;%3CKitchen%3E%2C&#43;I&#43;want&#43;to&#43;order&#43;Smart&#43;Ear&#43;Cleaner.`, "Order via WhatsApp",
			`class="op-bar"`, "From ₦25,000", "1 item · pay on delivery", `href="#order"`, "oneproduct.", "sell.",
			`<meta property="og:title" content="Smart Ear Cleaner Kit">`, `<link rel="canonical" href="https://ada-kitchen.shops.test/ear-cleaner">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", theme, want)
			}
		}
		// No made-up count: no stock line where stock is not kept.
		e = formServer(t, oneProductPage(nil))
		withBrand(e.s, brand.Brand{Theme: theme})
		e.h = e.s.Handler()
		if body, _ := e.form(t, "/ear-cleaner"); strings.Contains(body, " left") {
			t.Errorf("%s: a stock line with no stock kept", theme)
		}
	}

	// The form under the page is the action's: sent, it is an order of
	// the package, priced from the published form.
	e := formServer(t, oneProductPage(&left))
	_, tok := e.form(t, "/ear-cleaner")
	*e.now = e.now.Add(20 * time.Second)
	w := e.post("/ear-cleaner/a/order-now", url.Values{"token": {tok}, "website": {""}, "name": {"Bola Ade"}, "phone": {"0803 123 4567"},
		"address": {"12 Admiralty Way"}, "zone": {"Lekki"}, "pkg": {"2"}, "total": {"1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("order: %d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/([A-Z]{2}-[A-Z2-9]{6})\?k=`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Basket) != 1 || sub.Basket[0].PackageQty != 2 || sub.Basket[0].LineKobo != 4500000 || sub.TotalKobo != 4650000 || sub.Zone != "Lekki" {
		t.Fatalf("the order: %+v", sub)
	}

	// Sold out: the page says so, with no form and no price bar.
	none := int64(0)
	e = formServer(t, oneProductPage(&none))
	sw := get(e.h, "GET", sfHost, "/ear-cleaner")
	if b := sw.Body.String(); sw.Code != 200 || !strings.Contains(b, "Smart Ear Cleaner is sold out right now.") || strings.Contains(b, `name="token"`) || strings.Contains(b, "op-bar") {
		t.Fatalf("sold out: %d %s", sw.Code, b)
	}

	// Not taking orders, in each way: no Order via WhatsApp either (it is
	// ordering too), only the words saying why.
	for _, c := range []struct {
		name, says string
		set        func(p *StorefrontPage, e *formEnv)
	}{
		{"closed", "is closed right now.", func(p *StorefrontPage, _ *formEnv) { p.Status = "closed" }},
		{"paused", "is not taking orders online right now.", func(p *StorefrontPage, _ *formEnv) { p.OrdersPaused = true }},
		{"not open yet", "Orders are not open yet.", func(_ *StorefrontPage, e *formEnv) { e.s.Submit = nil }},
		{"sold out", "is sold out right now.", func(p *StorefrontPage, _ *formEnv) { p.Lists["products"][1].Stock = &none }},
	} {
		p := oneProductPage(&left)
		if c.name != "not open yet" {
			c.set(&p, nil)
		}
		e := formServer(t, p)
		if c.name == "not open yet" {
			c.set(&p, e)
		}
		e.h = e.s.Handler()
		b := get(e.h, "GET", sfHost, "/ear-cleaner").Body.String()
		if !strings.Contains(b, c.says) || strings.Contains(b, "op-whats") || strings.Contains(b, "wa.me") || strings.Contains(b, `name="token"`) {
			t.Errorf("%s: %s", c.name, b)
		}
	}

	// No line of its own: the item's words, but never words with a link
	// (CreateOneProduct's rule).
	p := oneProductPage(&left)
	p.Model.Fronts[1].Tagline = ""
	p.Lists["products"][1].Description = "Cleans gently.\nOrder at https://cheap.example/ear"
	e = formServer(t, p)
	if body, _ := e.form(t, "/ear-cleaner"); strings.Contains(body, "cheap.example") || strings.Contains(body, `class="op-reason"`) {
		t.Error("the reason from words with a link")
	}
	p.Lists["products"][1].Description = "Cleans gently.\nSafe for children."
	e = formServer(t, p)
	if body, _ := e.form(t, "/ear-cleaner"); !strings.Contains(body, `<p class="op-reason">Cleans gently. Safe for children.</p>`) {
		t.Error("the reason from the item's words")
	}

	// Without the business's WhatsApp, its chat link; with neither, none.
	p = oneProductPage(&left)
	p.Model.WhatsLink, p.Model.ChatLink = "", "https://taskiem.test/m/5zw3sc3g"
	e = formServer(t, p)
	if body, _ := e.form(t, "/ear-cleaner"); !strings.Contains(body, `href="https://taskiem.test/m/5zw3sc3g"`) || !strings.Contains(body, "Order in a chat") {
		t.Fatal("the chat link")
	}
	p.Model.ChatLink = ""
	e = formServer(t, p)
	if body, _ := e.form(t, "/ear-cleaner"); strings.Contains(body, "op-whats") {
		t.Fatal("Order via WhatsApp with no number")
	}

	// A One product front whose form sells nothing now is drawn as any
	// front is.
	p = oneProductPage(&left)
	p.Model.Fronts[1].Actions = nil
	e = formServer(t, p)
	if sw := get(e.h, "GET", sfHost, "/ear-cleaner"); sw.Code != 200 || strings.Contains(sw.Body.String(), "op-h") || !strings.Contains(sw.Body.String(), "Smart Ear Cleaner") {
		t.Fatalf("no order form: %d", sw.Code)
	}
}
