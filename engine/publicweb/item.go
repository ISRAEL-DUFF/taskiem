package publicweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// itemPath and frontItemPath are an item's page: /i/<id> for an item on
// the home page, /<front>/i/<id> for one on a front's list. An item's id
// is its row's id in the catalogue copy.
var (
	itemPath      = regexp.MustCompile(`^/i/([A-Za-z0-9_-]{1,64})$`)
	frontItemPath = regexp.MustCompile(`^/([a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?)/i/([A-Za-z0-9_-]{1,64})$`)
)

// itemData is the item page's input.
type itemData struct {
	pageData
	Business string
	Home     string
	It       item
	Options  []formOption
	Max      int
	// Add is true when the item can go in a bag from here; Action is the
	// order form it goes to. Otherwise Note says why not (Whats and Phone
	// are where to ask).
	Add    bool
	Action string
	Note   string
	Whats  string
	Phone  string
	// Pickup, Delivery and DeliveryNote are how the order form hands
	// over, for the item's Delivery section.
	Pickup, Delivery bool
	DeliveryNote     string
	// Kicker is above the name: the item's section, and how long it
	// takes when it is a service ("Hair · 2 hours").
	Kicker string
	// Book is the booking form for a service ("Choose a time"), in place
	// of Add; DepositNote says what is paid now when there is a deposit.
	Book        string
	DepositNote string
	// Sizes are, on a product sold in sizes and colours, its choices to
	// pick one by; SizeData is them for the page's script (sizes.go).
	Sizes    []sizePick
	SizeData string
}

// duration says minutes as a person would: "45 minutes", "2 hours",
// "1 hour 30 minutes".
func duration(min int) string {
	h, m := min/60, min%60
	var parts []string
	switch {
	case h == 1:
		parts = append(parts, "1 hour")
	case h > 1:
		parts = append(parts, strconv.Itoa(h)+" hours")
	}
	if m > 0 {
		parts = append(parts, strconv.Itoa(m)+" minutes")
	}
	return strings.Join(parts, " ")
}

// serveItem answers an item's page: its photo, words, price and choices,
// with Add to bag when the page takes orders. front is the front whose
// list the item is on, "" for the home page.
func (s *Server) serveItem(w http.ResponseWriter, r *http.Request, site Site, front, id string) {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: item", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok || p.Status == "suspended" {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	items, actions, base, home := p.Items, p.Model.Home().Actions, "", "/"
	if front != "" {
		f, found := frontOf(p, front)
		if !found {
			s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
			return
		}
		items, actions, base, home = p.Lists[f.ListTable()], f.Actions, "/"+f.Slug, "/"+f.Slug
	}
	var it storefront.Item
	found := false
	for _, x := range items {
		if x.ID == id {
			it, found = x, true
			break
		}
	}
	if !found {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found", Look: s.lookFor(r.Context(), site.Tenant, p.Model.Card.Name)})
		return
	}
	c := p.Model.Card
	sized := len(it.Sizes) > 0
	x := item{Item: it, Small: it.Photo["320"], Large: it.Photo["800"], Orderable: orderable(it) || pickable(it)}
	if x.Large == "" {
		x.Large = it.Photo["2000"]
	}
	x.decorate()
	if it.PriceKobo != nil {
		x.Price = storefront.FormatNaira(*it.PriceKobo)
	}
	d := itemData{pageData: pageData{Title: it.Name, Lang: c.Language, Look: s.lookFor(r.Context(), site.Tenant, c.Name)},
		Business: c.Name, Home: home, It: x, Options: formOptions(it, it.ID), Max: most(it), Whats: p.Model.WhatsLink, Phone: c.Phone}
	if sized {
		d.Sizes, d.SizeData, d.Max = sizePicks(it), sizeData(it), maxQuantity
	}
	var kick []string
	if it.Category != "" {
		kick = append(kick, it.Category)
	}
	if it.DurationMinutes > 0 {
		kick = append(kick, duration(it.DurationMinutes))
	}
	d.Kicker = strings.Join(kick, " · ")
	act, hasOrder := findAction(actions, "order")
	if hasOrder {
		d.Pickup, d.Delivery, d.DeliveryNote = act.Pickup, act.Delivery, act.DeliveryNote
		d.BagHref = base + "/a/" + act.Ref()
	}
	hasBasket := hasOrder && storefront.HasWidget(storefront.EffectiveForm(act.Form, act.Kind), "basket")
	switch {
	case p.Status == "closed":
		d.Note = c.Name + " is closed right now."
	case p.OrdersPaused:
		d.Note = c.Name + " is not taking orders online right now."
	case !hasBasket || !s.formsOpen():
		d.Note = "Orders are not open on this page yet."
	case x.Orderable:
		d.Add, d.Action = true, base+"/a/"+act.Ref()
	}
	// A service is booked, not put in a bag: "Choose a time" opens the
	// booking form with it chosen.
	if book, ok := findAction(actions, "booking"); ok && it.DurationMinutes > 0 && it.Available && p.Status != "closed" && !p.OrdersPaused && s.formsOpen() {
		d.Add, d.Note, d.Book = false, "", base+"/a/"+book.Ref()+"?service="+url.QueryEscape(it.ID)
		if it.PriceKobo != nil && book.Payment != "none" {
			if dep := (storefront.ActionOptions{DepositPercent: book.DepositPercent}).OrderDeposit(*it.PriceKobo); dep > 0 {
				d.DepositNote = "Pay a " + storefront.FormatNaira(dep) + " deposit to hold your time. The rest is paid when you come."
			}
		}
	} else if d.Add && it.PriceKobo != nil && act.Payment != "none" {
		if dep := (storefront.ActionOptions{DepositPercent: act.DepositPercent}).OrderDeposit(*it.PriceKobo); dep > 0 {
			d.DepositNote = "Pay a " + strconv.Itoa(act.DepositPercent) + "% deposit now. The rest is paid when it is ready."
		}
	}
	if d.Lang == "" {
		d.Lang = "en"
	}
	s.render(w, r, http.StatusOK, "item.html", d)
}
