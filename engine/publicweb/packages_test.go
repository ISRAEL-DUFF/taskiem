package publicweb

import (
	"context"
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

// packagesPage is a page whose order form sells item 2 in three packages,
// pay at the door with delivery to two areas.
func packagesPage(stock *int64, choose string) StorefrontPage {
	p := samplePage("classic", 4)
	p.Items[1].Stock = stock
	form, err := storefront.PackagesForm("catalogue", "2", []storefront.Package{{Qty: 1, PriceKobo: 2500000, Label: "1 item + free delivery"},
		{Qty: 2, PriceKobo: 4500000}, {Qty: 3, PriceKobo: 6500000}}, choose, nil)
	if err != nil {
		panic(err)
	}
	id := uuid.New()
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Home", Actions: []storefront.PublicAction{{ID: &id, Slug: "ear-cleaner", Kind: "order",
		Label: "Order now", Payment: "after_accept", Delivery: true, PayOnDelivery: true, Form: form, Checkout: true,
		Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}, {Name: "Yaba", FeeKobo: 0}}}}}}
	return p
}

// Forms that sell L1 (§5.2, §5.3): the packages field is drawn with the
// product at the top, priced on the server from the published packages
// (never the browser's numbers), with the area's fee and the notice; a
// package the stock cannot fill is greyed and refused.
func TestPackagesForm(t *testing.T) {
	left := int64(2)
	e := formServer(t, packagesPage(&left, ""))
	body, tok := e.form(t, "/a/ear-cleaner")
	for _, want := range []string{`class="sell-one"`, "Dish number 2", "Secure checkout", `name="pkg" value="1"`, "1 item &#43; free delivery",
		"<strong>2 items</strong> <span>₦45,000 · ₦22,500 each", "₦22,500 each", `data-price="4500000"`, `class="pkg is-short"`, "Not enough in stock", `value="3" data-price="6500000" data-qty="3" disabled`,
		storefront.DefaultNotice, `class="sell-notice"`, "Lekki, ₦1,500 delivery", `value="on_delivery"`, `data-sell`, `/sell.`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	// The product is not asked for: no basket, no item to pick.
	if strings.Contains(body, `name="q.`) {
		t.Error("a basket on a form that sells one product")
	}
	// The first package the stock can fill is chosen.
	if !regexp.MustCompile(`name="pkg" value="1"[^>]*checked`).MatchString(body) {
		t.Error("no package chosen")
	}
	order := func(tok string, extra url.Values) url.Values {
		v := url.Values{"token": {tok}, "website": {""}, "name": {"Bola Ade"}, "phone": {"0803 123 4567"}, "address": {"12 Admiralty Way"},
			"zone": {"Lekki"}, "pay": {"on_delivery"}, "pkg": {"2"},
			// The browser's numbers are never read.
			"price": {"1"}, "total": {"100"}, "q.2": {"9"}}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	*e.now = e.now.Add(20 * time.Second)
	// Three are more than the stock holds: refused, the form kept.
	w := e.post("/a/ear-cleaner", order(tok, url.Values{"pkg": {"3"}}))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Not enough Dish number 2 in stock for that package") {
		t.Fatalf("a package the stock cannot fill: %d %s", w.Code, w.Body.String())
	}
	// A quantity no package offers.
	if w := e.post("/a/ear-cleaner", order(tok, url.Values{"pkg": {"4"}})); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no such package: %d", w.Code)
	}
	w = e.post("/a/ear-cleaner", order(tok, nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("order: %d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/([A-Z]{2}-[A-Z2-9]{6})\?k=`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Basket) != 1 {
		t.Fatalf("lines: %+v", sub.Basket)
	}
	l := sub.Basket[0]
	if l.ID != "2" || l.Quantity != 2 || l.LineKobo != 4500000 || l.PackageQty != 2 || l.PackageKobo != 4500000 || l.PriceKobo != 2250000 {
		t.Fatalf("the package's line: %+v", l)
	}
	if sub.TotalKobo != 4650000 || sub.DeliveryKobo != 150000 || sub.Zone != "Lekki" || sub.Payment != "on_delivery" || sub.Fulfilment != "delivery" {
		t.Fatalf("the submission: %+v", sub)
	}

	// Sold out: every package greyed, none taken.
	none := int64(0)
	e = formServer(t, packagesPage(&none, ""))
	body, tok = e.form(t, "/a/ear-cleaner")
	if strings.Count(body, "Not enough in stock") != 3 || !strings.Contains(body, "Sold out right now.") {
		t.Fatalf("sold out: %s", body)
	}
	*e.now = e.now.Add(20 * time.Second)
	if w := e.post("/a/ear-cleaner", order(tok, url.Values{"pkg": {"1"}})); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "sold out") {
		t.Fatalf("sold out, sent: %d", w.Code)
	}

	// A notice the owner set, or cleared.
	p := packagesPage(nil, "")
	p.Model.Fronts[0].Actions[0].Notice = "Do not order if you are not ready to receive it in 2 to 4 days."
	e = formServer(t, p)
	if body, _ = e.form(t, "/a/ear-cleaner"); !strings.Contains(body, "ready to receive it in 2 to 4 days") || strings.Contains(body, "Not enough in stock") {
		t.Fatal("the owner's notice, or stock that is not kept")
	}
	p.Model.Fronts[0].Actions[0].Notice = "-"
	e = formServer(t, p)
	if body, _ = e.form(t, "/a/ear-cleaner"); strings.Contains(body, "sell-notice") {
		t.Fatal("a cleared notice")
	}
}

// Sizes and colours (§5.2): one choice for all the package's units, or
// one per unit; the lines are the sizes', together the package's price.
func TestPackagesSizes(t *testing.T) {
	for _, choose := range []string{"all", "each"} {
		p := packagesPage(nil, choose)
		m, l := int64(5), int64(1)
		p.Items[1].Choices = []variants.Choice{{Name: "Size", Values: []string{"M", "L"}}}
		p.Items[1].Variants = 2
		p.Items[1].Sizes = []storefront.Size{{ID: "2m", Variant: map[string]string{"Size": "M"}, Stock: &m}, {ID: "2l", Variant: map[string]string{"Size": "L"}, Stock: &l}}
		e := formServer(t, p)
		body, tok := e.form(t, "/a/ear-cleaner")
		if choose == "all" && (!strings.Contains(body, `name="pz" required`) || !strings.Contains(body, `value="2l">L</option>`)) {
			t.Fatalf("all: %s", body)
		}
		if choose == "each" && (!strings.Contains(body, `name="pz.3"`) || !strings.Contains(body, "Size for item 2")) {
			t.Fatalf("each: %s", body)
		}
		*e.now = e.now.Add(20 * time.Second)
		base := url.Values{"token": {tok}, "website": {""}, "name": {"Bola Ade"}, "phone": {"08031234567"}, "address": {"12 Admiralty Way"}, "zone": {"Yaba"}, "pkg": {"3"}}
		v := url.Values{}
		for k, x := range base {
			v[k] = x
		}
		if choose == "all" {
			// Three L: only one is left.
			v.Set("pz", "2l")
			if w := e.post("/a/ear-cleaner", v); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Not enough") {
				t.Fatalf("all, short: %d %s", w.Code, w.Body.String())
			}
			v.Set("pz", "2m")
		} else {
			v.Set("pz.1", "2m")
			v.Set("pz.2", "2l")
			if w := e.post("/a/ear-cleaner", v); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "for item 3") {
				t.Fatalf("each, one missing: %d %s", w.Code, w.Body.String())
			}
			v.Set("pz.3", "2m")
		}
		w := e.post("/a/ear-cleaner", v)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", choose, w.Code, w.Body.String())
		}
		ref := regexp.MustCompile(`^/o/([A-Z]{2}-[A-Z2-9]{6})\?k=`).FindStringSubmatch(w.Header().Get("Location"))[1]
		sub, _ := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
		var sum int64
		units := 0
		for _, l := range sub.Basket {
			sum += l.LineKobo
			units += l.Quantity
			if l.PackageQty != 3 || l.PackageKobo != 6500000 {
				t.Fatalf("%s: line %+v", choose, l)
			}
		}
		if sum != 6500000 || units != 3 || sub.TotalKobo != 6500000 || (choose == "all" && len(sub.Basket) != 1) || (choose == "each" && len(sub.Basket) != 2) {
			t.Fatalf("%s: %+v", choose, sub.Basket)
		}
	}
}

// A basket of some rows shows those only; ?item= keeps working for a
// form that takes any item.
func TestBasketItems(t *testing.T) {
	p := samplePage("classic", 4)
	form, _ := storefront.BasketForm("catalogue", []string{"3", "2"}, nil)
	id := uuid.New()
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Home", Actions: []storefront.PublicAction{{ID: &id, Slug: "some", Kind: "order",
		Label: "Order", Payment: "none", Pickup: true, Form: form}}}}
	e := formServer(t, p)
	body, _ := e.form(t, "/a/some?item=3")
	if !strings.Contains(body, `name="q.3" value="1"`) || !strings.Contains(body, `name="q.2"`) || strings.Contains(body, `name="q.4"`) || strings.Contains(body, "sell-one") {
		t.Fatalf("a basket of some: %s", body)
	}
}

// Every theme draws the packages field and its notice.
func TestPackagesInEveryTheme(t *testing.T) {
	for _, theme := range brand.Themes {
		e := formServer(t, packagesPage(nil, ""))
		withBrand(e.s, brand.Brand{Theme: theme, Color: "#c2410c"})
		e.h = e.s.Handler()
		body, _ := e.form(t, "/a/ear-cleaner")
		for _, want := range []string{`class="packages"`, `class="sell-one"`, `class="sell-notice"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", theme, want)
			}
		}
	}
}

// Embedded (§5.5): the frame posts its height and, once sent, the action
// and the reference to the verified websites, and nothing of the buyer.
func TestPackagesEmbedded(t *testing.T) {
	e := formServer(t, packagesPage(nil, ""))
	e.s.Origins = func(context.Context, uuid.UUID) ([]string, error) { return []string{"https://adakitchen.ng"}, nil }
	e.h = e.s.Handler()
	body, tok := e.form(t, "/embed/a/ear-cleaner")
	if !strings.Contains(body, `data-kind="storefront" data-origins="https://adakitchen.ng"`) || !strings.Contains(body, "form-embed.") {
		t.Fatalf("embedded form: %s", body)
	}
	*e.now = e.now.Add(20 * time.Second)
	w := e.post("/embed/a/ear-cleaner", url.Values{"token": {tok}, "website": {""}, "name": {"Bola Ade"}, "phone": {"08031234567"}, "address": {"12 Admiralty Way"},
		"zone": {"Yaba"}, "pkg": {"1"}})
	loc := w.Header().Get("Location")
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/embed/o/") || !strings.HasSuffix(loc, "&sent=ear-cleaner") {
		t.Fatalf("embedded order: %d %q", w.Code, loc)
	}
	sw := get(e.h, "GET", sfHost, loc)
	b := sw.Body.String()
	if sw.Code != 200 || !regexp.MustCompile(`data-submitted="[A-Z]{2}-[A-Z2-9]{6}" data-action="ear-cleaner"`).MatchString(b) || strings.Contains(b, "Bola") {
		t.Fatalf("the status page: %d %s", sw.Code, b)
	}
	// Opened again without sent: nothing more is told.
	if sw := get(e.h, "GET", sfHost, strings.TrimSuffix(loc, "&sent=ear-cleaner")); strings.Contains(sw.Body.String(), "data-submitted") {
		t.Fatal("told again")
	}
}
