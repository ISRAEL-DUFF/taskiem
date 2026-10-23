package publicweb

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/brand"
)

// fakePay is a page's ways to pay, and the requests it was sent.
type fakePay struct {
	opts []PayOption
	reqs []string
	ok   bool
}

func (f *fakePay) Options(context.Context, []byte) ([]PayOption, error) { return f.opts, nil }

func (f *fakePay) Request(_ context.Context, _ []byte, kind string, amount int64) (bool, error) {
	f.reqs = append(f.reqs, kind+":"+strconv.FormatInt(amount, 10))
	return f.ok, nil
}

func TestInvoicePayOptions(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	until := now.Add(25 * time.Minute)
	p := InvoicePage{Business: "Ada Kitchen", Number: "INV-0007", Status: "sent", TotalKobo: 750000}
	cases := []struct {
		name    string
		opts    []PayOption
		wants   []string
		nots    []string
		refresh string
	}{
		{"none yet", []PayOption{{Kind: "transfer"}, {Kind: "link"}},
			[]string{"Pay this invoice", `value="transfer">Pay by bank transfer`, `value="link">Pay by card`, `action="/i/` + invoiceToken + `/pay"`},
			[]string{"inv-amount", "http-equiv"}, ""},
		{"waiting", []PayOption{{Kind: "transfer", Waiting: true}, {Kind: "link"}},
			[]string{"Getting your account number…"}, nil, `content="2"`},
		{"open", []PayOption{{Kind: "transfer", Status: "open", AmountKobo: 750000, AccountNumber: "9900112233", BankName: "Wema Bank", AccountName: "Paystack-Ada",
			ExpiresAt: &until, OnLate: "returned", OnWrongAmount: "returned", LastHour: 1, Issued: 1}, {Kind: "link"}},
			[]string{"Transfer ₦7,500 to", "9900112233", "Wema Bank · Paystack-Ada", "Valid until 13:25 (Lagos time)", "25 minutes left",
				"a different amount is sent back", "made after this time is sent back", "Get a new account number"}, nil, `content="30"`},
		{"expired", []PayOption{{Kind: "transfer", Status: "open", AccountNumber: "9900112233", ExpiresAt: &now, LastHour: 1, Issued: 1}},
			[]string{"has expired", "Get a new account number"}, []string{"9900112233"}, ""},
		{"limits", []PayOption{{Kind: "transfer", Status: "failed", LastHour: 5, Issued: 5}},
			[]string{"Too many account numbers", "contact Ada Kitchen"}, []string{`value="transfer"`}, ""},
		{"card link", []PayOption{{Kind: "transfer"}, {Kind: "link", Status: "open", PaymentURL: "https://checkout.example/abc"}},
			[]string{`href="https://checkout.example/abc"`, "Continue to card payment"}, nil, ""},
		{"part payments", []PayOption{{Kind: "transfer", PartPayments: true}},
			[]string{`id="inv-amount"`, "Leave empty to pay all of ₦7,500, or pay at least ₦1,000."}, nil, ""},
	}
	for _, theme := range brand.Themes {
		for _, c := range cases {
			s := invoiceServer(fakeInvoice(p), nil)
			withBrand(s, brand.Brand{Theme: theme})
			s.InvoicePay = &fakePay{opts: c.opts}
			s.now = func() time.Time { return now }
			res := get(s.Handler(), "GET", formHost, "/i/"+invoiceToken)
			body := res.Body.String()
			if res.Code != 200 {
				t.Fatalf("%s %s: %d %s", theme, c.name, res.Code, body)
			}
			for _, want := range c.wants {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s: no %q in %s", theme, c.name, want, body)
				}
			}
			for _, not := range c.nots {
				if strings.Contains(body, not) {
					t.Errorf("%s %s: %q in %s", theme, c.name, not, body)
				}
			}
			if got := strings.Contains(body, `http-equiv="refresh"`); got != (c.refresh != "") || c.refresh != "" && !strings.Contains(body, c.refresh) {
				t.Errorf("%s %s: refresh %v, want %q", theme, c.name, got, c.refresh)
			}
		}
	}
}

func TestInvoicePayRequest(t *testing.T) {
	pay := &fakePay{ok: true}
	s := invoiceServer(fakeInvoice(InvoicePage{Business: "Ada Kitchen", Number: "INV-0007", Status: "sent", TotalKobo: 750000}), nil)
	s.InvoicePay = pay
	h := s.Handler()
	res := post(h, formHost, "/i/"+invoiceToken+"/pay", "kind=transfer")
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/i/"+invoiceToken {
		t.Fatalf("transfer: %d %v", res.Code, res.Header())
	}
	if res := post(h, formHost, "/i/"+invoiceToken+"/pay", "kind=link&amount=2%2C500.5"); res.Code != http.StatusSeeOther {
		t.Fatalf("link: %d", res.Code)
	}
	if strings.Join(pay.reqs, " ") != "transfer:0 link:250050" {
		t.Errorf("requests %v", pay.reqs)
	}
	pay.ok = false
	for form, code := range map[string]int{"kind=transfer&amount=5": http.StatusUnprocessableEntity, "kind=transfer&amount=abc": http.StatusUnprocessableEntity,
		"kind=card": http.StatusBadRequest, "kind=transfer": http.StatusSeeOther} {
		if res := post(h, formHost, "/i/"+invoiceToken+"/pay", form); res.Code != code {
			t.Errorf("%s: %d, want %d", form, res.Code, code)
		}
	}
	if res := get(h, "GET", formHost, "/i/"+invoiceToken+"/pay"); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET pay: %d", res.Code)
	}
	// Without payments, there is no pay route.
	s.InvoicePay = nil
	if res := post(s.Handler(), formHost, "/i/"+invoiceToken+"/pay", "kind=transfer"); res.Code != http.StatusNotFound {
		t.Errorf("no payments: %d", res.Code)
	}
}

func TestParseNaira(t *testing.T) {
	for in, want := range map[string]int64{"5000": 500000, "5,000": 500000, "5,000.5": 500050, "₦1,000.05": 100005, "0": 0, "1.234": 0, "-5": 0, "": 0, "x": 0} {
		got, ok := parseNaira(in)
		if got != want || ok != (want > 0) {
			t.Errorf("%q: %d %v, want %d", in, got, ok, want)
		}
	}
}
