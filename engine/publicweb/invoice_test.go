package publicweb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

const invoiceToken = "abcdefghijklmnopqrstuvwxyz012345"

func invoiceServer(read Invoices, views *formguard.Limiter) *Server {
	if views == nil {
		views = formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)
	}
	s := &Server{Lookup: (&fakeHosts{}).lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: views, FormHost: formHost, Invoices: read}
	testTheme(s)
	return s
}

func fakeInvoice(p InvoicePage) Invoices {
	return func(_ context.Context, h []byte) (InvoicePage, bool, error) {
		if !bytes.Equal(h, invoicing.TokenHash(invoiceToken)) {
			return InvoicePage{}, false, nil
		}
		return p, true, nil
	}
}

// TestInvoicePage draws an invoice in every theme, without the forms'
// keys: the form host serves invoices even when forms are not open.
func TestInvoicePage(t *testing.T) {
	due := time.Date(2099, 1, 31, 0, 0, 0, 0, time.UTC)
	p := InvoicePage{Tenant: uuid.New(), Business: "Ada Kitchen", Number: "INV-0007", Status: "part_paid", DueOn: &due, Note: "Thanks",
		Lines: []invoicing.Line{{Description: "Jollof <rice>", Quantity: 3, UnitKobo: 250000}}, TotalKobo: 750000, PaidKobo: 250050}
	for _, theme := range brand.Themes {
		s := invoiceServer(fakeInvoice(p), nil)
		withBrand(s, brand.Brand{Theme: theme})
		res := get(s.Handler(), "GET", formHost, "/i/"+invoiceToken)
		body := res.Body.String()
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", theme, res.Code, body)
		}
		for _, want := range []string{"theme-" + theme + ".", "Ada Kitchen", "Invoice INV-0007", "Part-paid", "Jollof &lt;rice&gt;", "3 × ₦2,500", "₦7,500",
			"₦2,500.50", "₦4,999.50", "Due 31 Jan 2099", "Thanks", "Ask Ada Kitchen how to pay."} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q in %s", theme, want, body)
			}
		}
		if res.Header().Get("X-Robots-Tag") != "noindex" || res.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", theme, res.Header())
		}
	}

	// A draft, should one ever be read, is not found.
	p.Status = "draft"
	if res := get(invoiceServer(fakeInvoice(p), nil).Handler(), "GET", formHost, "/i/"+invoiceToken); res.Code != 404 {
		t.Errorf("draft: %d", res.Code)
	}
	// A failed read is "try again", not "not found".
	failing := func(context.Context, []byte) (InvoicePage, bool, error) {
		return InvoicePage{}, false, errors.New("down")
	}
	if res := get(invoiceServer(failing, nil).Handler(), "GET", formHost, "/i/"+invoiceToken); res.Code != http.StatusServiceUnavailable {
		t.Errorf("failed read: %d", res.Code)
	}
	// Without invoices, every token is not found.
	if res := get(invoiceServer(nil, nil).Handler(), "GET", formHost, "/i/"+invoiceToken); res.Code != 404 || !strings.Contains(res.Body.String(), "This invoice was not found.") {
		t.Errorf("no invoices: %d", res.Code)
	}
}

// TestInvoicePageLimit: invoice pages count against the per-address
// page-view limit, as every page does.
func TestInvoicePageLimit(t *testing.T) {
	h := invoiceServer(fakeInvoice(InvoicePage{Number: "INV-0001", Status: "sent", TotalKobo: 100}), formguard.NewLimiter(rate.Every(time.Hour), 2, 100, time.Hour)).Handler()
	for i, want := range []int{200, 200, http.StatusTooManyRequests} {
		if res := get(h, "GET", formHost, "/i/"+invoiceToken); res.Code != want {
			t.Errorf("view %d: %d, want %d", i+1, res.Code, want)
		}
	}
}
