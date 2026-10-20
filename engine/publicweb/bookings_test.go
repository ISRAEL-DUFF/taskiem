package publicweb

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// fakeCalendar stands in for the booking functions of 00181: services,
// holds, taskiem_public_book, taskiem_public_booking and
// taskiem_public_change, over the fake inbox.
type fakeCalendar struct {
	mu       sync.Mutex
	e        *formEnv
	page     ServicesPage
	holds    map[string]storefront.Busy // by reference
	bookings map[string]BookingState
	offer    *PriceOffer
	changes  []string
}

func (c *fakeCalendar) services(context.Context, uuid.UUID) (ServicesPage, bool, error) {
	return c.page, true, nil
}

func (c *fakeCalendar) busy(_ context.Context, _ uuid.UUID, from, to time.Time) ([]storefront.Busy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busyLocked("", from, to), nil
}

func (c *fakeCalendar) busyLocked(except string, from, to time.Time) []storefront.Busy {
	var out []storefront.Busy
	for ref, b := range c.holds {
		if ref != except && b.Start.Before(to) && from.Before(b.Until) {
			out = append(out, b)
		}
	}
	return out
}

func (c *fakeCalendar) book(_ context.Context, sf uuid.UUID, ref string, idem, hash, payload []byte, b storefront.Booked) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	in := c.e.inbox
	in.mu.Lock()
	defer in.mu.Unlock()
	if r, ok := in.byIdem[hex.EncodeToString(idem)]; ok {
		return r, true, nil
	}
	if in.closed {
		return "", false, nil
	}
	svc := storefront.Service{ID: b.ServiceID, Duration: b.End.Sub(b.Start), Buffer: b.Until.Sub(b.End), Capacity: b.Capacity}
	if !storefront.Free(svc, b.Start, c.busyLocked("", b.Start.Add(-24*time.Hour), b.Until.Add(24*time.Hour))) {
		return "", false, ErrSlotTaken
	}
	in.byIdem[hex.EncodeToString(idem)] = ref
	in.rows[ref] = inboxRow{sf: sf, kind: "booking", idem: idem, hash: hash, payload: payload}
	c.holds[ref] = storefront.Busy{Service: b.ServiceID, Start: b.Start, Until: b.Until, Count: 1}
	return ref, true, nil
}

// recorded is what the api role makes of a booking in the inbox.
func (c *fakeCalendar) recorded(t *testing.T, ref, status string) {
	t.Helper()
	sub, err := storefront.OpenSubmission(c.e.s.InboxKey, c.e.sf, ref, c.e.inbox.rows[ref].payload)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b := sub.Booking
	c.bookings[ref] = BookingState{Status: status, ServiceID: b.ServiceID, ServiceName: b.ServiceName, Start: b.Start, End: b.End, TotalKobo: sub.TotalKobo,
		DepositKobo: sub.DepositKobo, Payment: sub.Payment, Storefront: c.e.sf}
	c.e.inbox.states[ref] = OrderState{Status: status, Kind: "booking", TotalKobo: sub.TotalKobo, Payment: sub.Payment}
}

func (c *fakeCalendar) booking(_ context.Context, ref string, hash []byte) (BookingState, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bookings[ref]
	if !ok || !bytes.Equal(c.e.inbox.rows[ref].hash, hash) {
		return BookingState{}, false, nil
	}
	return b, true, nil
}

func (c *fakeCalendar) priceChange(_ context.Context, ref string, hash []byte) (PriceOffer, bool, error) {
	if c.offer == nil || !bytes.Equal(c.e.inbox.rows[ref].hash, hash) {
		return PriceOffer{}, false, nil
	}
	return *c.offer, true, nil
}

// change is taskiem_public_change's rules.
func (c *fakeCalendar) change(_ context.Context, ref string, hash []byte, action string, cutoff int, to *storefront.Booked) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !bytes.Equal(c.e.inbox.rows[ref].hash, hash) {
		return ChangeNotAllowed, nil
	}
	c.changes = append(c.changes, action)
	st := c.e.inbox.states[ref]
	if action == "price_accept" || action == "price_decline" {
		if st.Status != "price_changed" {
			return ChangeNotAllowed, nil
		}
		st.Status = map[string]string{"price_accept": "received", "price_decline": "cancelled"}[action]
		c.e.inbox.states[ref] = st
		return ChangeOK, nil
	}
	b, ok := c.bookings[ref]
	if !ok || !liveBooking(b.Status) {
		return ChangeNotAllowed, nil
	}
	if c.e.now.After(b.Start.Add(-time.Duration(cutoff) * time.Minute)) {
		return ChangeTooLate, nil
	}
	if action == "cancel" {
		b.Status = "cancelled"
		delete(c.holds, ref)
	} else {
		svc := storefront.Service{ID: b.ServiceID, Duration: to.End.Sub(to.Start), Buffer: to.Until.Sub(to.End), Capacity: to.Capacity}
		if !storefront.Free(svc, to.Start, c.busyLocked(ref, to.Start.Add(-24*time.Hour), to.Until.Add(24*time.Hour))) {
			return ChangeTaken, nil
		}
		b.Start, b.End = to.Start, to.End
		c.holds[ref] = storefront.Busy{Service: b.ServiceID, Start: to.Start, Until: to.Until, Count: 1}
	}
	c.bookings[ref] = b
	st.Status = b.Status
	c.e.inbox.states[ref] = st
	return ChangeOK, nil
}

var (
	haircut = storefront.Item{ID: "7", Name: "Haircut", PriceKobo: kobo(300000), Available: true, DurationMinutes: 30, Category: "Hair"}
	consult = storefront.Item{ID: "8", Name: "Consultation", Available: true, DurationMinutes: 60, BufferMinutes: 15}
	class   = storefront.Item{ID: "9", Name: "Braiding class", PriceKobo: kobo(500000), Available: true, DurationMinutes: 90, Capacity: 2}
	gone    = storefront.Item{ID: "10", Name: "Gone", PriceKobo: kobo(100), Available: false, DurationMinutes: 30}
)

// bookingEnv is a storefront taking bookings Monday to Saturday, 09:00 to
// 12:00. Its clock is Monday 19 October 2026, 10:00 in Lagos, so with the
// default two hours' notice the first free day is Tuesday.
func bookingEnv(t *testing.T, payment string, deposit int) (*formEnv, *fakeCalendar) {
	t.Helper()
	p := samplePage("classic", 0)
	p.Model.HasServices = true
	p.Model.Actions = []storefront.PublicAction{{Kind: "booking", Label: "Book", Payment: payment, DepositPercent: deposit},
		{Kind: "enquiry", Label: "Ask a question", Payment: "none"}}
	e := formServer(t, p)
	c := &fakeCalendar{e: e, holds: map[string]storefront.Busy{}, bookings: map[string]BookingState{}}
	var days [7][]storefront.Span
	for i := range 6 {
		days[i] = []storefront.Span{{Open: "09:00", Close: "12:00"}}
	}
	c.page = ServicesPage{Availability: storefront.Availability{Days: days, Off: []string{"2026-10-22"}}, Services: []storefront.Item{haircut, consult, class, gone}}
	e.s.Services, e.s.Busy, e.s.Book, e.s.Booking, e.s.Change, e.s.PriceChange = c.services, c.busy, c.book, c.booking, c.change, c.priceChange
	return e, c
}

func at(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, storefront.Lagos) }

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func goodBooking(token string, start time.Time) url.Values {
	return url.Values{"token": {token}, "website": {""}, "service": {"7"}, "start": {unix(start)}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"},
		// Whatever the browser says about the service is ignored.
		"duration": {"5"}, "price": {"1"}, "capacity": {"9"}}
}

// bookOne books a haircut at start and returns its status link.
func bookOne(t *testing.T, e *formEnv, start time.Time) string {
	t.Helper()
	_, tok := e.form(t, "/a/booking?service=7")
	*e.now = e.now.Add(10 * time.Second)
	w := e.post("/a/booking", goodBooking(tok, start))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("book: %d %s", w.Code, w.Body.String())
	}
	return w.Header().Get("Location")
}

func refOf(loc string) string {
	p := strings.Split(loc, "?")[0]
	return p[strings.LastIndex(p, "/")+1:]
}

func TestStorefrontListsServices(t *testing.T) {
	e, _ := bookingEnv(t, "after_accept", 0)
	body := get(e.h, "GET", sfHost, "/").Body.String()
	for _, want := range []string{"<h2>Services</h2>", "<h3>Haircut</h3>", "30 min · ₦3,000", "1 hr · Price on the day", "1 hr 30 min · ₦5,000",
		`href="/a/booking?service=7" aria-label="Book Haircut">Book</a>`, `<a class="btn btn-primary" href="/a/booking">Book</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	if strings.Contains(body, "service=10") {
		t.Error("an unavailable service can be booked")
	}

	// Without the booking hooks, or without services, no booking is offered.
	e.s.Book = nil
	if body := get(e.h, "GET", sfHost, "/").Body.String(); strings.Contains(body, "/a/booking") || strings.Contains(body, "<h2>Services</h2>") {
		t.Error("bookings are offered with no way to take them")
	}
	if w := get(e.h, "GET", sfHost, "/a/booking"); w.Code != 503 || !strings.Contains(w.Body.String(), "Bookings are not open yet") {
		t.Fatalf("no Book hook: %d", w.Code)
	}
}

func TestBookingFormRenders(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	w := get(e.h, "GET", sfHost, "/a/booking")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Choose a service.") || !strings.Contains(body, `href="/a/booking?service=8"`) || tokenRe.MatchString(body) {
		t.Fatalf("services: %d %s", w.Code, body)
	}

	c.holds["XX-AAAAAA"] = storefront.Busy{Service: "7", Start: at(20, 9, 0), Until: at(20, 9, 30), Count: 1}
	body, _ = e.form(t, "/a/booking?service=7")
	for _, want := range []string{
		"<strong>Haircut</strong> · 30 min · ₦3,000", `<a href="/a/booking">Choose another</a>`,
		// The first day is Tuesday (Monday is past its notice); Thursday is off, Sunday closed.
		`href="/a/booking?service=7&amp;day=2026-10-20" aria-current="date">Tue 20 Oct</a>`, `href="/a/booking?service=7&amp;day=2026-10-21">Wed 21 Oct</a>`,
		"Times on Tue 20 Oct", `value="` + unix(at(20, 9, 30)) + `" required> 09:30`, `> 11:30`,
		`name="service" value="7"`, "Pay after we confirm.", "to handle your booking", `action="/a/booking"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	for _, not := range []string{"Mon 19 Oct", "Thu 22 Oct", "Sun 25 Oct", "> 09:00", "> 12:00"} {
		if strings.Contains(body, not) {
			t.Errorf("%q is offered", not)
		}
	}
	for _, re := range policyBreaks {
		if re.MatchString(body) {
			t.Errorf("%s in the page", re)
		}
	}
	body, _ = e.form(t, "/a/booking?service=7&day=2026-10-21")
	if !strings.Contains(body, "Times on Wed 21 Oct") || !strings.Contains(body, `> 09:00`) {
		t.Fatalf("?day=: %s", body)
	}
	// A day with nothing free says so.
	if body := get(e.h, "GET", sfHost, "/a/booking?service=7&day=2026-10-22").Body.String(); !strings.Contains(body, "no free times on that day") || tokenRe.MatchString(body) {
		t.Fatalf("a day off: %s", body)
	}
	// The class's 90 minutes fit twice in the morning.
	body, _ = e.form(t, "/a/booking?service=9&day=2026-10-21")
	if !strings.Contains(body, "> 09:00") || !strings.Contains(body, "> 10:30") || strings.Contains(body, "> 10:00") {
		t.Fatalf("class slots: %s", body)
	}
	if w := get(e.h, "GET", sfHost, "/a/booking?service=10"); !strings.Contains(w.Body.String(), "Choose a service.") {
		t.Fatal("an unavailable service has a form")
	}
}

func TestBookingPaymentWords(t *testing.T) {
	for _, c := range []struct {
		payment string
		deposit int
		service string
		want    string
	}{
		{"at_order", 0, "7", "Pay ₦3,000 now to book."},
		{"at_order", 50, "7", "Pay ₦1,500 now to book, and the rest at the appointment."},
		{"after_accept", 0, "7", "Pay after we confirm."},
		{"none", 0, "7", "Pay at the appointment."},
		{"at_order", 0, "8", "Pay at the appointment."}, // priced on the day
	} {
		e, _ := bookingEnv(t, c.payment, c.deposit)
		if body, _ := e.form(t, "/a/booking?service="+c.service); !strings.Contains(body, c.want) {
			t.Errorf("%s %d: no %q", c.payment, c.deposit, c.want)
		}
	}
}

func TestBookingSubmission(t *testing.T) {
	e, c := bookingEnv(t, "at_order", 50)
	loc := bookOne(t, e, at(20, 10, 0))
	m := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})\?k=([a-z2-9]{29})$`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("redirect to %q", loc)
	}
	ref, key := m[1], m[2]
	row := e.inbox.rows[ref]
	if row.kind != "booking" || !bytes.Equal(row.hash, storefront.StatusHash(key)) {
		t.Fatalf("row %+v", row)
	}
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, row.payload)
	if err != nil {
		t.Fatal(err)
	}
	b := sub.Booking
	if b == nil || b.ServiceID != "7" || b.ServiceName != "Haircut" || !b.Start.Equal(at(20, 10, 0)) || !b.End.Equal(at(20, 10, 30)) || !b.Until.Equal(at(20, 10, 30)) || b.Capacity != 1 {
		t.Fatalf("booking %+v", b)
	}
	if sub.Kind != "booking" || sub.TotalKobo != 300000 || sub.DepositKobo != 150000 || sub.Payment != "at_order" || sub.Channel != "direct" ||
		sub.Customer.Name != "Ada Obi" || sub.Customer.Phone != "+2348031234567" || sub.StatusKey != key {
		t.Fatalf("submission %+v", sub)
	}
	if h, ok := c.holds[ref]; !ok || !h.Start.Equal(at(20, 10, 0)) {
		t.Fatal("no hold")
	}
	// While in the inbox, the status page says it has it.
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, "We have your booking "+ref) || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("status: %s", body)
	}

	// A service priced on the day, with a buffer: no total, no deposit.
	_, tok := e.form(t, "/a/booking?service=8")
	*e.now = e.now.Add(10 * time.Second)
	v := goodBooking(tok, at(21, 9, 0))
	v.Set("service", "8")
	w := e.post("/a/booking", v)
	if w.Code != 303 {
		t.Fatalf("consultation: %d %s", w.Code, w.Body.String())
	}
	ref = refOf(w.Header().Get("Location"))
	sub, _ = storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if sub.TotalKobo != 0 || sub.DepositKobo != 0 || !sub.Booking.Until.Equal(at(21, 10, 15)) {
		t.Fatalf("consultation %+v %+v", sub, sub.Booking)
	}
}

func TestBookingRefusals(t *testing.T) {
	e, _ := bookingEnv(t, "after_accept", 0)
	for _, c := range []struct {
		name   string
		change func(v url.Values)
		status int
		msg    string
	}{
		{"off the grid", func(v url.Values) { v.Set("start", unix(at(20, 9, 15))) }, 422, msgPickSlot},
		{"too soon", func(v url.Values) { v.Set("start", unix(at(19, 11, 30))) }, 422, msgPickSlot},
		{"a day off", func(v url.Values) { v.Set("start", unix(at(22, 9, 0))) }, 422, msgPickSlot},
		{"past the window", func(v url.Values) { v.Set("start", unix(at(19, 9, 0).AddDate(0, 0, 40))) }, 422, msgPickSlot},
		{"running past closing", func(v url.Values) { v.Set("service", "9"); v.Set("start", unix(at(20, 11, 0))) }, 422, msgPickSlot},
		{"no time", func(v url.Values) { v.Del("start") }, 422, msgPickSlot},
		{"bad phone", func(v url.Values) { v.Set("phone", "12345") }, 422, "Nigerian mobile number"},
		{"bvn in the name", func(v url.Values) { v.Set("name", "Ada 22123456789") }, 422, "card numbers, BVNs or NINs"},
		{"honeypot", func(v url.Values) { v.Set("website", "x") }, 400, "could not be sent"},
		{"unknown service", func(v url.Values) { v.Set("service", "10") }, 200, "Please choose a service."},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, tok := e.form(t, "/a/booking?service=7")
			*e.now = e.now.Add(10 * time.Second)
			v := goodBooking(tok, at(20, 10, 0))
			c.change(v)
			w := e.post("/a/booking", v)
			if w.Code != c.status || !strings.Contains(w.Body.String(), c.msg) {
				t.Fatalf("%d, want %d %q\n%s", w.Code, c.status, c.msg, w.Body.String())
			}
			if len(e.inbox.rows) != 0 {
				t.Fatal("a refused booking was recorded")
			}
		})
	}
	// Too fast keeps the token and the time chosen.
	_, tok := e.form(t, "/a/booking?service=7")
	w := e.post("/a/booking", goodBooking(tok, at(20, 10, 0)))
	if m := tokenRe.FindStringSubmatch(w.Body.String()); w.Code != 422 || m == nil || m[1] != tok || !strings.Contains(w.Body.String(), `required checked> 10:00`) {
		t.Fatalf("too fast: %d %s", w.Code, w.Body.String())
	}
}

// Two customers open the form; the first books 10:00, the second is told
// it was just taken and shown what is left, keeping what they typed.
func TestBookingTaken(t *testing.T) {
	e, c := bookingEnv(t, "none", 0)
	_, tok1 := e.form(t, "/a/booking?service=7")
	_, tok2 := e.form(t, "/a/booking?service=7")
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post("/a/booking", goodBooking(tok1, at(20, 10, 0))); w.Code != 303 {
		t.Fatalf("first: %d", w.Code)
	}
	v := goodBooking(tok2, at(20, 10, 0))
	v.Set("name", "Bola")
	w := e.post("/a/booking", v)
	body := w.Body.String()
	if w.Code != http.StatusConflict || !strings.Contains(body, msgSlotTaken) || !strings.Contains(body, `value="Bola"`) || !strings.Contains(body, `value="0803 123 4567"`) {
		t.Fatalf("second: %d %s", w.Code, body)
	}
	if strings.Contains(body, "> 10:00") || !strings.Contains(body, "> 10:30") || !strings.Contains(body, "Times on Tue 20 Oct") {
		t.Fatalf("the times shown are stale: %s", body)
	}
	if m := tokenRe.FindStringSubmatch(body); m == nil || m[1] == tok2 {
		t.Fatal("the taken form keeps its token")
	}
	if len(c.holds) != 1 {
		t.Fatalf("%d holds", len(c.holds))
	}
	// A repeat of the first booking is still the first booking.
	if w := e.post("/a/booking", goodBooking(tok1, at(20, 10, 0))); w.Code != 303 || len(e.inbox.rows) != 1 {
		t.Fatalf("a repeat: %d, %d rows", w.Code, len(e.inbox.rows))
	}
}

func TestBookingStatusPage(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	loc := bookOne(t, e, at(20, 10, 0))
	ref := refOf(loc)
	key := strings.Split(loc, "k=")[1]
	for _, s := range []struct {
		status string
		want   []string
		change bool
	}{
		{"received", []string{"Waiting for Ada&#39;s &lt;Kitchen&gt; to confirm", "Haircut, Tue 20 Oct, 10:00", "Price: ₦3,000"}, true},
		{"accepted", []string{"Your booking is confirmed"}, true},
		{"paid", []string{"Your booking is confirmed", "Your payment has been received."}, true},
		{"declined", []string{"Your booking was declined"}, false},
		{"cancelled", []string{"Your booking was cancelled"}, false},
		{"refund_due", []string{"Your payment will be refunded."}, false},
		{"expired", []string{"Your booking expired"}, false},
		{"completed", []string{"Thank you for visiting"}, false},
		{"no_show", []string{"Your booking was missed"}, false},
	} {
		c.recorded(t, ref, s.status)
		body := get(e.h, "GET", sfHost, loc).Body.String()
		for _, want := range s.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", s.status, want)
			}
		}
		links := strings.Contains(body, `href="/o/`+ref+`/move?k=`+key+`">Move to another time</a>`) && strings.Contains(body, `/o/`+ref+`/cancel?k=`+key+`">Cancel booking</a>`)
		if links != s.change {
			t.Errorf("%s: change links %v\n%s", s.status, links, body)
		}
		if strings.Contains(body, "Ada Obi") || strings.Contains(body, "8031234567") {
			t.Errorf("%s: the page shows the customer", s.status)
		}
	}

	c.recorded(t, ref, "awaiting_payment")
	c.bookings[ref] = func() BookingState {
		b := c.bookings[ref]
		b.DepositKobo, b.PaymentURL = 100000, "https://checkout.paystack.test/x"
		return b
	}()
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, ">Pay ₦1,000</a>") || !strings.Contains(body, "Move to another time") {
		t.Fatalf("awaiting payment: %s", body)
	}

	// Past the cut-off (two hours before), the links go and it says so.
	c.recorded(t, ref, "accepted")
	*e.now = at(20, 8, 1)
	body := get(e.h, "GET", sfHost, loc).Body.String()
	if strings.Contains(body, "Move to another time") || !strings.Contains(body, "too close to the time") {
		t.Fatalf("past the cut-off: %s", body)
	}
	for _, page := range []string{"move", "cancel"} {
		if w := get(e.h, "GET", sfHost, "/o/"+ref+"/"+page+"?k="+key); w.Code != 409 || !strings.Contains(w.Body.String(), "Too late to change online") {
			t.Errorf("%s past the cut-off: %d", page, w.Code)
		}
	}
	// A longer cut-off set by the business counts.
	*e.now = at(19, 10, 0)
	c.page.Availability.CutoffMinutes = 25 * 60
	e.s.servicesCache = nil
	if body := get(e.h, "GET", sfHost, loc).Body.String(); strings.Contains(body, "Move to another time") {
		t.Fatal("the business's cut-off is ignored")
	}

	// A wrong key is no page at all.
	if w := get(e.h, "GET", sfHost, "/o/"+ref+"/cancel?k=nope"); w.Code != 404 {
		t.Fatalf("wrong key: %d", w.Code)
	}
}

func TestBookingCancel(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	loc := bookOne(t, e, at(20, 10, 0))
	ref := refOf(loc)
	key := strings.Split(loc, "k=")[1]
	c.recorded(t, ref, "accepted")
	page := "/o/" + ref + "/cancel?k=" + key
	body, tok := e.form(t, page)
	if !strings.Contains(body, "Cancel your booking?") || !strings.Contains(body, "Haircut, Tue 20 Oct, 10:00") || !strings.Contains(body, `action="`+strings.ReplaceAll(page, "&", "&amp;")+`"`) ||
		!strings.Contains(body, "Keep my booking") {
		t.Fatalf("confirm page: %s", body)
	}
	// Too fast keeps the token; the honeypot and another page's token refuse.
	if w := e.post(page, url.Values{"token": {tok}}); w.Code != 422 || !strings.Contains(w.Body.String(), "press the button again") || !strings.Contains(w.Body.String(), tok) {
		t.Fatalf("too fast: %d", w.Code)
	}
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post(page, url.Values{"token": {tok}, "website": {"x"}}); w.Code != 400 {
		t.Fatalf("honeypot: %d", w.Code)
	}
	_, moveTok := e.form(t, "/o/"+ref+"/move?k="+key)
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post(page, url.Values{"token": {moveTok}}); w.Code != 400 || len(c.changes) != 0 {
		t.Fatalf("the move page's token cancelled: %d", w.Code)
	}
	w := e.post(page, url.Values{"token": {tok}})
	if w.Code != 303 || w.Header().Get("Location") != loc {
		t.Fatalf("cancel: %d %q", w.Code, w.Header().Get("Location"))
	}
	if _, held := c.holds[ref]; held || c.bookings[ref].Status != "cancelled" {
		t.Fatal("not cancelled")
	}
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, "Your booking was cancelled") || strings.Contains(body, "Cancel booking") {
		t.Fatalf("after: %s", body)
	}
	// Again: nothing to cancel.
	if w := get(e.h, "GET", sfHost, page); w.Code != 409 || !strings.Contains(w.Body.String(), "can no longer be changed") {
		t.Fatalf("again: %d", w.Code)
	}
}

func TestBookingMove(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	loc := bookOne(t, e, at(20, 10, 0))
	ref := refOf(loc)
	key := strings.Split(loc, "k=")[1]
	c.recorded(t, ref, "received")
	c.holds["XX-OTHER1"] = storefront.Busy{Service: "9", Start: at(20, 11, 0), Until: at(20, 12, 30), Count: 1}
	page := "/o/" + ref + "/move?k=" + key
	body, tok := e.form(t, page)
	for _, want := range []string{"Move your booking", "Booked for Tue 20 Oct, 10:00", "> 09:30", "> 10:30", "Move to this time",
		`href="/o/` + ref + `/move?k=` + key + `&amp;day=2026-10-21"`, "Keep my booking as it is"} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	// Its own time is not offered; the class's is not free; no details are asked.
	for _, not := range []string{"> 10:00", "> 11:00", `name="phone"`} {
		if strings.Contains(body, not) {
			t.Errorf("%q on the move page", not)
		}
	}
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post(page, url.Values{"token": {tok}, "start": {unix(at(20, 10, 0))}}); w.Code != 422 || !strings.Contains(w.Body.String(), msgPickSlot) {
		t.Fatalf("to its own time: %d", w.Code)
	}
	// Taken between the page and the press.
	c.holds["XX-OTHER2"] = storefront.Busy{Service: "7", Start: at(20, 9, 30), Until: at(20, 10, 0), Count: 1}
	w := e.post(page, url.Values{"token": {tok}, "start": {unix(at(20, 9, 30))}})
	if w.Code != 409 || !strings.Contains(w.Body.String(), msgSlotTaken) || strings.Contains(w.Body.String(), "> 09:30") {
		t.Fatalf("taken: %d %s", w.Code, w.Body.String())
	}
	_, tok = e.form(t, page+"&day=2026-10-21")
	*e.now = e.now.Add(10 * time.Second)
	w = e.post(page, url.Values{"token": {tok}, "start": {unix(at(21, 11, 30))}})
	if w.Code != 303 || w.Header().Get("Location") != loc {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	if b := c.bookings[ref]; !b.Start.Equal(at(21, 11, 30)) || !b.End.Equal(at(21, 12, 0)) || !c.holds[ref].Start.Equal(at(21, 11, 30)) {
		t.Fatalf("moved to %v", b.Start)
	}
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, "Haircut, Wed 21 Oct, 11:30") {
		t.Fatalf("after the move: %s", body)
	}
}

func TestPriceChanged(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	loc := bookOne(t, e, at(20, 10, 0))
	ref := refOf(loc)
	key := strings.Split(loc, "k=")[1]
	e.inbox.states[ref] = OrderState{Status: "price_changed", Kind: "order"}
	c.offer = &PriceOffer{TotalKobo: 360000, Gone: []string{"Zobo"}}
	body := get(e.h, "GET", sfHost, loc).Body.String()
	for _, want := range []string{"<h1>Prices have changed</h1>", "No longer available: Zobo.", "The new total is ₦3,600.",
		`action="/o/` + ref + `/prices?k=` + key + `"`, `name="do" value="accept"`, ">Confirm new total</button>", `name="do" value="decline"`, ">Cancel</button>"} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	tok := tokenRe.FindStringSubmatch(body)[1]
	page := "/o/" + ref + "/prices?k=" + key
	if w := e.post(page, url.Values{"token": {tok}, "do": {"accept"}}); w.Code != 422 || !strings.Contains(w.Body.String(), "Prices have changed") {
		t.Fatalf("too fast: %d", w.Code)
	}
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post(page, url.Values{"token": {tok}, "do": {"maybe"}}); w.Code != 400 {
		t.Fatalf("a made-up answer: %d", w.Code)
	}
	if w := e.post(page, url.Values{"token": {tok}, "do": {"accept"}}); w.Code != 303 || w.Header().Get("Location") != loc || e.inbox.states[ref].Status != "received" {
		t.Fatalf("accept: %d", w.Code)
	}
	if w := e.post(page, url.Values{"token": {tok}, "do": {"decline"}}); w.Code != 409 {
		t.Fatalf("after accepting: %d", w.Code)
	}

	// Without the new total, the page says where it went; a booking whose
	// service is gone can only be cancelled.
	e.inbox.states[ref] = OrderState{Status: "price_changed", Kind: "booking"}
	c.offer = nil
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, "the new total was sent to you by SMS") || !strings.Contains(body, "Confirm new total") {
		t.Fatalf("no offer: %s", body)
	}
	c.offer = &PriceOffer{Gone: []string{"Haircut"}}
	body = get(e.h, "GET", sfHost, loc).Body.String()
	if strings.Contains(body, "Confirm new total") || !strings.Contains(body, ">Cancel</button>") {
		t.Fatalf("gone: %s", body)
	}
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post(page, url.Values{"token": {tokenRe.FindStringSubmatch(body)[1]}, "do": {"decline"}}); w.Code != 303 || e.inbox.states[ref].Status != "cancelled" {
		t.Fatalf("decline: %d", w.Code)
	}
	if got := strings.Join(c.changes, ","); got != "price_accept,price_decline" {
		t.Fatalf("changes %s", got)
	}
}

func TestEmbeddedBooking(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	e.s.Origins = func(context.Context, uuid.UUID) ([]string, error) { return []string{"https://adakitchen.ng"}, nil }
	w := get(e.h, "GET", sfHost, "/embed/a/booking")
	if body := w.Body.String(); !strings.Contains(body, `href="/embed/a/booking?service=7"`) || strings.Contains(body, `class="sf-back"`) ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors https://adakitchen.ng") {
		t.Fatalf("embedded list: %s", body)
	}
	body, tok := e.form(t, "/embed/a/booking?service=7")
	if !strings.Contains(body, `action="/embed/a/booking"`) || !strings.Contains(body, `href="/embed/a/booking?service=7&amp;day=2026-10-21"`) {
		t.Fatalf("embedded form: %s", body)
	}
	*e.now = e.now.Add(10 * time.Second)
	w = e.post("/embed/a/booking", goodBooking(tok, at(20, 10, 0)))
	loc := w.Header().Get("Location")
	if w.Code != 303 || !strings.HasPrefix(loc, "/embed/o/") {
		t.Fatalf("embedded booking: %d %q", w.Code, loc)
	}
	ref := refOf(loc)
	if sub, _ := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload); sub.Channel != "embed" {
		t.Fatalf("channel %q", sub.Channel)
	}
	c.recorded(t, ref, "accepted")
	key := strings.Split(loc, "k=")[1]
	if body := get(e.h, "GET", sfHost, loc).Body.String(); !strings.Contains(body, `href="/embed/o/`+ref+`/move?k=`+key+`"`) {
		t.Fatalf("embedded status: %s", body)
	}
	body, tok = e.form(t, "/embed/o/"+ref+"/cancel?k="+key)
	if !strings.Contains(body, `action="/embed/o/`+ref+`/cancel?k=`+key+`"`) {
		t.Fatalf("embedded cancel: %s", body)
	}
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post("/embed/o/"+ref+"/cancel?k="+key, url.Values{"token": {tok}}); w.Code != 303 || w.Header().Get("Location") != loc {
		t.Fatalf("embedded cancel: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestBookingBudget(t *testing.T) {
	e, c := bookingEnv(t, "after_accept", 0)
	c.page.Availability.AheadDays = storefront.MaxAheadDays
	c.page.Availability.Days[6] = []storefront.Span{{Open: "06:00", Close: "22:00"}}
	for i := range 40 {
		it := haircut
		it.ID, it.Name, it.DurationMinutes = strconv.Itoa(100+i), "Service "+strconv.Itoa(i), 5
		c.page.Services = append(c.page.Services, it)
	}
	for _, p := range []string{"/a/booking", "/a/booking?service=100&day=2026-10-25", "/"} {
		if n := get(e.h, "GET", sfHost, p).Body.Len(); n > maxPageHTML {
			t.Errorf("%s: %d bytes, budget %d", p, n, maxPageHTML)
		}
	}
}
