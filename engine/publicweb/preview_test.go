package publicweb

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// TestWritePreview draws the main pages in every theme into the folder
// TASKIEM_PREVIEW_DIR names, with the files they load, for a person to
// look at: serve the folder (python3 -m http.server) and open
// /index.html. Skipped without it.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("TASKIEM_PREVIEW_DIR")
	if dir == "" {
		t.Skip("TASKIEM_PREVIEW_DIR is not set")
	}
	color := os.Getenv("TASKIEM_PREVIEW_COLOR")
	write := func(name string, body []byte) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, a := range assets {
		write("_static/"+name, a.body)
	}
	links := regexp.MustCompile(`/_brand/1/([0-9a-f]{6})\.css`)
	var index strings.Builder
	index.WriteString("<!doctype html><meta charset=utf-8><title>Themes</title><h1>Themes</h1>")
	for _, theme := range brand.Themes {
		b := brand.Brand{Theme: theme, Color: color}
		pages := map[string]string{}

		p := previewShop()
		e := formServer(t, p)
		withBrand(e.s, b)
		e.s.Storefronts = func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil }
		pages["storefront"] = get(e.h, "GET", sfHost, "/").Body.String()
		pages["order"], _ = e.form(t, "/a/order?item=2")
		pages["enquiry"], _ = e.form(t, "/a/enquiry")

		be, _ := bookingEnv(t, "after_accept", 0)
		withBrand(be.s, b)
		pages["services"] = get(be.h, "GET", sfHost, "/").Body.String()
		pages["booking"], _ = be.form(t, "/a/booking?service=7")

		h := newFormHost(t)
		withBrand(h.s, b)
		h.add("loan00000001", loanTrigger, loanSchema)
		h.add("widgets00001", `{"title":"Everything"}`, widgetSchema)
		pages["hosted-form"] = h.get("/f/loan00000001").Body.String()
		pages["hosted-widgets"] = h.get("/f/widgets00001").Body.String()
		pages["notfound"] = get(e.h, "GET", "nobody.shops.test", "/").Body.String()

		index.WriteString("<h2>" + theme + "</h2><ul>")
		for name, body := range pages {
			for _, m := range links.FindAllStringSubmatch(body, -1) {
				write("_brand/1/"+m[1]+".css", []byte(brand.PaletteOf("#"+m[1]).CSS()))
			}
			write(theme+"/"+name+".html", []byte(body))
			index.WriteString(`<li><a href="` + theme + "/" + name + `.html">` + name + "</a>")
		}
		index.WriteString("</ul>")
	}
	write("index.html", []byte(index.String()))
}

// previewShop is a storefront with a catalogue in sections, as a real one
// would look.
func previewShop() StorefrontPage {
	p := samplePage("classic", 0)
	p.Model.Card.Name = "Adunni Bakes"
	p.Model.Card.Tagline = "Celebration cakes, small chops and pastries, baked fresh every morning."
	p.Model.Card.Area = "Lekki Phase 1, Lagos"
	p.Model.Card.Address = "14 Admiralty Way, Lekki Phase 1, Lagos"
	p.Model.Actions = allForms()
	items := []struct {
		name, desc, cat string
		kobo            int64
		sold            bool
	}{
		{"Party pack, 50 pieces", "Puff-puff, samosa, spring rolls, chicken and gizdodo.", "Small chops", 2500000, false},
		{"Puff-puff, 20 pieces", "Soft, golden, dusted with sugar.", "Small chops", 350000, false},
		{"Samosa and spring rolls, 12", "Six of each, with sweet chilli dip.", "Small chops", 600000, false},
		{"Red velvet, 8 inch", "Cream cheese frosting. Order a day ahead.", "Cakes", 3800000, false},
		{"Chocolate fudge, 8 inch", "Back on Saturday.", "Cakes", 3500000, true},
		{"Meat pie, box of 6", "Minced beef, potato and carrot.", "Pastries", 480000, false},
	}
	none := int64(0)
	for i, it := range items {
		x := storefront.Item{ID: string(rune('1' + i)), Name: it.name, Description: it.desc, Category: it.cat, PriceKobo: kobo(it.kobo), Available: true}
		if it.sold {
			x.Stock = &none
		}
		p.Items = append(p.Items, x)
	}
	return p
}
