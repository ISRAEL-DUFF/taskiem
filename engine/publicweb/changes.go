package publicweb

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The change pages under a status link (docs/spec/storefronts.md 6.9 and
// 8.1): /o/{reference}/move and /o/{reference}/cancel for a booking, up to
// the business's cut-off, and /o/{reference}/prices to confirm or refuse
// new prices. The status key is the only credential, as for the status
// page; every change is a POST with a form token and the honeypot, and
// the database checks again what the page offered.

// changeForm is the formguard form id of one change to one reference.
func changeForm(sf uuid.UUID, ref, page string) string {
	return "sf:" + sf.String() + "/o/" + ref + "/" + page
}

func (s *Server) changesOpen() bool { return s.guard != nil && s.Change != nil }

// priceWords is the status page of an order or booking whose prices
// changed before it went ahead: the new total, and buttons to take it or
// cancel.
func (s *Server) priceWords(ctx context.Context, d *statusData, site Site, ref, key string, o OrderState, prefix string) error {
	d.Headline = "Prices have changed"
	offer, known := PriceOffer{}, false
	if s.PriceChange != nil {
		var err error
		if offer, known, err = s.PriceChange(ctx, ref, storefront.StatusHash(key)); err != nil {
			return err
		}
	}
	accept := true
	if known {
		d.Lines = []string{d.Business + "'s prices changed after you sent this."}
		if len(offer.Gone) > 0 {
			d.Lines = append(d.Lines, "No longer available: "+strings.Join(offer.Gone, ", ")+".")
		}
		if (o.Kind == "booking" && len(offer.Gone) > 0) || offer.TotalKobo <= 0 {
			accept = false
			d.Lines = append(d.Lines, "Nothing in it can be had now, so it can only be cancelled.")
		} else {
			line := "The new total is " + storefront.FormatNaira(offer.TotalKobo) + "."
			if offer.DepositKobo > 0 && offer.DepositKobo < offer.TotalKobo {
				line += " You pay " + storefront.FormatNaira(offer.DepositKobo) + " now."
			}
			d.Lines = append(d.Lines, line)
		}
	} else {
		d.Lines = []string{d.Business + "'s prices changed; the new total was sent to you by SMS."}
	}
	if !s.changesOpen() {
		return nil
	}
	d.Lines = append(d.Lines, "Please confirm or cancel within a day.")
	if d.Token == "" {
		t, err := s.guard.Issue(changeForm(site.ID, ref, "prices"), s.now(), 0)
		if err != nil {
			return err
		}
		d.Token = string(t)
	}
	action := statusLink(prefix, ref, key, "prices")
	if accept {
		d.Buttons = append(d.Buttons, changeButton{Action: action, Do: "accept", Label: "Confirm new total", Primary: true})
	}
	d.Buttons = append(d.Buttons, changeButton{Action: action, Do: "decline", Label: "Cancel"})
	return nil
}

func (s *Server) serveChange(w http.ResponseWriter, r *http.Request, site Site, ref, page string, embed bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	if !s.changesOpen() {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	key := r.URL.Query().Get("k")
	o, ok := s.statusOf(w, r, site, ref, key)
	if !ok {
		return
	}
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	back := statusLink(prefix, ref, key, "")
	if page == "prices" {
		s.servePrices(w, r, site, ref, key, o, embed, back)
		return
	}
	if o.Kind != "booking" || s.Booking == nil {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	b, ok, err := s.Booking(r.Context(), ref, storefront.StatusHash(key))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: booking status", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok || b.Storefront != site.ID {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	d, p := s.statusHead(r.Context(), site, ref, embed)
	cutoff, known := s.cutoff(r.Context(), site, p)
	switch {
	case !known:
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	case !liveBooking(b.Status):
		s.changeOutcome(w, r, d, ChangeNotAllowed, back)
		return
	case !changeable(b.Start, cutoff, s.now()):
		s.changeOutcome(w, r, d, ChangeTooLate, back)
		return
	}
	if page == "cancel" {
		s.serveCancel(w, r, site, d, b, ref, key, cutoff, back)
		return
	}
	s.serveMove(w, r, site, p, d, b, ref, key, cutoff, back)
}

// changeOutcome is the page for a change that cannot be made.
func (s *Server) changeOutcome(w http.ResponseWriter, r *http.Request, d statusData, outcome, back string) {
	switch outcome {
	case ChangeTooLate:
		d.Headline = "Too late to change online"
		d.Lines = []string{tooLate(d.Business)}
	default:
		d.Headline = "This can no longer be changed"
		d.Lines = []string{"Contact " + d.Business + " directly if you need to change it."}
	}
	d.Title = d.Headline
	d.Links = []statusLinkView{{Href: back, Label: "Back"}}
	s.renderPrivate(w, r, http.StatusConflict, "status.html", d)
}

// guardChange checks a change's POST as submitForm checks a form: size,
// the submission limit, the honeypot and the token. On a refusal msg says
// why and keep is the token to send back when it may be used again.
func (s *Server) guardChange(w http.ResponseWriter, r *http.Request, form string) (v url.Values, status int, msg, keep string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		return nil, http.StatusBadRequest, "This could not be read. Please try again.", ""
	}
	v = r.PostForm
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		return v, http.StatusTooManyRequests, "Too many forms have been sent from this connection. Please wait a few minutes and try again.", v.Get(tokenField)
	}
	if formguard.HoneypotTripped(v) {
		return v, http.StatusBadRequest, "This could not be sent. Please try again.", ""
	}
	_, err := s.guard.Verify(v.Get(tokenField), form, s.now())
	switch {
	case errors.Is(err, formguard.ErrTooFast):
		return v, http.StatusUnprocessableEntity, "Please check and press the button again.", v.Get(tokenField)
	case errors.Is(err, formguard.ErrExpired):
		return v, http.StatusUnprocessableEntity, "This page was open for a long time. Please try again.", ""
	case err != nil:
		return v, http.StatusBadRequest, "This could not be sent. Please try again.", ""
	}
	return v, 0, "", ""
}

func (s *Server) serveCancel(w http.ResponseWriter, r *http.Request, site Site, d statusData, b BookingState, ref, key string, cutoff int, back string) {
	form := changeForm(site.ID, ref, "cancel")
	show := func(status int, msg, token string) {
		d.Headline, d.Title, d.Error = "Cancel your booking?", "Cancel your booking?", msg
		d.Lines = []string{b.ServiceName + ", " + storefront.When(b.Start)}
		if b.Status == "paid" {
			d.Lines = append(d.Lines, "Your payment will be refunded.")
		}
		if token == "" {
			t, err := s.guard.Issue(form, s.now(), 0)
			if err != nil {
				s.Logger.Error("public host: form token", "err", err)
				s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
				return
			}
			token = string(t)
		}
		d.Token = token
		d.Buttons = []changeButton{{Action: statusLink(prefixOf(back), ref, key, "cancel"), Label: "Cancel booking", Primary: true}}
		d.Links = []statusLinkView{{Href: back, Label: "Keep my booking"}}
		s.renderPrivate(w, r, status, "status.html", d)
	}
	if r.Method != http.MethodPost {
		show(http.StatusOK, "", "")
		return
	}
	if _, status, msg, keep := s.guardChange(w, r, form); status != 0 {
		show(status, msg, keep)
		return
	}
	got, err := s.Change(r.Context(), ref, storefront.StatusHash(key), "cancel", cutoff, nil)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: change", "storefront", site.ID, "action", "cancel", "err", err)
		}
		show(http.StatusServiceUnavailable, "Something went wrong on our side. Please try again.", "")
		return
	}
	if got != ChangeOK {
		s.changeOutcome(w, r, d, got, back)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: back is statusLink's path on this host
}

func (s *Server) serveMove(w http.ResponseWriter, r *http.Request, site Site, p StorefrontPage, sd statusData, b BookingState, ref, key string, cutoff int, back string) {
	sp, _, err := s.services(r.Context(), site.ID)
	if err != nil || s.Busy == nil {
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	it, svc, found := findService(sp.Services, b.ServiceID)
	if !found {
		sd.Headline = "This booking cannot be moved online"
		sd.Title = sd.Headline
		sd.Lines = []string{b.ServiceName + " cannot be booked online right now. Please contact " + sd.Business + " to change your booking."}
		sd.Links = []statusLinkView{{Href: back, Label: "Back"}}
		s.renderPrivate(w, r, http.StatusConflict, "status.html", sd)
		return
	}
	prefix := prefixOf(back)
	action := statusLink(prefix, ref, key, "move")
	d := bookingData{pageData: pageData{Title: "Move your booking", Lang: sd.Lang, Look: sd.Look}, Business: sd.Business, Label: "Move your booking",
		Action: action, Home: sd.Home, Back: back, Honeypot: formguard.HoneypotField, Move: true, form: changeForm(site.ID, ref, "move"),
		Service: &serviceView{ID: it.ID, Name: it.Name, Length: length(it.DurationMinutes), Price: servicePrice(it)}, Current: storefront.When(b.Start)}
	a := sp.Availability.Effective(p.Model.Card.Hours)
	link := func(day string) string { return action + "&day=" + day }
	day := r.URL.Query().Get("day")
	var v url.Values
	var status int
	var msg, keep string
	if r.Method == http.MethodPost {
		v, status, msg, keep = s.guardChange(w, r, d.form)
		if t, err := strconv.ParseInt(v.Get("start"), 10, 64); err == nil {
			day = time.Unix(t, 0).In(storefront.Lagos).Format(time.DateOnly)
		}
	}
	fill := func() bool {
		d.Days, d.Slots, d.Day, d.NoTimes = nil, nil, "", ""
		if err := s.picker(r.Context(), site.ID, &d, a, svc, day, &b, link); err != nil {
			if !errors.Is(err, context.Canceled) {
				s.Logger.Error("public host: busy", "storefront", site.ID, "err", err)
			}
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return false
		}
		return true
	}
	if !fill() {
		return
	}
	if r.Method != http.MethodPost {
		s.showBooking(w, r, http.StatusOK, d, "")
		return
	}
	if status != 0 {
		d.Error = msg
		d.check(v.Get("start"))
		s.showBooking(w, r, status, d, keep)
		return
	}
	t, err := strconv.ParseInt(v.Get("start"), 10, 64)
	start := time.Unix(t, 0)
	if err != nil || start.Equal(b.Start) || !a.Offers(svc, start, s.now(), nil) {
		d.Error = msgPickSlot
		s.showBooking(w, r, http.StatusUnprocessableEntity, d, v.Get(tokenField))
		return
	}
	to := storefront.Booked{ServiceID: it.ID, ServiceName: it.Name, Start: start.UTC(), End: start.Add(svc.Duration).UTC(), Until: start.Add(svc.Duration + svc.Buffer).UTC(),
		Capacity: svc.Capacity}
	got, err := s.Change(r.Context(), ref, storefront.StatusHash(key), "move", cutoff, &to)
	switch {
	case err != nil:
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: change", "storefront", site.ID, "action", "move", "err", err)
		}
		d.Error = "Something went wrong on our side. Please try again."
		d.check(v.Get("start"))
		s.showBooking(w, r, http.StatusServiceUnavailable, d, v.Get(tokenField))
	case got == ChangeOK:
		http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: back is statusLink's path on this host
	case got == ChangeTaken:
		if !fill() {
			return
		}
		d.Error = msgSlotTaken
		s.showBooking(w, r, http.StatusConflict, d, "")
	default:
		s.changeOutcome(w, r, sd, got, back)
	}
}

func (s *Server) servePrices(w http.ResponseWriter, r *http.Request, site Site, ref, key string, o OrderState, embed bool, back string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: back is statusLink's path on this host
		return
	}
	d, _ := s.statusHead(r.Context(), site, ref, embed)
	if o.Status != "price_changed" {
		s.changeOutcome(w, r, d, ChangeNotAllowed, back)
		return
	}
	v, status, msg, keep := s.guardChange(w, r, changeForm(site.ID, ref, "prices"))
	action := ""
	if status == 0 {
		switch v.Get("do") {
		case "accept":
			action = "price_accept"
		case "decline":
			action = "price_decline"
		default:
			status, msg = http.StatusBadRequest, "Please choose to confirm or cancel."
			keep = v.Get(tokenField)
		}
	}
	if status == 0 {
		got, err := s.Change(r.Context(), ref, storefront.StatusHash(key), action, 0, nil)
		switch {
		case err != nil:
			if !errors.Is(err, context.Canceled) {
				s.Logger.Error("public host: change", "storefront", site.ID, "action", action, "err", err)
			}
			status, msg = http.StatusServiceUnavailable, "Something went wrong on our side. Please try again."
		case got == ChangeOK:
			http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: back is statusLink's path on this host
			return
		default:
			s.changeOutcome(w, r, d, got, back)
			return
		}
	}
	page, err := s.statusPage(r.Context(), site, ref, key, o, embed)
	if err != nil {
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if keep != "" {
		page.Token = keep
	}
	page.Error = msg
	s.renderPrivate(w, r, status, "status.html", page)
}

// prefixOf is the /embed prefix of a status link, if it has one.
func prefixOf(link string) string {
	if strings.HasPrefix(link, "/embed/") {
		return "/embed"
	}
	return ""
}
