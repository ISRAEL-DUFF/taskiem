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
	Status        string // published, closed or suspended
	Model         storefront.Model
	ClosedMessage string
	Items         []storefront.Item
	// Verified: the business was checked, or onboarded by a field agent
	// (the badge). OrdersPaused: orders and bookings are not taken now
	// (the plan's month, or the unverified daily limit).
	Verified     bool
	OrdersPaused bool
	// Lists are the copies of the other fronts' lists, by table (fronts
	// F3, §5.2).
	Lists map[string][]storefront.Item
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
		var closed, paused *string
		err := pool.QueryRow(ctx, `SELECT status, version, model, closed_message, verified, orders_paused FROM taskiem_public_storefront($1)`, id).
			Scan(&p.Status, &version, &model, &closed, &p.Verified, &paused)
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
		p.OrdersPaused = paused != nil
		if p.Status == "suspended" {
			return p, true, nil // nothing of it is shown
		}
		var items []byte
		if err := pool.QueryRow(ctx, `SELECT taskiem_public_storefront_catalogue($1)`, id).Scan(&items); err != nil {
			return StorefrontPage{}, false, err
		}
		if err := json.Unmarshal(items, &p.Items); err != nil {
			return StorefrontPage{}, false, err
		}
		// Each other front's list, its table read once.
		for _, f := range p.Model.Fronts {
			if f.Slug == "" {
				continue
			}
			for _, t := range f.ListTables() {
				if _, ok := p.Lists[t]; ok {
					continue
				}
				var raw []byte
				if err := pool.QueryRow(ctx, `SELECT taskiem_public_storefront_list($1, $2)`, id, t).Scan(&raw); err != nil {
					return StorefrontPage{}, false, err
				}
				var list []storefront.Item
				if err := json.Unmarshal(raw, &list); err != nil {
					return StorefrontPage{}, false, err
				}
				if p.Lists == nil {
					p.Lists = map[string][]storefront.Item{}
				}
				p.Lists[t] = list
			}
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
	Verified    bool
	Sections    []section
	OpenKnown   bool
	OpenNow     bool
	Hours       []hoursRow
	Phone       string
	Description string
	Image       string
	// URL is the page's public address (og:url and canonical).
	URL      string
	HasItems bool
	// Forms are the enabled forms' buttons; Ordering is true when the
	// order form is one, so items get an Add link.
	Forms    []storefront.PublicAction
	Ordering bool
	// A front's page (fronts F3): the front, the prefix of its actions'
	// addresses (Base, "" on the home page) and the way home. The home
	// page lists the other fronts.
	Front  *storefront.PublicFront
	Base   string
	Home   string
	Fronts []frontCard
	// Services are what can be booked, each with a Book link, when the
	// booking form is one of Forms.
	Services []serviceView
	// Search is what the visitor looked for (?q=), Found how many items
	// matched: the catalogue is narrowed to them.
	Search string
	Found  int
}

// searchTerm is the visitor's search from ?q=, trimmed and kept short.
func searchTerm(r *http.Request) string {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if rs := []rune(q); len(rs) > 40 {
		q = string(rs[:40])
	}
	return q
}

// search narrows the page's sections to the items whose name, words or
// category hold q, ignoring case; an empty q leaves them all.
func (d *storefrontData) search(q string) {
	if q == "" {
		return
	}
	d.Search = q
	needle := strings.ToLower(q)
	var out []section
	for _, sec := range d.Sections {
		var keep []item
		for _, it := range sec.Items {
			if strings.Contains(strings.ToLower(it.Name+" "+it.Description+" "+it.Category), needle) {
				keep = append(keep, it)
			}
		}
		if len(keep) > 0 {
			out = append(out, section{Title: sec.Title, Items: keep})
			d.Found += len(keep)
		}
	}
	d.Sections = out
}

// frontCard is another front as the home page lists it.
type frontCard struct {
	Name, Tagline, Href, Small string
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
	// Pics are the item's photos after the first, each a thumbnail and
	// the full picture; Few is true when only a handful are left.
	Pics []pic
	Few  bool
	// Max is the most one order may take (the stock left, or the cap), for
	// a card's own quantity.
	Max int
}

// pic is one photo of an item's gallery.
type pic struct{ Small, Large string }

// fewLeft is the stock at or under which an item says how few are left.
const fewLeft = 5

// decorate fills what an item shows beyond its photo and price.
func (x *item) decorate() {
	for _, g := range x.Gallery {
		l := g["800"]
		if l == "" {
			l = g["2000"]
		}
		sm := g["320"]
		if sm == "" {
			sm = l
		}
		if l != "" {
			x.Pics = append(x.Pics, pic{Small: sm, Large: l})
		}
	}
	x.Few = x.Available && x.Stock != nil && *x.Stock > 0 && *x.Stock <= fewLeft
	x.Max = most(x.Item)
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
		s.page(w, r, http.StatusOK, "setup", pageData{Title: "Coming soon", Look: s.lookFor(r.Context(), site.Tenant, "")})
		return
	}
	d := storefrontView(p, s.now())
	d.Look = s.lookFor(r.Context(), site.Tenant, p.Model.Card.Name)
	if d.Look.LogoLarge != "" {
		d.Image = d.Look.LogoLarge
	}
	d.URL = s.address(r, site, p)
	q := searchTerm(r)
	d.search(q)
	if !s.formsOpen() {
		d.Forms, d.Ordering = nil, false
	}
	if _, ok := findAction(d.Forms, "booking"); ok {
		s.bookingOffer(r.Context(), site, p, &d)
	}
	if d.Closed == "" {
		for _, f := range p.Model.Fronts {
			if f.Slug != "" {
				d.Fronts = append(d.Fronts, frontCard{Name: f.Name, Tagline: f.Tagline, Href: "/" + f.Slug})
			}
		}
	}
	if q != "" {
		s.renderPrivate(w, r, http.StatusOK, "storefront.html", d)
		return
	}
	s.render(w, r, http.StatusOK, "storefront.html", d)
}

// frontOf is the published front at an address (false when there is none,
// or it is off: an off front is not in the snapshot).
func frontOf(p StorefrontPage, slug string) (storefront.PublicFront, bool) {
	for _, f := range p.Model.Fronts {
		if f.Slug == slug && slug != "" {
			return f, true
		}
	}
	return storefront.PublicFront{}, false
}

// serveFront answers /<slug>: a front's page (fronts F3, §7.3), the
// business's card above the front's own header, list and buttons.
func (s *Server) serveFront(w http.ResponseWriter, r *http.Request, site Site, slug string) {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: storefront", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	f, found := frontOf(p, slug)
	if !ok || !found || p.Status == "suspended" {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	front := p
	front.Items = p.Lists[f.ListTable()]
	front.Model.HasServices = false
	d := storefrontView(front, s.now())
	d.Title = f.Name + " · " + p.Model.Card.Name
	d.Front, d.Base, d.Home = &f, "/"+f.Slug, "/"
	d.Forms, d.Ordering = nil, false
	if d.Closed == "" {
		for _, a := range f.Actions {
			d.Forms = append(d.Forms, a)
			d.Ordering = d.Ordering || a.Ref() == "order"
		}
	}
	d.Look = s.lookFor(r.Context(), site.Tenant, p.Model.Card.Name)
	d.URL = s.address(r, site, p) + "/" + f.Slug
	q := searchTerm(r)
	d.search(q)
	if f.Tagline != "" {
		d.Description = f.Tagline
	}
	if !s.formsOpen() {
		d.Forms, d.Ordering = nil, false
	}
	if q != "" {
		s.renderPrivate(w, r, http.StatusOK, "storefront.html", d)
		return
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
	png, err := storefront.QRPNG(s.address(r, site, p))
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

// address is the page's public address: its own primary domain, else the
// preview address, else the host asked for.
func (s *Server) address(r *http.Request, site Site, p StorefrontPage) string {
	switch {
	case site.Primary != "":
		return "https://" + site.Primary
	case s.PreviewDomain != "" && p.Model.Slug != "":
		return "https://" + p.Model.Slug + "." + strings.ToLower(s.PreviewDomain)
	}
	return "https://" + hostOf(r)
}

func storefrontView(p StorefrontPage, now time.Time) storefrontData {
	c := p.Model.Card
	d := storefrontData{pageData: pageData{Title: c.Name, Lang: c.Language}, M: p.Model}
	if p.Status == "closed" {
		d.Closed = p.ClosedMessage
	}
	d.Verified = p.Verified
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
	for _, k := range storefront.BuiltinKinds {
		if a, ok := findAction(p.Model.Home().Actions, k); ok {
			d.Forms = append(d.Forms, a)
			d.Ordering = d.Ordering || k == "order"
		}
	}
	// Then the other actions on (fronts F2), in their order.
	for _, a := range p.Model.Home().Actions {
		if a.Ref() != a.Kind {
			d.Forms = append(d.Forms, a)
		}
	}
	byTitle := map[string]int{}
	for _, it := range p.Items {
		x := item{Item: it, Small: it.Photo["320"], Large: it.Photo["800"], Orderable: orderable(it)}
		if x.Large == "" {
			x.Large = it.Photo["2000"]
		}
		x.decorate()
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
