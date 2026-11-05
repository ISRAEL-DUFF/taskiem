package publicweb

import (
	"context"
	"errors"
	"net/http"
	"regexp"

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
	x := item{Item: it, Small: it.Photo["320"], Large: it.Photo["800"], Orderable: orderable(it)}
	if x.Large == "" {
		x.Large = it.Photo["2000"]
	}
	x.decorate()
	if it.PriceKobo != nil {
		x.Price = storefront.FormatNaira(*it.PriceKobo)
	}
	d := itemData{pageData: pageData{Title: it.Name, Lang: c.Language, Look: s.lookFor(r.Context(), site.Tenant, c.Name)},
		Business: c.Name, Home: home, It: x, Options: formOptions(it, it.ID), Max: most(it), Whats: p.Model.WhatsLink, Phone: c.Phone}
	act, hasOrder := findAction(actions, "order")
	if hasOrder {
		d.Pickup, d.Delivery, d.DeliveryNote = act.Pickup, act.Delivery, act.DeliveryNote
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
	if d.Lang == "" {
		d.Lang = "en"
	}
	s.render(w, r, http.StatusOK, "item.html", d)
}
