package publicweb

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

var (
	testMaster = bytes.Repeat([]byte{7}, 32)
	sfHost     = "ada-kitchen.shops.test"
)

// fakeInbox stands in for taskiem_public_submit and
// taskiem_public_order_status.
type fakeInbox struct {
	mu     sync.Mutex
	byIdem map[string]string
	rows   map[string]inboxRow
	states map[string]OrderState // what the workflow has made of a reference
	closed bool
	taken  int // answer this many submissions with ErrReferenceTaken first
}

type inboxRow struct {
	sf                  uuid.UUID
	kind                string
	idem, hash, payload []byte
}

func (f *fakeInbox) submit(_ context.Context, sf uuid.UUID, kind, ref string, idem, hash, payload []byte) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.byIdem[hex.EncodeToString(idem)]; ok {
		return r, true, nil
	}
	if f.closed {
		return "", false, nil
	}
	if f.taken > 0 {
		f.taken--
		return "", false, ErrReferenceTaken
	}
	f.byIdem[hex.EncodeToString(idem)] = ref
	f.rows[ref] = inboxRow{sf: sf, kind: kind, idem: idem, hash: hash, payload: payload}
	return ref, true, nil
}

func (f *fakeInbox) status(_ context.Context, ref string, hash []byte) (OrderState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[ref]
	if !ok || !bytes.Equal(row.hash, hash) {
		return OrderState{}, false, nil
	}
	if st, ok := f.states[ref]; ok {
		st.Storefront = row.sf
		return st, true, nil
	}
	return OrderState{Status: "processing", Kind: row.kind, Storefront: row.sf}, true, nil
}

type formEnv struct {
	s     *Server
	h     http.Handler
	inbox *fakeInbox
	sf    uuid.UUID
	now   *time.Time
}

func allForms() []storefront.PublicAction {
	return []storefront.PublicAction{
		{Kind: "order", Label: "Order now", Payment: "after_accept", Pickup: true, Delivery: true, DeliveryNote: "Yaba and Surulere only"},
		{Kind: "enquiry", Label: "Ask a question", Payment: "none"},
		{Kind: "callback", Label: "Request a call back", Payment: "none"},
	}
}

func formServer(t *testing.T, p StorefrontPage) *formEnv {
	t.Helper()
	s, f := storefrontServer(t, p, true)
	e := &formEnv{s: s, inbox: &fakeInbox{byIdem: map[string]string{}, rows: map[string]inboxRow{}, states: map[string]OrderState{}}, sf: f.sites[sfHost].ID}
	now := s.now()
	e.now = &now
	s.now = func() time.Time { return *e.now }
	var err error
	if s.FormKey, err = storefront.FormKey(testMaster); err != nil {
		t.Fatal(err)
	}
	if s.InboxKey, err = storefront.InboxKey(testMaster); err != nil {
		t.Fatal(err)
	}
	s.Submit, s.OrderStatus = e.inbox.submit, e.inbox.status
	s.Submissions = formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)
	e.h = s.Handler()
	return e
}

func orderPage() StorefrontPage {
	p := samplePage("classic", 4) // item 1 is unavailable
	p.Model.Actions = allForms()
	return p
}

var tokenRe = regexp.MustCompile(`name="token" value="([A-Za-z0-9_-]+)"`)

// form opens a form page and returns its body and token.
func (e *formEnv) form(t *testing.T, path string) (string, string) {
	t.Helper()
	w := get(e.h, "GET", sfHost, path)
	if w.Code != 200 {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
	}
	m := tokenRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("GET %s: no token", path)
	}
	return w.Body.String(), m[1]
}

func (e *formEnv) post(path string, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
	r.Host = sfHost
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func goodOrder(token string) url.Values {
	return url.Values{"token": {token}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "email": {"Ada@Example.com"},
		"q.2": {"2"}, "q.3": {"1"}, "q.4": {"0"}, "fulfilment": {"delivery"}, "address": {"4 Akoka Road, Yaba"}, "message": {"No pepper please"},
		// Whatever the browser says about prices is ignored.
		"price.2": {"1"}, "total": {"100"}}
}

func TestFormsRender(t *testing.T) {
	e := formServer(t, orderPage())
	for _, kind := range []string{"order", "enquiry", "callback"} {
		w := get(e.h, "GET", sfHost, "/a/"+kind)
		body := w.Body.String()
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
			t.Fatalf("%s: %d %v", kind, w.Code, w.Header())
		}
		for _, want := range []string{
			`name="token" value="`, `<div class="hp" aria-hidden="true">`, `name="website"`, `tabindex="-1"`,
			`action="/a/` + kind + `"`, "Your details go to Ada&#39;s &lt;Kitchen&gt; only, to " + purposes[kind],
			"Never enter a password or card PIN on this page.", `name="phone"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", kind, want)
			}
		}
		for _, re := range policyBreaks {
			if re.MatchString(body) {
				t.Errorf("%s: %s in the page", kind, re)
			}
		}
	}
	body, _ := e.form(t, "/a/order?item=2")
	for _, want := range []string{`name="q.2" value="1"`, `name="q.3" value="0"`, `data-price="150050"`, "Yaba and Surulere only",
		`value="pickup"`, `value="delivery"`, `name="address"`, `data-order`, `<script src="/_static/order.`} {
		if !strings.Contains(body, want) {
			t.Errorf("order: no %q", want)
		}
	}
	if strings.Contains(body, `name="q.1"`) {
		t.Error("an unavailable item can be ordered")
	}
	if body, _ := e.form(t, "/a/order?item=1"); strings.Contains(body, `value="1"`) && strings.Contains(body, `name="q.1"`) {
		t.Error("?item= pre-filled an unavailable item")
	}
	// Two views, two tokens: a cached form would merge customers' orders.
	_, t1 := e.form(t, "/a/enquiry")
	_, t2 := e.form(t, "/a/enquiry")
	if t1 == t2 {
		t.Fatal("the same token twice")
	}
}

func TestOrderSubmission(t *testing.T) {
	e := formServer(t, orderPage())
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(20 * time.Second)
	w := e.post("/a/order", goodOrder(tok))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	loc := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})\?k=([a-z2-9]{29})$`).FindStringSubmatch(w.Header().Get("Location"))
	if loc == nil {
		t.Fatalf("redirect to %q", w.Header().Get("Location"))
	}
	ref, key := loc[1], loc[2]
	row, ok := e.inbox.rows[ref]
	if !ok || row.sf != e.sf || row.kind != "order" || !bytes.Equal(row.hash, storefront.StatusHash(key)) {
		t.Fatalf("inbox row %+v", row)
	}
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, row.payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []storefront.Line{{ID: "2", Name: "Dish number 2", Quantity: 2, PriceKobo: 150050, LineKobo: 300100}, {ID: "3", Name: "Dish number 3", Quantity: 1, PriceKobo: 150100, LineKobo: 150100}}
	if len(sub.Basket) != 2 || sub.Basket[0] != want[0] || sub.Basket[1] != want[1] || sub.TotalKobo != 450200 {
		t.Fatalf("basket %+v total %d", sub.Basket, sub.TotalKobo)
	}
	c := sub.Customer
	if c.Name != "Ada Obi" || c.Phone != "+2348031234567" || c.Email != "ada@example.com" || c.Address != "4 Akoka Road, Yaba" || c.Message != "No pepper please" {
		t.Fatalf("customer %+v", c)
	}
	if sub.Kind != "order" || sub.Fulfilment != "delivery" || sub.Payment != "after_accept" || sub.Channel != "direct" || sub.StatusKey != key || !sub.Submitted.Equal(e.now.UTC()) {
		t.Fatalf("submission %+v", sub)
	}

	// The status page it leads to.
	sw := get(e.h, "GET", sfHost, w.Header().Get("Location"))
	if sw.Code != 200 || !strings.Contains(sw.Body.String(), "We have your order "+ref) || !strings.Contains(sw.Body.String(), `http-equiv="refresh" content="3"`) {
		t.Fatalf("status: %d %s", sw.Code, sw.Body.String())
	}
}

func TestRepeatSubmission(t *testing.T) {
	e := formServer(t, orderPage())
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	first := e.post("/a/order", goodOrder(tok))
	again := e.post("/a/order", goodOrder(tok))
	if first.Code != 303 || again.Code != 303 || first.Header().Get("Location") != again.Header().Get("Location") {
		t.Fatalf("a repeat went to %q, the first to %q", again.Header().Get("Location"), first.Header().Get("Location"))
	}
	if len(e.inbox.rows) != 1 {
		t.Fatalf("%d submissions", len(e.inbox.rows))
	}
	// The repeat's status link works: its key is the first one's.
	if w := get(e.h, "GET", sfHost, again.Header().Get("Location")); w.Code != 200 {
		t.Fatalf("the repeat's status link: %d", w.Code)
	}
	// Another form view is another order.
	_, tok2 := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post("/a/order", goodOrder(tok2)); w.Header().Get("Location") == first.Header().Get("Location") {
		t.Fatal("two orders, one reference")
	}
	// A reference that is taken is drawn again.
	e.inbox.taken = 2
	_, tok3 := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post("/a/order", goodOrder(tok3)); w.Code != 303 || len(e.inbox.rows) != 3 {
		t.Fatalf("after a taken reference: %d, %d rows", w.Code, len(e.inbox.rows))
	}
}

func TestOtherForms(t *testing.T) {
	e := formServer(t, orderPage())
	_, tok := e.form(t, "/a/enquiry")
	*e.now = e.now.Add(10 * time.Second)
	w := e.post("/a/enquiry", url.Values{"token": {tok}, "name": {"Bola"}, "phone": {"+234 809 000 1111"}, "message": {"Do you cater weddings?"}})
	if w.Code != 303 {
		t.Fatalf("enquiry: %d %s", w.Code, w.Body.String())
	}
	ref := strings.TrimPrefix(strings.Split(w.Header().Get("Location"), "?")[0], "/o/")
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil || sub.Kind != "enquiry" || sub.Customer.Message != "Do you cater weddings?" || sub.Customer.Phone != "+2348090001111" || sub.Payment != "none" || len(sub.Basket) != 0 {
		t.Fatalf("enquiry %+v %v", sub, err)
	}
	e.inbox.states[ref] = OrderState{Status: "received", Kind: "enquiry"}
	if body := get(e.h, "GET", sfHost, w.Header().Get("Location")).Body.String(); !strings.Contains(body, "Your question was sent") {
		t.Fatalf("enquiry status: %s", body)
	}

	_, tok = e.form(t, "/embed/a/callback")
	*e.now = e.now.Add(10 * time.Second)
	w = e.post("/embed/a/callback", url.Values{"token": {tok}, "name": {"Chidi"}, "phone": {"07031234567"}, "best_time": {"After 5pm"}})
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/embed/o/") {
		t.Fatalf("callback: %d %q", w.Code, w.Header().Get("Location"))
	}
	ref = strings.TrimPrefix(strings.Split(w.Header().Get("Location"), "?")[0], "/embed/o/")
	sub, err = storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil || sub.Kind != "callback" || sub.Customer.BestTime != "After 5pm" || sub.Channel != "embed" {
		t.Fatalf("callback %+v %v", sub, err)
	}
	e.inbox.states[ref] = OrderState{Status: "received", Kind: "callback"}
	if body := get(e.h, "GET", sfHost, w.Header().Get("Location")).Body.String(); !strings.Contains(body, "We&#39;ll call you back") {
		t.Fatalf("callback status: %s", body)
	}
}

func TestFormRefusals(t *testing.T) {
	e := formServer(t, orderPage())
	_, enquiryTok := e.form(t, "/a/enquiry")
	for _, c := range []struct {
		name   string
		change func(v url.Values)
		wait   bool
		status int
		msg    string
		gone   string // a value the refusal must not show again
	}{
		{"honeypot", func(v url.Values) { v.Set("website", "http://spam.test") }, true, 400, "could not be sent", ""},
		{"too fast", func(url.Values) {}, false, 422, "Please check your details and send again.", ""},
		{"bad phone", func(v url.Values) { v.Set("phone", "12345") }, true, 422, "Nigerian mobile number", `value="12345"`},
		{"no name", func(v url.Values) { v.Set("name", " ") }, true, 422, "your name", ""},
		{"bad email", func(v url.Values) { v.Set("email", "ada@") }, true, 422, "email address", `value="ada@"`},
		{"unavailable item", func(v url.Values) { v.Set("q.1", "1") }, true, 422, "Dish number 1 is not available", ""},
		{"quantity 0", func(v url.Values) { v.Set("q.2", "0"); v.Set("q.3", "0") }, true, 422, "at least one item", ""},
		{"quantity 21", func(v url.Values) { v.Set("q.2", "21") }, true, 422, "from 0 to 20 of Dish number 2", ""},
		{"negative quantity", func(v url.Values) { v.Set("q.2", "-1") }, true, 422, "from 0 to 20", ""},
		{"no address", func(v url.Values) { v.Set("address", "") }, true, 422, "delivery address", ""},
		{"no fulfilment", func(v url.Values) { v.Del("fulfilment") }, true, 422, "pickup or delivery", ""},
		{"tampered token", func(v url.Values) {
			b := []byte(v.Get("token"))
			b[10] ^= 1
			v.Set("token", string(b))
		}, true, 400, "could not be sent", ""},
		{"wrong form's token", func(v url.Values) { v.Set("token", enquiryTok) }, true, 400, "could not be sent", ""},
		{"card number", func(v url.Values) { v.Set("message", "my card is 4111 1111 1111 1111") }, true, 422, "card numbers, BVNs or NINs", "4111"},
		{"bvn", func(v url.Values) { v.Set("message", "bvn 22123456789") }, true, 422, "card numbers, BVNs or NINs", "22123456789"},
		{"long message", func(v url.Values) { v.Set("message", strings.Repeat("a", 501)) }, true, 422, "500 characters", ""},
		{"control character", func(v url.Values) { v.Set("name", "Ada\x00") }, true, 422, "your name", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, tok := e.form(t, "/a/order")
			if c.wait {
				*e.now = e.now.Add(10 * time.Second)
			}
			v := goodOrder(tok)
			c.change(v)
			w := e.post("/a/order", v)
			body := w.Body.String()
			if w.Code != c.status || !strings.Contains(body, c.msg) || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %q\n%s", w.Code, c.msg, body)
			}
			if c.gone != "" && strings.Contains(body, c.gone) {
				t.Errorf("the refused value %q is shown again", c.gone)
			}
			if c.name != "no name" && c.name != "control character" && !strings.Contains(body, `value="Ada Obi"`) {
				t.Error("the name was not kept")
			}
			if !tokenRe.MatchString(body) {
				t.Error("the form came back without a token")
			}
			if len(e.inbox.rows) != 0 {
				t.Fatalf("a refused form was recorded")
			}
		})
	}

	// Too fast keeps its token, which works a moment later.
	_, tok := e.form(t, "/a/order")
	w := e.post("/a/order", goodOrder(tok))
	if m := tokenRe.FindStringSubmatch(w.Body.String()); w.Code != 422 || m == nil || m[1] != tok {
		t.Fatalf("too fast: %d", w.Code)
	}
	*e.now = e.now.Add(5 * time.Second)
	if w := e.post("/a/order", goodOrder(tok)); w.Code != 303 {
		t.Fatalf("after waiting: %d", w.Code)
	}
}

func TestSubmissionLimit(t *testing.T) {
	e := formServer(t, orderPage())
	e.s.Submissions = formguard.NewLimiter(rate.Every(time.Hour), 2, 100, time.Hour)
	codes := map[int]int{}
	for range 3 {
		_, tok := e.form(t, "/a/enquiry")
		*e.now = e.now.Add(10 * time.Second)
		w := e.post("/a/enquiry", url.Values{"token": {tok}, "name": {"Bola"}, "phone": {"08090001111"}, "message": {"Open on Sunday?"}})
		codes[w.Code]++
		if w.Code == 429 && !strings.Contains(w.Body.String(), "Too many forms") {
			t.Fatal(w.Body.String())
		}
	}
	if codes[303] != 2 || codes[429] != 1 {
		t.Fatalf("codes %v", codes)
	}
}

func TestStatusPage(t *testing.T) {
	e := formServer(t, orderPage())
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	loc := e.post("/a/order", goodOrder(tok)).Header().Get("Location")
	ref := strings.TrimPrefix(strings.Split(loc, "?")[0], "/o/")
	for _, c := range []struct {
		state   OrderState
		want    []string
		refresh bool
	}{
		{OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200, PaymentURL: "https://checkout.paystack.test/abc"},
			[]string{`<a class="btn btn-primary btn-big" href="https://checkout.paystack.test/abc" rel="noopener noreferrer">Pay ₦4,502</a>`, "Your order is held for 30 minutes"}, false},
		{OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, []string{"preparing your payment"}, true},
		{OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200, PaymentURL: "javascript:alert(1)"}, []string{"preparing your payment"}, true},
		{OrderState{Status: "received", Kind: "order", TotalKobo: 450200}, []string{"Order received", "Ada&#39;s &lt;Kitchen&gt; will confirm by SMS", "₦4,502"}, false},
		{OrderState{Status: "paid", Kind: "order"}, []string{"Order received", "will confirm by SMS"}, false},
		{OrderState{Status: "accepted", Kind: "order"}, []string{"accepted"}, false},
		{OrderState{Status: "declined", Kind: "order"}, []string{"declined"}, false},
		{OrderState{Status: "completed", Kind: "order"}, []string{"complete"}, false},
		{OrderState{Status: "expired", Kind: "order"}, []string{"expired"}, false},
		{OrderState{Status: "cancelled", Kind: "order"}, []string{"cancelled"}, false},
		{OrderState{Status: "refund_due", Kind: "order"}, []string{"refunded"}, false},
	} {
		e.inbox.states[ref] = c.state
		w := get(e.h, "GET", sfHost, loc)
		body := w.Body.String()
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, ref) {
			t.Fatalf("%s: %d %v", c.state.Status, w.Code, w.Header())
		}
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("%s: no %q in\n%s", c.state.Status, s, body)
			}
		}
		if got := strings.Contains(body, `http-equiv="refresh"`); got != c.refresh {
			t.Errorf("%s: refresh %v", c.state.Status, got)
		}
		for _, personal := range []string{"Ada Obi", "8031234567", "Akoka", "pepper", "javascript:"} {
			if strings.Contains(body, personal) {
				t.Errorf("%s: the page shows %q", c.state.Status, personal)
			}
		}
	}

	// Embedded, payment opens outside the frame.
	e.inbox.states[ref] = OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 100, PaymentURL: "https://checkout.paystack.test/abc"}
	if body := get(e.h, "GET", sfHost, "/embed"+loc).Body.String(); !strings.Contains(body, `target="_blank" rel="noopener noreferrer"`) {
		t.Fatalf("embedded payment link: %s", body)
	}

	// A wrong key and an unknown reference are the same 404.
	key := strings.Split(loc, "k=")[1]
	wrong := get(e.h, "GET", sfHost, "/o/"+ref+"?k=a"+key[1:])
	unknown := get(e.h, "GET", sfHost, "/o/AD-ZZZZZZ?k="+key)
	if wrong.Code != 404 || unknown.Code != 404 || wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("wrong key %d, unknown %d", wrong.Code, unknown.Code)
	}
	for _, p := range []string{"/o/" + ref, "/o/" + ref + "?k=", "/o/" + ref + "/x?k=" + key} {
		if w := get(e.h, "GET", sfHost, p); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	// Another storefront's order is not shown on this one.
	row := e.inbox.rows[ref]
	row.sf = uuid.New()
	e.inbox.rows[ref] = row
	if w := get(e.h, "GET", sfHost, loc); w.Code != 404 {
		t.Fatalf("another storefront's order: %d", w.Code)
	}
}

func TestFormsNotOffered(t *testing.T) {
	p := samplePage("classic", 4)
	p.Model.Actions = []storefront.PublicAction{{Kind: "enquiry", Label: "Ask", Payment: "none"}}
	e := formServer(t, p)
	for _, path := range []string{"/a/order", "/a/booking", "/a/", "/embed/a/order"} {
		if w := get(e.h, "GET", sfHost, path); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if w := e.post("/a/order", url.Values{}); w.Code != 404 {
		t.Errorf("POST a form not offered: %d", w.Code)
	}
	body := get(e.h, "GET", sfHost, "/").Body.String()
	if strings.Contains(body, "/a/order") || !strings.Contains(body, `href="/a/enquiry"`) {
		t.Error("the storefront offers the wrong forms")
	}

	// Closed: the forms say so, and take nothing.
	p = orderPage()
	p.Status, p.ClosedMessage = "closed", "Back on Monday"
	e = formServer(t, p)
	w := get(e.h, "GET", sfHost, "/a/order")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "closed") || !strings.Contains(w.Body.String(), "Back on Monday") || tokenRe.MatchString(w.Body.String()) {
		t.Fatalf("closed: %d %s", w.Code, w.Body.String())
	}
	if w := e.post("/a/order", url.Values{"name": {"x"}}); w.Code != 409 || !strings.Contains(w.Body.String(), "closed") {
		t.Fatalf("closed POST: %d", w.Code)
	}

	// Closed since the page was cached: the database refuses it.
	e = formServer(t, orderPage())
	e.inbox.closed = true
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	if w := e.post("/a/order", goodOrder(tok)); w.Code != 409 || !strings.Contains(w.Body.String(), "not taking this form") {
		t.Fatalf("refused by the database: %d", w.Code)
	}

	// Without TASKIEM_PUBLIC_KEY.
	s, _ := storefrontServer(t, orderPage(), true)
	h := s.Handler()
	if w := get(h, "GET", sfHost, "/a/order"); w.Code != 503 || !strings.Contains(w.Body.String(), "Orders are not open yet") {
		t.Fatalf("no key: %d", w.Code)
	}
	if body := get(h, "GET", sfHost, "/").Body.String(); strings.Contains(body, "/a/order") {
		t.Fatal("the storefront offers forms that are off")
	}
}

func TestStorefrontOffersForms(t *testing.T) {
	e := formServer(t, orderPage())
	body := get(e.h, "GET", sfHost, "/").Body.String()
	for _, want := range []string{`<a class="btn btn-primary" href="/a/order">Order now</a>`, `<a class="btn" href="/a/enquiry">Ask a question</a>`,
		`href="/a/callback"`, `href="/a/order?item=2"`, `<a class="btn" href="https://wa.me/`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q", want)
		}
	}
	if strings.Contains(body, `href="/a/order?item=1"`) {
		t.Error("an unavailable item can be added")
	}
}

func TestEmbeddedForm(t *testing.T) {
	e := formServer(t, orderPage())
	e.s.Origins = func(context.Context, uuid.UUID) ([]string, error) { return []string{"https://adakitchen.ng"}, nil }
	w := get(e.h, "GET", sfHost, "/embed/a/order")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors https://adakitchen.ng") || w.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("embed: %d %v", w.Code, w.Header())
	}
	if body := w.Body.String(); !strings.Contains(body, `action="/embed/a/order"`) || strings.Contains(body, `class="sf-back"`) {
		t.Fatal("the embedded form posts elsewhere or links out")
	}
	if w := get(e.h, "GET", sfHost, "/a/order"); w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("the direct form is frameable")
	}
}

func TestFormBudget(t *testing.T) {
	p := samplePage("bold", 100)
	p.Model.Actions = allForms()
	e := formServer(t, p)
	body, _ := e.form(t, "/a/order")
	if len(body) > maxPageHTML {
		t.Fatalf("a 100-item order form is %d bytes, budget %d", len(body), maxPageHTML)
	}
	t.Logf("100-item order form: %d bytes", len(body))
}

func TestSensitive(t *testing.T) {
	for s, want := range map[string]bool{
		"":                              false,
		"call 08031234567 after 5":      false,
		"+2348031234567":                false,
		"order 12 for the party":        false,
		"card 4111111111111111":         true,
		"5399 8300 0000 0008":           true,
		"my bvn is 22123456789":         true,
		"nin 12345678901":               true,
		"[card] is a word here, really": false,
	} {
		if got := sensitive(s); got != want {
			t.Errorf("sensitive(%q) = %v", s, got)
		}
	}
}

// Where the catalogue tracks stock, a form offers no more than is left
// and a sold-out item cannot be ordered.
func TestFormStock(t *testing.T) {
	p := orderPage()
	one, none := int64(1), int64(0)
	p.Items[1].Stock, p.Items[2].Stock = &one, &none // dishes 2 and 3
	e := formServer(t, p)
	body, _ := e.form(t, "/a/order")
	if !strings.Contains(body, `name="q.2" value="0" min="0" max="1"`) || !strings.Contains(body, `<span>Dish number 3</span> <span class="sf-badge">Sold out</span>`) {
		t.Fatalf("the form does not show the stock:\n%s", body)
	}
	if page := get(e.h, "GET", sfHost, "/").Body.String(); !strings.Contains(page, `<span class="sf-badge">Sold out</span>`) {
		t.Fatalf("the page does not show sold out:\n%s", page)
	}
	for _, c := range []struct {
		q2, q3 string
		status int
		msg    string
	}{
		{"2", "0", 422, "Only 1 of Dish number 2 left"},
		{"1", "1", 422, "Dish number 3 is sold out"},
		{"1", "0", 303, ""},
	} {
		_, tok := e.form(t, "/a/order")
		*e.now = e.now.Add(10 * time.Second)
		v := goodOrder(tok)
		v.Set("q.2", c.q2)
		v.Set("q.3", c.q3)
		w := e.post("/a/order", v)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.msg) {
			t.Fatalf("%s and %s: %d, want %d %q\n%s", c.q2, c.q3, w.Code, c.status, c.msg, w.Body.String())
		}
	}
}
