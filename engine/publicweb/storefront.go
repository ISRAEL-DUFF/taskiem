package publicweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// StorefrontPage is what the public host renders for a storefront: its
// newest snapshot and catalogue copy.
type StorefrontPage struct {
	Status        string // published or closed
	Model         storefront.Model
	ClosedMessage string
	Items         []storefront.Item
}

// Storefronts reads a storefront's page; ok is false when it is not
// served (a draft, an inactive tenant).
type Storefronts func(ctx context.Context, id uuid.UUID) (page StorefrontPage, ok bool, err error)

// DBStorefronts reads pages with taskiem_public_storefront and
// taskiem_public_storefront_catalogue.
func DBStorefronts(pool *pgxpool.Pool) Storefronts {
	return func(ctx context.Context, id uuid.UUID) (StorefrontPage, bool, error) {
		var p StorefrontPage
		var version int
		var model []byte
		var closed *string
		err := pool.QueryRow(ctx, `SELECT status, version, model, closed_message FROM taskiem_public_storefront($1)`, id).
			Scan(&p.Status, &version, &model, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return StorefrontPage{}, false, nil
		}
		if err != nil {
			return StorefrontPage{}, false, err
		}
		if err := json.Unmarshal(model, &p.Model); err != nil {
			return StorefrontPage{}, false, err
		}
		if closed != nil {
			p.ClosedMessage = *closed
		}
		var items []byte
		if err := pool.QueryRow(ctx, `SELECT taskiem_public_storefront_catalogue($1)`, id).Scan(&items); err != nil {
			return StorefrontPage{}, false, err
		}
		if err := json.Unmarshal(items, &p.Items); err != nil {
			return StorefrontPage{}, false, err
		}
		return p, true, nil
	}
}

type pageEntry struct {
	page StorefrontPage
	ok   bool
}

// storefrontPage reads a page through a cache as short as the host
// cache's: an edit or a publish shows within 30 seconds on every replica.
func (s *Server) storefrontPage(ctx context.Context, id uuid.UUID) (StorefrontPage, bool, error) {
	if s.Storefronts == nil {
		return StorefrontPage{}, false, nil
	}
	if s.pages == nil {
		s.pages = lru.New[uuid.UUID, pageEntry](2000, HostTTL)
	}
	if e, ok := s.pages.Get(id); ok {
		return e.page, e.ok, nil
	}
	p, ok, err := s.Storefronts(ctx, id)
	if err != nil {
		return StorefrontPage{}, false, err
	}
	s.pages.Put(id, pageEntry{p, ok})
	return p, ok, nil
}

// storefrontData is the storefront template's input.
type storefrontData struct {
	pageData
	M           storefront.Model
	Closed      string
	Sections    []section
	OpenKnown   bool
	OpenNow     bool
	Hours       []hoursRow
	Phone       string
	Accent      string
	Description string
	Image       string
	HasItems    bool
	// Forms are the enabled forms' buttons; Ordering is true when the
	// order form is one, so items get an Add link.
	Forms    []storefront.PublicAction
	Ordering bool
	// Services are what can be booked, each with a Book link, when the
	// booking form is one of Forms.
	Services []serviceView
}

type section struct {
	Title string
	Items []item
}

type item struct {
	storefront.Item
	Price     string
	Small     string
	Large     string
	Orderable bool
}

type hoursRow struct {
	Day   string
	Spans string
	Today bool
}

var weekdays = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func (s *Server) serveStorefront(w http.ResponseWriter, r *http.Request, site Site) {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: storefront", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok {
		s.page(w, r, http.StatusOK, "setup", pageData{Title: "Coming soon"})
		return
	}
	d := storefrontView(p, s.now())
	if !s.formsOpen() {
		d.Forms, d.Ordering = nil, false
	}
	if _, ok := findAction(d.Forms, "booking"); ok {
		s.bookingOffer(r.Context(), site, p, &d)
	}
	s.render(w, r, http.StatusOK, "storefront.html", d)
}

// serveQR answers /qr.png: the storefront's address as a QR code, the
// same one the web app prints (storefront.QRPNG), for the owner to share
// from chat. It holds nothing but the address.
func (s *Server) serveQR(w http.ResponseWriter, r *http.Request, site Site) {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: storefront QR", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok || p.Model.Slug == "" {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	address := "https://" + hostOf(r)
	if s.PreviewDomain != "" {
		address = "https://" + p.Model.Slug + "." + strings.ToLower(s.PreviewDomain)
	}
	png, err := storefront.QRPNG(address)
	if err != nil {
		s.Logger.Error("public host: storefront QR", "storefront", site.ID, "err", err)
		s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(png) //nolint:gosec // a PNG made here from the storefront's own address, sent as image/png with nosniff
	}
}

func storefrontView(p StorefrontPage, now time.Time) storefrontData {
	c := p.Model.Card
	d := storefrontData{pageData: pageData{Title: c.Name, Lang: c.Language}, M: p.Model, Accent: c.Accent}
	if _, ok := storefront.Accents[d.Accent]; !ok {
		d.Accent = "green"
	}
	if p.Status == "closed" {
		d.Closed = p.ClosedMessage
	}
	d.OpenNow, d.OpenKnown = c.Hours.OpenNow(now)
	today := (int(now.In(storefront.Lagos).Weekday()) + 6) % 7
	if d.OpenKnown {
		for i, spans := range c.Hours.Days {
			row := hoursRow{Day: weekdays[i], Spans: "Closed", Today: i == today}
			if len(spans) > 0 {
				parts := make([]string, len(spans))
				for j, sp := range spans {
					parts[j] = sp.Open + "–" + sp.Close
				}
				row.Spans = strings.Join(parts, ", ")
			}
			d.Hours = append(d.Hours, row)
		}
	}
	if c.Phone != "" {
		d.Phone = c.Phone
	}
	d.Description = c.Tagline
	if d.Description == "" {
		d.Description = c.About
	}
	if len(d.Description) > 160 {
		d.Description = d.Description[:157] + "…"
	}
	if u := p.Model.Logo["800"]; u != "" {
		d.Image = u
	}
	if d.Closed != "" {
		return d
	}
	// Order first: it is the primary button.
	for _, k := range storefront.Kinds {
		if a, ok := findAction(p.Model.Actions, k); ok {
			d.Forms = append(d.Forms, a)
			d.Ordering = d.Ordering || k == "order"
		}
	}
	byTitle := map[string]int{}
	for _, it := range p.Items {
		x := item{Item: it, Small: it.Photo["320"], Large: it.Photo["800"], Orderable: orderable(it)}
		if x.Large == "" {
			x.Large = it.Photo["2000"]
		}
		if it.PriceKobo != nil {
			x.Price = storefront.FormatNaira(*it.PriceKobo)
		}
		i, ok := byTitle[it.Category]
		if !ok {
			i = len(d.Sections)
			byTitle[it.Category] = i
			d.Sections = append(d.Sections, section{Title: it.Category})
		}
		d.Sections[i].Items = append(d.Sections[i].Items, x)
		d.HasItems = true
	}
	return d
}

// bookingOffer lists the services on the page, or takes the booking form
// off it when nothing can be booked.
func (s *Server) bookingOffer(ctx context.Context, site Site, p StorefrontPage, d *storefrontData) {
	sp, ok, err := s.services(ctx, site.ID)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.Logger.Error("public host: services", "storefront", site.ID, "err", err)
	}
	if ok && err == nil && s.bookingsOpen() && p.Model.HasServices {
		for _, it := range bookable(sp.Services) {
			d.Services = append(d.Services, serviceView{ID: it.ID, Name: it.Name, Length: length(it.DurationMinutes), Price: servicePrice(it),
				Href: "/a/booking?service=" + url.QueryEscape(it.ID)})
		}
	}
	if len(d.Services) > 0 {
		return
	}
	forms := d.Forms[:0:0]
	for _, a := range d.Forms {
		if a.Kind != "booking" {
			forms = append(forms, a)
		}
	}
	d.Forms = forms
}
