package publicweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// The customer's status page, GET /o/{reference}?k={status key}: what has
// happened to what they sent, and the payment link when there is one. It
// shows nothing personal, so a link passed on or left in a browser's
// history gives nothing away but the order's state.

// statusData is the status template's input.
type statusData struct {
	pageData
	Business  string
	Accent    string
	Reference string
	Headline  string
	Lines     []string
	Refresh   bool
	PayURL    string
	PayLabel  string
	NewTab    bool // embedded: payment opens outside the frame
	Home      string
}

func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request, site Site, ref string, embed bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	key := r.URL.Query().Get("k")
	if s.OrderStatus == nil || ref == "" || len(ref) > 20 || key == "" || len(key) > 64 {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	o, ok, err := s.OrderStatus(r.Context(), ref, storefront.StatusHash(key))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: order status", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	// Another storefront's order answers as unknown on this host.
	if !ok || o.Storefront != site.ID {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	d := statusData{pageData: pageData{Lang: "en"}, Business: "the business", Accent: "green", Reference: ref, NewTab: embed, Home: "/"}
	if embed {
		d.Home = ""
	}
	if p, ok, err := s.storefrontPage(r.Context(), site.ID); err == nil && ok {
		c := p.Model.Card
		d.Business = c.Name
		if c.Language != "" {
			d.Lang = c.Language
		}
		if _, ok := storefront.Accents[c.Accent]; ok {
			d.Accent = c.Accent
		}
	}
	statusWords(&d, o)
	d.Title = d.Headline
	s.renderPrivate(w, r, http.StatusOK, "status.html", d)
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
			d.Lines = []string{"Your order is held for 30 minutes.", "You pay " + b + " through their payment provider. Never share a PIN or one-time code with anyone."}
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
	if total != "" && d.PayURL == "" && o.Status != "processing" {
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
