package publicweb

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// A One product front (docs/spec/forms-that-sell.md §5.4): the whole page
// of a pay-on-delivery seller, drawn from one catalogue row in the
// storefront's look: the promise line, the stock line, the headline and
// the reason to buy, the photos, the feature chips, the order form with
// its packages (the action's own, posting to its address), Order via
// WhatsApp under the button, and a price bar fixed to the bottom on a
// phone. Nothing here is a page builder: the owner sets words, photos and
// chips, never layout.

// oneProductData is the One product page's input: an order form's (the
// form drawn under the page) and the page's own parts.
type oneProductData struct {
	formData
	Promise  string
	Stock    string
	Headline string
	Reason   string
	Photos   []pic
	Chips    []chipView
	// PayAtDoor is the page's Pay on delivery mark.
	PayAtDoor bool
	// Price is the price bar's: the cheapest package, said "From" when
	// there are several; PriceNote what it is ("1 item").
	Price, PriceNote string
	// Ordering is true when the form is drawn; Shut says why it is not
	// (closed, paused, not open yet).
	Ordering bool
	Shut     string
	// Whats is Order via WhatsApp, with the product named in the first
	// message (the business's own number), or its chat link; WhatsLabel
	// its words.
	Whats, WhatsLabel string
	// Description, Image and URL are the page's link preview.
	Description, Image, URL string
}

// chipView is a feature chip as drawn: its icon (one of the fixed set,
// drawn here) and its words.
type chipView struct {
	Icon template.HTML
	Text string
}

// chipIcons are the chips' fixed icons (storefront.ChipIcons), stroked in
// the text's colour as the page's other icons are.
var chipIcons = map[string]string{
	"truck":   `<path d="M3 6h11v9H3z"/><path d="M14 9h4l3 3v3h-7"/><circle cx="7" cy="17" r="2"/><circle cx="17" cy="17" r="2"/>`,
	"cash":    `<rect x="3" y="6" width="18" height="12" rx="2"/><circle cx="12" cy="12" r="2.5"/><path d="M6 9v.01M18 15v.01"/>`,
	"shield":  `<path d="M12 3l8 3v6c0 4.5-3.4 8.2-8 9-4.6-.8-8-4.5-8-9V6l8-3z"/><path d="M9 12l2 2 4-4"/>`,
	"star":    `<path d="M12 3l2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3 6.4 20.2l1.1-6.2L3 9.6l6.2-.9L12 3z"/>`,
	"clock":   `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`,
	"check":   `<path d="M5 12l5 5L20 7"/>`,
	"gift":    `<rect x="3" y="8" width="18" height="5" rx="1"/><path d="M5 13v8h14v-8M12 8v13M12 8c-1.5-3-5-3-5-1s3 1 5 1c2 0 5 1 5-1s-3.5-2-5 1"/>`,
	"leaf":    `<path d="M5 19c0-9 6-14 15-14 0 9-5 15-14 15"/><path d="M5 19l8-8"/>`,
	"phone":   `<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/>`,
	"sparkle": `<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z"/><path d="M19 16l.7 1.8 1.8.7-1.8.7L19 21l-.7-1.8-1.8-.7 1.8-.7z"/>`,
	"heart":   `<path d="M12 20s-7-4.4-7-10a4 4 0 017-2.6A4 4 0 0119 10c0 5.6-7 10-7 10z"/>`,
	"return":  `<path d="M9 14l-4-4 4-4"/><path d="M5 10h9a5 5 0 010 10h-3"/>`,
}

// chipIcon is a chip's icon as an inline picture; an icon not of the set
// (none can be saved) draws as a tick.
func chipIcon(name string) template.HTML {
	p, ok := chipIcons[name]
	if !ok {
		p = chipIcons["check"]
	}
	return template.HTML(`<svg class="i" viewBox="0 0 24 24" aria-hidden="true">` + p + `</svg>`) //nolint:gosec // fixed paths, never the owner's
}

// oneProductAction is the front's order form that sells one product (the
// first one on); false when it has none, and the page lists its item as a
// front does.
func oneProductAction(f storefront.PublicFront) (storefront.PublicAction, bool) {
	for _, a := range f.Actions {
		if a.Kind == "order" && storefront.OneItem(storefront.EffectiveForm(a.Form, a.Kind)) != "" {
			return a, true
		}
	}
	return storefront.PublicAction{}, false
}

// serveOneProduct draws a One product front's page; false when the front
// has no order form selling one product on it now, and the front is drawn
// as any front is.
func (s *Server) serveOneProduct(w http.ResponseWriter, r *http.Request, site Site, p StorefrontPage, f storefront.PublicFront) bool {
	act, ok := oneProductAction(f)
	if !ok || p.Status == "suspended" {
		return false
	}
	base := "/" + f.Slug
	p.Items = p.Lists[f.ListTable()]
	p.Model.HasServices = false
	form := storefront.EffectiveForm(act.Form, act.Kind)
	it, found := findItem(p.Items, storefront.OneItem(form))
	if !found {
		return false
	}
	fd := formView(p, act, site, false, base)
	fd.Home = "/"
	fd.Look = s.lookFor(r.Context(), site.Tenant, fd.Business)
	d := oneProductData{formData: fd, Headline: f.Name, Reason: f.Tagline}
	d.Title = f.Name + " · " + p.Model.Card.Name
	if d.Reason == "" {
		// The rule CreateOneProduct made the front's line by: the item's
		// words, none when they hold a link.
		d.Reason = storefront.ReasonFrom(it.Description)
	}
	op := storefront.OneProductSettings{}
	if f.OneProduct != nil {
		op = *f.OneProduct
	}
	d.Promise = op.Promise
	if op.StockLine && it.Stock != nil && *it.Stock > 0 && it.Available {
		d.Stock = "Only " + strconv.FormatInt(*it.Stock, 10) + " left"
	}
	for _, c := range op.Chips {
		d.Chips = append(d.Chips, chipView{Icon: chipIcon(c.Icon), Text: c.Text})
	}
	if l := it.Photo["800"]; l != "" {
		d.Photos = append(d.Photos, pic{Small: it.Photo["320"], Large: l})
	} else if l := it.Photo["2000"]; l != "" {
		d.Photos = append(d.Photos, pic{Small: it.Photo["320"], Large: l})
	}
	x := item{Item: it}
	x.decorate()
	d.Photos = append(d.Photos, x.Pics...)
	d.PayAtDoor = act.Delivery && (act.PayOnDelivery || act.Payment == "none")
	if pf, ok := storefront.FormPackages(form); ok && len(pf.Packages) > 0 {
		low := pf.Packages[0]
		for _, pk := range pf.Packages[1:] {
			if pk.PriceKobo < low.PriceKobo {
				low = pk
			}
		}
		d.Price, d.PriceNote = storefront.FormatNaira(low.PriceKobo), storefront.PackageLabel(low.Qty, low.PriceKobo)
		if i := strings.Index(d.PriceNote, " · "); i > 0 {
			d.PriceNote = d.PriceNote[:i]
		}
		if len(pf.Packages) > 1 {
			d.Price = "From " + d.Price
		}
	} else if it.PriceKobo != nil {
		d.Price = storefront.FormatNaira(*it.PriceKobo)
	}
	if d.PayAtDoor {
		d.PriceNote = strings.TrimPrefix(d.PriceNote+" · pay on delivery", " · ")
	}
	switch {
	case p.Model.WhatsLink != "":
		d.Whats, d.WhatsLabel = p.Model.WhatsLink+"?text="+url.QueryEscape("Hello "+p.Model.Card.Name+", I want to order "+it.Name+"."), "Order via WhatsApp"
	case p.Model.ChatLink != "":
		d.Whats, d.WhatsLabel = p.Model.ChatLink, "Order in a chat"
	}
	d.Description = d.Reason
	if d.Description == "" {
		d.Description = p.Model.Card.Tagline
	}
	if r := []rune(d.Description); len(r) > 160 {
		d.Description = string(r[:157]) + "…"
	}
	if len(d.Photos) > 0 {
		d.Image = d.Photos[0].Large
	}
	d.URL = s.address(r, site, p) + base
	switch {
	case p.Status == "closed":
		d.Shut = p.Model.Card.Name + " is closed right now."
		if p.ClosedMessage != "" {
			d.Shut += " " + p.ClosedMessage
		}
	case p.OrdersPaused:
		d.Shut = p.Model.Card.Name + " is not taking orders online right now."
	case !s.formsOpen():
		d.Shut = "Orders are not open yet."
	case !it.Available || it.SoldOut():
		d.Shut = it.Name + " is sold out right now."
	}
	if d.Shut != "" {
		// Order via WhatsApp is ordering too: not while orders are not
		// taken.
		d.Whats, d.WhatsLabel = "", ""
		s.renderPrivate(w, r, http.StatusOK, "oneproduct.html", d)
		return true
	}
	d.Packages.choose(r.URL.Query())
	t, err := s.guard.Issue(d.form, s.now(), 0)
	if err != nil {
		s.Logger.Error("public host: form token", "err", err)
		s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
		return true
	}
	d.Token, d.Ordering = string(t), true
	if act.ID != nil && p.Followups[*act.ID] && s.FormKeys != nil && s.Keeps != nil {
		// A form that follows up a buyer who leaves (forms that sell §6):
		// the consent line, and the keep token, the submit token's twin.
		var named bool
		d.Consent, d.WAOptIn, named = s.followupWords(p, d.Lang)
		s.keepFor(&d.formData, named)
		if k, err := s.guard.Twin(t, d.form+keepSuffix); err == nil {
			d.Keep.Token = string(k)
		} else {
			d.Keep = nil
		}
	}
	s.renderPrivate(w, r, http.StatusOK, "oneproduct.html", d)
	return true
}
