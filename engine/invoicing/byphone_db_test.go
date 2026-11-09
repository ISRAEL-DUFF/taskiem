package invoicing_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

func TestPhoneForms(t *testing.T) {
	for in, want := range map[string][]string{
		"2348031234567":     {"2348031234567", "+2348031234567", "08031234567"},
		"+234 803 123 4567": {"+234 803 123 4567", "+2348031234567", "2348031234567", "08031234567"},
		"08031234567":       {"08031234567", "+2348031234567", "2348031234567"},
		"55501":             {"55501", "+55501"},
	} {
		if got := invoicing.PhoneForms(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

// What a customer owes, by their number however WhatsApp gives it
// (decision 0062 V9): their open invoices, oldest due first, and the last
// one they paid; another customer's are never counted.
func TestFindByPhone(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tenant := w.env.Tenant
	svc := invoicing.Service{Invoices: w.inv}
	send := func(c invoicing.Customer, kobo int64, due, key string) invoicing.Invoice {
		t.Helper()
		x := w.create(invoicing.Draft{Customer: c, AmountKobo: kobo, DueOn: due})
		if _, err := w.inv.Send(ctx, tenant, x.Number, "user:owner", key); err != nil {
			t.Fatal(err)
		}
		return x
	}
	paid := send(ada, 100000, "2026-10-01", "s0")
	w.pay(paid.Number, "cash-0", 100000)
	later := send(ada, 300000, "2026-12-01", "s1")
	sooner := send(invoicing.Customer{Name: "Ada Obi", Phone: "08031234567"}, 200000, "2026-11-20", "s2")
	send(invoicing.Customer{Name: "Bayo", Phone: "+2348099999999"}, 900000, "2026-11-01", "s3")

	got, err := svc.FindByPhone(ctx, tenant, "2348031234567")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || got.DueKobo != 500000 || len(got.Open) != 2 || got.LastPaid == nil || got.LastPaid.Number != paid.Number || got.CustomerName != "Ada Obi" {
		t.Fatalf("found: %+v", got)
	}
	numbers := map[string]bool{got.Open[0].Number: true, got.Open[1].Number: true}
	if !numbers[later.Number] || !numbers[sooner.Number] {
		t.Fatalf("open: %v", numbers)
	}
	none, err := svc.FindByPhone(ctx, tenant, "2348000000000")
	if err != nil || none.Found || none.DueKobo != 0 {
		t.Fatalf("a stranger: %+v %v", none, err)
	}
}
