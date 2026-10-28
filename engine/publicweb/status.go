package publicweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The customer's status page, GET /o/{reference}?k={status key}: what has
// happened to what they sent, and the payment link when there is one. It
// shows nothing personal, so a link passed on or left in a browser's
// history gives nothing away but the order's state.

// statusData is the status template's input; the change pages use it too.
type statusData struct {
	pageData
	Business  string
	Reference string
	Headline  string
	Error     string
	Lines     []string
	Refresh   bool
	PayURL    string
	PayLabel  string
	NewTab    bool // embedded: payment opens outside the frame
	// Transfer is Pay by bank transfer (N3); RefreshAfter refreshes the
	// page after that many seconds while an account is coming or open.
	Transfer     *transferView
	RefreshAfter int
	// pay is the waiting order's transfer, read before the words.
	pay  *OrderPayment
	Home string
	// Buttons post a change (each a form with Token); Links go to the
	// change pages, or back.
	Buttons  []changeButton
	Links    []statusLinkView
	Token    string
	Honeypot string
}

type changeButton struct {
	Action, Do, Label string
	Primary           bool
}

type statusLinkView struct {
	Href, Label string
}

func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request, site Site, ref string, embed bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	key := r.URL.Query().Get("k")
	o, ok := s.statusOf(w, r, site, ref, key)
	if !ok {
		return
	}
	d, err := s.statusPage(r.Context(), site, ref, key, o, embed)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: booking status", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	s.renderPrivate(w, r, http.StatusOK, "status.html", d)
}

// statusOf reads the submission behind a status link, answering the
// request itself when there is none to show.
func (s *Server) statusOf(w http.ResponseWriter, r *http.Request, site Site, ref, key string) (OrderState, bool) {
	if s.OrderStatus == nil || ref == "" || len(ref) > 20 || key == "" || len(key) > 64 {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return OrderState{}, false
	}
	o, ok, err := s.OrderStatus(r.Context(), ref, storefront.StatusHash(key))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: order status", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return OrderState{}, false
	}
	// Another storefront's order answers as unknown on this host.
	if !ok || o.Storefront != site.ID {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return OrderState{}, false
	}
	return o, true
}

// statusHead is a status or change page's frame: the business's name and
// colour, and the way home.
func (s *Server) statusHead(ctx context.Context, site Site, ref string, embed bool) (statusData, StorefrontPage) {
	d := statusData{pageData: pageData{Lang: "en"}, Business: "the business", Reference: ref, NewTab: embed, Home: "/", Honeypot: formguard.HoneypotField}
	if embed {
		d.Home = ""
	}
	p, ok, err := s.storefrontPage(ctx, site.ID)
	if err == nil && ok {
		c := p.Model.Card
		d.Business = c.Name
		if c.Language != "" {
			d.Lang = c.Language
		}
		d.Look = s.lookFor(ctx, site.Tenant, c.Name)
	}
	return d, p
}

// statusPage builds a status page: the plain words for where the
// submission is and, for a booking or new prices, what the customer may
// change.
func (s *Server) statusPage(ctx context.Context, site Site, ref, key string, o OrderState, embed bool) (statusData, error) {
	d, p := s.statusHead(ctx, site, ref, embed)
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	if o.Status == "awaiting_payment" && s.OrderPay != nil {
		p, ok, err := s.OrderPay.Payment(ctx, ref, storefront.StatusHash(key))
		if err != nil {
			return d, err
		}
		if ok {
			d.pay = &p
		}
	}
	switch {
	case o.Status == "price_changed":
		if err := s.priceWords(ctx, &d, site, ref, key, o, prefix); err != nil {
			return d, err
		}
	case o.Kind == "booking" && o.Status == "processing":
		d.Headline, d.Refresh = "We have your booking "+ref, true
		d.Lines = []string{"This page will update in a moment."}
	case o.Kind == "booking" && s.Booking != nil:
		b, ok, err := s.Booking(ctx, ref, storefront.StatusHash(key))
		if err != nil {
			return d, err
		}
		if !ok {
			statusWords(&d, o)
			break
		}
		bookingWords(&d, b)
		if !liveBooking(b.Status) || !s.changesOpen() {
			break
		}
		cutoff, ok := s.cutoff(ctx, site, p)
		switch {
		case !ok:
		case changeable(b.Start, cutoff, s.now()):
			d.Links = append(d.Links, statusLinkView{Href: statusLink(prefix, ref, key, "move"), Label: "Move to another time"},
				statusLinkView{Href: statusLink(prefix, ref, key, "cancel"), Label: "Cancel booking"})
		default:
			d.Lines = append(d.Lines, tooLate(d.Business))
		}
	default:
		statusWords(&d, o)
	}
	if o.Status == "awaiting_payment" {
		s.payByTransfer(&d, ref, key, prefix)
	}
	d.Title = d.Headline
	return d, nil
}

// cutoff is how close to a booking's start it may still be changed; ok is
// false when the services cannot be read.
func (s *Server) cutoff(ctx context.Context, site Site, p StorefrontPage) (int, bool) {
	sp, ok, err := s.services(ctx, site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: services", "storefront", site.ID, "err", err)
		}
		return 0, false
	}
	if !ok {
		return 0, false
	}
	return sp.Availability.Effective(p.Model.Card.Hours).CutoffMinutes, true
}

func tooLate(business string) string {
	return "It is too close to the time to change or cancel online. Please contact " + business + " directly."
}

// bookingWords says where a booking is, in plain sentences.
func bookingWords(d *statusData, b BookingState) {
	bn := d.Business
	what := b.ServiceName + ", " + storefront.When(b.Start)
	price := ""
	if b.TotalKobo > 0 {
		price = "Price: " + storefront.FormatNaira(b.TotalKobo)
	}
	switch b.Status {
	case "awaiting_payment":
		amount := b.TotalKobo
		if b.DepositKobo > 0 && b.DepositKobo < amount {
			amount = b.DepositKobo
		}
		if u := payLink(b.PaymentURL); u != "" {
			d.Headline = "Pay to book " + d.Reference
			d.PayURL, d.PayLabel = u, "Pay "+storefront.FormatNaira(amount)
			d.Lines = []string{what, d.heldLine(true), "You pay " + bn + " through their payment provider. Never share a PIN or one-time code with anyone."}
			return
		}
		if d.transferOnly() {
			d.Headline = "Pay to book " + d.Reference
			d.Lines = []string{what, d.heldLine(true), "Pay " + storefront.FormatNaira(amount) + " by bank transfer to the account number " + bn + " gives you here."}
			return
		}
		d.Headline, d.Refresh = "We have your booking "+d.Reference, true
		d.Lines = []string{what, "We are preparing your payment. This page will update in a moment."}
		return
	case "received":
		d.Headline = "Waiting for " + bn + " to confirm"
		d.Lines = []string{what, "You will get an SMS when they do."}
	case "accepted", "paid":
		d.Headline = "Your booking is confirmed"
		d.Lines = []string{what}
		if b.Status == "paid" {
			d.Lines = append(d.Lines, "Your payment has been received.")
		}
	case "declined":
		d.Headline = "Your booking was declined"
		d.Lines = []string{what, bn + " could not take this booking. Contact them directly if you have questions."}
	case "cancelled":
		d.Headline = "Your booking was cancelled"
		d.Lines = []string{what, "You can book another time."}
		price = ""
	case "refund_due":
		d.Headline = "Your booking was cancelled"
		d.Lines = []string{what, "Your payment will be refunded."}
		price = ""
	case "expired":
		d.Headline = "Your booking expired"
		d.Lines = []string{what, "It was not confirmed or paid for in time. You can book again."}
		price = ""
	case "completed":
		d.Headline = "Thank you for visiting"
		d.Lines = []string{what, "Thank you for booking with " + bn + "."}
		price = ""
	case "no_show":
		d.Headline = "Your booking was missed"
		d.Lines = []string{what, "Contact " + bn + " if you would like to book again."}
		price = ""
	default:
		d.Headline = "Your booking " + d.Reference
		d.Lines = []string{what, bn + " will be in touch by SMS."}
	}
	if price != "" {
		d.Lines = append(d.Lines, price)
	}
}

// statusWords says where a submission is, in plain sentences.
func statusWords(d *statusData, o OrderState) {
	b := d.Business
	switch o.Kind {
	case "enquiry":
		switch o.Status {
		case "processing":
			d.Headline, d.Refresh = "Sending your question", true
		case "completed":
			d.Headline = "Your question was answered"
		case "declined", "cancelled", "expired":
			d.Headline = "Your question was closed"
			d.Lines = []string{"Contact " + b + " directly if you still need an answer."}
		default:
			d.Headline = "Your question was sent"
			d.Lines = []string{b + " will reply by SMS or a call."}
		}
		return
	case "callback":
		switch o.Status {
		case "processing":
			d.Headline, d.Refresh = "Sending your request", true
		case "completed":
			d.Headline = b + " has called you back"
		case "declined", "cancelled", "expired":
			d.Headline = "Your call back request was closed"
			d.Lines = []string{"Contact " + b + " directly if you still need to talk."}
		default:
			d.Headline = "We'll call you back"
			d.Lines = []string{b + " has your request and will call you."}
		}
		return
	}
	total := ""
	if o.TotalKobo > 0 {
		total = storefront.FormatNaira(o.TotalKobo)
	}
	switch o.Status {
	case "processing":
		d.Headline, d.Refresh = "We have your order "+d.Reference, true
		d.Lines = []string{"This page will update in a moment."}
	case "awaiting_payment":
		if u := payLink(o.PaymentURL); u != "" {
			d.Headline = "Pay for your order " + d.Reference
			d.PayURL, d.PayLabel = u, "Pay "+total
			d.Lines = []string{d.heldLine(false), "You pay " + b + " through their payment provider. Never share a PIN or one-time code with anyone."}
		} else if d.transferOnly() {
			d.Headline = "Pay for your order " + d.Reference
			d.Lines = []string{d.heldLine(false), "Pay " + total + " by bank transfer to the account number " + b + " gives you here."}
		} else {
			d.Headline, d.Refresh = "We have your order "+d.Reference, true
			d.Lines = []string{"We are preparing your payment. This page will update in a moment."}
		}
	case "received", "paid":
		d.Headline = "Order received"
		d.Lines = []string{b + " will confirm by SMS."}
		if o.Status == "paid" {
			d.Lines = append([]string{"Your payment has been received."}, d.Lines...)
		}
	case "accepted":
		d.Headline = "Your order was accepted"
		d.Lines = []string{b + " is preparing it and will be in touch by SMS."}
	case "declined":
		d.Headline = "Your order was declined"
		d.Lines = []string{b + " could not take this order. Contact them directly if you have questions."}
	case "dispatched":
		d.Headline = "A rider has your order"
		d.Lines = []string{"You will get an SMS when it is on its way."}
	case "on_the_way":
		d.Headline = "Your order is on its way"
		d.Lines = []string{"The rider will be at your door soon."}
	case "arrived":
		d.Headline = "Your rider is at the door"
		d.Lines = []string{"Collect your order from the rider."}
		if o.Payment == "on_delivery" {
			d.Lines = []string{"Pay the rider's account number by transfer (it is in your SMS too), or pay in cash, then collect your order."}
		}
	case "completed":
		d.Headline = "Your order is complete"
		d.Lines = []string{"Thank you for ordering from " + b + "."}
	case "expired":
		d.Headline = "Your order expired"
		d.Lines = []string{"It was not paid for in time, so it was not sent to " + b + ". You can order again."}
	case "cancelled":
		d.Headline = "Your order was cancelled"
		d.Lines = []string{"Contact " + b + " directly if you have questions."}
	case "refund_due":
		d.Headline = "Your payment will be refunded"
		d.Lines = []string{b + " could not take this order, so your payment is being returned."}
	default:
		d.Headline = "Your order " + d.Reference
		d.Lines = []string{b + " will be in touch by SMS."}
	}
	if total != "" && d.PayURL == "" && o.Status != "processing" && (o.Status != "awaiting_payment" || !d.transferOnly()) {
		d.Lines = append(d.Lines, "Total: "+total)
	}
}

// payLink is a payment URL fit for a link: https only. It is the
// provider's page, made by the workflow; the page never frames it.
func payLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}
