package publicweb

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The brand screen's live preview (N13): the web app shows the real pages,
// drawn from each theme's own templates with sample content, in the look
// the owner is choosing, before it is saved. The api role seals what to
// show (Preview) with the key it shares with the public host, so only an
// owner signed in to the app can make one, for their own name and images,
// and only for a while.

// previewPrefix is where previews are served, on any public host:
// /_preview/<page>?p=<sealed Preview>.
const previewPrefix = "/_preview/"

// PreviewPages are the pages a preview shows.
var PreviewPages = []string{"storefront", "order", "booking", "form"}

// PreviewTTL is how long a preview's address works.
const PreviewTTL = time.Hour

// Preview is a look to show: a brand being chosen, the business's name,
// and its images' URLs.
type Preview struct {
	Theme      string    `json:"theme"`
	Color      string    `json:"color"`
	Name       string    `json:"name"`
	Logo       string    `json:"logo,omitempty"`
	LogoLarge  string    `json:"logo_large,omitempty"`
	Cover      string    `json:"cover,omitempty"`
	CoverLarge string    `json:"cover_large,omitempty"`
	Expires    time.Time `json:"expires"`
}

const previewAAD = "taskiem brand preview"

// PreviewPath seals p into the path of a page's preview.
func PreviewPath(keys forms.Keys, p Preview, page string) (string, error) {
	sealed, err := keys.Seal(previewAAD, p)
	if err != nil {
		return "", err
	}
	return previewPrefix + page + "?p=" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Server) servePreview(w http.ResponseWriter, r *http.Request) {
	page := strings.TrimPrefix(r.URL.Path, previewPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("p"))
	var p Preview
	if err != nil || s.FormKeys == nil || !contains(PreviewPages, page) || s.FormKeys.Open(previewAAD, raw, &p) != nil || s.now().After(p.Expires) {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	// Framed by the web app only.
	var app []string
	if u, err := url.Parse(s.AppURL); err == nil && u.Scheme != "" && u.Host != "" {
		app = []string{u.Scheme + "://" + u.Host}
	}
	httpsec.SetPublicEmbed(w.Header(), app)
	w.Header().Set("X-Robots-Tag", "noindex")
	l := lookOf(brand.Brand{Theme: p.Theme, Color: p.Color}, p.Name)
	l.Logo, l.LogoLarge, l.Cover, l.CoverLarge = p.Logo, p.LogoLarge, p.Cover, p.CoverLarge
	shop := SampleShop(p.Name)
	switch page {
	case "storefront":
		d := storefrontView(shop, s.now())
		d.Look = l
		d.Services = nil
		s.renderPrivate(w, r, http.StatusOK, "storefront.html", d)
	case "order":
		act, _ := findAction(shop.Model.Actions, "order")
		f := formView(shop, act, Site{}, false)
		f.Look, f.Token, f.Action = l, "preview", previewPrefix
		for i, it := range shop.Items {
			fi := formItem{N: i, ID: it.ID, Name: it.Name, Max: 20, Orderable: orderable(it), SoldOut: it.Stock != nil && *it.Stock == 0}
			if it.PriceKobo != nil {
				fi.Price, fi.PriceKobo = storefront.FormatNaira(*it.PriceKobo), *it.PriceKobo
			}
			if i == 1 {
				fi.Qty = 1
			}
			f.Items = append(f.Items, fi)
		}
		f.Pickup, f.Delivery, f.DeliveryNote = true, true, "Lekki and Victoria Island only"
		s.renderPrivate(w, r, http.StatusOK, "form.html", f)
	case "booking":
		d := bookingData{pageData: pageData{Title: "Book", Lang: "en", Look: l}, Business: p.Name, Label: "Book an appointment", Action: previewPrefix,
			Home: "/", Token: "preview", Honeypot: "website", Purpose: purposes["booking"], Day: "Saturday 24 October",
			Service: &serviceView{ID: "1", Name: "Knotless braids, medium", Length: "4 hr", Price: "₦28,000"}, Others: "/", Pay: "Pay at the appointment."}
		for i, day := range []string{"Thu 22 Oct", "Fri 23 Oct", "Sat 24 Oct", "Mon 26 Oct", "Tue 27 Oct"} {
			d.Days = append(d.Days, dayLink{Label: day, Href: "#", Current: i == 2})
		}
		for i, t := range []string{"09:00", "10:30", "12:00", "13:30", "15:00", "16:30"} {
			d.Slots = append(d.Slots, slotView{Value: fmt.Sprint(i), Label: t, Checked: i == 3})
		}
		s.renderPrivate(w, r, http.StatusOK, "booking.html", d)
	case "form":
		d := hformData{pageData: pageData{Title: "Join the waiting list", Lang: "en", Look: l}, Business: p.Name, Description: "Takes about a minute.",
			Action: previewPrefix, Token: "preview", Honeypot: "website", Step: "page", PageTitle: "About you", Last: true, Report: "preview",
			Purpose: "the waiting list"}
		d.Fields = []*field{
			{Name: "name", ID: "f-1", Label: "Full name", Widget: "text", Required: true},
			{Name: "phone", ID: "f-2", Label: "Phone", Widget: "tel", Required: true, Place: "0803 123 4567"},
			{Name: "size", ID: "f-3", Label: "Cake size", Widget: "radio", Required: true,
				Options: []option{{Value: "6", Label: "6 inch"}, {Value: "8", Label: "8 inch", Checked: true}, {Value: "10", Label: "10 inch"}}},
			{Name: "note", ID: "f-4", Label: "Anything else?", Widget: "textarea"},
		}
		s.renderPrivate(w, r, http.StatusOK, "hostedform.html", d)
	}
}

// SampleShop is a storefront with sample content under a business's
// name, for previews.
func SampleShop(name string) StorefrontPage {
	if name = strings.TrimSpace(name); name == "" {
		name = "Your business"
	}
	card := storefront.Card{Name: name, Tagline: "Celebration cakes, small chops and pastries, baked fresh every morning.",
		Area: "Lekki Phase 1, Lagos", Address: "14 Admiralty Way, Lekki Phase 1, Lagos", Phone: "+2348031234567", WhatsApp: "+2348031234567", Language: "en"}
	for d := range 6 {
		card.Hours.Days[d] = []storefront.Span{{Open: "08:00", Close: "19:00"}}
	}
	p := StorefrontPage{Status: "published", Model: storefront.Model{Card: card, MapLink: card.MapLink(), WhatsLink: card.WhatsAppLink(), HasCatalog: true,
		Actions: []storefront.PublicAction{{Kind: "order", Label: "Order now", Payment: "none"}, {Kind: "enquiry", Label: "Ask a question", Payment: "none"}}}}
	none := int64(0)
	for i, it := range []struct {
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
	} {
		k := it.kobo
		x := storefront.Item{ID: fmt.Sprint(i + 1), Name: it.name, Description: it.desc, Category: it.cat, PriceKobo: &k, Available: true}
		if it.sold {
			x.Stock = &none
		}
		p.Items = append(p.Items, x)
	}
	return p
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
