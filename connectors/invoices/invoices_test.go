package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/invoices"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

type ops struct {
	created invoicing.Draft
	listed  string
	account invoicing.AccountRequest
}

func (o *ops) CreateOnce(_ context.Context, _ uuid.UUID, _ string, d invoicing.Draft, _ string) (invoicing.Invoice, error) {
	o.created = d
	return invoicing.Invoice{Number: "INV-0007", Status: "draft", TotalKobo: 2500000}, nil
}
func (o *ops) Send(context.Context, uuid.UUID, string, string, string) (invoicing.Sent, error) {
	return invoicing.Sent{}, invoicing.ErrState
}
func (o *ops) Read(context.Context, uuid.UUID, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, invoicing.ErrNotFound
}
func (o *ops) Cancel(context.Context, uuid.UUID, string, string, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, nil
}
func (o *ops) NotifyCustomer(context.Context, uuid.UUID, string, string, string, string) (map[string]any, error) {
	return map[string]any{}, nil
}
func (o *ops) ListFor(_ context.Context, _ uuid.UUID, status string, within time.Duration, limit int) ([]invoicing.Listed, error) {
	o.listed = fmt.Sprintf("%s %s %d", status, within, limit)
	if status != "overdue" && status != "paid" {
		return nil, invoicing.ErrInvalid
	}
	return []invoicing.Listed{{Number: "INV-0007", Status: "overdue", TotalKobo: 2500000, DueKobo: 500000, DueOn: "2026-10-01", DaysOverdue: 22}}, nil
}

func (o *ops) GiveAccount(_ context.Context, _ uuid.UUID, in invoicing.AccountRequest, _ string) (invoicing.CustomerAccount, error) {
	o.account = in
	if in.Channel == "nope/main" {
		return invoicing.CustomerAccount{}, invoicing.ErrNoRail
	}
	return invoicing.CustomerAccount{Channel: in.Channel, AccountNumber: "9900000001", BankName: "Wema Bank", Reference: "tsk_abc", Customer: in.Customer}, nil
}

var _ invoices.Ops = invoicing.Service{}

// register_account: issued on the connection, or the one the business
// has; by hand needs the number and reference; a connection that cannot
// issue fails the step.
func TestRegisterAccount(t *testing.T) {
	o := &ops{}
	c := invoices.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "acct"})
	r, err := c.Actions["register_account"].Execute(ctx, connector.Request{Input: map[string]any{"customer": map[string]any{"name": "Ada Obi", "phone": "+2348012345678"},
		"connection": "paystack/main", "issue": true, "idempotency_key": "tsk_a"}, IdempotencyKey: "tsk_a"})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["account_number"] != "9900000001" || out["reference"] != "tsk_abc" || out["connection"] != "paystack/main" || out["bank_name"] != "Wema Bank" || out["customer_name"] != "Ada Obi" ||
		!o.account.Issue || o.account.Customer.Phone != "+2348012345678" {
		t.Errorf("issued: %v (%+v)", out, o.account)
	}
	r, err = c.Actions["register_account"].Execute(ctx, connector.Request{Input: map[string]any{"customer": map[string]any{"name": "Ada Obi", "key": "cust:7"},
		"connection": "iswallet/main", "account_number": "1234567890", "reference": "ref-1", "bank_name": "Sterling"}})
	if err != nil || o.account.Issue || o.account.AccountNumber != "1234567890" || o.account.Customer.Key != "cust:7" || r.Output.(map[string]any)["connection"] != "iswallet/main" {
		t.Errorf("by hand: %v %v %+v", r.Output, err, o.account)
	}
	for _, in := range []map[string]any{
		{"customer": map[string]any{"name": "Ada"}, "connection": "iswallet/main"},
		{"customer": map[string]any{"name": "Ada"}, "connection": "nope/main", "issue": true},
	} {
		if _, err := c.Actions["register_account"].Execute(ctx, connector.Request{Input: in}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%v: %v", in, err)
		}
	}
	if _, err := c.Actions["register_account"].Execute(context.Background(), connector.Request{Input: map[string]any{"customer": map[string]any{"name": "Ada"}, "connection": "x/y", "issue": true}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
}

// list reads overdue or lately paid invoices, with what is due and how
// late, and no customer details.
func TestList(t *testing.T) {
	o := &ops{}
	c := invoices.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "overdue"})
	r, err := c.Actions["list"].Execute(ctx, connector.Request{Input: map[string]any{"status": "overdue"}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	got := out["invoices"].([]any)[0].(map[string]any)
	if out["count"] != 1 || got["number"] != "INV-0007" || got["due"] != "₦5,000" || got["days_overdue"] != 22 || got["due_on"] != "2026-10-01" ||
		o.listed != "overdue 24h0m0s 50" {
		t.Errorf("list %v (%s)", out, o.listed)
	}
	if _, ok := got["customer_name"]; ok {
		t.Error("a customer's details in the list")
	}
	if _, err := c.Actions["list"].Execute(ctx, connector.Request{Input: map[string]any{"status": "paid", "paid_within_hours": float64(6), "limit": float64(10)}}); err != nil ||
		o.listed != "paid 6h0m0s 10" {
		t.Errorf("paid: %v %s", err, o.listed)
	}
	for _, in := range []map[string]any{{"status": "draft"}, {"status": "overdue", "limit": float64(500)}} {
		if _, err := c.Actions["list"].Execute(ctx, connector.Request{Input: in}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%v: %v", in, err)
		}
	}
}

func TestActions(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(invoices.New(nil)); err != nil {
		t.Fatal(err)
	}
	o := &ops{}
	c := invoices.New(o)
	in := map[string]any{"customer": map[string]any{"name": "Ada"}, "amount_kobo": 2500000, "idempotency_key": "tsk_x"}
	// Outside a run, nothing happens.
	if _, err := c.Actions["create"].Execute(context.Background(), connector.Request{Input: in}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "bill"})
	r, err := c.Actions["create"].Execute(ctx, connector.Request{Input: in, IdempotencyKey: "tsk_x"})
	if err != nil || r.Output.(map[string]any)["total"] != "₦25,000" || o.created.Customer.Name != "Ada" || o.created.AmountKobo != 2500000 {
		t.Fatalf("create %v %v %+v", r.Output, err, o.created)
	}
	// What the invoice's state refuses, and a missing invoice, fail the step.
	for _, a := range []string{"send", "get"} {
		if _, err := c.Actions[a].Execute(ctx, connector.Request{Input: map[string]any{"number": "INV-0007"}}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: %v", a, err)
		}
	}
	// With no service bound, every action fails plainly.
	late := &invoices.Late{}
	if _, err := invoices.New(late).Actions["get"].Execute(ctx, connector.Request{Input: map[string]any{"number": "INV-1"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unbound: %v", err)
	}
}
