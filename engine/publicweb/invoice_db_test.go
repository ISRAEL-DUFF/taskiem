package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/invoicing"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

// invoiceWorld is a business with invoices in every state a customer can
// see, read back through the real functions as taskiem_public.
type invoiceWorld struct {
	t      *testing.T
	env    *rt.Env
	inv    *invoicing.Invoices
	tokens map[string]string // by name
}

var invoiceCustomer = invoicing.Customer{Name: "Chiamaka Okafor", Phone: "+2348039876543", Email: "chiamaka@example.com"}

func (w *invoiceWorld) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

// make creates an invoice and, unless draft, sends it, keeping its token.
func (w *invoiceWorld) make(name string, d invoicing.Draft, send bool) string {
	w.t.Helper()
	ctx := context.Background()
	d.Customer = invoiceCustomer
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.inv.Create(ctx, tx, w.env.Tenant, d, "user:ada")
		return err
	})
	if send {
		sent, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", name)
		if err != nil {
			w.t.Fatal(err)
		}
		w.tokens[name] = strings.TrimPrefix(sent.Link, "https://"+formHost+"/i/")
	}
	return x.Number
}

func (w *invoiceWorld) pay(number string, kobo int64, id string) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.inv.RecordPayment(context.Background(), tx, w.env.Tenant, number, invoicing.PaymentIn{AmountKobo: kobo, PaymentID: id, How: "manual", By: "user:ada"})
		return err
	})
}

// TestInvoicePageThroughTheDatabase: a sent invoice's page, in every
// theme, over taskiem_public_customer_invoice and taskiem_public_brand.
func TestInvoicePageThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Ada Kitchen' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	w := &invoiceWorld{t: t, env: env, tokens: map[string]string{}, inv: &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}}

	part := w.make("part", invoicing.Draft{Lines: []invoicing.Line{{Description: "Bags of rice", Quantity: 5, UnitKobo: 500000}, {Description: "Delivery", Quantity: 1, UnitKobo: 250050}},
		DueOn: "2099-01-31", Note: "Thank you for your custom.\nCollect from the shop."}, true)
	w.pay(part, 1000000, "pay-1")
	w.make("sent", invoicing.Draft{AmountKobo: 1500000, Description: "Catering, 12 October"}, true)
	late := w.make("overdue", invoicing.Draft{AmountKobo: 700000, DueOn: "2099-01-31"}, true)
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET due_on = '2020-01-01' WHERE tenant_id = $1 AND number = $2`, env.Tenant, late); err != nil {
		t.Fatal(err)
	}
	void := w.make("void", invoicing.Draft{AmountKobo: 300000}, true)
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.Void(ctx, tx, w.env.Tenant, void, "Sent twice", "user:ada")
		return err
	})
	paid := w.make("paid", invoicing.Draft{AmountKobo: 450000}, true)
	w.pay(paid, 450000, "pay-2")
	// A draft whose token was made but never sent: not shown.
	draft := w.make("draft", invoicing.Draft{AmountKobo: 100000}, false)
	draftToken := strings.Repeat("d", 32)
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET page_token_hash = $3 WHERE tenant_id = $1 AND number = $2`, env.Tenant, draft, invoicing.TokenHash(draftToken)); err != nil {
		t.Fatal(err)
	}

	pool := env.DB.PublicPool(t, 2)
	newSrv := func() http.Handler {
		s := &Server{Lookup: DBLookup(pool), Brands: DBBrands(pool), Invoices: DBInvoices(pool), FormHost: formHost,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
		return s.Handler()
	}
	personal := []string{"Chiamaka", "Okafor", "8039876543", "803 987 6543", "chiamaka@example.com", "example.com"}
	check := func(theme, path string, code int, wants ...string) string {
		t.Helper()
		res := get(newSrv(), "GET", formHost, path)
		body := res.Body.String()
		if res.Code != code {
			t.Fatalf("%s %s: status %d, want %d: %s", theme, path, res.Code, code, body)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s %s: no %q in %s", theme, path, want, body)
			}
		}
		for _, p := range personal {
			if strings.Contains(body, p) {
				t.Errorf("%s %s: the page shows %q", theme, path, p)
			}
		}
		for _, re := range policyBreaks {
			if re.MatchString(body) {
				t.Errorf("%s %s: %s breaks the public CSP", theme, path, re)
			}
		}
		h := res.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex" || h.Get("Content-Security-Policy") != httpsec.PublicCSP || h.Get("Set-Cookie") != "" {
			t.Errorf("%s %s: headers %v", theme, path, h)
		}
		return body
	}

	for _, theme := range brand.Themes {
		if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET brand = jsonb_build_object('theme', $2::text, 'color', '#1f3a5f') WHERE id = $1`, env.Tenant, theme); err != nil {
			t.Fatal(err)
		}
		look := []string{"theme-" + theme + ".", "/_brand/1/1f3a5f.css", "Ada Kitchen"}
		check(theme, "/i/"+w.tokens["part"], 200, append(look, "Invoice INV-0001", "Part-paid", "Bags of rice", "5 × ₦5,000", "₦25,000", "Delivery", "1 × ₦2,500.50",
			"₦27,500.50", "₦10,000", "₦17,500.50", "Due 31 Jan 2099", "Thank you for your custom.\nCollect from the shop.", "Payment options will appear here soon.")...)
		check(theme, "/i/"+w.tokens["sent"], 200, append(look, "Invoice INV-0002", "Awaiting payment", "Catering, 12 October", "1 × ₦15,000", "₦15,000", "Payment options will appear here soon.")...)
		check(theme, "/i/"+w.tokens["overdue"], 200, append(look, "Invoice INV-0003", "Overdue", "Due 1 Jan 2020", "₦7,000")...)
		body := check(theme, "/i/"+w.tokens["void"], 200, append(look, "Invoice INV-0004", "Void", "This invoice was cancelled.", "₦3,000")...)
		if strings.Contains(body, "Payment options") || strings.Contains(body, "Sent twice") {
			t.Errorf("%s: a void invoice offers payment or shows its reason: %s", theme, body)
		}
		body = check(theme, "/i/"+w.tokens["paid"], 200, append(look, "Invoice INV-0005", "Paid in full. Thank you.", "₦4,500")...)
		if strings.Contains(body, "Payment options") || strings.Contains(body, "Still due") {
			t.Errorf("%s: a paid invoice asks for more: %s", theme, body)
		}
	}

	// A draft's token, an unknown one and malformed ones answer the same.
	want := check("", "/i/"+draftToken, 404, "This invoice was not found.")
	for _, p := range []string{"/i/" + strings.Repeat("A", 32), "/i/short", "/i/" + strings.Repeat("A", 33), "/i/" + w.tokens["sent"] + "/x", "/i/", "/i/a%2Fb"} {
		if got := check("", p, 404); got != want {
			t.Errorf("%s answers differently from a draft's token:\n%s\n%s", p, got, want)
		}
	}
	// HEAD and GET only.
	if res := get(newSrv(), "POST", formHost, "/i/"+w.tokens["sent"]); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", res.Code)
	}
	// Only on the form host.
	if res := get(newSrv(), "GET", "ada.shops.test", "/i/"+w.tokens["sent"]); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "INV-0002") {
		t.Errorf("another host: %d", res.Code)
	}
}
