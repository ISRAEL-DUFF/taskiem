package capability

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
)

func TestSet(t *testing.T) {
	rails := []string{"balance", "collect_link", "collect_push", "collect_transfer", "get_payment", "get_payout", "list_banks", "list_credits", "pay_bank", "pay_many", "pay_mobile_money", "pay_wallet", "refund", "resolve_account", "virtual_account"}
	books := []string{"books_accounts", "books_bill", "books_customer", "books_expense", "books_invoice", "books_items", "books_payment", "books_sale", "books_taxes"}
	want := append(append([]string{}, rails...), books...)
	if got := Names(); len(got) != len(want) {
		t.Fatalf("capabilities %v, want %v", got, want)
	}
	// Rails pay and collect; the books are products' (decision 0051).
	for kind, names := range map[string][]string{"rail": rails, "product": books} {
		for _, n := range names {
			if s, ok := Get(n); !ok || s.Kind != kind {
				t.Errorf("%s: kind %v, want %s", n, s, kind)
			}
		}
	}
	for _, n := range []string{"books.invoice.created", "books.invoice.paid", "books.payment.recorded"} {
		if e, ok := GetEvent(n); !ok || e.Kind != "product" || e.Correlation == "" {
			t.Errorf("%s: %+v", n, e)
		}
	}
	if e, _ := GetEvent("payout.settled"); e.Kind != "rail" {
		t.Errorf("payout.settled: kind %q", e.Kind)
	}
	for _, n := range want {
		s, ok := Get(n)
		if !ok {
			t.Fatalf("no %s", n)
		}
		if len(s.Classes) == 0 {
			t.Errorf("%s: no classes", n)
		}
		for _, c := range s.Classes {
			if _, err := effects.ParseClass(string(c)); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}
		// Every schema compiles.
		_ = s.CheckInput(map[string]any{})
		if msgs := s.CheckOutput(map[string]any{}); len(msgs) == 0 || msgs[0] == "" {
			t.Errorf("%s: an empty output passes", n)
		}
	}
	for _, n := range EventNames() {
		e, _ := GetEvent(n)
		if !hasField(Fields(e.Fields), e.Correlation) {
			t.Errorf("%s: correlation %q is not a field", n, e.Correlation)
		}
		if msgs := e.CheckFields(map[string]any{}); len(msgs) == 0 {
			t.Errorf("%s: empty fields pass", n)
		}
	}
	for _, n := range PayoutEvents {
		if _, ok := GetEvent(n); !ok {
			t.Errorf("no %s", n)
		}
	}
}

func hasField(fs []Field, name string) bool {
	for _, f := range fs {
		if f.Name() == name {
			return true
		}
	}
	return false
}

func TestPayBank(t *testing.T) {
	s, _ := Get("pay_bank")
	if !s.IsPayout() || !s.Allows(effects.IdempotentWrite) || s.Allows(effects.UnsafeWrite) {
		t.Fatal("pay_bank is a payout by an idempotent or reconcilable write")
	}
	if b, _ := Get("balance"); b.IsPayout() {
		t.Fatal("balance is no payout")
	}
	if f, _ := s.Field("account_name"); !f.Required || f.PII != "name" {
		t.Fatalf("account_name: %+v", f)
	}
	in := s.WithDefaults(map[string]any{"amount_kobo": 500})
	if in["currency"] != "NGN" || in["amount_kobo"] != 500 {
		t.Fatalf("defaults: %v", in)
	}
	if msgs := s.CheckOutput(map[string]any{"payout_id": "p1", "status": "settled"}); len(msgs) > 0 {
		t.Fatal(msgs)
	}
	if msgs := s.CheckOutput(map[string]any{"payout_id": "p1", "status": "done"}); len(msgs) == 0 {
		t.Fatal("an unknown status passes")
	}
	if msgs := s.CheckOutput(map[string]any{"payout_id": "p1", "status": "settled", "extra": 1}); len(msgs) == 0 {
		t.Fatal("an extra field passes")
	}
	va, _ := Get("virtual_account")
	if f, ok := va.Field("customer.bvn"); !ok || f.Required || f.PII != "bvn" {
		t.Fatalf("customer.bvn: %+v", f)
	}
}
