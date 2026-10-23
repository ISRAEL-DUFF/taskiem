package invoicing_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// fakeRails is a payments rail that opens accounts and reports them paid
// when told.
type fakeRails struct {
	mu    sync.Mutex
	calls []string
	ins   []map[string]any
	paid  map[string]int64 // payment id → amount
	fail  error
}

func (f *fakeRails) Resolve(_ context.Context, _ uuid.UUID, connection string) (invoicing.Rail, error) {
	if connection != "main" && connection != "paystack/main" {
		return invoicing.Rail{}, invoicing.ErrNoRail
	}
	return invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Link: true,
		Transfer: &connector.TransferFacts{OnLate: "returned", OnWrongAmount: "returned", MinMinutes: 15, MaxMinutes: 480}}, nil
}

func (f *fakeRails) Key(_ invoicing.Rail, capability string, k effects.KeyInput) (string, error) {
	return "tsk_" + capability + "_" + k.Seed + "_" + k.StepID, nil
}

func (f *fakeRails) Call(_ context.Context, _ uuid.UUID, r invoicing.Rail, capability string, in map[string]any, k effects.KeyInput) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, capability)
	f.ins = append(f.ins, in)
	switch capability {
	case "collect_transfer":
		if f.fail != nil {
			return nil, f.fail
		}
		pid, _ := f.Key(r, capability, k)
		return map[string]any{"payment_id": pid, "account_number": "9900112233", "bank_name": "Wema Bank", "account_name": "Shop / Ada",
			"amount_kobo": in["amount_kobo"], "expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)}, nil
	case "collect_link":
		pid, _ := f.Key(r, capability, k)
		return map[string]any{"payment_id": pid, "payment_url": "https://pay.test/" + pid}, nil
	case "get_payment":
		id, _ := in["payment_id"].(string)
		if a, ok := f.paid[id]; ok {
			return map[string]any{"payment_id": id, "status": "paid", "amount_kobo": a}, nil
		}
		return map[string]any{"payment_id": id, "status": "pending"}, nil
	}
	return nil, errors.New("no such capability")
}

func TestAccountsIssuedAndPaid(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	rails := &fakeRails{paid: map[string]int64{}}
	w.inv.Rails = rails
	var signalled []string
	sig := w.inv.Signal
	w.inv.Signal = func(ctx context.Context, tenant uuid.UUID, env, event, corr, key string, payload any) ([]uuid.UUID, bool, error) {
		signalled = append(signalled, event+":"+corr)
		return sig(ctx, tenant, env, event, corr, key, payload)
	}
	x := w.create(invoicing.Draft{Customer: invoicing.Customer{Name: "Ada Obi", Phone: "+2348031234567"}, AmountKobo: 2500000})

	// A draft gets no account.
	if _, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "transfer", 0); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("draft: %v", err)
	}
	sent, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1")
	if err != nil {
		t.Fatal(err)
	}

	// The page asks; the queue issues it.
	tok := invoicing.TokenHash(strings.TrimPrefix(sent.Link, "https://forms.test/i/"))
	var rid *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, 'transfer', 0)`, tok).Scan(&rid); err != nil || rid == nil {
		t.Fatalf("request %v %v", err, rid)
	}
	// Part of an invoice that is paid in full is refused.
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, 'transfer', 100000)`, tok).Scan(&rid); err != nil || rid != nil {
		t.Fatalf("part request %v %v", err, rid)
	}
	if err := w.inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var status, number string
	var waiting bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, account_number, waiting FROM taskiem_public_invoice_payment($1) WHERE kind = 'transfer'`, tok).
		Scan(&status, &number, &waiting); err != nil || status != "open" || number != "9900112233" || waiting {
		t.Fatalf("page sees %v %q %q waiting %v", err, status, number, waiting)
	}
	in := rails.ins[0]
	cust, _ := in["customer"].(map[string]any)
	if in["amount_kobo"] != int64(2500000) || in["expires_minutes"] != 30 || in["description"] != "Invoice "+x.Number ||
		cust["email"] != "invoices+inv0001@forms.test" {
		t.Errorf("rail was asked %+v", in)
	}

	// Asking again gives the same open account.
	a, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "transfer", 0)
	if err != nil || a.Seq != 1 || len(rails.calls) != 1 {
		t.Fatalf("again %v %+v calls %v", err, a, rails.calls)
	}

	// Nothing paid yet: the checker finds nothing.
	if err := w.inv.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	// Paid: the checker counts it, once, and tells the waiting run.
	rails.paid[a.PaymentID] = 2500000
	later := time.Now().Add(time.Minute)
	w.inv.Now = func() time.Time { return later }
	for range 2 {
		if err := w.inv.CheckPayments(ctx); err != nil {
			t.Fatal(err)
		}
		later = later.Add(time.Minute)
	}
	y, err := (invoicing.Service{Invoices: w.inv}).Read(ctx, w.env.Tenant, x.Number)
	if err != nil || y.Status != "paid" || len(y.Payments) != 1 || y.Payments[0].PaymentID != a.PaymentID || y.Payments[0].How != "exact" {
		t.Fatalf("after payment %v %+v", err, y)
	}
	if len(signalled) != 1 || signalled[0] != "invoice.paid:"+x.Number {
		t.Errorf("signals %v", signalled)
	}
	// A paid invoice takes no new account.
	if _, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "link", 0); !errors.Is(err, invoicing.ErrState) {
		t.Errorf("paid: %v", err)
	}
}

func TestAccountLimitsPartsAndFailures(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	rails := &fakeRails{paid: map[string]int64{}}
	w.inv.Rails = rails
	yes := true
	x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 2500000, AllowPartPayments: &yes})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	// Parts: between ₦1,000 and what is due.
	for _, bad := range []int64{50000, 2600000} {
		if _, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "transfer", bad); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("part %d: %v", bad, err)
		}
	}
	// Different amounts are different accounts, up to five an hour.
	for i := range invoicing.MaxPerHour {
		a, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "transfer", int64(100000*(i+1)))
		if err != nil || a.Seq != i+1 || a.Status != "open" {
			t.Fatalf("account %d: %v %+v", i, err, a)
		}
	}
	if _, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "transfer", 900000); !errors.Is(err, invoicing.ErrLimit) {
		t.Errorf("sixth: %v", err)
	}
	// Links count apart.
	l, err := w.inv.Issue(ctx, w.env.Tenant, x.ID, "link", 0)
	if err != nil || !strings.HasPrefix(l.PaymentURL, "https://pay.test/") {
		t.Fatalf("link %v %+v", err, l)
	}

	// A refusal fails the account and says why; an unknown outcome keeps
	// it requested, to be looked up.
	y := w.create(invoicing.Draft{Customer: ada, AmountKobo: 100000})
	if _, err := w.inv.Send(ctx, w.env.Tenant, y.Number, "user:ada", "k2"); err != nil {
		t.Fatal(err)
	}
	rails.fail = errors.New("transfer payments are not enabled")
	if _, err := w.inv.Issue(ctx, w.env.Tenant, y.ID, "transfer", 0); err == nil {
		t.Fatal("no error")
	}
	rails.fail = effects.ErrUnknownOutcome
	w.inv.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := w.inv.Issue(ctx, w.env.Tenant, y.ID, "transfer", 0); err == nil {
		t.Fatal("no error")
	}
	got := map[string]int{}
	for _, a := range w.accounts(y.ID) {
		got[a.Status]++
		if a.Status == "failed" && !strings.Contains(a.Error, "not enabled") {
			t.Errorf("failed without the reason: %+v", a)
		}
	}
	if got["failed"] != 1 || got["requested"] != 1 {
		t.Errorf("statuses %v", got)
	}
	// The requested one is paid all the same: found by its payment id.
	req := w.accounts(y.ID)[0]
	rails.paid[req.PaymentID] = 100000
	if err := w.inv.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if z, err := (invoicing.Service{Invoices: w.inv}).Read(ctx, w.env.Tenant, y.Number); err != nil || z.Status != "paid" {
		t.Fatalf("requested account paid: %v %+v", err, z)
	}
}

func (w *world) accounts(invoice uuid.UUID) []invoicing.Account {
	w.t.Helper()
	var out []invoicing.Account
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = invoicing.Accounts(context.Background(), tx, invoice)
		return err
	})
	return out
}
