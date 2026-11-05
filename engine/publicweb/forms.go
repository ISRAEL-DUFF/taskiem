package publicweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	maxArea       = 40
	maxLandmark   = 120
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
	Kind     string
	// Hire is a hire's dates-and-quantity field (fronts F5).
	Hire     *hireView
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
	// Zones are the delivery areas to choose from, with fees; Zone the
	// one chosen (dispatch D1).
	Zones []zoneItem
	Zone  string
	// Area (no areas set), Landmark and Pin say where a delivery goes;
	// PayOnDelivery offers paying at the door, PayLater is it chosen
	// (dispatch D2, D4).
	Area, Landmark, Pin     string
	PayOnDelivery, PayLater bool
	// Questions are the action's own questions (fronts F1): its form's
	// fields but the basket and time, drawn as hosted forms' fields.
	Questions []*field
	// Step is where an order is in its two pages: "bag" (what is in it,
	// to change or confirm), "details" (who and where, with the bag as a
	// summary), or empty (one page, with the whole menu to choose from).
	// Nothing is kept between them: the bag travels in the address and
	// the form, so the page has no cookies and works without a script.
	Step     string
	Bag      []bagLine
	BagCount int
	Subtotal string
	// DepositPercent is the part of an order paid online when it takes a
	// deposit; Deposit and Balance are that part of the bag and the rest.
	DepositPercent   int
	Deposit, Balance string
	// MoreHref is where to add more to the bag, ChangeHref where the
	// bag is changed from the details page.
	MoreHref, ChangeHref string
}

type zoneItem struct {
	Name     string
	Fee      string // "₦1,500 delivery", or "free delivery"
	FeeKobo  int64
	Selected bool
}

type formItem struct {
	N  int
	ID string
	// Key names the line's fields: the item's id for its first line in a
	// bag, <id>~2 to ~9 for the same item with other choices.
	Key       string
	Name      string
	Price     string
	PriceKobo int64
	Qty       int
	Max       int // the most one order may take: maxQuantity, or the stock left
	Orderable bool
	SoldOut   bool
	// Small is the item's thumbnail, for the bag.
	Small string
	// Options are a Food dish's groups, a choice each (fronts F3).
	Options []formOption
	// More are the bag's other lines of this item, each with its own
	// choices (Key <id>~2 and on).
	More []formItem
}

// maxVariants is how many lines one item may have in a bag, each with
// different choices.
const maxVariants = 9

// lineKey is the key of an item's k-th line in a bag (k from 1).
func lineKey(id string, k int) string {
	if k <= 1 {
		return id
	}
	return id + "~" + strconv.Itoa(k)
}

// formOption is one group of a dish's choices as the form asks it.
type formOption struct {
	Input, Name string
	Choices     []formChoice
}

type formChoice struct {
	Value, Label string
	// Name is the choice on its own, PriceKobo what it adds.
	Name      string
	PriceKobo int64
	Selected  bool
}

// optionInput is the name of a dish's choice in one of its groups.
func optionInput(id string, group int) string { return fmt.Sprintf("o.%s.%d", id, group) }

// formOptions are an item's groups for the form, under the line's key,
// the first choice of each taken.
func formOptions(it storefront.Item, key string) []formOption {
	var out []formOption
	for gi, g := range it.Options {
		o := formOption{Input: optionInput(key, gi), Name: g.Name}
		for ci, c := range g.Choices {
			label := c.Name
			if c.PriceKobo > 0 {
				label += " (+" + storefront.FormatNaira(c.PriceKobo) + ")"
			}
			o.Choices = append(o.Choices, formChoice{Value: strconv.Itoa(ci), Label: label, Name: c.Name, PriceKobo: c.PriceKobo, Selected: ci == 0})
		}
		out = append(out, o)
	}
	return out
}

// closedData is the page a form answers with when it takes nothing.
type closedData struct {
	pageData
	Business string
	Message  string
	Home     string
}

var purposes = map[string]string{"order": "handle your order", "enquiry": "answer your question", "callback": "call you back", "booking": "handle your booking",
	"hire": "handle your hire", "space": "handle your booking", "tickets": "send your tickets", "class": "handle your enrolment", "quote": "send you a price"}

func findAction(list []storefront.PublicAction, ref string) (storefront.PublicAction, bool) {
	for _, a := range list {
		if a.Ref() == ref {
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

// serveForm serves an action's form: one of the home page's at /a/<ref>,
// or one of a front's at /<front>/a/<ref> (fronts F3).
func (s *Server) serveForm(w http.ResponseWriter, r *http.Request, site Site, ref string, embed bool) {
	s.serveFrontForm(w, r, site, "", ref, embed)
}

func (s *Server) serveFrontForm(w http.ResponseWriter, r *http.Request, site Site, frontSlug, ref string, embed bool) {
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
	actions, base := p.Model.Home().Actions, ""
	var cal *storefront.Calendar
	if frontSlug != "" {
		f, found := frontOf(p, frontSlug)
		if !found {
			s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
			return
		}
		// A front's basket draws on its own list.
		actions, base = f.Actions, "/"+f.Slug
		cal = f.ListCalendar()
		p.Items = p.Lists[f.ListTable()]
		p.Model.HasServices = false
	}
	act, enabled := findAction(actions, ref)
	kind := act.Kind
	if !ok || !enabled || (kind == "booking" && !p.Model.HasServices) || (kind == "space" && cal == nil) {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	f := formView(p, act, site, embed, base)
	switch act.Kind {
	case "hire":
		f.Hire = hireField(p.Items, s.now(), act.DepositPercent)
	case "space":
		f.Hire = spaceField(p.Items, *cal, s.now(), act.DepositPercent)
	case "tickets", "class":
		f.Hire = countField(p.Items, act, s.now())
	}
	f.Look = s.lookFor(r.Context(), site.Tenant, f.Business)
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
		s.renderPrivate(w, r, refuse, "formclosed.html", closedData{pageData: pageData{Title: "Closed", Lang: f.Lang, Look: f.Look}, Business: f.Business, Message: msg, Home: f.Home})
		return
	case p.OrdersPaused && (kind == "order" || kind == "booking"):
		// The plan's month, or the unverified daily limit (docs/moderation.md).
		what := map[string]string{"order": "orders", "booking": "bookings"}[kind]
		s.renderPrivate(w, r, refuse, "formclosed.html", closedData{pageData: pageData{Title: "Paused", Lang: f.Lang, Look: f.Look}, Business: f.Business,
			Message: f.Business + " is not taking " + what + " online right now. Please contact them directly.", Home: f.Home})
		return
	case !s.formsOpen() || (kind == "booking" && !s.bookingsOpen()):
		// Without TASKIEM_PUBLIC_KEY; the server warns once, at start.
		what := "Orders"
		if kind == "booking" {
			what = "Bookings"
		}
		s.renderPrivate(w, r, http.StatusServiceUnavailable, "formclosed.html", closedData{pageData: pageData{Title: "Not open yet", Lang: f.Lang, Look: f.Look}, Business: f.Business,
			Message: what + " are not open yet. Please try again later, or contact " + f.Business + " directly.", Home: f.Home})
		return
	case kind == "booking":
		s.serveBooking(w, r, site, p, act, embed)
		return
	}
	if r.Method != http.MethodPost {
		f.stepFor(r.URL.Query())
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

func formView(p StorefrontPage, act storefront.PublicAction, site Site, embed bool, base string) formData {
	c := p.Model.Card
	f := formData{pageData: pageData{Title: act.Label, Lang: c.Language}, Business: c.Name, Kind: act.Kind, Label: act.Label,
		Action: base + "/a/" + act.Ref(), Home: "/", Honeypot: formguard.HoneypotField, Purpose: purposes[act.Kind],
		form: formID(site.ID, strings.TrimPrefix(base+"/"+act.Ref(), "/"))}
	if base != "" {
		f.Home = base
	}
	if f.Lang == "" {
		f.Lang = "en"
	}
	if embed {
		f.Action, f.Home = "/embed"+base+"/a/"+act.Ref(), ""
	}
	// The page is drawn from the action's form (fronts F1): the basket
	// where it has one, then its questions.
	form := storefront.EffectiveForm(act.Form, act.Kind)
	f.Questions = questions(form)
	if act.Kind == "order" {
		f.Pickup, f.Delivery, f.DeliveryNote = act.Pickup, act.Delivery, act.DeliveryNote
		f.PayOnDelivery = act.Delivery && act.PayOnDelivery && act.Payment != "none"
		if act.Payment != "none" && act.DepositPercent > 0 && act.DepositPercent < 100 {
			f.DepositPercent = act.DepositPercent
		}
		if act.Delivery {
			for _, z := range act.Zones {
				fee := "free delivery"
				if z.FeeKobo > 0 {
					fee = storefront.FormatNaira(z.FeeKobo) + " delivery"
				}
				f.Zones = append(f.Zones, zoneItem{Name: z.Name, Fee: fee, FeeKobo: z.FeeKobo})
			}
		}
		switch {
		case f.Pickup && !f.Delivery:
			f.Fulfilment = "pickup"
		case f.Delivery && !f.Pickup:
			f.Fulfilment = "delivery"
		}
		if storefront.HasWidget(form, "basket") {
			f.Items = basketItems(p.Items)
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
	if r.Method == http.MethodPost {
		f.detailsAgain()
	}
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
	claims, err := s.guard.Verify(v.Get(tokenField), f.form, s.now())
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
	switch {
	case msg != "":
	case act.Kind == "hire":
		msg = checkHire(v, p.Items, act, s.now(), &sub)
	case act.Kind == "space" && f.Hire != nil:
		msg = checkSpace(v, p.Items, act, f.Hire.cal, s.now(), &sub)
	case act.Kind == "tickets" || act.Kind == "class":
		msg = checkCount(v, p.Items, act, s.now(), &sub)
	}
	if msg != "" {
		f.Error = msg
		s.showForm(w, r, http.StatusUnprocessableEntity, f, v.Get(tokenField))
		return
	}
	idem := claims.IdempotencyKey()
	sub.StatusKey = s.statusKey(idem)
	if sub.Payment != "on_delivery" {
		sub.Payment = act.Payment
	}
	// An order that takes a deposit pays that part online, the rest being
	// the order's balance (paid in parts, fronts F5).
	// One under a naira is not taken: the order is paid in full.
	if act.Kind == "order" && sub.Payment != "on_delivery" && sub.Payment != "none" {
		sub.DepositKobo = storefront.ActionOptions{DepositPercent: act.DepositPercent}.OrderDeposit(sub.TotalKobo)
	}
	sub.Channel = "direct"
	if embed {
		sub.Channel = "embed"
	}
	sub.Submitted = s.now().UTC()
	record := s.record
	// A name on a full class's waiting list holds no place, so it is
	// sent as a plain submission.
	if (act.Kind == "hire" || act.Kind == "space" || act.Kind == "tickets" || act.Kind == "class") && !sub.Waitlist {
		record = s.recordHire
	}
	ref, ok, err := record(r.Context(), site.ID, p.Model.Slug, act, idem, sub)
	if errors.Is(err, ErrNotFree) {
		f.Error = "Not that many are free for those days. Choose other days, or fewer."
		switch act.Kind {
		case "space":
			f.Error = "That space is booked for some of that time. Choose another time."
		case "tickets":
			f.Error = "Not that many tickets are left. Choose fewer, or another event."
		case "class":
			f.Error = "Not that many places are left in that class. Choose fewer, or another class."
			if act.WaitingList && f.Hire != nil {
				f.Error = "That class is full. You can join its waiting list, and we will tell you when a place is free."
				f.Hire.Waitlist = true
			}
		}
		s.showForm(w, r, http.StatusConflict, f, v.Get(tokenField))
		return
	}
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
		s.renderPrivate(w, r, http.StatusConflict, "formclosed.html", closedData{pageData: pageData{Title: "Closed", Lang: f.Lang, Look: f.Look}, Business: f.Business,
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
func (s *Server) record(ctx context.Context, sf uuid.UUID, slug string, act storefront.PublicAction, idem [32]byte, sub storefront.Submission) (string, bool, error) {
	hash := storefront.StatusHash(sub.StatusKey)
	for range 3 {
		ref := storefront.NewReference(slug)
		payload, err := storefront.SealSubmission(s.InboxKey, sf, ref, sub)
		if err != nil {
			return "", false, err
		}
		got, ok, err := s.Submit(ctx, sf, act.ID, act.Kind, ref, idem[:], hash, payload)
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
	if f.Hire != nil {
		f.Hire.keep(v)
		f.Hire.keepCount(v)
	}
	f.Name, f.Phone, f.Email = v.Get("name"), v.Get("phone"), v.Get("email")
	f.Message, f.BestTime, f.Address = v.Get("message"), v.Get("best_time"), v.Get("address")
	if fl := v.Get("fulfilment"); f.Pickup && f.Delivery && (fl == "pickup" || fl == "delivery") {
		f.Fulfilment = fl
	}
	f.chooseZone(v.Get("zone"))
	f.Area, f.Landmark, f.Pin = v.Get("area"), v.Get("landmark"), v.Get("pin")
	f.PayLater = f.PayOnDelivery && v.Get("pay") == "on_delivery"
	for _, q := range f.Questions {
		q.show(v)
	}
	f.keepBasket(v)
}

// keepBasket takes the quantities and the choices a form (or an address)
// names: an item's first line under its id, its others under <id>~k.
func (f *formData) keepBasket(v url.Values) {
	for i := range f.Items {
		it := &f.Items[i]
		base := *it
		base.Qty, base.More = 0, nil
		it.More = nil
		f.keepLine(it, v)
		for k := 2; k <= maxVariants; k++ {
			key := lineKey(it.ID, k)
			if v.Get(quantityField+key) == "" {
				continue
			}
			x := base
			x.Key = key
			x.Options = make([]formOption, len(base.Options))
			for gi, o := range base.Options {
				x.Options[gi] = formOption{Input: optionInput(key, gi), Name: o.Name, Choices: append([]formChoice(nil), o.Choices...)}
				for c := range x.Options[gi].Choices {
					x.Options[gi].Choices[c].Selected = c == 0
				}
			}
			if f.keepLine(&x, v) {
				it.More = append(it.More, x)
			}
		}
	}
}

// keepLine takes one line's quantity and choices; false when it has none.
// A choice the item no longer offers takes the line out, saying so,
// rather than putting another choice in its place.
func (f *formData) keepLine(x *formItem, v url.Values) bool {
	n, err := strconv.Atoi(strings.TrimSpace(v.Get(quantityField + x.Key)))
	if err != nil || n <= 0 || n > x.Max || !x.Orderable {
		return false
	}
	for _, o := range x.Options {
		picked := v.Get(o.Input)
		if picked == "" {
			continue
		}
		found := false
		for c := range o.Choices {
			if o.Choices[c].Value == picked {
				found = true
			}
		}
		if !found {
			f.Error = "The choices for " + x.Name + " have changed. Please choose again."
			return false
		}
		for c := range o.Choices {
			o.Choices[c].Selected = o.Choices[c].Value == picked
		}
	}
	x.Qty = n
	return true
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
			if len(f.Zones) > 0 {
				z, ok := f.chooseZone(v.Get("zone"))
				if !ok {
					return sub, "Please choose the area you want it delivered to."
				}
				sub.Zone, sub.DeliveryKobo = z.Name, z.FeeKobo
				sub.TotalKobo += z.FeeKobo
			}
			if c.Address, bad = text(v.Get("address"), maxAddress, true); bad || c.Address == "" {
				f.Address = ""
				return sub, "Please enter the delivery address (up to 200 characters)."
			}
			if len(f.Zones) == 0 {
				if sub.Zone, bad = text(v.Get("area"), maxArea, false); bad {
					f.Area = ""
					return sub, "Please keep the area to 40 characters."
				}
			}
			if c.Landmark, bad = text(v.Get("landmark"), maxLandmark, false); bad {
				f.Landmark = ""
				return sub, "Please keep the landmark to 120 characters."
			}
			if c.Pin, bad = mapsPin(v.Get("pin")); bad {
				f.Pin = ""
				return sub, "Please paste a link from your maps app, or leave the map pin empty."
			}
			if f.PayOnDelivery && v.Get("pay") == "on_delivery" {
				sub.Payment = "on_delivery"
			}
		}
	case "enquiry":
		// A form with questions of its own (a blank form, fronts F7) asks
		// those; the message is then optional.
		if c.Message, bad = text(v.Get("message"), maxEnquiry, true); bad || (c.Message == "" && len(f.Questions) == 0) {
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
	}{{c.Name, &f.Name}, {c.Message, &f.Message}, {c.Address, &f.Address}, {c.BestTime, &f.BestTime}, {c.Landmark, &f.Landmark}} {
		if sensitive(x.val) {
			*x.clear = ""
			return sub, "Please do not put card numbers, BVNs or NINs in this form. " + f.Business + " will never ask for them here."
		}
	}
	answers, msg := readAnswers(f.Questions, v)
	if msg != "" {
		return sub, msg
	}
	sub.Answers = answers
	return sub, ""
}

// questions are a form's questions as fields, in order.
func questions(form []byte) []*field {
	var schema map[string]any
	if json.Unmarshal(form, &schema) != nil {
		return nil
	}
	all := fields(schema)
	var out []*field
	for _, name := range storefront.FormFields(form) {
		if f, ok := all[name]; ok && !f.Hidden && f.Widget != "file" {
			out = append(out, f)
		}
	}
	return out
}

// readAnswers reads the questions' answers; msg is the first refusal, the
// field at fault marked.
func readAnswers(qs []*field, v url.Values) (map[string]any, string) {
	var answers map[string]any
	for _, q := range qs {
		q.show(v)
		val, present, msg := q.parse(v)
		if msg != "" {
			q.Error = msg
			return nil, msg
		}
		if present {
			if s, ok := val.(string); ok && sensitive(s) {
				q.Value, q.Error = "", "Please do not put card numbers, BVNs or NINs here."
				return nil, q.Error
			}
			if answers == nil {
				answers = map[string]any{}
			}
			answers[q.Name] = val
		}
	}
	return answers, ""
}

// basket builds the order's lines. Quantities come from the form; ids,
// names and prices from the catalogue copy, never from the browser. An
// item may have several lines (<id>~k), each with its own choices; their
// quantities together are held to what is left.
func (f *formData) basket(v url.Values, items []storefront.Item, sub *storefront.Submission) string {
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		taken := 0
		for k := 1; k <= maxVariants; k++ {
			key := lineKey(it.ID, k)
			raw := strings.TrimSpace(v.Get(quantityField + key))
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
			if taken += n; taken > most(it) {
				f.clearQty(it.ID)
				return fmt.Sprintf("Only %d of %s left. Please choose fewer.", most(it), it.Name)
			}
			if len(sub.Basket) == maxLines {
				return fmt.Sprintf("An order can have up to %d different items.", maxLines)
			}
			// A dish's choices, one per group, priced from the copy.
			picked := make([]int, len(it.Options))
			for gi := range it.Options {
				if raw := v.Get(optionInput(key, gi)); raw != "" {
					if picked[gi], err = strconv.Atoi(raw); err != nil {
						picked[gi] = -1
					}
				}
			}
			opts, add, ok := it.Choose(picked)
			if !ok {
				return "Please choose from the options for " + it.Name + "."
			}
			unit := *it.PriceKobo + add
			line := storefront.Line{ID: it.ID, Name: it.Name, Quantity: n, PriceKobo: unit, LineKobo: unit * int64(n), Options: opts}
			sub.Basket = append(sub.Basket, line)
			sub.TotalKobo += line.LineKobo
		}
	}
	if len(sub.Basket) == 0 {
		return "Please choose at least one item."
	}
	return ""
}

// mapsPin reads a map pin: a link from a maps app, or "latitude,longitude"
// (what the browser's location gives), as a maps link; "" for none. bad
// when it is neither.
func mapsPin(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if m := latLong.FindStringSubmatch(s); m != nil {
		lat, err1 := strconv.ParseFloat(m[1], 64)
		lng, err2 := strconv.ParseFloat(m[2], 64)
		if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			return "", true
		}
		return fmt.Sprintf("https://www.google.com/maps?q=%.6f,%.6f", lat, lng), false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || len(s) > 300 {
		return "", true
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range mapHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return u.String(), false
		}
	}
	return "", true
}

var (
	latLong  = regexp.MustCompile(`^(-?\d{1,2}(?:\.\d+)?)\s*,\s*(-?\d{1,3}(?:\.\d+)?)$`)
	mapHosts = []string{"google.com", "maps.google.com", "goo.gl", "maps.app.goo.gl", "maps.apple.com", "openstreetmap.org", "waze.com"}
)

// chooseZone marks the delivery area named (exactly as offered) chosen;
// false when it is none of them.
func (f *formData) chooseZone(name string) (zoneItem, bool) {
	var got zoneItem
	ok := false
	f.Zone = ""
	for i := range f.Zones {
		f.Zones[i].Selected = name != "" && f.Zones[i].Name == name
		if f.Zones[i].Selected {
			got, ok, f.Zone = f.Zones[i], true, name
		}
	}
	return got, ok
}

func (f *formData) clearQty(id string) {
	for i := range f.Items {
		if f.Items[i].ID == id {
			f.Items[i].Qty, f.Items[i].More = 0, nil
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

// basketItems are the catalogue's items as the basket shows them, each once.
func basketItems(items []storefront.Item) []formItem {
	var out []formItem
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		x := formItem{N: len(out), ID: it.ID, Key: it.ID, Name: it.Name, Max: most(it), Orderable: orderable(it), SoldOut: it.SoldOut(), Options: formOptions(it, it.ID), Small: it.Photo["320"]}
		if it.PriceKobo != nil {
			x.PriceKobo, x.Price = *it.PriceKobo, storefront.FormatNaira(*it.PriceKobo)
		}
		out = append(out, x)
	}
	return out
}
