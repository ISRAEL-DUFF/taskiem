package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

const familyToken = "FAMILYfamilyFAMILYfamily01234567"

// fakeFamilies is a family with two children, as the record would give it.
type fakeFamilies struct {
	link     FamilyLink
	students []FamilyStudent
	invoices []FamilyInvoice
	receipts []FamilyReceipt
	requests []FamilyRequest
	asked    []string // kind:student
	payloads [][]byte
	answer   string
}

func (f *fakeFamilies) Link(_ context.Context, h []byte) (FamilyLink, bool, error) {
	if !bytes.Equal(h, schools.FamilyTokenHash(familyToken)) {
		return FamilyLink{}, false, nil
	}
	return f.link, true, nil
}
func (f *fakeFamilies) Students(context.Context, []byte) ([]FamilyStudent, error) {
	return f.students, nil
}
func (f *fakeFamilies) Invoices(context.Context, []byte) ([]FamilyInvoice, error) {
	return f.invoices, nil
}
func (f *fakeFamilies) Receipts(context.Context, []byte) ([]FamilyReceipt, error) {
	return f.receipts, nil
}
func (f *fakeFamilies) Requests(context.Context, []byte) ([]FamilyRequest, error) {
	return f.requests, nil
}
func (f *fakeFamilies) Ask(_ context.Context, h []byte, kind string, student *uuid.UUID, _, payload []byte) (string, error) {
	if !bytes.Equal(h, schools.FamilyTokenHash(familyToken)) {
		return "", nil
	}
	s := ""
	if student != nil {
		s = student.String()
	}
	f.asked = append(f.asked, kind+":"+s)
	f.payloads = append(f.payloads, payload)
	if f.answer == "" {
		return "ok", nil
	}
	return f.answer, nil
}

var inboxKey = bytes.Repeat([]byte{7}, 32)

func familyWorld(t *testing.T, expired bool) (*fakeFamilies, *Server) {
	t.Helper()
	ebube, chidi := uuid.New(), uuid.New()
	link := uuid.New()
	inv1, inv2, rc1 := uuid.New(), uuid.New(), uuid.New()
	view := schools.FamilyView{Names: map[string]string{ebube.String(): "Ebube <Eze>", chidi.String(): "Chidi Eze"},
		AccountNames: map[string]string{ebube.String(): "GREENFIELD / EBUBE EZE"},
		InvoiceLinks: map[string]string{inv2.String(): "https://" + formHost + "/i/" + invoiceToken},
		ReceiptLinks: map[string]string{rc1.String(): "https://" + formHost + "/r/" + strings.Repeat("r", 32)}}
	sealed, err := schools.SealFamilyView(familyToken, link, view)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour)
	if expired {
		expires = time.Now().Add(-time.Hour)
	}
	due := time.Date(2099, 9, 7, 0, 0, 0, 0, time.UTC)
	f := &fakeFamilies{link: FamilyLink{Tenant: uuid.New(), Family: uuid.New(), Link: link, Business: "Greenfield Court Academy", ExpiresAt: expires, View: sealed, SiblingsShare: true, LinkDays: 3},
		students: []FamilyStudent{{ID: ebube, AdmissionNumber: "GCA/001", Class: "JSS 1 A", Status: "active", CreditKobo: 150000, AccountNumber: "9199711905", BankName: "Wema Bank"},
			{ID: chidi, AdmissionNumber: "GCA/002", Class: "JSS 2 A", Status: "active"}},
		invoices: []FamilyInvoice{
			{ID: inv1, StudentID: ebube, Term: "First term 2026/2027", Current: true, Number: "INV-0007", Status: "part_paid", TotalKobo: 11_800_000, PaidKobo: 2_500_050, DueOn: &due,
				Lines: []invoicing.Line{{Description: "Tuition", Quantity: 1, UnitKobo: 10_000_000}, {Description: "WAEC fee", Quantity: 1, UnitKobo: 1_800_000, PassThroughFor: "WAEC"}}},
			{ID: inv2, StudentID: chidi, Term: "First term 2026/2027", Current: true, Number: "INV-0008", Status: "sent", TotalKobo: 10_000_000, DueOn: &due,
				Lines: []invoicing.Line{{Description: "Tuition", Quantity: 1, UnitKobo: 10_000_000}}},
			{ID: uuid.New(), StudentID: chidi, Term: "Third term 2025/2026", Current: false, Number: "INV-0003", Status: "overdue", TotalKobo: 5_000_000, PaidKobo: 1_000_000,
				Lines: []invoicing.Line{{Description: "Tuition", Quantity: 1, UnitKobo: 5_000_000}}}},
		receipts: []FamilyReceipt{{ID: rc1, StudentID: ebube, InvoiceID: inv1, Number: "RCT-2026T1-0001", AmountKobo: 2_500_050, PaidAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}},
		requests: []FamilyRequest{{Kind: "claim", ID: uuid.New(), StudentID: ebube, AmountKobo: 500000, How: "cash", Status: "open", CreatedAt: time.Now()},
			{Kind: "plan", ID: uuid.New(), StudentID: chidi, Status: "approved", Instalments: []schools.Instalment{{AmountKobo: 5_000_000, DueOn: "2099-10-01"}},
				DecidedInstalments: []schools.Instalment{{AmountKobo: 4_000_000, DueOn: "2099-10-01"}, {AmountKobo: 6_000_000, DueOn: "2099-11-01"}}, DecisionNote: "Pay the first part before resumption", CreatedAt: time.Now()}}}
	s := &Server{Lookup: (&fakeHosts{}).lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour), FormHost: formHost, Families: f, InboxKey: inboxKey}
	testTheme(s)
	return f, s
}

func postForm(h http.Handler, host, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://"+host+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:4000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestFamilyPage draws the page in every theme: each child switchable
// with the choice in the address, the chosen child's fees line by line
// with the pass-through line labelled, what was paid, the receipts with
// their links, the balance and credit, the account number, the claims and
// plans, and the two forms. The other child's names and numbers stay on
// their own page.
func TestFamilyPage(t *testing.T) {
	f, s := familyWorld(t, false)
	ebube, chidi := f.students[0].ID.String(), f.students[1].ID.String()
	for _, theme := range brand.Themes {
		withBrand(s, brand.Brand{Theme: theme})
		s.brands = nil // the look is cached per business
		res := get(s.Handler(), "GET", formHost, "/s/"+familyToken)
		body := res.Body.String()
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", theme, res.Code, body)
		}
		for _, want := range []string{"theme-" + theme + ".", "Greenfield Court Academy", "Ebube &lt;Eze&gt;", "Chidi Eze", `href="/s/` + familyToken + `?child=` + chidi + `"`,
			"GCA/001", "JSS 1 A", "9199711905", "Wema Bank", "GREENFIELD / EBUBE EZE", "₦1,500", "brothers&#39; and sisters&#39;", "₦92,999.50", "First term 2026/2027", "Tuition", "Collected on behalf of WAEC.",
			"₦25,000.50", "RCT-2026T1-0001", `/r/` + strings.Repeat("r", 32), ">Valid<", "₦5,000 by cash", "The school is looking for it", "I have paid some other way", "Ask for a payment plan",
			`action="/s/` + familyToken + `/paid"`, `action="/s/` + familyToken + `/plan"`, `name="child" value="` + ebube + `"`, "copy.", "data-copy"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", theme, want)
			}
		}
		// The first child has an account: no invoice page link for them.
		if strings.Contains(body, "/i/"+invoiceToken) || strings.Contains(body, "INV-0008") || strings.Contains(body, "earlier term") {
			t.Errorf("%s: the other child's fees are on the first child's page", theme)
		}
		if h := res.Header(); h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s: headers %v", theme, h)
		}
	}
	// The second child: no account, so each open invoice links its own
	// page; a past term's balance still open; the decided plan.
	body := get(s.Handler(), "GET", formHost, "/s/"+familyToken+"?child="+chidi).Body.String()
	for _, want := range []string{"INV-0008", "/i/" + invoiceToken, "Pay this invoice by transfer or card", "Third term 2025/2026 (earlier term)", "earlier term still open", "₦140,000",
		"has no account number yet", "Approved", "₦40,000 by 1 Oct 2099", "₦60,000 by 1 Nov 2099", "Pay the first part before resumption"} {
		if !strings.Contains(body, want) {
			t.Errorf("child 2: no %q", want)
		}
	}
	if strings.Contains(body, "9199711905") {
		t.Error("child 2 shows child 1's account number")
	}
	// A notice after a form, and an error back on its form.
	if body := get(s.Handler(), "GET", formHost, "/s/"+familyToken+"?done=paid").Body.String(); !strings.Contains(body, "The school will look for your payment") {
		t.Error("no notice after a claim")
	}
	if body := get(s.Handler(), "GET", formHost, "/s/"+familyToken+"?err=amount&for=paid").Body.String(); !strings.Contains(body, "Type the amount in naira") {
		t.Error("no error on the paid form")
	}
	// A token nobody minted: "ask the school for a new link", no number,
	// no button; a shorter one is the status link's not-found.
	res := get(s.Handler(), "GET", formHost, "/s/"+strings.Repeat("A", 32))
	if res.Code != http.StatusGone || !strings.Contains(res.Body.String(), "ask the school for a new link") || strings.Contains(res.Body.String(), "Send me a new link") {
		t.Errorf("unknown token: %d %s", res.Code, res.Body.String())
	}
	if res := get(s.Handler(), "GET", formHost, "/s/short"); res.Code == 200 || res.Code == http.StatusGone {
		t.Errorf("a status reference: %d", res.Code)
	}
	// A wrong token with a right hash cannot happen; a view that does
	// not open (another link's token) is treated as unknown.
	f.link.Link = uuid.New()
	if res := get(s.Handler(), "GET", formHost, "/s/"+familyToken); res.Code != http.StatusGone || strings.Contains(res.Body.String(), "Send me a new link") {
		t.Errorf("a view that does not open: %d", res.Code)
	}
}

// TestFamilyForms: I have paid and the plan request are checked, sealed
// for the inbox under the key and bound to the child; the parent is sent
// back to the page. An expired link's page offers a new link and asks the
// inbox for one, saying the same whatever the answer.
func TestFamilyForms(t *testing.T) {
	f, s := familyWorld(t, false)
	ebube := f.students[0].ID.String()
	h := s.Handler()
	res := postForm(h, formHost, "/s/"+familyToken+"/paid", url.Values{"child": {ebube}, "idem": {"abc"}, "amount": {"25,000.50"}, "date": {"2026-10-20"}, "how": {"transfer"}, "reference": {"TRF/123"}, "note": {"Paid at GTBank"}})
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "done=paid") {
		t.Fatalf("claim: %d %s", res.Code, res.Header().Get("Location"))
	}
	if len(f.asked) != 1 || f.asked[0] != "claim:"+ebube {
		t.Fatalf("asked %v", f.asked)
	}
	ask, err := schools.OpenFamilyAsk(inboxKey, f.payloads[0][:32], f.payloads[0][32:])
	if err != nil || ask.Kind != "claim" || ask.AmountKobo != 2_500_050 || ask.PaidOn != "2026-10-20" || ask.How != "transfer" || ask.Reference != "TRF/123" || ask.Note != "Paid at GTBank" || ask.StudentID.String() != ebube {
		t.Errorf("claim payload: %+v %v", ask, err)
	}
	// The same idem again is the same ask (the inbox keeps one).
	if !bytes.Equal(familyIdem(familyToken, "claim", "abc"), f.payloads[0][:32]) {
		t.Error("the idem is not the payload's prefix")
	}
	// Bad input goes back to the form, not to the inbox.
	for _, c := range []struct {
		form url.Values
		code string
	}{
		{url.Values{"child": {ebube}, "idem": {"x"}, "amount": {"nothing"}, "date": {"2026-10-20"}, "how": {"cash"}}, "amount"},
		{url.Values{"child": {ebube}, "idem": {"x"}, "amount": {"5000"}, "date": {"yesterday"}, "how": {"cash"}}, "date"},
		{url.Values{"child": {ebube}, "idem": {"x"}, "amount": {"5000"}, "date": {"2026-10-20"}, "how": {"crypto"}}, "how"},
		{url.Values{"child": {"nobody"}, "idem": {"x"}, "amount": {"5000"}, "date": {"2026-10-20"}, "how": {"cash"}}, "child"},
	} {
		res := postForm(h, formHost, "/s/"+familyToken+"/paid", c.form)
		if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "err="+c.code) {
			t.Errorf("%s: %d %s", c.code, res.Code, res.Header().Get("Location"))
		}
	}
	if len(f.asked) != 1 {
		t.Errorf("bad input reached the inbox: %v", f.asked)
	}
	// A plan: rows in order, empty rows skipped.
	d1, d2 := time.Now().AddDate(0, 1, 0).Format(time.DateOnly), time.Now().AddDate(0, 2, 0).Format(time.DateOnly)
	res = postForm(h, formHost, "/s/"+familyToken+"/plan", url.Values{"child": {ebube}, "idem": {"p1"}, "amount1": {"40,000"}, "date1": {d1}, "amount3": {"53000"}, "date3": {d2}, "message": {"Salary comes monthly"}})
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "done=plan") {
		t.Fatalf("plan: %d %s", res.Code, res.Header().Get("Location"))
	}
	ask, err = schools.OpenFamilyAsk(inboxKey, f.payloads[1][:32], f.payloads[1][32:])
	if err != nil || ask.Kind != "plan" || len(ask.Instalments) != 2 || ask.Instalments[1].AmountKobo != 5_300_000 || ask.Instalments[1].DueOn != d2 || ask.Message != "Salary comes monthly" {
		t.Errorf("plan payload: %+v %v", ask, err)
	}
	if res := postForm(h, formHost, "/s/"+familyToken+"/plan", url.Values{"child": {ebube}, "idem": {"p2"}, "amount1": {"40,000"}, "date1": {d2}, "amount2": {"1"}, "date2": {d1}}); !strings.Contains(res.Header().Get("Location"), "err=instalments") {
		t.Errorf("out of order: %s", res.Header().Get("Location"))
	}
	// What the record refuses comes back as its own words.
	f.answer = "pending"
	if res := postForm(h, formHost, "/s/"+familyToken+"/plan", url.Values{"child": {ebube}, "idem": {"p3"}, "amount1": {"40,000"}, "date1": {d1}}); !strings.Contains(res.Header().Get("Location"), "err=pending") {
		t.Errorf("pending: %s", res.Header().Get("Location"))
	}
	f.answer = "limited"
	res = postForm(h, formHost, "/s/"+familyToken+"/paid", url.Values{"child": {ebube}, "idem": {"l"}, "amount": {"5000"}, "date": {"2026-10-20"}, "how": {"cash"}})
	if !strings.Contains(res.Header().Get("Location"), "err=limited") {
		t.Errorf("limited: %s", res.Header().Get("Location"))
	}
	if body := get(h, "GET", formHost, "/s/"+familyToken+"?err=limited&for=paid").Body.String(); !strings.Contains(body, "Too many were sent from this page") {
		t.Error("no limit message")
	}
	// Without the inbox key the forms are not open; the page still is.
	s.InboxKey = nil
	if res := postForm(s.Handler(), formHost, "/s/"+familyToken+"/paid", url.Values{"child": {ebube}, "idem": {"k"}, "amount": {"5000"}, "date": {"2026-10-20"}, "how": {"cash"}}); res.Code != http.StatusServiceUnavailable {
		t.Errorf("no key: %d", res.Code)
	}

	// Expired: the page says so and offers a new link, never "not found";
	// asking sends the parent back with the same words whatever happened.
	f, s = familyWorld(t, true)
	f.answer = ""
	h = s.Handler()
	for _, theme := range brand.Themes {
		withBrand(s, brand.Brand{Theme: theme})
		s.brands = nil
		res := get(h, "GET", formHost, "/s/"+familyToken)
		body := res.Body.String()
		if res.Code != http.StatusGone || !strings.Contains(body, "This link has expired") || !strings.Contains(body, "work for 3 days") || !strings.Contains(body, "Send me a new link") || strings.Contains(body, "not found") {
			t.Errorf("%s: expired page: %d %s", theme, res.Code, body)
		}
		if strings.Contains(body, "Ebube") || strings.Contains(body, "9199711905") || strings.Contains(body, "8031234567") {
			t.Errorf("%s: the expired page shows the family's data", theme)
		}
	}
	for _, answer := range []string{"ok", "limited"} {
		f.answer = answer
		res := postForm(h, formHost, "/s/"+familyToken+"/link", url.Values{"idem": {"n-" + answer}})
		if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "done=link") {
			t.Errorf("new link (%s): %d %s", answer, res.Code, res.Header().Get("Location"))
		}
	}
	if len(f.asked) != 2 || f.asked[0] != "link:" {
		t.Errorf("asked %v", f.asked)
	}
	if body := get(h, "GET", formHost, "/s/"+familyToken+"?done=link").Body.String(); !strings.Contains(body, "a new link is on its way") {
		t.Error("no notice after asking for a link")
	}
	// A claim on an expired link is refused by the record: back to the
	// expired page.
	f.answer = "expired"
	if res := postForm(h, formHost, "/s/"+familyToken+"/paid", url.Values{"child": {f.students[0].ID.String()}, "idem": {"e"}, "amount": {"5000"}, "date": {"2026-10-20"}, "how": {"cash"}}); res.Code != http.StatusSeeOther {
		t.Errorf("claim on an expired link: %d", res.Code)
	}
}
