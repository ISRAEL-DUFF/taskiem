package publicweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The storefront's forms (docs/spec/storefronts.md 5.2, 6.3, 6.4 and 7.5):
// GET /a/{kind} shows one, POST /a/{kind} checks it, seals it for the inbox
// and sends the customer to their status page. They work without
// JavaScript; order.js only adds a live total.

// Limits on what a form takes.
const (
	maxFormBody   = 32 << 10
	maxName       = 80
	maxAddress    = 200
	maxOrderNote  = 500
	maxEnquiry    = 1000
	maxBestTime   = 60
	maxQuantity   = 20
	maxLines      = 50
	tokenField    = "token"
	quantityField = "q." // + the item's id
	// SubmissionLimiterSize bounds the addresses tracked for submission
	// limits.
	SubmissionLimiterSize = 50000
)

// phoneCountries are where a customer's phone may be: SMS reaches them.
var phoneCountries = []string{"234"}

// formID is the formguard form id of a storefront's form: a token for one
// form or storefront is refused by every other.
func formID(sf uuid.UUID, kind string) string { return "sf:" + sf.String() + "/" + kind }

// statusKey is the secret of a submission's status link. It is derived
// from the token's idempotency key under the form key, not drawn at
// random, so a repeat of the same submission (a double tap, a retry on a
// bad connection), which the database answers with the first reference,
// also gets the first status key, whose hash is the one stored. The idem
// is stored too, so the derivation is keyed: reading the inbox is not
// enough to make a status link.
func (s *Server) statusKey(idem [32]byte) string {
	m := hmac.New(sha256.New, s.FormKey)
	m.Write([]byte("storefront status key\x00"))
	m.Write(idem[:])
	return statusEncoding.EncodeToString(m.Sum(nil)[:18])
}

// statusEncoding is storefront.NewStatusKey's alphabet.
var statusEncoding = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

// ipHashKey keys formguard.HashIP, apart from the token and status keys.
func (s *Server) ipHashKey() []byte {
	m := hmac.New(sha256.New, s.FormKey)
	m.Write([]byte("storefront client address"))
	return m.Sum(nil)
}

// formData is the form template's input.
type formData struct {
	pageData
	Business string
	Accent   string
	Kind     string
	Label    string
	Action   string // where the form posts
	Home     string // the storefront page; empty when embedded
	Token    string
	Honeypot string
	Error    string
	Purpose  string // why the business gets the details
	form     string // the formguard form id

	Name, Phone, Email, Message, BestTime, Address, Fulfilment string

	Items        []formItem
	Pickup       bool
	Delivery     bool
	DeliveryNote string
}

type formItem struct {
	N         int
	ID        string
	Name      string
	Price     string
	PriceKobo int64
	Qty       int
	Max       int // the most one order may take: maxQuantity, or the stock left
	Orderable bool
	SoldOut   bool
}

// closedData is the page a form answers with when it takes nothing.
type closedData struct {
	pageData
	Business string
	Accent   string
	Message  string
	Home     string
}

var purposes = map[string]string{"order": "handle your order", "enquiry": "answer your question", "callback": "call you back"}

func findAction(list []storefront.PublicAction, kind string) (storefront.PublicAction, bool) {
	for _, a := range list {
		if a.Kind == kind {
			return a, true
		}
	}
	return storefront.PublicAction{}, false
}

// orderable reports whether an item can be put in a basket: available,
// priced and not sold out.
func orderable(it storefront.Item) bool {
	return it.Available && it.PriceKobo != nil && it.ID != "" && !it.SoldOut()
}

// most is how many of an item one order may take.
func most(it storefront.Item) int {
	if it.Stock != nil && *it.Stock < maxQuantity {
		return int(*it.Stock)
	}
	return maxQuantity
}

func (s *Server) serveForm(w http.ResponseWriter, r *http.Request, site Site, kind string, embed bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: storefront", "storefront", site.ID, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	act, enabled := findAction(p.Model.Actions, kind)
	if !ok || !enabled {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	f := formView(p, act, site, embed)
	refuse := http.StatusOK
	if r.Method == http.MethodPost {
		refuse = http.StatusConflict
	}
	switch {
	case p.Status == "closed":
		msg := p.Model.Card.Name + " is closed right now."
		if p.ClosedMessage != "" {
			msg += " " + p.ClosedMessage
		}
		s.renderPrivate(w, r, refuse, "formclosed.html", closedData{pageData: pageData{Title: "Closed", Lang: f.Lang}, Business: f.Business, Accent: f.Accent, Message: msg, Home: f.Home})
		return
	case !s.formsOpen():
		// Without TASKIEM_PUBLIC_KEY; the server warns once, at start.
		s.renderPrivate(w, r, http.StatusServiceUnavailable, "formclosed.html", closedData{pageData: pageData{Title: "Not open yet", Lang: f.Lang}, Business: f.Business, Accent: f.Accent,
			Message: "Orders are not open yet. Please try again later, or contact " + f.Business + " directly.", Home: f.Home})
		return
	}
	if r.Method != http.MethodPost {
		if item := r.URL.Query().Get("item"); item != "" {
			for i := range f.Items {
				if f.Items[i].ID == item && f.Items[i].Orderable {
					f.Items[i].Qty = 1
					break
				}
			}
		}
		s.showForm(w, r, http.StatusOK, f, "")
		return
	}
	s.submitForm(w, r, site, p, act, f, embed)
}

func (s *Server) formsOpen() bool {
	return s.guard != nil && len(s.InboxKey) == 32 && s.Submit != nil
}

func formView(p StorefrontPage, act storefront.PublicAction, site Site, embed bool) formData {
	c := p.Model.Card
	f := formData{pageData: pageData{Title: act.Label, Lang: c.Language}, Business: c.Name, Accent: c.Accent, Kind: act.Kind, Label: act.Label,
		Action: "/a/" + act.Kind, Home: "/", Honeypot: formguard.HoneypotField, Purpose: purposes[act.Kind],
		form: formID(site.ID, act.Kind)}
	if f.Lang == "" {
		f.Lang = "en"
	}
	if _, ok := storefront.Accents[f.Accent]; !ok {
		f.Accent = "green"
	}
	if embed {
		f.Action, f.Home = "/embed/a/"+act.Kind, ""
	}
	if act.Kind == "order" {
		f.Pickup, f.Delivery, f.DeliveryNote = act.Pickup, act.Delivery, act.DeliveryNote
		switch {
		case f.Pickup && !f.Delivery:
			f.Fulfilment = "pickup"
		case f.Delivery && !f.Pickup:
			f.Fulfilment = "delivery"
		}
		seen := map[string]bool{}
		for _, it := range p.Items {
			if seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			x := formItem{N: len(f.Items), ID: it.ID, Name: it.Name, Max: most(it), Orderable: orderable(it), SoldOut: it.SoldOut()}
			if it.PriceKobo != nil {
				x.PriceKobo, x.Price = *it.PriceKobo, storefront.FormatNaira(*it.PriceKobo)
			}
			f.Items = append(f.Items, x)
		}
	}
	return f
}

// showForm renders a form with a token: a fresh one unless token is given
// (a refused submission keeps a token that verified, so sending it again
// stays one submission).
func (s *Server) showForm(w http.ResponseWriter, r *http.Request, status int, f formData, token string) {
	if token == "" {
		t, err := s.guard.Issue(f.form, s.now(), 0)
		if err != nil {
			s.Logger.Error("public host: form token", "err", err)
			s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		token = string(t)
	}
	f.Token = token
	s.renderPrivate(w, r, status, "form.html", f)
}

func (s *Server) submitForm(w http.ResponseWriter, r *http.Request, site Site, p StorefrontPage, act storefront.PublicAction, f formData, embed bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		f.Error = "This form could not be read. Please check your details and send again."
		s.showForm(w, r, http.StatusBadRequest, f, "")
		return
	}
	v := r.PostForm
	f.keep(v)
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		f.Error = "Too many forms have been sent from this connection. Please wait a few minutes and send again."
		s.showForm(w, r, http.StatusTooManyRequests, f, v.Get(tokenField))
		return
	}
	if formguard.HoneypotTripped(v) {
		f.Error = "This form could not be sent. Please check your details and send again."
		s.showForm(w, r, http.StatusBadRequest, f, "")
		return
	}
	claims, err := s.guard.Verify(v.Get(tokenField), formID(site.ID, act.Kind), s.now())
	switch {
	case errors.Is(err, formguard.ErrTooFast):
		// A fast typist must not lose an order: the same token works in a
		// moment.
		f.Error = "Please check your details and send again."
		s.showForm(w, r, http.StatusUnprocessableEntity, f, v.Get(tokenField))
		return
	case errors.Is(err, formguard.ErrExpired):
		f.Error = "This page was open for a long time. Please check your details and send again."
		s.showForm(w, r, http.StatusUnprocessableEntity, f, "")
		return
	case err != nil:
		f.Error = "This form could not be sent. Please check your details and send again."
		s.showForm(w, r, http.StatusBadRequest, f, "")
		return
	}
	sub, msg := f.check(v, p.Items)
	if msg != "" {
		f.Error = msg
		s.showForm(w, r, http.StatusUnprocessableEntity, f, v.Get(tokenField))
		return
	}
	idem := claims.IdempotencyKey()
	sub.StatusKey = s.statusKey(idem)
	sub.Payment = act.Payment
	sub.Channel = "direct"
	if embed {
		sub.Channel = "embed"
	}
	sub.Submitted = s.now().UTC()
	ref, ok, err := s.record(r.Context(), site.ID, p.Model.Slug, act.Kind, idem, sub)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host: submit", "storefront", site.ID, "kind", act.Kind, "err", err)
		}
		f.Error = "Something went wrong on our side. Please send again."
		s.showForm(w, r, http.StatusServiceUnavailable, f, v.Get(tokenField))
		return
	}
	if !ok {
		// Closed or the form turned off since the page was cached.
		s.renderPrivate(w, r, http.StatusConflict, "formclosed.html", closedData{pageData: pageData{Title: "Closed", Lang: f.Lang}, Business: f.Business, Accent: f.Accent,
			Message: f.Business + " is not taking this form right now.", Home: f.Home})
		return
	}
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	http.Redirect(w, r, prefix+"/o/"+url.PathEscape(ref)+"?k="+url.QueryEscape(sub.StatusKey), http.StatusSeeOther)
}

// record seals a submission and records it, with a new reference if the
// one drawn is taken. A repeat of idem answers with its first reference.
func (s *Server) record(ctx context.Context, sf uuid.UUID, slug, kind string, idem [32]byte, sub storefront.Submission) (string, bool, error) {
	hash := storefront.StatusHash(sub.StatusKey)
	for range 3 {
		ref := storefront.NewReference(slug)
		payload, err := storefront.SealSubmission(s.InboxKey, sf, ref, sub)
		if err != nil {
			return "", false, err
		}
		got, ok, err := s.Submit(ctx, sf, kind, ref, idem[:], hash, payload)
		if errors.Is(err, ErrReferenceTaken) {
			continue
		}
		return got, ok, err
	}
	return "", false, ErrReferenceTaken
}

// keep copies what was sent back into the form, so a refusal loses
// nothing the customer typed.
func (f *formData) keep(v url.Values) {
	f.Name, f.Phone, f.Email = v.Get("name"), v.Get("phone"), v.Get("email")
	f.Message, f.BestTime, f.Address = v.Get("message"), v.Get("best_time"), v.Get("address")
	if fl := v.Get("fulfilment"); f.Pickup && f.Delivery && (fl == "pickup" || fl == "delivery") {
		f.Fulfilment = fl
	}
	for i := range f.Items {
		if n, err := strconv.Atoi(strings.TrimSpace(v.Get(quantityField + f.Items[i].ID))); err == nil && n > 0 && n <= f.Items[i].Max && f.Items[i].Orderable {
			f.Items[i].Qty = n
		}
	}
}

// check validates a submission and builds it, pricing the basket from the
// catalogue copy. On a refusal msg says why in plain words and the field
// at fault is cleared from the form.
func (f *formData) check(v url.Values, items []storefront.Item) (sub storefront.Submission, msg string) {
	sub.Kind = f.Kind
	c := &sub.Customer
	var bad bool
	if c.Name, bad = text(v.Get("name"), maxName, false); bad || c.Name == "" {
		f.Name = ""
		return sub, "Please enter your name (up to 80 characters)."
	}
	phone, err := sender.NormalisePhone(v.Get("phone"), phoneCountries)
	if err != nil {
		f.Phone = ""
		return sub, "Please enter a Nigerian mobile number, like 0803 123 4567."
	}
	c.Phone = phone
	if e := strings.TrimSpace(v.Get("email")); e != "" {
		if c.Email, err = sender.NormaliseEmail(e); err != nil {
			f.Email = ""
			return sub, "Please check your email address, or leave it empty."
		}
	}
	switch f.Kind {
	case "order":
		if c.Message, bad = text(v.Get("message"), maxOrderNote, true); bad {
			f.Message = ""
			return sub, "Please keep your message to 500 characters."
		}
		if msg := f.basket(v, items, &sub); msg != "" {
			return sub, msg
		}
		switch {
		case f.Pickup && f.Delivery:
			sub.Fulfilment = v.Get("fulfilment")
			if sub.Fulfilment != "pickup" && sub.Fulfilment != "delivery" {
				return sub, "Please choose pickup or delivery."
			}
		default:
			sub.Fulfilment = f.Fulfilment
		}
		if sub.Fulfilment == "delivery" {
			if c.Address, bad = text(v.Get("address"), maxAddress, true); bad || c.Address == "" {
				f.Address = ""
				return sub, "Please enter the delivery address (up to 200 characters)."
			}
		}
	case "enquiry":
		if c.Message, bad = text(v.Get("message"), maxEnquiry, true); bad || c.Message == "" {
			if bad {
				f.Message = ""
			}
			return sub, "Please write your question, in up to 1000 characters."
		}
	case "callback":
		if c.BestTime, bad = text(v.Get("best_time"), maxBestTime, false); bad {
			f.BestTime = ""
			return sub, "Please keep the best time to call to 60 characters."
		}
	}
	for _, x := range []struct {
		val   string
		clear *string
	}{{c.Name, &f.Name}, {c.Message, &f.Message}, {c.Address, &f.Address}, {c.BestTime, &f.BestTime}} {
		if sensitive(x.val) {
			*x.clear = ""
			return sub, "Please do not put card numbers, BVNs or NINs in this form. " + f.Business + " will never ask for them here."
		}
	}
	return sub, ""
}

// basket builds the order's lines. Quantities come from the form; ids,
// names and prices from the catalogue copy, never from the browser.
func (f *formData) basket(v url.Values, items []storefront.Item, sub *storefront.Submission) string {
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		raw := strings.TrimSpace(v.Get(quantityField + it.ID))
		if raw == "" || raw == "0" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > maxQuantity {
			f.clearQty(it.ID)
			return fmt.Sprintf("Please choose from 0 to %d of %s.", maxQuantity, it.Name)
		}
		if n == 0 {
			continue
		}
		if it.SoldOut() {
			f.clearQty(it.ID)
			return it.Name + " is sold out. Please choose something else."
		}
		if !orderable(it) {
			f.clearQty(it.ID)
			return it.Name + " is not available right now. Please choose something else."
		}
		if n > most(it) {
			f.clearQty(it.ID)
			return fmt.Sprintf("Only %d of %s left. Please choose fewer.", most(it), it.Name)
		}
		if len(sub.Basket) == maxLines {
			return fmt.Sprintf("An order can have up to %d different items.", maxLines)
		}
		line := storefront.Line{ID: it.ID, Name: it.Name, Quantity: n, PriceKobo: *it.PriceKobo, LineKobo: *it.PriceKobo * int64(n)}
		sub.Basket = append(sub.Basket, line)
		sub.TotalKobo += line.LineKobo
	}
	if len(sub.Basket) == 0 {
		return "Please choose at least one item."
	}
	return ""
}

func (f *formData) clearQty(id string) {
	for i := range f.Items {
		if f.Items[i].ID == id {
			f.Items[i].Qty = 0
		}
	}
}

// text cleans a free-text field: trimmed, valid UTF-8, no control
// characters (newlines only where multiline), at most max characters. bad
// is true when it cannot be taken as it is.
func text(s string, max int, multiline bool) (string, bool) {
	if !utf8.ValidString(s) {
		return "", true
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	for _, r := range s {
		if unicode.IsControl(r) && (!multiline || (r != '\n' && r != '\t')) {
			return "", true
		}
	}
	if utf8.RuneCountInString(s) > max {
		return "", true
	}
	return s, false
}

// sensitive reports whether free text holds what a storefront never
// collects (storefronts 6.3, 7.3): a card number or an 11-digit BVN or NIN
// that is not a phone, by the PII detector's own rules.
func sensitive(s string) bool {
	if s == "" {
		return false
	}
	red := pii.Redact(s)
	for _, m := range []string{"[card]", "[bvn]", "[nin]"} {
		if strings.Count(red, m) > strings.Count(s, m) {
			return true
		}
	}
	return false
}
