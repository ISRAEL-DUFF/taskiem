package publicweb

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeOrderRate answers an order's delivery as the test sets it.
type fakeOrderRate struct {
	d     OrderDelivery
	ok    bool
	rated []int
}

func (f *fakeOrderRate) Delivery(context.Context, string, []byte) (OrderDelivery, bool, error) {
	return f.d, f.ok, nil
}
func (f *fakeOrderRate) Rate(_ context.Context, _ string, _ []byte, n int) (bool, error) {
	f.rated = append(f.rated, n)
	f.d.Rating, f.d.CanRate = n, false
	return true, nil
}

// A delivered order's page asks how the delivery was, 1 to 5, never
// naming the rider; one tap keeps it and the page thanks them. An order
// still on its way, or picked up, asks nothing.
func TestStatusPageRatesDelivery(t *testing.T) {
	e := formServer(t, orderPage())
	rate := &fakeOrderRate{}
	e.s.OrderRate = rate
	e.h = e.s.Handler()
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	loc := e.post("/a/order", goodOrder(tok)).Header().Get("Location")
	ref := strings.TrimPrefix(strings.Split(loc, "?")[0], "/o/")
	key := strings.Split(loc, "k=")[1]
	page := func() string {
		t.Helper()
		w := get(e.h, "GET", sfHost, loc)
		if w.Code != 200 {
			t.Fatalf("status page: %d", w.Code)
		}
		return w.Body.String()
	}

	e.inbox.states[ref] = OrderState{Status: "on_the_way", Kind: "order", TotalKobo: 450200}
	rate.d, rate.ok = OrderDelivery{Status: "picked_up"}, true
	if body := page(); strings.Contains(body, "How was the delivery?") {
		t.Fatalf("asked before it was delivered:\n%s", body)
	}
	e.inbox.states[ref] = OrderState{Status: "completed", Kind: "order", TotalKobo: 450200}
	rate.d = OrderDelivery{Status: "delivered", CanRate: true}
	body := page()
	for _, s := range []string{"How was the delivery?", `name="rating" value="5"`, `aria-label="1 of 5"`, "/rate?k="} {
		if !strings.Contains(body, s) {
			t.Errorf("no %q in\n%s", s, body)
		}
	}
	// Out of range, or GET: nothing kept.
	rateURL := "/o/" + ref + "/rate?k=" + url.QueryEscape(key)
	if w := post(e.h, sfHost, rateURL, "rating=9"); w.Code != 422 || len(rate.rated) != 0 {
		t.Fatalf("rating 9: %d %v", w.Code, rate.rated)
	}
	if w := get(e.h, "GET", sfHost, rateURL); w.Code != 405 {
		t.Errorf("GET /rate: %d", w.Code)
	}
	// Another key: its first character changed, never to itself.
	wrong := "x" + key[1:]
	if key[0] == 'x' {
		wrong = "y" + key[1:]
	}
	if w := post(e.h, sfHost, "/o/"+ref+"/rate?k="+url.QueryEscape(wrong), "rating=4"); w.Code != 404 || len(rate.rated) != 0 {
		t.Errorf("a wrong key: %d %v", w.Code, rate.rated)
	}
	w := post(e.h, sfHost, rateURL, "rating=4")
	if w.Code != 303 || w.Header().Get("Location") != loc || len(rate.rated) != 1 || rate.rated[0] != 4 {
		t.Fatalf("rate: %d %q %v", w.Code, w.Header().Get("Location"), rate.rated)
	}
	if body := page(); !strings.Contains(body, "You rated the delivery 4 of 5") || strings.Contains(body, `name="rating"`) {
		t.Fatalf("after rating:\n%s", body)
	}
	// Past its 14 days and never rated: nothing asked.
	rate.d = OrderDelivery{Status: "delivered"}
	if body := page(); strings.Contains(body, "How was the delivery?") || strings.Contains(body, "You rated") {
		t.Fatalf("too late:\n%s", body)
	}
}
