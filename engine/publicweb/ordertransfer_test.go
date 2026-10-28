package publicweb

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeOrderPay answers an order's transfer as the test sets it.
type fakeOrderPay struct {
	p        OrderPayment
	requests int
}

func (f *fakeOrderPay) Payment(context.Context, string, []byte) (OrderPayment, bool, error) {
	return f.p, true, nil
}
func (f *fakeOrderPay) Request(context.Context, string, []byte) (bool, error) {
	f.requests++
	return true, nil
}

// An order waiting for its payment offers Pay by bank transfer: beside the
// card page on a Paystack form, alone on another rail's; the account
// number while it is open; and asking for one goes back to the page.
func TestStatusPagePayByTransfer(t *testing.T) {
	e := formServer(t, orderPage())
	pay := &fakeOrderPay{}
	e.s.OrderPay = pay
	e.h = e.s.Handler()
	_, tok := e.form(t, "/a/order")
	*e.now = e.now.Add(10 * time.Second)
	loc := e.post("/a/order", goodOrder(tok)).Header().Get("Location")
	ref := strings.TrimPrefix(strings.Split(loc, "?")[0], "/o/")
	held := e.now.Add(45 * time.Minute)
	open := e.now.Add(40 * time.Minute)
	for _, c := range []struct {
		name    string
		state   OrderState
		pay     OrderPayment
		want    []string
		not     []string
		refresh string
	}{
		{"beside the card page", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200, PaymentURL: "https://checkout.paystack.test/abc"},
			OrderPayment{Card: true, HeldUntil: &held}, []string{">Pay ₦4,502</a>", "Pay by bank transfer instead", "/pay?k=", "held until"}, nil, ""},
		{"the card page is being made", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, OrderPayment{Card: true},
			[]string{"preparing your payment"}, []string{"Pay by bank transfer"}, "3"},
		{"another rail: transfer only", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, OrderPayment{HeldUntil: &held},
			[]string{"Pay for your order", "Pay ₦4,502 by bank transfer", "btn-primary btn-brand", "Pay by bank transfer</button>"}, []string{"preparing", "Total:"}, ""},
		{"getting the number", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, OrderPayment{Waiting: true},
			[]string{"Getting your account number"}, []string{"<button class=\"btn btn-block btn-primary btn-brand\""}, "2"},
		{"the number", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200},
			OrderPayment{Status: "open", AmountKobo: 450200, AccountNumber: "9900112233", BankName: "Wema Bank", AccountName: "Shop / Ada", ExpiresAt: &open,
				OnWrongAmount: "returned", HeldUntil: &held},
			[]string{"9900112233", "Wema Bank", "Transfer ₦4,502 to", "40 minutes left", "a different amount is sent back", "Get a new account number"}, nil, "30"},
		{"expired", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, OrderPayment{Status: "open", AccountNumber: "9900112233", ExpiresAt: &held},
			nil, nil, "30"},
		{"limits", OrderState{Status: "awaiting_payment", Kind: "order", TotalKobo: 450200}, OrderPayment{Status: "failed", LastHour: 5},
			[]string{"Too many account numbers"}, []string{"Pay by bank transfer</button>"}, ""},
	} {
		e.inbox.states[ref] = c.state
		pay.p = c.pay
		if c.name == "expired" {
			past := e.now.Add(-time.Minute)
			pay.p.ExpiresAt = &past
			c.want, c.refresh = []string{"The last account number has expired", "Get a new account number"}, ""
		}
		w := get(e.h, "GET", sfHost, loc)
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d", c.name, w.Code)
		}
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("%s: no %q in\n%s", c.name, s, body)
			}
		}
		for _, s := range c.not {
			if strings.Contains(body, s) {
				t.Errorf("%s: %q in\n%s", c.name, s, body)
			}
		}
		refresh := ""
		if i := strings.Index(body, `http-equiv="refresh" content="`); i >= 0 {
			refresh = strings.SplitN(body[i+len(`http-equiv="refresh" content="`):], `"`, 2)[0]
		}
		if refresh != c.refresh {
			t.Errorf("%s: refresh %q, want %q", c.name, refresh, c.refresh)
		}
	}
	// Asking: queued, and back to the page. A wrong key asks nothing.
	key := strings.Split(loc, "k=")[1]
	w := post(e.h, sfHost, "/o/"+ref+"/pay?k="+url.QueryEscape(key), "")
	if w.Code != 303 || w.Header().Get("Location") != loc || pay.requests != 1 {
		t.Fatalf("ask: %d %q %d", w.Code, w.Header().Get("Location"), pay.requests)
	}
	// Another key: its first character changed, never to itself.
	wrong := "x" + key[1:]
	if key[0] == 'x' {
		wrong = "y" + key[1:]
	}
	if w := post(e.h, sfHost, "/o/"+ref+"/pay?k="+wrong, ""); w.Code != 404 || pay.requests != 1 {
		t.Errorf("a wrong key: %d %d", w.Code, pay.requests)
	}
	if w := get(e.h, "GET", sfHost, "/o/"+ref+"/pay?k="+key); w.Code != 405 {
		t.Errorf("GET /pay: %d", w.Code)
	}
}
