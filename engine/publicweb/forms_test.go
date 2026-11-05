package publicweb

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/brand"
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
	action              *uuid.UUID
	kind                string
	idem, hash, payload []byte
}

func (f *fakeInbox) submit(_ context.Context, sf uuid.UUID, action *uuid.UUID, kind, ref string, idem, hash, payload []byte) (string, bool, error) {
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
	f.rows[ref] = inboxRow{sf: sf, action: action, kind: kind, idem: idem, hash: hash, payload: payload}
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
	if !reflect.DeepEqual(sub.Basket, want) || sub.TotalKobo != 450200 {
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
			[]string{`href="https://checkout.paystack.test/abc" rel="noopener noreferrer">Pay ₦4,502</a>`, "Your order is held for 30 minutes"}, false},
		// A deposit: the card page charges the deposit, and the button says so.
		{OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200, AskedKobo: 135060, PaymentURL: "https://checkout.paystack.test/dep"},
			[]string{`href="https://checkout.paystack.test/dep" rel="noopener noreferrer">Pay ₦1,350.60</a>`}, false},
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
		// Paid in parts (fronts F5): what is paid of what it costs, and a
		// later step's own page for what is asked now.
		{OrderState{Status: "accepted", Kind: "order", TotalKobo: 450200, DueKobo: 450200, PaidKobo: 200000}, []string{"Paid ₦2,000 of ₦4,502."}, false},
		{OrderState{Status: "accepted", Kind: "order", TotalKobo: 450200, DueKobo: 450200, PaidKobo: 200000, AskedKobo: 250200, PaymentURL: "https://checkout.paystack.test/bal"},
			[]string{"Paid ₦2,000 of ₦4,502.", "₦2,502 is due now.", `href="https://checkout.paystack.test/bal" rel="noopener noreferrer">Pay the balance, ₦2,502</a>`}, false},
		{OrderState{Status: "accepted", Kind: "order", TotalKobo: 450200, DueKobo: 450200, PaidKobo: 200000, AskedKobo: 100000, PaymentURL: "https://checkout.paystack.test/part"},
			[]string{"₦1,000 is due now.", `>Pay ₦1,000</a>`}, false},
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
	other := "a"
	if key[0] == 'a' {
		other = "b" // never the key itself
	}
	wrong := get(e.h, "GET", sfHost, "/o/"+ref+"?k="+other+key[1:])
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
	for _, want := range []string{`href="/a/order">Order now</a>`, `href="/a/enquiry">Ask a question</a>`,
		`href="/a/callback"`, `href="/a/order?item=2"`, `href="https://wa.me/`} {
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
	if !strings.Contains(body, `name="q.2" value="0" min="0" max="1"`) || !strings.Contains(body, `<span>Dish number 3</span> <span class="badge">Sold out</span>`) {
		t.Fatalf("the form does not show the stock:\n%s", body)
	}
	if page := get(e.h, "GET", sfHost, "/").Body.String(); !strings.Contains(page, `<span class="badge">Sold out</span>`) {
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

// With delivery areas, the order form offers them with their fees (in
// every theme); delivery needs one, and its fee is added to the total.
// Pickup needs none.
func TestOrderDeliveryArea(t *testing.T) {
	for _, theme := range []string{"classic", "bold", "editorial"} {
		p := samplePage(theme, 4)
		p.Model.Actions = allForms()
		p.Model.Actions[0].Zones = []storefront.Zone{{Name: "Yaba", FeeKobo: 150000}, {Name: "Akoka", FeeKobo: 0}}
		e := formServer(t, p)
		body, tok := e.form(t, "/a/order")
		if !strings.Contains(body, `<option value="Yaba" data-fee="150000">Yaba, ₦1,500 delivery</option>`) || !strings.Contains(body, "Akoka, free delivery") {
			t.Fatalf("%s: the areas are not offered: %s", theme, body)
		}
		*e.now = e.now.Add(20 * time.Second)
		v := goodOrder(tok)
		if w := e.post("/a/order", v); w.Code != 422 || !strings.Contains(w.Body.String(), "choose the area") {
			t.Fatalf("%s: no area: %d %s", theme, w.Code, w.Body.String())
		}
		_, tok = e.form(t, "/a/order")
		*e.now = e.now.Add(20 * time.Second)
		v = goodOrder(tok)
		v.Set("zone", "Yaba")
		w := e.post("/a/order", v)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", theme, w.Code, w.Body.String())
		}
		ref := regexp.MustCompile(`/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
		sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
		if err != nil {
			t.Fatal(err)
		}
		if sub.Zone != "Yaba" || sub.DeliveryKobo != 150000 || sub.TotalKobo != 450200+150000 {
			t.Fatalf("%s: %+v", theme, sub)
		}
		// Pickup: no area, no fee.
		_, tok = e.form(t, "/a/order")
		*e.now = e.now.Add(20 * time.Second)
		v = goodOrder(tok)
		v.Set("fulfilment", "pickup")
		v.Set("zone", "Yaba")
		w = e.post("/a/order", v)
		ref = regexp.MustCompile(`/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
		if sub, _ := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload); sub.Zone != "" || sub.TotalKobo != 450200 {
			t.Fatalf("%s: pickup %+v", theme, sub)
		}
	}
}

// Where a delivery goes beyond the address (dispatch D2), and paying at
// the door (D4): a landmark and a map pin (a maps link, or the browser's
// latitude and longitude); without areas, a typed area; pay when it
// arrives where the business allows it.
func TestOrderDropAndPayAtDoor(t *testing.T) {
	p := orderPage()
	p.Model.Actions[0].PayOnDelivery = true
	e := formServer(t, p)
	body, tok := e.form(t, "/a/order")
	for _, want := range []string{`name="landmark"`, `name="pin"`, `name="area"`, `value="on_delivery"`, "When it arrives"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the form lacks %s", want)
		}
	}
	*e.now = e.now.Add(20 * time.Second)
	v := goodOrder(tok)
	v.Set("area", "Akoka")
	v.Set("landmark", "Opposite the bank")
	v.Set("pin", "6.5164, 3.3891")
	v.Set("pay", "on_delivery")
	w := e.post("/a/order", v)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Zone != "Akoka" || sub.Customer.Landmark != "Opposite the bank" || sub.Customer.Pin != "https://www.google.com/maps?q=6.516400,3.389100" ||
		sub.Payment != "on_delivery" || sub.DeliveryKobo != 0 {
		t.Fatalf("submission %+v", sub)
	}
	// A pin that is not a maps link is refused; a maps app's link is kept.
	for _, c := range []struct {
		pin string
		ok  bool
	}{{"https://evil.example/x", false}, {"91,3", false}, {"https://maps.app.goo.gl/abc123", true}} {
		_, tok = e.form(t, "/a/order")
		*e.now = e.now.Add(20 * time.Second)
		v = goodOrder(tok)
		v.Set("pin", c.pin)
		w = e.post("/a/order", v)
		if got := w.Code == http.StatusSeeOther; got != c.ok {
			t.Errorf("pin %q: %d", c.pin, w.Code)
		}
	}
	// Pay when it arrives is not offered where the business does not allow it.
	e = formServer(t, orderPage())
	if body, _ := e.form(t, "/a/order"); strings.Contains(body, `value="on_delivery"`) {
		t.Error("pay at the door offered without the setting")
	}
}

// Fronts F1 (§5.4): the order and booking pages are drawn from the
// action's form. A snapshot that carries the built-in forms (the basket,
// the time) renders exactly what one without forms does; an action's own
// question is shown with the hosted forms' fields, refused when required
// and missing, and sent as the submission's answers.
func TestPagesDrawnFromTheActionsForm(t *testing.T) {
	render := func(withForms bool, extra string) (*formEnv, map[string]string) {
		p := orderPage()
		p.Model.HasServices = true
		p.Model.Actions = append(p.Model.Actions, storefront.PublicAction{Kind: "booking", Label: "Book", Payment: "none"})
		if withForms {
			for i := range p.Model.Actions {
				p.Model.Actions[i].Form = storefront.BuiltinForm(p.Model.Actions[i].Kind)
				if extra != "" && (p.Model.Actions[i].Kind == "order" || p.Model.Actions[i].Kind == "booking") {
					var f map[string]any
					_ = json.Unmarshal(p.Model.Actions[i].Form, &f)
					var q any
					_ = json.Unmarshal([]byte(extra), &q)
					f["properties"].(map[string]any)["spice"] = q
					f["required"] = []any{"spice"}
					p.Model.Actions[i].Form, _ = json.Marshal(f)
				}
			}
		}
		e := formServer(t, p)
		c := &fakeCalendar{e: e, holds: map[string]storefront.Busy{}, bookings: map[string]BookingState{}}
		var days [7][]storefront.Span
		for i := range 6 {
			days[i] = []storefront.Span{{Open: "09:00", Close: "12:00"}}
		}
		c.page = ServicesPage{Availability: storefront.Availability{Days: days}, Services: []storefront.Item{haircut}}
		e.s.Services, e.s.Busy, e.s.Book, e.s.Booking, e.s.Change, e.s.PriceChange = c.services, c.busy, c.book, c.booking, c.change, c.priceChange
		out := map[string]string{}
		for _, path := range []string{"/a/order", "/a/enquiry", "/a/callback", "/a/booking?service=7"} {
			out[path] = tokenRe.ReplaceAllString(get(e.h, "GET", sfHost, path).Body.String(), `name="token" value="T"`)
		}
		return e, out
	}
	_, without := render(false, "")
	_, with := render(true, "")
	for path, body := range without {
		if with[path] != body {
			t.Errorf("%s: the built-in form changes the page", path)
		}
	}

	e, pages := render(true, `{"type": "string", "title": "How hot?", "enum": ["mild", "hot"], "x-form": {"widget": "radio"}}`)
	for _, path := range []string{"/a/order", "/a/booking?service=7"} {
		if !strings.Contains(pages[path], "How hot?") || !strings.Contains(pages[path], `name="a.spice" value="hot"`) {
			t.Fatalf("%s: the question is not shown", path)
		}
	}
	if strings.Contains(pages["/a/enquiry"], "How hot?") {
		t.Fatal("another action's question shown")
	}
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(20 * time.Second)
	w := e.post("/a/order", goodOrder(tok))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "How hot?") || !strings.Contains(w.Body.String(), "4 Akoka Road, Yaba</textarea>") {
		t.Fatalf("a required question left out: %d %s", w.Code, w.Body.String())
	}
	v := goodOrder(tok)
	v.Set("a.spice", "hot")
	if w = e.post("/a/order", v); w.Code != http.StatusSeeOther {
		t.Fatalf("answered: %d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil || len(sub.Answers) != 1 || sub.Answers["spice"] != "hot" || sub.TotalKobo != 450200 {
		t.Fatalf("the submission's answers: %v %v", sub.Answers, err)
	}
}

// Fronts F2: an action copied for another workflow and switched on is
// served at /a/<its slug>, has its own button after the built-in forms,
// posts to its own address and is sent to the inbox naming it.
func TestCopiedActionServed(t *testing.T) {
	builtin, copied := uuid.New(), uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada's <Kitchen>"}
	for _, a := range p.Model.Actions {
		a.Slug, a.Form = a.Kind, storefront.BuiltinForm(a.Kind)
		if a.Kind == "order" {
			a.ID = &builtin
		}
		home.Actions = append(home.Actions, a)
	}
	home.Actions = append(home.Actions, storefront.PublicAction{ID: &copied, Slug: "order-2", Kind: "order", Label: "Food delivery", Payment: "after_accept",
		Pickup: true, Form: storefront.BuiltinForm("order")})
	p.Model.Fronts = []storefront.PublicFront{home}
	e := formServer(t, p)

	page := get(e.h, "GET", sfHost, "/").Body.String()
	if !strings.Contains(page, `href="/a/order-2">Food delivery</a>`) || !strings.Contains(page, `href="/a/order"`) {
		t.Fatalf("the buttons: %s", page)
	}
	body, tok := e.form(t, "/a/order-2")
	if !strings.Contains(body, `action="/a/order-2"`) || !strings.Contains(body, "<h1>Food delivery</h1>") {
		t.Fatal("the copy's page")
	}
	if w := get(e.h, "GET", sfHost, "/a/order-3"); w.Code != http.StatusNotFound {
		t.Fatalf("an action the storefront does not have: %d", w.Code)
	}
	*e.now = e.now.Add(20 * time.Second)
	v := goodOrder(tok)
	v.Set("fulfilment", "pickup")
	w := e.post("/a/order-2", v)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	if row := e.inbox.rows[ref]; row.action == nil || *row.action != copied || row.kind != "order" {
		t.Fatalf("sent to %+v", row)
	}
}

// Fronts F3 (§7.3): a front is its own page at /<slug>: the business's
// card, the front's header, its own list and its buttons; its order form
// at /<slug>/a/order takes its basket from that list and sends the order
// to its action. The home page lists the other fronts; an address the
// page does not have answers 404.
func TestFrontPages(t *testing.T) {
	order := uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada's <Kitchen>"}
	for _, a := range p.Model.Actions {
		a.Slug, a.Form = a.Kind, storefront.BuiltinForm(a.Kind)
		home.Actions = append(home.Actions, a)
	}
	snacks := storefront.PublicFront{Slug: "snacks", Kind: "food", Name: "Snacks", Tagline: "Fried fresh",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
		Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order snacks", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
	p.Model.Fronts = []storefront.PublicFront{home, snacks}
	p.Lists = map[string][]storefront.Item{"menu": {{ID: "1", Name: "Puff puff", PriceKobo: kobo(50000), Available: true}}}
	e := formServer(t, p)

	if page := get(e.h, "GET", sfHost, "/").Body.String(); !strings.Contains(page, `<a href="/snacks">Snacks</a>`) || !strings.Contains(page, "Fried fresh") {
		t.Fatalf("the home page's other fronts: %s", page)
	}
	w := get(e.h, "GET", sfHost, "/snacks")
	page := w.Body.String()
	if w.Code != 200 || !strings.Contains(page, `<h2 id="front-h">Snacks</h2>`) || !strings.Contains(page, "Puff puff") || strings.Contains(page, "Dish number") ||
		!strings.Contains(page, `href="/snacks/a/order"`) || !strings.Contains(page, `href="/snacks/a/order?item=1"`) || strings.Contains(page, `href="/a/enquiry"`) {
		t.Fatalf("the front's page: %d %s", w.Code, page)
	}
	body, tok := e.form(t, "/snacks/a/order")
	if !strings.Contains(body, `action="/snacks/a/order"`) || !strings.Contains(body, `name="q.1"`) || strings.Contains(body, `name="q.2"`) {
		t.Fatal("the front's order form")
	}
	*e.now = e.now.Add(20 * time.Second)
	v := url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "q.1": {"3"}}
	if w = e.post("/snacks/a/order", v); w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	row := e.inbox.rows[ref]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, row.payload)
	if err != nil || row.action == nil || *row.action != order || sub.TotalKobo != 150000 || sub.Basket[0].Name != "Puff puff" {
		t.Fatalf("sent: %+v %+v %v", row, sub, err)
	}
	for _, path := range []string{"/nothing", "/snacks/a/enquiry", "/nothing/a/order"} {
		if w := get(e.h, "GET", sfHost, path); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}

// No route the public host has itself is an address a front can take.
func TestFrontSlugsMissTheRoutes(t *testing.T) {
	for _, route := range []string{"a", "o", "f", "i", "r", "report", "embed", "pay", "static"} {
		if err := storefront.CheckFrontSlug(route); err == nil {
			t.Errorf("a front may take /%s", route)
		}
	}
	for _, route := range []string{"qr.png", "_static"} {
		if frontPath.MatchString("/" + route) {
			t.Errorf("/%s reads as a front", route)
		}
	}
}

// Fronts F3, Food: a dish with options asks a choice from each group
// under its quantity, the price of each choice shown; the order is priced
// as the dish plus the choices taken, from the copy; the page shows how
// long a dish takes.
func TestFoodOptionsOnTheForm(t *testing.T) {
	order := uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada's <Kitchen>"}
	snacks := storefront.PublicFront{Slug: "food", Kind: "food", Name: "Food",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
		Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order food", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
	p.Model.Fronts = []storefront.PublicFront{home, snacks}
	rice := storefront.Item{ID: "1", Name: "Jollof rice", PriceKobo: kobo(150000), Available: true, ReadyIn: 25,
		Options: storefront.ParseOptions(`[{"name": "Size", "choices": ["Regular", {"name": "Large", "price": 500}]}]`)}
	p.Lists = map[string][]storefront.Item{"menu": {rice}}
	e := formServer(t, p)
	if page := get(e.h, "GET", sfHost, "/food").Body.String(); !strings.Contains(page, "Ready in 25 min") {
		t.Fatal("the dish's time")
	}
	body, tok := e.form(t, "/food/a/order")
	if !strings.Contains(body, `<select class="opt" name="o.1.0" aria-label="Size">`) || !strings.Contains(body, `<option value="1">Large (&#43;₦500)</option>`) {
		t.Fatalf("the dish's choices: %s", body)
	}
	*e.now = e.now.Add(20 * time.Second)
	v := url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "q.1": {"2"}, "o.1.0": {"7"}}
	if w := e.post("/food/a/order", v); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Please choose from the options for Jollof rice") {
		t.Fatalf("a choice the dish has not: %d", w.Code)
	}
	v.Set("o.1.0", "1")
	w := e.post("/food/a/order", v)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil || len(sub.Basket) != 1 || sub.Basket[0].PriceKobo != 200000 || sub.TotalKobo != 400000 || sub.Basket[0].LineName() != "Jollof rice (Large)" {
		t.Fatalf("priced: %+v %v", sub.Basket, err)
	}
}

// A quote form asks what is wanted and by when (fronts F7); a blank form
// asks only its own questions, its message optional.
func TestQuoteAndBlankForms(t *testing.T) {
	quote, blank := uuid.New(), uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada"}
	repairs := storefront.PublicFront{Slug: "repairs", Kind: "custom", Name: "Repairs",
		Actions: []storefront.PublicAction{
			{ID: &quote, Slug: "quote", Kind: "quote", Label: "Request a quote", Payment: "after_accept", Form: storefront.BuiltinForm("quote")},
			{ID: &blank, Slug: "form", Kind: "enquiry", Label: "Send", Payment: "none",
				Form: []byte(`{"type":"object","required":["device"],"properties":{"device":{"type":"string","title":"What needs fixing?"}}}`)}}}
	p.Model.Fronts = []storefront.PublicFront{home, repairs}
	e := formServer(t, p)

	body, tok := e.form(t, "/repairs/a/quote")
	if !strings.Contains(body, "What do you need?") || !strings.Contains(body, `name="a.needed_by"`) || !strings.Contains(body, "send you a price") {
		t.Fatalf("the quote form: %s", body)
	}
	*e.now = e.now.Add(20 * time.Second)
	v := url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"}}
	if w := e.post("/repairs/a/quote", v); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no details: %d", w.Code)
	}
	_, tok = e.form(t, "/repairs/a/quote")
	*e.now = e.now.Add(20 * time.Second)
	v = url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"}, "a.details": {"A wardrobe, 2m wide"}, "a.needed_by": {"2026-12-20"}}
	w := e.post("/repairs/a/quote", v)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("sent: %d %s", w.Code, w.Body.String())
	}
	ref := refOf(w.Header().Get("Location"))
	row := e.inbox.rows[ref]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, row.payload)
	if err != nil || row.kind != "quote" || sub.Answers["details"] != "A wardrobe, 2m wide" || sub.Answers["needed_by"] != "2026-12-20" || sub.TotalKobo != 0 {
		t.Fatalf("the request: %+v %+v %v", row, sub, err)
	}

	// The blank form: its own question, no message needed.
	_, tok = e.form(t, "/repairs/a/form")
	*e.now = e.now.Add(20 * time.Second)
	w = e.post("/repairs/a/form", url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"}, "a.device": {"Fridge"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("a blank form: %d %s", w.Code, w.Body.String())
	}
}

// An item is opened by its name, on a page of its own (/<front>/i/<id>):
// with the order form on it offers Add to bag; with no form at all the page
// and the item say that orders are not open yet, and where to ask, instead
// of leaving the item doing nothing. In every theme.
func TestItemsOpenAndSayWhenOrdersAreNotOpen(t *testing.T) {
	order := uuid.New()
	for _, theme := range brand.Themes {
		p := orderPage()
		home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada's Kitchen"}
		snacks := storefront.PublicFront{Slug: "snacks", Kind: "food", Name: "Snacks",
			Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
			Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order snacks", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
		cold := storefront.PublicFront{Slug: "cold", Kind: "food", Name: "Cold drinks", Blocks: snacks.Blocks}
		p.Model.Fronts = []storefront.PublicFront{home, snacks, cold}
		p.Model.WhatsLink = "https://wa.me/2348031234567"
		p.Lists = map[string][]storefront.Item{"menu": {{ID: "1", Name: "Puff puff", PriceKobo: kobo(50000), Description: "Fried fresh", Available: true},
			{ID: "2", Name: "Zobo", PriceKobo: kobo(30000), Available: false}}}
		e := formServer(t, p)
		withBrand(e.s, brand.Brand{Theme: theme})
		e.h = e.s.Handler()

		// With the order form on: the name opens the item's page, which
		// offers Add to bag and goes to the order form.
		page := get(e.h, "GET", sfHost, "/snacks").Body.String()
		if !strings.Contains(page, `class="item-open" href="/snacks/i/1"`) {
			t.Errorf("%s: with the order form on, the item's page is not linked", theme)
		}
		// A card adds: by the order form (the older looks), or straight to
		// the bag (the shop looks: a form with the item's quantity).
		toBag := strings.Contains(page, `action="/snacks/a/order"`) && strings.Contains(page, `name="q.1"`) && strings.Contains(page, `name="step" value="bag"`)
		if !strings.Contains(page, `href="/snacks/a/order?item=1"`) && !toBag {
			t.Errorf("%s: with the order form on, a card does not add the item", theme)
		}
		if strings.Contains(page, `id="i-1"`) {
			t.Errorf("%s: the item's pop-up is still on the page", theme)
		}
		if strings.Contains(page, "Orders are not open on this page yet") {
			t.Errorf("%s: the page says orders are not open while they are", theme)
		}
		item := get(e.h, "GET", sfHost, "/snacks/i/1")
		if item.Code != http.StatusOK {
			t.Fatalf("%s: the item's page: %d", theme, item.Code)
		}
		add := lookOf(brand.Brand{Theme: theme}, "").Words().Add
		for _, want := range []string{"Puff puff", "Fried fresh", add, `action="/snacks/a/order"`, `name="step" value="bag"`, `name="q.1"`, "/_static/item-sheet."} {
			if !strings.Contains(item.Body.String(), want) {
				t.Errorf("%s: the item's page is missing %q", theme, want)
			}
		}
		if page := get(e.h, "GET", sfHost, "/snacks/i/2").Body.String(); !strings.Contains(page, "Unavailable for now.") || strings.Contains(page, add) {
			t.Errorf("%s: an unavailable item offers to be bagged", theme)
		}
		if w := get(e.h, "GET", sfHost, "/snacks/i/99"); w.Code != http.StatusNotFound {
			t.Errorf("%s: an item that is not there: %d", theme, w.Code)
		}
		if w := get(e.h, "GET", sfHost, "/nothing/i/1"); w.Code != http.StatusNotFound {
			t.Errorf("%s: an item on a front that is not there: %d", theme, w.Code)
		}

		// With no form: the page and each item say so, and where to ask.
		page = get(e.h, "GET", sfHost, "/cold").Body.String()
		for _, want := range []string{`href="/cold/i/1"`, "Orders are not open on this page yet.", `href="https://wa.me/2348031234567"`, "on WhatsApp"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: with no form, missing %q", theme, want)
			}
		}
		if strings.Contains(page, "Add to order") || strings.Contains(page, `class="add"`) {
			t.Errorf("%s: an order button on a front that takes no orders", theme)
		}
		if page := get(e.h, "GET", sfHost, "/cold/i/1").Body.String(); !strings.Contains(page, "Orders are not open on this page yet.") || !strings.Contains(page, "on WhatsApp") || strings.Contains(page, add) {
			t.Errorf("%s: an item on a front that takes no orders: %s", theme, page)
		}
		if !strings.Contains(page, "/_static/item-sheet.") {
			t.Errorf("%s: the item view's stylesheet is not linked", theme)
		}
	}
}

// An order is two pages, the bag and then the details, with nothing kept
// between them but the address and the form's own fields (no cookie): the
// bag step lists what is in it and takes it on, the details step shows it
// as a summary above the details and posts it, and a refused submission
// stays on the details step. Without a step in the address the order is
// the one page it always was. In every theme.
func TestOrderBagAndDetailsSteps(t *testing.T) {
	order := uuid.New()
	for _, theme := range brand.Themes {
		p := orderPage()
		home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada"}
		food := storefront.PublicFront{Slug: "food", Kind: "food", Name: "Food",
			Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
			Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order food", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
		p.Model.Fronts = []storefront.PublicFront{home, food}
		rice := storefront.Item{ID: "1", Name: "Jollof rice", PriceKobo: kobo(150000), Available: true,
			Options: storefront.ParseOptions(`[{"name": "Size", "choices": ["Regular", {"name": "Large", "price": 500}]}]`)}
		p.Lists = map[string][]storefront.Item{"menu": {rice}}
		e := formServer(t, p)
		withBrand(e.s, brand.Brand{Theme: theme})
		e.h = e.s.Handler()

		// The bag: what is in it, with the choices and the sum.
		bagPage := get(e.h, "GET", sfHost, "/food/a/order?step=bag&q.1=2&o.1.0=1")
		if bagPage.Code != http.StatusOK {
			t.Fatalf("%s: the bag step: %d", theme, bagPage.Code)
		}
		bag := bagPage.Body.String()
		words := lookOf(brand.Brand{Theme: theme}, "").Words()
		for _, want := range []string{words.Your, `name="step" value="details"`, `name="q.1" value="2"`, "Large", "₦2,000 each", "Total: ₦4,000", `method="get"`} {
			if !strings.Contains(bag, want) {
				t.Errorf("%s: the bag step is missing %q", theme, want)
			}
		}
		if strings.Contains(bag, `name="token"`) || strings.Contains(bag, "Send order") {
			t.Errorf("%s: the bag step is the details form", theme)
		}

		// The details: the bag as a summary, its fields kept, the form to post.
		details, tok := e.form(t, "/food/a/order?step=details&q.1=2&o.1.0=1")
		for _, want := range []string{words.Your, "2 × Jollof rice", "Total: ₦4,000", `name="q.1" value="2"`, `name="o.1.0" value="1"`, `step=bag`, "Send order", `method="post"`} {
			if !strings.Contains(details, want) {
				t.Errorf("%s: the details step is missing %q", theme, want)
			}
		}
		if strings.Contains(details, `<select class="opt"`) {
			t.Errorf("%s: the details step asks the choices again", theme)
		}

		// No step: the one page it always was.
		if body, _ := e.form(t, "/food/a/order"); !strings.Contains(body, `<select class="opt" name="o.1.0"`) || strings.Contains(body, "Total: ₦") {
			t.Errorf("%s: no step is not the one-page order", theme)
		}
		// An emptied bag says so, and is forgotten in the browser.
		for _, path := range []string{"/food/a/order?step=bag", "/food/a/order?step=details&q.1=0"} {
			if body := get(e.h, "GET", sfHost, path).Body.String(); !strings.Contains(body, "Your "+words.Bag+" is empty") || !strings.Contains(body, "data-bag-clear") || strings.Contains(body, `name="token"`) {
				t.Errorf("%s: %s is not the empty bag", theme, path)
			}
		}

		// A refused submission stays on the details step, its bag kept.
		*e.now = e.now.Add(20 * time.Second)
		v := url.Values{"token": {tok}, "website": {""}, "name": {""}, "phone": {"0803 123 4567"}, "q.1": {"2"}, "o.1.0": {"1"}}
		w := e.post("/food/a/order", v)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "2 × Jollof rice") || !strings.Contains(w.Body.String(), "Please enter your name") {
			t.Fatalf("%s: a refused order: %d %s", theme, w.Code, w.Body.String())
		}

		// And an order from the details step is priced from the catalogue.
		_, tok = e.form(t, "/food/a/order?step=details&q.1=2&o.1.0=1")
		*e.now = e.now.Add(20 * time.Second)
		v = url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "q.1": {"2"}, "o.1.0": {"1"}}
		w = e.post("/food/a/order", v)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", theme, w.Code, w.Body.String())
		}
	}
}

// One item twice in a bag, with different choices: two lines (q.1 and
// q.1~2), priced each with its own; together they are held to what is
// left. A choice the dish no longer has takes its line out, saying so,
// rather than putting the first choice in its place.
func TestBagLinesOfOneItem(t *testing.T) {
	order := uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Ada"}
	food := storefront.PublicFront{Slug: "food", Kind: "food", Name: "Food",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
		Actions: []storefront.PublicAction{{ID: &order, Slug: "order", Kind: "order", Label: "Order food", Payment: "after_accept", Pickup: true, Form: storefront.BuiltinForm("order")}}}
	p.Model.Fronts = []storefront.PublicFront{home, food}
	left := int64(3)
	rice := storefront.Item{ID: "1", Name: "Jollof rice", PriceKobo: kobo(150000), Available: true, Stock: &left,
		Options: storefront.ParseOptions(`[{"name": "Size", "choices": ["Regular", {"name": "Large", "price": 500}]}]`)}
	p.Lists = map[string][]storefront.Item{"menu": {rice}}
	e := formServer(t, p)
	withBrand(e.s, brand.Brand{Theme: "kitchen"})
	e.h = e.s.Handler()

	bag := get(e.h, "GET", sfHost, "/food/a/order?step=bag&q.1=1&o.1.0=0&q.1~2=2&o.1~2.0=1").Body.String()
	for _, want := range []string{`name="q.1" value="1"`, `name="q.1~2" value="2"`, `name="o.1~2.0" value="1"`, "Regular", "Large", "Total: ₦5,500", "Remove<span class=\"sr\"> Jollof rice</span>"} {
		if !strings.Contains(bag, want) {
			t.Errorf("two lines of one dish: missing %q", want)
		}
	}
	for _, want := range []string{`href="/food/a/order?o.1~2.0=1&amp;q.1~2=2&amp;step=bag"`, `href="/food/a/order?o.1.0=0&amp;q.1=1&amp;step=bag"`} {
		if !strings.Contains(bag, want) {
			t.Errorf("removing one line keeps the other: missing %s", want)
		}
	}

	stale := get(e.h, "GET", sfHost, "/food/a/order?step=bag&q.1=1&o.1.0=7").Body.String()
	if !strings.Contains(stale, "The choices for Jollof rice have changed") || !strings.Contains(stale, "Your order is empty") {
		t.Errorf("a choice no longer offered: %s", stale)
	}

	_, tok := e.form(t, "/food/a/order?step=details&q.1=1&o.1.0=0&q.1~2=2&o.1~2.0=1")
	*e.now = e.now.Add(20 * time.Second)
	v := url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "q.1": {"2"}, "o.1.0": {"0"}, "q.1~2": {"2"}, "o.1~2.0": {"1"}}
	if w := e.post("/food/a/order", v); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Only 3 of Jollof rice left") {
		t.Fatalf("four of three left, over two lines: %d", w.Code)
	}
	_, tok = e.form(t, "/food/a/order?step=details&q.1=1&o.1.0=0&q.1~2=2&o.1~2.0=1")
	*e.now = e.now.Add(20 * time.Second)
	v = url.Values{"token": {tok}, "website": {""}, "name": {"Ada Obi"}, "phone": {"0803 123 4567"}, "q.1": {"1"}, "o.1.0": {"0"}, "q.1~2": {"2"}, "o.1~2.0": {"1"}}
	w := e.post("/food/a/order", v)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ref := regexp.MustCompile(`^/o/(AD-[A-Z2-9]{6})`).FindStringSubmatch(w.Header().Get("Location"))[1]
	sub, err := storefront.OpenSubmission(e.s.InboxKey, e.sf, ref, e.inbox.rows[ref].payload)
	if err != nil || len(sub.Basket) != 2 || sub.Basket[0].LineName() != "Jollof rice (Regular)" || sub.Basket[1].LineName() != "Jollof rice (Large)" || sub.TotalKobo != 550000 {
		t.Fatalf("priced: %+v %v", sub.Basket, err)
	}
}

// The shop looks' pages as their designs have them: a service's page says
// what kind and how long above its name and opens the booking form with
// it chosen, saying the deposit; an item's photos are all thumbnails,
// the first among them; the bag pill knows the order; and the details
// step's button says what is paid now.
func TestShopPagesAsDesigned(t *testing.T) {
	order, book := uuid.New(), uuid.New()
	p := orderPage()
	home := storefront.PublicFront{Slug: "", Kind: "shop", Name: "Bloom"}
	studio := storefront.PublicFront{Slug: "studio", Kind: "food", Name: "Studio",
		Blocks: []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table": "00000000-0000-0000-0000-000000000001", "table_name": "menu", "columns": {"name": "name"}}`)}},
		Actions: []storefront.PublicAction{
			{ID: &order, Slug: "order", Kind: "order", Label: "Order", Payment: "at_order", Pickup: true, DepositPercent: 50, Form: storefront.BuiltinForm("order")},
			{ID: &book, Slug: "booking", Kind: "booking", Label: "Book", Payment: "at_order", DepositPercent: 20, Form: storefront.BuiltinForm("booking")}}}
	p.Model.Fronts = []storefront.PublicFront{home, studio}
	press := storefront.Item{ID: "1", Name: "Silk press", Category: "Hair", PriceKobo: kobo(2500000), Available: true, DurationMinutes: 120}
	oil := storefront.Item{ID: "2", Name: "Hair oil", PriceKobo: kobo(800000), Available: true,
		Photo: storefront.Image{"320": "/s0.jpg", "800": "/l0.jpg"}, Gallery: []storefront.Image{{"320": "/s1.jpg", "800": "/l1.jpg"}}}
	p.Lists = map[string][]storefront.Item{"menu": {press, oil}}
	e := formServer(t, p)
	withBrand(e.s, brand.Brand{Theme: "salon"})
	e.h = e.s.Handler()

	svc := get(e.h, "GET", sfHost, "/studio/i/1").Body.String()
	for _, want := range []string{`<p class="pdp-kicker">Hair · 2 hours</p>`, `href="/studio/a/booking?service=1">Choose a time</a>`, "Pay a ₦5,000 deposit to hold your time."} {
		if !strings.Contains(svc, want) {
			t.Errorf("the service's page is missing %q", want)
		}
	}
	if strings.Contains(svc, "Add to booking") {
		t.Error("a service is booked, not added to a bag")
	}
	pics := get(e.h, "GET", sfHost, "/studio/i/2").Body.String()
	for _, want := range []string{`data-pic="/l0.jpg" aria-current="true"`, `alt="Photo 1 of 2"`, `data-pic="/l1.jpg"`, `alt="Photo 2 of 2"`, "Add to booking", `<span data-run-total> · ₦8,000</span>`, "Pay a 50% deposit now.",
		`class="sf-pill" href="/studio/a/order?step=bag" data-bag-link`, "/_static/item."} {
		if !strings.Contains(pics, want) {
			t.Errorf("the item's page is missing %q", want)
		}
	}

	details, _ := e.form(t, "/studio/a/order?step=details&q.2=2")
	for _, want := range []string{"<h1>Checkout</h1>", `class="form co"`, "Your booking", `Pay <span data-pay>₦8,000</span> deposit`, "Balance at the studio"} {
		if !strings.Contains(details, want) {
			t.Errorf("the details step is missing %q", want)
		}
	}
	if strings.Contains(details, ">Send order</button>") {
		t.Error("the details step has two buttons")
	}
	if bag := get(e.h, "GET", sfHost, "/studio/a/order?step=bag&q.2=1").Body.String(); !strings.Contains(bag, "<h1>Your booking</h1>") || !strings.Contains(bag, ">Add another service</a>") {
		t.Errorf("the bag step: %s", bag)
	}
}
