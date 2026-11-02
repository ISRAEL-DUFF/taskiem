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
			"₦27,500.50", "₦10,000", "₦17,500.50", "Due 31 Jan 2099", "Thank you for your custom.\nCollect from the shop.", "Ask Ada Kitchen how to pay.")...)
		check(theme, "/i/"+w.tokens["sent"], 200, append(look, "Invoice INV-0002", "Awaiting payment", "Catering, 12 October", "1 × ₦15,000", "₦15,000", "Ask Ada Kitchen how to pay.")...)
		check(theme, "/i/"+w.tokens["overdue"], 200, append(look, "Invoice INV-0003", "Overdue", "Due 1 Jan 2020", "₦7,000")...)
		body := check(theme, "/i/"+w.tokens["void"], 200, append(look, "Invoice INV-0004", "Void", "This invoice was cancelled.", "₦3,000")...)
		if strings.Contains(body, "how to pay") || strings.Contains(body, "Sent twice") {
			t.Errorf("%s: a void invoice offers payment or shows its reason: %s", theme, body)
		}
		body = check(theme, "/i/"+w.tokens["paid"], 200, append(look, "Invoice INV-0005", "Paid in full. Thank you.", "₦4,500")...)
		if strings.Contains(body, "how to pay") || strings.Contains(body, "Still due") {
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

// TestInvoicePayThroughTheDatabase: the page queues a request as
// taskiem_public, sees it waiting, then sees the account the api role
// issued, and is refused past the limits.
func TestInvoicePayThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	w := &invoiceWorld{t: t, env: env, tokens: map[string]string{}, inv: &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}}
	w.make("sent", invoicing.Draft{AmountKobo: 1500000}, true)
	tok := w.tokens["sent"]
	pool := env.DB.PublicPool(t, 2)
	h := (&Server{Lookup: DBLookup(pool), Brands: DBBrands(pool), Invoices: DBInvoices(pool), InvoicePay: DBInvoicePay{Pool: pool}, FormHost: formHost,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}).Handler()

	if body := get(h, "GET", formHost, "/i/"+tok).Body.String(); !strings.Contains(body, "Pay by bank transfer") {
		t.Fatalf("no button: %s", body)
	}
	if res := post(h, formHost, "/i/"+tok+"/pay", "kind=transfer"); res.Code != http.StatusSeeOther {
		t.Fatalf("pay: %d %s", res.Code, res.Body.String())
	}
	// A part of an invoice paid in full is refused.
	if res := post(h, formHost, "/i/"+tok+"/pay", "kind=transfer&amount=1000"); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("part: %d", res.Code)
	}
	if body := get(h, "GET", formHost, "/i/"+tok).Body.String(); !strings.Contains(body, "Getting your account number…") || !strings.Contains(body, `content="2"`) {
		t.Fatalf("not waiting: %s", body)
	}

	// The api role takes the request and issues the account.
	var rid, inv uuid.UUID
	if err := env.DB.App.QueryRow(ctx, `SELECT id, invoice_id FROM taskiem_invoice_requests_claim(10)`).Scan(&rid, &inv); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(20 * time.Minute)
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status,
			account_number, bank_name, account_name, expires_at, on_late, on_wrong_amount)
			VALUES (gen_random_uuid(), $1, $2, 1, 'transfer', 'paystack', 'main', 'tsk_1', 1500000, 'open', '9900112233', 'Wema Bank', 'Paystack-Ada', $3, 'returned', 'returned')`,
			env.Tenant, inv, until)
		return err
	})
	if _, err := env.DB.App.Exec(ctx, `SELECT taskiem_invoice_requests_done($1)`, rid); err != nil {
		t.Fatal(err)
	}
	body := get(h, "GET", formHost, "/i/"+tok).Body.String()
	for _, want := range []string{"9900112233", "Wema Bank", "Transfer ₦15,000 to", "Valid until", "Get a new account number", `content="30"`} {
		if !strings.Contains(body, want) {
			t.Errorf("open account: no %q in %s", want, body)
		}
	}
	for _, re := range policyBreaks {
		if re.MatchString(body) {
			t.Errorf("%s breaks the public CSP", re)
		}
	}

	// Past the hour's limit the page offers no new number, and the
	// database refuses one.
	if _, err := env.DB.Admin.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
		SELECT gen_random_uuid(), $1, $2, s, 'transfer', 'paystack', 'main', 'tsk_' || s, 1500000, 'failed' FROM generate_series(2, 5) s`, env.Tenant, inv); err != nil {
		t.Fatal(err)
	}
	if body := get(h, "GET", formHost, "/i/"+tok).Body.String(); strings.Contains(body, `value="transfer"`) {
		t.Errorf("a sixth number is offered: %s", body)
	}
	var sixth *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, 'transfer', 0)`, invoicing.TokenHash(tok)).Scan(&sixth); err != nil || sixth != nil {
		t.Errorf("a sixth request: %v %v", err, sixth)
	}

	// A customer with a number of their own sees it on every invoice's
	// page (W3); another customer's invoice does not.
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.RegisterAccount(ctx, tx, env.Tenant, invoicing.AccountIn{Customer: invoiceCustomer, Channel: "iswallet/taskiem",
			AccountNumber: "8800000042", Reference: "tsk_chiamaka", BankName: "Wema Bank"}, "user:ada")
		return err
	})
	if body := get(h, "GET", formHost, "/i/"+tok).Body.String(); !strings.Contains(body, "your own account number") || !strings.Contains(body, "8800000042") {
		t.Errorf("own account: %s", body)
	}
	other := invoicing.Draft{AmountKobo: 500000}
	other.Customer = invoicing.Customer{Name: "Bola Ade", Phone: "+2348031112222"}
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.inv.Create(ctx, tx, env.Tenant, other, "user:ada")
		return err
	})
	sent, err := w.inv.Send(ctx, env.Tenant, x.Number, "user:ada", "other")
	if err != nil {
		t.Fatal(err)
	}
	if body := get(h, "GET", formHost, strings.TrimPrefix(sent.Link, "https://"+formHost)).Body.String(); strings.Contains(body, "8800000042") {
		t.Errorf("another customer sees the number: %s", body)
	}
}

// TestInvoiceLinkCodeThroughTheDatabase: the public role reads the
// business's link code for its invoice's page (business links), and
// nothing before the business has one.
func TestInvoiceLinkCodeThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	w := &invoiceWorld{t: t, env: env, tokens: map[string]string{}, inv: &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://" + formHost,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}}
	w.make("sent", invoicing.Draft{AmountKobo: 150000, Description: "Rice"}, true)
	read := DBInvoices(env.DB.PublicPool(t, 1))
	p, ok, err := read(ctx, invoicing.TokenHash(w.tokens["sent"]))
	if err != nil || !ok || p.LinkCode != "" {
		t.Fatalf("before a code: %+v %v %v", p.LinkCode, ok, err)
	}
	if _, err := env.DB.Admin.Exec(ctx, `INSERT INTO link_settings (tenant_id, code, updated_by) VALUES ($1, 'abcd2345', 'test')`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	if p, _, err = read(ctx, invoicing.TokenHash(w.tokens["sent"])); err != nil || p.LinkCode != "abcd2345" {
		t.Errorf("the code: %q %v", p.LinkCode, err)
	}
}
