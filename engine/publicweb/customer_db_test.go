package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

// TestCustomerPageThroughTheDatabase: a customer's page (decision 0060,
// R5), in every theme, as taskiem_public: their open lines oldest first
// with what is due on each and the total, nothing personal, a part of the
// total asked for and the account shown; a customer who owes nothing,
// and unknown or malformed tokens, answer the same.
func TestCustomerPageThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Ada Kitchen' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	w := &invoiceWorld{t: t, env: env, tokens: map[string]string{}, inv: &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}}
	var link, customer string
	w.in(func(tx pgx.Tx) error {
		c, err := w.inv.NewBookCustomer(ctx, tx, env.Tenant, "Chiamaka Okafor", "+2348039876543", "user:ada")
		if err != nil {
			return err
		}
		for i, d := range []invoicing.CreditDraft{{AmountKobo: 500000, Description: "Bags of rice", DueOn: "2099-03-01"}, {AmountKobo: 250050, Description: "Palm oil"}} {
			x, err := w.inv.RecordCredit(ctx, tx, env.Tenant, "k"+string(rune('1'+i)), c, d, "user:ada")
			if err != nil {
				return err
			}
			customer = x.CustomerID()
			if i == 0 {
				if _, _, err := w.inv.RecordPayment(ctx, tx, env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 100000, PaymentID: "p1", How: "manual", By: "user:ada"}); err != nil {
					return err
				}
			}
		}
		link, err = w.inv.CustomerLink(ctx, tx, env.Tenant, customer, "user:ada")
		return err
	})
	tok := strings.TrimPrefix(link, "https://"+formHost+"/c/")
	pool := env.DB.PublicPool(t, 2)
	h := (&Server{Lookup: DBLookup(pool), Brands: DBBrands(pool), Customers: DBCustomers(pool), CustomerPay: DBCustomerPay{Pool: pool}, FormHost: formHost,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}).Handler()
	personal := []string{"Chiamaka", "Okafor", "8039876543"}
	page := func(path string, code int, wants ...string) string {
		t.Helper()
		res := get(h, "GET", formHost, path)
		body := res.Body.String()
		if res.Code != code {
			t.Fatalf("%s: %d %s", path, res.Code, body)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q in %s", path, want, body)
			}
		}
		for _, p := range personal {
			if strings.Contains(body, p) {
				t.Errorf("%s: the page shows %q", path, p)
			}
		}
		for _, re := range policyBreaks {
			if re.MatchString(body) {
				t.Errorf("%s: %s breaks the public CSP", path, re)
			}
		}
		if res.Header().Get("X-Robots-Tag") != "noindex" || res.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", path, res.Header())
		}
		return body
	}
	for _, theme := range brand.Themes {
		if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET brand = jsonb_build_object('theme', $2::text, 'color', '#1f3a5f') WHERE id = $1`, env.Tenant, theme); err != nil {
			t.Fatal(err)
		}
		body := page("/c/"+tok, 200, "What you owe Ada Kitchen", "INV-0001: Bags of rice", "due 1 Mar 2099", "₦4,000", "₦1,000 of ₦5,000 paid.",
			"INV-0002: Palm oil", "₦2,500.50", "₦6,500.50", "Pay what you owe", `action="/c/`+tok+`/pay"`, "pay at least ₦1,000", "the oldest first")
		// Undated, the palm oil is placed by the day it was recorded,
		// before the rice due in 2099: what her money pays first.
		if strings.Index(body, "INV-0002") > strings.Index(body, "INV-0001") {
			t.Errorf("%s: not in the order her money pays them", theme)
		}
	}

	// A part is asked for; too much is refused.
	if res := post(h, formHost, "/c/"+tok+"/pay", "kind=transfer&amount=7000"); res.Code != 422 || !strings.Contains(res.Body.String(), "cannot be paid here") {
		t.Errorf("too much: %d %s", res.Code, res.Body.String())
	}
	if res := post(h, formHost, "/c/"+tok+"/pay", "kind=transfer&amount=2,000"); res.Code != 303 || res.Header().Get("Location") != "/c/"+tok {
		t.Fatalf("pay: %d %v", res.Code, res.Header())
	}
	page("/c/"+tok, 200, "Getting your account number…", `content="2"`)

	// Paid off: the page says there is nothing to pay, as for an unknown
	// token.
	w.in(func(tx pgx.Tx) error {
		for _, n := range []string{"INV-0001", "INV-0002"} {
			x, err := w.inv.Get(ctx, tx, env.Tenant, n)
			if err != nil {
				return err
			}
			if _, _, err := w.inv.RecordPayment(ctx, tx, env.Tenant, n, invoicing.PaymentIn{AmountKobo: x.DueKobo, PaymentID: "off-" + n, How: "manual", By: "user:ada"}); err != nil {
				return err
			}
		}
		return nil
	})
	want := page("/c/"+tok, 404, "There is nothing to pay on this page.")
	for _, p := range []string{"/c/" + strings.Repeat("A", 32), "/c/short", "/c/" + tok + "/x", "/c/"} {
		if got := page(p, 404); got != want {
			t.Errorf("%s answers differently:\n%s\n%s", p, got, want)
		}
	}
}
