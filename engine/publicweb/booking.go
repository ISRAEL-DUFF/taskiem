package publicweb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The booking form (docs/spec/storefronts.md 5.4 and 6.9): GET /a/booking
// lists the services, then for one (?service=) the days with free times
// and one day's times (?day=), as links and radio buttons that work
// without JavaScript. POST holds the time and records the booking. The
// service's length, capacity and price always come from the services
// copy, never from the browser.

// Messages a booking form answers with.
const (
	msgSlotTaken  = "That time was just taken. Please choose another."
	msgPickSlot   = "Please choose one of the times shown."
	msgNoServices = "No services can be booked online right now."
)

// bookingData is the booking template's input: the booking form, or a
// booking's move page (Move).
type bookingData struct {
	pageData
	Business string
	Label    string
	Action   string // where the form posts
	Home     string // the storefront page; empty when embedded
	Back     string // the move page's way back to the booking
	Token    string
	Honeypot string
	Error    string
	Purpose  string
	Move     bool
	Current  string // the move page: the booking's time now
	form     string

	Services []serviceView // to choose from, before one is chosen
	Service  *serviceView
	Others   string // the link to choose another service
	Days     []dayLink
	Day      string // the chosen day, as shown
	NoTimes  string // why no time is offered
	Slots    []slotView
	Pay      string // how and when the customer pays
	Ahead    int

	Name, Phone, Email string
}

type serviceView struct {
	ID, Name, Length, Price, Href string
}

type dayLink struct {
	Label, Href string
	Current     bool
}

type slotView struct {
	Value, Label string
	Checked      bool
}

// length says how long a service takes: "30 min", "1 hr 30 min".
func length(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d hr", h)
	default:
		return fmt.Sprintf("%d hr %d min", h, m)
	}
}

// servicePrice is a service's price as shown.
func servicePrice(it storefront.Item) string {
	if it.PriceKobo == nil || *it.PriceKobo <= 0 {
		return "Price on the day"
	}
	return storefront.FormatNaira(*it.PriceKobo)
}

// bookable lists a services copy's services that can be booked, once each.
func bookable(items []storefront.Item) []storefront.Item {
	seen := map[string]bool{}
	var out []storefront.Item
	for _, it := range items {
		if _, ok := storefront.ServiceOf(it); ok && !seen[it.ID] {
			seen[it.ID] = true
			out = append(out, it)
		}
	}
	return out
}

func findService(items []storefront.Item, id string) (storefront.Item, storefront.Service, bool) {
	for _, it := range bookable(items) {
		if it.ID == id {
			svc, _ := storefront.ServiceOf(it)
			return it, svc, true
		}
	}
	return storefront.Item{}, storefront.Service{}, false
}

// payWords says how a booking is paid for.
func payWords(act storefront.PublicAction, it storefront.Item) string {
	price := int64(0)
	if it.PriceKobo != nil {
		price = *it.PriceKobo
	}
	switch {
	case act.Payment == "at_order" && price >= 100:
		dep := storefront.ActionOptions{DepositPercent: act.DepositPercent}.Deposit(price)
		if dep < price {
			return "Pay " + storefront.FormatNaira(dep) + " now to book, and the rest at the appointment."
		}
		return "Pay " + storefront.FormatNaira(price) + " now to book."
	case act.Payment == "after_accept" && price >= 100:
		return "Pay after we confirm."
	default:
		return "Pay at the appointment."
	}
}

func (s *Server) bookingsOpen() bool {
	return s.formsOpen() && s.Services != nil && s.Busy != nil && s.Book != nil
}

// window is the calendar's span that can be booked from now, for reading
// what is busy: from today to a day past the last date.
func window(a storefront.Availability, now time.Time) (time.Time, time.Time) {
	t := now.In(storefront.Lagos)
	from := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, storefront.Lagos)
	return from, from.AddDate(0, 0, a.AheadDays+2)
}

// picker fills a booking or move page's days and times for svc. day is
// the day asked for ("2026-10-24"), or empty for the first with a free
// time; own is the booking being moved, whose time is free to it.
func (s *Server) picker(ctx context.Context, sf uuid.UUID, d *bookingData, a storefront.Availability, svc storefront.Service, day string, own *BookingState, link func(day string) string) error {
	now := s.now()
	from, to := window(a, now)
	busy, err := s.Busy(ctx, sf, from, to)
	if err != nil {
		return err
	}
	if own != nil {
		busy = without(busy, own.ServiceID, own.Start)
	}
	var chosen []time.Time
	for _, date := range a.Dates(now) {
		slots := a.Slots(svc, date, now, busy)
		if own != nil {
			slots = drop(slots, own.Start)
		}
		if len(slots) == 0 {
			continue
		}
		key := date.Format(time.DateOnly)
		current := key == day || (day == "" && len(d.Days) == 0)
		if current {
			chosen = slots
			d.Day = date.Format("Mon 2 Jan")
		}
		d.Days = append(d.Days, dayLink{Label: date.Format("Mon 2 Jan"), Href: link(key), Current: current})
	}
	d.Ahead = a.AheadDays
	switch {
	case len(d.Days) == 0:
		d.NoTimes = fmt.Sprintf("There are no free times in the next %d days. Please contact %s directly.", a.AheadDays, d.Business)
	case chosen == nil:
		d.NoTimes = "There are no free times on that day. Please choose another."
	}
	for _, t := range chosen {
		d.Slots = append(d.Slots, slotView{Value: strconv.FormatInt(t.Unix(), 10), Label: t.In(storefront.Lagos).Format("15:04")})
	}
	return nil
}

// without is busy less one booking at start: a booking being moved does
// not stand in its own way.
func without(busy []storefront.Busy, service string, start time.Time) []storefront.Busy {
	out := make([]storefront.Busy, 0, len(busy))
	done := false
	for _, b := range busy {
		if !done && b.Service == service && b.Start.Equal(start) {
			done = true
			if b.Count <= 1 {
				continue
			}
			b.Count--
		}
		out = append(out, b)
	}
	return out
}

func drop(slots []time.Time, t time.Time) []time.Time {
	out := slots[:0:0]
	for _, x := range slots {
		if !x.Equal(t) {
			out = append(out, x)
		}
	}
	return out
}

// check marks the slot chosen, or says it is not shown.
func (d *bookingData) check(start string) bool {
	for i := range d.Slots {
		if d.Slots[i].Value == start {
			d.Slots[i].Checked = true
			return true
		}
	}
	return false
}

func (s *Server) serveBooking(w http.ResponseWriter, r *http.Request, site Site, p StorefrontPage, act storefront.PublicAction, embed bool) {
	sp, ok, err := s.services(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: services", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	c := p.Model.Card
	d := bookingData{pageData: pageData{Title: act.Label, Lang: c.Language}, Business: c.Name, Label: act.Label, Action: "/a/booking", Home: "/",
		Honeypot: formguard.HoneypotField, Purpose: purposes["booking"], form: formID(site.ID, "booking")}
	d.Look = s.lookFor(r.Context(), site.Tenant, c.Name)
	prefix := ""
	if embed {
		prefix, d.Action, d.Home = "/embed", "/embed/a/booking", ""
	}
	if d.Lang == "" {
		d.Lang = "en"
	}
	var items []storefront.Item
	if ok {
		items = bookable(sp.Services)
	}
	a := sp.Availability.Effective(c.Hours)

	var v url.Values
	id, day := r.URL.Query().Get("service"), r.URL.Query().Get("day")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
		if err := r.ParseForm(); err != nil {
			d.Error = "This form could not be read. Please check your details and send again."
			s.showBooking(w, r, http.StatusBadRequest, d, "")
			return
		}
		v = r.PostForm
		id = v.Get("service")
		d.Name, d.Phone, d.Email = v.Get("name"), v.Get("phone"), v.Get("email")
		if t, err := strconv.ParseInt(v.Get("start"), 10, 64); err == nil {
			day = time.Unix(t, 0).In(storefront.Lagos).Format(time.DateOnly)
		}
	}
	if len(items) == 1 && id == "" {
		id = items[0].ID
	}
	it, svc, found := findService(items, id)
	if !found {
		if r.Method == http.MethodPost {
			d.Error = "Please choose a service."
		}
		for _, x := range items {
			d.Services = append(d.Services, serviceView{ID: x.ID, Name: x.Name, Length: length(x.DurationMinutes), Price: servicePrice(x),
				Href: prefix + "/a/booking?service=" + url.QueryEscape(x.ID)})
		}
		if len(d.Services) == 0 {
			d.NoTimes = msgNoServices
		}
		s.showBooking(w, r, http.StatusOK, d, "")
		return
	}
	d.Service = &serviceView{ID: it.ID, Name: it.Name, Length: length(it.DurationMinutes), Price: servicePrice(it)}
	if len(items) > 1 {
		d.Others = prefix + "/a/booking"
	}
	d.Pay = payWords(act, it)
	link := func(day string) string {
		return prefix + "/a/booking?service=" + url.QueryEscape(it.ID) + "&day=" + day
	}
	if err := s.picker(r.Context(), site.ID, &d, a, svc, day, nil, link); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: busy", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if r.Method != http.MethodPost {
		s.showBooking(w, r, http.StatusOK, d, "")
		return
	}
	s.submitBooking(w, r, site, p, act, d, v, it, svc, a, embed)
}

func (s *Server) showBooking(w http.ResponseWriter, r *http.Request, status int, d bookingData, token string) {
	if token == "" && len(d.Slots) > 0 {
		t, err := s.guard.Issue(d.form, s.now(), 0)
		if err != nil {
			s.Logger.Error("public host: form token", "err", err)
			s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		token = string(t)
	}
	d.Token = token
	s.renderPrivate(w, r, status, "booking.html", d)
}

func (s *Server) submitBooking(w http.ResponseWriter, r *http.Request, site Site, p StorefrontPage, act storefront.PublicAction, d bookingData, v url.Values,
	it storefront.Item, svc storefront.Service, a storefront.Availability, embed bool) {
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		d.Error = "Too many forms have been sent from this connection. Please wait a few minutes and send again."
		d.check(v.Get("start"))
		s.showBooking(w, r, http.StatusTooManyRequests, d, v.Get(tokenField))
		return
	}
	if formguard.HoneypotTripped(v) {
		d.Error = "This form could not be sent. Please check your details and send again."
		s.showBooking(w, r, http.StatusBadRequest, d, "")
		return
	}
	claims, err := s.guard.Verify(v.Get(tokenField), d.form, s.now())
	switch {
	case errors.Is(err, formguard.ErrTooFast):
		d.Error = "Please check your details and send again."
		d.check(v.Get("start"))
		s.showBooking(w, r, http.StatusUnprocessableEntity, d, v.Get(tokenField))
		return
	case errors.Is(err, formguard.ErrExpired):
		d.Error = "This page was open for a long time. Please check your details and send again."
		s.showBooking(w, r, http.StatusUnprocessableEntity, d, "")
		return
	case err != nil:
		d.Error = "This form could not be sent. Please check your details and send again."
		s.showBooking(w, r, http.StatusBadRequest, d, "")
		return
	}
	f := formData{Kind: "booking", Business: d.Business, Name: d.Name, Phone: d.Phone, Email: d.Email}
	sub, msg := f.check(v, nil)
	d.Name, d.Phone, d.Email = f.Name, f.Phone, f.Email
	if msg != "" {
		d.Error = msg
		d.check(v.Get("start"))
		s.showBooking(w, r, http.StatusUnprocessableEntity, d, v.Get(tokenField))
		return
	}
	// The time must be one the calendar offers. Whether it is still free
	// is the database's to say, under the storefront's lock: a repeat of
	// this same booking finds its own hold there and gets its reference.
	t, err := strconv.ParseInt(v.Get("start"), 10, 64)
	start := time.Unix(t, 0)
	if err != nil || !a.Offers(svc, start, s.now(), nil) {
		d.Error = msgPickSlot
		s.showBooking(w, r, http.StatusUnprocessableEntity, d, v.Get(tokenField))
		return
	}
	sub.Booking = &storefront.Booked{ServiceID: it.ID, ServiceName: it.Name, Start: start.UTC(), End: start.Add(svc.Duration).UTC(),
		Until: start.Add(svc.Duration + svc.Buffer).UTC(), Capacity: svc.Capacity}
	if it.PriceKobo != nil && *it.PriceKobo > 0 {
		sub.TotalKobo = *it.PriceKobo
		if act.DepositPercent > 0 && act.Payment != "none" {
			sub.DepositKobo = storefront.ActionOptions{DepositPercent: act.DepositPercent}.Deposit(sub.TotalKobo)
		}
	}
	idem := claims.IdempotencyKey()
	sub.StatusKey = s.statusKey(idem)
	sub.Payment = act.Payment
	sub.Channel = "direct"
	if embed {
		sub.Channel = "embed"
	}
	sub.Submitted = s.now().UTC()
	ref, ok, err := s.recordBooking(r.Context(), site.ID, p.Model.Slug, idem, sub)
	switch {
	case errors.Is(err, ErrSlotTaken):
		// Fresh times, keeping what was typed; a new token, since the
		// next send is another booking.
		d.Error = msgSlotTaken
		d.Days, d.Slots, d.Day, d.NoTimes = nil, nil, "", ""
		prefix := ""
		if embed {
			prefix = "/embed"
		}
		link := func(day string) string {
			return prefix + "/a/booking?service=" + url.QueryEscape(it.ID) + "&day=" + day
		}
		if err := s.picker(r.Context(), site.ID, &d, a, svc, start.In(storefront.Lagos).Format(time.DateOnly), nil, link); err != nil {
			s.Logger.Error("public host: busy", "storefront", site.ID, "err", err)
		}
		s.showBooking(w, r, http.StatusConflict, d, "")
		return
	case err != nil:
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: book", "storefront", site.ID, "err", err)
		}
		d.Error = "Something went wrong on our side. Please send again."
		d.check(v.Get("start"))
		s.showBooking(w, r, http.StatusServiceUnavailable, d, v.Get(tokenField))
		return
	case !ok:
		s.renderPrivate(w, r, http.StatusConflict, "formclosed.html", closedData{pageData: pageData{Title: "Closed", Lang: d.Lang, Look: d.Look}, Business: d.Business,
			Message: d.Business + " is not taking bookings right now.", Home: d.Home})
		return
	}
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	http.Redirect(w, r, statusLink(prefix, ref, sub.StatusKey, ""), http.StatusSeeOther) //nolint:gosec // G710: statusLink is a path on this host (TestStatusLinkStaysOnThisHost)
}

// recordBooking seals a booking and holds its slot, as record does for
// the other forms.
func (s *Server) recordBooking(ctx context.Context, sf uuid.UUID, slug string, idem [32]byte, sub storefront.Submission) (string, bool, error) {
	hash := storefront.StatusHash(sub.StatusKey)
	for range 3 {
		ref := storefront.NewReference(slug)
		payload, err := storefront.SealSubmission(s.InboxKey, sf, ref, sub)
		if err != nil {
			return "", false, err
		}
		got, ok, err := s.Book(ctx, sf, ref, idem[:], hash, payload, *sub.Booking)
		if errors.Is(err, ErrReferenceTaken) {
			continue
		}
		return got, ok, err
	}
	return "", false, ErrReferenceTaken
}

// statusLink is a status page's address, or one of its change pages'.
// statusLink is a status page's path on this host: prefix is "" or
// "/embed", and the reference is path-escaped, so no reference makes it
// "//host" or a URL with a scheme.
func statusLink(prefix, ref, key, page string) string {
	p := prefix + "/o/" + url.PathEscape(ref)
	if page != "" {
		p += "/" + page
	}
	return p + "?k=" + url.QueryEscape(key)
}

// liveBooking reports whether a booking still holds its time.
func liveBooking(status string) bool {
	switch status {
	case "received", "accepted", "awaiting_payment", "paid":
		return true
	}
	return false
}

// changeable reports whether a live booking may still be changed online:
// up to cutoff minutes before it starts, as taskiem_public_change checks.
func changeable(start time.Time, cutoff int, now time.Time) bool {
	return !now.After(start.Add(-time.Duration(cutoff) * time.Minute))
}
